package agent

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func sanitizeHistoryForProvider(history []providers.Message) []providers.Message {
	if len(history) == 0 {
		return history
	}

	sanitized := make([]providers.Message, 0, len(history))
	for _, msg := range history {
		switch msg.Role {
		case "system", "developer":
			// Drop privileged messages from history. BuildMessages always
			// constructs its own single system message (static + dynamic +
			// summary); extra system messages would break providers that
			// only accept one (such as Anthropic). Tool output that fabricates a
			// developer role must not be promoted into an instruction message.
			logger.DebugCF("agent", "Dropping privileged message from history", map[string]any{"role": msg.Role})
			continue

		case "tool":
			if len(sanitized) == 0 {
				logger.DebugCF("agent", "Dropping orphaned leading tool message", map[string]any{})
				continue
			}
			// Walk backwards to find the nearest assistant message,
			// skipping over any preceding tool messages (multi-tool-call case).
			foundAssistant := false
			for i := len(sanitized) - 1; i >= 0; i-- {
				if sanitized[i].Role == "tool" {
					continue
				}
				if sanitized[i].Role == "assistant" && len(sanitized[i].ToolCalls) > 0 {
					foundAssistant = true
				}
				break
			}
			if !foundAssistant {
				logger.DebugCF("agent", "Dropping orphaned tool message", map[string]any{})
				continue
			}
			sanitized = append(sanitized, msg)

		case "assistant":
			if len(msg.ToolCalls) > 0 {
				if len(sanitized) == 0 {
					logger.DebugCF(
						"agent",
						"Dropping assistant tool-call turn at history start",
						map[string]any{},
					)
					continue
				}
				prev := sanitized[len(sanitized)-1]
				if prev.Role != "user" && prev.Role != "tool" {
					logger.DebugCF(
						"agent",
						"Dropping assistant tool-call turn with invalid predecessor",
						map[string]any{"prev_role": prev.Role},
					)
					continue
				}
			}
			sanitized = append(sanitized, msg)

		case "user":
			sanitized = append(sanitized, msg)

		default:
			logger.DebugCF("agent", "Dropping unsupported message role from history", map[string]any{"role": msg.Role})
		}
	}

	// Second pass: ensure every assistant message with tool_calls has matching
	// tool result messages following it. This is required by strict providers
	// like DeepSeek that enforce: "An assistant message with 'tool_calls' must
	// be followed by tool messages responding to each 'tool_call_id'."
	//
	// Deduplication is scoped to the contiguous tool-result block that follows a
	// single assistant tool-call message. Some providers legitimately reuse call
	// IDs across separate turns (for example "call_0"), so global deduplication
	// would incorrectly delete later valid tool results and leave an
	// assistant(tool_calls) -> assistant sequence behind.
	final := make([]providers.Message, 0, len(sanitized))
	for i := 0; i < len(sanitized); i++ {
		msg := sanitized[i]

		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			expected := make(map[string]bool, len(msg.ToolCalls))
			invalidToolCallID := false
			for _, tc := range msg.ToolCalls {
				if tc.ID == "" {
					invalidToolCallID = true
					continue
				}
				expected[tc.ID] = false
			}

			block := make([]providers.Message, 0, len(expected))
			seenInBlock := make(map[string]bool, len(expected))
			j := i + 1
			for ; j < len(sanitized); j++ {
				next := sanitized[j]
				if next.Role != "tool" {
					break
				}
				if next.ToolCallID == "" {
					logger.DebugCF(
						"agent",
						"Dropping tool result without tool_call_id",
						map[string]any{},
					)
					continue
				}
				if _, ok := expected[next.ToolCallID]; !ok {
					logger.DebugCF("agent", "Dropping unexpected tool result", map[string]any{
						"tool_call_id": next.ToolCallID,
					})
					continue
				}
				if seenInBlock[next.ToolCallID] {
					logger.DebugCF(
						"agent",
						"Dropping duplicate tool result in tool block",
						map[string]any{
							"tool_call_id": next.ToolCallID,
						},
					)
					continue
				}
				seenInBlock[next.ToolCallID] = true
				expected[next.ToolCallID] = true
				block = append(block, next)
			}

			allFound := !invalidToolCallID
			if invalidToolCallID {
				logger.DebugCF(
					"agent",
					"Dropping assistant message with empty tool_call_id",
					map[string]any{},
				)
			}
			for toolCallID, found := range expected {
				if !found {
					allFound = false
					logger.DebugCF(
						"agent",
						"Dropping assistant message with incomplete tool results",
						map[string]any{
							"missing_tool_call_id": toolCallID,
							"expected_count":       len(expected),
							"found_count":          len(block),
						},
					)
					break
				}
			}

			if !allFound {
				i = j - 1
				continue
			}

			final = append(final, msg)
			final = append(final, block...)
			i = j - 1
			continue
		}

		if msg.Role == "tool" {
			logger.DebugCF(
				"agent",
				"Dropping orphaned tool message after validation",
				map[string]any{
					"tool_call_id": msg.ToolCallID,
				},
			)
			continue
		}

		final = append(final, msg)
	}

	return final
}

