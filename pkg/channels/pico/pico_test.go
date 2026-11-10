package pico

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/biaobiaobiao108/gogoclaw/pkg/approval"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

func TestPicoMessageLimitAccommodatesMaxInlineImage(t *testing.T) {
	maxEncoded := base64.StdEncoding.EncodedLen(config.DefaultMaxMediaSize)
	if maxPicoMessageBytes < maxEncoded+1024 {
		t.Fatalf("max message bytes = %d, want at least %d", maxPicoMessageBytes, maxEncoded+1024)
	}
}

func TestApprovalRequestAndResponseRoundTrip(t *testing.T) {
	ch := newTestPicoChannel(t)
	conn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	pc := &picoConn{id: "approval-conn", conn: conn, sessionID: "approval-session"}
	ch.addConnForTest(pc)

	responded := make(chan approval.Response, 1)
	ch.SetApprovalHandler(func(_ context.Context, response approval.Response) error {
		responded <- response
		return nil
	})
	if err := ch.SendApprovalRequest(context.Background(), approval.Request{
		ID: "approval-1", ChatID: "pico:approval-session", Tool: "exec",
		Arguments: map[string]any{"command": "go test ./..."},
		Status:    approval.StatusPending, RiskSummary: "Runs a command",
	}); err != nil {
		t.Fatalf("SendApprovalRequest() error = %v", err)
	}
	request := mustReceivePicoMessage(t, received)
	if request.Type != TypeApprovalRequest || request.Payload["approval_id"] != "approval-1" {
		t.Fatalf("approval request = %+v", request)
	}

	ch.handleApprovalRespond(pc, PicoMessage{
		Type: TypeApprovalRespond,
		Payload: map[string]any{
			"approval_id": "approval-1",
			"approve":     true,
			"scope":       "session",
		},
	})
	select {
	case response := <-responded:
		if !response.Approve || response.Scope != approval.ScopeSession {
			t.Fatalf("approval response = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("approval handler was not called")
	}
	ack := mustReceivePicoMessage(t, received)
	if ack.Type != TypeApprovalAck {
		t.Fatalf("approval ack type = %q", ack.Type)
	}
	resolved := mustReceivePicoMessage(t, received)
	if resolved.Type != TypeApprovalResolved {
		t.Fatalf("approval resolved type = %q", resolved.Type)
	}
}

func TestApprovalSessionMappingExpires(t *testing.T) {
	ch := newTestPicoChannel(t)
	ch.approvalSessions["approval-expiring"] = "session-1"

	ch.forgetApprovalSession("approval-expiring", "session-1", 20*time.Millisecond)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ch.approvalMu.Lock()
		_, exists := ch.approvalSessions["approval-expiring"]
		ch.approvalMu.Unlock()
		if !exists {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expired approval session mapping was not removed")
}

func newTestPicoChannel(t *testing.T) *PicoChannel {
	t.Helper()

	bc := &config.Channel{Type: config.ChannelPico, Enabled: true}
	cfg := &config.PicoSettings{}
	cfg.SetToken("test-token")
	ch, err := NewPicoChannel(bc, cfg, bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewPicoChannel: %v", err)
	}

	ch.ctx = context.Background()
	return ch
}

func TestTurnStatusUsesConnectionSession(t *testing.T) {
	ch := newTestPicoChannel(t)
	ch.SetTurnStatusProvider(func(chatID string, _ bool) map[string]any {
		return map[string]any{"active": true, "chat": chatID}
	})
	conn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.handleMessage(&picoConn{conn: conn, sessionID: "own"}, PicoMessage{
		Type: TypeTurnStatusGet, SessionID: "another", ID: "request",
	})
	msg := mustReceivePicoMessage(t, received)
	if msg.Type != TypeTurnStatus || msg.SessionID != "own" || msg.Payload["chat"] != "pico:own" {
		t.Fatalf("unexpected status response: %+v", msg)
	}
}

func TestSend_UsesLogicalMessageIDAcrossRetries(t *testing.T) {
	ch := newTestPicoChannel(t)
	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})
	ch.SetRunning(true)
	msg := bus.OutboundMessage{
		ID:      "delivery-1",
		ChatID:  "pico:sess-1",
		Content: "hello",
		Context: bus.NewOutboundContext("pico", "pico:sess-1", ""),
	}

	if _, err := ch.Send(context.Background(), msg); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	if _, err := ch.Send(context.Background(), msg); err != nil {
		t.Fatalf("retry Send() error = %v", err)
	}

	for i := 0; i < 2; i++ {
		out := mustReceivePicoMessage(t, received)
		if got := out.Payload["message_id"]; got != "delivery-1" {
			t.Fatalf("message[%d].message_id = %#v, want delivery-1", i, got)
		}
	}
}





func TestFinalizeTrackedToolFeedbackMessage_StopsTrackingBeforeEdit(t *testing.T) {
	ch := &PicoChannel{
		progress: channels.NewToolFeedbackAnimator(nil),
	}
	ch.RecordToolFeedbackMessage("pico:chat-1", "msg-1", "🔧 `read_file`")

	msgIDs, handled := ch.finalizeTrackedToolFeedbackMessage(
		context.Background(),
		"pico:chat-1",
		"final reply",
		func(_ context.Context, chatID, messageID string, payload map[string]any, contextUsage *bus.ContextUsage) error {
			if _, ok := ch.currentToolFeedbackMessage(chatID); ok {
				t.Fatal("expected tracked tool feedback to be stopped before edit")
			}
			if chatID != "pico:chat-1" || messageID != "msg-1" {
				t.Fatalf("unexpected edit args: %s %s", chatID, messageID)
			}
			if got := payload[PayloadKeyContent]; got != "final reply" {
				t.Fatalf("unexpected content payload: %#v", got)
			}
			if contextUsage != nil {
				t.Fatalf("unexpected context usage: %+v", contextUsage)
			}
			return nil
		},
		nil,
		nil,
	)
	if !handled {
		t.Fatal("expected finalizeTrackedToolFeedbackMessage to handle tracked message")
	}
	if len(msgIDs) != 1 || msgIDs[0] != "msg-1" {
		t.Fatalf("finalizeTrackedToolFeedbackMessage() ids = %v, want [msg-1]", msgIDs)
	}
}

func TestDismissTrackedToolFeedbackMessage_DeletesProgressMessage(t *testing.T) {
	ch := &PicoChannel{
		progress: channels.NewToolFeedbackAnimator(nil),
	}
	ch.RecordToolFeedbackMessage("pico:chat-1", "msg-1", "🔧 `read_file`")

	var deleted struct {
		chatID    string
		messageID string
	}
	ch.deleteMessageFn = func(_ context.Context, chatID string, messageID string) error {
		deleted.chatID = chatID
		deleted.messageID = messageID
		return nil
	}

	ch.DismissToolFeedbackMessage(context.Background(), "pico:chat-1")

	if deleted.chatID != "pico:chat-1" || deleted.messageID != "msg-1" {
		t.Fatalf("unexpected delete target: %+v", deleted)
	}
	if _, ok := ch.currentToolFeedbackMessage("pico:chat-1"); ok {
		t.Fatal("expected tracked tool feedback to be cleared after dismissal")
	}
}

func TestSend_ThoughtMessageDoesNotFinalizeTrackedToolFeedback(t *testing.T) {
	ch := newTestPicoChannel(t)

	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	ch.RecordToolFeedbackMessage("pico:sess-1", "msg-progress", "🔧 `read_file`\nReading config")

	if _, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "pico:sess-1",
		Content: "thinking trace",
		Context: bus.InboundContext{
			Channel: "pico",
			ChatID:  "pico:sess-1",
			Raw: map[string]string{
				"message_kind":      MessageKindThought,
				PayloadKeyModelName: "gpt-5.4-mini",
			},
		},
	}); err != nil {
		t.Fatalf("Send(thought) error = %v", err)
	}

	select {
	case msg := <-received:
		if msg.Type != TypeMessageCreate {
			t.Fatalf("thought message type = %q, want %q", msg.Type, TypeMessageCreate)
		}
		payload := msg.Payload
		if got := payload[PayloadKeyContent]; got != "thinking trace" {
			t.Fatalf("thought content = %#v, want %q", got, "thinking trace")
		}
		if got := payload[PayloadKeyKind]; got != MessageKindThought {
			t.Fatalf("thought kind = %#v, want %q", got, MessageKindThought)
		}
		if got := payload[PayloadKeyModelName]; got != "gpt-5.4-mini" {
			t.Fatalf("thought model_name = %#v, want %q", got, "gpt-5.4-mini")
		}
		if got := payload["message_id"]; got == "msg-progress" || got == nil || got == "" {
			t.Fatalf("thought message_id = %#v, want new non-progress id", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected thought message to be delivered")
	}

	if msgID, ok := ch.currentToolFeedbackMessage("pico:sess-1"); !ok || msgID != "msg-progress" {
		t.Fatalf("tracked tool feedback = (%q, %v), want (msg-progress, true)", msgID, ok)
	}

	if _, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "pico:sess-1",
		Content: "final reply",
		Context: bus.InboundContext{
			Channel: "pico",
			ChatID:  "pico:sess-1",
			Raw: map[string]string{
				PayloadKeyModelName: "gpt-5.4",
			},
		},
		ContextUsage: &bus.ContextUsage{
			UsedTokens:       321,
			TotalTokens:      4096,
			CompressAtTokens: 3072,
			UsedPercent:      8,
		},
	}); err != nil {
		t.Fatalf("Send(final) error = %v", err)
	}

	select {
	case msg := <-received:
		if msg.Type != TypeMessageUpdate {
			t.Fatalf("final message type = %q, want %q", msg.Type, TypeMessageUpdate)
		}
		payload := msg.Payload
		if got := payload["message_id"]; got != "msg-progress" {
			t.Fatalf("final message_id = %#v, want %q", got, "msg-progress")
		}
		if got := payload[PayloadKeyContent]; got != "final reply" {
			t.Fatalf("final content = %#v, want %q", got, "final reply")
		}
		if got := payload[PayloadKeyModelName]; got != "gpt-5.4" {
			t.Fatalf("final model_name = %#v, want %q", got, "gpt-5.4")
		}
		rawUsage, ok := payload["context_usage"].(map[string]any)
		if !ok {
			t.Fatalf("final context_usage = %#v, want map payload", payload["context_usage"])
		}
		if got, ok := rawUsage["used_tokens"].(float64); !ok || got != 321 {
			t.Fatalf("used_tokens = %#v, want 321", rawUsage["used_tokens"])
		}
		if got, ok := rawUsage["total_tokens"].(float64); !ok || got != 4096 {
			t.Fatalf("total_tokens = %#v, want 4096", rawUsage["total_tokens"])
		}
	case <-time.After(time.Second):
		t.Fatal("expected final reply to finalize tracked tool feedback")
	}

	if _, ok := ch.currentToolFeedbackMessage("pico:sess-1"); ok {
		t.Fatal("expected tracked tool feedback to be cleared after final reply")
	}
}

