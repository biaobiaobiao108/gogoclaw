package tools

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

type orderedLoopTool struct {
	name  string
	order *[]string
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
