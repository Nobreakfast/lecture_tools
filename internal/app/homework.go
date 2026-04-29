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
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"strconv"

	"course-assistant/internal/ai"
	"course-assistant/internal/domain"
	"course-assistant/internal/pdftext"
	"course-assistant/internal/store"
)

func (s *Server) resolveCourseFromRequest(r *http.Request) (*domain.Course, error) {
	if idStr := r.URL.Query().Get("course_id"); idStr != "" {
		id, err := strconv.Atoi(idStr)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid course_id")
		}
		return s.store.GetCourse(r.Context(), id)
	}
	return nil, fmt.Errorf("missing course_id")
}

const (
	homeworkCookieName              = "homework_token"
	homeworkAssignmentsFolder       = "_homework"
	homeworkAssignmentPDFMaxSize    = 500 << 20
	homeworkOthersMaxSize           = 50 << 20
	homeworkQAImageMaxSize          = 8 << 20
	homeworkQAImageMaxCount         = 5
	homeworkOthersFolder            = "others"
	homeworkAssignmentVisibilityKey = "homework_assignment_visibility"
	homeworkGradeVisibilityKey      = "homework_grade_visibility"
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
	c, err := s.resolveCourseFromRequest(r)
	if err != nil {
		http.Error(w, "课程无效", http.StatusBadRequest)
		return
	}
	course := c.Slug
	assignments, _ := s.listHomeworkAssignments(course)
	seen := map[string]struct{}{}
	for _, id := range assignments {
		seen[id] = struct{}{}
	}
	for _, id := range s.listMetadataHomeworkAssignments(c) {
		seen[id] = struct{}{}
	}
	visibility := s.loadHomeworkAssignmentVisibility(r.Context())
	visible := make([]string, 0, len(seen))
	for id := range seen {
		if !homeworkAssignmentHidden(visibility, course, id) {
			visible = append(visible, id)
		}
	}
	sort.Strings(visible)
	writeJSON(w, map[string]any{"items": visible})
}

func (s *Server) apiHomeworkAssignmentPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c, err := s.resolveCourseFromRequest(r)
	if err != nil {
		writeJSON(w, map[string]any{"items": []map[string]any{}})
		return
	}
	assignmentID, err := validateHomeworkAssignmentID(r.URL.Query().Get("assignment_id"))
	if err != nil {
		writeJSON(w, map[string]any{"items": []map[string]any{}})
		return
	}
	if !s.homeworkAssignmentExists(c.Slug, c.ID, assignmentID) {
		writeJSON(w, map[string]any{"items": []map[string]any{}})
		return
	}
	visibility := s.loadHomeworkAssignmentVisibility(r.Context())
	if homeworkAssignmentHidden(visibility, c.Slug, assignmentID) {
		writeJSON(w, map[string]any{"items": []map[string]any{}})
		return
	}
	items := []map[string]any{s.homeworkAssignmentPayload(r.Context(), c.Slug, c.ID, assignmentID, false)}
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
	courseID := submission.CourseID
	assignmentID := submission.AssignmentID
	if !s.homeworkAssignmentExists(course, courseID, assignmentID) {
		writeJSON(w, map[string]any{"items": []map[string]any{}})
		return
	}
	items := []map[string]any{s.homeworkAssignmentPayload(r.Context(), course, courseID, assignmentID, false)}
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
	visibility := s.loadHomeworkAssignmentVisibility(r.Context())
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

