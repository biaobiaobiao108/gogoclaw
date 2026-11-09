package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

type orderedLoopTool struct {
	name  string
	order *[]string
}

type repeatingLoopProvider struct {
	calls int
}

func (p *repeatingLoopProvider) GetDefaultModel() string { return "test" }
func (p *repeatingLoopProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: "repeat", Name: "repeat", Arguments: map[string]any{},
	}}}, nil
}

type nilResultLoopTool struct{}

func (*nilResultLoopTool) Name() string               { return "nil_result" }
func (*nilResultLoopTool) Description() string        { return "Returns no result" }
func (*nilResultLoopTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (*nilResultLoopTool) Execute(context.Context, map[string]any) *ToolResult {
	return nil
}

type nilResultLoopProvider struct {
	calls    int
	messages []providers.Message
}

func (p *nilResultLoopProvider) GetDefaultModel() string { return "test" }
func (p *nilResultLoopProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "nil", Name: "nil_result"}}}, nil
	}
	p.messages = append([]providers.Message(nil), messages...)
	return &providers.LLMResponse{Content: "handled", FinishReason: "stop"}, nil
}

func (t *orderedLoopTool) Name() string               { return t.name }
func (t *orderedLoopTool) Description() string        { return "Records execution order" }
func (t *orderedLoopTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (t *orderedLoopTool) Execute(_ context.Context, _ map[string]any) *ToolResult {
	*t.order = append(*t.order, t.name)
	return SilentResult(t.name)
}

type orderedLoopProvider struct {
	calls    int
	messages []providers.Message
}

func (p *orderedLoopProvider) GetDefaultModel() string { return "test" }
func (p *orderedLoopProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{
			{ID: "first", Name: "first", Arguments: map[string]any{}},
			{ID: "second", Name: "second", Arguments: map[string]any{}},
		}}, nil
	}
	p.messages = append([]providers.Message(nil), messages...)
	return &providers.LLMResponse{Content: "done"}, nil
}

func TestRunToolLoopExecutesToolsInOrder(t *testing.T) {
	var order []string
	registry := NewToolRegistry()
	registry.Register(&orderedLoopTool{name: "first", order: &order})
	registry.Register(&orderedLoopTool{name: "second", order: &order})
	provider := &orderedLoopProvider{}
	_, err := RunToolLoop(context.Background(), ToolLoopConfig{Provider: provider, Tools: registry, MaxIterations: 2}, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("unexpected execution order: %v", order)
	}
	if len(provider.messages) != 3 || provider.messages[1].ToolCallID != "first" || provider.messages[2].ToolCallID != "second" {
		t.Fatalf("unexpected tool result order: %+v", provider.messages)
	}
}

func TestRunToolLoopReportsIterationLimit(t *testing.T) {
	var order []string
	registry := NewToolRegistry()
	registry.Register(&orderedLoopTool{name: "repeat", order: &order})
	provider := &repeatingLoopProvider{}

	_, err := RunToolLoop(
		context.Background(),
		ToolLoopConfig{Provider: provider, Tools: registry, MaxIterations: 2},
		nil,
		"",
		"",
	)
	if err == nil || !strings.Contains(err.Error(), "max iterations") {
		t.Fatalf("expected an iteration-limit error, got %v", err)
	}
	if provider.calls != 2 || len(order) != 2 {
		t.Fatalf("calls=%d tools=%d, want 2 each", provider.calls, len(order))
	}
}

func TestRunToolLoopRejectsMissingProvider(t *testing.T) {
	_, err := RunToolLoop(context.Background(), ToolLoopConfig{MaxIterations: 1}, nil, "", "")
	if err == nil || !strings.Contains(err.Error(), "provider is required") {
		t.Fatalf("expected provider validation error, got %v", err)
	}
}

func TestRunToolLoopHandlesNilToolResult(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&nilResultLoopTool{})
	provider := &nilResultLoopProvider{}

	result, err := RunToolLoop(
		context.Background(),
		ToolLoopConfig{Provider: provider, Tools: registry, MaxIterations: 2},
		nil,
		"",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "handled" {
		t.Fatalf("unexpected content %q", result.Content)
	}
	found := false
	for _, msg := range provider.messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "nil result") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("nil tool result was not converted to an explicit error")
	}
}
