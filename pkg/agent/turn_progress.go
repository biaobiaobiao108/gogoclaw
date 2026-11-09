package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

type toolProgressOutcome struct {
	Progress         tools.ToolProgress
	NoProgressStreak int
	RetrievalStalls  int
}

func (ts *turnState) toolPolicy(name string) tools.ToolPolicy {
	if ts == nil {
		return tools.StatePolicy()
	}
	registry := ts.toolRegistry()
	if registry != nil {
		if tool, ok := registry.Get(name); ok {
			return tools.PolicyFor(tool)
		}
	}
	return tools.PolicyForName(name)
}

func (ts *turnState) recordToolProgress(
	fingerprint string,
	policy tools.ToolPolicy,
	result *tools.ToolResult,
	duplicate bool,
) toolProgressOutcome {
	outcome := toolProgressOutcome{Progress: tools.ToolProgressNone}
	usable := !duplicate && toolResultHasUsableContent(result)
	if policy.Retrieval && !duplicate {
		outcome.RetrievalStalls = ts.recordRetrievalOutcome(usable)
	}

	if !usable {
		outcome.NoProgressStreak = ts.recordNoProgress()
		return outcome
	}

	switch policy.Progress {
	case tools.ToolProgressState:
		ts.recordToolStateChange()
		outcome.Progress = tools.ToolProgressState
		outcome.NoProgressStreak = 0
	case tools.ToolProgressEvidence:
		content := strings.TrimSpace(result.ContentForLLM())
		hash := sha256.Sum256([]byte(fingerprint + "\x00" + content))
		ts.recordEvidence(hex.EncodeToString(hash[:]))
		outcome.Progress = tools.ToolProgressEvidence
		outcome.NoProgressStreak = ts.currentNoProgressStreak()
	default:
		outcome.NoProgressStreak = ts.recordNoProgress()
	}
	return outcome
}

func (ts *turnState) currentNoProgressStreak() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.noProgressStreak
}
