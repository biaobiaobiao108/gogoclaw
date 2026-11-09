// Package harness provides trajectory-level evaluation for GoGoClaw agent runs.
// It records model calls, tool execution order, runtime events, and final state,
// and supports deterministic fault injection through scripted providers/tools.
package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

// Scenario is one deterministic agent run.
type Scenario struct {
	Name              string
	UserMessage       string
	SessionKey        string
	Model             string
	ContextWindow     int
	MaxTokens         int
	MaxToolIterations int
	Timeout           time.Duration
	Steps             []ModelStep
	Tools             []tools.Tool
}

// ModelStep scripts one provider response or fault.
type ModelStep struct {
	Content        string
	ToolCalls      []providers.ToolCall
	Err            error
	Delay          time.Duration
	Panic          any
	WaitForContext bool
}

// Text returns a final-answer step.
func Text(content string) ModelStep {
	return ModelStep{Content: content}
}

// Calls returns a tool-calling step.
func Calls(calls ...providers.ToolCall) ModelStep {
	return ModelStep{ToolCalls: calls}
}

// Fail returns a provider fault step.
func Fail(err error) ModelStep {
	return ModelStep{Err: err}
}

// Panic returns a provider panic step.
func Panic(value any) ModelStep {
	return ModelStep{Panic: value}
}

// ToolCall builds a provider tool call.
func ToolCall(id, name string, args map[string]any) providers.ToolCall {
	return providers.ToolCall{
		ID:        id,
		Name:      name,
		Arguments: args,
		Function:  &providers.FunctionCall{Name: name, Arguments: ""},
	}
}

// ModelCall is one recorded provider invocation.
type ModelCall struct {
	Index     int
	Model     string
	Messages  []providers.Message
	ToolNames []string
	At        time.Time
}

// ToolCallRecord is one observed tool execution.
type ToolCallRecord struct {
	Name      string
	Arguments map[string]any
	At        time.Time
}

// Result captures the complete trajectory of a scenario.
type Result struct {
	Name       string
	Content    string
	Err        error
	Duration   time.Duration
	ModelCalls []ModelCall
	ToolCalls  []ToolCallRecord
	Events     []runtimeevents.Event
	TurnEnd    *agent.TurnEndPayload
	Outbound   []string
	History    []providers.Message
}

// Expect is a declarative trajectory assertion.
type Expect struct {
	Final         string
	FinalContains []string
	ErrorContains string
	NoError       bool
	ModelCalls    int
	MinModelCalls int
	MaxModelCalls int
	Tools         []string
	NoTools       []string
	TurnStatus    agent.TurnEndStatus
	Phases        []agent.TurnPhase
	Events        []runtimeevents.Kind
	EventSequence []runtimeevents.Kind
}

// ScriptedProvider returns ModelStep responses in order and records every call.
type ScriptedProvider struct {
	mu    sync.Mutex
	model string
	steps []ModelStep
	next  int
	calls []ModelCall
}

func NewScriptedProvider(model string, steps ...ModelStep) *ScriptedProvider {
	return &ScriptedProvider{model: model, steps: append([]ModelStep(nil), steps...)}
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
	p.mu.Unlock()

	if step.Delay > 0 {
		select {
		case <-time.After(step.Delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if step.WaitForContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if step.Panic != nil {
		panic(step.Panic)
	}
	if step.Err != nil {
		return nil, step.Err
	}
	return &providers.LLMResponse{
		Content:   step.Content,
		ToolCalls: append([]providers.ToolCall(nil), step.ToolCalls...),
	}, nil
}

// Calls returns the recorded provider calls.
func (p *ScriptedProvider) Calls() []ModelCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ModelCall, len(p.calls))
	copy(out, p.calls)
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

func (t *ToolStub) Name() string               { return t.name }
func (t *ToolStub) Description() string        { return t.description }
func (t *ToolStub) Parameters() map[string]any { return t.parameters }

func (t *ToolStub) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	t.mu.Lock()
	index := len(t.calls) + 1
	t.calls = append(t.calls, ToolCallRecord{Name: t.name, Arguments: cloneArgs(args), At: time.Now().UTC()})
	t.mu.Unlock()
	if t.handler == nil {
		return tools.SilentResult(t.name + " completed")
	}
	return t.handler(ctx, args, index)
}

func (t *ToolStub) Calls() []ToolCallRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]ToolCallRecord, len(t.calls))
	copy(out, t.calls)
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

