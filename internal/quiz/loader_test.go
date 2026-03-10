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
	target := filepath.Join(root, "quiz", "week2_l1.yaml")
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read quiz file failed: %v", err)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse quiz failed: %v", err)
	}
	if parsed.QuizID != "week2_l1" {
		t.Fatalf("unexpected quiz id: %s", parsed.QuizID)
	}
	if parsed.Sampling == nil || len(parsed.Sampling.Groups) != 8 {
		t.Fatalf("unexpected sampling config")
	}
	if len(parsed.Questions) != 82 {
		t.Fatalf("unexpected question count: %d", len(parsed.Questions))
	}
}
