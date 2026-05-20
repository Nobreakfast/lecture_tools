// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"course-assistant/internal/domain"
)

const (
	teacherAgentMaxMessages         = 8
	teacherAgentMaxToolRounds       = 6
	teacherAgentMaxToolCalls        = 12
	teacherAgentMaxObservationRunes = 6000
	teacherAgentMaxTotalObsRunes    = 24000
)

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

type teacherAgentDecision struct {
	Action    string             `json:"action"`
	Answer    string             `json:"answer"`
	ToolCalls []plannedAgentTool `json:"tool_calls"`
}

type teacherAgentObservation struct {
	Tool    string         `json:"tool"`
	Args    map[string]any `json:"args"`
	Content string         `json:"content,omitempty"`
	Error   string         `json:"error,omitempty"`
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
		CourseID       string `json:"course_id"`
		ConversationID string `json:"conversation_id"`
		Message        string `json:"message"`
		Messages       []struct {
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

	convID := strings.TrimSpace(req.ConversationID)
	if convID == "" {
		convID = generateConversationID()
		title := string([]rune(req.Message)[:min(len([]rune(req.Message)), 30)])
		_ = s.store.CreateAgentConversation(r.Context(), &domain.AgentConversation{
			ID:        convID,
			TeacherID: sess.TeacherID,
			CourseID:  courseID,
			Title:     title,
		})
	}

	answer, events, err := s.runTeacherAgent(r.Context(), sess, courseID, req.Message, req.Messages, req.Mentions)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "events": events, "conversation_id": convID})
		return
	}

	eventsJSON, _ := json.Marshal(events)
	_ = s.store.CreateAgentMessage(r.Context(), &domain.AgentMessage{
		ConversationID: convID,
		Role:           "user",
		Content:        req.Message,
	})
	_ = s.store.CreateAgentMessage(r.Context(), &domain.AgentMessage{
		ConversationID: convID,
		Role:           "assistant",
		Content:        answer,
		Events:         string(eventsJSON),
	})

	writeJSON(w, map[string]any{"answer": answer, "events": events, "conversation_id": convID})
}

func generateConversationID() string {
	buf := make([]byte, 12)
	_, _ = crand.Read(buf)
	return hex.EncodeToString(buf)
}

func (s *Server) apiTeacherAgentConversations(w http.ResponseWriter, r *http.Request) {
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListAgentConversations(r.Context(), sess.TeacherID, 50)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []domain.AgentConversation{}
		}
		writeJSON(w, map[string]any{"conversations": items})
	case http.MethodPost:
		var req struct {
			CourseID int    `json:"course_id"`
			Title    string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		convID := generateConversationID()
		title := strings.TrimSpace(req.Title)
		if title == "" {
			title = "新对话"
		}
		conv := &domain.AgentConversation{
			ID:        convID,
			TeacherID: sess.TeacherID,
			CourseID:  req.CourseID,
			Title:     title,
		}
		if err := s.store.CreateAgentConversation(r.Context(), conv); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"conversation": conv})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) apiTeacherAgentConversationDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	convID := strings.TrimSpace(r.URL.Query().Get("id"))
	if convID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	conv, err := s.store.GetAgentConversation(r.Context(), convID)
	if err != nil || conv == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if conv.TeacherID != sess.TeacherID && sess.Role != domain.RoleAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	messages, err := s.store.ListAgentMessages(r.Context(), convID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if messages == nil {
		messages = []domain.AgentMessage{}
	}
	writeJSON(w, map[string]any{"conversation": conv, "messages": messages})
}

