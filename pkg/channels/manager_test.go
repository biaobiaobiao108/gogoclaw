package channels

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
)

// mockChannel is a test double that delegates Send to a configurable function.
type mockChannel struct {
	BaseChannel
	sendFn            func(ctx context.Context, msg bus.OutboundMessage) error
	startFn           func(ctx context.Context) error
	stopFn            func(ctx context.Context) error
	sentMessages      []bus.OutboundMessage
	placeholdersSent  int
	editedMessages    int
	lastPlaceholderID string
}

func (m *mockChannel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	m.sentMessages = append(m.sentMessages, msg)
	if m.sendFn == nil {
		return nil, nil
	}
	return nil, m.sendFn(ctx, msg)
}

func (m *mockChannel) Start(ctx context.Context) error {
	if m.startFn != nil {
		return m.startFn(ctx)
	}
	return nil
}

func (m *mockChannel) Stop(ctx context.Context) error {
	if m.stopFn != nil {
		return m.stopFn(ctx)
	}
	return nil
}

func (m *mockChannel) SendPlaceholder(ctx context.Context, chatID string) (string, error) {
	m.placeholdersSent++
	m.lastPlaceholderID = "mock-ph-123"
	return m.lastPlaceholderID, nil
}

func (m *mockChannel) EditMessage(ctx context.Context, chatID, messageID, content string) error {
	m.editedMessages++
	return nil
}

type mockMediaChannel struct {
	mockChannel
	sendMediaFn       func(ctx context.Context, msg bus.OutboundMediaMessage) ([]string, error)
	sentMediaMessages []bus.OutboundMediaMessage
}

func (m *mockMediaChannel) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) ([]string, error) {
	m.sentMediaMessages = append(m.sentMediaMessages, msg)
	if m.sendMediaFn != nil {
		return m.sendMediaFn(ctx, msg)
	}
	return nil, nil
}

type mockDeletingMediaChannel struct {
	mockMediaChannel
	deleteCalls     int
	dismissedChatID string
	lastDeleted     struct {
		chatID    string
		messageID string
	}
}

func (m *mockDeletingMediaChannel) DeleteMessage(
	_ context.Context,
	chatID string,
	messageID string,
) error {
	m.deleteCalls++
	m.lastDeleted.chatID = chatID
	m.lastDeleted.messageID = messageID
	return nil
}

func (m *mockDeletingMediaChannel) DismissToolFeedbackMessage(_ context.Context, chatID string) {
	m.dismissedChatID = chatID
}

type mockStreamer struct {
	finalizeFn            func(context.Context, string) error
	finalizeWithContextFn func(context.Context, string, *bus.ContextUsage) error
}

func (m *mockStreamer) Update(context.Context, string) error { return nil }

func (m *mockStreamer) Finalize(ctx context.Context, content string) error {
	if m.finalizeFn != nil {
		return m.finalizeFn(ctx, content)
	}
	return nil
}

func (m *mockStreamer) FinalizeWithContext(ctx context.Context, content string, usage *bus.ContextUsage) error {
	if m.finalizeWithContextFn != nil {
		return m.finalizeWithContextFn(ctx, content, usage)
	}
	return m.Finalize(ctx, content)
}

func (m *mockStreamer) Cancel(context.Context) {}

type mockReasoningStreamer struct {
	mockStreamer
	reasoningUpdates []string
	reasoningFinal   string
}

func (m *mockReasoningStreamer) UpdateReasoning(_ context.Context, content string) error {
	m.reasoningUpdates = append(m.reasoningUpdates, content)
	return nil
}

func (m *mockReasoningStreamer) FinalizeReasoning(_ context.Context, content string) error {
	m.reasoningFinal = content
	return nil
}

type modelTrackingReasoningStreamer struct {
	mockReasoningStreamer
	modelNames []string
}

func (m *modelTrackingReasoningStreamer) SetModelName(modelName string) {
	m.modelNames = append(m.modelNames, strings.TrimSpace(modelName))
}

type recordingStreamSegment struct {
	updates       []string
	finals        []string
	finalUsage    *bus.ContextUsage
	canceledCount int
	modelNames    []string
}

func (s *recordingStreamSegment) Update(_ context.Context, content string) error {
	s.updates = append(s.updates, content)
	return nil
}

func (s *recordingStreamSegment) Finalize(ctx context.Context, content string) error {
	return s.FinalizeWithContext(ctx, content, nil)
}

func (s *recordingStreamSegment) FinalizeWithContext(_ context.Context, content string, usage *bus.ContextUsage) error {
	s.finals = append(s.finals, content)
	s.finalUsage = usage
	return nil
}

func (s *recordingStreamSegment) Cancel(context.Context) {
	s.canceledCount++
}

func (s *recordingStreamSegment) SetModelName(modelName string) {
	s.modelNames = append(s.modelNames, strings.TrimSpace(modelName))
}

type mockStreamingChannel struct {
	mockMessageEditor
	streamer        Streamer
	beginStreamFn   func(context.Context, string) (Streamer, error)
	resolveChatIDFn func(chatID string, outboundCtx *bus.InboundContext) string
}

