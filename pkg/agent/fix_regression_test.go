package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

func TestRunTurnCompletesLifecycleOnNormalReturn(t *testing.T) {
	provider := &sequenceProvider{responses: []*providers.LLMResponse{{Content: "done"}}}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()

	opts := normalizeProcessOptions(makeTestProcessOpts("finish-lifecycle"))
	opts.Dispatch = DispatchRequest{SessionKey: "finish-lifecycle", UserMessage: "task"}
	ts := newTurnState(agent, opts, turnEventScope{
		turnID:  "finish-lifecycle",
		context: newTurnContext(nil, nil, nil),
	})
	if _, err := al.runTurn(context.Background(), ts, NewPipeline(al)); err != nil {
		t.Fatal(err)
	}
	if !ts.isFinished.Load() {
		t.Fatal("turn was not marked finished")
	}
	if !ts.parentEnded.Load() {
		t.Fatal("parentEnded was not set on normal completion")
	}
	select {
	case <-ts.Finished():
	default:
		t.Fatal("finished channel was not closed")
	}
	if al.GetActiveTurnBySession(ts.sessionKey) != nil {
		t.Fatal("completed turn remained active")
	}
}

func TestFinishHardAbortAlsoMarksParentEnded(t *testing.T) {
	ts := &turnState{turnID: "hard-finish"}
	ts.Finish(true)
	if !ts.parentEnded.Load() {
		t.Fatal("hard abort must also signal parentEnded")
	}
	ts.Finish(false)
	if !ts.isFinished.Load() {
		t.Fatal("finish state was lost")
	}
}

func TestSteerRejectsAmbiguousActiveTurns(t *testing.T) {
	al, _, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()

	al.activeTurnStates.Store("one", &turnState{turnID: "one", sessionKey: "one", agentID: "main"})
	al.activeTurnStates.Store("two", &turnState{turnID: "two", sessionKey: "two", agentID: "main"})
	defer func() {
		al.activeTurnStates.Delete("one")
		al.activeTurnStates.Delete("two")
	}()

	err := al.Steer(providers.Message{Role: "user", Content: "change course"})
	if err == nil || !strings.Contains(err.Error(), "multiple turns") {
		t.Fatalf("Steer() error = %v, want ambiguity error", err)
	}
	if err := al.SteerForSession("one", providers.Message{Role: "user", Content: "change course"}); err != nil {
		t.Fatal(err)
	}
	if got := al.steering.lenScope("one"); got != 1 {
		t.Fatalf("session-scoped steering queue = %d, want 1", got)
	}
}

func TestTrimMessagesToRuneLimitKeepsRecentTurn(t *testing.T) {
	messages := []providers.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: strings.Repeat("old", 100)},
		{Role: "assistant", Content: strings.Repeat("answer", 100)},
		{Role: "user", Content: "latest task"},
	}
	got := trimMessagesToRuneLimit(messages, 30)
	if len(got) != 2 || got[0].Role != "system" || got[1].Content != "latest task" {
		t.Fatalf("unexpected trimmed messages: %+v", got)
	}
}

type captureSubTurnProvider struct {
	mu       sync.Mutex
	model    string
	options  map[string]any
	toolDefs []providers.ToolDefinition
}

func (p *captureSubTurnProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	toolDefs []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.model = model
	p.options = options
	p.toolDefs = append([]providers.ToolDefinition(nil), toolDefs...)
	return &providers.LLMResponse{Content: "subturn done"}, nil
}

func (p *captureSubTurnProvider) GetDefaultModel() string {
	return "base-model"
}

type captureOnlyTool struct{}

func (captureOnlyTool) Name() string               { return "capture_only" }
func (captureOnlyTool) Description() string        { return "capture only" }
func (captureOnlyTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (captureOnlyTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.SilentResult("ok")
}

func TestSpawnSubTurnAppliesOverrides(t *testing.T) {
	provider := &captureSubTurnProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()

	parent := &turnState{
		agent:            agent,
		al:               al,
		turnID:           "parent",
		agentID:          agent.ID,
		sessionKey:       "parent",
		originAgentID:    agent.ID,
		originSessionKey: "parent",
		concurrencySem:   make(chan struct{}, 2),
		pendingResults:   make(chan *tools.ToolResult, 4),
		opts:             normalizeProcessOptions(makeTestProcessOpts("parent")),
	}
	result, err := spawnSubTurn(context.Background(), al, parent, SubTurnConfig{
		Model:        "override-model",
		SystemPrompt: "do the task",
		MaxTokens:    123,
		Temperature:  0.4,
		Tools:        []tools.Tool{captureOnlyTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.ForLLM != "subturn done" {
		t.Fatalf("result = %+v", result)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.model != "override-model" {
		t.Fatalf("model = %q, want override-model", provider.model)
	}
	if provider.options["max_tokens"] != 123 {
		t.Fatalf("max_tokens = %v, want 123", provider.options["max_tokens"])
	}
	if provider.options["temperature"] != 0.4 {
		t.Fatalf("temperature = %v, want 0.4", provider.options["temperature"])
	}
	found := false
	for _, def := range provider.toolDefs {
		if def.Function.Name == "capture_only" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tool override not applied: %+v", provider.toolDefs)
	}
}

func TestWaitForProviderIdleIncludesWholeTurnReference(t *testing.T) {
	al, _, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()

	provider := al.registry.GetDefaultAgent().Provider
	al.acquireProviderRef(provider)
	released := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		al.releaseProviderRef(provider)
		close(released)
	}()
	if !al.waitForProviderIdle(context.Background(), provider, time.Second) {
		t.Fatal("provider never became idle")
	}
	<-released
}
