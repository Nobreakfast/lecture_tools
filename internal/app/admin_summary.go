package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"course-assistant/internal/ai"
	"course-assistant/internal/domain"
	"course-assistant/internal/pdftext"
	"course-assistant/internal/quiz"
)

// Legacy global (non-course-scoped) admin summary handlers were removed as
// part of the multi-tenant migration. All AI class summaries now live under
// the teacher panel, scoped by course via apiTeacherCourseSummary.

// buildAdminSummaryInput builds the AI summarize payload.
// courseID > 0 scopes the attempt query to that course; 0 falls back to global quiz_id scan (admin mode).
func (s *Server) buildAdminSummaryInput(ctx context.Context, q *domain.Quiz, courseID int) (ai.AdminSummarizeInput, error) {
	var (
		allAttempts []domain.Attempt
		err         error
	)
	if courseID > 0 {
		all, e := s.store.ListAttemptsByCourse(ctx, courseID)
		if e != nil {
			return ai.AdminSummarizeInput{}, e
		}
		for _, a := range all {
			if a.QuizID == q.QuizID {
				allAttempts = append(allAttempts, a)
			}
		}
	} else {
		allAttempts, err = s.listAttemptsByQuizID(ctx, q.QuizID)
		if err != nil {
			return ai.AdminSummarizeInput{}, err
		}
	}

	latest := bestScoringAttempts(allAttempts, func(attemptID string) (int, int) {
		return s.calcScore(ctx, q, attemptID)
	})
	if len(latest) == 0 {
		return ai.AdminSummarizeInput{}, fmt.Errorf("没有已提交的学生记录")
	}

	totalScore := 0
	totalPossible := 0
	questionCorrect := map[string]int{}
	questionAnswered := map[string]int{}
	questionDist := map[string]map[string]int{}
	surveyDist := map[string]map[string]int{}
	shortTexts := map[string][]string{}

	for _, attempt := range latest {
		answers, aErr := s.store.GetAnswers(ctx, attempt.ID)
		if aErr != nil {
			continue
		}
		questions := shuffledQuestions(q, attempt.ID)
		for _, question := range questions {
			ans := answers[question.ID]
			switch question.Type {
			case domain.QuestionSurvey:
				if ans != "" {
					if surveyDist[question.ID] == nil {
						surveyDist[question.ID] = map[string]int{}
					}
					if question.AllowMultiple {
						for _, k := range strings.Split(ans, ",") {
							k = strings.TrimSpace(k)
							if k != "" {
								surveyDist[question.ID][k]++
							}
						}
					} else {
						surveyDist[question.ID][ans]++
					}
				}
			case domain.QuestionShortAnswer:
				text := domain.ShortAnswerText(ans)
				if strings.TrimSpace(text) != "" {
					shortTexts[question.ID] = append(shortTexts[question.ID], strings.TrimSpace(text))
				}
			default:
				questionAnswered[question.ID]++
				if questionDist[question.ID] == nil {
					questionDist[question.ID] = map[string]int{}
				}
				if ans != "" {
					questionDist[question.ID][ans]++
				}
				if isCorrectAnswer(question, ans) {
					questionCorrect[question.ID]++
					totalScore++
				}
				totalPossible++
			}
		}
	}

	var questionStats []ai.AdminQuestionStat
	for _, question := range q.Questions {
		if question.Type == domain.QuestionSurvey || question.Type == domain.QuestionShortAnswer {
			continue
		}
		answered := questionAnswered[question.ID]
		correct := questionCorrect[question.ID]
		rate := 0.0
		if answered > 0 {
			rate = float64(correct) / float64(answered)
		}
		var wrongAnswers []string
		dist := questionDist[question.ID]
		for key, count := range dist {
			if key != question.CorrectAnswer && count > 0 {
				optText := key
				for _, opt := range question.Options {
					if opt.Key == key {
						optText = opt.Key + "." + opt.Text
						break
					}
				}
				wrongAnswers = append(wrongAnswers, fmt.Sprintf("%s(%d人)", optText, count))
			}
		}
		questionStats = append(questionStats, ai.AdminQuestionStat{
			QuestionID:         question.ID,
			Stem:               question.Stem,
			KnowledgeTag:       question.KnowledgeTag,
			CorrectCount:       correct,
			AnsweredCount:      answered,
			CorrectRate:        rate,
			AnswerDistribution: dist,
			CommonWrongAnswers: wrongAnswers,
			Explanation:        question.Explanation,
		})
	}

	var feedbackItems []ai.AdminFeedbackItem
	for _, question := range q.Questions {
		if question.Type == domain.QuestionSurvey {
			counts := surveyDist[question.ID]
			if len(counts) > 0 {
				namedCounts := map[string]int{}
				for key, cnt := range counts {
					optText := key
					for _, opt := range question.Options {
						if opt.Key == key {
							optText = opt.Key + "." + opt.Text
							break
						}
					}
					namedCounts[optText] = cnt
				}
				feedbackItems = append(feedbackItems, ai.AdminFeedbackItem{
					QuestionID:   question.ID,
					Stem:         question.Stem,
					Type:         "survey",
					OptionCounts: namedCounts,
				})
			}
		}
		if question.Type == domain.QuestionShortAnswer {
			texts := shortTexts[question.ID]
			if len(texts) > 0 {
				samples := texts
				if len(samples) > 20 {
					samples = samples[:20]
				}
				feedbackItems = append(feedbackItems, ai.AdminFeedbackItem{
					QuestionID:  question.ID,
					Stem:        question.Stem,
					Type:        "short_answer",
					TextSamples: samples,
				})
			}
		}
	}

	avgScore := 0.0
	avgTotal := 0.0
	numStudents := len(latest)
	if numStudents > 0 {
		avgScore = float64(totalScore) / float64(numStudents)
		avgTotal = float64(totalPossible) / float64(numStudents)
	}

	return ai.AdminSummarizeInput{
		QuizID:        q.QuizID,
		QuizTitle:     q.Title,
		StudentCount:  len(latest),
		AvgScore:      avgScore,
		AvgTotal:      avgTotal,
		QuestionStats: questionStats,
		FeedbackItems: feedbackItems,
	}, nil
}

