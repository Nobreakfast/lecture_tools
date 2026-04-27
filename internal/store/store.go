// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package store

import (
	"context"

	"course-assistant/internal/domain"
)

// SettingStore provides global key-value configuration persistence.
type SettingStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

// TeacherStore provides teacher CRUD operations.
type TeacherStore interface {
	CreateTeacher(ctx context.Context, t *domain.Teacher) error
	GetTeacher(ctx context.Context, id string) (*domain.Teacher, error)
	ListTeachers(ctx context.Context) ([]domain.Teacher, error)
	UpdateTeacherPassword(ctx context.Context, id, passwordHash string) error
	UpdateTeacherRole(ctx context.Context, id string, role domain.UserRole) error
	DeleteTeacher(ctx context.Context, id string) error
}

// CourseStore provides course CRUD and state operations.
type CourseStore interface {
	CreateCourse(ctx context.Context, c *domain.Course) error
	GetCourse(ctx context.Context, id int) (*domain.Course, error)
	GetCourseByInviteCode(ctx context.Context, code string) (*domain.Course, error)
	ListCoursesByTeacher(ctx context.Context, teacherID string) ([]domain.Course, error)
	ListAllCourses(ctx context.Context) ([]domain.Course, error)
	UpdateCourse(ctx context.Context, c *domain.Course) error
	DeleteCourse(ctx context.Context, id int) error
	GetCourseState(ctx context.Context, courseID int) (*domain.CourseState, error)
	SetCourseState(ctx context.Context, cs *domain.CourseState) error
}

// AttemptStore provides quiz-attempt and answer operations.
type AttemptStore interface {
	CreateAttempt(ctx context.Context, a *domain.Attempt) error
	ListAttempts(ctx context.Context) ([]domain.Attempt, error)
	ListAttemptsByCourse(ctx context.Context, courseID int) ([]domain.Attempt, error)
	GetAttemptByID(ctx context.Context, attemptID string) (*domain.Attempt, error)
	GetAttemptByToken(ctx context.Context, token string) (*domain.Attempt, error)
	UpdateAttemptStatus(ctx context.Context, attemptID string, status domain.AttemptStatus) error
	SubmitAttempt(ctx context.Context, attemptID string) (int, error)
	SaveAnswer(ctx context.Context, answer domain.Answer) error
	GetAnswers(ctx context.Context, attemptID string) (map[string]string, error)
	UpsertSummary(ctx context.Context, attemptID string, summaryJSON string) error
	GetSummary(ctx context.Context, attemptID string) (string, error)
	GetLiveStats(ctx context.Context) (int, int, error)
	GetLiveStatsByCourse(ctx context.Context, courseID int) (int, int, error)
	GetLiveStatsByCourseQuiz(ctx context.Context, courseID int, quizID string) (int, int, error)
	GetInProgressAttempt(ctx context.Context, quizID, studentNo string, courseID int) (*domain.Attempt, error)
	UpdateAttemptSession(ctx context.Context, attemptID, token, name, className string, courseID int) error
	UpsertAdminSummary(ctx context.Context, courseID int, quizID string, summaryJSON string) error
	GetAdminSummary(ctx context.Context, courseID int, quizID string) (string, error)
	DeleteAdminSummary(ctx context.Context, courseID int, quizID string) error
	ClearAttempts(ctx context.Context, quizID string) error
	ClearAttemptsByCourse(ctx context.Context, courseID int, quizID string) error
	FixLegacyAttemptsCourse(ctx context.Context, quizID string, courseID int) (int, error)
	FixAllLegacyAttemptsCourse(ctx context.Context, quizIDs []string, courseID int) (int, error)
	// Quiz share operations
	CreateQuizShare(ctx context.Context, qs *domain.QuizShare) error
	GetQuizShareByID(ctx context.Context, id int) (*domain.QuizShare, error)
	GetQuizShareByToken(ctx context.Context, token string) (*domain.QuizShare, error)
	ListActiveQuizShares(ctx context.Context, courseID int, quizID string) ([]domain.QuizShare, error)
	RevokeQuizShare(ctx context.Context, id int) error
}

// HomeworkStore provides homework submission and file operations.
type HomeworkStore interface {
	CreateHomeworkSubmission(ctx context.Context, submission *domain.HomeworkSubmission) error
	GetHomeworkSubmissionByID(ctx context.Context, submissionID string) (*domain.HomeworkSubmission, error)
	GetHomeworkSubmissionByToken(ctx context.Context, token string) (*domain.HomeworkSubmission, error)
	GetHomeworkSubmissionByScope(ctx context.Context, courseID int, course, assignmentID, studentNo string) (*domain.HomeworkSubmission, error)
	UpdateHomeworkSubmissionSession(ctx context.Context, submissionID, token, name, className, secretKey string) error
	ListHomeworkSubmissions(ctx context.Context, courseID int, course, assignmentID string) ([]domain.HomeworkSubmission, error)
	SaveHomeworkFileMetadata(ctx context.Context, submissionID string, slot domain.HomeworkFileSlot, originalName string) error
	DeleteHomeworkFileMetadata(ctx context.Context, submissionID string, slot domain.HomeworkFileSlot) error
	DeleteHomeworkSubmission(ctx context.Context, submissionID string) error
}

// Store is the composition of all domain-scoped store interfaces.
// New consumers should prefer accepting the smallest interface they need
// (e.g. AttemptStore) rather than the full Store.
type Store interface {
	SettingStore
	TeacherStore
	CourseStore
	AttemptStore
	HomeworkStore
	Init(ctx context.Context) error
	Close() error
}