// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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

type HomeworkPregradeResult struct {
	SuggestedScore float64 `json:"suggested_score"`
	Feedback       string  `json:"feedback"`
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

const homeworkPregradeSystemPrompt = `你是一位大学课程作业批改助手。请根据学生报告正文摘录和教师给出的评价维度，生成 AI 预评。

要求：
- 返回严格 JSON，不要 markdown，不要代码块，不要额外解释。
- JSON 结构必须是 {"suggested_score": 数字, "feedback": "中文评语"}。
- suggested_score 必须在 0 到 100 之间，最多保留一位小数。
- feedback 可直接给教师作为正式评语草稿，需具体、客观，包含优点、主要问题和改进建议。
- 教师评价维度优先级最高；如果维度中给出扣分或重点要求，请体现到建议分和评语中。
- 如果报告正文信息不足，请谨慎评分并说明依据不足，不要编造报告中没有的细节。`

func (c *Client) PregradeHomework(ctx context.Context, in HomeworkGradeFeedbackInput) (HomeworkPregradeResult, error) {
	userMsg, _ := json.Marshal(in)
	content, err := c.chat(ctx, homeworkPregradeSystemPrompt, string(userMsg), 0.2)
	if err != nil {
		return HomeworkPregradeResult{}, err
	}
	return ParseHomeworkPregradeResult(content)
}

func ParseHomeworkPregradeResult(content string) (HomeworkPregradeResult, error) {
	content = strings.TrimSpace(stripCodeFence(content))
	var out HomeworkPregradeResult
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return HomeworkPregradeResult{}, fmt.Errorf("解析 AI 预评 JSON 失败: %w", err)
	}
	out.Feedback = strings.TrimSpace(out.Feedback)
	if out.Feedback == "" {
		return HomeworkPregradeResult{}, fmt.Errorf("AI 预评缺少 feedback")
	}
	if out.SuggestedScore < 0 || out.SuggestedScore > 100 {
		return HomeworkPregradeResult{}, fmt.Errorf("AI 建议分必须在 0 到 100 之间")
	}
	out.SuggestedScore = math.Round(out.SuggestedScore*10) / 10
	return out, nil
}
