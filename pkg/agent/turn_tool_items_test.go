package agent

import (
	"context"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

func TestRunTurnPersistsToolLifecycleItems(t *testing.T) {
	provider := &sequenceProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{
			ID: "write-1", Name: "write_note", Arguments: map[string]any{"path": "note.md"},
		}}},
		{Content: "done"},
	}}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	tool := &countingHarnessTool{
		name:   "write_note",
		result: func(map[string]any) *tools.ToolResult { return tools.SilentResult("saved") },
	}
	agent.Tools.Register(tool)
	ts := newTurnState(agent, makeTestProcessOpts("tool-items"), turnEventScope{
		turnID: "tool-items", context: newTurnContext(nil, nil, nil),
	})

	if _, err := al.runTurn(context.Background(), ts, NewPipeline(al)); err != nil {
		t.Fatal(err)
	}

	store, ok := agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		t.Fatal("session store does not expose turn checkpoints")
	}
	checkpoint, err := store.GetTurnCheckpoint(ts.sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.ToolItems) != 1 {
		t.Fatalf("tool items = %+v, want one item", checkpoint.ToolItems)
	}
	item := checkpoint.ToolItems[0]
	if item.BatchID == "" || item.CallID != "write-1" || item.Tool != "write_note" {
		t.Fatalf("unexpected tool item identity: %+v", item)
	}
	if item.Status != toolItemStatusCompleted || item.SideEffect != toolItemSideEffectPossible {
		t.Fatalf("unexpected tool item outcome: %+v", item)
	}
	if item.ArgumentsFingerprint == "" || item.ResultSummary == "" || item.CompletedAt == nil {
		t.Fatalf("tool item is missing lifecycle data: %+v", item)
	}
}
