package domain

import (
	"encoding/json"
	"strings"
)

func ParseShortAnswer(raw string) ShortAnswerValue {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ShortAnswerValue{}
	}
	var sa ShortAnswerValue
	if json.Unmarshal([]byte(raw), &sa) == nil && (sa.Text != "" || len(sa.Images) > 0) {
		return sa
	}
	return ShortAnswerValue{Text: raw}
}

func EncodeShortAnswer(sa ShortAnswerValue) string {
	if sa.Text == "" && len(sa.Images) == 0 {
		return ""
	}
	if len(sa.Images) == 0 {
		return sa.Text
	}
	sa.V = 1
	b, _ := json.Marshal(sa)
	return string(b)
}

func ShortAnswerText(raw string) string {
	return ParseShortAnswer(raw).Text
}

func ShortAnswerImages(raw string) []string {
	return ParseShortAnswer(raw).Images
}
