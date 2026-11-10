package agent

import (
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
