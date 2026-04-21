package app

import (
	"archive/zip"
	"bytes"
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
	"log"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"course-assistant/internal/ai"
	"course-assistant/internal/domain"
	"course-assistant/internal/quiz"
	"course-assistant/internal/store"

	qrcode "github.com/skip2/go-qrcode"
	"gopkg.in/yaml.v3"
	"golang.org/x/crypto/bcrypt"
)

//go:embed web/*
var webFS embed.FS

type Config struct {
	Addr          string
	BaseURL       string
	AdminPassword string // fallback only; auth is DB-based
	DataDir       string
	MetadataDir   string
	AIEndpoint    string
	AIKey         string
	AIModel       string
}

type authSession struct {
	TeacherID string
	Role      domain.UserRole
	Expiry    time.Time
}

type Server struct {
	cfg                 Config
	store               store.Store
	aiClient            *ai.Client
	mu                  sync.RWMutex
	courseQuizzes       map[int]*domain.Quiz // course_id -> loaded quiz
	courseQuizAssetDirs map[int]string       // course_id -> metadata quiz dir (for asset serving)
	authTokens          map[string]authSession
	shutdownFn          func()

	// currentQuiz is intentionally never populated. It exists only so that
	// a handful of now-unreachable legacy handlers (kept for binary size
	// parity while the frontend transitions) continue to compile. All real
	// quiz resolution happens through courseQuizzes[course_id].
	currentQuiz *domain.Quiz
}

func New(cfg Config, st store.Store) *Server {
	return &Server{
		cfg:                 cfg,
		store:               st,
		aiClient:            ai.NewClient(cfg.AIEndpoint, cfg.AIKey, cfg.AIModel),
		courseQuizzes:       map[int]*domain.Quiz{},
		courseQuizAssetDirs: map[int]string{},
		authTokens:          map[string]authSession{},
	}
}

func (s *Server) pathPrefix() string {
	if s.cfg.BaseURL == "" {
		return ""
	}
	u, err := url.Parse(s.cfg.BaseURL)
	if err != nil || u.Path == "" || u.Path == "/" {
		return ""
	}
	return strings.TrimRight(u.Path, "/")
}

func (s *Server) cookiePath() string {
	pfx := s.pathPrefix()
	if pfx != "" {
		return pfx + "/"
	}
	return "/"
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
	// Teacher bootstrap is now handled by cmd/migrate upgrade.
	// We no longer force entry_open=false on every boot (that would
	// silently disable in-progress quizzes across process restarts).
	s.restoreCourseQuizzes(ctx)
	return nil
}

