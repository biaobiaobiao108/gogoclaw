package pico

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

func outboundMessageIsThought(msg bus.OutboundMessage) bool {
	if len(msg.Context.Raw) == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(msg.Context.Raw["message_kind"]), MessageKindThought)
}

func outboundMessageIsToolFeedback(msg bus.OutboundMessage) bool {
	if len(msg.Context.Raw) == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(msg.Context.Raw["message_kind"]), "tool_feedback")
}

func outboundMessageIsToolCalls(msg bus.OutboundMessage) bool {
	if len(msg.Context.Raw) == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(msg.Context.Raw["message_kind"]), MessageKindToolCalls)
}

func outboundMessageFinalizesTrackedToolFeedback(msg bus.OutboundMessage) bool {
	return !outboundMessageIsToolFeedback(msg) &&
		!outboundMessageIsThought(msg) &&
		!outboundMessageIsToolCalls(msg)
}

// Send implements Channel — sends a message to the appropriate WebSocket connection.
func (c *PicoChannel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}
	isThought := outboundMessageIsThought(msg)
	isToolFeedback := outboundMessageIsToolFeedback(msg)
	isToolCalls := outboundMessageIsToolCalls(msg)
	if isToolFeedback {
		if msgID, handled, err := c.progress.Update(ctx, msg.ChatID, msg.Content); handled {
			if err != nil {
				return nil, err
			}
			return []string{msgID}, nil
		}
	}
	trackedMsgID, hasTrackedMsg := c.currentToolFeedbackMessage(msg.ChatID)
	if outboundMessageFinalizesTrackedToolFeedback(msg) {
		if msgIDs, handled := c.FinalizeToolFeedbackMessage(ctx, msg); handled {
			return msgIDs, nil
		}
	}

	content := msg.Content
	if isToolFeedback {
		content = channels.InitialAnimatedToolFeedbackContent(msg.Content)
	}
	deliveryID := strings.TrimSpace(msg.Context.Raw[PayloadKeyDeliveryID])
	msgID := strings.TrimSpace(msg.ID)
	if msgID == "" {
		msgID = deliveryID
	}
	if msgID == "" {
		msgID = uuid.New().String()
	}

	payload := map[string]any{
		PayloadKeyContent: content,
		"message_id":      msgID,
	}
	if deliveryID != "" {
		payload[PayloadKeyDeliveryID] = deliveryID
	}
	if modelName := strings.TrimSpace(msg.Context.Raw[PayloadKeyModelName]); modelName != "" {
		payload[PayloadKeyModelName] = modelName
	}
	switch {
	case isThought:
		payload[PayloadKeyKind] = MessageKindThought

		// This field is kept solely for compatibility with legacy pico clients that
		// do not yet support the newer "kind" field.
		// DO NOT use it for any purpose other than legacy client compatibility.
		payload[PayloadKeyThought] = true

	case isToolCalls:
		payload[PayloadKeyKind] = MessageKindToolCalls
		if toolCalls, ok := picoToolCallsPayload(msg); ok {
			payload[PayloadKeyToolCalls] = toolCalls
		}
	}
	setContextUsagePayload(payload, msg.ContextUsage)
	outMsg := newMessage(TypeMessageCreate, payload)
	outMsg.ID = msgID

	if err := c.broadcastToSession(msg.ChatID, outMsg); err != nil {
		return nil, err
	}
	if isToolFeedback {
		c.RecordToolFeedbackMessage(msg.ChatID, msgID, msg.Content)
	} else if hasTrackedMsg && outboundMessageFinalizesTrackedToolFeedback(msg) {
		c.dismissTrackedToolFeedbackMessage(ctx, msg.ChatID, trackedMsgID)
	}
	return []string{msgID}, nil
}

// EditMessage implements channels.MessageEditor.
func (c *PicoChannel) EditMessage(ctx context.Context, chatID string, messageID string, content string) error {
	return c.editMessage(ctx, chatID, messageID, content, nil)
}

func (c *PicoChannel) EditMessageWithPayload(
	ctx context.Context,
	chatID string,
	messageID string,
	payload map[string]any,
) error {
	return c.editMessagePayload(ctx, chatID, messageID, payload, nil)
}

