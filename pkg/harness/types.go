// Package harness provides trajectory-level evaluation for GoGoClaw agent runs.
// It records model calls, tool execution order, runtime events, and final state,
// and supports deterministic fault injection through scripted providers/tools.
package harness

import (
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
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
	ConcurrentSetup      func(ToolRuntime) error
	Session              session.SessionStore
	SessionFault         *SessionFault
	Workspace            string
	ProductionIngress    bool
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
	InjectSteering string
	InterruptKind  string
	InterruptHint  string
	OnCall         func(runtime ToolRuntime) error
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

// SteerOnCall returns a step that injects steering input to the turn when called.
func SteerOnCall(steering string, step ModelStep) ModelStep {
	step.InjectSteering = steering
	return step
}

// InterruptOnCall returns a step that requests graceful interruption when called.
func InterruptOnCall(hint string, step ModelStep) ModelStep {
	step.InterruptKind = "graceful"
	step.InterruptHint = hint
	return step
}

// HardAbortOnCall returns a step that requests hard abortion when called.
func HardAbortOnCall(step ModelStep) ModelStep {
	step.InterruptKind = "hard"
	return step
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
	Tools     []providers.ToolDefinition
	Options   map[string]any
	Response  *providers.LLMResponse
	Error     string
	At        time.Time
	Duration  time.Duration
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
	workspace      string
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
	Final                string                 `json:"final,omitempty"`
	FinalExact           *string                `json:"final_exact,omitempty"`
	FinalContains        []string               `json:"final_contains,omitempty"`
	ErrorContains        string                 `json:"error_contains,omitempty"`
	NoError              bool                   `json:"no_error,omitempty"`
	AllowError           bool                   `json:"allow_error,omitempty"`
	WorkspaceFiles       map[string]string      `json:"workspace_files,omitempty"`
	WorkspaceFilesAbsent []string               `json:"workspace_files_absent,omitempty"`
	ModelCalls           int                    `json:"model_calls,omitempty"`
	ModelCallsExact      *int                   `json:"model_calls_exact,omitempty"`
	MinModelCalls        int                    `json:"min_model_calls,omitempty"`
	MaxModelCalls        int                    `json:"max_model_calls,omitempty"`
	MaxModelCallsExact   *int                   `json:"max_model_calls_exact,omitempty"`
	TurnCount            int                    `json:"turn_count,omitempty"`
	AsyncFollowUps       int                    `json:"async_follow_ups,omitempty"`
	MinAsyncFollowUps    int                    `json:"min_async_follow_ups,omitempty"`
	MaxAsyncFollowUps    *int                   `json:"max_async_follow_ups,omitempty"`
	FollowUpContains     []string               `json:"follow_up_contains,omitempty"`
	Tools                []string               `json:"tools,omitempty"`
	ToolCalls            []ToolCallExpectation  `json:"tool_calls,omitempty"`
	NoTools              []string               `json:"no_tools,omitempty"`
	TurnStatus           agent.TurnEndStatus    `json:"turn_status,omitempty"`
	TurnReason           agent.TurnEndReason    `json:"turn_reason,omitempty"`
	Phases               []agent.TurnPhase      `json:"phases,omitempty"`
	Events               []runtimeevents.Kind   `json:"events,omitempty"`
	EventSequence        []runtimeevents.Kind   `json:"event_sequence,omitempty"`
	ExactEventSequence   []runtimeevents.Kind   `json:"exact_event_sequence,omitempty"`
	EventDetails         []EventExpectation     `json:"event_details,omitempty"`
	ModelDetails         []ModelCallExpectation `json:"model_details,omitempty"`
	GoldenTrace          string                 `json:"golden_trace,omitempty"`
	AllowInvalidTrace    bool                   `json:"allow_invalid_trace,omitempty"`
	HistoryContains      []string               `json:"history_contains,omitempty"`
	OutboundContains     []string               `json:"outbound_contains,omitempty"`
	NoOutbound           bool                   `json:"no_outbound,omitempty"`
	Iterations           int                    `json:"iterations,omitempty"`
}
