// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"course-assistant/internal/domain"
)

const teacherAgentMaxMessages = 8

type teacherAgentEvent struct {
	Type    string         `json:"type"`
	Title   string         `json:"title,omitempty"`
	Tool    string         `json:"tool,omitempty"`
	Args    map[string]any `json:"args,omitempty"`
	Content string         `json:"content,omitempty"`
	Error   string         `json:"error,omitempty"`
}

type teacherAgentMention struct {
	Type     string         `json:"type"`
	ID       string         `json:"id"`
	Label    string         `json:"label"`
	CourseID int            `json:"course_id"`
	Meta     map[string]any `json:"meta"`
}

func (s *Server) apiTeacherAgentChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		CourseID string `json:"course_id"`
		Message  string `json:"message"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Mentions []teacherAgentMention `json:"mentions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		http.Error(w, "问题不能为空", http.StatusBadRequest)
		return
	}
	req.Message = truncateAgentText(req.Message)

	courseID, _ := strconv.Atoi(strings.TrimSpace(req.CourseID))
	answer, events, err := s.runTeacherAgent(r.Context(), sess, courseID, req.Message, req.Messages, req.Mentions)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "events": events})
		return
	}
	writeJSON(w, map[string]any{"answer": answer, "events": events})
}

func (s *Server) apiTeacherAgentMentions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	courseID, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("course_id")))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if limit <= 0 || limit > 120 {
		limit = 80
	}
	items, err := s.agentMentionCandidates(r.Context(), sess, courseID, strings.ToLower(query), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) runTeacherAgent(ctx context.Context, sess *authSession, courseID int, latest string, messages []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}, mentions []teacherAgentMention) (string, []teacherAgentEvent, error) {
	events := []teacherAgentEvent{{Type: "thinking", Title: "理解教师问题并选择可用工具"}}
	tc := agentToolContext{Session: sess, Platform: true, CourseID: courseID}
	toolCalls, mentionEvents := s.planTeacherAgentMentionTools(ctx, sess, courseID, mentions)
	events = append(events, mentionEvents...)
	toolCalls = append(toolCalls, s.planTeacherAgentTools(latest, courseID)...)
	if len(toolCalls) == 0 {
		toolCalls = append(toolCalls, plannedAgentTool{Name: "list_courses", Args: map[string]any{}})
		if courseID > 0 {
			toolCalls = append(toolCalls, plannedAgentTool{Name: "get_course_context", Args: map[string]any{"course_id": courseID}})
		}
	}
	var ctxText strings.Builder
	for _, call := range toolCalls {
		events = append(events, teacherAgentEvent{Type: "tool_call", Title: "调用工具 " + call.Name, Tool: call.Name, Args: call.Args})
		text, err := s.callAgentTool(ctx, call.Name, tc, call.Args)
		if err != nil {
			events = append(events, teacherAgentEvent{Type: "tool_result", Title: "工具调用失败", Tool: call.Name, Error: err.Error()})
			continue
		}
		brief := text
		if len([]rune(brief)) > 500 {
			brief = string([]rune(brief)[:500]) + "..."
		}
		events = append(events, teacherAgentEvent{Type: "tool_result", Title: "已获得工具结果", Tool: call.Name, Content: brief})
		ctxText.WriteString("\n\n【工具 " + call.Name + "】\n")
		ctxText.WriteString(text)
	}
	if strings.TrimSpace(ctxText.String()) == "" {
		ctxText.WriteString("没有可用工具结果。")
	}
	prompt := s.teacherAgentPrompt(ctxText.String(), latest, messages)
	events = append(events, teacherAgentEvent{Type: "thinking", Title: "基于工具结果生成回复"})
	answer, err := s.aiClient.TeacherAgentChat(ctx, prompt)
	if err != nil {
		teacherLabel, courseLabel, nameLabel := s.teacherTrajectoryLabels(ctx, sess, courseID)
		s.saveAgentTrajectory(ctx, agentTrajectory{
			Kind:      "teacher",
			CreatedAt: time.Now(),
			Teacher:   teacherLabel,
			Course:    courseLabel,
			Name:      nameLabel,
			Request: map[string]any{
				"course_id":  courseID,
				"message":    latest,
				"messages":   messages,
				"mentions":   mentions,
				"tool_calls": toolCalls,
			},
			Prompt: prompt,
			Events: events,
			Error:  err.Error(),
		})
		return "", events, err
	}
	events = append(events, teacherAgentEvent{Type: "final", Title: "完成"})
	teacherLabel, courseLabel, nameLabel := s.teacherTrajectoryLabels(ctx, sess, courseID)
	s.saveAgentTrajectory(ctx, agentTrajectory{
		Kind:      "teacher",
		CreatedAt: time.Now(),
		Teacher:   teacherLabel,
		Course:    courseLabel,
		Name:      nameLabel,
		Request: map[string]any{
			"course_id":  courseID,
			"message":    latest,
			"messages":   messages,
			"mentions":   mentions,
			"tool_calls": toolCalls,
		},
		Prompt:      prompt,
		RawResponse: answer,
		Answer:      answer,
		Events:      events,
	})
	return answer, events, nil
}

