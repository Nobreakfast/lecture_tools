// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"course-assistant/internal/ai"
	"course-assistant/internal/domain"
	"course-assistant/internal/pdftext"
)

type agentToolKind string

const (
	agentToolStudentRead  agentToolKind = "student_read"
	agentToolTeacherRead  agentToolKind = "teacher_read"
	agentToolTeacherDraft agentToolKind = "teacher_draft"
	agentToolTeacherWrite agentToolKind = "teacher_write"
)

type agentToolContext struct {
	Session    *authSession
	Student    *domain.HomeworkSubmission
	Platform   bool
	Confirmed  bool
	CourseID   int
	Course     *domain.Course
	Assignment string
}

type agentTool struct {
	Name        string
	Description string
	Kind        agentToolKind
	Call        func(context.Context, agentToolContext, map[string]any) (string, error)
}

type AgentToolRegistry struct {
	tools map[string]agentTool
}

func (s *Server) agentTools() *AgentToolRegistry {
	r := &AgentToolRegistry{tools: map[string]agentTool{}}
	r.add(agentTool{Name: "list_courses", Description: "列出教师可访问的课程", Kind: agentToolTeacherRead, Call: s.agentToolListCourses})
	r.add(agentTool{Name: "search_agent_mentions", Description: "搜索教师 Agent 可引用的课程、小测、资料、学生、作业和 Q&A", Kind: agentToolTeacherRead, Call: s.agentToolSearchMentions})
	r.add(agentTool{Name: "get_course_context", Description: "读取课程概览、小测、作业和材料摘要", Kind: agentToolTeacherRead, Call: s.agentToolGetCourseContext})
	r.add(agentTool{Name: "get_quiz_bank_list", Description: "列出课程题库库，供 Agent 选择历史小测", Kind: agentToolTeacherRead, Call: s.agentToolGetQuizBankList})
	r.add(agentTool{Name: "read_quiz_bank_yaml", Description: "读取课程题库库中的 YAML 内容，用于参考历史小测", Kind: agentToolTeacherRead, Call: s.agentToolReadQuizBankYAML})
	r.add(agentTool{Name: "list_materials", Description: "列出课程资料文件", Kind: agentToolTeacherRead, Call: s.agentToolListMaterials})
	r.add(agentTool{Name: "read_material_text", Description: "读取课程资料文本，PDF 会提取正文", Kind: agentToolTeacherRead, Call: s.agentToolReadMaterialText})
	r.add(agentTool{Name: "get_quiz_attempts", Description: "获取课程小测记录和最佳成绩", Kind: agentToolTeacherRead, Call: s.agentToolGetQuizAttempts})
	r.add(agentTool{Name: "get_student_profile", Description: "读取单个学生跨小测、作业和 Q&A 的画像", Kind: agentToolTeacherRead, Call: s.agentToolGetStudentProfile})
	r.add(agentTool{Name: "get_student_deep_analysis", Description: "读取单个学生所有小测逐题作答、反馈原文、Q&A 和作业详情，用于深度分析", Kind: agentToolTeacherRead, Call: s.agentToolGetStudentDeepAnalysis})
	r.add(agentTool{Name: "get_attempt_detail", Description: "读取某次小测提交详情和错题", Kind: agentToolTeacherRead, Call: s.agentToolGetAttemptDetail})
	r.add(agentTool{Name: "get_assignment_context", Description: "读取作业说明、提交概览、评分和预评状态", Kind: agentToolTeacherRead, Call: s.agentToolGetAssignmentContext})
	r.add(agentTool{Name: "get_summary_stats", Description: "获取当前加载题库的统计概览", Kind: agentToolTeacherRead, Call: s.agentToolGetSummaryStats})
	r.add(agentTool{Name: "get_quiz_feedback", Description: "获取单次小测问卷和简答反馈", Kind: agentToolTeacherRead, Call: s.agentToolGetQuizFeedback})
	r.add(agentTool{Name: "get_quiz_question_stats", Description: "获取单次小测逐题统计", Kind: agentToolTeacherRead, Call: s.agentToolGetQuizQuestionStats})
	r.add(agentTool{Name: "get_homework_submissions", Description: "获取课程作业提交情况", Kind: agentToolTeacherRead, Call: s.agentToolGetHomeworkSubmissions})
	r.add(agentTool{Name: "get_qa_issues", Description: "查看课程 Q&A 列表和最近消息", Kind: agentToolTeacherRead, Call: s.agentToolGetQAIssues})
	r.add(agentTool{Name: "draft_quiz_from_prompt", Description: "根据教师提示生成题库 YAML 草稿", Kind: agentToolTeacherDraft, Call: s.agentToolDraftQuizFromPrompt})
	r.add(agentTool{Name: "draft_quiz_from_material", Description: "根据课程 PDF 资料生成题库 YAML 草稿", Kind: agentToolTeacherDraft, Call: s.agentToolDraftQuizFromMaterial})
	r.add(agentTool{Name: "autofill_quiz_yaml", Description: "补全题库 YAML 的解析和知识点", Kind: agentToolTeacherDraft, Call: s.agentToolAutofillQuizYAML})
	r.add(agentTool{Name: "draft_class_summary", Description: "生成当前小测课堂总结草稿", Kind: agentToolTeacherDraft, Call: s.agentToolDraftClassSummary})
	r.add(agentTool{Name: "draft_history_summary", Description: "生成课程历史小测趋势总结草稿", Kind: agentToolTeacherDraft, Call: s.agentToolDraftHistorySummary})
	r.add(agentTool{Name: "draft_homework_feedback", Description: "为单个作业提交生成评语草稿", Kind: agentToolTeacherDraft, Call: s.agentToolDraftHomeworkFeedback})
	r.add(agentTool{Name: "draft_student_analysis", Description: "基于学生全量数据生成深度学习行为与参与度分析报告", Kind: agentToolTeacherDraft, Call: s.agentToolDraftStudentAnalysis})
	r.add(agentTool{Name: "save_quiz_to_bank", Description: "保存题库 YAML 到课程题库库；平台内调用需要 confirmed=true", Kind: agentToolTeacherWrite, Call: s.agentToolWriteNotYetImplemented})
	r.add(agentTool{Name: "load_quiz", Description: "加载课程题库；平台内调用需要 confirmed=true", Kind: agentToolTeacherWrite, Call: s.agentToolWriteNotYetImplemented})
	r.add(agentTool{Name: "set_quiz_entry_open", Description: "开启或关闭小测入口；平台内调用需要 confirmed=true", Kind: agentToolTeacherWrite, Call: s.agentToolSetQuizEntryOpen})
	r.add(agentTool{Name: "reply_qa_issue", Description: "以教师身份回复 Q&A，可标记已解决", Kind: agentToolTeacherWrite, Call: s.agentToolReplyQAIssue})
	r.add(agentTool{Name: "start_homework_pregrade", Description: "启动作业批量 AI 预评；平台内调用需要 confirmed=true", Kind: agentToolTeacherWrite, Call: s.agentToolWriteNotYetImplemented})
	r.add(agentTool{Name: "get_my_quiz_history", Description: "学生读取本人历史小测", Kind: agentToolStudentRead, Call: s.agentToolStudentQuizHistory})
	r.add(agentTool{Name: "get_current_homework_status", Description: "学生读取本人当前作业状态", Kind: agentToolStudentRead, Call: s.agentToolStudentHomeworkStatus})
	r.add(agentTool{Name: "search_visible_qa_issues", Description: "学生检索当前课程/作业中可见的已有 Q&A", Kind: agentToolStudentRead, Call: s.agentToolStudentSearchVisibleQAIssues})
	r.add(agentTool{Name: "get_visible_course_materials", Description: "学生列出当前课程中可见的课程资料", Kind: agentToolStudentRead, Call: s.agentToolStudentVisibleCourseMaterials})
	r.add(agentTool{Name: "read_visible_material_text", Description: "学生读取当前课程中可见资料的文本", Kind: agentToolStudentRead, Call: s.agentToolStudentReadVisibleMaterialText})
	r.add(agentTool{Name: "get_visible_assignment_context", Description: "学生读取当前作业可见说明和附件信息", Kind: agentToolStudentRead, Call: s.agentToolStudentVisibleAssignmentContext})
	r.add(agentTool{Name: "create_qa_issue", Description: "学生把需教师确认的问题整理为 Q&A", Kind: agentToolStudentRead, Call: s.agentToolStudentCreateQAIssue})
	return r
}

