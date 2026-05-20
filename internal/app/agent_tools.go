// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

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

type agentToolParamType string

const (
	agentToolParamString  agentToolParamType = "string"
	agentToolParamInteger agentToolParamType = "integer"
	agentToolParamBoolean agentToolParamType = "boolean"
)

type agentToolParam struct {
	Name        string             `json:"name"`
	Type        agentToolParamType `json:"type"`
	Required    bool               `json:"required,omitempty"`
	Description string             `json:"description,omitempty"`
	Default     any                `json:"default,omitempty"`
	Enum        []string           `json:"enum,omitempty"`
}

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
	Name         string
	Description  string
	Kind         agentToolKind
	Params       []agentToolParam
	ModelVisible bool
	MCPVisible   bool
	Call         func(context.Context, agentToolContext, map[string]any) (string, error)
}

type AgentToolRegistry struct {
	tools map[string]agentTool
}

func (s *Server) agentTools() *AgentToolRegistry {
	r := &AgentToolRegistry{tools: map[string]agentTool{}}
	courseIDParam := agentToolParam{Name: "course_id", Type: agentToolParamInteger, Description: "课程ID；省略时在当前教师可访问的全部课程中查询"}
	limitParam := agentToolParam{Name: "limit", Type: agentToolParamInteger, Description: "最多返回数量"}
	studentParams := []agentToolParam{
		{Name: "student_no", Type: agentToolParamString, Description: "学号"},
		{Name: "name", Type: agentToolParamString, Description: "姓名"},
		{Name: "class_name", Type: agentToolParamString, Description: "班级"},
	}
	selectorParam := agentToolParam{Name: "question_selector", Type: agentToolParamString, Description: "题目筛选", Default: "last", Enum: []string{"all", "last", "feedback", "survey", "wrong"}}

	r.add(agentTool{Name: "list_courses", Description: "列出教师可访问的课程", Kind: agentToolTeacherRead, ModelVisible: true, MCPVisible: true, Call: s.agentToolListCourses})
	r.add(agentTool{Name: "search_course_data", Description: "跨教师可访问课程搜索课程、学生、小测、资料、作业和 Q&A", Kind: agentToolTeacherRead, Params: []agentToolParam{courseIDParam, {Name: "q", Type: agentToolParamString, Description: "关键词，可包含姓名、学号、小测ID、作业ID、资料名或问题标题"}, {Name: "types", Type: agentToolParamString, Description: "逗号分隔类型过滤：course,student,quiz,material,assignment,qa_issue"}, limitParam}, ModelVisible: true, MCPVisible: true, Call: s.agentToolSearchCourseData})
	r.add(agentTool{Name: "list_quizzes", Description: "列出课程内当前加载和题库库中的小测", Kind: agentToolTeacherRead, Params: []agentToolParam{courseIDParam, limitParam}, ModelVisible: true, MCPVisible: true, Call: s.agentToolListQuizzes})
	r.add(agentTool{Name: "read_quiz", Description: "读取小测标题、题目、最后一题和反馈题等结构化信息", Kind: agentToolTeacherRead, Params: []agentToolParam{courseIDParam, {Name: "quiz_id", Type: agentToolParamString, Required: true, Description: "小测/题库ID"}}, ModelVisible: true, MCPVisible: true, Call: s.agentToolReadQuiz})
	r.add(agentTool{Name: "list_quiz_attempts", Description: "按学生、课程、小测和状态列出小测提交记录", Kind: agentToolTeacherRead, Params: append([]agentToolParam{courseIDParam, {Name: "quiz_id", Type: agentToolParamString, Description: "小测/题库ID"}, {Name: "status", Type: agentToolParamString, Description: "提交状态", Enum: []string{"", "submitted", "in_progress"}}, limitParam}, studentParams...), ModelVisible: true, MCPVisible: true, Call: s.agentToolListQuizAttempts})
	r.add(agentTool{Name: "read_quiz_attempt", Description: "读取一次或多次小测提交的作答，可只看最后一题、反馈题、错题或全部题目", Kind: agentToolTeacherRead, Params: []agentToolParam{{Name: "attempt_id", Type: agentToolParamString, Description: "单个答题记录ID"}, {Name: "attempt_ids", Type: agentToolParamString, Description: "多个答题记录ID，逗号分隔；适合读取某学生所有小测"}, selectorParam}, ModelVisible: true, MCPVisible: true, Call: s.agentToolReadQuizAttempt})
	r.add(agentTool{Name: "list_assignments", Description: "列出教师可访问课程中的作业", Kind: agentToolTeacherRead, Params: []agentToolParam{courseIDParam, limitParam}, ModelVisible: true, MCPVisible: true, Call: s.agentToolListAssignments})
	r.add(agentTool{Name: "list_homework_submissions", Description: "按学生或作业列出作业提交记录", Kind: agentToolTeacherRead, Params: append([]agentToolParam{courseIDParam, {Name: "assignment_id", Type: agentToolParamString, Description: "作业编号"}, limitParam}, studentParams...), ModelVisible: true, MCPVisible: true, Call: s.agentToolListHomeworkSubmissions})
	r.add(agentTool{Name: "read_homework_submission", Description: "读取单次作业提交、附件、评分、评语和 AI 预评信息", Kind: agentToolTeacherRead, Params: []agentToolParam{{Name: "submission_id", Type: agentToolParamString, Required: true, Description: "作业提交ID"}}, ModelVisible: true, MCPVisible: true, Call: s.agentToolReadHomeworkSubmission})
	r.add(agentTool{Name: "list_materials", Description: "列出课程资料文件，并返回可传给 read_course_file 的 file_ref", Kind: agentToolTeacherRead, Params: []agentToolParam{courseIDParam, limitParam}, ModelVisible: true, MCPVisible: true, Call: s.agentToolListMaterials})
	r.add(agentTool{Name: "read_course_file", Description: "读取课程内受控文件引用的文本内容，只接受工具返回的 file_ref", Kind: agentToolTeacherRead, Params: []agentToolParam{{Name: "file_ref", Type: agentToolParamString, Required: true, Description: "由 list/search/read 工具返回的受控文件引用"}}, ModelVisible: true, MCPVisible: true, Call: s.agentToolReadCourseFile})
	r.add(agentTool{Name: "list_qa_issues", Description: "查看课程或作业相关 Q&A 列表", Kind: agentToolTeacherRead, Params: []agentToolParam{courseIDParam, {Name: "assignment_id", Type: agentToolParamString, Description: "作业编号"}, {Name: "status", Type: agentToolParamString, Description: "状态", Enum: []string{"", "open", "resolved"}}, limitParam}, ModelVisible: true, MCPVisible: true, Call: s.agentToolListQAIssues})
	r.add(agentTool{Name: "read_qa_issue", Description: "读取一条 Q&A 的详情和消息", Kind: agentToolTeacherRead, Params: []agentToolParam{{Name: "issue_id", Type: agentToolParamInteger, Required: true, Description: "Q&A issue ID"}}, ModelVisible: true, MCPVisible: true, Call: s.agentToolReadQAIssue})

	r.add(agentTool{Name: "search_agent_mentions", Description: "搜索教师 Agent 可引用的课程、小测、资料、学生、作业和 Q&A", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolSearchMentions})
	r.add(agentTool{Name: "get_course_context", Description: "读取课程概览、小测、作业和材料摘要", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolGetCourseContext})
	r.add(agentTool{Name: "get_quiz_bank_list", Description: "列出课程题库库，供 Agent 选择历史小测", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolGetQuizBankList})
	r.add(agentTool{Name: "read_quiz_bank_yaml", Description: "读取课程题库库中的 YAML 内容，用于参考历史小测", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolReadQuizBankYAML})
	r.add(agentTool{Name: "read_material_text", Description: "读取课程资料文本，PDF 会提取正文", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolReadMaterialText})
	r.add(agentTool{Name: "get_quiz_attempts", Description: "获取课程小测记录和最佳成绩", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolGetQuizAttempts})
	r.add(agentTool{Name: "get_student_profile", Description: "读取单个学生跨小测、作业和 Q&A 的画像", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolGetStudentProfile})
	r.add(agentTool{Name: "get_student_quiz_responses", Description: "读取单个学生所有小测的指定题目作答，默认返回每次小测最后一题", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolGetStudentQuizResponses})
	r.add(agentTool{Name: "get_student_homework", Description: "读取单个学生在课程内所有作业的发布、提交、评分、评语和预评详情", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolGetStudentHomework})
	r.add(agentTool{Name: "get_student_deep_analysis", Description: "读取单个学生所有小测逐题作答、反馈原文、Q&A 和作业详情，用于深度分析", Kind: agentToolTeacherRead, Call: s.agentToolGetStudentDeepAnalysis})
	r.add(agentTool{Name: "get_attempt_detail", Description: "读取某次小测提交详情和错题", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolGetAttemptDetail})
	r.add(agentTool{Name: "get_assignment_context", Description: "读取作业说明、提交概览、评分和预评状态", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolGetAssignmentContext})
	r.add(agentTool{Name: "get_summary_stats", Description: "获取当前加载题库的统计概览", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolGetSummaryStats})
	r.add(agentTool{Name: "get_quiz_feedback", Description: "获取单次小测问卷和简答反馈", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolGetQuizFeedback})
	r.add(agentTool{Name: "get_quiz_question_stats", Description: "获取单次小测逐题统计", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolGetQuizQuestionStats})
	r.add(agentTool{Name: "get_homework_submissions", Description: "获取课程作业提交情况", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolGetHomeworkSubmissions})
	r.add(agentTool{Name: "get_qa_issues", Description: "查看课程 Q&A 列表和最近消息", Kind: agentToolTeacherRead, MCPVisible: true, Call: s.agentToolGetQAIssues})
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

func (r *AgentToolRegistry) Tools() []agentTool {
	out := make([]agentTool, 0, len(r.tools))
	for _, tool := range r.tools {
		out = append(out, tool)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *AgentToolRegistry) ModelTools() []agentTool {
	var out []agentTool
	for _, tool := range r.Tools() {
		if tool.ModelVisible {
			out = append(out, tool)
		}
	}
	return out
}

func (r *AgentToolRegistry) MCPTools() []agentTool {
	var out []agentTool
	for _, tool := range r.Tools() {
		if tool.MCPVisible {
			out = append(out, tool)
		}
	}
	return out
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
	validated, err := tool.validateArgs(args)
	if err != nil {
		return "", err
	}
	return tool.Call(ctx, tc, validated)
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

func (tool agentTool) validateArgs(args map[string]any) (map[string]any, error) {
	if args == nil {
		args = map[string]any{}
	}
	if len(tool.Params) == 0 {
		return args, nil
	}
	out := make(map[string]any, len(args)+len(tool.Params))
	for k, v := range args {
		out[k] = v
	}
	for _, p := range tool.Params {
		raw, ok := args[p.Name]
		if !ok || strings.TrimSpace(fmt.Sprint(raw)) == "" {
			if p.Default != nil {
				out[p.Name] = p.Default
				continue
			}
			if p.Required {
				return nil, fmt.Errorf("缺少参数 %s", p.Name)
			}
			continue
		}
		switch p.Type {
		case agentToolParamInteger:
			n, err := coerceAgentToolInt(raw)
			if err != nil {
				return nil, fmt.Errorf("参数 %s 必须是整数", p.Name)
			}
			out[p.Name] = n
		case agentToolParamBoolean:
			b, err := coerceAgentToolBool(raw)
			if err != nil {
				return nil, fmt.Errorf("参数 %s 必须是布尔值", p.Name)
			}
			out[p.Name] = b
		default:
			value := strings.TrimSpace(fmt.Sprint(raw))
			if len(p.Enum) > 0 && !agentToolEnumContains(p.Enum, value) {
				return nil, fmt.Errorf("参数 %s 必须是以下值之一：%s", p.Name, strings.Join(p.Enum, ", "))
			}
			out[p.Name] = value
		}
	}
	return out, nil
}

func coerceAgentToolInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("not an integer")
		}
		return int(n), nil
	case json.Number:
		parsed, err := strconv.Atoi(n.String())
		return parsed, err
	case string:
		return strconv.Atoi(strings.TrimSpace(n))
	default:
		return 0, fmt.Errorf("not an integer")
	}
}

