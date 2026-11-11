package pico

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/biaobiaobiao108/gogoclaw/pkg/approval"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/identity"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
)

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

	c.backfillApprovalSessions(sessionID)
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
	pc.conn.SetReadLimit(maxPicoMessageBytes)
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
		c.rememberApprovalFromTurnStatus(pc.sessionID, payload)
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

	case TypeBranchSelect:
		c.handleBranchSelect(pc, msg)

	default:
		errMsg := newError("unknown_type", fmt.Sprintf("unknown message type: %s", msg.Type))
		pc.writeJSON(errMsg)
	}
}

func (c *PicoChannel) handleBranchSelect(pc *picoConn, msg PicoMessage) {
	versionID, _ := msg.Payload["branch_id"].(string)
	versionID = strings.TrimSpace(versionID)
	if versionID == "" {
		_ = pc.writeJSON(newErrorWithPayload("invalid_branch", "branch_id is required", map[string]any{"request_id": msg.ID}))
		return
	}
	if c.sessionBusy != nil && c.sessionBusy(pc.sessionID) {
		_ = pc.writeJSON(newErrorWithPayload("session_busy", "stop the active task before switching answers", map[string]any{"request_id": msg.ID}))
		return
	}
	if c.branchHandler == nil {
		_ = pc.writeJSON(newErrorWithPayload("branch_unavailable", "branch switching is unavailable", map[string]any{"request_id": msg.ID}))
		return
	}
	if err := c.branchHandler(c.ctx, pc.sessionID, versionID); err != nil {
		_ = pc.writeJSON(newErrorWithPayload("branch_switch_failed", err.Error(), map[string]any{"request_id": msg.ID}))
		return
	}
	updated := newMessage(TypeBranchUpdated, map[string]any{"branch_id": versionID})
	updated.SessionID = pc.sessionID
	updated.ID = msg.ID
	if err := c.broadcastToSession("pico:"+pc.sessionID, updated); err != nil {
		_ = pc.writeJSON(newErrorWithPayload("branch_switch_failed", err.Error(), map[string]any{"request_id": msg.ID}))
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
	response := approval.Response{
		ID:      approvalID,
		Approve: approve,
		Scope:   approval.Scope(scope),
		Reason:  reason,
		Channel: "pico",
		ChatID:  "pico:" + pc.sessionID,
	}
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
			c.writeMessageAck(pc, msg.ID, "", "duplicate")
			return
		}
	}
	content, _ := msg.Payload["content"].(string)
	media, attachments, err := parsePicoAttachmentPayload(pc.sessionID, msg.Payload, c.attachmentResolve)
	if err != nil {
		c.releaseMessage(dedupeKey)
		errMsg := newErrorWithPayload("invalid_media", err.Error(), map[string]any{
			"request_id": msg.ID,
		})
		pc.writeJSON(errMsg)
		return
	}

	regenerateID, _ := msg.Payload["regenerate_from_message_id"].(string)
	editID, _ := msg.Payload["edit_message_id"].(string)
	if (strings.TrimSpace(regenerateID) != "" || strings.TrimSpace(editID) != "") &&
		c.sessionBusy != nil && c.sessionBusy(pc.sessionID) {
		c.releaseMessage(dedupeKey)
		pc.writeJSON(newErrorWithPayload("session_busy", "stop the active task before editing or regenerating an answer", map[string]any{
			"request_id": msg.ID,
		}))
		return
	}
	if strings.TrimSpace(content) == "" && len(media) == 0 && strings.TrimSpace(regenerateID) == "" && strings.TrimSpace(editID) == "" {
		c.releaseMessage(dedupeKey)
		errMsg := newErrorWithPayload("empty_content", "message content is empty", map[string]any{
			"request_id": msg.ID,
		})
		pc.writeJSON(errMsg)
		return
	}

	sessionID := pc.sessionID

	chatID := "pico:" + sessionID
	senderID := "pico-user"

	metadata := map[string]string{
		"platform":   "pico",
		"session_id": sessionID,
		"conn_id":    pc.id,
	}
	for _, key := range []string{"thinking_level", "regenerate_from_message_id", "edit_message_id"} {
		if value, ok := msg.Payload[key].(string); ok && strings.TrimSpace(value) != "" {
			metadata[key] = strings.TrimSpace(value)
		}
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

	if err := c.HandleInboundContextWithAttachments(c.ctx, chatID, content, media, attachments, inboundCtx, sender); err != nil {
		c.releaseMessage(dedupeKey)
		errMsg := newErrorWithPayload("send_failed", err.Error(), map[string]any{"request_id": msg.ID})
		_ = pc.writeJSON(errMsg)
		return
	}
	c.writeMessageAck(pc, msg.ID, strings.TrimSpace(editID), "accepted")
}

func (c *PicoChannel) writeMessageAck(pc *picoConn, requestID, targetID, status string) {
	if requestID == "" || pc == nil || pc.conn == nil {
		return
	}
	ack := newMessage(TypeMessageAck, map[string]any{
		"request_id":        requestID,
		"target_message_id": targetID,
		"status":            status,
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
