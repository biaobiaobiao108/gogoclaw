package channels

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

func TestPreSend_PlaceholderEditSuccess(t *testing.T) {
	m := newTestManager()
	var sendCalled bool
	var editCalled bool

	ch := &mockMessageEditor{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
				sendCalled = true
				return nil
			},
		},
		editFn: func(_ context.Context, chatID, messageID, content string) error {
			editCalled = true
			if chatID != "123" {
				t.Fatalf("expected chatID 123, got %s", chatID)
			}
			if messageID != "456" {
				t.Fatalf("expected messageID 456, got %s", messageID)
			}
			if content != "hello" {
				t.Fatalf("expected content 'hello', got %s", content)
			}
			return nil
		},
	}

	// Register placeholder
	m.RecordPlaceholder("test", "123", "456")

	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "123", Content: "hello"})
	_, edited := m.preSend(context.Background(), "test", msg, ch)

	if !edited {
		t.Fatal("expected preSend to return true (placeholder edited)")
	}
	if !editCalled {
		t.Fatal("expected EditMessage to be called")
	}
	if sendCalled {
		t.Fatal("expected Send to NOT be called when placeholder edited")
	}
}

func TestPreSend_ToolFeedbackPlaceholderEditRecordsTrackedMessage(t *testing.T) {
	m := newTestManager()

	ch := &mockMessageEditor{
		editFn: func(_ context.Context, chatID, messageID, content string) error {
			if chatID != "123" || messageID != "456" || content != "hello" {
				t.Fatalf("unexpected edit args: %s %s %s", chatID, messageID, content)
			}
			return nil
		},
	}

	m.RecordPlaceholder("test", "123", "456")

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "hello",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"message_kind": "tool_feedback",
			},
		},
	})
	_, edited := m.preSend(context.Background(), "test", msg, ch)
	if !edited {
		t.Fatal("expected preSend to edit placeholder")
	}
	if ch.recordedChatID != "123" || ch.recordedMessageID != "456" {
		t.Fatalf("expected tracked message 123/456, got %q/%q", ch.recordedChatID, ch.recordedMessageID)
	}
}

func TestPreSend_ToolFeedbackPlaceholderEditUsesResolvedTrackedChatID(t *testing.T) {
	m := newTestManager()

	ch := &mockResolvedToolFeedbackEditor{
		mockMessageEditor: mockMessageEditor{
			editFn: func(_ context.Context, chatID, messageID, content string) error {
				if chatID != "-100123" || messageID != "456" || content != "hello" {
					t.Fatalf("unexpected edit args: %s %s %s", chatID, messageID, content)
				}
				return nil
			},
		},
		resolveChatIDFn: func(chatID string, outboundCtx *bus.InboundContext) string {
			if chatID != "-100123" {
				t.Fatalf("expected raw chat ID, got %q", chatID)
			}
			if outboundCtx == nil || outboundCtx.TopicID != "42" {
				t.Fatalf("expected topic-aware outbound context, got %+v", outboundCtx)
			}
			return chatID + "/" + outboundCtx.TopicID
		},
	}

	m.RecordPlaceholder("test", "-100123", "456")

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "-100123",
		Content: "hello",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "-100123",
			TopicID: "42",
			Raw: map[string]string{
				"message_kind": "tool_feedback",
			},
		},
	})
	_, edited := m.preSend(context.Background(), "test", msg, ch)
	if !edited {
		t.Fatal("expected preSend to edit placeholder")
	}
	if ch.recordedChatID != "-100123/42" || ch.recordedMessageID != "456" {
		t.Fatalf("expected resolved tracked message -100123/42/456, got %q/%q",
			ch.recordedChatID, ch.recordedMessageID)
	}
}

func TestPreSend_ToolFeedbackPlaceholderEditUsesPreparedContent(t *testing.T) {
	m := newTestManager()

	const rawContent = "🔧 `read_file`\n" + "<raw>"
	const preparedContent = "🔧 `read_file`\n&lt;raw&gt;"

	ch := &mockPreparedToolFeedbackEditor{
		mockMessageEditor: mockMessageEditor{
			editFn: func(_ context.Context, chatID, messageID, content string) error {
				if chatID != "123" || messageID != "456" {
					t.Fatalf("unexpected edit target: %s/%s", chatID, messageID)
				}
				if content != InitialAnimatedToolFeedbackContent(preparedContent) {
					t.Fatalf("unexpected prepared content: %q", content)
				}
				return nil
			},
		},
		prepareFn: func(content string) string {
			if content != rawContent {
				t.Fatalf("unexpected raw tool feedback: %q", content)
			}
			return preparedContent
		},
	}

	m.RecordPlaceholder("test", "123", "456")

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: rawContent,
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"message_kind": "tool_feedback",
			},
		},
	})

	_, edited := m.preSend(context.Background(), "test", msg, ch)
	if !edited {
		t.Fatal("expected preSend to edit placeholder")
	}
	if ch.recordedContent != preparedContent {
		t.Fatalf("expected tracked content %q, got %q", preparedContent, ch.recordedContent)
	}
}

func TestPreSend_NonToolFeedbackLeavesTrackedMessageForChannelSend(t *testing.T) {
	m := newTestManager()
	ch := &mockMessageEditor{}

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "final reply",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
		},
	})

	_, edited := m.preSend(context.Background(), "test", msg, ch)
	if edited {
		t.Fatal("expected preSend to fall through when no placeholder exists")
	}
	if ch.dismissedChatID != "" {
		t.Fatalf("expected tracked tool feedback cleanup to be deferred to channel send, got %q", ch.dismissedChatID)
	}
}

