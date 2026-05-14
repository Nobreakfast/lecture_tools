package ai

import "testing"

func TestParseHomeworkPregradeResult(t *testing.T) {
	got, err := ParseHomeworkPregradeResult(`{"suggested_score": 91.26, "feedback": "结构完整，分析可以更深入。"}`)
	if err != nil {
		t.Fatalf("ParseHomeworkPregradeResult failed: %v", err)
	}
	if got.SuggestedScore != 91.3 {
		t.Fatalf("score=%v want 91.3", got.SuggestedScore)
	}
	if got.Feedback != "结构完整，分析可以更深入。" {
		t.Fatalf("feedback=%q", got.Feedback)
	}
}

func TestParseHomeworkPregradeResultRejectsInvalid(t *testing.T) {
	if _, err := ParseHomeworkPregradeResult(`{"suggested_score": 120, "feedback": "ok"}`); err == nil {
		t.Fatal("expected range error")
	}
	if _, err := ParseHomeworkPregradeResult(`{"suggested_score": 80, "feedback": ""}`); err == nil {
		t.Fatal("expected missing feedback error")
	}
}

func TestParseShortAnswerGradeResult(t *testing.T) {
	got, err := ParseShortAnswerGradeResult("```json\n{\"score\":0.86,\"feedback\":\"核心概念正确，表述略少。\"}\n```")
	if err != nil {
		t.Fatalf("ParseShortAnswerGradeResult failed: %v", err)
	}
	if got.Score != 0.9 {
		t.Fatalf("score=%v want 0.9", got.Score)
	}
	if got.Feedback != "核心概念正确，表述略少。" {
		t.Fatalf("feedback=%q", got.Feedback)
	}
}

func TestParseShortAnswerGradeResultRejectsInvalid(t *testing.T) {
	if _, err := ParseShortAnswerGradeResult(`{"score":1.2,"feedback":"ok"}`); err == nil {
		t.Fatal("expected range error")
	}
	if _, err := ParseShortAnswerGradeResult(`{"score":0.8,"feedback":""}`); err == nil {
		t.Fatal("expected missing feedback error")
	}
}
