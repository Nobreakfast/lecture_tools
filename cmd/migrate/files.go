// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// migrateFileLayout copies legacy file layout (./data, ./ppt, ./quiz) into
// the new metadata layout:
//
//	metadata/{teacher_id}/{course_slug}/
//	  materials/…                                  (was ppt/{course})
//	  assignment/{aid}/…                           (was ppt/_homework/{course}/{aid})
//	  assignment/{aid}/submissions/{studentNo}/…   (was data/homework/{course}/{aid}/{studentNo})
//	  quiz/{stem}/…                                (was quiz/{course}/*.yaml + quiz/assets)
//	  quiz/{stem}/submissions/{studentNo}/…        (was data/quiz/{class}/{quizID}/{name_studentNo}/)
//
// Source directories are never deleted.
func migrateFileLayout(ctx context.Context, db *sql.DB, opts UpgradeOptions, quizRenameMap map[string]string, rep *Report) {
	type courseInfo struct {
		ID        int
		TeacherID string
		Name      string
		Slug      string
	}
	rows, err := db.QueryContext(ctx, `SELECT id, teacher_id, name, slug FROM courses`)
	if err != nil {
		rep.Warnings = append(rep.Warnings, "read courses: "+err.Error())
		return
	}
	var courses []courseInfo
	for rows.Next() {
		var c courseInfo
		if err := rows.Scan(&c.ID, &c.TeacherID, &c.Name, &c.Slug); err != nil {
			continue
		}
		courses = append(courses, c)
	}
	rows.Close()
	if len(courses) == 0 {
		return
	}

	_ = os.MkdirAll(opts.MetadataDir, 0o755)
	_ = os.MkdirAll(filepath.Join(opts.DataDir, "assets"), 0o755)

	quizYAMLRoot := filepath.Dir(opts.QuizAssetsDir) // e.g. ./quiz
	quizIDToStem := scanQuizIDMapping(quizYAMLRoot, rep)
	// Merge the quiz rename map so file destinations use the new names.
	for old, nw := range quizRenameMap {
		if _, exists := quizIDToStem[old]; !exists {
			quizIDToStem[old] = nw
		}
	}
	resolveStem := func(id string) string {
		if s, ok := quizIDToStem[id]; ok {
			return s
		}
		return id
	}

	for _, c := range courses {
		slug := safePart(c.Slug)
		teacher := safePart(c.TeacherID)
		if slug == "" || teacher == "" {
			continue
		}
		courseRoot := filepath.Join(opts.MetadataDir, teacher, slug)

		// 1. Materials: ppt/{name|slug}/* → materials/
		for _, folder := range []string{c.Name, c.Slug} {
			src := filepath.Join(opts.PPTDir, folder)
			if n := copyDirFlat(src, filepath.Join(courseRoot, "materials")); n > 0 {
				rep.FilesCopied["materials"] += n
				ok("课程 %s: 迁移 %d 个材料 (from %s)", slug, n, folder)
			}
		}

		// 2. Assignments: ppt/_homework/{name|slug}/{aid}/* → assignment/{aid}/
		for _, folder := range []string{c.Name, c.Slug} {
			src := filepath.Join(opts.PPTDir, "_homework", folder)
			if aids, err := os.ReadDir(src); err == nil {
				for _, aid := range aids {
					if aid.IsDir() {
						srcAid := filepath.Join(src, aid.Name())
						dstAid := filepath.Join(courseRoot, "assignment", aid.Name())
						if n := copyDirRecursive(srcAid, dstAid); n > 0 {
							rep.FilesCopied["assignments"] += n
							ok("作业 %s/%s: %d 个文件", slug, aid.Name(), n)
						}
					}
				}
				for _, f := range aids {
					if !f.IsDir() && strings.HasSuffix(strings.ToLower(f.Name()), ".pdf") {
						aid := strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))
						dst := filepath.Join(courseRoot, "assignment", aid, f.Name())
						if err := copyFile(filepath.Join(src, f.Name()), dst); err == nil {
							rep.FilesCopied["assignments"]++
						}
					}
				}
			}
		}

		// 3. Quiz YAML: quiz/{name|slug}/*.yaml → quiz/{stem}/*.yaml
		for _, folder := range []string{c.Name, c.Slug} {
			src := filepath.Join(quizYAMLRoot, folder)
			yamls, err := os.ReadDir(src)
			if err != nil {
				continue
			}
			for _, f := range yamls {
				if f.IsDir() {
					continue
				}
				lower := strings.ToLower(f.Name())
				if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
					continue
				}
				stem := strings.TrimSuffix(strings.TrimSuffix(f.Name(), ".yaml"), ".yml")
				dst := filepath.Join(courseRoot, "quiz", stem, f.Name())
				if err := copyFile(filepath.Join(src, f.Name()), dst); err == nil {
					rep.FilesCopied["quiz_yaml"]++
				}
			}
		}

		// 4. Student homework submissions:
		//    data/homework/{slug|name}/{aid}/{studentNo}/[attempt/…]
		for _, folder := range []string{c.Slug, c.Name} {
			src := filepath.Join(opts.DataDir, "homework", folder)
			migrateHomeworkSubmissions(src, courseRoot, rep)
		}
	}

	// 5. Global quiz assets → data/assets for legacy asset resolution
	if n := copyDirFlat(opts.QuizAssetsDir, filepath.Join(opts.DataDir, "assets")); n > 0 {
		rep.FilesCopied["quiz_assets"] += n
	}

	// 6. Student answer images: data/quiz/{class}/{quizID}/{name_studentNo}/*
	migrateStudentAnswerImages(ctx, db, opts, resolveStem, rep)

	// 7. Fix answer URLs in DB
	migrateAnswerURLs(ctx, db, resolveStem, rep)

	// 8. homework_submissions.course (name → slug)
	for _, c := range courses {
		if c.Name != c.Slug {
			if res, err := db.ExecContext(ctx,
				`UPDATE homework_submissions SET course = ? WHERE course = ?`, c.Slug, c.Name); err == nil {
				if n, _ := res.RowsAffected(); n > 0 {
					ok("homework_submissions: course %q → %q (%d 行)", c.Name, c.Slug, n)
				}
			}
		}
	}

	// 9. course_state.quiz_source_path → metadata path
	for _, c := range courses {
		slug := safePart(c.Slug)
		teacher := safePart(c.TeacherID)
		var p sql.NullString
		if err := db.QueryRowContext(ctx,
			`SELECT quiz_source_path FROM course_state WHERE course_id = ?`, c.ID).Scan(&p); err != nil {
			continue
		}
		if !p.Valid || p.String == "" || strings.Contains(p.String, opts.MetadataDir) {
			continue
		}
		base := filepath.Base(p.String)
		stem := strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml")
		newPath := filepath.Join(opts.MetadataDir, teacher, slug, "quiz", stem, base)
		if _, err := os.Stat(newPath); err != nil {
			continue
		}
		db.ExecContext(ctx, `UPDATE course_state SET quiz_source_path = ? WHERE course_id = ?`, newPath, c.ID)
	}
}

