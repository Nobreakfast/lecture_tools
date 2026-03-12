package store

import (
	"context"
	"testing"
	"time"

	"course-assistant/internal/domain"
)

func TestSubmitAttemptAssignsAttemptNoBySubmitted(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore("file:test-submit-attempt?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("new sqlite store failed: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Now()
	createAttempt := func(id, token, quizID string) {
		err := st.CreateAttempt(ctx, &domain.Attempt{
			ID:           id,
			SessionToken: token,
			QuizID:       quizID,
			Name:         "张三",
			StudentNo:    "2026001",
			ClassName:    "1班",
			Status:       domain.StatusInProgress,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			t.Fatalf("create attempt failed: %v", err)
		}
	}

	createAttempt("a1", "t1", "quiz-a")
	no1, err := st.SubmitAttempt(ctx, "a1")
	if err != nil || no1 != 1 {
		t.Fatalf("first submit attempt_no invalid: no=%d err=%v", no1, err)
	}

	createAttempt("a2", "t2", "quiz-a")
	no2, err := st.SubmitAttempt(ctx, "a2")
	if err != nil || no2 != 2 {
		t.Fatalf("second submit attempt_no invalid: no=%d err=%v", no2, err)
	}

	createAttempt("a3", "t3", "quiz-a")

	createAttempt("a4", "t4", "quiz-a")
	no3, err := st.SubmitAttempt(ctx, "a4")
	if err != nil || no3 != 3 {
		t.Fatalf("third submit attempt_no invalid: no=%d err=%v", no3, err)
	}

	no3Again, err := st.SubmitAttempt(ctx, "a4")
	if err != nil || no3Again != 3 {
		t.Fatalf("resubmit should keep attempt_no: no=%d err=%v", no3Again, err)
	}

	createAttempt("b1", "tb1", "quiz-b")
	noQuizB, err := st.SubmitAttempt(ctx, "b1")
	if err != nil || noQuizB != 1 {
		t.Fatalf("new quiz should restart attempt_no: no=%d err=%v", noQuizB, err)
	}

	a4, err := st.GetAttemptByID(ctx, "a4")
	if err != nil {
		t.Fatalf("get attempt failed: %v", err)
	}
	if a4.Status != domain.StatusSubmitted || a4.AttemptNo != 3 {
		t.Fatalf("stored attempt invalid: status=%s attempt_no=%d", a4.Status, a4.AttemptNo)
	}
}

func TestClearAttemptsOnlyClearsTargetQuiz(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore("file:test-clear-attempts-by-quiz?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("new sqlite store failed: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Now()
	createAttempt := func(id, token, quizID string) {
		err := st.CreateAttempt(ctx, &domain.Attempt{
			ID:           id,
			SessionToken: token,
			QuizID:       quizID,
			Name:         "李四",
			StudentNo:    "2026002",
			ClassName:    "2班",
			Status:       domain.StatusInProgress,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		if err != nil {
			t.Fatalf("create attempt failed: %v", err)
		}
	}

	createAttempt("a1", "t-a1", "quiz-a")
	createAttempt("b1", "t-b1", "quiz-b")
	if err := st.SaveAnswer(ctx, domain.Answer{AttemptID: "a1", QuestionID: "q1", Value: "A", UpdatedAt: now}); err != nil {
		t.Fatalf("save answer a1 failed: %v", err)
	}
	if err := st.SaveAnswer(ctx, domain.Answer{AttemptID: "b1", QuestionID: "q1", Value: "B", UpdatedAt: now}); err != nil {
		t.Fatalf("save answer b1 failed: %v", err)
	}
	if err := st.UpsertSummary(ctx, "a1", `{"tip":"a"}`); err != nil {
		t.Fatalf("save summary a1 failed: %v", err)
	}
	if err := st.UpsertSummary(ctx, "b1", `{"tip":"b"}`); err != nil {
		t.Fatalf("save summary b1 failed: %v", err)
	}

	if err := st.ClearAttempts(ctx, "quiz-a"); err != nil {
		t.Fatalf("clear attempts failed: %v", err)
	}

	var attemptCountA int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE quiz_id = ?`, "quiz-a").Scan(&attemptCountA); err != nil {
		t.Fatalf("count quiz-a attempts failed: %v", err)
	}
	if attemptCountA != 0 {
		t.Fatalf("quiz-a attempts should be cleared, got %d", attemptCountA)
	}
	var answerCountA int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM answers WHERE attempt_id = ?`, "a1").Scan(&answerCountA); err != nil {
		t.Fatalf("count quiz-a answers failed: %v", err)
	}
	if answerCountA != 0 {
		t.Fatalf("quiz-a answers should be cleared, got %d", answerCountA)
	}
	var summaryCountA int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM summaries WHERE attempt_id = ?`, "a1").Scan(&summaryCountA); err != nil {
		t.Fatalf("count quiz-a summaries failed: %v", err)
	}
	if summaryCountA != 0 {
		t.Fatalf("quiz-a summaries should be cleared, got %d", summaryCountA)
	}

	var attemptCountB int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE quiz_id = ?`, "quiz-b").Scan(&attemptCountB); err != nil {
		t.Fatalf("count quiz-b attempts failed: %v", err)
	}
	if attemptCountB != 1 {
		t.Fatalf("quiz-b attempts should be kept, got %d", attemptCountB)
	}
	var answerCountB int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM answers WHERE attempt_id = ?`, "b1").Scan(&answerCountB); err != nil {
		t.Fatalf("count quiz-b answers failed: %v", err)
	}
	if answerCountB != 1 {
		t.Fatalf("quiz-b answers should be kept, got %d", answerCountB)
	}
	var summaryCountB int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM summaries WHERE attempt_id = ?`, "b1").Scan(&summaryCountB); err != nil {
		t.Fatalf("count quiz-b summaries failed: %v", err)
	}
	if summaryCountB != 1 {
		t.Fatalf("quiz-b summaries should be kept, got %d", summaryCountB)
	}
}
