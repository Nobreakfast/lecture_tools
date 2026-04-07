package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"course-assistant/internal/ai"
	"course-assistant/internal/domain"
	"course-assistant/internal/pdftext"
)

func (s *Server) apiAdminSummaryGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil {
		http.Error(w, "当前未加载题库", http.StatusBadRequest)
		return
	}
	saved, err := s.store.GetAdminSummary(r.Context(), current.QuizID)
	if err != nil || strings.TrimSpace(saved) == "" {
		writeJSON(w, map[string]any{"summary": nil, "has_pdf": s.matchedPDFPath() != "", "quiz_id": current.QuizID, "course": s.currentCourse()})
		return
	}
	var summary ai.AdminSummary
	if json.Unmarshal([]byte(saved), &summary) != nil {
		writeJSON(w, map[string]any{"summary": nil, "has_pdf": s.matchedPDFPath() != "", "quiz_id": current.QuizID, "course": s.currentCourse()})
		return
	}
	writeJSON(w, map[string]any{"summary": summary, "has_pdf": s.matchedPDFPath() != "", "quiz_id": current.QuizID, "course": s.currentCourse()})
}

func (s *Server) apiAdminSummaryGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mu.RLock()
	current := s.currentQuiz
	s.mu.RUnlock()
	if current == nil {
		http.Error(w, "当前未加载题库", http.StatusBadRequest)
		return
	}

	input, err := s.buildAdminSummaryInput(r.Context(), current)
	if err != nil {
		http.Error(w, "构建总结数据失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	pdfPath := s.matchedPDFPath()
	if pdfPath != "" {
		text, pErr := pdftext.ExtractText(pdfPath)
		if pErr == nil && text != "" {
			input.PDFContext = text
		}
	}

	summary, aiErr := s.aiClient.AdminSummarize(r.Context(), input)
	if aiErr != nil {
		writeJSON(w, map[string]any{"error": aiErr.Error(), "has_pdf": pdfPath != "", "quiz_id": current.QuizID, "course": s.currentCourse()})
		return
	}

	summaryJSON, _ := json.Marshal(summary)
	_ = s.store.UpsertAdminSummary(r.Context(), current.QuizID, string(summaryJSON))
	writeJSON(w, map[string]any{"summary": summary, "has_pdf": pdfPath != "", "quiz_id": current.QuizID, "course": s.currentCourse()})
}

func (s *Server) buildAdminSummaryInput(ctx context.Context, q *domain.Quiz) (ai.AdminSummarizeInput, error) {
	allAttempts, err := s.listAttemptsByQuizID(ctx, q.QuizID)
	if err != nil {
		return ai.AdminSummarizeInput{}, err
	}

	latest := latestAttempts(allAttempts)
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
	if totalPossible > 0 {
		avgScore = float64(totalScore) / float64(len(latest))
	}

	return ai.AdminSummarizeInput{
		QuizID:        q.QuizID,
		QuizTitle:     q.Title,
		StudentCount:  len(latest),
		AvgScore:      avgScore,
		QuestionStats: questionStats,
		FeedbackItems: feedbackItems,
	}, nil
}

func (s *Server) currentCourse() string {
	sourcePath, err := s.store.GetSetting(context.Background(), "quiz_source_path")
	if err != nil || strings.TrimSpace(sourcePath) == "" {
		return ""
	}
	dir := filepath.Dir(sourcePath)
	return filepath.Base(dir)
}

func (s *Server) matchedPDFPath() string {
	sourcePath, err := s.store.GetSetting(context.Background(), "quiz_source_path")
	if err != nil || strings.TrimSpace(sourcePath) == "" {
		return ""
	}
	dir := filepath.Dir(sourcePath)
	folder := filepath.Base(dir)
	stem := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
	pdfDir := filepath.Join(filepath.Dir(s.cfg.DataDir), "ppt")
	pdfPath := filepath.Join(pdfDir, folder, stem+".pdf")
	if _, err := fileExists(pdfPath); err == nil {
		return pdfPath
	}
	return ""
}

func latestAttempts(all []domain.Attempt) []domain.Attempt {
	best := map[string]*domain.Attempt{}
	for i := range all {
		a := &all[i]
		if a.Status != domain.StatusSubmitted {
			continue
		}
		existing, ok := best[a.StudentNo]
		if !ok || a.AttemptNo > existing.AttemptNo {
			best[a.StudentNo] = a
		}
	}
	result := make([]domain.Attempt, 0, len(best))
	for _, a := range best {
		result = append(result, *a)
	}
	return result
}
