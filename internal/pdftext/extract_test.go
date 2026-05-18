package pdftext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExtractTextFallsBackToPDFToText(t *testing.T) {
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.4\nbroken enough for go parser"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	script := filepath.Join(binDir, "pdftotext")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '中文报告正文 from pdftotext 12345\\n'\n"), 0o755); err != nil {
		t.Fatalf("write fake pdftotext: %v", err)
	}
	t.Setenv("PATH", binDir)

	got, err := ExtractText(pdfPath)
	if err != nil {
		t.Fatalf("ExtractText returned error: %v", err)
	}
	if !strings.Contains(got, "中文报告正文") || !strings.Contains(got, "12345") {
		t.Fatalf("expected fallback text, got %q", got)
	}
}

func TestCleanAndLimitTextKeepsValidUTF8(t *testing.T) {
	got := cleanAndLimitText(strings.Repeat("中文", maxTextLen))
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid utf-8")
	}
	if utf8.RuneCountInString(got) != maxTextLen {
		t.Fatalf("expected %d runes, got %d", maxTextLen, utf8.RuneCountInString(got))
	}
}
