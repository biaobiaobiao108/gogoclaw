package events

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestEventBus_KeyedConcurrencyOrdersPerKeyAndRunsAcrossKeysConcurrently(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	var (
		mu       sync.Mutex
		perKey   = make(map[string][]int)
		startedA = make(chan struct{})
		releaseA = make(chan struct{})
		startedB = make(chan struct{})
	)

	ctx := context.Background()
	sub, err := bus.subscribe(ctx, nil, SubscribeOptions{
		Name:        "keyed-worker",
		Concurrency: Keyed,
		Buffer:      16,
	}, func(_ context.Context, evt Event) error {
		seq := evt.Payload.(int)
		key := evt.Scope.SessionKey
		if key == "session-a" && seq == 1 {
			close(startedA)
			<-releaseA
		}
		if key == "session-b" && seq == 1 {
			close(startedB)
		}
		mu.Lock()
		perKey[key] = append(perKey[key], seq)
		mu.Unlock()
		return nil
	}, false)
	if err != nil {
		t.Fatalf("subscribe() error = %v", err)
	}

	for i := 1; i <= 3; i++ {
		if res := bus.Publish(ctx, Event{
			Kind:    "test.event",
			Scope:   Scope{SessionKey: "session-a"},
			Payload: i,
		}); res.Matched == 0 {
			t.Fatalf("Publish(session-a, %d) matched 0", i)
		}
	}
	if res := bus.Publish(ctx, Event{
		Kind:    "test.event",
		Scope:   Scope{SessionKey: "session-b"},
		Payload: 1,
	}); res.Matched == 0 {
		t.Fatalf("Publish(session-b, 1) matched 0")
	}

	select {
	case <-startedA:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session-a first event")
	}

	// session-b must make progress even while session-a is blocked.
	select {
	case <-startedB:
	case <-time.After(2 * time.Second):
		t.Fatal("session-b was blocked by session-a under Keyed concurrency")
	}

	close(releaseA)
	if err := sub.Close(); err != nil {
		t.Fatalf("sub.Close() error = %v", err)
	}
	<-sub.Done()

	mu.Lock()
	defer mu.Unlock()
	gotA := perKey["session-a"]
	if len(gotA) != 3 || gotA[0] != 1 || gotA[1] != 2 || gotA[2] != 3 {
		t.Fatalf("session-a order = %v, want [1 2 3]", gotA)
	}
	gotB := perKey["session-b"]
	if len(gotB) != 1 || gotB[0] != 1 {
		t.Fatalf("session-b order = %v, want [1]", gotB)
	}
}