func coerceAgentToolBool(v any) (bool, error) {
	switch b := v.(type) {
	case bool:
		return b, nil
	case string:
		return strconv.ParseBool(strings.TrimSpace(b))
	default:
		return false, fmt.Errorf("not a bool")
	}
}

func agentToolEnumContains(values []string, value string) bool {
	for _, item := range values {
		if value == item {
			return true
		}
	}
	return false
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

func (s *Server) agentReadableCourses(ctx context.Context, tc agentToolContext, args map[string]any) ([]domain.Course, error) {
	courseID := tc.CourseID
	if courseID <= 0 {
		courseID = agentArgInt(args, "course_id", 0)
	}
	if courseID > 0 {
		course, err := s.teacherMCPReadCourse(ctx, tc.Session, courseID)
		if err != nil {
			return nil, err
		}
		return []domain.Course{*course}, nil
	}
	if tc.Session == nil {
		return nil, fmt.Errorf("unauthorized")
	}
	var courses []domain.Course
	var err error
	if tc.Session.Role == domain.RoleAdmin {
		courses, err = s.store.ListAllCourses(ctx)
	} else {
		courses, err = s.store.ListCoursesByTeacher(ctx, tc.Session.TeacherID)
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(courses, func(i, j int) bool { return courses[i].ID < courses[j].ID })
	return courses, nil
}

func agentJSON(v any) (string, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func agentCoursePayload(course *domain.Course) map[string]any {
	if course == nil {
		return nil
	}
	return map[string]any{
		"id":           course.ID,
		"name":         teacherAgentCourseName(course),
		"slug":         course.Slug,
		"teacher_id":   course.TeacherID,
		"display_name": course.DisplayName,
	}
}

func agentFileRef(kind string, courseID int, parts ...string) string {
	values := []string{kind, strconv.Itoa(courseID)}
	for _, part := range parts {
		values = append(values, url.QueryEscape(part))
	}
	return strings.Join(values, ":")
}

func parseAgentFileRef(ref string) (string, int, []string, error) {
	parts := strings.Split(strings.TrimSpace(ref), ":")
	if len(parts) < 3 {
		return "", 0, nil, fmt.Errorf("file_ref 无效")
	}
	kind := strings.TrimSpace(parts[0])
	courseID, err := strconv.Atoi(parts[1])
	if err != nil || courseID <= 0 {
		return "", 0, nil, fmt.Errorf("file_ref 课程无效")
	}
	decoded := make([]string, 0, len(parts)-2)
	for _, raw := range parts[2:] {
		value, err := url.QueryUnescape(raw)
		if err != nil {
			return "", 0, nil, fmt.Errorf("file_ref 编码无效")
		}
		decoded = append(decoded, value)
	}
	return kind, courseID, decoded, nil
}

func agentSafeFileName(raw string) (string, error) {
	name := strings.TrimSpace(filepath.Base(raw))
	if name == "" || name == "." || name == ".." || filepath.IsAbs(raw) || strings.Contains(raw, "/") || strings.Contains(raw, "\\") || strings.Contains(name, "..") || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("文件名无效")
	}
	return name, nil
}

func agentReadControlledFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("不能读取目录")
	}
	if info.Size() > 2*1024*1024 {
		return "", fmt.Errorf("文件过大，请先缩小范围")
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		text, err := pdftext.ExtractText(path)
		if err != nil {
			return "", err
		}
		return truncateAgentText(text), nil
	case ".txt", ".md", ".csv", ".json", ".yaml", ".yml", ".py", ".ipynb":
		body, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return truncateAgentText(string(body)), nil
	default:
		return "", fmt.Errorf("暂不支持读取该类型文件：%s", ext)
	}
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

func (s *Server) agentToolSearchCourseData(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	courseID := agentArgInt(args, "course_id", tc.CourseID)
	query := strings.ToLower(agentArgString(args, "q"))
	limit := agentArgInt(args, "limit", 80)
	if limit <= 0 || limit > 200 {
		limit = 80
	}
	typeFilter := agentTypeFilter(agentArgString(args, "types"))
	items, err := s.agentMentionCandidates(ctx, tc.Session, courseID, query, limit)
	if err != nil {
		return "", err
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if len(typeFilter) > 0 && !typeFilter[item.Type] {
			continue
		}
		payload := map[string]any{
			"type":      item.Type,
			"id":        item.ID,
			"label":     item.Label,
			"course_id": item.CourseID,
			"meta":      item.Meta,
		}
		switch item.Type {
		case "quiz":
			payload["quiz_id"] = item.ID
			payload["file_ref"] = agentFileRef("quiz_yaml", item.CourseID, item.ID)
		case "material":
			payload["material_file"] = item.ID
			payload["file_ref"] = agentFileRef("material", item.CourseID, item.ID)
		case "assignment":
			payload["assignment_id"] = item.ID
		case "qa_issue":
			payload["issue_id"] = item.ID
		case "student":
			if item.Meta != nil {
				payload["student_no"] = item.Meta["student_no"]
				payload["name"] = item.Meta["name"]
				payload["class_name"] = item.Meta["class_name"]
			}
		}
		out = append(out, payload)
		if len(out) >= limit {
			break
		}
	}
	return agentJSON(map[string]any{"items": out})
}

func agentTypeFilter(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out[part] = true
		}
	}
	return out
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

