package channels

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
)

func TestSendWithRetry_Success(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			return nil
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"})

	m.sendWithRetry(ctx, "test", w, msg)

	if callCount != 1 {
		t.Fatalf("expected 1 Send call, got %d", callCount)
	}
}

func TestSendWithRetryPublishesOutboundRuntimeEvents(t *testing.T) {
	eventBus := runtimeevents.NewBus()
	defer func() {
		if err := eventBus.Close(); err != nil {
			t.Errorf("event bus close failed: %v", err)
		}
	}()

	_, eventsCh, err := eventBus.Channel().OfKind(
		runtimeevents.KindChannelMessageOutboundSent,
		runtimeevents.KindChannelMessageOutboundFailed,
	).SubscribeChan(t.Context(), runtimeevents.SubscribeOptions{Name: "channel-outbound", Buffer: 2})
	if err != nil {
		t.Fatalf("SubscribeChan failed: %v", err)
	}

	m := newTestManager()
	m.runtimeEvents = eventBus

	successWorker := &channelWorker{
		ch:      &mockChannel{},
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.sendWithRetry(
		context.Background(),
		"test",
		successWorker,
		testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "chat-1", Content: "hello"}),
	)
	sent := receiveChannelRuntimeEvent(t, eventsCh)
	if sent.Kind != runtimeevents.KindChannelMessageOutboundSent || sent.Scope.ChatID != "chat-1" {
		t.Fatalf("sent event = %+v", sent)
	}
	if sent.Attrs["content_len"] != 5 {
		t.Fatalf("sent attrs = %#v, want content_len", sent.Attrs)
	}

	failWorker := &channelWorker{
		ch: &mockChannel{
			sendFn: func(context.Context, bus.OutboundMessage) error {
				return fmt.Errorf("send failed: %w", ErrSendFailed)
			},
		},
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.sendWithRetry(
		context.Background(),
		"test",
		failWorker,
		testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "chat-2", Content: "hello"}),
	)
	failed := receiveChannelRuntimeEvent(t, eventsCh)
	if failed.Kind != runtimeevents.KindChannelMessageOutboundFailed || failed.Scope.ChatID != "chat-2" {
		t.Fatalf("failed event = %+v", failed)
	}
	if failed.Severity != runtimeevents.SeverityError {
		t.Fatalf("failed severity = %q", failed.Severity)
	}
	if failed.Attrs["error"] == "" || failed.Attrs["retries"] != maxRetries {
		t.Fatalf("failed attrs = %#v, want error and retries", failed.Attrs)
	}
}

func TestSendWithRetry_TemporaryThenSuccess(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			if callCount <= 2 {
				return fmt.Errorf("network error: %w", ErrTemporary)
			}
			return nil
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"})

	m.sendWithRetry(ctx, "test", w, msg)

	if callCount != 3 {
		t.Fatalf("expected 3 Send calls (2 failures + 1 success), got %d", callCount)
	}
}

func TestSendWithRetry_RetriesUseSameLogicalMessageID(t *testing.T) {
	m := newTestManager()
	var messageIDs []string
	ch := &mockChannel{
		sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
			messageIDs = append(messageIDs, msg.ID)
			if len(messageIDs) == 1 {
				return fmt.Errorf("network error: %w", ErrTemporary)
			}
			return nil
		},
	}
	w := &channelWorker{ch: ch, limiter: rate.NewLimiter(rate.Inf, 1)}
	msg := testOutboundMessage(bus.OutboundMessage{
		ID:      "delivery-123",
		Channel: "test",
		ChatID:  "1",
		Content: "hello",
	})

	m.sendWithRetry(context.Background(), "test", w, msg)

	if len(messageIDs) != 2 {
		t.Fatalf("Send calls = %d, want 2", len(messageIDs))
	}
	if messageIDs[0] != "delivery-123" || messageIDs[1] != messageIDs[0] {
		t.Fatalf("message IDs = %q, want identical delivery-123", messageIDs)
	}
}

