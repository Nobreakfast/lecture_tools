// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

// migrate — upgrade a legacy (main-branch) database and file layout to the
// current multi-teacher schema. Safe to re-run; every step is idempotent
// (copies only, never deletes source).
//
// Primary usage:
//
//	go run ./cmd/migrate upgrade \
//	    --db ./data/app.db \
//	    --teacher-id SC25132 --teacher-name 赵浩诚 \
//	    --password 'xxxxxx' \
//	    --course 'optimization:最优化方法' \
//	    --course 'robot:机器人控制技术:robot_'
//
// Flags:
//
//	--db              path to sqlite db (default ./data/app.db)
//	--metadata-dir    target metadata root (default ./metadata)
//	--data-dir        legacy data dir (default ./data)
//	--ppt-dir         legacy ppt dir (default ./ppt)
//	--quiz-assets-dir legacy quiz assets dir (default ./quiz/assets)
//	--teacher-id      default teacher id for legacy data binding (required on first run)
//	--teacher-name    default teacher display name
//	--password        default teacher password (random if omitted; printed once)
//	--course          repeatable; format "slug:name[:prefix]" (first is default)
//	--course-slug     (deprecated) default course slug
//	--course-name     (deprecated) default course display name
//	--no-backup       skip db backup (not recommended)
//	--dry-run         print actions without modifying anything
//	--report          report file path (default ./data/migration_report.json)
//
// Legacy positional form (still accepted, deprecated):
//
//	go run ./cmd/migrate [db-path]
//	go run ./cmd/migrate ./data/app.db admin <id> <name> <password>
//	go run ./cmd/migrate ./data/app.db metadata [metadata-dir] [data-dir] [ppt-dir]
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "upgrade" {
		runUpgrade(os.Args[2:])
		return
	}
	// Legacy positional mode
	runLegacy(os.Args[1:])
}

