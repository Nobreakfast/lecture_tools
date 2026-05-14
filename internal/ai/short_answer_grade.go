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

type ShortAnswerGradeInput struct {
	QuizTitle       string `json:"quiz_title"`
	QuestionID      string `json:"question_id"`
	Stem            string `json:"stem"`
	ReferenceAnswer string `json:"reference_answer"`
	ScoringRubric   string `json:"scoring_rubric,omitempty"`
	StudentAnswer   string `json:"student_answer"`
}

type ShortAnswerGradeResult struct {
	Score    float64 `json:"score"`
	Feedback string  `json:"feedback"`
}

const shortAnswerGradeSystemPrompt = `你是一位大学课堂小测简答题评分助手。请根据题干、参考答案、评分说明和学生作答，对单道简答题评分。

要求：
- 返回严格 JSON，不要 markdown，不要代码块，不要额外解释。
- JSON 结构必须是 {"score": 数字, "feedback": "中文反馈"}。
- score 必须在 0 到 1 之间，最多保留一位小数；1 表示完全正确，0 表示未答或基本错误。
- 评分说明优先级最高；没有评分说明时，以参考答案为主要依据，允许同义表述、合理步骤和等价代码思路。
- feedback 面向教师/学生都可读，简洁说明主要得分依据和缺失点。
- 不要编造学生没有写出的内容；作答信息不足时应谨慎给分。`

func (c *Client) GradeShortAnswer(ctx context.Context, in ShortAnswerGradeInput) (ShortAnswerGradeResult, string, error) {
	userMsg, _ := json.Marshal(in)
	content, err := c.chat(ctx, shortAnswerGradeSystemPrompt, string(userMsg), 0.1)
	if err != nil {
		return ShortAnswerGradeResult{}, "", err
	}
	out, err := ParseShortAnswerGradeResult(content)
	if err != nil {
		return ShortAnswerGradeResult{}, content, err
	}
	return out, content, nil
}

func ParseShortAnswerGradeResult(content string) (ShortAnswerGradeResult, error) {
	var out ShortAnswerGradeResult
	if err := unmarshalAIJSONObject(content, &out); err != nil {
		return ShortAnswerGradeResult{}, fmt.Errorf("解析简答题评分 JSON 失败: %w", err)
	}
	out.Feedback = strings.TrimSpace(out.Feedback)
	if out.Feedback == "" {
		return ShortAnswerGradeResult{}, fmt.Errorf("简答题评分缺少 feedback")
	}
	if out.Score < 0 || out.Score > 1 {
		return ShortAnswerGradeResult{}, fmt.Errorf("简答题评分 score 必须在 0 到 1 之间")
	}
	out.Score = math.Round(out.Score*10) / 10
	return out, nil
}
