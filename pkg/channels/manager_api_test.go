package channels

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"golang.org/x/time/rate"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
)

func TestManager_PlaceholderConsumedByResponse(t *testing.T) {
	mgr := &Manager{
		channels:     make(map[string]Channel),
		workers:      make(map[string]*channelWorker),
		placeholders: sync.Map{},
	}

	mockCh := &mockChannel{
		sendFn: func(ctx context.Context, msg bus.OutboundMessage) error {
			return nil
		},
	}
	worker := newChannelWorker("mock", mockCh, "mock")
	mgr.channels["mock"] = mockCh
	mgr.workers["mock"] = worker

	ctx := context.Background()
	key := "mock:chat-1"

	// Simulate a placeholder recorded by base.go HandleMessage
	mgr.RecordPlaceholder("mock", "chat-1", "ph-123")

	if _, ok := mgr.placeholders.Load(key); !ok {
		t.Fatal("expected placeholder to be recorded")
	}

	// Transcription feedback arrives first — it should consume the placeholder
	// and be delivered via EditMessage, not Send.
	msgTranscript := testOutboundMessage(bus.OutboundMessage{
		Channel: "mock",
		ChatID:  "chat-1",
		Content: "Transcript: hello",
	})
	mgr.sendWithRetry(ctx, "mock", worker, msgTranscript)

	if mockCh.editedMessages != 1 {
		t.Errorf("expected 1 edited message (placeholder consumed by transcript), got %d", mockCh.editedMessages)
	}
	if len(mockCh.sentMessages) != 0 {
		t.Errorf("expected 0 normal messages (transcript used edit), got %d", len(mockCh.sentMessages))
	}

	// Placeholder should be gone now
	if _, ok := mgr.placeholders.Load(key); ok {
		t.Error("expected placeholder to be removed after being consumed")
	}

	// Final LLM response arrives — no placeholder left, so it goes through Send
	msgFinal := testOutboundMessage(bus.OutboundMessage{
		Channel: "mock",
		ChatID:  "chat-1",
		Content: "Final Answer",
	})
	mgr.sendWithRetry(ctx, "mock", worker, msgFinal)

	if len(mockCh.sentMessages) != 1 {
		t.Errorf("expected 1 normal message sent, got %d", len(mockCh.sentMessages))
	}
}

func TestSendMessage_Synchronous(t *testing.T) {
	m := newTestManager()

	var received []bus.OutboundMessage
	ch := &mockChannel{
		sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
			received = append(received, msg)
			return nil
		},
	}

	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel:          "test",
		ChatID:           "123",
		Content:          "hello world",
		ReplyToMessageID: "msg-456",
	})

	err := m.SendMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// SendMessage is synchronous — message should already be delivered
	if len(received) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(received))
	}
	if received[0].ReplyToMessageID != "msg-456" {
		t.Fatalf("expected ReplyToMessageID msg-456, got %s", received[0].ReplyToMessageID)
	}
	if received[0].Content != "hello world" {
		t.Fatalf("expected content 'hello world', got %s", received[0].Content)
	}
}

func TestSendMessage_UnknownChannel(t *testing.T) {
	m := newTestManager()

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "nonexistent",
		ChatID:  "123",
		Content: "hello",
	})

	err := m.SendMessage(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error for unknown channel")
	}
}

func TestSendMessage_NoWorker(t *testing.T) {
	m := newTestManager()

	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error { return nil },
	}
	m.channels["test"] = ch
	// No worker registered

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "hello",
	})

	err := m.SendMessage(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error when no worker exists")
	}
}

func TestSendMessage_WithRetry(t *testing.T) {
	m := newTestManager()

	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			if callCount == 1 {
				return fmt.Errorf("transient: %w", ErrTemporary)
			}
			return nil
		},
	}

	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "retry me",
	})

	err := m.SendMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if callCount != 2 {
		t.Fatalf("expected 2 Send calls (1 failure + 1 success), got %d", callCount)
	}
}

func TestSendMessage_ContextOnlyUsesContextAddressing(t *testing.T) {
	m := newTestManager()

	var received []bus.OutboundMessage
	ch := &mockChannel{
		sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
			received = append(received, msg)
			return nil
		},
	}

	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	msg := testOutboundMessage(bus.OutboundMessage{
		Context: bus.NewOutboundContext("test", "123", "msg-9"),
		Content: "hello",
	})

	if err := m.SendMessage(context.Background(), msg); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(received) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(received))
	}
	if received[0].Channel != "test" || received[0].ChatID != "123" {
		t.Fatalf("expected mirrored legacy address, got %+v", received[0])
	}
	if received[0].Context.Channel != "test" || received[0].Context.ChatID != "123" {
		t.Fatalf("expected context address to be preserved, got %+v", received[0].Context)
	}
	if received[0].ReplyToMessageID != "msg-9" {
		t.Fatalf("expected reply_to_message_id msg-9, got %q", received[0].ReplyToMessageID)
	}
}

