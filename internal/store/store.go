package store

import (
	"context"

	"course-assistant/internal/domain"
)

type Store interface {
	Init(ctx context.Context) error
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	CreateAttempt(ctx context.Context, a *domain.Attempt) error
	ListAttempts(ctx context.Context) ([]domain.Attempt, error)
	GetAttemptByID(ctx context.Context, attemptID string) (*domain.Attempt, error)
	GetAttemptByToken(ctx context.Context, token string) (*domain.Attempt, error)
	UpdateAttemptStatus(ctx context.Context, attemptID string, status domain.AttemptStatus) error
	SubmitAttempt(ctx context.Context, attemptID string) (int, error)
	SaveAnswer(ctx context.Context, answer domain.Answer) error
	GetAnswers(ctx context.Context, attemptID string) (map[string]string, error)
	UpsertSummary(ctx context.Context, attemptID string, summaryJSON string) error
	GetSummary(ctx context.Context, attemptID string) (string, error)
	GetLiveStats(ctx context.Context) (int, int, error)
	GetInProgressAttempt(ctx context.Context, quizID, studentNo string) (*domain.Attempt, error)
	UpdateAttemptSession(ctx context.Context, attemptID, token, name, className string) error
	UpsertAdminSummary(ctx context.Context, quizID string, summaryJSON string) error
	GetAdminSummary(ctx context.Context, quizID string) (string, error)
	DeleteAdminSummary(ctx context.Context, quizID string) error
	ClearAttempts(ctx context.Context, quizID string) error
	CreateHomeworkSubmission(ctx context.Context, submission *domain.HomeworkSubmission) error
	GetHomeworkSubmissionByID(ctx context.Context, submissionID string) (*domain.HomeworkSubmission, error)
	GetHomeworkSubmissionByToken(ctx context.Context, token string) (*domain.HomeworkSubmission, error)
	GetHomeworkSubmissionByScope(ctx context.Context, course, assignmentID, studentNo string) (*domain.HomeworkSubmission, error)
	UpdateHomeworkSubmissionSession(ctx context.Context, submissionID, token, name, className, secretKey string) error
	ListHomeworkSubmissions(ctx context.Context, course, assignmentID string) ([]domain.HomeworkSubmission, error)
	SaveHomeworkFileMetadata(ctx context.Context, submissionID string, slot domain.HomeworkFileSlot, originalName string) error
	DeleteHomeworkFileMetadata(ctx context.Context, submissionID string, slot domain.HomeworkFileSlot) error
}
