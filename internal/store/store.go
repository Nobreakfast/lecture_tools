// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package store

import (
	"context"

	"course-assistant/internal/domain"
)

type Store interface {
	Init(ctx context.Context) error
	Close() error

	// Settings (global key-value)
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error

	// Teachers
	CreateTeacher(ctx context.Context, t *domain.Teacher) error
	GetTeacher(ctx context.Context, id string) (*domain.Teacher, error)
	ListTeachers(ctx context.Context) ([]domain.Teacher, error)
	UpdateTeacherPassword(ctx context.Context, id, passwordHash string) error
	UpdateTeacherRole(ctx context.Context, id string, role domain.UserRole) error
	DeleteTeacher(ctx context.Context, id string) error

	// Courses
	CreateCourse(ctx context.Context, c *domain.Course) error
	GetCourse(ctx context.Context, id int) (*domain.Course, error)
	GetCourseByInviteCode(ctx context.Context, code string) (*domain.Course, error)
	ListCoursesByTeacher(ctx context.Context, teacherID string) ([]domain.Course, error)
	ListAllCourses(ctx context.Context) ([]domain.Course, error)
	UpdateCourse(ctx context.Context, c *domain.Course) error
	DeleteCourse(ctx context.Context, id int) error

	// Course state (per-course quiz runtime)
	GetCourseState(ctx context.Context, courseID int) (*domain.CourseState, error)
	SetCourseState(ctx context.Context, cs *domain.CourseState) error

	// Quiz attempts
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
	// GetLiveStatsByCourseQuiz counts attempts for a specific (course_id, quiz_id)
	// so that the "已进入 / 已提交" meters only reflect the currently loaded quiz.
	GetLiveStatsByCourseQuiz(ctx context.Context, courseID int, quizID string) (int, int, error)
	// courseID must match the attempt's course scope (0 for the legacy global quiz).
	GetInProgressAttempt(ctx context.Context, quizID, studentNo string, courseID int) (*domain.Attempt, error)
	// courseID: when > 0, also persist course_id (fixes resumed sessions that had legacy 0/missing course scope).
	UpdateAttemptSession(ctx context.Context, attemptID, token, name, className string, courseID int) error
	// Admin/AI summaries are keyed by (course_id, quiz_id). Pass courseID=0 for the legacy global namespace.
	UpsertAdminSummary(ctx context.Context, courseID int, quizID string, summaryJSON string) error
	GetAdminSummary(ctx context.Context, courseID int, quizID string) (string, error)
	DeleteAdminSummary(ctx context.Context, courseID int, quizID string) error
	ClearAttempts(ctx context.Context, quizID string) error
	// ClearAttemptsByCourse deletes attempts, answers, summaries, and admin_summaries
	// scoped to a specific (courseID, quizID) pair instead of all courses.
	ClearAttemptsByCourse(ctx context.Context, courseID int, quizID string) error
	// FixLegacyAttemptsCourse sets course_id on attempts where course_id=0 and quiz_id matches, returning the count updated.
	FixLegacyAttemptsCourse(ctx context.Context, quizID string, courseID int) (int, error)
	// FixAllLegacyAttemptsCourse sets course_id on all course_id=0 attempts whose quiz_id is in the provided list.
	FixAllLegacyAttemptsCourse(ctx context.Context, quizIDs []string, courseID int) (int, error)

	// Homework
	CreateHomeworkSubmission(ctx context.Context, submission *domain.HomeworkSubmission) error
	GetHomeworkSubmissionByID(ctx context.Context, submissionID string) (*domain.HomeworkSubmission, error)
	GetHomeworkSubmissionByToken(ctx context.Context, token string) (*domain.HomeworkSubmission, error)
	// GetHomeworkSubmissionByScope looks up by (courseID, assignmentID, studentNo).
	// Falls back to legacy (course slug) lookup when courseID <= 0.
	GetHomeworkSubmissionByScope(ctx context.Context, courseID int, course, assignmentID, studentNo string) (*domain.HomeworkSubmission, error)
	UpdateHomeworkSubmissionSession(ctx context.Context, submissionID, token, name, className, secretKey string) error
	// ListHomeworkSubmissions lists submissions scoped by courseID.
	// Falls back to legacy (course slug) lookup when courseID <= 0.
	ListHomeworkSubmissions(ctx context.Context, courseID int, course, assignmentID string) ([]domain.HomeworkSubmission, error)
	SaveHomeworkFileMetadata(ctx context.Context, submissionID string, slot domain.HomeworkFileSlot, originalName string) error
	DeleteHomeworkFileMetadata(ctx context.Context, submissionID string, slot domain.HomeworkFileSlot) error
	DeleteHomeworkSubmission(ctx context.Context, submissionID string) error
}
