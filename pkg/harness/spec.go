package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

// ScenarioSpec is the JSON-friendly subset of Scenario used by the standalone
// harness CLI. Go tests can still use arbitrary tools and setup callbacks.
type ScenarioSpec struct {
	Name                 string             `json:"name"`
	UserMessage          string             `json:"user_message"`
	Messages             []string           `json:"messages,omitempty"`
	SessionKey           string             `json:"session_key,omitempty"`
	Model                string             `json:"model,omitempty"`
	ContextWindow        int                `json:"context_window,omitempty"`
	MaxTokens            int                `json:"max_tokens,omitempty"`
	MaxToolIterations    int                `json:"max_tool_iterations,omitempty"`
	TimeoutMS            int64              `json:"timeout_ms,omitempty"`
	AsyncSettleMS        int64              `json:"async_settle_ms,omitempty"`
	MaxAsyncFollowUps    int                `json:"max_async_follow_ups,omitempty"`
	ProcessFollowUps     bool               `json:"process_follow_ups,omitempty"`
	CloseBusBeforeSettle bool               `json:"close_bus_before_settle,omitempty"`
	ReplayPath           string             `json:"replay_path,omitempty"`
	RecordPath           string             `json:"record_path,omitempty"`
	Workspace            string             `json:"workspace,omitempty"`
	Steps                []ModelStepSpec    `json:"steps"`
	Tools                []ToolSpec         `json:"tools,omitempty"`
	SessionFaults        []SessionFaultSpec `json:"session_faults,omitempty"`
	Expectation          *Expect            `json:"expect,omitempty"`
}

// SuiteSpec groups multiple scenario specs in one JSON file.
type SuiteSpec struct {
	Name      string         `json:"name,omitempty"`
	Scenarios []ScenarioSpec `json:"scenarios"`
}

type ModelStepSpec struct {
	Content        string         `json:"content,omitempty"`
	ToolCalls      []ToolCallSpec `json:"tool_calls,omitempty"`
	Error          string         `json:"error,omitempty"`
	Panic          string         `json:"panic,omitempty"`
	DelayMS        int64          `json:"delay_ms,omitempty"`
	WaitForContext bool           `json:"wait_for_context,omitempty"`
	Response       *ResponseSpec  `json:"response,omitempty"`
}

type ToolCallSpec struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type ResponseSpec struct {
	Content          string         `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	FinishReason     string         `json:"finish_reason,omitempty"`
	ToolCalls        []ToolCallSpec `json:"tool_calls,omitempty"`
}

type ToolSpec struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
	DelayMS int64  `json:"delay_ms,omitempty"`
}

type SessionFaultSpec struct {
	Operation string `json:"operation"`
	Times     int    `json:"times,omitempty"`
	Error     string `json:"error,omitempty"`
}

func LoadScenarioSpec(path string) (Scenario, error) {
	scenarios, err := loadScenarioFile(path)
	if err != nil {
		return Scenario{}, err
	}
	if len(scenarios) != 1 {
		return Scenario{}, fmt.Errorf("harness: scenario file %s contains %d scenarios, want 1", path, len(scenarios))
	}
	return scenarios[0], nil
}

// LoadScenarios loads scenarios from a scenario file, suite file, or a directory
// containing JSON scenario files.
func LoadScenarios(path string) ([]Scenario, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("harness: stat %s: %w", path, err)
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf("harness: read dir %s: %w", path, err)
		}
		var files []string
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			files = append(files, filepath.Join(path, entry.Name()))
		}
		sort.Strings(files)
		if len(files) == 0 {
			return nil, fmt.Errorf("harness: no JSON scenario files found in %s", path)
		}
		var all []Scenario
		for _, file := range files {
			scenarios, err := loadScenarioFile(file)
			if err != nil {
				return nil, err
			}
			all = append(all, scenarios...)
		}
		return all, nil
	}
	return loadScenarioFile(path)
}

func loadScenarioFile(path string) ([]Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("harness: read scenario spec %s: %w", path, err)
	}
	var probe struct {
		Scenarios json.RawMessage `json:"scenarios"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("harness: decode scenario spec %s: %w", path, err)
	}
	if probe.Scenarios != nil {
		var suite SuiteSpec
		if err := decodeStrictJSON(data, &suite); err != nil {
			return nil, fmt.Errorf("harness: decode suite spec %s: %w", path, err)
		}
		if len(suite.Scenarios) == 0 {
			return nil, fmt.Errorf("harness: suite spec %s contains no scenarios", path)
		}
		scenarios := make([]Scenario, 0, len(suite.Scenarios))
		for i, spec := range suite.Scenarios {
			scenario, err := spec.Scenario()
			if err != nil {
				return nil, fmt.Errorf("harness: suite spec %s scenario %d: %w", path, i+1, err)
			}
			scenarios = append(scenarios, scenario)
		}
		return scenarios, nil
	}
	var spec ScenarioSpec
	if err := decodeStrictJSON(data, &spec); err != nil {
		return nil, fmt.Errorf("harness: decode scenario spec %s: %w", path, err)
	}
	scenario, err := spec.Scenario()
	if err != nil {
		return nil, fmt.Errorf("harness: scenario spec %s: %w", path, err)
	}
	return []Scenario{scenario}, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func (s ScenarioSpec) Scenario() (Scenario, error) {
	scenario := Scenario{
		Name:                 s.Name,
		UserMessage:          s.UserMessage,
		Messages:             append([]string(nil), s.Messages...),
		SessionKey:           s.SessionKey,
		Model:                s.Model,
		ContextWindow:        s.ContextWindow,
		MaxTokens:            s.MaxTokens,
		MaxToolIterations:    s.MaxToolIterations,
		Timeout:              time.Duration(s.TimeoutMS) * time.Millisecond,
		AsyncSettle:          time.Duration(s.AsyncSettleMS) * time.Millisecond,
		MaxAsyncFollowUps:    s.MaxAsyncFollowUps,
		ProcessFollowUps:     s.ProcessFollowUps,
		CloseBusBeforeSettle: s.CloseBusBeforeSettle,
		ReplayPath:           s.ReplayPath,
		RecordPath:           s.RecordPath,
		Workspace:            s.Workspace,
		Expectation:          s.Expectation,
	}
	for _, step := range s.Steps {
		modelStep := ModelStep{
			Content:        step.Content,
			Delay:          time.Duration(step.DelayMS) * time.Millisecond,
			WaitForContext: step.WaitForContext,
		}
		for _, call := range step.ToolCalls {
			modelStep.ToolCalls = append(modelStep.ToolCalls, ToolCall(call.ID, call.Name, call.Arguments))
		}
		if step.Error != "" {
			modelStep.Err = errors.New(step.Error)
		}
		if step.Panic != "" {
			modelStep.Panic = step.Panic
		}
		if step.Response != nil {
			response := &providers.LLMResponse{
				Content:          step.Response.Content,
				ReasoningContent: step.Response.ReasoningContent,
				FinishReason:     step.Response.FinishReason,
			}
			for _, call := range step.Response.ToolCalls {
				response.ToolCalls = append(response.ToolCalls, ToolCall(call.ID, call.Name, call.Arguments))
			}
			modelStep.Response = response
		}
		scenario.Steps = append(scenario.Steps, modelStep)
	}
	for _, tool := range s.Tools {
		created, err := tool.Tool()
		if err != nil {
			return Scenario{}, err
		}
		scenario.Tools = append(scenario.Tools, created)
	}
	if len(s.SessionFaults) > 0 {
		fault := NewSessionFault()
		for _, spec := range s.SessionFaults {
			operation, err := parseSessionOperation(spec.Operation)
			if err != nil {
				return Scenario{}, err
			}
			var faultErr error
			if spec.Error != "" {
				faultErr = errors.New(spec.Error)
			}
			fault.Fail(operation, spec.Times, faultErr)
		}
		scenario.SessionFault = fault
	}
	if err := validateScenario(scenario); err != nil {
		return Scenario{}, err
	}
	return scenario, nil
}

