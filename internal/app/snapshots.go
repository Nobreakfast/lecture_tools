package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"course-assistant/internal/domain"

	_ "modernc.org/sqlite"
)

const (
	snapshotKindScheduled = "scheduled"
	snapshotKindManual    = "manual"

	snapshotModeLite = "lite"
	snapshotModeFull = "full"

	snapshotRetentionDays = 14

	// snapshotUploadMaxBytes caps the size of an uploaded restore archive
	// (request body, not just multipart memory). Anything larger is
	// rejected before being streamed to disk so an attacker cannot fill
	// the snapshot volume by sending a TB-sized request body.
	snapshotUploadMaxBytes = 4 << 30 // 4 GiB

	// snapshotUploadPrefix marks files staged by upload-restore in the
	// SnapshotDir. listSnapshots skips these so they do not appear as
	// real snapshots until they are committed by a successful restart.
	snapshotUploadPrefix = "restore_upload_"
)

type snapshotManifest struct {
	Version           int      `json:"version"`
	CreatedAt         string   `json:"created_at"`
	CreatedBy         string   `json:"created_by"`
	Kind              string   `json:"kind"`
	Mode              string   `json:"mode"`
	DatabaseFile      string   `json:"database_file"`
	DatabaseSizeBytes int64    `json:"database_size_bytes"`
	MetadataDir       string   `json:"metadata_dir"`
	MetadataFiles     int      `json:"metadata_files"`
	MetadataSizeBytes int64    `json:"metadata_size_bytes"`
	IncludedScopes    []string `json:"included_scopes,omitempty"`
}

type snapshotItem struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	CreatedAt string `json:"created_at"`
	CreatedBy string `json:"created_by"`
	Kind      string `json:"kind"`
	Mode      string `json:"mode"`
	SizeBytes int64  `json:"size_bytes"`
}

type pendingSnapshotRestore struct {
	ArchivePath   string `json:"archive_path"`
	RequestedBy   string `json:"requested_by"`
	RequestedAt   string `json:"requested_at"`
	CleanupOnDone bool   `json:"cleanup_on_done"`
}

func (s *Server) startSnapshotScheduler() {
	go func() {
		s.pruneExpiredSnapshots()
		for {
			now := time.Now()
			next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
			if !next.After(now) {
				next = next.Add(24 * time.Hour)
			}
			time.Sleep(time.Until(next))
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			if _, err := s.createStoredSnapshot(ctx, snapshotKindScheduled, snapshotModeLite, "system"); err != nil {
				log.Printf("scheduled snapshot failed: %v", err)
			}
			cancel()
			s.pruneExpiredSnapshots()
		}
	}()
}

func (s *Server) withMaintenanceGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.isMaintenanceMode() {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/admin" {
			next.ServeHTTP(w, r)
			return
		}
		if s.requireAdmin(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "系统正在执行快照恢复，请稍后重试", http.StatusServiceUnavailable)
	})
}

func (s *Server) isMaintenanceMode() bool {
	s.serverMu.RLock()
	defer s.serverMu.RUnlock()
	return s.maintenanceMode
}

func (s *Server) setMaintenanceMode(enabled bool) {
	s.serverMu.Lock()
	defer s.serverMu.Unlock()
	s.maintenanceMode = enabled
}

