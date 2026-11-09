package pico

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/biaobiaobiao108/gogoclaw/pkg/approval"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/identity"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
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

// writeJSON sends a JSON message to the connection with write locking.
func (pc *picoConn) writeJSON(v any) error {
	if pc == nil || pc.conn == nil {
		return fmt.Errorf("connection unavailable")
	}
	if pc.closed.Load() {
		return fmt.Errorf("connection closed")
	}
	pc.writeMu.Lock()
	defer pc.writeMu.Unlock()
	return pc.conn.WriteJSON(v)
}

// close closes the connection.
func (pc *picoConn) close() {
	if pc == nil {
		return
	}
	if pc.closed.CompareAndSwap(false, true) {
		if pc.cancel != nil {
			pc.cancel()
		}
		if pc.conn != nil {
			pc.conn.Close()
		}
	}
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
	broadcastFn      func(chatID string, msg PicoMessage) error
	turnStatus       func(chatID string, includeRecovery bool) map[string]any
	approvalHandler  func(context.Context, approval.Response) error
	approvalMu       sync.Mutex
	approvalSessions map[string]string
	deliveryAck      func(context.Context, string, string, string) error
	dedupMu          sync.Mutex
	messageSeen      map[string]time.Time
	dedupePath       string
	dedupeLoaded     bool
}

// SetTurnStatusProvider supplies a session-scoped snapshot for connected clients.
func (c *PicoChannel) SetTurnStatusProvider(provider func(string, bool) map[string]any) {
	c.turnStatus = provider
}

