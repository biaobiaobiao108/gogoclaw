// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

func (al *AgentLoop) maybePublishError(ctx context.Context, channel, chatID, sessionKey string, err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if pubErr := al.PublishResponseIfNeededChecked(ctx, channel, chatID, sessionKey, formatProcessingError(err)); pubErr != nil {
		logger.ErrorCF("agent", "Failed to publish processing error", map[string]any{
			"channel": channel, "chat_id": chatID, "error": pubErr.Error(),
		})
	}
	return true
}

func (al *AgentLoop) publishResponseOrError(
	ctx context.Context,
	channel, chatID, sessionKey string,
	response string,
	err error,
) {
	if err != nil {
		if !al.maybePublishError(ctx, channel, chatID, sessionKey, err) {
			return
		}
		response = ""
	}
	if pubErr := al.PublishResponseIfNeededChecked(ctx, channel, chatID, sessionKey, response); pubErr != nil {
		logger.ErrorCF("agent", "Failed to deliver response", map[string]any{
			"channel": channel, "chat_id": chatID, "session_key": sessionKey, "error": pubErr.Error(),
		})
	}
}

func (al *AgentLoop) PublishResponseIfNeeded(ctx context.Context, channel, chatID, sessionKey, response string) {
	if err := al.PublishResponseIfNeededChecked(ctx, channel, chatID, sessionKey, response); err != nil {
		logger.WarnCF("agent", "Failed to publish final response", map[string]any{
			"channel": channel, "chat_id": chatID, "session_key": sessionKey, "error": err.Error(),
		})
	}
}

func (al *AgentLoop) PublishResponseIfNeededChecked(ctx context.Context, channel, chatID, sessionKey, response string) error {
	if response == "" {
		return nil
	}

	alreadySentToSameChat := false
	runtime := al.runtimeSnapshot()
	defaultAgent := runtime.registry.GetDefaultAgent()
	if defaultAgent != nil {
		if tool, ok := defaultAgent.Tools.Get("message"); ok {
			if mt, ok := tool.(*tools.MessageTool); ok {
				alreadySentToSameChat = mt.HasSentTo(sessionKey, channel, chatID)
			}
		}
	}

	if alreadySentToSameChat {
		if runtime.channelManager != nil && channel != "" && chatID != "" {
			dismissCtx, dismissCancel := context.WithTimeout(ctx, 5*time.Second)
			runtime.channelManager.DismissToolFeedback(
				dismissCtx,
				channel,
				chatID,
				nil,
			)
			dismissCancel()
		}
		logger.DebugCF(
			"agent",
			"Skipped outbound (message tool already sent to same chat)",
			map[string]any{"channel": channel, "chat_id": chatID},
		)
		return al.setTurnDelivery(sessionKey, session.TurnDelivery{
			ID: fmt.Sprintf("delivery-%d", time.Now().UnixNano()), Status: deliveryStatusDeliveredTool,
			Channel: channel, ChatID: chatID,
		})
	}

	var usage *bus.ContextUsage
	if sessionKey != "" {
		usage = computeContextUsage(al.agentForSession(sessionKey), sessionKey)
		if agent := al.agentForSession(sessionKey); agent != nil {
			if store, ok := agent.Sessions.(session.TurnCheckpointStore); ok {
				checkpoint, err := store.GetTurnCheckpoint(sessionKey)
				if err != nil {
					return err
				}
				if checkpoint.Usage != nil {
					usage = checkpoint.Usage
				}
			}
		}
	}
	if err := al.publishTrackedResponse(ctx, channel, chatID, sessionKey, response, usage); err != nil {
		return err
	}
	logger.InfoCF("agent", "Published outbound response",
		map[string]any{
			"channel":     channel,
			"chat_id":     chatID,
			"content_len": len(response),
		})
	return nil
}

func (al *AgentLoop) targetReasoningChannelID(channelName string) (chatID string) {
	channelManager := al.runtimeSnapshot().channelManager
	if channelManager == nil {
		return ""
	}
	if ch, ok := channelManager.GetChannel(channelName); ok {
		return ch.ReasoningChannelID()
	}
	return ""
}

func (al *AgentLoop) publishPicoReasoning(
	ctx context.Context,
	reasoningContent, chatID, sessionKey, modelName string,
	messageIDs ...string,
) {
	if reasoningContent == "" || chatID == "" {
		return
	}

	if ctx.Err() != nil {
		return
	}

	pubCtx, pubCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pubCancel()

	raw := map[string]string{metadataKeyMessageKind: messageKindThought}
	if trimmedModelName := strings.TrimSpace(modelName); trimmedModelName != "" {
		raw["model_name"] = trimmedModelName
	}

	messageID := ""
	if len(messageIDs) > 0 {
		messageID = messageIDs[0]
	}
	if err := al.bus.PublishOutbound(pubCtx, bus.OutboundMessage{
		ID: messageID,
		Context: bus.InboundContext{
			Channel: "pico",
			ChatID:  chatID,
			Raw:     raw,
		},
		SessionKey: sessionKey,
		Content:    reasoningContent,
	}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
			errors.Is(err, bus.ErrBusClosed) {
			logger.DebugCF("agent", "Pico reasoning publish skipped (timeout/cancel)", map[string]any{
				"channel": "pico",
				"error":   err.Error(),
			})
		} else {
			logger.WarnCF("agent", "Failed to publish pico reasoning (best-effort)", map[string]any{
				"channel": "pico",
				"error":   err.Error(),
			})
		}
	}
}