func (s *Server) planTeacherAgentMentionTools(ctx context.Context, sess *authSession, fallbackCourseID int, mentions []teacherAgentMention) ([]plannedAgentTool, []teacherAgentEvent) {
	var calls []plannedAgentTool
	var events []teacherAgentEvent
	for _, mention := range mentions {
		mtype := strings.ToLower(strings.TrimSpace(mention.Type))
		mid := strings.TrimSpace(mention.ID)
		if mtype == "" || mid == "" {
			continue
		}
		courseID := mention.CourseID
		if courseID <= 0 {
			courseID = fallbackCourseID
		}
		if courseID <= 0 {
			events = append(events, teacherAgentEvent{Type: "tool_result", Title: "忽略无课程范围的 @" + mtype, Error: mention.Label})
			continue
		}
		if _, err := s.teacherMCPReadCourse(ctx, sess, courseID); err != nil {
			events = append(events, teacherAgentEvent{Type: "tool_result", Title: "无权限读取 @" + mention.Label, Error: err.Error()})
			continue
		}
		events = append(events, teacherAgentEvent{Type: "thinking", Title: "识别引用 @" + mention.Label, Content: mtype})
		base := map[string]any{"course_id": courseID}
		switch mtype {
		case "course":
			calls = append(calls, plannedAgentTool{Name: "get_course_context", Args: base})
		case "quiz":
			args := cloneAgentArgs(base)
			args["quiz_id"] = mid
			calls = append(calls,
				plannedAgentTool{Name: "get_quiz_question_stats", Args: cloneAgentArgs(args)},
				plannedAgentTool{Name: "get_quiz_feedback", Args: cloneAgentArgs(args)},
			)
		case "material":
			args := cloneAgentArgs(base)
			args["material_file"] = mid
			calls = append(calls, plannedAgentTool{Name: "read_material_text", Args: args})
		case "student":
			args := cloneAgentArgs(base)
			args["student_id"] = mid
			for k, v := range mention.Meta {
				args[k] = v
			}
			calls = append(calls, plannedAgentTool{Name: "get_student_profile", Args: args})
		case "assignment":
			args := cloneAgentArgs(base)
			args["assignment_id"] = mid
			calls = append(calls,
				plannedAgentTool{Name: "get_assignment_context", Args: cloneAgentArgs(args)},
				plannedAgentTool{Name: "get_homework_submissions", Args: cloneAgentArgs(args)},
				plannedAgentTool{Name: "get_qa_issues", Args: cloneAgentArgs(args)},
			)
		case "qa_issue":
			args := cloneAgentArgs(base)
			args["status"] = "all"
			args["max_messages"] = 8
			if aid, ok := mention.Meta["assignment_id"]; ok {
				args["assignment_id"] = aid
			}
			calls = append(calls, plannedAgentTool{Name: "get_qa_issues", Args: args})
		case "attempt":
			args := cloneAgentArgs(base)
			args["attempt_id"] = mid
			calls = append(calls, plannedAgentTool{Name: "get_attempt_detail", Args: args})
		}
	}
	return dedupePlannedAgentTools(calls), events
}

type plannedAgentTool struct {
	Name string
	Args map[string]any
}

