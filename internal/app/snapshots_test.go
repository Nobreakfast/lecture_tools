package app

import (
	"context"
	"encoding/json"
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
	materialFile := filepath.Join(cfg.MetadataDir, "teacher1", "course1", "materials", "slide.pdf")
	assignmentSpec := filepath.Join(cfg.MetadataDir, "teacher1", "course1", "assignment", "hw1", "spec.pdf")
	for path, content := range map[string]string{
		quizFile:       "before-quiz",
		submissionFile: "before-submission",
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
