// GoGoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package openairesponses

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildRequestBody(t *testing.T) {
	p := NewProvider("test-key", "https://api.openai.com/v1", "", "test-agent", 0, nil, nil)
	body, err := p.buildRequestBody(
		[]Message{{Role: "system", Content: "Be concise"}, {Role: "user", Content: "Hello"}},
		[]ToolDefinition{{
			Type: "function",
			Function: ToolFunctionDefinition{
				Name:        "weather",
				Description: "Get weather",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
		"gpt-5.4",
		map[string]any{
			"max_tokens":       2048,
			"temperature":      0.2,
			"thinking_level":   "medium",
			"prompt_cache_key": "chat-1",
			"native_search":    false,
		},
		false,
	)
	if err != nil {
		t.Fatalf("buildRequestBody() error = %v", err)
	}
	if body["model"] != "gpt-5.4" {
		t.Errorf("model = %#v, want gpt-5.4", body["model"])
	}
	if body["max_output_tokens"] != float64(2048) {
		t.Errorf("max_output_tokens = %#v, want 2048", body["max_output_tokens"])
	}
	if body["instructions"] != "Be concise" {
		t.Errorf("instructions = %#v, want Be concise", body["instructions"])
	}
	if body["prompt_cache_key"] != "chat-1" {
		t.Errorf("prompt_cache_key = %#v, want chat-1", body["prompt_cache_key"])
	}
	if _, ok := body["stream"]; ok {
		t.Fatal("non-stream request should not set stream")
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "medium" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v, want medium/auto", body["reasoning"])
	}
}

func TestChatUsesResponsesEndpointAndBearerAuth(t *testing.T) {
	var gotPath, gotAuth string
	var gotStream any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var requestBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotStream = requestBody["stream"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"resp_1",
			"object":"response",
			"status":"completed",
			"output":[{
				"id":"msg_1",
				"type":"message",
				"role":"assistant",
				"status":"completed",
				"content":[{"type":"output_text","text":"hello","annotations":[]}]
			}],
			"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}
		}`)
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL+"/v1", "", "", 0, nil, map[string]string{"X-Test": "yes"})
	resp, err := p.Chat(
		context.Background(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"gpt-5.4",
		map[string]any{"max_tokens": 128},
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if gotPath != "/v1/responses" {
		t.Errorf("path = %q, want /v1/responses", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", gotAuth)
	}
	if gotStream != nil {
		t.Errorf("stream = %#v, want omitted", gotStream)
	}
	if resp.Content != "hello" {
		t.Errorf("Content = %q, want hello", resp.Content)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 5 {
		t.Errorf("Usage = %#v, want total=5", resp.Usage)
	}
}

func TestParseStreamResponse_StreamsReasoningAndText(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"先分析","item_id":"r1","output_index":0,"summary_index":0,"sequence_number":1}`,
		"",
		`data: {"type":"response.reasoning_summary_text.delta","delta":"问题","item_id":"r1","output_index":0,"summary_index":0,"sequence_number":2}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"你","item_id":"m1","output_index":1,"content_index":0,"logprobs":[],"sequence_number":3}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"好","item_id":"m1","output_index":1,"content_index":0,"logprobs":[],"sequence_number":4}`,
		"",
		`data: {"type":"response.completed","sequence_number":5,"response":{"id":"resp_1","object":"response","status":"completed","output":[{"id":"r1","type":"reasoning","summary":[{"type":"summary_text","text":"先分析问题"}]},{"id":"m1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"你好","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":6,"total_tokens":16}}}`,
		"",
	}, "\n")

	var chunks []StreamChunk
	resp, err := parseStreamResponse(context.Background(), strings.NewReader(stream), func(chunk StreamChunk) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("parseStreamResponse() error = %v", err)
	}
	if len(chunks) != 4 {
		t.Fatalf("chunks = %d, want 4", len(chunks))
	}
	if chunks[0].ReasoningContent != "先分析" || chunks[0].Content != "" {
		t.Errorf("first chunk = %#v", chunks[0])
	}
	if chunks[1].ReasoningContent != "先分析问题" || chunks[1].Content != "" {
		t.Errorf("second chunk = %#v", chunks[1])
	}
	if chunks[2].ReasoningContent != "先分析问题" || chunks[2].Content != "你" {
		t.Errorf("third chunk = %#v", chunks[2])
	}
	if chunks[3].Content != "你好" {
		t.Errorf("fourth chunk = %#v", chunks[3])
	}
	if resp.Content != "你好" || resp.ReasoningContent != "先分析问题" {
		t.Errorf("response = %#v", resp)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 16 {
		t.Errorf("Usage = %#v, want total=16", resp.Usage)
	}
}

func TestParseStreamResponse_AssemblesFinalToolCall(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.output_item.done","output_index":0,"sequence_number":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Shanghai\"}","status":"completed"}}`,
		"",
		`data: {"type":"response.completed","sequence_number":2,"response":{"id":"resp_1","object":"response","status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Shanghai\"}","status":"completed"}]}}`,
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
		t.Fatalf("ToolCalls = %d, want 1", len(resp.ToolCalls))
	}
	call := resp.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "weather" || call.Arguments["city"] != "Shanghai" {
		t.Errorf("ToolCall = %#v", call)
	}
}

func TestParseStreamResponse_UsesOutputItemWhenCompletedEventIsMissing(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.output_item.done","output_index":0,"sequence_number":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Shanghai\"}","status":"completed"}}`,
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
		t.Fatalf("ToolCalls = %d, want 1", len(resp.ToolCalls))
	}
	call := resp.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "weather" || call.Arguments["city"] != "Shanghai" {
		t.Errorf("ToolCall = %#v", call)
	}
}

func TestParseStreamResponse_ReturnsErrorEvents(t *testing.T) {
	stream := `data: {"type":"error","code":"overloaded","message":"try later","param":"","sequence_number":1}` + "\n\n"
	_, err := parseStreamResponse(context.Background(), strings.NewReader(stream), nil)
	if err == nil || !strings.Contains(err.Error(), "try later") {
		t.Fatalf("error = %v, want stream error", err)
	}
}
