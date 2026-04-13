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

	// a1 is now submitted, so we can create a2 for the same student
	createAttempt("a2", "t2", "quiz-a")
	no2, err := st.SubmitAttempt(ctx, "a2")
	if err != nil || no2 != 2 {
		t.Fatalf("second submit attempt_no invalid: no=%d err=%v", no2, err)
	}

	// a2 is now submitted, create a3
	createAttempt("a3", "t3", "quiz-a")
	no3, err := st.SubmitAttempt(ctx, "a3")
	if err != nil || no3 != 3 {
		t.Fatalf("third submit attempt_no invalid: no=%d err=%v", no3, err)
	}

	no3Again, err := st.SubmitAttempt(ctx, "a3")
	if err != nil || no3Again != 3 {
		t.Fatalf("resubmit should keep attempt_no: no=%d err=%v", no3Again, err)
	}

	createAttempt("b1", "tb1", "quiz-b")
	noQuizB, err := st.SubmitAttempt(ctx, "b1")
	if err != nil || noQuizB != 1 {
		t.Fatalf("new quiz should restart attempt_no: no=%d err=%v", noQuizB, err)
	}

	a3, err := st.GetAttemptByID(ctx, "a3")
	if err != nil {
		t.Fatalf("get attempt failed: %v", err)
	}
	if a3.Status != domain.StatusSubmitted || a3.AttemptNo != 3 {
		t.Fatalf("stored attempt invalid: status=%s attempt_no=%d", a3.Status, a3.AttemptNo)
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

func TestHomeworkSubmissionLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore("file:test-homework-submission?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("new sqlite store failed: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Now()
	submission := &domain.HomeworkSubmission{
		ID:           "hw-1",
		SessionToken: "token-1",
		Course:       "course-a",
		AssignmentID: "task-1",
		Name:         "张三",
		StudentNo:    "2026001",
		ClassName:    "1班",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := st.CreateHomeworkSubmission(ctx, submission); err != nil {
		t.Fatalf("CreateHomeworkSubmission failed: %v", err)
	}
	if err := st.SaveHomeworkFileMetadata(ctx, submission.ID, domain.HomeworkSlotReport, "原始报告名.pdf"); err != nil {
		t.Fatalf("SaveHomeworkFileMetadata report failed: %v", err)
	}
	if err := st.SaveHomeworkFileMetadata(ctx, submission.ID, domain.HomeworkSlotCode, "代码包.zip"); err != nil {
		t.Fatalf("SaveHomeworkFileMetadata code failed: %v", err)
	}
	if err := st.DeleteHomeworkFileMetadata(ctx, submission.ID, domain.HomeworkSlotCode); err != nil {
		t.Fatalf("DeleteHomeworkFileMetadata failed: %v", err)
	}
	if err := st.SaveHomeworkFileMetadata(ctx, submission.ID, domain.HomeworkSlotCode, "最终代码包.zip"); err != nil {
		t.Fatalf("SaveHomeworkFileMetadata code replace failed: %v", err)
	}
	if err := st.UpdateHomeworkSubmissionSession(ctx, submission.ID, "token-2", "张三同学", "1班", "my-secret"); err != nil {
		t.Fatalf("UpdateHomeworkSubmissionSession failed: %v", err)
	}

	got, err := st.GetHomeworkSubmissionByScope(ctx, "course-a", "task-1", "2026001")
	if err != nil {
		t.Fatalf("GetHomeworkSubmissionByScope failed: %v", err)
	}
	if got.SessionToken != "token-2" || got.Name != "张三同学" {
		t.Fatalf("unexpected resumed session data: %+v", got)
	}
	if got.ReportOriginalName != "原始报告名.pdf" || got.CodeOriginalName != "最终代码包.zip" {
		t.Fatalf("unexpected original names: %+v", got)
	}
	if got.ReportUploadedAt == nil || got.CodeUploadedAt == nil {
		t.Fatalf("expected uploaded timestamps: %+v", got)
	}

	items, err := st.ListHomeworkSubmissions(ctx, "course-a", "task-1")
	if err != nil {
		t.Fatalf("ListHomeworkSubmissions failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 homework submission, got %d", len(items))
	}
	if err := st.DeleteHomeworkFileMetadata(ctx, submission.ID, domain.HomeworkSlotCode); err != nil {
		t.Fatalf("DeleteHomeworkFileMetadata after upload failed: %v", err)
	}
}

func TestHomeworkSubmissionSchemaCutoverDropsLegacyColumns(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore("file:test-homework-schema-cutover?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("new sqlite store failed: %v", err)
	}
	defer st.Close()
	if _, err := st.db.ExecContext(ctx, `CREATE TABLE homework_submissions (
		id TEXT PRIMARY KEY,
		session_token TEXT UNIQUE NOT NULL,
		quiz_id TEXT NOT NULL,
		course TEXT NOT NULL,
		task_id TEXT NOT NULL,
		name TEXT NOT NULL,
		student_no TEXT NOT NULL,
		class_name TEXT NOT NULL,
		status TEXT NOT NULL,
		report_original_name TEXT NOT NULL DEFAULT '',
		report_uploaded_at TEXT,
		code_original_name TEXT NOT NULL DEFAULT '',
		code_uploaded_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		finalized_at TEXT
	)`); err != nil {
		t.Fatalf("create legacy table failed: %v", err)
	}
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	rows, err := st.db.QueryContext(ctx, `PRAGMA table_info(homework_submissions)`)
	if err != nil {
		t.Fatalf("table info query failed: %v", err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notnull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan table info failed: %v", err)
		}
		columns[name] = true
	}
	if columns["quiz_id"] || columns["task_id"] || columns["status"] || columns["finalized_at"] {
		t.Fatalf("legacy homework columns should be removed, got %+v", columns)
	}
	if !columns["course"] || !columns["assignment_id"] || !columns["updated_at"] {
		t.Fatalf("new homework columns missing: %+v", columns)
	}
}
