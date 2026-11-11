package agent

import (
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

func TestRegressionCheckpointPreservesOutbox(t *testing.T) {
	store := session.NewSessionManager("")
	first := session.TurnDelivery{ID: "first", Status: "failed", Content: "old undelivered answer"}
	second := session.TurnDelivery{ID: "second", Status: "failed", Content: "second undelivered answer"}
	if err := store.SetTurnCheckpoint("review", session.TurnCheckpoint{TurnID: "old-turn", Delivery: &second, Deliveries: []session.TurnDelivery{first, second}}); err != nil {
		t.Fatal(err)
	}
	agent := &AgentInstance{ID: "main", Sessions: store}
	ts := newTurnState(agent, normalizeProcessOptions(makeTestProcessOpts("review")), turnEventScope{turnID: "new-turn", context: newTurnContext(nil, nil, nil)})
	ts.al = &AgentLoop{}
	if err := ts.saveTurnCheckpoint("running", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetTurnCheckpoint("review")
	if deliveries := checkpointDeliveries(got); len(deliveries) != 2 {
		t.Fatalf("outbox lost entries after checkpoint: got=%+v", deliveries)
	}
}
