package app

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"course-assistant/internal/domain"
	"course-assistant/internal/store"
)

func TestCreateAndApplyPendingSnapshotRestore(t *testing.T) {
	t.Parallel()

	cfg := snapshotTestConfig(t)
	ctx := context.Background()

	st, err := store.NewSQLiteStore(filepath.Join(cfg.DataDir, "app.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	srv := New(cfg, st)
	if err := st.SetSetting(ctx, "snapshot_test", "before"); err != nil {
		t.Fatalf("SetSetting before: %v", err)
	}
	quizFile := filepath.Join(cfg.MetadataDir, "teacher1", "course1", "quiz", "week1", "week1.yaml")
	submissionFile := filepath.Join(cfg.MetadataDir, "teacher1", "course1", "assignment", "hw1", "submissions", "2023001", "sub1", "report.pdf")
	qaFile := filepath.Join(cfg.MetadataDir, "teacher1", "course1", "assignment", "hw1", "qa", "qa1", "question", "q.jpg")
	materialFile := filepath.Join(cfg.MetadataDir, "teacher1", "course1", "materials", "slide.pdf")
	assignmentSpec := filepath.Join(cfg.MetadataDir, "teacher1", "course1", "assignment", "hw1", "spec.pdf")
	for path, content := range map[string]string{
		quizFile:       "before-quiz",
		submissionFile: "before-submission",
		qaFile:         "before-qa",
		materialFile:   "before-material",
		assignmentSpec: "before-spec",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll before %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile before %s: %v", path, err)
		}
	}

	item, err := srv.createStoredSnapshot(ctx, snapshotKindScheduled, snapshotModeLite, "system")
	if err != nil {
		t.Fatalf("createStoredSnapshot: %v", err)
	}
	if item.Kind != snapshotKindScheduled || item.Mode != snapshotModeLite {
		t.Fatalf("unexpected snapshot kind/mode: %+v", item)
	}

	if err := st.SetSetting(ctx, "snapshot_test", "after"); err != nil {
		t.Fatalf("SetSetting after: %v", err)
	}
	for path, content := range map[string]string{
		quizFile:       "after-quiz",
		submissionFile: "after-submission",
		qaFile:         "after-qa",
		materialFile:   "after-material",
		assignmentSpec: "after-spec",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile after %s: %v", path, err)
		}
	}

	snapshotPath, err := srv.snapshotPath(item.ID)
	if err != nil {
		t.Fatalf("snapshotPath: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close before restore: %v", err)
	}

	if err := queuePendingSnapshotRestore(cfg, pendingSnapshotRestore{
		ArchivePath: snapshotPath,
		RequestedBy: "admin:test",
		RequestedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("queuePendingSnapshotRestore: %v", err)
	}
	if err := ApplyPendingSnapshotRestore(cfg); err != nil {
		t.Fatalf("ApplyPendingSnapshotRestore: %v", err)
	}

	reopened, err := store.NewSQLiteStore(filepath.Join(cfg.DataDir, "app.db"))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	if err := reopened.Init(ctx); err != nil {
		t.Fatalf("reopened Init: %v", err)
	}

	got, err := reopened.GetSetting(ctx, "snapshot_test")
	if err != nil {
		t.Fatalf("GetSetting after restore: %v", err)
	}
	if got != "before" {
		t.Fatalf("restored setting = %q, want before", got)
	}
	for path, want := range map[string]string{
		quizFile:       "before-quiz",
		submissionFile: "before-submission",
		qaFile:         "before-qa",
		materialFile:   "after-material",
		assignmentSpec: "after-spec",
	} {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("ReadFile restored %s: %v", path, readErr)
		}
		if string(raw) != want {
			t.Fatalf("restored %s = %q, want %q", path, string(raw), want)
		}
	}
	if _, err := os.Stat(pendingSnapshotRestorePath(cfg)); !os.IsNotExist(err) {
		t.Fatalf("pending restore marker should be removed, got err=%v", err)
	}
}

