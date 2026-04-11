package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func newHomeworkTestServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	s := &Server{
		cfg:         Config{DataDir: dataDir},
		store:       &memStore{},
		adminTokens: map[string]time.Time{"test-admin": time.Now().Add(time.Hour)},
	}
	writeHomeworkAssignmentPDF(t, s, "course-a", "task-1", []byte("%PDF-1.4\nassignment-one"))
	writeHomeworkAssignmentBundleFile(t, s, "course-a", "task-2", "task-2.pdf", []byte("%PDF-1.4\nassignment-two"))
	writeHomeworkAssignmentBundleFile(t, s, "course-a", "task-2", "dataset.npy", []byte("NUMPY-DATA"))
	writeHomeworkAssignmentBundleFile(t, s, "course-a", "task-2", "starter.py", []byte("print('hello')\n"))
	writeHomeworkAssignmentPDF(t, s, "course-b", "lab-1", []byte("%PDF-1.4\nassignment-three"))
	return s
}

func writeHomeworkAssignmentPDF(t *testing.T, s *Server, course, assignmentID string, data []byte) {
	t.Helper()
	dir := filepath.Join(s.pptDir(), homeworkAssignmentsFolder, course)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir assignment dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, assignmentID+".pdf"), data, 0o644); err != nil {
		t.Fatalf("write assignment pdf: %v", err)
	}
}

func writeHomeworkAssignmentBundleFile(t *testing.T, s *Server, course, assignmentID, fileName string, data []byte) {
	t.Helper()
	dir := filepath.Join(s.pptDir(), homeworkAssignmentsFolder, course, assignmentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir assignment bundle dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), data, 0o644); err != nil {
		t.Fatalf("write assignment bundle file: %v", err)
	}
}

