package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/approval"
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
		decision, err := manager.request(context.Background(), first, persist)
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
	decision, err := manager.request(context.Background(), second, persist)
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
	decision, err := manager.request(context.Background(), approval.Request{
		SessionKey: "session", Tool: "exec",
	}, func(req approval.Request) error {
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

	decision, err := manager.request(context.Background(), approval.Request{
		SessionKey: "session", Tool: "exec",
	}, func(approval.Request) error { return nil })
	if err == nil {
		t.Fatal("expected delivery error")
	}
	if decision.Approved || decision.Reason != "failed to deliver approval request" {
		t.Fatalf("decision = %+v, want delivery denial", decision)
	}
}
