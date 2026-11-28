// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

func (p *Pipeline) executeParallelToolCalls(
	turnCtx context.Context,
	ts *turnState,
	exec *turnExecution,
	toolBatchID string,
	normalizedToolCalls []providers.ToolCall,
) ([]*tools.ToolResult, []string, []time.Duration, bool) {
	al := p.al
	cfg := p.Cfg

	preExecutedResults := make([]*tools.ToolResult, len(normalizedToolCalls))
	parallelActivities := make([]string, len(normalizedToolCalls))
	parallelDurations := make([]time.Duration, len(normalizedToolCalls))

	for i, call := range normalizedToolCalls {
		args, _ := json.Marshal(call.Arguments)
		parallelActivities[i] = ts.startTurnActivity("tool", call.Name, call.ID, call.Name, utils.Truncate(cfg.RedactDiagnostic(string(args)), 200))
		ts.markToolItemRunning(toolBatchID, i, call.Name, call.Arguments)
	}
	if !ts.opts.NoHistory {
		if err := ts.saveTurnCheckpoint("running", "parallel_tools"); err != nil {
			exec.executionErr = err
			return nil, nil, nil, false
		}
	}

	// Bound workers independently of the number of tool calls produced by a model.
	jobs := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < min(4, len(normalizedToolCalls)); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				call := normalizedToolCalls[i]
				if turnCtx.Err() != nil || ts.hardAbortRequested() {
					ts.markToolItemTerminal(toolBatchID, i, toolItemStatusCancelled, toolItemSideEffectNone, "Tool cancelled before execution.")
					preExecutedResults[i] = tools.ErrorResult("Tool cancelled before execution.")
					ts.finishTurnActivity(parallelActivities[i], func(a *session.TurnActivity) { a.Status = "cancelled"; a.Detail = "Tool cancelled before execution." })
					continue
				}
				start := time.Now()
				ts.progress.markActivityStarted(parallelActivities[i], start)
				execCtx := tools.WithToolInboundContext(turnCtx, ts.channel, ts.chatID, ts.opts.Dispatch.MessageID(), ts.opts.Dispatch.ReplyToMessageID())
				execCtx = tools.WithToolSessionContext(execCtx, ts.agent.ID, ts.sessionKey, ts.opts.Dispatch.SessionScope)
				callID := toolCallIDFromCall(call)
				execCtx = tools.WithToolCallID(execCtx, callID)
				al.emitEvent(runtimeevents.KindAgentToolExecStart, ts.eventMeta("runTurn", "turn.tool.start"), ToolExecStartPayload{CallID: callID, Tool: call.Name, Arguments: cloneEventArguments(call.Arguments)})
				result := ts.toolRegistry().ExecuteWithContext(execCtx, call.Name, cloneStringAnyMap(call.Arguments), ts.channel, ts.chatID, nil)
				if result == nil {
					result = tools.ErrorResult("tool returned nil result")
				}
				ts.finishToolItemFromResult(al, toolBatchID, i, ts.toolPolicy(call.Name), result, false)
				preExecutedResults[i] = result
				parallelDurations[i] = time.Since(start)
				al.emitEvent(runtimeevents.KindAgentToolExecEnd, ts.eventMeta("runTurn", "turn.tool.end"), ToolExecEndPayload{CallID: callID, Tool: call.Name, Duration: parallelDurations[i], ForLLMLen: len(result.ContentForLLM()), ForUserLen: len(result.ForUser), IsError: result.IsError, Async: result.Async, Error: toolErrorSummary(cfg, result)})
				ts.finishTurnActivity(parallelActivities[i], func(a *session.TurnActivity) {
					a.Status = "completed"
					if result.IsError {
						a.Status = "failed"
					}
					a.Output = boundedActivityOutput(cfg.RedactDiagnostic(result.ContentForLLM()), cfg.RedactDiagnostic(result.ForUser))
				})
			}
		}()
	}
	for i := range normalizedToolCalls {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return preExecutedResults, parallelActivities, parallelDurations, true
}