func TestSendMediaWithRetry_RetriesUseSameLogicalMessageID(t *testing.T) {
	m := newTestManager()
	var messageIDs []string
	ch := &mockMediaChannel{
		sendMediaFn: func(_ context.Context, msg bus.OutboundMediaMessage) ([]string, error) {
			messageIDs = append(messageIDs, msg.ID)
			if len(messageIDs) == 1 {
				return nil, fmt.Errorf("network error: %w", ErrTemporary)
			}
			return nil, nil
		},
	}
	w := &channelWorker{ch: ch, limiter: rate.NewLimiter(rate.Inf, 1)}
	msg := testOutboundMediaMessage(bus.OutboundMediaMessage{
		ID:      "media-delivery-123",
		Channel: "test",
		ChatID:  "1",
		Parts:   []bus.MediaPart{{Ref: "media://abc"}},
	})

	_, _ = m.sendMediaWithRetry(context.Background(), "test", w, msg)

	if len(messageIDs) != 2 {
		t.Fatalf("SendMedia calls = %d, want 2", len(messageIDs))
	}
	if messageIDs[0] != "media-delivery-123" || messageIDs[1] != messageIDs[0] {
		t.Fatalf("message IDs = %q, want identical media-delivery-123", messageIDs)
	}
}

func TestSendWithRetry_PermanentFailure(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			return fmt.Errorf("bad chat ID: %w", ErrSendFailed)
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"})

	m.sendWithRetry(ctx, "test", w, msg)

	if callCount != 1 {
		t.Fatalf("expected 1 Send call (no retry for permanent failure), got %d", callCount)
	}
}

func TestSendWithRetry_NotRunning(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			return ErrNotRunning
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"})

	m.sendWithRetry(ctx, "test", w, msg)

	if callCount != 1 {
		t.Fatalf("expected 1 Send call (no retry for ErrNotRunning), got %d", callCount)
	}
}

func TestSendWithRetry_RateLimitRetry(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			if callCount == 1 {
				return fmt.Errorf("429: %w", ErrRateLimit)
			}
			return nil
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"})

	start := time.Now()
	m.sendWithRetry(ctx, "test", w, msg)
	elapsed := time.Since(start)

	if callCount != 2 {
		t.Fatalf("expected 2 Send calls (1 rate limit + 1 success), got %d", callCount)
	}
	// Should have waited at least rateLimitDelay (1s) but allow some slack
	if elapsed < 900*time.Millisecond {
		t.Fatalf("expected at least ~1s delay for rate limit retry, got %v", elapsed)
	}
}

func TestSendWithRetry_MaxRetriesExhausted(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			return fmt.Errorf("timeout: %w", ErrTemporary)
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"})

	m.sendWithRetry(ctx, "test", w, msg)

	expected := maxRetries + 1 // initial attempt + maxRetries retries
	if callCount != expected {
		t.Fatalf("expected %d Send calls, got %d", expected, callCount)
	}
}

