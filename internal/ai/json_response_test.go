// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package ai

import (
	"strings"
	"testing"
)

func TestUnmarshalAIJSONObjectAcceptsFencedJSON(t *testing.T) {
	var got AdminSummary
	raw := "```json\n{\"answer_analysis\":\"答题稳定\",\"feedback_summary\":\"希望更多案例\",\"teaching_suggestions\":\"补充练习\"}\n```"
	if err := unmarshalAIJSONObject(raw, &got); err != nil {
		t.Fatalf("unmarshalAIJSONObject failed: %v", err)
	}
	if got.AnswerAnalysis != "答题稳定" {
		t.Fatalf("AnswerAnalysis=%q", got.AnswerAnalysis)
	}
}

func TestUnmarshalAIJSONObjectAcceptsLeadingAndTrailingText(t *testing.T) {
	var got AdminSummary
	raw := "以下是总结 JSON：\n{\"answer_analysis\":\"答题稳定\",\"feedback_summary\":\"希望更多案例\",\"teaching_suggestions\":\"补充练习\"}\n这部分可以直接展示给教师。"
	if err := unmarshalAIJSONObject(raw, &got); err != nil {
		t.Fatalf("unmarshalAIJSONObject failed: %v", err)
	}
	if got.FeedbackSummary != "希望更多案例" {
		t.Fatalf("FeedbackSummary=%q", got.FeedbackSummary)
	}
}

func TestUnmarshalAIJSONObjectRejectsTruncatedJSON(t *testing.T) {
	var got AdminSummary
	if err := unmarshalAIJSONObject(`{"answer_analysis":"答题稳定"`, &got); err == nil {
		t.Fatal("expected truncated JSON error")
	}
}

func TestFallbackAdminSummaryIncludesContext(t *testing.T) {
	got := fallbackAdminSummary(AdminSummarizeInput{
		QuizID:       "week1",
		QuizTitle:    "第一周课堂反馈",
		StudentCount: 2,
		AvgScore:     1,
		AvgTotal:     2,
		QuestionStats: []AdminQuestionStat{
			{QuestionID: "q1", Stem: "题目一", KnowledgeTag: "结构体", CorrectRate: 0.5, CommonWrongAnswers: []string{"B.错误(1人)"}},
		},
	})
	if !strings.Contains(got.AnswerAnalysis, "AI 返回格式异常") {
		t.Fatalf("expected fallback note, got %q", got.AnswerAnalysis)
	}
	if !strings.Contains(got.TeachingSuggestions, "结构体") {
		t.Fatalf("expected knowledge tag in suggestions, got %q", got.TeachingSuggestions)
	}
}
