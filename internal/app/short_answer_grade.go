// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"course-assistant/internal/ai"
	"course-assistant/internal/domain"
)

type quizScoreDetail struct {
	Correct float64
	Total   int
	Pending int
	Errors  int
}

func shortAnswerUsesAgentScoring(q domain.Question) bool {
	return q.Type == domain.QuestionShortAnswer &&
		q.ScoreWithAgent &&
		strings.TrimSpace(q.ReferenceAnswer) != ""
}

func (s *Server) maybeStartShortAnswerGrading(ctx context.Context, attempt *domain.Attempt, q *domain.Quiz) {
	if attempt == nil || q == nil || !quizHasAgentScoredShortAnswer(q) {
		return
	}
	if err := s.ensureShortAnswerGradePlaceholders(ctx, attempt, q); err != nil {
		logAgentGradeError(ctx, s, attempt, q, "prepare", err)
	}

	s.shortAnswerGradeMu.Lock()
	if s.shortAnswerGradeJobs == nil {
		s.shortAnswerGradeJobs = map[string]struct{}{}
	}
	if _, ok := s.shortAnswerGradeJobs[attempt.ID]; ok {
		s.shortAnswerGradeMu.Unlock()
		return
	}
	s.shortAnswerGradeJobs[attempt.ID] = struct{}{}
	s.shortAnswerGradeMu.Unlock()

	attemptCopy := *attempt
	quizCopy := *q
	quizCopy.Questions = append([]domain.Question(nil), q.Questions...)
	go func() {
		defer func() {
			s.shortAnswerGradeMu.Lock()
			delete(s.shortAnswerGradeJobs, attemptCopy.ID)
			s.shortAnswerGradeMu.Unlock()
		}()
		s.gradeSubmittedShortAnswers(context.Background(), &attemptCopy, &quizCopy)
	}()
}

func quizHasAgentScoredShortAnswer(q *domain.Quiz) bool {
	for _, question := range q.Questions {
		if shortAnswerUsesAgentScoring(question) {
			return true
		}
	}
	return false
}

