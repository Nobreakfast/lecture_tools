package quiz

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseWeek2L1Quiz(t *testing.T) {
	_, currentFile, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	target := filepath.Join(root, "quiz", "最优化方法", "week2_l1.yaml")
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read quiz file failed: %v", err)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse quiz failed: %v", err)
	}
	if parsed.QuizID != "optim_week2_l1" {
		t.Fatalf("unexpected quiz id: %s", parsed.QuizID)
	}
	if parsed.Sampling == nil || len(parsed.Sampling.Groups) != 8 {
		t.Fatalf("unexpected sampling config")
	}
	if len(parsed.Questions) != 82 {
		t.Fatalf("unexpected question count: %d", len(parsed.Questions))
	}
}

func TestParseMultiChoiceAnswerNormalization(t *testing.T) {
	raw := []byte(`
quiz_id: "q_multi"
title: "t"
questions:
  - id: "m1"
    type: "multi_choice"
    stem: "s"
    options:
      - key: "A"
        text: "a"
      - key: "B"
        text: "b"
      - key: "C"
        text: "c"
    correct_answer: " C, A , C "
`)
	q, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if q.Questions[0].CorrectAnswer != "A,C" {
		t.Fatalf("unexpected normalized correct answer: %s", q.Questions[0].CorrectAnswer)
	}
}

func TestParseWeek2L2Quiz(t *testing.T) {
	_, currentFile, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	target := filepath.Join(root, "quiz", "最优化方法", "week2_l2.yaml")
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read quiz file failed: %v", err)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse quiz failed: %v", err)
	}
	if parsed.QuizID != "optim_week2_l2" {
		t.Fatalf("unexpected quiz id: %s", parsed.QuizID)
	}
	if len(parsed.Questions) != 10 {
		t.Fatalf("unexpected question count: %d", len(parsed.Questions))
	}
}

func TestParseSurveyAllowMultiple(t *testing.T) {
	raw := []byte(`
quiz_id: "q_survey_multi"
title: "t"
questions:
  - id: "s1"
    type: "survey"
    allow_multiple: true
    stem: "s"
    options:
      - key: "A"
        text: "a"
      - key: "B"
        text: "b"
`)
	q, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if !q.Questions[0].AllowMultiple {
		t.Fatalf("expected allow_multiple=true")
	}
}

func TestParseAllowMultipleOnlyForSurvey(t *testing.T) {
	raw := []byte(`
quiz_id: "q_invalid"
title: "t"
questions:
  - id: "m1"
    type: "multi_choice"
    allow_multiple: true
    stem: "s"
    options:
      - key: "A"
        text: "a"
      - key: "B"
        text: "b"
    correct_answer: "A"
`)
	if _, err := Parse(raw); err == nil {
		t.Fatalf("expected parse error for non-survey allow_multiple")
	}
}

func TestParseShortAnswerMode(t *testing.T) {
	raw := []byte(`
quiz_id: "q_short_mode"
title: "t"
questions:
  - id: "s1"
    type: "short_answer"
    short_answer_mode: "image"
    stem: "上传图片"
`)
	q, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if q.Questions[0].ShortAnswerMode != "image" {
		t.Fatalf("unexpected short_answer_mode: %s", q.Questions[0].ShortAnswerMode)
	}
}

func TestParseShortAnswerModeInvalid(t *testing.T) {
	raw := []byte(`
quiz_id: "q_short_mode_invalid"
title: "t"
questions:
  - id: "s1"
    type: "short_answer"
    short_answer_mode: "upload"
    stem: "上传图片"
`)
	if _, err := Parse(raw); err == nil {
		t.Fatalf("expected parse error for invalid short_answer_mode")
	}
}

func TestParseShortAnswerModeOnlyForShortAnswer(t *testing.T) {
	raw := []byte(`
quiz_id: "q_short_mode_non_short"
title: "t"
questions:
  - id: "c1"
    type: "single_choice"
    short_answer_mode: "text"
    stem: "s"
    options:
      - key: "A"
        text: "a"
      - key: "B"
        text: "b"
    correct_answer: "A"
`)
	if _, err := Parse(raw); err == nil {
		t.Fatalf("expected parse error for short_answer_mode on non-short_answer")
	}
}