func TestSendMedia_Success(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockMediaChannel{
		sendMediaFn: func(_ context.Context, _ bus.OutboundMediaMessage) ([]string, error) {
			callCount++
			return nil, nil
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	err := m.SendMedia(context.Background(), testOutboundMediaMessage(bus.OutboundMediaMessage{
		Channel: "test",
		ChatID:  "chat1",
		Parts:   []bus.MediaPart{{Ref: "media://abc"}},
	}))
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 SendMedia call, got %d", callCount)
	}
}

func TestSendMedia_PropagatesFailure(t *testing.T) {
	m := newTestManager()
	ch := &mockMediaChannel{
		sendMediaFn: func(_ context.Context, _ bus.OutboundMediaMessage) ([]string, error) {
			return nil, fmt.Errorf("bad upload: %w", ErrSendFailed)
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	err := m.SendMedia(context.Background(), testOutboundMediaMessage(bus.OutboundMediaMessage{
		Channel: "test",
		ChatID:  "chat1",
		Parts:   []bus.MediaPart{{Ref: "media://abc"}},
	}))
	if err == nil {
		t.Fatal("expected SendMedia to return error")
	}
	if !errors.Is(err, ErrSendFailed) {
		t.Fatalf("expected ErrSendFailed, got %v", err)
	}
}

func TestSendMedia_UnsupportedChannelReturnsError(t *testing.T) {
	m := newTestManager()
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			return nil
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w

	err := m.SendMedia(context.Background(), testOutboundMediaMessage(bus.OutboundMediaMessage{
		Channel: "test",
		ChatID:  "chat1",
		Parts:   []bus.MediaPart{{Ref: "media://abc"}},
	}))
	if err == nil {
		t.Fatal("expected SendMedia to return error for unsupported channel")
	}
	if !strings.Contains(err.Error(), "does not support media sending") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendMedia_DeletesPlaceholderBeforeSending(t *testing.T) {
	m := newTestManager()
	ch := &mockDeletingMediaChannel{
		mockMediaChannel: mockMediaChannel{
			sendMediaFn: func(_ context.Context, _ bus.OutboundMediaMessage) ([]string, error) {
				return nil, nil
			},
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}
	m.channels["test"] = ch
	m.workers["test"] = w
	m.RecordPlaceholder("test", "chat1", "placeholder-1")

	err := m.SendMedia(context.Background(), testOutboundMediaMessage(bus.OutboundMediaMessage{
		Channel: "test",
		ChatID:  "chat1",
		Parts:   []bus.MediaPart{{Ref: "media://abc"}},
	}))
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}
	if ch.deleteCalls != 1 {
		t.Fatalf("expected placeholder delete to be called once, got %d", ch.deleteCalls)
	}
	if ch.lastDeleted.chatID != "chat1" || ch.lastDeleted.messageID != "placeholder-1" {
		t.Fatalf("unexpected placeholder deletion target: %+v", ch.lastDeleted)
	}
	if len(ch.sentMediaMessages) != 1 {
		t.Fatalf("expected media to be sent once, got %d", len(ch.sentMediaMessages))
	}
}

func TestSendWithRetry_UnknownErrorIsNotRetried(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			if callCount == 1 {
				return errors.New("random unexpected error")
			}
			return nil
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"})

	m.sendWithRetry(ctx, "test", w, msg)

	if callCount != 1 {
		t.Fatalf("expected 1 Send call (unknown delivery state), got %d", callCount)
	}
}

func TestSendWithRetry_ContextCancelled(t *testing.T) {
	m := newTestManager()
	var callCount int
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callCount++
			return fmt.Errorf("timeout: %w", ErrTemporary)
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())
	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"})

	// Cancel context after first Send attempt returns
	ch.sendFn = func(_ context.Context, _ bus.OutboundMessage) error {
		callCount++
		cancel()
		return fmt.Errorf("timeout: %w", ErrTemporary)
	}

	m.sendWithRetry(ctx, "test", w, msg)

	// Should have called Send once, then noticed ctx canceled during backoff
	if callCount != 1 {
		t.Fatalf("expected 1 Send call before context cancellation, got %d", callCount)
	}
}

func TestWorkerRateLimiter(t *testing.T) {
	m := newTestManager()

	var mu sync.Mutex
	var sendTimes []time.Time

	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			mu.Lock()
			sendTimes = append(sendTimes, time.Now())
			mu.Unlock()
			return nil
		},
	}

	// Create a worker with a low rate: 2 msg/s, burst 1
	w := &channelWorker{
		ch:      ch,
		queue:   make(chan bus.OutboundMessage, 10),
		done:    make(chan struct{}),
		limiter: rate.NewLimiter(2, 1),
	}

	ctx := t.Context()

	go m.runWorker(ctx, "test", w)

	// Enqueue 4 messages
	for i := range 4 {
		w.queue <- testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "1", Content: fmt.Sprintf("msg%d", i)})
	}

	// Wait enough time for all messages to be sent (4 msgs at 2/s = ~2s, give extra margin)
	time.Sleep(3 * time.Second)

	mu.Lock()
	times := make([]time.Time, len(sendTimes))
	copy(times, sendTimes)
	mu.Unlock()

	if len(times) != 4 {
		t.Fatalf("expected 4 sends, got %d", len(times))
	}

	// Verify rate limiting: total duration should be at least 1s
	// (first message immediate, then ~500ms between each subsequent one at 2/s)
	totalDuration := times[len(times)-1].Sub(times[0])
	if totalDuration < 1*time.Second {
		t.Fatalf("expected total duration >= 1s for 4 msgs at 2/s rate, got %v", totalDuration)
	}
}

