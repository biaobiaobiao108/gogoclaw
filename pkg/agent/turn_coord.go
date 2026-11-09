// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

const maxTurnCheckpointEvents = 128

func (al *AgentLoop) runTurn(ctx context.Context, ts *turnState, pipeline *Pipeline) (result turnResult, retErr error) {
	if ts.al == nil {
		ts.al = al
	}
	if err := ts.transitionPhase(TurnPhaseSetup); err != nil {
		return turnResult{}, err
	}
	if ts.concurrencySem == nil {
		ts.concurrencySem = make(chan struct{}, al.getSubTurnConfig().maxConcurrent)
	}
	if ts != nil && ts.agent != nil && ts.agent.Tools != nil {
		ts.tools = ts.agent.Tools.Clone()
		ts.tools.Register(&turnPlanTool{turn: ts})
	}
	if ts != nil && ts.agent != nil {
		modelMu := ts.agent.modelStateMutex()
		modelMu.RLock()
		defer modelMu.RUnlock()
	}
	turnCtx, turnCancel := context.WithCancel(ctx)
	defer turnCancel()
	ts.setTurnCancel(turnCancel)

	// Inject turnState and AgentLoop into context so tools (e.g. spawn) can retrieve them.
	turnCtx = withTurnState(turnCtx, ts)
	turnCtx = WithAgentLoop(turnCtx, al)

	al.registerActiveTurn(ts)
	defer al.flushDeferredFollowUps(ts)
	defer al.clearActiveTurn(ts)
	turnStatus := TurnEndStatusCompleted
	if ts.agent != nil {
		var providerRefs []providers.LLMProvider
		addProvider := func(provider providers.LLMProvider) {
			if !providerRefComparable(provider) {
				return
			}
			for _, existing := range providerRefs {
				if existing == provider {
					return
				}
			}
			providerRefs = append(providerRefs, provider)
			al.acquireProviderRef(provider)
		}
		addProvider(ts.agent.Provider)
		addProvider(ts.agent.LightProvider)
		defer func() {
			for _, provider := range providerRefs {
				al.releaseProviderRef(provider)
			}
		}()
	}
	defer func() {
		ts.Finish(ts.phaseSnapshot() == TurnPhaseAborted || ts.hardAbortRequested())
	}()
	defer func() {
		if r := recover(); r != nil {
			turnStatus = TurnEndStatusError
			_ = ts.finishLifecycle(TurnPhaseFailed)
			ts.Finish(true)
			panic(r)
		}
	}()
	defer func() {
		ts.mu.Lock()
		refs := ts.steeringMedia
		ts.steeringMedia = nil
		ts.mu.Unlock()
		media.Unpin(al.mediaStore, refs)
	}()

	if al.takePendingStop(ts.sessionKey) {
		_ = ts.requestHardAbort()
	}

	defer func() {
		if retErr != nil {
			ts.setEndReason(TurnEndReasonError)
		}
		attemptedSkills := ts.attemptedSkillsSnapshot()
		skillContextSnapshots := ts.skillContextSnapshotsSnapshot()
		finalSuccessfulPath := []string(nil)
		if turnStatus == TurnEndStatusCompleted {
			if latest := ts.latestSkillContextSnapshot(); len(latest) > 0 {
				finalSuccessfulPath = latest
			} else {
				finalSuccessfulPath = append([]string(nil), attemptedSkills...)
			}
		}
		al.emitEvent(
			runtimeevents.KindAgentTurnEnd,
			ts.eventMeta("runTurn", "turn.end"),
			TurnEndPayload{
				Status:                turnStatus,
				Reason:                ts.endReasonSnapshot(),
				Workspace:             ts.workspace,
				Iterations:            ts.currentIteration(),
				Duration:              time.Since(ts.startedAt),
				FinalContentLen:       ts.finalContentLen(),
				UserMessage:           ts.userMessage,
				FinalContent:          ts.finalContentSnapshot(),
				ActiveSkills:          append([]string(nil), ts.activeSkills...),
				AttemptedSkills:       attemptedSkills,
				FinalSuccessfulPath:   finalSuccessfulPath,
				SkillContextSnapshots: skillContextSnapshots,
				ToolKinds:             ts.toolKindsSnapshot(),
				ToolExecutions:        ts.toolExecutionsSnapshot(),
				PhaseHistory:          ts.phaseHistory(),
			},
		)
	}()
	defer func() {
		terminal := TurnPhaseCompleted
		switch {
		case ts.hardAbortRequested() || turnStatus == TurnEndStatusAborted:
			terminal = TurnPhaseAborted
		case retErr != nil || turnStatus == TurnEndStatusError || result.status == TurnEndStatusError:
			terminal = TurnPhaseFailed
		}
		if err := ts.finishLifecycle(terminal); err != nil {
			logger.ErrorCF("agent", "Illegal turn lifecycle transition", map[string]any{
				"turn_id": ts.turnID,
				"phase":   ts.phaseSnapshot(),
				"target":  terminal,
				"error":   err.Error(),
			})
			if retErr == nil {
				retErr = err
			}
		}
	}()
	defer func() {
		if r := recover(); r != nil {
			turnStatus = TurnEndStatusError
			_ = ts.finishLifecycle(TurnPhaseFailed)
			ts.Finish(true)
			panic(r)
		}
	}()
	al.emitEvent(
		runtimeevents.KindAgentTurnStart,
		ts.eventMeta("runTurn", "turn.start"),
		TurnStartPayload{
			UserMessage: ts.userMessage,
			MediaCount:  len(ts.media),
		},
	)
	if !ts.opts.NoHistory {
		if err := ts.saveTurnCheckpoint("running", ""); err != nil {
			turnStatus = TurnEndStatusError
			retErr = fmt.Errorf("save turn checkpoint: %w", err)
			return turnResult{}, retErr
		}
		defer func() {
			status := "completed"
			reason := ts.endReasonSnapshot()
			if turnStatus == TurnEndStatusAborted {
				status = "interrupted"
			} else if turnStatus == TurnEndStatusError || retErr != nil || result.status == "" {
				status = "failed"
			} else if reason != TurnEndReasonCompleted {
				status = "interrupted"
			}
			if err := ts.saveTurnCheckpoint(status, ""); err != nil && retErr == nil {
				retErr = fmt.Errorf("save turn checkpoint: %w", err)
			}
		}()
	}

	if ts.hardAbortRequested() {
		turnStatus = TurnEndStatusAborted
		return al.abortTurn(ts)
	}

	// SetupTurn extracts the one-time initialization phase.
	exec, err := pipeline.SetupTurn(turnCtx, ts)
	if err != nil {
		turnStatus = TurnEndStatusError
		return turnResult{}, err
	}
	defer exec.closeOwnedProviders()

	if err := ts.transitionPhase(TurnPhaseRunning); err != nil {
		turnStatus = TurnEndStatusError
		return turnResult{}, err
	}

	// Convenience references to exec fields used throughout the turn loop.
	messages := exec.messages
	pendingMessages := exec.pendingMessages
	maxMediaSize := pipeline.Cfg.Agents.Defaults.GetMaxMediaSize()
	finalContent := exec.finalContent
	finalizedEarly := false

turnLoop:
	for {
		switch ts.phaseSnapshot() {
		case TurnPhaseRunning:
			if !ts.canStartModelCall(len(exec.pendingMessages) > 0) {
				if err := ts.transitionPhase(TurnPhaseFinalizing); err != nil {
					turnStatus = TurnEndStatusError
					return turnResult{}, err
				}
				continue
			}
			if ts.hardAbortRequested() {
				turnStatus = TurnEndStatusAborted
				return al.abortTurn(ts)
			}

			iteration := ts.currentIteration() + 1
			ts.setIteration(iteration)
			if !ts.opts.NoHistory {
				if err := ts.saveTurnCheckpoint("running", ""); err != nil {
					turnStatus = TurnEndStatusError
					return turnResult{}, fmt.Errorf("save turn checkpoint: %w", err)
				}
			}

			if iteration > 1 {
				// For subsequent iterations, read from exec.pendingMessages which
				// is where ExecuteTools (or initial poll) deposits steering.
				if len(exec.pendingMessages) > 0 {
					pendingMessages = append(pendingMessages, exec.pendingMessages...)
					exec.pendingMessages = nil
				}
			} else if !ts.opts.SkipInitialSteeringPoll {
				if steerMsgs := al.dequeueSteeringMessagesForScopeWithFallback(ts.sessionKey); len(steerMsgs) > 0 {
					pendingMessages = append(pendingMessages, steerMsgs...)
				}
			}

			// Check if parent turn has ended (SubTurn support from HEAD)
			if ts.parentTurnState != nil && ts.IsParentEnded() {
				if !ts.critical {
					logger.InfoCF("agent", "Parent turn ended, non-critical SubTurn exiting gracefully", map[string]any{
						"agent_id":  ts.agentID,
						"iteration": iteration,
						"turn_id":   ts.turnID,
					})
					break turnLoop
				}
				logger.InfoCF("agent", "Parent turn ended, critical SubTurn continues running", map[string]any{
					"agent_id":  ts.agentID,
					"iteration": iteration,
					"turn_id":   ts.turnID,
				})
			}

			// Inject pending steering messages
			if len(pendingMessages) > 0 {
				resolvedPending := resolveMediaRefs(pendingMessages, al.mediaStore, maxMediaSize, 0)
				totalContentLen := 0
				for i, pm := range pendingMessages {
					messages = append(messages, resolvedPending[i])
					totalContentLen += len(pm.Content)
					if !ts.opts.NoHistory {
						if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, pm); err != nil {
							turnStatus = TurnEndStatusError
							return turnResult{}, fmt.Errorf("write steering message: %w", err)
						}
						ts.recordPersistedMessage(pm)
						if err := ts.ingestMessage(turnCtx, al, pm); err != nil {
							turnStatus = TurnEndStatusError
							return turnResult{}, err
						}
					}
					logger.InfoCF("agent", "Injected steering message into context",
						map[string]any{
							"agent_id":    ts.agent.ID,
							"iteration":   iteration,
							"content_len": len(pm.Content),
							"media_count": len(pm.Media),
						})
				}
				al.emitEvent(
					runtimeevents.KindAgentSteeringInjected,
					ts.eventMeta("runTurn", "turn.steering.injected"),
					SteeringInjectedPayload{
						Count:           len(pendingMessages),
						TotalContentLen: totalContentLen,
					},
				)
				exec.pendingMessages = nil
			}

			// Async completions are consumed exactly once, here, before the
			// next model call. No pipeline stage polls a private result channel.
			if err := al.consumeAsyncDeliveries(turnCtx, ts, &messages); err != nil {
				turnStatus = TurnEndStatusError
				return turnResult{}, err
			}

			if ts.maxContextRunes > 0 {
				before := len(messages)
				messages = trimMessagesToRuneLimit(messages, ts.maxContextRunes)
				if len(messages) != before {
					logger.DebugCF("agent", "Trimmed SubTurn context to rune limit", map[string]any{
						"turn_id":           ts.turnID,
						"messages_before":   before,
						"messages_after":    len(messages),
						"max_context_runes": ts.maxContextRunes,
					})
				}
			}
			exec.messages = messages

			logger.DebugCF("agent", "LLM iteration",
				map[string]any{
					"agent_id":  ts.agent.ID,
					"iteration": iteration,
					"max":       ts.budget.MaxIterations,
				})

			ctrl, callErr := pipeline.CallLLM(ctx, turnCtx, ts, exec, iteration)
			if callErr != nil {
				ts.setEndReason(TurnEndReasonProviderError)
				turnStatus = TurnEndStatusError
				return turnResult{}, callErr
			}
			messages = exec.messages
			pendingMessages = exec.pendingMessages
			finalContent = exec.finalContent

			switch ctrl {
			case ControlContinue:
			case ControlBreak:
				if exec.abortedByHardAbort {
					turnStatus = TurnEndStatusAborted
					return al.abortTurn(ts)
				}
				if exec.abortedByHook {
					turnStatus = TurnEndStatusError
					return turnResult{}, fmt.Errorf("hook requested turn abort")
				}
				if finalContent == "" {
					finalContent = ts.opts.DefaultResponse
				}
				if ts.hasPendingAsyncDeliveries() && !ts.hardAbortRequested() {
					continue
				}
				finalizedEarly = true
				if err := ts.transitionPhase(TurnPhaseFinalizing); err != nil {
					turnStatus = TurnEndStatusError
					return turnResult{}, err
				}
			case ControlToolLoop:
				if err := ts.transitionPhase(TurnPhaseTools); err != nil {
					turnStatus = TurnEndStatusError
					return turnResult{}, err
				}
			}

		case TurnPhaseTools:
			toolCtrl := pipeline.ExecuteTools(ctx, turnCtx, ts, exec, ts.currentIteration())
			switch toolCtrl {
			case ToolControlContinue:
				messages = exec.messages
				if err := ts.transitionPhase(TurnPhaseRunning); err != nil {
					turnStatus = TurnEndStatusError
					return turnResult{}, err
				}
			case ToolControlBreak, ToolControlFinalize:
				if exec.executionErr != nil {
					turnStatus = TurnEndStatusError
					return turnResult{}, exec.executionErr
				}
				if exec.abortedByHardAbort {
					turnStatus = TurnEndStatusAborted
					return al.abortTurn(ts)
				}
				if exec.abortedByHook {
					turnStatus = TurnEndStatusError
					return turnResult{}, fmt.Errorf("hook requested turn abort")
				}
				if exec.allResponsesHandled {
					finalContent = ""
				}
				if ts.hasPendingAsyncDeliveries() && !ts.hardAbortRequested() {
					if err := ts.transitionPhase(TurnPhaseRunning); err != nil {
						turnStatus = TurnEndStatusError
						return turnResult{}, err
					}
					continue
				}
				finalizedEarly = true
				if err := ts.transitionPhase(TurnPhaseFinalizing); err != nil {
					turnStatus = TurnEndStatusError
					return turnResult{}, err
				}
			default:
				turnStatus = TurnEndStatusError
				return turnResult{}, fmt.Errorf("unknown tool control state %d", toolCtrl)
			}

		case TurnPhaseFinalizing:
			break turnLoop

		default:
			turnStatus = TurnEndStatusError
			return turnResult{}, fmt.Errorf("turn entered unexpected phase %s", ts.phaseSnapshot())
		}
	}

	if ts.hardAbortRequested() {
		turnStatus = TurnEndStatusAborted
		return al.abortTurn(ts)
	}

	maxIterationsReached := !finalizedEarly && ts.iterationBudgetReached()
	if maxIterationsReached {
		ts.setEndReason(TurnEndReasonMaxIterations)
	}
	if finalContent == "" && !(finalizedEarly && exec.allResponsesHandled) {
		if maxIterationsReached {
			finalContent = toolLimitResponse
		} else {
			finalContent = ts.opts.DefaultResponse
		}
	}

	if ts.hardAbortRequested() {
		turnStatus = TurnEndStatusAborted
		return al.abortTurn(ts)
	}

	if err := ts.transitionPhase(TurnPhaseFinalizing); err != nil {
		turnStatus = TurnEndStatusError
		return turnResult{}, err
	}
	result, err = pipeline.Finalize(ctx, turnCtx, ts, exec, turnStatus, finalContent)
	if err != nil {
		turnStatus = TurnEndStatusError
	}
	return result, err
}