func (c *PicoChannel) SetApprovalHandler(handler func(context.Context, approval.Response) error) {
	c.approvalHandler = handler
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
	sessionID := strings.TrimPrefix(req.ChatID, "pico:")
	c.approvalMu.Lock()
	if c.approvalSessions == nil {
		c.approvalSessions = make(map[string]string)
	}
	c.approvalSessions[req.ID] = sessionID
	c.approvalMu.Unlock()
	if !req.ExpiresAt.IsZero() {
		c.forgetApprovalSession(req.ID, sessionID, time.Until(req.ExpiresAt))
	}

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

// NewPicoChannel creates a new Pico Protocol channel.
func NewPicoChannel(
	bc *config.Channel,
	cfg *config.PicoSettings,
	messageBus *bus.MessageBus,
) (*PicoChannel, error) {
	if cfg.Token.String() == "" {
		return nil, fmt.Errorf("pico token is required")
	}

	base := channels.NewBaseChannel("pico", cfg, messageBus, bc.AllowFrom)

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

// createAndAddConnection checks MaxConnections and registers a connection atomically.
func (c *PicoChannel) createAndAddConnection(conn *websocket.Conn, sessionID string, maxConns int) (*picoConn, error) {
	c.connsMu.Lock()
	defer c.connsMu.Unlock()
	if len(c.connections) >= maxConns {
		return nil, channels.ErrTemporary
	}

	var connID string
	for {
		connID = uuid.New().String()
		if _, exists := c.connections[connID]; !exists {
			break
		}
	}

	pc := &picoConn{
		id:        connID,
		conn:      conn,
		sessionID: sessionID,
	}

	c.connections[pc.id] = pc
	bySession, ok := c.sessionConnections[pc.sessionID]
	if !ok {
		bySession = make(map[string]*picoConn)
		c.sessionConnections[pc.sessionID] = bySession
	}
	bySession[pc.id] = pc

	return pc, nil
}

// removeConnection deletes a connection from indexes and returns it when found.
func (c *PicoChannel) removeConnection(connID string) *picoConn {
	c.connsMu.Lock()
	defer c.connsMu.Unlock()

	pc, ok := c.connections[connID]
	if !ok {
		return nil
	}

	delete(c.connections, connID)
	if bySession, ok := c.sessionConnections[pc.sessionID]; ok {
		delete(bySession, connID)
		if len(bySession) == 0 {
			delete(c.sessionConnections, pc.sessionID)
		}
	}

	return pc
}

// takeAllConnections snapshots and clears all connection indexes.
func (c *PicoChannel) takeAllConnections() []*picoConn {
	c.connsMu.Lock()
	defer c.connsMu.Unlock()

	all := make([]*picoConn, 0, len(c.connections))
	for _, pc := range c.connections {
		all = append(all, pc)
	}
	clear(c.connections)
	clear(c.sessionConnections)

	return all
}

// sessionConnectionsSnapshot returns all active connections for a session.
func (c *PicoChannel) sessionConnectionsSnapshot(sessionID string) []*picoConn {
	c.connsMu.RLock()
	defer c.connsMu.RUnlock()

	bySession, ok := c.sessionConnections[sessionID]
	if !ok || len(bySession) == 0 {
		return nil
	}

	conns := make([]*picoConn, 0, len(bySession))
	for _, pc := range bySession {
		conns = append(conns, pc)
	}
	return conns
}

// currentConnCount returns a lock-protected snapshot of active connection count.
func (c *PicoChannel) currentConnCount() int {
	c.connsMu.RLock()
	defer c.connsMu.RUnlock()
	return len(c.connections)
}

// Start implements Channel.
func (c *PicoChannel) Start(ctx context.Context) error {
	logger.InfoC("pico", "Starting Pico Protocol channel")
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.SetRunning(true)
	logger.InfoC("pico", "Pico Protocol channel started")
	return nil
}

// Stop implements Channel.
func (c *PicoChannel) Stop(ctx context.Context) error {
	logger.InfoC("pico", "Stopping Pico Protocol channel")
	c.SetRunning(false)

	// Close all connections
	for _, pc := range c.takeAllConnections() {
		pc.close()
	}

	if c.cancel != nil {
		c.cancel()
	}
	if c.progress != nil {
		c.progress.StopAll()
	}

	logger.InfoC("pico", "Pico Protocol channel stopped")
	return nil
}

// WebhookPath implements channels.WebhookHandler.
func (c *PicoChannel) WebhookPath() string { return "/pico/" }

// ServeHTTP implements http.Handler for the shared HTTP server.
func (c *PicoChannel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/pico")

	switch path {
	case "/ws", "/ws/":
		c.handleWebSocket(w, r)
	default:
		if strings.HasPrefix(path, "/media/") {
			c.handleMediaDownload(w, r)
			return
		}
		http.NotFound(w, r)
	}
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

// SendMedia implements channels.MediaSender for the Pico web UI.
// Media is delivered as a normal assistant message carrying structured
// attachments plus an authenticated same-origin download URL.
func (c *PicoChannel) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}
	trackedMsgID, hasTrackedMsg := c.currentToolFeedbackMessage(msg.ChatID)

	store := c.GetMediaStore()
	if store == nil {
		return nil, fmt.Errorf("no media store available: %w", channels.ErrSendFailed)
	}

	attachments := make([]map[string]any, 0, len(msg.Parts))
	caption := ""

	for _, part := range msg.Parts {
		localPath, meta, err := store.ResolveWithMeta(part.Ref)
		if err != nil {
			logger.ErrorCF("pico", "Failed to resolve media ref", map[string]any{
				"ref":   part.Ref,
				"error": err.Error(),
			})
			continue
		}

		filename := strings.TrimSpace(part.Filename)
		if filename == "" {
			filename = strings.TrimSpace(meta.Filename)
		}
		if filename == "" {
			filename = filepath.Base(localPath)
		}

		contentType := strings.TrimSpace(part.ContentType)
		if contentType == "" {
			contentType = strings.TrimSpace(meta.ContentType)
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		attachmentType := strings.TrimSpace(part.Type)
		if attachmentType == "" {
			attachmentType = picoInferAttachmentType(filename, contentType)
		}

		attachmentURL, err := picoDownloadURLForRef(part.Ref)
		if err != nil {
			logger.ErrorCF("pico", "Failed to build media download URL", map[string]any{
				"ref":   part.Ref,
				"error": err.Error(),
			})
			continue
		}

		attachments = append(attachments, map[string]any{
			"type":         attachmentType,
			"url":          attachmentURL,
			"filename":     filename,
			"content_type": contentType,
		})

		if caption == "" && strings.TrimSpace(part.Caption) != "" {
			caption = strings.TrimSpace(part.Caption)
		}
	}

	if len(attachments) == 0 {
		return nil, fmt.Errorf("no deliverable media parts: %w", channels.ErrSendFailed)
	}

	msgID := strings.TrimSpace(msg.ID)
	if msgID == "" {
		msgID = uuid.New().String()
	}
	outMsg := newMessage(TypeMessageCreate, map[string]any{
		PayloadKeyContent: caption,
		"attachments":     attachments,
		"message_id":      msgID,
	})
	outMsg.ID = msgID
	if modelName := strings.TrimSpace(msg.Context.Raw[PayloadKeyModelName]); modelName != "" {
		outMsg.Payload[PayloadKeyModelName] = modelName
	}

	if err := c.broadcastToSession(msg.ChatID, outMsg); err != nil {
		return nil, err
	}
	if hasTrackedMsg {
		c.dismissTrackedToolFeedbackMessage(ctx, msg.ChatID, trackedMsgID)
	}

	return []string{msgID}, nil
}

func picoDownloadURLForRef(ref string) (string, error) {
	refID, err := picoMediaRefID(ref)
	if err != nil {
		return "", err
	}
	return "/pico/media/" + url.PathEscape(refID), nil
}

func picoMediaRefID(ref string) (string, error) {
	refID := strings.TrimSpace(strings.TrimPrefix(ref, "media://"))
	if refID == "" || strings.Contains(refID, "/") {
		return "", fmt.Errorf("invalid media ref %q", ref)
	}
	return refID, nil
}

func picoInferAttachmentType(filename, contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	filename = strings.ToLower(strings.TrimSpace(filename))

	switch {
	case strings.HasPrefix(contentType, "image/"):
		return "image"
	case strings.HasPrefix(contentType, "audio/"):
		return "audio"
	case strings.HasPrefix(contentType, "video/"):
		return "video"
	}

	switch ext := filepath.Ext(filename); ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg":
		return "image"
	case ".mp3", ".wav", ".ogg", ".m4a", ".flac", ".aac", ".wma", ".opus":
		return "audio"
	case ".mp4", ".avi", ".mov", ".webm", ".mkv":
		return "video"
	default:
		return "file"
	}
}

func picoAllowsInlineDisplay(filename, contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	filename = strings.ToLower(strings.TrimSpace(filename))

	if strings.Contains(contentType, "svg") || filepath.Ext(filename) == ".svg" {
		return false
	}

	return picoInferAttachmentType(filename, contentType) == "image"
}

func (c *PicoChannel) handleMediaDownload(w http.ResponseWriter, r *http.Request) {
	if !c.IsRunning() {
		http.Error(w, "channel not running", http.StatusServiceUnavailable)
		return
	}
	if !c.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	refID := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/pico/media/"), "/"))
	if refID == "" {
		http.NotFound(w, r)
		return
	}

	store := c.GetMediaStore()
	if store == nil {
		http.Error(w, "media store unavailable", http.StatusServiceUnavailable)
		return
	}

	localPath, meta, err := store.ResolveWithMeta("media://" + refID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	file, err := os.Open(localPath)
	if err != nil {
		http.Error(w, "failed to open media", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		http.Error(w, "failed to stat media", http.StatusInternalServerError)
		return
	}

	filename := strings.TrimSpace(meta.Filename)
	if filename == "" {
		filename = filepath.Base(localPath)
	}
	contentType := strings.TrimSpace(meta.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	dispositionType := "attachment"
	if picoAllowsInlineDisplay(filename, contentType) {
		dispositionType = "inline"
	}

	if cd := mime.FormatMediaType(dispositionType, map[string]string{"filename": filename}); cd != "" {
		w.Header().Set("Content-Disposition", cd)
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

// broadcast routes through broadcastFn when set (tests), else broadcastToSession.
func (c *PicoChannel) broadcast(chatID string, msg PicoMessage) error {
	if c.broadcastFn != nil {
		return c.broadcastFn(chatID, msg)
	}
	return c.broadcastToSession(chatID, msg)
}

// broadcastToSession sends a message to all connections with a matching session.
func (c *PicoChannel) broadcastToSession(chatID string, msg PicoMessage) error {
	// chatID format: "pico:<sessionID>"
	sessionID := strings.TrimPrefix(chatID, "pico:")
	msg.SessionID = sessionID

	var sent bool
	for _, pc := range c.sessionConnectionsSnapshot(sessionID) {
		if err := pc.writeJSON(msg); err != nil {
			logger.DebugCF("pico", "Write to connection failed", map[string]any{
				"conn_id": pc.id,
				"error":   err.Error(),
			})
		} else {
			sent = true
		}
	}

	if !sent {
		return fmt.Errorf("no active connections for session %s: %w", sessionID, channels.ErrSendFailed)
	}
	return nil
}

// handleWebSocket upgrades the HTTP connection and manages the WebSocket lifecycle.
func (c *PicoChannel) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !c.IsRunning() {
		http.Error(w, "channel not running", http.StatusServiceUnavailable)
		return
	}

	// Authenticate
	if !c.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Check connection limit
	maxConns := c.config.MaxConnections
	if maxConns <= 0 {
		maxConns = 100
	}
	if c.currentConnCount() >= maxConns {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}

	// Echo the matched subprotocol back so the browser accepts the upgrade.
	var responseHeader http.Header
	if proto := c.matchedSubprotocol(r); proto != "" {
		responseHeader = http.Header{"Sec-WebSocket-Protocol": {proto}}
	}

	conn, err := c.upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		logger.ErrorCF("pico", "WebSocket upgrade failed", map[string]any{
			"error": err.Error(),
		})
		return
	}

	// Determine session ID from query param or generate one
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	pc, err := c.createAndAddConnection(conn, sessionID, maxConns)
	if err != nil {
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "too many connections"),
			time.Now().Add(2*time.Second),
		)
		_ = conn.Close()
		return
	}

	logger.InfoCF("pico", "WebSocket client connected", map[string]any{
		"conn_id":    pc.id,
		"session_id": sessionID,
	})

	go c.readLoop(pc)
}

// authenticate checks the request for a valid token:
//  1. Authorization: Bearer <token> header
//  2. Sec-WebSocket-Protocol "token.<value>" (for browsers that can't set headers)
//  3. Query parameter "token" (only when AllowTokenQuery is on)
func (c *PicoChannel) authenticate(r *http.Request) bool {
	token := c.config.Token.String()
	if token == "" {
		return false
	}

	// Check Authorization header
	auth := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(auth, "Bearer "); ok {
		if after == token {
			return true
		}
	}

	// Check Sec-WebSocket-Protocol subprotocol ("token.<value>")
	if c.matchedSubprotocol(r) != "" {
		return true
	}

	// Check query parameter only when explicitly allowed
	if c.config.AllowTokenQuery {
		if r.URL.Query().Get("token") == token {
			return true
		}
	}

	return false
}

// matchedSubprotocol returns the "token.<value>" subprotocol that matches
// the configured token, or "" if none do.
func (c *PicoChannel) matchedSubprotocol(r *http.Request) string {
	token := c.config.Token.String()
	for _, proto := range websocket.Subprotocols(r) {
		if after, ok := strings.CutPrefix(proto, "token."); ok && after == token {
			return proto
		}
	}
	return ""
}

// readLoop reads messages from a WebSocket connection.
func (c *PicoChannel) readLoop(pc *picoConn) {
	defer func() {
		pc.close()
		if removed := c.removeConnection(pc.id); removed != nil {
			logger.InfoCF("pico", "WebSocket client disconnected", map[string]any{
				"conn_id":    removed.id,
				"session_id": removed.sessionID,
			})
		}
	}()

	readTimeout := time.Duration(c.config.ReadTimeout) * time.Second
	if readTimeout <= 0 {
		readTimeout = 60 * time.Second
	}

	_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))
	pc.conn.SetPongHandler(func(appData string) error {
		_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))
		return nil
	})

	// Start ping ticker
	pingInterval := time.Duration(c.config.PingInterval) * time.Second
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}
	go c.pingLoop(pc, pingInterval)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		_, rawMsg, err := pc.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				logger.DebugCF("pico", "WebSocket read error", map[string]any{
					"conn_id": pc.id,
					"error":   err.Error(),
				})
			}
			return
		}

		_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))

		var msg PicoMessage
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			errMsg := newError("invalid_message", "failed to parse message")
			pc.writeJSON(errMsg)
			continue
		}

		c.handleMessage(pc, msg)
	}
}

