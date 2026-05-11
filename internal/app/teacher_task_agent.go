// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"course-assistant/internal/ai"
	"course-assistant/internal/domain"
)

type teacherTaskAgentRequest struct {
	TaskType string
	Session  *authSession
	CourseID int
	Prompt   string
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	Mentions []teacherAgentMention
	Args     map[string]any
}

type teacherTaskAgentResult struct {
	Text   string
	Value  any
	Events []teacherAgentEvent
	Prompt string
}

func (s *Server) runTeacherTaskAgent(ctx context.Context, req teacherTaskAgentRequest) (teacherTaskAgentResult, error) {
	req.TaskType = strings.TrimSpace(req.TaskType)
	req.Prompt = truncateAgentText(req.Prompt)
	events := []teacherAgentEvent{{Type: "thinking", Title: "启动固定任务 Agent", Content: req.TaskType}}
	ctxText, ctxEvents := s.teacherTaskAgentContext(ctx, req)
	events = append(events, ctxEvents...)
	prompt := s.teacherTaskPrompt(req, ctxText)
	events = append(events, teacherAgentEvent{Type: "thinking", Title: "基于固定模板和工具上下文生成结果"})

	var out teacherTaskAgentResult
	out.Events = events
	out.Prompt = prompt
	var err error
	switch req.TaskType {
	case "quiz_generate":
		out.Text, err = s.aiClient.GenerateQuiz(ctx, prompt)
	case "quiz_initialize":
		in, _ := req.Args["initialize_input"].(ai.QuizInitializeInput)
		in.BasePrompt = strings.TrimSpace(prompt)
		out.Text, err = s.aiClient.InitializeQuiz(ctx, in)
	case "quiz_autofill":
		yaml := strings.TrimSpace(fmt.Sprint(req.Args["yaml"]))
		out.Text, err = s.aiClient.AutoFillQuiz(ctx, yaml+"\n\n# 可参考的课程上下文（不要修改已有题目，只用于补全解析和知识点）\n"+ctxText)
	case "class_summary":
		input, _ := req.Args["summary_input"].(ai.AdminSummarizeInput)
		out.Value, err = s.aiClient.AdminSummarize(ctx, input)
	case "history_summary":
		input, _ := req.Args["history_input"].(ai.HistorySummarizeInput)
		out.Value, err = s.aiClient.HistorySummarize(ctx, input)
	case "homework_feedback":
		input, _ := req.Args["homework_input"].(ai.HomeworkGradeFeedbackInput)
		input.TeacherNote = strings.TrimSpace(prompt)
		out.Text, err = s.aiClient.GenerateHomeworkFeedback(ctx, input)
	case "homework_pregrade":
		input, _ := req.Args["homework_input"].(ai.HomeworkGradeFeedbackInput)
		input.TeacherNote = strings.TrimSpace(prompt)
		out.Value, err = s.aiClient.PregradeHomework(ctx, input)
	default:
		err = fmt.Errorf("unknown teacher task agent: %s", req.TaskType)
	}
	if err != nil {
		events = append(events, teacherAgentEvent{Type: "tool_result", Title: "固定任务生成失败", Error: err.Error()})
		out.Events = events
		s.saveTeacherTaskTrajectory(ctx, req, out, err)
		return out, err
	}
	events = append(events, teacherAgentEvent{Type: "final", Title: "完成固定任务"})
	out.Events = events
	s.saveTeacherTaskTrajectory(ctx, req, out, nil)
	return out, nil
}

