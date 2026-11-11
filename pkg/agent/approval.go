package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/biaobiaobiao108/gogoclaw/pkg/approval"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

var (
	errApprovalNotFound      = errors.New("approval request not found")
	errApprovalAlreadyClosed = errors.New("approval request already resolved")
)

type pendingHumanApproval struct {
	request     approval.Request
	fingerprint string
	decision    chan approval.Decision
}

type humanApprovalManager struct {
	mu       sync.Mutex
	timeout  time.Duration
	pending  map[string]*pendingHumanApproval
	resolved map[string]resolvedApproval
	grants   map[string]map[string]approval.Scope
	notifier func(context.Context, approval.Request) error
}

type resolvedApproval struct {
	decision   approval.Decision
	resolvedAt time.Time
}

func newHumanApprovalManager(timeout time.Duration) *humanApprovalManager {
	return &humanApprovalManager{
		timeout:  timeout,
		pending:  make(map[string]*pendingHumanApproval),
		resolved: make(map[string]resolvedApproval),
		grants:   make(map[string]map[string]approval.Scope),
	}
}

func (m *humanApprovalManager) setNotifier(notifier func(context.Context, approval.Request) error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.notifier = notifier
	m.mu.Unlock()
}

func (m *humanApprovalManager) request(
	ctx context.Context,
	req approval.Request,
	fingerprint string,
	persist func(approval.Request) error,
) (approval.Decision, error) {
	if m == nil {
		return approval.Decision{}, fmt.Errorf("human approval is not configured")
	}
	if req.ID == "" {
		req.ID = "approval-" + uuid.NewString()
	}
	now := time.Now().UTC()
	req.Status = approval.StatusPending
	req.CreatedAt = now
	if req.ExpiresAt.IsZero() {
		req.ExpiresAt = now.Add(m.timeout)
	}

	m.mu.Lock()
	if m.grants[req.SessionKey] != nil {
		if _, ok := m.grants[req.SessionKey][fingerprint]; ok {
			m.mu.Unlock()
			return approval.Decision{Approved: true, Scope: approval.ScopeSession}, nil
		}
	}
	notifier := m.notifier
	pending := &pendingHumanApproval{request: req, fingerprint: fingerprint, decision: make(chan approval.Decision, 1)}
	m.pending[req.ID] = pending
	m.mu.Unlock()

	if persist != nil {
		if err := persist(req); err != nil {
			m.removePending(req.ID)
			return approval.Decision{}, err
		}
	}

	if notifier == nil {
		decision := approval.Decision{Reason: "no human approval interface is connected"}
		if err := m.finishRequest(req, decision, approval.StatusDenied, persist); err != nil {
			return decision, fmt.Errorf("persist unresolved approval: %w", err)
		}
		return decision, nil
	}
	if err := notifier(ctx, req); err != nil {
		logger.WarnCF("agent", "Failed to notify human approval request", map[string]any{
			"approval_id": req.ID,
			"session_key": req.SessionKey,
			"tool":        req.Tool,
			"error":       err.Error(),
		})
		decision := approval.Decision{Reason: "failed to deliver approval request"}
		if persistErr := m.finishRequest(req, decision, approval.StatusDenied, persist); persistErr != nil {
			return decision, fmt.Errorf("notify human approval: %v (persist failure: %w)", err, persistErr)
		}
		return decision, fmt.Errorf("notify human approval: %w", err)
	}

	remaining := time.Until(req.ExpiresAt)
	if remaining <= 0 {
		decision := approval.Decision{Reason: "approval request expired"}
		if err := m.finishRequest(req, decision, approval.StatusExpired, persist); err != nil {
			return decision, fmt.Errorf("persist expired approval: %w", err)
		}
		return decision, nil
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()

	select {
	case decision := <-pending.decision:
		status := approval.StatusDenied
		if decision.Approved {
			status = approval.StatusApproved
		}
		if err := persistApprovalStatus(persist, req, decision, status, decision.Reason); err != nil {
			return decision, fmt.Errorf("persist approval decision: %w", err)
		}
		return decision, nil
	case <-timer.C:
		decision := approval.Decision{Reason: "approval request expired"}
		if err := m.finishRequest(req, decision, approval.StatusExpired, persist); err != nil {
			return decision, fmt.Errorf("persist expired approval: %w", err)
		}
		return decision, nil
	case <-ctx.Done():
		m.removePending(req.ID)
		if err := persistApprovalStatus(persist, req, approval.Decision{}, approval.StatusExpired, "turn canceled before approval"); err != nil {
			return approval.Decision{}, fmt.Errorf("persist canceled approval: %w", err)
		}
		return approval.Decision{}, ctx.Err()
	}
}

func (m *humanApprovalManager) respond(_ context.Context, response approval.Response) error {
	if m == nil {
		return errApprovalNotFound
	}
	if strings.TrimSpace(response.ID) == "" {
		return fmt.Errorf("approval id is required")
	}
	if response.Scope != approval.ScopeSession {
		response.Scope = approval.ScopeOnce
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.resolved[response.ID]; ok {
		if existing.decision.Approved == response.Approve {
			return nil
		}
		return errApprovalAlreadyClosed
	}
	pending, ok := m.pending[response.ID]
	if !ok {
		return errApprovalNotFound
	}
	delete(m.pending, response.ID)
	decision := approval.Decision{
		Approved: response.Approve,
		Scope:    response.Scope,
		Reason:   strings.TrimSpace(response.Reason),
	}
	if decision.Approved && decision.Scope == approval.ScopeSession {
		if m.grants[pending.request.SessionKey] == nil {
			m.grants[pending.request.SessionKey] = make(map[string]approval.Scope)
		}
		m.grants[pending.request.SessionKey][pending.fingerprint] = approval.ScopeSession
	}
	m.resolved[response.ID] = resolvedApproval{decision: decision, resolvedAt: time.Now()}
	pending.decision <- decision
	m.purgeResolvedLocked(time.Now())
	return nil
}

func (m *humanApprovalManager) pendingForSession(sessionKey string) *approval.Request {
	if m == nil || strings.TrimSpace(sessionKey) == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.pending))
	for id := range m.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		pending := m.pending[id]
		if pending.request.SessionKey == sessionKey {
			request := pending.request
			request.Arguments = cloneStringAnyMap(request.Arguments)
			return &request
		}
	}
	return nil
}

