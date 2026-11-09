package providers

import "testing"

func TestNormalizeLLMResponseStopReasons(t *testing.T) {
	tests := []struct {
		name         string
		finish       string
		toolCalls    int
		want         StopReason
		wantTools    bool
		wantTerminal bool
	}{
		{name: "complete", finish: "stop", want: StopReasonComplete},
		{name: "tool use", finish: "tool_calls", toolCalls: 1, want: StopReasonToolUse, wantTools: true},
		{name: "tool calls with generic stop", finish: "stop", toolCalls: 1, want: StopReasonToolUse, wantTools: true},
		{name: "length", finish: "max_tokens", toolCalls: 1, want: StopReasonLength, wantTerminal: true},
		{name: "content filter", finish: "safety", want: StopReasonContentFilter, wantTerminal: true},
		{name: "paused", finish: "pause_turn", want: StopReasonPaused, wantTerminal: true},
		{name: "malformed tool response", finish: "tool_calls", want: StopReasonError, wantTerminal: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := &LLMResponse{FinishReason: tt.finish}
			for i := 0; i < tt.toolCalls; i++ {
				response.ToolCalls = append(response.ToolCalls, ToolCall{ID: "call", Name: "read_file"})
			}
			normalized := NormalizeLLMResponse(response)
			if normalized.StopReason != tt.want {
				t.Fatalf("StopReason = %q, want %q", normalized.StopReason, tt.want)
			}
			if normalized.AllowsToolExecution() != tt.wantTools {
				t.Fatalf("AllowsToolExecution() = %v, want %v", normalized.AllowsToolExecution(), tt.wantTools)
			}
			if normalized.StopReason.Terminal() != tt.wantTerminal {
				t.Fatalf("Terminal() = %v, want %v", normalized.StopReason.Terminal(), tt.wantTerminal)
			}
		})
	}
}

func TestNormalizeLLMResponseItems(t *testing.T) {
	normalized := NormalizeLLMResponse(&LLMResponse{
		Content:          "answer",
		ReasoningContent: "reasoning",
		ToolCalls:        []ToolCall{{ID: "call", Name: "read_file"}},
		FinishReason:     "tool_calls",
	})

	if normalized.Text() != "answer" {
		t.Fatalf("Text() = %q", normalized.Text())
	}
	if normalized.ReasoningText() != "reasoning" {
		t.Fatalf("ReasoningText() = %q", normalized.ReasoningText())
	}
	if calls := normalized.ToolCalls(); len(calls) != 1 || calls[0].Name != "read_file" {
		t.Fatalf("ToolCalls() = %+v", calls)
	}
}