func (r *AgentToolRegistry) add(tool agentTool) {
	r.tools[tool.Name] = tool
}

func (r *AgentToolRegistry) Tool(name string) (agentTool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *AgentToolRegistry) Call(ctx context.Context, name string, tc agentToolContext, args map[string]any) (string, error) {
	tool, ok := r.Tool(name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	if tool.Kind == agentToolStudentRead {
		if tc.Student == nil {
			return "", fmt.Errorf("unauthorized")
		}
	} else {
		if tc.Session == nil {
			return "", fmt.Errorf("unauthorized")
		}
	}
	if tool.Kind == agentToolTeacherWrite && tc.Platform && !tc.Confirmed {
		return "", fmt.Errorf("该操作会修改系统状态，需要教师在平台内二次确认")
	}
	return tool.Call(ctx, tc, args)
}

func (s *Server) callAgentTool(ctx context.Context, name string, tc agentToolContext, args map[string]any) (string, error) {
	return s.agentTools().Call(ctx, name, tc, args)
}

func agentArgString(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func agentArgInt(args map[string]any, key string, fallback int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case int:
			return n
		case float64:
			return int(n)
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
				return parsed
			}
		}
	}
	return fallback
}

func agentArgBool(args map[string]any, key string, fallback bool) bool {
	if v, ok := args[key]; ok {
		switch b := v.(type) {
		case bool:
			return b
		case string:
			if parsed, err := strconv.ParseBool(strings.TrimSpace(b)); err == nil {
				return parsed
			}
		}
	}
	return fallback
}

func (s *Server) agentCourse(ctx context.Context, tc agentToolContext, args map[string]any, manage bool) (*domain.Course, error) {
	courseID := tc.CourseID
	if courseID <= 0 {
		courseID = agentArgInt(args, "course_id", 0)
	}
	if courseID <= 0 {
		return nil, fmt.Errorf("缺少 course_id")
	}
	if manage {
		return s.teacherMCPManageCourse(ctx, tc.Session, courseID)
	}
	return s.teacherMCPReadCourse(ctx, tc.Session, courseID)
}

func (s *Server) agentToolListCourses(ctx context.Context, tc agentToolContext, _ map[string]any) (string, error) {
	var courses []domain.Course
	var err error
	if tc.Session.Role == domain.RoleAdmin {
		courses, err = s.store.ListAllCourses(ctx)
	} else {
		courses, err = s.store.ListCoursesByTeacher(ctx, tc.Session.TeacherID)
	}
	if err != nil {
		return "", err
	}
	if len(courses) == 0 {
		return "暂无课程", nil
	}
	sort.Slice(courses, func(i, j int) bool { return courses[i].ID < courses[j].ID })
	var b strings.Builder
	b.WriteString("| ID | 课程名称 | 标识 | 邀请码 |\n|----|----------|------|--------|\n")
	for _, c := range courses {
		b.WriteString(fmt.Sprintf("| %d | %s | %s | %s |\n", c.ID, escapeMCPTableCell(teacherAgentCourseName(&c)), escapeMCPTableCell(c.Slug), escapeMCPTableCell(c.InviteCode)))
	}
	return b.String(), nil
}

func (s *Server) agentToolGetCourseContext(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return "", err
	}
	return s.teacherAgentCourseContext(ctx, course), nil
}

func (s *Server) agentToolSearchMentions(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	courseID := agentArgInt(args, "course_id", tc.CourseID)
	query := strings.ToLower(agentArgString(args, "q"))
	limit := agentArgInt(args, "limit", 80)
	if limit <= 0 || limit > 200 {
		limit = 80
	}
	items, err := s.agentMentionCandidates(ctx, tc.Session, courseID, query, limit)
	if err != nil {
		return "", err
	}
	body, _ := json.MarshalIndent(map[string]any{"items": items}, "", "  ")
	return string(body), nil
}

type agentMentionCandidate struct {
	Type     string         `json:"type"`
	ID       string         `json:"id"`
	Label    string         `json:"label"`
	CourseID int            `json:"course_id,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"`
}

