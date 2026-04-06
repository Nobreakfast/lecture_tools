package app

import (
	"context"
	crand "crypto/rand"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	mrand "math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"course-assistant/internal/ai"
	"course-assistant/internal/domain"
	"course-assistant/internal/quiz"
	"course-assistant/internal/store"
)

//go:embed web/*
var webFS embed.FS

type Config struct {
	Addr          string
	BaseURL       string
	AdminPassword string
	DataDir       string
	QuizAssetsDir string
	AIEndpoint    string
	AIKey         string
	AIModel       string
}

type Server struct {
	cfg         Config
	store       store.Store
	aiClient    *ai.Client
	mu          sync.RWMutex
	currentQuiz *domain.Quiz
	adminTokens map[string]time.Time
	shutdownFn  func()
}

func New(cfg Config, st store.Store) *Server {
	return &Server{
		cfg:         cfg,
		store:       st,
		aiClient:    ai.NewClient(cfg.AIEndpoint, cfg.AIKey, cfg.AIModel),
		adminTokens: map[string]time.Time{},
	}
}

func (s *Server) SetShutdownFunc(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shutdownFn = fn
}

func (s *Server) Init(ctx context.Context) error {
	if err := s.store.Init(ctx); err != nil {
		return err
	}
	_ = s.store.SetSetting(ctx, "entry_open", "false")
	raw, err := s.store.GetSetting(ctx, "quiz_yaml")
	if err == nil && strings.TrimSpace(raw) != "" {
		parsed, pErr := quiz.Parse([]byte(raw))
		if pErr == nil {
			s.currentQuiz = parsed
		}
	}
	return nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.pageJoin)
	mux.HandleFunc("/join", s.pageJoin)
	mux.HandleFunc("/quiz", s.pageQuiz)
	mux.HandleFunc("/result", s.pageResult)
	mux.HandleFunc("/admin", s.pageAdmin)
	mux.HandleFunc("/assets/", s.serveAsset)
	mux.HandleFunc("/api/join", s.apiJoin)
	mux.HandleFunc("/api/entry-status", s.apiEntryStatus)
	mux.HandleFunc("/api/me", s.apiMe)
	mux.HandleFunc("/api/answer", s.apiSaveAnswer)
	mux.HandleFunc("/api/submit", s.apiSubmit)
	mux.HandleFunc("/api/result", s.apiResult)
	mux.HandleFunc("/api/admin/login", s.apiAdminLogin)
	mux.HandleFunc("/api/admin/state", s.apiAdminState)
	mux.HandleFunc("/api/admin/entry", s.apiAdminEntry)
	mux.HandleFunc("/api/admin/clear-attempts", s.apiAdminClearAttempts)
	mux.HandleFunc("/api/admin/shutdown", s.apiAdminShutdown)
	mux.HandleFunc("/api/admin/load-quiz", s.apiAdminLoadQuiz)
	mux.HandleFunc("/api/admin/live", s.apiAdminLive)
	mux.HandleFunc("/api/admin/ai-health", s.apiAdminAIHealth)
	mux.HandleFunc("/api/admin/attempts", s.apiAdminAttempts)
	mux.HandleFunc("/api/admin/attempt-detail", s.apiAdminAttemptDetail)
	mux.HandleFunc("/api/admin/export-csv", s.apiAdminExportCSV)
	mux.HandleFunc("/api/retry", s.apiRetry)
	mux.HandleFunc("/api/ai-summary", s.apiAISummary)
	mux.HandleFunc("/api/admin/ai-config", s.apiAdminAIConfig)
	mux.HandleFunc("/api/admin/quiz-files", s.apiAdminQuizFiles)
	mux.HandleFunc("/api/answer-image", s.apiUploadAnswerImage)
	mux.HandleFunc("/uploads/", s.serveUpload)
	return withCORS(mux)
}

func (s *Server) pageJoin(w http.ResponseWriter, _ *http.Request) {
	s.servePage(w, "web/join.html")
}

func (s *Server) pageQuiz(w http.ResponseWriter, _ *http.Request) {
	s.servePage(w, "web/quiz.html")
}

func (s *Server) pageResult(w http.ResponseWriter, _ *http.Request) {
	s.servePage(w, "web/result.html")
}

func (s *Server) pageAdmin(w http.ResponseWriter, _ *http.Request) {
	s.servePage(w, "web/admin.html")
}

