package domain

import "strings"

// NormalizeCourseEnglishName returns the display and internal forms used by the
// course module. Leading/trailing whitespace is trimmed, repeated whitespace is
// collapsed to a single separator, display keeps spaces, and internal replaces
// spaces with underscores for storage/path usage.
func NormalizeCourseEnglishName(raw string) (displayName string, internalName string) {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return "", ""
	}
	displayName = strings.Join(parts, " ")
	internalName = strings.Join(parts, "_")
	return displayName, internalName
}