func TestSend_ToolCallsMessageIncludesModelName(t *testing.T) {
	ch := newTestPicoChannel(t)

	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	if _, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "pico:sess-1",
		Content: "",
		Context: bus.InboundContext{
			Channel: "pico",
			ChatID:  "pico:sess-1",
			Raw: map[string]string{
				"message_kind":      MessageKindToolCalls,
				PayloadKeyModelName: "gpt-5.4",
				PayloadKeyToolCalls: `[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]`,
			},
		},
	}); err != nil {
		t.Fatalf("Send(tool_calls) error = %v", err)
	}

	select {
	case msg := <-received:
		if msg.Type != TypeMessageCreate {
			t.Fatalf("tool_calls message type = %q, want %q", msg.Type, TypeMessageCreate)
		}
		payload := msg.Payload
		if got := payload[PayloadKeyKind]; got != MessageKindToolCalls {
			t.Fatalf("tool_calls kind = %#v, want %q", got, MessageKindToolCalls)
		}
		if got := payload[PayloadKeyModelName]; got != "gpt-5.4" {
			t.Fatalf("tool_calls model_name = %#v, want %q", got, "gpt-5.4")
		}
		if _, ok := payload[PayloadKeyToolCalls].([]any); !ok {
			t.Fatalf("tool_calls payload = %#v, want parsed array", payload[PayloadKeyToolCalls])
		}
	case <-time.After(time.Second):
		t.Fatal("expected tool_calls message to be delivered")
	}
}