// openDB opens the sqlite database with sane pragmas.
func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	ctx := context.Background()
	for _, p := range []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA busy_timeout = 5000`,
	} {
		if _, err := db.ExecContext(ctx, p); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

// multiFlag collects repeated --course flag values.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// parseCourseSpec parses "slug:name[:prefix]" into a CourseSpec.
func parseCourseSpec(s string) (CourseSpec, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return CourseSpec{}, fmt.Errorf("格式错误: %q（应为 slug:name[:prefix]）", s)
	}
	cs := CourseSpec{Slug: parts[0], Name: parts[1]}
	if len(parts) == 3 {
		cs.Prefix = parts[2]
	}
	return cs, nil
}

func runUpgrade(args []string) {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	var courseFlags multiFlag
	var (
		dbPath        = fs.String("db", "./data/app.db", "path to sqlite db")
		metadataDir   = fs.String("metadata-dir", "./metadata", "target metadata root")
		dataDir       = fs.String("data-dir", "./data", "legacy data dir (for materials/quiz/homework fallback)")
		pptDir        = fs.String("ppt-dir", "./ppt", "legacy ppt dir")
		quizAssetsDir = fs.String("quiz-assets-dir", "./quiz/assets", "legacy quiz assets dir")
		teacherID     = fs.String("teacher-id", "", "default teacher id for legacy data (required)")
		teacherName   = fs.String("teacher-name", "", "default teacher display name")
		password      = fs.String("password", "", "default teacher password (random if omitted)")
		courseSlug    = fs.String("course-slug", "", "deprecated: use --course")
		courseName    = fs.String("course-name", "", "deprecated: use --course")
		noBackup      = fs.Bool("no-backup", false, "skip db backup (not recommended)")
		dryRun        = fs.Bool("dry-run", false, "print actions without modifying anything")
		reportPath    = fs.String("report", "./data/migration_report.json", "path for migration report JSON")
	)
	fs.Var(&courseFlags, "course", `repeatable; format "slug:name[:prefix]" (first is default)`)
	fs.Parse(args)

	if strings.TrimSpace(*teacherID) == "" {
		log.Fatal("缺少 --teacher-id（用于把旧数据绑定到默认教师/课程）")
	}
	if strings.TrimSpace(*teacherName) == "" {
		*teacherName = *teacherID
	}

	// Build Courses list from --course flags; fall back to legacy --course-slug/--course-name.
	var courses []CourseSpec
	for _, raw := range courseFlags {
		cs, err := parseCourseSpec(raw)
		if err != nil {
			log.Fatalf("--course 参数错误: %v", err)
		}
		courses = append(courses, cs)
	}
	if len(courses) == 0 {
		slug := *courseSlug
		if slug == "" {
			slug = "default"
		}
		name := *courseName
		if name == "" {
			name = "随堂测验"
		}
		courses = []CourseSpec{{Slug: slug, Name: name}}
	}

	if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
		fmt.Printf("数据库不存在，将新建: %s\n", *dbPath)
	}

	opts := UpgradeOptions{
		DBPath:        *dbPath,
		MetadataDir:   *metadataDir,
		DataDir:       *dataDir,
		PPTDir:        *pptDir,
		QuizAssetsDir: *quizAssetsDir,
		TeacherID:     *teacherID,
		TeacherName:   *teacherName,
		Password:      *password,
		Courses:       courses,
		CourseSlug:    courses[0].Slug,
		CourseName:    courses[0].Name,
		NoBackup:      *noBackup,
		DryRun:        *dryRun,
		ReportPath:    *reportPath,
	}
	if err := Upgrade(context.Background(), opts); err != nil {
		log.Fatalf("升级失败: %v", err)
	}
}

// runLegacy mirrors the pre-upgrade positional CLI for backward compatibility.
// It forwards to the same Upgrade routine using sensible defaults.
func runLegacy(args []string) {
	dbPath := "./data/app.db"
	if len(args) > 0 {
		dbPath = args[0]
	}
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Printf("数据库文件不存在: %s\n", dbPath)
		fmt.Println("如果是新部署，直接启动服务即可，无需迁移。")
		return
	}

	opts := UpgradeOptions{
		DBPath:        dbPath,
		MetadataDir:   "./metadata",
		DataDir:       "./data",
		PPTDir:        "./ppt",
		QuizAssetsDir: "./quiz/assets",
		CourseSlug:    "default",
		CourseName:    "随堂测验",
		Courses:       []CourseSpec{{Slug: "default", Name: "随堂测验"}},
	}

	// Parse legacy: <db> admin <id> <name> <password>
	if len(args) >= 2 && args[1] == "admin" {
		if len(args) >= 3 {
			opts.TeacherID = args[2]
		}
		if len(args) >= 4 {
			opts.TeacherName = args[3]
		}
		if len(args) >= 5 {
			opts.Password = args[4]
		}
	}
	// Parse legacy: <db> metadata [metadata] [data] [ppt]
	if len(args) >= 2 && args[1] == "metadata" {
		if len(args) >= 3 {
			opts.MetadataDir = args[2]
		}
		if len(args) >= 4 {
			opts.DataDir = args[3]
		}
		if len(args) >= 5 {
			opts.PPTDir = args[4]
		}
	}
	// No admin args: try to reuse existing admin teacher; if none, fall back
	// to legacy defaults (SC25132/赵浩诚/admin123) to stay compatible with
	// the previous bootstrap behavior.
	if opts.TeacherID == "" {
		id, name, ok := findExistingAdmin(dbPath)
		if ok {
			opts.TeacherID = id
			opts.TeacherName = name
			opts.SkipTeacherCreate = true
		} else {
			opts.TeacherID = "SC25132"
			opts.TeacherName = "赵浩诚"
			opts.Password = "admin123"
			fmt.Println("提示: 未指定教师信息，使用旧版默认 SC25132/admin123；建议改用 `upgrade` 子命令并传入真实密码。")
		}
	}

	if err := Upgrade(context.Background(), opts); err != nil {
		log.Fatalf("迁移失败: %v", err)
	}
}

func findExistingAdmin(dbPath string) (id, name string, ok bool) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return
	}
	defer db.Close()
	row := db.QueryRow(`SELECT id, name FROM teachers WHERE role='admin' ORDER BY created_at LIMIT 1`)
	if err := row.Scan(&id, &name); err != nil {
		return "", "", false
	}
	return id, name, true
}