// pingLoop sends periodic ping frames to keep the connection alive.
func (c *PicoChannel) pingLoop(pc *picoConn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if pc.closed.Load() {
				return
			}
			pc.writeMu.Lock()
			err := pc.conn.WriteMessage(websocket.PingMessage, nil)
			pc.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

// handleMessage processes an inbound Pico Protocol message.
func (c *PicoChannel) handleMessage(pc *picoConn, msg PicoMessage) {
	switch msg.Type {
	case TypePing:
		pong := newMessage(TypePong, nil)
		pong.ID = msg.ID
		pc.writeJSON(pong)
	case TypeTurnStatusGet:
		payload := map[string]any{"active": false}
		if c.turnStatus != nil {
			includeRecovery, _ := msg.Payload["include_recovery"].(bool)
			payload = c.turnStatus("pico:"+pc.sessionID, includeRecovery)
		}
		status := newMessage(TypeTurnStatus, payload)
		status.SessionID = pc.sessionID
		status.ID = msg.ID
		pc.writeJSON(status)

	case TypeMessageSend:
		c.handleMessageSend(pc, msg)

	case TypeMediaSend:
		c.handleMessageSend(pc, msg)

	case TypeApprovalRespond:
		c.handleApprovalRespond(pc, msg)

	case TypeDeliveryAck:
		c.handleDeliveryAck(pc, msg)

	default:
		errMsg := newError("unknown_type", fmt.Sprintf("unknown message type: %s", msg.Type))
		pc.writeJSON(errMsg)
	}
}

func (c *PicoChannel) handleDeliveryAck(pc *picoConn, msg PicoMessage) {
	deliveryID, _ := msg.Payload[PayloadKeyDeliveryID].(string)
	deliveryID = strings.TrimSpace(deliveryID)
	if deliveryID == "" {
		deliveryID = strings.TrimSpace(msg.ID)
	}
	if deliveryID == "" || c.deliveryAck == nil {
		pc.writeJSON(newErrorWithPayload("invalid_delivery_ack", "delivery ACK handler is unavailable", map[string]any{
			"request_id": deliveryID,
		}))
		return
	}
	chatID := "pico:" + pc.sessionID
	if err := c.deliveryAck(c.ctx, "pico", chatID, deliveryID); err != nil {
		pc.writeJSON(newErrorWithPayload("delivery_ack_failed", err.Error(), map[string]any{
			"request_id": deliveryID,
		}))
		return
	}
	ack := newMessage(TypeDeliveryAck, map[string]any{
		PayloadKeyDeliveryID: deliveryID,
		"status":             "acknowledged",
	})
	ack.ID = deliveryID
	ack.SessionID = pc.sessionID
	_ = pc.writeJSON(ack)
}

func (c *PicoChannel) handleApprovalRespond(pc *picoConn, msg PicoMessage) {
	approvalID, _ := msg.Payload["approval_id"].(string)
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		approvalID = strings.TrimSpace(msg.ID)
	}
	if approvalID == "" || c.approvalHandler == nil {
		pc.writeJSON(newErrorWithPayload("invalid_approval", "approval handler is unavailable", map[string]any{"request_id": approvalID}))
		return
	}

	c.approvalMu.Lock()
	sessionID, ok := c.approvalSessions[approvalID]
	c.approvalMu.Unlock()
	if !ok || sessionID != pc.sessionID {
		pc.writeJSON(newErrorWithPayload("approval_not_found", "approval request not found", map[string]any{"request_id": approvalID}))
		return
	}
	approve, _ := msg.Payload["approve"].(bool)
	scope, _ := msg.Payload["scope"].(string)
	reason, _ := msg.Payload["reason"].(string)
	response := approval.Response{ID: approvalID, Approve: approve, Scope: approval.Scope(scope), Reason: reason}
	if err := c.approvalHandler(c.ctx, response); err != nil {
		pc.writeJSON(newErrorWithPayload("approval_failed", err.Error(), map[string]any{"request_id": approvalID}))
		return
	}
	c.forgetApprovalSession(approvalID, sessionID, 10*time.Minute)
	ack := newMessage(TypeApprovalAck, map[string]any{
		"approval_id": approvalID,
		"status":      "accepted",
	})
	ack.ID = approvalID
	ack.SessionID = pc.sessionID
	_ = pc.writeJSON(ack)
	resolved := newMessage(TypeApprovalResolved, map[string]any{
		"approval_id": approvalID,
		"approved":    approve,
		"scope":       scope,
	})
	resolved.ID = approvalID
	_ = c.broadcast("pico:"+pc.sessionID, resolved)
}