func (s *Server) agentMentionCandidates(ctx context.Context, sess *authSession, courseID int, query string, limit int) ([]agentMentionCandidate, error) {
	add := func(items *[]agentMentionCandidate, item agentMentionCandidate) {
		if len(*items) >= limit {
			return
		}
		hay := strings.ToLower(item.Type + " " + item.ID + " " + item.Label + " " + fmt.Sprint(item.Meta))
		if query != "" && !strings.Contains(hay, query) {
			return
		}
		*items = append(*items, item)
	}
	var out []agentMentionCandidate
	var courses []domain.Course
	if courseID > 0 {
		course, err := s.teacherMCPReadCourse(ctx, sess, courseID)
		if err != nil {
			return nil, err
		}
		courses = []domain.Course{*course}
	} else if sess.Role == domain.RoleAdmin {
		all, err := s.store.ListAllCourses(ctx)
		if err != nil {
			return nil, err
		}
		courses = all
	} else {
		owned, err := s.store.ListCoursesByTeacher(ctx, sess.TeacherID)
		if err != nil {
			return nil, err
		}
		courses = owned
	}
	sort.Slice(courses, func(i, j int) bool { return courses[i].ID < courses[j].ID })
	for _, course := range courses {
		add(&out, agentMentionCandidate{Type: "course", ID: strconv.Itoa(course.ID), Label: teacherAgentCourseName(&course), CourseID: course.ID, Meta: map[string]any{"slug": course.Slug}})
		for _, q := range s.agentQuizBankCandidates(&course) {
			add(&out, q)
		}
		for _, m := range s.agentMaterialCandidates(&course) {
			add(&out, m)
		}
		for _, st := range s.agentStudentCandidates(ctx, &course) {
			add(&out, st)
		}
		for _, a := range s.agentAssignmentCandidates(&course) {
			add(&out, a)
		}
		for _, issue := range s.agentQAIssueCandidates(ctx, &course) {
			add(&out, issue)
		}
	}
	return out, nil
}