func migrateHomeworkSubmissions(src, courseRoot string, rep *Report) {
	aids, err := os.ReadDir(src)
	if err != nil {
		return
	}
	for _, aid := range aids {
		if !aid.IsDir() {
			continue
		}
		aidPath := filepath.Join(src, aid.Name())
		students, _ := os.ReadDir(aidPath)
		for _, stu := range students {
			if !stu.IsDir() {
				continue
			}
			stuPath := filepath.Join(aidPath, stu.Name())
			entries, _ := os.ReadDir(stuPath)
			hasSubDir := false
			for _, e := range entries {
				if e.IsDir() {
					hasSubDir = true
					break
				}
			}
			dstSub := filepath.Join(courseRoot, "assignment", aid.Name(), "submissions", stu.Name())
			if hasSubDir {
				for _, sub := range entries {
					if !sub.IsDir() {
						continue
					}
					if n := copyDirFlat(filepath.Join(stuPath, sub.Name()), dstSub); n > 0 {
						rep.FilesCopied["homework_submissions"] += n
					}
				}
			} else {
				if n := copyDirFlat(stuPath, dstSub); n > 0 {
					rep.FilesCopied["homework_submissions"] += n
				}
			}
		}
	}
}

func migrateStudentAnswerImages(ctx context.Context, db *sql.DB, opts UpgradeOptions,
	resolveStem func(string) string, rep *Report) {

	root := filepath.Join(opts.DataDir, "quiz")
	classes, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, cls := range classes {
		if !cls.IsDir() {
			continue
		}
		className := cls.Name()
		quizDirs, _ := os.ReadDir(filepath.Join(root, className))
		for _, qd := range quizDirs {
			if !qd.IsDir() {
				continue
			}
			quizID := qd.Name()
			var courseID int
			err := db.QueryRowContext(ctx,
				`SELECT course_id FROM attempts WHERE class_name = ? AND quiz_id = ? AND course_id > 0 LIMIT 1`,
				className, quizID).Scan(&courseID)
			if err != nil || courseID == 0 {
				// fall back to default course if we have any
				_ = db.QueryRowContext(ctx,
					`SELECT course_id FROM attempts WHERE quiz_id = ? AND course_id > 0 LIMIT 1`,
					quizID).Scan(&courseID)
			}
			if courseID == 0 {
				continue
			}
			var teacher, slug string
			if err := db.QueryRowContext(ctx,
				`SELECT t.id, c.slug FROM courses c JOIN teachers t ON c.teacher_id = t.id WHERE c.id = ?`,
				courseID).Scan(&teacher, &slug); err != nil {
				continue
			}
			stem := resolveStem(quizID)
			students, _ := os.ReadDir(filepath.Join(root, className, quizID))
			for _, stu := range students {
				if !stu.IsDir() {
					continue
				}
				parts := strings.Split(stu.Name(), "_")
				studentNo := parts[len(parts)-1]
				srcDir := filepath.Join(root, className, quizID, stu.Name())
				dstDir := filepath.Join(opts.MetadataDir, safePart(teacher), safePart(slug),
					"quiz", safePart(stem), "submissions", safePart(studentNo))
				if n := flatCopyAllFiles(srcDir, dstDir); n > 0 {
					rep.FilesCopied["answer_images"] += n
				}
			}
		}
	}
}

