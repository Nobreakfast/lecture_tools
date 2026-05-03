package app

import (
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

	"course-assistant/internal/domain"
)

func newHomeworkTestServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	metadataDir := filepath.Join(root, "metadata")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.MkdirAll(metadataDir, 0o755); err != nil {
		t.Fatalf("mkdir metadata dir: %v", err)
	}
	s := &Server{
		cfg:        Config{DataDir: dataDir, MetadataDir: metadataDir},
		store:      &memStore{courses: []domain.Course{{ID: 1, TeacherID: "admin", Name: "course-a", Slug: "course-a", InternalName: "course-a"}, {ID: 2, TeacherID: "admin", Name: "course-b", Slug: "course-b", InternalName: "course-b"}}},
		authTokens: map[string]authSession{"test-admin": {TeacherID: "admin", Role: domain.RoleAdmin, Expiry: time.Now().Add(time.Hour)}},
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

func doHomeworkQAUpload(t *testing.T, h http.Handler, target string, fields map[string]string, images map[string][]byte, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for key, value := range fields {
		if err := mw.WriteField(key, value); err != nil {
			t.Fatalf("WriteField(%s): %v", key, err)
		}
	}
	for filename, data := range images {
		fw, err := mw.CreateFormFile("images", filename)
		if err != nil {
			t.Fatalf("CreateFormFile(%s): %v", filename, err)
		}
		if _, err := fw.Write(data); err != nil {
			t.Fatalf("Write QA image data(%s): %v", filename, err)
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

	// assignments and file download require a session
	noSessionRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/assignments", nil)
	if noSessionRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected assignments 401 without session, got %d", noSessionRR.Code)
	}

	body := []byte(`{"name":"张三","student_no":"2026001","class_name":"1班","secret_key":"abc123","course":"course-a","assignment_id":"task-1"}`)
	createRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", body)
	if createRR.Code != http.StatusOK {
		t.Fatalf("expected create 200, got %d body=%s", createRR.Code, createRR.Body.String())
	}
	cookie := homeworkCookieFromResponse(t, createRR)
	createdID := decodeSubmissionResponse(t, createRR)["submission"].(map[string]any)["id"].(string)

	assignmentsRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/assignments", nil, cookie)
	if assignmentsRR.Code != http.StatusOK {
		t.Fatalf("expected assignments 200, got %d body=%s", assignmentsRR.Code, assignmentsRR.Body.String())
	}
	if !bytes.Contains(assignmentsRR.Body.Bytes(), []byte(`"assignment_id":"task-1"`)) || !bytes.Contains(assignmentsRR.Body.Bytes(), []byte(`/api/homework/assignment-file?assignment_id=task-1`)) || !bytes.Contains(assignmentsRR.Body.Bytes(), []byte(`"name":"task-1.pdf"`)) {
		t.Fatalf("unexpected assignments response: %s", assignmentsRR.Body.String())
	}

	downloadRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/assignment-file?course=course-a&assignment_id=task-1&file=task-1.pdf", nil, cookie)
	if downloadRR.Code != http.StatusOK || !bytes.HasPrefix(downloadRR.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("expected assignment file download, got code=%d body=%q", downloadRR.Code, downloadRR.Body.String())
	}
	if !bytes.Contains([]byte(downloadRR.Header().Get("Content-Disposition")), []byte("attachment")) {
		t.Fatalf("expected attachment header for assignment file, got %s", downloadRR.Header().Get("Content-Disposition"))
	}

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

	submissionDir := filepath.Join(s.cfg.MetadataDir, "course-a", "assignment", "task-1", "submissions", "2026001")
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

func TestHomeworkSessionRequiresMatchingSecretKey(t *testing.T) {
	s := newHomeworkTestServer(t)
	h := s.Routes()

	body := []byte(`{"name":"张三","student_no":"2026001","class_name":"1班","secret_key":"mykey","course":"course-a","assignment_id":"task-1"}`)
	createRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", body)
	if createRR.Code != http.StatusOK {
		t.Fatalf("expected create 200, got %d body=%s", createRR.Code, createRR.Body.String())
	}

	wrongKeyBody := []byte(`{"name":"张三","student_no":"2026001","class_name":"1班","secret_key":"wrong","course":"course-a","assignment_id":"task-1"}`)
	wrongKeyRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", wrongKeyBody)
	if wrongKeyRR.Code != http.StatusForbidden {
		t.Fatalf("expected wrong key 403, got %d body=%s", wrongKeyRR.Code, wrongKeyRR.Body.String())
	}

	correctKeyBody := []byte(`{"name":"李四","student_no":"2026001","class_name":"2班","secret_key":"mykey","course":"course-a","assignment_id":"task-1"}`)
	correctKeyRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", correctKeyBody)
	if correctKeyRR.Code != http.StatusOK {
		t.Fatalf("expected correct key resume 200, got %d body=%s", correctKeyRR.Code, correctKeyRR.Body.String())
	}
}

func TestLockedHomeworkKeepsStudentReadOnly(t *testing.T) {
	s := newHomeworkTestServer(t)
	h := s.Routes()

	body := []byte(`{"name":"张三","student_no":"2026001","class_name":"1班","secret_key":"mykey","course":"course-a","assignment_id":"task-1"}`)
	createRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", body)
	if createRR.Code != http.StatusOK {
		t.Fatalf("expected create 200, got %d body=%s", createRR.Code, createRR.Body.String())
	}
	cookie := homeworkCookieFromResponse(t, createRR)
	uploadRR := doHomeworkUpload(t, h, "/api/homework/upload", map[string]string{"slot": "report"}, "report.pdf", []byte("%PDF-1.4\nreport"), cookie)
	if uploadRR.Code != http.StatusOK {
		t.Fatalf("expected initial upload 200, got %d body=%s", uploadRR.Code, uploadRR.Body.String())
	}

	adminCookie := &http.Cookie{Name: "auth_token", Value: "test-admin"}
	lockRR := doHomeworkJSON(t, h, http.MethodPost, "/api/teacher/courses/homework/assignments/lock?course_id=1", []byte(`{"assignment_id":"task-1","locked":true}`), adminCookie)
	if lockRR.Code != http.StatusOK {
		t.Fatalf("expected lock 200, got %d body=%s", lockRR.Code, lockRR.Body.String())
	}

	getRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/submission", nil, cookie)
	if getRR.Code != http.StatusOK {
		t.Fatalf("expected read-only submission 200, got %d body=%s", getRR.Code, getRR.Body.String())
	}
	submission := decodeSubmissionResponse(t, getRR)["submission"].(map[string]any)
	if submission["locked"] != true {
		t.Fatalf("expected locked payload, got %+v", submission)
	}
	downloadRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/download?slot=report", nil, cookie)
	if downloadRR.Code != http.StatusOK {
		t.Fatalf("expected locked download 200, got %d body=%s", downloadRR.Code, downloadRR.Body.String())
	}
	resumeRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", body)
	if resumeRR.Code != http.StatusOK {
		t.Fatalf("expected locked existing resume 200, got %d body=%s", resumeRR.Code, resumeRR.Body.String())
	}
	cookie = homeworkCookieFromResponse(t, resumeRR)

	replaceRR := doHomeworkUpload(t, h, "/api/homework/upload", map[string]string{"slot": "report"}, "new.pdf", []byte("%PDF-1.4\nnew"), cookie)
	if replaceRR.Code != http.StatusConflict {
		t.Fatalf("expected locked upload 409, got %d body=%s", replaceRR.Code, replaceRR.Body.String())
	}
	deleteRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/delete", []byte(`{"slot":"report"}`), cookie)
	if deleteRR.Code != http.StatusConflict {
		t.Fatalf("expected locked delete 409, got %d body=%s", deleteRR.Code, deleteRR.Body.String())
	}
	newStudentRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", []byte(`{"name":"李四","student_no":"2026002","class_name":"1班","secret_key":"k","course":"course-a","assignment_id":"task-1"}`))
	if newStudentRR.Code != http.StatusConflict {
		t.Fatalf("expected locked new submission 409, got %d body=%s", newStudentRR.Code, newStudentRR.Body.String())
	}

	unlockRR := doHomeworkJSON(t, h, http.MethodPost, "/api/teacher/courses/homework/assignments/lock?course_id=1", []byte(`{"assignment_id":"task-1","locked":false}`), adminCookie)
	if unlockRR.Code != http.StatusOK {
		t.Fatalf("expected unlock 200, got %d body=%s", unlockRR.Code, unlockRR.Body.String())
	}
	replaceAfterUnlockRR := doHomeworkUpload(t, h, "/api/homework/upload", map[string]string{"slot": "report"}, "new.pdf", []byte("%PDF-1.4\nnew"), cookie)
	if replaceAfterUnlockRR.Code != http.StatusOK {
		t.Fatalf("expected unlocked upload 200, got %d body=%s", replaceAfterUnlockRR.Code, replaceAfterUnlockRR.Body.String())
	}
}

// TestHomeworkAdminAssignmentAndSubmissionRoutes_DEPRECATED was removed as
// part of the multi-tenant migration. The legacy /api/admin/homework/*
// endpoints now return 410 Gone; all equivalent teacher functionality is
// covered by /api/teacher/courses/homework/*. A minimal regression check for
// the 410 behaviour is sufficient.
func TestLegacyAdminHomeworkRoutesAreGone(t *testing.T) {
	s := newHomeworkTestServer(t)
	h := s.Routes()
	adminCookie := &http.Cookie{Name: "auth_token", Value: "test-admin"}
	for _, path := range []string{
		"/api/admin/homework/assignments?course=course-a",
		"/api/admin/homework/submissions?course=course-a",
	} {
		rr := doHomeworkJSON(t, h, http.MethodGet, path, nil, adminCookie)
		if rr.Code != http.StatusGone {
			t.Fatalf("%s expected 410, got %d", path, rr.Code)
		}
	}
	_ = s // keep
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

func TestTeacherCanDownloadHiddenHomeworkAssignmentFile(t *testing.T) {
	s := newHomeworkTestServer(t)
	h := s.Routes()
	st := s.store.(*memStore)
	st.courses[0].TeacherID = "teacher-a"
	s.authTokens["teacher-a-token"] = authSession{TeacherID: "teacher-a", Role: domain.RoleTeacher, Expiry: time.Now().Add(time.Hour)}
	teacherCookie := &http.Cookie{Name: "auth_token", Value: "teacher-a-token"}

	hideRR := doHomeworkJSON(t, h, http.MethodPost, "/api/teacher/courses/homework/assignments/visibility?course_id=1", []byte(`{"assignment_id":"task-1","hidden":true}`), teacherCookie)
	if hideRR.Code != http.StatusOK {
		t.Fatalf("expected hide 200, got %d body=%s", hideRR.Code, hideRR.Body.String())
	}

	publicRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/assignment-file?course_id=1&assignment_id=task-1&file=task-1.pdf", nil)
	if publicRR.Code != http.StatusForbidden {
		t.Fatalf("expected public hidden file 403, got %d body=%s", publicRR.Code, publicRR.Body.String())
	}
	teacherRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/assignment-file?course_id=1&assignment_id=task-1&file=task-1.pdf", nil, teacherCookie)
	if teacherRR.Code != http.StatusOK || !bytes.HasPrefix(teacherRR.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("expected teacher hidden file download 200, got %d body=%q", teacherRR.Code, teacherRR.Body.String())
	}
}

func TestHomeworkOthersUploadListRenameDeleteFlow(t *testing.T) {
	s := newHomeworkTestServer(t)
	h := s.Routes()

	body := []byte(`{"name":"王五","student_no":"2026099","class_name":"3班","secret_key":"mykey","course":"course-a","assignment_id":"task-1"}`)
	createRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", body)
	if createRR.Code != http.StatusOK {
		t.Fatalf("expected create 200, got %d body=%s", createRR.Code, createRR.Body.String())
	}
	cookie := homeworkCookieFromResponse(t, createRR)

	// Upload file to others
	fileData := []byte("hello world content")
	uploadRR := doHomeworkUpload(t, h, "/api/homework/others/upload", nil, "notes.txt", fileData, cookie)
	if uploadRR.Code != http.StatusOK {
		t.Fatalf("expected others upload 200, got %d body=%s", uploadRR.Code, uploadRR.Body.String())
	}
	var uploadResp map[string]any
	if err := json.Unmarshal(uploadRR.Body.Bytes(), &uploadResp); err != nil {
		t.Fatalf("unmarshal upload response: %v", err)
	}
	if uploadResp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", uploadResp)
	}

	// Upload second file
	upload2RR := doHomeworkUpload(t, h, "/api/homework/others/upload", nil, "data.csv", []byte("a,b,c\n1,2,3"), cookie)
	if upload2RR.Code != http.StatusOK {
		t.Fatalf("expected second upload 200, got %d body=%s", upload2RR.Code, upload2RR.Body.String())
	}

	// List others
	listRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/others/list", nil, cookie)
	if listRR.Code != http.StatusOK {
		t.Fatalf("expected list 200, got %d body=%s", listRR.Code, listRR.Body.String())
	}
	var listResp map[string]any
	if err := json.Unmarshal(listRR.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	items := listResp["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d: %s", len(items), listRR.Body.String())
	}
	names := []string{}
	for _, item := range items {
		names = append(names, item.(map[string]any)["name"].(string))
	}
	sort.Strings(names)
	if names[0] != "data.csv" || names[1] != "notes.txt" {
		t.Fatalf("unexpected file names: %v", names)
	}

	// Download
	downloadRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/others/download?file=notes.txt", nil, cookie)
	if downloadRR.Code != http.StatusOK {
		t.Fatalf("expected download 200, got %d", downloadRR.Code)
	}
	if string(downloadRR.Body.Bytes()) != string(fileData) {
		t.Fatalf("unexpected download content: %q", downloadRR.Body.String())
	}

	// Rename
	renameBody := []byte(`{"old_name":"notes.txt","new_name":"readme.txt"}`)
	renameRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/others/rename", renameBody, cookie)
	if renameRR.Code != http.StatusOK {
		t.Fatalf("expected rename 200, got %d body=%s", renameRR.Code, renameRR.Body.String())
	}

	// Verify rename: old name gone, new name exists
	download404RR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/others/download?file=notes.txt", nil, cookie)
	if download404RR.Code != http.StatusNotFound {
		t.Fatalf("expected old name 404, got %d", download404RR.Code)
	}
	downloadNewRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/others/download?file=readme.txt", nil, cookie)
	if downloadNewRR.Code != http.StatusOK {
		t.Fatalf("expected renamed file download 200, got %d", downloadNewRR.Code)
	}

	// Delete
	deleteBody := []byte(`{"file":"data.csv"}`)
	deleteRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/others/delete", deleteBody, cookie)
	if deleteRR.Code != http.StatusOK {
		t.Fatalf("expected delete 200, got %d body=%s", deleteRR.Code, deleteRR.Body.String())
	}

	// List again — should have 1 item
	list2RR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/others/list", nil, cookie)
	if list2RR.Code != http.StatusOK {
		t.Fatalf("expected list 200, got %d", list2RR.Code)
	}
	var list2Resp map[string]any
	if err := json.Unmarshal(list2RR.Body.Bytes(), &list2Resp); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	items2 := list2Resp["items"].([]any)
	if len(items2) != 1 {
		t.Fatalf("expected 1 item after delete, got %d", len(items2))
	}
	if items2[0].(map[string]any)["name"].(string) != "readme.txt" {
		t.Fatalf("unexpected remaining file: %v", items2[0])
	}

	// Verify filesystem: others/ directory contains readme.txt
	submissionDir := filepath.Join(s.cfg.MetadataDir, "course-a", "assignment", "task-1", "submissions", "2026099")
	othersDir := filepath.Join(submissionDir, "others")
	entries, err := os.ReadDir(othersDir)
	if err != nil {
		t.Fatalf("ReadDir others: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "readme.txt" {
		var entryNames []string
		for _, e := range entries {
			entryNames = append(entryNames, e.Name())
		}
		t.Fatalf("expected [readme.txt] in others/, got %v", entryNames)
	}

	// Delete nonexistent file should 404
	delete404RR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/others/delete", []byte(`{"file":"nope.bin"}`), cookie)
	if delete404RR.Code != http.StatusNotFound {
		t.Fatalf("expected delete 404, got %d", delete404RR.Code)
	}

	// Rename to existing name should conflict
	upload3RR := doHomeworkUpload(t, h, "/api/homework/others/upload", nil, "conflict.txt", []byte("conflict"), cookie)
	if upload3RR.Code != http.StatusOK {
		t.Fatalf("expected upload 200, got %d", upload3RR.Code)
	}
	conflictBody := []byte(`{"old_name":"readme.txt","new_name":"conflict.txt"}`)
	conflictRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/others/rename", conflictBody, cookie)
	if conflictRR.Code != http.StatusConflict {
		t.Fatalf("expected rename conflict 409, got %d", conflictRR.Code)
	}

	// Verify others appear in submission payload
	subRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/submission", nil, cookie)
	if subRR.Code != http.StatusOK {
		t.Fatalf("expected submission 200, got %d", subRR.Code)
	}
	subResp := decodeSubmissionResponse(t, subRR)
	submission := subResp["submission"].(map[string]any)
	others := submission["others"].([]any)
	if len(others) != 2 {
		t.Fatalf("expected 2 others in submission payload, got %d", len(others))
	}
}

func TestHomeworkOthersEmptyUploadRejected(t *testing.T) {
	s := newHomeworkTestServer(t)
	h := s.Routes()

	body := []byte(`{"name":"赵六","student_no":"2026100","class_name":"4班","secret_key":"key2","course":"course-a","assignment_id":"task-1"}`)
	createRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", body)
	if createRR.Code != http.StatusOK {
		t.Fatalf("expected create 200, got %d", createRR.Code)
	}
	cookie := homeworkCookieFromResponse(t, createRR)

	emptyRR := doHomeworkUpload(t, h, "/api/homework/others/upload", nil, "empty.txt", []byte{}, cookie)
	if emptyRR.Code != http.StatusBadRequest {
		t.Fatalf("expected empty upload 400, got %d body=%s", emptyRR.Code, emptyRR.Body.String())
	}
}

func TestHomeworkOthersInvalidFilename(t *testing.T) {
	s := newHomeworkTestServer(t)
	h := s.Routes()

	body := []byte(`{"name":"钱七","student_no":"2026101","class_name":"5班","secret_key":"key3","course":"course-a","assignment_id":"task-1"}`)
	createRR := doHomeworkJSON(t, h, http.MethodPost, "/api/homework/session", body)
	if createRR.Code != http.StatusOK {
		t.Fatalf("expected create 200, got %d", createRR.Code)
	}
	cookie := homeworkCookieFromResponse(t, createRR)

	dotRR := doHomeworkUpload(t, h, "/api/homework/others/upload", nil, ".hidden", []byte("data"), cookie)
	if dotRR.Code != http.StatusBadRequest {
		t.Fatalf("expected dot-file upload 400, got %d", dotRR.Code)
	}
}

func TestHomeworkQAWithImagesLifecycle(t *testing.T) {
	s := newHomeworkTestServer(t)
	h := s.Routes()
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3}

	createRR := doHomeworkQAUpload(t, h, "/api/homework/qa", map[string]string{
		"course_id":     "1",
		"assignment_id": "task-1",
		"question":      "可以手写拍照吗？",
	}, map[string][]byte{"question.png": png})
	if createRR.Code != http.StatusOK {
		t.Fatalf("expected QA create 200, got %d body=%s", createRR.Code, createRR.Body.String())
	}

	publicRR := doHomeworkJSON(t, h, http.MethodGet, "/api/homework/qa?course_id=1&assignment_id=task-1", nil)
	if publicRR.Code != http.StatusOK {
		t.Fatalf("expected public QA list 200, got %d", publicRR.Code)
	}
	var publicResp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(publicRR.Body.Bytes(), &publicResp); err != nil {
		t.Fatalf("unmarshal public QA response: %v", err)
	}
	if len(publicResp.Items) != 0 {
		t.Fatalf("unanswered QA should not be public: %+v", publicResp.Items)
	}

	adminCookie := &http.Cookie{Name: "auth_token", Value: "test-admin"}
	teacherRR := doHomeworkJSON(t, h, http.MethodGet, "/api/teacher/courses/homework/qa?course_id=1&assignment_id=task-1", nil, adminCookie)
	if teacherRR.Code != http.StatusOK {
		t.Fatalf("expected teacher QA list 200, got %d body=%s", teacherRR.Code, teacherRR.Body.String())
	}
	var teacherResp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(teacherRR.Body.Bytes(), &teacherResp); err != nil {
		t.Fatalf("unmarshal teacher QA response: %v", err)
	}
	if len(teacherResp.Items) != 1 {
		t.Fatalf("teacher should see unanswered QA, got %+v", teacherResp.Items)
	}
	qaID, _ := teacherResp.Items[0]["id"].(string)
	if qaID == "" {
		t.Fatalf("missing QA id: %+v", teacherResp.Items[0])
	}

	answerRR := doHomeworkQAUpload(t, h, "/api/teacher/courses/homework/qa/answer?course_id=1", map[string]string{
		"id":     qaID,
		"answer": "可以，图片要清晰。",
	}, map[string][]byte{"answer.png": png}, adminCookie)
	if answerRR.Code != http.StatusOK {
		t.Fatalf("expected answer 200, got %d body=%s", answerRR.Code, answerRR.Body.String())
	}
	pinRR := doHomeworkJSON(t, h, http.MethodPost, "/api/teacher/courses/homework/qa/pin?course_id=1", []byte(`{"id":"`+qaID+`","value":true}`), adminCookie)
	if pinRR.Code != http.StatusOK {
		t.Fatalf("expected pin 200, got %d body=%s", pinRR.Code, pinRR.Body.String())
	}

	publicRR = doHomeworkJSON(t, h, http.MethodGet, "/api/homework/qa?course_id=1&assignment_id=task-1", nil)
	if err := json.Unmarshal(publicRR.Body.Bytes(), &publicResp); err != nil {
		t.Fatalf("unmarshal public answered QA response: %v", err)
	}
	if len(publicResp.Items) != 1 || publicResp.Items[0]["answer"] != "可以，图片要清晰。" || publicResp.Items[0]["pinned"] != true {
		t.Fatalf("unexpected public answered QA: %+v", publicResp.Items)
	}
	if len(publicResp.Items[0]["question_images"].([]any)) != 1 || len(publicResp.Items[0]["answer_images"].([]any)) != 1 {
		t.Fatalf("expected question and answer images: %+v", publicResp.Items[0])
	}

	hideRR := doHomeworkJSON(t, h, http.MethodPost, "/api/teacher/courses/homework/qa/hidden?course_id=1", []byte(`{"id":"`+qaID+`","value":true}`), adminCookie)
	if hideRR.Code != http.StatusOK {
		t.Fatalf("expected hide 200, got %d body=%s", hideRR.Code, hideRR.Body.String())
	}
	publicRR = doHomeworkJSON(t, h, http.MethodGet, "/api/homework/qa?course_id=1&assignment_id=task-1", nil)
	if err := json.Unmarshal(publicRR.Body.Bytes(), &publicResp); err != nil {
		t.Fatalf("unmarshal public hidden QA response: %v", err)
	}
	if len(publicResp.Items) != 0 {
		t.Fatalf("hidden QA should not be public: %+v", publicResp.Items)
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