func TestNewChannelWorker_DefaultRate(t *testing.T) {
	ch := &mockChannel{}
	w := newChannelWorker("unknown_channel", ch, "unknown_channel")

	if w.limiter == nil {
		t.Fatal("expected limiter to be non-nil")
	}
	if w.limiter.Limit() != rate.Limit(defaultRateLimit) {
		t.Fatalf("expected rate limit %v, got %v", rate.Limit(defaultRateLimit), w.limiter.Limit())
	}
}

func TestNewChannelWorker_ConfiguredRate(t *testing.T) {
	ch := &mockChannel{}

	for channelType, expectedRate := range channelRateConfig {
		w := newChannelWorker(channelType, ch, channelType)
		if w.limiter.Limit() != rate.Limit(expectedRate) {
			t.Fatalf("channel %s: expected rate %v, got %v", channelType, expectedRate, w.limiter.Limit())
		}
	}
}

func TestRunWorker_MessageSplitting(t *testing.T) {
	m := newTestManager()

	var mu sync.Mutex
	var received []string

	ch := &mockChannelWithLength{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
				mu.Lock()
				received = append(received, msg.Content)
				mu.Unlock()
				return nil
			},
		},
		maxLen: 5,
	}

	w := &channelWorker{
		ch:      ch,
		queue:   make(chan bus.OutboundMessage, 10),
		done:    make(chan struct{}),
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := t.Context()

	go m.runWorker(ctx, "test", w)

	// Send a message that should be split
	w.queue <- testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello world"})

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	count := len(received)
	mu.Unlock()

	if count < 2 {
		t.Fatalf("expected message to be split into at least 2 chunks, got %d", count)
	}
}

func TestSendWithRetry_ExponentialBackoff(t *testing.T) {
	m := newTestManager()

	var callTimes []time.Time
	var callCount atomic.Int32
	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			callTimes = append(callTimes, time.Now())
			callCount.Add(1)
			return fmt.Errorf("timeout: %w", ErrTemporary)
		},
	}
	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx := context.Background()
	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "1", Content: "hello"})

	start := time.Now()
	m.sendWithRetry(ctx, "test", w, msg)
	totalElapsed := time.Since(start)

	// With maxRetries=3: attempts at 0, ~500ms, ~1.5s, ~3.5s
	// Total backoff: 500ms + 1s + 2s = 3.5s
	// Allow some margin
	if totalElapsed < 3*time.Second {
		t.Fatalf("expected total elapsed >= 3s for exponential backoff, got %v", totalElapsed)
	}

	if int(callCount.Load()) != maxRetries+1 {
		t.Fatalf("expected %d calls, got %d", maxRetries+1, callCount.Load())
	}
}

