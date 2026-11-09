package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

func TestTurnLifecycleAllowedPath(t *testing.T) {
	lifecycle := newTurnLifecycle()
	for _, phase := range []TurnPhase{
		TurnPhaseSetup,
		TurnPhaseRunning,
		TurnPhaseTools,
		TurnPhaseRunning,
		TurnPhaseFinalizing,
		TurnPhaseCompleted,
	} {
		if err := lifecycle.Transition(phase); err != nil {
			t.Fatalf("transition to %s failed: %v", phase, err)
		}
	}
	if got := lifecycle.Phase(); got != TurnPhaseCompleted {
		t.Fatalf("phase = %s, want completed", got)
	}
	if err := lifecycle.Transition(TurnPhaseRunning); err == nil {
		t.Fatal("completed turn accepted a transition back to running")
	}
	history := lifecycle.History()
	if len(history) != 6 || history[0].From != TurnPhaseCreated || history[len(history)-1].To != TurnPhaseCompleted {
		t.Fatalf("unexpected lifecycle history: %+v", history)
	}
}

func TestTurnLifecycleRejectsSkippedTerminal(t *testing.T) {
	lifecycle := newTurnLifecycle()
	if err := lifecycle.Transition(TurnPhaseSetup); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Transition(TurnPhaseCompleted); err == nil {
		t.Fatal("setup -> completed should be rejected")
	}
	if err := lifecycle.Transition(TurnPhaseFailed); err != nil {
		t.Fatalf("setup -> failed should be allowed: %v", err)
	}
}

func TestRunTurnEndsInCompletedPhase(t *testing.T) {
	provider := &sequenceProvider{responses: []*providers.LLMResponse{{Content: "done"}}}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	ts := newTurnState(agent, makeTestProcessOpts("lifecycle-complete"), turnEventScope{
		turnID: "lifecycle-complete", context: newTurnContext(nil, nil, nil),
	})
	if _, err := al.runTurn(context.Background(), ts, NewPipeline(al)); err != nil {
		t.Fatal(err)
	}
	if got := ts.phaseSnapshot(); got != TurnPhaseCompleted {
		t.Fatalf("phase = %s, want completed (history %+v)", got, ts.phaseHistory())
	}
}

func TestRunTurnSetupFailureEndsInFailedPhase(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()
	pipeline := NewPipeline(al)
	pipeline.ContextManager = &failingAssembleContextManager{}
	ts := newTurnState(agent, makeTestProcessOpts("lifecycle-failed"), turnEventScope{
		turnID: "lifecycle-failed", context: newTurnContext(nil, nil, nil),
	})
	if _, err := al.runTurn(context.Background(), ts, pipeline); err == nil || !strings.Contains(err.Error(), "history unavailable") {
		t.Fatalf("expected setup failure, got %v", err)
	}
	if got := ts.phaseSnapshot(); got != TurnPhaseFailed {
		t.Fatalf("phase = %s, want failed (history %+v)", got, ts.phaseHistory())
	}
}

func TestConsumeAsyncDeliveriesInjectsExactlyOnce(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()
	eventsCh, closeEvents := subscribeRuntimeEventsForTest(t, al, 8, runtimeevents.KindAgentAsyncResultDelivered)
	defer closeEvents()
	ts := newTurnState(agent, makeTestProcessOpts("async-consume"), turnEventScope{
		turnID: "async-consume", context: newTurnContext(nil, nil, nil),
	})
	if err := ts.transitionPhase(TurnPhaseRunning); err != nil {
		t.Fatal(err)
	}
	ts.enqueueAsyncDelivery(asyncDelivery{
		Origin: asyncOriginSubTurn,
		Source: "subturn-1",
		Result: &tools.ToolResult{ForLLM: "subtask output"},
	})
	ts.enqueueAsyncDelivery(asyncDelivery{
		Origin:     asyncOriginTool,
		Source:     "spawn",
		ToolCallID: "call-1",
		Result:     &tools.ToolResult{ForLLM: "background output"},
	})

	messages := []providers.Message{{Role: "user", Content: "task"}}
	if err := al.consumeAsyncDeliveries(context.Background(), ts, &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	if history := agent.Sessions.GetHistory(ts.sessionKey); len(history) != 2 {
		t.Fatalf("persisted async messages = %d, want 2", len(history))
	}
	if err := al.consumeAsyncDeliveries(context.Background(), ts, &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatal("async deliveries were injected twice")
	}
	waitForRuntimeEvent(t, eventsCh, time.Second, func(evt runtimeevents.Event) bool {
		return evt.Kind == runtimeevents.KindAgentAsyncResultDelivered
	})
}

func TestFinishDefersBufferedDeliveriesAsFollowUps(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()

	eventsCh, closeEvents := subscribeRuntimeEventsForTest(t, al, 16,
		runtimeevents.KindAgentFollowUpQueued,
		runtimeevents.KindAgentSubTurnOrphan,
	)
	defer closeEvents()

	ts := newTurnState(agent, makeTestProcessOpts("async-finish"), turnEventScope{
		turnID: "async-finish", context: newTurnContext(nil, nil, nil),
	})
	ts.al = al
	ts.enqueueAsyncDelivery(asyncDelivery{
		Origin: asyncOriginSubTurn,
		Source: "late-child",
		Result: &tools.ToolResult{ForLLM: "late output"},
	})
	ts.Finish(false)
	al.flushDeferredFollowUps(ts)

	waitForRuntimeEvent(t, eventsCh, time.Second, func(evt runtimeevents.Event) bool {
		return evt.Kind == runtimeevents.KindAgentFollowUpQueued
	})
	select {
	case evt := <-eventsCh:
		if evt.Kind == runtimeevents.KindAgentSubTurnOrphan {
			t.Fatal("graceful finish reported a buffered delivery as orphan")
		}
	case <-time.After(50 * time.Millisecond):
	}
}
