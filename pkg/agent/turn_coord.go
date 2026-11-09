// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

func (al *AgentLoop) runTurn(ctx context.Context, ts *turnState, pipeline *Pipeline) (result turnResult, retErr error) {
	if ts.al == nil {
		ts.al = al
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
	defer al.clearActiveTurn(ts)

	if al.takePendingStop(ts.sessionKey) {
		_ = ts.requestHardAbort()
	}

	turnStatus := TurnEndStatusCompleted
	if !ts.opts.NoHistory {
		if err := ts.saveTurnCheckpoint("running", ""); err != nil {
			return turnResult{}, fmt.Errorf("save turn checkpoint: %w", err)
		}
		defer func() {
			status := "completed"
			if turnStatus == TurnEndStatusAborted || result.finalContent == toolLimitResponse {
				status = "interrupted"
			} else if turnStatus == TurnEndStatusError || retErr != nil || result.status == "" {
				status = "failed"
			}
			if err := ts.saveTurnCheckpoint(status, ""); err != nil && retErr == nil {
				retErr = fmt.Errorf("save turn checkpoint: %w", err)
			}
		}()
	}
	defer func() {
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
			},
		)
	}()

	if ts.hardAbortRequested() {
		turnStatus = TurnEndStatusAborted
		return al.abortTurn(ts)
	}

	al.emitEvent(
		runtimeevents.KindAgentTurnStart,
		ts.eventMeta("runTurn", "turn.start"),
		TurnStartPayload{
			UserMessage: ts.userMessage,
			MediaCount:  len(ts.media),
		},
	)

	// SetupTurn extracts the one-time initialization phase.
	exec, err := pipeline.SetupTurn(turnCtx, ts)
	if err != nil {
		turnStatus = TurnEndStatusError
		return turnResult{}, err
	}
	defer exec.closeOwnedProviders()

	// Convenience references to exec fields used throughout the turn loop.
	messages := exec.messages
	pendingMessages := exec.pendingMessages
	maxMediaSize := pipeline.Cfg.Agents.Defaults.GetMaxMediaSize()
	finalContent := exec.finalContent

	for ts.currentIteration() < ts.agent.MaxIterations || len(exec.pendingMessages) > 0 || func() bool {
		graceful, _ := ts.gracefulInterruptRequested()
		return graceful
	}() {
		if ts.hardAbortRequested() {
			turnStatus = TurnEndStatusAborted
			return al.abortTurn(ts)
		}

		iteration := ts.currentIteration() + 1
		ts.setIteration(iteration)
		ts.setPhase(TurnPhaseRunning)
		if !ts.opts.NoHistory {
			if err := ts.saveTurnCheckpoint("running", ""); err != nil {
				turnStatus = TurnEndStatusError
				return turnResult{}, fmt.Errorf("save turn checkpoint: %w", err)
			}
		}

		if iteration > 1 {
			// For subsequent iterations, read from exec.pendingMessages which
			// is where ExecuteTools (or initial poll) deposits steering.
			// We do NOT call dequeueSteeringMessagesForScope here because
			// steering was already consumed from al.steering by ExecuteTools.
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
				break
			}
			logger.InfoCF("agent", "Parent turn ended, critical SubTurn continues running", map[string]any{
				"agent_id":  ts.agentID,
				"iteration": iteration,
				"turn_id":   ts.turnID,
			})
		}

		// Poll for pending SubTurn results (from HEAD)
		if ts.pendingResults != nil {
			select {
			case result, ok := <-ts.pendingResults:
				if ok && result != nil && result.ForLLM != "" {
					content := al.cfg.FilterSensitiveData(result.ForLLM)
					msg := subTurnResultPromptMessage(content)
					pendingMessages = append(pendingMessages, msg)
				}
			default:
				// No results available
			}
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
			// Clear exec.pendingMessages after injection so InitialSteeringMessages
			// are not re-injected on subsequent iterations (Issue 2 fix).
			exec.pendingMessages = nil
		}
		// Always sync messages into exec.messages so CallLLM sees the updated state
		exec.messages = messages

		logger.DebugCF("agent", "LLM iteration",
			map[string]any{
				"agent_id":  ts.agent.ID,
				"iteration": iteration,
				"max":       ts.agent.MaxIterations,
			})

		// Execute LLM call via Pipeline
		ts.setPhase(TurnPhaseRunning)
		ctrl, callErr := pipeline.CallLLM(ctx, turnCtx, ts, exec, iteration)
		if callErr != nil {
			turnStatus = TurnEndStatusError
			return turnResult{}, callErr
		}
		messages = exec.messages
		pendingMessages = exec.pendingMessages
		finalContent = exec.finalContent

		switch ctrl {
		case ControlContinue:
			continue
		case ControlBreak:
			// Hard abort: delegate to abortTurn (sets TurnEndStatusAborted)
			if exec.abortedByHardAbort {
				turnStatus = TurnEndStatusAborted
				return al.abortTurn(ts)
			}
			// Hook abort (HookActionAbortTurn): sets TurnEndStatusError, returns error
			if exec.abortedByHook {
				turnStatus = TurnEndStatusError
				return turnResult{}, fmt.Errorf("hook requested turn abort")
			}
			// Ensure empty response falls back to DefaultResponse
			if finalContent == "" {
				finalContent = ts.opts.DefaultResponse
			}
			result, finalizeErr := pipeline.Finalize(ctx, turnCtx, ts, exec, turnStatus, finalContent)
			if finalizeErr != nil {
				turnStatus = TurnEndStatusError
			}
			return result, finalizeErr
		case ControlToolLoop:
			// Execute tools via Pipeline
			toolCtrl := pipeline.ExecuteTools(ctx, turnCtx, ts, exec, iteration)
			switch toolCtrl {
			case ToolControlContinue:
				// Re-read exec.messages since ExecuteTools may have updated it
				// (added tool results/skipped messages) before returning ControlContinue
				messages = exec.messages
				continue
			case ToolControlBreak:
				if exec.executionErr != nil {
					turnStatus = TurnEndStatusError
					return turnResult{}, exec.executionErr
				}
				// Hard abort: delegate to abortTurn (sets TurnEndStatusAborted)
				if exec.abortedByHardAbort {
					turnStatus = TurnEndStatusAborted
					return al.abortTurn(ts)
				}
				// Hook abort (HookActionAbortTurn): sets TurnEndStatusError, returns error
				if exec.abortedByHook {
					turnStatus = TurnEndStatusError
					return turnResult{}, fmt.Errorf("hook requested turn abort")
				}
				// ExecuteTools returned ControlBreak:
				// - allResponsesHandled=true: finalize without DefaultResponse (exec.finalContent empty)
				// - allResponsesHandled=false: coordinator applies DefaultResponse before finalize
				if exec.allResponsesHandled {
					finalContent = ""
				}
				result, finalizeErr := pipeline.Finalize(ctx, turnCtx, ts, exec, turnStatus, finalContent)
				if finalizeErr != nil {
					turnStatus = TurnEndStatusError
				}
				return result, finalizeErr
			}
		}
	}

	if ts.hardAbortRequested() {
		turnStatus = TurnEndStatusAborted
		return al.abortTurn(ts)
	}

	if finalContent == "" {
		if ts.currentIteration() >= ts.agent.MaxIterations && ts.agent.MaxIterations > 0 {
			finalContent = toolLimitResponse
		} else {
			finalContent = ts.opts.DefaultResponse
		}
	}

	// Check hard abort before finalizing (may have been set during tool execution)
	if ts.hardAbortRequested() {
		turnStatus = TurnEndStatusAborted
		return al.abortTurn(ts)
	}

	result, err = pipeline.Finalize(ctx, turnCtx, ts, exec, turnStatus, finalContent)
	if err != nil {
		turnStatus = TurnEndStatusError
	}
	return result, err
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
		StartedAt: ts.startedAt.UTC(), UpdatedAt: time.Now().UTC(),
		Activities: ts.activitiesSnapshot(),
		Usage:      ts.contextUsageSnapshot(),
	}
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
	return store.SetTurnCheckpoint(ts.sessionKey, checkpoint)
}

func (al *AgentLoop) abortTurn(ts *turnState) (turnResult, error) {
	ts.setPhase(TurnPhaseAborted)
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
