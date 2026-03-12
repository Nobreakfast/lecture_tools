package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := s.ensureAttemptsColumns(ctx); err != nil {
		return err
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
