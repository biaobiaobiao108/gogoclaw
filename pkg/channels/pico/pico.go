package pico

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/biaobiaobiao108/gogoclaw/pkg/approval"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

// picoConn represents a single WebSocket connection.
type picoConn struct {
	id        string
	conn      *websocket.Conn
	sessionID string
	writeMu   sync.Mutex
	closed    atomic.Bool
	cancel    context.CancelFunc // cancels per-connection goroutines (e.g. pingLoop)
}

var allowedInlineImageMIMETypes = map[string]struct{}{
	"image/jpeg": {},
	"image/png":  {},
	"image/gif":  {},
	"image/webp": {},
	"image/bmp":  {},
}

// maxPicoMessageBytes must accommodate one maximum-size inline image after
// base64 expansion, plus the surrounding message and JSON overhead.
const maxPicoMessageBytes = 32 << 20

// picoAllowList trusts the authenticated Pico transport when no sender list is
// configured. External chat channels use deny-all for an empty allow_from.
func picoAllowList(allowFrom []string) []string {
	for _, sender := range allowFrom {
		if strings.TrimSpace(sender) != "" {
			return allowFrom
		}
	}
	return []string{"*"}
}

// PicoChannel implements the native Pico Protocol WebSocket channel.
// It serves as the reference implementation for all optional capability interfaces.
type PicoChannel struct {
	*channels.BaseChannel
	bc                 *config.Channel
	config             *config.PicoSettings
	upgrader           websocket.Upgrader
	connections        map[string]*picoConn            // connID -> *picoConn
	sessionConnections map[string]map[string]*picoConn // sessionID -> connID -> *picoConn
	connsMu            sync.RWMutex
	ctx                context.Context
	cancel             context.CancelFunc
	progress           *channels.ToolFeedbackAnimator
	deleteMessageFn    func(context.Context, string, string) error
	// broadcastFn lets tests intercept outbound broadcasts. nil → broadcastToSession.
	broadcastFn       func(chatID string, msg PicoMessage) error
	turnStatus        func(chatID string, includeRecovery bool) map[string]any
	approvalHandler   func(context.Context, approval.Response) error
	branchHandler     func(context.Context, string, string) error
	sessionBusy       func(string) bool
	attachmentResolve func(string, string) (string, int64, error)
	approvalMu        sync.Mutex
	approvalSessions  map[string]string
	deliveryAck       func(context.Context, string, string, string) error
	dedupMu           sync.Mutex
	messageSeen       map[string]time.Time
	dedupePath        string
	dedupeLoaded      bool
}

// SetTurnStatusProvider supplies a session-scoped snapshot for connected clients.
func (c *PicoChannel) SetTurnStatusProvider(provider func(string, bool) map[string]any) {
	c.turnStatus = provider
}

func (c *PicoChannel) SetApprovalHandler(handler func(context.Context, approval.Response) error) {
	c.approvalHandler = handler
}

// SetBranchHandler lets the gateway switch the live agent context to a
// previously recorded answer branch.
func (c *PicoChannel) SetBranchHandler(handler func(context.Context, string, string) error) {
	c.branchHandler = handler
}

// SetSessionBusyHandler reports whether a Pico session has an active turn.
func (c *PicoChannel) SetSessionBusyHandler(handler func(string) bool) {
	c.sessionBusy = handler
}

// SetAttachmentResolver resolves a session-scoped upload ID to a managed file.
func (c *PicoChannel) SetAttachmentResolver(
	handler func(sessionID, attachmentID string) (string, int64, error),
) {
	c.attachmentResolve = handler
}

func (c *PicoChannel) SetDeliveryAckHandler(
	handler func(context.Context, string, string, string) error,
) {
	c.deliveryAck = handler
}

func (c *PicoChannel) SendApprovalRequest(ctx context.Context, req approval.Request) error {
	if c == nil {
		return fmt.Errorf("pico channel is nil")
	}
	if strings.TrimSpace(req.ID) == "" || strings.TrimSpace(req.ChatID) == "" {
		return fmt.Errorf("approval request is missing id or chat id")
	}
	c.rememberApprovalSession(req.ID, req.ChatID, req.ExpiresAt)

	payload := map[string]any{
		"approval_id": req.ID,
		"tool":        req.Tool,
		"arguments":   req.Arguments,
		"status":      req.Status,
		"risk":        req.RiskSummary,
		"created_at":  req.CreatedAt.UnixMilli(),
		"expires_at":  req.ExpiresAt.UnixMilli(),
	}
	msg := newMessage(TypeApprovalRequest, payload)
	msg.ID = req.ID
	if err := c.broadcast(req.ChatID, msg); err != nil {
		return err
	}
	return ctx.Err()
}

