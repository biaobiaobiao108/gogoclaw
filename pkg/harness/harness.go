// Package harness provides trajectory-level evaluation for GoGoClaw agent runs.
// It records model calls, tool execution order, runtime events, and final state,
// and supports deterministic fault injection through scripted providers/tools.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

// Scenario is one deterministic agent run.
type Scenario struct {
	Name                 string
	UserMessage          string
	Messages             []string
	SessionKey           string
	Model                string
	ContextWindow        int
	MaxTokens            int
	MaxToolIterations    int
	Timeout              time.Duration
	AsyncSettle          time.Duration
	MaxAsyncFollowUps    int
	ProcessFollowUps     bool
	CloseBusBeforeSettle bool
	Steps                []ModelStep
	Provider             providers.LLMProvider
	ReplayPath           string
	RecordPath           string
	Tools                []tools.Tool
	ToolFactory          func(ToolRuntime) ([]tools.Tool, error)
	Setup                func(*agent.AgentLoop) error
	Session              session.SessionStore
	SessionFault         *SessionFault
	Workspace            string
	Expectation          *Expect
}

// ToolRuntime gives scenario-specific tool factories access to the live loop
// and bus without weakening their production dependencies.
type ToolRuntime struct {
	Loop       *agent.AgentLoop
	Bus        *bus.MessageBus
	SessionKey string
}

// ModelStep scripts one provider response or fault.
type ModelStep struct {
	Content        string
	ToolCalls      []providers.ToolCall
	Response       *providers.LLMResponse
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
	Response  *providers.LLMResponse
	Error     string
	At        time.Time
}

// ToolCallRecord is one observed tool execution.
type ToolCallRecord struct {
	CallID    string
	Name      string
	Arguments map[string]any
	Status    string
	IsError   bool
	Error     string
	Result    string
	Duration  time.Duration
	At        time.Time
}

// TurnResult captures one inbound user turn within a scenario.
type TurnResult struct {
	UserMessage string
	Content     string
	Err         error
	Duration    time.Duration
	ModelCalls  []ModelCall
	ToolCalls   []ToolCallRecord
	Events      []runtimeevents.Event
	TurnEnd     *agent.TurnEndPayload
	History     []providers.Message
}

// Result captures the complete trajectory of a scenario.
type Result struct {
	Name           string
	Content        string
	Err            error
	Duration       time.Duration
	Turns          []TurnResult
	ModelCalls     []ModelCall
	ToolCalls      []ToolCallRecord
	Events         []runtimeevents.Event
	TurnEnd        *agent.TurnEndPayload
	Outbound       []string
	History        []providers.Message
	AsyncFollowUps []bus.InboundMessage
}

type ToolCallExpectation struct {
	CallID         string         `json:"call_id,omitempty"`
	Name           string         `json:"name,omitempty"`
	Arguments      map[string]any `json:"arguments,omitempty"`
	Status         string         `json:"status,omitempty"`
	IsError        *bool          `json:"is_error,omitempty"`
	ErrorContains  string         `json:"error_contains,omitempty"`
	ResultContains string         `json:"result_contains,omitempty"`
}

type ModelCallExpectation struct {
	Model     string   `json:"model,omitempty"`
	Contains  []string `json:"contains,omitempty"`
	ToolNames []string `json:"tool_names,omitempty"`
}

type EventExpectation struct {
	Kind            runtimeevents.Kind `json:"kind,omitempty"`
	PayloadContains []string           `json:"payload_contains,omitempty"`
	TurnID          string             `json:"turn_id,omitempty"`
}