// currentCourse / matchedPDFPath were removed alongside the legacy global
// admin summary handlers. Per-course PDF matching lives in courseMatchedPDFPath.

// ── Teacher course-scoped summary ──

func (s *Server) apiTeacherCourseSummary(w http.ResponseWriter, r *http.Request) {
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
	s.quizMu.RLock()
	q := s.courseQuizzes[courseID]
	s.quizMu.RUnlock()
	if q == nil {
		http.Error(w, "当前未加载题库", http.StatusBadRequest)
		return
	}

	pdfPath := s.courseMatchedPDFPath(course.Slug, q)

	if r.Method == http.MethodDelete {
		_ = s.store.DeleteAdminSummary(r.Context(), courseID, q.QuizID)
		writeJSON(w, map[string]any{"ok": true})
		return
	}

	if r.Method == http.MethodPost {
		input, err := s.buildAdminSummaryInput(r.Context(), q, courseID)
		if err != nil {
			http.Error(w, "构建总结数据失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if pdfPath != "" {
			text, pErr := pdftext.ExtractText(pdfPath)
			if pErr == nil && text != "" {
				input.PDFContext = text
			}
		}
		summary, aiErr := s.aiClient.AdminSummarize(r.Context(), input)
		if aiErr != nil {
			writeJSON(w, map[string]any{"error": aiErr.Error(), "has_pdf": pdfPath != "", "quiz_id": q.QuizID, "course": course.Slug})
			return
		}
		summaryJSON, _ := json.Marshal(summary)
		_ = s.store.UpsertAdminSummary(r.Context(), courseID, q.QuizID, string(summaryJSON))
		writeJSON(w, map[string]any{"summary": summary, "has_pdf": pdfPath != "", "quiz_id": q.QuizID, "course": course.Slug})
		return
	}

	saved, err := s.store.GetAdminSummary(r.Context(), courseID, q.QuizID)
	// Build lightweight stats for display (no AI call), scoped to this course.
	stats := s.buildQuizRawStats(r.Context(), q, courseID)
	if err != nil || strings.TrimSpace(saved) == "" {
		writeJSON(w, map[string]any{"summary": nil, "has_pdf": pdfPath != "", "quiz_id": q.QuizID, "course": course.Slug, "stats": stats})
		return
	}
	var summary ai.AdminSummary
	if json.Unmarshal([]byte(saved), &summary) != nil {
		writeJSON(w, map[string]any{"summary": nil, "has_pdf": pdfPath != "", "quiz_id": q.QuizID, "course": course.Slug, "stats": stats})
		return
	}
	writeJSON(w, map[string]any{"summary": summary, "has_pdf": pdfPath != "", "quiz_id": q.QuizID, "course": course.Slug, "stats": stats})
}

// apiTeacherCourseHistorySummary generates/returns an AI summary across ALL quizzes for the course.
func (s *Server) apiTeacherCourseHistorySummary(w http.ResponseWriter, r *http.Request) {
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

	// Collect all submitted attempts for this course from the DB.
	allAttempts, err := s.store.ListAttemptsByCourse(r.Context(), courseID)
	if err != nil {
		http.Error(w, "读取答题记录失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Group by quiz_id.
	byQuiz := map[string][]domain.Attempt{}
	for _, a := range allAttempts {
		if a.Status == domain.StatusSubmitted {
			byQuiz[a.QuizID] = append(byQuiz[a.QuizID], a)
		}
	}
	if len(byQuiz) == 0 {
		http.Error(w, "该课程暂无已提交的答题记录", http.StatusBadRequest)
		return
	}

	// Build a map of all quiz titles from the on-disk quiz bank + currently loaded quiz.
	titleMap := s.quizBankTitles(course.TeacherID, course.Slug)
	s.quizMu.RLock()
	courseQuiz := s.courseQuizzes[courseID]
	s.quizMu.RUnlock()
	if courseQuiz != nil && courseQuiz.Title != "" {
		titleMap[courseQuiz.QuizID] = courseQuiz.Title
	}

	// For each quiz_id, load the quiz from disk to compute scores (not just the currently loaded one).
	loadQuizFromBank := func(qid string) *domain.Quiz {
		return s.loadCourseQuizFromBank(courseID, course.TeacherID, course.Slug, qid)
	}

	var quizStats []ai.HistoryQuizStat
	for qid, attempts := range byQuiz {
		quizObj := loadQuizFromBank(qid)
		var latest []domain.Attempt
		if quizObj != nil {
			latest = bestScoringAttempts(attempts, func(attemptID string) (int, int) {
				return s.calcScore(r.Context(), quizObj, attemptID)
			})
		} else {
			latest = latestAttempts(attempts)
		}
		totalScore, totalPossible := 0, 0
		for _, a := range latest {
			if quizObj != nil {
				answers, _ := s.store.GetAnswers(r.Context(), a.ID)
				for _, q := range shuffledQuestions(quizObj, a.ID) {
					if q.Type == domain.QuestionSurvey || q.Type == domain.QuestionShortAnswer {
						continue
					}
					totalPossible++
					if isCorrectAnswer(q, answers[q.ID]) {
						totalScore++
					}
				}
			}
		}
		avgScore := 0.0
		avgTotal := 0.0
		if len(latest) > 0 && totalPossible > 0 {
			avgScore = float64(totalScore) / float64(len(latest))
			avgTotal = float64(totalPossible) / float64(len(latest))
		}
		title := qid
		if t, ok := titleMap[qid]; ok && t != "" {
			title = t
		}
		quizStats = append(quizStats, ai.HistoryQuizStat{
			QuizID:       qid,
			QuizTitle:    title,
			StudentCount: len(latest),
			AvgScore:     avgScore,
			AvgTotal:     avgTotal,
		})
	}
	// Sort by quiz_id for consistent ordering.
	sort.Slice(quizStats, func(i, j int) bool { return quizStats[i].QuizID < quizStats[j].QuizID })

	// A special synthetic quiz_id is used to store the per-course history summary.
	const historyKey = "__history__"

	if r.Method == http.MethodDelete {
		_ = s.store.DeleteAdminSummary(r.Context(), courseID, historyKey)
		writeJSON(w, map[string]any{"ok": true})
		return
	}

	if r.Method == http.MethodPost {
		input := ai.HistorySummarizeInput{
			CourseName: course.Name,
			QuizStats:  quizStats,
		}
		summary, aiErr := s.aiClient.HistorySummarize(r.Context(), input)
		if aiErr != nil {
			writeJSON(w, map[string]any{"error": aiErr.Error()})
			return
		}
		// Persist for subsequent GETs.
		if b, err := json.Marshal(summary); err == nil {
			_ = s.store.UpsertAdminSummary(r.Context(), courseID, historyKey, string(b))
		}
		writeJSON(w, map[string]any{"summary": summary, "quiz_stats": quizStats})
		return
	}

	// GET: return stats + stored summary if available.
	if saved, err := s.store.GetAdminSummary(r.Context(), courseID, historyKey); err == nil && strings.TrimSpace(saved) != "" {
		var summary ai.HistorySummary
		if json.Unmarshal([]byte(saved), &summary) == nil {
			writeJSON(w, map[string]any{"summary": &summary, "quiz_stats": quizStats})
			return
		}
	}
	writeJSON(w, map[string]any{"quiz_stats": quizStats})
}

// quizBankTitles scans the quiz bank on disk and returns a quiz_id→title map.
// Keys are both the YAML internal quiz_id AND the directory name so lookups
// succeed regardless of whether the caller uses one or the other.
func (s *Server) quizBankTitles(teacherID, courseSlug string) map[string]string {
	result := map[string]string{}
	quizRoot := filepath.Join(s.metadataCourseDir(teacherID, courseSlug), "quiz")
	dirs, _ := os.ReadDir(quizRoot)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dirName := d.Name()
		subDir := filepath.Join(quizRoot, dirName)
		files, _ := os.ReadDir(subDir)
		for _, f := range files {
			name := strings.ToLower(f.Name())
			if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(subDir, f.Name()))
			if err != nil {
				continue
			}
			q, err := quiz.Parse(data)
			if err != nil {
				continue
			}
			if q.Title != "" {
				// Key by directory name (used by quiz bank API).
				result[dirName] = q.Title
				// Also key by YAML internal quiz_id (used in stored attempts).
				if q.QuizID != "" && q.QuizID != dirName {
					result[q.QuizID] = q.Title
				}
			}
			break
		}
	}
	return result
}

func (s *Server) loadCourseQuizFromBank(courseID int, teacherID, courseSlug, quizID string) *domain.Quiz {
	quizID = strings.TrimSpace(quizID)
	if quizID == "" {
		return nil
	}

	s.quizMu.RLock()
	courseQuiz := s.courseQuizzes[courseID]
	s.quizMu.RUnlock()
	if courseQuiz != nil && courseQuiz.QuizID == quizID {
		return courseQuiz
	}

	quizRoot := filepath.Join(s.metadataCourseDir(teacherID, courseSlug), "quiz")
	parseFirstYAML := func(dirPath string) *domain.Quiz {
		files, _ := os.ReadDir(dirPath)
		for _, f := range files {
			name := strings.ToLower(f.Name())
			if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dirPath, f.Name()))
			if err != nil {
				continue
			}
			q, err := quiz.Parse(data)
			if err == nil {
				return q
			}
		}
		return nil
	}

	if q := parseFirstYAML(filepath.Join(quizRoot, safePathPart(quizID))); q != nil {
		return q
	}
	dirs, _ := os.ReadDir(quizRoot)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		if q := parseFirstYAML(filepath.Join(quizRoot, d.Name())); q != nil && q.QuizID == quizID {
			return q
		}
	}
	return nil
}

