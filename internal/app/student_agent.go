// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"course-assistant/internal/domain"
)

const (
	studentAgentMaxMessages = 8
	agentMaxMessageRunes    = 2000
)

type studentAgentDecision struct {
	Action    string `json:"action"`
	Answer    string `json:"answer"`
	QATitle   string `json:"qa_title"`
	QASummary string `json:"qa_summary"`
}

func (s *Server) apiStudentAgentChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	submission, err := s.requireHomeworkStudent(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Message  string `json:"message"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
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

	prompt := s.studentAgentPrompt(r, submission, req.Message, req.Messages)
	raw, err := s.aiClient.StudentAgentChat(r.Context(), prompt)
	if err != nil {
		teacherLabel, courseLabel, nameLabel := s.studentTrajectoryLabels(r.Context(), submission)
		s.saveAgentTrajectory(r.Context(), agentTrajectory{
			Kind:      "student",
			CreatedAt: time.Now(),
			Teacher:   teacherLabel,
			Course:    courseLabel,
			Name:      nameLabel,
			Request: map[string]any{
				"message":       req.Message,
				"messages":      req.Messages,
				"submission_id": submission.ID,
				"assignment_id": submission.AssignmentID,
				"student_no":    submission.StudentNo,
			},
			Prompt: prompt,
			Error:  err.Error(),
		})
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	decision, ok := parseStudentAgentDecision(raw)
	if !ok {
		answer := strings.TrimSpace(raw)
		teacherLabel, courseLabel, nameLabel := s.studentTrajectoryLabels(r.Context(), submission)
		s.saveAgentTrajectory(r.Context(), agentTrajectory{
			Kind:      "student",
			CreatedAt: time.Now(),
			Teacher:   teacherLabel,
			Course:    courseLabel,
			Name:      nameLabel,
			Request: map[string]any{
				"message":       req.Message,
				"messages":      req.Messages,
				"submission_id": submission.ID,
				"assignment_id": submission.AssignmentID,
				"student_no":    submission.StudentNo,
			},
			Prompt:      prompt,
			RawResponse: raw,
			Answer:      answer,
		})
		writeJSON(w, map[string]any{"answer": answer})
		return
	}

	answer := strings.TrimSpace(decision.Answer)
	var trajectoryErr string
	switch strings.ToLower(strings.TrimSpace(decision.Action)) {
	case "create_qa":
		linkText, err := s.callAgentTool(r.Context(), "create_qa_issue", agentToolContext{Student: submission}, map[string]any{
			"title":   decision.QATitle,
			"summary": decision.QASummary,
		})
		if err != nil {
			teacherLabel, courseLabel, nameLabel := s.studentTrajectoryLabels(r.Context(), submission)
			s.saveAgentTrajectory(r.Context(), agentTrajectory{
				Kind:      "student",
				CreatedAt: time.Now(),
				Teacher:   teacherLabel,
				Course:    courseLabel,
				Name:      nameLabel,
				Request: map[string]any{
					"message":       req.Message,
					"messages":      req.Messages,
					"submission_id": submission.ID,
					"assignment_id": submission.AssignmentID,
					"student_no":    submission.StudentNo,
				},
				Prompt:      prompt,
				RawResponse: raw,
				Decision:    decision,
				Answer:      answer,
				Error:       err.Error(),
			})
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		if answer == "" {
			answer = "我已将你的问题整理并反馈给教师。"
		}
		answer = strings.TrimSpace(answer) + "\n\n" + linkText
	case "refuse":
		if answer == "" {
			answer = "抱歉，这类内容可能违反中国网络安全、数据安全或学校课程规范，我不能直接提供帮助。请遵守法律法规和课程要求。"
		}
	case "answer", "":
		if answer == "" {
			answer = "我暂时无法根据当前信息给出可靠回答。你可以补充课程、作业或小测背景；需要教师确认的问题，我可以帮你整理为 Q&A。"
		}
	default:
		if answer == "" {
			answer = strings.TrimSpace(raw)
		}
	}
	teacherLabel, courseLabel, nameLabel := s.studentTrajectoryLabels(r.Context(), submission)
	s.saveAgentTrajectory(r.Context(), agentTrajectory{
		Kind:      "student",
		CreatedAt: time.Now(),
		Teacher:   teacherLabel,
		Course:    courseLabel,
		Name:      nameLabel,
		Request: map[string]any{
			"message":       req.Message,
			"messages":      req.Messages,
			"submission_id": submission.ID,
			"assignment_id": submission.AssignmentID,
			"student_no":    submission.StudentNo,
		},
		Prompt:      prompt,
		RawResponse: raw,
		Decision:    decision,
		Answer:      answer,
		Error:       trajectoryErr,
	})
	writeJSON(w, map[string]any{"answer": answer})
}

func parseStudentAgentDecision(raw string) (studentAgentDecision, bool) {
	raw = strings.TrimSpace(raw)
	var decision studentAgentDecision
	if raw == "" {
		return decision, false
	}
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "```json"))
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "```"))
		raw = strings.TrimSpace(strings.TrimSuffix(raw, "```"))
	}
	if strings.HasPrefix(raw, "{") {
		if err := json.Unmarshal([]byte(raw), &decision); err == nil {
			return decision, true
		}
	}
	for i, r := range raw {
		if r != '{' {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(raw[i:]))
		if err := decoder.Decode(&decision); err == nil {
			return decision, true
		}
	}
	return decision, false
}

