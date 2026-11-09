// GoGoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package anthropicmessages

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers/common"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers/protocoltypes"
)

type (
	ToolCall               = protocoltypes.ToolCall
	FunctionCall           = protocoltypes.FunctionCall
	LLMResponse            = protocoltypes.LLMResponse
	StreamChunk            = protocoltypes.StreamChunk
	UsageInfo              = protocoltypes.UsageInfo
	Message                = protocoltypes.Message
	ToolDefinition         = protocoltypes.ToolDefinition
	ToolFunctionDefinition = protocoltypes.ToolFunctionDefinition
)

const (
	defaultAPIVersion     = "2023-06-01"
	defaultBaseURL        = "https://api.anthropic.com/v1"
	defaultRequestTimeout = 120 * time.Second
	streamIdleTimeout     = 5 * time.Minute
)

// Provider implements Anthropic Messages API via HTTP (without SDK).
// It supports custom endpoints that use Anthropic's native message format.
type Provider struct {
	apiKey     string
	apiBase    string
	httpClient *http.Client
	userAgent  string
}

// SupportsThinking implements providers.ThinkingCapable.
func (p *Provider) SupportsThinking() bool { return true }

// NewProvider creates a new Anthropic Messages API provider.
func NewProvider(apiKey, apiBase, userAgent string) *Provider {
	return NewProviderWithTimeout(apiKey, apiBase, userAgent, 0)
}

// NewProviderWithTimeout creates a provider with custom request timeout.
func NewProviderWithTimeout(apiKey, apiBase, userAgent string, timeoutSeconds int) *Provider {
	baseURL := common.NormalizeBaseURL(apiBase, defaultBaseURL, true)
	timeout := defaultRequestTimeout
	if timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}

	return &Provider{
		apiKey:    apiKey,
		apiBase:   baseURL,
		userAgent: userAgent,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Chat sends messages to the Anthropic Messages API and returns the response.
func (p *Provider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("API key not configured")
	}

	// Build request body
	requestBody, err := buildRequestBody(messages, tools, model, options)
	if err != nil {
		return nil, fmt.Errorf("building request body: %w", err)
	}

	// Serialize to JSON
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("serializing request body: %w", err)
	}

	// Build request URL
	endpointURL, err := url.JoinPath(p.apiBase, "messages")
	if err != nil {
		return nil, fmt.Errorf("building endpoint URL: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", endpointURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", p.apiKey) // Anthropic API requires exact header name
	req.Header.Set("Anthropic-Version", defaultAPIVersion)
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}

	// Execute request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	// Check for HTTP errors with detailed messages
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("authentication failed (401): check your API key")
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("rate limited (429): %s", string(body))
	case http.StatusBadRequest:
		return nil, fmt.Errorf("bad request (400): %s", string(body))
	case http.StatusNotFound:
		return nil, fmt.Errorf("endpoint not found (404): %s", string(body))
	case http.StatusInternalServerError:
		return nil, fmt.Errorf("internal server error (500): %s", string(body))
	case http.StatusServiceUnavailable:
		return nil, fmt.Errorf("service unavailable (503): %s", string(body))
	default:
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
		}
	}

	// Parse response
	return parseResponseBody(body)
}

// ChatStream implements providers.StreamingProvider.
func (p *Provider) ChatStream(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	onChunk func(accumulated string),
) (*LLMResponse, error) {
	return p.ChatStreamEvents(ctx, messages, tools, model, options, func(chunk StreamChunk) {
		if onChunk != nil && chunk.Content != "" {
			onChunk(chunk.Content)
		}
	})
}

// ChatStreamEvents streams Anthropic Messages SSE events, including thinking
// and tool-use deltas.
func (p *Provider) ChatStreamEvents(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	onChunk func(StreamChunk),
) (*LLMResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("API key not configured")
	}

	requestBody, err := buildRequestBody(messages, tools, model, options)
	if err != nil {
		return nil, fmt.Errorf("building request body: %w", err)
	}
	requestBody["stream"] = true

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("serializing request body: %w", err)
	}

	endpointURL, err := url.JoinPath(p.apiBase, "messages")
	if err != nil {
		return nil, fmt.Errorf("building endpoint URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-API-Key", p.apiKey)
	req.Header.Set("Anthropic-Version", defaultAPIVersion)
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}

	streamClient := &http.Client{Transport: p.httpClient.Transport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing streaming HTTP request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, common.HandleErrorResponse(resp, p.apiBase)
	}

	return parseStreamResponse(ctx, withStreamingReadIdleTimeout(resp.Body, streamIdleTimeout), onChunk)
}

