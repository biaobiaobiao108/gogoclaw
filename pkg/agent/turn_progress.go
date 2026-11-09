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

type toolProgressDecision struct {
	Hint          string
	EndReason     TurnEndReason
	InterruptHint string
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

func (ts *turnState) evaluateToolProgress(outcome toolProgressOutcome, retrieval bool) toolProgressDecision {
	if ts == nil {
		return toolProgressDecision{}
	}
	policy := ts.budget.Progress
	if retrieval {
		if outcome.RetrievalStalls == policy.RetrievalWarningAfter {
			return toolProgressDecision{
				Hint: "\n\nThree consecutive web retrieval attempts returned no usable results. Do not keep rephrasing the same searches. Use the evidence already collected, try a materially different source, or explain what could not be verified.",
			}
		}
		if outcome.RetrievalStalls >= policy.RetrievalStopAfter {
			return toolProgressDecision{
				EndReason:     TurnEndReasonRetrievalStalled,
				InterruptHint: "Web retrieval repeatedly returned no usable results. Stop retrying and summarize the available evidence and limitations.",
			}
		}
		return toolProgressDecision{}
	}

	if outcome.NoProgressStreak == policy.NoProgressWarningAfter {
		return toolProgressDecision{
			Hint: "\n\nSeveral tool calls produced no new evidence or state changes. Stop exploring in circles. Use the existing findings or explain what is still missing.",
		}
	}
	if outcome.NoProgressStreak >= policy.NoProgressStopAfter {
		return toolProgressDecision{
			EndReason:     TurnEndReasonNoProgress,
			InterruptHint: "The turn produced no new evidence or state changes across repeated tool calls. Stop retrying and summarize the available findings and limitations.",
		}
	}
	return toolProgressDecision{}
}

func (ts *turnState) evaluateRepeatedFailure(failures int) toolProgressDecision {
	if ts == nil {
		return toolProgressDecision{}
	}
	policy := ts.budget.Progress
	if failures == policy.RepeatedFailureWarningAfter {
		return toolProgressDecision{
			Hint: "\n\nThis same tool call has failed three times. Change the approach or explain the blocker instead of repeating it.",
		}
	}
	if failures >= policy.RepeatedFailureStopAfter {
		return toolProgressDecision{
			EndReason:     TurnEndReasonRepeatedFailure,
			InterruptHint: "The same tool call repeatedly failed. Explain the blocker, completed work, and what is needed to continue.",
		}
	}
	return toolProgressDecision{}
}