const defaultHistoricalToolOutputLimit = 800

func (cb *ContextBuilder) pruneHistoricalToolOutputs(history []providers.Message) []providers.Message {
	withArtifacts := append([]providers.Message(nil), history...)
	for i, msg := range withArtifacts {
		if msg.Role != "tool" || len(msg.Content) <= defaultHistoricalToolOutputLimit || strings.Contains(msg.Content, "[file:") {
			continue
		}
		hash := sha256.Sum256([]byte(msg.Content))
		path := filepath.Join(cb.workspace, "state", "tool_outputs", fmt.Sprintf("history-%x.txt", hash[:16]))
		if _, err := os.Stat(path); err != nil {
			if err := fileutil.WriteFileAtomic(path, []byte(msg.Content), 0o600); err != nil {
				logger.WarnCF("agent", "Historical tool output could not be archived", map[string]any{"error": err.Error()})
				// Retain the source when a recoverable prompt view cannot be built.
				return history
			}
		}
		withArtifacts[i].Content = msg.Content + "\n[file:" + path + "]"
	}
	return pruneHistoricalToolOutputs(withArtifacts)
}

// pruneHistoricalToolOutputs compacts oversized tool results from completed
// historical turns in the prompt view, preventing multi-turn context explosion.
// It preserves tool_call_id pairs, metadata, and artifact file pointers while
// pruning redundant intermediate text bodies.
func pruneHistoricalToolOutputs(history []providers.Message) []providers.Message {
	if len(history) == 0 {
		return history
	}

	pruned := make([]providers.Message, len(history))
	for i, msg := range history {
		if msg.Role != "tool" || len(msg.Content) <= defaultHistoricalToolOutputLimit {
			pruned[i] = msg
			continue
		}

		cloned := msg
		cloned.Content = pruneHistoricalToolContent(msg.Content, defaultHistoricalToolOutputLimit)
		pruned[i] = cloned
	}
	return pruned
}

func pruneHistoricalToolContent(content string, limit int) string {
	if len(content) <= limit {
		return content
	}

	artifactTag := ""
	if idx := strings.Index(content, "[file:"); idx != -1 {
		if endIdx := strings.Index(content[idx:], "]"); endIdx != -1 {
			artifactTag = content[idx : idx+endIdx+1]
		}
	}

	notice := "\n\n[... historical tool output pruned to save context tokens ...]"
	if artifactTag != "" {
		notice += "\nFull content: " + artifactTag
	}
	if len(notice) > limit {
		notice = notice[:max(0, limit)]
		for !utf8.ValidString(notice) && len(notice) > 0 {
			notice = notice[:len(notice)-1]
		}
		return notice
	}
	// Bound bytes as well as tokens, including one-line JSON and UTF-8 text.
	head := content[:max(0, min(len(content), limit-len(notice)))]
	for !utf8.ValidString(head) && len(head) > 0 {
		head = head[:len(head)-1]
	}

	return head + notice
}

func (cb *ContextBuilder) AddToolResult(
	messages []providers.Message,
	toolCallID, toolName, result string,
) []providers.Message {
	messages = append(messages, providers.Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: toolCallID,
	})
	return messages
}

func (cb *ContextBuilder) AddAssistantMessage(
	messages []providers.Message,
	content string,
	toolCalls []map[string]any,
) []providers.Message {
	msg := providers.Message{
		Role:    "assistant",
		Content: content,
	}
	// Always add assistant message, whether or not it has tool calls
	messages = append(messages, msg)
	return messages
}