func (c *PicoChannel) forgetApprovalSession(approvalID, sessionID string, after time.Duration) {
	if after < 0 {
		after = 0
	}
	time.AfterFunc(after, func() {
		c.approvalMu.Lock()
		if c.approvalSessions[approvalID] == sessionID {
			delete(c.approvalSessions, approvalID)
		}
		c.approvalMu.Unlock()
	})
}

// handleMessageSend processes an inbound message.send from a client.
func (c *PicoChannel) handleMessageSend(pc *picoConn, msg PicoMessage) {
	dedupeKey := pc.sessionID + "\x00" + msg.ID
	if msg.ID != "" {
		claimed, err := c.claimMessage(dedupeKey)
		if err != nil {
			pc.writeJSON(newErrorWithPayload("dedupe_failed", err.Error(), map[string]any{
				"request_id": msg.ID,
			}))
			return
		}
		if !claimed {
			c.writeMessageAck(pc, msg.ID, "duplicate")
			return
		}
	}
	content, _ := msg.Payload["content"].(string)
	media, err := parseInlineImageMedia(msg.Payload)
	if err != nil {
		c.releaseMessage(dedupeKey)
		errMsg := newErrorWithPayload("invalid_media", err.Error(), map[string]any{
			"request_id": msg.ID,
		})
		pc.writeJSON(errMsg)
		return
	}

	if strings.TrimSpace(content) == "" && len(media) == 0 {
		c.releaseMessage(dedupeKey)
		errMsg := newErrorWithPayload("empty_content", "message content is empty", map[string]any{
			"request_id": msg.ID,
		})
		pc.writeJSON(errMsg)
		return
	}

	sessionID := msg.SessionID
	if sessionID == "" {
		sessionID = pc.sessionID
	}

	chatID := "pico:" + sessionID
	senderID := "pico-user"

	metadata := map[string]string{
		"platform":   "pico",
		"session_id": sessionID,
		"conn_id":    pc.id,
	}

	logger.DebugCF("pico", "Received message", map[string]any{
		"session_id": sessionID,
		"preview":    truncate(content, 50),
		"media":      len(media),
	})

	sender := bus.SenderInfo{
		Platform:    "pico",
		PlatformID:  senderID,
		CanonicalID: identity.BuildCanonicalID("pico", senderID),
	}

	if !c.IsAllowedSender(sender) {
		c.releaseMessage(dedupeKey)
		return
	}

	inboundCtx := bus.InboundContext{
		Channel:   "pico",
		ChatID:    chatID,
		ChatType:  "direct",
		SenderID:  senderID,
		MessageID: msg.ID,
		Raw:       metadata,
	}

	if err := c.HandleInboundContext(c.ctx, chatID, content, media, inboundCtx, sender); err != nil {
		c.releaseMessage(dedupeKey)
		errMsg := newErrorWithPayload("send_failed", err.Error(), map[string]any{"request_id": msg.ID})
		_ = pc.writeJSON(errMsg)
		return
	}
	c.writeMessageAck(pc, msg.ID, "accepted")
}

