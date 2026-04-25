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

	got, err := st.GetHomeworkSubmissionByScope(ctx, 0, "course-a", "task-1", "2026001")
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

	items, err := st.ListHomeworkSubmissions(ctx, 0, "course-a", "task-1")
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

func TestCreateCoursePersistsDisplayAndInternalName(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore("file:test-course-names?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("new sqlite store failed: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Now()
	if err := st.CreateTeacher(ctx, &domain.Teacher{
		ID:           "T01",
		Name:         "测试教师",
		PasswordHash: "hash",
		Role:         domain.RoleTeacher,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("CreateTeacher failed: %v", err)
	}
	course := &domain.Course{
		TeacherID:    "T01",
		Name:         "机器学习导论",
		DisplayName:  "Machine Learning Intro",
		InternalName: "Machine_Learning_Intro",
		Slug:         "Machine_Learning_Intro",
		InviteCode:   "ABC123",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := st.CreateCourse(ctx, course); err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	got, err := st.GetCourse(ctx, course.ID)
	if err != nil {
		t.Fatalf("GetCourse failed: %v", err)
	}
	if got.DisplayName != "Machine Learning Intro" {
		t.Fatalf("display_name = %q", got.DisplayName)
	}
	if got.InternalName != "Machine_Learning_Intro" {
		t.Fatalf("internal_name = %q", got.InternalName)
	}
	if got.Slug != got.InternalName {
		t.Fatalf("legacy slug mismatch: %q vs %q", got.Slug, got.InternalName)
	}
}

func TestGetCourseFallsBackToLegacySlugWhenDualNamesMissing(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore("file:test-course-names-legacy?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("new sqlite store failed: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Now().Format(time.RFC3339Nano)
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO teachers(id, name, password_hash, role, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)`,
		"T01", "测试教师", "hash", string(domain.RoleTeacher), now, now); err != nil {
		t.Fatalf("insert teacher failed: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO courses(teacher_id, name, display_name, internal_name, slug, invite_code, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		"T01", "旧课程", "", "", "legacy_slug", "XYZ789", now, now); err != nil {
		t.Fatalf("insert legacy-like course failed: %v", err)
	}

	got, err := st.GetCourseByInviteCode(ctx, "XYZ789")
	if err != nil {
		t.Fatalf("GetCourseByInviteCode failed: %v", err)
	}
	if got.DisplayName != "legacy_slug" {
		t.Fatalf("display_name fallback = %q", got.DisplayName)
	}
	if got.InternalName != "legacy_slug" {
		t.Fatalf("internal_name fallback = %q", got.InternalName)
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
	if !columns["course_id"] {
		t.Fatalf("homework_submissions.course_id missing after Init")
	}
}

// TestSubmitAttempt_IsScopedToCourse verifies that two courses sharing the
// same YAML quiz_id maintain independent attempt_no sequences for the same
// student — the core multi-tenant isolation fix.
func TestSubmitAttempt_IsScopedToCourse(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore("file:test-submit-course?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("new sqlite store failed: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Now()
	mk := func(id, token string, course int) {
		if err := st.CreateAttempt(ctx, &domain.Attempt{
			ID:           id,
			SessionToken: token,
			QuizID:       "week1",
			CourseID:     course,
			Name:         "张三",
			StudentNo:    "2027001",
			ClassName:    "1班",
			Status:       domain.StatusInProgress,
			CreatedAt:    now,
			UpdatedAt:    now,
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	// Course 1, first attempt → attempt_no = 1
	mk("a1", "t1", 1)
	n, err := st.SubmitAttempt(ctx, "a1")
	if err != nil || n != 1 {
		t.Fatalf("course=1 first submit n=%d err=%v", n, err)
	}
	// Course 2, first attempt for the SAME student+quiz_id → should ALSO be 1
	mk("a2", "t2", 2)
	n, err = st.SubmitAttempt(ctx, "a2")
	if err != nil || n != 1 {
		t.Fatalf("course=2 first submit n=%d want 1 err=%v (would indicate cross-course numbering bug)", n, err)
	}
	// Course 1, second attempt → 2
	mk("a3", "t3", 1)
	n, err = st.SubmitAttempt(ctx, "a3")
	if err != nil || n != 2 {
		t.Fatalf("course=1 second submit n=%d want 2 err=%v", n, err)
	}
}

// TestAdminSummaries_CompositeKey verifies that two courses with the same
// YAML quiz_id can hold independent AI summaries without overwriting each other.
func TestAdminSummaries_CompositeKey(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore("file:test-summary-course?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("new sqlite store failed: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if err := st.UpsertAdminSummary(ctx, 1, "week1", `{"c":1}`); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	if err := st.UpsertAdminSummary(ctx, 2, "week1", `{"c":2}`); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	s1, err := st.GetAdminSummary(ctx, 1, "week1")
	if err != nil || s1 != `{"c":1}` {
		t.Fatalf("course=1 summary = %q err=%v", s1, err)
	}
	s2, err := st.GetAdminSummary(ctx, 2, "week1")
	if err != nil || s2 != `{"c":2}` {
		t.Fatalf("course=2 summary = %q err=%v", s2, err)
	}
	if err := st.DeleteAdminSummary(ctx, 1, "week1"); err != nil {
		t.Fatalf("delete 1: %v", err)
	}
	if _, err := st.GetAdminSummary(ctx, 1, "week1"); err == nil {
		t.Fatalf("course=1 summary should be deleted")
	}
	if _, err := st.GetAdminSummary(ctx, 2, "week1"); err != nil {
		t.Fatalf("course=2 summary should still exist")
	}
}

// TestClearAttemptsByCourse_IsolatesCourses verifies that clearing attempts
// for one course does not affect another course sharing the same quiz_id.
func TestClearAttemptsByCourse_IsolatesCourses(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore("file:test-clear-by-course?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("new sqlite store failed: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Now()
	mkAttempt := func(id, token string, courseID int) {
		if err := st.CreateAttempt(ctx, &domain.Attempt{
			ID:           id,
			SessionToken: token,
			QuizID:       "shared-quiz",
			CourseID:     courseID,
			Name:         "学生",
			StudentNo:    "001",
			ClassName:    "班级",
			Status:       domain.StatusSubmitted,
			CreatedAt:    now,
			UpdatedAt:    now,
		}); err != nil {
			t.Fatalf("create attempt %s: %v", id, err)
		}
		if err := st.SaveAnswer(ctx, domain.Answer{AttemptID: id, QuestionID: "q1", Value: "A", UpdatedAt: now}); err != nil {
			t.Fatalf("save answer %s: %v", id, err)
		}
		if err := st.UpsertSummary(ctx, id, `{"tip":"ok"}`); err != nil {
			t.Fatalf("save summary %s: %v", id, err)
		}
	}

	mkAttempt("c1a1", "t-c1a1", 1)
	mkAttempt("c2a1", "t-c2a1", 2)
	if err := st.UpsertAdminSummary(ctx, 1, "shared-quiz", `{"c":1}`); err != nil {
		t.Fatalf("upsert admin summary c1: %v", err)
	}
	if err := st.UpsertAdminSummary(ctx, 2, "shared-quiz", `{"c":2}`); err != nil {
		t.Fatalf("upsert admin summary c2: %v", err)
	}

	if err := st.ClearAttemptsByCourse(ctx, 1, "shared-quiz"); err != nil {
		t.Fatalf("ClearAttemptsByCourse failed: %v", err)
	}

	// Course 1 data should be gone
	if _, err := st.GetAttemptByID(ctx, "c1a1"); err == nil {
		t.Fatal("course 1 attempt should be deleted")
	}
	var ansC1 int
	st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM answers WHERE attempt_id = ?`, "c1a1").Scan(&ansC1)
	if ansC1 != 0 {
		t.Fatalf("course 1 answers should be deleted, got %d", ansC1)
	}
	if _, err := st.GetAdminSummary(ctx, 1, "shared-quiz"); err == nil {
		t.Fatal("course 1 admin summary should be deleted")
	}

	// Course 2 data should still exist
	a2, err := st.GetAttemptByID(ctx, "c2a1")
	if err != nil || a2 == nil {
		t.Fatalf("course 2 attempt should still exist: %v", err)
	}
	var ansC2 int
	st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM answers WHERE attempt_id = ?`, "c2a1").Scan(&ansC2)
	if ansC2 != 1 {
		t.Fatalf("course 2 answers should be kept, got %d", ansC2)
	}
	s2, err := st.GetAdminSummary(ctx, 2, "shared-quiz")
	if err != nil || s2 != `{"c":2}` {
		t.Fatalf("course 2 admin summary should still exist: %q err=%v", s2, err)
	}
}

// TestDeleteCourse_CascadesAllData verifies that deleting a course also
// removes its attempts, answers, summaries, admin_summaries, and homework.
func TestDeleteCourse_CascadesAllData(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore("file:test-delete-cascade?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("new sqlite store failed: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Now()
	nowStr := now.Format(time.RFC3339Nano)

	// Create teacher + course
	if err := st.CreateTeacher(ctx, &domain.Teacher{
		ID: "t1", Name: "T1", PasswordHash: "h", Role: domain.RoleTeacher,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create teacher: %v", err)
	}
	if err := st.CreateCourse(ctx, &domain.Course{
		TeacherID: "t1", Name: "C1", Slug: "c1", InviteCode: "INV1",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create course: %v", err)
	}
	courses, _ := st.ListCoursesByTeacher(ctx, "t1")
	if len(courses) != 1 {
		t.Fatalf("expected 1 course, got %d", len(courses))
	}
	cid := courses[0].ID

	if err := st.SetCourseState(ctx, &domain.CourseState{CourseID: cid, EntryOpen: true}); err != nil {
		t.Fatalf("set course state: %v", err)
	}
	if err := st.CreateAttempt(ctx, &domain.Attempt{
		ID: "a1", SessionToken: "tok1", QuizID: "q", CourseID: cid,
		Name: "S", StudentNo: "001", ClassName: "C",
		Status: domain.StatusSubmitted, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create attempt: %v", err)
	}
	st.SaveAnswer(ctx, domain.Answer{AttemptID: "a1", QuestionID: "q1", Value: "A", UpdatedAt: now})
	st.UpsertSummary(ctx, "a1", `{"x":1}`)
	st.UpsertAdminSummary(ctx, cid, "q", `{"admin":1}`)
	st.db.ExecContext(ctx, `INSERT INTO homework_submissions(id,session_token,course,course_id,assignment_id,name,student_no,class_name,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		"hw1", "hwtok", "c1", cid, "task1", "S", "001", "C", nowStr, nowStr)

	if err := st.DeleteCourse(ctx, cid); err != nil {
		t.Fatalf("delete course: %v", err)
	}

	countRow := func(table, where string, args ...any) int {
		var n int
		st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE "+where, args...).Scan(&n)
		return n
	}
	if countRow("courses", "id = ?", cid) != 0 {
		t.Fatal("course should be deleted")
	}
	if countRow("course_state", "course_id = ?", cid) != 0 {
		t.Fatal("course_state should be deleted")
	}
	if countRow("attempts", "course_id = ?", cid) != 0 {
		t.Fatal("attempts should be deleted")
	}
	if countRow("answers", "attempt_id = ?", "a1") != 0 {
		t.Fatal("answers should be deleted")
	}
	if countRow("summaries", "attempt_id = ?", "a1") != 0 {
		t.Fatal("summaries should be deleted")
	}
	if countRow("admin_summaries", "course_id = ?", cid) != 0 {
		t.Fatal("admin_summaries should be deleted")
	}
	if countRow("homework_submissions", "course_id = ?", cid) != 0 {
		t.Fatal("homework_submissions should be deleted")
	}
}

// TestHomeworkSubmission_ScopedByCourseID verifies that homework submissions
// are properly isolated by course_id, not just slug.
func TestHomeworkSubmission_ScopedByCourseID(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore("file:test-hw-course-scope?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("new sqlite store failed: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Now()
	// Two courses with the same slug (different teachers, allowed by schema)
	hw1 := &domain.HomeworkSubmission{
		ID: "hw1", SessionToken: "t1", Course: "same-slug", CourseID: 10,
		AssignmentID: "task1", Name: "张三", StudentNo: "001", ClassName: "A",
		CreatedAt: now, UpdatedAt: now,
	}
	hw2 := &domain.HomeworkSubmission{
		ID: "hw2", SessionToken: "t2", Course: "same-slug", CourseID: 20,
		AssignmentID: "task1", Name: "李四", StudentNo: "001", ClassName: "B",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := st.CreateHomeworkSubmission(ctx, hw1); err != nil {
		t.Fatalf("create hw1: %v", err)
	}
	if err := st.CreateHomeworkSubmission(ctx, hw2); err != nil {
		t.Fatalf("create hw2: %v", err)
	}

	// Scoped lookup by course_id should find the correct one
	got, err := st.GetHomeworkSubmissionByScope(ctx, 10, "", "task1", "001")
	if err != nil {
		t.Fatalf("scope lookup course=10: %v", err)
	}
	if got.ID != "hw1" || got.Name != "张三" {
		t.Fatalf("wrong submission for course 10: %+v", got)
	}

	got2, err := st.GetHomeworkSubmissionByScope(ctx, 20, "", "task1", "001")
	if err != nil {
		t.Fatalf("scope lookup course=20: %v", err)
	}
	if got2.ID != "hw2" || got2.Name != "李四" {
		t.Fatalf("wrong submission for course 20: %+v", got2)
	}

	// List by course_id
	list10, err := st.ListHomeworkSubmissions(ctx, 10, "", "")
	if err != nil {
		t.Fatalf("list course 10: %v", err)
	}
	if len(list10) != 1 || list10[0].ID != "hw1" {
		t.Fatalf("course 10 list wrong: %d items", len(list10))
	}

	list20, err := st.ListHomeworkSubmissions(ctx, 20, "", "task1")
	if err != nil {
		t.Fatalf("list course 20: %v", err)
	}
	if len(list20) != 1 || list20[0].ID != "hw2" {
		t.Fatalf("course 20 list wrong: %d items", len(list20))
	}
}

// TestForeignKeysEnforced verifies PRAGMA foreign_keys = ON is active.
func TestForeignKeysEnforced(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore("file:test-fk-enforced?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("new sqlite store failed: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	var fkEnabled int
	if err := st.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fkEnabled); err != nil {
		t.Fatalf("PRAGMA foreign_keys failed: %v", err)
	}
	if fkEnabled != 1 {
		t.Fatalf("foreign_keys should be ON, got %d", fkEnabled)
	}

	// Inserting a course with a non-existent teacher_id should fail
	err = st.CreateCourse(ctx, &domain.Course{
		TeacherID: "nonexistent", Name: "Bad", Slug: "bad", InviteCode: "BADCODE",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err == nil {
		t.Fatal("expected FK violation when inserting course with non-existent teacher_id")
	}
}
