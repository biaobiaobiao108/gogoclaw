package harness

import (
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

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

func eventsForTurnRange(events []runtimeevents.Event, rangeStart, rangeEnd int) []runtimeevents.Event {
	if rangeStart < 0 {
		rangeStart = 0
	}
	if rangeStart >= len(events) || rangeEnd < rangeStart || rangeEnd >= len(events) {
		return nil
	}

	start := rangeStart
	turnID := events[rangeEnd].Scope.TurnID
	if turnID != "" {
		for i := rangeStart; i <= rangeEnd; i++ {
			if events[i].Scope.TurnID == turnID {
				start = i
				break
			}
		}
	}
	return append([]runtimeevents.Event(nil), events[start:rangeEnd+1]...)
}

func lastTurnEnd(events []runtimeevents.Event) *agent.TurnEndPayload {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != runtimeevents.KindAgentTurnEnd || events[i].Correlation.ParentTurnID != "" {
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
