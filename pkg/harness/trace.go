package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

const traceVersion = 1

// Trace is the stable, timestamp-free representation used for golden files
// and provider replay. Volatile IDs, durations, and timestamps are omitted.
type Trace struct {
	Version        int              `json:"version"`
	Name           string           `json:"name"`
	Content        string           `json:"content"`
	Error          string           `json:"error,omitempty"`
	Turns          []TraceTurn      `json:"turns"`
	ModelCalls     []TraceModelCall `json:"model_calls"`
	Tools          []TraceToolCall  `json:"tools"`
	Events         []TraceEvent     `json:"events"`
	TurnEnd        *TraceTurnEnd    `json:"turn_end,omitempty"`
	Outbound       []string         `json:"outbound"`
	History        []TraceMessage   `json:"history"`
	AsyncFollowUps []TraceInbound   `json:"async_follow_ups,omitempty"`
}

type TraceTurn struct {
	UserMessage string           `json:"user_message"`
	Content     string           `json:"content"`
	Error       string           `json:"error,omitempty"`
	ModelCalls  []TraceModelCall `json:"model_calls"`
	Tools       []TraceToolCall  `json:"tools"`
	Events      []TraceEvent     `json:"events"`
	TurnEnd     *TraceTurnEnd    `json:"turn_end,omitempty"`
}

type TraceModelCall struct {
	Index     int                        `json:"index"`
	Model     string                     `json:"model"`
	Messages  []TraceMessage             `json:"messages"`
	ToolNames []string                   `json:"tool_names,omitempty"`
	Tools     []providers.ToolDefinition `json:"tools,omitempty"`
	Options   map[string]any             `json:"options,omitempty"`
	Response  *TraceModelResponse        `json:"response,omitempty"`
	Error     string                     `json:"error,omitempty"`
}

type TraceModelResponse struct {
	Content          string               `json:"content"`
	ReasoningContent string               `json:"reasoning_content,omitempty"`
	ToolCalls        []TraceToolCall      `json:"tool_calls,omitempty"`
	FinishReason     string               `json:"finish_reason,omitempty"`
	Usage            *providers.UsageInfo `json:"usage,omitempty"`
}

type TraceMessage struct {
	Role        string                 `json:"role"`
	Content     string                 `json:"content"`
	ToolCallID  string                 `json:"tool_call_id,omitempty"`
	ToolCalls   []TraceToolCall        `json:"tool_calls,omitempty"`
	Media       []string               `json:"media,omitempty"`
	Attachments []providers.Attachment `json:"attachments,omitempty"`
}

type TraceToolCall struct {
	CallID    string         `json:"call_id,omitempty"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Status    string         `json:"status,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
	Error     string         `json:"error,omitempty"`
	Result    string         `json:"result,omitempty"`
}

type TracePhaseTransition struct {
	From agent.TurnPhase `json:"from"`
	To   agent.TurnPhase `json:"to"`
}

type TraceTurnEnd struct {
	Status       agent.TurnEndStatus    `json:"status"`
	Reason       agent.TurnEndReason    `json:"reason"`
	Iterations   int                    `json:"iterations"`
	FinalContent string                 `json:"final_content"`
	ActiveSkills []string               `json:"active_skills,omitempty"`
	Phases       []agent.TurnPhase      `json:"phases"`
	Transitions  []TracePhaseTransition `json:"transitions"`
}

type TraceEvent struct {
	Kind         runtimeevents.Kind `json:"kind"`
	TurnID       string             `json:"turn_id,omitempty"`
	ParentTurnID string             `json:"parent_turn_id,omitempty"`
	Source       string             `json:"source,omitempty"`
	Payload      map[string]any     `json:"payload,omitempty"`
	Attrs        map[string]any     `json:"attrs,omitempty"`
}

type TraceInbound struct {
	Content    string `json:"content"`
	SessionKey string `json:"session_key,omitempty"`
	Channel    string `json:"channel,omitempty"`
	ChatID     string `json:"chat_id,omitempty"`
}

