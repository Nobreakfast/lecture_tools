package domain

import "testing"

func TestNormalizeCourseEnglishName(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		wantDisplayName string
		wantInternal    string
	}{
		{
			name:            "single spaces",
			input:           "Machine Learning Intro",
			wantDisplayName: "Machine Learning Intro",
			wantInternal:    "Machine_Learning_Intro",
		},
		{
			name:            "leading trailing and repeated spaces",
			input:           "  Deep   Learning  Basics  ",
			wantDisplayName: "Deep Learning Basics",
			wantInternal:    "Deep_Learning_Basics",
		},
		{
			name:            "underscore and hyphen stay untouched",
			input:           "AI_101 - Intro",
			wantDisplayName: "AI_101 - Intro",
			wantInternal:    "AI_101_-_Intro",
		},
		{
			name:            "empty after trim",
			input:           "   ",
			wantDisplayName: "",
			wantInternal:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDisplayName, gotInternal := NormalizeCourseEnglishName(tt.input)
			if gotDisplayName != tt.wantDisplayName {
				t.Fatalf("display_name = %q, want %q", gotDisplayName, tt.wantDisplayName)
			}
			if gotInternal != tt.wantInternal {
				t.Fatalf("internal_name = %q, want %q", gotInternal, tt.wantInternal)
			}
		})
	}
}
