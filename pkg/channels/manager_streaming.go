package channels

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
)

func streamSuppressionKey(channel, chatID, sessionKey string) string {
	key := channel + ":" + chatID
	if strings.TrimSpace(sessionKey) == "" {
		return key
	}
	return key + ":" + sessionKey
}

// GetStreamer implements bus.StreamDelegate.
// It checks if the named channel supports streaming and returns a Streamer.
func (m *Manager) GetStreamer(ctx context.Context, channelName, chatID, sessionKey string) (bus.Streamer, bool) {
	m.mu.RLock()
	ch, exists := m.channels[channelName]
	m.mu.RUnlock()

	if !exists {
		return nil, false
	}

	sc, ok := ch.(StreamingCapable)
	if !ok {
		return nil, false
	}

	streamer, err := sc.BeginStream(ctx, chatID)
	if err != nil {
		logger.DebugCF("channels", "Streaming unavailable, falling back to placeholder", map[string]any{
			"channel": channelName,
			"error":   err.Error(),
		})
		return nil, false
	}

	// Mark streamActive on Finalize so preSend knows to clean up the placeholder
	// and late auxiliary messages cannot leak after streaming produced a final.
	streamKey := streamSuppressionKey(channelName, chatID, sessionKey)
	placeholderKey := channelName + ":" + chatID
	clearMarker := func() {
		m.streamActive.Delete(streamKey)
	}
	onFinalize := func(finalizeCtx context.Context, finalContent string) {
		if m.toolFeedbackSeparateMessagesEnabled() {
			clearTrackedToolFeedbackMessage(
				ch,
				chatID,
				&bus.InboundContext{
					Channel: channelName,
					ChatID:  chatID,
				},
			)
		} else {
			dismissTrackedToolFeedbackMessage(
				finalizeCtx,
				ch,
				chatID,
				&bus.InboundContext{
					Channel: channelName,
					ChatID:  chatID,
				},
			)
		}
		if v, loaded := m.placeholders.LoadAndDelete(placeholderKey); loaded {
			if entry, ok := v.(placeholderEntry); ok && entry.id != "" {
				if deleter, ok := ch.(MessageDeleter); ok {
					deleter.DeleteMessage(finalizeCtx, chatID, entry.id) // best effort
				} else if editor, ok := ch.(MessageEditor); ok {
					editor.EditMessage(finalizeCtx, chatID, entry.id, finalContent) // best effort fallback
				}
			}
		}
		m.streamActive.Store(streamKey, true)
		m.streamAuxiliaryTombstones.Store(streamKey, time.Now())
	}

	if m.config != nil && m.config.Agents.Defaults.SplitOnMarker {
		return &splitMarkerStreamer{
			current:     streamer,
			reasoning:   reasoningStreamerFrom(streamer),
			begin:       func(beginCtx context.Context) (bus.Streamer, error) { return sc.BeginStream(beginCtx, chatID) },
			onFinalize:  onFinalize,
			clearMarker: clearMarker,
		}, true
	}

	return &finalizeHookStreamer{
		Streamer:    streamer,
		clearMarker: clearMarker,
		onFinalize:  onFinalize,
	}, true
}

func reasoningStreamerFrom(streamer bus.Streamer) bus.ReasoningStreamer {
	if reasoningStreamer, ok := streamer.(bus.ReasoningStreamer); ok {
		return reasoningStreamer
	}
	return nil
}

func setStreamerModelName(streamer any, modelName string) {
	setter, ok := streamer.(modelNameStreamer)
	if !ok {
		return
	}
	setter.SetModelName(modelName)
}

func setStreamerDeliveryID(streamer any, deliveryID string) {
	setter, ok := streamer.(deliveryIDStreamer)
	if ok {
		setter.SetDeliveryID(deliveryID)
	}
}

