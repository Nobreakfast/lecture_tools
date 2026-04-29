package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"course-assistant/internal/domain"
)

const (
	qaIssueImageMaxSize  = 8 << 20
	qaIssueImageMaxCount = 5
)

// ── Student-facing APIs ──

func (s *Server) apiQAIssueCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	submission, err := s.requireHomeworkStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64((qaIssueImageMaxCount*qaIssueImageMaxSize)+(1<<20)))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "请求过大或格式错误", http.StatusBadRequest)
		return
	}
	courseIDStr := r.FormValue("course_id")
	assignmentID := strings.TrimSpace(r.FormValue("assignment_id"))
	firstMessage := strings.TrimSpace(r.FormValue("message"))
	courseID, _ := strconv.Atoi(courseIDStr)
	if courseID == 0 && submission.CourseID > 0 {
		courseID = submission.CourseID
	}
	if courseID == 0 {
		http.Error(w, "缺少 course_id", http.StatusBadRequest)
		return
	}
	if assignmentID == "" {
		http.Error(w, "缺少 assignment_id", http.StatusBadRequest)
		return
	}
	if firstMessage == "" {
		http.Error(w, "消息不能为空", http.StatusBadRequest)
		return
	}
	if len([]rune(firstMessage)) > 3000 {
		http.Error(w, "消息不能超过 3000 字", http.StatusBadRequest)
		return
	}
	course, err := s.store.GetCourse(r.Context(), courseID)
	if err != nil {
		http.Error(w, "课程不存在", http.StatusBadRequest)
		return
	}
	now := time.Now()
	// Title is truncated first message
	title := firstMessage
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80]) + "..."
	}
	issue := &domain.QAIssue{
		CourseID:     courseID,
		Course:       course.Slug,
		AssignmentID: assignmentID,
		StudentNo:    submission.StudentNo,
		Title:        title,
		Status:       "open",
		MessageCount: 1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	issueID, err := s.store.CreateQAIssue(r.Context(), issue)
	if err != nil {
		http.Error(w, "创建 Issue 失败", http.StatusInternalServerError)
		return
	}
	// Save first message
	images, err := s.saveQAIssueImages(r, course, assignmentID, int(issueID), 1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	msg := &domain.QAMessage{
		IssueID:   int(issueID),
		Sender:    "student",
		Content:   firstMessage,
		Images:    images,
		CreatedAt: now,
	}
	if _, err := s.store.CreateQAMessage(r.Context(), msg); err != nil {
		http.Error(w, "保存消息失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "issue_id": issueID})
}

func (s *Server) apiQAIssuesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	courseIDStr := r.URL.Query().Get("course_id")
	assignmentID := r.URL.Query().Get("assignment_id")
	courseID, _ := strconv.Atoi(courseIDStr)
	if courseID == 0 {
		http.Error(w, "缺少 course_id", http.StatusBadRequest)
		return
	}
	issues, err := s.store.ListQAIssues(r.Context(), courseID, assignmentID, false)
	if err != nil {
		http.Error(w, "读取 Issues 失败", http.StatusInternalServerError)
		return
	}
	items := make([]map[string]any, 0, len(issues))
	for _, iss := range issues {
		items = append(items, qaIssuePayload(iss))
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) apiQAIssueGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	idStr := r.URL.Query().Get("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		http.Error(w, "无效的 Issue ID", http.StatusBadRequest)
		return
	}
	issue, err := s.store.GetQAIssueByID(r.Context(), id)
	if err != nil {
		http.Error(w, "Issue 不存在", http.StatusNotFound)
		return
	}
	messages, err := s.store.ListQAMessages(r.Context(), id)
	if err != nil {
		http.Error(w, "读取消息失败", http.StatusInternalServerError)
		return
	}
	msgPayloads := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		msgPayloads = append(msgPayloads, qaMessagePayload(m))
	}
	payload := qaIssuePayload(*issue)
	payload["messages"] = msgPayloads
	writeJSON(w, payload)
}

func (s *Server) apiQAIssueAddMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	isStudent := false
	_, studentErr := s.requireHomeworkStudent(r)
	if studentErr == nil {
		isStudent = true
	}
	if !isStudent && s.requireTeacherOrAdmin(r) == nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64((qaIssueImageMaxCount*qaIssueImageMaxSize)+(1<<20)))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "请求过大或格式错误", http.StatusBadRequest)
		return
	}
	issueIDStr := r.FormValue("issue_id")
	issueID, err := strconv.Atoi(issueIDStr)
	if err != nil || issueID <= 0 {
		http.Error(w, "无效的 Issue ID", http.StatusBadRequest)
		return
	}
	content := strings.TrimSpace(r.FormValue("message"))
	if content == "" {
		http.Error(w, "消息不能为空", http.StatusBadRequest)
		return
	}
	if len([]rune(content)) > 3000 {
		http.Error(w, "消息不能超过 3000 字", http.StatusBadRequest)
		return
	}
	issue, err := s.store.GetQAIssueByID(r.Context(), issueID)
	if err != nil {
		http.Error(w, "Issue 不存在", http.StatusNotFound)
		return
	}
	// Determine sender
	sender := "student"
	if !isStudent {
		sender = "teacher"
	}
	course, err := s.store.GetCourse(r.Context(), issue.CourseID)
	if err != nil {
		http.Error(w, "课程不存在", http.StatusInternalServerError)
		return
	}
	msgCount := issue.MessageCount + 1
	images, err := s.saveQAIssueImages(r, course, issue.AssignmentID, issueID, msgCount)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := time.Now()
	msg := &domain.QAMessage{
		IssueID:   issueID,
		Sender:    sender,
		Content:   content,
		Images:    images,
		CreatedAt: now,
	}
	if _, err := s.store.CreateQAMessage(r.Context(), msg); err != nil {
		http.Error(w, "保存消息失败", http.StatusInternalServerError)
		return
	}
	if err := s.store.IncrementQAIssueMessageCount(r.Context(), issueID); err != nil {
		http.Error(w, "更新 Issue 失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ── Shared link access (no auth required to read) ──

func (s *Server) apiQAIssueByInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	inviteCode := r.URL.Query().Get("invite")
	issueIDStr := r.URL.Query().Get("issue_id")
	issueID, err := strconv.Atoi(issueIDStr)
	if err != nil || issueID <= 0 {
		http.Error(w, "无效的 Issue ID", http.StatusBadRequest)
		return
	}
	course, err := s.store.GetCourseByInviteCode(r.Context(), inviteCode)
	if err != nil {
		http.Error(w, "邀请码无效", http.StatusBadRequest)
		return
	}
	issue, err := s.store.GetQAIssueByID(r.Context(), issueID)
	if err != nil {
		http.Error(w, "Issue 不存在", http.StatusNotFound)
		return
	}
	if issue.CourseID != course.ID {
		http.Error(w, "Issue 不属于该课程", http.StatusForbidden)
		return
	}
	messages, err := s.store.ListQAMessages(r.Context(), issueID)
	if err != nil {
		http.Error(w, "读取消息失败", http.StatusInternalServerError)
		return
	}
	msgPayloads := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		msgPayloads = append(msgPayloads, qaMessagePayload(m))
	}
	payload := qaIssuePayload(*issue)
	payload["messages"] = msgPayloads
	payload["course_name"] = course.DisplayName
	writeJSON(w, payload)
}