func TestSendWithRetry_ToolCallsPlaceholderDeleteAndFallsThroughToSend(t *testing.T) {
	m := newTestManager()

	ch := &mockDeletingMessageEditor{
		mockMessageEditor: mockMessageEditor{
			mockChannel: mockChannel{
				sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
					if got := msg.Context.Raw["message_kind"]; got != "tool_calls" {
						t.Fatalf("expected tool_calls message kind, got %q", got)
					}
					if msg.Content != "" {
						t.Fatalf("expected empty tool_calls content, got %q", msg.Content)
					}
					return nil
				},
			},
			editFn: func(_ context.Context, _, _, _ string) error {
				t.Fatal("expected tool_calls message to bypass placeholder edit")
				return nil
			},
		},
	}

	m.RecordPlaceholder("test", "123", "456")

	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"message_kind": "tool_calls",
				"tool_calls":   `[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{}"},"extra_content":{"tool_feedback_explanation":"Looking up config"}}]`,
			},
		},
	})

	m.sendWithRetry(context.Background(), "test", w, msg)

	if ch.deleteCalls != 1 {
		t.Fatalf("expected placeholder deletion, got %d delete calls", ch.deleteCalls)
	}
	if ch.deletedChatID != "123" || ch.deletedMessageID != "456" {
		t.Fatalf("unexpected placeholder deletion target: %s/%s", ch.deletedChatID, ch.deletedMessageID)
	}
	if len(ch.sentMessages) != 1 {
		t.Fatalf("expected structured tool_calls message to be sent once, got %d", len(ch.sentMessages))
	}
}

func TestRunWorker_ToolFeedbackSkipsMarkerSplitting(t *testing.T) {
	m := newTestManager()
	m.config = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				SplitOnMarker: true,
			},
		},
	}

	var (
		mu       sync.Mutex
		received []string
	)
	ch := &mockChannelWithLength{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
				mu.Lock()
				received = append(received, msg.Content)
				mu.Unlock()
				return nil
			},
		},
		maxLen: 200,
	}

	w := &channelWorker{
		ch:      ch,
		queue:   make(chan bus.OutboundMessage, 1),
		done:    make(chan struct{}),
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.runWorker(ctx, "test", w)

	content := "🔧 `read_file`\nRead current config first.<|[SPLIT]|>Then update the example."
	w.queue <- testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: content,
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"message_kind": "tool_feedback",
			},
		},
	})

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("len(received) = %d, want 1", len(received))
	}
	if received[0] != content {
		t.Fatalf("received[0] = %q, want %q", received[0], content)
	}
}

func TestRunWorker_FinalizedStreamSuppressesMarkerSplitBeforeSending(t *testing.T) {
	m := newTestManager()
	m.config = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				SplitOnMarker: true,
			},
		},
	}

	var (
		mu       sync.Mutex
		received []string
	)
	ch := &mockChannel{
		sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
			mu.Lock()
			received = append(received, msg.Content)
			mu.Unlock()
			return nil
		},
	}

	w := &channelWorker{
		ch:      ch,
		queue:   make(chan bus.OutboundMessage, 1),
		done:    make(chan struct{}),
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.runWorker(ctx, "test", w)

	streamKey := streamSuppressionKey("test", "123", "session-1")
	m.streamActive.Store(streamKey, true)
	w.queue <- testOutboundMessage(bus.OutboundMessage{
		Channel:    "test",
		ChatID:     "123",
		SessionKey: "session-1",
		Content:    "streamed full reply<|[SPLIT]|>duplicate chunk",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"outbound_kind": "final",
			},
		},
	})

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 0 {
		t.Fatalf("received split duplicate messages = %v, want none", received)
	}
	if _, ok := m.streamActive.Load(streamKey); ok {
		t.Fatal("expected finalized stream marker to be consumed")
	}
}

func TestSendWithRetry_PreSendEditsPlaceholder(t *testing.T) {
	m := newTestManager()
	var sendCalled bool

	ch := &mockMessageEditor{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
				sendCalled = true
				return nil
			},
		},
		editFn: func(_ context.Context, _, _, _ string) error {
			return nil // edit succeeds
		},
	}

	m.RecordPlaceholder("test", "123", "456")

	w := &channelWorker{
		ch:      ch,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "123", Content: "hello"})
	m.sendWithRetry(context.Background(), "test", w, msg)

	if sendCalled {
		t.Fatal("expected Send to NOT be called when placeholder was edited")
	}
}
