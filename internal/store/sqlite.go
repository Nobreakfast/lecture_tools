// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package store

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"course-assistant/internal/domain"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Init(ctx context.Context) error {
	pragmas := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA synchronous = NORMAL;`,
		`PRAGMA busy_timeout = 5000;`,
		`PRAGMA foreign_keys = ON;`,
	}
	for _, p := range pragmas {
		if _, err := s.db.ExecContext(ctx, p); err != nil {
			return err
		}
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS teachers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'teacher',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS courses (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			teacher_id TEXT NOT NULL REFERENCES teachers(id),
			name TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			internal_name TEXT NOT NULL DEFAULT '',
			slug TEXT NOT NULL,
			invite_code TEXT UNIQUE NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(teacher_id, slug)
		);`,
		`CREATE TABLE IF NOT EXISTS course_state (
			course_id INTEGER PRIMARY KEY REFERENCES courses(id),
			entry_open INTEGER NOT NULL DEFAULT 0,
			quiz_yaml TEXT,
			quiz_source_path TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS attempts (
			id TEXT PRIMARY KEY,
			session_token TEXT UNIQUE NOT NULL,
			quiz_id TEXT NOT NULL DEFAULT '',
			course_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			student_no TEXT NOT NULL,
			class_name TEXT NOT NULL,
			attempt_no INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			submitted_at TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS answers (
			attempt_id TEXT NOT NULL,
			question_id TEXT NOT NULL,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (attempt_id, question_id)
		);`,
		`CREATE TABLE IF NOT EXISTS summaries (
			attempt_id TEXT PRIMARY KEY,
			summary_json TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS admin_summaries (
			quiz_id TEXT NOT NULL,
			course_id INTEGER NOT NULL DEFAULT 0,
			summary_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(course_id, quiz_id)
		);`,
		`CREATE TABLE IF NOT EXISTS quiz_share (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			course_id INTEGER NOT NULL DEFAULT 0,
			quiz_id TEXT NOT NULL,
			share_token TEXT UNIQUE NOT NULL,
			created_at TEXT NOT NULL,
			revoked_at TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS homework_qa (
			id TEXT PRIMARY KEY,
			course TEXT NOT NULL,
			course_id INTEGER NOT NULL DEFAULT 0,
			assignment_id TEXT NOT NULL,
			question TEXT NOT NULL,
			question_images_json TEXT NOT NULL DEFAULT '[]',
			answer TEXT NOT NULL DEFAULT '',
			answer_images_json TEXT NOT NULL DEFAULT '[]',
			pinned INTEGER NOT NULL DEFAULT 0,
			hidden INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			answered_at TEXT,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS qa_issues (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			course_id INTEGER NOT NULL DEFAULT 0,
			course TEXT NOT NULL DEFAULT '',
			assignment_id TEXT NOT NULL,
			student_no TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'open',
			pinned INTEGER NOT NULL DEFAULT 0,
			hidden INTEGER NOT NULL DEFAULT 0,
			message_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS qa_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			issue_id INTEGER NOT NULL REFERENCES qa_issues(id),
			sender TEXT NOT NULL,
			content TEXT NOT NULL,
			images_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := s.ensureAttemptsColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureHomeworkSubmissionSchema(ctx); err != nil {
		return err
	}
	if err := s.ensureCourseIntID(ctx); err != nil {
		return err
	}
	if err := s.ensureCourseNamingColumns(ctx); err != nil {
		return err
	}
	if err := s.backfillCourseSlugs(ctx); err != nil {
		return err
	}
	if err := s.backfillCourseNames(ctx); err != nil {
		return err
	}
	if err := s.ensureAdminSummariesCourseID(ctx); err != nil {
		return err
	}
	if err := s.ensureHomeworkCourseID(ctx); err != nil {
		return err
	}
	if err := s.ensureHomeworkGradeColumns(ctx); err != nil {
		return err
	}

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_attempts_quiz_status ON attempts(quiz_id, status);`,
		`CREATE INDEX IF NOT EXISTS idx_attempts_lookup ON attempts(quiz_id, student_no, status);`,
		`CREATE INDEX IF NOT EXISTS idx_answers_attempt ON answers(attempt_id);`,
		`CREATE INDEX IF NOT EXISTS idx_attempts_course ON attempts(course_id);`,
		`CREATE INDEX IF NOT EXISTS idx_homework_submissions_lookup ON homework_submissions(course_id, assignment_id, student_no);`,
		`CREATE INDEX IF NOT EXISTS idx_homework_submissions_legacy ON homework_submissions(course, assignment_id, student_no);`,
		`CREATE INDEX IF NOT EXISTS idx_homework_submissions_assignment ON homework_submissions(course_id, assignment_id, created_at DESC);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_homework_course_scope ON homework_submissions(course_id, assignment_id, student_no) WHERE course_id > 0;`,
		`CREATE INDEX IF NOT EXISTS idx_courses_teacher ON courses(teacher_id);`,
		`CREATE INDEX IF NOT EXISTS idx_courses_invite ON courses(invite_code);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_courses_teacher_slug ON courses(teacher_id, slug) WHERE slug != '';`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_admin_summaries_course_quiz ON admin_summaries(course_id, quiz_id);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_quiz_share_token ON quiz_share(share_token) WHERE revoked_at IS NULL;`,
		`CREATE INDEX IF NOT EXISTS idx_quiz_share_lookup ON quiz_share(course_id, quiz_id) WHERE revoked_at IS NULL;`,
		`CREATE INDEX IF NOT EXISTS idx_homework_qa_lookup ON homework_qa(course_id, assignment_id, hidden, pinned, updated_at);`,
		`CREATE INDEX IF NOT EXISTS idx_homework_qa_course_assignment ON homework_qa(course_id, assignment_id);`,
		`CREATE INDEX IF NOT EXISTS idx_qa_issues_lookup ON qa_issues(course_id, assignment_id, hidden, pinned, updated_at);`,
		`CREATE INDEX IF NOT EXISTS idx_qa_issues_course ON qa_issues(course_id);`,
		`CREATE INDEX IF NOT EXISTS idx_qa_messages_issue ON qa_messages(issue_id, created_at);`,
	}
	for _, idx := range indexes {
		if _, err := s.db.ExecContext(ctx, idx); err != nil {
			return err
		}
	}

	// Fix legacy rows where course_id was stored as empty string instead of integer
	s.db.ExecContext(ctx, `UPDATE attempts SET course_id = 0 WHERE typeof(course_id) = 'text' OR course_id = ''`)

	if err := s.migrateInProgressUniqueIndex(ctx); err != nil {
		return err
	}

	return nil
}

// migrateInProgressUniqueIndex upgrades the one-active-attempt uniqueness constraint
// from (quiz_id, student_no) to (quiz_id, student_no, course_id), so that concurrent
// quizzes across different courses never collide even when they share the same quiz_id.
func (s *SQLiteStore) migrateInProgressUniqueIndex(ctx context.Context) error {
	// Check if the current index already includes course_id by inspecting its definition.
	var idxDef string
	err := s.db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_attempts_one_active'`,
	).Scan(&idxDef)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// If the index already covers course_id, nothing to do.
	if strings.Contains(strings.ToLower(idxDef), "course_id") {
		return nil
	}
	// Drop the old two-column index and replace it with the three-column version.
	if _, err := s.db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_attempts_one_active`); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_attempts_one_active
		 ON attempts(quiz_id, student_no, course_id) WHERE status = 'in_progress'`)
	return err
}