func trimMessagesToRuneLimit(messages []providers.Message, limit int) []providers.Message {
	if limit <= 0 || len(messages) == 0 {
		return messages
	}
	total := messagesRuneCount(messages)
	if total <= limit {
		return messages
	}

	out := append([]providers.Message(nil), messages...)
	for total > limit && len(out) > 1 {
		start := 0
		if out[0].Role == "system" {
			start = 1
		}
		if start >= len(out)-1 {
			break
		}
		dropCount := 1
		switch {
		case out[start].Role == "assistant" && len(out[start].ToolCalls) > 0:
			callIDs := make(map[string]struct{}, len(out[start].ToolCalls))
			for _, call := range out[start].ToolCalls {
				if strings.TrimSpace(call.ID) != "" {
					callIDs[call.ID] = struct{}{}
				}
			}
			for start+dropCount < len(out) && out[start+dropCount].Role == "tool" {
				if _, ok := callIDs[out[start+dropCount].ToolCallID]; !ok {
					break
				}
				dropCount++
			}
		case out[start].Role == "tool":
			for start+dropCount < len(out) && out[start+dropCount].Role == "tool" {
				dropCount++
			}
		}
		removed := messagesRuneCount(out[start : start+dropCount])
		out = append(out[:start], out[start+dropCount:]...)
		total -= removed
	}
	if total <= limit || len(out) == 0 {
		return out
	}

	last := &out[len(out)-1]
	runes := []rune(last.Content)
	excess := total - limit
	if excess >= len(runes) {
		last.Content = ""
		return out
	}
	last.Content = string(runes[:len(runes)-excess])
	return out
}

