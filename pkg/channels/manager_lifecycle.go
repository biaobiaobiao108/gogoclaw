package channels

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"sort"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/health"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
)

// initChannel is a helper that looks up a factory by type name and creates the channel.
// typeName is the channel type used for factory lookup (e.g., "telegram").
// channelName is the config map key used as the channel's runtime name (e.g., "my_telegram").
func (m *Manager) initChannel(typeName, channelName string) {
	f, ok := getFactory(typeName)
	if !ok {
		logger.WarnCF("channels", "Factory not registered", map[string]any{
			"channel": channelName,
			"type":    typeName,
		})
		return
	}
	logger.DebugCF("channels", "Attempting to initialize channel", map[string]any{
		"channel": channelName,
		"type":    typeName,
	})
	ch, err := f(channelName, typeName, m.config, m.bus)
	if err != nil {
		logger.ErrorCF("channels", "Failed to initialize channel", map[string]any{
			"channel": channelName,
			"type":    typeName,
			"error":   err.Error(),
		})
	} else {
		// Inject MediaStore if channel supports it
		if m.mediaStore != nil {
			if setter, ok := ch.(mediaStoreSetter); ok {
				setter.SetMediaStore(m.mediaStore)
			}
		}
		// Inject PlaceholderRecorder if channel supports it
		if setter, ok := ch.(interface{ SetPlaceholderRecorder(r PlaceholderRecorder) }); ok {
			setter.SetPlaceholderRecorder(m)
		}
		// Inject owner reference so BaseChannel.HandleMessage can auto-trigger typing/reaction
		if setter, ok := ch.(interface{ SetOwner(ch Channel) }); ok {
			setter.SetOwner(ch)
		}
		m.channels[channelName] = ch
		m.publishChannelEvent(
			runtimeevents.KindChannelLifecycleInitialized,
			channelName,
			runtimeevents.Scope{Channel: channelName},
			runtimeevents.SeverityInfo,
			ChannelLifecyclePayload{Type: typeName},
		)
		logger.InfoCF("channels", "Channel enabled successfully", map[string]any{
			"channel": channelName,
			"type":    typeName,
		})
	}
}

func (m *Manager) getChannelConfigAndEnabled(channelName string) (*config.Channel, bool) {
	bc, ok := m.config.Channels[channelName]
	if !ok || bc == nil {
		return nil, false
	}
	if !bc.Enabled {
		return bc, false
	}

	// Use Type to determine the config struct for validation.
	// The map key (channelName) is the config key, which may differ from the type.
	channelType := bc.Type
	if channelType == "" {
		channelType = channelName
	}

	// Settings have already been decoded by InitChannelList, so we just need to
	// type-assert and check the relevant fields.
	decoded, err := bc.GetDecoded()
	if err != nil {
		return bc, false
	}
	switch settings := decoded.(type) {
	case *config.PicoClientSettings:
		return bc, settings.URL != ""
	case *config.PicoSettings:
		return bc, settings.Token.String() != ""
	case *config.WeixinSettings:
		return bc, settings.Token.String() != ""
	case *config.QQSettings:
		return bc, settings.AppSecret.String() != ""
	case *config.TelegramSettings:
		return bc, settings.Token.String() != ""
	case *config.FeishuSettings:
		return bc, settings.AppSecret.String() != ""
	case *config.DiscordSettings:
		return bc, settings.Token.String() != ""
	}

	return bc, bc.Enabled
}

// initChannels initializes all enabled channels based on the configuration.
// It iterates config entries and uses bc.Type to look up the appropriate factory.
func (m *Manager) initChannels(channels *config.ChannelsConfig) error {
	logger.InfoC("channels", "Initializing channel manager")

	for name, bc := range *channels {
		if !bc.Enabled {
			continue
		}
		_, ready := m.getChannelConfigAndEnabled(name)
		if !ready {
			continue
		}
		typeName := bc.Type
		if typeName == "" {
			typeName = name
		}
		m.initChannel(typeName, name)
	}

	logger.InfoCF("channels", "Channel initialization completed", map[string]any{
		"enabled_channels": len(m.channels),
	})

	return nil
}

// SetupHTTPServer creates a shared HTTP server with the given listen address.
// It registers health endpoints from the health server and discovers channels
// that implement WebhookHandler and/or HealthChecker to register their handlers.
func (m *Manager) SetupHTTPServer(addr string, healthServer *health.Server) {
	m.SetupHTTPServerListeners(nil, addr, healthServer)
}

