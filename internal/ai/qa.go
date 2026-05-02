// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package ai

import (
	"context"
	"strings"
)

const qaAnswerSystemPrompt = `你是课程 Q&A 的 AI 助教，负责先回答学生提出的技术问题。

要求：
- 用中文回答，语气清楚、耐心、适合课堂学习场景。
- 优先解释概念、原理、步骤和常见误区，可给一个短例子。
- 如果问题不是技术问题，或缺少必要上下文，请简短说明需要教师进一步确认，不要编造课程事实。
- 不要声称自己已经替代教师批改或给出最终成绩。
- 控制在 300 字以内。`

// AnswerQA drafts a first-pass answer for a student Q&A issue.
func (c *Client) AnswerQA(ctx context.Context, courseName, assignmentID, question string) (string, error) {
	var b strings.Builder
	if strings.TrimSpace(courseName) != "" {
		b.WriteString("课程：")
		b.WriteString(strings.TrimSpace(courseName))
		b.WriteString("\n")
	}
	if strings.TrimSpace(assignmentID) != "" {
		b.WriteString("作业：")
		b.WriteString(strings.TrimSpace(assignmentID))
		b.WriteString("\n")
	}
	b.WriteString("学生问题：\n")
	b.WriteString(strings.TrimSpace(question))
	return c.chat(ctx, qaAnswerSystemPrompt, b.String(), 0.3)
}