func migrateAnswerURLs(ctx context.Context, db *sql.DB, resolveStem func(string) string, rep *Report) {
	rows, err := db.QueryContext(ctx,
		`SELECT a.attempt_id, a.question_id, a.value, at.class_name, at.quiz_id, at.student_no, at.course_id
		 FROM answers a JOIN attempts at ON a.attempt_id = at.id
		 WHERE a.value LIKE '%/uploads/%'`)
	if err != nil {
		return
	}
	type row struct {
		attemptID, questionID, value             string
		className, quizID, studentNo             string
		courseID                                 int
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.attemptID, &r.questionID, &r.value, &r.className, &r.quizID, &r.studentNo, &r.courseID); err != nil {
			continue
		}
		todo = append(todo, r)
	}
	rows.Close()
	for _, r := range todo {
		if r.courseID <= 0 {
			continue
		}
		var teacher, slug string
		if err := db.QueryRowContext(ctx,
			`SELECT t.id, c.slug FROM courses c JOIN teachers t ON c.teacher_id = t.id WHERE c.id = ?`,
			r.courseID).Scan(&teacher, &slug); err != nil {
			continue
		}
		stem := resolveStem(r.quizID)
		newBase := fmt.Sprintf("/uploads/%s/%s/quiz/%s/submissions/%s/", teacher, slug, stem, r.studentNo)
		if strings.Contains(r.value, newBase) {
			continue
		}
		newValue := r.value
		for {
			idx := strings.Index(newValue, "/uploads/")
			if idx < 0 {
				break
			}
			rest := newValue[idx:]
			end := len(rest)
			for _, ch := range []string{`"`, `'`, ` `, `]`, `}`} {
				if i := strings.Index(rest, ch); i > 0 && i < end {
					end = i
				}
			}
			oldURL := rest[:end]
			filename := filepath.Base(oldURL)
			lower := strings.ToLower(filename)
			if !strings.HasSuffix(lower, ".jpg") && !strings.HasSuffix(lower, ".jpeg") && !strings.HasSuffix(lower, ".png") {
				break
			}
			newURL := newBase + filename
			if oldURL == newURL {
				break
			}
			newValue = strings.Replace(newValue, oldURL, newURL, 1)
		}
		if newValue != r.value {
			if _, err := db.ExecContext(ctx,
				`UPDATE answers SET value = ? WHERE attempt_id = ? AND question_id = ?`,
				newValue, r.attemptID, r.questionID); err == nil {
				rep.FilesCopied["answer_urls_updated"]++
			}
		}
	}
}