// Run executes a scenario and returns its trajectory.
func Run(ctx context.Context, scenario Scenario) (*Result, error) {
	if strings.TrimSpace(scenario.Name) == "" {
		return nil, errors.New("harness: scenario name is required")
	}
	if strings.TrimSpace(scenario.UserMessage) == "" {
		return nil, errors.New("harness: user message is required")
	}
	if len(scenario.Steps) == 0 {
		return nil, errors.New("harness: at least one model step is required")
	}

	timeout := scenario.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	workspace, err := os.MkdirTemp("", "gogoclaw-harness-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workspace)

	model := scenario.Model
	if model == "" {
		model = "harness-model"
	}
	provider := NewScriptedProvider(model, scenario.Steps...)
	msgBus := bus.NewMessageBus()
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
		Workspace:         workspace,
		ModelName:         model,
		MaxTokens:         scenario.MaxTokens,
		ContextWindow:     scenario.ContextWindow,
		MaxToolIterations: scenario.MaxToolIterations,
	}}}
	if cfg.Agents.Defaults.MaxTokens <= 0 {
		cfg.Agents.Defaults.MaxTokens = 4096
	}
	if cfg.Agents.Defaults.ContextWindow <= 0 {
		cfg.Agents.Defaults.ContextWindow = 32768
	}
	if cfg.Agents.Defaults.MaxToolIterations <= 0 {
		cfg.Agents.Defaults.MaxToolIterations = 8
	}

	loop := agent.NewAgentLoop(cfg, msgBus, provider)
	defer loop.Close()
	for _, tool := range scenario.Tools {
		loop.RegisterTool(tool)
	}

	eventsCh, stopEvents, err := subscribe(ctx, loop, 512)
	if err != nil {
		return nil, err
	}
	defer stopEvents()

	var (
		mu       sync.Mutex
		events   []runtimeevents.Event
		outbound []string
	)
	turnEnd := make(chan struct{}, 1)
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		for evt := range eventsCh {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
			if evt.Kind == runtimeevents.KindAgentTurnEnd {
				select {
				case turnEnd <- struct{}{}:
				default:
				}
			}
		}
	}()
	outboundDone := make(chan struct{})
	go func() {
		defer close(outboundDone)
		for msg := range msgBus.OutboundChan() {
			mu.Lock()
			outbound = append(outbound, msg.Content)
			mu.Unlock()
		}
	}()

	sessionKey := strings.TrimSpace(scenario.SessionKey)
	if sessionKey == "" {
		sessionKey = "agent:main:harness:" + sanitizeSessionKey(scenario.Name)
	}
	started := time.Now()
	content, runErr := loop.ProcessDirectWithChannel(runCtx, scenario.UserMessage, sessionKey, "harness", "harness")
	duration := time.Since(started)

	select {
	case <-turnEnd:
	case <-time.After(time.Second):
	}
	stopEvents()
	<-eventsDone
	msgBus.Close()
	<-outboundDone

	mu.Lock()
	traceEvents := append([]runtimeevents.Event(nil), events...)
	traceOutbound := append([]string(nil), outbound...)
	mu.Unlock()

	result := &Result{
		Name:       scenario.Name,
		Content:    content,
		Err:        runErr,
		Duration:   duration,
		ModelCalls: provider.Calls(),
		ToolCalls:  toolCallsFromEvents(traceEvents),
		Events:     traceEvents,
		Outbound:   traceOutbound,
	}
	for i := len(traceEvents) - 1; i >= 0; i-- {
		if traceEvents[i].Kind != runtimeevents.KindAgentTurnEnd {
			continue
		}
		if payload, ok := traceEvents[i].Payload.(agent.TurnEndPayload); ok {
			copy := payload
			result.TurnEnd = &copy
		}
		break
	}
	if defaultAgent := loop.GetRegistry().GetDefaultAgent(); defaultAgent != nil && defaultAgent.Sessions != nil {
		result.History = defaultAgent.Sessions.GetHistory(sessionKey)
	}
	return result, nil
}

