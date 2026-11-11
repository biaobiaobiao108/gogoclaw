// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

const defaultAsyncDeliveryCapacity = 16

type asyncOrigin string

const (
	asyncOriginSubTurn asyncOrigin = "subturn"
	asyncOriginTool    asyncOrigin = "tool"
)

// asyncDelivery is the only representation of work that completes after the
// call which started it has returned. Every producer (sub-turns, async tools)
// creates one of these and hands it to AgentLoop.deliverAsyncResult.
type asyncDelivery struct {
	Origin         asyncOrigin
	Source         string
	ToolCallID     string
	Result         *tools.ToolResult
	PublishForUser bool
}

type asyncDeliveryQueue struct {
	mu       sync.Mutex
	items    []asyncDelivery
	capacity int
	closed   bool
}

func newAsyncDeliveryQueue(capacity int) *asyncDeliveryQueue {
	if capacity <= 0 {
		capacity = defaultAsyncDeliveryCapacity
	}
	return &asyncDeliveryQueue{capacity: capacity}
}

func (q *asyncDeliveryQueue) push(item asyncDelivery) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || len(q.items) >= q.capacity {
		return false
	}
	q.items = append(q.items, item)
	return true
}

func (q *asyncDeliveryQueue) pop() (asyncDelivery, bool) {
	if q == nil {
		return asyncDelivery{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return asyncDelivery{}, false
	}
	item := q.items[0]
	q.items[0] = asyncDelivery{}
	q.items = q.items[1:]
	return item, true
}

func (q *asyncDeliveryQueue) drain() []asyncDelivery {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil
	}
	out := append([]asyncDelivery(nil), q.items...)
	q.items = nil
	return out
}

func (q *asyncDeliveryQueue) close() []asyncDelivery {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	out := append([]asyncDelivery(nil), q.items...)
	q.items = nil
	return out
}

