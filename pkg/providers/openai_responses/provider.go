// GoGoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package openairesponses

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers/common"
	orc "github.com/biaobiaobiao108/gogoclaw/pkg/providers/openai_responses_common"
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
	defaultBaseURL             = "https://api.openai.com/v1"
	defaultModel               = "gpt-5.4"
	defaultRequestTimeout      = common.DefaultRequestTimeout
	defaultStreamIdleTimeout   = 5 * time.Minute
	responseEndpointPath       = "responses"
	responseOutputTextDelta    = "response.output_text.delta"
	reasoningSummaryTextDelta  = "response.reasoning_summary_text.delta"
	reasoningTextDelta         = "response.reasoning_text.delta"
	responseOutputItemDone     = "response.output_item.done"
	responseCompleted          = "response.completed"
	responseFailed             = "response.failed"
	responseIncomplete         = "response.incomplete"
	responseError              = "error"
	responseReasoningTextDone  = "response.reasoning_text.done"
	responseReasoningTitleDone = "response.reasoning_summary_text.done"
)

// Provider implements the OpenAI Responses API over plain HTTP.
type Provider struct {
	apiKey        string
	apiBase       string
	httpClient    *http.Client
	userAgent     string
	extraBody     map[string]any
	customHeaders map[string]string
}

func NewProvider(
	apiKey, apiBase, proxy, userAgent string,
	requestTimeoutSeconds int,
	extraBody map[string]any,
	customHeaders map[string]string,
) *Provider {
	baseURL := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	client := common.NewHTTPClient(proxy)
	if requestTimeoutSeconds > 0 {
		client.Timeout = time.Duration(requestTimeoutSeconds) * time.Second
	}
	return &Provider{
		apiKey:        strings.TrimSpace(apiKey),
		apiBase:       baseURL,
		httpClient:    client,
		userAgent:     userAgent,
		extraBody:     cloneMap(extraBody),
		customHeaders: cloneStringMap(customHeaders),
	}
}

func (p *Provider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	requestBody, err := p.buildRequestBody(messages, tools, model, options, false)
	if err != nil {
		return nil, err
	}
	resp, err := p.doRequest(ctx, requestBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, common.HandleErrorResponse(resp, p.apiBase)
	}
	return orc.ParseResponseBody(resp.Body)
}

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

func (p *Provider) ChatStreamEvents(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	onChunk func(StreamChunk),
) (*LLMResponse, error) {
	requestBody, err := p.buildRequestBody(messages, tools, model, options, true)
	if err != nil {
		return nil, err
	}
	resp, err := p.doRequest(ctx, requestBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, common.HandleErrorResponse(resp, p.apiBase)
	}
	return parseStreamResponse(
		ctx,
		withStreamingReadIdleTimeout(resp.Body, defaultStreamIdleTimeout),
		onChunk,
	)
}

func (p *Provider) GetDefaultModel() string {
	return defaultModel
}

func (p *Provider) SupportsThinking() bool {
	return true
}

func (p *Provider) SupportsNativeSearch() bool {
	return true
}

func (p *Provider) buildRequestBody(
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	stream bool,
) (map[string]any, error) {
	input, instructions := orc.TranslateMessages(messages)
	params := responses.ResponseNewParams{
		Model: model,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Store: openai.Opt(false),
	}
	if instructions != "" {
		params.Instructions = openai.Opt(instructions)
	}
	if maxTokens, ok := common.AsInt(options["max_tokens"]); ok {
		params.MaxOutputTokens = openai.Opt(int64(maxTokens))
	}
	if temperature, ok := common.AsFloat(options["temperature"]); ok {
		params.Temperature = openai.Opt(temperature)
	}
	if cacheKey, ok := options["prompt_cache_key"].(string); ok && cacheKey != "" {
		params.PromptCacheKey = openai.Opt(cacheKey)
	}
	enableWebSearch, _ := options["native_search"].(bool)
	if len(tools) > 0 || enableWebSearch {
		params.Tools = orc.TranslateTools(tools, enableWebSearch)
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptionsAuto),
		}
	}
	if effort, ok := responsesReasoningEffort(options["thinking_level"]); ok {
		params.Reasoning = shared.ReasoningParam{
			Effort:  effort,
			Summary: shared.ReasoningSummaryAuto,
		}
	}

	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshaling responses request: %w", err)
	}
	var requestBody map[string]any
	if err := json.Unmarshal(raw, &requestBody); err != nil {
		return nil, fmt.Errorf("building responses request: %w", err)
	}
	for key, value := range p.extraBody {
		requestBody[key] = value
	}
	if stream {
		requestBody["stream"] = true
	} else {
		delete(requestBody, "stream")
	}
	return requestBody, nil
}

