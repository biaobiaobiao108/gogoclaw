package utils

import (
	"strings"
)

func normalizeToolFeedbackComparisonText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func ToolFeedbackExplanationMatchesContent(explanation, content string) bool {
	normalizedExplanation := normalizeToolFeedbackComparisonText(explanation)
	return normalizedExplanation != "" && normalizedExplanation == normalizeToolFeedbackComparisonText(content)
}