func (s *SQLiteStore) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func (s *SQLiteStore) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *SQLiteStore) CreateAttempt(ctx context.Context, a *domain.Attempt) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO attempts(id, session_token, quiz_id, course_id, name, student_no, class_name, attempt_no, status, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.SessionToken, a.QuizID, a.CourseID, a.Name, a.StudentNo, a.ClassName, a.AttemptNo, string(a.Status), a.CreatedAt.Format(time.RFC3339Nano), a.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

const attemptCols = `id, session_token, quiz_id, course_id, name, student_no, class_name, attempt_no, status, created_at, updated_at, submitted_at`

func (s *SQLiteStore) ListAttempts(ctx context.Context) ([]domain.Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+attemptCols+` FROM attempts ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Attempt, 0)
	for rows.Next() {
		a, err := scanAttemptRows(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *a)
	}
	return items, nil
}

func (s *SQLiteStore) ListAttemptsByCourse(ctx context.Context, courseID int) ([]domain.Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+attemptCols+` FROM attempts WHERE course_id = ? ORDER BY created_at DESC`, courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Attempt, 0)
	for rows.Next() {
		a, err := scanAttemptRows(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *a)
	}
	return items, nil
}

func (s *SQLiteStore) GetAttemptByID(ctx context.Context, attemptID string) (*domain.Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+attemptCols+` FROM attempts WHERE id = ?`, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanAttemptRows(rows)
}

func (s *SQLiteStore) GetAttemptByToken(ctx context.Context, token string) (*domain.Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+attemptCols+` FROM attempts WHERE session_token = ?`, token)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanAttemptRows(rows)
}

func (s *SQLiteStore) UpdateAttemptStatus(ctx context.Context, attemptID string, status domain.AttemptStatus) error {
	now := time.Now().Format(time.RFC3339Nano)
	if status == domain.StatusSubmitted {
		_, err := s.db.ExecContext(ctx, `UPDATE attempts SET status = ?, updated_at = ?, submitted_at = ? WHERE id = ?`, string(status), now, now, attemptID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE attempts SET status = ?, updated_at = ? WHERE id = ?`, string(status), now, attemptID)
	return err
}

func (s *SQLiteStore) SubmitAttempt(ctx context.Context, attemptID string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var quizID string
	var studentNo string
	var status string
	var existingAttemptNo int
	var courseID int
	err = tx.QueryRowContext(ctx, `SELECT quiz_id, student_no, status, attempt_no, course_id FROM attempts WHERE id = ?`, attemptID).Scan(&quizID, &studentNo, &status, &existingAttemptNo, &courseID)
	if err != nil {
		return 0, err
	}
	if status == string(domain.StatusSubmitted) {
		if err = tx.Commit(); err != nil {
			return 0, err
		}
		return existingAttemptNo, nil
	}

	// Attempt numbering is per (course_id, quiz_id, student_no) so that
	// two courses sharing the same YAML quiz_id don't share a counter.
	var nextAttemptNo int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt_no), 0) + 1 FROM attempts WHERE quiz_id = ? AND student_no = ? AND course_id = ? AND status = ?`, quizID, studentNo, courseID, string(domain.StatusSubmitted)).Scan(&nextAttemptNo); err != nil {
		return 0, err
	}
	now := time.Now().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `UPDATE attempts SET status = ?, attempt_no = ?, updated_at = ?, submitted_at = ? WHERE id = ?`, string(domain.StatusSubmitted), nextAttemptNo, now, now, attemptID)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return nextAttemptNo, nil
}

func (s *SQLiteStore) SaveAnswer(ctx context.Context, answer domain.Answer) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO answers(attempt_id, question_id, value, updated_at) VALUES(?, ?, ?, ?) ON CONFLICT(attempt_id, question_id) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		answer.AttemptID, answer.QuestionID, answer.Value, answer.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) GetAnswers(ctx context.Context, attemptID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT question_id, value FROM answers WHERE attempt_id = ?`, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var qid, v string
		if err := rows.Scan(&qid, &v); err != nil {
			return nil, err
		}
		result[qid] = v
	}
	return result, nil
}

func (s *SQLiteStore) UpsertSummary(ctx context.Context, attemptID string, summaryJSON string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO summaries(attempt_id, summary_json) VALUES(?, ?) ON CONFLICT(attempt_id) DO UPDATE SET summary_json=excluded.summary_json`, attemptID, summaryJSON)
	return err
}

func (s *SQLiteStore) GetSummary(ctx context.Context, attemptID string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT summary_json FROM summaries WHERE attempt_id = ?`, attemptID).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func (s *SQLiteStore) GetLiveStats(ctx context.Context) (int, int, error) {
	var started, submitted int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts`).Scan(&started); err != nil {
		return 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE status = ?`, string(domain.StatusSubmitted)).Scan(&submitted); err != nil {
		return 0, 0, err
	}
	return started, submitted, nil
}

func (s *SQLiteStore) GetInProgressAttempt(ctx context.Context, quizID, studentNo string, courseID int) (*domain.Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+attemptCols+` FROM attempts WHERE quiz_id = ? AND student_no = ? AND course_id = ? AND status = 'in_progress' ORDER BY created_at DESC LIMIT 1`, quizID, studentNo, courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanAttemptRows(rows)
}

func (s *SQLiteStore) UpdateAttemptSession(ctx context.Context, attemptID, token, name, className string, courseID int) error {
	now := time.Now().Format(time.RFC3339Nano)
	if courseID > 0 {
		_, err := s.db.ExecContext(ctx, `UPDATE attempts SET session_token = ?, name = ?, class_name = ?, course_id = ?, updated_at = ? WHERE id = ?`, token, name, className, courseID, now, attemptID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE attempts SET session_token = ?, name = ?, class_name = ?, updated_at = ? WHERE id = ?`, token, name, className, now, attemptID)
	return err
}

// UpsertAdminSummary writes the AI summary for a given (course_id, quiz_id).
// Legacy callers can pass courseID=0 which maps to the global quiz namespace;
// new code should always pass the actual course id.
func (s *SQLiteStore) UpsertAdminSummary(ctx context.Context, courseID int, quizID string, summaryJSON string) error {
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO admin_summaries(course_id, quiz_id, summary_json, created_at, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(course_id, quiz_id) DO UPDATE SET summary_json=excluded.summary_json, updated_at=excluded.updated_at`, courseID, quizID, summaryJSON, now, now)
	return err
}

func (s *SQLiteStore) GetAdminSummary(ctx context.Context, courseID int, quizID string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT summary_json FROM admin_summaries WHERE course_id = ? AND quiz_id = ?`, courseID, quizID).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func (s *SQLiteStore) DeleteAdminSummary(ctx context.Context, courseID int, quizID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM admin_summaries WHERE course_id = ? AND quiz_id = ?`, courseID, quizID)
	return err
}