func (s *Server) apiHomeworkQA(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		course, assignmentID, ok := s.resolveHomeworkQARequest(w, r)
		if !ok {
			return
		}
		items, err := s.store.ListHomeworkQA(r.Context(), course.ID, course.Slug, assignmentID, false, false)
		if err != nil {
			http.Error(w, "读取 Q&A 失败", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"items": homeworkQAPayloads(items, false)})
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, int64((homeworkQAImageMaxCount*homeworkQAImageMaxSize)+(1<<20)))
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, "请求过大或格式错误", http.StatusBadRequest)
			return
		}
		course, assignmentID, ok := s.resolveHomeworkQARequest(w, r)
		if !ok {
			return
		}
		question := strings.TrimSpace(r.FormValue("question"))
		if question == "" {
			http.Error(w, "问题不能为空", http.StatusBadRequest)
			return
		}
		if len([]rune(question)) > 1000 {
			http.Error(w, "问题不能超过 1000 字", http.StatusBadRequest)
			return
		}
		qaID := newID()
		images, err := s.saveHomeworkQAImages(r, course, assignmentID, qaID, "question")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		now := time.Now()
		qa := &domain.HomeworkQA{
			ID:             qaID,
			Course:         course.Slug,
			CourseID:       course.ID,
			AssignmentID:   assignmentID,
			Question:       question,
			QuestionImages: images,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := s.store.CreateHomeworkQuestion(r.Context(), qa); err != nil {
			_ = os.RemoveAll(s.metadataHomeworkQADir(course.TeacherID, course.Slug, assignmentID, qaID))
			http.Error(w, "保存问题失败", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) apiTeacherCourseHomeworkQA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	courseID, course, err := s.resolveTeacherCourse(r, sess)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	assignmentID, err := validateHomeworkAssignmentID(r.URL.Query().Get("assignment_id"))
	if err != nil {
		http.Error(w, "作业编号无效", http.StatusBadRequest)
		return
	}
	if !s.homeworkAssignmentExists(course.Slug, courseID, assignmentID) {
		http.Error(w, "作业不存在", http.StatusNotFound)
		return
	}
	items, err := s.store.ListHomeworkQA(r.Context(), courseID, course.Slug, assignmentID, true, true)
	if err != nil {
		http.Error(w, "读取 Q&A 失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"items": homeworkQAPayloads(items, true)})
}

func (s *Server) apiTeacherCourseHomeworkQAAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	_, course, err := s.resolveTeacherCourse(r, sess)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64((homeworkQAImageMaxCount*homeworkQAImageMaxSize)+(1<<20)))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "请求过大或格式错误", http.StatusBadRequest)
		return
	}
	qa, ok := s.resolveTeacherHomeworkQA(w, r, course)
	if !ok {
		return
	}
	answer := strings.TrimSpace(r.FormValue("answer"))
	if answer == "" {
		http.Error(w, "回答不能为空", http.StatusBadRequest)
		return
	}
	if len([]rune(answer)) > 3000 {
		http.Error(w, "回答不能超过 3000 字", http.StatusBadRequest)
		return
	}
	images, err := s.saveHomeworkQAImages(r, course, qa.AssignmentID, qa.ID, "answer")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(images) == 0 {
		images = qa.AnswerImages
	}
	if err := s.store.AnswerHomeworkQuestion(r.Context(), qa.ID, answer, images); err != nil {
		http.Error(w, "保存回答失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiTeacherCourseHomeworkQAPin(w http.ResponseWriter, r *http.Request) {
	s.apiTeacherCourseHomeworkQABool(w, r, "pinned")
}

func (s *Server) apiTeacherCourseHomeworkQAHidden(w http.ResponseWriter, r *http.Request) {
	s.apiTeacherCourseHomeworkQABool(w, r, "hidden")
}

func (s *Server) apiTeacherCourseHomeworkQABool(w http.ResponseWriter, r *http.Request, field string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	_, course, err := s.resolveTeacherCourse(r, sess)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var req struct {
		ID    string `json:"id"`
		Value bool   `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	qa, ok := s.resolveTeacherHomeworkQAByID(w, r, course, req.ID)
	if !ok {
		return
	}
	value := req.Value
	if field == "pinned" {
		err = s.store.SetHomeworkQuestionPinned(r.Context(), qa.ID, value)
	} else {
		err = s.store.SetHomeworkQuestionHidden(r.Context(), qa.ID, value)
	}
	if err != nil {
		http.Error(w, "更新失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
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
		CourseID     int    `json:"course_id"`
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
	var course string
	var courseID int
	if req.CourseID > 0 {
		c, err := s.store.GetCourse(r.Context(), req.CourseID)
		if err != nil {
			http.Error(w, "课程无效", http.StatusBadRequest)
			return
		}
		course = c.Slug
		courseID = c.ID
	} else {
		var err error
		course, err = validateHomeworkCourse(req.Course)
		if err != nil {
			http.Error(w, "课程无效", http.StatusBadRequest)
			return
		}
	}
	assignmentID, err := validateHomeworkAssignmentID(req.AssignmentID)
	if err != nil {
		http.Error(w, "作业编号无效", http.StatusBadRequest)
		return
	}
	if !s.homeworkAssignmentExists(course, courseID, assignmentID) {
		http.Error(w, "作业不存在", http.StatusNotFound)
		return
	}
	token := newID() + newID()
	existing, err := s.store.GetHomeworkSubmissionByScope(r.Context(), courseID, course, assignmentID, req.StudentNo)
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
		writeJSON(w, map[string]any{"ok": true, "submission": s.homeworkSubmissionPayload(fresh, false, 0)})
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
		CourseID:     courseID,
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
	writeJSON(w, map[string]any{"ok": true, "submission": s.homeworkSubmissionPayload(submission, false, 0)})
}

func (s *Server) apiHomeworkSubmission(w http.ResponseWriter, r *http.Request) {
	submission, err := s.requireHomeworkStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	payload := s.homeworkSubmissionPayload(submission, false, 0)
	if s.homeworkGradePublished(r.Context(), submission.Course, submission.AssignmentID) && submission.Score != nil {
		payload["score"] = *submission.Score
		payload["feedback"] = submission.Feedback
		payload["graded_at"] = submission.GradedAt
		payload["grade_updated_at"] = submission.GradeUpdatedAt
		payload["grade_published"] = true
	}
	writeJSON(w, map[string]any{"submission": payload})
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
	writeJSON(w, map[string]any{"ok": true, "submission": s.homeworkSubmissionPayload(updated, false, 0)})
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
	writeJSON(w, map[string]any{"ok": true, "submission": s.homeworkSubmissionPayload(updated, false, 0)})
}

func (s *Server) homeworkOthersDir(submission *domain.HomeworkSubmission) string {
	return filepath.Join(s.homeworkSubmissionDir(submission), homeworkOthersFolder)
}

func validateOthersFilename(raw string) (string, error) {
	name := strings.TrimSpace(filepath.Base(raw))
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("文件名无效")
	}
	return name, nil
}

func (s *Server) apiHomeworkOthersUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	submission, err := s.requireEditableHomeworkStudent(r)
	if err != nil {
		http.Error(w, err.Error(), homeworkAuthStatus(err))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, homeworkOthersMaxSize)
	if err := r.ParseMultipartForm(homeworkOthersMaxSize); err != nil {
		http.Error(w, "文件过大（限 50 MB）或格式错误", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "未找到上传文件", http.StatusBadRequest)
		return
	}
	defer file.Close()
	name, err := validateOthersFilename(header.Filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "读取文件失败", http.StatusInternalServerError)
		return
	}
	if len(data) == 0 {
		http.Error(w, "文件不能为空", http.StatusBadRequest)
		return
	}
	dir := s.homeworkOthersDir(submission)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "创建目录失败", http.StatusInternalServerError)
		return
	}
	finalPath := filepath.Join(dir, name)
	tmpPath := filepath.Join(dir, fmt.Sprintf(".%s.%d.tmp", name, time.Now().UnixNano()))
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		http.Error(w, "写入文件失败", http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmpPath)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		http.Error(w, "写入文件失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "file": s.othersFilePayload(name, finalPath)})
}

func (s *Server) apiHomeworkOthersList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	submission, err := s.requireHomeworkStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	items := s.listOthersFiles(submission)
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) apiHomeworkOthersDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	submission, err := s.requireHomeworkStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	name, err := validateOthersFilename(r.URL.Query().Get("file"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fp := filepath.Join(s.homeworkOthersDir(submission), name)
	if _, statErr := os.Stat(fp); statErr != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if strings.TrimSpace(ct) == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, fp)
}

func (s *Server) apiHomeworkOthersDelete(w http.ResponseWriter, r *http.Request) {
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
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	name, err := validateOthersFilename(req.File)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fp := filepath.Join(s.homeworkOthersDir(submission), name)
	if err := os.Remove(fp); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "文件不存在", http.StatusNotFound)
			return
		}
		http.Error(w, "删除失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiHomeworkOthersRename(w http.ResponseWriter, r *http.Request) {
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
		OldName string `json:"old_name"`
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	oldName, err := validateOthersFilename(req.OldName)
	if err != nil {
		http.Error(w, "旧文件名无效", http.StatusBadRequest)
		return
	}
	newName, err := validateOthersFilename(req.NewName)
	if err != nil {
		http.Error(w, "新文件名无效", http.StatusBadRequest)
		return
	}
	if oldName == newName {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	dir := s.homeworkOthersDir(submission)
	oldPath := filepath.Join(dir, oldName)
	newPath := filepath.Join(dir, newName)
	if _, statErr := os.Stat(oldPath); statErr != nil {
		http.Error(w, "文件不存在", http.StatusNotFound)
		return
	}
	if _, statErr := os.Stat(newPath); statErr == nil {
		http.Error(w, "目标文件名已存在", http.StatusConflict)
		return
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		http.Error(w, "重命名失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "file": s.othersFilePayload(newName, newPath)})
}

func (s *Server) listOthersFiles(submission *domain.HomeworkSubmission) []map[string]any {
	dir := s.homeworkOthersDir(submission)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []map[string]any{}
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		name, nameErr := validateOthersFilename(entry.Name())
		if nameErr != nil {
			continue
		}
		fp := filepath.Join(dir, name)
		items = append(items, s.othersFilePayload(name, fp))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["name"].(string) < items[j]["name"].(string)
	})
	return items
}

func (s *Server) othersFilePayload(name, fp string) map[string]any {
	info, err := os.Stat(fp)
	payload := map[string]any{"name": name}
	if err == nil {
		payload["size"] = info.Size()
		payload["updated_at"] = info.ModTime()
	}
	return payload
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
			items = append(items, s.homeworkAssignmentPayload(r.Context(), course, 0, assignmentID, true))
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
		result, failure := s.saveUploadedHomeworkAssignment(r.Context(), course, 0, assignmentID, dir, header)
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
	items, err := s.store.ListHomeworkSubmissions(r.Context(), 0, course, assignmentID)
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
		resp = append(resp, s.homeworkSubmissionPayload(&item, true, 0))
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
	writeJSON(w, map[string]any{"submission": s.homeworkSubmissionPayload(submission, true, submission.CourseID)})
}

func (s *Server) apiTeacherCourseHomeworkSubmissionGrade(w http.ResponseWriter, r *http.Request) {
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	courseID, course, err := s.resolveTeacherCourse(r, sess)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	submission, err := s.teacherHomeworkSubmission(r, courseID, course)
	if err != nil {
		http.Error(w, "提交记录不存在", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{
			"submission":      s.homeworkSubmissionPayload(submission, true, courseID),
			"grade_published": s.homeworkGradePublished(r.Context(), course.Slug, submission.AssignmentID),
		})
	case http.MethodPost:
		var req struct {
			Score    *float64 `json:"score"`
			Feedback string   `json:"feedback"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "请求格式错误", http.StatusBadRequest)
			return
		}
		feedback := strings.TrimSpace(req.Feedback)
		if len([]rune(feedback)) > 5000 {
			http.Error(w, "评语不能超过 5000 字", http.StatusBadRequest)
			return
		}
		if req.Score == nil {
			http.Error(w, "请输入总分", http.StatusBadRequest)
			return
		}
		score := *req.Score
		if score < 0 || score > 100 {
			http.Error(w, "总分必须在 0 到 100 之间", http.StatusBadRequest)
			return
		}
		if math.Abs(score*10-math.Round(score*10)) > 0.000001 {
			http.Error(w, "总分最多保留一位小数", http.StatusBadRequest)
			return
		}
		if err := s.store.SaveHomeworkGrade(r.Context(), submission.ID, &score, feedback); err != nil {
			http.Error(w, "保存评分失败", http.StatusInternalServerError)
			return
		}
		updated, err := s.store.GetHomeworkSubmissionByID(r.Context(), submission.ID)
		if err != nil {
			http.Error(w, "读取评分失败", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "submission": s.homeworkSubmissionPayload(updated, true, courseID)})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) apiTeacherCourseHomeworkSubmissionGradeAI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	courseID, course, err := s.resolveTeacherCourse(r, sess)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	submission, err := s.teacherHomeworkSubmission(r, courseID, course)
	if err != nil {
		http.Error(w, "提交记录不存在", http.StatusNotFound)
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(submission.ReportOriginalName) == "" {
		http.Error(w, "该提交没有报告 PDF，无法生成 AI 评语", http.StatusBadRequest)
		return
	}
	reportPath := filepath.Join(s.resolveHomeworkSubmissionDirForCourse(course, submission), homeworkDiskFilename(domain.HomeworkSlotReport))
	text, err := pdftext.ExtractText(reportPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(text) == "" {
		http.Error(w, "PDF 未提取到可用文本，无法生成 AI 评语", http.StatusBadRequest)
		return
	}
	feedback, err := s.aiClient.GenerateHomeworkFeedback(r.Context(), ai.HomeworkGradeFeedbackInput{
		CourseName:    course.DisplayName,
		AssignmentID:  submission.AssignmentID,
		StudentName:   submission.Name,
		StudentNo:     submission.StudentNo,
		ClassName:     submission.ClassName,
		TeacherNote:   strings.TrimSpace(req.Note),
		ReportContext: text,
	})
	if err != nil {
		http.Error(w, "AI 生成失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"feedback": feedback})
}

func (s *Server) apiTeacherCourseHomeworkGradeVisibility(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	_, course, err := s.resolveTeacherCourse(r, sess)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var req struct {
		AssignmentID string `json:"assignment_id"`
		Published    bool   `json:"published"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	assignmentID, err := validateHomeworkAssignmentID(req.AssignmentID)
	if err != nil {
		http.Error(w, "作业编号无效", http.StatusBadRequest)
		return
	}
	s.setHomeworkGradeVisibility(r.Context(), course.Slug, assignmentID, req.Published)
	writeJSON(w, map[string]any{"ok": true, "published": req.Published})
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
	s.setHomeworkAssignmentVisibility(r.Context(), course, assignmentID, req.Hidden)
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
	s.renameHomeworkAssignmentVisibility(r.Context(), course, oldID, newID)
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
	submissions, err := s.store.ListHomeworkSubmissions(r.Context(), 0, course, assignmentID)
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

func (s *Server) teacherHomeworkSubmission(r *http.Request, courseID int, course *domain.Course) (*domain.HomeworkSubmission, error) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		return nil, fmt.Errorf("file not found")
	}
	submission, err := s.store.GetHomeworkSubmissionByID(r.Context(), id)
	if err != nil {
		return nil, fmt.Errorf("file not found")
	}
	if courseID > 0 && submission.CourseID > 0 && submission.CourseID != courseID {
		return nil, fmt.Errorf("file not found")
	}
	if submission.Course != "" && submission.Course != course.Slug {
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

func (s *Server) loadHomeworkAssignmentVisibility(ctx context.Context) map[string]bool {
	raw, err := s.store.GetSetting(ctx, homeworkAssignmentVisibilityKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return map[string]bool{}
	}
	result := map[string]bool{}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

func (s *Server) setHomeworkAssignmentVisibility(ctx context.Context, course, assignmentID string, hidden bool) {
	s.settingMu.Lock()
	defer s.settingMu.Unlock()
	raw, _ := s.store.GetSetting(ctx, homeworkAssignmentVisibilityKey)
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
	_ = s.store.SetSetting(ctx, homeworkAssignmentVisibilityKey, string(payload))
}

func (s *Server) renameHomeworkAssignmentVisibility(ctx context.Context, course, oldID, newID string) {
	s.settingMu.Lock()
	defer s.settingMu.Unlock()
	raw, _ := s.store.GetSetting(ctx, homeworkAssignmentVisibilityKey)
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
		_ = s.store.SetSetting(ctx, homeworkAssignmentVisibilityKey, string(payload))
	}
}

func homeworkAssignmentHidden(visibility map[string]bool, course, assignmentID string) bool {
	return visibility[course+"/"+assignmentID]
}

func (s *Server) loadHomeworkGradeVisibility(ctx context.Context) map[string]bool {
	raw, err := s.store.GetSetting(ctx, homeworkGradeVisibilityKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return map[string]bool{}
	}
	result := map[string]bool{}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

func (s *Server) homeworkGradePublished(ctx context.Context, course, assignmentID string) bool {
	return s.loadHomeworkGradeVisibility(ctx)[course+"/"+assignmentID]
}

func (s *Server) setHomeworkGradeVisibility(ctx context.Context, course, assignmentID string, published bool) {
	s.settingMu.Lock()
	defer s.settingMu.Unlock()
	raw, _ := s.store.GetSetting(ctx, homeworkGradeVisibilityKey)
	visibility := map[string]bool{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &visibility)
	}
	key := course + "/" + assignmentID
	if published {
		visibility[key] = true
	} else {
		delete(visibility, key)
	}
	payload, _ := json.Marshal(visibility)
	_ = s.store.SetSetting(ctx, homeworkGradeVisibilityKey, string(payload))
}

func (s *Server) setHomeworkCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     homeworkCookieName,
		Value:    token,
		Path:     s.cookiePath(),
		MaxAge:   studentSessionMaxAge,
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

func (s *Server) metadataHomeworkAssignmentDir(teacherID, courseSlug, assignmentID string) string {
	return filepath.Join(s.metadataAssignmentDir(teacherID, courseSlug, assignmentID))
}

func (s *Server) metadataHomeworkSubmissionDir(teacherID, courseSlug string, submission *domain.HomeworkSubmission) string {
	// New layout: .../submissions/<student_no>/  (one submission per student per assignment)
	newDir := filepath.Join(
		s.metadataAssignmentDir(teacherID, courseSlug, submission.AssignmentID),
		"submissions",
		safePathPart(submission.StudentNo),
	)
	// Legacy layout included submission ID: .../submissions/<student_no>/<submission_id>/
	legacyDir := filepath.Join(newDir, safePathPart(submission.ID))
	if _, err := os.Stat(legacyDir); err == nil {
		return legacyDir
	}
	return newDir
}

func (s *Server) metadataHomeworkQADir(teacherID, courseSlug, assignmentID, qaID string) string {
	return filepath.Join(s.metadataAssignmentDir(teacherID, courseSlug, assignmentID), "qa", safePathPart(qaID))
}

func (s *Server) resolveHomeworkAssignmentDirForCourse(course *domain.Course, assignmentID string) string {
	metaDir := s.metadataHomeworkAssignmentDir(course.TeacherID, course.Slug, assignmentID)
	if _, err := os.Stat(metaDir); err == nil {
		return metaDir
	}
	return s.homeworkAssignmentDir(course.Slug, assignmentID)
}

func (s *Server) resolveHomeworkSubmissionDirForCourse(course *domain.Course, submission *domain.HomeworkSubmission) string {
	metaDir := s.metadataHomeworkSubmissionDir(course.TeacherID, course.Slug, submission)
	if _, err := os.Stat(filepath.Dir(metaDir)); err == nil {
		return metaDir
	}
	return s.homeworkSubmissionDir(submission)
}

func (s *Server) homeworkAssignmentPayload(ctx context.Context, course string, courseID int, assignmentID string, admin bool) map[string]any {
	files := s.listHomeworkAssignmentFiles(course, courseID, assignmentID)
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
		visibility := s.loadHomeworkAssignmentVisibility(ctx)
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

func (s *Server) listMetadataHomeworkAssignments(course *domain.Course) []string {
	assignmentRoot := filepath.Join(s.metadataCourseDir(course.TeacherID, course.Slug), "assignment")
	entries, err := os.ReadDir(assignmentRoot)
	if err != nil {
		return nil
	}
	var items []string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "submissions" {
			continue
		}
		items = append(items, entry.Name())
	}
	return items
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
			if !s.homeworkAssignmentExists(course, 0, assignmentID) {
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

func (s *Server) homeworkAssignmentExists(course string, courseID int, assignmentID string) bool {
	for _, file := range s.listHomeworkAssignmentFiles(course, courseID, assignmentID) {
		if _, ok := file["name"].(string); ok {
			return true
		}
	}
	return false
}

func (s *Server) resolveHomeworkAssignmentFileRequest(r *http.Request) (string, string, string, string, error) {
	assignmentID, err := validateHomeworkAssignmentID(r.URL.Query().Get("assignment_id"))
	if err != nil {
		return "", "", "", "", fmt.Errorf("作业编号无效")
	}
	fileName, err := normalizeHomeworkResourceFilename(r.URL.Query().Get("file"))
	if err != nil {
		return "", "", "", "", fmt.Errorf("文件名无效")
	}
	if c, cErr := s.resolveCourseFromRequest(r); cErr == nil && c != nil {
		metaPath := filepath.Join(s.metadataHomeworkAssignmentDir(c.TeacherID, c.Slug, assignmentID), fileName)
		if _, err := os.Stat(metaPath); err == nil {
			return c.Slug, assignmentID, fileName, metaPath, nil
		}
	}
	courseSlug, _ := validateHomeworkCourse(r.URL.Query().Get("course"))
	if courseSlug == "" {
		courseSlug = "default"
	}
	bundlePath := filepath.Join(s.homeworkAssignmentDir(courseSlug, assignmentID), fileName)
	if _, err := os.Stat(bundlePath); err == nil {
		return courseSlug, assignmentID, fileName, bundlePath, nil
	}
	legacyPath := s.homeworkLegacyAssignmentPath(courseSlug, assignmentID)
	if fileName == assignmentID+".pdf" {
		if _, err := os.Stat(legacyPath); err == nil {
			return courseSlug, assignmentID, fileName, legacyPath, nil
		}
	}
	return courseSlug, assignmentID, fileName, bundlePath, nil
}

func (s *Server) saveUploadedHomeworkAssignment(ctx context.Context, course string, courseID int, assignmentID, dir string, header *multipart.FileHeader) (map[string]any, map[string]any) {
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
	return s.homeworkAssignmentPayload(ctx, course, courseID, assignmentID, true), nil
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

func (s *Server) listHomeworkAssignmentFiles(course string, courseID int, assignmentID string) []map[string]any {
	items := make([]map[string]any, 0)
	seen := map[string]struct{}{}
	if c := s.resolveHomeworkCourse(courseID, course); c != nil {
		metaDir := s.metadataHomeworkAssignmentDir(c.TeacherID, c.Slug, assignmentID)
		if entries, err := os.ReadDir(metaDir); err == nil {
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
				items = append(items, s.homeworkAssignmentFilePayload(course, courseID, assignmentID, name, info.Size(), info.ModTime()))
				seen[name] = struct{}{}
			}
		}
	}
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
			items = append(items, s.homeworkAssignmentFilePayload(course, courseID, assignmentID, name, info.Size(), info.ModTime()))
			seen[name] = struct{}{}
		}
	}
	legacyPath := s.homeworkLegacyAssignmentPath(course, assignmentID)
	legacyName := assignmentID + ".pdf"
	if _, ok := seen[legacyName]; !ok {
		if info, err := os.Stat(legacyPath); err == nil {
			items = append(items, s.homeworkAssignmentFilePayload(course, courseID, assignmentID, legacyName, info.Size(), info.ModTime()))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["name"].(string) < items[j]["name"].(string)
	})
	return items
}

func (s *Server) homeworkAssignmentFilePayload(course string, courseID int, assignmentID, name string, size int64, updatedAt time.Time) map[string]any {
	params := url.Values{}
	params.Set("course", course)
	params.Set("assignment_id", assignmentID)
	params.Set("file", name)
	if courseID > 0 {
		params.Set("course_id", strconv.Itoa(courseID))
	}
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
	if c := s.resolveHomeworkCourse(submission.CourseID, submission.Course); c != nil {
		return s.metadataHomeworkSubmissionDir(c.TeacherID, c.Slug, submission)
	}
	return filepath.Join(s.cfg.MetadataDir, safePathPart(submission.Course),
		"assignment", safePathPart(submission.AssignmentID),
		"submissions", safePathPart(submission.StudentNo))
}

func (s *Server) resolveHomeworkCourse(courseID int, courseSlug string) *domain.Course {
	// Intentionally uses context.Background(): this method is called from
	// internal helpers like homeworkSubmissionDir and listHomeworkAssignmentFiles
	// that do not have access to a request context. The DB lookup here is
	// lightweight and not worth propagating ctx through 15+ callers.
	if courseID > 0 {
		if c, err := s.store.GetCourse(context.Background(), courseID); err == nil {
			return c
		}
	}
	return nil
}

func (s *Server) homeworkSubmissionPayload(submission *domain.HomeworkSubmission, admin bool, courseID int) map[string]any {
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
		"others": s.listOthersFiles(submission),
	}
	payload["report_download_url"] = s.pathPrefix() + "/api/homework/download?slot=report"
	payload["code_download_url"] = s.pathPrefix() + "/api/homework/download?slot=code"
	payload["extra_download_url"] = s.pathPrefix() + "/api/homework/download?slot=extra"
	if admin && courseID > 0 {
		cid := strconv.Itoa(courseID)
		dlParams := func(slot string) string {
			p := url.Values{}
			p.Set("course_id", cid)
			p.Set("id", submission.ID)
			p.Set("slot", slot)
			return p.Encode()
		}
		bulkParams := url.Values{}
		bulkParams.Set("course_id", cid)
		bulkParams.Set("assignment_id", submission.AssignmentID)
		payload["secret_key"] = submission.SecretKey
		if submission.Score != nil {
			payload["score"] = *submission.Score
		}
		payload["feedback"] = submission.Feedback
		payload["graded_at"] = submission.GradedAt
		payload["grade_updated_at"] = submission.GradeUpdatedAt
		payload["grade_published"] = s.homeworkGradePublished(context.Background(), submission.Course, submission.AssignmentID)
		payload["report_preview_url"] = s.pathPrefix() + "/api/teacher/courses/homework/submissions/download?" + dlParams("report")
		payload["report_download_url"] = s.pathPrefix() + "/api/teacher/courses/homework/submissions/download?" + dlParams("report") + "&download=1"
		payload["code_download_url"] = s.pathPrefix() + "/api/teacher/courses/homework/submissions/download?" + dlParams("code")
		payload["extra_download_url"] = s.pathPrefix() + "/api/teacher/courses/homework/submissions/download?" + dlParams("extra")
		payload["archive_download_url"] = s.pathPrefix() + "/api/teacher/courses/homework/submissions/archive?" + dlParams("")
		payload["bulk_archive_download_url"] = s.pathPrefix() + "/api/teacher/courses/homework/archive-all?" + bulkParams.Encode()
	}
	return payload
}

func (s *Server) resolveHomeworkQARequest(w http.ResponseWriter, r *http.Request) (*domain.Course, string, bool) {
	rawCourseID := strings.TrimSpace(r.FormValue("course_id"))
	if rawCourseID == "" {
		rawCourseID = strings.TrimSpace(r.URL.Query().Get("course_id"))
	}
	courseID, err := strconv.Atoi(rawCourseID)
	if err != nil || courseID <= 0 {
		http.Error(w, "课程无效", http.StatusBadRequest)
		return nil, "", false
	}
	course, err := s.store.GetCourse(r.Context(), courseID)
	if err != nil || course == nil {
		http.Error(w, "课程无效", http.StatusBadRequest)
		return nil, "", false
	}
	assignmentID, err := validateHomeworkAssignmentID(r.FormValue("assignment_id"))
	if err != nil {
		http.Error(w, "作业编号无效", http.StatusBadRequest)
		return nil, "", false
	}
	if !s.homeworkAssignmentExists(course.Slug, course.ID, assignmentID) {
		http.Error(w, "作业不存在", http.StatusNotFound)
		return nil, "", false
	}
	return course, assignmentID, true
}

func (s *Server) resolveTeacherHomeworkQA(w http.ResponseWriter, r *http.Request, course *domain.Course) (*domain.HomeworkQA, bool) {
	return s.resolveTeacherHomeworkQAByID(w, r, course, strings.TrimSpace(r.FormValue("id")))
}

func (s *Server) resolveTeacherHomeworkQAByID(w http.ResponseWriter, r *http.Request, course *domain.Course, id string) (*domain.HomeworkQA, bool) {
	if id == "" {
		http.Error(w, "缺少 id 参数", http.StatusBadRequest)
		return nil, false
	}
	qa, err := s.store.GetHomeworkQAByID(r.Context(), id)
	if err != nil {
		http.Error(w, "问题不存在", http.StatusNotFound)
		return nil, false
	}
	if qa.CourseID != course.ID {
		http.Error(w, "无权限访问此问题", http.StatusForbidden)
		return nil, false
	}
	return qa, true
}

func homeworkQAPayloads(items []domain.HomeworkQA, includePrivate bool) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		payload := map[string]any{
			"id":              item.ID,
			"assignment_id":   item.AssignmentID,
			"question":        item.Question,
			"question_images": item.QuestionImages,
			"answer":          item.Answer,
			"answer_images":   item.AnswerImages,
			"pinned":          item.Pinned,
			"created_at":      item.CreatedAt,
			"answered_at":     item.AnsweredAt,
			"updated_at":      item.UpdatedAt,
		}
		if includePrivate {
			payload["hidden"] = item.Hidden
			payload["unanswered"] = strings.TrimSpace(item.Answer) == ""
		}
		result = append(result, payload)
	}
	return result
}

func (s *Server) saveHomeworkQAImages(r *http.Request, course *domain.Course, assignmentID, qaID, kind string) ([]string, error) {
	files := homeworkQAImageHeaders(r)
	if len(files) == 0 {
		return []string{}, nil
	}
	if len(files) > homeworkQAImageMaxCount {
		return nil, fmt.Errorf("最多上传 %d 张图片", homeworkQAImageMaxCount)
	}
	dir := filepath.Join(s.metadataHomeworkQADir(course.TeacherID, course.Slug, assignmentID, qaID), safePathPart(kind))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建图片目录失败")
	}
	urls := make([]string, 0, len(files))
	for idx, header := range files {
		data, ext, err := readHomeworkQAImage(header)
		if err != nil {
			return nil, err
		}
		filename := fmt.Sprintf("%s_%d_%d%s", kind, time.Now().UnixMilli(), idx+1, ext)
		if err := os.WriteFile(filepath.Join(dir, filename), data, 0o644); err != nil {
			return nil, fmt.Errorf("写入图片失败")
		}
		rel, _ := filepath.Rel(s.cfg.MetadataDir, filepath.Join(dir, filename))
		urls = append(urls, s.pathPrefix()+"/uploads/"+filepath.ToSlash(rel))
	}
	return urls, nil
}

func homeworkQAImageHeaders(r *http.Request) []*multipart.FileHeader {
	if r.MultipartForm == nil || r.MultipartForm.File == nil {
		return nil
	}
	files := make([]*multipart.FileHeader, 0)
	files = append(files, r.MultipartForm.File["images"]...)
	files = append(files, r.MultipartForm.File["images[]"]...)
	return files
}

func readHomeworkQAImage(header *multipart.FileHeader) ([]byte, string, error) {
	if header == nil {
		return nil, "", fmt.Errorf("图片无效")
	}
	if header.Size > homeworkQAImageMaxSize {
		return nil, "", fmt.Errorf("单张图片不能超过 8 MB")
	}
	file, err := header.Open()
	if err != nil {
		return nil, "", fmt.Errorf("读取图片失败")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, homeworkQAImageMaxSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("读取图片失败")
	}
	if len(data) > homeworkQAImageMaxSize {
		return nil, "", fmt.Errorf("单张图片不能超过 8 MB")
	}
	if len(data) < 4 {
		return nil, "", fmt.Errorf("图片文件太小")
	}
	if data[0] == 0xFF && data[1] == 0xD8 {
		return data, ".jpg", nil
	}
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return data, ".png", nil
	}
	return nil, "", fmt.Errorf("仅支持 JPEG/PNG 格式图片")
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
	case string(domain.HomeworkSlotOthers):
		return domain.HomeworkSlotOthers, nil
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

func homeworkSubmissionFileBase(submission *domain.HomeworkSubmission) string {
	return strings.Join([]string{
		safePathPart(submission.ClassName),
		safePathPart(submission.AssignmentID),
		safePathPart(submission.Name),
		safePathPart(submission.StudentNo),
	}, "_")
}

func homeworkSubmissionDownloadFilename(submission *domain.HomeworkSubmission, slot domain.HomeworkFileSlot) string {
	ext := strings.ToLower(filepath.Ext(homeworkDiskFilename(slot)))
	if ext == "" {
		ext = ".bin"
	}
	return homeworkSubmissionFileBase(submission) + ext
}

func homeworkSubmissionArchiveFilename(submission *domain.HomeworkSubmission) string {
	return homeworkSubmissionFileBase(submission) + ".zip"
}

func homeworkBulkArchiveFilename(course *domain.Course, assignmentID string) string {
	courseName := course.InternalName
	if strings.TrimSpace(courseName) == "" {
		courseName = course.Slug
	}
	return fmt.Sprintf("%s_%s_all.zip", safePathPart(courseName), safePathPart(assignmentID))
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
	type archiveFile struct {
		Name string
		Path string
	}
	entries := []archiveFile{}
	if submission.ReportOriginalName != "" {
		entries = append(entries, archiveFile{
			Name: homeworkSubmissionDownloadFilename(submission, domain.HomeworkSlotReport),
			Path: filepath.Join(s.homeworkSubmissionDir(submission), "report.pdf"),
		})
	}
	if submission.CodeOriginalName != "" {
		entries = append(entries, archiveFile{
			Name: homeworkSubmissionDownloadFilename(submission, domain.HomeworkSlotCode),
			Path: s.homeworkStoredFilePath(submission, domain.HomeworkSlotCode),
		})
	}
	if submission.ExtraOriginalName != "" {
		entries = append(entries, archiveFile{
			Name: homeworkSubmissionDownloadFilename(submission, domain.HomeworkSlotExtra),
			Path: filepath.Join(s.homeworkSubmissionDir(submission), "extra.zip"),
		})
	}
	othersDir := s.homeworkOthersDir(submission)
	if dirEntries, err := os.ReadDir(othersDir); err == nil {
		for _, de := range dirEntries {
			if de.IsDir() || strings.HasPrefix(de.Name(), ".") {
				continue
			}
			entries = append(entries, archiveFile{
				Name: homeworkOthersFolder + "/" + de.Name(),
				Path: filepath.Join(othersDir, de.Name()),
			})
		}
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
				Name: homeworkSubmissionDownloadFilename(&submission, domain.HomeworkSlotReport),
				Path: filepath.Join(s.homeworkSubmissionDir(&submission), "report.pdf"),
				Slot: domain.HomeworkSlotReport,
			})
		}
		if submission.CodeOriginalName != "" {
			entries = append(entries, archiveEntry{
				Name: homeworkSubmissionDownloadFilename(&submission, domain.HomeworkSlotCode),
				Path: s.homeworkStoredFilePath(&submission, domain.HomeworkSlotCode),
				Slot: domain.HomeworkSlotCode,
			})
		}
		if submission.ExtraOriginalName != "" {
			entries = append(entries, archiveEntry{
				Name: homeworkSubmissionDownloadFilename(&submission, domain.HomeworkSlotExtra),
				Path: filepath.Join(s.homeworkSubmissionDir(&submission), "extra.zip"),
				Slot: domain.HomeworkSlotExtra,
			})
		}
		othersDir := s.homeworkOthersDir(&submission)
		if dirEntries, oErr := os.ReadDir(othersDir); oErr == nil {
			for _, de := range dirEntries {
				if de.IsDir() || strings.HasPrefix(de.Name(), ".") {
					continue
				}
				entries = append(entries, archiveEntry{
					Name: homeworkOthersFolder + "/" + de.Name(),
					Path: filepath.Join(othersDir, de.Name()),
					Slot: domain.HomeworkSlotOthers,
				})
			}
		}
		if len(entries) == 0 {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		baseFolder := homeworkSubmissionFileBase(&submission)
		folder := baseFolder
		if usedFolders[baseFolder] > 0 {
			folder = fmt.Sprintf("%s__%d", baseFolder, usedFolders[baseFolder]+1)
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
			w, err := zw.Create(bundle.folder + "/" + entry.Name)
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