func (s *Server) planTeacherAgentTools(latest string, courseID int) []plannedAgentTool {
	text := strings.ToLower(latest)
	args := map[string]any{}
	if courseID > 0 {
		args["course_id"] = courseID
	}
	var calls []plannedAgentTool
	if courseID <= 0 || strings.Contains(text, "课程") || strings.Contains(text, "邀请码") || strings.Contains(text, "list") {
		calls = append(calls, plannedAgentTool{Name: "list_courses", Args: map[string]any{}})
	}
	if courseID > 0 && (strings.Contains(text, "概览") || strings.Contains(text, "情况") || strings.Contains(text, "总结") || strings.Contains(text, "表现")) {
		calls = append(calls, plannedAgentTool{Name: "get_course_context", Args: cloneAgentArgs(args)})
	}
	if courseID > 0 && (strings.Contains(text, "逐题") || strings.Contains(text, "正确率") || strings.Contains(text, "错题") || strings.Contains(text, "薄弱")) {
		calls = append(calls, plannedAgentTool{Name: "get_quiz_question_stats", Args: cloneAgentArgs(args)})
	}
	if courseID > 0 && (strings.Contains(text, "问卷") || strings.Contains(text, "反馈") || strings.Contains(text, "简答")) {
		calls = append(calls, plannedAgentTool{Name: "get_quiz_feedback", Args: cloneAgentArgs(args)})
	}
	if courseID > 0 && (strings.Contains(text, "作业") || strings.Contains(text, "提交") || strings.Contains(text, "报告")) {
		calls = append(calls, plannedAgentTool{Name: "get_homework_submissions", Args: cloneAgentArgs(args)})
	}
	if courseID > 0 && (strings.Contains(text, "资料") || strings.Contains(text, "课件") || strings.Contains(text, "pdf")) {
		calls = append(calls, plannedAgentTool{Name: "list_materials", Args: cloneAgentArgs(args)})
	}
	if courseID > 0 && (strings.Contains(text, "q&a") || strings.Contains(text, "qa") || strings.Contains(text, "问题")) {
		calls = append(calls, plannedAgentTool{Name: "get_qa_issues", Args: cloneAgentArgs(args)})
	}
	return dedupePlannedAgentTools(calls)
}

