// GoGoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package anthropicmessages

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestChatStreamSendsImageContent(t *testing.T) {
	wantMessages := `[
		{"role":"user","content":[
			{"type":"text","text":"Inspect this"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1hZ2U="}}
		]},
		{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"load_image","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"Image loaded"}]},
		{"role":"user","content":[
			{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"anBlZw=="}},
			{"type":"image","source":{"type":"url","url":"https://example.com/image.png"}}
		]}
	]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []any `json:"messages"`
			Stream   bool  `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var want []any
		if err := json.Unmarshal([]byte(wantMessages), &want); err != nil {
			t.Errorf("decode expected messages: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if !request.Stream || !reflect.DeepEqual(request.Messages, want) {
			t.Errorf("unexpected streaming request: %+v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	provider := NewProviderWithTimeout("test-key", server.URL, "", 0)
	_, err := provider.ChatStreamEvents(t.Context(), []Message{
		{Role: "user", Content: "Inspect this", Media: []string{"data:image/png;base64,aW1hZ2U="}},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "load_image"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "Image loaded"},
		{Role: "user", Media: []string{"data:image/jpeg;base64,anBlZw==", "https://example.com/image.png"}},
	}, nil, "test-model", map[string]any{"max_tokens": 64}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

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

func TestParseStreamResponse_AssemblesToolCallsAfterThinkingAndTextBlocks(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"先查垂类子域"}}`,
		"",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		"",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"先取子域清单。"}}`,
		"",
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_2","name":"get_sub_domains"}}`,
		"",
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"domain\":\"finance\"}"}}`,
		"",
		`data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_3","name":"get_sub_domains"}}`,
		"",
		`data: {"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"domain\":\"academic\"}"}}`,
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
	if resp.ReasoningContent != "先查垂类子域" {
		t.Errorf("ReasoningContent = %q, want %q", resp.ReasoningContent, "先查垂类子域")
	}
	if resp.Content != "先取子域清单。" {
		t.Errorf("Content = %q, want %q", resp.Content, "先取子域清单。")
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("ToolCalls length = %d, want 2", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "toolu_2" || resp.ToolCalls[0].Arguments["domain"] != "finance" {
		t.Errorf("first ToolCall = %#v, want toolu_2 domain=finance", resp.ToolCalls[0])
	}
	if resp.ToolCalls[1].ID != "toolu_3" || resp.ToolCalls[1].Arguments["domain"] != "academic" {
		t.Errorf("second ToolCall = %#v, want toolu_3 domain=academic", resp.ToolCalls[1])
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
