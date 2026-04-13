package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	}
	for _, p := range pragmas {
		if _, err := s.db.ExecContext(ctx, p); err != nil {
			return err
		}
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS attempts (
			id TEXT PRIMARY KEY,
			session_token TEXT UNIQUE NOT NULL,
			quiz_id TEXT NOT NULL DEFAULT '',
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
			quiz_id TEXT PRIMARY KEY,
			summary_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
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

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_attempts_quiz_status ON attempts(quiz_id, status);`,
		`CREATE INDEX IF NOT EXISTS idx_attempts_lookup ON attempts(quiz_id, student_no, status);`,
		`CREATE INDEX IF NOT EXISTS idx_answers_attempt ON answers(attempt_id);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_attempts_one_active ON attempts(quiz_id, student_no) WHERE status = 'in_progress';`,
		`CREATE INDEX IF NOT EXISTS idx_homework_submissions_lookup ON homework_submissions(course, assignment_id, student_no);`,
		`CREATE INDEX IF NOT EXISTS idx_homework_submissions_assignment ON homework_submissions(course, assignment_id, created_at DESC);`,
	}
	for _, idx := range indexes {
		if _, err := s.db.ExecContext(ctx, idx); err != nil {
			return err
		}
	}

	return nil
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO attempts(id, session_token, quiz_id, name, student_no, class_name, attempt_no, status, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.SessionToken, a.QuizID, a.Name, a.StudentNo, a.ClassName, a.AttemptNo, string(a.Status), a.CreatedAt.Format(time.RFC3339Nano), a.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) ListAttempts(ctx context.Context) ([]domain.Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, session_token, quiz_id, name, student_no, class_name, attempt_no, status, created_at, updated_at, submitted_at FROM attempts ORDER BY created_at DESC`)
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, session_token, quiz_id, name, student_no, class_name, attempt_no, status, created_at, updated_at, submitted_at FROM attempts WHERE id = ?`, attemptID)
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, session_token, quiz_id, name, student_no, class_name, attempt_no, status, created_at, updated_at, submitted_at FROM attempts WHERE session_token = ?`, token)
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
	err = tx.QueryRowContext(ctx, `SELECT quiz_id, student_no, status, attempt_no FROM attempts WHERE id = ?`, attemptID).Scan(&quizID, &studentNo, &status, &existingAttemptNo)
	if err != nil {
		return 0, err
	}
	if status == string(domain.StatusSubmitted) {
		if err = tx.Commit(); err != nil {
			return 0, err
		}
		return existingAttemptNo, nil
	}

	var nextAttemptNo int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt_no), 0) + 1 FROM attempts WHERE quiz_id = ? AND student_no = ? AND status = ?`, quizID, studentNo, string(domain.StatusSubmitted)).Scan(&nextAttemptNo); err != nil {
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

func (s *SQLiteStore) GetInProgressAttempt(ctx context.Context, quizID, studentNo string) (*domain.Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, session_token, quiz_id, name, student_no, class_name, attempt_no, status, created_at, updated_at, submitted_at FROM attempts WHERE quiz_id = ? AND student_no = ? AND status = 'in_progress' ORDER BY created_at DESC LIMIT 1`, quizID, studentNo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanAttemptRows(rows)
}

func (s *SQLiteStore) UpdateAttemptSession(ctx context.Context, attemptID, token, name, className string) error {
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `UPDATE attempts SET session_token = ?, name = ?, class_name = ?, updated_at = ? WHERE id = ?`, token, name, className, now, attemptID)
	return err
}

