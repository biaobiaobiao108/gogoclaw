package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/session"
)

const (
	deliveryStatusQueued        = "queued"
	deliveryStatusDeliveredBus  = "delivered_to_bus"
	deliveryStatusDeliveredTool = "delivered_by_tool"
	deliveryStatusFailed        = "failed"
)

func (al *AgentLoop) setTurnDelivery(sessionKey string, delivery session.TurnDelivery) error {
	if al == nil || sessionKey == "" {
		return nil
	}
	agent := al.agentForSession(sessionKey)
	if agent == nil {
		return nil
	}
	store, ok := agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		return nil
	}
	checkpoint, err := store.GetTurnCheckpoint(sessionKey)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = now
	}
	delivery.UpdatedAt = now
	checkpoint.Delivery = &delivery
	return store.SetTurnCheckpoint(sessionKey, checkpoint)
}

func (al *AgentLoop) publishTrackedResponse(
	ctx context.Context,
	channel, chatID, sessionKey, response string,
	usage *bus.ContextUsage,
) error {
	if response == "" {
		return nil
	}
	msg := bus.OutboundMessage{
		Context:      bus.NewOutboundContext(channel, chatID, ""),
		SessionKey:   sessionKey,
		Content:      response,
		ContextUsage: usage,
	}
	return al.publishTrackedOutbound(ctx, sessionKey, msg)
}

func (al *AgentLoop) publishTrackedOutbound(ctx context.Context, sessionKey string, msg bus.OutboundMessage) error {
	if msg.Content == "" {
		return nil
	}
	delivery := session.TurnDelivery{
		ID:     fmt.Sprintf("delivery-%d", time.Now().UnixNano()),
		Status: deliveryStatusQueued, Channel: msg.Context.Channel, ChatID: msg.Context.ChatID, Content: msg.Content,
	}
	if err := al.setTurnDelivery(sessionKey, delivery); err != nil {
		return fmt.Errorf("persist queued delivery: %w", err)
	}
	markFinalOutbound(&msg)
	if err := al.bus.PublishOutbound(ctx, msg); err != nil {
		delivery.Status = deliveryStatusFailed
		delivery.Attempts = 1
		delivery.LastError = err.Error()
		if persistErr := al.setTurnDelivery(sessionKey, delivery); persistErr != nil {
			return fmt.Errorf("publish final response: %v (persist failure: %w)", err, persistErr)
		}
		return fmt.Errorf("publish final response: %w", err)
	}
	delivery.Status = deliveryStatusDeliveredBus
	delivery.Attempts = 1
	if err := al.setTurnDelivery(sessionKey, delivery); err != nil {
		return fmt.Errorf("mark response delivered: %w", err)
	}
	return nil
}

func (al *AgentLoop) retryPendingDelivery(ctx context.Context) {
	if al == nil || al.bus == nil {
		return
	}
	registry := al.GetRegistry()
	if registry == nil {
		return
	}
	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok || agent == nil {
			continue
		}
		store, ok := agent.Sessions.(session.TurnCheckpointStore)
		if !ok {
			continue
		}
		for _, sessionKey := range agent.Sessions.ListSessions() {
			checkpoint, err := store.GetTurnCheckpoint(sessionKey)
			if err != nil || checkpoint.Delivery == nil {
				continue
			}
			if checkpoint.Delivery.Status != deliveryStatusQueued && checkpoint.Delivery.Status != deliveryStatusFailed {
				continue
			}
			delivery := *checkpoint.Delivery
			if delivery.Content == "" || delivery.Channel == "" || delivery.ChatID == "" {
				continue
			}
			for attempt := delivery.Attempts + 1; attempt <= 3; attempt++ {
				retryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				msg := bus.OutboundMessage{
					Context:    bus.NewOutboundContext(delivery.Channel, delivery.ChatID, ""),
					SessionKey: sessionKey, Content: delivery.Content,
				}
				markFinalOutbound(&msg)
				err = al.bus.PublishOutbound(retryCtx, msg)
				cancel()
				delivery.Attempts = attempt
				if err == nil {
					delivery.Status = deliveryStatusDeliveredBus
					delivery.LastError = ""
					break
				}
				delivery.LastError = err.Error()
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(attempt) * time.Second):
				}
			}
			if persistErr := al.setTurnDelivery(sessionKey, delivery); persistErr != nil {
				logger.WarnCF("agent", "Failed to persist delivery retry", map[string]any{
					"session_key": sessionKey, "error": persistErr.Error(),
				})
			}
		}
	}
}
