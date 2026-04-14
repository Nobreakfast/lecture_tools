package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"course-assistant/internal/domain"
	"course-assistant/internal/store"
)

const (
	homeworkCookieName              = "homework_token"
	homeworkAssignmentsFolder       = "_homework"
	homeworkAssignmentPDFMaxSize    = 500 << 20
	homeworkAssignmentVisibilityKey = "homework_assignment_visibility"
)

var errHomeworkIdentityMismatch = errors.New("已存在同学号的作业记录，请使用原姓名和班级继续")

func (s *Server) apiHomeworkCourses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	courses, err := s.listHomeworkCourses()
	if err != nil {
		writeJSON(w, map[string]any{"items": []map[string]any{}})
		return
	}
	items := make([]map[string]any, 0, len(courses))
	for _, course := range courses {
		items = append(items, map[string]any{"course": course})
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) apiHomeworkAssignmentIDs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	course, err := validateHomeworkCourse(r.URL.Query().Get("course"))
	if err != nil {
		http.Error(w, "课程无效", http.StatusBadRequest)
		return
	}
	assignments, err := s.listHomeworkAssignments(course)
	if err != nil {
		writeJSON(w, map[string]any{"items": []string{}})
		return
	}
	visibility := s.loadHomeworkAssignmentVisibility()
	visible := make([]string, 0, len(assignments))
	for _, id := range assignments {
		if !homeworkAssignmentHidden(visibility, course, id) {
			visible = append(visible, id)
		}
	}
	writeJSON(w, map[string]any{"items": visible})
}

func (s *Server) apiHomeworkAssignmentPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	course, err := validateHomeworkCourse(r.URL.Query().Get("course"))
	if err != nil {
		writeJSON(w, map[string]any{"items": []map[string]any{}})
		return
	}
	assignmentID, err := validateHomeworkAssignmentID(r.URL.Query().Get("assignment_id"))
	if err != nil {
		writeJSON(w, map[string]any{"items": []map[string]any{}})
		return
	}
	if !s.homeworkAssignmentExists(course, assignmentID) {
		writeJSON(w, map[string]any{"items": []map[string]any{}})
		return
	}
	visibility := s.loadHomeworkAssignmentVisibility()
	if homeworkAssignmentHidden(visibility, course, assignmentID) {
		writeJSON(w, map[string]any{"items": []map[string]any{}})
		return
	}
	items := []map[string]any{s.homeworkAssignmentPayload(course, assignmentID, false)}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) apiHomeworkAssignments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	submission, err := s.requireHomeworkStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	course := submission.Course
	assignmentID := submission.AssignmentID
	if !s.homeworkAssignmentExists(course, assignmentID) {
		writeJSON(w, map[string]any{"items": []map[string]any{}})
		return
	}
	items := []map[string]any{s.homeworkAssignmentPayload(course, assignmentID, false)}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) apiHomeworkAssignmentFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	course, _, fileName, fp, err := s.resolveHomeworkAssignmentFileRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reqAssignment := r.URL.Query().Get("assignment_id")
	visibility := s.loadHomeworkAssignmentVisibility()
	if homeworkAssignmentHidden(visibility, course, reqAssignment) {
		http.Error(w, "该作业不可访问", http.StatusForbidden)
		return
	}
	if _, err := os.Stat(fp); err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName)))
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeHomeworkMetadataFilename(fileName, "download.bin")))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, fp)
}

