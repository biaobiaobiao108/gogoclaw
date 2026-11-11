package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers/protocoltypes"
	"github.com/biaobiaobiao108/gogoclaw/pkg/seahorse"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tokenizer"
)

// seahorseContextManager adapts seahorse.Engine to agent.ContextManager.
type seahorseContextManager struct {
	engine       *seahorse.Engine
	sessions     session.SessionStore // for lazy history reconciliation
	al           *AgentLoop           // for resolving the agent that owns a session
	bootstrapMu  sync.Mutex
	bootstrapped map[string]struct{}
}

// Close releases the SQLite engine and its background compaction workers.
func (m *seahorseContextManager) Close() error {
	if m == nil || m.engine == nil {
		return nil
	}
	return m.engine.Close()
}

// newSeahorseContextManager creates a seahorse-backed ContextManager.
func newSeahorseContextManager(_ json.RawMessage, al *AgentLoop) (ContextManager, error) {
	if al == nil {
		return nil, fmt.Errorf("seahorse: AgentLoop is required")
	}

	// Resolve workspace for DB path
	// DB stores session data, so it goes in sessions/ directory
	agent := al.runtimeSnapshot().registry.GetDefaultAgent()
	dbPath := agent.Workspace + "/sessions/seahorse.db"

	// Create CompleteFn from provider
	completeFn := providerToCompleteFn(agent.Provider, agent.Model)

	// Create engine
	engine, err := seahorse.NewEngine(seahorse.Config{
		DBPath: dbPath,
	}, completeFn)
	if err != nil {
		return nil, fmt.Errorf("seahorse: create engine: %w", err)
	}

	mgr := &seahorseContextManager{
		engine:   engine,
		sessions: agent.Sessions,
		al:       al,
	}

	// Register seahorse tools with the agent's tool registry
	retrieval := mgr.engine.GetRetrieval()
	al.RegisterTool(seahorse.NewGrepTool(retrieval))
	al.RegisterTool(seahorse.NewExpandTool(retrieval))

	return mgr, nil
}

// providerToCompleteFn wraps providers.LLMProvider as a seahorse.CompleteFn.
func providerToCompleteFn(provider providers.LLMProvider, model string) seahorse.CompleteFn {
	return func(ctx context.Context, prompt string, opts seahorse.CompleteOptions) (string, error) {
		messages := []providers.Message{{Role: "user", Content: prompt}}
		options := map[string]any{
			"max_tokens":       opts.MaxTokens,
			"temperature":      opts.Temperature,
			"prompt_cache_key": "seahorse",
		}
		ts := turnStateFromContext(ctx)
		var resp *providers.LLMResponse
		var err error
		if ts != nil {
			resp, err = ts.runBudgetedModelCall(ctx, messages, nil, opts.MaxTokens,
				func() (*providers.LLMResponse, error) {
					return provider.Chat(ctx, messages, nil, model, options)
				},
				func(resp *providers.LLMResponse) error {
					if err := ts.recordUsageCall(resp.Usage, true, usageCall{
						Provider:  resolvedCandidateProvider(ts.agent.Candidates, ""),
						Model:     resolvedCandidateModelName(ts.agent.Candidates, ""),
						ModelID:   model,
						CostRates: ts.agent.usageRates(resolvedCandidateProvider(ts.agent.Candidates, ""), model, model),
					}); err != nil {
						return fmt.Errorf("record summary usage: %w", err)
					}
					if store, ok := ts.agent.Sessions.(session.TurnUsageStore); ok {
						if err := store.UpdateTurnUsage(ts.sessionKey, ts.turnID, ts.contextUsageSnapshot()); err != nil {
							return fmt.Errorf("persist summary usage: %w", err)
						}
					}
					return nil
				},
			)
		} else {
			resp, err = provider.Chat(ctx, messages, nil, model, options)
		}
		if err != nil {
			return "", err
		}
		if resp == nil {
			return "", fmt.Errorf("summary provider returned empty response")
		}
		return resp.Content, nil
	}
}

// Assemble builds budget-aware context from seahorse SQLite.
func (m *seahorseContextManager) Assemble(ctx context.Context, req *AssembleRequest) (*AssembleResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("seahorse assemble: nil request")
	}
	if err := m.ensureBootstrapped(ctx, req.SessionKey); err != nil {
		return nil, err
	}

	budget := req.Budget
	if budget <= 0 {
		budget = 100000
	}

	// Reserve space for model response (spec lines 1400-1410)
	effectiveBudget := budget - req.MaxTokens
	if effectiveBudget <= 0 {
		// MaxTokens >= budget is a configuration problem
		// Use 50% as minimum to avoid guaranteed overflow
		logger.WarnCF("agent", "MaxTokens >= budget, using 50% fallback",
			map[string]any{"budget": budget, "max_tokens": req.MaxTokens})
		effectiveBudget = budget / 2
	}

	result, err := m.engine.Assemble(ctx, req.SessionKey, seahorse.AssembleInput{
		Budget: effectiveBudget,
	})
	if err != nil {
		return nil, fmt.Errorf("seahorse assemble: %w", err)
	}

	history := seahorseToProviderMessages(result)

	// Summary is already formatted as XML with system prompt addition by assembler
	return &AssembleResponse{
		History: history,
		Summary: result.Summary,
	}, nil
}

