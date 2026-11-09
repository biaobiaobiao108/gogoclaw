package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

func TestSwitchModelWhileParentHoldsReadLock(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()
	opts := makeTestProcessOpts("model-lock")
	rt := al.buildCommandsRuntime(context.Background(), agent, &opts)
	mu := agent.modelStateMutex()
	mu.RLock()
	done := make(chan error, 1)
	go func() {
		_, err := rt.SwitchModel("test-model")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "busy") {
			mu.RUnlock()
			t.Fatalf("expected busy error, got %v", err)
		}
	case <-time.After(time.Second):
		mu.RUnlock()
		<-done
		t.Fatal("model switch blocked behind parent read lock")
	}
	// A child must still be able to acquire its read lock.
	mu.RLock()
	mu.RUnlock()
	mu.RUnlock()
}

type followUpInjectionTool struct {
	loop  *AgentLoop
	calls int
}

func (*followUpInjectionTool) Name() string               { return "queue_follow_up" }
func (*followUpInjectionTool) Description() string        { return "Queue the next task" }
func (*followUpInjectionTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (tool *followUpInjectionTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	tool.calls++
	if tool.calls == 1 {
		if err := tool.loop.InjectFollowUp(providers.Message{Role: "user", Content: "next task"}); err != nil {
			return tools.ErrorResult(err.Error())
		}
	}
	return tools.SilentResult("done")
}

func TestFollowUpWaitsUntilToolBatchFinishes(t *testing.T) {
	provider := &toolCallProvider{finalResp: "finished", toolCalls: []providers.ToolCall{
		{ID: "first", Name: "queue_follow_up", Arguments: map[string]any{}},
		{ID: "second", Name: "queue_follow_up", Arguments: map[string]any{}},
	}}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	tool := &followUpInjectionTool{loop: al}
	al.RegisterTool(tool)
	const key = "follow-up-batch"
	if _, err := al.runAgentLoop(context.Background(), agent, makeTestProcessOpts(key)); err != nil {
		t.Fatal(err)
	}
	if tool.calls != 2 {
		t.Fatalf("follow-up interrupted batch: executed %d tools", tool.calls)
	}
	if al.followUpQueue.lenScope(key) != 1 || al.pendingSteeringCountForScope(key) != 0 {
		t.Fatal("follow-up was consumed during the active turn")
	}
	if _, err := al.drainQueuedSteeringContinuations(context.Background(), &continuationTarget{SessionKey: key, Channel: "cli", ChatID: "test-chat"}); err != nil {
		t.Fatal(err)
	}
	if al.followUpQueue.lenScope(key) != 0 {
		t.Fatal("idle continuation did not consume follow-up")
	}
}

func TestFollowUpStaysScopedUntilSessionIsIdle(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()
	const key = "follow-up-owner"
	active := newTurnState(agent, normalizeProcessOptions(makeTestProcessOpts(key)), turnEventScope{turnID: "active"})
	al.registerActiveTurn(active)
	defer al.clearActiveTurn(active)
	if err := al.InjectFollowUpForSession(key, providers.Message{Role: "user", Content: "later"}); err != nil {
		t.Fatal(err)
	}
	if _, err := al.Continue(context.Background(), key, "cli", "test-chat"); err == nil {
		t.Fatal("Continue accepted work while the session was active")
	}
	if _, err := al.Continue(context.Background(), "other", "cli", "test-chat"); err != nil {
		t.Fatal(err)
	}
	if al.followUpQueue.lenScope(key) != 1 {
		t.Fatal("another session consumed the follow-up")
	}
	al.clearActiveTurn(active)
	if _, err := al.Continue(context.Background(), key, "cli", "test-chat"); err != nil {
		t.Fatal(err)
	}
	if al.followUpQueue.lenScope(key) != 0 {
		t.Fatal("idle owner did not consume its follow-up")
	}
}