func (s *Server) apiHomeworkSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name         string `json:"name"`
		StudentNo    string `json:"student_no"`
		ClassName    string `json:"class_name"`
		SecretKey    string `json:"secret_key"`
		Course       string `json:"course"`
		AssignmentID string `json:"assignment_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.StudentNo = strings.TrimSpace(req.StudentNo)
	req.ClassName = strings.TrimSpace(req.ClassName)
	req.SecretKey = strings.TrimSpace(req.SecretKey)
	if req.Name == "" || req.StudentNo == "" || req.ClassName == "" {
		http.Error(w, "信息不完整", http.StatusBadRequest)
		return
	}
	if req.SecretKey == "" {
		http.Error(w, "请设置密钥", http.StatusBadRequest)
		return
	}
	course, err := validateHomeworkCourse(req.Course)
	if err != nil {
		http.Error(w, "课程无效", http.StatusBadRequest)
		return
	}
	assignmentID, err := validateHomeworkAssignmentID(req.AssignmentID)
	if err != nil {
		http.Error(w, "作业编号无效", http.StatusBadRequest)
		return
	}
	if !s.homeworkAssignmentExists(course, assignmentID) {
		http.Error(w, "作业不存在", http.StatusNotFound)
		return
	}
	token := newID() + newID()
	existing, err := s.store.GetHomeworkSubmissionByScope(r.Context(), course, assignmentID, req.StudentNo)
	if err == nil && existing != nil {
		if existing.SecretKey != "" && existing.SecretKey != req.SecretKey {
			http.Error(w, "密钥错误", http.StatusForbidden)
			return
		}
		if err := s.store.UpdateHomeworkSubmissionSession(r.Context(), existing.ID, token, req.Name, req.ClassName, req.SecretKey); err != nil {
			http.Error(w, "恢复作业会话失败", http.StatusInternalServerError)
			return
		}
		s.setHomeworkCookie(w, token)
		fresh, freshErr := s.store.GetHomeworkSubmissionByID(r.Context(), existing.ID)
		if freshErr != nil {
			http.Error(w, "读取作业会话失败", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "submission": s.homeworkSubmissionPayload(fresh, false)})
		return
	}
	if err != nil && !store.IsNotFound(err) {
		http.Error(w, "读取作业会话失败", http.StatusInternalServerError)
		return
	}
	now := time.Now()
	submission := &domain.HomeworkSubmission{
		ID:           newID(),
		SessionToken: token,
		Course:       course,
		AssignmentID: assignmentID,
		Name:         req.Name,
		StudentNo:    req.StudentNo,
		ClassName:    req.ClassName,
		SecretKey:    req.SecretKey,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.CreateHomeworkSubmission(r.Context(), submission); err != nil {
		http.Error(w, "创建作业会话失败", http.StatusInternalServerError)
		return
	}
	s.setHomeworkCookie(w, token)
	writeJSON(w, map[string]any{"ok": true, "submission": s.homeworkSubmissionPayload(submission, false)})
}

func (s *Server) apiHomeworkSubmission(w http.ResponseWriter, r *http.Request) {
	submission, err := s.requireHomeworkStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{"submission": s.homeworkSubmissionPayload(submission, false)})
}

func (s *Server) apiHomeworkUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	submission, err := s.requireEditableHomeworkStudent(r)
	if err != nil {
		http.Error(w, err.Error(), homeworkAuthStatus(err))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, homeworkAssignmentPDFMaxSize)
	if err := r.ParseMultipartForm(homeworkAssignmentPDFMaxSize); err != nil {
		http.Error(w, "文件过大或格式错误", http.StatusBadRequest)
		return
	}
	slot, err := parseHomeworkSlot(r.FormValue("slot"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "未找到上传文件", http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "读取文件失败", http.StatusInternalServerError)
		return
	}
	if slot == domain.HomeworkSlotCode && !strings.EqualFold(filepath.Ext(strings.TrimSpace(header.Filename)), ".ipynb") {
		http.Error(w, "Notebook 文件必须使用 .ipynb 扩展名", http.StatusBadRequest)
		return
	}
	if err := validateHomeworkFile(slot, data); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.homeworkSubmissionDir(submission)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "创建目录失败", http.StatusInternalServerError)
		return
	}
	tmpPath := filepath.Join(dir, fmt.Sprintf(".%s.%d.tmp", homeworkDiskFilename(slot), time.Now().UnixNano()))
	finalPath := filepath.Join(dir, homeworkDiskFilename(slot))
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		http.Error(w, "写入文件失败", http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmpPath)
	if err := s.store.SaveHomeworkFileMetadata(r.Context(), submission.ID, slot, sanitizeHomeworkMetadataFilename(header.Filename, homeworkDiskFilename(slot))); err != nil {
		http.Error(w, "保存文件状态失败", http.StatusInternalServerError)
		return
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		http.Error(w, "写入文件失败", http.StatusInternalServerError)
		return
	}
	if slot == domain.HomeworkSlotCode {
		_ = os.Remove(filepath.Join(dir, "code.zip"))
	}
	updated, err := s.store.GetHomeworkSubmissionByID(r.Context(), submission.ID)
	if err != nil {
		http.Error(w, "读取作业状态失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "submission": s.homeworkSubmissionPayload(updated, false)})
}

func (s *Server) apiHomeworkDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	submission, err := s.requireHomeworkStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	slot, err := parseHomeworkSlot(r.URL.Query().Get("slot"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var originalName, contentType string
	switch slot {
	case domain.HomeworkSlotReport:
		originalName = submission.ReportOriginalName
		contentType = "application/pdf"
	case domain.HomeworkSlotCode:
		originalName = submission.CodeOriginalName
		contentType = "application/x-ipynb+json"
	case domain.HomeworkSlotExtra:
		originalName = submission.ExtraOriginalName
		contentType = "application/zip"
	}
	if strings.TrimSpace(originalName) == "" {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	fp := s.homeworkStoredFilePath(submission, slot)
	if _, err := os.Stat(fp); err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeHomeworkMetadataFilename(originalName, homeworkDiskFilename(slot))))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, fp)
}

func (s *Server) apiHomeworkDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	submission, err := s.requireEditableHomeworkStudent(r)
	if err != nil {
		http.Error(w, err.Error(), homeworkAuthStatus(err))
		return
	}
	var req struct {
		Slot string `json:"slot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	slot, err := parseHomeworkSlot(req.Slot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.DeleteHomeworkFileMetadata(r.Context(), submission.ID, slot); err != nil {
		http.Error(w, "删除文件失败", http.StatusInternalServerError)
		return
	}
	_ = os.Remove(filepath.Join(s.homeworkSubmissionDir(submission), homeworkDiskFilename(slot)))
	updated, err := s.store.GetHomeworkSubmissionByID(r.Context(), submission.ID)
	if err != nil {
		http.Error(w, "读取作业状态失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "submission": s.homeworkSubmissionPayload(updated, false)})
}

func (s *Server) apiAdminHomeworkAssignments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	courses, err := s.listHomeworkCourses()
	if err != nil {
		writeJSON(w, map[string]any{"courses": []string{}, "items": []map[string]any{}})
		return
	}
	filterCourse := strings.TrimSpace(r.URL.Query().Get("course"))
	items := make([]map[string]any, 0)
	for _, course := range courses {
		if filterCourse != "" && course != filterCourse {
			continue
		}
		assignments, err := s.listHomeworkAssignments(course)
		if err != nil {
			continue
		}
		for _, assignmentID := range assignments {
			items = append(items, s.homeworkAssignmentPayload(course, assignmentID, true))
		}
	}
	writeJSON(w, map[string]any{"courses": courses, "items": items})
}