func (s *Server) agentQuizBankCandidates(course *domain.Course) []agentMentionCandidate {
	quizRoot := filepath.Join(s.metadataCourseDir(course.TeacherID, course.Slug), "quiz")
	dirs, _ := os.ReadDir(quizRoot)
	var out []agentMentionCandidate
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		qid := d.Name()
		label := qid
		if q := s.loadCourseQuizFromBank(course.ID, course.TeacherID, course.Slug, qid); q != nil && strings.TrimSpace(q.Title) != "" {
			label = q.Title
		}
		out = append(out, agentMentionCandidate{Type: "quiz", ID: qid, Label: label, CourseID: course.ID, Meta: map[string]any{"course": teacherAgentCourseName(course)}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Server) agentMaterialCandidates(course *domain.Course) []agentMentionCandidate {
	materials, err := s.scanMaterialsFromDir(s.metadataMaterialsDir(course.TeacherID, course.Slug), course.Slug, true)
	if err != nil {
		return nil
	}
	var out []agentMentionCandidate
	for _, group := range materials {
		for _, d := range group.Downloads {
			out = append(out, agentMentionCandidate{Type: "material", ID: d.File, Label: d.File, CourseID: course.ID, Meta: map[string]any{"stem": group.Stem}})
		}
	}
	return out
}

func (s *Server) agentStudentCandidates(ctx context.Context, course *domain.Course) []agentMentionCandidate {
	attempts, _ := s.store.ListAttemptsByCourse(ctx, course.ID)
	homework, _ := s.store.ListHomeworkSubmissions(ctx, course.ID, course.Slug, "")
	seen := map[string]agentMentionCandidate{}
	for _, a := range attempts {
		key := agentStudentKey(a.StudentNo, a.Name, a.ClassName)
		seen[key] = agentMentionCandidate{Type: "student", ID: key, Label: a.Name, CourseID: course.ID, Meta: map[string]any{"student_no": a.StudentNo, "class_name": a.ClassName, "name": a.Name}}
	}
	for _, h := range homework {
		key := agentStudentKey(h.StudentNo, h.Name, h.ClassName)
		seen[key] = agentMentionCandidate{Type: "student", ID: key, Label: h.Name, CourseID: course.ID, Meta: map[string]any{"student_no": h.StudentNo, "class_name": h.ClassName, "name": h.Name}}
	}
	out := make([]agentMentionCandidate, 0, len(seen))
	for _, item := range seen {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return fmt.Sprint(out[i].Meta["student_no"]) < fmt.Sprint(out[j].Meta["student_no"])
	})
	return out
}

func agentStudentKey(studentNo, name, className string) string {
	return strings.TrimSpace(studentNo) + "|" + strings.TrimSpace(name) + "|" + strings.TrimSpace(className)
}

func (s *Server) agentAssignmentCandidates(course *domain.Course) []agentMentionCandidate {
	seen := map[string]bool{}
	for _, id := range s.listMetadataHomeworkAssignments(course) {
		seen[id] = true
	}
	var out []agentMentionCandidate
	for id := range seen {
		out = append(out, agentMentionCandidate{Type: "assignment", ID: id, Label: id, CourseID: course.ID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Server) agentQAIssueCandidates(ctx context.Context, course *domain.Course) []agentMentionCandidate {
	issues, err := s.store.ListQAIssuesByCourse(ctx, course.ID, false)
	if err != nil {
		return nil
	}
	var out []agentMentionCandidate
	for _, issue := range issues {
		out = append(out, agentMentionCandidate{Type: "qa_issue", ID: strconv.Itoa(issue.ID), Label: "#" + strconv.Itoa(issue.ID) + " " + issue.Title, CourseID: course.ID, Meta: map[string]any{"assignment_id": issue.AssignmentID, "status": issue.Status}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Server) agentToolGetQuizBankList(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return "", err
	}
	items := s.agentQuizBankCandidates(course)
	if len(items) == 0 {
		return "该课程暂无题库。", nil
	}
	var b strings.Builder
	b.WriteString("| 题库ID | 标题 |\n|--------|------|\n")
	for _, item := range items {
		b.WriteString(fmt.Sprintf("| %s | %s |\n", escapeMCPTableCell(item.ID), escapeMCPTableCell(item.Label)))
	}
	return b.String(), nil
}

func (s *Server) agentToolReadQuizBankYAML(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return "", err
	}
	quizID := agentArgString(args, "quiz_id")
	if quizID == "" {
		return "", fmt.Errorf("缺少 quiz_id")
	}
	text, err := s.quizBankYAMLText(course, quizID)
	if err != nil {
		return "", err
	}
	return truncateAgentText(text), nil
}

func (s *Server) agentToolListMaterials(_ context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(context.Background(), tc, args, false)
	if err != nil {
		return "", err
	}
	materials, err := s.scanMaterialsFromDir(s.metadataMaterialsDir(course.TeacherID, course.Slug), course.Slug, true)
	if err != nil || len(materials) == 0 {
		return "该课程暂无资料。", nil
	}
	var b strings.Builder
	b.WriteString("| 资料 | 文件 |\n|------|------|\n")
	for _, m := range materials {
		files := make([]string, 0, len(m.Downloads))
		for _, d := range m.Downloads {
			files = append(files, d.File)
		}
		b.WriteString(fmt.Sprintf("| %s | %s |\n", escapeMCPTableCell(m.Stem), escapeMCPTableCell(strings.Join(files, "、"))))
	}
	return b.String(), nil
}

func (s *Server) agentToolReadMaterialText(_ context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(context.Background(), tc, args, false)
	if err != nil {
		return "", err
	}
	file := agentArgString(args, "file")
	if file == "" {
		file = agentArgString(args, "material_file")
	}
	name, ext, err := normalizeMaterialFilename(file, "")
	if err != nil {
		return "", fmt.Errorf("资料文件名无效")
	}
	path := filepath.Join(s.metadataMaterialsDir(course.TeacherID, course.Slug), name)
	if ext == ".pdf" {
		text, err := pdftext.ExtractText(path)
		if err != nil {
			return "", err
		}
		return truncateAgentText(text), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return truncateAgentText(string(data)), nil
}

func (s *Server) agentToolGetQuizAttempts(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return "", err
	}
	attempts, err := s.store.ListAttemptsByCourse(ctx, course.ID)
	if err != nil {
		return "", err
	}
	s.quizMu.RLock()
	q := s.courseQuizzes[course.ID]
	s.quizMu.RUnlock()
	items := s.teacherCourseBestAttempts(ctx, course, q, attempts)
	if len(items) == 0 {
		return "该课程暂无答题记录", nil
	}
	var b strings.Builder
	b.WriteString("| 姓名 | 学号 | 班级 | 次数 | 状态 | 得分 | 题库 |\n|------|------|------|------|------|------|------|\n")
	for _, item := range items {
		a := item.Attempt
		score := "-"
		if item.QuizLoaded {
			score = fmt.Sprintf("%s/%d", formatScoreValue(item.Correct), item.Total)
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %d | %s | %s | %s |\n", escapeMCPTableCell(a.Name), escapeMCPTableCell(a.StudentNo), escapeMCPTableCell(a.ClassName), a.AttemptNo, a.Status, score, escapeMCPTableCell(a.QuizID)))
	}
	return b.String(), nil
}

func (s *Server) agentToolGetStudentProfile(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return "", err
	}
	studentNo, name, className := agentParseStudentArgs(args)
	if studentNo == "" && name == "" {
		return "", fmt.Errorf("缺少学生标识")
	}
	attempts, err := s.store.ListAttemptsByCourse(ctx, course.ID)
	if err != nil {
		return "", err
	}
	homework, _ := s.store.ListHomeworkSubmissions(ctx, course.ID, course.Slug, "")
	var matchedAttempts []domain.Attempt
	for _, a := range attempts {
		if agentStudentMatches(a.StudentNo, a.Name, a.ClassName, studentNo, name, className) {
			matchedAttempts = append(matchedAttempts, a)
		}
	}
	var matchedHomework []domain.HomeworkSubmission
	for _, h := range homework {
		if agentStudentMatches(h.StudentNo, h.Name, h.ClassName, studentNo, name, className) {
			matchedHomework = append(matchedHomework, h)
		}
	}
	if len(matchedAttempts) == 0 && len(matchedHomework) == 0 {
		return "", fmt.Errorf("未找到该学生的数据")
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("学生：%s（%s，%s）\n课程：%s\n\n", valueOr(name, "未知姓名"), valueOr(studentNo, "未知学号"), valueOr(className, "未知班级"), teacherAgentCourseName(course)))
	if len(matchedAttempts) > 0 {
		qmap := s.teacherCourseQuizMap(course, nil, matchedAttempts)
		b.WriteString("## 小测记录\n| 小测 | 状态 | 次数 | 得分 | 更新时间 |\n|------|------|------|------|----------|\n")
		sort.Slice(matchedAttempts, func(i, j int) bool { return matchedAttempts[i].UpdatedAt.After(matchedAttempts[j].UpdatedAt) })
		for _, a := range matchedAttempts {
			score := "-"
			if q := qmap[a.QuizID]; q != nil && a.Status == domain.StatusSubmitted {
				c, t := s.calcScore(ctx, q, a.ID)
				score = fmt.Sprintf("%s/%d", formatScoreValue(c), t)
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %d | %s | %s |\n", escapeMCPTableCell(a.QuizID), a.Status, a.AttemptNo, score, a.UpdatedAt.Format("2006-01-02 15:04")))
		}
	}
	if len(matchedHomework) > 0 {
		b.WriteString("\n## 作业提交\n| 作业 | 文件 | 评分 | 预评 | 更新时间 |\n|------|------|------|------|----------|\n")
		sort.Slice(matchedHomework, func(i, j int) bool { return matchedHomework[i].UpdatedAt.After(matchedHomework[j].UpdatedAt) })
		for _, h := range matchedHomework {
			score := "未评分"
			if h.Score != nil {
				score = fmt.Sprintf("%.1f", *h.Score)
			}
			pregrade := "无"
			if h.AIPregradeScore != nil || strings.TrimSpace(h.AIPregradeFeedback) != "" {
				pregrade = "有"
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n", escapeMCPTableCell(h.AssignmentID), teacherAgentHomeworkFiles(h), score, pregrade, h.UpdatedAt.Format("2006-01-02 15:04")))
		}
	}
	return b.String(), nil
}

func (s *Server) agentToolGetStudentDeepAnalysis(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return "", err
	}
	studentNo, name, className := agentParseStudentArgs(args)
	if studentNo == "" && name == "" {
		return "", fmt.Errorf("缺少学生标识")
	}

	attempts, err := s.store.ListAttemptsByCourse(ctx, course.ID)
	if err != nil {
		return "", err
	}
	homework, _ := s.store.ListHomeworkSubmissions(ctx, course.ID, course.Slug, "")
	qaIssues, _ := s.store.ListQAIssuesByCourse(ctx, course.ID, false)

	var matchedAttempts []domain.Attempt
	for _, a := range attempts {
		if agentStudentMatches(a.StudentNo, a.Name, a.ClassName, studentNo, name, className) {
			matchedAttempts = append(matchedAttempts, a)
		}
	}
	var matchedHomework []domain.HomeworkSubmission
	for _, h := range homework {
		if agentStudentMatches(h.StudentNo, h.Name, h.ClassName, studentNo, name, className) {
			matchedHomework = append(matchedHomework, h)
		}
	}
	var matchedIssues []domain.QAIssue
	for _, issue := range qaIssues {
		if studentNo != "" && strings.TrimSpace(issue.StudentNo) == studentNo {
			matchedIssues = append(matchedIssues, issue)
		}
	}

	if len(matchedAttempts) == 0 && len(matchedHomework) == 0 && len(matchedIssues) == 0 {
		return "", fmt.Errorf("未找到该学生的数据")
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# 学生深度分析数据\n学生：%s（%s，%s）\n课程：%s\n\n",
		valueOr(name, "未知姓名"), valueOr(studentNo, "未知学号"),
		valueOr(className, "未知班级"), teacherAgentCourseName(course)))

	// Quiz attempt details with per-question answers
	if len(matchedAttempts) > 0 {
		sort.Slice(matchedAttempts, func(i, j int) bool {
			return matchedAttempts[i].UpdatedAt.Before(matchedAttempts[j].UpdatedAt)
		})
		qmap := s.teacherCourseQuizMap(course, nil, matchedAttempts)
		b.WriteString("## 小测逐题作答详情\n")
		for _, a := range matchedAttempts {
			q := qmap[a.QuizID]
			b.WriteString(fmt.Sprintf("\n### 小测 %s（次数 %d，状态 %s，时间 %s）\n",
				a.QuizID, a.AttemptNo, a.Status, a.UpdatedAt.Format("2006-01-02 15:04")))
			if q == nil {
				b.WriteString("题库未找到，无法展示逐题详情。\n")
				continue
			}
			if a.Status != domain.StatusSubmitted {
				b.WriteString("未提交，跳过。\n")
				continue
			}
			answers, aErr := s.store.GetAnswers(ctx, a.ID)
			if aErr != nil {
				b.WriteString("答案读取失败。\n")
				continue
			}
			grades, _ := s.store.GetShortAnswerGrades(ctx, a.ID)
			questions := shuffledQuestions(q, a.ID)
			correct, total := 0, 0
			for _, question := range questions {
				ans := answers[question.ID]
				switch question.Type {
				case domain.QuestionSurvey:
					ansText := ans
					if question.AllowMultiple {
						ansText = ans
					}
					optLabel := agentOptionLabel(question, ansText)
					b.WriteString(fmt.Sprintf("- [调研] %s → %s\n", question.Stem, optLabel))
				case domain.QuestionShortAnswer:
					text := domain.ShortAnswerText(ans)
					if text == "" {
						text = "（未作答）"
					}
					b.WriteString(fmt.Sprintf("- [简答] %s → %s\n", question.Stem, text))
					if g, ok := grades[question.ID]; ok && g.Status == domain.ShortAnswerGradeGraded {
						score := "无"
						if g.Score != nil {
							score = fmt.Sprintf("%.1f", *g.Score)
						}
						b.WriteString(fmt.Sprintf("  AI评分: %s，反馈: %s\n", score, g.Feedback))
					}
				default:
					total++
					isCorrect := isCorrectAnswer(question, ans)
					if isCorrect {
						correct++
					}
					mark := "✗"
					if isCorrect {
						mark = "✓"
					}
					ansLabel := agentOptionLabel(question, ans)
					correctLabel := agentOptionLabel(question, question.CorrectAnswer)
					tag := ""
					if question.KnowledgeTag != "" {
						tag = " [" + question.KnowledgeTag + "]"
					}
					b.WriteString(fmt.Sprintf("- %s %s%s → 答 %s", mark, question.Stem, tag, ansLabel))
					if !isCorrect {
						b.WriteString(fmt.Sprintf("（正确 %s）", correctLabel))
					}
					b.WriteString("\n")
				}
			}
			if total > 0 {
				b.WriteString(fmt.Sprintf("得分: %d/%d\n", correct, total))
			}
		}
	}

	// Homework submissions with feedback
	if len(matchedHomework) > 0 {
		sort.Slice(matchedHomework, func(i, j int) bool {
			return matchedHomework[i].UpdatedAt.Before(matchedHomework[j].UpdatedAt)
		})
		b.WriteString("\n## 作业提交详情\n")
		for _, h := range matchedHomework {
			score := "未评分"
			if h.Score != nil {
				score = fmt.Sprintf("%.1f", *h.Score)
			}
			b.WriteString(fmt.Sprintf("\n### 作业 %s（评分 %s，时间 %s）\n", h.AssignmentID, score, h.UpdatedAt.Format("2006-01-02 15:04")))
			b.WriteString(fmt.Sprintf("文件: %s\n", teacherAgentHomeworkFiles(h)))
			if h.Feedback != "" {
				b.WriteString(fmt.Sprintf("教师评语: %s\n", h.Feedback))
			}
			if h.AIPregradeScore != nil || h.AIPregradeFeedback != "" {
				preScore := "无"
				if h.AIPregradeScore != nil {
					preScore = fmt.Sprintf("%.1f", *h.AIPregradeScore)
				}
				b.WriteString(fmt.Sprintf("AI预评: 分数 %s，反馈: %s\n", preScore, h.AIPregradeFeedback))
			}
			if h.AIPregradeError != "" {
				b.WriteString(fmt.Sprintf("AI预评错误: %s\n", h.AIPregradeError))
			}
		}
	}

	// Q&A issues and messages
	if len(matchedIssues) > 0 {
		sort.Slice(matchedIssues, func(i, j int) bool {
			return matchedIssues[i].CreatedAt.Before(matchedIssues[j].CreatedAt)
		})
		b.WriteString("\n## Q&A 互动记录\n")
		for _, issue := range matchedIssues {
			b.WriteString(fmt.Sprintf("\n### #%d %s（状态 %s，时间 %s）\n",
				issue.ID, issue.Title, issue.Status, issue.CreatedAt.Format("2006-01-02 15:04")))
			msgs, _ := s.store.ListQAMessages(ctx, issue.ID)
			for _, msg := range msgs {
				sender := "学生"
				if msg.Sender == "teacher" {
					sender = "教师"
				}
				content := msg.Content
				if len([]rune(content)) > 300 {
					content = string([]rune(content)[:300]) + "..."
				}
				b.WriteString(fmt.Sprintf("- [%s %s] %s\n", sender, msg.CreatedAt.Format("01-02 15:04"), content))
			}
		}
	}

	return truncateAgentText(b.String()), nil
}

func agentParseStudentArgs(args map[string]any) (studentNo, name, className string) {
	studentNo = agentArgString(args, "student_no")
	name = agentArgString(args, "name")
	className = agentArgString(args, "class_name")
	if studentNo == "" && name == "" {
		rawID := agentArgString(args, "student_id")
		parts := strings.Split(rawID, "|")
		if len(parts) >= 1 {
			studentNo = strings.TrimSpace(parts[0])
		}
		if len(parts) >= 2 {
			name = strings.TrimSpace(parts[1])
		}
		if len(parts) >= 3 {
			className = strings.TrimSpace(parts[2])
		}
	}
	return
}

func agentOptionLabel(q domain.Question, ansKey string) string {
	if ansKey == "" {
		return "（未作答）"
	}
	for _, opt := range q.Options {
		if opt.Key == ansKey {
			return opt.Key + "." + opt.Text
		}
	}
	if strings.Contains(ansKey, ",") {
		keys := strings.Split(ansKey, ",")
		labels := make([]string, 0, len(keys))
		for _, k := range keys {
			k = strings.TrimSpace(k)
			found := false
			for _, opt := range q.Options {
				if opt.Key == k {
					labels = append(labels, opt.Key+"."+opt.Text)
					found = true
					break
				}
			}
			if !found {
				labels = append(labels, k)
			}
		}
		return strings.Join(labels, ", ")
	}
	return ansKey
}

func agentStudentMatches(rowStudentNo, rowName, rowClassName, studentNo, name, className string) bool {
	if studentNo != "" && strings.TrimSpace(rowStudentNo) != studentNo {
		return false
	}
	if name != "" && strings.TrimSpace(rowName) != name {
		return false
	}
	if className != "" && strings.TrimSpace(rowClassName) != className {
		return false
	}
	return true
}

func valueOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}

func (s *Server) agentToolGetAttemptDetail(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return "", err
	}
	attemptID := agentArgString(args, "attempt_id")
	if attemptID == "" {
		attemptID = agentArgString(args, "id")
	}
	if attemptID == "" {
		return "", fmt.Errorf("缺少 attempt_id")
	}
	attempt, err := s.store.GetAttemptByID(ctx, attemptID)
	if err != nil || attempt == nil || attempt.CourseID != course.ID {
		return "", fmt.Errorf("答题记录不存在或无权限")
	}
	res, err := s.buildResult(ctx, attempt)
	if err != nil {
		return "", err
	}
	body, _ := json.MarshalIndent(res, "", "  ")
	return string(body), nil
}

func (s *Server) agentToolGetAssignmentContext(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return "", err
	}
	assignmentID := agentArgString(args, "assignment_id")
	if assignmentID == "" {
		return "", fmt.Errorf("缺少 assignment_id")
	}
	payload := s.homeworkAssignmentPayload(ctx, course.Slug, course.ID, assignmentID, true)
	submissions, err := s.store.ListHomeworkSubmissions(ctx, course.ID, course.Slug, assignmentID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("作业：%s\n课程：%s\n文件数：%v\n隐藏：%v\n锁定：%v\n\n", assignmentID, teacherAgentCourseName(course), payload["file_count"], payload["hidden"], payload["locked"]))
	b.WriteString(fmt.Sprintf("提交数：%d\n", len(submissions)))
	if len(submissions) > 0 {
		b.WriteString("| 姓名 | 学号 | 文件 | 评分 | AI预评 |\n|------|------|------|------|--------|\n")
		for _, sub := range submissions {
			score := "未评分"
			if sub.Score != nil {
				score = fmt.Sprintf("%.1f", *sub.Score)
			}
			pregrade := "无"
			if sub.AIPregradeScore != nil {
				pregrade = fmt.Sprintf("%.1f", *sub.AIPregradeScore)
			} else if sub.AIPregradeError != "" {
				pregrade = "失败"
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n", escapeMCPTableCell(sub.Name), escapeMCPTableCell(sub.StudentNo), teacherAgentHomeworkFiles(sub), score, pregrade))
		}
	}
	return b.String(), nil
}

func (s *Server) agentToolGetSummaryStats(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return "", err
	}
	s.quizMu.RLock()
	q := s.courseQuizzes[course.ID]
	s.quizMu.RUnlock()
	if q == nil {
		return "", fmt.Errorf("当前课程未加载题库")
	}
	raw := s.buildQuizRawStats(ctx, q, course.ID)
	if errText, _ := raw["error"].(string); errText != "" {
		return "", fmt.Errorf("%s", errText)
	}
	body, _ := json.MarshalIndent(raw, "", "  ")
	return string(body), nil
}

func (s *Server) agentToolGetQuizFeedback(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, q, err := s.agentQuizForStats(ctx, tc, args)
	if err != nil {
		return "", err
	}
	input, err := s.buildAdminSummaryInput(ctx, q, course.ID)
	if err != nil {
		return "该题库暂无已提交的答题记录", nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("题库：%s（%s），答题人数：%d\n\n", q.Title, q.QuizID, input.StudentCount))
	for _, item := range input.FeedbackItems {
		b.WriteString(fmt.Sprintf("【%s】%s：%s\n", item.Type, item.QuestionID, item.Stem))
		for k, v := range item.OptionCounts {
			b.WriteString(fmt.Sprintf("- %s：%d\n", k, v))
		}
		for _, sample := range item.TextSamples {
			b.WriteString("- " + sample + "\n")
		}
		b.WriteString("\n")
	}
	if len(input.FeedbackItems) == 0 {
		return "本次小测未收集到问卷/简答反馈。", nil
	}
	return b.String(), nil
}

func (s *Server) agentToolGetQuizQuestionStats(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, q, err := s.agentQuizForStats(ctx, tc, args)
	if err != nil {
		return "", err
	}
	input, err := s.buildAdminSummaryInput(ctx, q, course.ID)
	if err != nil {
		return "该题库暂无已提交的答题记录", nil
	}
	stats := input.QuestionStats
	sort.Slice(stats, func(i, j int) bool { return stats[i].CorrectRate < stats[j].CorrectRate })
	limit := agentArgInt(args, "limit", 10)
	if limit > 0 && len(stats) > limit {
		stats = stats[:limit]
	}
	var b strings.Builder
	b.WriteString("| 题号 | 知识点 | 正确率 | 作答 | 常见错误 |\n|------|--------|--------|------|----------|\n")
	for _, st := range stats {
		b.WriteString(fmt.Sprintf("| %s | %s | %.1f%% | %d | %s |\n", escapeMCPTableCell(st.QuestionID), escapeMCPTableCell(st.KnowledgeTag), st.CorrectRate*100, st.AnsweredCount, escapeMCPTableCell(strings.Join(st.CommonWrongAnswers, "、"))))
	}
	return b.String(), nil
}

func (s *Server) agentQuizForStats(ctx context.Context, tc agentToolContext, args map[string]any) (*domain.Course, *domain.Quiz, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return nil, nil, err
	}
	quizID := agentArgString(args, "quiz_id")
	if quizID != "" {
		q := s.loadCourseQuizFromBank(course.ID, course.TeacherID, course.Slug, quizID)
		if q == nil {
			return nil, nil, fmt.Errorf("题库不存在或无法解析")
		}
		return course, q, nil
	}
	s.quizMu.RLock()
	q := s.courseQuizzes[course.ID]
	s.quizMu.RUnlock()
	if q == nil {
		return nil, nil, fmt.Errorf("当前课程未加载题库")
	}
	return course, q, nil
}

func (s *Server) agentToolGetHomeworkSubmissions(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return "", err
	}
	items, err := s.store.ListHomeworkSubmissions(ctx, course.ID, course.Slug, agentArgString(args, "assignment_id"))
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "该课程暂无作业提交记录", nil
	}
	var b strings.Builder
	b.WriteString("| 姓名 | 学号 | 班级 | 作业 | 文件 | 评分 |\n|------|------|------|------|------|------|\n")
	for _, h := range items {
		score := "未评分"
		if h.Score != nil {
			score = fmt.Sprintf("%.1f", *h.Score)
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n", escapeMCPTableCell(h.Name), escapeMCPTableCell(h.StudentNo), escapeMCPTableCell(h.ClassName), escapeMCPTableCell(h.AssignmentID), teacherAgentHomeworkFiles(h), score))
	}
	return b.String(), nil
}

