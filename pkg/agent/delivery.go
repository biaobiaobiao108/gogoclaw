package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

const (
	deliveryStatusQueued        = "queued"
	deliveryStatusStreaming     = "streaming"
	deliveryStatusDeliveredBus  = "delivered_to_bus"
	deliveryStatusAcknowledged  = "acknowledged"
	deliveryStatusDeliveredTool = "delivered_by_tool"
	deliveryStatusFailed        = "failed"
	deliveryStatusDeadLetter    = "dead_letter"

	deliveryOutboxLimit    = 200
	deliveryMaxAttempts    = 8
	deliveryLeaseDuration  = 30 * time.Second
	deliveryAckGracePeriod = 12 * time.Second
	deliveryRetryInterval  = 3 * time.Second

	metadataKeyDeliveryID = "delivery_id"
)

type deliveryLocation struct {
	agentID    string
	sessionKey string
}

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
	previous := checkpointDeliveries(checkpoint)
	checkpoint.Deliveries = upsertTurnDelivery(checkpoint.Deliveries, delivery)
	checkpoint.Delivery = &delivery
	if err := store.SetTurnCheckpoint(sessionKey, checkpoint); err != nil {
		return err
	}
	if delivery.ID != "" {
		al.deliveryLocations.Store(delivery.ID, deliveryLocation{
			agentID:    agent.ID,
			sessionKey: sessionKey,
		})
	}
	for _, prior := range previous {
		if prior.ID != "" && !containsDelivery(checkpoint.Deliveries, prior.ID) {
			al.deliveryLocations.Delete(prior.ID)
		}
	}
	return nil
}

func upsertTurnDelivery(deliveries []session.TurnDelivery, delivery session.TurnDelivery) []session.TurnDelivery {
	for i := range deliveries {
		if deliveries[i].ID == delivery.ID {
			deliveries[i] = delivery
			return deliveries
		}
	}
	deliveries = append(deliveries, delivery)
	if overflow := len(deliveries) - deliveryOutboxLimit; overflow > 0 {
		deliveries = append([]session.TurnDelivery(nil), deliveries[overflow:]...)
	}
	return deliveries
}

func checkpointDeliveries(checkpoint session.TurnCheckpoint) []session.TurnDelivery {
	if len(checkpoint.Deliveries) > 0 {
		return checkpoint.Deliveries
	}
	if checkpoint.Delivery != nil && checkpoint.Delivery.ID != "" {
		return []session.TurnDelivery{*checkpoint.Delivery}
	}
	return nil
}

func containsDelivery(deliveries []session.TurnDelivery, deliveryID string) bool {
	for _, delivery := range deliveries {
		if delivery.ID == deliveryID {
			return true
		}
	}
	return false
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
	now := time.Now().UTC()
	delivery := turnDeliveryFromOutbound(msg)
	delivery.Status = deliveryStatusQueued
	delivery.CreatedAt = now
	if err := al.setTurnDelivery(sessionKey, delivery); err != nil {
		return fmt.Errorf("persist queued delivery: %w", err)
	}
	markTrackedDelivery(&msg, delivery)
	markFinalOutbound(&msg)
	if err := al.bus.PublishOutbound(ctx, msg); err != nil {
		delivery.Status = deliveryStatusFailed
		delivery.Attempts = 1
		delivery.LastError = err.Error()
		delivery.NextAttemptAt = nil
		if persistErr := al.setTurnDelivery(sessionKey, delivery); persistErr != nil {
			return fmt.Errorf("publish final response: %v (persist failure: %w)", err, persistErr)
		}
		return fmt.Errorf("publish final response: %w", err)
	}
	delivery.Status = deliveryStatusDeliveredBus
	delivery.Attempts = 1
	if delivery.RequiresAck {
		nextAttempt := now.Add(deliveryAckGracePeriod)
		leaseUntil := now.Add(deliveryLeaseDuration)
		delivery.NextAttemptAt = &nextAttempt
		delivery.LeaseUntil = &leaseUntil
	} else {
		acknowledgedAt := now
		delivery.Status = deliveryStatusAcknowledged
		delivery.AcknowledgedAt = &acknowledgedAt
	}
	if err := al.setTurnDelivery(sessionKey, delivery); err != nil {
		return fmt.Errorf("mark response delivered: %w", err)
	}
	return nil
}