func TestPreSend_NonToolFeedbackDefersTrackedMessageFinalizationToChannelSend(t *testing.T) {
	m := newTestManager()
	ch := &mockMessageEditor{
		finalizeFn: func(_ context.Context, msg bus.OutboundMessage) ([]string, bool) {
			if msg.ChatID != "123" || msg.Content != "final reply" {
				t.Fatalf("unexpected finalize msg: %+v", msg)
			}
			return []string{"tool-msg-1"}, true
		},
	}

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "final reply",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
		},
	})

	msgIDs, handled := m.preSend(context.Background(), "test", msg, ch)
	if handled {
		t.Fatalf("expected preSend to defer to channel Send, got msgIDs=%v", msgIDs)
	}
	if len(msgIDs) != 0 {
		t.Fatalf("expected no msgIDs from preSend, got %v", msgIDs)
	}
	if ch.dismissedChatID != "" {
		t.Fatalf("expected tracked cleanup to remain in channel Send, got %q", ch.dismissedChatID)
	}
	if ch.finalizeCalled {
		t.Fatal("expected preSend to skip channel tool feedback finalization")
	}
}

func TestPreSend_ToolFeedbackSeparateMessagesDeletesPlaceholderAndSkipsEdit(t *testing.T) {
	m := newTestManager()
	m.config = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				ToolFeedback: config.ToolFeedbackConfig{
					Enabled:          true,
					SeparateMessages: true,
				},
			},
		},
	}

	ch := &mockDeletingMessageEditor{
		mockMessageEditor: mockMessageEditor{
			editFn: func(_ context.Context, _, _, _ string) error {
				t.Fatal("expected placeholder edit to be skipped in separate message mode")
				return nil
			},
		},
	}

	m.RecordPlaceholder("test", "123", "456")

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "hello",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"message_kind": "tool_feedback",
			},
		},
	})

	msgIDs, handled := m.preSend(context.Background(), "test", msg, ch)
	if handled {
		t.Fatalf("expected preSend to fall through so the channel can send a new message, got %v", msgIDs)
	}
	if ch.deleteCalls != 1 {
		t.Fatalf("expected placeholder deletion, got %d delete calls", ch.deleteCalls)
	}
	if ch.deletedChatID != "123" || ch.deletedMessageID != "456" {
		t.Fatalf("unexpected placeholder deletion target: %s/%s", ch.deletedChatID, ch.deletedMessageID)
	}
	if ch.recordedMessageID != "" {
		t.Fatalf("expected no tracked placeholder record, got %q", ch.recordedMessageID)
	}
	if ch.clearedChatID != "123" {
		t.Fatalf("expected tracked tool feedback state to be cleared before sending, got %q", ch.clearedChatID)
	}
}

func TestPreSend_ThoughtPlaceholderDeleteAndSkipsEdit(t *testing.T) {
	m := newTestManager()

	ch := &mockDeletingMessageEditor{
		mockMessageEditor: mockMessageEditor{
			editFn: func(_ context.Context, _, _, _ string) error {
				t.Fatal("expected thought message to bypass placeholder edit")
				return nil
			},
		},
	}

	m.RecordPlaceholder("test", "123", "456")

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "thinking trace",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"message_kind": "thought",
			},
		},
	})

	msgIDs, handled := m.preSend(context.Background(), "test", msg, ch)
	if handled {
		t.Fatalf(
			"expected thought message to fall through so the channel can send a structured message, got %v",
			msgIDs,
		)
	}
	if ch.deleteCalls != 1 {
		t.Fatalf("expected placeholder deletion, got %d delete calls", ch.deleteCalls)
	}
	if ch.deletedChatID != "123" || ch.deletedMessageID != "456" {
		t.Fatalf("unexpected placeholder deletion target: %s/%s", ch.deletedChatID, ch.deletedMessageID)
	}
	if _, ok := m.placeholders.Load("test:123"); ok {
		t.Fatal("expected placeholder to be consumed before structured thought send")
	}
}

func TestPreSend_NonToolFeedbackSeparateMessagesClearsTrackedMessageWithoutDismiss(t *testing.T) {
	m := newTestManager()
	m.config = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				ToolFeedback: config.ToolFeedbackConfig{
					Enabled:          true,
					SeparateMessages: true,
				},
			},
		},
	}

	ch := &mockMessageEditor{}

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "final reply",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
		},
	})

	_, handled := m.preSend(context.Background(), "test", msg, ch)
	if handled {
		t.Fatal("expected preSend to leave final delivery to the channel")
	}
	if ch.clearedChatID != "123" {
		t.Fatalf("expected tracked tool feedback state to be cleared, got %q", ch.clearedChatID)
	}
	if ch.dismissedChatID != "" {
		t.Fatalf("expected tracked tool feedback message to be preserved, got dismissal for %q", ch.dismissedChatID)
	}
	if ch.finalizeCalled {
		t.Fatal("expected separate message mode to skip in-place finalization")
	}
}

