package pdftext

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

const (
	maxTextLen     = 15000
	minUsefulRunes = 20
	extractTimeout = 30 * time.Second
	ocrTimeout     = 90 * time.Second
	maxOCRPages    = 5
	pdftoppmDPI    = "160"
)

// ExtractText extracts text content from a PDF file.
// Returns empty string and nil error if no text could be extracted.
func ExtractText(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("PDF 路径为空")
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("打开PDF失败: %w", err)
	}

	best := ""
	var errs []error

	if text, err := extractWithGoPDF(path); err == nil {
		best = betterText(best, text)
		if isUsefulText(best) {
			return best, nil
		}
	} else {
		errs = append(errs, err)
	}

	if text, err := extractWithPDFToText(path); err == nil {
		best = betterText(best, text)
		if isUsefulText(best) {
			return best, nil
		}
	} else if !errors.Is(err, errExtractorUnavailable) {
		errs = append(errs, err)
	}

	if text, err := extractWithOCR(path); err == nil {
		best = betterText(best, text)
		if strings.TrimSpace(best) != "" {
			return best, nil
		}
	} else if !errors.Is(err, errExtractorUnavailable) {
		errs = append(errs, err)
	}

	if strings.TrimSpace(best) != "" {
		return best, nil
	}
	if len(errs) > 0 {
		return "", fmt.Errorf("PDF 文本提取失败: %w", errors.Join(errs...))
	}
	return "", nil
}

func extractWithGoPDF(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("打开PDF失败: %w", err)
	}
	defer f.Close()

	var buf strings.Builder
	totalPage := r.NumPage()
	for i := 1; i <= totalPage; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		buf.WriteString(text)
		buf.WriteString("\n")
		if utf8.RuneCountInString(buf.String()) > maxTextLen {
			break
		}
	}

	return cleanAndLimitText(buf.String()), nil
}

var errExtractorUnavailable = errors.New("PDF 文本提取工具不可用")

func extractWithPDFToText(path string) (string, error) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return "", errExtractorUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), extractTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pdftotext", "-enc", "UTF-8", "-layout", "-q", path, "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("pdftotext 提取超时")
		}
		return "", fmt.Errorf("pdftotext 提取失败: %s", strings.TrimSpace(stderr.String()))
	}
	return cleanAndLimitText(stdout.String()), nil
}

func extractWithOCR(path string) (string, error) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return "", errExtractorUnavailable
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		return "", errExtractorUnavailable
	}

	dir, err := os.MkdirTemp("", "course-assistant-pdf-ocr-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(context.Background(), ocrTimeout)
	defer cancel()

	prefix := filepath.Join(dir, "page")
	render := exec.CommandContext(ctx, "pdftoppm", "-r", pdftoppmDPI, "-f", "1", "-l", fmt.Sprint(maxOCRPages), "-png", path, prefix)
	var renderErr bytes.Buffer
	render.Stderr = &renderErr
	if err := render.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("PDF 转图片超时")
		}
		return "", fmt.Errorf("PDF 转图片失败: %s", strings.TrimSpace(renderErr.String()))
	}

	images, err := filepath.Glob(prefix + "-*.png")
	if err != nil || len(images) == 0 {
		return "", nil
	}

	lang := ocrLanguage()
	var buf strings.Builder
	for _, image := range images {
		ocr := exec.CommandContext(ctx, "tesseract", image, "stdout", "-l", lang, "--psm", "6")
		var stdout, stderr bytes.Buffer
		ocr.Stdout = &stdout
		ocr.Stderr = &stderr
		if err := ocr.Run(); err != nil {
			if ctx.Err() != nil {
				return cleanAndLimitText(buf.String()), nil
			}
			continue
		}
		buf.Write(stdout.Bytes())
		buf.WriteString("\n")
		if utf8.RuneCountInString(buf.String()) > maxTextLen {
			break
		}
	}
	return cleanAndLimitText(buf.String()), nil
}

func ocrLanguage() string {
	cmd := exec.Command("tesseract", "--list-langs")
	out, err := cmd.Output()
	if err != nil {
		return "eng"
	}
	langs := strings.Fields(string(out))
	hasChinese := false
	hasEnglish := false
	for _, lang := range langs {
		switch lang {
		case "chi_sim":
			hasChinese = true
		case "eng":
			hasEnglish = true
		}
	}
	if hasChinese && hasEnglish {
		return "chi_sim+eng"
	}
	if hasChinese {
		return "chi_sim"
	}
	return "eng"
}

func betterText(current, candidate string) string {
	candidate = cleanAndLimitText(candidate)
	current = cleanAndLimitText(current)
	if textScore(candidate) > textScore(current) {
		return candidate
	}
	return current
}

func cleanAndLimitText(text string) string {
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= maxTextLen {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:maxTextLen]))
}

func isUsefulText(text string) bool {
	return textScore(text) >= minUsefulRunes
}

func textScore(text string) int {
	score := 0
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			score++
		}
	}
	return score
}
