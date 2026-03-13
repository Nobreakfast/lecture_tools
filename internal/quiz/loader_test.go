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
