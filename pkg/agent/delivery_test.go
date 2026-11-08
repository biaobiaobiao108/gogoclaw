package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

type deliveryTestBus struct {
	mu       sync.Mutex
	failures int
	outbound []bus.OutboundMessage
}

func (b *deliveryTestBus) PublishInbound(context.Context, bus.InboundMessage) error {
	return nil
}

func (b *deliveryTestBus) PublishOutbound(_ context.Context, msg bus.OutboundMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures > 0 {
		b.failures--
		return errors.New("transport unavailable")
	}
	b.outbound = append(b.outbound, msg)
	return nil
}

func (b *deliveryTestBus) PublishOutboundMedia(context.Context, bus.OutboundMediaMessage) error {
	return nil
}

func (b *deliveryTestBus) GetStreamer(context.Context, string, string, string) (bus.Streamer, bool) {
	return nil, false
}

func (b *deliveryTestBus) InboundChan() <-chan bus.InboundMessage {
	return make(chan bus.InboundMessage)
}

type deliveryCheckpointStore struct {
	session.SessionStore
	store session.TurnCheckpointStore

	mu        sync.Mutex
	statuses  []string
	listCalls int
}

func (s *deliveryCheckpointStore) SetTurnCheckpoint(key string, checkpoint session.TurnCheckpoint) error {
	s.mu.Lock()
	if checkpoint.Delivery != nil {
		s.statuses = append(s.statuses, checkpoint.Delivery.Status)
	}
	s.mu.Unlock()
	return s.store.SetTurnCheckpoint(key, checkpoint)
}

func (s *deliveryCheckpointStore) GetTurnCheckpoint(key string) (session.TurnCheckpoint, error) {
	return s.store.GetTurnCheckpoint(key)
}

func (s *deliveryCheckpointStore) ClearTurnCheckpoints(key string) error {
	return s.store.ClearTurnCheckpoints(key)
}

func (s *deliveryCheckpointStore) ListSessions() []string {
	s.mu.Lock()
	s.listCalls++
	s.mu.Unlock()
	return s.SessionStore.ListSessions()
}

func (s *deliveryCheckpointStore) deliveryStatuses() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.statuses...)
}

func (s *deliveryCheckpointStore) resetListCalls() {
	s.mu.Lock()
	s.listCalls = 0
	s.mu.Unlock()
}

func (s *deliveryCheckpointStore) sessionListCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCalls
}

func TestPublishTrackedOutboundPersistsFailureAndRetries(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()

	checkpointStore, ok := agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		t.Fatal("test session store does not support turn checkpoints")
	}
	store := &deliveryCheckpointStore{SessionStore: agent.Sessions, store: checkpointStore}
	agent.Sessions = store

	transport := &deliveryTestBus{failures: 1}
	al.bus = transport
	sessionKey := "delivery-retry"

	err := al.publishTrackedOutbound(context.Background(), sessionKey, bus.OutboundMessage{
		Context:    bus.NewOutboundContext("pico", "pico:delivery-retry", ""),
		SessionKey: sessionKey,
		Content:    "final answer",
	})
	if err == nil {
		t.Fatal("publishTrackedOutbound() error = nil, want transport failure")
	}
	failed, err := store.GetTurnCheckpoint(sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Delivery == nil || failed.Delivery.Status != deliveryStatusFailed || failed.Delivery.Attempts != 1 {
		t.Fatalf("failed delivery = %+v", failed.Delivery)
	}
	if failed.Delivery.MessageID == "" {
		t.Fatal("failed delivery MessageID is empty")
	}
	failedMessageID := failed.Delivery.MessageID

	al.retryPendingDelivery(context.Background())

	delivered, err := store.GetTurnCheckpoint(sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if delivered.Delivery == nil || delivered.Delivery.Status != deliveryStatusDeliveredBus ||
		delivered.Delivery.Attempts != 2 || delivered.Delivery.LastError != "" {
		t.Fatalf("delivered retry = %+v", delivered.Delivery)
	}
	statuses := store.deliveryStatuses()
	want := []string{deliveryStatusQueued, deliveryStatusFailed, deliveryStatusDeliveredBus}
	if len(statuses) != len(want) {
		t.Fatalf("delivery statuses = %v, want %v", statuses, want)
	}
	for i := range want {
		if statuses[i] != want[i] {
			t.Fatalf("delivery statuses = %v, want %v", statuses, want)
		}
	}

	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.outbound) != 1 {
		t.Fatalf("outbound deliveries = %d, want 1", len(transport.outbound))
	}
	if transport.outbound[0].Context.Raw[metadataKeyOutboundKind] != outboundKindFinal {
		t.Fatalf("retried outbound was not marked final: %+v", transport.outbound[0].Context.Raw)
	}
	if transport.outbound[0].ID != failedMessageID {
		t.Fatalf("retried outbound ID = %q, want %q", transport.outbound[0].ID, failedMessageID)
	}
}

