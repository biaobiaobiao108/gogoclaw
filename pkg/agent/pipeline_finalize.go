// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

// Finalize handles turn finalization, either:
// - Early return when allResponsesHandled=true (ExecuteTools already finalized)
// - Normal finalization for allResponsesHandled=false (sets finalContent, saves session, compact)
func (p *Pipeline) Finalize(
	ctx context.Context,
	turnCtx context.Context,
	ts *turnState,
	exec *turnExecution,
	turnStatus TurnEndStatus,
	finalContent string,
) (turnResult, error) {
	al := p.al

	// When allResponsesHandled=true, ExecuteTools already finalized
	// (added handledToolResponseSummary, saved session, set phase to Completed).
	// But still check for hard abort - if requested, abort the turn.
	if exec.allResponsesHandled {
		if ts.hardAbortRequested() {
			return al.abortTurn(ts)
		}
		ts.setPhase(TurnPhaseCompleted)
		return turnResult{
			finalContent: finalContent,
			modelName:    exec.llmModelName,
			status:       turnStatus,
			followUps:    append([]bus.InboundMessage(nil), ts.followUps...),
		}, nil
	}

	responseID := assistantResponseID(ts, exec)
	ts.setPhase(TurnPhaseFinalizing)
	ts.setFinalContent(finalContent)
	if !ts.opts.NoHistory {
		finalMsg := providers.Message{
			ID:               responseID,
			Role:             "assistant",
			Content:          finalContent,
			ModelName:        exec.llmModelName,
			ReasoningContent: responseReasoningContent(exec.response),
		}
		if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, finalMsg); err != nil {
			return turnResult{status: TurnEndStatusError}, fmt.Errorf("write final message: %w", err)
		}
		ts.recordPersistedMessage(finalMsg)
		if err := ts.ingestMessage(turnCtx, al, finalMsg); err != nil {
			return turnResult{status: TurnEndStatusError}, err
		}
		if err := ts.agent.Sessions.Save(ts.sessionKey); err != nil {
			al.emitEvent(
				runtimeevents.KindAgentError,
				ts.eventMeta("runTurn", "turn.error"),
				ErrorPayload{
					Stage:   "session_save",
					Message: err.Error(),
				},
			)
			cancelConfiguredStreamingLLM(turnCtx, exec)
			return turnResult{status: TurnEndStatusError}, err
		}
		branchGroupID := ""
		if inbound := ts.opts.Dispatch.InboundContext; inbound != nil {
			branchGroupID = strings.TrimSpace(inbound.Raw["branch_group_id"])
		}
		if branchGroupID != "" {
			if history, historyErr := session.TranscriptChecked(ts.agent.Sessions, ts.sessionKey); historyErr == nil {
				if branchErr := al.recordBranchSnapshot(ts, branchGroupID, finalMsg, history); branchErr != nil {
					logger.WarnCF("agent", "Failed to record chat branch snapshot", map[string]any{
						"session_key": ts.sessionKey,
						"message_id":  finalMsg.ID,
						"error":       branchErr.Error(),
					})
				}
			}
		}
	}

	if !ts.opts.NoHistory && ts.opts.EnableSummary {
		if err := al.contextManager.Compact(
			turnCtx,
			&CompactRequest{
				SessionKey: ts.sessionKey,
				Reason:     ContextCompressReasonSummarize,
				Budget:     ts.agent.ContextWindow,
			},
		); err != nil {
			logger.WarnCF("agent", "Post-turn compaction failed", map[string]any{
				"session_key": ts.sessionKey,
				"turn_id":     ts.turnID,
				"error":       err.Error(),
			})
		}
	}

	contextUsage := ts.contextUsageSnapshot()
	if contextUsage == nil {
		contextUsage = computeContextUsage(ts.agent, ts.sessionKey)
	}
	streamPublished, streamErr := finalizeConfiguredStreamingLLM(al, turnCtx, ts, exec, finalContent, contextUsage)
	responsePublished := streamErr == nil && streamPublished
	// If streaming never became visible, keep the legacy Pico interim publish path
	// so the final answer is still delivered outside normal SendResponse.
	if ((streamErr != nil && !isConfiguredStreamingVisibleError(streamErr)) || exec.streamingFallback) &&
		!ts.opts.SendResponse && ts.opts.AllowInterimPicoPublish && finalContent != "" {
		msg := outboundMessageForTurnWithOptions(ts, finalContent, outboundTurnMessageOptions{
			modelName: exec.llmModelName,
		})
		msg.ID = responseID
		msg.ContextUsage = contextUsage
		if err := al.publishTrackedOutbound(turnCtx, ts.sessionKey, msg); err != nil {
			ts.setPhase(TurnPhaseCompleted)
			return turnResult{
				finalContent: finalContent,
				status:       TurnEndStatusError,
				followUps:    append([]bus.InboundMessage(nil), ts.followUps...),
			}, err
		}
		responsePublished = true
	}
	if streamErr != nil && isConfiguredStreamingVisibleError(streamErr) {
		ts.setPhase(TurnPhaseCompleted)
		return turnResult{
			finalContent: finalContent,
			status:       TurnEndStatusError,
			followUps:    append([]bus.InboundMessage(nil), ts.followUps...),
		}, streamErr
	}
	ts.setPhase(TurnPhaseCompleted)
	return turnResult{
		finalContent:      finalContent,
		modelName:         exec.llmModelName,
		status:            turnStatus,
		followUps:         append([]bus.InboundMessage(nil), ts.followUps...),
		responsePublished: responsePublished,
	}, nil
}
