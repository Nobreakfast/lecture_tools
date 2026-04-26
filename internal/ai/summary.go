// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"course-assistant/internal/domain"
)

type WrongQuestion struct {
	Stem          string `json:"stem"`
	StudentAnswer string `json:"student_answer"`
	CorrectAnswer string `json:"correct_answer"`
	KnowledgeTag  string `json:"knowledge_tag,omitempty"`
	Explanation   string `json:"explanation,omitempty"`
}

type SummarizeInput struct {
	QuizTitle      string          `json:"quiz_title"`
	ScoreCorrect   int             `json:"score_correct"`
	ScoreTotal     int             `json:"score_total"`
	Strengths      []string        `json:"strengths"`
	Weaknesses     []string        `json:"weaknesses"`
	WrongQuestions []WrongQuestion `json:"wrong_questions"`
}

const systemPrompt = `你是一位大学课堂教学助手。学生刚完成一次课堂小测，请根据他的错题信息给出个性化的学习反馈。

要求：
1. 分析学生的掌握情况，指出哪些知识点掌握良好，哪些有问题
2. 对每道错题，解释关键知识点和错误原因
3. 尽量给出一个生活中的例子帮助学生理解错题涉及的概念，格式如：
   "生活中的xxx例子可以帮助你理解这个问题：xxx是xxx样子，它与这道题的关系是xxx。当你下次遇到类似问题，可以想想这个例子。"
4. 给出具体可执行的改进建议

请输出 JSON，包含以下字段：
- strengths: string[] — 掌握较好的知识点（简短）
- weaknesses: string[] — 需要加强的知识点（简短）
- next_actions: string[] — 具体的行动建议（每条包含错题分析、生活类比、改进方法，可以较长）
- priority_level: string — 优先级（"高"/"中"/"低"）
- encouragement: string — 一句鼓励的话

注意：语言要亲切自然，像老师对学生说话一样。不要使用空洞的套话。只输出合法 JSON，不要 markdown 代码块或其他内容。`

func (c *Client) Summarize(ctx context.Context, in SummarizeInput) (domain.ResultSummary, error) {
	userMsg, _ := json.Marshal(in)
	content, err := c.chat(ctx, systemPrompt, string(userMsg), 0.7)
	if err != nil {
		return ruleBased(in), err
	}

	var summary domain.ResultSummary
	if err := json.Unmarshal([]byte(content), &summary); err != nil {
		c.setLastError("解析 AI JSON 失败: " + err.Error())
		return ruleBased(in), err
	}
	return summary, nil
}

func ruleBased(in SummarizeInput) domain.ResultSummary {
	ratio := 0.0
	if in.ScoreTotal > 0 {
		ratio = float64(in.ScoreCorrect) / float64(in.ScoreTotal)
	}
	priority := "中"
	if ratio < 0.6 {
		priority = "高"
	}
	if ratio >= 0.85 {
		priority = "低"
	}
	actions := []string{
		"先复盘错题，记录每题错因并归类。",
		"针对薄弱知识点做 2-3 道同类型练习。",
		"下一次课前用 15 分钟快速回顾本次重点。",
	}
	if len(in.Weaknesses) > 0 {
		actions[1] = fmt.Sprintf("优先复习：%s，并完成同类练习。", strings.Join(in.Weaknesses, "、"))
	}
	return domain.ResultSummary{
		Strengths:     in.Strengths,
		Weaknesses:    in.Weaknesses,
		NextActions:   actions,
		Priority:      priority,
		Encouragement: "你已经在持续进步，按建议复盘一轮会更稳。",
	}
}