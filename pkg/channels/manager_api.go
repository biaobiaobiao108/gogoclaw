package channels

import (
	"context"
	"errors"
	"fmt"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
)

func (m *Manager) GetChannel(name string) (Channel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	channel, ok := m.channels[name]
	return channel, ok
}

func (m *Manager) GetStatus() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := make(map[string]any)
	for name, channel := range m.channels {
		status[name] = map[string]any{
			"enabled": true,
			"running": channel.IsRunning(),
		}
	}
	return status
}

func (m *Manager) GetEnabledChannels() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.channels))
	for name := range m.channels {
		names = append(names, name)
	}
	return names
}

// Reload replaces changed channels and keeps unchanged workers running.
func (m *Manager) Reload(ctx context.Context, cfg *config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	list := toChannelHashes(cfg)
	added, removed := compareChannels(m.channelHashes, list)
	cc, err := toChannelConfig(cfg, added)
	if err != nil {
		return err
	}
	m.setConfig(cfg)

	var failures []error
	for _, name := range removed {
		channel := m.channels[name]
		if channel != nil {
			if err := channel.Stop(ctx); err != nil {
				failures = append(failures, fmt.Errorf("stop channel %s: %w", name, err))
				delete(*cc, name)
				continue
			}
			if m.mux != nil {
				m.unregisterChannelHTTPHandler(name, channel)
			}
			if worker := m.workers[name]; worker != nil && worker.cancel != nil {
				worker.cancel()
			}
		}
		delete(m.workers, name)
		delete(m.channels, name)
		delete(m.channelHashes, name)
	}
	if err := m.initChannels(cc); err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, name := range added {
		if (*cc)[name] == nil {
			continue
		}
		channel := m.channels[name]
		if channel == nil {
			failures = append(failures, fmt.Errorf("initialize channel %s", name))
			continue
		}
		logger.InfoCF("channels", "Starting channel", map[string]any{
			"channel": name,
		})
		if err := channel.Start(ctx); err != nil {
			logger.ErrorCF("channels", "Failed to start channel", map[string]any{
				"channel": name,
				"error":   err.Error(),
			})
			m.publishChannelEvent(
				runtimeevents.KindChannelLifecycleStartFailed,
				name,
				runtimeevents.Scope{Channel: name},
				runtimeevents.SeverityError,
				ChannelLifecyclePayload{Type: channelTypeForEvent(m, name), Error: err.Error()},
			)
			failures = append(failures, fmt.Errorf("start channel %s: %w", name, err))
			delete(m.channels, name)
			continue
		}
		// Lazily create worker only after channel starts successfully
		channelType := name
		if cfg := m.currentConfig(); cfg != nil {
			if bc := cfg.Channels.Get(name); bc != nil && bc.Type != "" {
				channelType = bc.Type
			}
		}
		workerCtx, workerCancel := context.WithCancel(ctx)
		w := newChannelWorker(name, channel, channelType)
		w.cancel = workerCancel
		w.ctx = workerCtx
		m.workers[name] = w
		go m.runWorker(workerCtx, name, w)
		go m.runMediaWorker(workerCtx, name, w)
		m.publishChannelEvent(
			runtimeevents.KindChannelLifecycleStarted,
			name,
			runtimeevents.Scope{Channel: name},
			runtimeevents.SeverityInfo,
			ChannelLifecyclePayload{Type: channelType},
		)
		if m.mux != nil {
			m.registerChannelHTTPHandler(name, channel)
		}
		m.channelHashes[name] = list[name]
	}
	return errors.Join(failures...)
}

func (m *Manager) RegisterChannel(name string, channel Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[name] = channel
	if m.mux != nil {
		m.registerChannelHTTPHandler(name, channel)
	}
}

func (m *Manager) UnregisterChannel(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.channels[name]; ok && m.mux != nil {
		m.unregisterChannelHTTPHandler(name, ch)
	}
	if w, ok := m.workers[name]; ok && w != nil {
		if w.cancel != nil {
			w.cancel()
		}
		<-w.done
		<-w.mediaDone
	}
	delete(m.workers, name)
	delete(m.channels, name)
}

// SendMessage sends an outbound message synchronously through the channel
// worker's rate limiter and retry logic. It blocks until the message is
// delivered (or all retries are exhausted), which preserves ordering when
// a subsequent operation depends on the message having been sent.
func (m *Manager) SendMessage(ctx context.Context, msg bus.OutboundMessage) error {
	msg = bus.NormalizeOutboundMessage(msg)
	channelName := outboundMessageChannel(msg)

	m.mu.RLock()
	_, exists := m.channels[channelName]
	w, wExists := m.workers[channelName]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("channel %s not found: %w", channelName, ErrNotRunning)
	}
	if !wExists || w == nil {
		return fmt.Errorf("channel %s has no active worker: %w", channelName, ErrNotRunning)
	}

	maxLen := 0
	if mlp, ok := w.ch.(MessageLengthProvider); ok {
		maxLen = mlp.MaxMessageLength()
	}
	chunks := splitOutboundMessageContent(msg, maxLen)
	if len(chunks) == 0 {
		_, err := m.sendWithRetry(ctx, channelName, w, msg)
		return err
	}
	for index, chunk := range chunks {
		chunkMsg := outboundChunkMessage(msg, chunk, index, len(chunks))
		if _, err := m.sendWithRetry(ctx, channelName, w, chunkMsg); err != nil {
			if index > 0 && msg.Context.Raw[bus.MetadataPlatformDelivery] != "" {
				err = fmt.Errorf("%w: %v", ErrDeliveryUnknown, err)
			}
			return fmt.Errorf("send channel %s chunk %d/%d: %w", channelName, index+1, len(chunks), err)
		}
	}
	return nil
}

// SendMedia sends outbound media synchronously through the channel worker's
// rate limiter and retry logic. It blocks until the media is delivered (or all
// retries are exhausted), which preserves ordering when later agent behavior
// depends on actual media delivery.
func (m *Manager) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) error {
	msg = bus.NormalizeOutboundMediaMessage(msg)
	channelName := outboundMediaChannel(msg)

	m.mu.RLock()
	_, exists := m.channels[channelName]
	w, wExists := m.workers[channelName]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("channel %s not found", channelName)
	}
	if !wExists || w == nil {
		return fmt.Errorf("channel %s has no active worker", channelName)
	}

	_, err := m.sendMediaWithRetry(ctx, channelName, w, msg)
	return err
}

func (m *Manager) SendToChannel(ctx context.Context, channelName, chatID, content string) error {
	m.mu.RLock()
	_, exists := m.channels[channelName]
	w, wExists := m.workers[channelName]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("channel %s not found", channelName)
	}

	msg := bus.OutboundMessage{
		Context: bus.NewOutboundContext(channelName, chatID, ""),
		Content: content,
	}
	msg = bus.NormalizeOutboundMessage(msg)

	if wExists && w != nil {
		select {
		case w.queue <- msg:
			m.publishOutboundQueued(channelName, msg)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Fallback: direct send (should not happen)
	channel, _ := m.channels[channelName]
	_, err := channel.Send(ctx, msg)
	return err
}