func TestSystemSnapshotsEndpoints(t *testing.T) {
	t.Parallel()

	cfg := snapshotTestConfig(t)
	ctx := context.Background()

	st, err := store.NewSQLiteStore(filepath.Join(cfg.DataDir, "app.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	srv := New(cfg, st)
	srv.SetShutdownFunc(func() {})
	srv.authTokens["admin-token"] = authSession{
		TeacherID: "admin",
		Role:      domain.RoleAdmin,
		Expiry:    time.Now().Add(time.Hour),
	}

	if err := os.WriteFile(filepath.Join(cfg.MetadataDir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	item, err := srv.createStoredSnapshot(ctx, snapshotKindScheduled, snapshotModeLite, "system")
	if err != nil {
		t.Fatalf("createStoredSnapshot: %v", err)
	}

	handler := srv.Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/system/snapshots", nil)
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "admin-token"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var listResp struct {
		Items []snapshotItem `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0].ID != item.ID {
		t.Fatalf("unexpected snapshot list: %+v", listResp.Items)
	}
	if listResp.Items[0].Kind != snapshotKindScheduled || listResp.Items[0].Mode != snapshotModeLite {
		t.Fatalf("unexpected snapshot metadata: %+v", listResp.Items[0])
	}

	restoreReq := httptest.NewRequest(http.MethodPost, "/api/system/snapshots/restore", strings.NewReader(`{"id":"`+item.ID+`"}`))
	restoreReq.Header.Set("Content-Type", "application/json")
	restoreReq.AddCookie(&http.Cookie{Name: "auth_token", Value: "admin-token"})
	restoreRR := httptest.NewRecorder()
	handler.ServeHTTP(restoreRR, restoreReq)
	if restoreRR.Code != http.StatusOK {
		t.Fatalf("restore status = %d, body=%s", restoreRR.Code, restoreRR.Body.String())
	}

	raw, err := os.ReadFile(pendingSnapshotRestorePath(cfg))
	if err != nil {
		t.Fatalf("read pending restore file: %v", err)
	}
	var pending pendingSnapshotRestore
	if err := json.Unmarshal(raw, &pending); err != nil {
		t.Fatalf("unmarshal pending restore: %v", err)
	}
	if pending.ArchivePath == "" || !strings.HasSuffix(pending.ArchivePath, item.ID) {
		t.Fatalf("unexpected pending restore payload: %+v", pending)
	}
}

func TestSystemSnapshotsCreateServerReturnsSCPCommand(t *testing.T) {
	t.Parallel()

	cfg := snapshotTestConfig(t)
	ctx := context.Background()

	st, err := store.NewSQLiteStore(filepath.Join(cfg.DataDir, "app.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	srv := New(cfg, st)
	srv.authTokens["admin-token"] = authSession{
		TeacherID: "admin",
		Role:      domain.RoleAdmin,
		Expiry:    time.Now().Add(time.Hour),
	}
	if err := os.WriteFile(filepath.Join(cfg.MetadataDir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/system/snapshots/create-server?mode=full", nil)
	req.Host = "course.example.edu:8443"
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "admin-token"})
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create-server status = %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp snapshotServerCopyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal create-server response: %v", err)
	}
	if !resp.OK || resp.Snapshot.Mode != snapshotModeFull || resp.Snapshot.Kind != snapshotKindManual {
		t.Fatalf("unexpected create-server response: %+v", resp)
	}
	if resp.ServerPath == "" || !filepath.IsAbs(resp.ServerPath) {
		t.Fatalf("server path should be absolute: %+v", resp)
	}
	if _, err := os.Stat(resp.ServerPath); err != nil {
		t.Fatalf("stored snapshot missing at %s: %v", resp.ServerPath, err)
	}
	if !strings.Contains(resp.SCPCommand, "root@'course.example.edu':") || !strings.Contains(resp.SCPCommand, shellQuote(resp.ServerPath)) {
		t.Fatalf("unexpected scp command: %s", resp.SCPCommand)
	}
	if resp.DownloadURL == "" || !strings.Contains(resp.DownloadURL, resp.Snapshot.ID) {
		t.Fatalf("unexpected fallback download url: %+v", resp)
	}
}

// TestListSnapshotsHidesUploadStaging guards the listSnapshots filter that
// keeps in-flight upload-restore archives (snapshotUploadPrefix) out of the
// snapshot list, so admins do not see ghost "snapshots" while a restore is
// queued but not yet applied.
func TestListSnapshotsHidesUploadStaging(t *testing.T) {
	t.Parallel()

	cfg := snapshotTestConfig(t)
	ctx := context.Background()

	st, err := store.NewSQLiteStore(filepath.Join(cfg.DataDir, "app.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	srv := New(cfg, st)
	real, err := srv.createStoredSnapshot(ctx, snapshotKindManual, snapshotModeLite, "admin:test")
	if err != nil {
		t.Fatalf("createStoredSnapshot: %v", err)
	}

	// Stage a fake upload-restore archive that should NOT show up in the
	// snapshot list. We reuse the real archive bytes so its manifest is
	// valid; the filter is purely name-based.
	realPath, err := srv.snapshotPath(real.ID)
	if err != nil {
		t.Fatalf("snapshotPath: %v", err)
	}
	realBytes, err := os.ReadFile(realPath)
	if err != nil {
		t.Fatalf("ReadFile real snapshot: %v", err)
	}
	stagingName := snapshotUploadPrefix + "2026-04-25_153646.tar.gz"
	stagingPath := filepath.Join(cfg.SnapshotDir, stagingName)
	if err := os.WriteFile(stagingPath, realBytes, 0o644); err != nil {
		t.Fatalf("WriteFile staging: %v", err)
	}

	items, err := srv.listSnapshots()
	if err != nil {
		t.Fatalf("listSnapshots: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 visible snapshot, got %d: %+v", len(items), items)
	}
	if items[0].ID != real.ID {
		t.Fatalf("unexpected visible snapshot id: %s (staging leaked into list)", items[0].ID)
	}
	for _, item := range items {
		if strings.HasPrefix(item.ID, snapshotUploadPrefix) {
			t.Fatalf("upload staging file leaked into snapshot list: %+v", item)
		}
	}
}

func TestSystemSnapshotsUploadRestoreQueuesUploadedArchive(t *testing.T) {
	t.Parallel()

	cfg := snapshotTestConfig(t)
	ctx := context.Background()

	st, err := store.NewSQLiteStore(filepath.Join(cfg.DataDir, "app.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.SetSetting(ctx, "snapshot_upload_test", "from-uploaded-snapshot"); err != nil {
		t.Fatalf("SetSetting before snapshot: %v", err)
	}

	srv := New(cfg, st)
	srv.SetShutdownFunc(func() {})
	srv.authTokens["admin-token"] = authSession{
		TeacherID: "admin",
		Role:      domain.RoleAdmin,
		Expiry:    time.Now().Add(time.Hour),
	}

	item, err := srv.createStoredSnapshot(ctx, snapshotKindManual, snapshotModeLite, "admin:source")
	if err != nil {
		t.Fatalf("createStoredSnapshot: %v", err)
	}
	archivePath, err := srv.snapshotPath(item.ID)
	if err != nil {
		t.Fatalf("snapshotPath: %v", err)
	}
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("ReadFile archive: %v", err)
	}
	if err := st.SetSetting(ctx, "snapshot_upload_test", "after-snapshot"); err != nil {
		t.Fatalf("SetSetting after snapshot: %v", err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "cloud-snapshot.tar.gz")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(archiveBytes); err != nil {
		t.Fatalf("Write multipart archive: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/system/snapshots/upload-restore", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "admin-token"})
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("upload restore status = %d, body=%s", rr.Code, rr.Body.String())
	}

	raw, err := os.ReadFile(pendingSnapshotRestorePath(cfg))
	if err != nil {
		t.Fatalf("read pending restore file: %v", err)
	}
	var pending pendingSnapshotRestore
	if err := json.Unmarshal(raw, &pending); err != nil {
		t.Fatalf("unmarshal pending restore: %v", err)
	}
	if !pending.CleanupOnDone {
		t.Fatalf("uploaded restore should clean staged archive on apply: %+v", pending)
	}
	if !strings.HasPrefix(filepath.Base(pending.ArchivePath), snapshotUploadPrefix) {
		t.Fatalf("uploaded archive should be staged with upload prefix: %+v", pending)
	}
	if _, err := readSnapshotManifest(pending.ArchivePath); err != nil {
		t.Fatalf("staged uploaded archive is invalid: %v", err)
	}

	if err := st.Close(); err != nil {
		t.Fatalf("Close store before restore: %v", err)
	}
	if err := ApplyPendingSnapshotRestore(cfg); err != nil {
		t.Fatalf("ApplyPendingSnapshotRestore: %v", err)
	}
	if _, err := os.Stat(pending.ArchivePath); !os.IsNotExist(err) {
		t.Fatalf("expected staged uploaded archive to be removed, stat err=%v", err)
	}

	reopened, err := store.NewSQLiteStore(filepath.Join(cfg.DataDir, "app.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore reopened: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.GetSetting(ctx, "snapshot_upload_test")
	if err != nil {
		t.Fatalf("GetSetting after uploaded restore: %v", err)
	}
	if got != "from-uploaded-snapshot" {
		t.Fatalf("uploaded restore value = %q, want %q", got, "from-uploaded-snapshot")
	}
}

func snapshotTestConfig(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	cfg := Config{
		DataDir:     filepath.Join(root, "data"),
		MetadataDir: filepath.Join(root, "metadata"),
		SnapshotDir: filepath.Join(root, "snapshots"),
	}
	for _, dir := range []string{cfg.DataDir, cfg.MetadataDir, cfg.SnapshotDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	return cfg
}