// SetupHTTPServerListeners creates a shared HTTP server on pre-opened listeners.
// When listeners is empty it falls back to Addr-based ListenAndServe behavior.
func (m *Manager) SetupHTTPServerListeners(listeners []net.Listener, addr string, healthServer *health.Server) {
	m.mux = newDynamicServeMux()

	// Register health endpoints
	if healthServer != nil {
		healthServer.RegisterOnMux(m.mux)
	}

	// Discover and register webhook handlers and health checkers
	m.registerHTTPHandlersLocked()

	m.httpServer = &http.Server{
		Addr:         addr,
		Handler:      m.mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	m.httpListeners = append([]net.Listener(nil), listeners...)
}

// registerHTTPHandlersLocked registers webhook and health-check handlers for
// all channels currently in m.channels. Caller must hold m.mu (or ensure
// exclusive access).
func (m *Manager) registerHTTPHandlersLocked() {
	for name, ch := range m.channels {
		m.registerChannelHTTPHandler(name, ch)
	}
}

// registerChannelHTTPHandler registers the webhook/health handlers for a
// single channel onto m.mux.
func (m *Manager) registerChannelHTTPHandler(name string, ch Channel) {
	if wh, ok := ch.(WebhookHandler); ok {
		m.mux.Handle(wh.WebhookPath(), wh)
		m.publishChannelEvent(
			runtimeevents.KindChannelWebhookRegistered,
			name,
			runtimeevents.Scope{Channel: name},
			runtimeevents.SeverityInfo,
			ChannelLifecyclePayload{Type: channelTypeForEvent(m, name)},
		)
		logger.InfoCF("channels", "Webhook handler registered", map[string]any{
			"channel": name,
			"path":    wh.WebhookPath(),
		})
	}
	if hc, ok := ch.(HealthChecker); ok {
		m.mux.HandleFunc(hc.HealthPath(), hc.HealthHandler)
		logger.InfoCF("channels", "Health endpoint registered", map[string]any{
			"channel": name,
			"path":    hc.HealthPath(),
		})
	}
}

// unregisterChannelHTTPHandler removes the webhook/health handlers for a
// single channel from m.mux.
func (m *Manager) unregisterChannelHTTPHandler(name string, ch Channel) {
	if wh, ok := ch.(WebhookHandler); ok {
		m.mux.Unhandle(wh.WebhookPath())
		m.publishChannelEvent(
			runtimeevents.KindChannelWebhookUnregistered,
			name,
			runtimeevents.Scope{Channel: name},
			runtimeevents.SeverityInfo,
			ChannelLifecyclePayload{Type: channelTypeForEvent(m, name)},
		)
		logger.InfoCF("channels", "Webhook handler unregistered", map[string]any{
			"channel": name,
			"path":    wh.WebhookPath(),
		})
	}
	if hc, ok := ch.(HealthChecker); ok {
		m.mux.Unhandle(hc.HealthPath())
		logger.InfoCF("channels", "Health endpoint unregistered", map[string]any{
			"channel": name,
			"path":    hc.HealthPath(),
		})
	}
}

func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.channels) == 0 {
		logger.WarnC("channels", "No channels enabled")
	}

	logger.InfoC("channels", "Starting all channels")

	dispatchCtx, cancel := context.WithCancel(ctx)
	m.dispatchTask = &asyncTask{cancel: cancel}
	failedStarts := make([]error, 0, len(m.channels))
	failedNames := make([]string, 0, len(m.channels))

	for name, channel := range m.channels {
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
			failedStarts = append(failedStarts, fmt.Errorf("channel %s: %w", name, err))
			failedNames = append(failedNames, name)
			continue
		}
		// Lazily create worker only after channel starts successfully
		channelType := name
		if m.config != nil {
			if bc := m.config.Channels.Get(name); bc != nil && bc.Type != "" {
				channelType = bc.Type
			}
		}
		workerCtx, workerCancel := context.WithCancel(dispatchCtx)
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
	}

	if len(m.channels) > 0 && len(m.workers) == 0 {
		if m.dispatchTask != nil {
			m.dispatchTask.cancel()
			m.dispatchTask = nil
		}

		sort.Strings(failedNames)
		if len(failedStarts) == 0 {
			return fmt.Errorf("failed to start any enabled channels")
		}

		logger.ErrorCF("channels", "All enabled channels failed to start", map[string]any{
			"failed":          len(failedNames),
			"total":           len(m.channels),
			"failed_channels": failedNames,
		})

		return fmt.Errorf("failed to start any enabled channels: %w", errors.Join(failedStarts...))
	}

	if len(failedNames) > 0 {
		sort.Strings(failedNames)
		logger.WarnCF("channels", "Some channels failed to start", map[string]any{
			"failed":          len(failedNames),
			"started":         len(m.workers),
			"total":           len(m.channels),
			"failed_channels": failedNames,
		})
	}

	// Start the dispatcher that reads from the bus and routes to workers
	go m.dispatchOutbound(dispatchCtx)
	go m.dispatchOutboundMedia(dispatchCtx)

	// Start the TTL janitor that cleans up stale typing/placeholder entries
	go m.runTTLJanitor(dispatchCtx)

	// Start shared HTTP server if configured
	if m.httpServer != nil {
		if len(m.httpListeners) > 0 {
			for _, listener := range m.httpListeners {
				ln := listener
				go func() {
					defer func() {
						if r := recover(); r != nil {
							logger.ErrorCF("channels", "HTTP server goroutine panic recovered",
								map[string]any{
									"addr":  ln.Addr().String(),
									"panic": fmt.Sprintf("%v", r),
									"stack": string(debug.Stack()),
								})
						}
					}()
					logger.InfoCF("channels", "Shared HTTP server listening", map[string]any{
						"addr": ln.Addr().String(),
					})
					if err := m.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
						logger.FatalCF("channels", "Shared HTTP server error", map[string]any{
							"addr":  ln.Addr().String(),
							"error": err.Error(),
						})
					}
				}()
			}
		} else {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						logger.ErrorCF("channels", "HTTP server goroutine panic recovered",
							map[string]any{
								"addr":  m.httpServer.Addr,
								"panic": fmt.Sprintf("%v", r),
								"stack": string(debug.Stack()),
							})
					}
				}()
				logger.InfoCF("channels", "Shared HTTP server listening", map[string]any{
					"addr": m.httpServer.Addr,
				})
				if err := m.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.FatalCF("channels", "Shared HTTP server error", map[string]any{
						"error": err.Error(),
					})
				}
			}()
		}
	}

	logger.InfoCF("channels", "Channel startup completed", map[string]any{
		"started": len(m.workers),
		"failed":  len(failedNames),
		"total":   len(m.channels),
	})
	return nil
}

