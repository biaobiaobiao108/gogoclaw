package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/session"
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

	mu       sync.Mutex
	statuses []string
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

func (s *deliveryCheckpointStore) deliveryStatuses() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.statuses...)
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
}