func (c *PicoChannel) writeMessageAck(pc *picoConn, requestID, status string) {
	if requestID == "" || pc == nil || pc.conn == nil {
		return
	}
	ack := newMessage(TypeMessageAck, map[string]any{
		"request_id": requestID,
		"status":     status,
	})
	ack.ID = requestID
	ack.SessionID = pc.sessionID
	_ = pc.writeJSON(ack)
}

func (c *PicoChannel) claimMessage(key string) (bool, error) {
	c.dedupMu.Lock()
	defer c.dedupMu.Unlock()
	now := time.Now()
	if err := c.loadMessageDedupeLocked(); err != nil {
		return false, err
	}
	for existing, seenAt := range c.messageSeen {
		if now.Sub(seenAt) > 24*time.Hour {
			delete(c.messageSeen, existing)
		}
	}
	if _, ok := c.messageSeen[key]; ok {
		return false, nil
	}
	c.messageSeen[key] = now
	if err := c.saveMessageDedupeLocked(); err != nil {
		delete(c.messageSeen, key)
		return false, err
	}
	return true, nil
}

func (c *PicoChannel) releaseMessage(key string) {
	if key == "" || strings.HasSuffix(key, "\x00") {
		return
	}
	c.dedupMu.Lock()
	defer c.dedupMu.Unlock()
	delete(c.messageSeen, key)
	if err := c.saveMessageDedupeLocked(); err != nil {
		logger.WarnCF("pico", "Failed to release inbound message reservation", map[string]any{
			"error": err.Error(),
		})
	}
}