func (q *asyncDeliveryQueue) len() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (ts *turnState) asyncQueue() *asyncDeliveryQueue {
	if ts == nil {
		return nil
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.deliveries == nil {
		ts.deliveries = newAsyncDeliveryQueue(defaultAsyncDeliveryCapacity)
	}
	return ts.deliveries
}

func (ts *turnState) enqueueAsyncDelivery(delivery asyncDelivery) bool {
	queue := ts.asyncQueue()
	if queue == nil {
		return false
	}
	return queue.push(delivery)
}

func (ts *turnState) nextAsyncDelivery() (asyncDelivery, bool) {
	if ts == nil {
		return asyncDelivery{}, false
	}
	ts.mu.RLock()
	queue := ts.deliveries
	ts.mu.RUnlock()
	return queue.pop()
}

func (ts *turnState) drainAsyncDeliveries() []asyncDelivery {
	if ts == nil {
		return nil
	}
	ts.mu.RLock()
	queue := ts.deliveries
	ts.mu.RUnlock()
	return queue.drain()
}

func (ts *turnState) hasPendingAsyncDeliveries() bool {
	if ts == nil {
		return false
	}
	ts.mu.RLock()
	queue := ts.deliveries
	ts.mu.RUnlock()
	return queue.len() > 0
}

func (ts *turnState) pendingAsyncDeliveryCount() int {
	if ts == nil {
		return 0
	}
	ts.mu.RLock()
	queue := ts.deliveries
	ts.mu.RUnlock()
	return queue.len()
}

// deferAsyncFollowUp parks a result that cannot enter the live turn queue.
// runTurn flushes these after the turn has been unregistered, so the follow-up
// cannot be mistaken for steering for the turn that just ended.
func (ts *turnState) deferAsyncFollowUp(delivery asyncDelivery) bool {
	if ts == nil {
		return false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.followUpsFlushed {
		return false
	}
	ts.deferredFollowUps = append(ts.deferredFollowUps, delivery)
	return true
}

func (ts *turnState) takeDeferredFollowUps() []asyncDelivery {
	if ts == nil {
		return nil
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.followUpsFlushed = true
	out := ts.deferredFollowUps
	ts.deferredFollowUps = nil
	return out
}

// deliverAsyncResult is the single entry point for asynchronous completion.
// While the owning turn is alive the result joins its delivery queue. Once the
// turn has finished, or if the queue cannot accept the item, the same delivery
// is published as a follow-up inbound turn so background work is never lost.
func (al *AgentLoop) deliverAsyncResult(ts *turnState, delivery asyncDelivery) {
	if ts == nil || delivery.Result == nil {
		return
	}
	if ts.hardAbortRequested() {
		al.emitAsyncOrphan(ts, delivery, "turn_hard_aborted")
		return
	}
	if ts.enqueueAsyncDelivery(delivery) {
		if delivery.Origin == asyncOriginSubTurn {
			al.emitEvent(
				runtimeevents.KindAgentSubTurnResultDelivered,
				ts.eventMeta("deliverAsyncResult", "subturn.result_delivered"),
				SubTurnResultDeliveredPayload{ContentLen: len(delivery.Result.ForLLM)},
			)
		}
		return
	}
	if ts.deferAsyncFollowUp(delivery) {
		return
	}
	al.publishAsyncFollowUp(ts, delivery)
}

// flushDeferredFollowUps runs after the turn has been removed from the active
// registry, so processSystemMessage starts a fresh turn or steers a different
// live turn instead of targeting the one that just ended.
func (al *AgentLoop) flushDeferredFollowUps(ts *turnState) {
	if al == nil || ts == nil {
		return
	}
	for _, delivery := range ts.takeDeferredFollowUps() {
		al.publishAsyncFollowUp(ts, delivery)
	}
}

func (al *AgentLoop) publishAsyncFollowUp(ts *turnState, delivery asyncDelivery) {
	if al == nil || ts == nil || delivery.Result == nil {
		return
	}
	if al.bus == nil {
		al.emitAsyncOrphan(ts, delivery, "message_bus_unavailable")
		return
	}

	result := delivery.Result
	if delivery.PublishForUser && !result.Silent && result.ForUser != "" {
		if err := al.publishAsyncUserResult(ts, delivery); err != nil {
			al.reportAsyncUserResultFailure(ts, delivery, err)
		}
	}

	content := result.ContentForLLM()
	if content == "" {
		return
	}
	content = al.runtimeSnapshot().cfg.FilterSensitiveData(content)
	if delivery.Origin == asyncOriginTool {
		callID := delivery.ToolCallID
		if callID == "" {
			callID = delivery.Source
		}
		content = al.compactToolResultForPrompt(ts, delivery.Source, callID+"-async", content).Content
	}

	al.emitEvent(
		runtimeevents.KindAgentFollowUpQueued,
		ts.eventMeta("deliverAsyncResult", "turn.follow_up.queued"),
		FollowUpQueuedPayload{SourceTool: delivery.Source, ContentLen: len(content)},
	)

	sessionKey := ts.originSessionKey
	if sessionKey == "" {
		sessionKey = ts.sessionKey
	}
	chatID := ts.chatID
	if ts.channel != "" {
		chatID = fmt.Sprintf("%s:%s", ts.channel, ts.chatID)
	}
	pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := al.bus.PublishInbound(pubCtx, bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "system",
			ChatID:   chatID,
			ChatType: "direct",
			SenderID: fmt.Sprintf("async:%s", delivery.Source),
		},
		Content:    content,
		SessionKey: sessionKey,
	}); err != nil {
		logger.WarnCF("agent", "Failed to queue async follow-up", map[string]any{
			"turn_id": ts.turnID,
			"source":  delivery.Source,
			"error":   err.Error(),
		})
		al.emitAsyncOrphan(ts, delivery, "follow_up_publish_failed")
	}
}

func (al *AgentLoop) emitAsyncOrphan(ts *turnState, delivery asyncDelivery, reason string) {
	if al == nil || ts == nil {
		return
	}
	if delivery.Origin == asyncOriginSubTurn {
		al.emitEvent(
			runtimeevents.KindAgentSubTurnOrphan,
			ts.eventMeta("deliverAsyncResult", "subturn.orphan"),
			SubTurnOrphanPayload{
				ParentTurnID: ts.turnID,
				ChildTurnID:  delivery.Source,
				Reason:       reason,
			},
		)
		return
	}
	al.emitEvent(
		runtimeevents.KindAgentError,
		ts.eventMeta("deliverAsyncResult", "turn.error"),
		ErrorPayload{
			Stage:   "async_delivery",
			Message: fmt.Sprintf("async result from %s could not be delivered: %s", delivery.Source, reason),
		},
	)
}

// consumeAsyncDeliveries injects every queued completion into the conversation
// exactly once. This is the sole consumer; pipeline stages must not poll their
// own result channels.
func (al *AgentLoop) consumeAsyncDeliveries(ctx context.Context, ts *turnState, messages *[]providers.Message) error {
	deliveries := ts.drainAsyncDeliveries()
	for _, delivery := range deliveries {
		result := delivery.Result
		if result == nil {
			continue
		}
		if delivery.PublishForUser && !result.Silent && result.ForUser != "" {
			if err := al.publishAsyncUserResult(ts, delivery); err != nil {
				al.reportAsyncUserResultFailure(ts, delivery, err)
			}
		}

		content := al.runtimeSnapshot().cfg.FilterSensitiveData(result.ContentForLLM())
		if content == "" {
			continue
		}
		if delivery.Origin == asyncOriginTool {
			callID := delivery.ToolCallID
			if callID == "" {
				callID = delivery.Source
			}
			content = al.compactToolResultForPrompt(ts, delivery.Source, callID+"-async", content).Content
		}

		msg := asyncResultPromptMessage(delivery.Origin, delivery.Source, content)
		*messages = append(*messages, msg)
		if !ts.opts.NoHistory {
			if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, msg); err != nil {
				return fmt.Errorf("write async result: %w", err)
			}
			ts.recordPersistedMessage(msg)
			if err := ts.ingestMessage(ctx, al, msg); err != nil {
				return err
			}
		}
		logger.InfoCF("agent", "Async result injected into turn", map[string]any{
			"turn_id":     ts.turnID,
			"origin":      string(delivery.Origin),
			"source":      delivery.Source,
			"content_len": len(content),
		})
		al.emitEvent(
			runtimeevents.KindAgentAsyncResultDelivered,
			ts.eventMeta("consumeAsyncDeliveries", "turn.async.delivered"),
			AsyncResultDeliveredPayload{
				Origin:     string(delivery.Origin),
				Source:     delivery.Source,
				ContentLen: len(content),
			},
		)
	}
	return nil
}

