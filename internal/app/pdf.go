package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)


type pdfItem struct {
	Folder string `json:"folder"`
	File   string `json:"file"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
}

type materialDownloadItem struct {
	File      string `json:"file"`
	Extension string `json:"extension"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
}

type materialPreviewItem struct {
	File        string `json:"file"`
	PreviewURL  string `json:"preview_url"`
	DownloadURL string `json:"download_url"`
	Size        int64  `json:"size"`
}

type adminMaterialDownloadItem struct {
	File      string `json:"file"`
	Extension string `json:"extension"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Visible   bool   `json:"visible"`
}

type adminMaterialPreviewItem struct {
	File        string `json:"file"`
	PreviewURL  string `json:"preview_url"`
	DownloadURL string `json:"download_url"`
	Size        int64  `json:"size"`
	Visible     bool   `json:"visible"`
}

type materialGroupItem struct {
	Folder    string                 `json:"folder"`
	Stem      string                 `json:"stem"`
	PDF       *materialPreviewItem   `json:"pdf,omitempty"`
	Downloads []materialDownloadItem `json:"downloads"`
}

type adminMaterialGroupItem struct {
	Folder    string                      `json:"folder"`
	Stem      string                      `json:"stem"`
	PDF       *adminMaterialPreviewItem   `json:"pdf,omitempty"`
	Downloads []adminMaterialDownloadItem `json:"downloads"`
}

type materialUploadSuccess struct {
	Folder      string `json:"folder"`
	File        string `json:"file"`
	Extension   string `json:"extension"`
	URL         string `json:"url"`
	PreviewURL  string `json:"preview_url,omitempty"`
	DownloadURL string `json:"download_url"`
	Size        int64  `json:"size"`
}

type materialUploadFailure struct {
	File  string `json:"file"`
	Error string `json:"error"`
}

type materialGroupBuilder struct {
	folder    string
	stem      string
	pdf       *materialPreviewItem
	downloads []materialDownloadItem
}

type adminMaterialGroupBuilder struct {
	folder    string
	stem      string
	pdf       *adminMaterialPreviewItem
	downloads []adminMaterialDownloadItem
}

const materialVisibilitySettingKey = "material_visibility"

func (s *Server) pptDir() string {
	return filepath.Join(filepath.Dir(s.cfg.DataDir), "ppt")
}

func (s *Server) pageStudent(w http.ResponseWriter, _ *http.Request) {
	s.servePage(w, "web/student.html")
}

func (s *Server) apiMaterials(w http.ResponseWriter, r *http.Request) {
	items, err := s.scanMaterialGroups()
	if err != nil {
		writeJSON(w, map[string]any{"items": []materialGroupItem{}})
		return
	}
	if course := strings.TrimSpace(r.URL.Query().Get("course")); course != "" {
		filtered := make([]materialGroupItem, 0)
		for _, item := range items {
			if item.Folder == course {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) apiPDFs(w http.ResponseWriter, _ *http.Request) {
	groups, err := s.scanMaterialGroups()
	if err != nil {
		writeJSON(w, map[string]any{"items": []pdfItem{}})
		return
	}
	items := make([]pdfItem, 0)
	for _, group := range groups {
		if group.PDF == nil {
			continue
		}
		items = append(items, pdfItem{
			Folder: group.Folder,
			File:   group.PDF.File,
			URL:    group.PDF.PreviewURL,
			Size:   group.PDF.Size,
		})
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) scanMaterialGroups() ([]materialGroupItem, error) {
	visibility, err := s.loadMaterialVisibility(contextOrBackground(nil))
	if err != nil {
		return nil, err
	}
	return s.scanMaterialGroupsWithVisibility(visibility, false)
}

func (s *Server) scanMaterialGroupsWithVisibility(visibility map[string]bool, includeHidden bool) ([]materialGroupItem, error) {
	root := s.pptDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []materialGroupItem{}, nil
		}
		return nil, err
	}

	builders := map[string]*materialGroupBuilder{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		folder := entry.Name()
		if folder == homeworkAssignmentsFolder {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, folder))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			name := f.Name()
			if !includeHidden && !materialVisible(visibility, folder, name) {
				continue
			}
			ext := allowedMaterialExt(name)
			if ext == "" {
				continue
			}
			info, _ := f.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			stem := strings.TrimSuffix(name, filepath.Ext(name))
			key := folder + "\x00" + stem
			builder, ok := builders[key]
			if !ok {
				builder = &materialGroupBuilder{folder: folder, stem: stem}
				builders[key] = builder
			}
			download := materialDownloadItem{
				File:      name,
				Extension: ext,
				URL:       s.materialDownloadURL(folder, name),
				Size:      size,
			}
			builder.downloads = append(builder.downloads, download)
			if ext == ".pdf" {
				builder.pdf = &materialPreviewItem{
					File:        name,
					PreviewURL:  s.materialPreviewURL(folder, name),
					DownloadURL: download.URL,
					Size:        size,
				}
			}
		}
	}

	items := make([]materialGroupItem, 0, len(builders))
	for _, builder := range builders {
		sort.Slice(builder.downloads, func(i, j int) bool {
			return builder.downloads[i].File < builder.downloads[j].File
		})
		items = append(items, materialGroupItem{
			Folder:    builder.folder,
			Stem:      builder.stem,
			PDF:       builder.pdf,
			Downloads: builder.downloads,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Folder == items[j].Folder {
			return items[i].Stem < items[j].Stem
		}
		return items[i].Folder < items[j].Folder
	})
	return items, nil
}

func (s *Server) scanAdminMaterialGroups() ([]adminMaterialGroupItem, error) {
	visibility, err := s.loadMaterialVisibility(contextOrBackground(nil))
	if err != nil {
		return nil, err
	}
	root := s.pptDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []adminMaterialGroupItem{}, nil
		}
		return nil, err
	}

	builders := map[string]*adminMaterialGroupBuilder{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		folder := entry.Name()
		if folder == homeworkAssignmentsFolder {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, folder))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			name := f.Name()
			ext := allowedMaterialExt(name)
			if ext == "" {
				continue
			}
			info, _ := f.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			visible := materialVisible(visibility, folder, name)
			stem := strings.TrimSuffix(name, filepath.Ext(name))
			key := folder + "\x00" + stem
			builder, ok := builders[key]
			if !ok {
				builder = &adminMaterialGroupBuilder{folder: folder, stem: stem}
				builders[key] = builder
			}
			download := adminMaterialDownloadItem{
				File:      name,
				Extension: ext,
				URL:       s.materialDownloadURL(folder, name),
				Size:      size,
				Visible:   visible,
			}
			builder.downloads = append(builder.downloads, download)
			if ext == ".pdf" {
				builder.pdf = &adminMaterialPreviewItem{
					File:        name,
					PreviewURL:  s.materialPreviewURL(folder, name),
					DownloadURL: download.URL,
					Size:        size,
					Visible:     visible,
				}
			}
		}
	}

	items := make([]adminMaterialGroupItem, 0, len(builders))
	for _, builder := range builders {
		sort.Slice(builder.downloads, func(i, j int) bool {
			return builder.downloads[i].File < builder.downloads[j].File
		})
		items = append(items, adminMaterialGroupItem{
			Folder:    builder.folder,
			Stem:      builder.stem,
			PDF:       builder.pdf,
			Downloads: builder.downloads,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Folder == items[j].Folder {
			return items[i].Stem < items[j].Stem
		}
		return items[i].Folder < items[j].Folder
	})
	return items, nil
}

func (s *Server) servePPT(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/ppt/")
	if strings.HasPrefix(rel, homeworkAssignmentsFolder+"/") && !s.requireAdmin(r) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	cleaned, err := cleanMaterialRelativePath(rel)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if allowedMaterialExt(cleaned) != ".pdf" {
		http.Error(w, "only PDF allowed", http.StatusBadRequest)
		return
	}
	if strings.HasPrefix(cleaned, homeworkAssignmentsFolder+string(filepath.Separator)) && !s.requireAdmin(r) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if !s.requireAdmin(r) && !s.isMaterialPathVisible(r.Context(), cleaned) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	fp := filepath.Join(s.pptDir(), cleaned)
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, fp)
}

func (s *Server) serveMaterialDownload(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/materials-files/")
	if strings.HasPrefix(rel, homeworkAssignmentsFolder+"/") && !s.requireAdmin(r) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	cleaned, err := cleanMaterialRelativePath(rel)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if allowedMaterialExt(cleaned) == "" {
		http.Error(w, "unsupported file type", http.StatusBadRequest)
		return
	}
	if strings.HasPrefix(cleaned, homeworkAssignmentsFolder+string(filepath.Separator)) && !s.requireAdmin(r) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if !s.requireAdmin(r) && !s.isMaterialPathVisible(r.Context(), cleaned) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	fp := filepath.Join(s.pptDir(), cleaned)
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(cleaned)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(cleaned)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, fp)
}

func (s *Server) apiAdminPDFUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 500<<20)
	if err := r.ParseMultipartForm(500 << 20); err != nil {
		http.Error(w, "文件过大或格式错误", http.StatusBadRequest)
		return
	}
	folder, err := validateMaterialFolder(r.FormValue("folder"))
	if err != nil {
		http.Error(w, "文件夹名称无效", http.StatusBadRequest)
		return
	}
	headers, err := collectUploadHeaders(r.MultipartForm)
	if err != nil {
		http.Error(w, "未找到上传文件", http.StatusBadRequest)
		return
	}
	dir := filepath.Join(s.pptDir(), folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "创建目录失败", http.StatusInternalServerError)
		return
	}

	uploaded := make([]materialUploadSuccess, 0, len(headers))
	failed := make([]materialUploadFailure, 0)
	for _, header := range headers {
		result, failure := s.saveUploadedMaterial(dir, folder, header)
		if failure != nil {
			failed = append(failed, *failure)
			continue
		}
		uploaded = append(uploaded, *result)
	}

	resp := map[string]any{
		"ok":       len(failed) == 0,
		"uploaded": uploaded,
		"failed":   failed,
	}
	if len(uploaded) > 0 {
		resp["url"] = uploaded[0].URL
	}
	if len(uploaded) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, resp)
		return
	}
	writeJSONStatus(w, http.StatusOK, resp)
}

func (s *Server) saveUploadedMaterial(dir, folder string, header *multipart.FileHeader) (*materialUploadSuccess, *materialUploadFailure) {
	name, ext, err := normalizeMaterialFilename(header.Filename, "")
	if err != nil {
		return nil, &materialUploadFailure{File: filepath.Base(header.Filename), Error: err.Error()}
	}
	file, err := header.Open()
	if err != nil {
		return nil, &materialUploadFailure{File: name, Error: "读取文件失败"}
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, &materialUploadFailure{File: name, Error: "读取文件失败"}
	}
	if ext == ".pdf" && !looksLikePDF(data) {
		return nil, &materialUploadFailure{File: name, Error: "PDF 文件内容无效"}
	}
	fp := filepath.Join(dir, name)
	if err := os.WriteFile(fp, data, 0o644); err != nil {
		return nil, &materialUploadFailure{File: name, Error: "写入文件失败"}
	}
	if err := s.setMaterialVisibility(context.Background(), folder, name, true); err != nil {
		_ = os.Remove(fp)
		return nil, &materialUploadFailure{File: name, Error: "保存文件状态失败"}
	}
	success := &materialUploadSuccess{
		Folder:      folder,
		File:        name,
		Extension:   ext,
		URL:         s.materialPrimaryURL(folder, name),
		DownloadURL: s.materialDownloadURL(folder, name),
		Size:        int64(len(data)),
	}
	if ext == ".pdf" {
		success.PreviewURL = s.materialPreviewURL(folder, name)
	}
	return success, nil
}

func (s *Server) apiAdminPDFDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Folder string `json:"folder"`
		File   string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	folder, err := validateMaterialFolder(req.Folder)
	if err != nil {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	name, _, err := normalizeMaterialFilename(req.File, "")
	if err != nil {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	fp := filepath.Join(s.pptDir(), folder, name)
	if err := os.Remove(fp); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "文件不存在", http.StatusNotFound)
			return
		}
		http.Error(w, "删除失败", http.StatusInternalServerError)
		return
	}
	if err := s.deleteMaterialVisibility(r.Context(), folder, name); err != nil {
		http.Error(w, "删除失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAdminPDFRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Folder  string `json:"folder"`
		OldName string `json:"old_name"`
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	folder, err := validateMaterialFolder(req.Folder)
	if err != nil {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	oldName, oldExt, err := normalizeMaterialFilename(req.OldName, "")
	if err != nil {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	newName, _, err := normalizeMaterialFilename(req.NewName, oldExt)
	if err != nil {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	dir := filepath.Join(s.pptDir(), folder)
	oldPath := filepath.Join(dir, oldName)
	newPath := filepath.Join(dir, newName)
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		http.Error(w, "原文件不存在", http.StatusNotFound)
		return
	}
	if oldPath == newPath {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if _, err := os.Stat(newPath); err == nil {
		http.Error(w, "目标文件名已存在", http.StatusConflict)
		return
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		http.Error(w, "重命名失败", http.StatusInternalServerError)
		return
	}
	if err := s.renameMaterialVisibility(r.Context(), folder, oldName, newName); err != nil {
		_ = os.Rename(newPath, oldPath)
		http.Error(w, "重命名失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAdminMaterials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	items, err := s.scanAdminMaterialGroups()
	if err != nil {
		writeJSON(w, map[string]any{"items": []adminMaterialGroupItem{}})
		return
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) apiAdminPDFVisibility(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Folder  string `json:"folder"`
		File    string `json:"file"`
		Visible bool   `json:"visible"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	folder, err := validateMaterialFolder(req.Folder)
	if err != nil {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	name, _, err := normalizeMaterialFilename(req.File, "")
	if err != nil {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(filepath.Join(s.pptDir(), folder, name)); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "文件不存在", http.StatusNotFound)
			return
		}
		http.Error(w, "读取失败", http.StatusInternalServerError)
		return
	}
	if err := s.setMaterialVisibility(r.Context(), folder, name, req.Visible); err != nil {
		http.Error(w, "保存失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "visible": req.Visible})
}

func materialVisibilityPath(folder, file string) string {
	return folder + "/" + file
}

func materialVisible(visibility map[string]bool, folder, file string) bool {
	visible, ok := visibility[materialVisibilityPath(folder, file)]
	if !ok {
		return true
	}
	return visible
}

func (s *Server) isMaterialPathVisible(ctx context.Context, rel string) bool {
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 2 {
		return false
	}
	visibility, err := s.loadMaterialVisibility(ctx)
	if err != nil {
		return false
	}
	return materialVisible(visibility, parts[0], parts[1])
}

func (s *Server) loadMaterialVisibility(ctx context.Context) (map[string]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadMaterialVisibilityUnlocked(ctx)
}

func (s *Server) loadMaterialVisibilityUnlocked(ctx context.Context) (map[string]bool, error) {
	raw, err := s.store.GetSetting(contextOrBackground(ctx), materialVisibilitySettingKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return map[string]bool{}, nil
	}
	visibility := map[string]bool{}
	if err := json.Unmarshal([]byte(raw), &visibility); err != nil {
		return nil, err
	}
	return visibility, nil
}

func (s *Server) saveMaterialVisibility(ctx context.Context, visibility map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveMaterialVisibilityUnlocked(ctx, visibility)
}

func (s *Server) saveMaterialVisibilityUnlocked(ctx context.Context, visibility map[string]bool) error {
	payload, err := json.Marshal(visibility)
	if err != nil {
		return err
	}
	return s.store.SetSetting(contextOrBackground(ctx), materialVisibilitySettingKey, string(payload))
}

func (s *Server) setMaterialVisibility(ctx context.Context, folder, file string, visible bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	visibility, err := s.loadMaterialVisibilityUnlocked(ctx)
	if err != nil {
		return err
	}
	key := materialVisibilityPath(folder, file)
	if visible {
		delete(visibility, key)
	} else {
		visibility[key] = false
	}
	return s.saveMaterialVisibilityUnlocked(ctx, visibility)
}

func (s *Server) renameMaterialVisibility(ctx context.Context, folder, oldName, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	visibility, err := s.loadMaterialVisibilityUnlocked(ctx)
	if err != nil {
		return err
	}
	oldKey := materialVisibilityPath(folder, oldName)
	newKey := materialVisibilityPath(folder, newName)
	visible, ok := visibility[oldKey]
	if ok {
		visibility[newKey] = visible
		delete(visibility, oldKey)
	} else {
		delete(visibility, newKey)
	}
	return s.saveMaterialVisibilityUnlocked(ctx, visibility)
}

func (s *Server) deleteMaterialVisibility(ctx context.Context, folder, file string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	visibility, err := s.loadMaterialVisibilityUnlocked(ctx)
	if err != nil {
		return err
	}
	delete(visibility, materialVisibilityPath(folder, file))
	return s.saveMaterialVisibilityUnlocked(ctx, visibility)
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func allowedMaterialExt(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" || strings.HasPrefix(filepath.Base(name), ".") {
		return ""
	}
	return ext
}

func validateMaterialFolder(raw string) (string, error) {
	folder := strings.TrimSpace(raw)
	if folder == "" || strings.Contains(folder, "..") || strings.Contains(folder, "/") || strings.Contains(folder, "\\") {
		return "", fmt.Errorf("invalid folder")
	}
	return folder, nil
}

func normalizeMaterialFilename(raw, defaultExt string) (string, string, error) {
	name := strings.TrimSpace(filepath.Base(raw))
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.HasPrefix(name, ".") {
		return "", "", fmt.Errorf("invalid filename")
	}
	defaultExt = strings.ToLower(strings.TrimSpace(defaultExt))
	ext := allowedMaterialExt(name)
	if ext == "" {
		if defaultExt == "" {
			return "", "", fmt.Errorf("unsupported file type")
		}
		ext = defaultExt
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if stem == "" || stem == "." || stem == ".." {
		return "", "", fmt.Errorf("invalid filename")
	}
	name = stem + ext
	return name, ext, nil
}

func cleanMaterialRelativePath(rel string) (string, error) {
	cleaned := filepath.Clean(rel)
	if cleaned == "." || strings.Contains(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("invalid path")
	}
	parts := strings.Split(cleaned, string(filepath.Separator))
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid path")
	}
	if _, err := validateMaterialFolder(parts[0]); err != nil {
		return "", err
	}
	if _, _, err := normalizeMaterialFilename(parts[1], ""); err != nil {
		return "", err
	}
	return cleaned, nil
}

func collectUploadHeaders(form *multipart.Form) ([]*multipart.FileHeader, error) {
	if form == nil {
		return nil, fmt.Errorf("missing form")
	}
	headers := make([]*multipart.FileHeader, 0)
	if files := form.File["files"]; len(files) > 0 {
		headers = append(headers, files...)
	}
	if file := form.File["file"]; len(file) > 0 {
		headers = append(headers, file...)
	}
	if len(headers) == 0 {
		return nil, fmt.Errorf("missing files")
	}
	return headers, nil
}

func looksLikePDF(data []byte) bool {
	return len(data) >= 5 && string(data[:5]) == "%PDF-"
}

func (s *Server) materialPreviewURL(folder, file string) string {
	return s.pathPrefix() + "/ppt/" + folder + "/" + file
}

func (s *Server) materialDownloadURL(folder, file string) string {
	return s.pathPrefix() + "/materials-files/" + folder + "/" + file
}

func (s *Server) materialPrimaryURL(folder, file string) string {
	if allowedMaterialExt(file) == ".pdf" {
		return s.materialPreviewURL(folder, file)
	}
	return s.materialDownloadURL(folder, file)
}

func writeJSONStatus(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func fileExists(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
