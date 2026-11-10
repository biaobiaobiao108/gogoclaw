package pico

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
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
	ch.dedupePath = filepath.Join(t.TempDir(), "pico-inbound.json")
	ch.ctx = context.Background()

	ch.handleMessageSend(&picoConn{id: "conn-1", sessionID: "sess-1"}, PicoMessage{
		Type:      TypeMessageSend,
		ID:        "msg-1",
		SessionID: "another-session",
		Payload: map[string]any{
			PayloadKeyContent: "hello",
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
	case <-time.After(time.Second):
		t.Fatal("expected inbound pico message")
	}
}

func TestLoadMessageDedupe_AcceptsSnapshotAndRecoversCorruptState(t *testing.T) {
	newChannel := func(t *testing.T, state string) *PicoChannel {
		t.Helper()
		msgBus := bus.NewMessageBus()
		bc := &config.Channel{Type: config.ChannelPico, Enabled: true}
		cfg := &config.PicoSettings{}
		cfg.SetToken("test-token")
		ch, err := NewPicoChannel(bc, cfg, msgBus)
		if err != nil {
			t.Fatalf("NewPicoChannel: %v", err)
		}
		ch.dedupePath = filepath.Join(t.TempDir(), "pico-inbound.json")
		if err := os.WriteFile(ch.dedupePath, []byte(state), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		return ch
	}

	t.Run("current snapshot format", func(t *testing.T) {
		seenAt := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		state := fmt.Sprintf(`[{"key":"sess-1\u0000msg-1","seen_at":%q}]`, seenAt)
		ch := newChannel(t, state)
		claimed, err := ch.claimMessage("sess-1\x00msg-1")
		if err != nil {
			t.Fatalf("claimMessage existing: %v", err)
		}
		if claimed {
			t.Fatal("existing message was not deduplicated")
		}
		claimed, err = ch.claimMessage("sess-1\x00msg-2")
		if err != nil {
			t.Fatalf("claimMessage new: %v", err)
		}
		if !claimed {
			t.Fatal("new message should be claimed")
		}
	})

	t.Run("legacy map format", func(t *testing.T) {
		seenAt := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		state := fmt.Sprintf(`{"sess-1\u0000msg-1":%q}`, seenAt)
		ch := newChannel(t, state)
		claimed, err := ch.claimMessage("sess-1\x00msg-1")
		if err != nil {
			t.Fatalf("claimMessage: %v", err)
		}
		if claimed {
			t.Fatal("legacy message was not deduplicated")
		}
	})

	t.Run("corrupt state starts clean", func(t *testing.T) {
		ch := newChannel(t, `{not-json`)
		claimed, err := ch.claimMessage("sess-1\x00msg-1")
		if err != nil {
			t.Fatalf("claimMessage: %v", err)
		}
		if !claimed {
			t.Fatal("message should be claimed after corrupt state recovery")
		}
	})
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