// setStreamerTurnUsage forwards real per-turn token usage to a streamer that
// supports it, transparently unwrapping the manager's streamer wrappers.
func setStreamerTurnUsage(streamer any, inputTokens, outputTokens int) {
	setter, ok := streamer.(turnUsageStreamer)
	if !ok {
		return
	}
	setter.SetTurnUsage(inputTokens, outputTokens)
}

func (s *splitMarkerStreamer) Update(ctx context.Context, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateLocked(ctx, content)
}

func (s *splitMarkerStreamer) Finalize(ctx context.Context, content string) error {
	return s.FinalizeWithContext(ctx, content, nil)
}

func (s *splitMarkerStreamer) FinalizeWithContext(ctx context.Context, content string, usage *bus.ContextUsage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.finalizeLocked(ctx, content, usage); err != nil {
		return err
	}
	s.runFinalizeHook(ctx, content)
	return nil
}

func (s *splitMarkerStreamer) UpdateReasoning(ctx context.Context, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reasoning == nil {
		return nil
	}
	setStreamerModelName(s.reasoning, s.modelName)
	return s.reasoning.UpdateReasoning(ctx, content)
}

func (s *splitMarkerStreamer) FinalizeReasoning(ctx context.Context, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reasoning == nil {
		return nil
	}
	setStreamerModelName(s.reasoning, s.modelName)
	return s.reasoning.FinalizeReasoning(ctx, content)
}

func (s *splitMarkerStreamer) SetModelName(modelName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modelName = strings.TrimSpace(modelName)
	setStreamerModelName(s.current, s.modelName)
	setStreamerModelName(s.reasoning, s.modelName)
}

func (s *splitMarkerStreamer) SetDeliveryID(deliveryID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deliveryID = strings.TrimSpace(deliveryID)
}

func (s *splitMarkerStreamer) SetTurnUsage(inputTokens, outputTokens int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnInputTokens = inputTokens
	s.turnOutputTokens = outputTokens
	setStreamerTurnUsage(s.current, s.turnInputTokens, s.turnOutputTokens)
}

func (s *splitMarkerStreamer) Cancel(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		s.current.Cancel(ctx)
	}
}

func (s *splitMarkerStreamer) ClearFinalizedStreamMarker() {
	if s.clearMarker != nil {
		s.clearMarker()
	}
}

func (s *splitMarkerStreamer) updateLocked(ctx context.Context, content string) error {
	parts := strings.Split(content, MessageSplitMarker)
	completedLimit := len(parts) - 1
	if err := s.finalizeCompletedPartsLocked(ctx, parts, completedLimit, nil); err != nil {
		return err
	}
	active := strings.TrimSpace(parts[len(parts)-1])
	if active == "" {
		return nil
	}
	if err := s.ensureCurrentLocked(ctx); err != nil {
		return err
	}
	return s.current.Update(ctx, active)
}

func (s *splitMarkerStreamer) finalizeLocked(ctx context.Context, content string, usage *bus.ContextUsage) error {
	parts := strings.Split(content, MessageSplitMarker)
	return s.finalizeCompletedPartsLocked(ctx, parts, len(parts), usage)
}

func (s *splitMarkerStreamer) finalizeCompletedPartsLocked(
	ctx context.Context,
	parts []string,
	limit int,
	usage *bus.ContextUsage,
) error {
	for s.completedParts < limit {
		content := strings.TrimSpace(parts[s.completedParts])
		isLast := s.completedParts == limit-1
		if content != "" {
			if err := s.ensureCurrentLocked(ctx); err != nil {
				return err
			}
			if isLast {
				setStreamerDeliveryID(s.current, s.deliveryID)
			}
			if isLast && usage != nil {
				if contextStreamer, ok := s.current.(bus.ContextUsageStreamer); ok {
					if err := contextStreamer.FinalizeWithContext(ctx, content, usage); err != nil {
						return err
					}
				} else if err := s.current.Finalize(ctx, content); err != nil {
					return err
				}
			} else if err := s.current.Finalize(ctx, content); err != nil {
				return err
			}
			s.current = nil
		}
		s.completedParts++
	}
	return nil
}

