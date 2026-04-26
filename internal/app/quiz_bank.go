// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"course-assistant/internal/domain"
	"course-assistant/internal/quiz"

	"gopkg.in/yaml.v3"
)

// ── Teacher course quiz loading & quiz bank management ──

func (s *Server) apiTeacherCourseLoadQuiz(w http.ResponseWriter, r *http.Request) {
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
	var raw []byte
	var quizSourcePath string
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(5 << 20); err != nil {
			http.Error(w, "读取文件失败", http.StatusBadRequest)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "未上传文件", http.StatusBadRequest)
			return
		}
		defer f.Close()
		raw, _ = io.ReadAll(f)
	} else {
		var req struct {
			YAML     string `json:"yaml"`
			FilePath string `json:"file_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if req.FilePath != "" {
			data, err := os.ReadFile(req.FilePath)
			if err != nil {
				http.Error(w, "读取题库文件失败: "+err.Error(), http.StatusBadRequest)
				return
			}
			raw = data
			quizSourcePath = req.FilePath
		} else {
			raw = []byte(req.YAML)
		}
	}
	parsed, err := quiz.Parse(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	quizRoot := filepath.Join(s.metadataCourseDir(course.TeacherID, course.Slug), "quiz")
	assetDirs := s.collectQuizAssetDirs()
	assetDirs = append(assetDirs, quizRoot)
	if err := quiz.ValidateImagePaths(parsed, assetDirs...); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cs := &domain.CourseState{
		CourseID:       courseID,
		EntryOpen:      false,
		QuizYAML:       string(raw),
		QuizSourcePath: quizSourcePath,
	}
	if err := s.store.SetCourseState(r.Context(), cs); err != nil {
		http.Error(w, "保存题库失败", http.StatusInternalServerError)
		return
	}
	s.quizMu.Lock()
	s.courseQuizzes[courseID] = parsed
	s.quizMu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "count": len(parsed.Questions)})
}

func (s *Server) apiTeacherCourseEntry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
	var req struct {
		Open bool `json:"open"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	cs, _ := s.store.GetCourseState(r.Context(), courseID)
	if cs == nil {
		cs = &domain.CourseState{CourseID: courseID}
	}
	cs.EntryOpen = req.Open
	if err := s.store.SetCourseState(r.Context(), cs); err != nil {
		http.Error(w, "保存失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiTeacherCourseState(w http.ResponseWriter, r *http.Request) {
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
	cs, _ := s.store.GetCourseState(r.Context(), courseID)
	entryOpen := false
	if cs != nil {
		entryOpen = cs.EntryOpen
	}
	s.quizMu.RLock()
	q := s.courseQuizzes[courseID]
	s.quizMu.RUnlock()
	title := ""
	quizID := ""
	if q != nil && strings.TrimSpace(q.QuizID) != "" {
		title = q.Title
		quizID = q.QuizID
	}
	// Scope counters to the currently loaded quiz only, so teachers see "this
	// round" numbers, not the course's cumulative history.
	var started, submitted int
	if q != nil && strings.TrimSpace(q.QuizID) != "" {
		started, submitted, _ = s.store.GetLiveStatsByCourseQuiz(r.Context(), courseID, q.QuizID)
	}
	writeJSON(w, map[string]any{
		"entry_open": entryOpen, "quiz_title": title, "quiz_id": quizID,
		"started": started, "submitted": submitted,
	})
}

func (s *Server) apiTeacherCourseQuizFiles(w http.ResponseWriter, r *http.Request) {
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
	_ = courseID
	contentDir := s.metadataCourseDir(course.TeacherID, course.Slug)
	quizDir := filepath.Join(contentDir, "quiz")
	type quizFileItem struct {
		File string `json:"file"`
		Path string `json:"path"`
	}
	var items []quizFileItem
	files, err := os.ReadDir(quizDir)
	if err != nil {
		writeJSON(w, map[string]any{"items": items})
		return
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			items = append(items, quizFileItem{
				File: name,
				Path: filepath.Join(quizDir, name),
			})
		}
	}
	writeJSON(w, map[string]any{"items": items})
}

// --- quiz bank management (metadata dir) ---

func (s *Server) apiTeacherCourseQuizBank(w http.ResponseWriter, r *http.Request) {
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
	quizRoot := filepath.Join(s.metadataCourseDir(course.TeacherID, course.Slug), "quiz")
	dirs, _ := os.ReadDir(quizRoot)
	type fileItem struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	type bankItem struct {
		QuizID  string     `json:"quiz_id"`
		Files   []fileItem `json:"files"`
		HasYAML bool       `json:"has_yaml"`
	}
	var items []bankItem
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		qid := d.Name()
		subDir := filepath.Join(quizRoot, qid)
		files, _ := os.ReadDir(subDir)
		var fis []fileItem
		hasYAML := false
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			info, _ := f.Info()
			sz := int64(0)
			if info != nil {
				sz = info.Size()
			}
			fis = append(fis, fileItem{Name: f.Name(), Size: sz})
			name := strings.ToLower(f.Name())
			if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
				hasYAML = true
			}
		}
		items = append(items, bankItem{QuizID: qid, Files: fis, HasYAML: hasYAML})
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) apiTeacherCourseQuizBankUpload(w http.ResponseWriter, r *http.Request) {
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
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		http.Error(w, "读取文件失败", http.StatusBadRequest)
		return
	}
	quizID := strings.TrimSpace(r.FormValue("quiz_id"))
	headers := r.MultipartForm.File["files"]
	if len(headers) == 0 {
		http.Error(w, "未上传文件", http.StatusBadRequest)
		return
	}

	allYAML := true
	for _, h := range headers {
		name := strings.ToLower(h.Filename)
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			allYAML = false
			break
		}
	}

	if !allYAML && quizID == "" {
		http.Error(w, "上传混合文件时必须指定 quiz_id", http.StatusBadRequest)
		return
	}

	type uploadResult struct {
		File   string `json:"file"`
		QuizID string `json:"quiz_id"`
		OK     bool   `json:"ok"`
		Error  string `json:"error,omitempty"`
	}
	var results []uploadResult

	if allYAML {
		for _, h := range headers {
			stem := strings.TrimSuffix(strings.TrimSuffix(h.Filename, ".yaml"), ".yml")
			if quizID != "" {
				stem = quizID
			}
			dir := s.metadataQuizDir(course.TeacherID, course.Slug, stem)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				results = append(results, uploadResult{File: h.Filename, QuizID: stem, Error: "创建目录失败"})
				continue
			}
			f, err := h.Open()
			if err != nil {
				results = append(results, uploadResult{File: h.Filename, QuizID: stem, Error: "打开文件失败"})
				continue
			}
			data, _ := io.ReadAll(f)
			f.Close()
			if _, pErr := quiz.Parse(data); pErr != nil {
				results = append(results, uploadResult{File: h.Filename, QuizID: stem, Error: pErr.Error()})
				continue
			}
			if err := os.WriteFile(filepath.Join(dir, h.Filename), data, 0o644); err != nil {
				results = append(results, uploadResult{File: h.Filename, QuizID: stem, Error: "写入失败"})
				continue
			}
			results = append(results, uploadResult{File: h.Filename, QuizID: stem, OK: true})
		}
	} else {
		dir := s.metadataQuizDir(course.TeacherID, course.Slug, quizID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			http.Error(w, "创建目录失败", http.StatusInternalServerError)
			return
		}
		for _, h := range headers {
			f, err := h.Open()
			if err != nil {
				results = append(results, uploadResult{File: h.Filename, QuizID: quizID, Error: "打开文件失败"})
				continue
			}
			data, _ := io.ReadAll(f)
			f.Close()
			name := strings.ToLower(h.Filename)
			if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
				if _, pErr := quiz.Parse(data); pErr != nil {
					results = append(results, uploadResult{File: h.Filename, QuizID: quizID, Error: pErr.Error()})
					continue
				}
			}
			if err := os.WriteFile(filepath.Join(dir, h.Filename), data, 0o644); err != nil {
				results = append(results, uploadResult{File: h.Filename, QuizID: quizID, Error: "写入失败"})
				continue
			}
			results = append(results, uploadResult{File: h.Filename, QuizID: quizID, OK: true})
		}
	}
	writeJSON(w, map[string]any{"results": results})
}