func turnDeliveryFromOutbound(msg bus.OutboundMessage) session.TurnDelivery {
	return session.TurnDelivery{
		ID:           "delivery-" + uuid.NewString(),
		Status:       deliveryStatusQueued,
		Channel:      msg.Context.Channel,
		ChatID:       msg.Context.ChatID,
		AgentID:      msg.AgentID,
		Context:      cloneInboundContext(&msg.Context),
		Scope:        cloneOutboundScope(msg.Scope),
		Content:      msg.Content,
		ContextUsage: cloneContextUsage(msg.ContextUsage),
		RequiresAck:  msg.Context.Channel == "pico",
	}
}

func cloneOutboundScope(scope *bus.OutboundScope) *bus.OutboundScope {
	if scope == nil {
		return nil
	}
	cloned := *scope
	cloned.Dimensions = append([]string(nil), scope.Dimensions...)
	if len(scope.Values) > 0 {
		cloned.Values = cloneStringMap(scope.Values)
	}
	return &cloned
}

func cloneContextUsage(usage *bus.ContextUsage) *bus.ContextUsage {
	if usage == nil {
		return nil
	}
	cloned := *usage
	return &cloned
}

func outboundFromTurnDelivery(sessionKey string, delivery session.TurnDelivery) bus.OutboundMessage {
	contextValue := bus.NewOutboundContext(delivery.Channel, delivery.ChatID, "")
	if delivery.Context != nil {
		contextValue = *cloneInboundContext(delivery.Context)
	}
	return bus.OutboundMessage{
		Channel:      contextValue.Channel,
		ChatID:       contextValue.ChatID,
		Context:      contextValue,
		AgentID:      delivery.AgentID,
		SessionKey:   sessionKey,
		Scope:        cloneOutboundScope(delivery.Scope),
		Content:      delivery.Content,
		ContextUsage: cloneContextUsage(delivery.ContextUsage),
	}
}

func (al *AgentLoop) prepareStreamingDelivery(ts *turnState) session.TurnDelivery {
	msg := outboundMessageForTurn(ts, "")
	delivery := turnDeliveryFromOutbound(msg)
	delivery.Status = deliveryStatusStreaming
	if err := al.setTurnDelivery(ts.sessionKey, delivery); err != nil {
		logger.WarnCF("agent", "Failed to persist streaming delivery", map[string]any{
			"session_key": ts.sessionKey, "delivery_id": delivery.ID, "error": err.Error(),
		})
	}
	return delivery
}

func (al *AgentLoop) completeStreamingDelivery(
	sessionKey string,
	delivery session.TurnDelivery,
	content string,
	contextUsage *bus.ContextUsage,
	visible bool,
) error {
	now := time.Now().UTC()
	delivery.Content = content
	delivery.ContextUsage = cloneContextUsage(contextUsage)
	delivery.Attempts = 1
	delivery.UpdatedAt = now
	if !visible {
		delivery.Status = deliveryStatusFailed
		delivery.LastError = "streamed final message was not delivered"
		delivery.NextAttemptAt = &now
		delivery.LeaseUntil = nil
	} else {
		delivery.Status = deliveryStatusDeliveredBus
		delivery.LastError = ""
		if delivery.RequiresAck {
			nextAttempt := now.Add(deliveryAckGracePeriod)
			leaseUntil := now.Add(deliveryLeaseDuration)
			delivery.NextAttemptAt = &nextAttempt
			delivery.LeaseUntil = &leaseUntil
		} else {
			delivery.Status = deliveryStatusAcknowledged
			delivery.AcknowledgedAt = &now
			delivery.NextAttemptAt = nil
			delivery.LeaseUntil = nil
		}
	}
	return al.setTurnDelivery(sessionKey, delivery)
}

func (al *AgentLoop) failStreamingDelivery(sessionKey string, delivery session.TurnDelivery, streamErr error) {
	now := time.Now().UTC()
	delivery.Status = deliveryStatusFailed
	delivery.Attempts = 1
	if streamErr != nil {
		delivery.LastError = streamErr.Error()
	}
	delivery.NextAttemptAt = nil
	delivery.LeaseUntil = nil
	delivery.UpdatedAt = now
	if err := al.setTurnDelivery(sessionKey, delivery); err != nil {
		logger.WarnCF("agent", "Failed to persist failed streaming delivery", map[string]any{
			"session_key": sessionKey, "delivery_id": delivery.ID, "error": err.Error(),
		})
	}
}