func (m *humanApprovalManager) removePending(id string) {
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
}

func (m *humanApprovalManager) finishRequest(
	req approval.Request,
	decision approval.Decision,
	status approval.Status,
	persist func(approval.Request) error,
) error {
	m.removePending(req.ID)
	m.mu.Lock()
	m.resolved[req.ID] = resolvedApproval{decision: decision, resolvedAt: time.Now()}
	m.purgeResolvedLocked(time.Now())
	m.mu.Unlock()
	return persistApprovalStatus(persist, req, decision, status, decision.Reason)
}

func (m *humanApprovalManager) purgeResolvedLocked(now time.Time) {
	for id, resolved := range m.resolved {
		if now.Sub(resolved.resolvedAt) > 10*time.Minute {
			delete(m.resolved, id)
		}
	}
}

func persistApprovalStatus(
	persist func(approval.Request) error,
	req approval.Request,
	decision approval.Decision,
	status approval.Status,
	reason string,
) error {
	if persist == nil {
		return nil
	}
	now := time.Now().UTC()
	req.Status = status
	req.Scope = decision.Scope
	req.ResolvedAt = &now
	if reason != "" {
		req.Reason = reason
	}
	return persist(req)
}

func approvalFingerprint(sessionKey, tool string, arguments map[string]any) string {
	encoded, _ := json.Marshal(arguments)
	digest := sha256.Sum256(encoded)
	return sessionKey + "\x00" + tool + "\x00" + hex.EncodeToString(digest[:])
}