func (s *Server) apiTeacherCourseQuizBankLoad(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		QuizID string `json:"quiz_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.QuizID == "" {
		http.Error(w, "quiz_id 不能为空", http.StatusBadRequest)
		return
	}
	dir := s.metadataQuizDir(course.TeacherID, course.Slug, req.QuizID)
	files, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "题库不存在", http.StatusNotFound)
		return
	}
	var yamlFile string
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := strings.ToLower(f.Name())
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			yamlFile = f.Name()
			break
		}
	}
	if yamlFile == "" {
		http.Error(w, "该题库中未找到 YAML 文件", http.StatusBadRequest)
		return
	}
	raw, err := os.ReadFile(filepath.Join(dir, yamlFile))
	if err != nil {
		http.Error(w, "读取题库文件失败", http.StatusInternalServerError)
		return
	}
	parsed, err := quiz.Parse(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := quiz.ValidateImagePaths(parsed, dir); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cs := &domain.CourseState{
		CourseID:       courseID,
		EntryOpen:      false,
		QuizYAML:       string(raw),
		QuizSourcePath: filepath.Join(dir, yamlFile),
	}
	if err := s.store.SetCourseState(r.Context(), cs); err != nil {
		http.Error(w, "保存题库失败", http.StatusInternalServerError)
		return
	}
	s.quizMu.Lock()
	s.courseQuizzes[courseID] = parsed
	s.courseQuizAssetDirs[courseID] = dir
	s.quizMu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "count": len(parsed.Questions), "quiz_id": parsed.QuizID})
}

func (s *Server) apiTeacherCourseQuizBankDelete(w http.ResponseWriter, r *http.Request) {
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
		QuizID string `json:"quiz_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.QuizID == "" {
		http.Error(w, "quiz_id 不能为空", http.StatusBadRequest)
		return
	}
	dir := s.metadataQuizDir(course.TeacherID, course.Slug, req.QuizID)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		http.Error(w, "题库不存在", http.StatusNotFound)
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		http.Error(w, "删除失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) pageQuizEditor(w http.ResponseWriter, _ *http.Request) {
	s.servePage(w, "web/quiz-editor.html")
}