func (s *Server) apiTeacherAgentConversationDelete(w http.ResponseWriter, r *http.Request) {
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
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	convID := strings.TrimSpace(req.ID)
	if convID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	conv, err := s.store.GetAgentConversation(r.Context(), convID)
	if err != nil || conv == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if conv.TeacherID != sess.TeacherID && sess.Role != domain.RoleAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := s.store.DeleteAgentConversation(r.Context(), convID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
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
	events := []teacherAgentEvent{{Type: "thinking", Title: "理解教师问题并规划数据读取"}}
	tc := agentToolContext{Session: sess, Platform: true, CourseID: courseID}
	registry := s.agentTools()
	var observations []teacherAgentObservation
	var toolCalls []plannedAgentTool
	var prompt string
	var answer string
	var rawResponse string
	callCount := 0
	for round := 0; round < teacherAgentMaxToolRounds; round++ {
		forceFinal := round == teacherAgentMaxToolRounds-1 || callCount >= teacherAgentMaxToolCalls
		prompt = s.teacherAgentToolLoopPrompt(registry.ModelTools(), observations, latest, messages, mentions, courseID, forceFinal)
		if forceFinal {
			events = append(events, teacherAgentEvent{Type: "thinking", Title: "基于已读取数据生成回复"})
		} else {
			events = append(events, teacherAgentEvent{Type: "thinking", Title: "决定下一步要读取的数据"})
		}
		decision, raw, err := s.teacherAgentModelDecision(ctx, prompt)
		rawResponse = raw
		if err != nil {
			events = append(events, teacherAgentEvent{Type: "tool_result", Title: "AI 工具规划解析失败", Error: err.Error()})
			if strings.TrimSpace(raw) == "" {
				return s.failTeacherAgentRun(ctx, sess, courseID, latest, messages, mentions, toolCalls, prompt, events, err)
			}
			answer = "我没有稳定解析出下一步工具调用。请稍微换一种问法，或补充课程/学生/小测范围后再试。"
			break
		}
		switch decision.Action {
		case "final":
			answer = strings.TrimSpace(decision.Answer)
			if answer == "" {
				answer = "已读取到相关数据，但没有形成有效结论。请补充你想看的具体范围。"
			}
			events = append(events, teacherAgentEvent{Type: "final", Title: "完成"})
			return s.finishTeacherAgentRun(ctx, sess, courseID, latest, messages, mentions, toolCalls, prompt, rawResponse, answer, events)
		case "call_tools":
			if forceFinal {
				events = append(events, teacherAgentEvent{Type: "tool_result", Title: "已达到工具调用上限", Error: "停止继续读取，准备基于现有数据回答"})
				continue
			}
			if len(decision.ToolCalls) == 0 {
				observations = append(observations, teacherAgentObservation{Tool: "system", Error: "AI 请求调用工具但未提供 tool_calls"})
				continue
			}
			for _, call := range decision.ToolCalls {
				if callCount >= teacherAgentMaxToolCalls {
					break
				}
				call.Name = strings.TrimSpace(call.Name)
				if call.Args == nil {
					call.Args = map[string]any{}
				}
				tool, ok := registry.Tool(call.Name)
				if !ok || !tool.ModelVisible || tool.Kind != agentToolTeacherRead {
					msg := "该工具不可由教师 Agent 自动调用"
					events = append(events, teacherAgentEvent{Type: "tool_result", Title: "工具调用被拒绝", Tool: call.Name, Args: call.Args, Error: msg})
					observations = append(observations, teacherAgentObservation{Tool: call.Name, Args: call.Args, Error: msg})
					continue
				}
				toolCalls = append(toolCalls, call)
				callCount++
				events = append(events, teacherAgentEvent{Type: "tool_call", Title: "读取数据：" + call.Name, Tool: call.Name, Args: call.Args})
				text, err := s.callAgentTool(ctx, call.Name, tc, call.Args)
				if err != nil {
					events = append(events, teacherAgentEvent{Type: "tool_result", Title: "读取失败", Tool: call.Name, Error: err.Error()})
					observations = append(observations, teacherAgentObservation{Tool: call.Name, Args: call.Args, Error: err.Error()})
					continue
				}
				brief := teacherAgentBriefToolResult(text)
				events = append(events, teacherAgentEvent{Type: "tool_result", Title: "已读取数据", Tool: call.Name, Content: brief})
				observations = append(observations, teacherAgentObservation{Tool: call.Name, Args: call.Args, Content: teacherAgentTrimObservation(text)})
			}
		default:
			observations = append(observations, teacherAgentObservation{Tool: "system", Error: "AI 返回了未知 action：" + decision.Action})
		}
	}
	if strings.TrimSpace(answer) == "" {
		prompt = s.teacherAgentToolLoopPrompt(registry.ModelTools(), observations, latest, messages, mentions, courseID, true)
		events = append(events, teacherAgentEvent{Type: "thinking", Title: "基于已读取数据生成回复"})
		decision, raw, err := s.teacherAgentModelDecision(ctx, prompt)
		rawResponse = raw
		if err == nil && decision.Action == "final" && strings.TrimSpace(decision.Answer) != "" {
			answer = strings.TrimSpace(decision.Answer)
		} else if strings.TrimSpace(raw) != "" && !strings.HasPrefix(strings.TrimSpace(raw), "{") {
			answer = strings.TrimSpace(raw)
		} else {
			answer = "我已经尝试读取相关课程数据，但还没有得到足够明确的结果。请补充课程、学生学号/班级或小测/作业编号后再问一次。"
		}
	}
	events = append(events, teacherAgentEvent{Type: "final", Title: "完成"})
	return s.finishTeacherAgentRun(ctx, sess, courseID, latest, messages, mentions, toolCalls, prompt, rawResponse, answer, events)
}

func (s *Server) failTeacherAgentRun(ctx context.Context, sess *authSession, courseID int, latest string, messages []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}, mentions []teacherAgentMention, toolCalls []plannedAgentTool, prompt string, events []teacherAgentEvent, err error) (string, []teacherAgentEvent, error) {
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

func (s *Server) finishTeacherAgentRun(ctx context.Context, sess *authSession, courseID int, latest string, messages []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}, mentions []teacherAgentMention, toolCalls []plannedAgentTool, prompt, rawResponse, answer string, events []teacherAgentEvent) (string, []teacherAgentEvent, error) {
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
		RawResponse: rawResponse,
		Answer:      answer,
		Events:      events,
	})
	return answer, events, nil
}