func (s *SQLiteStore) ClearAttempts(ctx context.Context, quizID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM answers WHERE attempt_id IN (SELECT id FROM attempts WHERE quiz_id = ?)`, quizID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM summaries WHERE attempt_id IN (SELECT id FROM attempts WHERE quiz_id = ?)`, quizID); err != nil {
		_ = tx.Rollback()
		return err
	}
	// Legacy ClearAttempts wipes the legacy global namespace; course-scoped
	// variants should be preferred by new callers (see ClearAttemptsByCourse).
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_summaries WHERE quiz_id = ? AND course_id = 0`, quizID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM attempts WHERE quiz_id = ?`, quizID); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ClearAttemptsByCourse(ctx context.Context, courseID int, quizID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	scope := `attempt_id IN (SELECT id FROM attempts WHERE quiz_id = ? AND course_id = ?)`
	if _, err := tx.ExecContext(ctx, `DELETE FROM answers WHERE `+scope, quizID, courseID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM summaries WHERE `+scope, quizID, courseID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_summaries WHERE quiz_id = ? AND course_id = ?`, quizID, courseID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM attempts WHERE quiz_id = ? AND course_id = ?`, quizID, courseID); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) FixLegacyAttemptsCourse(ctx context.Context, quizID string, courseID int) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE attempts SET course_id = ? WHERE quiz_id = ? AND course_id = 0`, courseID, quizID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) FixAllLegacyAttemptsCourse(ctx context.Context, quizIDs []string, courseID int) (int, error) {
	if len(quizIDs) == 0 {
		return 0, nil
	}
	placeholders := strings.Repeat("?,", len(quizIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, 1+len(quizIDs))
	args = append(args, courseID)
	for _, q := range quizIDs {
		args = append(args, q)
	}
	query := fmt.Sprintf(`UPDATE attempts SET course_id = ? WHERE quiz_id IN (%s) AND course_id = 0`, placeholders)
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// ── Teachers ──

func (s *SQLiteStore) CreateTeacher(ctx context.Context, t *domain.Teacher) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO teachers(id, name, password_hash, role, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.PasswordHash, string(t.Role),
		t.CreatedAt.Format(time.RFC3339Nano), t.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) GetTeacher(ctx context.Context, id string) (*domain.Teacher, error) {
	var t domain.Teacher
	var role, created, updated string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, password_hash, role, created_at, updated_at FROM teachers WHERE id = ?`, id).
		Scan(&t.ID, &t.Name, &t.PasswordHash, &role, &created, &updated)
	if err != nil {
		return nil, err
	}
	t.Role = domain.UserRole(role)
	t.CreatedAt = parseTime(time.RFC3339Nano, created)
	t.UpdatedAt = parseTime(time.RFC3339Nano, updated)
	return &t, nil
}

func (s *SQLiteStore) ListTeachers(ctx context.Context) ([]domain.Teacher, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, password_hash, role, created_at, updated_at FROM teachers ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.Teacher
	for rows.Next() {
		var t domain.Teacher
		var role, created, updated string
		if err := rows.Scan(&t.ID, &t.Name, &t.PasswordHash, &role, &created, &updated); err != nil {
			return nil, err
		}
		t.Role = domain.UserRole(role)
		t.CreatedAt = parseTime(time.RFC3339Nano, created)
		t.UpdatedAt = parseTime(time.RFC3339Nano, updated)
		items = append(items, t)
	}
	return items, nil
}

func (s *SQLiteStore) UpdateTeacherPassword(ctx context.Context, id, passwordHash string) error {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE teachers SET password_hash = ?, updated_at = ? WHERE id = ?`, passwordHash, now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) UpdateTeacherRole(ctx context.Context, id string, role domain.UserRole) error {
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`UPDATE teachers SET role = ?, updated_at = ? WHERE id = ?`, string(role), now, id)
	return err
}

func (s *SQLiteStore) DeleteTeacher(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM teachers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ── Courses ──

func (s *SQLiteStore) CreateCourse(ctx context.Context, c *domain.Course) error {
	if c.InternalName == "" {
		c.InternalName = c.Slug
	}
	if c.Slug == "" {
		c.Slug = c.InternalName
	}
	if c.DisplayName == "" {
		c.DisplayName = c.InternalName
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO courses(teacher_id, name, display_name, internal_name, slug, invite_code, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		c.TeacherID, c.Name, c.DisplayName, c.InternalName, c.Slug, c.InviteCode,
		c.CreatedAt.Format(time.RFC3339Nano), c.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	c.ID = int(id)
	return nil
}

func (s *SQLiteStore) GetCourse(ctx context.Context, id int) (*domain.Course, error) {
	return s.scanCourse(s.db.QueryRowContext(ctx,
		`SELECT id, teacher_id, name, COALESCE(display_name,''), COALESCE(internal_name,''), COALESCE(slug,''), invite_code, created_at, updated_at FROM courses WHERE id = ?`, id))
}

func (s *SQLiteStore) GetCourseByInviteCode(ctx context.Context, code string) (*domain.Course, error) {
	return s.scanCourse(s.db.QueryRowContext(ctx,
		`SELECT id, teacher_id, name, COALESCE(display_name,''), COALESCE(internal_name,''), COALESCE(slug,''), invite_code, created_at, updated_at FROM courses WHERE invite_code = ?`, code))
}

func (s *SQLiteStore) ListCoursesByTeacher(ctx context.Context, teacherID string) ([]domain.Course, error) {
	return s.listCourses(ctx,
		`SELECT id, teacher_id, name, COALESCE(display_name,''), COALESCE(internal_name,''), COALESCE(slug,''), invite_code, created_at, updated_at FROM courses WHERE teacher_id = ? ORDER BY created_at`, teacherID)
}

func (s *SQLiteStore) ListAllCourses(ctx context.Context) ([]domain.Course, error) {
	return s.listCourses(ctx,
		`SELECT id, teacher_id, name, COALESCE(display_name,''), COALESCE(internal_name,''), COALESCE(slug,''), invite_code, created_at, updated_at FROM courses ORDER BY teacher_id, created_at`)
}

func (s *SQLiteStore) UpdateCourse(ctx context.Context, c *domain.Course) error {
	if c.InternalName == "" {
		c.InternalName = c.Slug
	}
	if c.Slug == "" {
		c.Slug = c.InternalName
	}
	if c.DisplayName == "" {
		c.DisplayName = c.InternalName
	}
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`UPDATE courses SET name = ?, display_name = ?, internal_name = ?, slug = ?, updated_at = ? WHERE id = ?`,
		c.Name, c.DisplayName, c.InternalName, c.Slug, now, c.ID)
	return err
}

func (s *SQLiteStore) DeleteCourse(ctx context.Context, id int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	attemptScope := `attempt_id IN (SELECT id FROM attempts WHERE course_id = ?)`
	if _, err := tx.ExecContext(ctx, `DELETE FROM answers WHERE `+attemptScope, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM summaries WHERE `+attemptScope, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM attempts WHERE course_id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_summaries WHERE course_id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM homework_submissions WHERE course_id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM homework_qa WHERE course_id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM quiz_share WHERE course_id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM course_state WHERE course_id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM courses WHERE id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) scanCourse(row *sql.Row) (*domain.Course, error) {
	var c domain.Course
	var created, updated string
	if err := row.Scan(&c.ID, &c.TeacherID, &c.Name, &c.DisplayName, &c.InternalName, &c.Slug, &c.InviteCode, &created, &updated); err != nil {
		return nil, err
	}
	normalizeCourseRecord(&c)
	c.CreatedAt = parseTime(time.RFC3339Nano, created)
	c.UpdatedAt = parseTime(time.RFC3339Nano, updated)
	return &c, nil
}

func (s *SQLiteStore) listCourses(ctx context.Context, query string, args ...any) ([]domain.Course, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.Course
	for rows.Next() {
		var c domain.Course
		var created, updated string
		if err := rows.Scan(&c.ID, &c.TeacherID, &c.Name, &c.DisplayName, &c.InternalName, &c.Slug, &c.InviteCode, &created, &updated); err != nil {
			return nil, err
		}
		normalizeCourseRecord(&c)
		c.CreatedAt = parseTime(time.RFC3339Nano, created)
		c.UpdatedAt = parseTime(time.RFC3339Nano, updated)
		items = append(items, c)
	}
	return items, nil
}

// ── Course state ──

func (s *SQLiteStore) GetCourseState(ctx context.Context, courseID int) (*domain.CourseState, error) {
	var cs domain.CourseState
	var entryOpen int
	var yamlN, pathN sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT course_id, entry_open, quiz_yaml, quiz_source_path FROM course_state WHERE course_id = ?`, courseID).
		Scan(&cs.CourseID, &entryOpen, &yamlN, &pathN)
	if err != nil {
		return nil, err
	}
	cs.EntryOpen = entryOpen != 0
	if yamlN.Valid {
		cs.QuizYAML = yamlN.String
	}
	if pathN.Valid {
		cs.QuizSourcePath = pathN.String
	}
	return &cs, nil
}

func (s *SQLiteStore) SetCourseState(ctx context.Context, cs *domain.CourseState) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO course_state(course_id, entry_open, quiz_yaml, quiz_source_path) VALUES(?, ?, ?, ?)
		 ON CONFLICT(course_id) DO UPDATE SET entry_open=excluded.entry_open, quiz_yaml=excluded.quiz_yaml, quiz_source_path=excluded.quiz_source_path`,
		cs.CourseID, boolToInt(cs.EntryOpen), nullStr(cs.QuizYAML), nullStr(cs.QuizSourcePath))
	return err
}

func (s *SQLiteStore) GetLiveStatsByCourse(ctx context.Context, courseID int) (int, int, error) {
	var started, submitted int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE course_id = ?`, courseID).Scan(&started); err != nil {
		return 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE course_id = ? AND status = ?`, courseID, string(domain.StatusSubmitted)).Scan(&submitted); err != nil {
		return 0, 0, err
	}
	return started, submitted, nil
}

func (s *SQLiteStore) GetLiveStatsByCourseQuiz(ctx context.Context, courseID int, quizID string) (int, int, error) {
	var started, submitted int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT name) FROM attempts WHERE course_id = ? AND quiz_id = ?`, courseID, quizID).Scan(&started); err != nil {
		return 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT name) FROM attempts WHERE course_id = ? AND quiz_id = ? AND status = ?`, courseID, quizID, string(domain.StatusSubmitted)).Scan(&submitted); err != nil {
		return 0, 0, err
	}
	return started, submitted, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const homeworkSubmissionSelectCols = `id, session_token, course, course_id, assignment_id, name, student_no, class_name, secret_key, report_original_name, report_uploaded_at, code_original_name, code_uploaded_at, extra_original_name, extra_uploaded_at, score, feedback, graded_at, grade_updated_at, created_at, updated_at`

func (s *SQLiteStore) CreateHomeworkSubmission(ctx context.Context, submission *domain.HomeworkSubmission) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO homework_submissions(
		id, session_token, course, course_id, assignment_id, name, student_no, class_name, secret_key,
		report_original_name, report_uploaded_at, code_original_name, code_uploaded_at,
		extra_original_name, extra_uploaded_at, score, feedback, graded_at, grade_updated_at,
		created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		submission.ID,
		submission.SessionToken,
		submission.Course,
		submission.CourseID,
		submission.AssignmentID,
		submission.Name,
		submission.StudentNo,
		submission.ClassName,
		submission.SecretKey,
		submission.ReportOriginalName,
		formatTimePtr(submission.ReportUploadedAt),
		submission.CodeOriginalName,
		formatTimePtr(submission.CodeUploadedAt),
		submission.ExtraOriginalName,
		formatTimePtr(submission.ExtraUploadedAt),
		floatPtrValue(submission.Score),
		submission.Feedback,
		formatTimePtr(submission.GradedAt),
		formatTimePtr(submission.GradeUpdatedAt),
		submission.CreatedAt.Format(time.RFC3339Nano),
		submission.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) GetHomeworkSubmissionByID(ctx context.Context, submissionID string) (*domain.HomeworkSubmission, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+homeworkSubmissionSelectCols+` FROM homework_submissions WHERE id = ?`, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanHomeworkSubmissionRows(rows)
}

func (s *SQLiteStore) GetHomeworkSubmissionByToken(ctx context.Context, token string) (*domain.HomeworkSubmission, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+homeworkSubmissionSelectCols+` FROM homework_submissions WHERE session_token = ?`, token)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanHomeworkSubmissionRows(rows)
}

func (s *SQLiteStore) GetHomeworkSubmissionByScope(ctx context.Context, courseID int, course, assignmentID, studentNo string) (*domain.HomeworkSubmission, error) {
	var query string
	var args []any
	if courseID > 0 {
		query = `SELECT ` + homeworkSubmissionSelectCols + ` FROM homework_submissions WHERE course_id = ? AND assignment_id = ? AND student_no = ? LIMIT 1`
		args = []any{courseID, assignmentID, studentNo}
	} else {
		query = `SELECT ` + homeworkSubmissionSelectCols + ` FROM homework_submissions WHERE course = ? AND assignment_id = ? AND student_no = ? LIMIT 1`
		args = []any{course, assignmentID, studentNo}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanHomeworkSubmissionRows(rows)
}

func (s *SQLiteStore) UpdateHomeworkSubmissionSession(ctx context.Context, submissionID, token, name, className, secretKey string) error {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE homework_submissions SET session_token = ?, name = ?, class_name = ?, secret_key = ?, updated_at = ? WHERE id = ?`, token, name, className, secretKey, now, submissionID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return sql.ErrNoRows
}

func (s *SQLiteStore) ListHomeworkSubmissions(ctx context.Context, courseID int, course, assignmentID string) ([]domain.HomeworkSubmission, error) {
	query := `SELECT ` + homeworkSubmissionSelectCols + ` FROM homework_submissions`
	args := make([]any, 0, 2)
	filters := make([]string, 0, 2)
	if courseID > 0 {
		filters = append(filters, "course_id = ?")
		args = append(args, courseID)
	} else if strings.TrimSpace(course) != "" {
		filters = append(filters, "course = ?")
		args = append(args, course)
	}
	if strings.TrimSpace(assignmentID) != "" {
		filters = append(filters, "assignment_id = ?")
		args = append(args, assignmentID)
	}
	if len(filters) > 0 {
		query += " WHERE " + strings.Join(filters, " AND ")
	}
	query += ` ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.HomeworkSubmission, 0)
	for rows.Next() {
		item, err := scanHomeworkSubmissionRows(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, nil
}

func (s *SQLiteStore) SaveHomeworkFileMetadata(ctx context.Context, submissionID string, slot domain.HomeworkFileSlot, originalName string) error {
	nameCol, timeCol, err := homeworkFileColumns(slot)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339Nano)
	query := fmt.Sprintf(`UPDATE homework_submissions SET %s = ?, %s = ?, updated_at = ? WHERE id = ?`, nameCol, timeCol)
	res, err := s.db.ExecContext(ctx, query, originalName, now, now, submissionID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return sql.ErrNoRows
}

func (s *SQLiteStore) DeleteHomeworkFileMetadata(ctx context.Context, submissionID string, slot domain.HomeworkFileSlot) error {
	nameCol, timeCol, err := homeworkFileColumns(slot)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339Nano)
	query := fmt.Sprintf(`UPDATE homework_submissions SET %s = '', %s = NULL, updated_at = ? WHERE id = ?`, nameCol, timeCol)
	res, err := s.db.ExecContext(ctx, query, now, submissionID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return sql.ErrNoRows
}

func (s *SQLiteStore) SaveHomeworkGrade(ctx context.Context, submissionID string, score *float64, feedback string) error {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE homework_submissions SET score = ?, feedback = ?, graded_at = COALESCE(graded_at, ?), grade_updated_at = ?, updated_at = ? WHERE id = ?`, floatPtrValue(score), feedback, now, now, now, submissionID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return sql.ErrNoRows
}

func (s *SQLiteStore) DeleteHomeworkSubmission(ctx context.Context, submissionID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM homework_submissions WHERE id = ?`, submissionID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return sql.ErrNoRows
}

func (s *SQLiteStore) CreateHomeworkQuestion(ctx context.Context, qa *domain.HomeworkQA) error {
	questionImages, err := encodeStringSlice(qa.QuestionImages)
	if err != nil {
		return err
	}
	answerImages, err := encodeStringSlice(qa.AnswerImages)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO homework_qa(
		id, course, course_id, assignment_id, question, question_images_json,
		answer, answer_images_json, pinned, hidden, created_at, answered_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		qa.ID, qa.Course, qa.CourseID, qa.AssignmentID, qa.Question, questionImages,
		qa.Answer, answerImages, boolToInt(qa.Pinned), boolToInt(qa.Hidden),
		qa.CreatedAt.Format(time.RFC3339Nano), formatTimePtr(qa.AnsweredAt), qa.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) ListHomeworkQA(ctx context.Context, courseID int, course, assignmentID string, includeUnanswered, includeHidden bool) ([]domain.HomeworkQA, error) {
	query := `SELECT id, course, course_id, assignment_id, question, question_images_json, answer, answer_images_json, pinned, hidden, created_at, answered_at, updated_at FROM homework_qa`
	args := make([]any, 0, 4)
	filters := make([]string, 0, 4)
	if courseID > 0 {
		filters = append(filters, "course_id = ?")
		args = append(args, courseID)
	} else if strings.TrimSpace(course) != "" {
		filters = append(filters, "course = ?")
		args = append(args, course)
	}
	if strings.TrimSpace(assignmentID) != "" {
		filters = append(filters, "assignment_id = ?")
		args = append(args, assignmentID)
	}
	if !includeUnanswered {
		filters = append(filters, "answer != ''")
	}
	if !includeHidden {
		filters = append(filters, "hidden = 0")
	}
	if len(filters) > 0 {
		query += " WHERE " + strings.Join(filters, " AND ")
	}
	query += ` ORDER BY pinned DESC, CASE WHEN answer = '' THEN 0 ELSE 1 END ASC, updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.HomeworkQA, 0)
	for rows.Next() {
		item, err := scanHomeworkQARows(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) GetHomeworkQAByID(ctx context.Context, id string) (*domain.HomeworkQA, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, course, course_id, assignment_id, question, question_images_json, answer, answer_images_json, pinned, hidden, created_at, answered_at, updated_at FROM homework_qa WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanHomeworkQARows(rows)
}

func (s *SQLiteStore) AnswerHomeworkQuestion(ctx context.Context, id, answer string, answerImages []string) error {
	imagesJSON, err := encodeStringSlice(answerImages)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE homework_qa SET answer = ?, answer_images_json = ?, answered_at = ?, updated_at = ? WHERE id = ?`, answer, imagesJSON, now, now, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return sql.ErrNoRows
}

func (s *SQLiteStore) SetHomeworkQuestionPinned(ctx context.Context, id string, pinned bool) error {
	return s.updateHomeworkQABool(ctx, id, "pinned", pinned)
}

func (s *SQLiteStore) SetHomeworkQuestionHidden(ctx context.Context, id string, hidden bool) error {
	return s.updateHomeworkQABool(ctx, id, "hidden", hidden)
}

func (s *SQLiteStore) updateHomeworkQABool(ctx context.Context, id, column string, value bool) error {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`UPDATE homework_qa SET %s = ?, updated_at = ? WHERE id = ?`, column), boolToInt(value), now, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return sql.ErrNoRows
}

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// parseTime attempts to parse a timestamp using the given layout. If that
// fails, it falls back to the SQLite datetime() format ("2006-01-02 15:04:05")
// which some older rows still use. If both fail, it logs a warning and
// returns the zero time value.
func parseTime(layout, value string) time.Time {
	t, err := time.Parse(layout, value)
	if err == nil {
		return t
	}
	// Fallback: SQLite datetime() produces "YYYY-MM-DD HH:MM:SS" (space
	// separator, no timezone). Older rows may store this format.
	if fallback, err2 := time.Parse(sqliteDatetimeLayout, value); err2 == nil {
		return fallback
	}
	log.Printf("store: time.Parse(%s, %q): %v; fallback also failed", layout, value, err)
	return time.Time{}
}

const sqliteDatetimeLayout = "2006-01-02 15:04:05"

func WrapErr(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAttemptRows(sc rowScanner) (*domain.Attempt, error) {
	var a domain.Attempt
	var status string
	var created, updated string
	var submitted sql.NullString
	var courseIDRaw sql.NullString
	if err := sc.Scan(&a.ID, &a.SessionToken, &a.QuizID, &courseIDRaw, &a.Name, &a.StudentNo, &a.ClassName, &a.AttemptNo, &status, &created, &updated, &submitted); err != nil {
		return nil, err
	}
	if courseIDRaw.Valid && courseIDRaw.String != "" {
		fmt.Sscanf(courseIDRaw.String, "%d", &a.CourseID)
	}
	a.Status = domain.AttemptStatus(status)
	a.CreatedAt = parseTime(time.RFC3339Nano, created)
	a.UpdatedAt = parseTime(time.RFC3339Nano, updated)
	if submitted.Valid {
		t := parseTime(time.RFC3339Nano, submitted.String)
		a.SubmittedAt = &t
	}
	return &a, nil
}

func scanHomeworkSubmissionRows(sc rowScanner) (*domain.HomeworkSubmission, error) {
	var item domain.HomeworkSubmission
	var reportUploaded sql.NullString
	var codeUploaded sql.NullString
	var extraUploaded sql.NullString
	var score sql.NullFloat64
	var gradedAt sql.NullString
	var gradeUpdatedAt sql.NullString
	var created string
	var updated string
	if err := sc.Scan(
		&item.ID,
		&item.SessionToken,
		&item.Course,
		&item.CourseID,
		&item.AssignmentID,
		&item.Name,
		&item.StudentNo,
		&item.ClassName,
		&item.SecretKey,
		&item.ReportOriginalName,
		&reportUploaded,
		&item.CodeOriginalName,
		&codeUploaded,
		&item.ExtraOriginalName,
		&extraUploaded,
		&score,
		&item.Feedback,
		&gradedAt,
		&gradeUpdatedAt,
		&created,
		&updated,
	); err != nil {
		return nil, err
	}
	item.CreatedAt = parseTime(time.RFC3339Nano, created)
	item.UpdatedAt = parseTime(time.RFC3339Nano, updated)
	item.ReportUploadedAt = parseTimePtr(reportUploaded)
	item.CodeUploadedAt = parseTimePtr(codeUploaded)
	item.ExtraUploadedAt = parseTimePtr(extraUploaded)
	if score.Valid {
		item.Score = &score.Float64
	}
	item.GradedAt = parseTimePtr(gradedAt)
	item.GradeUpdatedAt = parseTimePtr(gradeUpdatedAt)
	return &item, nil
}

func scanHomeworkQARows(sc rowScanner) (*domain.HomeworkQA, error) {
	var item domain.HomeworkQA
	var questionImages string
	var answerImages string
	var pinned int
	var hidden int
	var created string
	var answered sql.NullString
	var updated string
	if err := sc.Scan(
		&item.ID,
		&item.Course,
		&item.CourseID,
		&item.AssignmentID,
		&item.Question,
		&questionImages,
		&item.Answer,
		&answerImages,
		&pinned,
		&hidden,
		&created,
		&answered,
		&updated,
	); err != nil {
		return nil, err
	}
	item.QuestionImages = decodeStringSlice(questionImages)
	item.AnswerImages = decodeStringSlice(answerImages)
	item.Pinned = pinned != 0
	item.Hidden = hidden != 0
	item.CreatedAt = parseTime(time.RFC3339Nano, created)
	item.AnsweredAt = parseTimePtr(answered)
	item.UpdatedAt = parseTime(time.RFC3339Nano, updated)
	return &item, nil
}

func encodeStringSlice(items []string) (string, error) {
	if items == nil {
		items = []string{}
	}
	data, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeStringSlice(raw string) []string {
	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return []string{}
	}
	if items == nil {
		return []string{}
	}
	return items
}

func (s *SQLiteStore) ensureHomeworkSubmissionSchema(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(homework_submissions)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		columns[name] = true
	}
	if len(columns) > 0 {
		required := []string{"id", "session_token", "course", "assignment_id", "name", "student_no", "class_name", "secret_key", "report_original_name", "report_uploaded_at", "code_original_name", "code_uploaded_at", "extra_original_name", "extra_uploaded_at", "created_at", "updated_at"}
		legacy := columns["quiz_id"] || columns["task_id"] || columns["status"] || columns["finalized_at"]
		missing := false
		for _, name := range required {
			if !columns[name] {
				missing = true
				break
			}
		}
		if !legacy && !missing {
			return nil
		}
		if _, err := s.db.ExecContext(ctx, `DROP TABLE IF EXISTS homework_submissions`); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS homework_submissions (
		id TEXT PRIMARY KEY,
		session_token TEXT UNIQUE NOT NULL,
		course TEXT NOT NULL,
		assignment_id TEXT NOT NULL,
		name TEXT NOT NULL,
		student_no TEXT NOT NULL,
		class_name TEXT NOT NULL,
		secret_key TEXT NOT NULL DEFAULT '',
		report_original_name TEXT NOT NULL DEFAULT '',
		report_uploaded_at TEXT,
		code_original_name TEXT NOT NULL DEFAULT '',
		code_uploaded_at TEXT,
		extra_original_name TEXT NOT NULL DEFAULT '',
		extra_uploaded_at TEXT,
		score REAL,
		feedback TEXT NOT NULL DEFAULT '',
		graded_at TEXT,
		grade_updated_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`)
	return err
}

func homeworkFileColumns(slot domain.HomeworkFileSlot) (string, string, error) {
	switch slot {
	case domain.HomeworkSlotReport:
		return "report_original_name", "report_uploaded_at", nil
	case domain.HomeworkSlotCode:
		return "code_original_name", "code_uploaded_at", nil
	case domain.HomeworkSlotExtra:
		return "extra_original_name", "extra_uploaded_at", nil
	default:
		return "", "", fmt.Errorf("invalid homework slot")
	}
}

func formatTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339Nano)
}