func (al *AgentLoop) publishAsyncUserResult(ts *turnState, delivery asyncDelivery) error {
	if al == nil || ts == nil || delivery.Result == nil {
		return fmt.Errorf("async delivery context unavailable")
	}
	if al.bus == nil {
		return fmt.Errorf("message bus unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	outMsg := outboundMessageForTurn(ts, delivery.Result.ForUser)
	if ts.originSessionKey != "" {
		outMsg.SessionKey = ts.originSessionKey
	}
	if ts.originAgentID != "" {
		outMsg.AgentID = ts.originAgentID
	}
	sessionKey := outMsg.SessionKey
	if sessionKey == "" {
		sessionKey = ts.sessionKey
	}
	return al.publishTrackedOutbound(ctx, sessionKey, outMsg)
}

func (al *AgentLoop) reportAsyncUserResultFailure(ts *turnState, delivery asyncDelivery, err error) {
	if al == nil || ts == nil || err == nil {
		return
	}
	logger.WarnCF("agent", "Failed to publish async result to user", map[string]any{
		"turn_id": ts.turnID,
		"source":  delivery.Source,
		"error":   err.Error(),
	})
	al.emitEvent(
		runtimeevents.KindAgentError,
		ts.eventMeta("deliverAsyncResult", "turn.error"),
		ErrorPayload{
			Stage:   "async_user_notification",
			Message: fmt.Sprintf("async result from %s could not be sent to the user", delivery.Source),
		},
	)
}

func asyncResultPromptMessage(origin asyncOrigin, source, content string) providers.Message {
	if origin == asyncOriginTool {
		return promptMessageWithMetadata(
			providers.Message{Role: "user", Content: fmt.Sprintf("[Background task %s completed] %s", source, content)},
			PromptLayerTurn,
			PromptSlotToolResult,
			PromptSourceToolResult,
		)
	}
	return subTurnResultPromptMessage(content)
}