func (c *PicoChannel) loadMessageDedupeLocked() error {
	if c.dedupeLoaded {
		return nil
	}
	c.dedupeLoaded = true
	if c.dedupePath == "" {
		return nil
	}
	data, err := os.ReadFile(c.dedupePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read inbound dedupe state: %w", err)
	}

	type entry struct {
		Key      string    `json:"key"`
		SeenTime time.Time `json:"seen_at"`
	}
	var entries []entry
	if err := json.Unmarshal(data, &entries); err == nil {
		c.messageSeen = make(map[string]time.Time, len(entries))
		for _, item := range entries {
			if item.Key != "" {
				c.messageSeen[item.Key] = item.SeenTime
			}
		}
		return nil
	}

	var legacy map[string]time.Time
	if err := json.Unmarshal(data, &legacy); err == nil {
		c.messageSeen = legacy
		if c.messageSeen == nil {
			c.messageSeen = make(map[string]time.Time)
		}
		return nil
	}

	logger.WarnCF("pico", "Ignoring corrupt inbound dedupe state", map[string]any{
		"path": c.dedupePath,
	})
	c.messageSeen = make(map[string]time.Time)
	return nil
}

func (c *PicoChannel) saveMessageDedupeLocked() error {
	if c.dedupePath == "" {
		return nil
	}
	type entry struct {
		Key      string    `json:"key"`
		SeenTime time.Time `json:"seen_at"`
	}
	entries := make([]entry, 0, len(c.messageSeen))
	for key, seenAt := range c.messageSeen {
		entries = append(entries, entry{Key: key, SeenTime: seenAt.UTC()})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].SeenTime.After(entries[j].SeenTime)
	})
	if len(entries) > 10000 {
		entries = entries[:10000]
		pruned := make(map[string]time.Time, len(entries))
		for _, item := range entries {
			pruned[item.Key] = item.SeenTime
		}
		c.messageSeen = pruned
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(c.dedupePath, data, 0o600); err != nil {
		return fmt.Errorf("write inbound dedupe state: %w", err)
	}
	return nil
}