// Snapshot converts a result into a canonical trace suitable for persisted
// golden comparisons and replay.
func (r *Result) Snapshot() Trace {
	trace := Trace{Version: traceVersion}
	if r == nil {
		return trace
	}
	trace.Name = r.Name
	trace.Content = r.Content
	if r.Err != nil {
		trace.Error = r.Err.Error()
	}
	trace.Outbound = append([]string(nil), r.Outbound...)
	trace.History = snapshotMessages(r.History)
	trace.ModelCalls = snapshotModelCalls(r.ModelCalls)
	trace.Tools = snapshotTools(r.ToolCalls)
	trace.Events = snapshotEvents(r.Events)
	trace.TurnEnd = snapshotTurnEnd(r.TurnEnd)
	for _, turn := range r.Turns {
		trace.Turns = append(trace.Turns, TraceTurn{
			UserMessage: turn.UserMessage,
			Content:     turn.Content,
			Error:       errorString(turn.Err),
			ModelCalls:  snapshotModelCalls(turn.ModelCalls),
			Tools:       snapshotTools(turn.ToolCalls),
			Events:      snapshotEvents(turn.Events),
			TurnEnd:     snapshotTurnEnd(turn.TurnEnd),
		})
	}
	for _, followUp := range r.AsyncFollowUps {
		trace.AsyncFollowUps = append(trace.AsyncFollowUps, TraceInbound{
			Content:    followUp.Content,
			SessionKey: followUp.SessionKey,
			Channel:    followUp.Context.Channel,
			ChatID:     followUp.Context.ChatID,
		})
	}
	canonicalizeWorkspace(&trace, r.workspace)
	return trace
}

func canonicalizeWorkspace(trace *Trace, workspace string) {
	if trace == nil || workspace == "" {
		return
	}
	paths := []string{workspace, filepath.ToSlash(workspace), filepath.FromSlash(workspace)}
	replace := func(value string) string {
		for _, path := range paths {
			if path != "" {
				value = strings.ReplaceAll(value, path, "{{workspace}}")
			}
		}
		return value
	}
	var replaceMessages func([]TraceMessage)
	replaceMessages = func(messages []TraceMessage) {
		for i := range messages {
			messages[i].Content = canonicalizeRuntimeTime(replace(messages[i].Content))
			for j := range messages[i].Media {
				messages[i].Media[j] = replace(messages[i].Media[j])
			}
			for j := range messages[i].Attachments {
				messages[i].Attachments[j].URL = replace(messages[i].Attachments[j].URL)
			}
		}
	}
	var replaceCalls func([]TraceModelCall)
	replaceCalls = func(calls []TraceModelCall) {
		for i := range calls {
			replaceMessages(calls[i].Messages)
		}
	}
	replaceMessages(trace.History)
	replaceCalls(trace.ModelCalls)
	for i := range trace.Turns {
		replaceCalls(trace.Turns[i].ModelCalls)
	}
}

func canonicalizeRuntimeTime(content string) string {
	content = canonicalizeSection(content, "## Current Time", "## Runtime Time\n{{runtime-time}}")
	return canonicalizeSection(content, "## Runtime\n", "## Runtime Metadata\n{{runtime}}")
}

func canonicalizeSection(content, marker, replacement string) string {
	for {
		start := strings.Index(content, marker)
		if start < 0 {
			return content
		}
		valueStart := start + len(marker)
		end := strings.Index(content[valueStart:], "\n## ")
		if end < 0 {
			return content
		}
		content = content[:start] + replacement + content[valueStart+end:]
	}
}

func (r *Result) SaveTrace(path string) error {
	return WriteTrace(path, r.Snapshot())
}

func WriteTrace(path string, trace Trace) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("harness: trace path is required")
	}
	data, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return fmt.Errorf("harness: marshal trace: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("harness: create trace directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("harness: write trace: %w", err)
	}
	return nil
}

