// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"course-assistant/internal/domain"
)

const studentAgentMaxMessages = 8

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

	prompt := s.studentAgentPrompt(r, submission, req.Message, req.Messages)
	raw, err := s.aiClient.StudentAgentChat(r.Context(), prompt)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	decision, ok := parseStudentAgentDecision(raw)
	if !ok {
		writeJSON(w, map[string]any{"answer": strings.TrimSpace(raw)})
		return
	}

	answer := strings.TrimSpace(decision.Answer)
	switch strings.ToLower(strings.TrimSpace(decision.Action)) {
	case "create_qa":
		linkText, err := s.studentMCPCreateQAIssue(r.Context(), submission, decision.QATitle, decision.QASummary)
		if err != nil {
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
	writeJSON(w, map[string]any{"answer": answer})
}

func parseStudentAgentDecision(raw string) (studentAgentDecision, bool) {
	raw = strings.TrimSpace(raw)
	var decision studentAgentDecision
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return decision, false
	}
	if err := json.Unmarshal([]byte(raw), &decision); err != nil {
		return decision, false
	}
	return decision, true
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
	b.WriteString("3. 如果学生的问题需要教师判断、确认课程/作业规则、课程安排、作业要求、申诉或个性化处理，请 action=create_qa，把诉求总结成 Q&A。\n")
	b.WriteString("4. 遇到违反中国网络安全、数据安全或学校师德师风要求的内容，请 action=refuse，不要直接解答。\n")
	b.WriteString("5. 必须只输出一个 JSON 对象：{\"action\":\"answer|create_qa|refuse\",\"answer\":\"给学生看的中文回复\",\"qa_title\":\"可选标题\",\"qa_summary\":\"需要创建 Q&A 时给教师看的问题摘要\"}。\n\n")

	b.WriteString("【历史小测内部结果】\n")
	if text, err := s.studentMCPQuizHistory(r.Context(), submission, 10); err == nil {
		b.WriteString(text)
	} else {
		b.WriteString("读取失败：" + err.Error())
	}
	b.WriteString("\n\n【当前作业内部结果】\n")
	if text, err := s.studentMCPHomeworkStatus(r.Context(), submission); err == nil {
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
		if role != "assistant" {
			role = "student"
		}
		b.WriteString(fmt.Sprintf("%s: %s\n", role, content))
	}
	b.WriteString("student: ")
	b.WriteString(latest)
	return b.String()
}

func (s *Server) apiStudentMCPDisabled(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}