// ── Teacher-facing APIs ──

func (s *Server) apiTeacherQAIssues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	courseID, _, err := s.resolveTeacherCourse(r, sess)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	issues, err := s.store.ListQAIssuesByCourse(r.Context(), courseID, true)
	if err != nil {
		http.Error(w, "读取 Issues 失败", http.StatusInternalServerError)
		return
	}
	items := make([]map[string]any, 0, len(issues))
	for _, iss := range issues {
		items = append(items, qaIssuePayload(iss))
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) apiTeacherQAIssueResolve(w http.ResponseWriter, r *http.Request) {
	s.apiTeacherQAIssueBoolAction(w, r, "resolved")
}

func (s *Server) apiTeacherQAIssueReopen(w http.ResponseWriter, r *http.Request) {
	s.apiTeacherQAIssueBoolAction(w, r, "open")
}

func (s *Server) apiTeacherQAIssuePin(w http.ResponseWriter, r *http.Request) {
	s.apiTeacherQAIssueToggle(w, r, "pinned")
}

func (s *Server) apiTeacherQAIssueHide(w http.ResponseWriter, r *http.Request) {
	s.apiTeacherQAIssueToggle(w, r, "hidden")
}

func (s *Server) apiTeacherQAIssueBoolAction(w http.ResponseWriter, r *http.Request, status string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	_, _, err := s.resolveTeacherCourse(r, sess)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var req struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := s.store.UpdateQAIssueStatus(r.Context(), req.ID, status); err != nil {
		http.Error(w, "更新失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiTeacherQAIssueToggle(w http.ResponseWriter, r *http.Request, field string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	_, _, err := s.resolveTeacherCourse(r, sess)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var req struct {
		ID    int  `json:"id"`
		Value bool `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	switch field {
	case "pinned":
		err = s.store.SetQAIssuePinned(r.Context(), req.ID, req.Value)
	case "hidden":
		err = s.store.SetQAIssueHidden(r.Context(), req.ID, req.Value)
	}
	if err != nil {
		http.Error(w, "更新失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ── Page handler ──

func (s *Server) serveQAPage(w http.ResponseWriter, r *http.Request) {
	data, err := webFS.ReadFile("web/qa.html")
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (s *Server) serveTeacherQAPage(w http.ResponseWriter, r *http.Request) {
	data, err := webFS.ReadFile("web/qa.html")
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// ── Helpers ──

func qaIssuePayload(iss domain.QAIssue) map[string]any {
	return map[string]any{
		"id":            iss.ID,
		"course_id":     iss.CourseID,
		"assignment_id": iss.AssignmentID,
		"student_no":    iss.StudentNo,
		"title":         iss.Title,
		"status":        iss.Status,
		"pinned":        iss.Pinned,
		"hidden":        iss.Hidden,
		"message_count": iss.MessageCount,
		"created_at":    iss.CreatedAt,
		"updated_at":    iss.UpdatedAt,
	}
}

func qaMessagePayload(msg domain.QAMessage) map[string]any {
	return map[string]any{
		"id":         msg.ID,
		"issue_id":   msg.IssueID,
		"sender":     msg.Sender,
		"content":    msg.Content,
		"images":     msg.Images,
		"created_at": msg.CreatedAt,
	}
}

func (s *Server) saveQAIssueImages(r *http.Request, course *domain.Course, assignmentID string, issueID, msgSeq int) ([]string, error) {
	files := r.MultipartForm.File["images"]
	if len(files) == 0 {
		return nil, nil
	}
	if len(files) > qaIssueImageMaxCount {
		return nil, fmt.Errorf("最多 %d 张图片", qaIssueImageMaxCount)
	}
	dir := filepath.Join(s.metadataAssignmentDir(course.TeacherID, course.Slug, assignmentID), "qa", fmt.Sprintf("issue_%d", issueID), fmt.Sprintf("msg_%d", msgSeq))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建图片目录失败")
	}
	var urls []string
	for _, fh := range files {
		if fh.Size > qaIssueImageMaxSize {
			return nil, fmt.Errorf("图片不能超过 8 MB")
		}
		f, err := fh.Open()
		if err != nil {
			return nil, fmt.Errorf("读取图片失败")
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("读取图片失败")
		}
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".gif" && ext != ".webp" {
			ext = ".png"
		}
		name := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
		fp := filepath.Join(dir, name)
		if err := os.WriteFile(fp, data, 0o644); err != nil {
			return nil, fmt.Errorf("保存图片失败")
		}
		url := fmt.Sprintf("%s/api/qa/issue-image?course_id=%d&assignment_id=%s&issue_id=%d&msg=%d&file=%s",
			s.pathPrefix(), course.ID, assignmentID, issueID, msgSeq, name)
		urls = append(urls, url)
	}
	return urls, nil
}

func (s *Server) apiQAIssueImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	courseIDStr := r.URL.Query().Get("course_id")
	assignmentID := r.URL.Query().Get("assignment_id")
	issueIDStr := r.URL.Query().Get("issue_id")
	msgStr := r.URL.Query().Get("msg")
	fileName := r.URL.Query().Get("file")
	courseID, _ := strconv.Atoi(courseIDStr)
	issueID, _ := strconv.Atoi(issueIDStr)
	msgSeq, _ := strconv.Atoi(msgStr)
	if courseID == 0 || assignmentID == "" || issueID == 0 || msgSeq == 0 || fileName == "" {
		http.Error(w, "参数不完整", http.StatusBadRequest)
		return
	}
	course, err := s.store.GetCourse(r.Context(), courseID)
	if err != nil {
		http.Error(w, "课程不存在", http.StatusNotFound)
		return
	}
	dir := filepath.Join(s.metadataAssignmentDir(course.TeacherID, course.Slug, assignmentID), "qa", fmt.Sprintf("issue_%d", issueID), fmt.Sprintf("msg_%d", msgSeq))
	fp := filepath.Join(dir, filepath.Base(fileName))
	if _, err := os.Stat(fp); err != nil {
		http.Error(w, "图片不存在", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, fp)
}