func (s *Server) apiSystemSnapshots(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items, err := s.listSnapshots()
	if err != nil {
		http.Error(w, "读取快照列表失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"items": items, "retention_days": snapshotRetentionDays})
}

func (s *Server) apiSystemSnapshotsCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.getAuthSession(r)
	if sess == nil || sess.Role != domain.RoleAdmin {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	item, err := s.createStoredSnapshot(r.Context(), snapshotKindManual, snapshotModeLite, "admin:"+sess.TeacherID)
	if err != nil {
		http.Error(w, "生成快照失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "snapshot": item})
}

func (s *Server) apiSystemSnapshotsCreateDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.getAuthSession(r)
	if sess == nil || sess.Role != domain.RoleAdmin {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	mode := normalizeSnapshotMode(r.URL.Query().Get("mode"), snapshotModeFull)
	item, path, cleanup, err := s.createDownloadSnapshot(r.Context(), mode, "admin:"+sess.TeacherID)
	if err != nil {
		http.Error(w, "生成快照失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer cleanup()
	s.serveSnapshotDownload(w, r, path, item.Filename)
}

func (s *Server) apiSystemSnapshotsDownload(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	path, err := s.snapshotPath(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.serveSnapshotDownload(w, r, path, filepath.Base(path))
}

func (s *Server) apiSystemSnapshotsRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.getAuthSession(r)
	if sess == nil || sess.Role != domain.RoleAdmin {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	path, err := s.snapshotPath(req.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := readSnapshotManifest(path); err != nil {
		http.Error(w, "快照文件无效: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := queuePendingSnapshotRestore(s.cfg, pendingSnapshotRestore{
		ArchivePath: path,
		RequestedBy: "admin:" + sess.TeacherID,
		RequestedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		http.Error(w, "写入恢复任务失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("snapshot restore queued by %s from %s", sess.TeacherID, path)
	s.setMaintenanceMode(true)
	writeJSON(w, map[string]any{"ok": true, "message": "恢复任务已写入，服务即将停止；请等待外部进程管理器（systemd / docker / supervisor）拉起服务以应用快照"})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go s.triggerShutdownAfter(500 * time.Millisecond)
}

func (s *Server) apiSystemSnapshotsUploadRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.getAuthSession(r)
	if sess == nil || sess.Role != domain.RoleAdmin {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Cap the entire request body. ParseMultipartForm only bounds the
	// in-memory portion; without MaxBytesReader an attacker (or an admin
	// with a slip of the finger) can stream an arbitrarily large body
	// straight to disk and fill the snapshot volume.
	r.Body = http.MaxBytesReader(w, r.Body, snapshotUploadMaxBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "读取上传文件失败（可能超过 4GB 上限）", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "未上传文件", http.StatusBadRequest)
		return
	}
	defer file.Close()
	ext := strings.ToLower(header.Filename)
	if !strings.HasSuffix(ext, ".tar.gz") && !strings.HasSuffix(ext, ".tgz") {
		http.Error(w, "仅支持 .tar.gz 快照文件", http.StatusBadRequest)
		return
	}
	tmpName := fmt.Sprintf("%s%s.tar.gz", snapshotUploadPrefix, time.Now().Format("2006-01-02_150405"))
	tmpPath := filepath.Join(s.cfg.SnapshotDir, tmpName)
	out, err := os.Create(tmpPath)
	if err != nil {
		http.Error(w, "创建临时恢复文件失败", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		_ = out.Close()
		_ = os.Remove(tmpPath)
		http.Error(w, "保存上传文件失败", http.StatusInternalServerError)
		return
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmpPath)
		http.Error(w, "保存上传文件失败", http.StatusInternalServerError)
		return
	}
	if _, err := readSnapshotManifest(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		http.Error(w, "快照文件无效: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := queuePendingSnapshotRestore(s.cfg, pendingSnapshotRestore{
		ArchivePath:   tmpPath,
		RequestedBy:   "admin:" + sess.TeacherID,
		RequestedAt:   time.Now().Format(time.RFC3339),
		CleanupOnDone: true,
	}); err != nil {
		_ = os.Remove(tmpPath)
		http.Error(w, "写入恢复任务失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("uploaded snapshot restore queued by %s from %s", sess.TeacherID, header.Filename)
	s.setMaintenanceMode(true)
	writeJSON(w, map[string]any{"ok": true, "message": "上传快照已接收，服务即将停止；请等待外部进程管理器（systemd / docker / supervisor）拉起服务以应用快照"})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go s.triggerShutdownAfter(500 * time.Millisecond)
}

func (s *Server) triggerShutdownAfter(delay time.Duration) {
	time.Sleep(delay)
	s.serverMu.RLock()
	fn := s.shutdownFn
	s.serverMu.RUnlock()
	if fn != nil {
		fn()
		return
	}
	os.Exit(0)
}

func (s *Server) createStoredSnapshot(ctx context.Context, kind, mode, createdBy string) (snapshotItem, error) {
	fileName := buildSnapshotFileName(kind, mode)
	outPath := filepath.Join(s.cfg.SnapshotDir, fileName)
	item, err := s.createSnapshotArchive(ctx, outPath, kind, mode, createdBy)
	if err != nil {
		return snapshotItem{}, err
	}
	return item, nil
}

func (s *Server) createDownloadSnapshot(ctx context.Context, mode, createdBy string) (snapshotItem, string, func(), error) {
	tmpDir, err := os.MkdirTemp(s.cfg.SnapshotDir, "snapshot-download-*")
	if err != nil {
		return snapshotItem{}, "", nil, err
	}
	fileName := buildSnapshotFileName(snapshotKindManual, mode)
	outPath := filepath.Join(tmpDir, fileName)
	item, err := s.createSnapshotArchive(ctx, outPath, snapshotKindManual, mode, createdBy)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return snapshotItem{}, "", nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}
	return item, outPath, cleanup, nil
}

func (s *Server) createSnapshotArchive(ctx context.Context, outPath, kind, mode, createdBy string) (snapshotItem, error) {
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()

	tmpDir, err := os.MkdirTemp(s.cfg.SnapshotDir, "snapshot-build-*")
	if err != nil {
		return snapshotItem{}, err
	}
	defer os.RemoveAll(tmpDir)

	dbSnapshotPath := filepath.Join(tmpDir, "app.db")
	err = createSQLiteSnapshot(ctx, filepath.Join(s.cfg.DataDir, "app.db"), dbSnapshotPath)
	if err != nil {
		return snapshotItem{}, err
	}
	dbInfo, err := os.Stat(dbSnapshotPath)
	if err != nil {
		return snapshotItem{}, err
	}
	metadataPath, includedScopes, metaFiles, metaSize, err := s.prepareSnapshotMetadata(tmpDir, mode)
	if err != nil {
		return snapshotItem{}, err
	}
	manifest := snapshotManifest{
		Version:           1,
		CreatedAt:         time.Now().Format(time.RFC3339),
		CreatedBy:         createdBy,
		Kind:              kind,
		Mode:              mode,
		DatabaseFile:      "app.db",
		DatabaseSizeBytes: dbInfo.Size(),
		MetadataDir:       "metadata",
		MetadataFiles:     metaFiles,
		MetadataSizeBytes: metaSize,
		IncludedScopes:    includedScopes,
	}
	err = writeSnapshotArchive(outPath, dbSnapshotPath, metadataPath, manifest)
	if err != nil {
		return snapshotItem{}, err
	}
	info, err := os.Stat(outPath)
	if err != nil {
		return snapshotItem{}, err
	}
	return snapshotItem{
		ID:        filepath.Base(outPath),
		Filename:  filepath.Base(outPath),
		CreatedAt: manifest.CreatedAt,
		CreatedBy: createdBy,
		Kind:      kind,
		Mode:      mode,
		SizeBytes: info.Size(),
	}, nil
}

func (s *Server) listSnapshots() ([]snapshotItem, error) {
	if err := os.MkdirAll(s.cfg.SnapshotDir, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.cfg.SnapshotDir)
	if err != nil {
		return nil, err
	}
	items := make([]snapshotItem, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tar.gz") {
			continue
		}
		// Skip staged upload-restore files: they are not real snapshots
		// until the server restarts and ApplyPendingSnapshotRestore
		// either consumes (and removes) or fails them.
		if strings.HasPrefix(entry.Name(), snapshotUploadPrefix) {
			continue
		}
		path := filepath.Join(s.cfg.SnapshotDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		manifest, err := readSnapshotManifest(path)
		if err != nil {
			items = append(items, snapshotItem{
				ID:        entry.Name(),
				Filename:  entry.Name(),
				CreatedAt: info.ModTime().Format(time.RFC3339),
				CreatedBy: "",
				Kind:      "",
				Mode:      "",
				SizeBytes: info.Size(),
			})
			continue
		}
		items = append(items, snapshotItem{
			ID:        entry.Name(),
			Filename:  entry.Name(),
			CreatedAt: manifest.CreatedAt,
			CreatedBy: manifest.CreatedBy,
			Kind:      manifest.Kind,
			Mode:      normalizeSnapshotMode(manifest.Mode, snapshotModeFull),
			SizeBytes: info.Size(),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt > items[j].CreatedAt
	})
	return items, nil
}

func (s *Server) pruneExpiredSnapshots() {
	items, err := s.listSnapshots()
	if err != nil {
		log.Printf("snapshot prune skipped: %v", err)
		return
	}
	cutoff := time.Now().Add(-snapshotRetentionDays * 24 * time.Hour)
	for _, item := range items {
		createdAt, err := time.Parse(time.RFC3339, item.CreatedAt)
		if err != nil || createdAt.After(cutoff) {
			continue
		}
		path, err := s.snapshotPath(item.ID)
		if err != nil {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("snapshot prune failed for %s: %v", path, err)
		}
	}
}

func (s *Server) snapshotPath(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("快照 id 不能为空")
	}
	if id != filepath.Base(id) || strings.Contains(id, "..") {
		return "", errors.New("快照 id 非法")
	}
	path := filepath.Join(s.cfg.SnapshotDir, id)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("快照不存在")
		}
		return "", err
	}
	return path, nil
}

func (s *Server) serveSnapshotDownload(w http.ResponseWriter, r *http.Request, path, name string) {
	w.Header().Set("Content-Type", mime.TypeByExtension(".gz"))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeFile(w, r, path)
}

func buildSnapshotFileName(kind, mode string) string {
	return fmt.Sprintf("snapshot_%s_%s_%s.tar.gz", time.Now().Format("2006-01-02_150405"), kind, mode)
}

func normalizeSnapshotMode(mode, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case snapshotModeLite:
		return snapshotModeLite
	case snapshotModeFull:
		return snapshotModeFull
	default:
		return fallback
	}
}

func (s *Server) prepareSnapshotMetadata(tmpDir, mode string) (string, []string, int, int64, error) {
	switch normalizeSnapshotMode(mode, snapshotModeLite) {
	case snapshotModeFull:
		files, size, err := dirStats(s.cfg.MetadataDir)
		return s.cfg.MetadataDir, []string{"metadata/**"}, files, size, err
	case snapshotModeLite:
		liteRoot := filepath.Join(tmpDir, "metadata-lite")
		if err := buildLiteSnapshotMetadata(s.cfg.MetadataDir, liteRoot); err != nil {
			return "", nil, 0, 0, err
		}
		files, size, err := dirStats(liteRoot)
		return liteRoot, []string{"metadata/*/*/quiz/**", "metadata/*/*/assignment/*/submissions/**"}, files, size, err
	default:
		return "", nil, 0, 0, fmt.Errorf("不支持的快照模式: %s", mode)
	}
}

func buildLiteSnapshotMetadata(srcRoot, dstRoot string) error {
	if err := os.RemoveAll(dstRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(srcRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if !shouldIncludeLiteMetadata(rel, d.IsDir()) {
			return nil
		}
		target := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(path, target)
	})
}

func shouldIncludeLiteMetadata(rel string, isDir bool) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 3 {
		return isDir
	}
	if parts[2] == "quiz" {
		return true
	}
	if parts[2] != "assignment" {
		return false
	}
	if len(parts) <= 4 {
		return isDir
	}
	return parts[4] == "submissions"
}

func createSQLiteSnapshot(ctx context.Context, dbPath, snapshotPath string) error {
	_ = os.Remove(snapshotPath)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, execErr := db.ExecContext(ctx, `PRAGMA busy_timeout = 5000;`); execErr != nil {
		err = execErr
		return err
	}
	sqlPath := strings.ReplaceAll(snapshotPath, `'`, `''`)
	_, err = db.ExecContext(ctx, "VACUUM INTO '"+sqlPath+"'")
	return err
}

func writeSnapshotArchive(outPath, dbSnapshotPath, metadataDir string, manifest snapshotManifest) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	gzw := gzip.NewWriter(out)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeTarBytes(tw, "manifest.json", manifestBytes, 0o644); err != nil {
		return err
	}
	if err := writeTarFile(tw, dbSnapshotPath, "app.db"); err != nil {
		return err
	}
	if err := writeTarDir(tw, metadataDir, "metadata"); err != nil {
		return err
	}
	return nil
}

func writeTarBytes(tw *tar.Writer, name string, data []byte, mode int64) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    mode,
		Size:    int64(len(data)),
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func writeTarFile(tw *tar.Writer, srcPath, tarPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(tarPath)
	if writeErr := tw.WriteHeader(hdr); writeErr != nil {
		return writeErr
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

func writeTarDir(tw *tar.Writer, srcRoot, tarRoot string) error {
	if _, err := os.Stat(srcRoot); errors.Is(err, os.ErrNotExist) {
		return writeTarDirHeader(tw, tarRoot)
	}
	if err := writeTarDirHeader(tw, tarRoot); err != nil {
		return err
	}
	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == srcRoot {
			return nil
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		archivePath := filepath.ToSlash(filepath.Join(tarRoot, rel))
		if d.IsDir() {
			return writeTarDirHeader(tw, archivePath)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return writeTarFile(tw, path, archivePath)
	})
}

func writeTarDirHeader(tw *tar.Writer, name string) error {
	name = strings.TrimSuffix(filepath.ToSlash(name), "/") + "/"
	hdr := &tar.Header{
		Name:     name,
		Typeflag: tar.TypeDir,
		Mode:     0o755,
		ModTime:  time.Now(),
	}
	return tw.WriteHeader(hdr)
}

func dirStats(root string) (int, int64, error) {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	files := 0
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files++
		total += info.Size()
		return nil
	})
	return files, total, err
}

func readSnapshotManifest(archivePath string) (snapshotManifest, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return snapshotManifest{}, err
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return snapshotManifest{}, err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return snapshotManifest{}, err
		}
		if filepath.ToSlash(hdr.Name) != "manifest.json" {
			continue
		}
		var manifest snapshotManifest
		if err := json.NewDecoder(tr).Decode(&manifest); err != nil {
			return snapshotManifest{}, err
		}
		if manifest.Version <= 0 || strings.TrimSpace(manifest.DatabaseFile) == "" {
			return snapshotManifest{}, errors.New("manifest 缺少必要字段")
		}
		manifest.Mode = normalizeSnapshotMode(manifest.Mode, snapshotModeFull)
		return manifest, nil
	}
	return snapshotManifest{}, errors.New("未找到 manifest.json")
}

func queuePendingSnapshotRestore(cfg Config, req pendingSnapshotRestore) error {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return errors.New("DATA_DIR 未配置")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(pendingSnapshotRestorePath(cfg), data, 0o644)
}

func pendingSnapshotRestorePath(cfg Config) string {
	return filepath.Join(cfg.DataDir, "snapshot_restore_pending.json")
}

func ApplyPendingSnapshotRestore(cfg Config) error {
	reqPath := pendingSnapshotRestorePath(cfg)
	raw, err := os.ReadFile(reqPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var req pendingSnapshotRestore
	if err := json.Unmarshal(raw, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.ArchivePath) == "" {
		return errors.New("待恢复快照路径为空")
	}
	if err := applySnapshotArchive(cfg, req.ArchivePath); err != nil {
		failed := filepath.Join(cfg.DataDir, "snapshot_restore_failed_"+time.Now().Format("2006-01-02_150405")+".json")
		_ = os.Rename(reqPath, failed)
		if req.CleanupOnDone {
			_ = os.Remove(req.ArchivePath)
		}
		return err
	}
	if req.CleanupOnDone {
		_ = os.Remove(req.ArchivePath)
	}
	return os.Remove(reqPath)
}

func applySnapshotArchive(cfg Config, archivePath string) error {
	manifest, err := readSnapshotManifest(archivePath)
	if err != nil {
		return err
	}
	tmpExtractDir, err := os.MkdirTemp(cfg.SnapshotDir, "snapshot-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpExtractDir)

	if err := extractSnapshotArchive(archivePath, tmpExtractDir); err != nil {
		return err
	}
	dbSource := filepath.Join(tmpExtractDir, "app.db")
	metadataSource := filepath.Join(tmpExtractDir, "metadata")
	if _, err := os.Stat(dbSource); err != nil {
		return fmt.Errorf("快照中缺少 app.db: %w", err)
	}
	if _, err := os.Stat(metadataSource); err != nil {
		return fmt.Errorf("快照中缺少 metadata 目录: %w", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.MetadataDir), 0o755); err != nil {
		return err
	}

	dbPath := filepath.Join(cfg.DataDir, "app.db")
	dbTmp := dbPath + ".restore_new"

	_ = os.Remove(dbTmp)
	if err := copyFile(dbSource, dbTmp); err != nil {
		return err
	}
	if err := replaceDatabaseFromSnapshot(dbTmp, dbPath); err != nil {
		_ = os.Remove(dbTmp)
		return err
	}
	if manifest.Mode == snapshotModeLite {
		return applyLiteMetadataRestore(metadataSource, cfg.MetadataDir)
	}
	return replaceMetadataTree(metadataSource, cfg.MetadataDir)
}

func replaceDatabaseFromSnapshot(src, dbPath string) error {
	stamp := time.Now().Format("20060102_150405")
	dbBackup := dbPath + ".pre_restore_" + stamp
	dbBackedUp := false
	if _, err := os.Stat(dbPath); err == nil {
		_ = os.Remove(dbBackup)
		if err := os.Rename(dbPath, dbBackup); err != nil {
			return err
		}
		dbBackedUp = true
	}
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	if err := os.Rename(src, dbPath); err != nil {
		if dbBackedUp {
			_ = os.Rename(dbBackup, dbPath)
		}
		return err
	}
	if dbBackedUp {
		_ = os.Remove(dbBackup)
	}
	return nil
}

func replaceMetadataTree(src, dst string) error {
	stamp := time.Now().Format("20060102_150405")
	metaTmp := dst + ".restore_new"
	metaBackup := dst + ".pre_restore_" + stamp
	_ = os.RemoveAll(metaTmp)
	if err := copyDir(src, metaTmp); err != nil {
		_ = os.RemoveAll(metaTmp)
		return err
	}
	metaBackedUp := false
	if _, err := os.Stat(dst); err == nil {
		_ = os.RemoveAll(metaBackup)
		if err := os.Rename(dst, metaBackup); err != nil {
			return err
		}
		metaBackedUp = true
	}
	if err := os.Rename(metaTmp, dst); err != nil {
		if metaBackedUp {
			_ = os.Rename(metaBackup, dst)
		}
		return err
	}
	if metaBackedUp {
		_ = os.RemoveAll(metaBackup)
	}
	return nil
}

func applyLiteMetadataRestore(srcRoot, dstRoot string) error {
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return err
	}
	teachers, err := os.ReadDir(srcRoot)
	if err != nil {
		return err
	}
	for _, teacher := range teachers {
		if !teacher.IsDir() {
			continue
		}
		teacherSrc := filepath.Join(srcRoot, teacher.Name())
		courses, err := os.ReadDir(teacherSrc)
		if err != nil {
			return err
		}
		for _, course := range courses {
			if !course.IsDir() {
				continue
			}
			courseSrc := filepath.Join(teacherSrc, course.Name())
			courseDst := filepath.Join(dstRoot, teacher.Name(), course.Name())
			if err := os.MkdirAll(courseDst, 0o755); err != nil {
				return err
			}
			quizSrc := filepath.Join(courseSrc, "quiz")
			if _, err := os.Stat(quizSrc); err == nil {
				if err := replacePath(quizSrc, filepath.Join(courseDst, "quiz")); err != nil {
					return err
				}
			}
			assignmentsSrc := filepath.Join(courseSrc, "assignment")
			assignments, err := os.ReadDir(assignmentsSrc)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			for _, assignment := range assignments {
				if !assignment.IsDir() {
					continue
				}
				submissionsSrc := filepath.Join(assignmentsSrc, assignment.Name(), "submissions")
				if _, err := os.Stat(submissionsSrc); errors.Is(err, os.ErrNotExist) {
					continue
				} else if err != nil {
					return err
				}
				assignmentDst := filepath.Join(courseDst, "assignment", assignment.Name())
				if err := os.MkdirAll(assignmentDst, 0o755); err != nil {
					return err
				}
				if err := replacePath(submissionsSrc, filepath.Join(assignmentDst, "submissions")); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func replacePath(src, dst string) error {
	stamp := time.Now().Format("20060102_150405")
	tmp := dst + ".restore_new"
	backup := dst + ".pre_restore_" + stamp
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := copyDir(src, tmp); err != nil {
		return err
	}
	backedUp := false
	if _, err := os.Stat(dst); err == nil {
		_ = os.RemoveAll(backup)
		if err := os.Rename(dst, backup); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
		backedUp = true
	}
	if err := os.Rename(tmp, dst); err != nil {
		if backedUp {
			_ = os.Rename(backup, dst)
		}
		return err
	}
	if backedUp {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func extractSnapshotArchive(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("非法归档路径: %s", hdr.Name)
		}
		target := filepath.Join(destDir, name)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) && filepath.Clean(target) != filepath.Clean(destDir) {
			return fmt.Errorf("非法归档路径: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("不支持的归档条目: %s", hdr.Name)
		}
	}
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func copyDir(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(path, target)
	})
}