func TestPreSend_StaleToolFeedbackDoesNotConsumeStreamActiveMarker(t *testing.T) {
	m := newTestManager()
	m.streamActive.Store("test:123", true)
	m.RecordPlaceholder("test", "123", "placeholder-1")

	var editedContent string
	ch := &mockMessageEditor{
		editFn: func(_ context.Context, chatID, messageID, content string) error {
			if chatID != "123" || messageID != "placeholder-1" {
				t.Fatalf("unexpected edit target: %s/%s", chatID, messageID)
			}
			editedContent = content
			return nil
		},
	}

	toolFeedback := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "🔧 `read_file`\nReading config",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"message_kind": "tool_feedback",
			},
		},
	})

	msgIDs, handled := m.preSend(context.Background(), "test", toolFeedback, ch)
	if !handled {
		t.Fatal("expected stale tool feedback to be dropped after stream finalize")
	}
	if len(msgIDs) != 0 {
		t.Fatalf("expected no delivered message IDs for stale feedback, got %v", msgIDs)
	}
	if _, ok := m.streamActive.Load("test:123"); !ok {
		t.Fatal("expected streamActive marker to remain for the final outbound message")
	}
	if _, ok := m.placeholders.Load("test:123"); !ok {
		t.Fatal("expected placeholder cleanup to remain deferred to the final outbound message")
	}
	if ch.editedMessages != 0 {
		t.Fatalf("expected no placeholder edit for stale feedback, got %d edits", ch.editedMessages)
	}

	finalMsg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "final streamed reply",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"outbound_kind": "final",
			},
		},
	})

	_, handled = m.preSend(context.Background(), "test", finalMsg, ch)
	if !handled {
		t.Fatal("expected final outbound message to consume streamActive marker")
	}
	if _, ok := m.streamActive.Load("test:123"); ok {
		t.Fatal("expected streamActive marker to be cleared by final outbound message")
	}
	if _, ok := m.placeholders.Load("test:123"); ok {
		t.Fatal("expected placeholder to be cleaned up by final outbound message")
	}
	if editedContent != "final streamed reply" {
		t.Fatalf("editedContent = %q, want final streamed reply", editedContent)
	}
}

func TestPreSend_StaleThoughtDoesNotConsumeStreamActiveMarker(t *testing.T) {
	m := newTestManager()
	m.streamActive.Store("test:123", true)
	m.streamAuxiliaryTombstones.Store("test:123", time.Now())
	m.RecordPlaceholder("test", "123", "placeholder-1")

	var editedContent string
	ch := &mockMessageEditor{
		editFn: func(_ context.Context, chatID, messageID, content string) error {
			if chatID != "123" || messageID != "placeholder-1" {
				t.Fatalf("unexpected edit target: %s/%s", chatID, messageID)
			}
			editedContent = content
			return nil
		},
	}

	thought := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "late reasoning",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"message_kind": "thought",
			},
		},
	})

	msgIDs, handled := m.preSend(context.Background(), "test", thought, ch)
	if !handled {
		t.Fatal("expected stale thought to be dropped after stream finalize")
	}
	if len(msgIDs) != 0 {
		t.Fatalf("expected no delivered message IDs for stale thought, got %v", msgIDs)
	}
	if _, ok := m.streamActive.Load("test:123"); !ok {
		t.Fatal("expected streamActive marker to remain for the final outbound message")
	}
	if _, ok := m.placeholders.Load("test:123"); !ok {
		t.Fatal("expected placeholder cleanup to remain deferred to the final outbound message")
	}
	if ch.editedMessages != 0 {
		t.Fatalf("expected no placeholder edit for stale thought, got %d edits", ch.editedMessages)
	}

	finalMsg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "final streamed reply",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"outbound_kind": "final",
			},
		},
	})

	_, handled = m.preSend(context.Background(), "test", finalMsg, ch)
	if !handled {
		t.Fatal("expected final outbound message to consume streamActive marker")
	}
	if _, ok := m.streamActive.Load("test:123"); ok {
		t.Fatal("expected streamActive marker to be cleared by final outbound message")
	}
	if _, ok := m.placeholders.Load("test:123"); ok {
		t.Fatal("expected placeholder to be cleaned up by final outbound message")
	}
	if editedContent != "final streamed reply" {
		t.Fatalf("editedContent = %q, want final streamed reply", editedContent)
	}

	lateThought := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "later reasoning",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"message_kind": "thought",
			},
		},
	})
	msgIDs, handled = m.preSend(context.Background(), "test", lateThought, ch)
	if !handled {
		t.Fatal("expected tombstone to drop late thought after final outbound was suppressed")
	}
	if len(msgIDs) != 0 {
		t.Fatalf("expected no delivered message IDs for late thought, got %v", msgIDs)
	}
}

func TestPreSend_StreamActiveDoesNotConsumeEarlierVisibleMessage(t *testing.T) {
	m := newTestManager()
	m.streamActive.Store("test:123", true)
	m.streamAuxiliaryTombstones.Store("test:123", time.Now())
	m.RecordPlaceholder("test", "123", "placeholder-1")

	editCalls := 0
	ch := &mockMessageEditor{
		editFn: func(_ context.Context, chatID, messageID, content string) error {
			editCalls++
			if chatID != "123" || messageID != "placeholder-1" || content != "final streamed reply" {
				t.Fatalf("unexpected placeholder edit for %s/%s: %q", chatID, messageID, content)
			}
			return nil
		},
	}

	earlierVisible := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "earlier visible message",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
		},
	})
	_, handled := m.preSend(context.Background(), "test", earlierVisible, ch)
	if handled {
		t.Fatal("expected earlier visible message to be delivered normally")
	}
	if editCalls != 0 {
		t.Fatalf("placeholder edits after earlier visible message = %d, want 0", editCalls)
	}
	if _, ok := m.streamActive.Load("test:123"); !ok {
		t.Fatal("expected streamActive marker to remain for final outbound")
	}
	if _, ok := m.streamAuxiliaryTombstones.Load("test:123"); !ok {
		t.Fatal("expected auxiliary tombstone to remain")
	}
	if _, ok := m.placeholders.Load("test:123"); !ok {
		t.Fatal("expected placeholder cleanup to remain deferred to final outbound")
	}

	finalMsg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "final streamed reply",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"outbound_kind": "final",
			},
		},
	})
	_, handled = m.preSend(context.Background(), "test", finalMsg, ch)
	if !handled {
		t.Fatal("expected final outbound message to consume streamActive marker")
	}
	if _, ok := m.streamActive.Load("test:123"); ok {
		t.Fatal("expected streamActive marker to be cleared by final outbound message")
	}
	if editCalls != 1 {
		t.Fatalf("placeholder edits after final outbound = %d, want 1", editCalls)
	}
}

