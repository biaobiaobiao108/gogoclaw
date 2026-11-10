package harness

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

// ScriptedProvider returns ModelStep responses in order and records every call.
type ScriptedProvider struct {
	mu      sync.Mutex
	model   string
	steps   []ModelStep
	next    int
	calls   []ModelCall
	runtime ToolRuntime
}

func NewScriptedProvider(model string, steps ...ModelStep) *ScriptedProvider {
	return &ScriptedProvider{model: model, steps: append([]ModelStep(nil), steps...)}
}

func (p *ScriptedProvider) bindRuntime(rt ToolRuntime) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runtime = rt
}

func (p *ScriptedProvider) GetDefaultModel() string {
	if p == nil || p.model == "" {
		return "harness-model"
	}
	return p.model
}

func (p *ScriptedProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	model string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	index := p.next
	if index >= len(p.steps) {
		p.mu.Unlock()
		return nil, fmt.Errorf("harness: scripted provider exhausted at call %d", index+1)
	}
	step := p.steps[index]
	p.next++
	call := ModelCall{
		Index:     index + 1,
		Model:     model,
		Messages:  cloneMessages(messages),
		ToolNames: toolNames(toolDefs),
		At:        time.Now().UTC(),
	}
	p.calls = append(p.calls, call)
	runtime := p.runtime
	p.mu.Unlock()

	if step.InjectSteering != "" && runtime.Loop != nil && runtime.SessionKey != "" {
		_ = runtime.Loop.SteerForSession(runtime.SessionKey, providers.Message{
			Role:    "user",
			Content: step.InjectSteering,
		})
	}
	if step.InterruptKind == "graceful" && runtime.Loop != nil && runtime.SessionKey != "" {
		_ = runtime.Loop.InterruptGracefulForSession(runtime.SessionKey, step.InterruptHint)
	} else if step.InterruptKind == "hard" && runtime.Loop != nil && runtime.SessionKey != "" {
		_ = runtime.Loop.HardAbort(runtime.SessionKey)
	}
	if step.OnCall != nil {
		if err := step.OnCall(runtime); err != nil {
			p.finishCall(index, nil, err)
			return nil, err
		}
	}

	if step.Delay > 0 {
		timer := time.NewTimer(step.Delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			p.finishCall(index, nil, ctx.Err())
			return nil, ctx.Err()
		}
	}
	if step.WaitForContext {
		<-ctx.Done()
		p.finishCall(index, nil, ctx.Err())
		return nil, ctx.Err()
	}
	if step.Panic != nil {
		p.finishCall(index, nil, fmt.Errorf("panic: %v", step.Panic))
		panic(step.Panic)
	}
	if step.Err != nil {
		p.finishCall(index, nil, step.Err)
		return nil, step.Err
	}
	response := step.Response
	if response == nil {
		response = &providers.LLMResponse{
			Content:   step.Content,
			ToolCalls: append([]providers.ToolCall(nil), step.ToolCalls...),
		}
	}
	p.finishCall(index, cloneLLMResponse(response), nil)
	return cloneLLMResponse(response), nil
}

func (p *ScriptedProvider) finishCall(index int, response *providers.LLMResponse, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.calls) {
		return
	}
	p.calls[index].Response = response
	p.calls[index].Duration = time.Since(p.calls[index].At)
	if err != nil {
		p.calls[index].Error = err.Error()
	}
}

// Calls returns the recorded provider calls.
func (p *ScriptedProvider) Calls() []ModelCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ModelCall, len(p.calls))
	for i, call := range p.calls {
		call.Messages = cloneMessages(call.Messages)
		call.ToolNames = append([]string(nil), call.ToolNames...)
		call.Response = cloneLLMResponse(call.Response)
		out[i] = call
	}
	return out
}

// ToolHandler handles one scripted tool call. call is 1-based.
type ToolHandler func(ctx context.Context, args map[string]any, call int) *tools.ToolResult

// ToolStub is a deterministic tool with optional faults.
type ToolStub struct {
	name        string
	description string
	parameters  map[string]any
	handler     ToolHandler
	mu          sync.Mutex
	calls       []ToolCallRecord
}

func NewToolStub(name string, handler ToolHandler) *ToolStub {
	return &ToolStub{
		name:        name,
		description: "harness tool " + name,
		parameters:  map[string]any{"type": "object", "additionalProperties": true},
		handler:     handler,
	}
}

func (t *ToolStub) Name() string { return t.name }

func (t *ToolStub) Description() string { return t.description }

func (t *ToolStub) Parameters() map[string]any { return t.parameters }

func (t *ToolStub) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	t.mu.Lock()
	index := len(t.calls) + 1
	call := ToolCallRecord{
		CallID:    tools.ToolCallID(ctx),
		Name:      t.name,
		Arguments: cloneArgs(args),
		Status:    "running",
		At:        time.Now().UTC(),
	}
	t.calls = append(t.calls, call)
	t.mu.Unlock()
	start := time.Now()
	var result *tools.ToolResult
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.mu.Lock()
				t.calls[index-1].Duration = time.Since(start)
				t.calls[index-1].Status = "error"
				t.calls[index-1].IsError = true
				t.calls[index-1].Error = fmt.Sprintf("panic: %v", recovered)
				t.mu.Unlock()
				panic(recovered)
			}
		}()
		if t.handler == nil {
			result = tools.SilentResult(t.name + " completed")
		} else {
			result = t.handler(ctx, args, index)
		}
	}()
	if result == nil {
		result = tools.ErrorResult("harness tool returned nil result")
	}
	t.mu.Lock()
	t.calls[index-1].Duration = time.Since(start)
	t.calls[index-1].Status = "completed"
	t.calls[index-1].IsError = result.IsError
	t.calls[index-1].Result = result.ContentForLLM()
	if result.IsError {
		t.calls[index-1].Status = "error"
		t.calls[index-1].Error = toolResultError(result)
	}
	t.mu.Unlock()
	return result
}

func (t *ToolStub) Calls() []ToolCallRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]ToolCallRecord, len(t.calls))
	for i, call := range t.calls {
		call.Arguments = cloneArgs(call.Arguments)
		out[i] = call
	}
	return out
}

func TextTool(name, content string) *ToolStub {
	return NewToolStub(name, func(context.Context, map[string]any, int) *tools.ToolResult {
		return tools.SilentResult(content)
	})
}

func ErrorTool(name, message string) *ToolStub {
	return NewToolStub(name, func(context.Context, map[string]any, int) *tools.ToolResult {
		return tools.ErrorResult(message)
	})
}

func PanicTool(name string, value any) *ToolStub {
	return NewToolStub(name, func(context.Context, map[string]any, int) *tools.ToolResult {
		panic(value)
	})
}