func (s *Server) servePage(w http.ResponseWriter, path string) {
	f, err := webFS.ReadFile(path)
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(f)
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimPrefix(r.URL.Path, "/assets/")
	fp, ok := s.resolveAssetPath(target)
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if fp == "" {
		http.Error(w, "asset not found", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, fp)
}

func (s *Server) apiJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	open, err := s.isEntryOpen(r.Context())
	if err != nil || !open {
		http.Error(w, "入口未开放", http.StatusForbidden)
		return
	}
	var req struct {
		Name      string `json:"name"`
		StudentNo string `json:"student_no"`
		ClassName string `json:"class_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.StudentNo) == "" || strings.TrimSpace(req.ClassName) == "" {
		http.Error(w, "信息不完整", http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil {
		http.Error(w, "当前未加载题库", http.StatusServiceUnavailable)
		return
	}
	studentNo := strings.TrimSpace(req.StudentNo)
	token := newID() + newID()
	existing, existErr := s.store.GetInProgressAttempt(r.Context(), current.QuizID, studentNo)
	if existErr == nil && existing != nil {
		existing.SessionToken = token
		existing.Name = strings.TrimSpace(req.Name)
		existing.ClassName = strings.TrimSpace(req.ClassName)
		_ = s.store.UpdateAttemptSession(r.Context(), existing.ID, token, existing.Name, existing.ClassName)
		http.SetCookie(w, &http.Cookie{
			Name:     "student_token",
			Value:    token,
			Path:     "/",
			MaxAge:   7 * 24 * 3600,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	attemptID := newID()
	now := time.Now()
	a := &domain.Attempt{
		ID:           attemptID,
		SessionToken: token,
		QuizID:       current.QuizID,
		Name:         strings.TrimSpace(req.Name),
		StudentNo:    studentNo,
		ClassName:    strings.TrimSpace(req.ClassName),
		Status:       domain.StatusInProgress,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.CreateAttempt(r.Context(), a); err != nil {
		http.Error(w, "创建会话失败", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "student_token",
		Value:    token,
		Path:     "/",
		MaxAge:   7 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiEntryStatus(w http.ResponseWriter, r *http.Request) {
	open, err := s.isEntryOpen(r.Context())
	if err != nil {
		http.Error(w, "读取状态失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"entry_open": open})
}

func (s *Server) apiMe(w http.ResponseWriter, r *http.Request) {
	attempt, err := s.requireStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil {
		http.Error(w, "当前未加载题库", http.StatusServiceUnavailable)
		return
	}
	if attempt.QuizID != current.QuizID {
		http.Error(w, "该答题会话不属于当前题库", http.StatusForbidden)
		return
	}
	answers, err := s.store.GetAnswers(r.Context(), attempt.ID)
	if err != nil {
		http.Error(w, "读取答案失败", http.StatusInternalServerError)
		return
	}
	qs := shuffledQuestions(current, attempt.ID)
	safeQs := make([]map[string]any, 0, len(qs))
	for _, q := range qs {
		sq := map[string]any{
			"id":      q.ID,
			"type":    q.Type,
			"stem":    q.Stem,
			"options": q.Options,
		}
		if q.AllowMultiple {
			sq["allow_multiple"] = true
		}
		if q.Image != "" {
			sq["image"] = q.Image
		}
		if q.PoolTag != "" {
			sq["pool_tag"] = q.PoolTag
		}
		safeQs = append(safeQs, sq)
	}
	answerImages := map[string][]string{}
	for qid, val := range answers {
		imgs := domain.ShortAnswerImages(val)
		if len(imgs) > 0 {
			answerImages[qid] = imgs
		}
	}
	textAnswers := map[string]string{}
	for qid, val := range answers {
		isShort := false
		for _, q := range qs {
			if q.ID == qid && q.Type == domain.QuestionShortAnswer {
				isShort = true
				break
			}
		}
		if isShort {
			textAnswers[qid] = domain.ShortAnswerText(val)
		} else {
			textAnswers[qid] = val
		}
	}
	writeJSON(w, map[string]any{
		"attempt": map[string]any{
			"name":       attempt.Name,
			"student_no": attempt.StudentNo,
			"class_name": attempt.ClassName,
			"status":     attempt.Status,
		},
		"quiz":          map[string]any{"quiz_id": current.QuizID, "title": current.Title, "questions": safeQs},
		"answers":       textAnswers,
		"answer_images": answerImages,
	})
}

func (s *Server) apiSaveAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	attempt, err := s.requireStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	if attempt.Status != domain.StatusInProgress {
		http.Error(w, "已提交不可修改", http.StatusForbidden)
		return
	}
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil || attempt.QuizID != current.QuizID {
		http.Error(w, "该答题会话不属于当前题库", http.StatusForbidden)
		return
	}
	var req struct {
		QuestionID string `json:"question_id"`
		Answer     string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.QuestionID == "" {
		http.Error(w, "参数不完整", http.StatusBadRequest)
		return
	}
	q, ok := findQuestion(current, req.QuestionID)
	if !ok {
		http.Error(w, "题目不存在", http.StatusBadRequest)
		return
	}
	normalized := strings.TrimSpace(req.Answer)
	if normalized != "" {
		normalized, err = normalizeAnswer(*q, req.Answer)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if q.Type == domain.QuestionShortAnswer && normalized != "" {
		existing, _ := s.store.GetAnswers(r.Context(), attempt.ID)
		if old, ok := existing[req.QuestionID]; ok && old != "" {
			sa := domain.ParseShortAnswer(old)
			if len(sa.Images) > 0 {
				sa.Text = normalized
				normalized = domain.EncodeShortAnswer(sa)
			}
		}
	}
	err = s.store.SaveAnswer(r.Context(), domain.Answer{
		AttemptID:  attempt.ID,
		QuestionID: req.QuestionID,
		Value:      normalized,
		UpdatedAt:  time.Now(),
	})
	if err != nil {
		http.Error(w, "保存失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	attempt, err := s.requireStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	if attempt.Status == domain.StatusSubmitted {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil || attempt.QuizID != current.QuizID {
		http.Error(w, "该答题会话不属于当前题库", http.StatusForbidden)
		return
	}
	if _, err := s.store.SubmitAttempt(r.Context(), attempt.ID); err != nil {
		http.Error(w, "提交失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiResult(w http.ResponseWriter, r *http.Request) {
	attempt, err := s.requireStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	if attempt.Status != domain.StatusSubmitted {
		http.Error(w, "未提交", http.StatusForbidden)
		return
	}
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil || attempt.QuizID != current.QuizID {
		http.Error(w, "该答题会话不属于当前题库", http.StatusForbidden)
		return
	}
	res, err := s.buildResult(r.Context(), attempt)
	if err != nil {
		http.Error(w, "读取结果失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, res)
}

func (s *Server) apiRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	attempt, err := s.requireStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	if attempt.Status != domain.StatusSubmitted {
		http.Error(w, "当前状态不可重试", http.StatusForbidden)
		return
	}
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil || attempt.QuizID != current.QuizID {
		http.Error(w, "该答题会话不属于当前题库", http.StatusForbidden)
		return
	}
	token := newID() + newID()
	now := time.Now()
	a := &domain.Attempt{
		ID:           newID(),
		SessionToken: token,
		QuizID:       current.QuizID,
		Name:         attempt.Name,
		StudentNo:    attempt.StudentNo,
		ClassName:    attempt.ClassName,
		Status:       domain.StatusInProgress,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.CreateAttempt(r.Context(), a); err != nil {
		http.Error(w, "创建重试会话失败", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "student_token",
		Value:    token,
		Path:     "/",
		MaxAge:   7 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAISummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	attempt, err := s.requireStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	if attempt.Status != domain.StatusSubmitted {
		http.Error(w, "未提交", http.StatusForbidden)
		return
	}
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil || attempt.QuizID != current.QuizID {
		http.Error(w, "该答题会话不属于当前题库", http.StatusForbidden)
		return
	}
	saved, sErr := s.store.GetSummary(r.Context(), attempt.ID)
	if sErr == nil && strings.TrimSpace(saved) != "" {
		var summary domain.ResultSummary
		if json.Unmarshal([]byte(saved), &summary) == nil && len(summary.NextActions) > 0 {
			writeJSON(w, map[string]any{"summary": summary, "cached": true})
			return
		}
	}
	answers, err := s.store.GetAnswers(r.Context(), attempt.ID)
	if err != nil {
		http.Error(w, "读取答案失败", http.StatusInternalServerError)
		return
	}
	questions := shuffledQuestions(current, attempt.ID)
	correct := 0
	total := 0
	knowledgeGood := map[string]int{}
	knowledgeBad := map[string]int{}
	var wrongQs []ai.WrongQuestion
	for _, q := range questions {
		if q.Type == domain.QuestionSurvey || q.Type == domain.QuestionShortAnswer {
			continue
		}
		total++
		ans := answers[q.ID]
		if isCorrectAnswer(q, ans) {
			correct++
			if q.KnowledgeTag != "" {
				knowledgeGood[q.KnowledgeTag]++
			}
		} else {
			if q.KnowledgeTag != "" {
				knowledgeBad[q.KnowledgeTag]++
			}
			wrongQs = append(wrongQs, ai.WrongQuestion{
				Stem:          q.Stem,
				StudentAnswer: ans,
				CorrectAnswer: q.CorrectAnswer,
				KnowledgeTag:  q.KnowledgeTag,
				Explanation:   q.Explanation,
			})
		}
	}
	input := ai.SummarizeInput{
		QuizTitle:      current.Title,
		ScoreCorrect:   correct,
		ScoreTotal:     total,
		Strengths:      topKeys(knowledgeGood, 5),
		Weaknesses:     topKeys(knowledgeBad, 5),
		WrongQuestions: wrongQs,
	}
	summary, aiErr := s.aiClient.Summarize(r.Context(), input)
	if aiErr != nil {
		writeJSON(w, map[string]any{"summary": summary, "ai_error": aiErr.Error()})
		return
	}
	summaryJSON, _ := json.Marshal(summary)
	_ = s.store.UpsertSummary(r.Context(), attempt.ID, string(summaryJSON))
	writeJSON(w, map[string]any{"summary": summary, "cached": false})
}

func (s *Server) apiAdminAIConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
		APIKey   string `json:"api_key"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	s.aiClient.UpdateConfig(req.Endpoint, req.APIKey, req.Model)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAdminQuizFiles(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	quizDir := filepath.Join(filepath.Dir(s.cfg.DataDir), "quiz")
	if info, err := os.Stat(quizDir); err != nil || !info.IsDir() {
		quizDir = "./quiz"
	}
	type quizFileItem struct {
		Course string `json:"course"`
		File   string `json:"file"`
		Path   string `json:"path"`
	}
	var items []quizFileItem
	entries, err := os.ReadDir(quizDir)
	if err != nil {
		writeJSON(w, map[string]any{"items": items})
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		courseName := entry.Name()
		courseDir := filepath.Join(quizDir, courseName)
		files, err := os.ReadDir(courseDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			name := f.Name()
			if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
				items = append(items, quizFileItem{
					Course: courseName,
					File:   name,
					Path:   filepath.Join(courseDir, name),
				})
			}
		}
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) apiAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Password != s.cfg.AdminPassword {
		http.Error(w, "密码错误", http.StatusUnauthorized)
		return
	}
	token := newID() + newID()
	s.mu.Lock()
	s.adminTokens[token] = time.Now().Add(24 * time.Hour)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     "admin_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAdminState(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	open, _ := s.isEntryOpen(r.Context())
	s.mu.RLock()
	q := s.currentQuiz
	s.mu.RUnlock()
	started := 0
	submitted := 0
	if q != nil {
		items, err := s.listAttemptsByQuizID(r.Context(), q.QuizID)
		if err == nil {
			started = len(items)
			submitted = countSubmitted(items)
		}
	}
	title := ""
	if q != nil {
		title = q.Title
	}
	writeJSON(w, map[string]any{
		"entry_open": open,
		"started":    started,
		"submitted":  submitted,
		"quiz_title": title,
	})
}

func (s *Server) apiAdminEntry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Open bool `json:"open"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := s.store.SetSetting(r.Context(), "entry_open", fmt.Sprintf("%v", req.Open)); err != nil {
		http.Error(w, "保存失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAdminClearAttempts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if !req.Confirm {
		http.Error(w, "need confirmation", http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil {
		http.Error(w, "当前未加载题库", http.StatusBadRequest)
		return
	}
	if err := s.store.ClearAttempts(r.Context(), current.QuizID); err != nil {
		http.Error(w, "清空失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAdminShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mu.RLock()
	fn := s.shutdownFn
	s.mu.RUnlock()
	if fn == nil {
		http.Error(w, "shutdown unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
	go func() {
		time.Sleep(200 * time.Millisecond)
		fn()
	}()
}

func (s *Server) apiAdminLoadQuiz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var raw []byte
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
		} else {
			raw = []byte(req.YAML)
		}
	}
	parsed, err := quiz.Parse(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = os.MkdirAll(filepath.Join(s.cfg.DataDir, "assets"), 0o755)
	if err := quiz.ValidateImagePaths(parsed, filepath.Join(s.cfg.DataDir, "assets"), s.cfg.QuizAssetsDir); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.SetSetting(r.Context(), "quiz_yaml", string(raw)); err != nil {
		http.Error(w, "保存题库失败", http.StatusInternalServerError)
		return
	}
	_ = s.store.SetSetting(r.Context(), "entry_open", "false")
	s.mu.Lock()
	s.currentQuiz = parsed
	s.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "count": len(parsed.Questions)})
}

func (s *Server) apiAdminLive(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			open, _ := s.isEntryOpen(r.Context())
			started := 0
			submitted := 0
			s.mu.RLock()
			q := s.currentQuiz
			s.mu.RUnlock()
			if q != nil {
				items, err := s.listAttemptsByQuizID(r.Context(), q.QuizID)
				if err == nil {
					started = len(items)
					submitted = countSubmitted(items)
				}
			}
			payload := map[string]any{"entry_open": open, "started": started, "submitted": submitted}
			b, _ := json.Marshal(payload)
			_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
			flusher.Flush()
		}
	}
}

func (s *Server) apiAdminAIHealth(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, s.aiClient.Health())
}

func (s *Server) apiAdminAttempts(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil {
		writeJSON(w, map[string]any{"items": []map[string]any{}})
		return
	}
	items, err := s.listAttemptsByQuizID(r.Context(), current.QuizID)
	if err != nil {
		http.Error(w, "读取失败", http.StatusInternalServerError)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, a := range items {
		correct := 0
		total := 0
		if current != nil {
			correct, total = s.calcScore(r.Context(), current, a.ID)
		}
		result = append(result, map[string]any{
			"id":         a.ID,
			"name":       a.Name,
			"student_no": a.StudentNo,
			"class_name": a.ClassName,
			"attempt_no": a.AttemptNo,
			"status":     a.Status,
			"correct":    correct,
			"total":      total,
			"updated_at": a.UpdatedAt,
		})
	}
	writeJSON(w, map[string]any{"items": result})
}

func (s *Server) apiAdminAttemptDetail(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "id 不能为空", http.StatusBadRequest)
		return
	}
	attempt, err := s.store.GetAttemptByID(r.Context(), id)
	if err != nil {
		http.Error(w, "未找到该学生记录", http.StatusNotFound)
		return
	}
	res, err := s.buildResult(r.Context(), attempt)
	if err != nil {
		http.Error(w, "读取详情失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, res)
}

func (s *Server) apiAdminExportCSV(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil {
		http.Error(w, "当前没有题库", http.StatusBadRequest)
		return
	}
	items, err := s.listAttemptsByQuizID(r.Context(), current.QuizID)
	if err != nil {
		http.Error(w, "读取失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="class_report.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	header := []string{"姓名", "学号", "班级", "状态", "尝试次数", "正确数", "总题数"}
	for idx := range current.Questions {
		header = append(header, fmt.Sprintf("第%d题", idx+1))
	}
	_ = cw.Write(header)
	for _, a := range items {
		correct, total := s.calcScore(r.Context(), current, a.ID)
		attemptNo := ""
		if a.AttemptNo > 0 {
			attemptNo = fmt.Sprintf("%d", a.AttemptNo)
		}
		row := []string{safeCSV(a.Name), safeCSV(a.StudentNo), safeCSV(a.ClassName), string(a.Status), attemptNo, fmt.Sprintf("%d", correct), fmt.Sprintf("%d", total)}
		answers, err := s.store.GetAnswers(r.Context(), a.ID)
		if err != nil {
			http.Error(w, "读取答案失败", http.StatusInternalServerError)
			return
		}
		for _, q := range current.Questions {
			val := answers[q.ID]
			if q.Type == domain.QuestionShortAnswer {
				val = domain.ShortAnswerText(val)
			}
			row = append(row, safeCSV(val))
		}
		_ = cw.Write(row)
	}
	cw.Flush()
}

func (s *Server) requireStudent(r *http.Request) (*domain.Attempt, error) {
	cookie, err := r.Cookie("student_token")
	if err != nil {
		return nil, err
	}
	a, err := s.store.GetAttemptByToken(r.Context(), cookie.Value)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Server) requireAdmin(r *http.Request) bool {
	cookie, err := r.Cookie("admin_token")
	if err != nil || cookie.Value == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.adminTokens[cookie.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.adminTokens, cookie.Value)
		return false
	}
	return true
}

func (s *Server) isEntryOpen(ctx context.Context) (bool, error) {
	val, err := s.store.GetSetting(ctx, "entry_open")
	if err != nil {
		return false, err
	}
	return val == "true", nil
}

func (s *Server) buildResult(ctx context.Context, attempt *domain.Attempt) (map[string]any, error) {
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil {
		return nil, errors.New("题库为空")
	}
	answers, err := s.store.GetAnswers(ctx, attempt.ID)
	if err != nil {
		return nil, err
	}
	questions := shuffledQuestions(current, attempt.ID)
	correct := 0
	total := 0
	perQuestion := make([]map[string]any, 0, len(questions))
	knowledgeGood := map[string]int{}
	knowledgeBad := map[string]int{}
	for _, q := range questions {
		ans := answers[q.ID]
		item := map[string]any{
			"id":          q.ID,
			"stem":        q.Stem,
			"type":        q.Type,
			"answer":      ans,
			"options":     q.Options,
			"image":       q.Image,
			"correct":     q.CorrectAnswer,
			"reference":   q.ReferenceAnswer,
			"explanation": q.Explanation,
			"is_multi":    q.Type == domain.QuestionMultiChoice || (q.Type == domain.QuestionSurvey && q.AllowMultiple),
			"is_survey":   q.Type == domain.QuestionSurvey || q.Type == domain.QuestionShortAnswer,
		}
		if q.Type == domain.QuestionShortAnswer {
			sa := domain.ParseShortAnswer(ans)
			item["answer"] = sa.Text
			imgs := sa.Images
			if imgs == nil {
				imgs = []string{}
			}
			item["answer_images"] = imgs
		}
		if q.AllowMultiple {
			item["allow_multiple"] = true
		}
		if q.Type != domain.QuestionSurvey && q.Type != domain.QuestionShortAnswer {
			total++
			ok := isCorrectAnswer(q, ans)
			item["is_correct"] = ok
			if ok {
				correct++
				if q.KnowledgeTag != "" {
					knowledgeGood[q.KnowledgeTag]++
				}
			} else if q.KnowledgeTag != "" {
				knowledgeBad[q.KnowledgeTag]++
			}
		}
		perQuestion = append(perQuestion, item)
	}
	summary := domain.ResultSummary{}
	_ = topKeys(knowledgeGood, 3)
	_ = topKeys(knowledgeBad, 3)
	return map[string]any{
		"quiz_title": current.Title,
		"student": map[string]any{
			"name":       attempt.Name,
			"student_no": attempt.StudentNo,
			"class_name": attempt.ClassName,
			"attempt_no": attempt.AttemptNo,
		},
		"score": map[string]any{
			"correct": correct,
			"total":   total,
		},
		"questions": perQuestion,
		"summary":   summary,
	}, nil
}

func (s *Server) calcScore(ctx context.Context, q *domain.Quiz, attemptID string) (int, int) {
	answers, err := s.store.GetAnswers(ctx, attemptID)
	if err != nil {
		return 0, 0
	}
	questions := shuffledQuestions(q, attemptID)
	correct := 0
	total := 0
	for _, item := range questions {
		if item.Type == domain.QuestionSurvey || item.Type == domain.QuestionShortAnswer {
			continue
		}
		total++
		if isCorrectAnswer(item, answers[item.ID]) {
			correct++
		}
	}
	return correct, total
}

func (s *Server) resolveAssetPath(raw string) (string, bool) {
	name := filepath.Clean(strings.TrimSpace(raw))
	if name == "" || name == "." {
		return "", false
	}
	if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
		return "", false
	}
	candidates := []string{
		filepath.Join(s.cfg.QuizAssetsDir, name),
		filepath.Join(s.cfg.DataDir, "assets", name),
	}
	for _, fp := range candidates {
		if _, err := os.Stat(fp); err == nil {
			return fp, true
		}
	}
	return "", true
}

func (s *Server) apiUploadAnswerImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	attempt, err := s.requireStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	if attempt.Status != domain.StatusInProgress {
		http.Error(w, "已提交不可修改", http.StatusForbidden)
		return
	}
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil || attempt.QuizID != current.QuizID {
		http.Error(w, "该答题会话不属于当前题库", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "文件过大或格式错误", http.StatusBadRequest)
		return
	}
	questionID := strings.TrimSpace(r.FormValue("question_id"))
	if questionID == "" {
		http.Error(w, "参数不完整", http.StatusBadRequest)
		return
	}
	q, ok := findQuestion(current, questionID)
	if !ok {
		http.Error(w, "题目不存在", http.StatusBadRequest)
		return
	}
	if q.Type != domain.QuestionShortAnswer {
		http.Error(w, "该题型不支持图片上传", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("image")
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
	if len(data) < 4 {
		http.Error(w, "文件太小", http.StatusBadRequest)
		return
	}
	isJPEG := data[0] == 0xFF && data[1] == 0xD8
	isPNG := data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47
	if !isJPEG && !isPNG {
		http.Error(w, "仅支持 JPEG/PNG 格式图片", http.StatusBadRequest)
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "quiz",
		safePathPart(attempt.ClassName),
		safePathPart(current.QuizID),
		safePathPart(attempt.Name+"_"+attempt.StudentNo))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "创建目录失败", http.StatusInternalServerError)
		return
	}
	filename := fmt.Sprintf("q_%s_%d.jpg", questionID, time.Now().UnixMilli())
	if err := os.WriteFile(filepath.Join(dir, filename), data, 0o644); err != nil {
		http.Error(w, "写入文件失败", http.StatusInternalServerError)
		return
	}
	urlPath := "/uploads/" +
		safePathPart(attempt.ClassName) + "/" +
		safePathPart(current.QuizID) + "/" +
		safePathPart(attempt.Name+"_"+attempt.StudentNo) + "/" +
		filename
	answers, _ := s.store.GetAnswers(r.Context(), attempt.ID)
	sa := domain.ParseShortAnswer(answers[questionID])
	sa.Images = append(sa.Images, urlPath)
	encoded := domain.EncodeShortAnswer(sa)
	_ = s.store.SaveAnswer(r.Context(), domain.Answer{
		AttemptID:  attempt.ID,
		QuestionID: questionID,
		Value:      encoded,
		UpdatedAt:  time.Now(),
	})
	writeJSON(w, map[string]any{"ok": true, "url": urlPath, "images": sa.Images})
}

func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/uploads/")
	cleaned := filepath.Clean(rel)
	if cleaned == "." || strings.Contains(cleaned, "..") || filepath.IsAbs(cleaned) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	fp := filepath.Join(s.cfg.DataDir, "quiz", cleaned)
	http.ServeFile(w, r, fp)
}

func safePathPart(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\x00':
			return '_'
		}
		return r
	}, s)
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	return s
}

func findQuestion(qz *domain.Quiz, id string) (*domain.Question, bool) {
	for i := range qz.Questions {
		if qz.Questions[i].ID == id {
			return &qz.Questions[i], true
		}
	}
	return nil, false
}

func normalizeAnswer(q domain.Question, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("答案不能为空")
	}
	if q.Type == domain.QuestionShortAnswer {
		return raw, nil
	}
	opt := map[string]struct{}{}
	for _, it := range q.Options {
		opt[it.Key] = struct{}{}
	}
	normalizeMulti := func(v string) (string, error) {
		parts := strings.Split(v, ",")
		seen := map[string]struct{}{}
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			k := strings.TrimSpace(p)
			if k == "" {
				continue
			}
			if _, ok := opt[k]; !ok {
				return "", errors.New("答案选项无效")
			}
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, k)
		}
		if len(out) == 0 {
			return "", errors.New("至少选择一个选项")
		}
		sort.Strings(out)
		return strings.Join(out, ","), nil
	}
	switch q.Type {
	case domain.QuestionMultiChoice:
		return normalizeMulti(raw)
	case domain.QuestionSurvey:
		if q.AllowMultiple {
			return normalizeMulti(raw)
		}
		if _, ok := opt[raw]; !ok {
			return "", errors.New("答案选项无效")
		}
		return raw, nil
	case domain.QuestionSingleChoice, domain.QuestionYesNo:
		if _, ok := opt[raw]; !ok {
			return "", errors.New("答案选项无效")
		}
		return raw, nil
	default:
		return "", errors.New("暂不支持的题型")
	}
}

func isCorrectAnswer(q domain.Question, ans string) bool {
	if strings.TrimSpace(ans) == "" {
		return false
	}
	switch q.Type {
	case domain.QuestionMultiChoice:
		normAns, err1 := normalizeAnswer(q, ans)
		normCorrect, err2 := normalizeAnswer(q, q.CorrectAnswer)
		if err1 != nil || err2 != nil {
			return false
		}
		return normAns == normCorrect
	default:
		return ans == q.CorrectAnswer
	}
}

func topKeys(m map[string]int, n int) []string {
	type kv struct {
		Key string
		Val int
	}
	vs := make([]kv, 0, len(m))
	for k, v := range m {
		vs = append(vs, kv{k, v})
	}
	for i := 0; i < len(vs); i++ {
		for j := i + 1; j < len(vs); j++ {
			if vs[j].Val > vs[i].Val {
				vs[i], vs[j] = vs[j], vs[i]
			}
		}
	}
	out := []string{}
	for i := 0; i < len(vs) && i < n; i++ {
		out = append(out, vs[i].Key)
	}
	return out
}

func shuffledQuestions(q *domain.Quiz, attemptID string) []domain.Question {
	items := make([]domain.Question, 0, len(q.Questions))
	tailFixed := make([]domain.Question, 0)
	if q.Sampling != nil && len(q.Sampling.Groups) > 0 {
		byTag := map[string][]domain.Question{}
		for _, question := range q.Questions {
			tag := strings.TrimSpace(question.PoolTag)
			if tag == "" {
				if question.Type == domain.QuestionShortAnswer {
					tailFixed = append(tailFixed, question)
				} else {
					items = append(items, question)
				}
				continue
			}
			byTag[tag] = append(byTag[tag], question)
		}
		for _, group := range q.Sampling.Groups {
			tag := strings.TrimSpace(group.Tag)
			pool := byTag[tag]
			if len(pool) == 0 || group.Pick <= 0 {
				continue
			}
			r := seededRandom(q.QuizID + ":" + attemptID + ":" + tag)
			r.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
			pick := group.Pick
			if pick > len(pool) {
				pick = len(pool)
			}
			items = append(items, pool[:pick]...)
		}
	} else {
		for _, question := range q.Questions {
			if question.Type == domain.QuestionShortAnswer {
				tailFixed = append(tailFixed, question)
				continue
			}
			items = append(items, question)
		}
	}
	r := seededRandom(q.QuizID + ":" + attemptID + ":final")
	r.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
	for i := range items {
		items[i] = shuffleQuestionOptions(items[i], q.QuizID, attemptID)
	}
	for i := range tailFixed {
		tailFixed[i] = shuffleQuestionOptions(tailFixed[i], q.QuizID, attemptID)
	}
	items = append(items, tailFixed...)
	return items
}

func shuffleQuestionOptions(question domain.Question, quizID, attemptID string) domain.Question {
	if len(question.Options) <= 1 {
		return question
	}
	opts := append([]domain.Option(nil), question.Options...)
	r := seededRandom(quizID + ":" + attemptID + ":options:" + question.ID)
	r.Shuffle(len(opts), func(i, j int) { opts[i], opts[j] = opts[j], opts[i] })
	question.Options = opts
	return question
}

func (s *Server) listAttemptsByQuizID(ctx context.Context, quizID string) ([]domain.Attempt, error) {
	all, err := s.store.ListAttempts(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]domain.Attempt, 0, len(all))
	for _, item := range all {
		if item.QuizID == quizID {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func countSubmitted(items []domain.Attempt) int {
	sum := 0
	for _, item := range items {
		if item.Status == domain.StatusSubmitted {
			sum++
		}
	}
	return sum
}

func formatQuestionCorrectForCSV(q domain.Question) string {
	switch q.Type {
	case domain.QuestionShortAnswer:
		return q.ReferenceAnswer
	case domain.QuestionSurvey:
		return ""
	case domain.QuestionMultiChoice:
		keys := strings.Split(q.CorrectAnswer, ",")
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			text := ""
			for _, opt := range q.Options {
				if opt.Key == k {
					text = opt.Text
					break
				}
			}
			if text != "" {
				parts = append(parts, k+":"+text)
			} else {
				parts = append(parts, k)
			}
		}
		return strings.Join(parts, "；")
	default:
		key := strings.TrimSpace(q.CorrectAnswer)
		if key == "" {
			return ""
		}
		for _, opt := range q.Options {
			if opt.Key == key {
				if opt.Text != "" {
					return key + ":" + opt.Text
				}
				return key
			}
		}
		return key
	}
}

func formatQuestionOptionsForCSV(q domain.Question) string {
	if len(q.Options) == 0 {
		return ""
	}
	parts := make([]string, 0, len(q.Options))
	for _, opt := range q.Options {
		text := strings.TrimSpace(opt.Text)
		image := strings.TrimSpace(opt.Image)
		switch {
		case text != "" && image != "":
			parts = append(parts, fmt.Sprintf("%s:%s(图片:%s)", opt.Key, text, image))
		case text != "":
			parts = append(parts, fmt.Sprintf("%s:%s", opt.Key, text))
		case image != "":
			parts = append(parts, fmt.Sprintf("%s:[图片:%s]", opt.Key, image))
		default:
			parts = append(parts, opt.Key)
		}
	}
	return strings.Join(parts, " | ")
}

func seededRandom(key string) *mrand.Rand {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(key))
	seed := hasher.Sum64()
	return mrand.New(mrand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

func newID() string {
	buf := make([]byte, 16)
	_, _ = crand.Read(buf)
	return hex.EncodeToString(buf)
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(data)
}

func safeCSV(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@':
		return "'" + s
	}
	return s
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}