func TestPreSend_StreamActiveDoesNotConsumeOtherSessionFinal(t *testing.T) {
	m := newTestManager()
	m.streamActive.Store("test:123", true)
	m.RecordPlaceholder("test", "123", "placeholder-1")

	ch := &mockMessageEditor{
		editFn: func(_ context.Context, _, _, _ string) error {
			t.Fatal("placeholder edit should remain deferred for the streaming session")
			return nil
		},
	}

	otherSessionFinal := testOutboundMessage(bus.OutboundMessage{
		Channel:    "test",
		ChatID:     "123",
		SessionKey: "session-other",
		Content:    "other session final",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"outbound_kind": "final",
			},
		},
	})

	_, handled := m.preSend(context.Background(), "test", otherSessionFinal, ch)
	if handled {
		t.Fatal("expected final outbound from a different session to be delivered normally")
	}
	if _, ok := m.streamActive.Load("test:123"); !ok {
		t.Fatal("expected streaming marker to remain for the streaming session")
	}
	if _, ok := m.placeholders.Load("test:123"); !ok {
		t.Fatal("expected placeholder cleanup to remain deferred to the streaming session")
	}
}

func TestPreSendMedia_LeavesTrackedMessageForChannelSend(t *testing.T) {
	m := newTestManager()
	ch := &mockDeletingMediaChannel{}

	m.preSendMedia(context.Background(), "test", bus.OutboundMediaMessage{
		ChatID: "123",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
		},
	}, ch)

	if ch.dismissedChatID != "" {
		t.Fatalf(
			"expected tracked tool feedback cleanup to be deferred to channel media send, got %q",
			ch.dismissedChatID,
		)
	}
}

func TestPreSendMedia_SeparateMessagesClearsTrackedMessageWithoutDismiss(t *testing.T) {
	m := newTestManager()
	m.config = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				ToolFeedback: config.ToolFeedbackConfig{
					Enabled:          true,
					SeparateMessages: true,
				},
			},
		},
	}

	ch := &mockMessageEditor{}

	m.preSendMedia(context.Background(), "test", bus.OutboundMediaMessage{
		ChatID: "123",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
		},
	}, ch)

	if ch.clearedChatID != "123" {
		t.Fatalf("expected tracked tool feedback state to be cleared before media delivery, got %q", ch.clearedChatID)
	}
	if ch.dismissedChatID != "" {
		t.Fatalf("expected tracked tool feedback message to be preserved"+
			" for media delivery, got %q", ch.dismissedChatID)
	}
}

func TestSplitOutboundMessageContent_ToolFeedbackTruncatesInsteadOfSplitting(t *testing.T) {
	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "\U0001f527 `read_file`\nRead README.md first to confirm the current project structure before editing the config example.",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"message_kind": "tool_feedback",
			},
		},
	})

	chunks := splitOutboundMessageContent(msg, 40)
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}
	want := utils.FitToolFeedbackMessage(msg.Content, 40-MaxToolFeedbackAnimationFrameLength())
	if chunks[0] != want {
		t.Fatalf("chunk = %q, want %q", chunks[0], want)
	}
}

func TestSplitOutboundMessageContent_ToolFeedbackReservesAnimationFrame(t *testing.T) {
	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "🔧 `read_file`\n1234567890",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"message_kind": "tool_feedback",
			},
		},
	})

	chunks := splitOutboundMessageContent(msg, len([]rune(msg.Content)))
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}

	animated := formatAnimatedToolFeedbackContent(chunks[0], strings.Repeat(".", MaxToolFeedbackAnimationFrameLength()))
	if got, maxLen := len([]rune(animated)), len([]rune(msg.Content)); got > maxLen {
		t.Fatalf("animated len = %d, want <= %d; content=%q", got, maxLen, animated)
	}
}