func withStreamingReadIdleTimeout(body io.ReadCloser, timeout time.Duration) io.ReadCloser {
	if body == nil || timeout <= 0 {
		return body
	}
	return &streamingReadIdleTimeoutBody{body: body, timeout: timeout}
}

type streamingReadIdleTimeoutBody struct {
	body    io.ReadCloser
	timeout time.Duration
}

func (b *streamingReadIdleTimeoutBody) Read(p []byte) (int, error) {
	timedOut := make(chan struct{})
	timer := time.AfterFunc(b.timeout, func() {
		close(timedOut)
		_ = b.body.Close()
	})
	n, err := b.body.Read(p)
	if !timer.Stop() {
		<-timedOut
		return n, fmt.Errorf("stream idle timeout after %s", b.timeout)
	}
	return n, err
}

func (b *streamingReadIdleTimeoutBody) Close() error {
	return b.body.Close()
}

// GetDefaultModel returns the default model for this provider.
func (p *Provider) GetDefaultModel() string {
	return "claude-sonnet-4.6"
}

// buildRequestBody converts internal message format to Anthropic Messages API format.
func buildRequestBody(
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (map[string]any, error) {
	// max_tokens is required and guaranteed by agent loop
	maxTokens, ok := common.AsInt(options["max_tokens"])
	if !ok {
		return nil, fmt.Errorf("max_tokens is required in options")
	}

	result := map[string]any{
		"model":      model,
		"max_tokens": int64(maxTokens),
		"messages":   []any{},
	}

	// Set temperature from options
	if temp, ok := common.AsFloat(options["temperature"]); ok {
		result["temperature"] = temp
	}

	// Process messages
	var systemPrompt string
	var apiMessages []any

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			// Accumulate system messages
			if systemPrompt != "" {
				systemPrompt += "\n\n" + msg.Content
			} else {
				systemPrompt = msg.Content
			}

		case "user":
			if msg.ToolCallID != "" {
				// Tool result message — merge into previous user message if it contains tool_results
				toolResultBlock := map[string]any{
					"type":        "tool_result",
					"tool_use_id": msg.ToolCallID,
					"content":     msg.Content,
				}
				if len(apiMessages) > 0 {
					if prev, ok := apiMessages[len(apiMessages)-1].(map[string]any); ok && prev["role"] == "user" {
						if content, ok := prev["content"].([]map[string]any); ok {
							prev["content"] = append(content, toolResultBlock)
							continue
						}
					}
				}
				apiMessages = append(apiMessages, map[string]any{
					"role":    "user",
					"content": []map[string]any{toolResultBlock},
				})
			} else {
				// Regular user message
				apiMessages = append(apiMessages, map[string]any{
					"role":    "user",
					"content": msg.Content,
				})
			}

		case "assistant":
			content := []any{}

			// Add text content if present
			if msg.Content != "" {
				content = append(content, map[string]any{
					"type": "text",
					"text": msg.Content,
				})
			}

			// Add tool_use blocks
			for _, tc := range msg.ToolCalls {
				// Resolve tool name: prefer tc.Name, fallback to tc.Function.Name
				// (tc.Name/tc.Arguments are json:"-" and may be empty when
				// history is reloaded from the session store)
				toolName := tc.Name
				if toolName == "" && tc.Function != nil {
					toolName = tc.Function.Name
				}
				if strings.TrimSpace(toolName) == "" {
					continue
				}

				// Resolve arguments: prefer tc.Arguments, fallback to parsing
				// tc.Function.Arguments
				input := tc.Arguments
				if input == nil && tc.Function != nil && tc.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
						input = map[string]any{}
					}
				}
				// Handle nil Arguments (GLM-4 may return null input)
				if input == nil {
					input = map[string]any{}
				}

				toolUse := map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  toolName,
					"input": input,
				}
				content = append(content, toolUse)
			}

			apiMessages = append(apiMessages, map[string]any{
				"role":    "assistant",
				"content": content,
			})

		case "tool":
			// Tool result (alternative format) — merge into previous user message if it contains tool_results
			toolResultBlock := map[string]any{
				"type":        "tool_result",
				"tool_use_id": msg.ToolCallID,
				"content":     msg.Content,
			}
			if len(apiMessages) > 0 {
				if prev, ok := apiMessages[len(apiMessages)-1].(map[string]any); ok && prev["role"] == "user" {
					if content, ok := prev["content"].([]map[string]any); ok {
						prev["content"] = append(content, toolResultBlock)
						continue
					}
				}
			}
			apiMessages = append(apiMessages, map[string]any{
				"role":    "user",
				"content": []map[string]any{toolResultBlock},
			})
		}
	}

	result["messages"] = apiMessages

	// Set system prompt if present
	if systemPrompt != "" {
		result["system"] = systemPrompt
	}

	applyThinkingOptions(result, int(maxTokens), options)

	// Add tools if present
	if len(tools) > 0 {
		result["tools"] = buildTools(tools)
	}

	return result, nil
}