func (s *Server) teacherAgentModelDecision(ctx context.Context, prompt string) (teacherAgentDecision, string, error) {
	chat := s.teacherAgentChat
	if chat == nil && s.aiClient != nil {
		chat = s.aiClient.TeacherAgentChat
	}
	if chat == nil {
		return teacherAgentDecision{}, "", fmt.Errorf("AI 未配置")
	}
	raw, err := chat(ctx, prompt)
	if err != nil {
		return teacherAgentDecision{}, raw, err
	}
	decision, parseErr := parseTeacherAgentDecision(raw)
	if parseErr == nil {
		return decision, raw, nil
	}
	repairPrompt := prompt + "\n\n上一轮输出无法解析为规定 JSON。请只修正格式，严格返回 {\"action\":\"call_tools\",\"tool_calls\":[...]} 或 {\"action\":\"final\",\"answer\":\"...\"}。上一轮输出：\n" + truncateAgentText(raw)
	repaired, repairErr := chat(ctx, repairPrompt)
	if repairErr != nil {
		return teacherAgentDecision{}, repaired, repairErr
	}
	decision, parseErr = parseTeacherAgentDecision(repaired)
	return decision, repaired, parseErr
}

func (s *Server) teacherAgentToolLoopPrompt(tools []agentTool, observations []teacherAgentObservation, latest string, messages []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}, mentions []teacherAgentMention, courseID int, forceFinal bool) string {
	var b strings.Builder
	b.WriteString("你是教师课程数据 Agent。你可以分多步读取平台数据，然后回答教师问题。\n")
	b.WriteString("必须严格输出一个 JSON 对象，不要输出 Markdown 代码块、解释前缀或后缀。\n")
	if forceFinal {
		b.WriteString("本轮必须给最终回答，格式为：{\"action\":\"final\",\"answer\":\"...\"}。\n")
	} else {
		b.WriteString("如果需要读取数据，格式为：{\"action\":\"call_tools\",\"tool_calls\":[{\"name\":\"工具名\",\"args\":{}}]}。\n")
		b.WriteString("如果已有数据足够回答，格式为：{\"action\":\"final\",\"answer\":\"...\"}。\n")
	}
	b.WriteString("只能调用工具清单里的只读工具。不要编造工具结果；数据不足时说明还缺什么。\n")
	b.WriteString("course_id 省略时，工具会在当前教师可访问的全部课程中查询；如同名学生无法唯一判断，请先搜索并在回答中说明需要班级或学号。\n")
	b.WriteString("优先使用通用工具逐步定位：search_course_data -> list_* -> read_*。不要使用未列出的旧高层工具。\n\n")

	toolSpecs := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		params := make([]agentToolParam, len(tool.Params))
		copy(params, tool.Params)
		toolSpecs = append(toolSpecs, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"params":      params,
		})
	}
	if body, err := json.MarshalIndent(toolSpecs, "", "  "); err == nil {
		b.WriteString("可用工具：\n")
		b.Write(body)
		b.WriteString("\n\n")
	}
	if courseID > 0 {
		b.WriteString(fmt.Sprintf("当前页面课程ID：%d\n", courseID))
	} else {
		b.WriteString("当前页面未限定课程；可以跨该教师可访问课程查询。\n")
	}
	if len(mentions) > 0 {
		if body, err := json.MarshalIndent(mentions, "", "  "); err == nil {
			b.WriteString("教师显式引用：\n")
			b.Write(body)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n最近对话：\n")
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
		if role != "assistant" {
			role = "teacher"
		}
		b.WriteString(fmt.Sprintf("%s: %s\n", role, truncateAgentText(content)))
	}
	b.WriteString("teacher: ")
	b.WriteString(latest)
	b.WriteString("\n\n已读取的数据：\n")
	if len(observations) == 0 {
		b.WriteString("[]\n")
	} else if body, err := json.MarshalIndent(teacherAgentTrimObservations(observations), "", "  "); err == nil {
		b.Write(body)
		b.WriteString("\n")
	}
	return b.String()
}