func TestSendPlaceholder_EmitsNormalMessageWithoutKind(t *testing.T) {
	ch := newTestPicoChannel(t)
	ch.bc.Placeholder.Enabled = true

	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	msgID, err := ch.SendPlaceholder(context.Background(), "pico:sess-1")
	if err != nil {
		t.Fatalf("SendPlaceholder() error = %v", err)
	}
	if msgID == "" {
		t.Fatal("expected placeholder message id")
	}

	select {
	case msg := <-received:
		if msg.Type != TypeMessageCreate {
			t.Fatalf("placeholder message type = %q, want %q", msg.Type, TypeMessageCreate)
		}
		payload := msg.Payload
		if got := payload["message_id"]; got != msgID {
			t.Fatalf("placeholder message_id = %#v, want %q", got, msgID)
		}
		if got := payload[PayloadKeyContent]; got != "Thinking..." {
			t.Fatalf("placeholder content = %#v, want %q", got, "Thinking...")
		}
		if got := payload[PayloadKeyPlaceholder]; got != true {
			t.Fatalf("placeholder marker = %#v, want true", got)
		}
		if got, ok := payload[PayloadKeyKind]; ok {
			t.Fatalf("placeholder kind = %#v, want absent", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected placeholder message to be delivered")
	}
}





























func mustReceivePicoMessage(t *testing.T, received <-chan PicoMessage) PicoMessage {
	t.Helper()
	select {
	case msg := <-received:
		return msg
	case <-time.After(time.Second):
		t.Fatal("expected pico message")
	}
	return PicoMessage{}
}

func mustReceivePicoContentSequence(
	t *testing.T,
	received <-chan PicoMessage,
	want string,
) []PicoMessage {
	t.Helper()
	deadline := time.After(3 * time.Second)
	var messages []PicoMessage
	for {
		select {
		case msg := <-received:
			messages = append(messages, msg)
			if content, _ := msg.Payload[PayloadKeyContent].(string); content == want {
				return messages
			}
		case <-deadline:
			t.Fatalf("expected pico content %q, got %+v", want, messages)
		}
	}
}

func assertNoPicoMessage(t *testing.T, received <-chan PicoMessage) {
	t.Helper()
	select {
	case msg := <-received:
		t.Fatalf("unexpected pico message: %+v", msg)
	case <-time.After(150 * time.Millisecond):
	}
}





func (c *PicoChannel) addConnForTest(pc *picoConn) {
	c.connsMu.Lock()
	defer c.connsMu.Unlock()
	if c.connections == nil {
		c.connections = make(map[string]*picoConn)
	}
	if c.sessionConnections == nil {
		c.sessionConnections = make(map[string]map[string]*picoConn)
	}
	if _, exists := c.connections[pc.id]; exists {
		panic(fmt.Sprintf("duplicate conn id in test: %s", pc.id))
	}
	c.connections[pc.id] = pc
	bySession, ok := c.sessionConnections[pc.sessionID]
	if !ok {
		bySession = make(map[string]*picoConn)
		c.sessionConnections[pc.sessionID] = bySession
	}
	bySession[pc.id] = pc
}

func newTestPicoWebSocket(t *testing.T) (*websocket.Conn, <-chan PicoMessage, func()) {
	t.Helper()

	received := make(chan PicoMessage, 4)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade() error = %v", err)
			return
		}
		defer conn.Close()
		for {
			var msg PicoMessage
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			received <- msg
		}
	}))

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		server.Close()
		t.Fatalf("Dial() error = %v", err)
	}

	cleanup := func() {
		clientConn.Close()
		server.Close()
	}
	defer resp.Body.Close()
	return clientConn, received, cleanup
}
