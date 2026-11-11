package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

// RecordingProvider wraps a provider and records deterministic responses for
// later replay. It records through the same ModelCall shape used by traces.
type RecordingProvider struct {
	inner providers.LLMProvider
	mu    sync.Mutex
	calls []ModelCall
}

func NewRecordingProvider(inner providers.LLMProvider) *RecordingProvider {
	return &RecordingProvider{inner: inner}
}

func (p *RecordingProvider) GetDefaultModel() string {
	if p == nil || p.inner == nil {
		return "harness-model"
	}
	return p.inner.GetDefaultModel()
}

func (p *RecordingProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	started := time.Now().UTC()
	response, err := p.inner.Chat(ctx, messages, toolDefs, model, options)
	p.mu.Lock()
	call := ModelCall{
		Index:     len(p.calls) + 1,
		Model:     model,
		Messages:  cloneMessages(messages),
		ToolNames: toolNames(toolDefs),
		Tools:     cloneToolDefinitions(toolDefs),
		Options:   cloneStringAnyMap(options),
		Response:  cloneLLMResponse(response),
		At:        started,
		Duration:  time.Since(started),
	}
	if err != nil {
		call.Error = err.Error()
	}
	p.calls = append(p.calls, call)
	p.mu.Unlock()
	return response, err
}

func (p *RecordingProvider) Calls() []ModelCall {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ModelCall, len(p.calls))
	for i, call := range p.calls {
		call.Messages = cloneMessages(call.Messages)
		call.ToolNames = append([]string(nil), call.ToolNames...)
		call.Tools = cloneToolDefinitions(call.Tools)
		call.Options = cloneStringAnyMap(call.Options)
		call.Response = cloneLLMResponse(call.Response)
		out[i] = call
	}
	return out
}

func (p *RecordingProvider) Save(path string) error {
	trace := Trace{Version: traceVersion, ModelCalls: snapshotModelCalls(p.Calls())}
	return WriteTrace(path, trace)
}

// ReplayProvider returns previously recorded model responses in order.
type ReplayProvider struct {
	model   string
	mu      sync.Mutex
	records []TraceModelCall
	next    int
}

func NewReplayProvider(model string, trace Trace) *ReplayProvider {
	return &ReplayProvider{
		model:   model,
		records: append([]TraceModelCall(nil), trace.ModelCalls...),
	}
}

func (p *ReplayProvider) GetDefaultModel() string {
	if p == nil || p.model == "" {
		return "harness-model"
	}
	return p.model
}

func (p *ReplayProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	model string,
	toolOptions map[string]any,
) (*providers.LLMResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next >= len(p.records) {
		return nil, fmt.Errorf("harness: replay provider exhausted at call %d", p.next+1)
	}
	record := p.records[p.next]
	p.next++
	if record.Model != "" && model != "" && record.Model != model {
		return nil, fmt.Errorf("harness: replay model = %s, want %s", model, record.Model)
	}
	wantInput, err := json.Marshal(replayInputMessages(record.Messages))
	if err != nil {
		return nil, fmt.Errorf("harness: encode replay input: %w", err)
	}
	gotInput, err := json.Marshal(replayInputMessages(snapshotMessages(messages)))
	if err != nil {
		return nil, fmt.Errorf("harness: encode current input: %w", err)
	}
	if !bytes.Equal(gotInput, wantInput) {
		return nil, fmt.Errorf("harness: replay input messages differ at call %d", p.next)
	}
	if record.Tools != nil {
		wantTools, err := json.Marshal(record.Tools)
		if err != nil {
			return nil, fmt.Errorf("harness: encode recorded tools: %w", err)
		}
		gotTools, err := json.Marshal(cloneToolDefinitions(toolDefs))
		if err != nil {
			return nil, fmt.Errorf("harness: encode current tools: %w", err)
		}
		if !bytes.Equal(gotTools, wantTools) {
			return nil, fmt.Errorf("harness: replay tool definitions at call %d differ", p.next)
		}
	} else {
		wantTools, _ := json.Marshal(record.ToolNames)
		gotTools, _ := json.Marshal(toolNames(toolDefs))
		if !bytes.Equal(gotTools, wantTools) {
			return nil, fmt.Errorf("harness: replay tools at call %d differ", p.next)
		}
	}
	if record.Options != nil {
		wantOptions, err := json.Marshal(record.Options)
		if err != nil {
			return nil, fmt.Errorf("harness: encode recorded options: %w", err)
		}
		gotOptions, err := json.Marshal(cloneStringAnyMap(toolOptions))
		if err != nil {
			return nil, fmt.Errorf("harness: encode current options: %w", err)
		}
		if !bytes.Equal(gotOptions, wantOptions) {
			return nil, fmt.Errorf("harness: replay tool options at call %d differ", p.next)
		}
	}
	if record.Error != "" {
		return nil, errors.New(record.Error)
	}
	if record.Response == nil {
		return nil, errors.New("harness: replay response is missing")
	}
	response := &providers.LLMResponse{
		Content:          record.Response.Content,
		ReasoningContent: record.Response.ReasoningContent,
		FinishReason:     record.Response.FinishReason,
		ToolCalls:        providerToolCalls(record.Response.ToolCalls),
	}
	if record.Response.Usage != nil {
		usage := *record.Response.Usage
		response.Usage = &usage
	}
	return response, nil
}

// Machine-specific system prompts may contain paths and runtime facts. Replay
// validates the conversational context and tool surface.
func replayInputMessages(messages []TraceMessage) []TraceMessage {
	var input []TraceMessage
	for _, message := range messages {
		if message.Role != "system" {
			input = append(input, message)
		}
	}
	return input
}

func providerToolCalls(calls []TraceToolCall) []providers.ToolCall {
	out := make([]providers.ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, ToolCall(call.CallID, call.Name, cloneArgs(call.Arguments)))
	}
	return out
}