func messagesRuneCount(messages []providers.Message) int {
	total := 0
	for _, msg := range messages {
		total += utf8.RuneCountInString(msg.Content)
		for _, call := range msg.ToolCalls {
			total += utf8.RuneCountInString(call.Name)
			total += utf8.RuneCountInString(fmt.Sprint(call.Arguments))
		}
	}
	return total
}

func (ts *turnState) saveTurnCheckpoint(status, tool string) error {
	store, ok := ts.agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		return nil
	}
	previous, err := store.GetTurnCheckpoint(ts.sessionKey)
	if err != nil {
		return err
	}
	checkpoint := session.TurnCheckpoint{
		TurnID: ts.turnID, Status: status, Iteration: ts.currentIteration(), Tool: tool,
		EndReason: string(ts.endReasonSnapshot()),
		StartedAt: ts.startedAt.UTC(), UpdatedAt: time.Now().UTC(),
		Activities: ts.activitiesSnapshot(),
		ToolItems:  ts.toolItemsSnapshot(),
		Usage:      ts.contextUsageSnapshot(),
	}
	checkpoint.Deliveries = append([]session.TurnDelivery(nil), previous.Deliveries...)
	if previous.Delivery != nil {
		checkpoint.Delivery = previous.Delivery
	}
	if previous.Approval != nil {
		checkpoint.Approval = previous.Approval
	}
	if previous.TurnID == ts.turnID && !(status == "running" && checkpoint.Iteration == 0) {
		checkpoint.Events = append(checkpoint.Events, previous.Events...)
		if status == "failed" || status == "interrupted" {
			checkpoint.Tool = previous.Tool
		}
	}
	var plan []session.TurnPlanStep
	for _, step := range ts.snapshot().Plan {
		plan = append(plan, session.TurnPlanStep{Text: step.Text, Status: step.Status})
	}
	checkpoint.Plan = plan
	checkpoint.Events = append(checkpoint.Events, session.TurnCheckpointEvent{Status: status, Iteration: checkpoint.Iteration, Tool: checkpoint.Tool, At: time.Now().UTC(), Plan: plan})
	if overflow := len(checkpoint.Events) - maxTurnCheckpointEvents; overflow > 0 {
		checkpoint.Events = append([]session.TurnCheckpointEvent(nil), checkpoint.Events[overflow:]...)
	}
	return store.SetTurnCheckpoint(ts.sessionKey, checkpoint)
}

