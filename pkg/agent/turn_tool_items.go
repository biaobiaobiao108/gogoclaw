package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

const (
	toolItemStatusPending          = "pending"
	toolItemStatusAwaitingApproval = "awaiting_approval"
	toolItemStatusRunning          = "running"
	toolItemStatusCompleted        = "completed"
	toolItemStatusFailed           = "failed"
	toolItemStatusDenied           = "denied"
	toolItemStatusSkipped          = "skipped"
	toolItemStatusCancelled        = "cancelled"
	toolItemStatusEffectsUnknown   = "effects_unknown"

	toolItemSideEffectNone     = "none"
	toolItemSideEffectPossible = "possible"
	toolItemSideEffectUnknown  = "unknown"

	maxTurnToolItems = 256
)

func (ts *turnState) beginToolBatch(iteration int, calls []providers.ToolCall) string {
	if ts == nil || len(calls) == 0 {
		return ""
	}

	policies := make([]tools.ToolPolicy, len(calls))
	for i, call := range calls {
		policies[i] = ts.toolPolicy(call.Name)
	}

	return ts.progress.beginToolBatch(ts.turnID, iteration, calls, policies)
}

func (ts *turnState) markToolItemAwaitingApproval(batchID string, index int, toolName string, args map[string]any) {
	ts.updateToolItem(batchID, index, func(item *session.TurnToolItem) {
		item.Status = toolItemStatusAwaitingApproval
		item.Tool = toolName
		item.ArgumentsFingerprint = toolCallFingerprint(toolName, args)
	})
}

func (ts *turnState) markToolItemRunning(batchID string, index int, toolName string, args map[string]any) {
	policy := ts.toolPolicy(toolName)
	ts.updateToolItem(batchID, index, func(item *session.TurnToolItem) {
		now := time.Now().UTC()
		if item.StartedAt == nil {
			item.StartedAt = &now
		}
		item.Status = toolItemStatusRunning
		item.Tool = toolName
		item.ArgumentsFingerprint = toolCallFingerprint(toolName, args)
		item.ReadOnly = policy.ReadOnly
		item.Idempotent = policy.Idempotent
		item.Destructive = policy.Destructive
		item.SideEffect = toolItemSideEffectPossible
		if policy.ReadOnly {
			item.SideEffect = toolItemSideEffectNone
		}
	})
}

func (ts *turnState) finishToolItem(
	batchID string,
	index int,
	status string,
	sideEffect string,
	resultSummary string,
	errorSummary string,
) {
	ts.updateToolItem(batchID, index, func(item *session.TurnToolItem) {
		now := time.Now().UTC()
		item.Status = status
		if sideEffect != "" {
			item.SideEffect = sideEffect
		}
		item.ResultSummary = resultSummary
		item.ErrorSummary = errorSummary
		item.CompletedAt = &now
	})
}

func (ts *turnState) markToolItemTerminal(
	batchID string,
	index int,
	status string,
	sideEffect string,
	summary string,
) {
	ts.finishToolItem(batchID, index, status, sideEffect, summary, "")
}

func (ts *turnState) updateToolItem(batchID string, index int, update func(*session.TurnToolItem)) {
	if ts == nil || batchID == "" || update == nil {
		return
	}
	ts.progress.updateToolItem(batchID, index, update)
}

func (ts *turnState) toolItemsSnapshot() []session.TurnToolItem {
	if ts == nil {
		return nil
	}
	return ts.progress.toolItemsSnapshot()
}

// sideEffectFailureNotice makes failed state-changing work visible without
// relying on the model to describe its own tool failures accurately. The last
// attempt for the same tool and arguments wins, so a successful retry clears an
// earlier failure without hiding a failure from a different operation.
func (ts *turnState) sideEffectFailureNotice(finalContent string) string {
	items := ts.toolItemsSnapshot()
	latest := make(map[string]session.TurnToolItem, len(items))
	for _, item := range items {
		if item.ReadOnly || strings.TrimSpace(item.Tool) == "" {
			continue
		}
		latest[item.Tool+"\x00"+item.ArgumentsFingerprint] = item
	}
	var uncertain []string
	var failed []string
	var denied []string
	for _, item := range latest {
		switch item.Status {
		case toolItemStatusFailed:
			if item.SideEffect != toolItemSideEffectNone {
				failed = append(failed, item.Tool)
			}
		case toolItemStatusEffectsUnknown:
			uncertain = append(uncertain, item.Tool)
		case toolItemStatusDenied:
			if !finalContentMentions(finalContent, item.ResultSummary) {
				denied = append(denied, item.Tool)
			}
		}
	}
	if len(uncertain) > 0 {
		return fmt.Sprintf(
			"Action verification notice: %s did not return a verifiable result. Its effects may be unknown; inspect the external state before relying on this answer.",
			strings.Join(sortedStrings(uncertain), ", "),
		)
	}
	if len(failed) > 0 {
		return fmt.Sprintf(
			"Action verification notice: %s did not complete successfully. Do not treat this answer as confirmation that the requested change was applied.",
			strings.Join(sortedStrings(failed), ", "),
		)
	}
	if len(denied) > 0 {
		return fmt.Sprintf(
			"Action verification notice: %s was denied before execution. Do not treat this answer as confirmation that the requested change was applied.",
			strings.Join(sortedStrings(denied), ", "),
		)
	}
	return ""
}

func finalContentMentions(finalContent, detail string) bool {
	finalContent = strings.TrimSpace(finalContent)
	detail = strings.TrimSpace(detail)
	return finalContent != "" && detail != "" && strings.Contains(finalContent, detail)
}

func sortedStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	return values
}