func LoadTrace(path string) (Trace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Trace{}, fmt.Errorf("harness: read trace: %w", err)
	}
	var trace Trace
	if err := json.Unmarshal(data, &trace); err != nil {
		return Trace{}, fmt.Errorf("harness: decode trace: %w", err)
	}
	if trace.Version != traceVersion {
		return Trace{}, fmt.Errorf("harness: trace version = %d, want %d", trace.Version, traceVersion)
	}
	return trace, nil
}

func CompareTrace(got, want Trace) error {
	gotJSON, err := json.Marshal(got)
	if err != nil {
		return fmt.Errorf("harness: marshal actual trace: %w", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		return fmt.Errorf("harness: marshal golden trace: %w", err)
	}
	if bytes.Equal(gotJSON, wantJSON) {
		return nil
	}
	return fmt.Errorf("harness trace mismatch: actual=%s golden=%s", compactJSON(gotJSON), compactJSON(wantJSON))
}

func snapshotModelCalls(calls []ModelCall) []TraceModelCall {
	out := make([]TraceModelCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, TraceModelCall{
			Index:     call.Index,
			Model:     call.Model,
			Messages:  snapshotMessages(call.Messages),
			ToolNames: append([]string(nil), call.ToolNames...),
			Tools:     cloneToolDefinitions(call.Tools),
			Options:   cloneStringAnyMap(call.Options),
			Response:  snapshotResponse(call.Response),
			Error:     call.Error,
		})
	}
	return out
}

func snapshotResponse(response *providers.LLMResponse) *TraceModelResponse {
	if response == nil {
		return nil
	}
	var usage *providers.UsageInfo
	if response.Usage != nil {
		copy := *response.Usage
		usage = &copy
	}
	return &TraceModelResponse{
		Content:          response.Content,
		ReasoningContent: response.ReasoningContent,
		ToolCalls:        snapshotProviderToolCalls(response.ToolCalls),
		FinishReason:     response.FinishReason,
		Usage:            usage,
	}
}

func snapshotMessages(messages []providers.Message) []TraceMessage {
	out := make([]TraceMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, TraceMessage{
			Role:        message.Role,
			Content:     message.Content,
			ToolCallID:  message.ToolCallID,
			ToolCalls:   snapshotProviderToolCalls(message.ToolCalls),
			Media:       append([]string(nil), message.Media...),
			Attachments: append([]providers.Attachment(nil), message.Attachments...),
		})
	}
	return out
}

func snapshotProviderToolCalls(calls []providers.ToolCall) []TraceToolCall {
	out := make([]TraceToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, TraceToolCall{
			CallID:    call.ID,
			Name:      providers.NormalizeToolCall(call).Name,
			Arguments: cloneArgs(call.Arguments),
		})
	}
	return out
}

func snapshotTools(records []ToolCallRecord) []TraceToolCall {
	out := make([]TraceToolCall, 0, len(records))
	for _, record := range records {
		out = append(out, TraceToolCall{
			CallID:    record.CallID,
			Name:      record.Name,
			Arguments: cloneArgs(record.Arguments),
			Status:    record.Status,
			IsError:   record.IsError,
			Error:     record.Error,
			Result:    record.Result,
		})
	}
	return out
}

func snapshotEvents(events []runtimeevents.Event) []TraceEvent {
	out := make([]TraceEvent, 0, len(events))
	turnIDs := make(map[string]string)
	nextTurn := 0
	canonicalTurnID := func(id string) string {
		if id == "" {
			return ""
		}
		if canonical, ok := turnIDs[id]; ok {
			return canonical
		}
		nextTurn++
		canonical := fmt.Sprintf("turn-%d", nextTurn)
		turnIDs[id] = canonical
		return canonical
	}
	for _, evt := range events {
		out = append(out, TraceEvent{
			Kind:         evt.Kind,
			TurnID:       canonicalTurnID(evt.Scope.TurnID),
			ParentTurnID: canonicalTurnID(evt.Correlation.ParentTurnID),
			Source:       evt.Source.Component,
			Payload:      snapshotPayload(evt.Payload),
			Attrs:        snapshotMap(evt.Attrs),
		})
	}
	return out
}

