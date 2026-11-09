package agent

import (
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

func (ts *turnState) recordUsage(usage *providers.UsageInfo, summary bool) {
	if usage == nil {
		return
	}
	ts.mu.Lock()
	ts.usage.PromptTokens += usage.PromptTokens
	ts.usage.CompletionTokens += usage.CompletionTokens
	ts.usage.TotalTokens += usage.TotalTokens
	if summary {
		ts.summaryUsage.PromptTokens += usage.PromptTokens
		ts.summaryUsage.CompletionTokens += usage.CompletionTokens
	} else {
		copy := *usage
		ts.lastUsage = &copy
		if ts.requestUsage != nil && usage.PromptTokens > 0 {
			ts.requestUsage.UsedTokens = usage.PromptTokens
			ts.requestUsage.UsedPercent = min(100, usage.PromptTokens*100/max(1, ts.requestUsage.CompressAtTokens))
		}
	}
	ts.mu.Unlock()
	// A spawned turn contributes to its parent's task cost, but must not
	// replace the parent's current-request occupancy or last response usage.
	for parent := ts.parentTurnState; parent != nil; parent = parent.parentTurnState {
		parent.mu.Lock()
		parent.usage.PromptTokens += usage.PromptTokens
		parent.usage.CompletionTokens += usage.CompletionTokens
		parent.usage.TotalTokens += usage.TotalTokens
		if summary {
			parent.summaryUsage.PromptTokens += usage.PromptTokens
			parent.summaryUsage.CompletionTokens += usage.CompletionTokens
		}
		parent.mu.Unlock()
		if parent.agent != nil {
			if store, ok := parent.agent.Sessions.(session.TurnUsageStore); ok {
				if err := store.UpdateTurnUsage(parent.sessionKey, parent.turnID, parent.contextUsageSnapshot()); err != nil {
					logger.WarnCF("agent", "Failed to persist child turn usage", map[string]any{"error": err.Error(), "turn_id": parent.turnID})
				}
			}
		}
	}
}

func (ts *turnState) totalTokenUsage() *providers.UsageInfo {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	usage := ts.usage
	return &usage
}

func (ts *turnState) recordRequestContext(messages []providers.Message, defs []providers.ToolDefinition) {
	used := EstimateToolDefsTokens(defs)
	history := 0
	for _, message := range messages {
		tokens := EstimateMessageTokens(message)
		used += tokens
		if message.Role != "system" {
			history += tokens
		}
	}
	window := ts.agent.ContextWindow
	limit := max(1, window-ts.agent.MaxTokens)
	ts.mu.Lock()
	ts.requestUsage = &bus.ContextUsage{UsedTokens: used, TotalTokens: window, HistoryTokens: history, CompressAtTokens: limit, SummarizeAtTokens: window * ts.agent.SummarizeTokenPercent / 100, UsedPercent: min(100, used*100/limit)}
	ts.mu.Unlock()
}

func (ts *turnState) contextUsageSnapshot() *bus.ContextUsage {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	if ts.requestUsage == nil {
		return nil
	}
	usage := *ts.requestUsage
	usage.TaskInputTokens = ts.usage.PromptTokens
	usage.TaskOutputTokens = ts.usage.CompletionTokens
	usage.SummaryInputTokens = ts.summaryUsage.PromptTokens
	usage.SummaryOutputTokens = ts.summaryUsage.CompletionTokens
	return &usage
}

func (al *AgentLoop) ConversationUsage(channel, chatID, senderID string) (*bus.ContextUsage, error) {
	msg := bus.InboundMessage{Channel: channel, ChatID: chatID, SenderID: senderID, Context: bus.InboundContext{Channel: channel, ChatID: chatID, SenderID: senderID, ChatType: "direct"}}
	key, agentID, ok := al.resolveSteeringTarget(msg)
	if !ok {
		return nil, nil
	}
	if turn := al.getActiveTurnState(key); turn != nil {
		return turn.contextUsageSnapshot(), nil
	}
	agent, ok := al.registry.GetAgent(agentID)
	if !ok || agent.Sessions == nil {
		return nil, nil
	}
	if store, ok := agent.Sessions.(session.TurnCheckpointStore); ok {
		checkpoint, err := store.GetTurnCheckpoint(key)
		return checkpoint.Usage, err
	}
	return nil, nil
}

// computeContextUsage estimates current context window consumption for the
// given agent and session. Includes history, system prompt (with dynamic context,
// summary, and skills — mirroring BuildMessages composition), and tool definitions.
// The output reserve (MaxTokens) is not counted as "used" but reduces the
// effective budget, matching isOverContextBudget's compression trigger:
//
//	compress when: history + system + tools + maxTokens > contextWindow
//	equivalent to: history + system + tools > contextWindow - maxTokens
//
// Returns nil when the agent or session is unavailable.
func computeContextUsage(agent *AgentInstance, sessionKey string) *bus.ContextUsage {
	if agent == nil || agent.Sessions == nil {
		return nil
	}
	contextWindow := agent.ContextWindow
	if contextWindow <= 0 {
		return nil
	}

	// History tokens
	history := agent.Sessions.GetHistory(sessionKey)
	historyTokens := 0
	for _, m := range history {
		historyTokens += EstimateMessageTokens(m)
	}

	// System message tokens: uses EstimateSystemTokens which mirrors
	// the full system message composition in BuildMessages (static prompt,
	// dynamic context, active skills, summary with wrapping prefix).
	systemTokens := 0
	if agent.ContextBuilder != nil {
		summary := agent.Sessions.GetSummary(sessionKey)
		// Pass nil for active skills: skills are only injected when the user
		// explicitly activates them via /use, which is rare. Using nil matches
		// the common case and avoids over-counting all installed skills.
		systemTokens = agent.ContextBuilder.EstimateSystemTokens(summary, nil)
	}

	// Tool definition tokens
	toolTokens := 0
	if agent.Tools != nil {
		toolTokens = EstimateToolDefsTokens(agent.Tools.ToProviderDefs())
	}

	// Used = history + system (includes summary) + tools
	usedTokens := historyTokens + systemTokens + toolTokens

	// Effective budget = contextWindow minus output reserve (maxTokens)
	effectiveWindow := contextWindow - agent.MaxTokens
	if effectiveWindow < 0 {
		effectiveWindow = contextWindow
	}

	// compressAt = effectiveWindow: aligns with isOverContextBudget's
	// proactive trigger (msgTokens + toolTokens + maxTokens > contextWindow).
	compressAt := effectiveWindow

	// summarizeAt = soft summarization trigger: matches maybeSummarize's
	// threshold (contextWindow * SummarizeTokenPercent / 100).
	//
	// The engine compares this against history-message tokens ONLY (not
	// UsedTokens).  HistoryTokens is exposed alongside UsedTokens so the
	// UI can show both values and avoid user confusion.
	summarizeAt := contextWindow * agent.SummarizeTokenPercent / 100
	if summarizeAt <= 0 {
		summarizeAt = compressAt
	}

	usedPercent := 0
	if compressAt > 0 {
		usedPercent = usedTokens * 100 / compressAt
	}
	if usedPercent > 100 {
		usedPercent = 100
	}

	return &bus.ContextUsage{
		UsedTokens:        usedTokens,
		TotalTokens:       contextWindow,
		HistoryTokens:     historyTokens,
		CompressAtTokens:  compressAt,
		SummarizeAtTokens: summarizeAt,
		UsedPercent:       usedPercent,
	}
}