func floatPtrValue(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func parseTimePtr(v sql.NullString) *time.Time {
	if !v.Valid || strings.TrimSpace(v.String) == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, v.String)
	if err != nil {
		return nil
	}
	return &t
}

func (s *SQLiteStore) ensureAttemptsColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(attempts)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		cols[name] = true
	}
	if !cols["quiz_id"] {
		if _, err = s.db.ExecContext(ctx, `ALTER TABLE attempts ADD COLUMN quiz_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !cols["attempt_no"] {
		if _, err = s.db.ExecContext(ctx, `ALTER TABLE attempts ADD COLUMN attempt_no INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !cols["course_id"] {
		if _, err = s.db.ExecContext(ctx, `ALTER TABLE attempts ADD COLUMN course_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return nil
}

// ensureCourseIntID migrates the courses table from TEXT PRIMARY KEY (legacy)
// to INTEGER PRIMARY KEY AUTOINCREMENT, reassigning orphaned courses whose
// teacher_id no longer exists in the teachers table.
func (s *SQLiteStore) ensureCourseIntID(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(courses)`)
	if err != nil {
		return err
	}
	needsMigration := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "id" && strings.EqualFold(typ, "TEXT") {
			needsMigration = true
		}
	}
	rows.Close()
	if !needsMigration {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ensureCourseIntID: begin: %w", err)
	}
	defer tx.Rollback()

	var adminID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM teachers WHERE role='admin' LIMIT 1`).Scan(&adminID)
	if err != nil {
		err = tx.QueryRowContext(ctx, `SELECT id FROM teachers LIMIT 1`).Scan(&adminID)
		if err != nil {
			return nil // no teachers, nothing to migrate
		}
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE courses SET teacher_id = ? WHERE teacher_id NOT IN (SELECT id FROM teachers)`, adminID); err != nil {
		return fmt.Errorf("ensureCourseIntID: reassign orphans: %w", err)
	}

	if _, err = tx.ExecContext(ctx, `CREATE TABLE courses_new (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		teacher_id TEXT NOT NULL,
		name TEXT NOT NULL,
		display_name TEXT NOT NULL DEFAULT '',
		internal_name TEXT NOT NULL DEFAULT '',
		slug TEXT NOT NULL DEFAULT '',
		invite_code TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return fmt.Errorf("ensureCourseIntID: create new table: %w", err)
	}

	oldRows, err := tx.QueryContext(ctx,
		`SELECT id, teacher_id, name, COALESCE(slug,''), COALESCE(invite_code,''), created_at, COALESCE(updated_at, created_at) FROM courses`)
	if err != nil {
		return fmt.Errorf("ensureCourseIntID: read old: %w", err)
	}
	type idPair struct {
		old string
		new int64
	}
	var pairs []idPair
	for oldRows.Next() {
		var oldID sql.NullString
		var teacherID, name, slug, invCode, createdAt, updatedAt string
		if err := oldRows.Scan(&oldID, &teacherID, &name, &slug, &invCode, &createdAt, &updatedAt); err != nil {
			oldRows.Close()
			return fmt.Errorf("ensureCourseIntID: scan: %w", err)
		}
		if invCode == "" {
			invCode = migrationInviteCode()
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO courses_new(teacher_id, name, display_name, internal_name, slug, invite_code, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			teacherID, name, slug, slug, slug, invCode, createdAt, updatedAt)
		if err != nil {
			oldRows.Close()
			return fmt.Errorf("ensureCourseIntID: insert: %w", err)
		}
		newID, _ := res.LastInsertId()
		if oldID.Valid && oldID.String != "" {
			pairs = append(pairs, idPair{oldID.String, newID})
		}
	}
	oldRows.Close()

	for _, p := range pairs {
		tx.ExecContext(ctx, `UPDATE course_state SET course_id = ? WHERE CAST(course_id AS TEXT) = ?`, p.new, p.old)
		tx.ExecContext(ctx, `UPDATE attempts SET course_id = ? WHERE course_id = ?`, p.new, p.old)
		tx.ExecContext(ctx, `UPDATE course_students SET course_id = ? WHERE course_id = ?`, p.new, p.old)
	}

	if _, err = tx.ExecContext(ctx, `DROP TABLE courses`); err != nil {
		return fmt.Errorf("ensureCourseIntID: drop old: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `ALTER TABLE courses_new RENAME TO courses`); err != nil {
		return fmt.Errorf("ensureCourseIntID: rename: %w", err)
	}
	return tx.Commit()
}

// backfillCourseSlugs sets slug = name for any courses with empty slugs.
// This handles legacy courses created before the teacher-based slug model.
func (s *SQLiteStore) backfillCourseSlugs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE courses SET slug = name WHERE slug = '' OR slug IS NULL`)
	return err
}