func (s *Server) restoreCourseQuizzes(ctx context.Context) {
	courses, err := s.store.ListAllCourses(ctx)
	if err != nil {
		return
	}
	for _, c := range courses {
		cs, err := s.store.GetCourseState(ctx, c.ID)
		if err != nil || cs.QuizYAML == "" {
			continue
		}
		parsed, err := quiz.Parse([]byte(cs.QuizYAML))
		if err == nil {
			s.courseQuizzes[c.ID] = parsed
			if cs.QuizSourcePath != "" {
				dir := filepath.Dir(cs.QuizSourcePath)
				if strings.Contains(dir, s.cfg.MetadataDir) {
					s.courseQuizAssetDirs[c.ID] = dir
				}
			}
		}
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.pageStudent)
	mux.HandleFunc("/join", s.redirectJoinToStudent)
	mux.HandleFunc("/s/", s.redirectShortInvite) // /s/:code → /?code=:code
	mux.HandleFunc("/quiz", s.pageQuiz)
	mux.HandleFunc("/result", s.pageResult)
	mux.HandleFunc("/admin", s.pageAdmin)
	mux.HandleFunc("/teacher", s.pageTeacher)
	mux.HandleFunc("/t", s.pageTeacher) // short alias
	mux.HandleFunc("/student", s.redirectStudentToRoot)
	mux.HandleFunc("/assets/", s.serveAsset)
	mux.HandleFunc("/static/", s.serveStatic)
	mux.HandleFunc("/api/join", s.apiJoin)
	mux.HandleFunc("/api/homework/courses", s.apiHomeworkCourses)
	mux.HandleFunc("/api/homework/assignment-ids", s.apiHomeworkAssignmentIDs)
	mux.HandleFunc("/api/homework/assignment-preview", s.apiHomeworkAssignmentPreview)
	mux.HandleFunc("/api/homework/assignments", s.apiHomeworkAssignments)
	mux.HandleFunc("/api/homework/assignment-file", s.apiHomeworkAssignmentFile)
	mux.HandleFunc("/api/homework/session", s.apiHomeworkSession)
	mux.HandleFunc("/api/homework/submission", s.apiHomeworkSubmission)
	mux.HandleFunc("/api/homework/upload", s.apiHomeworkUpload)
	mux.HandleFunc("/api/homework/download", s.apiHomeworkDownload)
	mux.HandleFunc("/api/homework/delete", s.apiHomeworkDelete)
	mux.HandleFunc("/api/homework/others/upload", s.apiHomeworkOthersUpload)
	mux.HandleFunc("/api/homework/others/list", s.apiHomeworkOthersList)
	mux.HandleFunc("/api/homework/others/download", s.apiHomeworkOthersDownload)
	mux.HandleFunc("/api/homework/others/delete", s.apiHomeworkOthersDelete)
	mux.HandleFunc("/api/homework/others/rename", s.apiHomeworkOthersRename)
	mux.HandleFunc("/api/entry-status", s.apiEntryStatus)
	mux.HandleFunc("/api/me", s.apiMe)
	mux.HandleFunc("/api/student-signout", s.apiStudentSignout)
	mux.HandleFunc("/api/answer", s.apiSaveAnswer)
	mux.HandleFunc("/api/submit", s.apiSubmit)
	mux.HandleFunc("/api/result", s.apiResult)
	mux.HandleFunc("/api/auth/login", s.apiAuthLogin)
	mux.HandleFunc("/api/auth/logout", s.apiAuthLogout)
	mux.HandleFunc("/api/auth/me", s.apiAuthMe)
	mux.HandleFunc("/api/auth/change-password", s.apiAuthChangePassword)
	mux.HandleFunc("/api/admin/overview", s.apiAdminOverview)
	mux.HandleFunc("/api/admin/teachers", s.apiAdminTeachers)
	mux.HandleFunc("/api/admin/teachers/delete", s.apiAdminTeacherDelete)
	mux.HandleFunc("/api/admin/teachers/reset-password", s.apiAdminTeacherResetPassword)
	// New /api/system/* aliases for the slimmed-down admin page. These call
	// the exact same handlers as /api/admin/*; the old paths remain for
	// now so clients that haven't updated yet keep working.
	mux.HandleFunc("/api/system/overview", s.apiAdminOverview)
	mux.HandleFunc("/api/system/teachers", s.apiAdminTeachers)
	mux.HandleFunc("/api/system/teachers/delete", s.apiAdminTeacherDelete)
	mux.HandleFunc("/api/system/teachers/reset-password", s.apiAdminTeacherResetPassword)
	mux.HandleFunc("/api/system/ai-health", s.apiAdminAIHealth)
	mux.HandleFunc("/api/system/ai-config", s.apiAdminAIConfig)
	mux.HandleFunc("/api/system/shutdown", s.apiAdminShutdown)
	mux.HandleFunc("/api/system/login", s.apiAdminLogin)
	mux.HandleFunc("/api/admin/courses", s.apiTeacherCourses)
	mux.HandleFunc("/api/teacher/courses", s.apiTeacherCourses)
	mux.HandleFunc("/api/teacher/courses/delete", s.apiTeacherCourseDelete)
	mux.HandleFunc("/api/teacher/courses/load-quiz", s.apiTeacherCourseLoadQuiz)
	mux.HandleFunc("/api/teacher/courses/entry", s.apiTeacherCourseEntry)
	mux.HandleFunc("/api/teacher/courses/state", s.apiTeacherCourseState)
	mux.HandleFunc("/api/teacher/courses/quiz-files", s.apiTeacherCourseQuizFiles)
	mux.HandleFunc("/api/teacher/courses/quiz-bank", s.apiTeacherCourseQuizBank)
	mux.HandleFunc("/api/teacher/courses/quiz-bank/upload", s.apiTeacherCourseQuizBankUpload)
	mux.HandleFunc("/api/teacher/courses/quiz-bank/load", s.apiTeacherCourseQuizBankLoad)
	mux.HandleFunc("/api/teacher/courses/quiz-bank/delete", s.apiTeacherCourseQuizBankDelete)
	mux.HandleFunc("/api/teacher/courses/quiz-bank/content", s.apiTeacherCourseQuizBankContent)
	mux.HandleFunc("/api/teacher/courses/quiz-bank/save", s.apiTeacherCourseQuizBankSave)
	mux.HandleFunc("/api/teacher/courses/quiz-bank/upload-image", s.apiTeacherCourseQuizBankUploadImage)
	mux.HandleFunc("/api/teacher/courses/quiz-bank/ai-generate", s.apiTeacherCourseQuizBankAIGenerate)
	mux.HandleFunc("/api/teacher/courses/quiz-bank/ai-autofill", s.apiTeacherCourseQuizBankAIAutoFill)
	mux.HandleFunc("/api/teacher/courses/quiz-bank/rename", s.apiTeacherCourseQuizBankRename)
	mux.HandleFunc("/api/teacher/courses/quiz-bank/download", s.apiTeacherCourseQuizBankDownload)
	mux.HandleFunc("/api/teacher/courses/quiz-bank/download-all", s.apiTeacherCourseQuizBankDownloadAll)
	mux.HandleFunc("/quiz-editor", s.pageQuizEditor)
	mux.HandleFunc("/api/teacher/courses/attempts", s.apiTeacherCourseAttempts)
	mux.HandleFunc("/api/teacher/courses/attempt-detail", s.apiTeacherCourseAttemptDetail)
	mux.HandleFunc("/api/teacher/courses/live", s.apiTeacherCourseLive)
	mux.HandleFunc("/api/teacher/courses/clear-attempts", s.apiTeacherCourseClearAttempts)
	mux.HandleFunc("/api/teacher/courses/fix-legacy-attempts", s.apiTeacherCourseFixLegacyAttempts)
	mux.HandleFunc("/api/teacher/courses/fix-all-legacy", s.apiTeacherCourseFixAllLegacy)
	mux.HandleFunc("/api/teacher/courses/export-csv", s.apiTeacherCourseExportCSV)
	mux.HandleFunc("/api/teacher/courses/summary", func(w http.ResponseWriter, r *http.Request) {
		s.apiTeacherCourseSummary(w, r)
	})
	mux.HandleFunc("/api/teacher/courses/history-summary", func(w http.ResponseWriter, r *http.Request) {
		s.apiTeacherCourseHistorySummary(w, r)
	})
	mux.HandleFunc("/api/teacher/courses/materials", s.apiTeacherCourseMaterials)
	mux.HandleFunc("/api/teacher/courses/materials/upload", s.apiTeacherCourseMaterialUpload)
	mux.HandleFunc("/api/teacher/courses/materials/delete", s.apiTeacherCourseMaterialDelete)
	mux.HandleFunc("/api/teacher/courses/materials/rename", s.apiTeacherCourseMaterialRename)
	mux.HandleFunc("/api/teacher/courses/materials/visibility", s.apiTeacherCourseMaterialVisibility)
	mux.HandleFunc("/api/teacher/courses/homework/assignments", s.apiTeacherCourseHomeworkAssignments)
	mux.HandleFunc("/api/teacher/courses/homework/assignments/upload", s.apiTeacherCourseHomeworkAssignmentUpload)
	mux.HandleFunc("/api/teacher/courses/homework/assignments/delete", s.apiTeacherCourseHomeworkAssignmentDelete)
	mux.HandleFunc("/api/teacher/courses/homework/assignments/delete-file", s.apiTeacherCourseHomeworkAssignmentDeleteFile)
	mux.HandleFunc("/api/teacher/courses/homework/assignments/visibility", s.apiTeacherCourseHomeworkAssignmentVisibility)
	mux.HandleFunc("/api/teacher/courses/homework/submissions", s.apiTeacherCourseHomeworkSubmissions)
	mux.HandleFunc("/api/teacher/courses/homework/submissions/download", s.apiTeacherCourseHomeworkSubmissionDownload)
	mux.HandleFunc("/api/teacher/courses/homework/submissions/archive", s.apiTeacherCourseHomeworkSubmissionArchive)
	mux.HandleFunc("/api/teacher/courses/homework/submissions/delete", s.apiTeacherCourseHomeworkSubmissionDelete)
	mux.HandleFunc("/api/teacher/courses/homework/archive-all", s.apiTeacherCourseHomeworkArchiveAll)
	mux.HandleFunc("/api/teacher/courses/invite-qr", s.apiTeacherCourseInviteQR)
	mux.HandleFunc("/api/course", s.apiCourseByInviteCode)
	// System-level admin APIs (teachers, AI config, system status).
	mux.HandleFunc("/api/admin/login", s.apiAdminLogin)
	mux.HandleFunc("/api/admin/shutdown", s.apiAdminShutdown)
	mux.HandleFunc("/api/admin/ai-health", s.apiAdminAIHealth)
	mux.HandleFunc("/api/admin/ai-config", s.apiAdminAIConfig)
	mux.HandleFunc("/api/admin/update/check", s.apiAdminUpdateCheck)
	mux.HandleFunc("/api/admin/update/pull", s.apiAdminUpdatePull)
	mux.HandleFunc("/api/admin/update/restart", s.apiAdminUpdateRestart)
	mux.HandleFunc("/api/retry", s.apiRetry)
	mux.HandleFunc("/api/ai-summary", s.apiAISummary)
	// Legacy teaching-level admin routes (entry/load-quiz/live/attempts/
	// homework/pdfs/admin-summary/export-csv) were removed in the multi-
	// tenant cleanup — all of these now live under /api/teacher/courses/*
	// or the /api/system/* namespace.
	mux.HandleFunc("/api/admin/entry", gone410("use /api/teacher/courses/entry"))
	mux.HandleFunc("/api/admin/state", gone410("use /api/teacher/courses/state"))
	mux.HandleFunc("/api/admin/load-quiz", gone410("use /api/teacher/courses/load-quiz"))
	mux.HandleFunc("/api/admin/live", gone410("use /api/teacher/courses/live"))
	mux.HandleFunc("/api/admin/attempts", gone410("use /api/teacher/courses/attempts"))
	mux.HandleFunc("/api/admin/attempt-detail", gone410("use /api/teacher/courses/attempt-detail"))
	mux.HandleFunc("/api/admin/clear-attempts", gone410("use /api/teacher/courses/clear-attempts"))
	mux.HandleFunc("/api/admin/export-csv", gone410("use /api/teacher/courses/export-csv"))
	mux.HandleFunc("/api/admin/quiz-files", gone410("use /api/teacher/courses/quiz-files"))
	mux.HandleFunc("/api/admin/admin-summary", gone410("use /api/teacher/courses/summary"))
	mux.HandleFunc("/api/admin/homework/assignments", gone410("use /api/teacher/courses/homework/assignments"))
	mux.HandleFunc("/api/admin/homework/assignments/upload", gone410("use /api/teacher/courses/homework/assignments/upload"))
	mux.HandleFunc("/api/admin/homework/assignments/delete-file", gone410("use /api/teacher/courses/homework/assignments/delete"))
	mux.HandleFunc("/api/admin/homework/assignments/delete", gone410("use /api/teacher/courses/homework/assignments/delete"))
	mux.HandleFunc("/api/admin/homework/assignments/visibility", gone410("use /api/teacher/courses/homework/assignments/visibility"))
	mux.HandleFunc("/api/admin/homework/assignments/rename", gone410("use /api/teacher/courses/homework/assignments/rename"))
	mux.HandleFunc("/api/admin/homework/submissions", gone410("use /api/teacher/courses/homework/submissions"))
	mux.HandleFunc("/api/admin/homework/submission", gone410("use /api/teacher/courses/homework/submissions"))
	mux.HandleFunc("/api/admin/homework/report", gone410("use /api/teacher/courses/homework/submissions"))
	mux.HandleFunc("/api/admin/homework/code", gone410("use /api/teacher/courses/homework/submissions"))
	mux.HandleFunc("/api/admin/homework/extra", gone410("use /api/teacher/courses/homework/submissions"))
	mux.HandleFunc("/api/admin/homework/archive", gone410("use /api/teacher/courses/homework/archive-all"))
	mux.HandleFunc("/api/admin/homework/archive-all", gone410("use /api/teacher/courses/homework/archive-all"))
	mux.HandleFunc("/api/admin/pdfs/upload", gone410("use /api/teacher/courses/materials/upload"))
	mux.HandleFunc("/api/admin/pdfs/delete", gone410("use /api/teacher/courses/materials/delete"))
	mux.HandleFunc("/api/admin/pdfs/rename", gone410("use /api/teacher/courses/materials/rename"))
	mux.HandleFunc("/api/admin/pdfs/visibility", gone410("use /api/teacher/courses/materials/visibility"))
	mux.HandleFunc("/api/admin/materials", gone410("use /api/teacher/courses/materials"))
	mux.HandleFunc("/api/materials", s.apiMaterials)
	mux.HandleFunc("/api/pdfs", s.apiPDFs)
	mux.HandleFunc("/ppt/", s.servePPT)
	mux.HandleFunc("/materials-files/", s.serveMaterialDownload)
	mux.HandleFunc("/api/answer-image", s.apiUploadAnswerImage)
	mux.HandleFunc("/api/answer-image/delete", s.apiDeleteAnswerImage)
	mux.HandleFunc("/uploads/", s.serveUpload)

	var handler http.Handler = withCORS(mux)
	pfx := s.pathPrefix()
	if pfx != "" {
		inner := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == pfx {
				http.Redirect(w, r, pfx+"/", http.StatusMovedPermanently)
				return
			}
			if strings.HasPrefix(r.URL.Path, pfx+"/") {
				http.StripPrefix(pfx, inner).ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		})
	}
	return handler
}