func (s *Server) agentToolGetQAIssues(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	courseID := agentArgInt(args, "course_id", tc.CourseID)
	return s.teacherMCPQAIssues(ctx, tc.Session, courseID, agentArgString(args, "assignment_id"), agentArgString(args, "status"), agentArgBool(args, "include_hidden", false), agentArgInt(args, "limit", 20), agentArgInt(args, "max_messages", 3))
}

func (s *Server) agentToolDraftQuizFromPrompt(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	prompt := agentArgString(args, "prompt")
	if prompt == "" {
		return "", fmt.Errorf("prompt 不能为空")
	}
	courseID := agentArgInt(args, "course_id", tc.CourseID)
	if courseID <= 0 || tc.Session == nil {
		return s.aiClient.GenerateQuiz(ctx, prompt)
	}
	out, err := s.runTeacherTaskAgent(ctx, teacherTaskAgentRequest{
		TaskType: "quiz_generate",
		Session:  tc.Session,
		CourseID: courseID,
		Prompt:   prompt,
		Args:     cloneAgentArgs(args),
	})
	return out.Text, err
}

func (s *Server) agentToolDraftQuizFromMaterial(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	text, err := s.agentToolReadMaterialText(ctx, tc, args)
	if err != nil {
		return "", err
	}
	in := ai.QuizInitializeInput{
		QuizID:        agentArgString(args, "quiz_id"),
		QuizTitle:     agentArgString(args, "quiz_title"),
		MaterialName:  agentArgString(args, "material_file"),
		QuestionCount: agentArgInt(args, "question_count", 8),
		BasePrompt:    agentArgString(args, "base_prompt"),
		PDFText:       text,
	}
	courseID := agentArgInt(args, "course_id", tc.CourseID)
	if courseID > 0 && tc.Session != nil {
		taskArgs := cloneAgentArgs(args)
		taskArgs["initialize_input"] = in
		out, err := s.runTeacherTaskAgent(ctx, teacherTaskAgentRequest{
			TaskType: "quiz_initialize",
			Session:  tc.Session,
			CourseID: courseID,
			Prompt:   in.BasePrompt,
			Args:     taskArgs,
		})
		return out.Text, err
	}
	return s.aiClient.InitializeQuiz(ctx, in)
}