func parseTeacherAgentDecision(raw string) (teacherAgentDecision, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "```"))
	if raw == "" {
		return teacherAgentDecision{}, fmt.Errorf("AI 返回为空")
	}
	candidates := []string{raw}
	if idx := strings.Index(raw, "{"); idx > 0 {
		candidates = append(candidates, raw[idx:])
	}
	var firstErr error
	for _, candidate := range candidates {
		var decision teacherAgentDecision
		decoder := json.NewDecoder(strings.NewReader(candidate))
		decoder.UseNumber()
		if err := decoder.Decode(&decision); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		decision.Action = strings.TrimSpace(decision.Action)
		if decision.Action != "call_tools" && decision.Action != "final" {
			return teacherAgentDecision{}, fmt.Errorf("未知 action: %s", decision.Action)
		}
		return decision, nil
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("无法解析 JSON")
	}
	return teacherAgentDecision{}, firstErr
}

func teacherAgentBriefToolResult(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > 500 {
		return string(runes[:500]) + "..."
	}
	return text
}

func teacherAgentTrimObservation(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > teacherAgentMaxObservationRunes {
		return string(runes[:teacherAgentMaxObservationRunes]) + "..."
	}
	return text
}

func teacherAgentTrimObservations(in []teacherAgentObservation) []teacherAgentObservation {
	if len(in) == 0 {
		return nil
	}
	out := make([]teacherAgentObservation, 0, len(in))
	used := 0
	for i := len(in) - 1; i >= 0; i-- {
		item := in[i]
		used += len([]rune(item.Content)) + len([]rune(item.Error))
		if used > teacherAgentMaxTotalObsRunes && len(out) > 0 {
			break
		}
		out = append(out, item)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (s *Server) planTeacherAgentMentionTools(ctx context.Context, sess *authSession, fallbackCourseID int, mentions []teacherAgentMention, latestMsg string) ([]plannedAgentTool, []teacherAgentEvent) {
	deepAnalysis := needsDeepStudentAnalysis(latestMsg)
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
			homeworkFocus := needsStudentHomework(latestMsg)
			quizResponseFocus := needsStudentQuizResponses(latestMsg)
			if quizResponseFocus {
				qArgs := cloneAgentArgs(args)
				qArgs["question_selector"] = studentQuizQuestionSelector(latestMsg)
				calls = append(calls, plannedAgentTool{Name: "get_student_quiz_responses", Args: qArgs})
			}
			if deepAnalysis {
				calls = append(calls, plannedAgentTool{Name: "get_student_deep_analysis", Args: cloneAgentArgs(args)})
				if homeworkFocus {
					calls = append(calls, plannedAgentTool{Name: "get_student_homework", Args: cloneAgentArgs(args)})
				}
				calls = append(calls, plannedAgentTool{Name: "draft_student_analysis", Args: args})
			} else if homeworkFocus {
				calls = append(calls, plannedAgentTool{Name: "get_student_homework", Args: args})
			} else if !quizResponseFocus {
				calls = append(calls, plannedAgentTool{Name: "get_student_profile", Args: args})
			}
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

func (s *Server) planTeacherAgentImplicitStudentTools(ctx context.Context, sess *authSession, courseID int, latest string) []plannedAgentTool {
	if sess == nil || courseID <= 0 {
		return nil
	}
	deepAnalysis := needsDeepStudentAnalysis(latest)
	homeworkFocus := needsStudentHomework(latest)
	quizResponseFocus := needsStudentQuizResponses(latest)
	if !deepAnalysis && !homeworkFocus && !quizResponseFocus {
		return nil
	}
	items, err := s.agentMentionCandidates(ctx, sess, courseID, "", 200)
	if err != nil {
		return nil
	}
	var calls []plannedAgentTool
	for _, item := range items {
		if item.Type != "student" || !agentStudentCandidateMentioned(latest, item) {
			continue
		}
		args := map[string]any{"course_id": item.CourseID, "student_id": item.ID}
		for k, v := range item.Meta {
			args[k] = v
		}
		if quizResponseFocus {
			qArgs := cloneAgentArgs(args)
			qArgs["question_selector"] = studentQuizQuestionSelector(latest)
			calls = append(calls, plannedAgentTool{Name: "get_student_quiz_responses", Args: qArgs})
		}
		if deepAnalysis {
			calls = append(calls, plannedAgentTool{Name: "get_student_deep_analysis", Args: cloneAgentArgs(args)})
			if homeworkFocus {
				calls = append(calls, plannedAgentTool{Name: "get_student_homework", Args: cloneAgentArgs(args)})
			}
			calls = append(calls, plannedAgentTool{Name: "draft_student_analysis", Args: args})
		} else if homeworkFocus {
			calls = append(calls, plannedAgentTool{Name: "get_student_homework", Args: args})
		}
		if len(calls) >= 3 {
			break
		}
	}
	return dedupePlannedAgentTools(calls)
}

func needsDeepStudentAnalysis(msg string) bool {
	text := strings.ToLower(msg)
	keywords := []string{
		"分析", "画像", "深度", "心理", "不参与", "不理",
		"沉默", "低参与", "不回应", "不配合", "不互动",
		"不活跃", "消极", "退出", "缺席", "旷课",
		"情况分析", "行为", "态度", "状态", "关注",
	}
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func needsStudentHomework(msg string) bool {
	text := strings.ToLower(msg)
	keywords := []string{
		"作业", "提交", "报告", "评分", "评语", "预评", "批改",
		"homework", "assignment", "submission", "grade", "feedback",
	}
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func needsStudentQuizResponses(msg string) bool {
	text := strings.ToLower(msg)
	hasQuiz := strings.Contains(text, "小测") || strings.Contains(text, "测验") || strings.Contains(text, "题") || strings.Contains(text, "quiz")
	if !hasQuiz {
		return false
	}
	keywords := []string{"最后一题", "最后", "反馈", "回答", "作答", "内容", "建议", "不懂", "问卷", "简答", "response", "answer", "feedback"}
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func studentQuizQuestionSelector(msg string) string {
	text := strings.ToLower(msg)
	if strings.Contains(text, "全部题") || strings.Contains(text, "所有题") || strings.Contains(text, "all") {
		return "all"
	}
	if strings.Contains(text, "反馈") || strings.Contains(text, "问卷") || strings.Contains(text, "建议") || strings.Contains(text, "不懂") || strings.Contains(text, "feedback") {
		if strings.Contains(text, "最后") {
			return "last"
		}
		return "feedback"
	}
	return "last"
}

func agentStudentCandidateMentioned(text string, item agentMentionCandidate) bool {
	hay := strings.ToLower(strings.TrimSpace(text))
	if hay == "" {
		return false
	}
	if studentNo := strings.ToLower(strings.TrimSpace(fmt.Sprint(item.Meta["student_no"]))); studentNo != "" && strings.Contains(hay, studentNo) {
		return true
	}
	name := strings.TrimSpace(fmt.Sprint(item.Meta["name"]))
	if name == "" {
		name = strings.TrimSpace(item.Label)
	}
	if len([]rune(name)) >= 2 && strings.Contains(hay, strings.ToLower(name)) {
		return true
	}
	parts := strings.Split(item.ID, "|")
	if len(parts) > 0 {
		rawNo := strings.ToLower(strings.TrimSpace(parts[0]))
		if rawNo != "" && strings.Contains(hay, rawNo) {
			return true
		}
	}
	return false
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

func plannedAgentToolsContain(in []plannedAgentTool, name string) bool {
	for _, item := range in {
		if item.Name == name {
			return true
		}
	}
	return false
}

func filterPlannedAgentTools(in []plannedAgentTool, skip map[string]bool) []plannedAgentTool {
	out := make([]plannedAgentTool, 0, len(in))
	for _, item := range in {
		if skip[item.Name] {
			continue
		}
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
				score = fmt.Sprintf("%s/%d", formatScoreValue(item.Correct), item.Total)
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
