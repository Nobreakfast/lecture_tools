// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// buildMainBranchDB creates a SQLite database with the main-branch schema
// (no teachers/courses tables, no course_id on attempts) plus some sample data.
func buildMainBranchDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	for _, s := range []string{
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE attempts (
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
		)`,
		`CREATE TABLE answers (
			attempt_id TEXT NOT NULL,
			question_id TEXT NOT NULL,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (attempt_id, question_id)
		)`,
		`CREATE TABLE summaries (attempt_id TEXT PRIMARY KEY, summary_json TEXT NOT NULL)`,
		`CREATE TABLE admin_summaries (
			quiz_id TEXT PRIMARY KEY,
			summary_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE homework_submissions (
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
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(course, assignment_id, student_no)
		)`,
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("create: %v (%s)", err, s)
		}
	}

	now := time.Now().Format(time.RFC3339Nano)

	// Seed 2 submitted + 1 in_progress attempts across a single quiz.
	attempts := []struct {
		id, token, quiz, name, stu, class, status string
	}{
		{"a1", "t1", "week7_l1", "张三", "2023001", "A班", "submitted"},
		{"a2", "t2", "week7_l1", "张三", "2023001", "A班", "submitted"}, // second try
		{"a3", "t3", "week7_l1", "李四", "2023002", "A班", "in_progress"},
	}
	for _, a := range attempts {
		if _, err := db.ExecContext(ctx, `INSERT INTO attempts(id,session_token,quiz_id,name,student_no,class_name,attempt_no,status,created_at,updated_at,submitted_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			a.id, a.token, a.quiz, a.name, a.stu, a.class, 0, a.status, now, now, map[bool]string{true: now, false: ""}[a.status == "submitted"]); err != nil {
			t.Fatalf("insert attempt: %v", err)
		}
	}

	// One answer with a legacy image URL pointing into /uploads/
	if _, err := db.ExecContext(ctx,
		`INSERT INTO answers(attempt_id,question_id,value,updated_at) VALUES(?,?,?,?)`,
		"a1", "q1", "before /uploads/A班/week7_l1/张三_2023001/image.jpg after", now); err != nil {
		t.Fatalf("insert answer: %v", err)
	}

	// One homework submission
	if _, err := db.ExecContext(ctx,
		`INSERT INTO homework_submissions(id,session_token,course,assignment_id,name,student_no,class_name,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"h1", "ht1", "最优化方法", "task_1", "张三", "2023001", "A班", now, now); err != nil {
		t.Fatalf("insert hw: %v", err)
	}

	// One legacy setting
	if _, err := db.ExecContext(ctx,
		`INSERT INTO settings(key,value) VALUES('entry_open','true')`); err != nil {
		t.Fatalf("insert setting: %v", err)
	}
}

func TestUpgrade_FreshDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	opts := UpgradeOptions{
		DBPath:      dbPath,
		MetadataDir: filepath.Join(dir, "metadata"),
		DataDir:     filepath.Join(dir, "data"),
		PPTDir:      filepath.Join(dir, "ppt"),
		TeacherID:   "T01",
		TeacherName: "Prof",
		Password:    "secret",
		CourseSlug:  "default",
		CourseName:  "Default",
		NoBackup:    true,
		ReportPath:  filepath.Join(dir, "report.json"),
	}
	if err := Upgrade(context.Background(), opts); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM teachers`).Scan(&n)
	if n != 1 {
		t.Errorf("teachers=%d want 1", n)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM courses`).Scan(&n)
	if n != 1 {
		t.Errorf("courses=%d want 1", n)
	}
}

func TestUpgrade_MainBranch_BackfillsCourseIDs(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	buildMainBranchDB(t, dbPath)

	opts := UpgradeOptions{
		DBPath:      dbPath,
		MetadataDir: filepath.Join(dir, "metadata"),
		DataDir:     filepath.Join(dir, "data"),
		PPTDir:      filepath.Join(dir, "ppt"),
		TeacherID:   "T01",
		TeacherName: "Prof",
		Password:    "secret",
		CourseSlug:  "default",
		CourseName:  "默认课程",
		NoBackup:    true,
		ReportPath:  filepath.Join(dir, "report.json"),
	}
	if err := Upgrade(context.Background(), opts); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	db, _ := sql.Open("sqlite", dbPath)
	defer db.Close()

	// All attempts should have course_id > 0.
	var zero int
	_ = db.QueryRow(`SELECT COUNT(*) FROM attempts WHERE course_id = 0`).Scan(&zero)
	if zero != 0 {
		t.Errorf("attempts with course_id=0: %d want 0", zero)
	}

	// homework_submissions should have course_id column and course_id > 0.
	_ = db.QueryRow(`SELECT COUNT(*) FROM homework_submissions WHERE course_id = 0`).Scan(&zero)
	if zero != 0 {
		t.Errorf("homework with course_id=0: %d want 0", zero)
	}

	// attempt_no should be 1 and 2 for the two submitted attempts of 2023001.
	rows, err := db.Query(`SELECT attempt_no FROM attempts WHERE student_no='2023001' AND status='submitted' ORDER BY attempt_no`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var nos []int
	for rows.Next() {
		var n int
		_ = rows.Scan(&n)
		nos = append(nos, n)
	}
	if len(nos) != 2 || nos[0] != 1 || nos[1] != 2 {
		t.Errorf("attempt_nos = %v want [1 2]", nos)
	}

	// Legacy settings should be gone.
	var has int
	_ = db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'entry_open'`).Scan(&has)
	if has != 0 {
		t.Errorf("settings.entry_open not cleaned: %d", has)
	}
}