func TestGetStreamer_FinalizeDismissesTrackedToolFeedback(t *testing.T) {
	m := newTestManager()
	ch := &mockStreamingChannel{
		mockMessageEditor: mockMessageEditor{},
		streamer: &mockStreamer{
			finalizeFn: func(_ context.Context, content string) error {
				if content != "final reply" {
					t.Fatalf("unexpected finalize content: %q", content)
				}
				return nil
			},
		},
	}
	m.channels["test"] = ch

	streamer, ok := m.GetStreamer(context.Background(), "test", "123", "")
	if !ok {
		t.Fatal("expected streamer to be available")
	}
	if err := streamer.Finalize(context.Background(), "final reply"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if ch.dismissedChatID != "123" {
		t.Fatalf("expected tracked tool feedback to be dismissed for chat 123, got %q", ch.dismissedChatID)
	}
	if _, ok := m.streamActive.Load("test:123"); !ok {
		t.Fatal("expected streamActive marker to be recorded after finalize")
	}
}

func TestGetStreamer_FinalizeCleansPlaceholderImmediately(t *testing.T) {
	m := newTestManager()
	m.RecordPlaceholder("test", "123", "placeholder-1")
	var editedContent string
	editCalls := 0
	ch := &mockStreamingChannel{
		mockMessageEditor: mockMessageEditor{
			editFn: func(_ context.Context, chatID, messageID, content string) error {
				if chatID != "123" || messageID != "placeholder-1" {
					t.Fatalf("unexpected edit target: %s/%s", chatID, messageID)
				}
				editCalls++
				editedContent = content
				return nil
			},
		},
		streamer: &mockStreamer{},
	}
	m.channels["test"] = ch

	streamer, ok := m.GetStreamer(context.Background(), "test", "123", "")
	if !ok {
		t.Fatal("expected streamer to be available")
	}
	if err := streamer.Finalize(context.Background(), "final reply"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if editedContent != "final reply" {
		t.Fatalf("edited placeholder content = %q, want final reply", editedContent)
	}
	if _, placeholderExists := m.placeholders.Load("test:123"); placeholderExists {
		t.Fatal("expected placeholder to be cleaned up during finalize")
	}
	if _, streamActiveExists := m.streamActive.Load("test:123"); !streamActiveExists {
		t.Fatal("expected streamActive marker to be recorded after finalize")
	}
	cleaner, ok := streamer.(interface{ ClearFinalizedStreamMarker() })
	if !ok {
		t.Fatal("expected streamer to expose marker cleanup")
	}
	cleaner.ClearFinalizedStreamMarker()
	if _, streamActiveExists := m.streamActive.Load("test:123"); streamActiveExists {
		t.Fatal("expected streamActive marker to be cleared")
	}
	if _, ok := m.streamAuxiliaryTombstones.Load("test:123"); !ok {
		t.Fatal("expected auxiliary tombstone to remain after final marker cleanup")
	}

	lateThought := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "late reasoning",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
			Raw: map[string]string{
				"message_kind": "thought",
			},
		},
	})
	msgIDs, handled := m.preSend(context.Background(), "test", lateThought, ch)
	if !handled {
		t.Fatal("expected auxiliary tombstone to drop late thought")
	}
	if len(msgIDs) != 0 {
		t.Fatalf("expected no delivered message IDs for late thought, got %v", msgIDs)
	}
	if editCalls != 1 {
		t.Fatalf("expected late thought not to edit placeholder, got %d edits", editCalls)
	}

	finalOutbound := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "123",
		Content: "visible final reply",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "123",
		},
	})
	_, handled = m.preSend(context.Background(), "test", finalOutbound, ch)
	if handled {
		t.Fatal("expected cleared final marker to let normal outbound send")
	}
	if _, ok := m.streamAuxiliaryTombstones.Load("test:123"); ok {
		t.Fatal("expected normal outbound to clear auxiliary tombstone")
	}
}