func (al *AgentLoop) publishPicoToolCallInterim(
	ctx context.Context,
	ts *turnState,
	modelName string,
	reasoningContent string,
	content string,
	toolCalls []providers.ToolCall,
	messageIDs ...string,
) {
	if ts == nil || ts.chatID == "" || al == nil || al.bus == nil {
		return
	}

	messageID := fmt.Sprintf("%s-llm-%d", ts.turnID, ts.currentIteration())
	if len(messageIDs) > 0 {
		messageID = messageIDs[0]
	}
	if strings.TrimSpace(reasoningContent) != "" {
		pubCtx, pubCancel := context.WithTimeout(ctx, 3*time.Second)
		err := al.bus.PublishOutbound(
			pubCtx,
			outboundMessageForTurnWithOptions(
				ts,
				reasoningContent,
				outboundTurnMessageOptions{
					kind:      messageKindThought,
					messageID: messageID + "-thought",
					modelName: modelName,
				},
			),
		)
		pubCancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, context.Canceled) &&
			!errors.Is(err, bus.ErrBusClosed) {
			logger.WarnCF("agent", "Failed to publish pico reasoning", map[string]any{
				"channel": ts.channel,
				"chat_id": ts.chatID,
				"error":   err.Error(),
			})
		}
	}

	if !ts.opts.AllowInterimPicoPublish {
		return
	}

	visibleToolCalls := utils.BuildVisibleToolCalls(
		toolCalls,
		al.runtimeSnapshot().cfg.Agents.Defaults.GetToolFeedbackMaxArgsLength(),
	)
	duplicateToolCallContent := len(visibleToolCalls) > 0 &&
		utils.ToolCallExplanationDuplicatesContent(content, toolCalls)

	if strings.TrimSpace(content) != "" && !duplicateToolCallContent {
		pubCtx, pubCancel := context.WithTimeout(ctx, 3*time.Second)
		err := al.bus.PublishOutbound(
			pubCtx,
			outboundMessageForTurnWithOptions(ts, content, outboundTurnMessageOptions{
				messageID: messageID,
				modelName: modelName,
			}),
		)
		pubCancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, context.Canceled) &&
			!errors.Is(err, bus.ErrBusClosed) {
			logger.WarnCF("agent", "Failed to publish pico interim assistant content", map[string]any{
				"channel": ts.channel,
				"chat_id": ts.chatID,
				"error":   err.Error(),
			})
		}
	}

	if len(visibleToolCalls) == 0 {
		return
	}

	rawToolCalls, err := json.Marshal(visibleToolCalls)
	if err != nil {
		logger.WarnCF("agent", "Failed to serialize pico tool calls", map[string]any{
			"channel": ts.channel,
			"chat_id": ts.chatID,
			"error":   err.Error(),
		})
		return
	}

	msg := outboundMessageForTurnWithOptions(ts, "", outboundTurnMessageOptions{
		kind:      messageKindToolCalls,
		modelName: modelName,
		raw: map[string]string{
			metadataKeyToolCalls: string(rawToolCalls),
		},
	})

	if len(messageIDs) > 0 {
		msg.ID = messageIDs[0] + "-tools"
	}
	pubCtx, pubCancel := context.WithTimeout(ctx, 3*time.Second)
	err = al.bus.PublishOutbound(pubCtx, msg)
	pubCancel()
	if err != nil && !errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, context.Canceled) &&
		!errors.Is(err, bus.ErrBusClosed) {
		logger.WarnCF("agent", "Failed to publish pico tool calls", map[string]any{
			"channel": ts.channel,
			"chat_id": ts.chatID,
			"error":   err.Error(),
		})
	}
}

func (al *AgentLoop) handleReasoning(
	ctx context.Context,
	reasoningContent, channelName, channelID string,
) {
	if reasoningContent == "" || channelName == "" || channelID == "" {
		return
	}

	// Check context cancellation before attempting to publish,
	// since PublishOutbound's select may race between send and ctx.Done().
	if ctx.Err() != nil {
		return
	}

	// Use a short timeout so the goroutine does not block indefinitely when
	// the outbound bus is full.  Reasoning output is best-effort; dropping it
	// is acceptable to avoid goroutine accumulation.
	pubCtx, pubCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pubCancel()

	if err := al.bus.PublishOutbound(pubCtx, bus.OutboundMessage{
		Context: bus.NewOutboundContext(channelName, channelID, ""),
		Content: reasoningContent,
	}); err != nil {
		// Treat context.DeadlineExceeded / context.Canceled as expected
		// (bus full under load, or parent canceled).  Check the error
		// itself rather than ctx.Err(), because pubCtx may time out
		// (5 s) while the parent ctx is still active.
		// Also treat ErrBusClosed as expected — it occurs during normal
		// shutdown when the bus is closed before all goroutines finish.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
			errors.Is(err, bus.ErrBusClosed) {
			logger.DebugCF("agent", "Reasoning publish skipped (timeout/cancel)", map[string]any{
				"channel": channelName,
				"error":   err.Error(),
			})
		} else {
			logger.WarnCF("agent", "Failed to publish reasoning (best-effort)", map[string]any{
				"channel": channelName,
				"error":   err.Error(),
			})
		}
	}
}