// DeleteMessage implements channels.MessageDeleter.
func (c *PicoChannel) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	outMsg := newMessage(TypeMessageDelete, map[string]any{
		"message_id": messageID,
	})
	outMsg.ID = messageID
	return c.broadcastToSession(chatID, outMsg)
}

func (c *PicoChannel) currentToolFeedbackMessage(chatID string) (string, bool) {
	if c.progress == nil {
		return "", false
	}
	return c.progress.Current(chatID)
}

func (c *PicoChannel) takeToolFeedbackMessage(chatID string) (string, string, bool) {
	if c.progress == nil {
		return "", "", false
	}
	return c.progress.Take(chatID)
}

func (c *PicoChannel) RecordToolFeedbackMessage(chatID, messageID, content string) {
	if c.progress == nil {
		return
	}
	c.progress.Record(chatID, messageID, content)
}

func (c *PicoChannel) ClearToolFeedbackMessage(chatID string) {
	if c.progress == nil {
		return
	}
	c.progress.Clear(chatID)
}

func (c *PicoChannel) DismissToolFeedbackMessage(ctx context.Context, chatID string) {
	msgID, ok := c.currentToolFeedbackMessage(chatID)
	if !ok {
		return
	}
	c.dismissTrackedToolFeedbackMessage(ctx, chatID, msgID)
}

func (c *PicoChannel) dismissTrackedToolFeedbackMessage(ctx context.Context, chatID, messageID string) {
	if strings.TrimSpace(chatID) == "" || strings.TrimSpace(messageID) == "" {
		return
	}
	c.ClearToolFeedbackMessage(chatID)
	deleteFn := c.deleteMessageFn
	if deleteFn == nil {
		deleteFn = c.DeleteMessage
	}
	_ = deleteFn(ctx, chatID, messageID)
}

func (c *PicoChannel) finalizeTrackedToolFeedbackMessage(
	ctx context.Context,
	chatID string,
	content string,
	editFn func(context.Context, string, string, map[string]any, *bus.ContextUsage) error,
	payload map[string]any,
	contextUsage *bus.ContextUsage,
) ([]string, bool) {
	msgID, baseContent, ok := c.takeToolFeedbackMessage(chatID)
	if !ok || editFn == nil {
		return nil, false
	}
	if payload == nil {
		payload = map[string]any{
			PayloadKeyContent: content,
		}
	}
	if _, ok := payload[PayloadKeyContent]; !ok {
		payload[PayloadKeyContent] = content
	}
	if err := editFn(ctx, chatID, msgID, payload, contextUsage); err != nil {
		c.RecordToolFeedbackMessage(chatID, msgID, baseContent)
		return nil, false
	}
	return []string{msgID}, true
}

func (c *PicoChannel) FinalizeToolFeedbackMessage(ctx context.Context, msg bus.OutboundMessage) ([]string, bool) {
	if !outboundMessageFinalizesTrackedToolFeedback(msg) {
		return nil, false
	}
	payload := map[string]any{
		PayloadKeyContent: msg.Content,
	}
	if deliveryID := strings.TrimSpace(msg.Context.Raw[PayloadKeyDeliveryID]); deliveryID != "" {
		payload[PayloadKeyDeliveryID] = deliveryID
	}
	if modelName := strings.TrimSpace(msg.Context.Raw[PayloadKeyModelName]); modelName != "" {
		payload[PayloadKeyModelName] = modelName
	}
	return c.finalizeTrackedToolFeedbackMessage(
		ctx,
		msg.ChatID,
		msg.Content,
		c.editMessagePayload,
		payload,
		msg.ContextUsage,
	)
}

// StartTyping implements channels.TypingCapable.
func (c *PicoChannel) StartTyping(ctx context.Context, chatID string) (func(), error) {
	startMsg := newMessage(TypeTypingStart, nil)
	if err := c.broadcastToSession(chatID, startMsg); err != nil {
		return func() {}, err
	}
	return func() {
		stopMsg := newMessage(TypeTypingStop, nil)
		c.broadcastToSession(chatID, stopMsg)
	}, nil
}

