package memory

import (
	"errors"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func TestClaimPicoUserCommitsWithTranscript(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	inbound := bus.InboundMessage{Context: bus.InboundContext{Channel: "pico"}, Channel: "pico", Content: "hello"}
	if _, err := store.QueuePicoInbound(ctx, "session", "request", inbound); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingPicoInbound(ctx, "pico", 0)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %#v, error = %v", pending, err)
	}
	id := pending[0].ID
	msg := providers.Message{ID: "request", Role: "user", Content: inbound.Content}
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER reject_test_message BEFORE INSERT ON session_messages
		BEGIN SELECT RAISE(ABORT, 'test write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimPicoUser(ctx, id, "session", msg); claimed || err == nil {
		t.Fatalf("failed append allowed claim: %v, %v", claimed, err)
	}
	if _, err := store.db.ExecContext(ctx, "DROP TRIGGER reject_test_message"); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimPicoUser(ctx, id, "session", msg); !claimed || err != nil {
		t.Fatalf("retry claim = %v, error = %v", claimed, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := store.PendingPicoInbound(ctx, "pico", 0); err != nil || len(pending) != 0 {
		t.Fatalf("claimed request replayed: %#v, %v", pending, err)
	}
	if claimed, err := store.ClaimPicoUser(ctx, id, "session", msg); claimed || err != nil {
		t.Fatalf("second claim = %v, error = %v", claimed, err)
	}
	history, err := store.GetTranscript(ctx, "session")
	if err != nil || len(history) != 1 || history[0].ID != "request" {
		t.Fatalf("transcript = %#v, error = %v", history, err)
	}
	if removed, err := store.DeletePicoSession(ctx, "session", "session", true); removed || !errors.Is(err, ErrPicoInboundInFlight) {
		t.Fatalf("delete during active claim = %v, %v", removed, err)
	}
	if err := store.RecoverPicoInbound(ctx); err != nil {
		t.Fatal(err)
	}
	if status, err := store.QueuePicoInbound(ctx, "session", "request", inbound); err != nil || status != "unknown" {
		t.Fatalf("retry after recovery = %q, %v", status, err)
	}
	if pending, err := store.PendingPicoInbound(ctx, "pico", 0); err != nil || len(pending) != 0 {
		t.Fatalf("interrupted request replayed: %#v, %v", pending, err)
	}
	if removed, err := store.DeletePicoSession(ctx, "session", "session", true); !removed || err != nil {
		t.Fatalf("delete after recovery = %v, %v", removed, err)
	}
}

func TestQueuePicoInboundOnlyBindsNewSession(t *testing.T) {
	ctx := t.Context()
	store, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, err := store.CreateChatProject(ctx, "First", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateChatProject(ctx, "Second", "")
	if err != nil {
		t.Fatal(err)
	}
	inbound := bus.InboundMessage{Channel: "pico", Context: bus.InboundContext{
		Channel: "pico", Raw: map[string]string{PicoProjectMetadataKey: first.ID},
	}}
	if _, err := store.QueuePicoInbound(ctx, "session", "first", inbound); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionChatProject(ctx, "session", second.ID); err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{"first", "second"} {
		if _, err := store.QueuePicoInbound(ctx, "session", request, inbound); err != nil {
			t.Fatal(err)
		}
	}
	project, err := store.SessionChatProject(ctx, "session")
	if err != nil || project == nil || project.ID != second.ID {
		t.Fatalf("project after replay = %#v, error = %v", project, err)
	}
	if err := store.SetSessionChatProject(ctx, "session", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.QueuePicoInbound(ctx, "session", "third", inbound); err != nil {
		t.Fatal(err)
	}
	if project, err := store.SessionChatProject(ctx, "session"); project != nil || err != nil {
		t.Fatalf("unassigned project after send = %#v, error = %v", project, err)
	}
}