// gone410 returns a handler that replies 410 Gone with a short hint telling
// callers which modern endpoint replaces the removed legacy one.
func gone410(hint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		_, _ = io.WriteString(w, `{"error":"endpoint removed","hint":"`+hint+`"}`)
	}
}

// pageJoin and web/join.html were removed. /join is a compatibility redirect
// to the student SPA at / so existing QR codes keep working.
func (s *Server) redirectJoinToStudent(w http.ResponseWriter, r *http.Request) {
	target := s.pathPrefix() + "/"
	if code := r.URL.Query().Get("code"); code != "" {
		target += "?code=" + url.QueryEscape(code)
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// redirectStudentToRoot canonicalises the student entry to /. The /student
// route used to render the student SPA directly; now it's just an alias.
func (s *Server) redirectStudentToRoot(w http.ResponseWriter, r *http.Request) {
	target := s.pathPrefix() + "/"
	if raw := r.URL.RawQuery; raw != "" {
		target += "?" + raw
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// redirectShortInvite handles /s/:code (QR-code friendly short path) and
// forwards to the landing SPA with the invite code pre-filled.
func (s *Server) redirectShortInvite(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimPrefix(r.URL.Path, "/s/")
	target := s.pathPrefix() + "/"
	if code != "" {
		target += "?code=" + url.QueryEscape(code)
	}
	http.Redirect(w, r, target, http.StatusFound)
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

func (s *Server) pageTeacher(w http.ResponseWriter, _ *http.Request) {
	s.servePage(w, "web/teacher.html")
}

func (s *Server) servePage(w http.ResponseWriter, path string) {
	f, err := webFS.ReadFile(path)
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	content := string(f)
	pfx := s.pathPrefix()
	if pfx != "" {
		injection := `<script>var __PREFIX='` + pfx + `';</script>`
		content = strings.Replace(content, "<head>", "<head>"+injection, 1)
		content = strings.ReplaceAll(content, "fetch('/", "fetch(__PREFIX+'/")
		content = strings.ReplaceAll(content, "location.href = '/", "location.href = __PREFIX+'/")
		content = strings.ReplaceAll(content, `src="/assets/${`, `src="${__PREFIX}/assets/${`)
		content = strings.ReplaceAll(content, "window.open('/", "window.open(__PREFIX+'/")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, content)
}

// serveStatic exposes the embedded web/static/* directory. Pure assets only
// (CSS/JS); they are kept in the embedded FS so the binary is single-file.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/static/")
	if rel == "" || strings.Contains(rel, "..") {
		http.NotFound(w, r)
		return
	}
	data, err := webFS.ReadFile("web/static/" + rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(rel, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(rel, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(data)
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
	var req struct {
		Name      string `json:"name"`
		StudentNo string `json:"student_no"`
		ClassName string `json:"class_name"`
		CourseID  int    `json:"course_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.StudentNo) == "" || strings.TrimSpace(req.ClassName) == "" {
		http.Error(w, "信息不完整", http.StatusBadRequest)
		return
	}

	courseID := req.CourseID
	if courseID <= 0 {
		http.Error(w, "缺少课程上下文（course_id）", http.StatusBadRequest)
		return
	}
	cs, err := s.store.GetCourseState(r.Context(), courseID)
	if err != nil || cs == nil || !cs.EntryOpen {
		http.Error(w, "入口未开放", http.StatusForbidden)
		return
	}
	s.mu.RLock()
	current := s.courseQuizzes[courseID]
	s.mu.RUnlock()
	if current == nil {
		http.Error(w, "当前未加载题库", http.StatusServiceUnavailable)
		return
	}
	studentNo := strings.TrimSpace(req.StudentNo)
	token := newID() + newID()
	existing, existErr := s.store.GetInProgressAttempt(r.Context(), current.QuizID, studentNo, courseID)
	if existErr == nil && existing != nil {
		existing.SessionToken = token
		existing.Name = strings.TrimSpace(req.Name)
		existing.ClassName = strings.TrimSpace(req.ClassName)
		_ = s.store.UpdateAttemptSession(r.Context(), existing.ID, token, existing.Name, existing.ClassName, courseID)
		http.SetCookie(w, &http.Cookie{
			Name:     "student_token",
			Value:    token,
			Path:     s.cookiePath(),
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
		CourseID:     courseID,
		Name:         strings.TrimSpace(req.Name),
		StudentNo:    studentNo,
		ClassName:    strings.TrimSpace(req.ClassName),
		Status:       domain.StatusInProgress,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.CreateAttempt(r.Context(), a); err != nil {
		log.Printf("[apiJoin] CreateAttempt failed: %v", err)
		http.Error(w, "创建会话失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "student_token",
		Value:    token,
		Path:     s.cookiePath(),
		MaxAge:   7 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiEntryStatus(w http.ResponseWriter, r *http.Request) {
	var courseID int
	if s := r.URL.Query().Get("course_id"); s != "" {
		fmt.Sscanf(s, "%d", &courseID)
	}
	if courseID <= 0 {
		http.Error(w, "缺少 course_id", http.StatusBadRequest)
		return
	}
	cs, err := s.store.GetCourseState(r.Context(), courseID)
	if err != nil {
		writeJSON(w, map[string]any{"entry_open": false})
		return
	}
	s.mu.RLock()
	q := s.courseQuizzes[courseID]
	s.mu.RUnlock()
	quizTitle := ""
	if q != nil {
		quizTitle = q.Title
	}
	writeJSON(w, map[string]any{"entry_open": cs.EntryOpen, "quiz_title": quizTitle})
}

func (s *Server) apiMe(w http.ResponseWriter, r *http.Request) {
	attempt, err := s.requireStudent(r)
	if err != nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	current := s.resolveQuizForAttempt(attempt)
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
		if q.Type == domain.QuestionShortAnswer && strings.TrimSpace(q.ShortAnswerMode) != "" {
			sq["short_answer_mode"] = q.ShortAnswerMode
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

func (s *Server) apiStudentSignout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "student_token",
		Value:    "",
		Path:     s.cookiePath(),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, map[string]any{"ok": true})
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
	current := s.resolveQuizForAttempt(attempt)
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
	current := s.resolveQuizForAttempt(attempt)
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
	current := s.resolveQuizForAttempt(attempt)
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
	current := s.resolveQuizForAttempt(attempt)
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
		CourseID:     attempt.CourseID,
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
		Path:     s.cookiePath(),
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
	current := s.resolveQuizForAttempt(attempt)
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
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		// Never leak the API key itself; the page only needs to show
		// endpoint + model + whether a key is configured.
		h := s.aiClient.Health()
		writeJSON(w, map[string]any{
			"endpoint":        h["endpoint"],
			"model":           h["model"],
			"key_loaded":      h["key_loaded"],
			"last_success_at": h["last_success_at"],
			"last_error":      h["last_error"],
		})
	case http.MethodPost:
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
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) apiAdminQuizFiles(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	type quizFileItem struct {
		Course string `json:"course"`
		File   string `json:"file"`
		Path   string `json:"path"`
	}
	var items []quizFileItem
	teachers, err := os.ReadDir(s.cfg.MetadataDir)
	if err != nil {
		writeJSON(w, map[string]any{"items": items})
		return
	}
	for _, teacher := range teachers {
		if !teacher.IsDir() {
			continue
		}
		courses, err := os.ReadDir(filepath.Join(s.cfg.MetadataDir, teacher.Name()))
		if err != nil {
			continue
		}
		for _, course := range courses {
			if !course.IsDir() {
				continue
			}
			quizDir := filepath.Join(s.cfg.MetadataDir, teacher.Name(), course.Name(), "quiz")
			quizBanks, err := os.ReadDir(quizDir)
			if err != nil {
				continue
			}
			for _, bank := range quizBanks {
				if !bank.IsDir() {
					continue
				}
				bankDir := filepath.Join(quizDir, bank.Name())
				files, err := os.ReadDir(bankDir)
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
							Course: course.Name(),
							File:   name,
							Path:   filepath.Join(bankDir, name),
						})
					}
				}
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
	teachers, _ := s.store.ListTeachers(r.Context())
	for _, t := range teachers {
		if t.Role == domain.RoleAdmin && checkPassword(t.PasswordHash, req.Password) {
			token := newID() + newID()
			sess := authSession{TeacherID: t.ID, Role: domain.RoleAdmin, Expiry: time.Now().Add(24 * time.Hour)}
			s.mu.Lock()
			s.authTokens[token] = sess
			s.mu.Unlock()
			http.SetCookie(w, &http.Cookie{
				Name: "auth_token", Value: token, Path: s.cookiePath(),
				HttpOnly: true, SameSite: http.SameSiteLaxMode,
			})
			writeJSON(w, map[string]any{"ok": true})
			return
		}
	}
	// Note: the legacy ADMIN_PASSWORD env-var fallback was removed.
	// Admin login now requires a teachers-table row with role='admin'.
	http.Error(w, "密码错误", http.StatusUnauthorized)
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
	quizID := ""
	if q != nil {
		quizID = q.QuizID
	}

	totalAttempts := 0
	allAttempts, err := s.store.ListAttempts(r.Context())
	if err == nil {
		for _, a := range allAttempts {
			if a.Status == domain.StatusSubmitted {
				totalAttempts++
			}
		}
	}

	feedbackCount := 0
	if q != nil && len(q.Questions) > 0 {
		lastQ := q.Questions[len(q.Questions)-1]
		items, err := s.listAttemptsByQuizID(r.Context(), q.QuizID)
		if err == nil {
			for _, item := range items {
				if item.Status != domain.StatusSubmitted {
					continue
				}
				answers, aErr := s.store.GetAnswers(r.Context(), item.ID)
				if aErr != nil {
					continue
				}
				ans := answers[lastQ.ID]
				if strings.TrimSpace(ans) != "" {
					feedbackCount++
				}
			}
		}
	}

	writeJSON(w, map[string]any{
		"entry_open":     open,
		"started":        started,
		"submitted":      submitted,
		"quiz_title":     title,
		"quiz_id":        quizID,
		"total_attempts": totalAttempts,
		"feedback_count": feedbackCount,
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
	assetDirs := s.collectQuizAssetDirs()
	if len(assetDirs) > 0 {
		if err := quiz.ValidateImagePaths(parsed, assetDirs...); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := s.store.SetSetting(r.Context(), "quiz_yaml", string(raw)); err != nil {
		http.Error(w, "保存题库失败", http.StatusInternalServerError)
		return
	}
	_ = s.store.SetSetting(r.Context(), "entry_open", "false")
	_ = s.store.SetSetting(r.Context(), "quiz_source_path", quizSourcePath)
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

func gitRepoRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	// try the executable's directory first, then walk up
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
	}
	// fallback: try current working directory
	if wd, err := os.Getwd(); err == nil {
		for d := wd; ; d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
				return d, nil
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
		}
	}
	return "", fmt.Errorf("git repository not found")
}

func runCmd(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *Server) apiAdminUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	dir, err := gitRepoRoot()
	if err != nil {
		writeJSON(w, map[string]any{"error": "无法定位项目目录: " + err.Error()})
		return
	}
	// fetch latest from origin
	if out, err := runCmd(dir, "git", "fetch", "origin"); err != nil {
		writeJSON(w, map[string]any{"error": "git fetch 失败: " + err.Error(), "detail": out})
		return
	}
	localHash, _ := runCmd(dir, "git", "rev-parse", "HEAD")
	remoteHash, _ := runCmd(dir, "git", "rev-parse", "origin/main")
	localHash = strings.TrimSpace(localHash)
	remoteHash = strings.TrimSpace(remoteHash)
	hasUpdate := localHash != remoteHash
	// get log of new commits if any
	var commits string
	if hasUpdate {
		commits, _ = runCmd(dir, "git", "log", "--oneline", "HEAD..origin/main")
		commits = strings.TrimSpace(commits)
	}
	writeJSON(w, map[string]any{
		"has_update":  hasUpdate,
		"local_hash":  localHash,
		"remote_hash": remoteHash,
		"commits":     commits,
	})
}

func (s *Server) apiAdminUpdatePull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	dir, err := gitRepoRoot()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "无法定位项目目录: " + err.Error()})
		return
	}
	// git pull
	if out, err := runCmd(dir, "git", "pull", "origin", "main"); err != nil {
		writeJSON(w, map[string]any{"ok": false, "step": "git pull", "error": err.Error(), "detail": out})
		return
	}
	// build
	if out, err := runCmd(dir, "go", "build", "-o", "bin/server", "./cmd/server"); err != nil {
		writeJSON(w, map[string]any{"ok": false, "step": "go build", "error": err.Error(), "detail": out})
		return
	}
	hash, _ := runCmd(dir, "git", "rev-parse", "HEAD")
	writeJSON(w, map[string]any{"ok": true, "hash": strings.TrimSpace(hash)})
}

func (s *Server) apiAdminUpdateRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "message": "服务即将重启"})
	// flush response before shutting down
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		s.mu.RLock()
		fn := s.shutdownFn
		s.mu.RUnlock()
		if fn != nil {
			fn()
		} else {
			os.Exit(0)
		}
	}()
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

// requireAdmin now derives admin status purely from the unified auth_token
// session. The legacy admin_token cookie and in-memory adminTokens map have
// been removed.
func (s *Server) requireAdmin(r *http.Request) bool {
	sess := s.getAuthSession(r)
	return sess != nil && sess.Role == domain.RoleAdmin
}

func (s *Server) requireTeacherOrAdmin(r *http.Request) *authSession {
	return s.getAuthSession(r)
}

// getAuthSession reads the auth_token cookie and returns the matching session.
// Uses RLock since the common case is read-only.
func (s *Server) getAuthSession(r *http.Request) *authSession {
	cookie, err := r.Cookie("auth_token")
	if err != nil || cookie.Value == "" {
		return nil
	}
	s.mu.RLock()
	sess, ok := s.authTokens[cookie.Value]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	if time.Now().After(sess.Expiry) {
		s.mu.Lock()
		delete(s.authTokens, cookie.Value)
		s.mu.Unlock()
		return nil
	}
	return &sess
}

func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ── Unified auth API ──

func (s *Server) apiAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" || req.Password == "" {
		http.Error(w, "工号和密码不能为空", http.StatusBadRequest)
		return
	}
	teacher, err := s.store.GetTeacher(r.Context(), req.ID)
	if err != nil {
		http.Error(w, "工号或密码错误", http.StatusUnauthorized)
		return
	}
	if !checkPassword(teacher.PasswordHash, req.Password) {
		http.Error(w, "工号或密码错误", http.StatusUnauthorized)
		return
	}
	token := newID() + newID()
	sess := authSession{
		TeacherID: teacher.ID,
		Role:      teacher.Role,
		Expiry:    time.Now().Add(24 * time.Hour),
	}
	s.mu.Lock()
	s.authTokens[token] = sess
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    token,
		Path:     s.cookiePath(),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, map[string]any{"ok": true, "role": teacher.Role, "name": teacher.Name, "id": teacher.ID})
}

func (s *Server) apiAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie("auth_token"); err == nil {
		s.mu.Lock()
		delete(s.authTokens, cookie.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    "",
		Path:     s.cookiePath(),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	// Also clear the legacy admin_token cookie on old clients.
	http.SetCookie(w, &http.Cookie{
		Name:     "admin_token",
		Value:    "",
		Path:     s.cookiePath(),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAuthMe(w http.ResponseWriter, r *http.Request) {
	sess := s.getAuthSession(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	teacher, err := s.store.GetTeacher(r.Context(), sess.TeacherID)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{"id": teacher.ID, "name": teacher.Name, "role": teacher.Role})
}

func (s *Server) apiAuthChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.getAuthSession(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if len(req.NewPassword) < 6 {
		http.Error(w, "新密码至少6个字符", http.StatusBadRequest)
		return
	}
	teacher, err := s.store.GetTeacher(r.Context(), sess.TeacherID)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !checkPassword(teacher.PasswordHash, req.OldPassword) {
		http.Error(w, "原密码错误", http.StatusForbidden)
		return
	}
	hash, err := hashPassword(req.NewPassword)
	if err != nil {
		http.Error(w, "加密失败", http.StatusInternalServerError)
		return
	}
	if err := s.store.UpdateTeacherPassword(r.Context(), sess.TeacherID, hash); err != nil {
		http.Error(w, "修改失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ── Admin overview ──

func (s *Server) apiAdminOverview(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	teachers, _ := s.store.ListTeachers(r.Context())
	allCourses, _ := s.store.ListAllCourses(r.Context())

	teacherItems := make([]map[string]any, 0, len(teachers))
	for _, t := range teachers {
		var courseCount int
		for _, c := range allCourses {
			if c.TeacherID == t.ID {
				courseCount++
			}
		}
		teacherItems = append(teacherItems, map[string]any{
			"id": t.ID, "name": t.Name, "role": t.Role,
			"course_count": courseCount,
		})
	}

	courseItems := make([]map[string]any, 0, len(allCourses))
	for _, c := range allCourses {
		var teacherName string
		for _, t := range teachers {
			if t.ID == c.TeacherID {
				teacherName = t.Name
				break
			}
		}
		courseItems = append(courseItems, map[string]any{
			"id": c.ID, "teacher_id": c.TeacherID, "teacher_name": teacherName,
			"name": c.Name, "slug": c.Slug, "invite_code": c.InviteCode,
		})
	}

	// Aggregate student + attempt counts by scanning attempts once.
	allAttempts, _ := s.store.ListAttempts(r.Context())
	students := map[string]struct{}{}
	for _, a := range allAttempts {
		if a.StudentNo != "" {
			students[a.StudentNo] = struct{}{}
		}
	}

	writeJSON(w, map[string]any{
		"teacher_count": len(teachers),
		"course_count":  len(allCourses),
		"student_count": len(students),
		"attempt_count": len(allAttempts),
		"teachers":      teacherItems,
		"courses":       courseItems,
	})
}

// ── Admin teacher management ──

func (s *Server) apiAdminTeachers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		teachers, err := s.store.ListTeachers(r.Context())
		if err != nil {
			http.Error(w, "读取失败", http.StatusInternalServerError)
			return
		}
		items := make([]map[string]any, 0, len(teachers))
		for _, t := range teachers {
			items = append(items, map[string]any{
				"id": t.ID, "name": t.Name, "role": t.Role,
				"created_at": t.CreatedAt, "updated_at": t.UpdatedAt,
			})
		}
		writeJSON(w, map[string]any{"items": items})
	case http.MethodPost:
		var req struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		req.ID = strings.TrimSpace(req.ID)
		req.Name = strings.TrimSpace(req.Name)
		if req.ID == "" || req.Name == "" {
			http.Error(w, "工号和姓名不能为空", http.StatusBadRequest)
			return
		}
		if req.Password == "" {
			req.Password = "admin123"
		}
		hash, err := hashPassword(req.Password)
		if err != nil {
			http.Error(w, "加密失败", http.StatusInternalServerError)
			return
		}
		now := time.Now()
		t := &domain.Teacher{
			ID: req.ID, Name: req.Name, PasswordHash: hash,
			Role: domain.RoleTeacher, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.store.CreateTeacher(r.Context(), t); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "unique") {
				http.Error(w, "该工号已存在", http.StatusConflict)
				return
			}
			http.Error(w, "创建失败", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "teacher": map[string]any{"id": t.ID, "name": t.Name, "role": t.Role}})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) apiAdminTeacherDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	target, err := s.store.GetTeacher(r.Context(), req.ID)
	if err != nil {
		http.Error(w, "教师不存在", http.StatusNotFound)
		return
	}
	if target.Role == domain.RoleAdmin {
		http.Error(w, "不能删除管理员账户", http.StatusForbidden)
		return
	}
	if err := s.store.DeleteTeacher(r.Context(), req.ID); err != nil {
		http.Error(w, "删除失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAdminTeacherResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Password == "" {
		req.Password = "admin123"
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		http.Error(w, "加密失败", http.StatusInternalServerError)
		return
	}
	if err := s.store.UpdateTeacherPassword(r.Context(), req.ID, hash); err != nil {
		http.Error(w, "重置失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ── Teacher course API ──

func (s *Server) apiTeacherCourses(w http.ResponseWriter, r *http.Request) {
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		courses, err := s.store.ListCoursesByTeacher(r.Context(), sess.TeacherID)
		if err != nil {
			http.Error(w, "读取失败", http.StatusInternalServerError)
			return
		}
		items := make([]map[string]any, 0, len(courses))
		for _, c := range courses {
			items = append(items, map[string]any{
				"id": c.ID, "teacher_id": c.TeacherID, "name": c.Name,
				"slug": c.Slug, "invite_code": c.InviteCode,
				"created_at": c.CreatedAt, "updated_at": c.UpdatedAt,
			})
		}
		writeJSON(w, map[string]any{"items": items})
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		req.Slug = strings.TrimSpace(req.Slug)
		if req.Name == "" || req.Slug == "" {
			http.Error(w, "课程名称和标识不能为空", http.StatusBadRequest)
			return
		}
		if _, err := validateMaterialFolder(req.Slug); err != nil {
			http.Error(w, "课程标识不合法（不能包含 / \\ .. 等特殊字符）", http.StatusBadRequest)
			return
		}
		now := time.Now()
		c := &domain.Course{
			TeacherID:  sess.TeacherID,
			Name:       req.Name,
			Slug:       req.Slug,
			InviteCode: generateInviteCode(),
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := s.store.CreateCourse(r.Context(), c); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "unique") {
				http.Error(w, "该课程标识已存在", http.StatusConflict)
				return
			}
			http.Error(w, "创建失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "course": map[string]any{
			"id": c.ID, "name": c.Name, "slug": c.Slug, "invite_code": c.InviteCode,
		}})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) apiTeacherCourseDelete(w http.ResponseWriter, r *http.Request) {
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
		ID int `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	course, err := s.store.GetCourse(r.Context(), req.ID)
	if err != nil {
		http.Error(w, "课程不存在", http.StatusNotFound)
		return
	}
	if sess.Role != domain.RoleAdmin && course.TeacherID != sess.TeacherID {
		http.Error(w, "无权限", http.StatusForbidden)
		return
	}
	if err := s.store.DeleteCourse(r.Context(), req.ID); err != nil {
		http.Error(w, "删除失败", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	delete(s.courseQuizzes, req.ID)
	s.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiCourseByInviteCode(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("code")))
	if code == "" {
		http.Error(w, "缺少邀请码", http.StatusBadRequest)
		return
	}
	course, err := s.store.GetCourseByInviteCode(r.Context(), code)
	if err != nil {
		http.Error(w, "邀请码无效", http.StatusNotFound)
		return
	}
	teacher, _ := s.store.GetTeacher(r.Context(), course.TeacherID)
	teacherName := ""
	if teacher != nil {
		teacherName = teacher.Name
	}
	writeJSON(w, map[string]any{
		"id": course.ID, "name": course.Name, "slug": course.Slug,
		"teacher_name": teacherName, "invite_code": course.InviteCode,
	})
}

// ── Course-scoped quiz APIs ──

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
	_ = course

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
	s.mu.Lock()
	s.courseQuizzes[courseID] = parsed
	s.mu.Unlock()
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
	s.mu.RLock()
	q := s.courseQuizzes[courseID]
	s.mu.RUnlock()
	title := ""
	quizID := ""
	if q != nil {
		title = q.Title
		quizID = q.QuizID
	}
	// Scope counters to the currently loaded quiz only, so teachers see "this
	// round" numbers, not the course's cumulative history.
	var started, submitted int
	if q != nil {
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
	s.mu.Lock()
	s.courseQuizzes[courseID] = parsed
	s.courseQuizAssetDirs[courseID] = dir
	s.mu.Unlock()
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

func (s *Server) apiTeacherCourseAttempts(w http.ResponseWriter, r *http.Request) {
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
	items, err := s.store.ListAttemptsByCourse(r.Context(), courseID)
	if err != nil {
		http.Error(w, "读取失败", http.StatusInternalServerError)
		return
	}

	// Build a quiz map: in-memory loaded quiz + on-disk quiz bank YAMLs for scoring.
	s.mu.RLock()
	q := s.courseQuizzes[courseID]
	s.mu.RUnlock()
	quizMap := map[string]*domain.Quiz{}
	if q != nil {
		quizMap[q.QuizID] = q
	}
	// Collect distinct quiz_ids from attempts that aren't already in the map.
	needed := map[string]struct{}{}
	for _, a := range items {
		if _, ok := quizMap[a.QuizID]; !ok {
			needed[a.QuizID] = struct{}{}
		}
	}
	if len(needed) > 0 {
		quizRoot := filepath.Join(s.metadataCourseDir(course.TeacherID, course.Slug), "quiz")
		for qid := range needed {
			subDir := filepath.Join(quizRoot, safePathPart(qid))
			files, _ := os.ReadDir(subDir)
			for _, f := range files {
				name := strings.ToLower(f.Name())
				if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
					continue
				}
				data, _ := os.ReadFile(filepath.Join(subDir, f.Name()))
				if parsed, err := quiz.Parse(data); err == nil {
					quizMap[qid] = parsed
				}
				break
			}
		}
	}

	// Score all submitted attempts and keep only the best per (student_no, quiz_id).
	type key struct{ studentNo, quizID string }
	type scored struct {
		attempt domain.Attempt
		correct int
		total   int
		loaded  bool
	}
	bestMap := map[key]*scored{}
	var inProgress []map[string]any

	for _, a := range items {
		if a.Status != domain.StatusSubmitted {
			inProgress = append(inProgress, map[string]any{
				"id": a.ID, "name": a.Name, "student_no": a.StudentNo,
				"class_name": a.ClassName, "attempt_no": a.AttemptNo,
				"status": a.Status, "correct": 0, "total": 0,
				"quiz_id": a.QuizID, "quiz_loaded": false, "updated_at": a.UpdatedAt,
			})
			continue
		}
		var correct, total int
		loaded := false
		if qForAttempt, ok := quizMap[a.QuizID]; ok {
			correct, total = s.calcScore(r.Context(), qForAttempt, a.ID)
			loaded = true
		}
		k := key{a.StudentNo, a.QuizID}
		if prev, ok := bestMap[k]; !ok || correct > prev.correct || (correct == prev.correct && a.AttemptNo > prev.attempt.AttemptNo) {
			bestMap[k] = &scored{attempt: a, correct: correct, total: total, loaded: loaded}
		}
	}

	currentQuizID := ""
	if q != nil {
		currentQuizID = q.QuizID
	}
	result := make([]map[string]any, 0, len(bestMap)+len(inProgress))
	for _, s := range bestMap {
		a := s.attempt
		result = append(result, map[string]any{
			"id": a.ID, "name": a.Name, "student_no": a.StudentNo,
			"class_name": a.ClassName, "attempt_no": a.AttemptNo,
			"status": a.Status, "correct": s.correct, "total": s.total,
			"quiz_id": a.QuizID, "quiz_loaded": s.loaded, "updated_at": a.UpdatedAt,
		})
	}
	result = append(result, inProgress...)
	writeJSON(w, map[string]any{"items": result, "current_quiz_id": currentQuizID})
}

func (s *Server) apiTeacherCourseAttemptDetail(w http.ResponseWriter, r *http.Request) {
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
	if attempt.CourseID != courseID {
		http.Error(w, "无权限访问此记录", http.StatusForbidden)
		return
	}
	res, err := s.buildResult(r.Context(), attempt)
	if err != nil {
		http.Error(w, "读取详情失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, res)
}

func (s *Server) apiTeacherCourseLive(w http.ResponseWriter, r *http.Request) {
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
			cs, _ := s.store.GetCourseState(r.Context(), courseID)
			entryOpen := cs != nil && cs.EntryOpen
			// Scope counters to the currently loaded quiz so "已进入 / 已提交"
			// reflect only this round's data, not the course's cumulative total.
			s.mu.RLock()
			q := s.courseQuizzes[courseID]
			s.mu.RUnlock()
			var started, submitted int
			var quizID, quizTitle string
			if q != nil {
				quizID = q.QuizID
				quizTitle = q.Title
				started, submitted, _ = s.store.GetLiveStatsByCourseQuiz(r.Context(), courseID, q.QuizID)
			}
			payload := map[string]any{
				"entry_open": entryOpen,
				"started":    started,
				"submitted":  submitted,
				"quiz_id":    quizID,
				"quiz_title": quizTitle,
			}
			b, _ := json.Marshal(payload)
			_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
			flusher.Flush()
		}
	}
}

func (s *Server) apiTeacherCourseClearAttempts(w http.ResponseWriter, r *http.Request) {
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
	s.mu.RLock()
	q := s.courseQuizzes[courseID]
	s.mu.RUnlock()
	if q == nil {
		http.Error(w, "当前未加载题库", http.StatusBadRequest)
		return
	}
	if err := s.store.ClearAttemptsByCourse(r.Context(), courseID, q.QuizID); err != nil {
		http.Error(w, "清空失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiTeacherCourseFixLegacyAttempts(w http.ResponseWriter, r *http.Request) {
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
	s.mu.RLock()
	q := s.courseQuizzes[courseID]
	s.mu.RUnlock()
	if q == nil {
		http.Error(w, "当前未加载题库", http.StatusBadRequest)
		return
	}
	n, err := s.store.FixLegacyAttemptsCourse(r.Context(), q.QuizID, courseID)
	if err != nil {
		http.Error(w, "修复失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "updated": n})
}

// apiTeacherCourseFixAllLegacy assigns course_id to ALL course_id=0 attempts
// whose quiz_id appears in this course's quiz bank directory on disk.
// Called automatically when the teacher opens the summary tab so that
// historical records from before the multi-teacher migration are visible.
func (s *Server) apiTeacherCourseFixAllLegacy(w http.ResponseWriter, r *http.Request) {
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
	quizRoot := filepath.Join(s.metadataCourseDir(course.TeacherID, course.Slug), "quiz")
	dirs, _ := os.ReadDir(quizRoot)
	var quizIDs []string
	for _, d := range dirs {
		if d.IsDir() {
			quizIDs = append(quizIDs, d.Name())
		}
	}
	n, err := s.store.FixAllLegacyAttemptsCourse(r.Context(), quizIDs, courseID)
	if err != nil {
		http.Error(w, "修复失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "updated": n})
}

func (s *Server) apiTeacherCourseExportCSV(w http.ResponseWriter, r *http.Request) {
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
	s.mu.RLock()
	q := s.courseQuizzes[courseID]
	s.mu.RUnlock()
	if q == nil {
		http.Error(w, "当前没有题库", http.StatusBadRequest)
		return
	}
	items, err := s.store.ListAttemptsByCourse(r.Context(), courseID)
	if err != nil {
		http.Error(w, "读取失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="class_report.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	header := []string{"姓名", "学号", "班级", "状态", "尝试次数", "正确数", "总题数"}
	for idx := range q.Questions {
		header = append(header, fmt.Sprintf("第%d题", idx+1))
	}
	_ = cw.Write(header)
	for _, a := range items {
		correct, total := s.calcScore(r.Context(), q, a.ID)
		attemptNo := ""
		if a.AttemptNo > 0 {
			attemptNo = fmt.Sprintf("%d", a.AttemptNo)
		}
		row := []string{safeCSV(a.Name), safeCSV(a.StudentNo), safeCSV(a.ClassName), string(a.Status), attemptNo, fmt.Sprintf("%d", correct), fmt.Sprintf("%d", total)}
		answers, aErr := s.store.GetAnswers(r.Context(), a.ID)
		if aErr != nil {
			continue
		}
		for _, question := range q.Questions {
			val := answers[question.ID]
			if question.Type == domain.QuestionShortAnswer {
				val = domain.ShortAnswerText(val)
			}
			row = append(row, safeCSV(val))
		}
		_ = cw.Write(row)
	}
	cw.Flush()
}

// ── Teacher course-scoped materials ──

func (s *Server) apiTeacherCourseMaterials(w http.ResponseWriter, r *http.Request) {
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
	dir := s.metadataMaterialsDir(course.TeacherID, course.Slug)
	items, err := s.scanMaterialsFromDir(dir, course.Slug, true)
	if err != nil {
		writeJSON(w, map[string]any{"items": []adminMaterialGroupItem{}})
		return
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) apiTeacherCourseMaterialUpload(w http.ResponseWriter, r *http.Request) {
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
	r.Body = http.MaxBytesReader(w, r.Body, 500<<20)
	if err := r.ParseMultipartForm(500 << 20); err != nil {
		http.Error(w, "文件过大或格式错误", http.StatusBadRequest)
		return
	}
	folder := course.Slug
	headers, err := collectUploadHeaders(r.MultipartForm)
	if err != nil {
		http.Error(w, "未找到上传文件", http.StatusBadRequest)
		return
	}
	dir := s.metadataMaterialsDir(course.TeacherID, course.Slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "创建目录失败", http.StatusInternalServerError)
		return
	}
	uploaded := make([]materialUploadSuccess, 0, len(headers))
	failed := make([]materialUploadFailure, 0)
	for _, header := range headers {
		result, failure := s.saveUploadedMaterial(dir, folder, header)
		if failure != nil {
			failed = append(failed, *failure)
			continue
		}
		uploaded = append(uploaded, *result)
	}
	resp := map[string]any{"ok": len(failed) == 0, "uploaded": uploaded, "failed": failed}
	if len(uploaded) > 0 {
		resp["url"] = uploaded[0].URL
	}
	if len(uploaded) == 0 {
		writeJSONStatus(w, http.StatusBadRequest, resp)
		return
	}
	writeJSONStatus(w, http.StatusOK, resp)
}

func (s *Server) apiTeacherCourseMaterialDelete(w http.ResponseWriter, r *http.Request) {
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
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	name, _, err := normalizeMaterialFilename(req.File, "")
	if err != nil {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	fp := filepath.Join(s.metadataMaterialsDir(course.TeacherID, course.Slug), name)
	if err := os.Remove(fp); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "文件不存在", http.StatusNotFound)
			return
		}
		http.Error(w, "删除失败", http.StatusInternalServerError)
		return
	}
	if err := s.deleteMaterialVisibility(r.Context(), course.Slug, name); err != nil {
		http.Error(w, "删除失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiTeacherCourseMaterialRename(w http.ResponseWriter, r *http.Request) {
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
		OldName string `json:"old_name"`
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	oldName, oldExt, err := normalizeMaterialFilename(req.OldName, "")
	if err != nil {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	newName, _, err := normalizeMaterialFilename(req.NewName, oldExt)
	if err != nil {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	dir := s.metadataMaterialsDir(course.TeacherID, course.Slug)
	oldPath := filepath.Join(dir, oldName)
	newPath := filepath.Join(dir, newName)
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		http.Error(w, "原文件不存在", http.StatusNotFound)
		return
	}
	if oldPath == newPath {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if _, err := os.Stat(newPath); err == nil {
		http.Error(w, "目标文件名已存在", http.StatusConflict)
		return
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		http.Error(w, "重命名失败", http.StatusInternalServerError)
		return
	}
	if err := s.renameMaterialVisibility(r.Context(), course.Slug, oldName, newName); err != nil {
		_ = os.Rename(newPath, oldPath)
		http.Error(w, "重命名失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiTeacherCourseMaterialVisibility(w http.ResponseWriter, r *http.Request) {
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
		File    string `json:"file"`
		Visible bool   `json:"visible"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	name, _, err := normalizeMaterialFilename(req.File, "")
	if err != nil {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(filepath.Join(s.metadataMaterialsDir(course.TeacherID, course.Slug), name)); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "文件不存在", http.StatusNotFound)
			return
		}
		http.Error(w, "读取失败", http.StatusInternalServerError)
		return
	}
	if err := s.setMaterialVisibility(r.Context(), course.Slug, name, req.Visible); err != nil {
		http.Error(w, "保存失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "visible": req.Visible})
}

// ── Teacher course-scoped homework ──

func (s *Server) apiTeacherCourseHomeworkAssignments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
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
	assignments := s.listMetadataHomeworkAssignments(course)
	legacyAssignments, _ := s.listHomeworkAssignments(course.Slug)
	seen := map[string]struct{}{}
	for _, a := range assignments {
		seen[a] = struct{}{}
	}
	for _, a := range legacyAssignments {
		if _, ok := seen[a]; !ok {
			assignments = append(assignments, a)
		}
	}
	sort.Strings(assignments)
	items := make([]map[string]any, 0, len(assignments))
	for _, assignmentID := range assignments {
		items = append(items, s.homeworkAssignmentPayload(course.Slug, course.ID, assignmentID, true))
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) apiTeacherCourseHomeworkAssignmentUpload(w http.ResponseWriter, r *http.Request) {
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
	r.Body = http.MaxBytesReader(w, r.Body, 500<<20)
	if err := r.ParseMultipartForm(500 << 20); err != nil {
		http.Error(w, "文件过大或格式错误", http.StatusBadRequest)
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
	dir := s.metadataHomeworkAssignmentDir(course.TeacherID, course.Slug, assignmentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "创建目录失败", http.StatusInternalServerError)
		return
	}
	uploaded := make([]map[string]any, 0, len(headers))
	failed := make([]map[string]any, 0)
	for _, header := range headers {
		result, failure := s.saveUploadedHomeworkAssignment(course.Slug, course.ID, assignmentID, dir, header)
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

func (s *Server) apiTeacherCourseHomeworkAssignmentDelete(w http.ResponseWriter, r *http.Request) {
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	assignmentID, err := validateHomeworkAssignmentID(req.AssignmentID)
	if err != nil {
		http.Error(w, "作业编号无效", http.StatusBadRequest)
		return
	}
	_ = os.RemoveAll(s.metadataHomeworkAssignmentDir(course.TeacherID, course.Slug, assignmentID))
	_ = os.RemoveAll(s.homeworkAssignmentDir(course.Slug, assignmentID))
	_ = os.Remove(s.homeworkLegacyAssignmentPath(course.Slug, assignmentID))
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiTeacherCourseHomeworkAssignmentDeleteFile(w http.ResponseWriter, r *http.Request) {
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
		FileName     string `json:"file_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	assignmentID, err := validateHomeworkAssignmentID(req.AssignmentID)
	if err != nil {
		http.Error(w, "作业编号无效", http.StatusBadRequest)
		return
	}
	fileName, err := normalizeHomeworkResourceFilename(req.FileName)
	if err != nil {
		http.Error(w, "文件名无效", http.StatusBadRequest)
		return
	}
	metaDir := s.metadataHomeworkAssignmentDir(course.TeacherID, course.Slug, assignmentID)
	_ = os.Remove(filepath.Join(metaDir, fileName))
	dataDir := s.homeworkAssignmentDir(course.Slug, assignmentID)
	_ = os.Remove(filepath.Join(dataDir, fileName))
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiTeacherCourseHomeworkAssignmentVisibility(w http.ResponseWriter, r *http.Request) {
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
		Hidden       bool   `json:"hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	assignmentID, err := validateHomeworkAssignmentID(req.AssignmentID)
	if err != nil {
		http.Error(w, "作业编号无效", http.StatusBadRequest)
		return
	}
	s.setHomeworkAssignmentVisibility(course.Slug, assignmentID, req.Hidden)
	writeJSON(w, map[string]any{"ok": true, "hidden": req.Hidden})
}

func (s *Server) apiTeacherCourseHomeworkSubmissions(w http.ResponseWriter, r *http.Request) {
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
	assignmentID := strings.TrimSpace(r.URL.Query().Get("assignment_id"))
	items, err := s.store.ListHomeworkSubmissions(r.Context(), courseID, course.Slug, assignmentID)
	if err != nil {
		http.Error(w, "读取作业列表失败", http.StatusInternalServerError)
		return
	}
	resp := make([]map[string]any, 0, len(items))
	for _, item := range items {
		resp = append(resp, s.homeworkSubmissionPayload(&item, true, courseID))
	}
	writeJSON(w, map[string]any{"items": resp})
}

func (s *Server) apiTeacherCourseHomeworkSubmissionDownload(w http.ResponseWriter, r *http.Request) {
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
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "缺少 id 参数", http.StatusBadRequest)
		return
	}
	submission, err := s.store.GetHomeworkSubmissionByID(r.Context(), id)
	if err != nil || submission.Course != course.Slug {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	slotStr := strings.TrimSpace(r.URL.Query().Get("slot"))
	slot, err := parseHomeworkSlot(slotStr)
	if err != nil {
		http.Error(w, "无效的 slot 参数", http.StatusBadRequest)
		return
	}
	subDir := s.resolveHomeworkSubmissionDirForCourse(course, submission)
	switch slot {
	case domain.HomeworkSlotReport:
		if strings.TrimSpace(submission.ReportOriginalName) == "" {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		fp := filepath.Join(subDir, homeworkDiskFilename(domain.HomeworkSlotReport))
		if _, statErr := os.Stat(fp); statErr != nil {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeHomeworkMetadataFilename(submission.ReportOriginalName, "report.pdf")))
		}
		http.ServeFile(w, r, fp)
	case domain.HomeworkSlotCode:
		if strings.TrimSpace(submission.CodeOriginalName) == "" {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		fp := s.homeworkStoredFilePath(submission, domain.HomeworkSlotCode)
		if _, statErr := os.Stat(fp); statErr != nil {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-ipynb+json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeHomeworkMetadataFilename(submission.CodeOriginalName, "notebook.ipynb")))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeFile(w, r, fp)
	case domain.HomeworkSlotExtra:
		if strings.TrimSpace(submission.ExtraOriginalName) == "" {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		fp := filepath.Join(subDir, "extra.zip")
		if _, statErr := os.Stat(fp); statErr != nil {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeHomeworkMetadataFilename(submission.ExtraOriginalName, "extra.zip")))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeFile(w, r, fp)
	default:
		http.Error(w, "无效的 slot 参数", http.StatusBadRequest)
	}
}

func (s *Server) apiTeacherCourseHomeworkSubmissionArchive(w http.ResponseWriter, r *http.Request) {
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
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "缺少 id 参数", http.StatusBadRequest)
		return
	}
	submission, err := s.store.GetHomeworkSubmissionByID(r.Context(), id)
	if err != nil || submission.Course != course.Slug {
		http.Error(w, "file not found", http.StatusNotFound)
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

func (s *Server) apiTeacherCourseHomeworkSubmissionDelete(w http.ResponseWriter, r *http.Request) {
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
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ID) == "" {
		http.Error(w, "缺少 id 参数", http.StatusBadRequest)
		return
	}
	submission, err := s.store.GetHomeworkSubmissionByID(r.Context(), req.ID)
	if err != nil || submission.Course != course.Slug {
		http.Error(w, "提交记录不存在", http.StatusNotFound)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.homeworkSubmissionDir(submission)
	if err := s.store.DeleteHomeworkSubmission(r.Context(), submission.ID); err != nil {
		http.Error(w, "删除失败", http.StatusInternalServerError)
		return
	}
	_ = os.RemoveAll(dir)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiTeacherCourseHomeworkArchiveAll(w http.ResponseWriter, r *http.Request) {
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
	submissions, err := s.store.ListHomeworkSubmissions(r.Context(), courseID, course.Slug, assignmentID)
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
	archiveName := fmt.Sprintf("homework_%s_%s_all.zip", safePathPart(course.Slug), safePathPart(assignmentID))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", archiveName))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(archiveData)
}

// ── Helper: resolve course from request ──

func (s *Server) resolveTeacherCourse(r *http.Request, sess *authSession) (int, *domain.Course, error) {
	idStr := r.URL.Query().Get("course_id")
	if idStr == "" {
		idStr = r.FormValue("course_id")
	}
	if idStr == "" {
		return 0, nil, fmt.Errorf("缺少 course_id 参数")
	}
	var courseID int
	if _, err := fmt.Sscanf(idStr, "%d", &courseID); err != nil {
		return 0, nil, fmt.Errorf("course_id 无效")
	}
	course, err := s.store.GetCourse(r.Context(), courseID)
	if err != nil {
		return 0, nil, fmt.Errorf("课程不存在")
	}
	if sess.Role != domain.RoleAdmin && course.TeacherID != sess.TeacherID {
		return 0, nil, fmt.Errorf("无权限访问此课程")
	}
	return courseID, course, nil
}

// joinURL builds the public student join URL for a given invite code.
// It prefers the configured BaseURL; falls back to the incoming request's scheme+host.
func (s *Server) joinURL(r *http.Request, inviteCode string) string {
	base := strings.TrimRight(s.cfg.BaseURL, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	pfx := s.pathPrefix()
	return base + pfx + "/join?code=" + url.QueryEscape(inviteCode)
}

func (s *Server) apiTeacherCourseInviteQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "未登录", http.StatusUnauthorized)
		return
	}
	_, course, err := s.resolveTeacherCourse(r, sess)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target := s.joinURL(r, course.InviteCode)
	png, err := qrcode.Encode(target, qrcode.Medium, 512)
	if err != nil {
		http.Error(w, "生成二维码失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	filename := "invite-" + course.InviteCode + ".png"
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (s *Server) metadataCourseDir(teacherID, courseSlug string) string {
	return filepath.Join(s.cfg.MetadataDir, safePathPart(teacherID), safePathPart(courseSlug))
}

func (s *Server) metadataQuizDir(teacherID, courseSlug, quizID string) string {
	return filepath.Join(s.metadataCourseDir(teacherID, courseSlug), "quiz", safePathPart(quizID))
}

// resolveQuizSubmissionDir returns the quiz bank directory for storing
// submissions. It prefers the courseQuizAssetDirs entry (set when a quiz
// bank is activated) so that quiz_id ("w7_l1") maps to the correct
// bank directory ("week7_l1") even when they differ.
func (s *Server) resolveQuizSubmissionDir(courseID int, teacherID, courseSlug, quizID string) string {
	s.mu.RLock()
	bankDir := s.courseQuizAssetDirs[courseID]
	s.mu.RUnlock()
	if bankDir != "" {
		if _, err := os.Stat(bankDir); err == nil {
			return bankDir
		}
	}
	return s.metadataQuizDir(teacherID, courseSlug, quizID)
}

func (s *Server) metadataAssignmentDir(teacherID, courseSlug, assignmentID string) string {
	return filepath.Join(s.metadataCourseDir(teacherID, courseSlug), "assignment", safePathPart(assignmentID))
}

func (s *Server) metadataMaterialsDir(teacherID, courseSlug string) string {
	return filepath.Join(s.metadataCourseDir(teacherID, courseSlug), "materials")
}

func generateInviteCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, 6)
	_, _ = crand.Read(buf)
	for i := range buf {
		buf[i] = chars[int(buf[i])%len(chars)]
	}
	return string(buf)
}

func (s *Server) resolveQuizForAttempt(attempt *domain.Attempt) *domain.Quiz {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if attempt.CourseID > 0 {
		if q, ok := s.courseQuizzes[attempt.CourseID]; ok {
			return q
		}
	}
	return s.currentQuiz
}

func (s *Server) isEntryOpen(ctx context.Context) (bool, error) {
	val, err := s.store.GetSetting(ctx, "entry_open")
	if err != nil {
		return false, err
	}
	return val == "true", nil
}

func (s *Server) buildResult(ctx context.Context, attempt *domain.Attempt) (map[string]any, error) {
	current := s.resolveQuizForAttempt(attempt)
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
	courseSlug := ""
	if attempt.CourseID > 0 {
		if c, err := s.store.GetCourse(context.Background(), attempt.CourseID); err == nil && c != nil {
			courseSlug = c.Slug
		}
	}
	return map[string]any{
		"quiz_title": current.Title,
		"quiz_id":    current.QuizID,
		"course":     courseSlug,
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

func (s *Server) collectQuizAssetDirs() []string {
	s.mu.RLock()
	dirs := make([]string, 0, len(s.courseQuizAssetDirs))
	for _, dir := range s.courseQuizAssetDirs {
		dirs = append(dirs, dir)
	}
	s.mu.RUnlock()
	return dirs
}

func (s *Server) resolveAssetPath(raw string) (string, bool) {
	name := filepath.Clean(strings.TrimSpace(raw))
	if name == "" || name == "." {
		return "", false
	}
	if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
		return "", false
	}
	s.mu.RLock()
	candidates := make([]string, 0, len(s.courseQuizAssetDirs))
	for _, dir := range s.courseQuizAssetDirs {
		candidates = append(candidates, filepath.Join(dir, name))
	}
	s.mu.RUnlock()
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
	current := s.resolveQuizForAttempt(attempt)
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
	var dir, urlPath string
	filename := fmt.Sprintf("q_%s_%d.jpg", questionID, time.Now().UnixMilli())
	if attempt.CourseID > 0 {
		if course, cErr := s.store.GetCourse(r.Context(), attempt.CourseID); cErr == nil && course != nil {
			quizDir := s.resolveQuizSubmissionDir(attempt.CourseID, course.TeacherID, course.Slug, current.QuizID)
			dir = filepath.Join(quizDir, "submissions", safePathPart(attempt.StudentNo))
			relToMeta, _ := filepath.Rel(s.cfg.MetadataDir, filepath.Join(dir, filename))
			urlPath = s.pathPrefix() + "/uploads/" + filepath.ToSlash(relToMeta)
		}
	}
	if dir == "" {
		relParts := filepath.Join(
			"_global",
			safePathPart(attempt.ClassName),
			"quiz",
			safePathPart(current.QuizID),
			"submissions",
			safePathPart(attempt.StudentNo))
		dir = filepath.Join(s.cfg.MetadataDir, relParts)
		urlPath = s.pathPrefix() + "/uploads/" + relParts + "/" + filename
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "创建目录失败", http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, filename), data, 0o644); err != nil {
		http.Error(w, "写入文件失败", http.StatusInternalServerError)
		return
	}
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

func (s *Server) apiDeleteAnswerImage(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		QuestionID string `json:"question_id"`
		URL        string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "参数错误", http.StatusBadRequest)
		return
	}
	if body.QuestionID == "" || body.URL == "" {
		http.Error(w, "参数不完整", http.StatusBadRequest)
		return
	}
	answers, _ := s.store.GetAnswers(r.Context(), attempt.ID)
	sa := domain.ParseShortAnswer(answers[body.QuestionID])
	newImages := make([]string, 0, len(sa.Images))
	var deleted bool
	for _, img := range sa.Images {
		if img == body.URL {
			deleted = true
		} else {
			newImages = append(newImages, img)
		}
	}
	if !deleted {
		http.Error(w, "图片不存在", http.StatusNotFound)
		return
	}
	sa.Images = newImages
	encoded := domain.EncodeShortAnswer(sa)
	_ = s.store.SaveAnswer(r.Context(), domain.Answer{
		AttemptID:  attempt.ID,
		QuestionID: body.QuestionID,
		Value:      encoded,
		UpdatedAt:  time.Now(),
	})
	// Best-effort: delete the file from disk if within MetadataDir.
	urlPfx := s.pathPrefix() + "/uploads/"
	if strings.HasPrefix(body.URL, urlPfx) {
		rel := strings.TrimPrefix(body.URL, urlPfx)
		cleaned := filepath.Clean(rel)
		if cleaned != "." && !strings.Contains(cleaned, "..") && !filepath.IsAbs(cleaned) {
			_ = os.Remove(filepath.Join(s.cfg.MetadataDir, cleaned))
		}
	}
	writeJSON(w, map[string]any{"ok": true, "images": sa.Images})
}

func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/uploads/")
	cleaned := filepath.Clean(rel)
	if cleaned == "." || strings.Contains(cleaned, "..") || filepath.IsAbs(cleaned) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	fp := filepath.Join(s.cfg.MetadataDir, cleaned)
	if _, err := os.Stat(fp); err != nil {
		http.NotFound(w, r)
		return
	}
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
	// Check if any question explicitly uses FixedPosition. If none do,
	// fall back to legacy behavior (short_answer pinned at the end).
	anyExplicitFixed := false
	for _, question := range q.Questions {
		if question.FixedPosition {
			anyExplicitFixed = true
			break
		}
	}

	var allItems []domain.Question
	if q.Sampling != nil && len(q.Sampling.Groups) > 0 {
		byTag := map[string][]domain.Question{}
		var ungrouped []domain.Question
		for _, question := range q.Questions {
			tag := strings.TrimSpace(question.PoolTag)
			if tag == "" {
				ungrouped = append(ungrouped, question)
				continue
			}
			byTag[tag] = append(byTag[tag], question)
		}
		allItems = append(allItems, ungrouped...)
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
			allItems = append(allItems, pool[:pick]...)
		}
	} else {
		allItems = append(allItems, q.Questions...)
	}

	isFixed := func(question domain.Question) bool {
		if anyExplicitFixed {
			return question.FixedPosition
		}
		return question.Type == domain.QuestionShortAnswer
	}

	type indexedQ struct {
		idx int
		q   domain.Question
	}
	var fixed []indexedQ
	var shuffleable []domain.Question
	for i, question := range allItems {
		if isFixed(question) {
			fixed = append(fixed, indexedQ{i, question})
		} else {
			shuffleable = append(shuffleable, question)
		}
	}

	r := seededRandom(q.QuizID + ":" + attemptID + ":final")
	r.Shuffle(len(shuffleable), func(i, j int) { shuffleable[i], shuffleable[j] = shuffleable[j], shuffleable[i] })

	result := make([]domain.Question, len(allItems))
	fixedSet := map[int]bool{}
	for _, fq := range fixed {
		result[fq.idx] = fq.q
		fixedSet[fq.idx] = true
	}
	si := 0
	for i := range result {
		if !fixedSet[i] {
			result[i] = shuffleable[si]
			si++
		}
	}

	for i := range result {
		result[i] = shuffleQuestionOptions(result[i], q.QuizID, attemptID)
	}
	return result
}

func shuffleQuestionOptions(question domain.Question, quizID, attemptID string) domain.Question {
	if len(question.Options) <= 1 {
		return question
	}
	if question.ShuffleOptions != nil && !*question.ShuffleOptions {
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
