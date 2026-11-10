package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/approval"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

func TestHumanApprovalManagerApproveOnceAndSessionGrant(t *testing.T) {
	manager := newHumanApprovalManager(time.Hour)
	notified := make(chan approval.Request, 2)
	manager.setNotifier(func(_ context.Context, req approval.Request) error {
		notified <- req
		return nil
	})

	var persistedMu sync.Mutex
	var persisted []approval.Request
	persist := func(req approval.Request) error {
		persistedMu.Lock()
		persisted = append(persisted, req)
		persistedMu.Unlock()
		return nil
	}

	first := approval.Request{SessionKey: "session", Tool: "exec", Arguments: map[string]any{"command": "go test ./..."}}
	result := make(chan approval.Decision, 1)
	errCh := make(chan error, 1)
	go func() {
		decision, err := manager.request(context.Background(), first, approvalFingerprint(first.SessionKey, first.Tool, first.Arguments), persist)
		if err != nil {
			errCh <- err
			return
		}
		result <- decision
	}()

	pending := <-notified
	if err := manager.respond(context.Background(), approval.Response{ID: pending.ID, Approve: true, Scope: approval.ScopeSession}); err != nil {
		t.Fatalf("respond() error = %v", err)
	}
	if err := manager.respond(context.Background(), approval.Response{ID: pending.ID, Approve: true, Scope: approval.ScopeSession}); err != nil {
		t.Fatalf("duplicate respond() error = %v", err)
	}
	select {
	case err := <-errCh:
		t.Fatal(err)
	case decision := <-result:
		if !decision.Approved || decision.Scope != approval.ScopeSession {
			t.Fatalf("decision = %+v, want approved session decision", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("approval request did not resolve")
	}

	second := first
	second.ID = "second"
	decision, err := manager.request(context.Background(), second, approvalFingerprint(second.SessionKey, second.Tool, second.Arguments), persist)
	if err != nil {
		t.Fatalf("session-granted request error = %v", err)
	}
	if !decision.Approved {
		t.Fatal("session-granted request was not approved")
	}
	select {
	case unexpected := <-notified:
		t.Fatalf("session grant unexpectedly notified: %+v", unexpected)
	default:
	}

	persistedMu.Lock()
	defer persistedMu.Unlock()
	if len(persisted) < 2 || persisted[0].Status != approval.StatusPending || persisted[len(persisted)-1].Status != approval.StatusApproved {
		t.Fatalf("persisted statuses = %+v, want pending then approved", persisted)
	}
}

func TestHumanApprovalManagerExpiresWithoutBlockingForever(t *testing.T) {
	manager := newHumanApprovalManager(20 * time.Millisecond)
	manager.setNotifier(func(context.Context, approval.Request) error { return nil })

	var persisted approval.Request
	req := approval.Request{
		SessionKey: "session", Tool: "exec",
	}
	decision, err := manager.request(context.Background(), req, approvalFingerprint(req.SessionKey, req.Tool, req.Arguments), func(req approval.Request) error {
		persisted = req
		return nil
	})
	if err != nil {
		t.Fatalf("request() error = %v", err)
	}
	if decision.Approved || decision.Reason != "approval request expired" {
		t.Fatalf("decision = %+v, want expired denial", decision)
	}
	if persisted.Status != approval.StatusExpired {
		t.Fatalf("persisted status = %q, want %q", persisted.Status, approval.StatusExpired)
	}
}

func TestHumanApprovalManagerFailsWhenRequestCannotBeDelivered(t *testing.T) {
	manager := newHumanApprovalManager(time.Hour)
	manager.setNotifier(func(context.Context, approval.Request) error {
		return errors.New("channel unavailable")
	})

	req := approval.Request{
		SessionKey: "session", Tool: "exec",
	}
	decision, err := manager.request(context.Background(), req, approvalFingerprint(req.SessionKey, req.Tool, req.Arguments), func(approval.Request) error { return nil })
	if err == nil {
		t.Fatal("expected delivery error")
	}
	if decision.Approved || decision.Reason != "failed to deliver approval request" {
		t.Fatalf("decision = %+v, want delivery denial", decision)
	}
}

func TestHumanApprovalSessionGrantDoesNotMatchDifferentRedactedArguments(t *testing.T) {
	manager := newHumanApprovalManager(time.Hour)
	notified := make(chan approval.Request, 2)
	manager.setNotifier(func(_ context.Context, req approval.Request) error {
		notified <- req
		return nil
	})

	firstSafe := approval.Request{SessionKey: "session", Tool: "exec", Arguments: map[string]any{"command": "use [FILTERED]"}}
	firstFingerprint := approvalFingerprint(firstSafe.SessionKey, firstSafe.Tool, map[string]any{"command": "use secret-value-one"})
	firstResult := make(chan error, 1)
	go func() {
		_, err := manager.request(context.Background(), firstSafe, firstFingerprint, nil)
		firstResult <- err
	}()
	firstPending := <-notified
	if err := manager.respond(context.Background(), approval.Response{ID: firstPending.ID, Approve: true, Scope: approval.ScopeSession}); err != nil {
		t.Fatalf("approve first request: %v", err)
	}
	if err := <-firstResult; err != nil {
		t.Fatalf("first request: %v", err)
	}

	secondSafe := approval.Request{SessionKey: "session", Tool: "exec", Arguments: map[string]any{"command": "use [FILTERED]"}}
	secondFingerprint := approvalFingerprint(secondSafe.SessionKey, secondSafe.Tool, map[string]any{"command": "use secret-value-two"})
	ctx, cancel := context.WithCancel(context.Background())
	secondResult := make(chan error, 1)
	go func() {
		_, err := manager.request(ctx, secondSafe, secondFingerprint, nil)
		secondResult <- err
	}()
	select {
	case pending := <-notified:
		if pending.Arguments["command"] != "use [FILTERED]" {
			t.Fatalf("pending arguments exposed secret: %v", pending.Arguments)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("different raw arguments incorrectly received the session grant")
	}
	cancel()
	if err := <-secondResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("second request error = %v, want context canceled while pending", err)
	}
}

func TestRespondPersistedApprovalMakesInterruptedTurnResumable(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()

	store, ok := agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		t.Fatal("test session store does not support turn checkpoints")
	}
	sessionKey := "agent:default:cli:persisted-approval"
	checkpoint := session.TurnCheckpoint{
		TurnID: sessionKey,
		Status: "awaiting_approval",
		Approval: &approval.Request{
			ID:         "approval-persisted",
			SessionKey: sessionKey,
			Tool:       "exec",
			Status:     approval.StatusPending,
		},
	}
	if err := store.SetTurnCheckpoint(sessionKey, checkpoint); err != nil {
		t.Fatalf("SetTurnCheckpoint() error = %v", err)
	}

	if err := al.RespondApproval(context.Background(), approval.Response{
		ID:      checkpoint.Approval.ID,
		Approve: true,
		Scope:   approval.ScopeSession,
	}); err != nil {
		t.Fatalf("RespondApproval() error = %v", err)
	}

	resolved, err := store.GetTurnCheckpoint(sessionKey)
	if err != nil {
		t.Fatalf("GetTurnCheckpoint() error = %v", err)
	}
	if resolved.Status != "ready_to_resume" || resolved.Approval == nil ||
		resolved.Approval.Status != approval.StatusApproved ||
		resolved.Approval.Scope != approval.ScopeSession {
		t.Fatalf("resolved checkpoint = %+v", resolved)
	}

	response, err := al.ResumeSession(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if response != "Hello! How can I help you today?" {
		t.Fatalf("ResumeSession() response = %q", response)
	}
}

func TestRespondPersistedApprovalRejectsClosedAndExpiredRequests(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()
	store := agent.Sessions.(session.TurnCheckpointStore)
	sessionKey := "agent:default:cli:persisted-approval-state"

	closed := session.TurnCheckpoint{
		TurnID: sessionKey,
		Status: "ready_to_resume",
		Approval: &approval.Request{
			ID: "approval-closed", SessionKey: sessionKey, Tool: "exec",
			Status: approval.StatusApproved,
		},
	}
	if err := store.SetTurnCheckpoint(sessionKey, closed); err != nil {
		t.Fatal(err)
	}
	if err := al.RespondApproval(context.Background(), approval.Response{
		ID: "approval-closed", Approve: true,
	}); err == nil {
		t.Fatal("expected closed approval to be rejected")
	}

	expired := session.TurnCheckpoint{
		TurnID: sessionKey,
		Status: "awaiting_approval",
		Approval: &approval.Request{
			ID: "approval-expired", SessionKey: sessionKey, Tool: "exec",
			Status:    approval.StatusPending,
			ExpiresAt: time.Now().Add(-time.Minute),
		},
	}
	if err := store.SetTurnCheckpoint(sessionKey, expired); err != nil {
		t.Fatal(err)
	}
	if err := al.RespondApproval(context.Background(), approval.Response{
		ID: "approval-expired", Approve: true,
	}); err == nil {
		t.Fatal("expected expired approval to be rejected")
	}
}