func (s *Server) agentToolAutofillQuizYAML(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	yaml := agentArgString(args, "yaml")
	if yaml == "" {
		return "", fmt.Errorf("yaml 不能为空")
	}
	courseID := agentArgInt(args, "course_id", tc.CourseID)
	if courseID > 0 && tc.Session != nil {
		out, err := s.runTeacherTaskAgent(ctx, teacherTaskAgentRequest{
			TaskType: "quiz_autofill",
			Session:  tc.Session,
			CourseID: courseID,
			Prompt:   agentArgString(args, "prompt"),
			Args:     cloneAgentArgs(args),
		})
		return out.Text, err
	}
	return s.aiClient.AutoFillQuiz(ctx, yaml)
}

func (s *Server) agentToolDraftClassSummary(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, q, err := s.agentQuizForStats(ctx, tc, args)
	if err != nil {
		return "", err
	}
	input, err := s.buildAdminSummaryInput(ctx, q, course.ID)
	if err != nil {
		return "", err
	}
	summary, err := s.aiClient.AdminSummarize(ctx, input)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("## 答题情况与错题总结\n%s\n\n## 学生反馈总结\n%s\n\n## 教学建议\n%s", summary.AnswerAnalysis, summary.FeedbackSummary, summary.TeachingSuggestions), nil
}