func (m *mockStreamingChannel) BeginStream(ctx context.Context, chatID string) (Streamer, error) {
	if m.beginStreamFn != nil {
		return m.beginStreamFn(ctx, chatID)
	}
	if m.streamer == nil {
		return nil, errors.New("missing streamer")
	}
	return m.streamer, nil
}

func (m *mockStreamingChannel) ToolFeedbackMessageChatID(
	chatID string,
	outboundCtx *bus.InboundContext,
) string {
	if m.resolveChatIDFn != nil {
		return m.resolveChatIDFn(chatID, outboundCtx)
	}
	return chatID
}

// newTestManager creates a minimal Manager suitable for unit tests.
func newTestManager() *Manager {
	return &Manager{
		channels: make(map[string]Channel),
		workers:  make(map[string]*channelWorker),
		bus:      bus.NewMessageBus(),
	}
}

func testOutboundMessage(msg bus.OutboundMessage) bus.OutboundMessage {
	if msg.Context.Channel == "" && msg.Context.ChatID == "" {
		msg.Context = bus.NewOutboundContext(msg.Channel, msg.ChatID, msg.ReplyToMessageID)
	}
	return bus.NormalizeOutboundMessage(msg)
}

func testOutboundMediaMessage(msg bus.OutboundMediaMessage) bus.OutboundMediaMessage {
	if msg.Context.Channel == "" && msg.Context.ChatID == "" {
		msg.Context = bus.NewOutboundContext(msg.Channel, msg.ChatID, "")
	}
	return bus.NormalizeOutboundMediaMessage(msg)
}

func receiveChannelRuntimeEvent(t *testing.T, ch <-chan runtimeevents.Event) runtimeevents.Event {
	t.Helper()

	select {
	case evt, ok := <-ch:
		if !ok {
			t.Fatal("runtime event channel closed before expected event")
		}
		return evt
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runtime event")
		return runtimeevents.Event{}
	}
}

// mockChannelWithLength implements MessageLengthProvider.
type mockChannelWithLength struct {
	mockChannel
	maxLen int
}

func (m *mockChannelWithLength) MaxMessageLength() int {
	return m.maxLen
}

// --- Phase 10: preSend orchestration tests ---

// mockMessageEditor is a channel that supports MessageEditor.
type mockMessageEditor struct {
	mockChannel
	editFn            func(ctx context.Context, chatID, messageID, content string) error
	finalizeFn        func(ctx context.Context, msg bus.OutboundMessage) ([]string, bool)
	finalizeCalled    bool
	recordedChatID    string
	recordedMessageID string
	recordedContent   string
	clearedChatID     string
	dismissedChatID   string
}

func (m *mockMessageEditor) EditMessage(ctx context.Context, chatID, messageID, content string) error {
	return m.editFn(ctx, chatID, messageID, content)
}

func (m *mockMessageEditor) RecordToolFeedbackMessage(chatID, messageID, content string) {
	m.recordedChatID = chatID
	m.recordedMessageID = messageID
	m.recordedContent = content
}

func (m *mockMessageEditor) ClearToolFeedbackMessage(chatID string) {
	m.clearedChatID = chatID
}

func (m *mockMessageEditor) DismissToolFeedbackMessage(_ context.Context, chatID string) {
	m.dismissedChatID = chatID
}

func (m *mockMessageEditor) FinalizeToolFeedbackMessage(
	ctx context.Context,
	msg bus.OutboundMessage,
) ([]string, bool) {
	m.finalizeCalled = true
	if m.finalizeFn == nil {
		return nil, false
	}
	return m.finalizeFn(ctx, msg)
}

type mockResolvedToolFeedbackEditor struct {
	mockMessageEditor
	resolveChatIDFn func(chatID string, outboundCtx *bus.InboundContext) string
}

type mockDeletingMessageEditor struct {
	mockMessageEditor
	deleteCalls      int
	deletedChatID    string
	deletedMessageID string
}

func (m *mockDeletingMessageEditor) DeleteMessage(_ context.Context, chatID, messageID string) error {
	m.deleteCalls++
	m.deletedChatID = chatID
	m.deletedMessageID = messageID
	return nil
}

func (m *mockResolvedToolFeedbackEditor) ToolFeedbackMessageChatID(
	chatID string,
	outboundCtx *bus.InboundContext,
) string {
	if m.resolveChatIDFn != nil {
		return m.resolveChatIDFn(chatID, outboundCtx)
	}
	return chatID
}

type mockPreparedToolFeedbackEditor struct {
	mockMessageEditor
	prepareFn func(content string) string
}

func (m *mockPreparedToolFeedbackEditor) PrepareToolFeedbackMessageContent(content string) string {
	if m.prepareFn != nil {
		return m.prepareFn(content)
	}
	return content
}

// turnUsageTrackingStreamer is a mockStreamer that records SetTurnUsage calls,
// used to verify the manager's streamer wrappers forward per-turn token usage
// to the inner streamer (regression: the wrappers previously dropped it because
// SetTurnUsage is not part of the bus.Streamer interface).
type turnUsageTrackingStreamer struct {
	mockStreamer
	inputTokens  int
	outputTokens int
	usageCalls   int
}

func (m *turnUsageTrackingStreamer) SetTurnUsage(inputTokens, outputTokens int) {
	m.usageCalls++
	m.inputTokens = inputTokens
	m.outputTokens = outputTokens
}
