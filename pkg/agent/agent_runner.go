package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/constants"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

// ReloadProviderAndConfig atomically swaps the provider and config with proper synchronization.
// It uses a context to allow timeout control from the caller.
// Returns an error if the reload fails or context is canceled.
func (al *AgentLoop) ReloadProviderAndConfig(
	ctx context.Context,
	provider providers.LLMProvider,
	cfg *config.Config,
) error {
	// Validate inputs
	if provider == nil {
		return fmt.Errorf("provider cannot be nil")
	}
	if cfg == nil {
		return fmt.Errorf("config cannot be nil")
	}

	var registry *AgentRegistry
	var registryErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.RecoverPanicNoExit(r)
				registryErr = fmt.Errorf("registry creation panic: %v", r)
			}
		}()
		registry, registryErr = newAgentRegistry(cfg, provider)
	}()
	if registryErr != nil {
		return fmt.Errorf("registry creation failed: %w", registryErr)
	}

	// Check context again before proceeding
	if err := ctx.Err(); err != nil {
		registry.closeExcept(append(al.GetRegistry().providers(), provider))
		return fmt.Errorf("context canceled after registry creation: %w", err)
	}

	// Ensure shared tools are re-registered on the new registry.
	current := al.runtimeSnapshot()
	registerSharedTools(al, cfg, current.bus, registry, provider)

	newEvolution, evolutionErr := newEvolutionBridge(registry, cfg, provider)
	if evolutionErr != nil {
		logger.WarnCF("agent", "Failed to reinitialize evolution bridge during reload",
			map[string]any{"error": evolutionErr.Error()})
	}
	if newEvolution != nil {
		newEvolution.setCurrentCheck(al.isCurrentEvolutionBridge)
		if err := newEvolution.subscribeRuntimeEvents(al.runtimeEvents.Channel()); err != nil {
			logger.WarnCF("agent", "Failed to subscribe reloaded evolution bridge to runtime events",
				map[string]any{"error": err.Error()})
		}
	}

	newRL := providers.NewRateLimiterRegistry()
	for _, agentID := range registry.ListAgentIDs() {
		if agent, ok := registry.GetAgent(agentID); ok {
			newRL.RegisterCandidates(agent.Candidates)
			newRL.RegisterCandidates(agent.LightCandidates)
		}
	}
	newFallback := providers.NewFallbackChain(providers.NewCooldownTracker(), newRL)

	// Swap every reload-owned dependency under one lock so a Pipeline captures
	// either the complete old version or the complete new version.
	al.mu.Lock()
	oldRegistry := al.registry
	oldEvolution := al.evolution
	al.cfg = cfg
	al.registry = registry
	al.evolution = newEvolution
	al.fallback = newFallback
	al.mu.Unlock()
	al.propagateMediaStore(registry, current.mediaStore)
	al.refreshRuntimeEventLogger(cfg)

	oldMCPManager := al.mcp.reset()
	al.hookRuntime.reset(al)
	configureHookManagerFromConfig(al.hooks, cfg)
	if err := al.ensureHooksInitialized(ctx); err != nil {
		logger.WarnCF("agent", "Configured hooks failed to reinitialize after reload",
			map[string]any{"error": err.Error()})
	}
	if oldMCPManager != nil {
		if err := oldMCPManager.Close(); err != nil {
			logger.WarnCF("agent", "Failed to close previous MCP manager during reload",
				map[string]any{"error": err.Error()})
		}
	}
	if oldEvolution != nil {
		if err := oldEvolution.Close(); err != nil {
			logger.WarnCF("agent", "Failed to close previous evolution bridge during reload",
				map[string]any{"error": err.Error()})
		}
	}
	if err := al.ensureMCPInitialized(ctx); err != nil {
		logger.WarnCF("agent", "MCP failed to reinitialize after reload",
			map[string]any{"error": err.Error()})
	}

	al.retireRegistry(ctx, oldRegistry)

	logger.InfoCF("agent", "Provider and config reloaded successfully",
		map[string]any{
			"model": cfg.Agents.Defaults.GetModelName(),
		})

	return nil
}

// runAgentLoop keeps the string-only API for callers that return the response
// directly. Inbound channel turns use runAgentLoopResult so the outer delivery
// layer knows whether the turn already published its final response.
func (al *AgentLoop) runAgentLoop(
	ctx context.Context,
	agent *AgentInstance,
	opts processOptions,
) (string, error) {
	result, err := al.runAgentLoopResult(ctx, agent, opts)
	return result.content, err
}