func TestGetStreamer_FinalizeCleansPlaceholderWithSessionKey(t *testing.T) {
	m := newTestManager()
	m.RecordPlaceholder("test", "123", "placeholder-1")
	ch := &mockStreamingChannel{
		mockMessageEditor: mockMessageEditor{
			editFn: func(_ context.Context, chatID, messageID, content string) error {
				if chatID != "123" || messageID != "placeholder-1" || content != "final reply" {
					t.Fatalf("unexpected edit for %s/%s: %q", chatID, messageID, content)
				}
				return nil
			},
		},
		streamer: &mockStreamer{},
	}
	m.channels["test"] = ch

	streamer, ok := m.GetStreamer(context.Background(), "test", "123", "session-1")
	if !ok {
		t.Fatal("expected streamer to be available")
	}
	if err := streamer.Finalize(context.Background(), "final reply"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if _, placeholderExists := m.placeholders.Load("test:123"); placeholderExists {
		t.Fatal("expected placeholder to be cleaned up during finalize")
	}
	if _, streamActiveExists := m.streamActive.Load("test:123:session-1"); !streamActiveExists {
		t.Fatal("expected session streamActive marker to be recorded after finalize")
	}
}

func TestGetStreamer_PreservesContextUsageStreamer(t *testing.T) {
	m := newTestManager()
	var gotUsage *bus.ContextUsage
	ch := &mockStreamingChannel{
		streamer: &mockStreamer{
			finalizeWithContextFn: func(_ context.Context, content string, usage *bus.ContextUsage) error {
				if content != "final reply" {
					t.Fatalf("unexpected finalize content: %q", content)
				}
				gotUsage = usage
				return nil
			},
		},
	}
	m.channels["test"] = ch

	streamer, ok := m.GetStreamer(context.Background(), "test", "123", "")
	if !ok {
		t.Fatal("expected streamer to be available")
	}
	contextStreamer, ok := streamer.(bus.ContextUsageStreamer)
	if !ok {
		t.Fatal("manager-wrapped streamer should preserve ContextUsageStreamer")
	}
	usage := &bus.ContextUsage{UsedTokens: 10, TotalTokens: 100, CompressAtTokens: 80, UsedPercent: 10}
	if err := contextStreamer.FinalizeWithContext(context.Background(), "final reply", usage); err != nil {
		t.Fatalf("FinalizeWithContext() error = %v", err)
	}
	if gotUsage != usage {
		t.Fatalf("context usage = %#v, want original usage", gotUsage)
	}
	if _, ok := m.streamActive.Load("test:123"); !ok {
		t.Fatal("expected streamActive marker to be recorded after finalize with context")
	}
}

func TestGetStreamer_PreservesReasoningStreamer(t *testing.T) {
	m := newTestManager()
	inner := &mockReasoningStreamer{}
	ch := &mockStreamingChannel{
		streamer: inner,
	}
	m.channels["test"] = ch

	streamer, ok := m.GetStreamer(context.Background(), "test", "123", "")
	if !ok {
		t.Fatal("expected streamer to be available")
	}
	reasoningStreamer, ok := streamer.(bus.ReasoningStreamer)
	if !ok {
		t.Fatal("manager-wrapped streamer should preserve ReasoningStreamer")
	}
	if err := reasoningStreamer.UpdateReasoning(context.Background(), "thinking"); err != nil {
		t.Fatalf("UpdateReasoning() error = %v", err)
	}
	if err := reasoningStreamer.FinalizeReasoning(context.Background(), "final thought"); err != nil {
		t.Fatalf("FinalizeReasoning() error = %v", err)
	}
	if got := inner.reasoningUpdates; len(got) != 1 || got[0] != "thinking" {
		t.Fatalf("reasoning updates = %v, want [thinking]", got)
	}
	if inner.reasoningFinal != "final thought" {
		t.Fatalf("reasoning final = %q, want final thought", inner.reasoningFinal)
	}
}

func TestGetStreamer_PreservesModelNameSetter(t *testing.T) {
	m := newTestManager()
	inner := &modelTrackingReasoningStreamer{}
	ch := &mockStreamingChannel{
		streamer: inner,
	}
	m.channels["test"] = ch

	streamer, ok := m.GetStreamer(context.Background(), "test", "123", "")
	if !ok {
		t.Fatal("expected streamer to be available")
	}
	setter, ok := streamer.(interface{ SetModelName(modelName string) })
	if !ok {
		t.Fatal("manager-wrapped streamer should preserve SetModelName")
	}
	setter.SetModelName("gpt-5.4")
	if err := streamer.Update(context.Background(), "hello"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	reasoningStreamer, ok := streamer.(bus.ReasoningStreamer)
	if !ok {
		t.Fatal("manager-wrapped streamer should preserve ReasoningStreamer")
	}
	setter.SetModelName("gpt-5.4")
	if err := reasoningStreamer.UpdateReasoning(context.Background(), "thinking"); err != nil {
		t.Fatalf("UpdateReasoning() error = %v", err)
	}
	if len(inner.modelNames) != 2 {
		t.Fatalf("model name calls = %v, want 2 forwarded calls", inner.modelNames)
	}
	if inner.modelNames[0] != "gpt-5.4" || inner.modelNames[1] != "gpt-5.4" {
		t.Fatalf("model name calls = %v, want both forwarded as gpt-5.4", inner.modelNames)
	}
}

func TestGetStreamer_SplitOnMarkerStreamsSeparateSegments(t *testing.T) {
	m := newTestManager()
	m.config = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				SplitOnMarker: true,
			},
		},
	}

	var segments []*recordingStreamSegment
	ch := &mockStreamingChannel{
		beginStreamFn: func(context.Context, string) (Streamer, error) {
			segment := &recordingStreamSegment{}
			segments = append(segments, segment)
			return segment, nil
		},
	}
	m.channels["test"] = ch

	streamer, ok := m.GetStreamer(context.Background(), "test", "123", "session-1")
	if !ok {
		t.Fatal("expected streamer to be available")
	}
	contextStreamer, ok := streamer.(bus.ContextUsageStreamer)
	if !ok {
		t.Fatal("split streamer should preserve ContextUsageStreamer")
	}

	if err := streamer.Update(context.Background(), "hello"); err != nil {
		t.Fatalf("Update(first) error = %v", err)
	}
	if err := streamer.Update(context.Background(), "hello<|[SPLIT]|>world"); err != nil {
		t.Fatalf("Update(split) error = %v", err)
	}
	if err := streamer.Update(context.Background(), "hello<|[SPLIT]|>world!"); err != nil {
		t.Fatalf("Update(second segment) error = %v", err)
	}
	usage := &bus.ContextUsage{UsedTokens: 10, TotalTokens: 100}
	if err := contextStreamer.FinalizeWithContext(
		context.Background(),
		"hello<|[SPLIT]|>world!",
		usage,
	); err != nil {
		t.Fatalf("FinalizeWithContext() error = %v", err)
	}

	if len(segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(segments))
	}
	if got := segments[0].updates; len(got) != 1 || got[0] != "hello" {
		t.Fatalf("segment 0 updates = %v, want [hello]", got)
	}
	if got := segments[0].finals; len(got) != 1 || got[0] != "hello" {
		t.Fatalf("segment 0 finals = %v, want [hello]", got)
	}
	if got := segments[1].updates; len(got) != 2 || got[0] != "world" || got[1] != "world!" {
		t.Fatalf("segment 1 updates = %v, want [world world!]", got)
	}
	if got := segments[1].finals; len(got) != 1 || got[0] != "world!" {
		t.Fatalf("segment 1 finals = %v, want [world!]", got)
	}
	if segments[1].finalUsage != usage {
		t.Fatalf("final usage = %#v, want original usage", segments[1].finalUsage)
	}
	if _, ok := m.streamActive.Load("test:123:session-1"); !ok {
		t.Fatal("expected streamActive marker to be recorded after split stream finalize")
	}
}