// SendPlaceholder implements channels.PlaceholderCapable.
// It sends a placeholder message via the Pico Protocol that will later be
// edited to the actual response via EditMessage (channels.MessageEditor).
func (c *PicoChannel) SendPlaceholder(ctx context.Context, chatID string) (string, error) {
	if !c.bc.Placeholder.Enabled {
		return "", nil
	}

	text := c.bc.Placeholder.GetRandomText()

	msgID := uuid.New().String()
	outMsg := newMessage(TypeMessageCreate, map[string]any{
		PayloadKeyContent:     text,
		PayloadKeyPlaceholder: true,
		"message_id":          msgID,
	})
	outMsg.ID = msgID

	if err := c.broadcastToSession(chatID, outMsg); err != nil {
		return "", err
	}

	return msgID, nil
}

// BeginStream implements channels.StreamingCapable for Pico WebUI.
func (c *PicoChannel) BeginStream(ctx context.Context, chatID string) (channels.Streamer, error) {
	if c == nil || c.config == nil || !c.config.Streaming.Enabled {
		return nil, fmt.Errorf("streaming disabled in config")
	}
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}
	streamCfg := c.config.Streaming.WithDefaults(0, 1)
	return &picoStreamer{
		channel:          c,
		chatID:           chatID,
		throttleInterval: time.Duration(streamCfg.ThrottleSeconds) * time.Second,
		minGrowth:        streamCfg.MinGrowthChars,
	}, nil
}

func (s *picoStreamer) SetModelName(modelName string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modelName = strings.TrimSpace(modelName)
}

func (s *picoStreamer) SetDeliveryID(deliveryID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deliveryID = strings.TrimSpace(deliveryID)
}

// SetTurnUsage records the real per-turn LLM token usage to emit on finalize.
func (s *picoStreamer) SetTurnUsage(inputTokens, outputTokens int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnInputTokens = inputTokens
	s.turnOutputTokens = outputTokens
}

func (s *picoStreamer) Update(ctx context.Context, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateLocked(ctx, content, false, nil)
}

func (s *picoStreamer) Finalize(ctx context.Context, content string) error {
	return s.FinalizeWithContext(ctx, content, nil)
}

func (s *picoStreamer) FinalizeWithContext(ctx context.Context, content string, contextUsage *bus.ContextUsage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateLocked(ctx, content, true, contextUsage)
}

func (s *picoStreamer) UpdateReasoning(ctx context.Context, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateReasoningLocked(ctx, content, false)
}

func (s *picoStreamer) FinalizeReasoning(ctx context.Context, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateReasoningLocked(ctx, content, true)
}

func (s *picoStreamer) Cancel(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.channel == nil || s.messageID == "" {
		if s.channel != nil && s.reasoningID != "" {
			_ = s.channel.DeleteMessage(ctx, s.chatID, s.reasoningID)
			s.reasoningID = ""
		}
		return
	}
	_ = s.channel.DeleteMessage(ctx, s.chatID, s.messageID)
	s.messageID = ""
	if s.reasoningID != "" {
		_ = s.channel.DeleteMessage(ctx, s.chatID, s.reasoningID)
		s.reasoningID = ""
	}
}

func (s *picoStreamer) updateLocked(
	ctx context.Context,
	content string,
	force bool,
	contextUsage *bus.ContextUsage,
) error {
	if s == nil || s.channel == nil {
		return fmt.Errorf("streamer is not initialized")
	}
	if strings.TrimSpace(content) == "" && s.messageID == "" {
		return nil
	}

	now := time.Now()
	contentLen := len([]rune(content))
	if s.messageID != "" && !force {
		growth := contentLen - s.lastLen
		if now.Sub(s.lastAt) < s.throttleInterval || growth < s.minGrowth {
			return nil
		}
	}

	return s.sendPacedLocked(ctx, content, force, contextUsage, false)
}