func (s *Server) ensureShortAnswerGradePlaceholders(ctx context.Context, attempt *domain.Attempt, q *domain.Quiz) error {
	answers, err := s.store.GetAnswers(ctx, attempt.ID)
	if err != nil {
		return err
	}
	existing, err := s.store.GetShortAnswerGrades(ctx, attempt.ID)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, question := range q.Questions {
		if !shortAnswerUsesAgentScoring(question) {
			continue
		}
		if grade, ok := existing[question.ID]; ok && grade.Status != "" {
			continue
		}
		text := strings.TrimSpace(domain.ShortAnswerText(answers[question.ID]))
		grade := domain.ShortAnswerGrade{
			AttemptID:  attempt.ID,
			QuestionID: question.ID,
			Status:     domain.ShortAnswerGradePending,
			UpdatedAt:  now,
		}
		if text == "" {
			score := 0.0
			grade.Status = domain.ShortAnswerGradeGraded
			grade.Score = &score
			grade.Feedback = "未作答，自动记为 0 分。"
			grade.GradedAt = &now
		}
		if err := s.store.UpsertShortAnswerGrade(ctx, grade); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) gradeSubmittedShortAnswers(ctx context.Context, attempt *domain.Attempt, q *domain.Quiz) {
	answers, err := s.store.GetAnswers(ctx, attempt.ID)
	if err != nil {
		logAgentGradeError(ctx, s, attempt, q, "answers", err)
		return
	}
	for _, question := range q.Questions {
		if !shortAnswerUsesAgentScoring(question) {
			continue
		}
		text := strings.TrimSpace(domain.ShortAnswerText(answers[question.ID]))
		if text == "" {
			continue
		}
		grades, err := s.store.GetShortAnswerGrades(ctx, attempt.ID)
		if err != nil {
			logAgentGradeError(ctx, s, attempt, q, question.ID, err)
			continue
		}
		if grade, ok := grades[question.ID]; ok && grade.Status == domain.ShortAnswerGradeGraded && grade.Score != nil {
			continue
		}

		release, err := s.acquireShortAnswerGradeSlot(ctx)
		if err != nil {
			_ = s.store.UpsertShortAnswerGrade(ctx, shortAnswerGradeError(attempt.ID, question.ID, err))
			continue
		}
		result, raw, err := s.runShortAnswerGradeAgent(ctx, q, question, attempt, text)
		release()

		now := time.Now()
		if err != nil {
			_ = s.store.UpsertShortAnswerGrade(ctx, shortAnswerGradeError(attempt.ID, question.ID, err))
			continue
		}
		score := clampScore(result.Score)
		_ = s.store.UpsertShortAnswerGrade(ctx, domain.ShortAnswerGrade{
			AttemptID:   attempt.ID,
			QuestionID:  question.ID,
			Status:      domain.ShortAnswerGradeGraded,
			Score:       &score,
			Feedback:    result.Feedback,
			RawResponse: raw,
			GradedAt:    &now,
			UpdatedAt:   now,
		})
	}
}

func (s *Server) acquireShortAnswerGradeSlot(ctx context.Context) (func(), error) {
	s.shortAnswerGradeMu.Lock()
	if s.shortAnswerGradeSem == nil {
		s.shortAnswerGradeSem = make(chan struct{}, 3)
	}
	sem := s.shortAnswerGradeSem
	s.shortAnswerGradeMu.Unlock()
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Server) runShortAnswerGradeAgent(ctx context.Context, q *domain.Quiz, question domain.Question, attempt *domain.Attempt, answer string) (ai.ShortAnswerGradeResult, string, error) {
	if s.aiClient == nil {
		return ai.ShortAnswerGradeResult{}, "", fmt.Errorf("AI client 未初始化")
	}
	input := ai.ShortAnswerGradeInput{
		QuizTitle:       q.Title,
		QuestionID:      question.ID,
		Stem:            question.Stem,
		ReferenceAnswer: question.ReferenceAnswer,
		ScoringRubric:   question.ScoringRubric,
		StudentAnswer:   answer,
	}
	result, raw, err := s.aiClient.GradeShortAnswer(ctx, input)
	s.saveShortAnswerGradeTrajectory(ctx, q, question, attempt, input, raw, result, err)
	return result, raw, err
}

func (s *Server) saveShortAnswerGradeTrajectory(ctx context.Context, q *domain.Quiz, question domain.Question, attempt *domain.Attempt, input ai.ShortAnswerGradeInput, raw string, result ai.ShortAnswerGradeResult, err error) {
	teacherLabel := "unknown_teacher"
	courseLabel := q.QuizID
	if attempt.CourseID > 0 {
		if c, getErr := s.store.GetCourse(ctx, attempt.CourseID); getErr == nil && c != nil {
			teacherLabel = c.TeacherID
			courseLabel = c.Name
		}
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	s.saveAgentTrajectory(ctx, agentTrajectory{
		Kind:      "quiz_short_answer_grade",
		CreatedAt: time.Now(),
		Teacher:   teacherLabel,
		Course:    courseLabel,
		Name:      attempt.Name,
		Request: map[string]any{
			"attempt_id":  attempt.ID,
			"quiz_id":     q.QuizID,
			"question_id": question.ID,
			"student_no":  attempt.StudentNo,
			"input":       input,
		},
		RawResponse: raw,
		Decision:    result,
		Error:       errText,
	})
}

func logAgentGradeError(ctx context.Context, s *Server, attempt *domain.Attempt, q *domain.Quiz, stage string, err error) {
	if err == nil {
		return
	}
	s.saveAgentTrajectory(ctx, agentTrajectory{
		Kind:      "quiz_short_answer_grade",
		CreatedAt: time.Now(),
		Teacher:   "unknown_teacher",
		Course:    q.QuizID,
		Name:      attempt.Name,
		Request: map[string]any{
			"attempt_id": attempt.ID,
			"quiz_id":    q.QuizID,
			"stage":      stage,
		},
		Error: err.Error(),
	})
}

func shortAnswerGradeError(attemptID, questionID string, err error) domain.ShortAnswerGrade {
	now := time.Now()
	return domain.ShortAnswerGrade{
		AttemptID:  attemptID,
		QuestionID: questionID,
		Status:     domain.ShortAnswerGradeError,
		Error:      err.Error(),
		UpdatedAt:  now,
	}
}

func (s *Server) calcScoreDetail(ctx context.Context, q *domain.Quiz, attemptID string) quizScoreDetail {
	answers, err := s.store.GetAnswers(ctx, attemptID)
	if err != nil {
		return quizScoreDetail{}
	}
	grades, _ := s.store.GetShortAnswerGrades(ctx, attemptID)
	questions := shuffledQuestions(q, attemptID)
	var detail quizScoreDetail
	for _, item := range questions {
		if item.Type == domain.QuestionSurvey {
			continue
		}
		if item.Type == domain.QuestionShortAnswer {
			if !shortAnswerUsesAgentScoring(item) {
				continue
			}
			grade, ok := grades[item.ID]
			switch {
			case ok && grade.Status == domain.ShortAnswerGradeGraded && grade.Score != nil:
				detail.Total++
				detail.Correct += clampScore(*grade.Score)
			case ok && grade.Status == domain.ShortAnswerGradeError:
				detail.Errors++
			default:
				detail.Pending++
			}
			continue
		}
		detail.Total++
		if isCorrectAnswer(item, answers[item.ID]) {
			detail.Correct++
		}
	}
	detail.Correct = math.Round(detail.Correct*10) / 10
	return detail
}

func clampScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return math.Round(v*10) / 10
}

func formatScoreValue(v float64) string {
	if math.Abs(v-math.Round(v)) < 0.000001 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

func shortAnswerGradePayload(grade domain.ShortAnswerGrade) map[string]any {
	payload := map[string]any{
		"status": string(grade.Status),
	}
	if grade.Score != nil {
		payload["score"] = *grade.Score
	}
	if strings.TrimSpace(grade.Feedback) != "" {
		payload["feedback"] = grade.Feedback
	}
	if strings.TrimSpace(grade.Error) != "" {
		payload["error"] = grade.Error
	}
	if grade.GradedAt != nil {
		payload["graded_at"] = grade.GradedAt.Format(time.RFC3339)
	}
	return payload
}