// Compact compresses conversation history via seahorse summarization.
func (m *seahorseContextManager) Compact(ctx context.Context, req *CompactRequest) error {
	if req == nil {
		return nil
	}

	// For retry (LLM overflow), use aggressive CompactUntilUnder to guarantee
	// context shrinks below budget (spec lines ~1410).
	if req.Reason == ContextCompressReasonRetry && req.Budget > 0 {
		_, err := m.engine.CompactUntilUnder(ctx, req.SessionKey, req.Budget)
		return err
	}

	_, err := m.engine.Compact(ctx, req.SessionKey, seahorse.CompactInput{
		Force:  req.Reason == ContextCompressReasonRetry,
		Budget: &req.Budget,
	})
	return err
}

// Ingest records a new message after Assemble has reconciled the session.
func (m *seahorseContextManager) Ingest(ctx context.Context, req *IngestRequest) error {
	if req == nil {
		return nil
	}

	msg := providerToSeahorseMessage(req.Message)
	_, err := m.engine.Ingest(ctx, req.SessionKey, []seahorse.Message{msg})
	return err
}

// Clear removes all stored context for a session from both SQLite indexes.
func (m *seahorseContextManager) Clear(ctx context.Context, sessionKey string) error {
	m.bootstrapMu.Lock()
	delete(m.bootstrapped, sessionKey)
	m.bootstrapMu.Unlock()
	if err := m.engine.ClearSession(ctx, sessionKey); err != nil {
		return err
	}
	// The session may belong to a routed (non-default) agent whose SQLite
	// store differs from the bootstrap store, so clear the owner's store.
	sessions := m.sessions
	if m.al != nil {
		if agent := m.al.agentForSession(sessionKey); agent != nil && agent.Sessions != nil {
			sessions = agent.Sessions
		}
	}
	if sessions != nil {
		sessions.SetHistory(sessionKey, []providers.Message{})
		sessions.SetSummary(sessionKey, "")
		return sessions.Save(sessionKey)
	}
	return nil
}

// Reconcile on first use, not at startup. The bounded cache avoids retaining
// every session key for the lifetime of a long-running gateway.
func (m *seahorseContextManager) ensureBootstrapped(ctx context.Context, sessionKey string) error {
	m.bootstrapMu.Lock()
	defer m.bootstrapMu.Unlock()
	if _, ok := m.bootstrapped[sessionKey]; ok {
		return nil
	}
	sessions := m.sessions
	if m.al != nil {
		if agent := m.al.agentForSession(sessionKey); agent != nil {
			sessions = agent.Sessions
		}
	}
	if sessions == nil {
		return nil
	}
	history, err := session.TranscriptChecked(sessions, sessionKey)
	if err != nil {
		return fmt.Errorf("read seahorse bootstrap history: %w", err)
	}

	// Convert provider messages to seahorse messages
	msgs := make([]seahorse.Message, len(history))
	for i, h := range history {
		msgs[i] = providerToSeahorseMessage(h)
	}

	if err := m.engine.Bootstrap(ctx, sessionKey, msgs); err != nil {
		return fmt.Errorf("bootstrap seahorse: %w", err)
	}
	if m.bootstrapped == nil {
		m.bootstrapped = make(map[string]struct{})
	}
	if len(m.bootstrapped) >= 128 {
		for key := range m.bootstrapped {
			delete(m.bootstrapped, key)
			break
		}
	}
	m.bootstrapped[sessionKey] = struct{}{}
	return nil
}