func TestGetStreamer_SplitOnMarkerKeepsReasoningOnInitialStreamer(t *testing.T) {
	m := newTestManager()
	m.config = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				SplitOnMarker: true,
			},
		},
	}

	initial := &mockReasoningStreamer{}
	next := &recordingStreamSegment{}
	callCount := 0
	ch := &mockStreamingChannel{
		beginStreamFn: func(context.Context, string) (Streamer, error) {
			callCount++
			if callCount == 1 {
				return initial, nil
			}
			return next, nil
		},
	}
	m.channels["test"] = ch

	streamer, ok := m.GetStreamer(context.Background(), "test", "123", "")
	if !ok {
		t.Fatal("expected streamer to be available")
	}
	if err := streamer.Update(context.Background(), "hello<|[SPLIT]|>world"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	reasoningStreamer, ok := streamer.(bus.ReasoningStreamer)
	if !ok {
		t.Fatal("split streamer should preserve ReasoningStreamer")
	}
	if err := reasoningStreamer.UpdateReasoning(context.Background(), "thinking"); err != nil {
		t.Fatalf("UpdateReasoning() error = %v", err)
	}
	if err := reasoningStreamer.FinalizeReasoning(context.Background(), "final thought"); err != nil {
		t.Fatalf("FinalizeReasoning() error = %v", err)
	}

	if got := initial.reasoningUpdates; len(got) != 1 || got[0] != "thinking" {
		t.Fatalf("initial reasoning updates = %v, want [thinking]", got)
	}
	if initial.reasoningFinal != "final thought" {
		t.Fatalf("initial reasoning final = %q, want final thought", initial.reasoningFinal)
	}
}

func TestGetStreamer_SplitOnMarkerPreservesModelNameSetter(t *testing.T) {
	m := newTestManager()
	m.config = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				SplitOnMarker: true,
			},
		},
	}

	initial := &modelTrackingReasoningStreamer{}
	next := &recordingStreamSegment{}
	callCount := 0
	ch := &mockStreamingChannel{
		beginStreamFn: func(context.Context, string) (Streamer, error) {
			callCount++
			if callCount == 1 {
				return initial, nil
			}
			return next, nil
		},
	}
	m.channels["test"] = ch

	streamer, ok := m.GetStreamer(context.Background(), "test", "123", "")
	if !ok {
		t.Fatal("expected streamer to be available")
	}
	setter, ok := streamer.(interface{ SetModelName(modelName string) })
	if !ok {
		t.Fatal("split streamer should preserve SetModelName")
	}
	setter.SetModelName("gpt-5.4-mini")
	if err := streamer.Update(context.Background(), "hello<|[SPLIT]|>world"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	reasoningStreamer, ok := streamer.(bus.ReasoningStreamer)
	if !ok {
		t.Fatal("split streamer should preserve ReasoningStreamer")
	}
	if err := reasoningStreamer.UpdateReasoning(context.Background(), "thinking"); err != nil {
		t.Fatalf("UpdateReasoning() error = %v", err)
	}

	if len(initial.modelNames) == 0 || initial.modelNames[0] != "gpt-5.4-mini" {
		t.Fatalf("initial model names = %v, want forwarded gpt-5.4-mini", initial.modelNames)
	}
	if len(next.modelNames) == 0 || next.modelNames[0] != "gpt-5.4-mini" {
		t.Fatalf("next model names = %v, want forwarded gpt-5.4-mini", next.modelNames)
	}
}

func TestGetStreamer_FinalizeSeparateMessagesClearsTrackedToolFeedback(t *testing.T) {
	m := newTestManager()
	m.config = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				ToolFeedback: config.ToolFeedbackConfig{
					Enabled:          true,
					SeparateMessages: true,
				},
			},
		},
	}
	ch := &mockStreamingChannel{
		mockMessageEditor: mockMessageEditor{},
		streamer: &mockStreamer{
			finalizeFn: func(_ context.Context, content string) error {
				if content != "final reply" {
					t.Fatalf("unexpected finalize content: %q", content)
				}
				return nil
			},
		},
	}
	m.channels["test"] = ch

	streamer, ok := m.GetStreamer(context.Background(), "test", "123", "")
	if !ok {
		t.Fatal("expected streamer to be available")
	}
	if err := streamer.Finalize(context.Background(), "final reply"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if ch.clearedChatID != "123" {
		t.Fatalf("expected tracked tool feedback to be cleared for chat 123, got %q", ch.clearedChatID)
	}
	if ch.dismissedChatID != "" {
		t.Fatalf("expected tracked tool feedback message to be preserved, got dismissal for %q", ch.dismissedChatID)
	}
	if _, ok := m.streamActive.Load("test:123"); !ok {
		t.Fatal("expected streamActive marker to be recorded after finalize")
	}
}

