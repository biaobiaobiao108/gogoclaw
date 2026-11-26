package pico

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
)

func TestHandleMessageSend_ForwardsMessageMetadata(t *testing.T) {
	msgBus := bus.NewMessageBus()
	bc := &config.Channel{Type: config.ChannelPico, Enabled: true}
	cfg := &config.PicoSettings{}
	cfg.SetToken("test-token")
	ch, err := NewPicoChannel(bc, cfg, msgBus)
	if err != nil {
		t.Fatalf("NewPicoChannel: %v", err)
	}
	ch.inboxDir = filepath.Join(t.TempDir(), "sessions")
	if err := ch.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Stop(context.Background()) })
	project, err := ch.currentInbox().CreateChatProject(context.Background(), "Test Project", "")
	if err != nil {
		t.Fatal(err)
	}

	ch.handleMessageSend(&picoConn{id: "conn-1", sessionID: "sess-1"}, PicoMessage{
		Type:      TypeMessageSend,
		ID:        "msg-1",
		SessionID: "another-session",
		Payload: map[string]any{
			PayloadKeyContent: "hello",
			"project_id":      project.ID,
		},
	})

	select {
	case inbound := <-msgBus.InboundChan():
		if inbound.Content != "hello" {
			t.Fatalf("content = %q, want hello", inbound.Content)
		}
		if got := inbound.Context.Raw["session_id"]; got != "sess-1" {
			t.Fatalf("session_id raw = %q, want sess-1", got)
		}
		if got := inbound.Context.Raw["chat_project_id"]; got != project.ID {
			t.Fatalf("chat_project_id raw = %q, want %q", got, project.ID)
		}
		assigned, err := ch.currentInbox().SessionChatProject(context.Background(), "sess-1")
		if err != nil || assigned == nil || assigned.ID != project.ID {
			t.Fatalf("assigned project = %#v, %v", assigned, err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected inbound pico message")
	}
}

func TestHandleMessageSend_RejectsMissingProject(t *testing.T) {
	msgBus := bus.NewMessageBus()
	cfg := &config.PicoSettings{}
	cfg.SetToken("test-token")
	ch, err := NewPicoChannel(&config.Channel{Type: config.ChannelPico, Enabled: true}, cfg, msgBus)
	if err != nil {
		t.Fatal(err)
	}
	ch.inboxDir = filepath.Join(t.TempDir(), "sessions")
	if err := ch.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer ch.Stop(context.Background())
	conn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.handleMessageSend(&picoConn{id: "conn-project", conn: conn, sessionID: "sess-project"}, PicoMessage{
		Type: TypeMessageSend, ID: "msg-project",
		Payload: map[string]any{PayloadKeyContent: "hello", "project_id": "missing-project"},
	})
	response := mustReceivePicoMessage(t, received)
	if response.Type != TypeError || response.Payload["code"] != "project_not_found" {
		t.Fatalf("response = %#v", response)
	}
}

func TestPicoInboxReplaysPendingButFencesStartedRequests(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	start := func() (*PicoChannel, *bus.MessageBus) {
		t.Helper()
		mb := bus.NewMessageBus()
		configuration := &config.PicoSettings{}
		configuration.SetToken("test-token")
		channel, err := NewPicoChannel(&config.Channel{Type: config.ChannelPico, Enabled: true}, configuration, mb)
		if err != nil {
			t.Fatal(err)
		}
		channel.inboxDir = dir
		if err := channel.Start(ctx); err != nil {
			t.Fatal(err)
		}
		return channel, mb
	}
	receive := func(mb *bus.MessageBus) bus.InboundMessage {
		t.Helper()
		select {
		case msg := <-mb.InboundChan():
			return msg
		case <-time.After(5 * time.Second):
			t.Fatal("pending inbound message was not dispatched")
			return bus.InboundMessage{}
		}
	}
	channel, mb := start()
	conn, received, closeConn := newTestPicoWebSocket(t)
	defer closeConn()
	channel.handleMessageSend(&picoConn{conn: conn, sessionID: "chat"}, PicoMessage{
		ID: "request", Payload: map[string]any{PayloadKeyContent: "keep me"},
	})
	ack := mustReceivePicoMessage(t, received)
	if ack.Type != TypeMessageAck || ack.Payload["status"] != "accepted" {
		t.Fatalf("unexpected message acknowledgement: %+v", ack)
	}
	receive(mb) // The first process loses its volatile queue before claiming.
	if err := channel.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	mb.Close()

	channel, mb = start()
	replayed := receive(mb)
	if replayed.Content != "keep me" || replayed.MessageID != "request" {
		t.Fatalf("unexpected replay: %q (%s)", replayed.Content, replayed.MessageID)
	}
	if claimed, err := replayed.Claim(ctx); err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	if err := channel.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	mb.Close()

	store, err := memory.OpenSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if pending, err := store.PendingPicoInbound(ctx, "pico", 0); err != nil || len(pending) != 0 {
		t.Fatalf("started request was replayable: %v, %v", len(pending), err)
	}
	status, err := store.QueuePicoInbound(ctx, "chat", "request", replayed)
	if err != nil || status != "unknown" {
		t.Fatalf("retry of started request = %q, %v", status, err)
	}
	// Replacing the channel leaves the Agent worker responsible for finishing.
	replayed.Finish(ctx)
	status, err = store.QueuePicoInbound(ctx, "chat", "request", replayed)
	if err != nil || status != "duplicate" {
		t.Fatalf("retry of finished request after channel replacement = %q, %v", status, err)
	}
}

func TestPicoInboxAcknowledgesEditRetryDuringActiveTurn(t *testing.T) {
	ctx := t.Context()
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	settings := &config.PicoSettings{}
	settings.SetToken("test-token")
	channel, err := NewPicoChannel(&config.Channel{Type: config.ChannelPico, Enabled: true}, settings, msgBus)
	if err != nil {
		t.Fatal(err)
	}
	channel.inboxDir = t.TempDir()
	if err := channel.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer channel.Stop(ctx)
	conn, received, closeConn := newTestPicoWebSocket(t)
	defer closeConn()
	client := &picoConn{conn: conn, sessionID: "chat"}
	request := PicoMessage{
		ID:      "edit-request",
		Payload: map[string]any{PayloadKeyContent: "edited", "edit_message_id": "original"},
	}
	channel.handleMessageSend(client, request)
	if ack := mustReceivePicoMessage(t, received); ack.Type != TypeMessageAck || ack.Payload["status"] != "accepted" {
		t.Fatalf("initial acknowledgement = %#v", ack)
	}
	select {
	case inbound := <-msgBus.InboundChan():
		if claimed, err := inbound.Claim(ctx); err != nil || !claimed {
			t.Fatalf("claim = %v, %v", claimed, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("edit was not dispatched")
	}
	channel.SetSessionBusyHandler(func(string) bool { return true })
	channel.handleMessageSend(client, request)
	if ack := mustReceivePicoMessage(t, received); ack.Type != TypeMessageAck || ack.Payload["status"] != "unknown" {
		t.Fatalf("retry acknowledgement = %#v", ack)
	}
	request.ID = "new-edit"
	channel.handleMessageSend(client, request)
	if result := mustReceivePicoMessage(t, received); result.Type != TypeError || result.Payload["code"] != "session_busy" {
		t.Fatalf("unseen request response = %#v", result)
	}
}

func TestCreateAndAddConnection_RespectsMaxConnectionsConcurrently(t *testing.T) {
	ch := newTestPicoChannel(t)

	const (
		maxConns   = 5
		goroutines = 64
		sessionID  = "session-a"
	)

	var wg sync.WaitGroup
	var mu sync.Mutex
	successCount := 0
	errCount := 0

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			pc, err := ch.createAndAddConnection(nil, sessionID, maxConns)
			mu.Lock()
			defer mu.Unlock()

			if err == nil {
				successCount++
				if pc == nil {
					t.Errorf("pc is nil on success")
				}
				return
			}
			if !errors.Is(err, channels.ErrTemporary) {
				t.Errorf("unexpected error: %v", err)
				return
			}
			errCount++
		}()
	}
	wg.Wait()

	if successCount > maxConns {
		t.Fatalf("successCount=%d > maxConns=%d", successCount, maxConns)
	}
	if successCount+errCount != goroutines {
		t.Fatalf("success=%d err=%d total=%d want=%d", successCount, errCount, successCount+errCount, goroutines)
	}
	if got := ch.currentConnCount(); got != maxConns {
		t.Fatalf("currentConnCount=%d want=%d", got, maxConns)
	}
}

func TestRemoveConnection_CleansBothIndexes(t *testing.T) {
	ch := newTestPicoChannel(t)

	pc, err := ch.createAndAddConnection(nil, "session-cleanup", 10)
	if err != nil {
		t.Fatalf("createAndAddConnection: %v", err)
	}

	removed := ch.removeConnection(pc.id)
	if removed == nil {
		t.Fatal("removeConnection returned nil")
	}

	ch.connsMu.RLock()
	defer ch.connsMu.RUnlock()

	if _, ok := ch.connections[pc.id]; ok {
		t.Fatalf("connID %s still exists in connections", pc.id)
	}
	if _, ok := ch.sessionConnections[pc.sessionID]; ok {
		t.Fatalf("session %s still exists in sessionConnections", pc.sessionID)
	}
	if got := len(ch.connections); got != 0 {
		t.Fatalf("len(connections)=%d want=0", got)
	}
}

func TestBroadcastToSession_TargetsOnlyRequestedSession(t *testing.T) {
	ch := newTestPicoChannel(t)

	target := &picoConn{id: "target", sessionID: "s-target"}
	target.closed.Store(true)
	ch.addConnForTest(target)

	other := &picoConn{id: "other", sessionID: "s-other"}
	ch.addConnForTest(other)

	err := ch.broadcastToSession("pico:s-target", newMessage(TypeMessageCreate, map[string]any{"content": "hello"}))
	if err == nil {
		t.Fatal("expected send failure due to closed target connection")
	}
	if !errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("expected ErrSendFailed, got %v", err)
	}
}
