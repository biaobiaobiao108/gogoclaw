package agent

import (
	"fmt"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

func (ts *turnState) finishTurnCheckpoint(turnStatus TurnEndStatus, result turnResult, turnErr error) error {
	var status string
	switch {
	case turnStatus == TurnEndStatusAborted:
		status = "interrupted"
	case turnStatus == TurnEndStatusError || turnErr != nil || result.status == "":
		status = "failed"
	case ts.endReasonSnapshot() != TurnEndReasonCompleted:
		status = "interrupted"
	default:
		status = "completed"
	}
	if err := ts.saveTurnCheckpoint(status, ""); err != nil && turnErr == nil {
		return fmt.Errorf("save turn checkpoint: %w", err)
	}
	return turnErr
}

func (ts *turnState) saveTurnCheckpoint(status, tool string) error {
	store, ok := ts.agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		return nil
	}
	var plan []session.TurnPlanStep
	for _, step := range ts.snapshot().Plan {
		plan = append(plan, session.TurnPlanStep{Text: step.Text, Status: step.Status})
	}
	al := ts.al
	if al == nil {
		return fmt.Errorf("agent loop is unavailable")
	}
	return al.updateTurnCheckpoint(store, ts.sessionKey, func(previous *session.TurnCheckpoint) error {
		checkpoint := session.TurnCheckpoint{
			TurnID: ts.turnID, Status: status, Iteration: ts.currentIteration(), Tool: tool,
			ReplyTurnID: ts.opts.ResumeReplyTurnID,
			EndReason:   string(ts.endReasonSnapshot()),
			StartedAt:   ts.startedAt.UTC(), UpdatedAt: time.Now().UTC(),
			Activities: ts.activitiesSnapshot(),
			ToolItems:  ts.toolItemsSnapshot(),
			Usage:      ts.contextUsageSnapshot(),
			Plan:       plan,
		}
		checkpoint.Deliveries = append([]session.TurnDelivery(nil), previous.Deliveries...)
		checkpoint.Delivery = previous.Delivery
		checkpoint.Approval = previous.Approval
		starting := status == "running" && checkpoint.Iteration == 0
		checkpoint.Iteration += ts.opts.ResumeIterationOffset
		if ts.tools != nil {
			if starting {
				ts.tools.RestoreUsedDiscoveredTools(previous.UsedDiscoveredTools)
			}
			checkpoint.UsedDiscoveredTools = ts.tools.UsedDiscoveredTools()
		}
		if previous.TurnID == ts.turnID && !starting {
			checkpoint.Events = append(checkpoint.Events, previous.Events...)
			if status == "failed" || status == "interrupted" {
				checkpoint.Tool = previous.Tool
			}
		}
		checkpoint.Events = append(checkpoint.Events, session.TurnCheckpointEvent{Status: status, Iteration: checkpoint.Iteration, Tool: checkpoint.Tool, At: time.Now().UTC(), Plan: plan})
		if overflow := len(checkpoint.Events) - maxTurnCheckpointEvents; overflow > 0 {
			checkpoint.Events = append([]session.TurnCheckpointEvent(nil), checkpoint.Events[overflow:]...)
		}
		*previous = checkpoint
		return nil
	})
}
