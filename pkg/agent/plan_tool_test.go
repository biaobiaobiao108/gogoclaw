package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestPlanToolUpdatesTurnAndRejectsMultipleActiveSteps(t *testing.T) {
	ts := &turnState{}
	tool := &turnPlanTool{turn: ts}
	steps := []any{
		map[string]any{"text": "Inspect", "status": "in_progress"},
		map[string]any{"text": "Implement", "status": "pending"},
	}
	if result := tool.Execute(context.Background(), map[string]any{"steps": steps}); result.IsError {
		t.Fatalf("plan update failed: %s", result.ForLLM)
	}
	if got := ts.snapshot().Plan; len(got) != 2 || got[0].Text != "Inspect" {
		t.Fatalf("unexpected plan: %+v", got)
	}
	steps[1] = map[string]any{"text": "Implement", "status": "in_progress"}
	if result := tool.Execute(context.Background(), map[string]any{"steps": steps}); !result.IsError {
		t.Fatal("expected invalid plan rejection")
	}
	if got := ts.snapshot().Plan; got[1].Status != "pending" {
		t.Fatalf("invalid update changed plan: %+v", got)
	}
}

func TestRunTurnPublishesPlanFromToolCall(t *testing.T) {
	provider := &sequenceProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "plan", Name: "update_plan", Arguments: map[string]any{
			"steps": []any{map[string]any{"text": "Inspect", "status": "in_progress"}},
		}}}},
		{Content: "done"},
	}}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	ts := newTurnState(agent, makeTestProcessOpts("plan"), turnEventScope{turnID: "plan", context: newTurnContext(nil, nil, nil)})
	if _, err := al.runTurn(context.Background(), ts, NewPipeline(al)); err != nil {
		t.Fatal(err)
	}
	if plan := ts.snapshot().Plan; len(plan) != 1 || plan[0].Text != "Inspect" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}
