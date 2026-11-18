package agent

import (
	"context"
	"errors"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

func (al *AgentLoop) tracksPlatformDelivery(sessionKey, name string) bool {
	if sessionKey == "" {
		return false
	}
	agent := al.agentForSession(sessionKey)
	if agent == nil {
		return false
	}
	if _, ok := agent.Sessions.(session.TurnCheckpointStore); !ok {
		return false
	}
	var channels config.ChannelsConfig
	if cfg := al.runtimeSnapshot().cfg; cfg != nil {
		channels = cfg.Channels
	}
	return channels.TracksPlatformTextDelivery(name)
}

func (al *AgentLoop) changePlatformDelivery(
	sessionKey, id string,
	change func(*session.TurnDelivery) bool,
) (bool, error) {
	agent := al.agentForSession(sessionKey)
	if agent == nil {
		return false, nil
	}
	store, ok := agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		return false, nil
	}
	unlock := al.lockDeliveryCheckpoint(sessionKey)
	defer unlock()
	checkpoint, err := store.GetTurnCheckpoint(sessionKey)
	if err != nil {
		return false, err
	}
	for _, delivery := range checkpointDeliveries(checkpoint) {
		if delivery.ID != id || !delivery.PlatformTracked || !change(&delivery) {
			continue
		}
		delivery.UpdatedAt = time.Now().UTC()
		checkpoint.Deliveries = upsertTurnDelivery(checkpoint.Deliveries, delivery)
		if checkpoint.Delivery != nil && checkpoint.Delivery.ID == id {
			checkpoint.Delivery = &delivery
		}
		if err := store.SetTurnCheckpoint(sessionKey, checkpoint); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// BeginPlatformDelivery claims a queued message before the channel sends any part.
func (al *AgentLoop) BeginPlatformDelivery(msg bus.OutboundMessage) (bool, error) {
	return al.changePlatformDelivery(msg.SessionKey, msg.Context.Raw[bus.MetadataPlatformDelivery], func(d *session.TurnDelivery) bool {
		if d.Status != deliveryStatusQueued {
			return false
		}
		d.Status = deliveryStatusDispatching
		d.DispatchOwner = platformDeliveryOwner
		d.NextAttemptAt = nil
		return true
	})
}

// FinishPlatformDelivery records platform acceptance or a terminal outcome.
func (al *AgentLoop) FinishPlatformDelivery(msg bus.OutboundMessage, ids []string, sendErr error) error {
	_, err := al.changePlatformDelivery(msg.SessionKey, msg.Context.Raw[bus.MetadataPlatformDelivery], func(d *session.TurnDelivery) bool {
		if d.Status != deliveryStatusDispatching || d.DispatchOwner != platformDeliveryOwner {
			return false
		}
		d.DispatchOwner = ""
		d.PlatformIDs = ids
		if sendErr == nil {
			now := time.Now().UTC()
			d.Status = deliveryStatusAcknowledged
			d.AcknowledgedAt = &now
			d.LastError = ""
		} else if errors.Is(sendErr, channels.ErrSendFailed) || errors.Is(sendErr, channels.ErrNotRunning) ||
			errors.Is(sendErr, channels.ErrTemporary) || errors.Is(sendErr, channels.ErrRateLimit) {
			d.Status = deliveryStatusDeadLetter
			now := time.Now().UTC()
			d.DeadLetteredAt = &now
			d.LastError = "platform rejected delivery or retry limit reached"
		} else {
			d.Status = deliveryStatusUnknown
			d.LastError = "platform acceptance could not be verified"
		}
		return true
	})
	return err
}

func (al *AgentLoop) retryPlatformDelivery(ctx context.Context, sessionKey string, candidate session.TurnDelivery) {
	now := time.Now().UTC()
	if candidate.Status != deliveryStatusDispatching && !deliveryNeedsDispatch(candidate, now) {
		return
	}
	if candidate.Status == deliveryStatusDispatching && candidate.DispatchOwner == platformDeliveryOwner {
		return
	}

	reoffer := false
	_, err := al.changePlatformDelivery(sessionKey, candidate.ID, func(d *session.TurnDelivery) bool {
		switch {
		case d.Status == deliveryStatusDispatching && d.DispatchOwner != platformDeliveryOwner:
			d.Status = deliveryStatusUnknown
			d.DispatchOwner = ""
			d.LastError = "gateway restarted during platform delivery"
		case d.Status == deliveryStatusQueued && deliveryNeedsDispatch(*d, now):
			if d.Attempts >= deliveryMaxAttempts {
				d.Status = deliveryStatusDeadLetter
				d.DeadLetteredAt = &now
				d.NextAttemptAt = nil
				d.LastError = "no channel worker claimed the message"
				return true
			}
			d.Attempts++
			next := now.Add(deliveryLeaseDuration)
			d.NextAttemptAt = &next
			reoffer = true
		default:
			return false
		}
		return true
	})
	if err == nil && reoffer {
		msg := outboundFromTurnDelivery(sessionKey, candidate)
		markTrackedDelivery(&msg, candidate)
		if msg.Context.Raw[metadataKeyOutboundKind] == "" {
			markFinalOutbound(&msg)
		}
		retryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = al.bus.PublishOutbound(retryCtx, msg)
		cancel()
	}
	if err != nil {
		logger.WarnCF("agent", "Failed to recover platform delivery", map[string]any{
			"delivery_id": candidate.ID, "error": err.Error(),
		})
	}
}

func (al *AgentLoop) sendTrackedPlatformMessage(
	ctx context.Context,
	msg bus.OutboundMessage,
	send func(context.Context, bus.OutboundMessage) error,
) error {
	msg = bus.NormalizeOutboundMessage(msg)
	delivery := turnDeliveryFromOutbound(msg)
	delivery.PlatformTracked = true
	delivery.Status = deliveryStatusDispatching
	delivery.DispatchOwner = platformDeliveryOwner
	delivery.Attempts = 1
	if err := al.setTurnDelivery(msg.SessionKey, delivery); err != nil {
		return err
	}
	markTrackedDelivery(&msg, delivery)
	sendErr := send(ctx, msg)
	return errors.Join(sendErr, al.FinishPlatformDelivery(msg, nil, sendErr))
}