func (al *AgentLoop) discardDelivery(sessionKey, deliveryID string) {
	if al == nil || sessionKey == "" || deliveryID == "" {
		return
	}
	agent := al.agentForSession(sessionKey)
	if agent == nil {
		return
	}
	store, ok := agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		return
	}
	checkpoint, err := store.GetTurnCheckpoint(sessionKey)
	if err != nil {
		return
	}
	deliveries := checkpointDeliveries(checkpoint)
	kept := deliveries[:0]
	for _, delivery := range deliveries {
		if delivery.ID != deliveryID {
			kept = append(kept, delivery)
		}
	}
	checkpoint.Deliveries = kept
	if checkpoint.Delivery != nil && checkpoint.Delivery.ID == deliveryID {
		checkpoint.Delivery = nil
	}
	_ = store.SetTurnCheckpoint(sessionKey, checkpoint)
	al.deliveryLocations.Delete(deliveryID)
}

func markTrackedDelivery(msg *bus.OutboundMessage, delivery session.TurnDelivery) {
	if msg == nil || delivery.ID == "" {
		return
	}
	if msg.Context.Raw == nil {
		msg.Context.Raw = make(map[string]string, 2)
	}
	msg.Context.Raw[metadataKeyDeliveryID] = delivery.ID
}

func (al *AgentLoop) runDeliveryOutbox(ctx context.Context) {
	al.retryPendingDelivery(ctx)
	ticker := time.NewTicker(deliveryRetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			al.retryPendingDelivery(ctx)
		}
	}
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
			if err != nil {
				continue
			}
			for _, candidate := range checkpointDeliveries(checkpoint) {
				if candidate.Status == deliveryStatusStreaming {
					candidate = reconcileInterruptedStreamingDelivery(candidate, time.Now().UTC())
					if err := al.setTurnDelivery(sessionKey, candidate); err != nil {
						logger.WarnCF("agent", "Failed to reconcile interrupted streaming delivery", map[string]any{
							"session_key": sessionKey, "delivery_id": candidate.ID, "error": err.Error(),
						})
						continue
					}
				}
				if !deliveryNeedsDispatch(candidate, time.Now().UTC()) {
					continue
				}
				delivery := candidate
				if delivery.Content == "" || delivery.Channel == "" || delivery.ChatID == "" {
					continue
				}
				attempt := delivery.Attempts + 1
				if attempt > deliveryMaxAttempts {
					deadLetteredAt := time.Now().UTC()
					delivery.Status = deliveryStatusDeadLetter
					delivery.DeadLetteredAt = &deadLetteredAt
					delivery.NextAttemptAt = nil
					delivery.LeaseUntil = nil
					_ = al.setTurnDelivery(sessionKey, delivery)
					continue
				}
				retryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				msg := outboundFromTurnDelivery(sessionKey, delivery)
				markTrackedDelivery(&msg, delivery)
				markFinalOutbound(&msg)
				err = al.bus.PublishOutbound(retryCtx, msg)
				cancel()
				now := time.Now().UTC()
				delivery.Attempts = attempt
				if err == nil {
					delivery.Status = deliveryStatusDeliveredBus
					delivery.LastError = ""
					if delivery.RequiresAck {
						nextAttempt := now.Add(deliveryAckGracePeriod)
						leaseUntil := now.Add(deliveryLeaseDuration)
						delivery.NextAttemptAt = &nextAttempt
						delivery.LeaseUntil = &leaseUntil
					} else {
						delivery.Status = deliveryStatusAcknowledged
						delivery.AcknowledgedAt = &now
						delivery.NextAttemptAt = nil
						delivery.LeaseUntil = nil
					}
				} else {
					delivery.Status = deliveryStatusFailed
					delivery.LastError = err.Error()
					nextAttempt := now.Add(time.Duration(attempt) * time.Second)
					delivery.NextAttemptAt = &nextAttempt
					delivery.LeaseUntil = nil
					if attempt >= deliveryMaxAttempts {
						delivery.Status = deliveryStatusDeadLetter
						delivery.DeadLetteredAt = &now
						delivery.NextAttemptAt = nil
					}
				}
				if persistErr := al.setTurnDelivery(sessionKey, delivery); persistErr != nil {
					logger.WarnCF("agent", "Failed to persist delivery retry", map[string]any{
						"session_key": sessionKey, "delivery_id": delivery.ID, "error": persistErr.Error(),
					})
				}
				if err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Duration(attempt) * time.Second):
					}
				}
			}
		}
	}
}