func subscribe(ctx context.Context, loop *agent.AgentLoop, buffer int) (<-chan runtimeevents.Event, func(), error) {
	channel := loop.RuntimeEvents()
	if channel == nil {
		return nil, nil, errors.New("harness: runtime event bus is unavailable")
	}
	sub, events, err := channel.SubscribeChan(ctx, runtimeevents.SubscribeOptions{
		Name:         "harness",
		Buffer:       buffer,
		Concurrency:  runtimeevents.Locked,
		Backpressure: runtimeevents.Block,
	})
	if err != nil {
		return nil, nil, err
	}
	var once sync.Once
	stop := func() { once.Do(func() { _ = sub.Close() }) }
	return events, stop, nil
}

// ToolSequence returns the executed tool names in order.
func (r *Result) ToolSequence() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.ToolCalls))
	for _, call := range r.ToolCalls {
		out = append(out, call.Name)
	}
	return out
}

// EventKinds returns the observed runtime event kinds in order.
func (r *Result) EventKinds() []runtimeevents.Kind {
	if r == nil {
		return nil
	}
	out := make([]runtimeevents.Kind, 0, len(r.Events))
	for _, evt := range r.Events {
		out = append(out, evt.Kind)
	}
	return out
}

// PhaseSequence returns the accepted turn lifecycle transitions in order.
func (r *Result) PhaseSequence() []agent.TurnPhase {
	if r == nil || r.TurnEnd == nil || len(r.TurnEnd.PhaseHistory) == 0 {
		return nil
	}
	out := make([]agent.TurnPhase, 0, len(r.TurnEnd.PhaseHistory)+1)
	out = append(out, r.TurnEnd.PhaseHistory[0].From)
	for _, transition := range r.TurnEnd.PhaseHistory {
		out = append(out, transition.To)
	}
	return out
}