func (s *Server) teacherTaskPrompt(req teacherTaskAgentRequest, ctxText string) string {
	var b strings.Builder
	switch req.TaskType {
	case "quiz_generate":
		b.WriteString("固定任务：生成题库 YAML 草稿。请把教师需求和工具上下文结合起来，生成新的课堂小测题库；历史题库只作为风格、难度、知识覆盖参考，不要照抄原题。\n")
	case "quiz_initialize":
		b.WriteString("固定任务：基于指定课程资料初始化题库 YAML 草稿。请优先围绕已读取资料出题，并结合教师额外要求和历史上下文。\n")
	case "quiz_autofill":
		b.WriteString("固定任务：补全题库 YAML 的 explanation 和 knowledge_tag。不要修改已有题干、选项、答案和结构。\n")
	case "class_summary":
		b.WriteString("固定任务：生成课堂小测总结。请基于统计输入和工具上下文输出结构化总结。\n")
	case "history_summary":
		b.WriteString("固定任务：生成课程历史小测趋势总结。请基于历史统计和工具上下文输出结构化总结。\n")
	case "homework_feedback":
		b.WriteString("固定任务：为单个作业提交生成教师评语草稿。请结合报告正文、教师要求和课程上下文。\n")
	case "homework_pregrade":
		b.WriteString("固定任务：为作业提交生成 AI 预评。请按教师评分要求给出建议分和反馈。\n")
	default:
		b.WriteString("固定任务 Agent。\n")
	}
	if strings.TrimSpace(ctxText) != "" {
		b.WriteString("\n【Agent 按需读取的上下文】\n")
		b.WriteString(ctxText)
	}
	if strings.TrimSpace(req.Prompt) != "" {
		b.WriteString("\n\n【教师额外要求】\n")
		b.WriteString(req.Prompt)
	}
	if len(req.Messages) > 0 {
		b.WriteString("\n\n【最近对话】\n")
		start := 0
		if len(req.Messages) > teacherAgentMaxMessages {
			start = len(req.Messages) - teacherAgentMaxMessages
		}
		for _, msg := range req.Messages[start:] {
			role := strings.TrimSpace(msg.Role)
			if role != "assistant" {
				role = "teacher"
			}
			b.WriteString(fmt.Sprintf("%s: %s\n", role, truncateAgentText(msg.Content)))
		}
	}
	return b.String()
}

func (s *Server) teacherTaskAgentContext(ctx context.Context, req teacherTaskAgentRequest) (string, []teacherAgentEvent) {
	if req.Args == nil {
		req.Args = map[string]any{}
	}
	tc := agentToolContext{Session: req.Session, Platform: true, CourseID: req.CourseID}
	calls, events := s.planTeacherAgentMentionTools(ctx, req.Session, req.CourseID, req.Mentions)
	calls = append(calls, s.planTeacherTaskTools(ctx, req)...)
	calls = dedupePlannedAgentTools(calls)
	var b strings.Builder
	for _, call := range calls {
		events = append(events, teacherAgentEvent{Type: "tool_call", Title: "固定任务读取上下文 " + call.Name, Tool: call.Name, Args: call.Args})
		text, err := s.callAgentTool(ctx, call.Name, tc, call.Args)
		if err != nil {
			events = append(events, teacherAgentEvent{Type: "tool_result", Title: "上下文读取失败", Tool: call.Name, Error: err.Error()})
			continue
		}
		brief := text
		if len([]rune(brief)) > 500 {
			brief = string([]rune(brief)[:500]) + "..."
		}
		events = append(events, teacherAgentEvent{Type: "tool_result", Title: "已读取上下文", Tool: call.Name, Content: brief})
		b.WriteString("\n\n【工具 " + call.Name + "】\n")
		b.WriteString(text)
	}
	return b.String(), events
}