// reconcileInterruptedStreamingDelivery repairs a checkpoint left in the
// transient streaming state by a process exit. A completed payload can be
// retried with its original delivery ID; a partial payload cannot be finalized
// safely and is moved to the dead letter queue for inspection.
func reconcileInterruptedStreamingDelivery(delivery session.TurnDelivery, now time.Time) session.TurnDelivery {
	delivery.LeaseUntil = nil
	delivery.UpdatedAt = now
	if strings.TrimSpace(delivery.Content) == "" {
		delivery.Status = deliveryStatusDeadLetter
		delivery.LastError = "streaming delivery interrupted before final content was persisted"
		delivery.NextAttemptAt = nil
		delivery.DeadLetteredAt = &now
		return delivery
	}
	delivery.Status = deliveryStatusFailed
	delivery.LastError = "streaming delivery interrupted after final content was persisted"
	delivery.NextAttemptAt = &now
	return delivery
}

func deliveryNeedsDispatch(delivery session.TurnDelivery, now time.Time) bool {
	switch delivery.Status {
	case deliveryStatusQueued, deliveryStatusFailed:
		return delivery.NextAttemptAt == nil || !delivery.NextAttemptAt.After(now)
	case deliveryStatusDeliveredBus:
		if !delivery.RequiresAck {
			return false
		}
		if delivery.LeaseUntil != nil && delivery.LeaseUntil.After(now) {
			return false
		}
		return delivery.NextAttemptAt == nil || !delivery.NextAttemptAt.After(now)
	default:
		return false
	}
}

func (al *AgentLoop) AcknowledgeDelivery(ctx context.Context, channel, chatID, deliveryID string) error {
	if al == nil || deliveryID == "" {
		return fmt.Errorf("delivery id is required")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	registry := al.GetRegistry()
	if registry == nil {
		return fmt.Errorf("agent registry is unavailable")
	}
	if location, ok := al.deliveryLocations.Load(deliveryID); ok {
		loc, valid := location.(deliveryLocation)
		if valid {
			if acknowledged, err := al.acknowledgeDeliveryAt(
				loc.sessionKey,
				deliveryID,
				channel,
				chatID,
			); acknowledged || err != nil {
				return err
			}
			al.deliveryLocations.Delete(deliveryID)
		}
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
			if err != nil {
				continue
			}
			for _, candidate := range checkpointDeliveries(checkpoint) {
				if candidate.ID != deliveryID {
					continue
				}
				al.deliveryLocations.Store(deliveryID, deliveryLocation{
					agentID:    agent.ID,
					sessionKey: sessionKey,
				})
				_, err := al.acknowledgeDeliveryAt(sessionKey, deliveryID, channel, chatID)
				return err
			}
		}
	}
	return fmt.Errorf("delivery %s not found", deliveryID)
}

func (al *AgentLoop) acknowledgeDeliveryAt(
	sessionKey, deliveryID, channel, chatID string,
) (bool, error) {
	agent := al.agentForSession(sessionKey)
	if agent == nil {
		return false, nil
	}
	store, ok := agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		return false, nil
	}
	checkpoint, err := store.GetTurnCheckpoint(sessionKey)
	if err != nil {
		return false, err
	}
	for _, candidate := range checkpointDeliveries(checkpoint) {
		if candidate.ID != deliveryID {
			continue
		}
		if channel != "" && candidate.Channel != channel {
			return true, fmt.Errorf("delivery %s belongs to channel %s", deliveryID, candidate.Channel)
		}
		if chatID != "" && candidate.ChatID != chatID {
			return true, fmt.Errorf("delivery %s belongs to chat %s", deliveryID, candidate.ChatID)
		}
		if candidate.Status == deliveryStatusAcknowledged {
			return true, nil
		}
		now := time.Now().UTC()
		candidate.Status = deliveryStatusAcknowledged
		candidate.AcknowledgedAt = &now
		candidate.NextAttemptAt = nil
		candidate.LeaseUntil = nil
		return true, al.setTurnDelivery(sessionKey, candidate)
	}
	return false, nil
}