func (s *SQLiteStore) ensureCourseNamingColumns(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE courses ADD COLUMN display_name TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE courses ADD COLUMN internal_name TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	return nil
}

func (s *SQLiteStore) backfillCourseNames(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE courses SET internal_name = slug WHERE internal_name = '' OR internal_name IS NULL`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE courses SET display_name = internal_name WHERE display_name = '' OR display_name IS NULL`)
	return err
}

func normalizeCourseRecord(c *domain.Course) {
	if c.InternalName == "" {
		c.InternalName = c.Slug
	}
	if c.Slug == "" {
		c.Slug = c.InternalName
	}
	if c.DisplayName == "" {
		c.DisplayName = c.InternalName
	}
}

// ensureAdminSummariesCourseID adds the course_id column to admin_summaries
// and, if the existing primary key is single-column (legacy main-branch
// schema), rebuilds the table with a composite (course_id, quiz_id) PK so
// two courses sharing a YAML quiz_id can hold independent summaries.
func (s *SQLiteStore) ensureAdminSummariesCourseID(ctx context.Context) error {
	cols := map[string]bool{}
	pks := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(admin_summaries)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
		if pk > 0 {
			pks[name] = true
		}
	}
	rows.Close()
	if !cols["course_id"] {
		if _, err := s.db.ExecContext(ctx,
			`ALTER TABLE admin_summaries ADD COLUMN course_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	// If the existing PK is single-column on quiz_id only, recreate the table
	// with a composite (course_id, quiz_id) primary key.
	if pks["quiz_id"] && !pks["course_id"] {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `ALTER TABLE admin_summaries RENAME TO admin_summaries_old`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE admin_summaries (
			quiz_id TEXT NOT NULL,
			course_id INTEGER NOT NULL DEFAULT 0,
			summary_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(course_id, quiz_id)
		)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO admin_summaries(quiz_id, course_id, summary_json, created_at, updated_at) SELECT quiz_id, COALESCE(course_id,0), summary_json, created_at, updated_at FROM admin_summaries_old`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE admin_summaries_old`); err != nil {
			return err
		}
		return tx.Commit()
	}
	return nil
}

// ensureHomeworkCourseID adds the course_id column and drops the legacy
// UNIQUE(course, assignment_id, student_no) table constraint, replacing it
// with a partial unique index on (course_id, assignment_id, student_no).
func (s *SQLiteStore) ensureHomeworkCourseID(ctx context.Context) error {
	cols := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(homework_submissions)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	rows.Close()
	if !cols["course_id"] {
		if _, err := s.db.ExecContext(ctx,
			`ALTER TABLE homework_submissions ADD COLUMN course_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	// Drop the legacy UNIQUE(course, assignment_id, student_no) constraint
	// by checking the table DDL. If it contains the old constraint, recreate.
	var tableDDL string
	err = s.db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='homework_submissions'`,
	).Scan(&tableDDL)
	if err != nil {
		return nil
	}
	if strings.Contains(tableDDL, "UNIQUE(course, assignment_id, student_no)") {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `ALTER TABLE homework_submissions RENAME TO homework_submissions_old`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE homework_submissions (
			id TEXT PRIMARY KEY,
			session_token TEXT UNIQUE NOT NULL,
			course TEXT NOT NULL,
			course_id INTEGER NOT NULL DEFAULT 0,
			assignment_id TEXT NOT NULL,
			name TEXT NOT NULL,
			student_no TEXT NOT NULL,
			class_name TEXT NOT NULL,
			secret_key TEXT NOT NULL DEFAULT '',
			report_original_name TEXT NOT NULL DEFAULT '',
			report_uploaded_at TEXT,
			code_original_name TEXT NOT NULL DEFAULT '',
			code_uploaded_at TEXT,
			extra_original_name TEXT NOT NULL DEFAULT '',
			extra_uploaded_at TEXT,
			score REAL,
			feedback TEXT NOT NULL DEFAULT '',
			graded_at TEXT,
			grade_updated_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO homework_submissions(id, session_token, course, course_id, assignment_id, name, student_no, class_name, secret_key, report_original_name, report_uploaded_at, code_original_name, code_uploaded_at, extra_original_name, extra_uploaded_at, score, feedback, graded_at, grade_updated_at, created_at, updated_at) SELECT id, session_token, course, course_id, assignment_id, name, student_no, class_name, secret_key, report_original_name, report_uploaded_at, code_original_name, code_uploaded_at, extra_original_name, extra_uploaded_at, NULL, '', NULL, NULL, created_at, updated_at FROM homework_submissions_old`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE homework_submissions_old`); err != nil {
			return err
		}
		return tx.Commit()
	}
	return nil
}

func (s *SQLiteStore) ensureHomeworkGradeColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(homework_submissions)`)
	if err != nil {
		return err
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	rows.Close()
	stmts := []struct {
		name string
		sql  string
	}{
		{"score", `ALTER TABLE homework_submissions ADD COLUMN score REAL`},
		{"feedback", `ALTER TABLE homework_submissions ADD COLUMN feedback TEXT NOT NULL DEFAULT ''`},
		{"graded_at", `ALTER TABLE homework_submissions ADD COLUMN graded_at TEXT`},
		{"grade_updated_at", `ALTER TABLE homework_submissions ADD COLUMN grade_updated_at TEXT`},
	}
	for _, stmt := range stmts {
		if cols[stmt.name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, stmt.sql); err != nil {
			return err
		}
	}
	return nil
}

// ── Quiz Share ──

func (s *SQLiteStore) CreateQuizShare(ctx context.Context, qs *domain.QuizShare) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO quiz_share(course_id, quiz_id, share_token, created_at) VALUES(?, ?, ?, ?)`,
		qs.CourseID, qs.QuizID, qs.ShareToken, qs.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	// Get the auto-incremented ID
	var id int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM quiz_share WHERE share_token = ?`, qs.ShareToken).Scan(&id)
	if err != nil {
		return err
	}
	qs.ID = int(id)
	return nil
}