func (s *Server) apiTeacherCourseQuizBankContent(w http.ResponseWriter, r *http.Request) {
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
	quizID := r.URL.Query().Get("quiz_id")
	if quizID == "" {
		http.Error(w, "quiz_id 不能为空", http.StatusBadRequest)
		return
	}
	dir := s.metadataQuizDir(course.TeacherID, course.Slug, quizID)
	files, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "题库不存在", http.StatusNotFound)
		return
	}
	var yamlContent string
	var yamlFile string
	type fileItem struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	var items []fileItem
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		info, _ := f.Info()
		sz := int64(0)
		if info != nil {
			sz = info.Size()
		}
		items = append(items, fileItem{Name: f.Name(), Size: sz})
		name := strings.ToLower(f.Name())
		if yamlFile == "" && (strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			raw, rErr := os.ReadFile(filepath.Join(dir, f.Name()))
			if rErr == nil {
				yamlContent = string(raw)
				yamlFile = f.Name()
			}
		}
	}
	writeJSON(w, map[string]any{"yaml": yamlContent, "quiz_id": quizID, "filename": yamlFile, "files": items})
}

func (s *Server) apiTeacherCourseQuizBankSave(w http.ResponseWriter, r *http.Request) {
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
		QuizID   string `json:"quiz_id"`
		YAML     string `json:"yaml"`
		Filename string `json:"filename"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	if req.QuizID == "" || req.YAML == "" {
		http.Error(w, "quiz_id 和 yaml 不能为空", http.StatusBadRequest)
		return
	}
	parsed, pErr := quiz.Parse([]byte(req.YAML))
	if pErr != nil {
		http.Error(w, pErr.Error(), http.StatusBadRequest)
		return
	}
	dir := s.metadataQuizDir(course.TeacherID, course.Slug, req.QuizID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "创建目录失败", http.StatusInternalServerError)
		return
	}
	filename := req.Filename
	if filename == "" {
		filename = req.QuizID + ".yaml"
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".yaml") && !strings.HasSuffix(strings.ToLower(filename), ".yml") {
		filename += ".yaml"
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(req.YAML), 0o644); err != nil {
		http.Error(w, "写入失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "question_count": len(parsed.Questions), "quiz_id": req.QuizID})
}

func (s *Server) apiTeacherCourseQuizBankUploadImage(w http.ResponseWriter, r *http.Request) {
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
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "读取文件失败", http.StatusBadRequest)
		return
	}
	quizID := r.FormValue("quiz_id")
	if quizID == "" {
		http.Error(w, "quiz_id 不能为空", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "未上传文件", http.StatusBadRequest)
		return
	}
	defer file.Close()

	dir := s.metadataQuizDir(course.TeacherID, course.Slug, quizID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "创建目录失败", http.StatusInternalServerError)
		return
	}
	data, _ := io.ReadAll(file)
	dst := filepath.Join(dir, header.Filename)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		http.Error(w, "写入失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "filename": header.Filename})
}

func (s *Server) apiTeacherCourseQuizBankAIGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		CourseID int    `json:"course_id"`
		Prompt   string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		http.Error(w, "prompt 不能为空", http.StatusBadRequest)
		return
	}
	yaml, err := s.aiClient.GenerateQuiz(r.Context(), req.Prompt)
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"yaml": yaml})
}

func (s *Server) apiTeacherCourseQuizBankAIAutoFill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		CourseID int    `json:"course_id"`
		YAML     string `json:"yaml"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.YAML) == "" {
		http.Error(w, "yaml 不能为空", http.StatusBadRequest)
		return
	}
	result, err := s.aiClient.AutoFillQuiz(r.Context(), req.YAML)
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"yaml": result})
}

