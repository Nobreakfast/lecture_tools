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
	ClearAttempts(ctx context.Context, quizID string) error
}