func (s *SQLiteStore) GetQuizShareByID(ctx context.Context, id int) (*domain.QuizShare, error) {
	var qs domain.QuizShare
	var created string
	var revoked sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, course_id, quiz_id, share_token, created_at, revoked_at FROM quiz_share WHERE id = ?`,
		id).Scan(&qs.ID, &qs.CourseID, &qs.QuizID, &qs.ShareToken, &created, &revoked)
	if err != nil {
		return nil, err
	}
	qs.CreatedAt = parseTime(time.RFC3339Nano, created)
	if revoked.Valid {
		t := parseTime(time.RFC3339Nano, revoked.String)
		qs.RevokedAt = &t
	}
	return &qs, nil
}

func (s *SQLiteStore) GetQuizShareByToken(ctx context.Context, token string) (*domain.QuizShare, error) {
	var qs domain.QuizShare
	var created string
	var revoked sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, course_id, quiz_id, share_token, created_at, revoked_at FROM quiz_share WHERE share_token = ?`,
		token).Scan(&qs.ID, &qs.CourseID, &qs.QuizID, &qs.ShareToken, &created, &revoked)
	if err != nil {
		return nil, err
	}
	qs.CreatedAt = parseTime(time.RFC3339Nano, created)
	if revoked.Valid {
		t := parseTime(time.RFC3339Nano, revoked.String)
		qs.RevokedAt = &t
	}
	return &qs, nil
}

