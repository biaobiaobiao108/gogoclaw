package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tokenizer"
)

const (
	maxToolResultPromptTokens    = 12 * 1024
	minToolResultPromptTokens    = 512
	toolResultPromptSharePct     = 8
	toolResultNoticeTokenReserve = 160
)

type compactedToolResult struct {
	Content  string
	Artifact string
}

func (al *AgentLoop) compactToolResultForPrompt(
	ts *turnState,
	tool, toolCallID, content string,
) compactedToolResult {
	tokenBudget := toolResultPromptTokenBudget(ts.agent.ContextWindow)
	if tokenizer.EstimateTextTokens(content) <= tokenBudget {
		return compactedToolResult{Content: content}
	}

	artifactPath, writeErr := persistToolResultArtifact(ts, tool, toolCallID, content)
	if writeErr != nil {
		logger.WarnCF("agent", "Failed to persist oversized tool result", map[string]any{
			"tool":  tool,
			"error": writeErr.Error(),
		})
	}

	retainedTokens := max(2, tokenBudget-toolResultNoticeTokenReserve)
	headBudget := retainedTokens * 3 / 4
	tailBudget := retainedTokens - headBudget
	head := truncateHeadToTokenBudget(content, headBudget)
	tail := truncateTailToTokenBudget(content, tailBudget)
	note := "\n\n[Tool result compacted to protect the context budget.]\n"
	if artifactPath != "" {
		note += "Full output: " + artifactPath + "\nUse read_file with pagination parameters (offset/length or start_line/max_lines) if the omitted middle section is required.\n"
	} else {
		note += "Full output could not be persisted; the middle section is unavailable.\n"
	}
	return compactedToolResult{
		Content:  head + note + tail,
		Artifact: artifactPath,
	}
}

func toolResultPromptTokenBudget(contextWindow int) int {
	total := maxToolResultPromptTokens
	if contextWindow > 0 {
		// Keep one tool result below roughly 1/8 of the model window, leaving
		// room for history, tool schemas, and the output reserve.
		windowBudget := contextWindow / toolResultPromptSharePct
		if windowBudget < minToolResultPromptTokens {
			windowBudget = minToolResultPromptTokens
		}
		if windowBudget < total {
			total = windowBudget
		}
	}
	return total
}

func persistToolResultArtifact(ts *turnState, tool, toolCallID, content string) (string, error) {
	if ts == nil || ts.agent == nil || strings.TrimSpace(ts.agent.Workspace) == "" {
		return "", fmt.Errorf("workspace is unavailable")
	}
	name := strings.TrimSpace(toolCallID)
	if name == "" {
		name = uuid.NewString()
	}
	name = sanitizeToolOutputName(tool + "-" + name)
	path := filepath.Join(ts.agent.Workspace, "state", "tool_outputs", name+".txt")
	if err := fileutil.WriteFileAtomic(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func sanitizeToolOutputName(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
		if b.Len() >= 96 {
			break
		}
	}
	result := strings.Trim(b.String(), "-.")
	if result == "" {
		return "tool-output"
	}
	return result
}

func truncateHeadToTokenBudget(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if tokenizer.EstimateTextTokens(value) <= limit {
		return value
	}
	low, high := 0, len(runes)-1
	for low < high {
		mid := (low + high + 1) / 2
		if tokenizer.EstimateTextTokens(string(runes[:mid])) <= limit {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return string(runes[:low])
}

func truncateTailToTokenBudget(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if tokenizer.EstimateTextTokens(value) <= limit {
		return value
	}
	low, high := 0, len(runes)-1
	for low < high {
		mid := (low + high + 1) / 2
		if tokenizer.EstimateTextTokens(string(runes[len(runes)-mid:])) <= limit {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return string(runes[len(runes)-low:])
}