func (s *Server) planTeacherTaskTools(ctx context.Context, req teacherTaskAgentRequest) []plannedAgentTool {
	if req.CourseID <= 0 {
		return nil
	}
	base := map[string]any{"course_id": req.CourseID}
	var calls []plannedAgentTool
	switch req.TaskType {
	case "quiz_generate", "quiz_initialize", "quiz_autofill":
		calls = append(calls, plannedAgentTool{Name: "get_quiz_bank_list", Args: cloneAgentArgs(base)})
		for _, quizID := range s.detectQuizRefs(ctx, req) {
			args := cloneAgentArgs(base)
			args["quiz_id"] = quizID
			calls = append(calls, plannedAgentTool{Name: "read_quiz_bank_yaml", Args: args})
		}
		if req.TaskType != "quiz_autofill" {
			calls = append(calls, s.planTeacherAgentTools(req.Prompt, req.CourseID)...)
		}
	case "class_summary":
		calls = append(calls, plannedAgentTool{Name: "get_summary_stats", Args: cloneAgentArgs(base)}, plannedAgentTool{Name: "get_quiz_question_stats", Args: cloneAgentArgs(base)}, plannedAgentTool{Name: "get_quiz_feedback", Args: cloneAgentArgs(base)})
	case "history_summary":
		calls = append(calls, plannedAgentTool{Name: "get_course_context", Args: cloneAgentArgs(base)}, plannedAgentTool{Name: "get_quiz_bank_list", Args: cloneAgentArgs(base)})
	case "homework_feedback", "homework_pregrade":
		args := cloneAgentArgs(base)
		if assignmentID, ok := req.Args["assignment_id"].(string); ok && strings.TrimSpace(assignmentID) != "" {
			args["assignment_id"] = assignmentID
			calls = append(calls, plannedAgentTool{Name: "get_assignment_context", Args: args})
		}
	}
	if material, ok := req.Args["material_file"].(string); ok && strings.TrimSpace(material) != "" {
		args := cloneAgentArgs(base)
		args["material_file"] = material
		calls = append(calls, plannedAgentTool{Name: "read_material_text", Args: args})
	}
	return calls
}

func (s *Server) detectQuizRefs(ctx context.Context, req teacherTaskAgentRequest) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, m := range req.Mentions {
		if strings.EqualFold(strings.TrimSpace(m.Type), "quiz") {
			add(m.ID)
		}
	}
	re := regexp.MustCompile(`(?i)\b[a-z]+[a-z0-9_-]*\d+[a-z0-9_-]*\b`)
	for _, match := range re.FindAllString(req.Prompt, -1) {
		add(match)
	}
	text := strings.ToLower(req.Prompt)
	if strings.Contains(text, "上一") || strings.Contains(text, "上周") || strings.Contains(text, "历史") || strings.Contains(text, "参考") || strings.Contains(text, "实验小测") {
		if course, err := s.teacherMCPReadCourse(ctx, req.Session, req.CourseID); err == nil {
			items := s.agentQuizBankCandidates(course)
			if len(items) > 0 {
				add(items[len(items)-1].ID)
			}
		}
	}
	return out
}

func (s *Server) saveTeacherTaskTrajectory(ctx context.Context, req teacherTaskAgentRequest, out teacherTaskAgentResult, err error) {
	teacherLabel, courseLabel, nameLabel := s.teacherTrajectoryLabels(ctx, req.Session, req.CourseID)
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	s.saveAgentTrajectory(ctx, agentTrajectory{
		Kind:      "teacher_task:" + req.TaskType,
		CreatedAt: time.Now(),
		Teacher:   teacherLabel,
		Course:    courseLabel,
		Name:      nameLabel,
		Request: map[string]any{
			"task_type": req.TaskType,
			"course_id": req.CourseID,
			"prompt":    req.Prompt,
			"messages":  req.Messages,
			"mentions":  req.Mentions,
			"args":      req.Args,
		},
		Prompt:      out.Prompt,
		RawResponse: out.Text,
		Decision:    out.Value,
		Answer:      out.Text,
		Events:      out.Events,
		Error:       errText,
	})
}

func (s *Server) quizBankYAMLText(course *domain.Course, quizID string) (string, error) {
	dir := s.metadataQuizDir(course.TeacherID, course.Slug, quizID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext == ".yaml" || ext == ".yml" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", fmt.Errorf("题库 %s 没有 YAML 文件", quizID)
	}
	data, err := os.ReadFile(filepath.Join(dir, names[0]))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
