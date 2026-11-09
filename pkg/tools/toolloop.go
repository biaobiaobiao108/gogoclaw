// GoGoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

// ToolLoopConfig configures the tool execution loop.
type ToolLoopConfig struct {
	Provider      providers.LLMProvider
	Model         string
	Tools         *ToolRegistry
	MaxIterations int
	LLMOptions    map[string]any

	// MediaResolver resolves media:// refs in messages before each LLM call.
	// This is optional and is mainly used by subagent legacy fallback execution
	// so subagents can reuse the same multimodal media handling as the main loop.
	MediaResolver func(messages []providers.Message) []providers.Message
}

// ToolLoopResult contains the result of running the tool loop.
type ToolLoopResult struct {
	Content    string
	Iterations int
}

// RunToolLoop executes the LLM + tool call iteration loop.
// This is the core agent logic that can be reused by both main agent and subagents.
func RunToolLoop(
	ctx context.Context,
	config ToolLoopConfig,
	messages []providers.Message,
	channel, chatID string,
) (*ToolLoopResult, error) {
	if config.Provider == nil {
		return nil, fmt.Errorf("tool loop provider is required")
	}
	if config.MaxIterations <= 0 {
		return nil, fmt.Errorf("tool loop max iterations must be positive")
	}

	iteration := 0
	var finalContent string
	completed := false

	for iteration < config.MaxIterations {
		iteration++

		logger.DebugCF("toolloop", "LLM iteration",
			map[string]any{
				"iteration": iteration,
				"max":       config.MaxIterations,
			})

		// 1. Build tool definitions
		var providerToolDefs []providers.ToolDefinition
		if config.Tools != nil {
			providerToolDefs = config.Tools.ToProviderDefs()
		}

		// 2. Set default LLM options
		llmOpts := config.LLMOptions
		if llmOpts == nil {
			llmOpts = map[string]any{}
		}

		// 3. Resolve media:// refs and Call LLM.
		// Tools like load_image produce media:// refs in their result messages.
		// Without this step, the LLM would receive raw "media://uuid" strings
		// instead of base64-encoded image data URLs.
		//
		// We build a separate callMessages slice so that:
		//   (a) the resolver output is used for the LLM call only,
		//   (b) the original `messages` slice keeps the unresolved refs for
		//       subsequent iterations — the resolver is idempotent but working
		//       on the original avoids double-encoding issues.
		//
		// On iteration 1 the initial user messages typically have no media://
		// refs (they come from plain text), so this is effectively a no-op;
		// it becomes relevant from iteration 2 onward when tool results may
		// contain media refs.
		callMessages := messages
		if config.MediaResolver != nil && iteration > 1 {
			callMessages = config.MediaResolver(messages)
		}
		response, err := config.Provider.Chat(ctx, callMessages, providerToolDefs, config.Model, llmOpts)
		if err != nil {
			logger.ErrorCF("toolloop", "LLM call failed",
				map[string]any{
					"iteration": iteration,
					"error":     err.Error(),
				})
			return nil, fmt.Errorf("LLM call failed: %w", err)
		}
		if response == nil {
			return nil, fmt.Errorf("LLM returned an empty response")
		}

		// 4. If no tool calls, we're done
		if len(response.ToolCalls) == 0 {
			finalContent = response.Content
			completed = true
			logger.InfoCF("toolloop", "LLM response without tool calls (direct answer)",
				map[string]any{
					"iteration":     iteration,
					"content_chars": len(finalContent),
				})
			break
		}

		normalizedToolCalls := make([]providers.ToolCall, 0, len(response.ToolCalls))
		for _, tc := range response.ToolCalls {
			normalizedToolCalls = append(normalizedToolCalls, providers.NormalizeToolCall(tc))
		}

		// 5. Log tool calls
		toolNames := make([]string, 0, len(normalizedToolCalls))
		for _, tc := range normalizedToolCalls {
			toolNames = append(toolNames, tc.Name)
		}
		logger.InfoCF("toolloop", "LLM requested tool calls",
			map[string]any{
				"tools":     toolNames,
				"count":     len(normalizedToolCalls),
				"iteration": iteration,
			})

		// 6. Build assistant message with tool calls
		assistantMsg := providers.Message{
			Role:    "assistant",
			Content: response.Content,
		}
		for _, tc := range normalizedToolCalls {
			argumentsJSON, err := json.Marshal(tc.Arguments)
			if err != nil {
				logger.Warnf("toolloop: failed to marshal tool call arguments for %s: %v", tc.Name, err)
				argumentsJSON = []byte("{}")
			}
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, providers.ToolCall{
				ID:        tc.ID,
				Type:      "function",
				Name:      tc.Name,
				Arguments: tc.Arguments,
				Function: &providers.FunctionCall{
					Name:      tc.Name,
					Arguments: string(argumentsJSON),
				},
			})
		}
		messages = append(messages, assistantMsg)

		// Preserve the model's order: tool calls can depend on earlier side effects.
		for _, tc := range normalizedToolCalls {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			argsJSON, _ := json.Marshal(tc.Arguments)
			argsPreview := utils.Truncate(string(argsJSON), 200)
			logger.InfoCF("toolloop", fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
				map[string]any{"tool": tc.Name, "iteration": iteration})
			var result *ToolResult
			if config.Tools != nil {
				result = config.Tools.ExecuteWithContext(ctx, tc.Name, tc.Arguments, channel, chatID, nil)
			} else {
				result = ErrorResult("No tools available")
			}
			if result == nil {
				result = ErrorResult("Tool returned no result")
			}
			contentForLLM := result.ContentForLLM()

			toolMsg := providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: tc.ID,
			}
			if len(result.Media) > 0 && !result.ResponseHandled {
				toolMsg.Media = append(toolMsg.Media, result.Media...)
			}
			messages = append(messages, toolMsg)
		}
	}
	if !completed {
		return nil, fmt.Errorf("tool loop reached max iterations (%d) without a final response", config.MaxIterations)
	}

	return &ToolLoopResult{
		Content:    finalContent,
		Iterations: iteration,
	}, nil
}