// runAgentLoopResult is the top-level shell that starts a turn and publishes
// any post-turn work. runTurn owns the full turn lifecycle.
func (al *AgentLoop) runAgentLoopResult(
	ctx context.Context,
	agent *AgentInstance,
	opts processOptions,
) (agentLoopResult, error) {
	opts = normalizeProcessOptions(opts)
	runtime, release := al.retainRuntime()
	defer release()
	if opts.runtime != nil {
		runtime = *opts.runtime
	}
	var err error
	opts, err = resolveTurnProfileOptions(runtime.cfg, opts)
	if err != nil {
		return agentLoopResult{}, err
	}

	// Record last channel for heartbeat notifications (skip internal channels and cli)
	if opts.Dispatch.Channel() != "" &&
		opts.Dispatch.ChatID() != "" &&
		!constants.IsInternalChannel(opts.Dispatch.Channel()) {
		channelKey := fmt.Sprintf("%s:%s", opts.Dispatch.Channel(), opts.Dispatch.ChatID())
		if recordErr := al.RecordLastChannel(channelKey); recordErr != nil {
			logger.WarnCF(
				"agent",
				"Failed to record last channel",
				map[string]any{"error": recordErr.Error()},
			)
		}
	}

	ensureSessionMetadata(
		agent.Sessions,
		opts.Dispatch.SessionKey,
		opts.Dispatch.SessionScope,
		opts.Dispatch.SessionAliases,
	)

	turnScope := al.newTurnEventScope(
		agent.ID,
		opts.Dispatch.SessionKey,
		newTurnContext(opts.Dispatch.InboundContext, opts.Dispatch.RouteResult, opts.Dispatch.SessionScope),
	)
	ts := newTurnState(agent, opts, turnScope)
	pipeline := newPipelineWithRuntime(al, runtime)
	result, err := al.runTurn(ctx, ts, pipeline)
	if err != nil {
		return agentLoopResult{}, err
	}
	if result.status == TurnEndStatusAborted {
		return agentLoopResult{}, nil
	}

	for _, followUp := range result.followUps {
		if pubErr := al.bus.PublishInbound(ctx, followUp); pubErr != nil {
			logger.WarnCF("agent", "Failed to publish follow-up after turn",
				map[string]any{
					"turn_id": ts.turnID,
					"error":   pubErr.Error(),
				})
		}
	}

	if opts.SendResponse && result.finalContent != "" {
		agentID, sessionKey, scope := outboundTurnMetadata(
			agent.ID,
			opts.Dispatch.SessionKey,
			opts.Dispatch.SessionScope,
		)
		msg := bus.OutboundMessage{
			ID: result.responseID,
			Context: outboundContextFromInbound(
				opts.Dispatch.InboundContext,
				opts.Dispatch.Channel(),
				opts.Dispatch.ChatID(),
				opts.Dispatch.ReplyToMessageID(),
			),
			AgentID:      agentID,
			SessionKey:   sessionKey,
			Scope:        scope,
			Content:      result.finalContent,
			ContextUsage: computeContextUsage(agent, opts.Dispatch.SessionKey),
		}
		if modelName := strings.TrimSpace(result.modelName); modelName != "" {
			if msg.Context.Raw == nil {
				msg.Context.Raw = make(map[string]string, 1)
			}
			msg.Context.Raw["model_name"] = modelName
		}
		markFinalOutbound(&msg)
		if pubErr := al.publishTrackedOutbound(ctx, opts.Dispatch.SessionKey, msg); pubErr != nil {
			return agentLoopResult{}, pubErr
		}
		result.responsePublished = true
	}

	if result.finalContent != "" {
		logger.InfoCF("agent", "Response completed",
			map[string]any{
				"turn_id":      ts.turnID,
				"agent_id":     agent.ID,
				"session_key":  opts.Dispatch.SessionKey,
				"iterations":   ts.currentIteration(),
				"final_length": len(result.finalContent),
			})
		if logger.ContentDiagnosticsEnabled() {
			logger.DebugCF("agent", "Response preview", map[string]any{
				"turn_id": ts.turnID, "preview": utils.Truncate(result.finalContent, 120),
			})
		}
	}

	return agentLoopResult{
		content:           result.finalContent,
		responsePublished: result.responsePublished,
	}, nil
}

// runAgentLoopClaimed runs a top-level turn while claiming the session slot so
// direct API calls cannot race with an inbound turn for the same conversation.
func (al *AgentLoop) runAgentLoopClaimed(
	ctx context.Context,
	agent *AgentInstance,
	opts processOptions,
) (agentLoopResult, error) {
	opts = normalizeProcessOptions(opts)
	sessionKey := strings.TrimSpace(opts.Dispatch.SessionKey)
	if sessionKey == "" {
		return al.runAgentLoopResult(ctx, agent, opts)
	}

	placeholder, err := al.claimSessionSlot(sessionKey)
	if err != nil {
		return agentLoopResult{}, err
	}
	defer al.releaseSessionTurnState(sessionKey, placeholder)

	return al.runAgentLoopResult(ctx, agent, opts)
}

func (al *AgentLoop) claimSessionSlot(sessionKey string) (*turnState, error) {
	placeholder := &turnState{
		turnID:     makePendingTurnID(sessionKey, al.turnSeq.Add(1)),
		lifecycle:  newTurnLifecycleAt(TurnPhaseSetup),
		sessionKey: sessionKey,
	}
	if actual, loaded := al.activeTurnStates.LoadOrStore(sessionKey, placeholder); loaded {
		if active, ok := actual.(*turnState); ok && active != nil {
			return nil, fmt.Errorf("turn %s is still active for session %q", active.turnID, sessionKey)
		}
		return nil, fmt.Errorf("session %q is still active", sessionKey)
	}
	return placeholder, nil
}