// Expect is a declarative trajectory assertion.
type Expect struct {
	Final              string                 `json:"final,omitempty"`
	FinalExact         *string                `json:"final_exact,omitempty"`
	FinalContains      []string               `json:"final_contains,omitempty"`
	ErrorContains      string                 `json:"error_contains,omitempty"`
	NoError            bool                   `json:"no_error,omitempty"`
	ModelCalls         int                    `json:"model_calls,omitempty"`
	ModelCallsExact    *int                   `json:"model_calls_exact,omitempty"`
	MinModelCalls      int                    `json:"min_model_calls,omitempty"`
	MaxModelCalls      int                    `json:"max_model_calls,omitempty"`
	MaxModelCallsExact *int                   `json:"max_model_calls_exact,omitempty"`
	TurnCount          int                    `json:"turn_count,omitempty"`
	AsyncFollowUps     int                    `json:"async_follow_ups,omitempty"`
	MinAsyncFollowUps  int                    `json:"min_async_follow_ups,omitempty"`
	MaxAsyncFollowUps  *int                   `json:"max_async_follow_ups,omitempty"`
	FollowUpContains   []string               `json:"follow_up_contains,omitempty"`
	Tools              []string               `json:"tools,omitempty"`
	ToolCalls          []ToolCallExpectation  `json:"tool_calls,omitempty"`
	NoTools            []string               `json:"no_tools,omitempty"`
	TurnStatus         agent.TurnEndStatus    `json:"turn_status,omitempty"`
	TurnReason         agent.TurnEndReason    `json:"turn_reason,omitempty"`
	Phases             []agent.TurnPhase      `json:"phases,omitempty"`
	Events             []runtimeevents.Kind   `json:"events,omitempty"`
	EventSequence      []runtimeevents.Kind   `json:"event_sequence,omitempty"`
	ExactEventSequence []runtimeevents.Kind   `json:"exact_event_sequence,omitempty"`
	EventDetails       []EventExpectation     `json:"event_details,omitempty"`
	ModelDetails       []ModelCallExpectation `json:"model_details,omitempty"`
	GoldenTrace        string                 `json:"golden_trace,omitempty"`
	AllowInvalidTrace  bool                   `json:"allow_invalid_trace,omitempty"`
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

func (t *ToolStub) Name() string               { return t.name }
func (t *ToolStub) Description() string        { return t.description }
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

func validateScenario(scenario Scenario) error {
	if strings.TrimSpace(scenario.Name) == "" {
		return errors.New("harness: scenario name is required")
	}
	if len(scenarioMessages(scenario)) == 0 {
		return errors.New("harness: user message is required")
	}
	sources := 0
	if scenario.Provider != nil {
		sources++
	}
	if strings.TrimSpace(scenario.ReplayPath) != "" {
		sources++
	}
	if len(scenario.Steps) > 0 {
		sources++
	}
	if sources == 0 {
		return errors.New("harness: provider, replay_path, or model steps are required")
	}
	if sources > 1 {
		return errors.New("harness: provider, replay_path, and model steps are mutually exclusive")
	}
	if scenario.Timeout < 0 {
		return errors.New("harness: timeout cannot be negative")
	}
	if scenario.AsyncSettle < 0 {
		return errors.New("harness: async settle cannot be negative")
	}
	if scenario.MaxAsyncFollowUps < 0 {
		return errors.New("harness: max async follow-ups cannot be negative")
	}
	if scenario.CloseBusBeforeSettle && scenario.AsyncSettle <= 0 {
		return errors.New("harness: close_bus_before_settle requires async_settle")
	}
	return nil
}

// Run executes a scenario and returns its trajectory.
func Run(ctx context.Context, scenario Scenario) (*Result, error) {
	if err := validateScenario(scenario); err != nil {
		return nil, err
	}
	messages := scenarioMessages(scenario)

	timeout := scenario.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	workspace := strings.TrimSpace(scenario.Workspace)
	removeWorkspace := false
	if workspace == "" {
		var err error
		workspace, err = os.MkdirTemp("", "gogoclaw-harness-*")
		if err != nil {
			return nil, err
		}
		removeWorkspace = true
	} else if err := os.MkdirAll(workspace, 0o755); err != nil {
		return nil, fmt.Errorf("harness: create workspace: %w", err)
	}
	if removeWorkspace {
		defer os.RemoveAll(workspace)
	}

	model := scenario.Model
	if model == "" {
		model = "harness-model"
	}
	provider := scenario.Provider
	if strings.TrimSpace(scenario.ReplayPath) != "" {
		replayTrace, replayErr := LoadTrace(scenario.ReplayPath)
		if replayErr != nil {
			return nil, replayErr
		}
		provider = NewReplayProvider(model, replayTrace)
	}
	if provider == nil {
		provider = NewScriptedProvider(model, scenario.Steps...)
	}
	var recorder *RecordingProvider
	if strings.TrimSpace(scenario.RecordPath) != "" {
		recorder = NewRecordingProvider(provider)
		provider = recorder
	}
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
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
	sessionKey := harnessSessionKey(scenario)
	if err := configureScenarioSession(loop, workspace, scenario); err != nil {
		return nil, err
	}
	if scenario.Setup != nil {
		if err := scenario.Setup(loop); err != nil {
			return nil, fmt.Errorf("harness: scenario setup: %w", err)
		}
	}
	registeredTools := append([]tools.Tool(nil), scenario.Tools...)
	if scenario.ToolFactory != nil {
		created, factoryErr := scenario.ToolFactory(ToolRuntime{Loop: loop, Bus: msgBus, SessionKey: sessionKey})
		if factoryErr != nil {
			return nil, fmt.Errorf("harness: tool factory: %w", factoryErr)
		}
		registeredTools = append(registeredTools, created...)
	}
	for _, tool := range registeredTools {
		loop.RegisterTool(tool)
	}

	eventsCh, stopEvents, err := subscribe(ctx, loop, 1024)
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

	result := &Result{Name: scenario.Name}
	modelCursor := 0
	for _, userMessage := range messages {
		turn, nextModel, err := runScenarioTurn(
			ctx, runCtx, loop, provider, msgBus, scenario, registeredTools, userMessage, sessionKey,
			modelCursor, turnEnd, &mu, &events, &outbound,
		)
		modelCursor = nextModel
		if err != nil {
			stopEvents()
			<-eventsDone
			msgBus.Close()
			<-outboundDone
			return result, err
		}
		result.Turns = append(result.Turns, turn)
	}

	if scenario.AsyncSettle > 0 {
		if scenario.CloseBusBeforeSettle {
			msgBus.Close()
			<-outboundDone
		}
		if err := waitForContext(runCtx, scenario.AsyncSettle); err != nil {
			stopEvents()
			<-eventsDone
			msgBus.Close()
			<-outboundDone
			return result, err
		}
	}
	if err := waitAsyncTools(runCtx, registeredTools, scenario.Timeout); err != nil {
		stopEvents()
		<-eventsDone
		msgBus.Close()
		<-outboundDone
		return result, err
	}

	if !scenario.CloseBusBeforeSettle {
		result.AsyncFollowUps = append(result.AsyncFollowUps, drainInbound(msgBus.InboundChan())...)
	}
	maxFollowUps := scenario.MaxAsyncFollowUps
	if maxFollowUps <= 0 {
		maxFollowUps = 8
	}
	if scenario.ProcessFollowUps {
		processed := 0
		for processed < len(result.AsyncFollowUps) {
			if processed >= maxFollowUps {
				stopEvents()
				<-eventsDone
				msgBus.Close()
				<-outboundDone
				return result, fmt.Errorf(
					"harness: async follow-up limit %d reached with %d unprocessed",
					maxFollowUps,
					len(result.AsyncFollowUps)-processed,
				)
			}
			followUp := result.AsyncFollowUps[processed]
			turn, nextModel, err := runScenarioTurn(
				ctx, runCtx, loop, provider, msgBus, scenario, registeredTools, followUp.Content, sessionKey,
				modelCursor, turnEnd, &mu, &events, &outbound,
			)
			modelCursor = nextModel
			if err != nil {
				stopEvents()
				<-eventsDone
				msgBus.Close()
				<-outboundDone
				return result, err
			}
			result.Turns = append(result.Turns, turn)
			processed++
			if scenario.AsyncSettle > 0 {
				if err := waitForContext(runCtx, scenario.AsyncSettle); err != nil {
					stopEvents()
					<-eventsDone
					msgBus.Close()
					<-outboundDone
					return result, err
				}
			}
			if err := waitAsyncTools(runCtx, registeredTools, scenario.Timeout); err != nil {
				stopEvents()
				<-eventsDone
				msgBus.Close()
				<-outboundDone
				return result, err
			}
			result.AsyncFollowUps = append(result.AsyncFollowUps, drainInbound(msgBus.InboundChan())...)
		}
	}

	stopEvents()
	<-eventsDone
	msgBus.Close()
	<-outboundDone

	mu.Lock()
	traceEvents := append([]runtimeevents.Event(nil), events...)
	traceOutbound := append([]string(nil), outbound...)
	mu.Unlock()
	result.ModelCalls = callsFromProvider(provider)
	result.ToolCalls = mergeToolObservations(toolCallsFromEvents(traceEvents), registeredTools)
	result.Events = traceEvents
	result.Outbound = traceOutbound
	for _, turn := range result.Turns {
		result.Duration += turn.Duration
		if result.Err == nil && turn.Err != nil {
			result.Err = turn.Err
		}
		result.Content = turn.Content
		result.TurnEnd = turn.TurnEnd
	}
	if defaultAgent := loop.GetRegistry().GetDefaultAgent(); defaultAgent != nil && defaultAgent.Sessions != nil {
		result.History = snapshotSessionHistory(defaultAgent.Sessions, sessionKey)
	}
	if recorder != nil {
		if err := recorder.Save(scenario.RecordPath); err != nil {
			return result, err
		}
	}
	return result, nil
}

func runScenarioTurn(
	ctx context.Context,
	runCtx context.Context,
	loop *agent.AgentLoop,
	provider providers.LLMProvider,
	msgBus *bus.MessageBus,
	scenario Scenario,
	scenarioTools []tools.Tool,
	userMessage string,
	sessionKey string,
	modelCursor int,
	turnEnd <-chan struct{},
	mu *sync.Mutex,
	events *[]runtimeevents.Event,
	outbound *[]string,
) (TurnResult, int, error) {
	started := time.Now()
	content, runErr := processTurnSafely(runCtx, loop, userMessage, sessionKey)
	duration := time.Since(started)

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-turnEnd:
	case <-timer.C:
		if runErr != nil {
			return TurnResult{}, modelCursor, fmt.Errorf("harness: turn ended without TurnEnd event: %w", runErr)
		}
		return TurnResult{}, modelCursor, errors.New("harness: turn ended without TurnEnd event")
	}

	calls := callsFromProvider(provider)
	if modelCursor > len(calls) {
		modelCursor = len(calls)
	}
	turnCalls := append([]ModelCall(nil), calls[modelCursor:]...)
	modelCursor = len(calls)

	mu.Lock()
	turnEvents := append([]runtimeevents.Event(nil), (*events)...)
	mu.Unlock()
	if len(turnEvents) > 0 {
		turnEvents = eventsForLastTurn(turnEvents)
	}
	if defaultAgent := loop.GetRegistry().GetDefaultAgent(); defaultAgent != nil && defaultAgent.Sessions != nil {
		turnHistory := snapshotSessionHistory(defaultAgent.Sessions, sessionKey)
		_ = msgBus
		return TurnResult{
			UserMessage: userMessage,
			Content:     content,
			Err:         runErr,
			Duration:    duration,
			ModelCalls:  turnCalls,
			ToolCalls:   mergeToolObservations(toolCallsFromEvents(turnEvents), scenarioTools),
			Events:      turnEvents,
			TurnEnd:     lastTurnEnd(turnEvents),
			History:     turnHistory,
		}, modelCursor, nil
	}
	return TurnResult{
		UserMessage: userMessage,
		Content:     content,
		Err:         runErr,
		Duration:    duration,
		ModelCalls:  turnCalls,
		ToolCalls:   mergeToolObservations(toolCallsFromEvents(turnEvents), scenarioTools),
		Events:      turnEvents,
		TurnEnd:     lastTurnEnd(turnEvents),
	}, modelCursor, nil
}