func (s *splitMarkerStreamer) ensureCurrentLocked(ctx context.Context) error {
	if s.current != nil {
		return nil
	}
	if s.begin == nil {
		return fmt.Errorf("streamer is not initialized")
	}
	streamer, err := s.begin(ctx)
	if err != nil {
		return err
	}
	s.current = streamer
	setStreamerModelName(s.current, s.modelName)
	setStreamerTurnUsage(s.current, s.turnInputTokens, s.turnOutputTokens)
	return nil
}

func (s *splitMarkerStreamer) runFinalizeHook(ctx context.Context, content string) {
	if s.finalized {
		return
	}
	s.finalized = true
	if s.onFinalize != nil {
		s.onFinalize(ctx, content)
	}
}

func (m *Manager) streamAuxiliaryTombstoneActive(key string) bool {
	v, ok := m.streamAuxiliaryTombstones.Load(key)
	if !ok {
		return false
	}
	createdAt, ok := v.(time.Time)
	if !ok || time.Since(createdAt) > streamAuxiliaryTombstoneTTL {
		m.streamAuxiliaryTombstones.Delete(key)
		return false
	}
	return true
}

func (m *Manager) streamActiveForChat(channel, chatID string) bool {
	chatKey := streamSuppressionKey(channel, chatID, "")
	found := false
	m.streamActive.Range(func(key, _ any) bool {
		keyString, ok := key.(string)
		if !ok {
			return true
		}
		if keyString == chatKey || strings.HasPrefix(keyString, chatKey+":") {
			found = true
			return false
		}
		return true
	})
	return found
}

func (s *finalizeHookStreamer) Finalize(ctx context.Context, content string) error {
	if err := s.Streamer.Finalize(ctx, content); err != nil {
		return err
	}
	s.runFinalizeHook(ctx, content)
	return nil
}

func (s *finalizeHookStreamer) FinalizeWithContext(ctx context.Context, content string, usage *bus.ContextUsage) error {
	if streamer, ok := s.Streamer.(bus.ContextUsageStreamer); ok {
		if err := streamer.FinalizeWithContext(ctx, content, usage); err != nil {
			return err
		}
	} else if err := s.Streamer.Finalize(ctx, content); err != nil {
		return err
	}
	s.runFinalizeHook(ctx, content)
	return nil
}

func (s *finalizeHookStreamer) UpdateReasoning(ctx context.Context, content string) error {
	if streamer, ok := s.Streamer.(bus.ReasoningStreamer); ok {
		return streamer.UpdateReasoning(ctx, content)
	}
	return nil
}

func (s *finalizeHookStreamer) FinalizeReasoning(ctx context.Context, content string) error {
	if streamer, ok := s.Streamer.(bus.ReasoningStreamer); ok {
		return streamer.FinalizeReasoning(ctx, content)
	}
	return nil
}

func (s *finalizeHookStreamer) SetModelName(modelName string) {
	setStreamerModelName(s.Streamer, strings.TrimSpace(modelName))
}

func (s *finalizeHookStreamer) SetDeliveryID(deliveryID string) {
	setStreamerDeliveryID(s.Streamer, strings.TrimSpace(deliveryID))
}

func (s *finalizeHookStreamer) SetTurnUsage(inputTokens, outputTokens int) {
	setStreamerTurnUsage(s.Streamer, inputTokens, outputTokens)
}

func (s *finalizeHookStreamer) runFinalizeHook(ctx context.Context, content string) {
	if s.onFinalize != nil {
		s.onFinalize(ctx, content)
	}
}

func (s *finalizeHookStreamer) ClearFinalizedStreamMarker() {
	if s.clearMarker != nil {
		s.clearMarker()
	}
}