func filterApprovalValue(cfg *config.Config, value any) any {
	switch typed := value.(type) {
	case string:
		if cfg == nil {
			return typed
		}
		return cfg.FilterSensitiveData(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = filterApprovalValue(cfg, item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = filterApprovalValue(cfg, item)
		}
		return out
	default:
		return value
	}
}

func approvalRiskSummary(tool string) string {
	switch tool {
	case "exec":
		return "Runs a command on the host and may modify files or external state."
	case "write_file", "edit_file", "append_file":
		return "Changes files in the workspace."
	case "spawn", "subagent":
		return "Starts another agent or background process with tool access."
	case "install_skill":
		return "Downloads and installs executable instructions into the workspace."
	default:
		return "This tool can change state outside the conversation."
	}
}

type ApprovalEventPayload struct {
	ApprovalID string          `json:"approval_id"`
	Tool       string          `json:"tool"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Status     approval.Status `json:"status"`
	Scope      approval.Scope  `json:"scope,omitempty"`
	Reason     string          `json:"reason,omitempty"`
}

func (al *AgentLoop) SetApprovalNotifier(notifier func(context.Context, approval.Request) error) {
	if al == nil || al.humanApprovals == nil {
		return
	}
	al.humanApprovals.setNotifier(notifier)
}

func (al *AgentLoop) RespondApproval(ctx context.Context, response approval.Response) error {
	if al == nil || al.humanApprovals == nil {
		return errApprovalNotFound
	}
	err := al.humanApprovals.respond(ctx, response)
	if err == nil || !errors.Is(err, errApprovalNotFound) {
		return err
	}
	return al.respondPersistedApproval(ctx, response)
}

func (al *AgentLoop) PendingApprovalByConversation(channel, chatID string) *approval.Request {
	turn := al.GetActiveTurnByConversation(channel, chatID)
	if turn == nil {
		return al.persistedPendingApprovalByConversation(channel, chatID)
	}
	if al.humanApprovals == nil {
		return nil
	}
	return al.humanApprovals.pendingForSession(turn.SessionKey)
}

func (al *AgentLoop) persistedPendingApprovalByConversation(channel, chatID string) *approval.Request {
	registry := al.GetRegistry()
	if registry == nil {
		return nil
	}
	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok || agent == nil {
			continue
		}
		store, ok := agent.Sessions.(session.TurnCheckpointStore)
		if !ok {
			continue
		}
		for _, sessionKey := range agent.Sessions.ListSessions() {
			checkpoint, err := store.GetTurnCheckpoint(sessionKey)
			if err != nil || checkpoint.Approval == nil ||
				checkpoint.Approval.Status != approval.StatusPending {
				continue
			}
			request := checkpoint.Approval
			if request.Channel != channel || request.ChatID != chatID {
				continue
			}
			if !request.ExpiresAt.IsZero() && time.Now().After(request.ExpiresAt) {
				continue
			}
			cloned := *request
			cloned.Arguments = cloneStringAnyMap(request.Arguments)
			return &cloned
		}
	}
	return nil
}

func (al *AgentLoop) respondPersistedApproval(ctx context.Context, response approval.Response) error {
	if strings.TrimSpace(response.ID) == "" {
		return fmt.Errorf("approval id is required")
	}
	registry := al.GetRegistry()
	if registry == nil {
		return errApprovalNotFound
	}
	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok || agent == nil {
			continue
		}
		store, ok := agent.Sessions.(session.TurnCheckpointStore)
		if !ok {
			continue
		}
		for _, sessionKey := range agent.Sessions.ListSessions() {
			checkpoint, err := store.GetTurnCheckpoint(sessionKey)
			if err != nil || checkpoint.Approval == nil || checkpoint.Approval.ID != response.ID {
				continue
			}
			if response.SessionKey != "" && checkpoint.Approval.SessionKey != response.SessionKey {
				continue
			}
			if response.Channel != "" && checkpoint.Approval.Channel != response.Channel {
				continue
			}
			if response.ChatID != "" && checkpoint.Approval.ChatID != response.ChatID {
				continue
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			return al.updateTurnCheckpoint(store, sessionKey, func(checkpoint *session.TurnCheckpoint) error {
				if checkpoint.Approval == nil || checkpoint.Approval.ID != response.ID {
					return errApprovalNotFound
				}
				if checkpoint.Approval.Status != approval.StatusPending {
					return errApprovalAlreadyClosed
				}
				if !checkpoint.Approval.ExpiresAt.IsZero() && time.Now().After(checkpoint.Approval.ExpiresAt) {
					return fmt.Errorf("approval request %s has expired", response.ID)
				}
				now := time.Now().UTC()
				status := approval.StatusDenied
				checkpoint.Status = "denied"
				if response.Approve {
					status = approval.StatusApproved
					checkpoint.Status = "ready_to_resume"
				}
				checkpoint.Approval.Status = status
				checkpoint.Approval.Scope = response.Scope
				if checkpoint.Approval.Scope != approval.ScopeSession {
					checkpoint.Approval.Scope = approval.ScopeOnce
				}
				checkpoint.Approval.Reason = strings.TrimSpace(response.Reason)
				checkpoint.Approval.ResolvedAt = &now
				checkpoint.UpdatedAt = now
				return nil
			})
		}
	}
	return errApprovalNotFound
}

// ResumeSession continues an interrupted or approved turn from its durable
// checkpoint. The existing history supplies the prior work; the synthetic
// prompt only tells the model how to continue.
func (al *AgentLoop) ResumeSession(ctx context.Context, sessionKey string) (string, error) {
	if al == nil {
		return "", fmt.Errorf("agent loop is unavailable")
	}
	agent := al.agentForSession(sessionKey)
	if agent == nil {
		return "", fmt.Errorf("session %q is unavailable", sessionKey)
	}
	store, ok := agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		return "", fmt.Errorf("session store does not support resumable turns")
	}
	checkpoint, err := store.GetTurnCheckpoint(sessionKey)
	if err != nil {
		return "", err
	}
	if checkpoint.Status == "" || checkpoint.Status == "completed" {
		return "", fmt.Errorf("session %q has no interrupted task", sessionKey)
	}
	if checkpoint.Approval != nil && checkpoint.Approval.Status == approval.StatusPending {
		return "", fmt.Errorf("task is waiting for approval %s", checkpoint.Approval.ID)
	}
	placeholder, err := al.claimSessionSlot(sessionKey)
	if err != nil {
		return "", err
	}
	defer al.releaseSessionTurnState(sessionKey, placeholder)
	if err := al.updateTurnCheckpoint(store, sessionKey, func(checkpoint *session.TurnCheckpoint) error {
		if checkpoint.Status == "" || checkpoint.Status == "completed" {
			return fmt.Errorf("session %q no longer has an interrupted task", sessionKey)
		}
		if checkpoint.Approval != nil && checkpoint.Approval.Status == approval.StatusPending {
			return fmt.Errorf("task is still waiting for approval %s", checkpoint.Approval.ID)
		}
		checkpoint.Status = "resuming"
		checkpoint.UpdatedAt = time.Now().UTC()
		return nil
	}); err != nil {
		return "", err
	}
	return al.processDirectWithChannel(ctx,
		"Continue the interrupted task from the durable checkpoint. Review the existing conversation, preserve completed work, and resume from the first incomplete step. Do not repeat completed tool calls.",
		sessionKey, "cli", "direct", true,
	)
}

func (al *AgentLoop) requestHumanApproval(
	ctx context.Context,
	ts *turnState,
	toolName, toolCallID string,
	arguments map[string]any,
	reason string,
) (approval.Decision, error) {
	if al == nil || ts == nil || al.humanApprovals == nil {
		return approval.Decision{}, fmt.Errorf("human approval is not configured")
	}
	safeArguments, _ := filterApprovalValue(al.runtimeSnapshot().cfg, cloneStringAnyMap(arguments)).(map[string]any)
	req := approval.Request{
		ID:          "approval-" + uuid.NewString(),
		TurnID:      ts.turnID,
		SessionKey:  ts.sessionKey,
		Channel:     ts.channel,
		ChatID:      ts.chatID,
		Tool:        toolName,
		ToolCallID:  toolCallID,
		Arguments:   safeArguments,
		RiskSummary: approvalRiskSummary(toolName),
		Reason:      strings.TrimSpace(reason),
	}
	al.emitEvent(
		runtimeevents.KindAgentApprovalRequested,
		ts.eventMeta("runTurn", "turn.approval.requested"),
		ApprovalEventPayload{ApprovalID: req.ID, Tool: req.Tool, ToolCallID: req.ToolCallID, Status: approval.StatusPending},
	)
	persist := func(updated approval.Request) error {
		return ts.saveApprovalCheckpoint(updated)
	}
	fingerprint := approvalFingerprint(ts.sessionKey, toolName, arguments)
	decision, err := al.humanApprovals.request(ctx, req, fingerprint, persist)
	status := approval.StatusDenied
	if decision.Approved {
		status = approval.StatusApproved
	} else if errors.Is(ctx.Err(), context.DeadlineExceeded) || decision.Reason == "approval request expired" {
		status = approval.StatusExpired
	}
	al.emitEvent(
		runtimeevents.KindAgentApprovalResolved,
		ts.eventMeta("runTurn", "turn.approval.resolved"),
		ApprovalEventPayload{
			ApprovalID: req.ID, Tool: req.Tool, ToolCallID: req.ToolCallID,
			Status: status, Scope: decision.Scope, Reason: decision.Reason,
		},
	)
	return decision, err
}

func (ts *turnState) saveApprovalCheckpoint(req approval.Request) error {
	if ts == nil || ts.agent == nil {
		return nil
	}
	store, ok := ts.agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		return nil
	}
	al := ts.al
	if al == nil {
		return fmt.Errorf("agent loop is unavailable")
	}
	return al.updateTurnCheckpoint(store, ts.sessionKey, func(checkpoint *session.TurnCheckpoint) error {
		checkpoint.TurnID = ts.turnID
		checkpoint.UpdatedAt = time.Now().UTC()
		checkpoint.Activities = ts.activitiesSnapshot()
		checkpoint.Approval = &req
		if req.Status == approval.StatusPending {
			checkpoint.Status = "awaiting_approval"
		} else if checkpoint.Status == "awaiting_approval" {
			checkpoint.Status = "running"
		}
		return nil
	})
}