func (s *Server) apiAdminHomeworkAssignmentUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, homeworkAssignmentPDFMaxSize)
	if err := r.ParseMultipartForm(homeworkAssignmentPDFMaxSize); err != nil {
		http.Error(w, "文件过大或格式错误", http.StatusBadRequest)
		return
	}
	course, err := validateHomeworkCourse(r.FormValue("course"))
	if err != nil {
		http.Error(w, "课程无效", http.StatusBadRequest)
		return
	}
	headers, err := collectUploadHeaders(r.MultipartForm)
	if err != nil {
		http.Error(w, "未找到上传文件", http.StatusBadRequest)
		return
	}
	assignmentID, err := validateHomeworkAssignmentID(r.FormValue("assignment_id"))
	if err != nil {
		http.Error(w, "作业编号无效", http.StatusBadRequest)
		return
	}
	dir := s.homeworkAssignmentDir(course, assignmentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "创建目录失败", http.StatusInternalServerError)
		return
	}
	if err := s.migrateLegacyHomeworkAssignmentToBundle(course, assignmentID); err != nil {
		http.Error(w, "迁移旧作业资源失败", http.StatusInternalServerError)
		return
	}
	uploaded := make([]map[string]any, 0, len(headers))
	failed := make([]map[string]any, 0)
	for _, header := range headers {
		result, failure := s.saveUploadedHomeworkAssignment(course, assignmentID, dir, header)
		if failure != nil {
			failed = append(failed, failure)
			continue
		}
		uploaded = append(uploaded, result)
	}
	resp := map[string]any{"ok": len(failed) == 0, "uploaded": uploaded, "failed": failed}
	if len(uploaded) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, resp)
		return
	}
	writeJSONStatus(w, http.StatusOK, resp)
}

func (s *Server) apiAdminHomeworkAssignmentDeleteFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Course       string `json:"course"`
		AssignmentID string `json:"assignment_id"`
		File         string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	course, err := validateHomeworkCourse(req.Course)
	if err != nil {
		http.Error(w, "课程无效", http.StatusBadRequest)
		return
	}
	assignmentID, err := validateHomeworkAssignmentID(req.AssignmentID)
	if err != nil {
		http.Error(w, "作业编号无效", http.StatusBadRequest)
		return
	}
	fileName, err := normalizeHomeworkResourceFilename(req.File)
	if err != nil {
		http.Error(w, "文件名无效", http.StatusBadRequest)
		return
	}
	deleted := false
	bundlePath := filepath.Join(s.homeworkAssignmentDir(course, assignmentID), fileName)
	if err := os.Remove(bundlePath); err == nil {
		deleted = true
	} else if !os.IsNotExist(err) {
		http.Error(w, "删除失败", http.StatusInternalServerError)
		return
	}
	legacyName := assignmentID + ".pdf"
	if fileName == legacyName {
		if err := os.Remove(s.homeworkLegacyAssignmentPath(course, assignmentID)); err == nil {
			deleted = true
		} else if !os.IsNotExist(err) {
			http.Error(w, "删除失败", http.StatusInternalServerError)
			return
		}
	}
	if !deleted {
		http.Error(w, "文件不存在", http.StatusNotFound)
		return
	}
	_ = s.removeHomeworkAssignmentDirIfEmpty(course, assignmentID)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAdminHomeworkAssignmentDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Course       string `json:"course"`
		AssignmentID string `json:"assignment_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	course, err := validateHomeworkCourse(req.Course)
	if err != nil {
		http.Error(w, "课程无效", http.StatusBadRequest)
		return
	}
	assignmentID, err := validateHomeworkAssignmentID(req.AssignmentID)
	if err != nil {
		http.Error(w, "作业编号无效", http.StatusBadRequest)
		return
	}
	deleted := false
	if err := os.RemoveAll(s.homeworkAssignmentDir(course, assignmentID)); err == nil {
		deleted = true
	} else if !os.IsNotExist(err) {
		http.Error(w, "删除失败", http.StatusInternalServerError)
		return
	}
	if err := os.Remove(s.homeworkLegacyAssignmentPath(course, assignmentID)); err == nil {
		deleted = true
	} else if !os.IsNotExist(err) {
		http.Error(w, "删除失败", http.StatusInternalServerError)
		return
	}
	if !deleted {
		http.Error(w, "文件不存在", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAdminHomeworkSubmissions(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	course := strings.TrimSpace(r.URL.Query().Get("course"))
	assignmentID := strings.TrimSpace(r.URL.Query().Get("assignment_id"))
	items, err := s.store.ListHomeworkSubmissions(r.Context(), course, assignmentID)
	if err != nil {
		http.Error(w, "读取作业列表失败", http.StatusInternalServerError)
		return
	}
	studentFilter := strings.TrimSpace(r.URL.Query().Get("student_no"))
	nameFilter := strings.TrimSpace(r.URL.Query().Get("name"))
	resp := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if studentFilter != "" && !strings.Contains(item.StudentNo, studentFilter) {
			continue
		}
		if nameFilter != "" && !strings.Contains(item.Name, nameFilter) {
			continue
		}
		resp = append(resp, s.homeworkSubmissionPayload(&item, true))
	}
	writeJSON(w, map[string]any{"items": resp})
}

func (s *Server) apiAdminHomeworkSubmission(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	submission, err := s.adminHomeworkSubmission(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"submission": s.homeworkSubmissionPayload(submission, true)})
}

func (s *Server) apiAdminHomeworkReport(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	submission, err := s.adminHomeworkSubmission(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if strings.TrimSpace(submission.ReportOriginalName) == "" {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	fp := filepath.Join(s.homeworkSubmissionDir(submission), homeworkDiskFilename(domain.HomeworkSlotReport))
	if _, err := os.Stat(fp); err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeHomeworkMetadataFilename(submission.ReportOriginalName, "report.pdf")))
	}
	http.ServeFile(w, r, fp)
}

func (s *Server) apiAdminHomeworkCode(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	submission, err := s.adminHomeworkSubmission(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if strings.TrimSpace(submission.CodeOriginalName) == "" {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	fp := s.homeworkStoredFilePath(submission, domain.HomeworkSlotCode)
	if _, err := os.Stat(fp); err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/x-ipynb+json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeHomeworkMetadataFilename(submission.CodeOriginalName, "notebook.ipynb")))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, fp)
}

func (s *Server) apiAdminHomeworkExtra(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	submission, err := s.adminHomeworkSubmission(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if strings.TrimSpace(submission.ExtraOriginalName) == "" {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	fp := filepath.Join(s.homeworkSubmissionDir(submission), "extra.zip")
	if _, err := os.Stat(fp); err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeHomeworkMetadataFilename(submission.ExtraOriginalName, "extra.zip")))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, fp)
}

func (s *Server) apiAdminHomeworkAssignmentVisibility(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Course       string `json:"course"`
		AssignmentID string `json:"assignment_id"`
		Hidden       bool   `json:"hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	course, err := validateHomeworkCourse(req.Course)
	if err != nil {
		http.Error(w, "课程无效", http.StatusBadRequest)
		return
	}
	assignmentID, err := validateHomeworkAssignmentID(req.AssignmentID)
	if err != nil {
		http.Error(w, "作业编号无效", http.StatusBadRequest)
		return
	}
	s.setHomeworkAssignmentVisibility(course, assignmentID, req.Hidden)
	writeJSON(w, map[string]any{"ok": true, "hidden": req.Hidden})
}