func (p *Provider) doRequest(ctx context.Context, requestBody map[string]any) (*http.Response, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("API key not configured")
	}
	endpointURL, err := url.JoinPath(p.apiBase, responseEndpointPath)
	if err != nil {
		return nil, fmt.Errorf("building Responses endpoint: %w", err)
	}
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("serializing Responses request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating Responses request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Accept", "application/json")
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}
	if requestBody["stream"] == true {
		req.Header.Set("Accept", "text/event-stream")
	}
	for key, value := range p.customHeaders {
		req.Header.Set(key, value)
	}
	client := p.httpClient
	if requestBody["stream"] == true {
		// The request timeout covers the complete response body. Streaming
		// requests use the idle timeout wrapper instead.
		client = &http.Client{Transport: p.httpClient.Transport}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing Responses request: %w", err)
	}
	return resp, nil
}

func responsesReasoningEffort(raw any) (shared.ReasoningEffort, bool) {
	level, _ := raw.(string)
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "minimal":
		return shared.ReasoningEffortMinimal, true
	case "low":
		return shared.ReasoningEffortLow, true
	case "medium":
		return shared.ReasoningEffortMedium, true
	case "high", "adaptive":
		return shared.ReasoningEffortHigh, true
	case "xhigh":
		return shared.ReasoningEffortXhigh, true
	case "max":
		return shared.ReasoningEffortMax, true
	default:
		return "", false
	}
}

func parseStreamResponse(
	ctx context.Context,
	reader io.Reader,
	onChunk func(StreamChunk),
) (*LLMResponse, error) {
	var content strings.Builder
	var reasoning strings.Builder
	var final responses.Response
	var completed bool
	var outputItems []responses.ResponseOutputItemUnion

	processEvent := func(data string) error {
		if strings.TrimSpace(data) == "" || strings.TrimSpace(data) == "[DONE]" {
			return nil
		}
		var event responses.ResponseStreamEventUnion
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("decoding Responses stream event: %w", err)
		}

		switch event.Type {
		case responseError:
			apiErr := event.AsError()
			return fmt.Errorf("Responses stream error: %s", apiErr.Message)
		case responseOutputTextDelta:
			delta := event.AsResponseOutputTextDelta()
			content.WriteString(delta.Delta)
			if onChunk != nil {
				onChunk(StreamChunk{Content: content.String(), ReasoningContent: reasoning.String()})
			}
		case reasoningSummaryTextDelta:
			delta := event.AsResponseReasoningSummaryTextDelta()
			reasoning.WriteString(delta.Delta)
			if onChunk != nil {
				onChunk(StreamChunk{Content: content.String(), ReasoningContent: reasoning.String()})
			}
		case reasoningTextDelta:
			delta := event.AsResponseReasoningTextDelta()
			reasoning.WriteString(delta.Delta)
			if onChunk != nil {
				onChunk(StreamChunk{Content: content.String(), ReasoningContent: reasoning.String()})
			}
		case responseReasoningTextDone:
			// The final response includes the complete reasoning output. This
			// branch only prevents unhandled-event churn in proxies.
		case responseReasoningTitleDone:
		case responseOutputItemDone:
			item := event.AsResponseOutputItemDone().Item
			if item.Type != "" {
				outputItems = append(outputItems, item)
			}
		case responseCompleted:
			final = event.AsResponseCompleted().Response
			completed = true
		case responseIncomplete:
			final = event.AsResponseIncomplete().Response
			completed = true
		case responseFailed:
			failed := event.AsResponseFailed().Response
			if failed.Error.Message != "" {
				return fmt.Errorf("Responses request failed: %s", failed.Error.Message)
			}
			return fmt.Errorf("Responses request failed")
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
		chunk := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data.Len() > 0 {
			data.WriteByte('\n')
		}
		data.WriteString(chunk)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading Responses stream: %w", err)
	}
	if data.Len() > 0 {
		if err := processEvent(data.String()); err != nil {
			return nil, err
		}
	}

	if completed {
		if len(final.Output) == 0 && len(outputItems) > 0 {
			final.Output = outputItems
		}
		parsed := orc.ParseResponseFromStruct(&final)
		if parsed.Content == "" {
			parsed.Content = content.String()
		}
		if parsed.ReasoningContent == "" {
			parsed.ReasoningContent = reasoning.String()
		}
		if parsed.Reasoning == "" {
			parsed.Reasoning = parsed.ReasoningContent
		}
		return parsed, nil
	}
	if content.Len() == 0 && reasoning.Len() == 0 && len(outputItems) == 0 {
		return nil, fmt.Errorf("Responses stream ended without a completed response")
	}
	return &LLMResponse{
		Content:          content.String(),
		ReasoningContent: reasoning.String(),
		Reasoning:        reasoning.String(),
		FinishReason:     "stop",
	}, nil
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

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