func TestGetStreamer_FinalizeDismissesResolvedTrackedToolFeedback(t *testing.T) {
	m := newTestManager()
	ch := &mockStreamingChannel{
		mockMessageEditor: mockMessageEditor{},
		streamer: &mockStreamer{
			finalizeFn: func(_ context.Context, content string) error {
				if content != "final reply" {
					t.Fatalf("unexpected finalize content: %q", content)
				}
				return nil
			},
		},
		resolveChatIDFn: func(chatID string, outboundCtx *bus.InboundContext) string {
			if outboundCtx == nil {
				t.Fatal("expected outbound context during stream finalize")
			}
			if outboundCtx.ChatID != "-100123/42" {
				t.Fatalf("unexpected outbound context: %+v", outboundCtx)
			}
			return outboundCtx.ChatID
		},
	}
	m.channels["test"] = ch

	streamer, ok := m.GetStreamer(context.Background(), "test", "-100123/42", "")
	if !ok {
		t.Fatal("expected streamer to be available")
	}
	if err := streamer.Finalize(context.Background(), "final reply"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if ch.dismissedChatID != "-100123/42" {
		t.Fatalf("expected resolved tracked tool feedback dismissal, got %q", ch.dismissedChatID)
	}
	if _, ok := m.streamActive.Load("test:-100123/42"); !ok {
		t.Fatal("expected streamActive marker to be recorded after finalize")
	}
}

func TestPreSend_PlaceholderEditSuccessDismissesResolvedTrackedToolFeedback(t *testing.T) {
	m := newTestManager()

	ch := &mockResolvedToolFeedbackEditor{
		mockMessageEditor: mockMessageEditor{
			editFn: func(_ context.Context, chatID, messageID, content string) error {
				if chatID != "-100123" || messageID != "456" || content != "done" {
					t.Fatalf("unexpected edit args: %s %s %s", chatID, messageID, content)
				}
				return nil
			},
		},
		resolveChatIDFn: func(chatID string, outboundCtx *bus.InboundContext) string {
			if outboundCtx == nil || outboundCtx.TopicID != "42" {
				t.Fatalf("expected topic-aware outbound context, got %+v", outboundCtx)
			}
			return chatID + "/" + outboundCtx.TopicID
		},
	}

	m.RecordPlaceholder("test", "-100123", "456")

	msg := testOutboundMessage(bus.OutboundMessage{
		Channel: "test",
		ChatID:  "-100123",
		Content: "done",
		Context: bus.InboundContext{
			Channel: "test",
			ChatID:  "-100123",
			TopicID: "42",
		},
	})

	_, edited := m.preSend(context.Background(), "test", msg, ch)
	if !edited {
		t.Fatal("expected preSend to edit placeholder")
	}
	if ch.dismissedChatID != "-100123/42" {
		t.Fatalf("expected resolved tracked dismissal, got %q", ch.dismissedChatID)
	}
}

func TestGetStreamer_FinalizeFailureDoesNotDismissTrackedToolFeedback(t *testing.T) {
	m := newTestManager()
	ch := &mockStreamingChannel{
		mockMessageEditor: mockMessageEditor{},
		streamer: &mockStreamer{
			finalizeFn: func(context.Context, string) error {
				return errors.New("finalize failed")
			},
		},
	}
	m.channels["test"] = ch

	streamer, ok := m.GetStreamer(context.Background(), "test", "123", "")
	if !ok {
		t.Fatal("expected streamer to be available")
	}
	if err := streamer.Finalize(context.Background(), "final reply"); err == nil {
		t.Fatal("expected Finalize() to fail")
	}
	if ch.dismissedChatID != "" {
		t.Fatalf("expected no tool feedback dismissal on finalize failure, got %q", ch.dismissedChatID)
	}
	if _, ok := m.streamActive.Load("test:123"); ok {
		t.Fatal("expected no streamActive marker after finalize failure")
	}
}

func TestPreSend_PlaceholderEditFails_FallsThrough(t *testing.T) {
	m := newTestManager()

	ch := &mockMessageEditor{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
				return nil
			},
		},
		editFn: func(_ context.Context, _, _, _ string) error {
			return fmt.Errorf("edit failed")
		},
	}

	m.RecordPlaceholder("test", "123", "456")

	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "123", Content: "hello"})
	_, edited := m.preSend(context.Background(), "test", msg, ch)

	if edited {
		t.Fatal("expected preSend to return false when edit fails")
	}
}

func TestPreSend_TypingStopCalled(t *testing.T) {
	m := newTestManager()
	var stopCalled bool

	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			return nil
		},
	}

	m.RecordTypingStop("test", "123", func() {
		stopCalled = true
	})

	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "123", Content: "hello"})
	m.preSend(context.Background(), "test", msg, ch)

	if !stopCalled {
		t.Fatal("expected typing stop func to be called")
	}
}

func TestPreSend_NoRegisteredState(t *testing.T) {
	m := newTestManager()

	ch := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
			return nil
		},
	}

	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "123", Content: "hello"})
	_, edited := m.preSend(context.Background(), "test", msg, ch)

	if edited {
		t.Fatal("expected preSend to return false with no registered state")
	}
}

func TestPreSend_TypingAndPlaceholder(t *testing.T) {
	m := newTestManager()
	var stopCalled bool
	var editCalled bool

	ch := &mockMessageEditor{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
				return nil
			},
		},
		editFn: func(_ context.Context, _, _, _ string) error {
			editCalled = true
			return nil
		},
	}

	m.RecordTypingStop("test", "123", func() {
		stopCalled = true
	})
	m.RecordPlaceholder("test", "123", "456")

	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "123", Content: "hello"})
	_, edited := m.preSend(context.Background(), "test", msg, ch)

	if !stopCalled {
		t.Fatal("expected typing stop to be called")
	}
	if !editCalled {
		t.Fatal("expected EditMessage to be called")
	}
	if !edited {
		t.Fatal("expected preSend to return true")
	}
}

func TestPreSendStillWorksWithWrappedTypes(t *testing.T) {
	m := newTestManager()
	var stopCalled bool
	var editCalled bool

	ch := &mockMessageEditor{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
				return nil
			},
		},
		editFn: func(_ context.Context, chatID, messageID, content string) error {
			editCalled = true
			if messageID != "ph_id" {
				t.Fatalf("expected messageID ph_id, got %s", messageID)
			}
			return nil
		},
	}

	// Use the new wrapped types via the public API
	m.RecordTypingStop("test", "chat1", func() {
		stopCalled = true
	})
	m.RecordPlaceholder("test", "chat1", "ph_id")

	msg := testOutboundMessage(bus.OutboundMessage{Channel: "test", ChatID: "chat1", Content: "response"})
	_, edited := m.preSend(context.Background(), "test", msg, ch)

	if !stopCalled {
		t.Fatal("expected typing stop to be called via wrapped type")
	}
	if !editCalled {
		t.Fatal("expected EditMessage to be called via wrapped type")
	}
	if !edited {
		t.Fatal("expected preSend to return true")
	}
}