func doHomeworkMultiUpload(t *testing.T, h http.Handler, target string, fields map[string]string, files map[string][]byte, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for key, value := range fields {
		if err := mw.WriteField(key, value); err != nil {
			t.Fatalf("WriteField(%s): %v", key, err)
		}
	}
	for filename, data := range files {
		fw, err := mw.CreateFormFile("files", filename)
		if err != nil {
			t.Fatalf("CreateFormFile(%s): %v", filename, err)
		}
		if _, err := fw.Write(data); err != nil {
			t.Fatalf("Write upload data(%s): %v", filename, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func doHomeworkJSON(t *testing.T, h http.Handler, method, target string, body []byte, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func doHomeworkUpload(t *testing.T, h http.Handler, target string, fields map[string]string, filename string, data []byte, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for key, value := range fields {
		if err := mw.WriteField(key, value); err != nil {
			t.Fatalf("WriteField(%s): %v", key, err)
		}
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("Write upload data: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func homeworkCookieFromResponse(t *testing.T, rr *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range rr.Result().Cookies() {
		if cookie.Name == homeworkCookieName {
			return cookie
		}
	}
	t.Fatalf("missing homework cookie")
	return nil
}

func decodeSubmissionResponse(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, rr.Body.String())
	}
	return resp
}

func TestHomeworkCatalogAndSubmissionFlow(t *testing.T) {
	s := newHomeworkTestServer(t)
	h := s.Routes()

	coursesRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/courses", nil)
	if coursesRR.Code != http.StatusOK {
		t.Fatalf("expected courses 200, got %d body=%s", coursesRR.Code, coursesRR.Body.String())
	}
	if !bytes.Contains(coursesRR.Body.Bytes(), []byte(`"course":"course-a"`)) || !bytes.Contains(coursesRR.Body.Bytes(), []byte(`"course":"course-b"`)) {
		t.Fatalf("unexpected courses response: %s", coursesRR.Body.String())
	}

	assignmentsRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/assignments?course=course-a", nil)
	if assignmentsRR.Code != http.StatusOK {
		t.Fatalf("expected assignments 200, got %d body=%s", assignmentsRR.Code, assignmentsRR.Body.String())
	}
	if !bytes.Contains(assignmentsRR.Body.Bytes(), []byte(`"assignment_id":"task-1"`)) || !bytes.Contains(assignmentsRR.Body.Bytes(), []byte(`/api/homework/assignment-file?assignment_id=task-1`)) || !bytes.Contains(assignmentsRR.Body.Bytes(), []byte(`"name":"task-1.pdf"`)) {
		t.Fatalf("unexpected assignments response: %s", assignmentsRR.Body.String())
	}

	downloadRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/assignment-file?course=course-a&assignment_id=task-1&file=task-1.pdf", nil)
	if downloadRR.Code != http.StatusOK || !bytes.HasPrefix(downloadRR.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("expected assignment file download, got code=%d body=%q", downloadRR.Code, downloadRR.Body.String())
	}
	if !bytes.Contains([]byte(downloadRR.Header().Get("Content-Disposition")), []byte("attachment")) {
		t.Fatalf("expected attachment header for assignment file, got %s", downloadRR.Header().Get("Content-Disposition"))
	}

	body := []byte(`{"name":"张三","student_no":"2026001","class_name":"1班","course":"course-a","assignment_id":"task-1"}`)
	createRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", body)
	if createRR.Code != http.StatusOK {
		t.Fatalf("expected create 200, got %d body=%s", createRR.Code, createRR.Body.String())
	}
	cookie := homeworkCookieFromResponse(t, createRR)
	createdID := decodeSubmissionResponse(t, createRR)["submission"].(map[string]any)["id"].(string)

	resumeRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", body)
	if resumeRR.Code != http.StatusOK {
		t.Fatalf("expected resume 200, got %d body=%s", resumeRR.Code, resumeRR.Body.String())
	}
	cookie = homeworkCookieFromResponse(t, resumeRR)
	if gotID := decodeSubmissionResponse(t, resumeRR)["submission"].(map[string]any)["id"].(string); gotID != createdID {
		t.Fatalf("expected resumed same submission id, got %s want %s", gotID, createdID)
	}
	if len(s.store.(*memStore).homeworkSubmissions) != 1 {
		t.Fatalf("expected 1 homework submission, got %d", len(s.store.(*memStore).homeworkSubmissions))
	}

	badUploadRR := doHomeworkUpload(t, h, "/api/homework/upload", map[string]string{"slot": "code"}, "fake.bin", []byte("not-a-notebook"), cookie)
	if badUploadRR.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid notebook upload 400, got %d", badUploadRR.Code)
	}

	report1 := []byte("%PDF-1.4\nreport-one")
	reportUpload1RR := doHomeworkUpload(t, h, "/api/homework/upload", map[string]string{"slot": "report"}, "draft.txt", report1, cookie)
	if reportUpload1RR.Code != http.StatusOK {
		t.Fatalf("expected report upload 200, got %d body=%s", reportUpload1RR.Code, reportUpload1RR.Body.String())
	}

	report2 := []byte("%PDF-1.4\nreport-two")
	reportUpload2RR := doHomeworkUpload(t, h, "/api/homework/upload", map[string]string{"slot": "report"}, "final-report.pdf", report2, cookie)
	if reportUpload2RR.Code != http.StatusOK {
		t.Fatalf("expected report replace 200, got %d body=%s", reportUpload2RR.Code, reportUpload2RR.Body.String())
	}

	notebookData := []byte(`{"cells":[],"metadata":{},"nbformat":4,"nbformat_minor":5}`)
	codeUploadRR := doHomeworkUpload(t, h, "/api/homework/upload", map[string]string{"slot": "code"}, "source.ipynb", notebookData, cookie)
	if codeUploadRR.Code != http.StatusOK {
		t.Fatalf("expected code upload 200, got %d body=%s", codeUploadRR.Code, codeUploadRR.Body.String())
	}

	deleteCodeRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/delete", []byte(`{"slot":"code"}`), cookie)
	if deleteCodeRR.Code != http.StatusOK {
		t.Fatalf("expected code delete 200, got %d body=%s", deleteCodeRR.Code, deleteCodeRR.Body.String())
	}

	codeUploadAgainRR := doHomeworkUpload(t, h, "/api/homework/upload", map[string]string{"slot": "code"}, "analysis.ipynb", notebookData, cookie)
	if codeUploadAgainRR.Code != http.StatusOK {
		t.Fatalf("expected code re-upload 200, got %d body=%s", codeUploadAgainRR.Code, codeUploadAgainRR.Body.String())
	}

	getRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/submission", nil, cookie)
	if getRR.Code != http.StatusOK {
		t.Fatalf("expected get submission 200, got %d body=%s", getRR.Code, getRR.Body.String())
	}
	submission := decodeSubmissionResponse(t, getRR)["submission"].(map[string]any)
	files := submission["files"].(map[string]any)
	reportFile := files["report"].(map[string]any)
	codeFile := files["code"].(map[string]any)
	if submission["assignment_id"].(string) != "task-1" {
		t.Fatalf("unexpected assignment id payload: %+v", submission)
	}
	if reportFile["original_name"].(string) != "final-report.pdf" {
		t.Fatalf("unexpected report original name: %+v", reportFile)
	}
	if codeFile["original_name"].(string) != "analysis.ipynb" {
		t.Fatalf("unexpected code original name: %+v", codeFile)
	}

	submissionDir := filepath.Join(s.cfg.DataDir, "homework", "course-a", "task-1", "2026001", createdID)
	reportPath := filepath.Join(submissionDir, "report.pdf")
	codePath := filepath.Join(submissionDir, "notebook.ipynb")
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("expected report file: %v", err)
	}
	if _, err := os.Stat(codePath); err != nil {
		t.Fatalf("expected code file: %v", err)
	}
	reportOnDisk, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report file: %v", err)
	}
	if string(reportOnDisk) != string(report2) {
		t.Fatalf("expected replaced report contents, got %q", string(reportOnDisk))
	}
	entries, err := os.ReadDir(submissionDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := []string{entries[0].Name(), entries[1].Name()}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "notebook.ipynb" || names[1] != "report.pdf" {
		t.Fatalf("expected fixed filenames only, got %+v", names)
	}
}

func TestHomeworkSessionRequiresMatchingIdentityForExistingRecord(t *testing.T) {
	s := newHomeworkTestServer(t)
	h := s.Routes()

	body := []byte(`{"name":"张三","student_no":"2026001","class_name":"1班","course":"course-a","assignment_id":"task-1"}`)
	createRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", body)
	if createRR.Code != http.StatusOK {
		t.Fatalf("expected create 200, got %d body=%s", createRR.Code, createRR.Body.String())
	}

	mismatchBody := []byte(`{"name":"李四","student_no":"2026001","class_name":"2班","course":"course-a","assignment_id":"task-1"}`)
	mismatchRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", mismatchBody)
	if mismatchRR.Code != http.StatusForbidden {
		t.Fatalf("expected mismatch resume 403, got %d body=%s", mismatchRR.Code, mismatchRR.Body.String())
	}
}

func TestHomeworkAdminAssignmentAndSubmissionRoutes(t *testing.T) {
	s := newHomeworkTestServer(t)
	h := s.Routes()

	body := []byte(`{"name":"李四","student_no":"2026999","class_name":"2班","course":"course-a","assignment_id":"task-2"}`)
	createRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", body)
	if createRR.Code != http.StatusOK {
		t.Fatalf("expected create 200, got %d body=%s", createRR.Code, createRR.Body.String())
	}
	cookie := homeworkCookieFromResponse(t, createRR)
	submissionID := decodeSubmissionResponse(t, createRR)["submission"].(map[string]any)["id"].(string)

	if rr := doHomeworkUpload(t, h, "/api/homework/upload", map[string]string{"slot": "report"}, "老师版.pdf", []byte("%PDF-1.4\nhello"), cookie); rr.Code != http.StatusOK {
		t.Fatalf("expected report upload 200, got %d", rr.Code)
	}
	if rr := doHomeworkUpload(t, h, "/api/homework/upload", map[string]string{"slot": "code"}, "实验记录.ipynb", []byte(`{"cells":[],"metadata":{},"nbformat":4,"nbformat_minor":5}`), cookie); rr.Code != http.StatusOK {
		t.Fatalf("expected code upload 200, got %d", rr.Code)
	}

	privateRR := doHomeworkJSON(t, h, http.MethodGet, "/api/admin/homework/report?id="+submissionID, nil)
	if privateRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected private report 401, got %d", privateRR.Code)
	}

	adminCookie := &http.Cookie{Name: "admin_token", Value: "test-admin"}
	assignmentsRR := doHomeworkJSON(t, h, http.MethodGet, "/api/admin/homework/assignments?course=course-a", nil, adminCookie)
	if assignmentsRR.Code != http.StatusOK {
		t.Fatalf("expected admin assignments 200, got %d body=%s", assignmentsRR.Code, assignmentsRR.Body.String())
	}
	if !bytes.Contains(assignmentsRR.Body.Bytes(), []byte(`"assignment_id":"task-1"`)) || !bytes.Contains(assignmentsRR.Body.Bytes(), []byte(`"assignment_id":"task-2"`)) {
		t.Fatalf("unexpected admin assignments: %s", assignmentsRR.Body.String())
	}

	uploadRR := doHomeworkMultiUpload(t, h, "/api/admin/homework/assignments/upload", map[string]string{"course": "course-a", "assignment_id": "task-3"}, map[string][]byte{
		"task-3.pdf": []byte("%PDF-1.4\nadmin-upload"),
		"data.npy":   []byte("NUMPY"),
	}, adminCookie)
	if uploadRR.Code != http.StatusOK {
		t.Fatalf("expected admin assignment upload 200, got %d body=%s", uploadRR.Code, uploadRR.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.pptDir(), homeworkAssignmentsFolder, "course-a", "task-3", "task-3.pdf")); err != nil {
		t.Fatalf("expected uploaded assignment pdf in bundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.pptDir(), homeworkAssignmentsFolder, "course-a", "task-3", "data.npy")); err != nil {
		t.Fatalf("expected uploaded assignment data file in bundle: %v", err)
	}

	listRR := doHomeworkJSON(t, h, http.MethodGet, "/api/admin/homework/submissions?course=course-a&assignment_id=task-2", nil, adminCookie)
	if listRR.Code != http.StatusOK {
		t.Fatalf("expected admin list 200, got %d body=%s", listRR.Code, listRR.Body.String())
	}
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0]["id"].(string) != submissionID {
		t.Fatalf("unexpected admin list items: %+v", listResp.Items)
	}

	detailRR := doHomeworkJSON(t, h, http.MethodGet, "/api/admin/homework/submission?id="+submissionID, nil, adminCookie)
	if detailRR.Code != http.StatusOK {
		t.Fatalf("expected admin detail 200, got %d body=%s", detailRR.Code, detailRR.Body.String())
	}
	var detailResp struct {
		Submission map[string]any `json:"submission"`
	}
	if err := json.Unmarshal(detailRR.Body.Bytes(), &detailResp); err != nil {
		t.Fatalf("unmarshal detail response: %v", err)
	}
	if detailResp.Submission["report_preview_url"].(string) == "" || detailResp.Submission["archive_download_url"].(string) == "" {
		t.Fatalf("expected admin download urls, got %+v", detailResp.Submission)
	}

	reportRR := doHomeworkJSON(t, h, http.MethodGet, "/api/admin/homework/report?id="+submissionID, nil, adminCookie)
	if reportRR.Code != http.StatusOK || !bytes.HasPrefix(reportRR.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("expected admin report pdf, got code=%d body=%q", reportRR.Code, reportRR.Body.String())
	}

	reportDownloadRR := doHomeworkJSON(t, h, http.MethodGet, "/api/admin/homework/report?id="+submissionID+"&download=1", nil, adminCookie)
	if !bytes.Contains([]byte(reportDownloadRR.Header().Get("Content-Disposition")), []byte("attachment")) {
		t.Fatalf("expected report attachment header, got %s", reportDownloadRR.Header().Get("Content-Disposition"))
	}

	codeRR := doHomeworkJSON(t, h, http.MethodGet, "/api/admin/homework/code?id="+submissionID, nil, adminCookie)
	if codeRR.Code != http.StatusOK || !bytes.Contains(codeRR.Body.Bytes(), []byte(`"nbformat":4`)) {
		t.Fatalf("expected admin notebook, got code=%d body=%q", codeRR.Code, codeRR.Body.String())
	}

	archiveRR := doHomeworkJSON(t, h, http.MethodGet, "/api/admin/homework/archive?id="+submissionID, nil, adminCookie)
	if archiveRR.Code != http.StatusOK {
		t.Fatalf("expected archive 200, got %d body=%s", archiveRR.Code, archiveRR.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(archiveRR.Body.Bytes()), int64(archiveRR.Body.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("expected 2 archive files, got %d", len(zr.File))
	}
	gotNames := []string{zr.File[0].Name, zr.File[1].Name}
	if !containsString(gotNames, "老师版.pdf") || !containsString(gotNames, "实验记录.ipynb") {
		t.Fatalf("unexpected archive names: %+v", gotNames)
	}

	secondBody := []byte(`{"name":"王五","student_no":"2026888","class_name":"2班","course":"course-a","assignment_id":"task-2"}`)
	secondCreateRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", secondBody)
	if secondCreateRR.Code != http.StatusOK {
		t.Fatalf("expected second create 200, got %d body=%s", secondCreateRR.Code, secondCreateRR.Body.String())
	}
	secondCookie := homeworkCookieFromResponse(t, secondCreateRR)
	if rr := doHomeworkUpload(t, h, "/api/homework/upload", map[string]string{"slot": "report"}, "第二份报告.pdf", []byte("%PDF-1.4\nhello-2"), secondCookie); rr.Code != http.StatusOK {
		t.Fatalf("expected second report upload 200, got %d", rr.Code)
	}

	bulkArchiveRR := doHomeworkJSON(t, h, http.MethodGet, "/api/admin/homework/archive-all?course=course-a&assignment_id=task-2", nil, adminCookie)
	if bulkArchiveRR.Code != http.StatusOK {
		t.Fatalf("expected bulk archive 200, got %d body=%s", bulkArchiveRR.Code, bulkArchiveRR.Body.String())
	}
	bulkZR, err := zip.NewReader(bytes.NewReader(bulkArchiveRR.Body.Bytes()), int64(bulkArchiveRR.Body.Len()))
	if err != nil {
		t.Fatalf("bulk zip.NewReader: %v", err)
	}
	bulkNames := make([]string, 0, len(bulkZR.File))
	for _, file := range bulkZR.File {
		bulkNames = append(bulkNames, file.Name)
	}
	if len(bulkNames) != 3 {
		t.Fatalf("expected 3 bulk archive files, got %d names=%+v", len(bulkNames), bulkNames)
	}
	if !containsString(bulkNames, "2026999_李四/report__老师版.pdf") || !containsString(bulkNames, "2026999_李四/notebook__实验记录.ipynb") || !containsString(bulkNames, "2026888_王五/report__第二份报告.pdf") {
		t.Fatalf("unexpected bulk archive names: %+v", bulkNames)
	}

	bulkArchiveUnauthorizedRR := doHomeworkJSON(t, h, http.MethodGet, "/api/admin/homework/archive-all?course=course-a&assignment_id=task-2", nil)
	if bulkArchiveUnauthorizedRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected bulk archive 401 without admin, got %d", bulkArchiveUnauthorizedRR.Code)
	}

	emptyBulkArchiveRR := doHomeworkJSON(t, h, http.MethodGet, "/api/admin/homework/archive-all?course=course-a&assignment_id=task-empty", nil, adminCookie)
	if emptyBulkArchiveRR.Code != http.StatusNotFound {
		t.Fatalf("expected empty bulk archive 404, got %d body=%s", emptyBulkArchiveRR.Code, emptyBulkArchiveRR.Body.String())
	}

	deleteFileRR := doHomeworkJSON(t, h, http.MethodPost, "/api/admin/homework/assignments/delete-file", []byte(`{"course":"course-a","assignment_id":"task-3","file":"data.npy"}`), adminCookie)
	if deleteFileRR.Code != http.StatusOK {
		t.Fatalf("expected delete assignment file 200, got %d body=%s", deleteFileRR.Code, deleteFileRR.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.pptDir(), homeworkAssignmentsFolder, "course-a", "task-3", "data.npy")); !os.IsNotExist(err) {
		t.Fatalf("expected assignment file removed, stat err=%v", err)
	}

	deleteAssignmentRR := doHomeworkJSON(t, h, http.MethodPost, "/api/admin/homework/assignments/delete", []byte(`{"course":"course-a","assignment_id":"task-3"}`), adminCookie)
	if deleteAssignmentRR.Code != http.StatusOK {
		t.Fatalf("expected delete assignment 200, got %d body=%s", deleteAssignmentRR.Code, deleteAssignmentRR.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.pptDir(), homeworkAssignmentsFolder, "course-a", "task-3")); !os.IsNotExist(err) {
		t.Fatalf("expected assignment removed, stat err=%v", err)
	}

	publicGuessRR := doHomeworkJSON(t, h, http.MethodGet, "/uploads/homework/course-a/task-2/2026999/"+submissionID+"/report.pdf", nil)
	if publicGuessRR.Code != http.StatusNotFound {
		t.Fatalf("expected homework file hidden from public uploads, got %d", publicGuessRR.Code)
	}
}

func TestHomeworkAssignmentFilesStayOutOfMaterialsRoutes(t *testing.T) {
	s := newHomeworkTestServer(t)
	h := s.Routes()

	materialsRR := doHomeworkJSON(t, h, http.MethodGet, "/api/materials", nil)
	if materialsRR.Code != http.StatusOK {
		t.Fatalf("expected materials 200, got %d", materialsRR.Code)
	}
	if bytes.Contains(materialsRR.Body.Bytes(), []byte(homeworkAssignmentsFolder)) {
		t.Fatalf("homework assignment subtree should stay out of materials list: %s", materialsRR.Body.String())
	}

	pptRR := doHomeworkJSON(t, h, http.MethodGet, "/ppt/_homework/course-a/task-1.pdf", nil)
	if pptRR.Code != http.StatusNotFound {
		t.Fatalf("expected direct student ppt access blocked, got %d", pptRR.Code)
	}
	pptBundleRR := doHomeworkJSON(t, h, http.MethodGet, "/ppt/_homework/course-a/task-2/task-2.pdf", nil)
	if pptBundleRR.Code != http.StatusNotFound {
		t.Fatalf("expected direct student ppt bundle access blocked, got %d", pptBundleRR.Code)
	}

	downloadRR := doHomeworkJSON(t, h, http.MethodGet, "/materials-files/_homework/course-a/task-1.pdf", nil)
	if downloadRR.Code != http.StatusNotFound {
		t.Fatalf("expected direct student material download blocked, got %d", downloadRR.Code)
	}
	bundleDownloadRR := doHomeworkJSON(t, h, http.MethodGet, "/materials-files/_homework/course-a/task-2/task-2.pdf", nil)
	if bundleDownloadRR.Code != http.StatusNotFound {
		t.Fatalf("expected direct student material bundle download blocked, got %d", bundleDownloadRR.Code)
	}
}

func TestHomeworkSubmitRouteServesPage(t *testing.T) {
	s := newHomeworkTestServer(t)
	h := s.Routes()
	req := httptest.NewRequest(http.MethodGet, "/submit", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("作业提交")) {
		t.Fatalf("expected homework submit page body, got %q", rr.Body.String())
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