func (m *Manager) StopAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	logger.InfoC("channels", "Stopping all channels")

	// Shutdown shared HTTP server first
	if m.httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := m.httpServer.Shutdown(shutdownCtx); err != nil {
			logger.ErrorCF("channels", "Shared HTTP server shutdown error", map[string]any{
				"error": err.Error(),
			})
		}
		m.httpServer = nil
		m.httpListeners = nil
	}

	// Cancel dispatcher
	if m.dispatchTask != nil {
		m.dispatchTask.cancel()
		m.dispatchTask = nil
	}

	// Workers stop from the canceled dispatch context. Do not close their
	// queues here: the dispatcher may already be sending to one.
	for _, w := range m.workers {
		if w != nil {
			if w.cancel != nil {
				w.cancel()
			}
		}
	}
	for _, w := range m.workers {
		if w != nil {
			<-w.done
		}
	}
	for _, w := range m.workers {
		if w != nil {
			<-w.mediaDone
		}
	}
	m.workers = make(map[string]*channelWorker)

	// Stop all channels
	for name, channel := range m.channels {
		logger.InfoCF("channels", "Stopping channel", map[string]any{
			"channel": name,
		})
		if err := channel.Stop(ctx); err != nil {
			logger.ErrorCF("channels", "Error stopping channel", map[string]any{
				"channel": name,
				"error":   err.Error(),
			})
			continue
		}
		m.publishChannelEvent(
			runtimeevents.KindChannelLifecycleStopped,
			name,
			runtimeevents.Scope{Channel: name},
			runtimeevents.SeverityInfo,
			ChannelLifecyclePayload{Type: channelTypeForEvent(m, name)},
		)
	}

	logger.InfoC("channels", "All channels stopped")
	return nil
}