func processTurnSafely(
	ctx context.Context,
	loop *agent.AgentLoop,
	userMessage string,
	sessionKey string,
) (content string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("harness: recovered turn panic: %v", recovered)
		}
	}()
	return loop.ProcessDirectWithChannel(ctx, userMessage, sessionKey, "harness", "harness")
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
		if call.Status == "skipped" {
			continue
		}
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
	if !expect.AllowInvalidTrace {
		problems = append(problems, r.ValidateTrace()...)
	}
	if expect.Final != "" && r.Content != expect.Final {
		problems = append(problems, fmt.Sprintf("final content = %q, want %q", r.Content, expect.Final))
	}
	if expect.FinalExact != nil && r.Content != *expect.FinalExact {
		problems = append(problems, fmt.Sprintf("final content = %q, want exactly %q", r.Content, *expect.FinalExact))
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
	if expect.ModelCallsExact != nil && len(r.ModelCalls) != *expect.ModelCallsExact {
		problems = append(problems, fmt.Sprintf("model calls = %d, want exactly %d", len(r.ModelCalls), *expect.ModelCallsExact))
	}
	if expect.MinModelCalls > 0 && len(r.ModelCalls) < expect.MinModelCalls {
		problems = append(problems, fmt.Sprintf("model calls = %d, want at least %d", len(r.ModelCalls), expect.MinModelCalls))
	}
	if expect.MaxModelCalls > 0 && len(r.ModelCalls) > expect.MaxModelCalls {
		problems = append(problems, fmt.Sprintf("model calls = %d, want at most %d", len(r.ModelCalls), expect.MaxModelCalls))
	}
	if expect.MaxModelCallsExact != nil && len(r.ModelCalls) > *expect.MaxModelCallsExact {
		problems = append(problems, fmt.Sprintf("model calls = %d, want at most %d", len(r.ModelCalls), *expect.MaxModelCallsExact))
	}
	if expect.TurnCount > 0 && len(r.Turns) != expect.TurnCount {
		problems = append(problems, fmt.Sprintf("turn count = %d, want %d", len(r.Turns), expect.TurnCount))
	}
	if expect.AsyncFollowUps > 0 && len(r.AsyncFollowUps) != expect.AsyncFollowUps {
		problems = append(problems, fmt.Sprintf("async follow-ups = %d, want %d", len(r.AsyncFollowUps), expect.AsyncFollowUps))
	}
	if expect.MinAsyncFollowUps > 0 && len(r.AsyncFollowUps) < expect.MinAsyncFollowUps {
		problems = append(problems, fmt.Sprintf("async follow-ups = %d, want at least %d", len(r.AsyncFollowUps), expect.MinAsyncFollowUps))
	}
	if expect.MaxAsyncFollowUps != nil && len(r.AsyncFollowUps) > *expect.MaxAsyncFollowUps {
		problems = append(problems, fmt.Sprintf("async follow-ups = %d, want at most %d", len(r.AsyncFollowUps), *expect.MaxAsyncFollowUps))
	}
	for _, needle := range expect.FollowUpContains {
		found := false
		for _, followUp := range r.AsyncFollowUps {
			if strings.Contains(followUp.Content, needle) {
				found = true
				break
			}
		}
		if !found {
			problems = append(problems, fmt.Sprintf("async follow-ups do not contain %q", needle))
		}
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
	problems = append(problems, verifyToolExpectations(r.ToolCalls, expect.ToolCalls)...)
	if expect.TurnStatus != "" {
		if r.TurnEnd == nil {
			problems = append(problems, "missing turn end event")
		} else if r.TurnEnd.Status != expect.TurnStatus {
			problems = append(problems, fmt.Sprintf("turn status = %s, want %s", r.TurnEnd.Status, expect.TurnStatus))
		}
	}
	if expect.TurnReason != "" {
		if r.TurnEnd == nil {
			problems = append(problems, "missing turn end event")
		} else if r.TurnEnd.Reason != expect.TurnReason {
			problems = append(problems, fmt.Sprintf("turn reason = %s, want %s", r.TurnEnd.Reason, expect.TurnReason))
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
	if expect.ExactEventSequence != nil {
		got := r.EventKinds()
		if !reflect.DeepEqual(got, expect.ExactEventSequence) {
			problems = append(problems, fmt.Sprintf("event kinds = %v, want exactly %v", got, expect.ExactEventSequence))
		}
	}
	problems = append(problems, verifyEventExpectations(r.Events, expect.EventDetails)...)
	problems = append(problems, verifyModelExpectations(r.ModelCalls, expect.ModelDetails)...)
	if expect.GoldenTrace != "" {
		want, err := LoadTrace(expect.GoldenTrace)
		if err != nil {
			problems = append(problems, fmt.Sprintf("load golden trace: %v", err))
		} else if err := CompareTrace(r.Snapshot(), want); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("harness %s failed: %s", r.Name, strings.Join(problems, "; "))
	}
	return nil
}

// ValidateTrace checks structural invariants that every completed scenario
// should satisfy regardless of its functional expectations.
func (r *Result) ValidateTrace() []string {
	if r == nil {
		return []string{"nil result"}
	}
	var problems []string
	if len(r.Turns) == 0 {
		problems = append(problems, "trace contains no turns")
	}
	turnStarts := make(map[string]int)
	turnEnds := make(map[string]int)
	for _, evt := range r.Events {
		switch evt.Kind {
		case runtimeevents.KindAgentTurnStart:
			turnStarts[evt.Scope.TurnID]++
		case runtimeevents.KindAgentTurnEnd:
			turnEnds[evt.Scope.TurnID]++
		}
	}
	if len(turnStarts) != len(r.Turns) {
		problems = append(problems, fmt.Sprintf("turn start events = %d, turns = %d", len(turnStarts), len(r.Turns)))
	}
	if len(turnEnds) != len(r.Turns) {
		problems = append(problems, fmt.Sprintf("turn end events = %d, turns = %d", len(turnEnds), len(r.Turns)))
	}
	for turnID, count := range turnStarts {
		if count != 1 {
			problems = append(problems, fmt.Sprintf("turn %s has %d start events", turnID, count))
		}
		if turnEnds[turnID] != 1 {
			problems = append(problems, fmt.Sprintf("turn %s has %d end events", turnID, turnEnds[turnID]))
		}
	}
	for turnID, count := range turnEnds {
		if turnStarts[turnID] != 1 {
			problems = append(problems, fmt.Sprintf("turn %s has %d start events", turnID, turnStarts[turnID]))
		}
		if count != 1 {
			problems = append(problems, fmt.Sprintf("turn %s has %d end events", turnID, count))
		}
	}
	for i, turn := range r.Turns {
		if turn.TurnEnd == nil {
			problems = append(problems, fmt.Sprintf("turn %d is missing TurnEnd payload", i+1))
			continue
		}
		phases := phaseSequence(turn.TurnEnd.PhaseHistory)
		if len(phases) == 0 {
			problems = append(problems, fmt.Sprintf("turn %d has no lifecycle history", i+1))
		} else {
			if phases[0] != agent.TurnPhaseCreated {
				problems = append(problems, fmt.Sprintf("turn %d starts at %s, want created", i+1, phases[0]))
			}
			if last := phases[len(phases)-1]; !agent.IsTerminalTurnPhase(last) {
				problems = append(problems, fmt.Sprintf("turn %d ends at non-terminal phase %s", i+1, last))
			}
		}
		for transitionIndex, transition := range turn.TurnEnd.PhaseHistory {
			if !agent.ValidTurnPhaseTransition(transition.From, transition.To) {
				problems = append(problems, fmt.Sprintf("turn %d phase history %d has invalid transition %s -> %s", i+1, transitionIndex+1, transition.From, transition.To))
			}
		}
		wantPhase := turnStatusPhase(turn.TurnEnd.Status)
		if wantPhase != "" && len(phases) > 0 && phases[len(phases)-1] != wantPhase {
			problems = append(problems, fmt.Sprintf("turn %d status %s ended in phase %s, want %s", i+1, turn.TurnEnd.Status, phases[len(phases)-1], wantPhase))
		}
	}
	for _, call := range r.ToolCalls {
		if call.Status == "started" || call.Status == "running" {
			problems = append(problems, fmt.Sprintf("tool call %s (%s) has no terminal record", call.CallID, call.Name))
		}
	}
	return problems
}

func verifyToolExpectations(records []ToolCallRecord, expectations []ToolCallExpectation) []string {
	var problems []string
	used := make(map[int]bool)
	for expectationIndex, expectation := range expectations {
		matched := -1
		for i, record := range records {
			if used[i] || !toolRecordMatches(record, expectation) {
				continue
			}
			matched = i
			break
		}
		if matched < 0 {
			problems = append(problems, fmt.Sprintf("tool expectation %d (%s/%s) did not match any call", expectationIndex+1, expectation.CallID, expectation.Name))
			continue
		}
		used[matched] = true
		record := records[matched]
		if expectation.ErrorContains != "" && !strings.Contains(record.Error, expectation.ErrorContains) {
			problems = append(problems, fmt.Sprintf("tool %s error = %q, want substring %q", record.Name, record.Error, expectation.ErrorContains))
		}
		if expectation.ResultContains != "" && !strings.Contains(record.Result, expectation.ResultContains) {
			problems = append(problems, fmt.Sprintf("tool %s result does not contain %q", record.Name, expectation.ResultContains))
		}
	}
	return problems
}

func toolRecordMatches(record ToolCallRecord, expectation ToolCallExpectation) bool {
	if expectation.CallID != "" && record.CallID != expectation.CallID {
		return false
	}
	if expectation.Name != "" && record.Name != expectation.Name {
		return false
	}
	if expectation.Status != "" && record.Status != expectation.Status {
		return false
	}
	if expectation.IsError != nil && record.IsError != *expectation.IsError {
		return false
	}
	for key, want := range expectation.Arguments {
		got, ok := record.Arguments[key]
		if !ok || !reflect.DeepEqual(got, want) {
			return false
		}
	}
	return true
}

func verifyEventExpectations(events []runtimeevents.Event, expectations []EventExpectation) []string {
	var problems []string
	for expectationIndex, expectation := range expectations {
		matched := false
		for _, evt := range events {
			if expectation.Kind != "" && evt.Kind != expectation.Kind {
				continue
			}
			if expectation.TurnID != "" && evt.Scope.TurnID != expectation.TurnID {
				continue
			}
			payload, _ := json.Marshal(snapshotPayload(evt.Payload))
			if !containsAll(string(payload), expectation.PayloadContains) {
				continue
			}
			matched = true
			break
		}
		if !matched {
			problems = append(problems, fmt.Sprintf("event expectation %d did not match kind=%s turn=%s payload=%v", expectationIndex+1, expectation.Kind, expectation.TurnID, expectation.PayloadContains))
		}
	}
	return problems
}

func verifyModelExpectations(calls []ModelCall, expectations []ModelCallExpectation) []string {
	var problems []string
	cursor := 0
	for expectationIndex, expectation := range expectations {
		matched := -1
		for i := cursor; i < len(calls); i++ {
			call := calls[i]
			if expectation.Model != "" && call.Model != expectation.Model {
				continue
			}
			if len(expectation.ToolNames) > 0 && !reflect.DeepEqual(call.ToolNames, expectation.ToolNames) {
				continue
			}
			if !messagesContainAll(call.Messages, expectation.Contains) {
				continue
			}
			matched = i
			break
		}
		if matched < 0 {
			problems = append(problems, fmt.Sprintf("model expectation %d did not match model=%s contains=%v tools=%v", expectationIndex+1, expectation.Model, expectation.Contains, expectation.ToolNames))
			continue
		}
		cursor = matched + 1
	}
	return problems
}

func messagesContainAll(messages []providers.Message, values []string) bool {
	if len(values) == 0 {
		return true
	}
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString(message.Content)
		builder.WriteByte('\n')
	}
	return containsAll(builder.String(), values)
}

func containsAll(value string, needles []string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}

func phaseSequence(history []agent.TurnPhaseTransition) []agent.TurnPhase {
	if len(history) == 0 {
		return nil
	}
	out := make([]agent.TurnPhase, 0, len(history)+1)
	out = append(out, history[0].From)
	for _, transition := range history {
		out = append(out, transition.To)
	}
	return out
}

func turnStatusPhase(status agent.TurnEndStatus) agent.TurnPhase {
	switch status {
	case agent.TurnEndStatusCompleted:
		return agent.TurnPhaseCompleted
	case agent.TurnEndStatusError:
		return agent.TurnPhaseFailed
	case agent.TurnEndStatusAborted:
		return agent.TurnPhaseAborted
	default:
		return ""
	}
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
	byCallID := make(map[string]int)
	for _, evt := range events {
		switch evt.Kind {
		case runtimeevents.KindAgentToolExecStart:
			payload, ok := evt.Payload.(agent.ToolExecStartPayload)
			if !ok {
				continue
			}
			out = append(out, ToolCallRecord{
				CallID:    payload.CallID,
				Name:      payload.Tool,
				Arguments: cloneArgs(payload.Arguments),
				Status:    "started",
				At:        evt.Time,
			})
			if payload.CallID != "" {
				byCallID[payload.CallID] = len(out) - 1
			}
		case runtimeevents.KindAgentToolExecEnd:
			payload, ok := evt.Payload.(agent.ToolExecEndPayload)
			if !ok {
				continue
			}
			index, found := byCallID[payload.CallID]
			if !found {
				for i := len(out) - 1; i >= 0; i-- {
					if out[i].Status == "started" && out[i].Name == payload.Tool {
						index = i
						found = true
						break
					}
				}
			}
			if !found {
				out = append(out, ToolCallRecord{CallID: payload.CallID, Name: payload.Tool, At: evt.Time})
				index = len(out) - 1
			}
			out[index].Duration = payload.Duration
			out[index].IsError = payload.IsError
			out[index].Error = payload.Error
			out[index].Status = "completed"
			if payload.IsError {
				out[index].Status = "error"
			}
		case runtimeevents.KindAgentToolExecSkipped:
			payload, ok := evt.Payload.(agent.ToolExecSkippedPayload)
			if !ok {
				continue
			}
			out = append(out, ToolCallRecord{
				CallID: payload.CallID,
				Name:   payload.Tool,
				Status: "skipped",
				Error:  payload.Reason,
				At:     evt.Time,
			})
		}
	}
	return out
}

func mergeToolObservations(records []ToolCallRecord, scenarioTools []tools.Tool) []ToolCallRecord {
	observedByCallID := make(map[string]ToolCallRecord)
	observedByName := make(map[string][]ToolCallRecord)
	for _, tool := range scenarioTools {
		observer, ok := tool.(interface{ Calls() []ToolCallRecord })
		if !ok {
			continue
		}
		for _, observed := range observer.Calls() {
			if observed.CallID != "" {
				observedByCallID[observed.CallID] = observed
			}
			observedByName[observed.Name] = append(observedByName[observed.Name], observed)
		}
	}
	for i := range records {
		observed, ok := observedByCallID[records[i].CallID]
		if !ok && len(observedByName[records[i].Name]) > 0 {
			observed = observedByName[records[i].Name][0]
			observedByName[records[i].Name] = observedByName[records[i].Name][1:]
		}
		if !ok && observed.Name == "" {
			continue
		}
		if records[i].CallID == "" {
			records[i].CallID = observed.CallID
		}
		if records[i].Status == "" || records[i].Status == "started" || records[i].Status == "running" {
			records[i].Status = observed.Status
		}
		records[i].IsError = records[i].IsError || observed.IsError
		if records[i].Result == "" {
			records[i].Result = observed.Result
		}
		if records[i].Duration == 0 {
			records[i].Duration = observed.Duration
		}
		if records[i].Error == "" && observed.Error != "" {
			records[i].Error = observed.Error
		}
	}
	return records
}

func cloneMessages(messages []providers.Message) []providers.Message {
	out := make([]providers.Message, len(messages))
	for i, msg := range messages {
		msg.Media = append([]string(nil), msg.Media...)
		msg.Attachments = append([]providers.Attachment(nil), msg.Attachments...)
		msg.ToolCalls = cloneToolCalls(msg.ToolCalls)
		out[i] = msg
	}
	return out
}

type faultFreeHistory interface {
	FaultFreeHistory(string) []providers.Message
}

func snapshotSessionHistory(store session.SessionStore, sessionKey string) []providers.Message {
	if store == nil {
		return nil
	}
	if snapshotter, ok := store.(faultFreeHistory); ok {
		return cloneMessages(snapshotter.FaultFreeHistory(sessionKey))
	}
	return cloneMessages(store.GetHistory(sessionKey))
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

func scenarioMessages(scenario Scenario) []string {
	messages := make([]string, 0, len(scenario.Messages)+1)
	if strings.TrimSpace(scenario.UserMessage) != "" {
		messages = append(messages, scenario.UserMessage)
	}
	for _, message := range scenario.Messages {
		if strings.TrimSpace(message) != "" {
			messages = append(messages, message)
		}
	}
	return messages
}

func harnessSessionKey(scenario Scenario) string {
	sessionKey := strings.TrimSpace(scenario.SessionKey)
	if sessionKey == "" {
		sessionKey = "agent:main:harness:" + sanitizeSessionKey(scenario.Name)
	}
	return sessionKey
}

func configureScenarioSession(loop *agent.AgentLoop, workspace string, scenario Scenario) error {
	if scenario.Session == nil && scenario.SessionFault == nil {
		return nil
	}
	registry := loop.GetRegistry()
	defaultAgent := registry.GetDefaultAgent()
	if defaultAgent == nil {
		return errors.New("harness: default agent is unavailable")
	}
	store := scenario.Session
	if store == nil {
		store = session.NewSessionManager(workspace)
	}
	if scenario.SessionFault != nil {
		store = NewSessionFaultStore(store, scenario.SessionFault)
	}
	old := defaultAgent.Sessions
	defaultAgent.Sessions = store
	if old != nil && old != store {
		_ = old.Close()
	}
	return nil
}

func waitForContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func waitAsyncTools(ctx context.Context, scenarioTools []tools.Tool, scenarioTimeout time.Duration) error {
	timeout := scenarioTimeout
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for _, tool := range scenarioTools {
		waiter, ok := tool.(interface{ Wait(context.Context) error })
		if !ok {
			continue
		}
		if err := waiter.Wait(ctx); err != nil {
			return fmt.Errorf("harness: wait for async tool %s: %w", tool.Name(), err)
		}
	}
	return nil
}

func drainInbound(channel <-chan bus.InboundMessage) []bus.InboundMessage {
	var messages []bus.InboundMessage
	for {
		select {
		case message, ok := <-channel:
			if !ok {
				return messages
			}
			messages = append(messages, message)
		default:
			return messages
		}
	}
}

func callsFromProvider(provider providers.LLMProvider) []ModelCall {
	type callRecorder interface {
		Calls() []ModelCall
	}
	recorder, ok := provider.(callRecorder)
	if !ok {
		return nil
	}
	return recorder.Calls()
}

func eventsForLastTurn(events []runtimeevents.Event) []runtimeevents.Event {
	if len(events) == 0 {
		return nil
	}
	turnID := ""
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == runtimeevents.KindAgentTurnEnd {
			turnID = events[i].Scope.TurnID
			break
		}
	}
	if turnID == "" {
		return append([]runtimeevents.Event(nil), events...)
	}
	start := 0
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == runtimeevents.KindAgentTurnStart && events[i].Scope.TurnID == turnID {
			start = i
			break
		}
	}
	return append([]runtimeevents.Event(nil), events[start:]...)
}

func lastTurnEnd(events []runtimeevents.Event) *agent.TurnEndPayload {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != runtimeevents.KindAgentTurnEnd {
			continue
		}
		if payload, ok := events[i].Payload.(agent.TurnEndPayload); ok {
			copy := payload
			return &copy
		}
	}
	return nil
}

func cloneLLMResponse(response *providers.LLMResponse) *providers.LLMResponse {
	if response == nil {
		return nil
	}
	copy := *response
	copy.ToolCalls = cloneToolCalls(response.ToolCalls)
	copy.ReasoningDetails = append(copy.ReasoningDetails[:0:0], response.ReasoningDetails...)
	if response.Usage != nil {
		usage := *response.Usage
		copy.Usage = &usage
	}
	return &copy
}

func cloneToolCalls(calls []providers.ToolCall) []providers.ToolCall {
	out := make([]providers.ToolCall, len(calls))
	for i, call := range calls {
		call.Arguments = cloneArgs(call.Arguments)
		if call.Function != nil {
			function := *call.Function
			out[i] = call
			out[i].Function = &function
			continue
		}
		out[i] = call
	}
	return out
}

func toolResultError(result *tools.ToolResult) string {
	if result == nil {
		return ""
	}
	if result.Err != nil {
		return result.Err.Error()
	}
	return result.ForLLM
}