func TestAcknowledgeDeliveryUsesIndexedSession(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()

	checkpointStore, ok := agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		t.Fatal("test session store does not support turn checkpoints")
	}
	store := &deliveryCheckpointStore{SessionStore: agent.Sessions, store: checkpointStore}
	agent.Sessions = store
	al.bus = &deliveryTestBus{}

	sessionKey := "delivery-ack-index"
	msg := bus.OutboundMessage{
		Context:    bus.NewOutboundContext("pico", "pico:delivery-ack-index", ""),
		SessionKey: sessionKey,
		Content:    "final answer",
	}
	if err := al.publishTrackedOutbound(context.Background(), sessionKey, msg); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := store.GetTurnCheckpoint(sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Delivery == nil {
		t.Fatal("delivery checkpoint is nil")
	}

	store.resetListCalls()
	if err := al.AcknowledgeDelivery(
		context.Background(),
		"pico",
		"pico:delivery-ack-index",
		checkpoint.Delivery.ID,
	); err != nil {
		t.Fatal(err)
	}
	if calls := store.sessionListCalls(); calls != 0 {
		t.Fatalf("indexed acknowledgement scanned %d session lists, want 0", calls)
	}
	acknowledged, err := store.GetTurnCheckpoint(sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if acknowledged.Delivery == nil || acknowledged.Delivery.Status != deliveryStatusAcknowledged {
		t.Fatalf("acknowledged delivery = %+v", acknowledged.Delivery)
	}
}

func TestRetryPendingDeliveryReconcilesInterruptedStreaming(t *testing.T) {
	for _, tt := range []struct {
		name       string
		content    string
		wantStatus string
		wantOutbox int
		wantError  string
	}{
		{
			name:       "partial stream is dead lettered",
			wantStatus: deliveryStatusDeadLetter,
			wantError:  "interrupted before final content",
		},
		{
			name:       "persisted final content is retried",
			content:    "complete answer",
			wantStatus: deliveryStatusDeliveredBus,
			wantOutbox: 1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
			defer cleanup()

			transport := &deliveryTestBus{}
			al.bus = transport
			store, ok := agent.Sessions.(session.TurnCheckpointStore)
			if !ok {
				t.Fatal("test session store does not support turn checkpoints")
			}
			sessionKey := "delivery-interrupted-stream"
			delivery := session.TurnDelivery{
				ID:          "delivery-interrupted",
				Status:      deliveryStatusStreaming,
				Channel:     "pico",
				ChatID:      "pico:interrupted",
				Content:     tt.content,
				RequiresAck: true,
				CreatedAt:   time.Now().UTC().Add(-time.Minute),
				UpdatedAt:   time.Now().UTC().Add(-time.Minute),
			}
			if err := store.SetTurnCheckpoint(sessionKey, session.TurnCheckpoint{
				TurnID:     "turn-interrupted",
				Status:     "running",
				Deliveries: []session.TurnDelivery{delivery},
				Delivery:   &delivery,
			}); err != nil {
				t.Fatal(err)
			}

			al.retryPendingDelivery(context.Background())

			checkpoint, err := store.GetTurnCheckpoint(sessionKey)
			if err != nil {
				t.Fatal(err)
			}
			if checkpoint.Delivery == nil {
				t.Fatal("delivery checkpoint is nil")
			}
			if checkpoint.Delivery.Status != tt.wantStatus {
				t.Fatalf("delivery status = %q, want %q", checkpoint.Delivery.Status, tt.wantStatus)
			}
			if tt.wantError != "" && !strings.Contains(checkpoint.Delivery.LastError, tt.wantError) {
				t.Fatalf("delivery error = %q, want substring %q", checkpoint.Delivery.LastError, tt.wantError)
			}
			if tt.wantStatus == deliveryStatusDeadLetter && checkpoint.Delivery.DeadLetteredAt == nil {
				t.Fatal("dead-letter timestamp is nil")
			}
			transport.mu.Lock()
			outbox := append([]bus.OutboundMessage(nil), transport.outbound...)
			transport.mu.Unlock()
			if len(outbox) != tt.wantOutbox {
				t.Fatalf("outbox deliveries = %d, want %d", len(outbox), tt.wantOutbox)
			}
			if tt.wantOutbox > 0 && outbox[0].ID != delivery.ID {
				t.Fatalf("legacy delivery retry ID = %q, want stable fallback %q", outbox[0].ID, delivery.ID)
			}
		})
	}
}