func (al *AgentLoop) abortTurn(ts *turnState) (turnResult, error) {
	if err := ts.transitionPhase(TurnPhaseAborted); err != nil {
		return turnResult{}, err
	}
	if !ts.opts.NoHistory {
		// Close any unfinished tool call with an explicitly uncertain result.
		// A canceled tool may already have performed its side effect, so it must
		// not be silently discarded or represented as safe to replay.
		history, err := session.HistoryChecked(ts.agent.Sessions, ts.sessionKey)
		if err != nil {
			return turnResult{}, fmt.Errorf("read interrupted turn: %w", err)
		}
		persisted := ts.persistedMessagesSnapshot()
		_, activeHistory := splitHistoryForActiveTurn(history, persisted)
		for i := len(activeHistory) - 1; i >= 0; i-- {
			if activeHistory[i].Role != "assistant" || len(activeHistory[i].ToolCalls) == 0 {
				continue
			}
			completed := make(map[string]bool)
			for _, msg := range activeHistory[i+1:] {
				if msg.Role == "tool" {
					completed[msg.ToolCallID] = true
				}
			}
			for _, call := range activeHistory[i].ToolCalls {
				if !completed[call.ID] {
					msg := providers.Message{Role: "tool", ToolCallID: call.ID, Content: "Task interrupted before this tool returned. Its effects are unknown; inspect the current state before retrying."}
					if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, msg); err != nil {
						return turnResult{}, fmt.Errorf("write interrupted tool result: %w", err)
					}
					if err := ts.ingestMessage(context.Background(), al, msg); err != nil {
						return turnResult{}, err
					}
				}
			}
			break
		}
		if len(persisted) > 0 {
			msg := providers.Message{Role: "assistant", Content: "Task stopped. Completed work is retained; unfinished tool effects may be unknown. Ask me to continue when ready."}
			if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, msg); err != nil {
				return turnResult{}, fmt.Errorf("write interrupted turn: %w", err)
			}
			if err := ts.ingestMessage(context.Background(), al, msg); err != nil {
				return turnResult{}, err
			}
		}
		if err := ts.agent.Sessions.Save(ts.sessionKey); err != nil {
			al.emitEvent(
				runtimeevents.KindAgentError,
				ts.eventMeta("abortTurn", "turn.error"),
				ErrorPayload{
					Stage:   "session_save",
					Message: err.Error(),
				},
			)
			return turnResult{}, err
		}
	}
	return turnResult{status: TurnEndStatusAborted}, nil
}

