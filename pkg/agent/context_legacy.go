package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

// legacyContextManager wraps the existing summarization/compression logic
// as a ContextManager implementation. It is the default when no other
// ContextManager is configured.
type legacyContextManager struct {
	al          *AgentLoop
	summarizing sync.Map // dedup for async Compact (post-turn)
}

func (m *legacyContextManager) Assemble(_ context.Context, req *AssembleRequest) (*AssembleResponse, error) {
	// Legacy: read history from session, return as-is.
	// Budget enforcement happens in BuildMessages caller via
	// isOverContextBudget + forceCompression.
	agent := m.al.agentForSession(req.SessionKey)
	if agent == nil {
		return nil, fmt.Errorf("session agent not found for %q", req.SessionKey)
	}
	history, err := session.HistoryChecked(agent.Sessions, req.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("read session history: %w", err)
	}
	summary, err := session.SummaryChecked(agent.Sessions, req.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("read session summary: %w", err)
	}
	return &AssembleResponse{
		History: history,
		Summary: summary,
	}, nil
}

func (m *legacyContextManager) Compact(_ context.Context, req *CompactRequest) error {
	switch req.Reason {
	case ContextCompressReasonProactive, ContextCompressReasonRetry:
		// Sync emergency compression — budget exceeded.
		result, ok, err := m.forceCompression(req.SessionKey)
		if err != nil {
			return err
		}
		if ok {
			m.al.emitEvent(
				runtimeevents.KindAgentContextCompress,
				m.al.newTurnEventScope("", req.SessionKey, nil).meta(0, "forceCompression", "turn.context.compress"),
				ContextCompressPayload{
					Reason:            req.Reason,
					DroppedMessages:   result.DroppedMessages,
					RemainingMessages: result.RemainingMessages,
				},
			)
		}
	case ContextCompressReasonSummarize:
		m.maybeSummarize(req.SessionKey)
	}
	return nil
}

func (m *legacyContextManager) Ingest(_ context.Context, _ *IngestRequest) error {
	// Legacy: no-op. Messages are persisted by the session store.
	return nil
}

func (m *legacyContextManager) Clear(_ context.Context, sessionKey string) error {
	// Routed (non-default) agents keep history in their own session store,
	// so resolve the owning agent instead of assuming the default one.
	agent := m.al.agentForSession(sessionKey)
	if agent == nil || agent.Sessions == nil {
		return fmt.Errorf("sessions not initialized")
	}
	if err := session.SetHistoryChecked(agent.Sessions, sessionKey, []providers.Message{}); err != nil {
		return err
	}
	if err := session.SetSummaryChecked(agent.Sessions, sessionKey, ""); err != nil {
		return err
	}
	return agent.Sessions.Save(sessionKey)
}

// maybeSummarize triggers summarization if the session history exceeds thresholds.
// It runs asynchronously in a goroutine.
func (m *legacyContextManager) maybeSummarize(sessionKey string) {
	agent := m.al.agentForSession(sessionKey)
	if agent == nil {
		return
	}

	newHistory := agent.Sessions.GetHistory(sessionKey)
	tokenEstimate := m.estimateTokens(newHistory)
	threshold := agent.ContextWindow * agent.SummarizeTokenPercent / 100

	if len(newHistory) > agent.SummarizeMessageThreshold || tokenEstimate > threshold {
		summarizeKey := agent.ID + ":" + sessionKey
		if _, loading := m.summarizing.LoadOrStore(summarizeKey, true); !loading {
			turn := m.al.getActiveTurnState(sessionKey)
			go func() {
				defer m.summarizing.Delete(summarizeKey)
				defer func() {
					if r := recover(); r != nil {
						logger.WarnCF("agent", "Summarization panic recovered", map[string]any{
							"session_key": sessionKey,
							"panic":       r,
						})
					}
				}()
				logger.Debug("Memory threshold reached. Optimizing conversation history...")
				m.summarizeSession(agent, sessionKey, turn)
			}()
		}
	}
}

type compressionResult struct {
	DroppedMessages   int
	RemainingMessages int
}