func applyThinkingOptions(result map[string]any, maxTokens int, options map[string]any) {
	level, _ := options["thinking_level"].(string)
	level = strings.ToLower(strings.TrimSpace(level))
	if level == "" || level == "off" {
		return
	}

	// Anthropic rejects temperature when thinking is enabled.
	delete(result, "temperature")
	if level == "adaptive" {
		result["thinking"] = map[string]any{
			"type":    "adaptive",
			"display": "summarized",
		}
		result["output_config"] = map[string]any{"effort": "high"}
		return
	}

	budgets := map[string]int{
		"low":    4096,
		"medium": 16384,
		"high":   32000,
		"xhigh":  64000,
	}
	budget, ok := budgets[level]
	if !ok {
		return
	}
	if maxTokens > 1 && budget >= maxTokens {
		budget = maxTokens - 1
	}
	result["thinking"] = map[string]any{
		"type":          "enabled",
		"budget_tokens": budget,
	}
}

// buildTools converts tool definitions to Anthropic format.
func buildTools(tools []ToolDefinition) []any {
	result := make([]any, len(tools))
	for i, tool := range tools {
		toolDef := map[string]any{
			"name":         tool.Function.Name,
			"description":  tool.Function.Description,
			"input_schema": tool.Function.Parameters,
		}
		result[i] = toolDef
	}
	return result
}

// parseResponseBody parses Anthropic Messages API response.
func parseResponseBody(body []byte) (*LLMResponse, error) {
	var resp anthropicMessageResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing JSON response: %w", err)
	}

	// Extract content and tool calls
	var content strings.Builder
	var reasoning strings.Builder
	toolCalls := make([]ToolCall, 0) // Initialize as empty slice (not nil) for consistent JSON serialization

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "thinking":
			reasoning.WriteString(block.Thinking)
		case "tool_use":
			argsJSON, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
				Function: &FunctionCall{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	// Map stop_reason
	finishReason := "stop"
	switch resp.StopReason {
	case "tool_use":
		finishReason = "tool_calls"
	case "max_tokens":
		finishReason = "length"
	case "model_context_window_exceeded":
		finishReason = "length"
	case "refusal":
		finishReason = "content_filter"
	case "pause_turn":
		finishReason = "pause_turn"
	case "end_turn":
		finishReason = "stop"
	case "stop_sequence":
		finishReason = "stop"
	}

	return &LLMResponse{
		Content:          content.String(),
		ReasoningContent: reasoning.String(),
		Reasoning:        reasoning.String(),
		ToolCalls:        toolCalls,
		FinishReason:     finishReason,
		Usage: &UsageInfo{
			PromptTokens:     int(resp.Usage.InputTokens),
			CompletionTokens: int(resp.Usage.OutputTokens),
			TotalTokens:      int(resp.Usage.InputTokens + resp.Usage.OutputTokens),
		},
	}, nil
}

// Anthropic API response structures

type anthropicMessageResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Model      string         `json:"model"`
	Usage      usageInfo      `json:"usage"`
}

type contentBlock struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	Thinking string         `json:"thinking,omitempty"`
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name,omitempty"`
	Input    map[string]any `json:"input,omitempty"`
}

type usageInfo struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

type anthropicStreamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage usageInfo `json:"usage"`
	} `json:"message,omitempty"`
	ContentBlock *contentBlock `json:"content_block,omitempty"`
	Delta        *struct {
		Type        string `json:"type"`
		Text        string `json:"text,omitempty"`
		Thinking    string `json:"thinking,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
		StopReason  string `json:"stop_reason,omitempty"`
	} `json:"delta,omitempty"`
	Usage *usageInfo `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func parseStreamResponse(
	ctx context.Context,
	reader io.Reader,
	onChunk func(StreamChunk),
) (*LLMResponse, error) {
	var content strings.Builder
	var reasoning strings.Builder
	var finishReason string
	var usage usageInfo

	type toolAccum struct {
		id       string
		name     string
		argsJSON strings.Builder
	}
	tools := make(map[int]*toolAccum)

	processEvent := func(data string) error {
		if strings.TrimSpace(data) == "" {
			return nil
		}

		var event anthropicStreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("decoding stream event: %w", err)
		}
		if event.Error != nil {
			return fmt.Errorf("Anthropic stream error: %s", event.Error.Message)
		}
		if event.Message != nil {
			if event.Message.Usage.InputTokens > 0 {
				usage.InputTokens = event.Message.Usage.InputTokens
			}
			if event.Message.Usage.OutputTokens > 0 {
				usage.OutputTokens = event.Message.Usage.OutputTokens
			}
		}
		if event.Usage != nil {
			if event.Usage.InputTokens > 0 {
				usage.InputTokens = event.Usage.InputTokens
			}
			if event.Usage.OutputTokens > 0 {
				usage.OutputTokens = event.Usage.OutputTokens
			}
		}

		switch event.Type {
		case "content_block_start":
			if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
				tools[event.Index] = &toolAccum{
					id:   event.ContentBlock.ID,
					name: event.ContentBlock.Name,
				}
			}
		case "content_block_delta":
			if event.Delta == nil {
				return nil
			}
			if event.Delta.Thinking != "" {
				reasoning.WriteString(event.Delta.Thinking)
				if onChunk != nil {
					onChunk(StreamChunk{ReasoningContent: reasoning.String()})
				}
			}
			if event.Delta.Text != "" {
				content.WriteString(event.Delta.Text)
				if onChunk != nil {
					onChunk(StreamChunk{Content: content.String()})
				}
			}
			if event.Delta.PartialJSON != "" {
				acc := tools[event.Index]
				if acc == nil {
					acc = &toolAccum{}
					tools[event.Index] = acc
				}
				acc.argsJSON.WriteString(event.Delta.PartialJSON)
			}
		case "message_delta":
			if event.Delta != nil && event.Delta.StopReason != "" {
				finishReason = mapStopReason(event.Delta.StopReason)
			}
		}
		return nil
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	var data strings.Builder
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := scanner.Text()
		if line == "" {
			if err := processEvent(data.String()); err != nil {
				return nil, err
			}
			data.Reset()
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		chunk := strings.TrimPrefix(line, "data:")
		chunk = strings.TrimPrefix(chunk, " ")
		if data.Len() > 0 {
			data.WriteByte('\n')
		}
		data.WriteString(chunk)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading stream: %w", err)
	}
	if data.Len() > 0 {
		if err := processEvent(data.String()); err != nil {
			return nil, err
		}
	}

	toolIndexes := make([]int, 0, len(tools))
	for index := range tools {
		toolIndexes = append(toolIndexes, index)
	}
	sort.Ints(toolIndexes)

	toolCalls := make([]ToolCall, 0, len(tools))
	for _, index := range toolIndexes {
		acc := tools[index]
		args := make(map[string]any)
		raw := acc.argsJSON.String()
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &args); err != nil {
				args = map[string]any{"raw": raw}
			}
		}
		argsJSON, _ := json.Marshal(args)
		toolCalls = append(toolCalls, ToolCall{
			ID:        acc.id,
			Name:      acc.name,
			Arguments: args,
			Function: &FunctionCall{
				Name:      acc.name,
				Arguments: string(argsJSON),
			},
		})
	}
	if finishReason == "" {
		finishReason = "stop"
	}

	return &LLMResponse{
		Content:          content.String(),
		ReasoningContent: reasoning.String(),
		Reasoning:        reasoning.String(),
		ToolCalls:        toolCalls,
		FinishReason:     finishReason,
		Usage: &UsageInfo{
			PromptTokens:     int(usage.InputTokens),
			CompletionTokens: int(usage.OutputTokens),
			TotalTokens:      int(usage.InputTokens + usage.OutputTokens),
		},
	}, nil
}

func mapStopReason(reason string) string {
	switch reason {
	case "tool_use":
		return "tool_calls"
	case "max_tokens", "model_context_window_exceeded":
		return "length"
	case "refusal":
		return "content_filter"
	case "pause_turn":
		return "pause_turn"
	default:
		return "stop"
	}
}