func snapshotTurnEnd(payload *agent.TurnEndPayload) *TraceTurnEnd {
	if payload == nil {
		return nil
	}
	phases := phaseSequence(payload.PhaseHistory)
	transitions := make([]TracePhaseTransition, 0, len(payload.PhaseHistory))
	for _, transition := range payload.PhaseHistory {
		transitions = append(transitions, TracePhaseTransition{From: transition.From, To: transition.To})
	}
	return &TraceTurnEnd{
		Status:       payload.Status,
		Reason:       payload.Reason,
		Iterations:   payload.Iterations,
		FinalContent: payload.FinalContent,
		ActiveSkills: append([]string(nil), payload.ActiveSkills...),
		Phases:       phases,
		Transitions:  transitions,
	}
}

func snapshotPayload(payload any) map[string]any {
	switch typed := payload.(type) {
	case agent.TurnEndPayload:
		return snapshotTurnEndPayload(&typed)
	case agent.TurnStartPayload:
		return map[string]any{
			"user_message": typed.UserMessage,
			"media_count":  typed.MediaCount,
		}
	case agent.ToolExecStartPayload:
		return map[string]any{
			"call_id":   typed.CallID,
			"tool":      typed.Tool,
			"arguments": cloneArgs(typed.Arguments),
		}
	case agent.ToolExecEndPayload:
		return map[string]any{
			"call_id":      typed.CallID,
			"tool":         typed.Tool,
			"for_llm_len":  typed.ForLLMLen,
			"for_user_len": typed.ForUserLen,
			"is_error":     typed.IsError,
			"async":        typed.Async,
			"error":        typed.Error,
		}
	case agent.ToolExecSkippedPayload:
		return map[string]any{
			"call_id": typed.CallID,
			"tool":    typed.Tool,
			"reason":  typed.Reason,
		}
	default:
		return snapshotMap(payload)
	}
}

func snapshotTurnEndPayload(payload *agent.TurnEndPayload) map[string]any {
	if payload == nil {
		return nil
	}
	return map[string]any{
		"status":                  payload.Status,
		"reason":                  payload.Reason,
		"iterations":              payload.Iterations,
		"final_content":           payload.FinalContent,
		"active_skills":           append([]string(nil), payload.ActiveSkills...),
		"attempted_skills":        append([]string(nil), payload.AttemptedSkills...),
		"final_successful_path":   append([]string(nil), payload.FinalSuccessfulPath...),
		"skill_context_snapshots": append([]agent.SkillContextSnapshot(nil), payload.SkillContextSnapshots...),
		"tool_kinds":              append([]string(nil), payload.ToolKinds...),
		"tool_executions":         append([]agent.ToolExecutionRecord(nil), payload.ToolExecutions...),
		"phases":                  phaseSequence(payload.PhaseHistory),
	}
}

func snapshotMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"value": fmt.Sprint(value)}
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return map[string]any{"value": fmt.Sprint(value)}
	}
	return sanitizeMap(result)
}

func sanitizeMap(value map[string]any) map[string]any {
	for key, child := range value {
		if volatileKey(key) {
			delete(value, key)
			continue
		}
		switch typed := child.(type) {
		case map[string]any:
			value[key] = sanitizeMap(typed)
		case []any:
			value[key] = sanitizeSlice(typed)
		}
	}
	return value
}

func sanitizeSlice(values []any) []any {
	for i, value := range values {
		switch typed := value.(type) {
		case map[string]any:
			values[i] = sanitizeMap(typed)
		case []any:
			values[i] = sanitizeSlice(typed)
		}
	}
	return values
}

func volatileKey(key string) bool {
	switch strings.ToLower(key) {
	case "at", "time", "timestamp", "duration", "duration_ms", "started_at", "completed_at", "created_at", "workspace",
		"child_turn_id":
		return true
	default:
		return false
	}
}

func compactJSON(data []byte) string {
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, data); err != nil {
		return string(data)
	}
	return buffer.String()
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