func (s *SQLiteStore) ListActiveQuizShares(ctx context.Context, courseID int, quizID string) ([]domain.QuizShare, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, course_id, quiz_id, share_token, created_at, revoked_at FROM quiz_share WHERE course_id = ? AND quiz_id = ? AND revoked_at IS NULL ORDER BY created_at DESC`,
		courseID, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.QuizShare
	for rows.Next() {
		var qs domain.QuizShare
		var created string
		var revoked sql.NullString
		if err := rows.Scan(&qs.ID, &qs.CourseID, &qs.QuizID, &qs.ShareToken, &created, &revoked); err != nil {
			return nil, err
		}
		qs.CreatedAt = parseTime(time.RFC3339Nano, created)
		if revoked.Valid {
			t := parseTime(time.RFC3339Nano, revoked.String)
			qs.RevokedAt = &t
		}
		items = append(items, qs)
	}
	return items, nil
}

func (s *SQLiteStore) RevokeQuizShare(ctx context.Context, id int) error {
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `UPDATE quiz_share SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now, id)
	return err
}

func migrationInviteCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, 6)
	_, _ = crand.Read(buf)
	for i := range buf {
		buf[i] = chars[int(buf[i])%len(chars)]
	}
	return string(buf)
}

// ── QAIssue / QAMessage ──