func (s *Server) courseMatchedPDFPath(courseSlug string, q *domain.Quiz) string {
	stem := strings.TrimSuffix(q.QuizID, filepath.Ext(q.QuizID))
	pattern := filepath.Join(s.cfg.MetadataDir, "*", safePathPart(courseSlug), "materials", stem+".pdf")
	if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
		return matches[0]
	}
	return ""
}

// buildQuizRawStats returns per-student score rows for the current quiz without AI.
// courseID scopes the query; pass 0 to fall back to the global quiz_id scan (legacy).
func (s *Server) buildQuizRawStats(ctx context.Context, q *domain.Quiz, courseID int) map[string]any {
	var (
		allAttempts []domain.Attempt
		err         error
	)
	if courseID > 0 {
		all, e := s.store.ListAttemptsByCourse(ctx, courseID)
		if e != nil {
			return map[string]any{"error": e.Error()}
		}
		// Keep only attempts for this quiz.
		for _, a := range all {
			if a.QuizID == q.QuizID {
				allAttempts = append(allAttempts, a)
			}
		}
	} else {
		allAttempts, err = s.listAttemptsByQuizID(ctx, q.QuizID)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
	}
	latest := bestScoringAttempts(allAttempts, func(attemptID string) (int, int) {
		return s.calcScore(ctx, q, attemptID)
	})
	rows := make([]map[string]any, 0, len(latest))
	totalScore := 0
	totalPossible := 0
	for _, a := range latest {
		answers, _ := s.store.GetAnswers(ctx, a.ID)
		correct := 0
		possible := 0
		for _, question := range shuffledQuestions(q, a.ID) {
			if question.Type == domain.QuestionSurvey || question.Type == domain.QuestionShortAnswer {
				continue
			}
			possible++
			if isCorrectAnswer(question, answers[question.ID]) {
				correct++
			}
		}
		totalScore += correct
		totalPossible += possible
		rows = append(rows, map[string]any{
			"name":       a.Name,
			"student_no": a.StudentNo,
			"correct":    correct,
			"total":      possible,
			"attempt_no": a.AttemptNo,
		})
	}
	avgScore := 0.0
	avgTotal := 0.0
	if len(latest) > 0 {
		avgScore = float64(totalScore) / float64(len(latest))
		avgTotal = float64(totalPossible) / float64(len(latest))
	}
	return map[string]any{
		"student_count": len(latest),
		"avg_score":     avgScore,
		"avg_total":     avgTotal,
		"students":      rows,
	}
}

