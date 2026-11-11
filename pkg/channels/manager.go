// GoGoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package channels

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
)

const (
	defaultChannelQueueSize = 16
	defaultRateLimit        = 10 // default 10 msg/s
	maxRetries              = 3
	rateLimitDelay          = 1 * time.Second
	baseBackoff             = 500 * time.Millisecond
	maxBackoff              = 8 * time.Second

	janitorInterval = 10 * time.Second
	typingStopTTL   = 5 * time.Minute
	placeholderTTL  = 10 * time.Minute

	streamAuxiliaryTombstoneTTL = 30 * time.Second

	workerShutdownTimeout  = 5 * time.Second
	workerShutdownFallback = 250 * time.Millisecond
)

// typingEntry wraps a typing stop function with a creation timestamp for TTL eviction.
type typingEntry struct {
	stop      func()
	createdAt time.Time
}

// reactionEntry wraps a reaction undo function with a creation timestamp for TTL eviction.
type reactionEntry struct {
	undo      func()
	createdAt time.Time
}

// placeholderEntry wraps a placeholder ID with a creation timestamp for TTL eviction.
type placeholderEntry struct {
	id        string
	createdAt time.Time
}

// channelRateConfig maps channel name to per-second rate limit.
var channelRateConfig = map[string]float64{
	"telegram": 20,
	"discord":  1,
	"qq":       5,
}

type channelWorker struct {
	ch         Channel
	queue      chan bus.OutboundMessage
	mediaQueue chan bus.OutboundMediaMessage
	done       chan struct{}
	mediaDone  chan struct{}
	limiter    *rate.Limiter
	cancel     context.CancelFunc
	ctx        context.Context
}

func (w *channelWorker) stopped() <-chan struct{} {
	if w == nil || w.ctx == nil {
		return nil
	}
	return w.ctx.Done()
}

type Manager struct {
	channels                  map[string]Channel
	workers                   map[string]*channelWorker
	bus                       *bus.MessageBus
	runtimeEvents             runtimeevents.Bus
	config                    *config.Config
	mediaStore                media.MediaStore
	dispatchTask              *asyncTask
	mux                       *dynamicServeMux
	httpServer                *http.Server
	httpListeners             []net.Listener
	mu                        sync.RWMutex
	configMu                  sync.RWMutex
	placeholders              sync.Map          // "channel:chatID" → placeholderID (string)
	typingStops               sync.Map          // "channel:chatID" → func()
	reactionUndos             sync.Map          // "channel:chatID" → reactionEntry
	streamActive              sync.Map          // streamSuppressionKey → true (set when streamer.Finalize sent the message)
	streamAuxiliaryTombstones sync.Map          // streamSuppressionKey → time.Time (drops late auxiliary messages after stream final)
	channelHashes             map[string]string // channel name → config hash
}

func (m *Manager) currentConfig() *config.Config {
	if m == nil {
		return nil
	}
	m.configMu.RLock()
	defer m.configMu.RUnlock()
	return m.config
}

func (m *Manager) setConfig(cfg *config.Config) {
	m.configMu.Lock()
	defer m.configMu.Unlock()
	m.config = cfg
}

type mediaStoreSetter interface {
	SetMediaStore(s media.MediaStore)
}

// ManagerOption configures a channel Manager.
type ManagerOption func(*Manager)

// WithRuntimeEvents injects the runtime event bus used for channel observations.
func WithRuntimeEvents(eventBus runtimeevents.Bus) ManagerOption {
	return func(m *Manager) {
		m.runtimeEvents = eventBus
	}
}

// ChannelLifecyclePayload describes channel lifecycle runtime events.
type ChannelLifecyclePayload struct {
	Type  string `json:"type,omitempty"`
	Error string `json:"error,omitempty"`
}

// ChannelOutboundPayload describes channel outbound message runtime events.
type ChannelOutboundPayload struct {
	Media            bool     `json:"media,omitempty"`
	ContentLen       int      `json:"content_len,omitempty"`
	MessageIDs       []string `json:"message_ids,omitempty"`
	ReplyToMessageID string   `json:"reply_to_message_id,omitempty"`
	Error            string   `json:"error,omitempty"`
	Retries          int      `json:"retries,omitempty"`
}

type toolFeedbackMessageTracker interface {
	RecordToolFeedbackMessage(chatID, messageID, content string)
	ClearToolFeedbackMessage(chatID string)
}

type toolFeedbackMessageCleaner interface {
	DismissToolFeedbackMessage(ctx context.Context, chatID string)
}

type toolFeedbackMessageTargetResolver interface {
	ToolFeedbackMessageChatID(chatID string, outboundCtx *bus.InboundContext) string
}

type toolFeedbackMessageContentPreparer interface {
	PrepareToolFeedbackMessageContent(content string) string
}

type asyncTask struct {
	cancel context.CancelFunc
}

func NewManager(
	cfg *config.Config,
	messageBus *bus.MessageBus,
	store media.MediaStore,
	opts ...ManagerOption,
) (*Manager, error) {
	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		bus:           messageBus,
		config:        cfg,
		mediaStore:    store,
		channelHashes: make(map[string]string),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}

	// Register as streaming delegate so the agent loop can obtain streamers
	messageBus.SetStreamDelegate(m)

	if err := m.initChannels(&cfg.Channels); err != nil {
		return nil, err
	}

	// Store initial config hashes for all channels
	m.channelHashes = toChannelHashes(cfg)

	return m, nil
}

// SetMediaStore updates the store used by the manager and every channel that
// accepts media store injection. Gateway reload creates a fresh store, so
// keeping existing channels on the same store as the agent is required for
// inbound media refs to remain resolvable after reload.
func (m *Manager) SetMediaStore(store media.MediaStore) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.mediaStore = store
	for _, ch := range m.channels {
		if setter, ok := ch.(mediaStoreSetter); ok {
			setter.SetMediaStore(store)
		}
	}
}

type modelNameStreamer interface {
	SetModelName(modelName string)
}

type deliveryIDStreamer interface {
	SetDeliveryID(deliveryID string)
}

type turnUsageStreamer interface {
	SetTurnUsage(inputTokens, outputTokens int)
}

// splitMarkerStreamer turns accumulated streaming text containing
// MessageSplitMarker into separate channel stream messages.
type splitMarkerStreamer struct {
	mu               sync.Mutex
	current          bus.Streamer
	reasoning        bus.ReasoningStreamer
	begin            func(context.Context) (bus.Streamer, error)
	completedParts   int
	finalized        bool
	onFinalize       func(context.Context, string)
	clearMarker      func()
	modelName        string
	deliveryID       string
	turnInputTokens  int
	turnOutputTokens int
}

// finalizeHookStreamer wraps a Streamer to run a hook on Finalize.
type finalizeHookStreamer struct {
	Streamer
	onFinalize  func(context.Context, string)
	clearMarker func()
}