func (s *Server) apiTeacherCourseQuizBankRename(w http.ResponseWriter, r *http.Request) {
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
		OldQuizID string `json:"old_quiz_id"`
		NewQuizID string `json:"new_quiz_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	if req.OldQuizID == "" || req.NewQuizID == "" {
		http.Error(w, "old_quiz_id 和 new_quiz_id 不能为空", http.StatusBadRequest)
		return
	}
	if req.OldQuizID == req.NewQuizID {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	oldDir := s.metadataQuizDir(course.TeacherID, course.Slug, req.OldQuizID)
	newDir := s.metadataQuizDir(course.TeacherID, course.Slug, req.NewQuizID)
	if _, err := os.Stat(oldDir); os.IsNotExist(err) {
		http.Error(w, "原题库不存在", http.StatusNotFound)
		return
	}
	if _, err := os.Stat(newDir); err == nil {
		http.Error(w, "目标题库 ID 已存在", http.StatusConflict)
		return
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		http.Error(w, "重命名失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Update YAML files inside: replace quiz_id field
	entries, _ := os.ReadDir(newDir)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".yaml") && !strings.HasSuffix(strings.ToLower(name), ".yml") {
			continue
		}
		fpath := filepath.Join(newDir, name)
		content, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}
		var q domain.Quiz
		if err := yaml.Unmarshal(content, &q); err != nil {
			continue
		}
		q.QuizID = req.NewQuizID
		out, err := yaml.Marshal(&q)
		if err != nil {
			continue
		}
		_ = os.WriteFile(fpath, out, 0o644)
		// Rename the YAML file itself to match the new quiz ID
		newName := req.NewQuizID + filepath.Ext(name)
		if newName != name {
			_ = os.Rename(fpath, filepath.Join(newDir, newName))
		}
	}
	writeJSON(w, map[string]any{"ok": true, "new_quiz_id": req.NewQuizID})
}

func (s *Server) apiTeacherCourseQuizBankDownload(w http.ResponseWriter, r *http.Request) {
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
	quizID := strings.TrimSpace(r.URL.Query().Get("quiz_id"))
	if quizID == "" {
		http.Error(w, "quiz_id 不能为空", http.StatusBadRequest)
		return
	}
	bankDir := s.metadataQuizDir(course.TeacherID, course.Slug, quizID)
	data, err := s.buildQuizBankZip(bankDir, quizID)
	if err != nil {
		http.Error(w, "生成压缩包失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if len(data) == 0 {
		http.Error(w, "题库为空或不存在", http.StatusNotFound)
		return
	}
	archiveName := safePathPart(quizID) + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", archiveName))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

func (s *Server) apiTeacherCourseQuizBankDownloadAll(w http.ResponseWriter, r *http.Request) {
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
	quizRoot := filepath.Join(s.metadataCourseDir(course.TeacherID, course.Slug), "quiz")
	dirs, _ := os.ReadDir(quizRoot)
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	filesWritten := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		qid := d.Name()
		bankDir := filepath.Join(quizRoot, qid)
		entries, err := os.ReadDir(bankDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fpath := filepath.Join(bankDir, entry.Name())
			data, err := os.ReadFile(fpath)
			if err != nil {
				continue
			}
			fw, err := zw.Create(filepath.Join(qid, entry.Name()))
			if err != nil {
				continue
			}
			_, _ = fw.Write(data)
			filesWritten++
		}
	}
	if err := zw.Close(); err != nil {
		http.Error(w, "生成压缩包失败", http.StatusInternalServerError)
		return
	}
	if filesWritten == 0 {
		http.Error(w, "题库为空", http.StatusNotFound)
		return
	}
	archiveName := safePathPart(course.Slug) + "_quiz_bank_all.zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", archiveName))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(buf.Bytes())
}

// buildQuizBankZip packs all files in a single quiz bank directory into a ZIP.
func (s *Server) buildQuizBankZip(bankDir, quizID string) ([]byte, error) {
	entries, err := os.ReadDir(bankDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	filesWritten := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fpath := filepath.Join(bankDir, entry.Name())
		data, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}
		fw, err := zw.Create(filepath.Join(quizID, entry.Name()))
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		if _, err := fw.Write(data); err != nil {
			_ = zw.Close()
			return nil, err
		}
		filesWritten++
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if filesWritten == 0 {
		return nil, nil
	}
	return buf.Bytes(), nil
}