// latestAttempts picks the attempt with the highest attempt_no per student, grouped by name.
func latestAttempts(all []domain.Attempt) []domain.Attempt {
	best := map[string]*domain.Attempt{}
	for i := range all {
		a := &all[i]
		if a.Status != domain.StatusSubmitted {
			continue
		}
		existing, ok := best[a.Name]
		if !ok || a.AttemptNo > existing.AttemptNo {
			best[a.Name] = a
		}
	}
	result := make([]domain.Attempt, 0, len(best))
	for _, a := range best {
		result = append(result, *a)
	}
	return result
}

// bestScoringAttempts picks the highest-scoring attempt per student (grouped by name).
// scoreFn returns (correct, total) for a given attempt ID.
func bestScoringAttempts(all []domain.Attempt, scoreFn func(attemptID string) (int, int)) []domain.Attempt {
	type scored struct {
		attempt *domain.Attempt
		correct int
	}
	best := map[string]*scored{}
	for i := range all {
		a := &all[i]
		if a.Status != domain.StatusSubmitted {
			continue
		}
		c, _ := scoreFn(a.ID)
		existing, ok := best[a.Name]
		if !ok || c > existing.correct || (c == existing.correct && a.AttemptNo > existing.attempt.AttemptNo) {
			best[a.Name] = &scored{attempt: a, correct: c}
		}
	}
	result := make([]domain.Attempt, 0, len(best))
	for _, s := range best {
		result = append(result, *s.attempt)
	}
	return result
}