func (s *SQLiteStore) UpsertAdminSummary(ctx context.Context, quizID string, summaryJSON string) error {
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO admin_summaries(quiz_id, summary_json, created_at, updated_at) VALUES(?, ?, ?, ?) ON CONFLICT(quiz_id) DO UPDATE SET summary_json=excluded.summary_json, updated_at=excluded.updated_at`, quizID, summaryJSON, now, now)
	return err
}

func (s *SQLiteStore) GetAdminSummary(ctx context.Context, quizID string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT summary_json FROM admin_summaries WHERE quiz_id = ?`, quizID).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func (s *SQLiteStore) DeleteAdminSummary(ctx context.Context, quizID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM admin_summaries WHERE quiz_id = ?`, quizID)
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_summaries WHERE quiz_id = ?`, quizID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM attempts WHERE quiz_id = ?`, quizID); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteStore) CreateHomeworkSubmission(ctx context.Context, submission *domain.HomeworkSubmission) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO homework_submissions(
		id, session_token, course, assignment_id, name, student_no, class_name, secret_key,
		report_original_name, report_uploaded_at, code_original_name, code_uploaded_at,
		created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		submission.ID,
		submission.SessionToken,
		submission.Course,
		submission.AssignmentID,
		submission.Name,
		submission.StudentNo,
		submission.ClassName,
		submission.SecretKey,
		submission.ReportOriginalName,
		formatTimePtr(submission.ReportUploadedAt),
		submission.CodeOriginalName,
		formatTimePtr(submission.CodeUploadedAt),
		submission.CreatedAt.Format(time.RFC3339Nano),
		submission.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) GetHomeworkSubmissionByID(ctx context.Context, submissionID string) (*domain.HomeworkSubmission, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, session_token, course, assignment_id, name, student_no, class_name, secret_key, report_original_name, report_uploaded_at, code_original_name, code_uploaded_at, created_at, updated_at FROM homework_submissions WHERE id = ?`, submissionID)
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, session_token, course, assignment_id, name, student_no, class_name, secret_key, report_original_name, report_uploaded_at, code_original_name, code_uploaded_at, created_at, updated_at FROM homework_submissions WHERE session_token = ?`, token)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanHomeworkSubmissionRows(rows)
}

func (s *SQLiteStore) GetHomeworkSubmissionByScope(ctx context.Context, course, assignmentID, studentNo string) (*domain.HomeworkSubmission, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, session_token, course, assignment_id, name, student_no, class_name, secret_key, report_original_name, report_uploaded_at, code_original_name, code_uploaded_at, created_at, updated_at FROM homework_submissions WHERE course = ? AND assignment_id = ? AND student_no = ? LIMIT 1`, course, assignmentID, studentNo)
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

func (s *SQLiteStore) ListHomeworkSubmissions(ctx context.Context, course, assignmentID string) ([]domain.HomeworkSubmission, error) {
	query := `SELECT id, session_token, course, assignment_id, name, student_no, class_name, secret_key, report_original_name, report_uploaded_at, code_original_name, code_uploaded_at, created_at, updated_at FROM homework_submissions`
	args := make([]any, 0, 2)
	filters := make([]string, 0, 2)
	if strings.TrimSpace(course) != "" {
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

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

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
	if err := sc.Scan(&a.ID, &a.SessionToken, &a.QuizID, &a.Name, &a.StudentNo, &a.ClassName, &a.AttemptNo, &status, &created, &updated, &submitted); err != nil {
		return nil, err
	}
	a.Status = domain.AttemptStatus(status)
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	a.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if submitted.Valid {
		t, _ := time.Parse(time.RFC3339Nano, submitted.String)
		a.SubmittedAt = &t
	}
	return &a, nil
}

func scanHomeworkSubmissionRows(sc rowScanner) (*domain.HomeworkSubmission, error) {
	var item domain.HomeworkSubmission
	var reportUploaded sql.NullString
	var codeUploaded sql.NullString
	var created string
	var updated string
	if err := sc.Scan(
		&item.ID,
		&item.SessionToken,
		&item.Course,
		&item.AssignmentID,
		&item.Name,
		&item.StudentNo,
		&item.ClassName,
		&item.SecretKey,
		&item.ReportOriginalName,
		&reportUploaded,
		&item.CodeOriginalName,
		&codeUploaded,
		&created,
		&updated,
	); err != nil {
		return nil, err
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	item.ReportUploadedAt = parseTimePtr(reportUploaded)
	item.CodeUploadedAt = parseTimePtr(codeUploaded)
	return &item, nil
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
		required := []string{"id", "session_token", "course", "assignment_id", "name", "student_no", "class_name", "secret_key", "report_original_name", "report_uploaded_at", "code_original_name", "code_uploaded_at", "created_at", "updated_at"}
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
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(course, assignment_id, student_no)
	)`)
	return err
}

func homeworkFileColumns(slot domain.HomeworkFileSlot) (string, string, error) {
	switch slot {
	case domain.HomeworkSlotReport:
		return "report_original_name", "report_uploaded_at", nil
	case domain.HomeworkSlotCode:
		return "code_original_name", "code_uploaded_at", nil
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
	hasQuizID := false
	hasAttemptNo := false
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
		if name == "quiz_id" {
			hasQuizID = true
		}
		if name == "attempt_no" {
			hasAttemptNo = true
		}
	}
	if !hasQuizID {
		if _, err = s.db.ExecContext(ctx, `ALTER TABLE attempts ADD COLUMN quiz_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !hasAttemptNo {
		if _, err = s.db.ExecContext(ctx, `ALTER TABLE attempts ADD COLUMN attempt_no INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return nil
}
