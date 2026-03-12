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
