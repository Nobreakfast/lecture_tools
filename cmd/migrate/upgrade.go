// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// CourseSpec defines a course to create during migration.
// Prefix is optional: quiz_ids starting with it are assigned to this course
// and the prefix is stripped during quiz_id normalization.
type CourseSpec struct {
	Slug   string
	Name   string
	Prefix string
}

// UpgradeOptions configures a full upgrade run.
type UpgradeOptions struct {
	DBPath        string
	MetadataDir   string
	DataDir       string
	PPTDir        string
	QuizAssetsDir string

	TeacherID         string
	TeacherName       string
	Password          string
	SkipTeacherCreate bool
	CourseSlug        string // deprecated: use Courses
	CourseName        string // deprecated: use Courses
	Courses           []CourseSpec

	NoBackup   bool
	DryRun     bool
	ReportPath string
}

// Report summarises what the upgrade did. Written to ReportPath on success.
type Report struct {
	StartedAt       time.Time         `json:"started_at"`
	FinishedAt      time.Time         `json:"finished_at"`
	DBPath          string            `json:"db_path"`
	BackupPath      string            `json:"backup_path,omitempty"`
	SourceSchema    string            `json:"source_schema"`
	TeacherID       string            `json:"teacher_id"`
	CourseID        int64             `json:"course_id"`
	CourseSlug      string            `json:"course_slug"`
	CoursesCreated  map[string]int64  `json:"courses_created,omitempty"`
	AttemptsUpdated int64             `json:"attempts_updated"`
	HomeworkUpdated int64             `json:"homework_updated"`
	FilesCopied     map[string]int    `json:"files_copied"`
	QuizIDsRenamed  map[string]string `json:"quiz_ids_renamed,omitempty"`
	Warnings        []string          `json:"warnings,omitempty"`
	Notes           []string          `json:"notes,omitempty"`
}

