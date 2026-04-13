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
)

func newMaterialTestServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	return &Server{
		cfg:   Config{DataDir: dataDir},
		store: &memStore{settings: map[string]string{}},
		adminTokens: map[string]time.Time{
			"test-admin": time.Now().Add(time.Hour),
		},
	}
}

func createMaterialFile(t *testing.T, s *Server, folder, name string, data []byte) {
	t.Helper()
	dir := filepath.Join(s.pptDir(), folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir ppt dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatalf("write material file: %v", err)
	}
}

func listMaterialFileNames(t *testing.T, s *Server, folder string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.pptDir(), folder))
	if err != nil {
		t.Fatalf("read material dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func setMaterialHiddenForTest(t *testing.T, s *Server, folder, file string) {
	t.Helper()
	if err := s.setMaterialVisibility(context.Background(), folder, file, false); err != nil {
		t.Fatalf("setMaterialVisibility: %v", err)
	}
}

func readMaterialVisibilitySetting(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	raw, err := s.store.GetSetting(context.Background(), materialVisibilitySettingKey)
	if err != nil {
		t.Fatalf("GetSetting(%s): %v", materialVisibilitySettingKey, err)
	}
	got := map[string]bool{}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal visibility setting: %v", err)
	}
	return got
}

func newAdminRequest(method, target string, body []byte) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "admin_token", Value: "test-admin"})
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func TestScanMaterialGroups(t *testing.T) {
	s := newMaterialTestServer(t)
	createMaterialFile(t, s, "course-a", "quiz1.pdf", []byte("%PDF-1.4\nhello"))
	createMaterialFile(t, s, "course-a", "quiz1.ipynb", []byte("{}"))
	createMaterialFile(t, s, "course-a", "quiz2.zip", []byte("zip"))
	createMaterialFile(t, s, "course-a", "ignore.txt", []byte("nope"))

	items, err := s.scanMaterialGroups()
	if err != nil {
		t.Fatalf("scanMaterialGroups: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(items))
	}
	if items[0].Stem != "quiz1" {
		t.Fatalf("expected first stem quiz1, got %s", items[0].Stem)
	}
	if items[0].PDF == nil || !strings.Contains(items[0].PDF.PreviewURL, "/ppt/course-a/quiz1.pdf") {
		t.Fatalf("expected pdf preview url in first group: %+v", items[0].PDF)
	}
	if len(items[0].Downloads) != 2 {
		t.Fatalf("expected 2 downloads in first group, got %d", len(items[0].Downloads))
	}
	if items[1].PDF != nil {
		t.Fatalf("expected non-pdf group without preview pdf")
	}
}

func TestAPIPDFsCompatibilityView(t *testing.T) {
	s := newMaterialTestServer(t)
	createMaterialFile(t, s, "course-a", "quiz1.pdf", []byte("%PDF-1.4\nhello"))
	createMaterialFile(t, s, "course-a", "quiz1.ipynb", []byte("{}"))

	req := httptest.NewRequest(http.MethodGet, "/api/pdfs", nil)
	rr := httptest.NewRecorder()
	s.apiPDFs(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp struct {
		Items []pdfItem `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected only one pdf item, got %d", len(resp.Items))
	}
	if resp.Items[0].File != "quiz1.pdf" {
		t.Fatalf("unexpected file: %+v", resp.Items[0])
	}
}

func TestMaterialVisibilityFiltersPublicListings(t *testing.T) {
	s := newMaterialTestServer(t)
	createMaterialFile(t, s, "course-a", "visible.pdf", []byte("%PDF-1.4\nvisible"))
	createMaterialFile(t, s, "course-a", "visible.ipynb", []byte("{}"))
	createMaterialFile(t, s, "course-a", "hidden.pdf", []byte("%PDF-1.4\nhidden"))
	createMaterialFile(t, s, "course-a", "hidden.ipynb", []byte("{}"))
	setMaterialHiddenForTest(t, s, "course-a", "hidden.pdf")
	setMaterialHiddenForTest(t, s, "course-a", "hidden.ipynb")

	materialsReq := httptest.NewRequest(http.MethodGet, "/api/materials", nil)
	materialsRR := httptest.NewRecorder()
	s.apiMaterials(materialsRR, materialsReq)
	if materialsRR.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", materialsRR.Code)
	}
	var materialsResp struct {
		Items []materialGroupItem `json:"items"`
	}
	if err := json.Unmarshal(materialsRR.Body.Bytes(), &materialsResp); err != nil {
		t.Fatalf("unmarshal materials response: %v", err)
	}
	if len(materialsResp.Items) != 1 {
		t.Fatalf("expected 1 visible group, got %d", len(materialsResp.Items))
	}
	if materialsResp.Items[0].Stem != "visible" {
		t.Fatalf("expected visible stem, got %+v", materialsResp.Items[0])
	}
	if len(materialsResp.Items[0].Downloads) != 2 {
		t.Fatalf("expected visible downloads only, got %+v", materialsResp.Items[0].Downloads)
	}

	pdfsReq := httptest.NewRequest(http.MethodGet, "/api/pdfs", nil)
	pdfsRR := httptest.NewRecorder()
	s.apiPDFs(pdfsRR, pdfsReq)
	if pdfsRR.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", pdfsRR.Code)
	}
	var pdfsResp struct {
		Items []pdfItem `json:"items"`
	}
	if err := json.Unmarshal(pdfsRR.Body.Bytes(), &pdfsResp); err != nil {
		t.Fatalf("unmarshal pdfs response: %v", err)
	}
	if len(pdfsResp.Items) != 1 || pdfsResp.Items[0].File != "visible.pdf" {
		t.Fatalf("expected only visible.pdf, got %+v", pdfsResp.Items)
	}
}

func TestHiddenMaterialDirectAccessReturns404(t *testing.T) {
	s := newMaterialTestServer(t)
	createMaterialFile(t, s, "course-a", "slides.pdf", []byte("%PDF-1.4\nhello"))
	createMaterialFile(t, s, "course-a", "lab.ipynb", []byte("{}"))
	setMaterialHiddenForTest(t, s, "course-a", "slides.pdf")
	setMaterialHiddenForTest(t, s, "course-a", "lab.ipynb")

	pdfReq := httptest.NewRequest(http.MethodGet, "/ppt/course-a/slides.pdf", nil)
	pdfRR := httptest.NewRecorder()
	s.servePPT(pdfRR, pdfReq)
	if pdfRR.Code != http.StatusNotFound {
		t.Fatalf("expected hidden PDF 404, got %d", pdfRR.Code)
	}

	downloadReq := httptest.NewRequest(http.MethodGet, "/materials-files/course-a/lab.ipynb", nil)
	downloadRR := httptest.NewRecorder()
	s.serveMaterialDownload(downloadRR, downloadReq)
	if downloadRR.Code != http.StatusNotFound {
		t.Fatalf("expected hidden download 404, got %d", downloadRR.Code)
	}
}

func TestAdminCanStillAccessHiddenMaterialFiles(t *testing.T) {
	s := newMaterialTestServer(t)
	createMaterialFile(t, s, "course-a", "slides.pdf", []byte("%PDF-1.4\nhello"))
	createMaterialFile(t, s, "course-a", "lab.ipynb", []byte("{}"))
	setMaterialHiddenForTest(t, s, "course-a", "slides.pdf")
	setMaterialHiddenForTest(t, s, "course-a", "lab.ipynb")

	pdfReq := httptest.NewRequest(http.MethodGet, "/ppt/course-a/slides.pdf", nil)
	pdfReq.AddCookie(&http.Cookie{Name: "admin_token", Value: "test-admin"})
	pdfRR := httptest.NewRecorder()
	s.servePPT(pdfRR, pdfReq)
	if pdfRR.Code != http.StatusOK {
		t.Fatalf("expected admin hidden PDF access 200, got %d", pdfRR.Code)
	}

	downloadReq := httptest.NewRequest(http.MethodGet, "/materials-files/course-a/lab.ipynb", nil)
	downloadReq.AddCookie(&http.Cookie{Name: "admin_token", Value: "test-admin"})
	downloadRR := httptest.NewRecorder()
	s.serveMaterialDownload(downloadRR, downloadReq)
	if downloadRR.Code != http.StatusOK {
		t.Fatalf("expected admin hidden download access 200, got %d", downloadRR.Code)
	}
}

func TestHiddenMaterialAccessFailsClosedWhenVisibilityStateIsInvalid(t *testing.T) {
	s := newMaterialTestServer(t)
	createMaterialFile(t, s, "course-a", "slides.pdf", []byte("%PDF-1.4\nhello"))
	st := s.store.(*memStore)
	st.settings[materialVisibilitySettingKey] = "{not-json"

	req := httptest.NewRequest(http.MethodGet, "/ppt/course-a/slides.pdf", nil)
	rr := httptest.NewRecorder()
	s.servePPT(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected invalid visibility state to fail closed with 404, got %d", rr.Code)
	}
}

func TestAPIAdminMaterialsIncludesHiddenFiles(t *testing.T) {
	s := newMaterialTestServer(t)
	createMaterialFile(t, s, "course-a", "visible.pdf", []byte("%PDF-1.4\nvisible"))
	createMaterialFile(t, s, "course-a", "hidden.ipynb", []byte("{}"))
	setMaterialHiddenForTest(t, s, "course-a", "hidden.ipynb")

	req := newAdminRequest(http.MethodGet, "/api/admin/materials", nil)
	rr := httptest.NewRecorder()
	s.apiAdminMaterials(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Items []adminMaterialGroupItem `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal admin materials response: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected both visible and hidden groups, got %+v", resp.Items)
	}
	if resp.Items[0].Stem != "hidden" || resp.Items[0].Downloads[0].Visible {
		t.Fatalf("expected hidden item with visible=false, got %+v", resp.Items[0])
	}
	if resp.Items[1].Stem != "visible" || !resp.Items[1].Downloads[0].Visible {
		t.Fatalf("expected visible item with visible=true, got %+v", resp.Items[1])
	}
}

func TestAPIAdminPDFVisibilityPersistsAcrossRestart(t *testing.T) {
	s := newMaterialTestServer(t)
	createMaterialFile(t, s, "course-a", "slides.pdf", []byte("%PDF-1.4\nhello"))
	body, err := json.Marshal(map[string]any{
		"folder":  "course-a",
		"file":    "slides.pdf",
		"visible": false,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := newAdminRequest(http.MethodPost, "/api/admin/pdfs/visibility", body)
	rr := httptest.NewRecorder()
	s.apiAdminPDFVisibility(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	visibility := readMaterialVisibilitySetting(t, s)
	if visibility[materialVisibilityPath("course-a", "slides.pdf")] {
		t.Fatalf("expected stored visibility false, got %+v", visibility)
	}

	restarted := &Server{
		cfg:   s.cfg,
		store: s.store,
		adminTokens: map[string]time.Time{
			"test-admin": time.Now().Add(time.Hour),
		},
	}
	publicReq := httptest.NewRequest(http.MethodGet, "/api/pdfs", nil)
	publicRR := httptest.NewRecorder()
	restarted.apiPDFs(publicRR, publicReq)
	if publicRR.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", publicRR.Code)
	}
	var publicResp struct {
		Items []pdfItem `json:"items"`
	}
	if err := json.Unmarshal(publicRR.Body.Bytes(), &publicResp); err != nil {
		t.Fatalf("unmarshal public response: %v", err)
	}
	if len(publicResp.Items) != 0 {
		t.Fatalf("expected hidden file to stay hidden after restart, got %+v", publicResp.Items)
	}

	directReq := httptest.NewRequest(http.MethodGet, "/ppt/course-a/slides.pdf", nil)
	directRR := httptest.NewRecorder()
	restarted.servePPT(directRR, directReq)
	if directRR.Code != http.StatusNotFound {
		t.Fatalf("expected hidden file 404 after restart, got %d", directRR.Code)
	}
}

func TestAPIAdminPDFRenamePreservesVisibility(t *testing.T) {
	s := newMaterialTestServer(t)
	createMaterialFile(t, s, "course-a", "old.pdf", []byte("%PDF-1.4\nhello"))
	setMaterialHiddenForTest(t, s, "course-a", "old.pdf")
	body, err := json.Marshal(map[string]string{
		"folder":   "course-a",
		"old_name": "old.pdf",
		"new_name": "new.pdf",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := newAdminRequest(http.MethodPost, "/api/admin/pdfs/rename", body)
	rr := httptest.NewRecorder()
	s.apiAdminPDFRename(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.pptDir(), "course-a", "old.pdf")); !os.IsNotExist(err) {
		t.Fatalf("expected old path gone, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(s.pptDir(), "course-a", "new.pdf")); err != nil {
		t.Fatalf("expected renamed file to exist: %v", err)
	}
	visibility := readMaterialVisibilitySetting(t, s)
	if _, ok := visibility[materialVisibilityPath("course-a", "old.pdf")]; ok {
		t.Fatalf("expected old visibility entry removed, got %+v", visibility)
	}
	if visibility[materialVisibilityPath("course-a", "new.pdf")] {
		t.Fatalf("expected renamed visibility to stay false, got %+v", visibility)
	}
	blockedReq := httptest.NewRequest(http.MethodGet, "/ppt/course-a/new.pdf", nil)
	blockedRR := httptest.NewRecorder()
	s.servePPT(blockedRR, blockedReq)
	if blockedRR.Code != http.StatusNotFound {
		t.Fatalf("expected renamed hidden file 404, got %d", blockedRR.Code)
	}
}

func TestAPIAdminPDFDeleteRemovesVisibilityEntry(t *testing.T) {
	s := newMaterialTestServer(t)
	createMaterialFile(t, s, "course-a", "old.pdf", []byte("%PDF-1.4\nhello"))
	setMaterialHiddenForTest(t, s, "course-a", "old.pdf")
	body, err := json.Marshal(map[string]string{
		"folder": "course-a",
		"file":   "old.pdf",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := newAdminRequest(http.MethodPost, "/api/admin/pdfs/delete", body)
	rr := httptest.NewRecorder()
	s.apiAdminPDFDelete(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.pptDir(), "course-a", "old.pdf")); !os.IsNotExist(err) {
		t.Fatalf("expected file deleted, got err=%v", err)
	}
	visibility := readMaterialVisibilitySetting(t, s)
	if _, ok := visibility[materialVisibilityPath("course-a", "old.pdf")]; ok {
		t.Fatalf("expected delete to clean visibility entry, got %+v", visibility)
	}
}

func TestAPIAdminPDFUploadPartialSuccess(t *testing.T) {
	s := newMaterialTestServer(t)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("folder", "course-a"); err != nil {
		t.Fatalf("write folder field: %v", err)
	}
	files := []struct {
		Name string
		Data []byte
	}{
		{Name: "quiz1.pdf", Data: []byte("%PDF-1.4\nhello")},
		{Name: "lab.ipynb", Data: []byte("{}")},
		{Name: "bad.txt", Data: []byte("bad")},
	}
	for _, file := range files {
		part, err := writer.CreateFormFile("files", file.Name)
		if err != nil {
			t.Fatalf("create form file %s: %v", file.Name, err)
		}
		if _, err := part.Write(file.Data); err != nil {
			t.Fatalf("write form file %s: %v", file.Name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/pdfs/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "admin_token", Value: "test-admin"})
	rr := httptest.NewRecorder()
	s.apiAdminPDFUpload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		OK       bool                    `json:"ok"`
		Uploaded []materialUploadSuccess `json:"uploaded"`
		Failed   []materialUploadFailure `json:"failed"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.OK {
		t.Fatalf("expected partial success ok=false")
	}
	if len(resp.Uploaded) != 2 || len(resp.Failed) != 1 {
		t.Fatalf("unexpected upload counts uploaded=%d failed=%d", len(resp.Uploaded), len(resp.Failed))
	}
	if _, err := os.Stat(filepath.Join(s.pptDir(), "course-a", "quiz1.pdf")); err != nil {
		t.Fatalf("expected quiz1.pdf to be saved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.pptDir(), "course-a", "lab.ipynb")); err != nil {
		t.Fatalf("expected lab.ipynb to be saved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.pptDir(), "course-a", "bad.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected bad.txt not to be saved, got err=%v", err)
	}
}

func TestAPIAdminPDFUploadNormalizesUppercasePDFExtension(t *testing.T) {
	s := newMaterialTestServer(t)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("folder", "course-a"); err != nil {
		t.Fatalf("write folder field: %v", err)
	}
	part, err := writer.CreateFormFile("files", "quiz1.PDF")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte("%PDF-1.4\nhello")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/pdfs/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "admin_token", Value: "test-admin"})
	rr := httptest.NewRecorder()
	s.apiAdminPDFUpload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	names := listMaterialFileNames(t, s, "course-a")
	if len(names) != 1 || names[0] != "quiz1.pdf" {
		t.Fatalf("expected only normalized lowercase file, got %v", names)
	}
}

func TestServeMaterialDownloadAttachment(t *testing.T) {
	s := newMaterialTestServer(t)
	createMaterialFile(t, s, "course-a", "lab.ipynb", []byte("{}"))
	req := httptest.NewRequest(http.MethodGet, "/materials-files/course-a/lab.ipynb", nil)
	rr := httptest.NewRecorder()
	s.serveMaterialDownload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Fatalf("expected attachment disposition, got %q", got)
	}
}


func TestMatchedPDFPathIgnoresCompanionFiles(t *testing.T) {
	s := newMaterialTestServer(t)
	st := s.store.(*memStore)
	st.settings["quiz_source_path"] = filepath.Join("quiz", "course-a", "quiz1.yaml")
	createMaterialFile(t, s, "course-a", "quiz1.pdf", []byte("%PDF-1.4\nhello"))
	createMaterialFile(t, s, "course-a", "quiz1.ipynb", []byte("{}"))
	if got := s.matchedPDFPath(); got != filepath.Join(s.pptDir(), "course-a", "quiz1.pdf") {
		t.Fatalf("unexpected matched pdf path: %s", got)
	}
}

func TestMatchedPDFPathWorksAfterUppercasePDFUpload(t *testing.T) {
	s := newMaterialTestServer(t)
	st := s.store.(*memStore)
	st.settings["quiz_source_path"] = filepath.Join("quiz", "course-a", "quiz1.yaml")
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("folder", "course-a"); err != nil {
		t.Fatalf("write folder field: %v", err)
	}
	part, err := writer.CreateFormFile("files", "quiz1.PDF")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte("%PDF-1.4\nhello")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/pdfs/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "admin_token", Value: "test-admin"})
	rr := httptest.NewRecorder()
	s.apiAdminPDFUpload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := s.matchedPDFPath(); got != filepath.Join(s.pptDir(), "course-a", "quiz1.pdf") {
		t.Fatalf("unexpected matched pdf path after upload: %s", got)
	}
}