func (s *SQLiteStore) CreateQAIssue(ctx context.Context, issue *domain.QAIssue) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO qa_issues(
		course_id, course, assignment_id, student_no, title, status, pinned, hidden, message_count, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		issue.CourseID, issue.Course, issue.AssignmentID, issue.StudentNo,
		issue.Title, issue.Status, boolToInt(issue.Pinned), boolToInt(issue.Hidden),
		issue.MessageCount, issue.CreatedAt.Format(time.RFC3339Nano), issue.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func (s *SQLiteStore) GetQAIssueByID(ctx context.Context, id int) (*domain.QAIssue, error) {
	var issue domain.QAIssue
	var pinned, hidden int
	var created, updated string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, course_id, course, assignment_id, student_no, title, status, pinned, hidden, message_count, created_at, updated_at FROM qa_issues WHERE id = ?`, id).
		Scan(&issue.ID, &issue.CourseID, &issue.Course, &issue.AssignmentID, &issue.StudentNo,
			&issue.Title, &issue.Status, &pinned, &hidden, &issue.MessageCount, &created, &updated)
	if err != nil {
		return nil, err
	}
	issue.Pinned = pinned != 0
	issue.Hidden = hidden != 0
	issue.CreatedAt = parseTime(time.RFC3339Nano, created)
	issue.UpdatedAt = parseTime(time.RFC3339Nano, updated)
	return &issue, nil
}

func (s *SQLiteStore) ListQAIssues(ctx context.Context, courseID int, assignmentID string, includeHidden bool) ([]domain.QAIssue, error) {
	query := `SELECT id, course_id, course, assignment_id, student_no, title, status, pinned, hidden, message_count, created_at, updated_at FROM qa_issues`
	args := make([]any, 0, 3)
	filters := make([]string, 0, 3)
	if courseID > 0 {
		filters = append(filters, "course_id = ?")
		args = append(args, courseID)
	}
	if strings.TrimSpace(assignmentID) != "" {
		filters = append(filters, "assignment_id = ?")
		args = append(args, assignmentID)
	}
	if !includeHidden {
		filters = append(filters, "hidden = 0")
	}
	if len(filters) > 0 {
		query += " WHERE " + strings.Join(filters, " AND ")
	}
	query += ` ORDER BY pinned DESC, updated_at DESC`
	return s.queryQAIssues(ctx, query, args...)
}

func (s *SQLiteStore) ListQAIssuesByCourse(ctx context.Context, courseID int, includeHidden bool) ([]domain.QAIssue, error) {
	query := `SELECT id, course_id, course, assignment_id, student_no, title, status, pinned, hidden, message_count, created_at, updated_at FROM qa_issues WHERE course_id = ?`
	args := []any{courseID}
	if !includeHidden {
		query += ` AND hidden = 0`
	}
	query += ` ORDER BY pinned DESC, updated_at DESC`
	return s.queryQAIssues(ctx, query, args...)
}

func (s *SQLiteStore) queryQAIssues(ctx context.Context, query string, args ...any) ([]domain.QAIssue, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.QAIssue
	for rows.Next() {
		var issue domain.QAIssue
		var pinned, hidden int
		var created, updated string
		if err := rows.Scan(&issue.ID, &issue.CourseID, &issue.Course, &issue.AssignmentID, &issue.StudentNo,
			&issue.Title, &issue.Status, &pinned, &hidden, &issue.MessageCount, &created, &updated); err != nil {
			return nil, err
		}
		issue.Pinned = pinned != 0
		issue.Hidden = hidden != 0
		issue.CreatedAt = parseTime(time.RFC3339Nano, created)
		issue.UpdatedAt = parseTime(time.RFC3339Nano, updated)
		items = append(items, issue)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) UpdateQAIssueStatus(ctx context.Context, id int, status string) error {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE qa_issues SET status = ?, updated_at = ? WHERE id = ?`, status, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) SetQAIssuePinned(ctx context.Context, id int, pinned bool) error {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE qa_issues SET pinned = ?, updated_at = ? WHERE id = ?`, boolToInt(pinned), now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) SetQAIssueHidden(ctx context.Context, id int, hidden bool) error {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE qa_issues SET hidden = ?, updated_at = ? WHERE id = ?`, boolToInt(hidden), now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) IncrementQAIssueMessageCount(ctx context.Context, id int) error {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE qa_issues SET message_count = message_count + 1, updated_at = ? WHERE id = ?`, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) CreateQAMessage(ctx context.Context, msg *domain.QAMessage) (int64, error) {
	imagesJSON, err := encodeStringSlice(msg.Images)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO qa_messages(issue_id, sender, content, images_json, created_at) VALUES(?, ?, ?, ?, ?)`,
		msg.IssueID, msg.Sender, msg.Content, imagesJSON, msg.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func (s *SQLiteStore) ListQAMessages(ctx context.Context, issueID int) ([]domain.QAMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, issue_id, sender, content, images_json, created_at FROM qa_messages WHERE issue_id = ? ORDER BY created_at ASC`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.QAMessage
	for rows.Next() {
		var msg domain.QAMessage
		var imagesJSON string
		var created string
		if err := rows.Scan(&msg.ID, &msg.IssueID, &msg.Sender, &msg.Content, &imagesJSON, &created); err != nil {
			return nil, err
		}
		msg.Images = decodeStringSlice(imagesJSON)
		msg.CreatedAt = parseTime(time.RFC3339Nano, created)
		items = append(items, msg)
	}
	return items, rows.Err()
}