// truncate truncates a string to maxLen runes.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func parseInlineImageMedia(payload map[string]any) ([]string, error) {
	if len(payload) == 0 {
		return nil, nil
	}

	media, err := parseInlineImageValues(payload["media"])
	if err != nil {
		return nil, err
	}

	attachments, err := parseInlineImageAttachments(payload["attachments"])
	if err != nil {
		return nil, err
	}
	media = append(media, attachments...)

	return media, nil
}

func parseInlineImageValues(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	switch values := raw.(type) {
	case []any:
		media := make([]string, 0, len(values))
		for i, item := range values {
			value, err := inlineImageValue(item)
			if err != nil {
				return nil, fmt.Errorf("media[%d]: %w", i, err)
			}
			if err := validateInlineImageDataURL(value); err != nil {
				return nil, fmt.Errorf("media[%d]: %w", i, err)
			}
			media = append(media, value)
		}
		return media, nil
	case []string:
		media := make([]string, 0, len(values))
		for i, value := range values {
			value = strings.TrimSpace(value)
			if err := validateInlineImageDataURL(value); err != nil {
				return nil, fmt.Errorf("media[%d]: %w", i, err)
			}
			media = append(media, value)
		}
		return media, nil
	case string:
		value := strings.TrimSpace(values)
		if err := validateInlineImageDataURL(value); err != nil {
			return nil, err
		}
		return []string{value}, nil
	default:
		return nil, fmt.Errorf("media must be a string or array of strings")
	}
}