func TestSendMessage_WithSplitting(t *testing.T) {
	m := newTestManager()

	var received []string
	ch := &mockChannelWithLength{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
				received = append(received, msg.Content)
				return nil
			},
		},
		maxLen: 5,
	}

	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "hello world",
	})

	err := m.SendMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(received) < 2 {
		t.Fatalf("expected message to be split into at least 2 chunks, got %d", len(received))
	}
}

func TestSendMedia_ContextOnlyUsesContextAddressing(t *testing.T) {
	m := newTestManager()

	var received []bus.OutboundMediaMessage
	ch := &mockMediaChannel{
		sendMediaFn: func(_ context.Context, msg bus.OutboundMediaMessage) ([]string, error) {
			received = append(received, msg)
			return nil, nil
		},
	}

	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	msg := testOutboundMediaMessage(bus.OutboundMediaMessage{
		Context: bus.NewOutboundContext("test", "media-chat", ""),
		Parts:   []bus.MediaPart{{Type: "image", Ref: "media://1"}},
	})

	if err := m.SendMedia(context.Background(), msg); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(received) != 1 {
		t.Fatalf("expected 1 media message sent, got %d", len(received))
	}
	if received[0].Channel != "test" || received[0].ChatID != "media-chat" {
		t.Fatalf("expected mirrored legacy media address, got %+v", received[0])
	}
	if received[0].Context.Channel != "test" || received[0].Context.ChatID != "media-chat" {
		t.Fatalf("expected media context address to be preserved, got %+v", received[0].Context)
	}
}

func TestSendMessage_PreservesOrdering(t *testing.T) {
	m := newTestManager()

	var order []string
	ch := &mockChannel{
		sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
			order = append(order, msg.Content)
			return nil
		},
	}

	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	// Send two messages sequentially — they must arrive in order
	_ = m.SendMessage(context.Background(), testOutboundMessage(bus.OutboundMessage{
		Channel: "test", ChatID: "1", Content: "first",
	}))
	_ = m.SendMessage(context.Background(), testOutboundMessage(bus.OutboundMessage{
		Channel: "test", ChatID: "1", Content: "second",
	}))

	if len(order) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(order))
	}
	if order[0] != "first" || order[1] != "second" {
		t.Fatalf("expected [first, second], got %v", order)
	}
}

func TestManager_SendPlaceholder(t *testing.T) {
	mgr := &Manager{
		channels:     make(map[string]Channel),
		workers:      make(map[string]*channelWorker),
		placeholders: sync.Map{},
	}

	mockCh := &mockChannel{
		sendFn: func(ctx context.Context, msg bus.OutboundMessage) error {
			return nil
		},
	}
	mgr.channels["mock"] = mockCh

	ctx := context.Background()

	// SendPlaceholder should send a placeholder and record it
	ok := mgr.SendPlaceholder(ctx, "mock", "chat-1")
	if !ok {
		t.Fatal("expected SendPlaceholder to succeed")
	}
	if mockCh.placeholdersSent != 1 {
		t.Errorf("expected 1 placeholder sent, got %d", mockCh.placeholdersSent)
	}

	key := "mock:chat-1"
	if _, loaded := mgr.placeholders.Load(key); !loaded {
		t.Error("expected placeholder to be recorded in manager")
	}

	// SendPlaceholder on unknown channel should return false
	ok = mgr.SendPlaceholder(ctx, "unknown", "chat-1")
	if ok {
		t.Error("expected SendPlaceholder to fail for unknown channel")
	}
}

func TestFinalizeHookStreamerForwardsTurnUsage(t *testing.T) {
	inner := &turnUsageTrackingStreamer{}
	wrapper := &finalizeHookStreamer{Streamer: inner}

	setter, ok := any(wrapper).(turnUsageStreamer)
	if !ok {
		t.Fatal("finalizeHookStreamer does not satisfy turnUsageStreamer")
	}
	setter.SetTurnUsage(1234, 567)

	if inner.usageCalls != 1 {
		t.Fatalf("inner SetTurnUsage calls = %d, want 1", inner.usageCalls)
	}
	if inner.inputTokens != 1234 || inner.outputTokens != 567 {
		t.Errorf("inner usage = (%d, %d), want (1234, 567)", inner.inputTokens, inner.outputTokens)
	}
}

func TestSplitMarkerStreamerForwardsTurnUsage(t *testing.T) {
	inner := &turnUsageTrackingStreamer{}
	wrapper := &splitMarkerStreamer{current: inner}

	setter, ok := any(wrapper).(turnUsageStreamer)
	if !ok {
		t.Fatal("splitMarkerStreamer does not satisfy turnUsageStreamer")
	}
	setter.SetTurnUsage(1234, 567)

	if inner.usageCalls != 1 {
		t.Fatalf("inner SetTurnUsage calls = %d, want 1", inner.usageCalls)
	}
	if inner.inputTokens != 1234 || inner.outputTokens != 567 {
		t.Errorf("inner usage = (%d, %d), want (1234, 567)", inner.inputTokens, inner.outputTokens)
	}
}