func (s *Server) apiAdminHomeworkAssignmentRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Course string `json:"course"`
		OldID  string `json:"old_id"`
		NewID  string `json:"new_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	course, err := validateHomeworkCourse(req.Course)
	if err != nil {
		http.Error(w, "课程无效", http.StatusBadRequest)
		return
	}
	oldID, err := validateHomeworkAssignmentID(req.OldID)
	if err != nil {
		http.Error(w, "旧作业编号无效", http.StatusBadRequest)
		return
	}
	newID, err := validateHomeworkAssignmentID(req.NewID)
	if err != nil {
		http.Error(w, "新作业编号无效", http.StatusBadRequest)
		return
	}
	if oldID == newID {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	oldDir := s.homeworkAssignmentDir(course, oldID)
	newDir := s.homeworkAssignmentDir(course, newID)
	if _, err := os.Stat(oldDir); os.IsNotExist(err) {
		http.Error(w, "作业不存在", http.StatusNotFound)
		return
	}
	if _, err := os.Stat(newDir); err == nil {
		http.Error(w, "目标作业编号已存在", http.StatusConflict)
		return
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		http.Error(w, "重命名失败", http.StatusInternalServerError)
		return
	}
	s.renameHomeworkAssignmentVisibility(course, oldID, newID)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAdminHomeworkArchive(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	submission, err := s.adminHomeworkSubmission(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	archiveData, err := s.buildHomeworkArchive(submission)
	if err != nil {
		http.Error(w, "生成压缩包失败", http.StatusInternalServerError)
		return
	}
	if len(archiveData) == 0 {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	archiveName := fmt.Sprintf("homework_%s_%s.zip", safePathPart(submission.StudentNo), safePathPart(submission.AssignmentID))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", archiveName))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(archiveData)
}

func (s *Server) apiAdminHomeworkArchiveAll(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	course, err := validateHomeworkCourse(r.URL.Query().Get("course"))
	if err != nil {
		http.Error(w, "课程无效", http.StatusBadRequest)
		return
	}
	assignmentID, err := validateHomeworkAssignmentID(r.URL.Query().Get("assignment_id"))
	if err != nil {
		http.Error(w, "作业编号无效", http.StatusBadRequest)
		return
	}
	submissions, err := s.store.ListHomeworkSubmissions(r.Context(), course, assignmentID)
	if err != nil {
		http.Error(w, "读取作业列表失败", http.StatusInternalServerError)
		return
	}
	archiveData, err := s.buildHomeworkBulkArchive(submissions)
	if err != nil {
		http.Error(w, "生成压缩包失败", http.StatusInternalServerError)
		return
	}
	if len(archiveData) == 0 {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	archiveName := fmt.Sprintf("homework_%s_%s_all.zip", safePathPart(course), safePathPart(assignmentID))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", archiveName))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(archiveData)
}

func (s *Server) adminHomeworkSubmission(r *http.Request) (*domain.HomeworkSubmission, error) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		return nil, fmt.Errorf("file not found")
	}
	submission, err := s.store.GetHomeworkSubmissionByID(r.Context(), id)
	if err != nil {
		return nil, fmt.Errorf("file not found")
	}
	return submission, nil
}

func (s *Server) requireHomeworkStudent(r *http.Request) (*domain.HomeworkSubmission, error) {
	cookie, err := r.Cookie(homeworkCookieName)
	if err != nil {
		return nil, err
	}
	return s.store.GetHomeworkSubmissionByToken(r.Context(), cookie.Value)
}

func (s *Server) requireEditableHomeworkStudent(r *http.Request) (*domain.HomeworkSubmission, error) {
	return s.requireHomeworkStudent(r)
}

func (s *Server) loadHomeworkAssignmentVisibility() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, err := s.store.GetSetting(context.Background(), homeworkAssignmentVisibilityKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return map[string]bool{}
	}
	result := map[string]bool{}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

func (s *Server) setHomeworkAssignmentVisibility(course, assignmentID string, hidden bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := s.store.GetSetting(context.Background(), homeworkAssignmentVisibilityKey)
	visibility := map[string]bool{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &visibility)
	}
	key := course + "/" + assignmentID
	if hidden {
		visibility[key] = true
	} else {
		delete(visibility, key)
	}
	payload, _ := json.Marshal(visibility)
	_ = s.store.SetSetting(context.Background(), homeworkAssignmentVisibilityKey, string(payload))
}

func (s *Server) renameHomeworkAssignmentVisibility(course, oldID, newID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := s.store.GetSetting(context.Background(), homeworkAssignmentVisibilityKey)
	visibility := map[string]bool{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &visibility)
	}
	oldKey := course + "/" + oldID
	newKey := course + "/" + newID
	if val, ok := visibility[oldKey]; ok {
		visibility[newKey] = val
		delete(visibility, oldKey)
		payload, _ := json.Marshal(visibility)
		_ = s.store.SetSetting(context.Background(), homeworkAssignmentVisibilityKey, string(payload))
	}
}

func homeworkAssignmentHidden(visibility map[string]bool, course, assignmentID string) bool {
	return visibility[course+"/"+assignmentID]
}

func (s *Server) setHomeworkCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     homeworkCookieName,
		Value:    token,
		Path:     s.cookiePath(),
		MaxAge:   7 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) homeworkAssignmentsRoot() string {
	return filepath.Join(s.pptDir(), homeworkAssignmentsFolder)
}

func (s *Server) homeworkAssignmentCourseDir(course string) string {
	return filepath.Join(s.homeworkAssignmentsRoot(), safePathPart(course))
}

func (s *Server) homeworkAssignmentDir(course, assignmentID string) string {
	return filepath.Join(s.homeworkAssignmentCourseDir(course), safePathPart(assignmentID))
}

func (s *Server) homeworkLegacyAssignmentPath(course, assignmentID string) string {
	return filepath.Join(s.homeworkAssignmentCourseDir(course), safePathPart(assignmentID)+".pdf")
}

func (s *Server) homeworkAssignmentPayload(course, assignmentID string, admin bool) map[string]any {
	files := s.listHomeworkAssignmentFiles(course, assignmentID)
	var updatedAt any
	var totalSize int64
	if len(files) > 0 {
		for _, file := range files {
			if size, ok := file["size"].(int64); ok {
				totalSize += size
			}
			if ts, ok := file["updated_at"].(time.Time); ok {
				if current, ok := updatedAt.(time.Time); !ok || ts.After(current) {
					updatedAt = ts
				}
			}
		}
	}
	payload := map[string]any{
		"course":        course,
		"assignment_id": assignmentID,
		"files":         files,
		"file_count":    len(files),
	}
	if len(files) > 0 {
		payload["size"] = totalSize
		payload["updated_at"] = updatedAt
	}
	if admin {
		payload["bundle_delete_url"] = s.pathPrefix() + "/api/admin/homework/assignments/delete"
		visibility := s.loadHomeworkAssignmentVisibility()
		payload["hidden"] = homeworkAssignmentHidden(visibility, course, assignmentID)
	}
	return payload
}

func (s *Server) listHomeworkCourses() ([]string, error) {
	entries, err := os.ReadDir(s.homeworkAssignmentsRoot())
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	items := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		course, err := validateHomeworkCourse(entry.Name())
		if err != nil {
			continue
		}
		items = append(items, course)
	}
	sort.Strings(items)
	return items, nil
}

func (s *Server) listHomeworkAssignments(course string) ([]string, error) {
	entries, err := os.ReadDir(s.homeworkAssignmentCourseDir(course))
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	items := make([]string, 0, len(entries))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() {
			assignmentID, err := validateHomeworkAssignmentID(entry.Name())
			if err != nil {
				continue
			}
			if !s.homeworkAssignmentExists(course, assignmentID) {
				continue
			}
			seen[assignmentID] = struct{}{}
			continue
		}
		assignmentID, ok := legacyHomeworkAssignmentID(entry.Name())
		if !ok {
			continue
		}
		seen[assignmentID] = struct{}{}
	}
	for assignmentID := range seen {
		items = append(items, assignmentID)
	}
	sort.Strings(items)
	return items, nil
}

func (s *Server) homeworkAssignmentExists(course, assignmentID string) bool {
	for _, file := range s.listHomeworkAssignmentFiles(course, assignmentID) {
		if _, ok := file["name"].(string); ok {
			return true
		}
	}
	return false
}

func (s *Server) resolveHomeworkAssignmentFileRequest(r *http.Request) (string, string, string, string, error) {
	course, err := validateHomeworkCourse(r.URL.Query().Get("course"))
	if err != nil {
		return "", "", "", "", fmt.Errorf("课程无效")
	}
	assignmentID, err := validateHomeworkAssignmentID(r.URL.Query().Get("assignment_id"))
	if err != nil {
		return "", "", "", "", fmt.Errorf("作业编号无效")
	}
	fileName, err := normalizeHomeworkResourceFilename(r.URL.Query().Get("file"))
	if err != nil {
		return "", "", "", "", fmt.Errorf("文件名无效")
	}
	bundlePath := filepath.Join(s.homeworkAssignmentDir(course, assignmentID), fileName)
	if _, err := os.Stat(bundlePath); err == nil {
		return course, assignmentID, fileName, bundlePath, nil
	}
	legacyPath := s.homeworkLegacyAssignmentPath(course, assignmentID)
	if fileName == assignmentID+".pdf" {
		if _, err := os.Stat(legacyPath); err == nil {
			return course, assignmentID, fileName, legacyPath, nil
		}
	}
	return course, assignmentID, fileName, bundlePath, nil
}

func (s *Server) saveUploadedHomeworkAssignment(course, assignmentID, dir string, header *multipart.FileHeader) (map[string]any, map[string]any) {
	name, err := normalizeHomeworkResourceFilename(header.Filename)
	if err != nil {
		return nil, map[string]any{"file": filepath.Base(header.Filename), "error": "文件名无效"}
	}
	file, err := header.Open()
	if err != nil {
		return nil, map[string]any{"file": name, "error": "读取文件失败"}
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, map[string]any{"file": name, "error": "读取文件失败"}
	}
	if strings.EqualFold(filepath.Ext(name), ".pdf") && !looksLikePDF(data) {
		return nil, map[string]any{"file": name, "error": "PDF 文件内容无效"}
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return nil, map[string]any{"file": name, "error": "写入文件失败"}
	}
	return s.homeworkAssignmentPayload(course, assignmentID, true), nil
}

func validateHomeworkCourse(raw string) (string, error) {
	return validateMaterialFolder(raw)
}

func validateHomeworkAssignmentID(raw string) (string, error) {
	name := strings.TrimSpace(filepath.Base(raw))
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("invalid assignment id")
	}
	if strings.HasSuffix(strings.ToLower(name), ".pdf") {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("invalid assignment id")
	}
	return name, nil
}

func normalizeHomeworkResourceFilename(raw string) (string, error) {
	name := strings.TrimSpace(filepath.Base(raw))
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("invalid filename")
	}
	return name, nil
}

func legacyHomeworkAssignmentID(name string) (string, bool) {
	base := strings.TrimSpace(filepath.Base(name))
	if !strings.EqualFold(filepath.Ext(base), ".pdf") {
		return "", false
	}
	id, err := validateHomeworkAssignmentID(strings.TrimSuffix(base, filepath.Ext(base)))
	if err != nil {
		return "", false
	}
	return id, true
}

func (s *Server) listHomeworkAssignmentFiles(course, assignmentID string) []map[string]any {
	items := make([]map[string]any, 0)
	seen := map[string]struct{}{}
	bundleDir := s.homeworkAssignmentDir(course, assignmentID)
	if entries, err := os.ReadDir(bundleDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name, err := normalizeHomeworkResourceFilename(entry.Name())
			if err != nil {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			items = append(items, s.homeworkAssignmentFilePayload(course, assignmentID, name, info.Size(), info.ModTime()))
			seen[name] = struct{}{}
		}
	}
	legacyPath := s.homeworkLegacyAssignmentPath(course, assignmentID)
	legacyName := assignmentID + ".pdf"
	if _, ok := seen[legacyName]; !ok {
		if info, err := os.Stat(legacyPath); err == nil {
			items = append(items, s.homeworkAssignmentFilePayload(course, assignmentID, legacyName, info.Size(), info.ModTime()))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["name"].(string) < items[j]["name"].(string)
	})
	return items
}

func (s *Server) homeworkAssignmentFilePayload(course, assignmentID, name string, size int64, updatedAt time.Time) map[string]any {
	params := url.Values{}
	params.Set("course", course)
	params.Set("assignment_id", assignmentID)
	params.Set("file", name)
	return map[string]any{
		"name":         name,
		"size":         size,
		"updated_at":   updatedAt,
		"extension":    strings.ToLower(filepath.Ext(name)),
		"download_url": s.pathPrefix() + "/api/homework/assignment-file?" + params.Encode(),
	}
}

func (s *Server) migrateLegacyHomeworkAssignmentToBundle(course, assignmentID string) error {
	legacyPath := s.homeworkLegacyAssignmentPath(course, assignmentID)
	if _, err := os.Stat(legacyPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	dir := s.homeworkAssignmentDir(course, assignmentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, assignmentID+".pdf")
	if _, err := os.Stat(target); err == nil {
		return os.Remove(legacyPath)
	}
	return os.Rename(legacyPath, target)
}

func (s *Server) removeHomeworkAssignmentDirIfEmpty(course, assignmentID string) error {
	dir := s.homeworkAssignmentDir(course, assignmentID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(entries) > 0 {
		return nil
	}
	if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Server) homeworkSubmissionDir(submission *domain.HomeworkSubmission) string {
	return filepath.Join(
		s.cfg.DataDir,
		"homework",
		safePathPart(submission.Course),
		safePathPart(submission.AssignmentID),
		safePathPart(submission.StudentNo),
		safePathPart(submission.ID),
	)
}

func (s *Server) homeworkSubmissionPayload(submission *domain.HomeworkSubmission, admin bool) map[string]any {
	payload := map[string]any{
		"id":            submission.ID,
		"course":        submission.Course,
		"assignment_id": submission.AssignmentID,
		"name":          submission.Name,
		"student_no":    submission.StudentNo,
		"class_name":    submission.ClassName,
		"created_at":    submission.CreatedAt,
		"updated_at":    submission.UpdatedAt,
		"files": map[string]any{
			"report": homeworkFilePayload(submission, domain.HomeworkSlotReport),
			"code":   homeworkFilePayload(submission, domain.HomeworkSlotCode),
			"extra":  homeworkFilePayload(submission, domain.HomeworkSlotExtra),
		},
	}
	payload["report_download_url"] = s.pathPrefix() + "/api/homework/download?slot=report"
	payload["code_download_url"] = s.pathPrefix() + "/api/homework/download?slot=code"
	payload["extra_download_url"] = s.pathPrefix() + "/api/homework/download?slot=extra"
	if admin {
		bulkParams := url.Values{}
		bulkParams.Set("course", submission.Course)
		bulkParams.Set("assignment_id", submission.AssignmentID)
		payload["secret_key"] = submission.SecretKey
		payload["report_preview_url"] = s.pathPrefix() + "/api/admin/homework/report?id=" + submission.ID
		payload["report_download_url"] = s.pathPrefix() + "/api/admin/homework/report?id=" + submission.ID + "&download=1"
		payload["code_download_url"] = s.pathPrefix() + "/api/admin/homework/code?id=" + submission.ID
		payload["extra_download_url"] = s.pathPrefix() + "/api/admin/homework/extra?id=" + submission.ID
		payload["archive_download_url"] = s.pathPrefix() + "/api/admin/homework/archive?id=" + submission.ID
		payload["bulk_archive_download_url"] = s.pathPrefix() + "/api/admin/homework/archive-all?" + bulkParams.Encode()
	}
	return payload
}

func homeworkFilePayload(submission *domain.HomeworkSubmission, slot domain.HomeworkFileSlot) map[string]any {
	switch slot {
	case domain.HomeworkSlotReport:
		return map[string]any{
			"uploaded":      submission.ReportOriginalName != "",
			"original_name": submission.ReportOriginalName,
			"uploaded_at":   submission.ReportUploadedAt,
		}
	case domain.HomeworkSlotCode:
		return map[string]any{
			"uploaded":      submission.CodeOriginalName != "",
			"original_name": submission.CodeOriginalName,
			"uploaded_at":   submission.CodeUploadedAt,
		}
	case domain.HomeworkSlotExtra:
		return map[string]any{
			"uploaded":      submission.ExtraOriginalName != "",
			"original_name": submission.ExtraOriginalName,
			"uploaded_at":   submission.ExtraUploadedAt,
		}
	default:
		return map[string]any{"uploaded": false}
	}
}

func parseHomeworkSlot(raw string) (domain.HomeworkFileSlot, error) {
	switch strings.TrimSpace(raw) {
	case string(domain.HomeworkSlotReport):
		return domain.HomeworkSlotReport, nil
	case string(domain.HomeworkSlotCode):
		return domain.HomeworkSlotCode, nil
	case string(domain.HomeworkSlotExtra):
		return domain.HomeworkSlotExtra, nil
	default:
		return "", fmt.Errorf("无效文件槽位")
	}
}

func validateHomeworkFile(slot domain.HomeworkFileSlot, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("文件不能为空")
	}
	switch slot {
	case domain.HomeworkSlotReport:
		if !looksLikePDF(data) {
			return fmt.Errorf("PDF 文件内容无效")
		}
	case domain.HomeworkSlotCode:
		if !looksLikeNotebook(data) {
			return fmt.Errorf("ipynb 文件内容无效")
		}
	case domain.HomeworkSlotExtra:
		// no content validation for zip
	default:
		return fmt.Errorf("无效文件槽位")
	}
	return nil
}

func homeworkDiskFilename(slot domain.HomeworkFileSlot) string {
	switch slot {
	case domain.HomeworkSlotCode:
		return "notebook.ipynb"
	case domain.HomeworkSlotExtra:
		return "extra.zip"
	default:
		return "report.pdf"
	}
}

func sanitizeHomeworkMetadataFilename(name, fallback string) string {
	cleaned := filepath.Base(strings.TrimSpace(name))
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return fallback
	}
	cleaned = strings.ReplaceAll(cleaned, `"`, "_")
	return cleaned
}