// repairInterruptedToolCalls closes an incomplete trailing tool batch left by
// an interrupted process before the next user-initiated turn starts.
func (al *AgentLoop) repairInterruptedToolCalls(ctx context.Context, ts *turnState) error {
	history, err := session.HistoryChecked(ts.agent.Sessions, ts.sessionKey)
	if err != nil {
		return fmt.Errorf("read interrupted turn: %w", err)
	}
	i := len(history) - 1
	completed := make(map[string]bool)
	for i >= 0 && history[i].Role == "tool" {
		completed[history[i].ToolCallID] = true
		i--
	}
	if i < 0 || history[i].Role != "assistant" || len(history[i].ToolCalls) == 0 {
		return nil
	}
	repaired := false
	for _, call := range history[i].ToolCalls {
		if completed[call.ID] {
			continue
		}
		msg := providers.Message{Role: "tool", ToolCallID: call.ID, Content: "Execution was interrupted before this tool returned. Its effects are unknown; inspect the current state before retrying."}
		if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, msg); err != nil {
			return fmt.Errorf("write interrupted tool result: %w", err)
		}
		if err := ts.ingestMessage(ctx, al, msg); err != nil {
			return err
		}
		repaired = true
	}
	content := "The previous task was interrupted after the recorded tool results. Inspect the current state before continuing."
	if repaired {
		content = "The previous task was interrupted. Unfinished tool effects may be unknown; inspect the current state before continuing."
	}
	msg := providers.Message{Role: "assistant", Content: content}
	if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, msg); err != nil {
		return fmt.Errorf("write interrupted turn: %w", err)
	}
	if err := ts.ingestMessage(ctx, al, msg); err != nil {
		return err
	}
	if err := ts.agent.Sessions.Save(ts.sessionKey); err != nil {
		return fmt.Errorf("save interrupted task history: %w", err)
	}
	return nil
}