func (s ToolSpec) Tool() (tools.Tool, error) {
	if strings.TrimSpace(s.Name) == "" {
		return nil, errors.New("harness: tool name is required")
	}
	switch strings.ToLower(strings.TrimSpace(s.Kind)) {
	case "text", "":
		return TextTool(s.Name, s.Content), nil
	case "error":
		if s.Error == "" {
			s.Error = s.Content
		}
		return ErrorTool(s.Name, s.Error), nil
	case "panic":
		value := s.Content
		if value == "" {
			value = s.Error
		}
		if value == "" {
			value = "injected tool panic"
		}
		return PanicTool(s.Name, value), nil
	case "async_text":
		return NewAsyncTextTool(s.Name, s.Content, time.Duration(s.DelayMS)*time.Millisecond), nil
	default:
		return nil, fmt.Errorf("harness: unsupported tool kind %q", s.Kind)
	}
}

func parseSessionOperation(op string) (SessionOperation, error) {
	switch SessionOperation(strings.ToLower(strings.TrimSpace(op))) {
	case SessionGetHistory:
		return SessionGetHistory, nil
	case SessionAddMessage:
		return SessionAddMessage, nil
	case SessionAddFullMessage:
		return SessionAddFullMessage, nil
	case SessionGetSummary:
		return SessionGetSummary, nil
	case SessionSetSummary:
		return SessionSetSummary, nil
	case SessionSetHistory:
		return SessionSetHistory, nil
	case SessionTruncateHistory:
		return SessionTruncateHistory, nil
	case SessionSave:
		return SessionSave, nil
	case SessionSetCheckpoint:
		return SessionSetCheckpoint, nil
	case SessionGetCheckpoint:
		return SessionGetCheckpoint, nil
	case SessionClearCheckpoint:
		return SessionClearCheckpoint, nil
	case SessionUpdateUsage:
		return SessionUpdateUsage, nil
	case SessionCommitSummary:
		return SessionCommitSummary, nil
	default:
		return "", fmt.Errorf("harness: unsupported session operation %q", op)
	}
}

// RunSpecFile runs one JSON scenario spec and writes its structured report by
// default to stdout through the caller-provided report path helper in the CLI.
func RunSpecFile(ctx context.Context, path string) (Report, error) {
	return RunSpecPath(ctx, path)
}

// RunSpecPath runs scenarios from a file, suite, or directory and returns a structured report.
func RunSpecPath(ctx context.Context, path string) (Report, error) {
	scenarios, err := LoadScenarios(path)
	if err != nil {
		return Report{}, err
	}
	return RunSuite(ctx, scenarios), nil
}