func (s *Server) agentToolListQuizzes(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	courses, err := s.agentReadableCourses(ctx, tc, args)
	if err != nil {
		return "", err
	}
	limit := agentArgInt(args, "limit", 120)
	if limit <= 0 || limit > 300 {
		limit = 120
	}
	var items []map[string]any
	for _, course := range courses {
		seen := map[string]bool{}
		s.quizMu.RLock()
		loaded := s.courseQuizzes[course.ID]
		s.quizMu.RUnlock()
		if loaded != nil {
			seen[loaded.QuizID] = true
			items = append(items, map[string]any{
				"course":         agentCoursePayload(&course),
				"quiz_id":        loaded.QuizID,
				"title":          loaded.Title,
				"question_count": len(loaded.Questions),
				"source":         "loaded",
			})
		}
		for _, candidate := range s.agentQuizBankCandidates(&course) {
			q := s.loadCourseQuizFromBank(course.ID, course.TeacherID, course.Slug, candidate.ID)
			questionCount := 0
			title := candidate.Label
			if q != nil {
				questionCount = len(q.Questions)
				title = q.Title
			}
			source := "bank"
			if seen[candidate.ID] {
				source = "loaded_and_bank"
			}
			items = append(items, map[string]any{
				"course":         agentCoursePayload(&course),
				"quiz_id":        candidate.ID,
				"title":          title,
				"question_count": questionCount,
				"source":         source,
				"file_ref":       agentFileRef("quiz_yaml", course.ID, candidate.ID),
			})
		}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return agentJSON(map[string]any{"items": items})
}

func (s *Server) agentToolReadQuiz(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	quizID := agentArgString(args, "quiz_id")
	if quizID == "" {
		return "", fmt.Errorf("缺少 quiz_id")
	}
	courses, err := s.agentReadableCourses(ctx, tc, args)
	if err != nil {
		return "", err
	}
	var items []map[string]any
	for _, course := range courses {
		q, source := s.agentLoadQuizForCourse(&course, quizID)
		if q == nil {
			continue
		}
		items = append(items, s.agentQuizPayload(&course, q, source))
	}
	if len(items) == 0 {
		return "", fmt.Errorf("未找到可读取的小测 %s", quizID)
	}
	return agentJSON(map[string]any{"items": items})
}

func (s *Server) agentLoadQuizForCourse(course *domain.Course, quizID string) (*domain.Quiz, string) {
	s.quizMu.RLock()
	loaded := s.courseQuizzes[course.ID]
	s.quizMu.RUnlock()
	if loaded != nil && loaded.QuizID == quizID {
		return loaded, "loaded"
	}
	if q := s.loadCourseQuizFromBank(course.ID, course.TeacherID, course.Slug, quizID); q != nil {
		return q, "bank"
	}
	return nil, ""
}

func (s *Server) agentQuizPayload(course *domain.Course, q *domain.Quiz, source string) map[string]any {
	questions := make([]map[string]any, 0, len(q.Questions))
	var feedback []map[string]any
	for i, question := range q.Questions {
		item := map[string]any{
			"index":         i + 1,
			"id":            question.ID,
			"type":          string(question.Type),
			"stem":          question.Stem,
			"knowledge_tag": question.KnowledgeTag,
		}
		if len(question.Options) > 0 {
			item["options"] = question.Options
		}
		if question.Type == domain.QuestionSurvey || question.Type == domain.QuestionShortAnswer {
			feedback = append(feedback, item)
		}
		questions = append(questions, item)
	}
	var last map[string]any
	if len(questions) > 0 {
		last = questions[len(questions)-1]
	}
	return map[string]any{
		"course":             agentCoursePayload(course),
		"quiz_id":            q.QuizID,
		"title":              q.Title,
		"source":             source,
		"question_count":     len(q.Questions),
		"last_question":      last,
		"feedback_questions": feedback,
		"questions":          questions,
		"file_ref":           agentFileRef("quiz_yaml", course.ID, q.QuizID),
	}
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

func (s *Server) agentToolListQuizAttempts(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	courses, err := s.agentReadableCourses(ctx, tc, args)
	if err != nil {
		return "", err
	}
	quizID := agentArgString(args, "quiz_id")
	status := agentArgString(args, "status")
	studentNo, name, className := agentParseStudentArgs(args)
	limit := agentArgInt(args, "limit", 120)
	if limit <= 0 || limit > 300 {
		limit = 120
	}
	var items []map[string]any
	for _, course := range courses {
		attempts, err := s.store.ListAttemptsByCourse(ctx, course.ID)
		if err != nil {
			return "", err
		}
		for _, attempt := range attempts {
			if quizID != "" && attempt.QuizID != quizID {
				continue
			}
			if status != "" && string(attempt.Status) != status {
				continue
			}
			if !agentStudentMatches(attempt.StudentNo, attempt.Name, attempt.ClassName, studentNo, name, className) {
				continue
			}
			item := map[string]any{
				"attempt_id":   attempt.ID,
				"course":       agentCoursePayload(&course),
				"quiz_id":      attempt.QuizID,
				"name":         attempt.Name,
				"student_no":   attempt.StudentNo,
				"class_name":   attempt.ClassName,
				"attempt_no":   attempt.AttemptNo,
				"status":       string(attempt.Status),
				"created_at":   formatMCPTime(attempt.CreatedAt),
				"updated_at":   formatMCPTime(attempt.UpdatedAt),
				"submitted_at": agentFormatTimePtr(attempt.SubmittedAt),
			}
			if q, _ := s.agentLoadQuizForCourse(&course, attempt.QuizID); q != nil {
				if correct, total, ok := s.agentAttemptScore(ctx, &attempt, q); ok {
					item["score"] = fmt.Sprintf("%s/%d", formatScoreValue(correct), total)
				}
			}
			items = append(items, item)
			if len(items) >= limit {
				return agentJSON(map[string]any{"items": items})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return fmt.Sprint(items[i]["updated_at"]) > fmt.Sprint(items[j]["updated_at"])
	})
	return agentJSON(map[string]any{"items": items})
}

func (s *Server) agentToolReadQuizAttempt(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	attemptIDs := agentSplitCSV(agentArgString(args, "attempt_ids"))
	if len(attemptIDs) == 0 {
		if attemptID := agentArgString(args, "attempt_id"); attemptID != "" {
			attemptIDs = []string{attemptID}
		}
	}
	if len(attemptIDs) == 0 {
		return "", fmt.Errorf("缺少 attempt_id 或 attempt_ids")
	}
	if len(attemptIDs) > 30 {
		attemptIDs = attemptIDs[:30]
	}
	selector := agentArgString(args, "question_selector")
	if selector == "" {
		selector = "last"
	}
	if len(attemptIDs) == 1 {
		payload, err := s.agentQuizAttemptPayload(ctx, tc, attemptIDs[0], selector)
		if err != nil {
			return "", err
		}
		return agentJSON(payload)
	}
	items := make([]map[string]any, 0, len(attemptIDs))
	for _, attemptID := range attemptIDs {
		payload, err := s.agentQuizAttemptPayload(ctx, tc, attemptID, selector)
		if err != nil {
			items = append(items, map[string]any{"attempt_id": attemptID, "error": err.Error()})
			continue
		}
		items = append(items, payload)
	}
	return agentJSON(map[string]any{"items": items, "question_selector": selector})
}

func agentSplitCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (s *Server) agentQuizAttemptPayload(ctx context.Context, tc agentToolContext, attemptID, selector string) (map[string]any, error) {
	attempt, err := s.store.GetAttemptByID(ctx, attemptID)
	if err != nil || attempt == nil {
		return nil, fmt.Errorf("答题记录不存在")
	}
	course, err := s.teacherMCPReadCourse(ctx, tc.Session, attempt.CourseID)
	if err != nil {
		return nil, err
	}
	q, source := s.agentLoadQuizForCourse(course, attempt.QuizID)
	if q == nil {
		return nil, fmt.Errorf("无法读取该提交对应的小测定义")
	}
	answers, err := s.store.GetAnswers(ctx, attempt.ID)
	if err != nil {
		return nil, err
	}
	questions := agentSelectQuizQuestionsWithAnswers(q, attempt.ID, selector, answers)
	answerItems := make([]map[string]any, 0, len(questions))
	for _, question := range questions {
		ans := answers[question.ID]
		item := map[string]any{
			"question_id": question.ID,
			"type":        string(question.Type),
			"stem":        question.Stem,
			"answer":      agentQuestionAnswerText(question, ans),
			"raw_answer":  ans,
		}
		if question.CorrectAnswer != "" {
			item["correct_answer"] = agentOptionLabel(question, question.CorrectAnswer)
			item["is_correct"] = isCorrectAnswer(question, ans)
		}
		answerItems = append(answerItems, item)
	}
	payload := map[string]any{
		"attempt": map[string]any{
			"attempt_id":   attempt.ID,
			"course":       agentCoursePayload(course),
			"quiz_id":      attempt.QuizID,
			"quiz_title":   q.Title,
			"quiz_source":  source,
			"name":         attempt.Name,
			"student_no":   attempt.StudentNo,
			"class_name":   attempt.ClassName,
			"attempt_no":   attempt.AttemptNo,
			"status":       string(attempt.Status),
			"submitted_at": agentFormatTimePtr(attempt.SubmittedAt),
			"updated_at":   formatMCPTime(attempt.UpdatedAt),
		},
		"question_selector": selector,
		"answers":           answerItems,
	}
	if correct, total, ok := s.agentAttemptScore(ctx, attempt, q); ok {
		payload["score"] = map[string]any{"correct": correct, "total": total, "label": fmt.Sprintf("%s/%d", formatScoreValue(correct), total)}
	}
	return payload, nil
}

func (s *Server) agentAttemptScore(ctx context.Context, attempt *domain.Attempt, q *domain.Quiz) (float64, int, bool) {
	if attempt == nil || q == nil {
		return 0, 0, false
	}
	answers, err := s.store.GetAnswers(ctx, attempt.ID)
	if err != nil {
		return 0, 0, false
	}
	total := 0
	correct := 0.0
	for _, question := range shuffledQuestions(q, attempt.ID) {
		switch question.Type {
		case domain.QuestionSingleChoice, domain.QuestionMultiChoice, domain.QuestionYesNo:
			total++
			if isCorrectAnswer(question, answers[question.ID]) {
				correct++
			}
		}
	}
	return correct, total, total > 0
}

func agentSelectQuizQuestionsWithAnswers(q *domain.Quiz, attemptID, selector string, answers map[string]string) []domain.Question {
	if selector == "wrong" {
		var out []domain.Question
		for _, question := range shuffledQuestions(q, attemptID) {
			if question.CorrectAnswer != "" && !isCorrectAnswer(question, answers[question.ID]) {
				out = append(out, question)
			}
		}
		return out
	}
	return agentSelectQuizQuestions(q, attemptID, selector)
}

func (s *Server) agentAssignmentCandidates(course *domain.Course) []agentMentionCandidate {
	seen := map[string]bool{}
	for _, id := range s.agentCourseHomeworkAssignmentIDs(course, nil) {
		seen[id] = true
	}
	var out []agentMentionCandidate
	for id := range seen {
		out = append(out, agentMentionCandidate{Type: "assignment", ID: id, Label: id, CourseID: course.ID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Server) agentToolListAssignments(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	courses, err := s.agentReadableCourses(ctx, tc, args)
	if err != nil {
		return "", err
	}
	limit := agentArgInt(args, "limit", 120)
	if limit <= 0 || limit > 300 {
		limit = 120
	}
	var items []map[string]any
	for _, course := range courses {
		submissions, _ := s.store.ListHomeworkSubmissions(ctx, course.ID, course.Slug, "")
		for _, assignmentID := range s.agentCourseHomeworkAssignmentIDs(&course, submissions) {
			files := s.listHomeworkAssignmentFiles(course.Slug, course.ID, assignmentID)
			for _, file := range files {
				if name, ok := file["name"].(string); ok {
					file["file_ref"] = agentFileRef("assignment_file", course.ID, assignmentID, name)
				}
			}
			count := 0
			for _, sub := range submissions {
				if sub.AssignmentID == assignmentID {
					count++
				}
			}
			items = append(items, map[string]any{
				"course":           agentCoursePayload(&course),
				"assignment_id":    assignmentID,
				"file_count":       len(files),
				"submission_count": count,
				"files":            files,
			})
			if len(items) >= limit {
				return agentJSON(map[string]any{"items": items})
			}
		}
	}
	return agentJSON(map[string]any{"items": items})
}

func (s *Server) agentToolListHomeworkSubmissions(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	courses, err := s.agentReadableCourses(ctx, tc, args)
	if err != nil {
		return "", err
	}
	assignmentID := agentArgString(args, "assignment_id")
	studentNo, name, className := agentParseStudentArgs(args)
	limit := agentArgInt(args, "limit", 120)
	if limit <= 0 || limit > 300 {
		limit = 120
	}
	var items []map[string]any
	for _, course := range courses {
		submissions, err := s.store.ListHomeworkSubmissions(ctx, course.ID, course.Slug, assignmentID)
		if err != nil {
			return "", err
		}
		for _, sub := range submissions {
			if !agentStudentMatches(sub.StudentNo, sub.Name, sub.ClassName, studentNo, name, className) {
				continue
			}
			items = append(items, s.agentHomeworkSubmissionListPayload(&course, &sub))
			if len(items) >= limit {
				return agentJSON(map[string]any{"items": items})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return fmt.Sprint(items[i]["updated_at"]) > fmt.Sprint(items[j]["updated_at"])
	})
	return agentJSON(map[string]any{"items": items})
}

func (s *Server) agentHomeworkSubmissionListPayload(course *domain.Course, sub *domain.HomeworkSubmission) map[string]any {
	score := "未评分"
	if sub.Score != nil {
		score = fmt.Sprintf("%.1f", *sub.Score)
	}
	pregrade := "无"
	if sub.AIPregradeScore != nil {
		pregrade = fmt.Sprintf("%.1f", *sub.AIPregradeScore)
	} else if strings.TrimSpace(sub.AIPregradeFeedback) != "" {
		pregrade = "有反馈"
	} else if strings.TrimSpace(sub.AIPregradeError) != "" {
		pregrade = "失败"
	}
	return map[string]any{
		"submission_id": sub.ID,
		"course":        agentCoursePayload(course),
		"assignment_id": sub.AssignmentID,
		"name":          sub.Name,
		"student_no":    sub.StudentNo,
		"class_name":    sub.ClassName,
		"files":         teacherAgentHomeworkFiles(*sub),
		"score":         score,
		"ai_pregrade":   pregrade,
		"created_at":    formatMCPTime(sub.CreatedAt),
		"updated_at":    formatMCPTime(sub.UpdatedAt),
	}
}

func (s *Server) agentToolReadHomeworkSubmission(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	submissionID := agentArgString(args, "submission_id")
	if submissionID == "" {
		return "", fmt.Errorf("缺少 submission_id")
	}
	sub, err := s.store.GetHomeworkSubmissionByID(ctx, submissionID)
	if err != nil || sub == nil {
		return "", fmt.Errorf("作业提交不存在")
	}
	course, err := s.teacherMCPReadCourse(ctx, tc.Session, sub.CourseID)
	if err != nil {
		return "", err
	}
	files := s.agentHomeworkSubmissionFiles(course, sub)
	payload := s.agentHomeworkSubmissionListPayload(course, sub)
	payload["file_details"] = files
	payload["feedback"] = sub.Feedback
	payload["graded_at"] = agentFormatTimePtr(sub.GradedAt)
	payload["grade_updated_at"] = agentFormatTimePtr(sub.GradeUpdatedAt)
	payload["ai_pregrade_score"] = sub.AIPregradeScore
	payload["ai_pregrade_feedback"] = sub.AIPregradeFeedback
	payload["ai_pregrade_prompt"] = sub.AIPregradePrompt
	payload["ai_pregraded_at"] = agentFormatTimePtr(sub.AIPregradedAt)
	payload["ai_pregrade_error"] = sub.AIPregradeError
	return agentJSON(payload)
}

func (s *Server) agentHomeworkSubmissionFiles(course *domain.Course, sub *domain.HomeworkSubmission) []map[string]any {
	var files []map[string]any
	add := func(slot domain.HomeworkFileSlot, original string, uploadedAt *time.Time) {
		if strings.TrimSpace(original) == "" {
			return
		}
		files = append(files, map[string]any{
			"slot":          string(slot),
			"original_name": original,
			"uploaded_at":   agentFormatTimePtr(uploadedAt),
			"file_ref":      agentFileRef("submission_file", course.ID, sub.ID, string(slot), original),
		})
	}
	add(domain.HomeworkSlotReport, sub.ReportOriginalName, sub.ReportUploadedAt)
	add(domain.HomeworkSlotCode, sub.CodeOriginalName, sub.CodeUploadedAt)
	add(domain.HomeworkSlotExtra, sub.ExtraOriginalName, sub.ExtraUploadedAt)
	for _, item := range s.listOthersFiles(sub) {
		name, _ := item["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		files = append(files, map[string]any{
			"slot":     string(domain.HomeworkSlotOthers),
			"name":     name,
			"size":     item["size"],
			"file_ref": agentFileRef("submission_file", course.ID, sub.ID, string(domain.HomeworkSlotOthers), name),
		})
	}
	return files
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

func (s *Server) agentToolListQAIssues(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	courses, err := s.agentReadableCourses(ctx, tc, args)
	if err != nil {
		return "", err
	}
	assignmentID := agentArgString(args, "assignment_id")
	status := agentArgString(args, "status")
	limit := agentArgInt(args, "limit", 80)
	if limit <= 0 || limit > 200 {
		limit = 80
	}
	var items []map[string]any
	for _, course := range courses {
		var issues []domain.QAIssue
		var err error
		if assignmentID != "" {
			issues, err = s.store.ListQAIssues(ctx, course.ID, assignmentID, false)
		} else {
			issues, err = s.store.ListQAIssuesByCourse(ctx, course.ID, false)
		}
		if err != nil {
			return "", err
		}
		for _, issue := range issues {
			if status != "" && issue.Status != status {
				continue
			}
			items = append(items, map[string]any{
				"issue_id":      issue.ID,
				"course":        agentCoursePayload(&course),
				"assignment_id": issue.AssignmentID,
				"student_no":    issue.StudentNo,
				"title":         issue.Title,
				"status":        issue.Status,
				"message_count": issue.MessageCount,
				"pinned":        issue.Pinned,
				"updated_at":    formatMCPTime(issue.UpdatedAt),
			})
			if len(items) >= limit {
				return agentJSON(map[string]any{"items": items})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return fmt.Sprint(items[i]["updated_at"]) > fmt.Sprint(items[j]["updated_at"])
	})
	return agentJSON(map[string]any{"items": items})
}

func (s *Server) agentToolReadQAIssue(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	issueID := agentArgInt(args, "issue_id", 0)
	if issueID <= 0 {
		return "", fmt.Errorf("缺少 issue_id")
	}
	issue, err := s.store.GetQAIssueByID(ctx, issueID)
	if err != nil || issue == nil {
		return "", fmt.Errorf("Q&A 不存在")
	}
	course, err := s.teacherMCPReadCourse(ctx, tc.Session, issue.CourseID)
	if err != nil {
		return "", err
	}
	messages, err := s.store.ListQAMessages(ctx, issue.ID)
	if err != nil {
		return "", err
	}
	msgItems := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		msgItems = append(msgItems, map[string]any{
			"id":         msg.ID,
			"sender":     msg.Sender,
			"content":    msg.Content,
			"images":     msg.Images,
			"created_at": formatMCPTime(msg.CreatedAt),
		})
	}
	return agentJSON(map[string]any{
		"issue": map[string]any{
			"issue_id":      issue.ID,
			"course":        agentCoursePayload(course),
			"assignment_id": issue.AssignmentID,
			"student_no":    issue.StudentNo,
			"title":         issue.Title,
			"status":        issue.Status,
			"pinned":        issue.Pinned,
			"hidden":        issue.Hidden,
			"created_at":    formatMCPTime(issue.CreatedAt),
			"updated_at":    formatMCPTime(issue.UpdatedAt),
		},
		"messages": msgItems,
	})
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

func (s *Server) agentToolListMaterials(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	courses, err := s.agentReadableCourses(ctx, tc, args)
	if err != nil {
		return "", err
	}
	limit := agentArgInt(args, "limit", 120)
	if limit <= 0 || limit > 300 {
		limit = 120
	}
	var items []map[string]any
	for _, course := range courses {
		materials, err := s.scanMaterialsFromDir(s.metadataMaterialsDir(course.TeacherID, course.Slug), course.Slug, true)
		if err != nil {
			return "", err
		}
		for _, m := range materials {
			for _, d := range m.Downloads {
				items = append(items, map[string]any{
					"course":    agentCoursePayload(&course),
					"stem":      m.Stem,
					"file":      d.File,
					"extension": d.Extension,
					"size":      d.Size,
					"visible":   d.Visible,
					"file_ref":  agentFileRef("material", course.ID, d.File),
				})
				if len(items) >= limit {
					return agentJSON(map[string]any{"items": items})
				}
			}
		}
	}
	return agentJSON(map[string]any{"items": items})
}

func (s *Server) agentToolReadCourseFile(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	ref := agentArgString(args, "file_ref")
	if ref == "" {
		return "", fmt.Errorf("缺少 file_ref")
	}
	kind, courseID, parts, err := parseAgentFileRef(ref)
	if err != nil {
		return "", err
	}
	course, err := s.teacherMCPReadCourse(ctx, tc.Session, courseID)
	if err != nil {
		return "", err
	}
	var path string
	var label string
	switch kind {
	case "material":
		if len(parts) != 1 {
			return "", fmt.Errorf("material file_ref 无效")
		}
		name, _, err := normalizeMaterialFilename(parts[0], "")
		if err != nil {
			return "", fmt.Errorf("资料文件名无效")
		}
		path = filepath.Join(s.metadataMaterialsDir(course.TeacherID, course.Slug), name)
		label = name
	case "quiz_yaml":
		if len(parts) != 1 {
			return "", fmt.Errorf("quiz file_ref 无效")
		}
		quizID := strings.TrimSpace(parts[0])
		if quizID == "" || strings.Contains(quizID, "/") || strings.Contains(quizID, "\\") || strings.Contains(quizID, "..") {
			return "", fmt.Errorf("题库ID无效")
		}
		text, err := s.quizBankYAMLText(course, quizID)
		if err != nil {
			return "", err
		}
		return agentJSON(map[string]any{"file_ref": ref, "course": agentCoursePayload(course), "kind": kind, "name": quizID + ".yaml", "text": truncateAgentText(text)})
	case "assignment_file":
		if len(parts) != 2 {
			return "", fmt.Errorf("assignment file_ref 无效")
		}
		assignmentID, err := validateHomeworkAssignmentID(parts[0])
		if err != nil {
			return "", fmt.Errorf("作业编号无效")
		}
		name, err := normalizeHomeworkResourceFilename(parts[1])
		if err != nil {
			return "", fmt.Errorf("作业文件名无效")
		}
		path = filepath.Join(s.metadataHomeworkAssignmentDir(course.TeacherID, course.Slug, assignmentID), name)
		if _, err := os.Stat(path); err != nil {
			bundlePath := filepath.Join(s.homeworkAssignmentDir(course.Slug, assignmentID), name)
			if _, bErr := os.Stat(bundlePath); bErr == nil {
				path = bundlePath
			} else if name == assignmentID+".pdf" {
				legacyPath := s.homeworkLegacyAssignmentPath(course.Slug, assignmentID)
				if _, lErr := os.Stat(legacyPath); lErr == nil {
					path = legacyPath
				}
			}
		}
		label = assignmentID + "/" + name
	case "submission_file":
		if len(parts) != 3 {
			return "", fmt.Errorf("submission file_ref 无效")
		}
		submissionID := strings.TrimSpace(parts[0])
		slot, err := parseHomeworkSlot(parts[1])
		if err != nil {
			return "", fmt.Errorf("作业提交文件类型无效")
		}
		submission, err := s.store.GetHomeworkSubmissionByID(ctx, submissionID)
		if err != nil || submission == nil || submission.CourseID != course.ID {
			return "", fmt.Errorf("作业提交不存在或无权限")
		}
		if slot == domain.HomeworkSlotOthers {
			name, err := validateOthersFilename(parts[2])
			if err != nil {
				return "", fmt.Errorf("补充文件名无效")
			}
			path = filepath.Join(s.homeworkOthersDir(submission), name)
			label = submissionID + "/" + name
		} else {
			path = s.homeworkStoredFilePath(submission, slot)
			label = submissionID + "/" + string(slot)
		}
	default:
		return "", fmt.Errorf("不支持的 file_ref 类型")
	}
	text, err := agentReadControlledFile(path)
	if err != nil {
		return "", err
	}
	return agentJSON(map[string]any{"file_ref": ref, "course": agentCoursePayload(course), "kind": kind, "name": label, "text": text})
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

func (s *Server) agentToolGetStudentQuizResponses(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
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
	var matched []domain.Attempt
	for _, a := range attempts {
		if a.Status != domain.StatusSubmitted {
			continue
		}
		if agentStudentMatches(a.StudentNo, a.Name, a.ClassName, studentNo, name, className) {
			matched = append(matched, a)
			if studentNo == "" {
				studentNo = strings.TrimSpace(a.StudentNo)
			}
			if name == "" {
				name = strings.TrimSpace(a.Name)
			}
			if className == "" {
				className = strings.TrimSpace(a.ClassName)
			}
		}
	}
	if len(matched) == 0 {
		return "", fmt.Errorf("未找到该学生的已提交小测记录")
	}

	latestByQuiz := map[string]domain.Attempt{}
	for _, a := range matched {
		current, ok := latestByQuiz[a.QuizID]
		if !ok || a.AttemptNo > current.AttemptNo || (a.AttemptNo == current.AttemptNo && a.UpdatedAt.After(current.UpdatedAt)) {
			latestByQuiz[a.QuizID] = a
		}
	}
	selected := make([]domain.Attempt, 0, len(latestByQuiz))
	for _, a := range latestByQuiz {
		selected = append(selected, a)
	}
	sort.Slice(selected, func(i, j int) bool {
		if !selected[i].UpdatedAt.Equal(selected[j].UpdatedAt) {
			return selected[i].UpdatedAt.Before(selected[j].UpdatedAt)
		}
		return selected[i].QuizID < selected[j].QuizID
	})

	qmap := s.teacherCourseQuizMap(course, nil, selected)
	selector := strings.ToLower(strings.TrimSpace(agentArgString(args, "question_selector")))
	if selector == "" {
		selector = "last"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# 学生小测作答摘录\n学生：%s（%s，%s）\n课程：%s\n题目筛选：%s\n",
		valueOr(name, "未知姓名"), valueOr(studentNo, "未知学号"), valueOr(className, "未知班级"), teacherAgentCourseName(course), agentQuizSelectorLabel(selector)))
	b.WriteString(fmt.Sprintf("共找到 %d 次已提交小测（同一题库取最新一次提交）。\n", len(selected)))
	for _, a := range selected {
		q := qmap[a.QuizID]
		title := a.QuizID
		if q != nil && strings.TrimSpace(q.Title) != "" {
			title = q.Title
		}
		b.WriteString(fmt.Sprintf("\n## %s（%s，第 %d 次，%s）\n", title, a.QuizID, a.AttemptNo, a.UpdatedAt.Format("2006-01-02 15:04")))
		if q == nil {
			b.WriteString("题库未找到，无法读取题目内容。\n")
			continue
		}
		questions := agentSelectQuizQuestions(q, a.ID, selector)
		if len(questions) == 0 {
			b.WriteString("未找到符合筛选条件的题目。\n")
			continue
		}
		answers, ansErr := s.store.GetAnswers(ctx, a.ID)
		if ansErr != nil {
			b.WriteString("答案读取失败。\n")
			continue
		}
		for _, question := range questions {
			ans := strings.TrimSpace(answers[question.ID])
			b.WriteString(fmt.Sprintf("- [%s] %s：%s\n", question.ID, strings.TrimSpace(question.Stem), agentQuestionAnswerText(question, ans)))
		}
	}
	return b.String(), nil
}

func (s *Server) agentToolGetStudentHomework(ctx context.Context, tc agentToolContext, args map[string]any) (string, error) {
	course, err := s.agentCourse(ctx, tc, args, false)
	if err != nil {
		return "", err
	}
	studentNo, name, className := agentParseStudentArgs(args)
	if studentNo == "" && name == "" {
		return "", fmt.Errorf("缺少学生标识")
	}

	homework, err := s.store.ListHomeworkSubmissions(ctx, course.ID, course.Slug, "")
	if err != nil {
		return "", err
	}
	var matched []domain.HomeworkSubmission
	for _, h := range homework {
		if agentStudentMatches(h.StudentNo, h.Name, h.ClassName, studentNo, name, className) {
			matched = append(matched, h)
			if studentNo == "" {
				studentNo = strings.TrimSpace(h.StudentNo)
			}
			if name == "" {
				name = strings.TrimSpace(h.Name)
			}
			if className == "" {
				className = strings.TrimSpace(h.ClassName)
			}
		}
	}

	assignmentIDs := s.agentCourseHomeworkAssignmentIDs(course, matched)
	if len(assignmentIDs) == 0 && len(matched) == 0 {
		return "该课程暂无作业发布或提交记录。", nil
	}

	byAssignment := map[string][]domain.HomeworkSubmission{}
	for _, h := range matched {
		byAssignment[h.AssignmentID] = append(byAssignment[h.AssignmentID], h)
	}
	for aid := range byAssignment {
		sort.Slice(byAssignment[aid], func(i, j int) bool {
			return byAssignment[aid][i].UpdatedAt.After(byAssignment[aid][j].UpdatedAt)
		})
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# 学生作业全量数据\n学生：%s（%s，%s）\n课程：%s\n",
		valueOr(name, "未知姓名"), valueOr(studentNo, "未知学号"), valueOr(className, "未知班级"), teacherAgentCourseName(course)))
	b.WriteString(fmt.Sprintf("作业发布数：%d；有提交记录：%d；未创建提交：%d。\n",
		len(assignmentIDs), len(byAssignment), max(0, len(assignmentIDs)-len(byAssignment))))

	for _, assignmentID := range assignmentIDs {
		payload := s.homeworkAssignmentPayload(ctx, course.Slug, course.ID, assignmentID, true)
		b.WriteString(fmt.Sprintf("\n## 作业 %s\n", assignmentID))
		b.WriteString(fmt.Sprintf("- 作业附件数：%v；隐藏：%v；锁定：%v\n", payload["file_count"], payload["hidden"], payload["locked"]))
		b.WriteString(agentAssignmentScheduleLine(payload))
		subs := byAssignment[assignmentID]
		if len(subs) == 0 {
			b.WriteString("- 提交状态：未创建提交记录/未提交。\n")
			continue
		}
		for _, h := range subs {
			b.WriteString(fmt.Sprintf("- 提交ID：%s；更新时间：%s；学生最后提交时间：%s\n",
				h.ID, formatMCPTime(h.UpdatedAt), formatMCPTime(homeworkLastStudentSubmissionAt(&h, s.listOthersFiles(&h)))))
			b.WriteString("- 文件：" + agentHomeworkFilesDetailed(s, &h) + "\n")
			if h.Score != nil {
				b.WriteString(fmt.Sprintf("- 教师评分：%.1f\n", *h.Score))
			} else {
				b.WriteString("- 教师评分：未评分\n")
			}
			if strings.TrimSpace(h.Feedback) != "" {
				b.WriteString("- 教师评语：" + strings.TrimSpace(h.Feedback) + "\n")
			}
			if h.GradedAt != nil || h.GradeUpdatedAt != nil {
				b.WriteString(fmt.Sprintf("- 评分时间：%s；最后改分：%s\n", agentFormatTimePtr(h.GradedAt), agentFormatTimePtr(h.GradeUpdatedAt)))
			}
			if h.AIPregradeScore != nil {
				b.WriteString(fmt.Sprintf("- AI预评分：%.1f\n", *h.AIPregradeScore))
			}
			if strings.TrimSpace(h.AIPregradeFeedback) != "" {
				b.WriteString("- AI预评反馈：" + strings.TrimSpace(h.AIPregradeFeedback) + "\n")
			}
			if strings.TrimSpace(h.AIPregradeError) != "" {
				b.WriteString("- AI预评错误：" + strings.TrimSpace(h.AIPregradeError) + "\n")
			}
			if h.AIPregradedAt != nil {
				b.WriteString("- AI预评时间：" + agentFormatTimePtr(h.AIPregradedAt) + "\n")
			}
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

func agentSelectQuizQuestions(q *domain.Quiz, attemptID, selector string) []domain.Question {
	questions := shuffledQuestions(q, attemptID)
	if len(questions) == 0 {
		return nil
	}
	switch selector {
	case "all":
		return questions
	case "feedback", "survey":
		var out []domain.Question
		for _, question := range questions {
			if question.Type == domain.QuestionSurvey || question.Type == domain.QuestionShortAnswer {
				out = append(out, question)
			}
		}
		return out
	default:
		return []domain.Question{questions[len(questions)-1]}
	}
}

func agentQuizSelectorLabel(selector string) string {
	switch selector {
	case "all":
		return "全部题目"
	case "feedback", "survey":
		return "反馈/调研/简答题"
	default:
		return "每次小测最后一题"
	}
}

func agentQuestionAnswerText(question domain.Question, ans string) string {
	if strings.TrimSpace(ans) == "" {
		return "（未作答）"
	}
	if question.Type == domain.QuestionShortAnswer {
		text := strings.TrimSpace(domain.ShortAnswerText(ans))
		if text == "" {
			return "（未作答）"
		}
		return text
	}
	return agentOptionLabel(question, ans)
}

func (s *Server) agentCourseHomeworkAssignmentIDs(course *domain.Course, submissions []domain.HomeworkSubmission) []string {
	seen := map[string]bool{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, err := validateHomeworkAssignmentID(id); err != nil {
			return
		}
		seen[id] = true
	}
	for _, id := range s.listMetadataHomeworkAssignments(course) {
		if s.homeworkAssignmentExists(course.Slug, course.ID, id) {
			add(id)
		}
	}
	if legacy, err := s.listHomeworkAssignments(course.Slug); err == nil {
		for _, id := range legacy {
			add(id)
		}
	}
	for _, sub := range submissions {
		add(sub.AssignmentID)
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func agentAssignmentScheduleLine(payload map[string]any) string {
	parts := make([]string, 0, 2)
	if v, ok := payload["visible_at"]; ok && fmt.Sprint(v) != "" && fmt.Sprint(v) != "<nil>" {
		parts = append(parts, "开放时间："+fmt.Sprint(v))
	}
	if v, ok := payload["deadline_at"]; ok && fmt.Sprint(v) != "" && fmt.Sprint(v) != "<nil>" {
		parts = append(parts, "截止时间："+fmt.Sprint(v))
	}
	if len(parts) == 0 {
		return ""
	}
	return "- " + strings.Join(parts, "；") + "\n"
}

func agentHomeworkFilesDetailed(s *Server, h *domain.HomeworkSubmission) string {
	var parts []string
	if strings.TrimSpace(h.ReportOriginalName) != "" {
		parts = append(parts, "报告="+strings.TrimSpace(h.ReportOriginalName)+"（"+agentFormatTimePtr(h.ReportUploadedAt)+"）")
	}
	if strings.TrimSpace(h.CodeOriginalName) != "" {
		parts = append(parts, "代码="+strings.TrimSpace(h.CodeOriginalName)+"（"+agentFormatTimePtr(h.CodeUploadedAt)+"）")
	}
	if strings.TrimSpace(h.ExtraOriginalName) != "" {
		parts = append(parts, "附件="+strings.TrimSpace(h.ExtraOriginalName)+"（"+agentFormatTimePtr(h.ExtraUploadedAt)+"）")
	}
	var others []string
	for _, item := range s.listOthersFiles(h) {
		if name, ok := item["name"].(string); ok && strings.TrimSpace(name) != "" {
			others = append(others, strings.TrimSpace(name))
		}
	}
	if len(others) > 0 {
		parts = append(parts, "补充文件="+strings.Join(others, "、"))
	}
	if len(parts) == 0 {
		return "未上传文件"
	}
	return strings.Join(parts, "；")
}

func agentFormatTimePtr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return formatMCPTime(*t)
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
	studentNo, name, className := agentParseStudentArgs(args)
	if studentNo != "" || name != "" || className != "" {
		filtered := make([]domain.HomeworkSubmission, 0, len(items))
		for _, h := range items {
			if agentStudentMatches(h.StudentNo, h.Name, h.ClassName, studentNo, name, className) {
				filtered = append(filtered, h)
			}
		}
		items = filtered
	}
	if len(items) == 0 {
		return "该课程暂无作业提交记录", nil
	}
	var b strings.Builder
	b.WriteString("| 姓名 | 学号 | 班级 | 作业 | 提交ID | 文件 | 评分 | AI预评 |\n|------|------|------|------|--------|------|------|--------|\n")
	for _, h := range items {
		score := "未评分"
		if h.Score != nil {
			score = fmt.Sprintf("%.1f", *h.Score)
		}
		pregrade := "无"
		if h.AIPregradeScore != nil {
			pregrade = fmt.Sprintf("%.1f", *h.AIPregradeScore)
		} else if strings.TrimSpace(h.AIPregradeFeedback) != "" {
			pregrade = "有反馈"
		} else if strings.TrimSpace(h.AIPregradeError) != "" {
			pregrade = "失败"
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s |\n", escapeMCPTableCell(h.Name), escapeMCPTableCell(h.StudentNo), escapeMCPTableCell(h.ClassName), escapeMCPTableCell(h.AssignmentID), escapeMCPTableCell(h.ID), teacherAgentHomeworkFiles(h), score, escapeMCPTableCell(pregrade)))
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