func looksLikeNotebook(data []byte) bool {
	if len(bytes.TrimSpace(data)) == 0 || !stdjson.Valid(data) {
		return false
	}
	var payload map[string]any
	if err := stdjson.Unmarshal(data, &payload); err != nil {
		return false
	}
	_, hasCells := payload["cells"]
	_, hasMetadata := payload["metadata"]
	_, hasNbformat := payload["nbformat"]
	return hasCells && hasMetadata && hasNbformat
}

func (s *Server) homeworkStoredFilePath(submission *domain.HomeworkSubmission, slot domain.HomeworkFileSlot) string {
	primary := filepath.Join(s.homeworkSubmissionDir(submission), homeworkDiskFilename(slot))
	if slot != domain.HomeworkSlotCode {
		return primary
	}
	if _, err := os.Stat(primary); err == nil {
		return primary
	}
	legacy := filepath.Join(s.homeworkSubmissionDir(submission), "code.zip")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return primary
}

func (s *Server) buildHomeworkArchive(submission *domain.HomeworkSubmission) ([]byte, error) {
	entries := []struct {
		Name string
		Path string
	}{}
	if submission.ReportOriginalName != "" {
		entries = append(entries, struct {
			Name string
			Path string
		}{
			Name: sanitizeHomeworkMetadataFilename(submission.ReportOriginalName, "report.pdf"),
			Path: filepath.Join(s.homeworkSubmissionDir(submission), "report.pdf"),
		})
	}
	if submission.CodeOriginalName != "" {
		entries = append(entries, struct {
			Name string
			Path string
		}{
			Name: sanitizeHomeworkMetadataFilename(submission.CodeOriginalName, "notebook.ipynb"),
			Path: s.homeworkStoredFilePath(submission, domain.HomeworkSlotCode),
		})
	}
	if submission.ExtraOriginalName != "" {
		entries = append(entries, struct {
			Name string
			Path string
		}{
			Name: sanitizeHomeworkMetadataFilename(submission.ExtraOriginalName, "extra.zip"),
			Path: filepath.Join(s.homeworkSubmissionDir(submission), "extra.zip"),
		})
	}
	if len(entries) == 0 {
		return nil, nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	for _, entry := range entries {
		data, err := os.ReadFile(entry.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			_ = zw.Close()
			return nil, err
		}
		w, err := zw.Create(entry.Name)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if buf.Len() == 22 {
		return nil, nil
	}
	return buf.Bytes(), nil
}

func (s *Server) buildHomeworkBulkArchive(submissions []domain.HomeworkSubmission) ([]byte, error) {
	type archiveEntry struct {
		Name string
		Path string
		Slot domain.HomeworkFileSlot
	}
	type submissionBundle struct {
		folder  string
		entries []archiveEntry
	}
	bundles := make([]submissionBundle, 0, len(submissions))
	usedFolders := map[string]int{}
	for _, submission := range submissions {
		entries := []archiveEntry{}
		if submission.ReportOriginalName != "" {
			entries = append(entries, archiveEntry{
				Name: sanitizeHomeworkMetadataFilename(submission.ReportOriginalName, "report.pdf"),
				Path: filepath.Join(s.homeworkSubmissionDir(&submission), "report.pdf"),
				Slot: domain.HomeworkSlotReport,
			})
		}
		if submission.CodeOriginalName != "" {
			entries = append(entries, archiveEntry{
				Name: sanitizeHomeworkMetadataFilename(submission.CodeOriginalName, "notebook.ipynb"),
				Path: s.homeworkStoredFilePath(&submission, domain.HomeworkSlotCode),
				Slot: domain.HomeworkSlotCode,
			})
		}
		if submission.ExtraOriginalName != "" {
			entries = append(entries, archiveEntry{
				Name: sanitizeHomeworkMetadataFilename(submission.ExtraOriginalName, "extra.zip"),
				Path: filepath.Join(s.homeworkSubmissionDir(&submission), "extra.zip"),
				Slot: domain.HomeworkSlotExtra,
			})
		}
		if len(entries) == 0 {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		baseFolder := safePathPart(submission.StudentNo) + "_" + safePathPart(submission.Name)
		folder := baseFolder
		if usedFolders[baseFolder] > 0 {
			folder = fmt.Sprintf("%s__%s", baseFolder, safePathPart(submission.ID))
		}
		usedFolders[baseFolder]++
		bundles = append(bundles, submissionBundle{folder: folder, entries: entries})
	}
	if len(bundles) == 0 {
		return nil, nil
	}
	sort.Slice(bundles, func(i, j int) bool { return bundles[i].folder < bundles[j].folder })
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	filesWritten := 0
	for _, bundle := range bundles {
		for _, entry := range bundle.entries {
			data, err := os.ReadFile(entry.Path)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				_ = zw.Close()
				return nil, err
			}
			archiveName := entry.Name
			if entry.Slot == domain.HomeworkSlotReport {
				archiveName = "report__" + archiveName
			} else if entry.Slot == domain.HomeworkSlotCode {
				archiveName = "notebook__" + archiveName
			}
			w, err := zw.Create(bundle.folder + "/" + archiveName)
			if err != nil {
				_ = zw.Close()
				return nil, err
			}
			if _, err := w.Write(data); err != nil {
				_ = zw.Close()
				return nil, err
			}
			filesWritten++
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if filesWritten == 0 {
		return nil, nil
	}
	return buf.Bytes(), nil
}

func homeworkAuthStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	return http.StatusUnauthorized
}
