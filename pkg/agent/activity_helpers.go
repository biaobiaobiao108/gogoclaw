package agent

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
)

var exitCodePattern = regexp.MustCompile(`"exit_code"\s*:\s*(-?\d+)`)

func toolCallIDFromCall(call providers.ToolCall) string {
	return strings.TrimSpace(call.ID)
}

func boundedActivityOutput(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		runes := []rune(value)
		if len(runes) > 4000 {
			return string(runes[:4000]) + "\n…"
		}
		return value
	}
	return ""
}

func activityDiffPreview(content string) string {
	start := strings.Index(content, "```diff\n")
	if start < 0 {
		return ""
	}
	start += len("```diff\n")
	end := strings.Index(content[start:], "\n```")
	if end < 0 {
		return ""
	}
	return content[start : start+end]
}

func activityExitCode(content string) *int {
	match := exitCodePattern.FindStringSubmatch(content)
	if len(match) != 2 {
		return nil
	}
	code, err := strconv.Atoi(match[1])
	if err != nil {
		return nil
	}
	return &code
}