func (al *AgentLoop) selectCandidates(
	agent *AgentInstance,
	userMsg string,
	history []providers.Message,
) (candidates []providers.FallbackCandidate, model string, usedLight bool) {
	if agent.Router == nil || len(agent.LightCandidates) == 0 {
		return agent.Candidates, resolvedCandidateModel(agent.Candidates, agent.Model), false
	}

	_, usedLight, score := agent.Router.SelectModel(userMsg, history, agent.Model)
	if !usedLight {
		logger.DebugCF("agent", "Model routing: primary model selected",
			map[string]any{
				"agent_id":  agent.ID,
				"score":     score,
				"threshold": agent.Router.Threshold(),
			})
		return agent.Candidates, resolvedCandidateModel(agent.Candidates, agent.Model), false
	}

	logger.InfoCF("agent", "Model routing: light model selected",
		map[string]any{
			"agent_id":    agent.ID,
			"light_model": agent.Router.LightModel(),
			"score":       score,
			"threshold":   agent.Router.Threshold(),
		})
	return agent.LightCandidates, resolvedCandidateModel(agent.LightCandidates, agent.Router.LightModel()), true
}

func (al *AgentLoop) resolveContextManager() ContextManager {
	name := al.cfg.Agents.Defaults.ContextManager
	if name == "" || name == "legacy" {
		return &legacyContextManager{al: al}
	}
	factory, ok := lookupContextManager(name)
	if !ok {
		logger.WarnCF("agent", "Unknown context manager, falling back to legacy", map[string]any{
			"name": name,
		})
		return &legacyContextManager{al: al}
	}
	cm, err := factory(al.cfg.Agents.Defaults.ContextManagerConfig, al)
	if err != nil {
		logger.WarnCF("agent", "Failed to create context manager, falling back to legacy", map[string]any{
			"name":  name,
			"error": err.Error(),
		})
		return &legacyContextManager{al: al}
	}
	return cm
}