func parseInlineImageAttachments(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}

	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("attachments must be an array")
	}

	media := make([]string, 0, len(values))
	for i, item := range values {
		attachment, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("attachments[%d]: attachment must be an object", i)
		}

		attachmentType, _ := attachment["type"].(string)
		attachmentType = strings.ToLower(strings.TrimSpace(attachmentType))
		if attachmentType != "" && attachmentType != "image" {
			continue
		}

		value, err := inlineImageValue(attachment)
		if err != nil {
			if attachmentType == "image" {
				return nil, fmt.Errorf("attachments[%d]: %w", i, err)
			}
			continue
		}
		if !strings.HasPrefix(value, "data:") {
			continue
		}
		if err := validateInlineImageDataURL(value); err != nil {
			return nil, fmt.Errorf("attachments[%d]: %w", i, err)
		}
		media = append(media, value)
	}
	return media, nil
}

func inlineImageValue(item any) (string, error) {
	switch value := item.(type) {
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return "", fmt.Errorf("image payload is empty")
		}
		return value, nil
	case map[string]any:
		for _, key := range []string{"url", "data_url"} {
			if raw, ok := value[key].(string); ok && strings.TrimSpace(raw) != "" {
				return strings.TrimSpace(raw), nil
			}
		}
		return "", fmt.Errorf("image payload must include url or data_url")
	default:
		return "", fmt.Errorf("image payload must be a string or object")
	}
}

func validateInlineImageDataURL(mediaURL string) error {
	if mediaURL == "" {
		return fmt.Errorf("image payload is empty")
	}
	if !strings.HasPrefix(mediaURL, "data:image/") {
		return fmt.Errorf("only inline image data URLs are supported")
	}

	header, data, found := strings.Cut(mediaURL, ",")
	if !found || strings.TrimSpace(data) == "" {
		return fmt.Errorf("image data URL is malformed")
	}
	if !strings.Contains(header, ";base64") {
		return fmt.Errorf("image data URL must be base64 encoded")
	}
	mimeType, _, _ := strings.Cut(strings.TrimPrefix(header, "data:"), ";")
	if _, ok := allowedInlineImageMIMETypes[mimeType]; !ok {
		return fmt.Errorf("unsupported image format: %s", mimeType)
	}

	data = strings.TrimSpace(data)
	if base64.StdEncoding.DecodedLen(len(data)) > config.DefaultMaxMediaSize {
		return fmt.Errorf("image exceeds %d byte limit", config.DefaultMaxMediaSize)
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return fmt.Errorf("invalid base64 image data")
	}

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