func scanQuizIDMapping(root string, rep *Report) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, d := range entries {
		if !d.IsDir() || d.Name() == "assets" {
			continue
		}
		yamls, _ := os.ReadDir(filepath.Join(root, d.Name()))
		for _, f := range yamls {
			if f.IsDir() {
				continue
			}
			lower := strings.ToLower(f.Name())
			if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
				continue
			}
			stem := strings.TrimSuffix(strings.TrimSuffix(f.Name(), ".yaml"), ".yml")
			content, err := os.ReadFile(filepath.Join(root, d.Name(), f.Name()))
			if err != nil {
				continue
			}
			qid := extractQuizID(string(content))
			if qid != "" && qid != stem {
				out[qid] = stem
				rep.QuizIDsRenamed[qid] = stem
			}
		}
	}
	return out
}

// normalizeQuizIDs makes every YAML file's internal quiz_id field match its
// directory name, and updates the DB to use the directory-name form.
func normalizeQuizIDs(ctx context.Context, db *sql.DB, metadataDir string, rep *Report) {
	type row struct {
		id        int
		teacherID string
		slug      string
	}
	rows, err := db.QueryContext(ctx, `SELECT id, teacher_id, slug FROM courses`)
	if err != nil {
		return
	}
	var courses []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.teacherID, &r.slug); err != nil {
			continue
		}
		courses = append(courses, r)
	}
	rows.Close()

	for _, c := range courses {
		quizRoot := filepath.Join(metadataDir, safePart(c.teacherID), safePart(c.slug), "quiz")
		dirs, err := os.ReadDir(quizRoot)
		if err != nil {
			continue
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			dirName := d.Name()
			files, _ := os.ReadDir(filepath.Join(quizRoot, dirName))
			for _, f := range files {
				if f.IsDir() {
					continue
				}
				lower := strings.ToLower(f.Name())
				if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
					continue
				}
				path := filepath.Join(quizRoot, dirName, f.Name())
				content, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				oldID := extractQuizID(string(content))
				if oldID == "" || oldID == dirName {
					break
				}
				newContent := rewriteQuizID(string(content), dirName)
				if err := os.WriteFile(path, []byte(newContent), 0o644); err == nil {
					rep.QuizIDsRenamed[oldID] = dirName
				}
				db.ExecContext(ctx,
					`UPDATE attempts SET quiz_id = ? WHERE course_id = ? AND quiz_id = ?`,
					dirName, c.id, oldID)
				db.ExecContext(ctx,
					`UPDATE admin_summaries SET quiz_id = ? WHERE course_id = ? AND quiz_id = ?`,
					dirName, c.id, oldID)
				break
			}
		}
	}
}

func extractQuizID(content string) string {
	for _, line := range strings.SplitN(content, "\n", 20) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "quiz_id:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "quiz_id:"))
			return strings.Trim(v, "'\"")
		}
	}
	return ""
}

func rewriteQuizID(content, newID string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "quiz_id:") {
			prefix := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = prefix + "quiz_id: " + newID
			break
		}
	}
	return strings.Join(lines, "\n")
}

// ─── basic filesystem helpers ───

func safePart(s string) string {
	s = strings.TrimSpace(s)
	result := make([]byte, 0, len(s))
	for _, b := range []byte(s) {
		switch b {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			result = append(result, '_')
		default:
			result = append(result, b)
		}
	}
	return string(result)
}

func copyFile(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()
	df, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer df.Close()
	_, err = io.Copy(df, sf)
	return err
}

func copyDirFlat(src, dst string) int {
	entries, err := os.ReadDir(src)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err == nil {
			count++
		}
	}
	return count
}

func copyDirRecursive(src, dst string) int {
	count := 0
	_ = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		if err := copyFile(path, filepath.Join(dst, rel)); err == nil {
			count++
		}
		return nil
	})
	return count
}

func flatCopyAllFiles(src, dst string) int {
	count := 0
	_ = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if err := copyFile(path, filepath.Join(dst, info.Name())); err == nil {
			count++
		}
		return nil
	})
	return count
}