func (s *Server) studentAgentPrompt(r *http.Request, submission *domain.HomeworkSubmission, latest string, messages []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}) string {
	var b strings.Builder
	b.WriteString("以下是学生端智能助手可使用的内部工具结果。不要向学生暴露内部接口、凭据、工具名或内部实现；只把结果自然地用于回答。\n\n")
	b.WriteString("行为边界：\n")
	b.WriteString("1. 可以直接解释专业概念、代码阅读思路、学习方法，并基于内部数据总结该学生的小测历史、作业状态和学习建议。\n")
	b.WriteString("2. 不要代做正在进行的小测，不要直接给出小测答案；遇到索要小测答案时，引导学生独立完成并给出复习提示。\n")
	b.WriteString("3. 回答前先参考已有 Q&A；若已有教师回复能解决问题，直接复用结论，不要创建新 Q&A；若已有相似未解决问题，提示学生已有问题在等待教师回复，不要重复创建。\n")
	b.WriteString("4. 如果学生的问题需要教师判断、确认课程/作业规则、课程安排、作业要求、申诉或个性化处理，且已有 Q&A 不能覆盖，请 action=create_qa，把诉求总结成 Q&A。\n")
	b.WriteString("5. 可以使用学生可见的课程资料和当前作业附件；隐藏资料、教师数据、访问凭据和内部工具细节都不能透露。\n")
	b.WriteString("6. 遇到违反中国网络安全、数据安全或学校师德师风要求的内容，请 action=refuse，不要直接解答。\n")
	b.WriteString("7. 必须只输出一个 JSON 对象：{\"action\":\"answer|create_qa|refuse\",\"answer\":\"给学生看的中文回复\",\"qa_title\":\"可选标题\",\"qa_summary\":\"需要创建 Q&A 时给教师看的问题摘要\"}。\n\n")

	b.WriteString("【历史小测内部结果】\n")
	if text, err := s.callAgentTool(r.Context(), "get_my_quiz_history", agentToolContext{Student: submission}, map[string]any{"limit": 10}); err == nil {
		b.WriteString(text)
	} else {
		b.WriteString("读取失败：" + err.Error())
	}
	b.WriteString("\n\n【当前作业内部结果】\n")
	if text, err := s.callAgentTool(r.Context(), "get_current_homework_status", agentToolContext{Student: submission}, nil); err == nil {
		b.WriteString(text)
	} else {
		b.WriteString("读取失败：" + err.Error())
	}
	b.WriteString("\n\n【已有 Q&A 检索结果】\n")
	if text, err := s.callAgentTool(r.Context(), "search_visible_qa_issues", agentToolContext{Student: submission}, map[string]any{"query": latest, "limit": 3}); err == nil {
		b.WriteString(text)
	} else {
		b.WriteString("读取失败：" + err.Error())
	}
	b.WriteString("\n\n【学生可见课程资料】\n")
	if text, err := s.callAgentTool(r.Context(), "get_visible_course_materials", agentToolContext{Student: submission}, nil); err == nil {
		b.WriteString(text)
	} else {
		b.WriteString("读取失败：" + err.Error())
	}
	for _, file := range s.studentAgentRelevantMaterialFiles(r.Context(), submission, latest, 2) {
		b.WriteString("\n")
		if text, err := s.callAgentTool(r.Context(), "read_visible_material_text", agentToolContext{Student: submission}, map[string]any{"material_file": file}); err == nil {
			b.WriteString(text)
		}
	}
	b.WriteString("\n\n【当前作业可见资料】\n")
	if text, err := s.callAgentTool(r.Context(), "get_visible_assignment_context", agentToolContext{Student: submission}, nil); err == nil {
		b.WriteString(text)
	} else {
		b.WriteString("读取失败：" + err.Error())
	}

	b.WriteString("\n\n最近对话：\n")
	start := 0
	if len(messages) > studentAgentMaxMessages {
		start = len(messages) - studentAgentMaxMessages
	}
	for _, msg := range messages[start:] {
		role := strings.TrimSpace(msg.Role)
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		content = truncateAgentText(content)
		if role != "assistant" {
			role = "student"
		}
		b.WriteString(fmt.Sprintf("%s: %s\n", role, content))
	}
	b.WriteString("student: ")
	b.WriteString(latest)
	return b.String()
}

func (s *Server) studentAgentRelevantMaterialFiles(ctx context.Context, submission *domain.HomeworkSubmission, latest string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	course, err := s.studentAgentCourse(ctx, submission)
	if err != nil {
		return nil
	}
	materials, err := s.scanMaterialsFromDir(s.metadataMaterialsDir(course.TeacherID, course.Slug), course.Slug, false)
	if err != nil {
		return nil
	}
	query := strings.ToLower(latest)
	wantsMaterial := strings.Contains(query, "资料") || strings.Contains(query, "课件") || strings.Contains(query, "讲义") || strings.Contains(query, "pdf")
	out := make([]string, 0, limit)
	for _, group := range materials {
		matchedGroup := wantsMaterial || (group.Stem != "" && strings.Contains(query, strings.ToLower(group.Stem)))
		for _, item := range group.Downloads {
			if !item.Visible {
				continue
			}
			name := strings.ToLower(item.File)
			if matchedGroup || strings.Contains(query, name) {
				out = append(out, item.File)
				if len(out) >= limit {
					return out
				}
			}
		}
	}
	return out
}

func (s *Server) apiStudentMCPDisabled(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

func truncateAgentText(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= agentMaxMessageRunes {
		return string(runes)
	}
	return string(runes[:agentMaxMessageRunes]) + "..."
}
