package providers

import "strings"

// StopReason is the provider-neutral terminal or continuation reason for one
// model response. Provider-specific finish reasons must be normalized at this
// boundary before the agent loop decides whether tools may run.
type StopReason string

const (
	StopReasonUnknown       StopReason = "unknown"
	StopReasonComplete      StopReason = "complete"
	StopReasonToolUse       StopReason = "tool_use"
	StopReasonLength        StopReason = "length"
	StopReasonContentFilter StopReason = "content_filter"
	StopReasonPaused        StopReason = "paused"
	StopReasonError         StopReason = "error"
)

// Terminal reports whether this response is complete for decision purposes and
// must not execute tool calls, even if a provider emitted partial call data.
func (r StopReason) Terminal() bool {
	switch r {
	case StopReasonLength, StopReasonContentFilter, StopReasonPaused, StopReasonError:
		return true
	default:
		return false
	}
}

type ResponseItemKind string

const (
	ResponseItemText      ResponseItemKind = "text"
	ResponseItemReasoning ResponseItemKind = "reasoning"
	ResponseItemToolCall  ResponseItemKind = "tool_call"
)

// ResponseItem is the stable representation of one model output item. The raw
// provider response remains available for adapter-specific replay metadata, but
// the agent loop consumes normalized items instead of provider wire fields.
type ResponseItem struct {
	Kind     ResponseItemKind `json:"kind"`
	Text     string           `json:"text,omitempty"`
	ToolCall *ToolCall        `json:"tool_call,omitempty"`
}

// NormalizedResponse is the provider-neutral response contract used by the
// agent loop. It deliberately preserves the raw finish reason for diagnostics.
type NormalizedResponse struct {
	Items              []ResponseItem `json:"items,omitempty"`
	StopReason         StopReason     `json:"stop_reason"`
	ProviderStopReason string         `json:"provider_stop_reason,omitempty"`
	Usage              *UsageInfo     `json:"usage,omitempty"`
}

// NormalizeLLMResponse converts a provider response into the stable response
// protocol. A response with tool calls is only executable when the normalized
// stop reason is StopReasonToolUse.
func NormalizeLLMResponse(response *LLMResponse) NormalizedResponse {
	if response == nil {
		return NormalizedResponse{StopReason: StopReasonError}
	}

	rawStop := strings.TrimSpace(response.FinishReason)
	stopReason := ClassifyStopReason(rawStop)
	hasToolCalls := len(response.ToolCalls) > 0

	if hasToolCalls && !stopReason.Terminal() {
		stopReason = StopReasonToolUse
	}
	if !hasToolCalls && stopReason == StopReasonToolUse {
		// A provider that claims tool use without a complete tool call is not a
		// valid tool continuation and must not be treated as a successful answer.
		stopReason = StopReasonError
	}

	normalized := NormalizedResponse{
		StopReason:         stopReason,
		ProviderStopReason: rawStop,
		Usage:              response.Usage,
	}
	if strings.TrimSpace(response.Content) != "" {
		normalized.Items = append(normalized.Items, ResponseItem{
			Kind: ResponseItemText,
			Text: response.Content,
		})
	}
	if reasoning := normalizedReasoningText(response); reasoning != "" {
		normalized.Items = append(normalized.Items, ResponseItem{
			Kind: ResponseItemReasoning,
			Text: reasoning,
		})
	}
	for i := range response.ToolCalls {
		toolCall := response.ToolCalls[i]
		normalized.Items = append(normalized.Items, ResponseItem{
			Kind:     ResponseItemToolCall,
			ToolCall: &toolCall,
		})
	}
	return normalized
}

// ClassifyStopReason maps provider wire values into the response protocol.
func ClassifyStopReason(reason string) StopReason {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "stop", "end_turn", "complete", "completed", "finished", "eos":
		return StopReasonComplete
	case "tool_calls", "tool_use", "function_call":
		return StopReasonToolUse
	case "length", "max_tokens", "max_output_tokens", "truncated", "model_context_window_exceeded":
		return StopReasonLength
	case "content_filter", "refusal", "safety", "blocklist", "prohibited_content", "spii",
		"image_safety", "recitation":
		return StopReasonContentFilter
	case "pause_turn", "paused":
		return StopReasonPaused
	case "error", "failed", "canceled", "cancelled", "malformed_function_call",
		"unexpected_tool_call", "too_many_tool_calls", "language", "other":
		return StopReasonError
	default:
		return StopReasonUnknown
	}
}

func (r NormalizedResponse) Text() string {
	var parts []string
	for _, item := range r.Items {
		if item.Kind == ResponseItemText && item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "")
}

func (r NormalizedResponse) ReasoningText() string {
	var parts []string
	for _, item := range r.Items {
		if item.Kind == ResponseItemReasoning && item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "")
}

func (r NormalizedResponse) ToolCalls() []ToolCall {
	var calls []ToolCall
	for _, item := range r.Items {
		if item.Kind == ResponseItemToolCall && item.ToolCall != nil {
			calls = append(calls, *item.ToolCall)
		}
	}
	return calls
}

func (r NormalizedResponse) AllowsToolExecution() bool {
	return r.StopReason == StopReasonToolUse && len(r.ToolCalls()) > 0
}

func normalizedReasoningText(response *LLMResponse) string {
	if response == nil {
		return ""
	}
	if strings.TrimSpace(response.Reasoning) != "" {
		return response.Reasoning
	}
	if strings.TrimSpace(response.ReasoningContent) != "" {
		return response.ReasoningContent
	}
	var parts []string
	for _, detail := range response.ReasoningDetails {
		if strings.TrimSpace(detail.Text) != "" {
			parts = append(parts, detail.Text)
		}
	}
	return strings.Join(parts, "")
}