// Upgrade runs the full idempotent upgrade pipeline.
func Upgrade(ctx context.Context, opts UpgradeOptions) error {
	// Normalize: populate Courses from legacy fields if not set.
	if len(opts.Courses) == 0 {
		slug := opts.CourseSlug
		if slug == "" {
			slug = "default"
		}
		name := opts.CourseName
		if name == "" {
			name = "随堂测验"
		}
		opts.Courses = []CourseSpec{{Slug: slug, Name: name}}
	}

	rep := &Report{
		StartedAt:      time.Now(),
		DBPath:         opts.DBPath,
		CourseSlug:     opts.Courses[0].Slug,
		FilesCopied:    map[string]int{},
		QuizIDsRenamed: map[string]string{},
		CoursesCreated: map[string]int64{},
	}

	step := 0
	next := func(title string) {
		step++
		fmt.Printf("\n=== 步骤 %d: %s ===\n", step, title)
	}

	// 1. Backup
	if !opts.NoBackup && !opts.DryRun {
		next("数据库备份")
		if _, err := os.Stat(opts.DBPath); err == nil {
			bp, err := backupDB(opts.DBPath)
			if err != nil {
				return fmt.Errorf("backup: %w", err)
			}
			rep.BackupPath = bp
			ok("备份 → %s", bp)
		} else {
			ok("数据库尚不存在，跳过备份")
		}
	}

	// Ensure parent dirs exist
	if !opts.DryRun {
		_ = os.MkdirAll(filepath.Dir(opts.DBPath), 0o755)
		_ = os.MkdirAll(opts.MetadataDir, 0o755)
	}

	db, err := openDB(opts.DBPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	// 2. Detect source schema
	next("识别源 schema")
	src := detectSchema(ctx, db)
	rep.SourceSchema = src
	ok("源 schema: %s", src)

	// 3. Core tables (idempotent CREATE IF NOT EXISTS)
	next("建立/确认核心表")
	if err := ensureCoreTables(ctx, db); err != nil {
		return err
	}
	ok("核心表就绪")

	// 4. Ensure columns / auxiliary schema
	next("确保列与辅助 schema")
	ensureColumn(db, ctx, "attempts", "quiz_id", `TEXT NOT NULL DEFAULT ''`)
	ensureColumn(db, ctx, "attempts", "attempt_no", `INTEGER NOT NULL DEFAULT 0`)
	ensureColumn(db, ctx, "attempts", "course_id", `INTEGER NOT NULL DEFAULT 0`)
	ensureColumn(db, ctx, "courses", "display_name", `TEXT NOT NULL DEFAULT ''`)
	ensureColumn(db, ctx, "courses", "internal_name", `TEXT NOT NULL DEFAULT ''`)
	db.ExecContext(ctx, `UPDATE attempts SET course_id = 0 WHERE typeof(course_id) = 'text' OR course_id = ''`)
	db.ExecContext(ctx, `UPDATE courses SET internal_name = slug WHERE internal_name = '' OR internal_name IS NULL`)
	db.ExecContext(ctx, `UPDATE courses SET display_name = internal_name WHERE display_name = '' OR display_name IS NULL`)
	if err := ensureHomeworkSchema(ctx, db); err != nil {
		return fmt.Errorf("homework schema: %w", err)
	}
	ensureColumn(db, ctx, "homework_submissions", "course_id", `INTEGER NOT NULL DEFAULT 0`)
	ensureColumn(db, ctx, "admin_summaries", "course_id", `INTEGER NOT NULL DEFAULT 0`)
	ok("列/辅助 schema 已确认")

	// 5. Ensure default teacher
	next("默认教师账号")
	if err := ensureTeacher(ctx, db, &opts); err != nil {
		return err
	}
	rep.TeacherID = opts.TeacherID

	// 6. Migrate legacy TEXT courses.id → INTEGER (if needed)
	next("courses 表 ID 类型迁移")
	if err := migrateCourseIDsToInt(ctx, db, opts.TeacherID); err != nil {
		return err
	}

	// 7. Create all courses
	next("创建课程")
	courseMap := map[string]int64{} // slug → course_id
	for _, cs := range opts.Courses {
		cid, err := ensureDefaultCourse(ctx, db, opts.TeacherID, cs.Slug, cs.Name)
		if err != nil {
			return err
		}
		courseMap[cs.Slug] = cid
		rep.CoursesCreated[cs.Slug] = cid
		ok("课程 id=%d slug=%s name=%s", cid, cs.Slug, cs.Name)
	}
	rep.CourseID = courseMap[opts.Courses[0].Slug]

	// 7b. Build quiz_id rename map
	quizRenameMap := buildQuizRenameMap(ctx, db, opts.Courses)
	if len(quizRenameMap) > 0 {
		ok("quiz_id 重命名映射: %d 条", len(quizRenameMap))
		for old, nw := range quizRenameMap {
			fmt.Printf("    %s → %s\n", old, nw)
		}
	}

	// 8. Backfill course_id with prefix-based assignment
	next("回填 course_id（按前缀分配课程）")
	hasPrefix := false
	for _, cs := range opts.Courses {
		if cs.Prefix != "" {
			hasPrefix = true
			break
		}
	}
	if hasPrefix {
		n, err := backfillCourseIDsByPrefix(ctx, db, opts.Courses, courseMap)
		if err != nil {
			return err
		}
		rep.AttemptsUpdated = n
	} else {
		defaultCID := courseMap[opts.Courses[0].Slug]
		if n, err := backfillCourseIDs(ctx, db, defaultCID); err == nil {
			rep.AttemptsUpdated = n
			ok("更新 attempts: %d 行 course_id=%d", n, defaultCID)
		} else {
			return err
		}
	}
	{
		defaultCID := courseMap[opts.Courses[0].Slug]
		if n, err := backfillHomeworkCourseIDs(ctx, db, defaultCID); err == nil {
			rep.HomeworkUpdated = n
			ok("更新 homework_submissions: %d 行 course_id=%d", n, defaultCID)
		} else {
			return err
		}
	}

	// 9. Clean duplicate in_progress attempts
	next("清理重复 in_progress 记录")
	cleanDuplicateInProgress(ctx, db)

	// 10. Indexes
	next("创建索引")
	for _, idx := range migrationIndexes {
		if _, err := db.ExecContext(ctx, idx); err != nil {
			rep.Warnings = append(rep.Warnings, "index: "+err.Error())
		}
	}
	// Migrate idx_attempts_one_active from (quiz_id, student_no) to (quiz_id, student_no, course_id)
	if err := migrateInProgressIndex(ctx, db); err != nil {
		rep.Warnings = append(rep.Warnings, "in_progress idx: "+err.Error())
	}
	ok("索引已创建/更新")

	// 11. Recompute attempt_no per (course_id, quiz_id, student_no)
	next("修复 attempt_no")
	if n, err := recomputeAttemptNo(ctx, db); err == nil && n > 0 {
		ok("重算 attempt_no 影响 %d 行", n)
	}

	// 12. Directory migration
	next("文件目录迁移 → metadata/")
	migrateFileLayout(ctx, db, opts, quizRenameMap, rep)

	// 12b. Apply quiz_id renames in DB (after file migration reads old IDs)
	if len(quizRenameMap) > 0 {
		next("重命名 quiz_id")
		n := applyQuizIDRenames(ctx, db, quizRenameMap)
		ok("重命名 quiz_id: %d 行", n)
		for old, nw := range quizRenameMap {
			rep.QuizIDsRenamed[old] = nw
		}
	}

	// 12c. Rename quiz submission directories to match new IDs
	if len(quizRenameMap) > 0 {
		next("重命名 quiz 提交目录")
		renameQuizSubmissionDirs(opts.MetadataDir, quizRenameMap, rep)
	}

	// 12d. Fix homework_submissions.course text column → slug
	next("修复 homework course 字段")
	for _, cs := range opts.Courses {
		cid := courseMap[cs.Slug]
		if cid > 0 {
			res, _ := db.ExecContext(ctx,
				`UPDATE homework_submissions SET course = ? WHERE course_id = ? AND course != ?`,
				cs.Slug, cid, cs.Slug)
			if res != nil {
				if n, _ := res.RowsAffected(); n > 0 {
					ok("homework course → %q: %d 行", cs.Slug, n)
				}
			}
		}
	}

	// 13. Normalize quiz_id (YAML internal id → directory name)
	next("统一 quiz_id 为文件名")
	normalizeQuizIDs(ctx, db, opts.MetadataDir, rep)

	// 14. Drop legacy global settings keys (replaced by course_state)
	next("清理 legacy 全局设置")
	res, _ := db.ExecContext(ctx,
		`DELETE FROM settings WHERE key IN ('quiz_yaml','quiz_source_path','entry_open')`)
	if res != nil {
		if n, _ := res.RowsAffected(); n > 0 {
			ok("删除 %d 个 legacy 全局设置", n)
		} else {
			ok("无需清理")
		}
	}

	// 15. Report
	rep.FinishedAt = time.Now()
	if !opts.DryRun {
		if err := writeReport(opts.ReportPath, rep); err != nil {
			log.Printf("写入 report 失败: %v", err)
		} else {
			fmt.Printf("\n迁移报告 → %s\n", opts.ReportPath)
		}
	}

	// Summary
	next("迁移完成 — 数据统计")
	var tCount, cCount, aCount, hCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM teachers`).Scan(&tCount)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM courses`).Scan(&cCount)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts`).Scan(&aCount)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM homework_submissions`).Scan(&hCount)
	fmt.Printf("  教师: %d   课程: %d   答题记录: %d   作业提交: %d\n", tCount, cCount, aCount, hCount)
	ok("可以安全启动新版本服务")
	return nil
}