func (al *AgentLoop) askSideQuestion(
	ctx context.Context,
	agent *AgentInstance,
	opts *processOptions,
	question string,
) (string, error) {
	if agent == nil {
		return "", fmt.Errorf("askSideQuestion: no agent available for /btw")
	}
	modelMu := agent.modelStateMutex()
	modelMu.RLock()
	defer modelMu.RUnlock()

	question = strings.TrimSpace(question)
	if question == "" {
		return "", fmt.Errorf("askSideQuestion: %w", fmt.Errorf("Usage: /btw <question>"))
	}

	if opts != nil {
		normalizeProcessOptionsInPlace(opts)
		resolved, err := resolveTurnProfileOptions(al.GetConfig(), *opts)
		if err != nil {
			return "", err
		}
		*opts = resolved
	}

	var media []string
	var channel, chatID, senderID, senderDisplayName string
	if opts != nil {
		media = opts.Media
		channel = opts.Channel
		chatID = opts.ChatID
		senderID = opts.SenderID
		senderDisplayName = opts.SenderDisplayName
	}

	// Build messages with context but WITHOUT adding to session history
	var history []providers.Message
	var summary string
	if opts != nil && !opts.NoHistory {
		if resp, err := al.contextManager.Assemble(ctx, &AssembleRequest{
			SessionKey: opts.SessionKey,
			Budget:     agent.ContextWindow,
			MaxTokens:  agent.MaxTokens,
		}); err == nil && resp != nil {
			history = resp.History
			summary = resp.Summary
		}
	}

	var promptReq PromptBuildRequest
	if opts == nil {
		promptReq = PromptBuildRequest{
			History:           history,
			Summary:           summary,
			CurrentMessage:    question,
			Media:             append([]string(nil), media...),
			Channel:           channel,
			ChatID:            chatID,
			SenderID:          senderID,
			SenderDisplayName: senderDisplayName,
		}
	} else {
		promptReq = promptBuildRequestForProcessOptions(
			agent,
			*opts,
			history,
			summary,
			question,
			media,
		)
	}
	promptReq.SuppressToolUseRule = true
	promptReq.ToolUseFallback = false
	messages := agent.ContextBuilder.BuildMessagesFromPrompt(promptReq)

	maxMediaSize := al.GetConfig().Agents.Defaults.GetMaxMediaSize()
	currentTurnStart := len(messages)
	if strings.TrimSpace(question) != "" || len(media) > 0 {
		currentTurnStart = len(messages) - 1
	}
	messages = resolveMediaRefs(messages, al.mediaStore, maxMediaSize, currentTurnStart)

	activeCandidates, activeModel, usedLight := al.selectCandidates(agent, question, messages)
	selectedModelName := sideQuestionModelName(agent, usedLight)

	llmOpts := map[string]any{
		"max_tokens":       agent.MaxTokens,
		"temperature":      agent.Temperature,
		"prompt_cache_key": agent.ID + ":btw",
	}

	hookModelChanged := false
	sideSuppressReasoning := false
	callProvider := func(
		ctx context.Context,
		candidate providers.FallbackCandidate,
		model string,
		forceModel bool,
		callMessages []providers.Message,
	) (*providers.LLMResponse, error) {
		baseModelName := selectedModelName
		if forceModel && strings.TrimSpace(model) != "" {
			baseModelName = model
		}
		provider, providerModel, modelCfg, cleanup, err := al.isolatedSideQuestionProvider(
			agent,
			baseModelName,
			candidate,
		)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		if !forceModel || strings.TrimSpace(model) == "" {
			model = providerModel
		}
		callOpts := llmOpts
		settings := thinkingSettingsFromModelConfig(modelCfg)
		sideSuppressReasoning = shouldSuppressReasoningFor(settings)
		if _, exists := callOpts["thinking_level"]; !exists {
			if settings.configured {
				callOpts = shallowCloneLLMOptions(llmOpts)
				applyThinkingOption(callOpts, provider, settings, false, agent.ID)
			}
		}
		return provider.Chat(ctx, callMessages, nil, model, callOpts)
	}

	turnCtx := newTurnContext(nil, nil, nil)
	if opts != nil {
		turnCtx = newTurnContext(opts.Dispatch.InboundContext, opts.Dispatch.RouteResult, opts.Dispatch.SessionScope)
	}
	llmModel := activeModel
	if al.hooks != nil {
		llmReq, decision := al.hooks.BeforeLLM(ctx, &LLMHookRequest{
			Meta: HookMeta{
				Source:      "askSideQuestion",
				TracePath:   "turn.llm.request",
				turnContext: cloneTurnContext(turnCtx),
			},
			Context:          cloneTurnContext(turnCtx),
			Model:            llmModel,
			Messages:         messages,
			Tools:            nil,
			Options:          llmOpts,
			GracefulTerminal: false,
		})
		switch decision.normalizedAction() {
		case HookActionContinue, HookActionModify:
			if llmReq != nil {
				if strings.TrimSpace(llmReq.Model) != "" && llmReq.Model != llmModel {
					hookModelChanged = true
				}
				llmModel = llmReq.Model
				messages = llmReq.Messages
				llmOpts = llmReq.Options
				delete(llmOpts, "native_search")
			}
		case HookActionAbortTurn:
			reason := decision.Reason
			if reason == "" {
				reason = "hook requested turn abort"
			}
			return "", fmt.Errorf("hook aborted turn during before_llm: %s", reason)
		case HookActionHardAbort:
			reason := decision.Reason
			if reason == "" {
				reason = "hook requested turn abort"
			}
			return "", fmt.Errorf("hook aborted turn during before_llm: %s", reason)
		}
	}
	if hookModelChanged {
		// Hook-selected models must not continue through the pre-hook fallback
		// candidate list, otherwise fallback execution would call the original
		// candidate model and silently ignore the hook decision.
		activeCandidates = nil
	}

	callSideLLM := func(callMessages []providers.Message) (*providers.LLMResponse, error) {
		if len(activeCandidates) > 1 && al.fallback != nil {
			fbResult, err := al.fallback.ExecuteCandidate(
				ctx,
				activeCandidates,
				func(ctx context.Context, candidate providers.FallbackCandidate) (*providers.LLMResponse, error) {
					return callProvider(ctx, candidate, candidate.Model, false, callMessages)
				},
			)
			if err != nil {
				return nil, err
			}
			return fbResult.Response, nil
		}

		var candidate providers.FallbackCandidate
		if len(activeCandidates) > 0 {
			candidate = activeCandidates[0]
		}
		return callProvider(ctx, candidate, llmModel, hookModelChanged, callMessages)
	}

	// Retry without media if vision is unsupported
	// Note: Vision retry is only applied to the initial call. If fallback chain
	// is used, vision errors from fallback providers will not trigger retry.
	var resp *providers.LLMResponse
	var err error
	resp, err = callSideLLM(messages)
	if err != nil && hasMediaRefs(messages) && isVisionUnsupportedError(err) {
		al.emitEvent(
			runtimeevents.KindAgentLLMRetry,
			HookMeta{
				Source:      "askSideQuestion",
				TracePath:   "turn.llm.retry",
				turnContext: cloneTurnContext(turnCtx),
			},
			LLMRetryPayload{
				Attempt:    1,
				MaxRetries: 1,
				Reason:     "vision_unsupported",
				Error:      err.Error(),
				Backoff:    0,
			},
		)
		messagesWithoutMedia := stripMessageMedia(messages)
		resp, err = callSideLLM(messagesWithoutMedia)
	}
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", nil
	}

	// Apply after_llm hooks
	if al.hooks != nil {
		llmResp, decision := al.hooks.AfterLLM(ctx, &LLMHookResponse{
			Meta: HookMeta{
				Source:      "askSideQuestion",
				TracePath:   "turn.llm.response",
				turnContext: cloneTurnContext(turnCtx),
			},
			Context:  cloneTurnContext(turnCtx),
			Model:    llmModel,
			Response: resp,
		})
		switch decision.normalizedAction() {
		case HookActionContinue, HookActionModify:
			if llmResp != nil && llmResp.Response != nil {
				resp = llmResp.Response
			}
		case HookActionAbortTurn, HookActionHardAbort:
			reason := decision.Reason
			if reason == "" {
				reason = "hook requested turn abort"
			}
			return "", fmt.Errorf("hook aborted turn during after_llm: %s", reason)
		}
	}
	if sideSuppressReasoning {
		resp.Reasoning = ""
		resp.ReasoningContent = ""
		resp.ReasoningDetails = nil
	}

	return sideQuestionResponseContent(resp), nil
}

