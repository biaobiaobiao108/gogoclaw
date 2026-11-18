package utils

import "testing"

func TestToolFeedbackExplanationMatchesContent(t *testing.T) {
	tests := []struct {
		name        string
		explanation string
		content     string
		want        bool
	}{
		{"exact duplicate", "Read the file.", "Read the file.", true},
		{"normalized whitespace", "Read   the file.\nThen reply.", " Read the file. Then reply. ", true},
		{"distinct text", "Read the file.", "I will summarize my findings.", false},
		{"empty text", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToolFeedbackExplanationMatchesContent(tt.explanation, tt.content); got != tt.want {
				t.Errorf("match = %v, want %v", got, tt.want)
			}
		})
	}
}