// ─── steps ───

func detectSchema(ctx context.Context, db *sql.DB) string {
	cols := tableColumns(db, ctx, "attempts")
	if cols["course_id"] != "" {
		// see if teachers + courses exist
		if has(tableColumns(db, ctx, "teachers")) && has(tableColumns(db, ctx, "courses")) {
			return "dev_multi"
		}
	}
	if has(tableColumns(db, ctx, "attempts")) {
		return "main"
	}
	return "empty"
}

func has(m map[string]string) bool { return m != nil && len(m) > 0 }

func ensureCoreTables(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS teachers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'teacher',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS courses (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			teacher_id TEXT NOT NULL,
			name TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			internal_name TEXT NOT NULL DEFAULT '',
			slug TEXT NOT NULL DEFAULT '',
			invite_code TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS course_state (
			course_id INTEGER PRIMARY KEY,
			entry_open INTEGER NOT NULL DEFAULT 0,
			quiz_yaml TEXT,
			quiz_source_path TEXT
		)`,
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
		)`,
		`CREATE TABLE IF NOT EXISTS answers (
			attempt_id TEXT NOT NULL,
			question_id TEXT NOT NULL,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (attempt_id, question_id)
		)`,
		`CREATE TABLE IF NOT EXISTS summaries (
			attempt_id TEXT PRIMARY KEY,
			summary_json TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS admin_summaries (
			quiz_id TEXT NOT NULL,
			course_id INTEGER NOT NULL DEFAULT 0,
			summary_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(quiz_id)
		)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

var migrationIndexes = []string{
	`CREATE INDEX IF NOT EXISTS idx_attempts_quiz_status ON attempts(quiz_id, status)`,
	`CREATE INDEX IF NOT EXISTS idx_attempts_lookup ON attempts(quiz_id, student_no, status)`,
	`CREATE INDEX IF NOT EXISTS idx_answers_attempt ON answers(attempt_id)`,
	`CREATE INDEX IF NOT EXISTS idx_attempts_course ON attempts(course_id)`,
	`CREATE INDEX IF NOT EXISTS idx_courses_teacher ON courses(teacher_id)`,
	`CREATE INDEX IF NOT EXISTS idx_courses_invite ON courses(invite_code)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_courses_teacher_slug ON courses(teacher_id, slug) WHERE slug != ''`,
	`CREATE INDEX IF NOT EXISTS idx_homework_submissions_lookup ON homework_submissions(course_id, assignment_id, student_no)`,
	`CREATE INDEX IF NOT EXISTS idx_homework_submissions_legacy ON homework_submissions(course, assignment_id, student_no)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_admin_summaries_course_quiz ON admin_summaries(course_id, quiz_id)`,
}

func migrateInProgressIndex(ctx context.Context, db *sql.DB) error {
	var idxDef sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_attempts_one_active'`).Scan(&idxDef)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if idxDef.Valid && strings.Contains(strings.ToLower(idxDef.String), "course_id") {
		return nil
	}
	if _, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_attempts_one_active`); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_attempts_one_active
		 ON attempts(quiz_id, student_no, course_id) WHERE status = 'in_progress'`)
	return err
}