func (s *picoStreamer) updateReasoningLocked(ctx context.Context, content string, force bool) error {
	if s == nil || s.channel == nil {
		return fmt.Errorf("streamer is not initialized")
	}
	if strings.TrimSpace(content) == "" && s.reasoningID == "" {
		return nil
	}

	now := time.Now()
	contentLen := len([]rune(content))
	if s.reasoningID != "" && !force {
		growth := contentLen - s.reasoningLastLen
		if now.Sub(s.reasoningLastAt) < s.throttleInterval || growth < s.minGrowth {
			return nil
		}
	}

	return s.sendPacedLocked(ctx, content, false, nil, true)
}

func (s *picoStreamer) sendPacedLocked(
	ctx context.Context,
	content string,
	final bool,
	contextUsage *bus.ContextUsage,
	reasoning bool,
) error {
	currentLen := s.lastLen
	if reasoning {
		currentLen = s.reasoningLastLen
	}
	if (!reasoning && s.messageID == "") || (reasoning && s.reasoningID == "") {
		currentLen = 0
	}

	runes := []rune(content)
	targetLen := len(runes)
	delta := targetLen - currentLen
	if delta <= streamPacingThresholdRunes {
		if reasoning {
			return s.sendReasoningLocked(ctx, content)
		}
		return s.sendLocked(ctx, content, final, contextUsage)
	}

	duration := time.Duration(delta) * 5 * time.Millisecond
	if duration < streamPacingMinDuration {
		duration = streamPacingMinDuration
	}
	if duration > streamPacingMaxDuration {
		duration = streamPacingMaxDuration
	}
	frames := int(math.Ceil(float64(duration) / float64(streamPacingFrameInterval)))
	if frames < 2 {
		frames = 2
	}
	if frames > streamPacingMaxFrames {
		frames = streamPacingMaxFrames
	}

	for frame := 1; frame < frames; frame++ {
		progress := float64(frame) / float64(frames)
		eased := 1 - math.Pow(1-progress, 3)
		nextLen := currentLen + int(math.Ceil(float64(delta)*eased))
		if nextLen <= currentLen || nextLen >= targetLen {
			continue
		}
		if reasoning {
			if err := s.sendReasoningLocked(ctx, string(runes[:nextLen])); err != nil {
				return err
			}
		} else if err := s.sendLocked(ctx, string(runes[:nextLen]), false, nil); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(streamPacingFrameInterval):
		}
	}

	if reasoning {
		return s.sendReasoningLocked(ctx, content)
	}
	return s.sendLocked(ctx, content, final, contextUsage)
}

func (s *picoStreamer) sendLocked(
	ctx context.Context,
	content string,
	final bool,
	contextUsage *bus.ContextUsage,
) error {
	now := time.Now()
	contentLen := len([]rune(content))

	if s.messageID == "" {
		s.messageID = s.deliveryID
		if s.messageID == "" {
			s.messageID = uuid.New().String()
		}
		payload := map[string]any{
			PayloadKeyContent: content,
			"message_id":      s.messageID,
		}
		if s.deliveryID != "" {
			payload[PayloadKeyDeliveryID] = s.deliveryID
			s.deliveryPublished = true
		}
		if s.modelName != "" {
			payload[PayloadKeyModelName] = s.modelName
		}
		setContextUsagePayload(payload, contextUsage)
		setTurnUsagePayload(payload, s.turnInputTokens, s.turnOutputTokens)
		outMsg := newMessage(TypeMessageCreate, payload)
		outMsg.ID = s.messageID
		if err := s.channel.broadcast(s.chatID, outMsg); err != nil {
			return err
		}
	} else if content != s.lastContent || contextUsage != nil ||
		(final && s.deliveryID != "" && !s.deliveryPublished) {
		payload := map[string]any{
			PayloadKeyContent: content,
			"message_id":      s.messageID,
		}
		if s.deliveryID != "" {
			payload[PayloadKeyDeliveryID] = s.deliveryID
			s.deliveryPublished = true
		}
		if s.modelName != "" {
			payload[PayloadKeyModelName] = s.modelName
		}
		setTurnUsagePayload(payload, s.turnInputTokens, s.turnOutputTokens)
		if err := s.channel.editMessagePayload(ctx, s.chatID, s.messageID, payload, contextUsage); err != nil {
			return err
		}
	}

	s.lastContent = content
	s.lastLen = contentLen
	s.lastAt = now
	return nil
}