// Verify evaluates a declarative expectation and returns all mismatches.
func (r *Result) Verify(expect Expect) error {
	if r == nil {
		return errors.New("harness: nil result")
	}
	var problems []string
	if expect.Final != "" && r.Content != expect.Final {
		problems = append(problems, fmt.Sprintf("final content = %q, want %q", r.Content, expect.Final))
	}
	for _, want := range expect.FinalContains {
		if !strings.Contains(r.Content, want) {
			problems = append(problems, fmt.Sprintf("final content does not contain %q", want))
		}
	}
	if expect.NoError && r.Err != nil {
		problems = append(problems, fmt.Sprintf("unexpected error: %v", r.Err))
	}
	if expect.ErrorContains != "" && (r.Err == nil || !strings.Contains(r.Err.Error(), expect.ErrorContains)) {
		problems = append(problems, fmt.Sprintf("error = %v, want substring %q", r.Err, expect.ErrorContains))
	}
	if expect.ModelCalls > 0 && len(r.ModelCalls) != expect.ModelCalls {
		problems = append(problems, fmt.Sprintf("model calls = %d, want %d", len(r.ModelCalls), expect.ModelCalls))
	}
	if expect.MinModelCalls > 0 && len(r.ModelCalls) < expect.MinModelCalls {
		problems = append(problems, fmt.Sprintf("model calls = %d, want at least %d", len(r.ModelCalls), expect.MinModelCalls))
	}
	if expect.MaxModelCalls > 0 && len(r.ModelCalls) > expect.MaxModelCalls {
		problems = append(problems, fmt.Sprintf("model calls = %d, want at most %d", len(r.ModelCalls), expect.MaxModelCalls))
	}
	if expect.Tools != nil {
		got := r.ToolSequence()
		if len(got) != len(expect.Tools) {
			problems = append(problems, fmt.Sprintf("tool sequence = %v, want %v", got, expect.Tools))
		} else {
			for i := range got {
				if got[i] != expect.Tools[i] {
					problems = append(problems, fmt.Sprintf("tool sequence = %v, want %v", got, expect.Tools))
					break
				}
			}
		}
	}
	for _, forbidden := range expect.NoTools {
		for _, name := range r.ToolSequence() {
			if name == forbidden {
				problems = append(problems, fmt.Sprintf("forbidden tool %q executed", forbidden))
				break
			}
		}
	}
	if expect.TurnStatus != "" {
		if r.TurnEnd == nil {
			problems = append(problems, "missing turn end event")
		} else if r.TurnEnd.Status != expect.TurnStatus {
			problems = append(problems, fmt.Sprintf("turn status = %s, want %s", r.TurnEnd.Status, expect.TurnStatus))
		}
	}
	if expect.Phases != nil {
		got := r.PhaseSequence()
		if len(got) != len(expect.Phases) {
			problems = append(problems, fmt.Sprintf("phase sequence = %v, want %v", got, expect.Phases))
		} else {
			for i := range got {
				if got[i] != expect.Phases[i] {
					problems = append(problems, fmt.Sprintf("phase sequence = %v, want %v", got, expect.Phases))
					break
				}
			}
		}
	}
	for _, kind := range expect.Events {
		if !r.hasEvent(kind) {
			problems = append(problems, fmt.Sprintf("missing event %s", kind))
		}
	}
	if len(expect.EventSequence) > 0 && !containsSubsequence(r.EventKinds(), expect.EventSequence) {
		problems = append(problems, fmt.Sprintf("event kinds %v do not contain %v", r.EventKinds(), expect.EventSequence))
	}
	if len(problems) > 0 {
		return fmt.Errorf("harness %s failed: %s", r.Name, strings.Join(problems, "; "))
	}
	return nil
}

func (r *Result) hasEvent(kind runtimeevents.Kind) bool {
	for _, evt := range r.Events {
		if evt.Kind == kind {
			return true
		}
	}
	return false
}

func toolCallsFromEvents(events []runtimeevents.Event) []ToolCallRecord {
	var out []ToolCallRecord
	for _, evt := range events {
		if evt.Kind != runtimeevents.KindAgentToolExecStart {
			continue
		}
		if payload, ok := evt.Payload.(agent.ToolExecStartPayload); ok {
			out = append(out, ToolCallRecord{
				Name:      payload.Tool,
				Arguments: cloneArgs(payload.Arguments),
				At:        evt.Time,
			})
		}
	}
	return out
}

func cloneMessages(messages []providers.Message) []providers.Message {
	out := make([]providers.Message, len(messages))
	for i, msg := range messages {
		msg.Media = append([]string(nil), msg.Media...)
		msg.Attachments = append([]providers.Attachment(nil), msg.Attachments...)
		msg.ToolCalls = append([]providers.ToolCall(nil), msg.ToolCalls...)
		out[i] = msg
	}
	return out
}

func cloneArgs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	out := make(map[string]any, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
}

func toolNames(defs []providers.ToolDefinition) []string {
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.Function.Name)
	}
	return out
}

func sanitizeSessionKey(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return "scenario"
	}
	return builder.String()
}

func containsSubsequence(got, want []runtimeevents.Kind) bool {
	if len(want) == 0 {
		return true
	}
	index := 0
	for _, kind := range got {
		if kind == want[index] {
			index++
			if index == len(want) {
				return true
			}
		}
	}
	return false
}