func ensureTeacher(ctx context.Context, db *sql.DB, opts *UpgradeOptions) error {
	var exists int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM teachers WHERE id = ?`, opts.TeacherID).Scan(&exists)
	if exists > 0 {
		// Optionally update role to admin if not already.
		_, _ = db.ExecContext(ctx,
			`UPDATE teachers SET role='admin', updated_at=? WHERE id=? AND role != 'admin'`,
			time.Now().Format(time.RFC3339Nano), opts.TeacherID)
		ok("教师 %s 已存在，跳过创建", opts.TeacherID)
		return nil
	}
	if opts.SkipTeacherCreate {
		return fmt.Errorf("teacher %s not found and --skip-teacher-create was set", opts.TeacherID)
	}
	pwd := strings.TrimSpace(opts.Password)
	generated := false
	if pwd == "" {
		pwd = randomPassword(12)
		generated = true
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO teachers(id,name,password_hash,role,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
		opts.TeacherID, opts.TeacherName, string(hash), "admin", now, now); err != nil {
		return err
	}
	if generated {
		fmt.Printf("⚠ 已生成初始密码（只显示一次）: %s\n", pwd)
	}
	ok("创建教师 id=%s 姓名=%s role=admin", opts.TeacherID, opts.TeacherName)
	return nil
}

func ensureDefaultCourse(ctx context.Context, db *sql.DB, teacherID, slug, name string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM courses WHERE teacher_id = ? AND slug = ? LIMIT 1`,
		teacherID, slug).Scan(&id)
	if err == nil && id > 0 {
		return id, nil
	}
	code := randomInviteCode()
	now := time.Now().Format(time.RFC3339Nano)
	res, err := db.ExecContext(ctx,
		`INSERT INTO courses(teacher_id,name,display_name,internal_name,slug,invite_code,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		teacherID, name, slug, slug, slug, code, now, now)
	if err != nil {
		return 0, err
	}
	id, _ = res.LastInsertId()
	return id, nil
}

// expandQuizAbbrev expands "w" prefix abbreviation: w5_l1 → week5_l1.
func expandQuizAbbrev(id string) string {
	if len(id) >= 2 && id[0] == 'w' && id[1] >= '0' && id[1] <= '9' {
		return "week" + id[1:]
	}
	return id
}

// buildQuizRenameMap scans distinct quiz_ids in attempts and builds
// old→new rename mappings based on prefix stripping and abbreviation expansion.
func buildQuizRenameMap(ctx context.Context, db *sql.DB, courses []CourseSpec) map[string]string {
	// Collect all prefixes that should be stripped (including optim_ for
	// the default optimization course -- it has no Prefix field but its
	// quiz_ids still carry the optim_ prefix).
	stripPrefixes := []string{}
	for _, c := range courses {
		if c.Prefix != "" {
			stripPrefixes = append(stripPrefixes, c.Prefix)
		}
	}
	// Always strip "optim_" as a known legacy prefix.
	hasOptim := false
	for _, p := range stripPrefixes {
		if p == "optim_" {
			hasOptim = true
			break
		}
	}
	if !hasOptim {
		stripPrefixes = append(stripPrefixes, "optim_")
	}

	rows, err := db.QueryContext(ctx, `SELECT DISTINCT quiz_id FROM attempts WHERE quiz_id != ''
		UNION SELECT DISTINCT quiz_id FROM admin_summaries WHERE quiz_id != ''`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	rename := map[string]string{}
	for rows.Next() {
		var qid string
		if err := rows.Scan(&qid); err != nil {
			continue
		}
		newID := qid
		for _, p := range stripPrefixes {
			if strings.HasPrefix(qid, p) {
				newID = strings.TrimPrefix(qid, p)
				break
			}
		}
		newID = expandQuizAbbrev(newID)
		if newID != qid {
			rename[qid] = newID
		}
	}
	return rename
}

// backfillCourseIDsByPrefix assigns attempts and admin_summaries to the
// correct course based on quiz_id prefix matching. Unmatched records go
// to the first (default) course.
func backfillCourseIDsByPrefix(ctx context.Context, db *sql.DB, courses []CourseSpec, courseMap map[string]int64) (int64, error) {
	var total int64
	for _, c := range courses {
		if c.Prefix == "" {
			continue
		}
		cid := courseMap[c.Slug]
		if cid == 0 {
			continue
		}
		res, err := db.ExecContext(ctx,
			`UPDATE attempts SET course_id = ? WHERE (course_id = 0 OR course_id IS NULL) AND quiz_id LIKE ?`,
			cid, c.Prefix+"%")
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n > 0 {
			ok("  quiz_id '%s%%' → course %s (id=%d): %d 行", c.Prefix, c.Slug, cid, n)
		}
		db.ExecContext(ctx,
			`UPDATE admin_summaries SET course_id = ? WHERE (course_id = 0 OR course_id IS NULL) AND quiz_id LIKE ?`,
			cid, c.Prefix+"%")
	}
	// Remaining → default (first) course
	if len(courses) > 0 {
		defaultID := courseMap[courses[0].Slug]
		if defaultID > 0 {
			res, err := db.ExecContext(ctx,
				`UPDATE attempts SET course_id = ? WHERE course_id = 0 OR course_id IS NULL`, defaultID)
			if err != nil {
				return total, err
			}
			n, _ := res.RowsAffected()
			total += n
			if n > 0 {
				ok("  其余 → course %s (id=%d): %d 行", courses[0].Slug, defaultID, n)
			}
			db.ExecContext(ctx,
				`UPDATE admin_summaries SET course_id = ? WHERE course_id = 0 OR course_id IS NULL`, defaultID)
		}
	}
	return total, nil
}

// applyQuizIDRenames updates quiz_id values in attempts and admin_summaries.
// Must be called AFTER file migration (which reads old quiz_ids from dirs).
func applyQuizIDRenames(ctx context.Context, db *sql.DB, renameMap map[string]string) int64 {
	var total int64
	for oldID, newID := range renameMap {
		res, _ := db.ExecContext(ctx,
			`UPDATE attempts SET quiz_id = ? WHERE quiz_id = ?`, newID, oldID)
		if res != nil {
			n, _ := res.RowsAffected()
			total += n
		}
		db.ExecContext(ctx,
			`UPDATE admin_summaries SET quiz_id = ? WHERE quiz_id = ?`, newID, oldID)
	}
	return total
}

// renameQuizSubmissionDirs renames quiz directories under metadata/ to match
// new quiz_ids from the rename map.
func renameQuizSubmissionDirs(metadataDir string, renameMap map[string]string, rep *Report) {
	teachers, err := os.ReadDir(metadataDir)
	if err != nil {
		return
	}
	for _, t := range teachers {
		if !t.IsDir() {
			continue
		}
		slugs, _ := os.ReadDir(filepath.Join(metadataDir, t.Name()))
		for _, s := range slugs {
			if !s.IsDir() {
				continue
			}
			quizRoot := filepath.Join(metadataDir, t.Name(), s.Name(), "quiz")
			dirs, err := os.ReadDir(quizRoot)
			if err != nil {
				continue
			}
			for _, d := range dirs {
				if !d.IsDir() {
					continue
				}
				newName, found := renameMap[d.Name()]
				if !found || newName == d.Name() {
					continue
				}
				oldPath := filepath.Join(quizRoot, d.Name())
				newPath := filepath.Join(quizRoot, newName)
				if _, err := os.Stat(newPath); err == nil {
					// Target already exists -- merge by copying contents
					entries, _ := os.ReadDir(oldPath)
					for _, e := range entries {
						src := filepath.Join(oldPath, e.Name())
						dst := filepath.Join(newPath, e.Name())
						if e.IsDir() {
							copyDirRecursive(src, dst)
						} else {
							copyFile(src, dst)
						}
					}
					os.RemoveAll(oldPath)
					ok("合并目录 %s/%s/quiz/%s → %s", t.Name(), s.Name(), d.Name(), newName)
				} else {
					if err := os.Rename(oldPath, newPath); err == nil {
						ok("重命名目录 %s/%s/quiz/%s → %s", t.Name(), s.Name(), d.Name(), newName)
					}
				}
			}
		}
	}
}

func backfillCourseIDs(ctx context.Context, db *sql.DB, courseID int64) (int64, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE attempts SET course_id = ? WHERE course_id = 0 OR course_id IS NULL`, courseID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func backfillHomeworkCourseIDs(ctx context.Context, db *sql.DB, defaultCourseID int64) (int64, error) {
	// First try to match via courses.slug:
	// UPDATE homework_submissions SET course_id = (SELECT c.id FROM courses c WHERE c.slug = homework_submissions.course LIMIT 1)
	var totalN int64
	res, err := db.ExecContext(ctx, `
		UPDATE homework_submissions
		SET course_id = (SELECT c.id FROM courses c WHERE c.slug = homework_submissions.course LIMIT 1)
		WHERE course_id = 0 AND EXISTS (SELECT 1 FROM courses c WHERE c.slug = homework_submissions.course)`)
	if err == nil {
		n, _ := res.RowsAffected()
		totalN += n
	}
	// Fallback: assign remaining to default course
	res, err = db.ExecContext(ctx,
		`UPDATE homework_submissions SET course_id = ? WHERE course_id = 0 OR course_id IS NULL`,
		defaultCourseID)
	if err != nil {
		return totalN, err
	}
	n, _ := res.RowsAffected()
	totalN += n
	return totalN, nil
}

// recomputeAttemptNo recomputes attempt_no so that for every
// (course_id, quiz_id, student_no) submitted attempts are numbered 1..N by
// submitted_at (falling back to created_at). In-progress attempts keep
// attempt_no = 0. Idempotent.
func recomputeAttemptNo(ctx context.Context, db *sql.DB) (int64, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, course_id, quiz_id, student_no
		FROM attempts
		WHERE status = 'submitted'
		ORDER BY course_id, quiz_id, student_no, COALESCE(submitted_at, created_at), created_at`)
	if err != nil {
		return 0, err
	}
	type key struct {
		course    int
		quizID    string
		studentNo string
	}
	counters := map[key]int{}
	type patch struct {
		id string
		no int
	}
	var patches []patch
	for rows.Next() {
		var id string
		var course int
		var quizID, studentNo string
		if err := rows.Scan(&id, &course, &quizID, &studentNo); err != nil {
			rows.Close()
			return 0, err
		}
		k := key{course, quizID, studentNo}
		counters[k]++
		patches = append(patches, patch{id, counters[k]})
	}
	rows.Close()
	if len(patches) == 0 {
		return 0, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, `UPDATE attempts SET attempt_no = ? WHERE id = ? AND attempt_no != ?`)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()
	var total int64
	for _, p := range patches {
		res, err := stmt.ExecContext(ctx, p.no, p.id, p.no)
		if err != nil {
			tx.Rollback()
			return 0, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, tx.Commit()
}

func cleanDuplicateInProgress(ctx context.Context, db *sql.DB) {
	rows, err := db.QueryContext(ctx, `
		SELECT quiz_id, student_no, course_id, COUNT(*) AS cnt
		FROM attempts WHERE status = 'in_progress'
		GROUP BY quiz_id, student_no, course_id HAVING cnt > 1`)
	if err != nil {
		warn("查询重复失败: %v", err)
		return
	}
	type dup struct {
		quizID, studentNo string
		courseID          int
	}
	var dups []dup
	for rows.Next() {
		var d dup
		var cnt int
		if err := rows.Scan(&d.quizID, &d.studentNo, &d.courseID, &cnt); err != nil {
			continue
		}
		dups = append(dups, d)
	}
	rows.Close()
	if len(dups) == 0 {
		ok("无重复 in_progress 记录")
		return
	}
	now := time.Now().Format(time.RFC3339Nano)
	total := 0
	for _, d := range dups {
		var keepID string
		if err := db.QueryRowContext(ctx,
			`SELECT id FROM attempts WHERE quiz_id=? AND student_no=? AND course_id=? AND status='in_progress' ORDER BY created_at DESC LIMIT 1`,
			d.quizID, d.studentNo, d.courseID).Scan(&keepID); err != nil {
			continue
		}
		res, err := db.ExecContext(ctx,
			`UPDATE attempts SET status='submitted', attempt_no=0, updated_at=?, submitted_at=?
			 WHERE quiz_id=? AND student_no=? AND course_id=? AND status='in_progress' AND id != ?`,
			now, now, d.quizID, d.studentNo, d.courseID, keepID)
		if err != nil {
			continue
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	if total > 0 {
		ok("清理 %d 条重复 in_progress 记录", total)
	}
}

func migrateCourseIDsToInt(ctx context.Context, db *sql.DB, adminTeacherID string) error {
	cols := tableColumns(db, ctx, "courses")
	if cols == nil || cols["id"] == "" {
		return nil
	}
	if !strings.EqualFold(cols["id"], "TEXT") {
		ok("courses.id 已是 INTEGER，跳过")
		return nil
	}
	fmt.Println("  courses.id 为 TEXT，迁移到 INTEGER...")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, _ = tx.ExecContext(ctx,
		`UPDATE courses SET teacher_id = ? WHERE teacher_id NOT IN (SELECT id FROM teachers)`,
		adminTeacherID)

	if _, err := tx.ExecContext(ctx, `CREATE TABLE courses_new (
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
		return err
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT id, teacher_id, name, COALESCE(slug,''), COALESCE(invite_code,''),
		        created_at, COALESCE(updated_at, created_at) FROM courses`)
	if err != nil {
		return err
	}
	type idPair struct {
		old string
		new int64
	}
	var pairs []idPair
	for rows.Next() {
		var oldID sql.NullString
		var teacher, name, slug, invCode, createdAt, updatedAt string
		if err := rows.Scan(&oldID, &teacher, &name, &slug, &invCode, &createdAt, &updatedAt); err != nil {
			rows.Close()
			return err
		}
		if invCode == "" {
			invCode = randomInviteCode()
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO courses_new(teacher_id,name,display_name,internal_name,slug,invite_code,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			teacher, name, slug, slug, slug, invCode, createdAt, updatedAt)
		if err != nil {
			rows.Close()
			return err
		}
		newID, _ := res.LastInsertId()
		if oldID.Valid && oldID.String != "" {
			pairs = append(pairs, idPair{oldID.String, newID})
		}
	}
	rows.Close()
	for _, p := range pairs {
		tx.ExecContext(ctx, `UPDATE course_state SET course_id = ? WHERE CAST(course_id AS TEXT) = ?`, p.new, p.old)
		tx.ExecContext(ctx, `UPDATE attempts SET course_id = ? WHERE CAST(course_id AS TEXT) = ?`, p.new, p.old)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE courses`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE courses_new RENAME TO courses`); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureHomeworkSchema(ctx context.Context, db *sql.DB) error {
	cols := tableColumns(db, ctx, "homework_submissions")
	if len(cols) == 0 {
		_, err := db.ExecContext(ctx, `CREATE TABLE homework_submissions (
			id TEXT PRIMARY KEY,
			session_token TEXT UNIQUE NOT NULL,
			course TEXT NOT NULL DEFAULT '',
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
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`)
		return err
	}
	required := []string{"secret_key", "extra_original_name", "extra_uploaded_at"}
	legacy := cols["quiz_id"] != "" || cols["task_id"] != "" || cols["status"] != "" || cols["finalized_at"] != ""
	missing := false
	for _, c := range required {
		if cols[c] == "" {
			missing = true
			break
		}
	}
	if !legacy && !missing {
		return nil
	}
	fmt.Println("  homework_submissions 需要重建...")
	tx, err := db.BeginTx(ctx, nil)
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
		course TEXT NOT NULL DEFAULT '',
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
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	// INSERT only the columns that exist in both.
	newCols := []string{
		"id", "session_token", "course", "assignment_id", "name",
		"student_no", "class_name", "created_at", "updated_at",
		"report_original_name", "report_uploaded_at",
		"code_original_name", "code_uploaded_at",
	}
	var common []string
	for _, c := range newCols {
		if cols[c] != "" {
			common = append(common, c)
		}
	}
	if len(common) > 0 {
		colList := strings.Join(common, ",")
		_, _ = tx.ExecContext(ctx, fmt.Sprintf(
			`INSERT OR IGNORE INTO homework_submissions(%s) SELECT %s FROM homework_submissions_old`,
			colList, colList))
	}
	_, _ = tx.ExecContext(ctx, `DROP TABLE IF EXISTS homework_submissions_old`)
	return tx.Commit()
}

// ─── helpers (shared with file migration) ───

func backupDB(path string) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	ts := time.Now().Format("20060102-150405")
	dst := path + ".prelegacy-" + ts + ".bak"
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, src); err != nil {
		os.Remove(dst)
		return "", err
	}
	return dst, nil
}

func writeReport(path string, rep *Report) error {
	if path == "" {
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

func randomPassword(n int) string {
	buf := make([]byte, n)
	if _, err := crand.Read(buf); err != nil {
		return "ChangeMe123!"
	}
	// Map bytes to a URL-friendly set.
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out)
}

func randomInviteCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, 6)
	_, _ = crand.Read(buf)
	for i := range buf {
		buf[i] = chars[int(buf[i])%len(chars)]
	}
	return string(buf)
}

// randHex returns a random hex string of the given length.
// Currently reserved for future secret generation; kept here to avoid
// dependency ordering when other helpers need it.
func randHex(n int) string {
	buf := make([]byte, n)
	_, _ = crand.Read(buf)
	return hex.EncodeToString(buf)
}

// ─── sqlite helpers ───

func tableColumns(db *sql.DB, ctx context.Context, table string) map[string]string {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return nil
	}
	defer rows.Close()
	cols := map[string]string{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			continue
		}
		cols[name] = strings.ToUpper(typ)
	}
	return cols
}

func ensureColumn(db *sql.DB, ctx context.Context, table, col, typedef string) {
	cols := tableColumns(db, ctx, table)
	if cols[col] != "" {
		return
	}
	ddl := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, col, typedef)
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		warn("添加列 %s.%s 失败: %v", table, col, err)
	} else {
		ok("添加列 %s.%s", table, col)
	}
}

func ok(msg string, args ...any)   { fmt.Printf("✓ "+msg+"\n", args...) }
func warn(msg string, args ...any) { fmt.Printf("⚠ "+msg+"\n", args...) }
