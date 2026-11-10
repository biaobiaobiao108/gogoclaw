package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

type deliveryTestBus struct {
	mu       sync.Mutex
	failures int
	inbound  []bus.InboundMessage
	outbound []bus.OutboundMessage
}

func (b *deliveryTestBus) PublishInbound(_ context.Context, msg bus.InboundMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inbound = append(b.inbound, msg)
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

func TestConsumeAsyncDeliveryReportsUserNotificationFailure(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()

	events, closeEvents := subscribeRuntimeEventsForTest(t, al, 4, runtimeevents.KindAgentError)
	defer closeEvents()

	al.bus = &deliveryTestBus{failures: 1}
	ts := &turnState{
		agent: agent,
		opts: processOptions{
			NoHistory: true,
			Dispatch: DispatchRequest{
				InboundContext: &bus.InboundContext{Channel: "cli", ChatID: "chat", ChatType: "direct"},
			},
		},
		sessionKey: "async-notification-failure",
		turnID:     "turn-async-notification-failure",
		channel:    "cli",
		chatID:     "chat",
	}
	if !ts.enqueueAsyncDelivery(asyncDelivery{
		Origin:         asyncOriginTool,
		Source:         "background_lookup",
		Result:         &tools.ToolResult{ForLLM: "lookup result", ForUser: "lookup finished"},
		PublishForUser: true,
	}) {
		t.Fatal("failed to queue async result")
	}

	var messages []providers.Message
	if err := al.consumeAsyncDeliveries(context.Background(), ts, &messages); err != nil {
		t.Fatalf("consumeAsyncDeliveries() error = %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Content, "lookup result") {
		t.Fatalf("async result was not retained for the active turn: %+v", messages)
	}

	event := waitForRuntimeEvent(t, events, time.Second, func(evt runtimeevents.Event) bool {
		return evt.Kind == runtimeevents.KindAgentError
	})
	payload, ok := event.Payload.(ErrorPayload)
	if !ok {
		t.Fatalf("error event payload = %T, want ErrorPayload", event.Payload)
	}
	if payload.Stage != "async_user_notification" {
		t.Fatalf("error event stage = %q, want async_user_notification", payload.Stage)
	}

	checkpointStore, ok := agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		t.Fatal("test session store does not support turn checkpoints")
	}
	checkpoint, err := checkpointStore.GetTurnCheckpoint(ts.sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Delivery == nil || checkpoint.Delivery.Status != deliveryStatusFailed {
		t.Fatalf("failed async notification was not persisted: %+v", checkpoint.Delivery)
	}

	al.retryPendingDelivery(context.Background())
	transport := al.bus.(*deliveryTestBus)
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.outbound) != 1 || transport.outbound[0].Content != "lookup finished" {
		t.Fatalf("retried async notifications = %+v, want one user-facing result", transport.outbound)
	}
}

func TestAsyncFollowUpReportsUserNotificationFailureAndContinues(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()

	events, closeEvents := subscribeRuntimeEventsForTest(
		t,
		al,
		4,
		runtimeevents.KindAgentError,
		runtimeevents.KindAgentFollowUpQueued,
	)
	defer closeEvents()

	transport := &deliveryTestBus{failures: 1}
	al.bus = transport
	ts := &turnState{
		agent: agent,
		opts: processOptions{
			Dispatch: DispatchRequest{
				InboundContext: &bus.InboundContext{Channel: "cli", ChatID: "chat", ChatType: "direct"},
			},
		},
		sessionKey:       "async-follow-up-notification-failure",
		turnID:           "turn-async-follow-up-notification-failure",
		channel:          "cli",
		chatID:           "chat",
		originSessionKey: "async-follow-up-notification-failure",
	}
	al.publishAsyncFollowUp(ts, asyncDelivery{
		Origin:         asyncOriginSubTurn,
		Source:         "child-turn",
		Result:         &tools.ToolResult{ForLLM: "background result", ForUser: "background task finished"},
		PublishForUser: true,
	})

	seen := map[runtimeevents.Kind]bool{}
	for range 2 {
		event := waitForRuntimeEvent(t, events, time.Second, func(evt runtimeevents.Event) bool {
			return evt.Kind == runtimeevents.KindAgentError || evt.Kind == runtimeevents.KindAgentFollowUpQueued
		})
		seen[event.Kind] = true
		if event.Kind == runtimeevents.KindAgentError {
			payload, ok := event.Payload.(ErrorPayload)
			if !ok || payload.Stage != "async_user_notification" {
				t.Fatalf("async notification error payload = %#v", event.Payload)
			}
		}
	}
	if !seen[runtimeevents.KindAgentError] || !seen[runtimeevents.KindAgentFollowUpQueued] {
		t.Fatalf("expected notification failure and follow-up events, got %v", seen)
	}
	al.retryPendingDelivery(context.Background())

	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.inbound) != 1 {
		t.Fatalf("follow-up inbound messages = %d, want 1", len(transport.inbound))
	}
	if len(transport.outbound) != 1 || transport.outbound[0].Content != "background task finished" {
		t.Fatalf("retried async notifications = %+v, want one user-facing result", transport.outbound)
	}
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

func TestPublishTrackedOutboundPreservesConcurrentSessionDeliveries(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()

	checkpointStore, ok := agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		t.Fatal("test session store does not support turn checkpoints")
	}
	transport := &deliveryTestBus{}
	al.bus = transport

	const deliveryCount = 32
	sessionKey := "concurrent-deliveries"
	start := make(chan struct{})
	errCh := make(chan error, deliveryCount)
	var workers sync.WaitGroup
	workers.Add(deliveryCount)
	for i := range deliveryCount {
		go func(index int) {
			defer workers.Done()
			<-start
			errCh <- al.publishTrackedOutbound(context.Background(), sessionKey, bus.OutboundMessage{
				Context:    bus.InboundContext{Channel: "cli", ChatID: "chat", ChatType: "direct"},
				SessionKey: sessionKey,
				Content:    fmt.Sprintf("notification-%d", index),
			})
		}(i)
	}
	close(start)
	workers.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("publishTrackedOutbound() error = %v", err)
		}
	}

	checkpoint, err := checkpointStore.GetTurnCheckpoint(sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Deliveries) != deliveryCount {
		t.Fatalf("persisted deliveries = %d, want %d", len(checkpoint.Deliveries), deliveryCount)
	}
	contents := make(map[string]bool, deliveryCount)
	for _, delivery := range checkpoint.Deliveries {
		if delivery.Status != deliveryStatusAcknowledged {
			t.Errorf("delivery %q status = %q, want %q", delivery.ID, delivery.Status, deliveryStatusAcknowledged)
		}
		contents[delivery.Content] = true
	}
	for i := range deliveryCount {
		content := fmt.Sprintf("notification-%d", i)
		if !contents[content] {
			t.Errorf("delivery %q was lost from the checkpoint", content)
		}
	}

	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.outbound) != deliveryCount {
		t.Fatalf("published messages = %d, want %d", len(transport.outbound), deliveryCount)
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