func TestUpgrade_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	buildMainBranchDB(t, dbPath)

	opts := UpgradeOptions{
		DBPath:      dbPath,
		MetadataDir: filepath.Join(dir, "metadata"),
		DataDir:     filepath.Join(dir, "data"),
		PPTDir:      filepath.Join(dir, "ppt"),
		TeacherID:   "T01",
		TeacherName: "Prof",
		Password:    "secret",
		CourseSlug:  "default",
		CourseName:  "Default",
		NoBackup:    true,
		ReportPath:  filepath.Join(dir, "report.json"),
	}
	if err := Upgrade(context.Background(), opts); err != nil {
		t.Fatalf("Upgrade first: %v", err)
	}

	db, _ := sql.Open("sqlite", dbPath)
	var tBefore, cBefore, aBefore int
	_ = db.QueryRow(`SELECT COUNT(*) FROM teachers`).Scan(&tBefore)
	_ = db.QueryRow(`SELECT COUNT(*) FROM courses`).Scan(&cBefore)
	_ = db.QueryRow(`SELECT COUNT(*) FROM attempts`).Scan(&aBefore)
	db.Close()

	// Run again, should be no-op (teacher already exists, no duplicates)
	if err := Upgrade(context.Background(), opts); err != nil {
		t.Fatalf("Upgrade second: %v", err)
	}

	db, _ = sql.Open("sqlite", dbPath)
	defer db.Close()
	var tAfter, cAfter, aAfter int
	_ = db.QueryRow(`SELECT COUNT(*) FROM teachers`).Scan(&tAfter)
	_ = db.QueryRow(`SELECT COUNT(*) FROM courses`).Scan(&cAfter)
	_ = db.QueryRow(`SELECT COUNT(*) FROM attempts`).Scan(&aAfter)
	if tAfter != tBefore || cAfter != cBefore || aAfter != aBefore {
		t.Errorf("counts changed: before(%d,%d,%d) after(%d,%d,%d)", tBefore, cBefore, aBefore, tAfter, cAfter, aAfter)
	}
}

func TestUpgrade_Backup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	buildMainBranchDB(t, dbPath)

	opts := UpgradeOptions{
		DBPath:      dbPath,
		MetadataDir: filepath.Join(dir, "metadata"),
		DataDir:     filepath.Join(dir, "data"),
		PPTDir:      filepath.Join(dir, "ppt"),
		TeacherID:   "T01",
		TeacherName: "Prof",
		Password:    "secret",
		CourseSlug:  "default",
		CourseName:  "Default",
		NoBackup:    false,
		ReportPath:  filepath.Join(dir, "report.json"),
	}
	if err := Upgrade(context.Background(), opts); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	// A backup file should exist matching app.db.prelegacy-*.bak
	matches, _ := filepath.Glob(dbPath + ".prelegacy-*.bak")
	if len(matches) == 0 {
		t.Errorf("backup file not created")
	}
	// Report exists
	if _, err := os.Stat(opts.ReportPath); err != nil {
		t.Errorf("report not written: %v", err)
	}
}