func (s *picoStreamer) sendReasoningLocked(ctx context.Context, content string) error {
	now := time.Now()
	contentLen := len([]rune(content))

	if s.reasoningID == "" {
		s.reasoningID = uuid.New().String()
		if s.deliveryID != "" {
			s.reasoningID = s.deliveryID + "-thought"
		}
		payload := map[string]any{
			PayloadKeyContent: content,
			"message_id":      s.reasoningID,
			PayloadKeyKind:    MessageKindThought,
			PayloadKeyThought: true,
		}
		if s.modelName != "" {
			payload[PayloadKeyModelName] = s.modelName
		}
		outMsg := newMessage(TypeMessageCreate, payload)
		if err := s.channel.broadcastToSession(s.chatID, outMsg); err != nil {
			return err
		}
	} else if content != s.reasoningContent {
		payload := map[string]any{
			PayloadKeyContent: content,
			"message_id":      s.reasoningID,
			PayloadKeyKind:    MessageKindThought,
			PayloadKeyThought: true,
		}
		if s.modelName != "" {
			payload[PayloadKeyModelName] = s.modelName
		}
		outMsg := newMessage(TypeMessageUpdate, payload)
		outMsg.ID = s.reasoningID
		if err := s.channel.broadcastToSession(s.chatID, outMsg); err != nil {
			return err
		}
	}

	s.reasoningContent = content
	s.reasoningLastLen = contentLen
	s.reasoningLastAt = now
	return nil
}

// setContextUsagePayload adds context window usage stats to a pico payload.
func setContextUsagePayload(payload map[string]any, u *bus.ContextUsage) {
	if u == nil {
		return
	}
	payload["context_usage"] = map[string]any{
		"used_tokens":         u.UsedTokens,
		"total_tokens":        u.TotalTokens,
		"history_tokens":      u.HistoryTokens,
		"compress_at_tokens":  u.CompressAtTokens,
		"summarize_at_tokens": u.SummarizeAtTokens,
		"used_percent":        u.UsedPercent,
	}
}

// setTurnUsagePayload attaches real per-turn LLM token usage to the payload.
// Input and output are kept separate (billed at different rates); total is a
// convenience sum. Omitted entirely when both counts are zero.
func setTurnUsagePayload(payload map[string]any, inputTokens, outputTokens int) {
	if inputTokens <= 0 && outputTokens <= 0 {
		return
	}
	payload[PayloadKeyUsage] = map[string]any{
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
		"total_tokens":  inputTokens + outputTokens,
	}
}

func picoToolCallsPayload(msg bus.OutboundMessage) ([]utils.VisibleToolCall, bool) {
	raw := strings.TrimSpace(msg.Context.Raw[PayloadKeyToolCalls])
	if raw == "" {
		return nil, false
	}

	var toolCalls []utils.VisibleToolCall
	if err := json.Unmarshal([]byte(raw), &toolCalls); err != nil || len(toolCalls) == 0 {
		return nil, false
	}
	return toolCalls, true
}

func (c *PicoChannel) editMessage(
	ctx context.Context,
	chatID string,
	messageID string,
	content string,
	contextUsage *bus.ContextUsage,
) error {
	return c.editMessagePayload(ctx, chatID, messageID, map[string]any{
		PayloadKeyContent: content,
	}, contextUsage)
}

func (c *PicoChannel) editMessagePayload(
	ctx context.Context,
	chatID string,
	messageID string,
	payload map[string]any,
	contextUsage *bus.ContextUsage,
) error {
	if payload == nil {
		payload = map[string]any{}
	}
	normalized := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		normalized[key] = value
	}
	if _, ok := normalized[PayloadKeyContent]; !ok {
		normalized[PayloadKeyContent] = ""
	}
	normalized["message_id"] = messageID
	setContextUsagePayload(normalized, contextUsage)
	outMsg := newMessage(TypeMessageUpdate, normalized)
	outMsg.ID = messageID
	return c.broadcastToSession(chatID, outMsg)
}
