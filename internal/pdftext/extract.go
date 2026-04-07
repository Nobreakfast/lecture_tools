package pdftext

import (
	"fmt"
	"strings"

	"github.com/ledongthuc/pdf"
)

const maxTextLen = 15000

// ExtractText extracts text content from a PDF file.
// Returns empty string and nil error if no text could be extracted.
func ExtractText(path string) (string, error) {
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
		if buf.Len() > maxTextLen {
			break
		}
	}

	result := strings.TrimSpace(buf.String())
	if len(result) > maxTextLen {
		result = result[:maxTextLen]
	}
	return result, nil
}