func cloneAgentArgs(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func dedupePlannedAgentTools(in []plannedAgentTool) []plannedAgentTool {
	seen := map[string]bool{}
	out := make([]plannedAgentTool, 0, len(in))
	for _, item := range in {
		key := item.Name + fmt.Sprint(item.Args)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func (s *Server) teacherAgentPrompt(ctxText, latest string, messages []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}) string {
	var b strings.Builder
	b.WriteString("以下是教师 Agent 按需调用平台工具得到的结果。请只基于这些工具结果回答；不要声称已经执行未确认的写操作。\n\n")
	b.WriteString(ctxText)
	b.WriteString("\n\n最近对话：\n")
	start := 0
	if len(messages) > teacherAgentMaxMessages {
		start = len(messages) - teacherAgentMaxMessages
	}
	for _, msg := range messages[start:] {
		role := strings.TrimSpace(msg.Role)
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		content = truncateAgentText(content)
		if role != "assistant" {
			role = "teacher"
		}
		b.WriteString(fmt.Sprintf("%s: %s\n", role, content))
	}
	b.WriteString("teacher: ")
	b.WriteString(latest)
	return b.String()
}

func (s *Server) teacherAgentContext(r *http.Request, sess *authSession, courseID string) (string, error) {
	if courseID != "" {
		q := r.URL.Query()
		q.Set("course_id", courseID)
		r2 := r.Clone(r.Context())
		r2.Method = http.MethodGet
		r2.URL.RawQuery = q.Encode()
		_, course, err := s.resolveTeacherCourse(r2, sess)
		if err != nil {
			return "", err
		}
		return s.teacherAgentCourseContext(r.Context(), course), nil
	}

	courses, err := s.store.ListCoursesByTeacher(r.Context(), sess.TeacherID)
	if err != nil {
		return "", fmt.Errorf("读取课程列表失败")
	}
	sort.Slice(courses, func(i, j int) bool { return courses[i].ID < courses[j].ID })
	if len(courses) == 0 {
		return "该教师暂无课程。", nil
	}
	if len(courses) > 5 {
		courses = courses[:5]
	}
	var b strings.Builder
	b.WriteString("教师课程概览（如需更详细分析，建议教师先选择具体课程）：\n")
	for _, course := range courses {
		b.WriteString("\n")
		b.WriteString(s.teacherAgentCourseContext(r.Context(), &course))
	}
	return b.String(), nil
}

func (s *Server) teacherAgentCourseContext(ctx context.Context, course *domain.Course) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("课程：%s（ID: %d，标识: %s）\n", teacherAgentCourseName(course), course.ID, course.Slug))

	s.quizMu.RLock()
	q := s.courseQuizzes[course.ID]
	s.quizMu.RUnlock()
	if q != nil {
		b.WriteString(fmt.Sprintf("当前加载小测：%s - %s，共 %d 题。\n", q.QuizID, q.Title, len(q.Questions)))
	} else {
		b.WriteString("当前未加载小测。\n")
	}

	attempts, err := s.store.ListAttemptsByCourse(ctx, course.ID)
	if err != nil {
		b.WriteString("小测记录：读取失败。\n")
	} else {
		bestItems := s.teacherCourseBestAttempts(ctx, course, q, attempts)
		b.WriteString(fmt.Sprintf("小测记录：共 %d 名/组学生记录。\n", len(bestItems)))
		limit := len(bestItems)
		if limit > 40 {
			limit = 40
		}
		for i := 0; i < limit; i++ {
			item := bestItems[i]
			a := item.Attempt
			score := "未计分"
			if item.QuizLoaded {
				score = fmt.Sprintf("%d/%d", item.Correct, item.Total)
			}
			b.WriteString(fmt.Sprintf("- 学生：%s，学号：%s，班级：%s，小测：%s，状态：%s，次数：%d，得分：%s，更新时间：%s\n",
				a.Name, a.StudentNo, a.ClassName, a.QuizID, a.Status, a.AttemptNo, score, a.UpdatedAt.Format("2006-01-02 15:04")))
		}
		if len(bestItems) > limit {
			b.WriteString(fmt.Sprintf("- 其余 %d 条小测记录已省略。\n", len(bestItems)-limit))
		}
	}

	homework, err := s.store.ListHomeworkSubmissions(ctx, course.ID, course.Slug, "")
	if err != nil {
		b.WriteString("作业提交：读取失败。\n")
	} else {
		b.WriteString(fmt.Sprintf("作业提交：共 %d 条。\n", len(homework)))
		limit := len(homework)
		if limit > 30 {
			limit = 30
		}
		for i := 0; i < limit; i++ {
			h := homework[i]
			score := "未评分"
			if h.Score != nil {
				score = fmt.Sprintf("%.1f", *h.Score)
			}
			b.WriteString(fmt.Sprintf("- 作业：%s，学生：%s，学号：%s，班级：%s，文件：%s，评分：%s，更新时间：%s\n",
				h.AssignmentID, h.Name, h.StudentNo, h.ClassName, teacherAgentHomeworkFiles(h), score, h.UpdatedAt.Format("2006-01-02 15:04")))
		}
		if len(homework) > limit {
			b.WriteString(fmt.Sprintf("- 其余 %d 条作业记录已省略。\n", len(homework)-limit))
		}
	}

	materials, err := s.scanMaterialsFromDir(s.metadataMaterialsDir(course.TeacherID, course.Slug), course.Slug, true)
	if err == nil && len(materials) > 0 {
		b.WriteString(fmt.Sprintf("课程资料：共 %d 个文件。\n", len(materials)))
		limit := len(materials)
		if limit > 20 {
			limit = 20
		}
		for i := 0; i < limit; i++ {
			m := materials[i]
			files := make([]string, 0, len(m.Downloads))
			for _, d := range m.Downloads {
				files = append(files, d.File)
			}
			b.WriteString(fmt.Sprintf("- %s：%s\n", m.Stem, strings.Join(files, "、")))
		}
	}
	return b.String()
}

func teacherAgentHomeworkFiles(h domain.HomeworkSubmission) string {
	files := make([]string, 0, 3)
	if h.ReportOriginalName != "" {
		files = append(files, "报告")
	}
	if h.CodeOriginalName != "" {
		files = append(files, "代码")
	}
	if h.ExtraOriginalName != "" {
		files = append(files, "附件")
	}
	if len(files) == 0 {
		return "未上传"
	}
	return strings.Join(files, "/")
}

func teacherAgentCourseName(course *domain.Course) string {
	if strings.TrimSpace(course.DisplayName) != "" {
		return strings.TrimSpace(course.DisplayName)
	}
	if strings.TrimSpace(course.Name) != "" {
		return strings.TrimSpace(course.Name)
	}
	return strings.TrimSpace(course.Slug)
}
