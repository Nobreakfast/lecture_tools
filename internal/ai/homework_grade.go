// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package ai

import (
	"context"
	"encoding/json"
	"strings"
)

type HomeworkGradeFeedbackInput struct {
	CourseName    string `json:"course_name"`
	AssignmentID  string `json:"assignment_id"`
	StudentName   string `json:"student_name"`
	StudentNo     string `json:"student_no"`
	ClassName     string `json:"class_name"`
	TeacherNote   string `json:"teacher_note"`
	ReportContext string `json:"report_context"`
}

const homeworkGradeFeedbackSystemPrompt = `你是一位大学课程作业批改助手。请根据学生报告正文摘录和教师的简短批注意见，生成一段可直接发送给学生的中文作业评价。

要求：
- 只生成评语，不要给分数或建议分数。
- 语气专业、具体、客观，指出优点、主要问题和下一步改进建议。
- 如果教师简短意见里有明确判断，应优先遵循教师意见。
- 如果报告正文信息不足，请基于已知内容谨慎表达，不要编造报告中没有的细节。
- 输出纯文本，不要 JSON，不要 markdown 标题。`

func (c *Client) GenerateHomeworkFeedback(ctx context.Context, in HomeworkGradeFeedbackInput) (string, error) {
	userMsg, _ := json.Marshal(in)
	content, err := c.chat(ctx, homeworkGradeFeedbackSystemPrompt, string(userMsg), 0.4)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(content), nil
}
