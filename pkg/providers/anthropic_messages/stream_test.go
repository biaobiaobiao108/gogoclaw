// GoGoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package anthropicmessages

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestParseStreamResponse_StreamsThinkingAndText(t *testing.T) {
	stream := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"usage":{"input_tokens":12,"output_tokens":1}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"先分析"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"问题"}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"你"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"好"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`,
		"",
	}, "\n")

	var chunks []StreamChunk
	resp, err := parseStreamResponse(context.Background(), strings.NewReader(stream), func(chunk StreamChunk) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("parseStreamResponse() error = %v", err)
	}

	wantReasoning := []string{"先分析", "先分析问题"}
	var gotReasoning []string
	wantContent := []string{"你", "你好"}
	var gotContent []string
	for _, chunk := range chunks {
		if chunk.ReasoningContent != "" {
			gotReasoning = append(gotReasoning, chunk.ReasoningContent)
		}
		if chunk.Content != "" {
			gotContent = append(gotContent, chunk.Content)
		}
	}
	if strings.Join(gotReasoning, "|") != strings.Join(wantReasoning, "|") {
		t.Fatalf("reasoning chunks = %#v, want %#v", gotReasoning, wantReasoning)
	}
	if strings.Join(gotContent, "|") != strings.Join(wantContent, "|") {
		t.Fatalf("content chunks = %#v, want %#v", gotContent, wantContent)
	}
	if resp.Content != "你好" {
		t.Errorf("Content = %q, want %q", resp.Content, "你好")
	}
	if resp.ReasoningContent != "先分析问题" {
		t.Errorf("ReasoningContent = %q, want %q", resp.ReasoningContent, "先分析问题")
	}
	if resp.Reasoning != resp.ReasoningContent {
		t.Errorf("Reasoning = %q, want %q", resp.Reasoning, resp.ReasoningContent)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", resp.FinishReason)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 12 || resp.Usage.CompletionTokens != 8 {
		t.Errorf("Usage = %#v, want prompt=12 completion=8", resp.Usage)
	}
}

func TestParseStreamResponse_AssemblesToolCall(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"weather"}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"Shanghai\"}"}}`,
		"",
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		"",
	}, "\n")

	resp, err := parseStreamResponse(context.Background(), strings.NewReader(stream), nil)
	if err != nil {
		t.Fatalf("parseStreamResponse() error = %v", err)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want tool_calls", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length = %d, want 1", len(resp.ToolCalls))
	}
	call := resp.ToolCalls[0]
	if call.ID != "toolu_1" || call.Name != "weather" {
		t.Errorf("ToolCall = %#v, want id=toolu_1 name=weather", call)
	}
	if call.Arguments["city"] != "Shanghai" {
		t.Errorf("ToolCall.Arguments = %#v, want city=Shanghai", call.Arguments)
	}
	if call.Function == nil || call.Function.Arguments != `{"city":"Shanghai"}` {
		t.Errorf("ToolCall.Function = %#v, want serialized arguments", call.Function)
	}
}

func TestParseStreamResponse_ReturnsStreamErrors(t *testing.T) {
	stream := "data: {\"type\":\"error\",\"error\":{\"message\":\"overloaded\"}}\n\n"
	_, err := parseStreamResponse(context.Background(), strings.NewReader(stream), nil)
	if err == nil || !strings.Contains(err.Error(), "overloaded") {
		t.Fatalf("parseStreamResponse() error = %v, want stream error", err)
	}

	_, err = parseStreamResponse(context.Background(), strings.NewReader("data: {bad json}\n\n"), nil)
	if err == nil || !strings.Contains(err.Error(), "decoding stream event") {
		t.Fatalf("parseStreamResponse() malformed JSON error = %v", err)
	}
}

func TestStreamingReadIdleTimeout_PropagatesRead(t *testing.T) {
	body := io.NopCloser(strings.NewReader("data: test\n\n"))
	wrapped := withStreamingReadIdleTimeout(body, 0)
	if wrapped != body {
		t.Fatal("zero timeout should return the original body")
	}
}
