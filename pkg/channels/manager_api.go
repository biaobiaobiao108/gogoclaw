package channels

import (
	"context"
	"fmt"
	"runtime/debug"

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

// Reload updates the config reference without restarting channels.
// This is used when channel config hasn't changed but other parts of the config have.
func (m *Manager) Reload(ctx context.Context, cfg *config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Save old config so we can revert on error.
	oldConfig := m.config

	// Update config early: initChannel uses m.config via factory(m.config, m.bus).
	m.config = cfg

	list := toChannelHashes(cfg)
	added, removed := compareChannels(m.channelHashes, list)

	deferFuncs := make([]func(), 0, len(removed)+len(added))
	for _, name := range removed {
		// Stop all channels
		channel := m.channels[name]
		logger.InfoCF("channels", "Stopping channel", map[string]any{
			"channel": name,
		})
		if err := channel.Stop(ctx); err != nil {
			logger.ErrorCF("channels", "Error stopping channel", map[string]any{
				"channel": name,
				"error":   err.Error(),
			})
		}
		deferFuncs = append(deferFuncs, func() {
			m.UnregisterChannel(name)
		})
	}
	cc, err := toChannelConfig(cfg, added)
	if err != nil {
		logger.ErrorC("channels", fmt.Sprintf("toChannelConfig error: %v", err))
		m.config = oldConfig
		return err
	}
	err = m.initChannels(cc)
	if err != nil {
		logger.ErrorC("channels", fmt.Sprintf("initChannels error: %v", err))
		m.config = oldConfig
		return err
	}
	for _, name := range added {
		channel := m.channels[name]
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
			continue
		}
		// Lazily create worker only after channel starts successfully
		channelType := name
		if m.config != nil {
			if bc := m.config.Channels.Get(name); bc != nil && bc.Type != "" {
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
		deferFuncs = append(deferFuncs, func() {
			m.RegisterChannel(name, channel)
		})
	}

	// Commit hashes only on full success.
	m.channelHashes = list
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("channels", "channel registration goroutine panic recovered",
					map[string]any{
						"panic": fmt.Sprintf("%v", r),
						"stack": string(debug.Stack()),
					})
			}
		}()
		for _, f := range deferFuncs {
			f()
		}
	}()
	return nil
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
		return fmt.Errorf("channel %s not found", channelName)
	}
	if !wExists || w == nil {
		return fmt.Errorf("channel %s has no active worker", channelName)
	}

	maxLen := 0
	if mlp, ok := w.ch.(MessageLengthProvider); ok {
		maxLen = mlp.MaxMessageLength()
	}
	if chunks := splitOutboundMessageContent(msg, maxLen); len(chunks) > 1 {
		for index, chunk := range chunks {
			chunkMsg := outboundChunkMessage(msg, chunk, index, len(chunks))
			m.sendWithRetry(ctx, channelName, w, chunkMsg)
		}
	} else {
		if len(chunks) == 1 {
			msg = outboundChunkMessage(msg, chunks[0], 0, len(chunks))
		}
		m.sendWithRetry(ctx, channelName, w, msg)
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