func (al *AgentLoop) isolatedSideQuestionProvider(
	agent *AgentInstance,
	baseModelName string,
	candidate providers.FallbackCandidate,
) (providers.LLMProvider, string, *config.ModelConfig, func(), error) {
	if agent == nil {
		return nil, "", nil, func() {}, fmt.Errorf("isolatedSideQuestionProvider: no agent available for /btw")
	}

	modelCfg, err := al.sideQuestionModelConfig(agent, baseModelName, candidate)
	if err != nil {
		return nil, "", nil, func() {}, fmt.Errorf("isolatedSideQuestionProvider: %w", err)
	}

	factory := al.providerFactory
	if factory == nil {
		factory = providers.CreateProviderFromConfig
	}
	provider, modelID, err := factory(modelCfg)
	if err != nil {
		return nil, "", nil, func() {}, fmt.Errorf("isolatedSideQuestionProvider: %w", err)
	}

	cleanup := func() {
		closeProviderIfStateful(provider)
	}
	return provider, modelID, modelCfg, cleanup, nil
}

func (al *AgentLoop) sideQuestionModelConfig(
	agent *AgentInstance,
	baseModelName string,
	candidate providers.FallbackCandidate,
) (*config.ModelConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("sideQuestionModelConfig: no agent available for /btw")
	}
	if candidate.ConfigIndex > 0 {
		if modelCfg, err := resolvedCandidateModelConfig(
			al.GetConfig(),
			candidate,
			agent.Workspace,
		); err == nil {
			return modelCfg, nil
		}
	}

	if name := modelAliasFromCandidateIdentityKey(candidate.IdentityKey); name != "" {
		modelCfg, err := resolvedRuntimeModelConfig(al.GetConfig(), name, agent.Workspace)
		if err == nil {
			return modelCfg, nil
		}
		// Fallback: create a minimal config if lookup fails
	}

	// Older identity keys used provider/model; keep resolving those by model.
	if name := modelNameFromIdentityKey(candidate.IdentityKey); name != "" {
		modelCfg, err := resolvedRuntimeModelConfig(al.GetConfig(), name, agent.Workspace)
		if err == nil {
			return modelCfg, nil
		}
		// Fallback: create a minimal config if lookup fails
	}

	if candidate.Provider != "" && candidate.Model != "" {
		candidateRef := providers.NormalizeProvider(candidate.Provider) + "/" + candidate.Model
		if modelCfg, err := resolvedRuntimeModelConfig(al.GetConfig(), candidateRef, agent.Workspace); err == nil {
			return modelCfg, nil
		}
		return &config.ModelConfig{
			ModelName: candidateRef,
			Model:     candidateRef,
			Workspace: agent.Workspace,
		}, nil
	}

	// Otherwise, clean up the base model name and use it
	baseModelName = strings.TrimSpace(baseModelName)
	modelCfg, err := resolvedRuntimeModelConfig(al.GetConfig(), baseModelName, agent.Workspace)
	if err != nil {
		// Fallback: create a minimal config for test scenarios
		model := strings.TrimSpace(baseModelName)
		if candidate.Model != "" {
			model = candidate.Model
		}
		if candidate.Provider != "" && candidate.Model != "" {
			model = providers.NormalizeProvider(candidate.Provider) + "/" + candidate.Model
		} else {
			model = ensureProtocolModel(model)
		}
		return &config.ModelConfig{
			ModelName: baseModelName,
			Model:     model,
			Workspace: agent.Workspace,
		}, nil
	}

	// If candidate specifies a different provider/model, override
	clone := *modelCfg
	return &clone, nil
}