// forceCompression aggressively reduces context when the limit is hit.
// It drops the oldest ~50% of Turns (a Turn is a complete user→LLM→response
// cycle, as defined in #1316), so tool-call sequences are never split.
func (m *legacyContextManager) forceCompression(sessionKey string) (compressionResult, bool, error) {
	agent := m.al.agentForSession(sessionKey)
	if agent == nil {
		return compressionResult{}, false, nil
	}

	history, err := session.HistoryChecked(agent.Sessions, sessionKey)
	if err != nil {
		return compressionResult{}, false, err
	}
	var activeTail []providers.Message
	if ts := m.al.getActiveTurnState(sessionKey); ts != nil {
		history, activeTail = splitHistoryForActiveTurn(history, ts.persistedMessagesSnapshot())
		if len(history) == 0 {
			return compressionResult{}, false, nil
		}
	}
	if len(history) <= 2 {
		return compressionResult{}, false, nil
	}

	turns := parseTurnBoundaries(history)
	var mid int
	if len(turns) >= 2 {
		mid = turns[len(turns)/2]
	} else {
		mid = findSafeBoundary(history, len(history)/2)
	}
	var keptHistory []providers.Message
	if mid <= 0 {
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == "user" {
				keptHistory = []providers.Message{history[i]}
				break
			}
		}
	} else {
		keptHistory = history[mid:]
	}
	keptHistory = append(keptHistory, activeTail...)

	droppedCount := len(history) + len(activeTail) - len(keptHistory)

	existingSummary, err := session.SummaryChecked(agent.Sessions, sessionKey)
	if err != nil {
		return compressionResult{}, false, err
	}
	compressionNote := fmt.Sprintf(
		"[Emergency compression dropped %d oldest messages due to context limit]",
		droppedCount,
	)
	if existingSummary != "" {
		compressionNote = existingSummary + "\n\n" + compressionNote
	}
	if err := session.SetSummaryChecked(agent.Sessions, sessionKey, compressionNote); err != nil {
		return compressionResult{}, false, err
	}
	if err := session.SetHistoryChecked(agent.Sessions, sessionKey, keptHistory); err != nil {
		return compressionResult{}, false, err
	}
	if err := agent.Sessions.Save(sessionKey); err != nil {
		return compressionResult{}, false, err
	}

	logger.WarnCF("agent", "Forced compression executed", map[string]any{
		"session_key":  sessionKey,
		"dropped_msgs": droppedCount,
		"new_count":    len(keptHistory),
	})

	return compressionResult{
		DroppedMessages:   droppedCount,
		RemainingMessages: len(keptHistory),
	}, true, nil
}

func (m *legacyContextManager) summarizeSession(agent *AgentInstance, sessionKey string, turns ...*turnState) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fail := func(err error) {
		logger.WarnCF("agent", "Session summarization failed", map[string]any{"session_key": sessionKey, "error": err.Error()})
	}
	if len(turns) > 0 && turns[0] != nil {
		turn := turns[0]
		ctx = withTurnState(ctx, turn)
		defer func() {
			if store, ok := agent.Sessions.(session.TurnUsageStore); ok {
				if err := store.UpdateTurnUsage(sessionKey, turn.turnID, turn.contextUsageSnapshot()); err != nil {
					fail(err)
				}
			}
		}()
	}
	history, err := session.HistoryChecked(agent.Sessions, sessionKey)
	if err != nil {
		fail(err)
		return
	}
	previous, err := session.SummaryChecked(agent.Sessions, sessionKey)
	if err != nil {
		fail(err)
		return
	}
	if len(history) <= 4 {
		return
	}
	cut := findSafeBoundary(history, len(history)-4)
	if cut <= 0 {
		return
	}
	summary, err := summarizeTaskTranscript(ctx, agent.Provider, agent.Model, history[:cut], previous, agent.ContextWindow)
	if err != nil {
		fail(err)
		return
	}
	store, ok := agent.Sessions.(session.SummaryCommitStore)
	if !ok {
		fail(fmt.Errorf("session store does not support atomic summary commits"))
		return
	}
	committed, err := store.CommitSummary(sessionKey, history[:cut], previous, summary)
	if err != nil {
		fail(err)
		return
	}
	if !committed {
		return
	}
	if err := agent.Sessions.Save(sessionKey); err != nil {
		fail(err)
		return
	}
	m.al.emitEvent(runtimeevents.KindAgentSessionSummarize,
		m.al.newTurnEventScope(agent.ID, sessionKey, nil).meta(0, "summarizeSession", "turn.session.summarize"),
		SessionSummarizePayload{SummarizedMessages: cut, KeptMessages: len(history) - cut, SummaryLen: len(summary)})
}

func (m *legacyContextManager) estimateTokens(messages []providers.Message) int {
	total := 0
	for _, msg := range messages {
		total += EstimateMessageTokens(msg)
	}
	return total
}