func (s *Server) agentToolDraftHistorySummary(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return "", err
	}
	attempts, err := s.store.ListAttemptsByCourse(ctx, course.ID)
	if err != nil {
		return "", err
	}
	byQuiz := map[string][]domain.Attempt{}
	for _, a := range attempts {
		if a.Status == domain.StatusSubmitted {
			byQuiz[a.QuizID] = append(byQuiz[a.QuizID], a)
		}
	}
	titleMap := s.quizBankTitles(course.TeacherID, course.Slug)
	stats := make([]ai.HistoryQuizStat, 0, len(byQuiz))
	for qid, rows := range byQuiz {
		title := titleMap[qid]
		if title == "" {
			title = qid
		}
		stats = append(stats, ai.HistoryQuizStat{QuizID: qid, QuizTitle: title, StudentCount: len(latestAttempts(rows))})
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].QuizID < stats[j].QuizID })
	summary, err := s.aiClient.HistorySummarize(ctx, ai.HistorySummarizeInput{CourseName: teacherAgentCourseName(course), QuizStats: stats})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("## 总体趋势\n%s\n\n## 表现洞察\n%s\n\n## 教学建议\n%s", summary.OverallTrend, summary.PerformanceInsights, summary.TeachingSuggestions), nil
}

func (s *Server) agentToolDraftHomeworkFeedback(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return "", err
	}
	submissionID := agentArgString(args, "submission_id")
	if submissionID == "" {
		return "", fmt.Errorf("缺少 submission_id")
	}
	submission, err := s.store.GetHomeworkSubmissionByID(ctx, submissionID)
	if err != nil || submission == nil || submission.CourseID != course.ID {
		return "", fmt.Errorf("作业提交不存在或无权限")
	}
	reportPath := filepath.Join(s.resolveHomeworkSubmissionDirForCourse(course, submission), homeworkDiskFilename(domain.HomeworkSlotReport))
	reportText, _ := pdftext.ExtractText(reportPath)
	note := agentArgString(args, "note")
	if tc.Session != nil {
		customPrompt := s.getTeacherPromptOrDefault(ctx, tc.Session.TeacherID, "homework_feedback")
		if strings.TrimSpace(customPrompt) != "" {
			if note != "" {
				note += "\n\n"
			}
			note += "【教师 AI 偏好】\n" + customPrompt
		}
	}
	return s.aiClient.GenerateHomeworkFeedback(ctx, ai.HomeworkGradeFeedbackInput{
		CourseName:    teacherAgentCourseName(course),
		AssignmentID:  submission.AssignmentID,
		StudentName:   submission.Name,
		StudentNo:     submission.StudentNo,
		ClassName:     submission.ClassName,
		TeacherNote:   note,
		ReportContext: reportText,
	})
}

