package harness

import (
	"context"
	"sync"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
)

func TestWaitForRootTurnEndUsesTurnBaselineAndSession(t *testing.T) {
	events := []runtimeevents.Event{
		{Kind: runtimeevents.KindAgentTurnEnd, Scope: runtimeevents.Scope{SessionKey: "other", TurnID: "stale"}},
		{Kind: runtimeevents.KindAgentTurnStart, Scope: runtimeevents.Scope{SessionKey: "target", TurnID: "turn-1"}},
		{Kind: runtimeevents.KindAgentTurnEnd, Scope: runtimeevents.Scope{SessionKey: "target", TurnID: "turn-1"}},
	}
	var mu sync.Mutex
	changed := make(chan struct{}, 1)
	index, err := waitForRootTurnEnd(context.Background(), "target", 1, changed, &mu, &events)
	if err != nil {
		t.Fatalf("waitForRootTurnEnd() error = %v", err)
	}
	if index != 2 {
		t.Fatalf("waitForRootTurnEnd() index = %d, want 2", index)
	}
}

func TestEventsForTurnRangeStartsAtMatchingTurn(t *testing.T) {
	events := []runtimeevents.Event{
		{Kind: runtimeevents.KindAgentTurnEnd, Scope: runtimeevents.Scope{SessionKey: "other", TurnID: "stale"}},
		{Kind: runtimeevents.KindAgentTurnStart, Scope: runtimeevents.Scope{SessionKey: "target", TurnID: "turn-1"}},
		{Kind: runtimeevents.KindAgentToolExecStart, Scope: runtimeevents.Scope{SessionKey: "target", TurnID: "turn-1"}},
		{Kind: runtimeevents.KindAgentTurnEnd, Scope: runtimeevents.Scope{SessionKey: "target", TurnID: "turn-1"}, Payload: agent.TurnEndPayload{}},
	}
	turnEvents := eventsForTurnRange(events, 0, 3)
	if len(turnEvents) != 3 {
		t.Fatalf("eventsForTurnRange() length = %d, want 3", len(turnEvents))
	}
	if turnEvents[0].Kind != runtimeevents.KindAgentTurnStart || turnEvents[0].Scope.TurnID != "turn-1" {
		t.Fatalf("eventsForTurnRange() starts with %+v", turnEvents[0])
	}
}