// providerToSeahorseMessage converts a providers.Message to a seahorse.Message.
func providerToSeahorseMessage(msg protocoltypes.Message) seahorse.Message {
	result := seahorse.Message{
		Role:             msg.Role,
		Content:          msg.Content,
		ModelName:        msg.ModelName,
		ReasoningContent: msg.ReasoningContent,
		TokenCount:       tokenizer.EstimateMessageTokens(msg),
		CreatedAt:        normalizeSeahorseMessageCreatedAt(msg.CreatedAt),
	}

	// Convert ToolCalls → MessageParts
	// Parts are authoritative in storage; retain text and provider metadata too.
	if msg.Content != "" && msg.ToolCallID == "" {
		result.Parts = append(result.Parts, seahorse.MessagePart{Type: "text", Text: msg.Content})
	}
	if msg.ID != "" || len(msg.Attachments) > 0 {
		metadata, _ := json.Marshal(struct {
			ID          string                 `json:"message_id,omitempty"`
			Attachments []providers.Attachment `json:"attachments,omitempty"`
		}{msg.ID, msg.Attachments})
		result.Parts = append(result.Parts, seahorse.MessagePart{Type: "provider_metadata", Text: string(metadata)})
	}
	for _, tc := range msg.ToolCalls {
		tc = providers.NormalizeToolCall(tc)
		if tc.Type == "" {
			tc.Type = "function"
		}
		if tc.ThoughtSignature != "" && tc.Function != nil {
			tc.Function = &providers.FunctionCall{Name: tc.Function.Name, Arguments: tc.Function.Arguments, ThoughtSignature: tc.ThoughtSignature}
		}
		encoded, _ := json.Marshal(tc)
		part := seahorse.MessagePart{
			Type:       "tool_use",
			Name:       tc.Name,
			Text:       string(encoded),
			ToolCallID: tc.ID,
		}
		if tc.Function != nil {
			part.Arguments = tc.Function.Arguments
		}
		result.Parts = append(result.Parts, part)
	}

	// Convert tool result
	if msg.ToolCallID != "" {
		part := seahorse.MessagePart{
			Type:       "tool_result",
			ToolCallID: msg.ToolCallID,
			Text:       msg.Content,
		}
		result.Parts = append(result.Parts, part)
	}

	// Convert media attachments
	for _, mediaURI := range msg.Media {
		part := seahorse.MessagePart{
			Type:     "media",
			MediaURI: mediaURI,
		}
		result.Parts = append(result.Parts, part)
	}

	return result
}

func normalizeSeahorseMessageCreatedAt(createdAt *time.Time) time.Time {
	if createdAt == nil || createdAt.IsZero() {
		return time.Time{}
	}
	return createdAt.UTC().Truncate(time.Second)
}

// seahorseToProviderMessages converts a seahorse.AssembleResult to []providers.Message.
func seahorseToProviderMessages(result *seahorse.AssembleResult) []protocoltypes.Message {
	messages := make([]protocoltypes.Message, 0, len(result.Messages))

	// Convert assembled messages (which already include summary XML messages)
	for _, msg := range result.Messages {
		pm := protocoltypes.Message{
			Role:             msg.Role,
			Content:          msg.Content,
			ModelName:        msg.ModelName,
			ReasoningContent: msg.ReasoningContent,
		}

		var text []string
		// Reconstruct provider content rather than the search-index rendering.
		for _, part := range msg.Parts {
			if part.Type == "text" {
				text = append(text, part.Text)
			}
			if part.Type == "provider_metadata" {
				var metadata struct {
					ID          string                 `json:"message_id"`
					Attachments []providers.Attachment `json:"attachments"`
				}
				if json.Unmarshal([]byte(part.Text), &metadata) == nil {
					pm.ID = metadata.ID
					pm.Attachments = metadata.Attachments
				}
			}
			if part.Type == "tool_use" {
				pm.ToolCalls = append(pm.ToolCalls, protocoltypes.ToolCall{
					ID:   part.ToolCallID,
					Type: "function", // Required by OpenAI-compatible APIs (GLM, etc.)
					Function: &protocoltypes.FunctionCall{
						Name:      part.Name,
						Arguments: part.Arguments,
					},
				})
				if part.Text != "" {
					var call providers.ToolCall
					if json.Unmarshal([]byte(part.Text), &call) == nil {
						pm.ToolCalls[len(pm.ToolCalls)-1] = providers.NormalizeToolCall(call)
					}
				}
			}
			if part.Type == "tool_result" {
				pm.ToolCallID = part.ToolCallID
				if part.Text != "" {
					pm.Content = part.Text
				}
			}
			if part.Type == "media" && part.MediaURI != "" {
				pm.Media = append(pm.Media, part.MediaURI)
			}
		}

		if len(text) > 0 {
			pm.Content = strings.Join(text, "\n")
		} else if len(pm.ToolCalls) > 0 {
			pm.Content = ""
		}
		if !msg.CreatedAt.IsZero() {
			created := msg.CreatedAt
			pm.CreatedAt = &created
		}
		messages = append(messages, pm)
	}

	return messages
}

func init() {
	if err := RegisterContextManager("seahorse", newSeahorseContextManager); err != nil {
		panic(fmt.Sprintf("register seahorse context manager: %v", err))
	}
}