func (s *Server) agentToolDraftStudentAnalysis(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	rawData, err := s.agentToolGetStudentDeepAnalysis(ctx, tc, args)
	if err != nil {
		return "", err
	}
	teacherNote := agentArgString(args, "note")
	if tc.Session != nil {
		customPrompt := s.getTeacherPromptOrDefault(ctx, tc.Session.TeacherID, "student_analysis")
		if strings.TrimSpace(customPrompt) != "" {
			if teacherNote != "" {
				teacherNote += "\n\n"
			}
			teacherNote += "【教师 AI 偏好】\n" + customPrompt
		}
	}
	return s.aiClient.StudentDeepAnalysis(ctx, ai.StudentAnalysisInput{
		RawData:     rawData,
		TeacherNote: teacherNote,
	})
}

func (s *Server) agentToolSetQuizEntryOpen(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, true)
	if err != nil {
		return "", err
	}
	cs, _ := s.store.GetCourseState(ctx, course.ID)
	if cs == nil {
		cs = &domain.CourseState{CourseID: course.ID}
	}
	cs.EntryOpen = agentArgBool(args, "open", false)
	if err := s.store.SetCourseState(ctx, cs); err != nil {
		return "", err
	}
	if cs.EntryOpen {
		return "已开启小测入口。", nil
	}
	return "已关闭小测入口。", nil
}

func (s *Server) agentToolReplyQAIssue(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	return s.teacherMCPReplyQAIssue(ctx, tc.Session, agentArgInt(args, "issue_id", 0), agentArgString(args, "reply"), agentArgBool(args, "resolve", true))
}

func (s *Server) agentToolWriteNotYetImplemented(_ context.Context, _ agentToolContext, _ map[string]any) (string, error) {
	return "", fmt.Errorf("该写入工具已登记，但当前阶段只开放受控入口；请先使用对应页面预览并确认")
}

func (s *Server) agentToolStudentQuizHistory(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	return s.studentMCPQuizHistory(ctx, tc.Student, agentArgInt(args, "limit", 10))
}

func (s *Server) agentToolStudentHomeworkStatus(ctx context.Context, tc agentToolContext, _ map[string]any) (string, error) {
	return s.studentMCPHomeworkStatus(ctx, tc.Student)
}

func (s *Server) agentToolStudentSearchVisibleQAIssues(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	return s.studentMCPSearchVisibleQAIssues(ctx, tc.Student, agentArgString(args, "query"), agentArgInt(args, "limit", 3))
}

func (s *Server) agentToolStudentVisibleCourseMaterials(ctx context.Context, tc agentToolContext, _ map[string]any) (string, error) {
	return s.studentMCPVisibleCourseMaterials(ctx, tc.Student)
}

func (s *Server) agentToolStudentReadVisibleMaterialText(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	return s.studentMCPReadVisibleMaterialText(ctx, tc.Student, agentArgString(args, "material_file"))
}

func (s *Server) agentToolStudentVisibleAssignmentContext(ctx context.Context, tc agentToolContext, _ map[string]any) (string, error) {
	return s.studentMCPVisibleAssignmentContext(ctx, tc.Student)
}

func (s *Server) agentToolStudentCreateQAIssue(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	return s.studentMCPCreateQAIssue(ctx, tc.Student, agentArgString(args, "title"), agentArgString(args, "summary"))
}
