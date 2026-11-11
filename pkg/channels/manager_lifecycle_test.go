package channels

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
)

func TestSetMediaStorePropagatesToExistingChannels(t *testing.T) {
	oldStore := media.NewFileMediaStore()
	newStore := media.NewFileMediaStore()
	ch := &mockChannel{}
	ch.SetMediaStore(oldStore)

	m := newTestManager()
	m.mediaStore = oldStore
	m.channels["telegram"] = ch

	m.SetMediaStore(newStore)

	if m.mediaStore != newStore {
		t.Fatal("manager media store was not updated")
	}
	if got := ch.GetMediaStore(); got != newStore {
		t.Fatalf("channel media store = %p, want %p", got, newStore)
	}
}

func TestStartAll_AllChannelsFail_ReturnsJoinedError(t *testing.T) {
	m := newTestManager()
	errA := errors.New("channel-a start failed")
	errB := errors.New("channel-b start failed")

	m.channels["a"] = &mockChannel{
		startFn: func(_ context.Context) error { return errA },
	}
	m.channels["b"] = &mockChannel{
		startFn: func(_ context.Context) error { return errB },
	}

	err := m.StartAll(t.Context())
	if err == nil {
		t.Fatal("expected StartAll to fail when all channels fail")
	}
	if !strings.Contains(err.Error(), "failed to start any enabled channels") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !errors.Is(err, errA) {
		t.Fatalf("expected error to wrap errA, got: %v", err)
	}
	if !errors.Is(err, errB) {
		t.Fatalf("expected error to wrap errB, got: %v", err)
	}
	if len(m.workers) != 0 {
		t.Fatalf("expected no workers on full startup failure, got %d", len(m.workers))
	}
	if m.dispatchTask != nil {
		t.Fatal("expected dispatch task to be cleared on full startup failure")
	}
}

func TestStartAll_PartialFailure_StartsSuccessfulWorkers(t *testing.T) {
	m := newTestManager()
	errBad := errors.New("bad channel start failed")
	processed := make(chan struct{}, 1)

	m.channels["good"] = &mockChannel{
		sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
			if msg.Channel == "good" {
				select {
				case processed <- struct{}{}:
				default:
				}
			}
			return nil
		},
	}
	m.channels["bad"] = &mockChannel{
		startFn: func(_ context.Context) error { return errBad },
	}

	err := m.StartAll(t.Context())
	if err != nil {
		t.Fatalf("expected StartAll to succeed with partial channel failures, got: %v", err)
	}
	if len(m.workers) != 1 {
		t.Fatalf("expected exactly 1 active worker, got %d", len(m.workers))
	}
	if _, ok := m.workers["good"]; !ok {
		t.Fatal("expected worker for successful channel 'good'")
	}
	if _, ok := m.workers["bad"]; ok {
		t.Fatal("did not expect worker for failed channel 'bad'")
	}
	if m.dispatchTask == nil {
		t.Fatal("expected dispatch task to run when at least one channel starts")
	}

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()
	if err := m.bus.PublishOutbound(pubCtx, testOutboundMessage(bus.OutboundMessage{
		Channel: "good",
		ChatID:  "chat-1",
		Content: "hello",
	})); err != nil {
		t.Fatalf("PublishOutbound() error = %v", err)
	}

	select {
	case <-processed:
		// worker processed outbound message as expected
	case <-time.After(2 * time.Second):
		t.Fatal("expected successful channel worker to process outbound message")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	if err := m.StopAll(stopCtx); err != nil {
		t.Fatalf("StopAll() error = %v", err)
	}
}

func TestStopAllIsIdempotentAfterPartialStartFailure(t *testing.T) {
	m := newTestManager()
	m.channels["good"] = &mockChannel{}
	m.channels["bad"] = &mockChannel{startFn: func(context.Context) error {
		return errors.New("bad start")
	}}
	if err := m.StartAll(t.Context()); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	if err := m.StopAll(t.Context()); err != nil {
		t.Fatalf("first StopAll() error = %v", err)
	}
	if len(m.workers) != 0 {
		t.Fatalf("workers retained after StopAll: %d", len(m.workers))
	}
	if err := m.StopAll(t.Context()); err != nil {
		t.Fatalf("second StopAll() error = %v", err)
	}
}

func TestStopAllReturnsAfterWorkerTimeout(t *testing.T) {
	m := newTestManager()
	channel := &mockChannel{}
	workerCtx, workerCancel := context.WithCancel(context.Background())
	worker := newChannelWorker("stuck", channel, "stuck")
	worker.ctx = workerCtx
	cancelCalled := make(chan struct{}, 1)
	worker.cancel = func() {
		workerCancel()
		select {
		case cancelCalled <- struct{}{}:
		default:
		}
	}
	m.channels["stuck"] = channel
	m.workers["stuck"] = worker

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := m.StopAll(ctx); err == nil {
		t.Fatal("expected StopAll to report a worker that did not stop")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("StopAll() elapsed = %v, want a bounded wait", elapsed)
	}
	select {
	case <-cancelCalled:
	default:
		t.Fatal("expected StopAll to cancel the worker")
	}
}

func TestStartAllPublishesLifecycleRuntimeEvents(t *testing.T) {
	eventBus := runtimeevents.NewBus()
	defer func() {
		if err := eventBus.Close(); err != nil {
			t.Errorf("event bus close failed: %v", err)
		}
	}()

	_, eventsCh, err := eventBus.Channel().SubscribeChan(
		t.Context(),
		runtimeevents.SubscribeOptions{Name: "channel-lifecycle", Buffer: 4},
	)
	if err != nil {
		t.Fatalf("SubscribeChan failed: %v", err)
	}

	m := newTestManager()
	m.runtimeEvents = eventBus
	m.config = &config.Config{Channels: config.ChannelsConfig{}}
	m.channels["good"] = &mockChannel{}
	m.channels["bad"] = &mockChannel{
		startFn: func(_ context.Context) error { return errors.New("bad start") },
	}

	if err := m.StartAll(t.Context()); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := m.StopAll(stopCtx); err != nil {
			t.Errorf("StopAll() error = %v", err)
		}
	})

	events := []runtimeevents.Event{
		receiveChannelRuntimeEvent(t, eventsCh),
		receiveChannelRuntimeEvent(t, eventsCh),
	}
	seen := map[runtimeevents.Kind]runtimeevents.Event{}
	for _, evt := range events {
		seen[evt.Kind] = evt
	}
	if evt, ok := seen[runtimeevents.KindChannelLifecycleStarted]; !ok || evt.Scope.Channel != "good" {
		t.Fatalf("missing started event for good channel: %+v", events)
	}
	if evt, ok := seen[runtimeevents.KindChannelLifecycleStartFailed]; !ok || evt.Scope.Channel != "bad" {
		t.Fatalf("missing failed event for bad channel: %+v", events)
	}
}