func (c *PicoChannel) rememberApprovalSession(approvalID, chatID string, expiresAt time.Time) {
	sessionID := strings.TrimPrefix(chatID, "pico:")
	c.approvalMu.Lock()
	if c.approvalSessions == nil {
		c.approvalSessions = make(map[string]string)
	}
	c.approvalSessions[approvalID] = sessionID
	c.approvalMu.Unlock()
	if !expiresAt.IsZero() {
		c.forgetApprovalSession(approvalID, sessionID, time.Until(expiresAt))
	}
}

// backfillApprovalSessions restores the runtime approval route from the
// persisted turn snapshot after a client reconnects to a new WebSocket.
func (c *PicoChannel) backfillApprovalSessions(sessionID string) {
	if c == nil || c.turnStatus == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	c.rememberApprovalFromTurnStatus(sessionID, c.turnStatus("pico:"+sessionID, false))
}

func (c *PicoChannel) rememberApprovalFromTurnStatus(sessionID string, payload map[string]any) {
	req, ok := payload["approval"].(*approval.Request)
	if !ok || req == nil || req.Status != approval.StatusPending || req.ID == "" || req.ChatID == "" {
		return
	}
	if strings.TrimPrefix(req.ChatID, "pico:") != sessionID {
		return
	}
	if !req.ExpiresAt.IsZero() && time.Now().After(req.ExpiresAt) {
		return
	}
	c.rememberApprovalSession(req.ID, req.ChatID, req.ExpiresAt)
}

// NewPicoChannel creates a new Pico Protocol channel.
func NewPicoChannel(
	bc *config.Channel,
	cfg *config.PicoSettings,
	messageBus *bus.MessageBus,
) (*PicoChannel, error) {
	if cfg.Token.String() == "" {
		return nil, fmt.Errorf("pico token is required")
	}

	base := channels.NewBaseChannel("pico", cfg, messageBus, picoAllowList(bc.AllowFrom))

	allowOrigins := cfg.AllowOrigins
	checkOrigin := func(r *http.Request) bool {
		if len(allowOrigins) == 0 {
			return true // allow all if not configured
		}
		origin := r.Header.Get("Origin")
		for _, allowed := range allowOrigins {
			if allowed == "*" || allowed == origin {
				return true
			}
		}
		return false
	}

	ch := &PicoChannel{
		BaseChannel: base,
		bc:          bc,
		config:      cfg,
		upgrader: websocket.Upgrader{
			CheckOrigin:     checkOrigin,
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		connections:        make(map[string]*picoConn),
		sessionConnections: make(map[string]map[string]*picoConn),
		messageSeen:        make(map[string]time.Time),
		dedupePath:         filepath.Join(config.GetHome(), "state", "pico-inbound.json"),
		approvalSessions:   make(map[string]string),
	}
	ch.progress = channels.NewToolFeedbackAnimator(ch.EditMessage)
	ch.deleteMessageFn = ch.DeleteMessage
	return ch, nil
}

type picoStreamer struct {
	channel           *PicoChannel
	chatID            string
	deliveryID        string
	modelName         string
	turnInputTokens   int
	turnOutputTokens  int
	messageID         string
	reasoningID       string
	throttleInterval  time.Duration
	minGrowth         int
	lastLen           int
	lastAt            time.Time
	lastContent       string
	deliveryPublished bool
	reasoningLastLen  int
	reasoningLastAt   time.Time
	reasoningContent  string
	mu                sync.Mutex
}

const (
	streamPacingThresholdRunes = 12
	streamPacingFrameInterval  = 24 * time.Millisecond
	streamPacingMinDuration    = 80 * time.Millisecond
	streamPacingMaxDuration    = 420 * time.Millisecond
	streamPacingMaxFrames      = 18
)

// truncate truncates a string to maxLen runes.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
