package pico

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/identity"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
)

// PicoClientChannel connects to a remote Pico Protocol WebSocket server.
type PicoClientChannel struct {
	*channels.BaseChannel
	config  *config.PicoClientSettings
	conn    *picoConn
	pending map[picoClientRequest]chan PicoMessage
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
}

type picoClientRequest struct {
	conn *picoConn
	id   string
}

const picoClientReceiptTimeout = 5 * time.Second

// NewPicoClientChannel creates a new Pico Protocol client channel.
func NewPicoClientChannel(
	bc *config.Channel,
	cfg *config.PicoClientSettings,
	messageBus *bus.MessageBus,
) (*PicoClientChannel, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("pico_client url is required")
	}

	base := channels.NewBaseChannel("pico_client", cfg, messageBus, bc.AllowFrom)

	return &PicoClientChannel{
		BaseChannel: base,
		config:      cfg,
		pending:     make(map[picoClientRequest]chan PicoMessage),
	}, nil
}

// Start dials the remote server and begins reading.
func (c *PicoClientChannel) Start(ctx context.Context) error {
	logger.InfoC("pico_client", "Starting Pico Client channel")
	c.ctx, c.cancel = context.WithCancel(ctx)

	if err := c.dial(); err != nil {
		c.cancel()
		return fmt.Errorf("pico_client initial connect: %w", err)
	}

	c.SetRunning(true)
	go c.reconnectLoop()

	logger.InfoCF("pico_client", "Connected", map[string]any{"url": c.config.URL})
	return nil
}

// Stop closes the connection.
func (c *PicoClientChannel) Stop(ctx context.Context) error {
	logger.InfoC("pico_client", "Stopping Pico Client channel")
	c.SetRunning(false)
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Lock()
	if c.conn != nil {
		c.conn.close()
	}
	c.mu.Unlock()
	logger.InfoC("pico_client", "Pico Client channel stopped")
	return nil
}

func (c *PicoClientChannel) dial() error {
	header := http.Header{}
	if c.config.Token.String() != "" {
		header.Set("Authorization", "Bearer "+c.config.Token.String())
	}

	ws, resp, err := websocket.DefaultDialer.DialContext(c.ctx, c.config.URL, header)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return err
	}

	connCtx, connCancel := context.WithCancel(c.ctx)

	pc := &picoConn{
		id:        uuid.New().String(),
		conn:      ws,
		sessionID: c.config.SessionID,
		cancel:    connCancel,
		done:      connCtx.Done(),
	}
	if pc.sessionID == "" {
		pc.sessionID = uuid.New().String()
	}

	c.mu.Lock()
	c.conn = pc
	c.mu.Unlock()

	go c.readLoop(connCtx, pc)
	return nil
}

// reconnectLoop re-dials when the connection drops.
func (c *PicoClientChannel) reconnectLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.mu.Lock()
		pc := c.conn
		c.mu.Unlock()

		if pc == nil || pc.closed.Load() {
			backoff := 5 * time.Second
			logger.InfoC("pico_client", "Reconnecting...")
			if err := c.dial(); err != nil {
				logger.WarnCF("pico_client", "Reconnect failed", map[string]any{
					"error": err.Error(),
				})
				select {
				case <-c.ctx.Done():
					return
				case <-time.After(backoff):
				}
				continue
			}
			logger.InfoC("pico_client", "Reconnected")
		}

		select {
		case <-c.ctx.Done():
			return
		case <-time.After(1 * time.Second):
		}
	}
}

func (c *PicoClientChannel) readLoop(connCtx context.Context, pc *picoConn) {
	defer pc.close()

	readTimeout := time.Duration(c.config.ReadTimeout) * time.Second
	if readTimeout <= 0 {
		readTimeout = 60 * time.Second
	}

	_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))
	pc.conn.SetReadLimit(maxPicoMessageBytes)
	pc.conn.SetPongHandler(func(string) error {
		return pc.conn.SetReadDeadline(time.Now().Add(readTimeout))
	})

	pingInterval := time.Duration(c.config.PingInterval) * time.Second
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}
	go c.pingLoop(connCtx, pc, pingInterval)

	for {
		select {
		case <-connCtx.Done():
			return
		default:
		}

		_, raw, err := pc.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(
				err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
			) {
				logger.DebugCF("pico_client", "Read error", map[string]any{
					"error": err.Error(),
				})
			}
			return
		}

		_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))

		var msg PicoMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		c.handleInbound(pc, msg)
	}
}

func (c *PicoClientChannel) pingLoop(connCtx context.Context, pc *picoConn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-connCtx.Done():
			return
		case <-ticker.C:
			if pc.closed.Load() {
				return
			}
			if err := pc.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(picoClientReceiptTimeout)); err != nil {
				return
			}
		}
	}
}

// handleInbound processes messages from the remote server.
// In client mode the server sends message.create (responses) and the client
// sends message.send (user input). We treat message.create from the server
// as inbound user messages to feed into the agent loop.
func (c *PicoClientChannel) handleInbound(pc *picoConn, msg PicoMessage) {
	switch msg.Type {
	case TypePong:
		// response to our ping, ignore
	case TypeMessageAck, TypeError:
		id, _ := msg.Payload["request_id"].(string)
		if id == "" && msg.Type == TypeMessageAck {
			id = msg.ID
		}
		if id != "" {
			c.mu.Lock()
			waiter := c.pending[picoClientRequest{conn: pc, id: id}]
			c.mu.Unlock()
			if waiter != nil {
				select {
				case waiter <- msg:
				default:
				}
			}
		}
	case TypeMessageCreate:
		// Server sent us a message — treat as inbound
		c.handleServerMessage(pc, msg)
	case TypeMessageUpdate:
		deliveryID, _ := msg.Payload[PayloadKeyDeliveryID].(string)
		draft, hasDraft := msg.Payload[PayloadKeyStreamDraft].(bool)
		if deliveryID != "" || (hasDraft && !draft) {
			c.handleServerMessage(pc, msg)
		}
	case TypeMediaCreate:
		c.handleServerMessage(pc, msg)
	case TypeDeliveryAck:
		// Receipt for our delivery.ack.
	default:
		logger.DebugCF("pico_client", "Ignoring message type", map[string]any{
			"type": msg.Type,
		})
	}
}

func (c *PicoClientChannel) handleServerMessage(pc *picoConn, msg PicoMessage) {
	draft, _ := msg.Payload[PayloadKeyStreamDraft].(bool)
	placeholder, _ := msg.Payload[PayloadKeyPlaceholder].(bool)
	if draft || placeholder || isThoughtPayload(msg.Payload) {
		return
	}

	content, _ := msg.Payload[PayloadKeyContent].(string)
	media, err := parseInlineImageMedia(msg.Payload)
	if err != nil {
		logger.WarnCF("pico_client", "Ignoring invalid media payload", map[string]any{
			"error": err.Error(),
		})
		if strings.TrimSpace(content) == "" {
			return
		}
		media = nil
	}
	if strings.TrimSpace(content) == "" && len(media) == 0 {
		return
	}

	sessionID := msg.SessionID
	if sessionID == "" {
		sessionID = pc.sessionID
	}

	chatID := "pico_client:" + sessionID
	senderID := "pico-remote"
	sender := bus.SenderInfo{
		Platform:    "pico_client",
		PlatformID:  senderID,
		CanonicalID: identity.BuildCanonicalID("pico_client", senderID),
	}

	if !c.IsAllowedSender(sender) {
		return
	}

	inboundCtx := bus.InboundContext{
		Channel:   "pico_client",
		ChatID:    chatID,
		ChatType:  "direct",
		SenderID:  senderID,
		MessageID: picoMessageID(msg),
		Raw: map[string]string{
			"platform":   "pico_client",
			"session_id": sessionID,
		},
	}

	if err := c.HandleInboundContext(c.ctx, chatID, content, media, inboundCtx, sender); err != nil {
		return
	}
	if deliveryID, _ := msg.Payload[PayloadKeyDeliveryID].(string); deliveryID != "" {
		ack := newMessage(TypeDeliveryAck, map[string]any{PayloadKeyDeliveryID: deliveryID})
		ack.SessionID = sessionID
		if err := pc.writeJSONBefore(ack, time.Now().Add(picoClientReceiptTimeout)); err != nil {
			logger.WarnCF("pico_client", "Failed to acknowledge inbound message", map[string]any{"delivery_id": deliveryID})
		}
	}
}

func picoMessageID(msg PicoMessage) string {
	if id := strings.TrimSpace(msg.ID); id != "" {
		return id
	}
	if id, ok := msg.Payload["message_id"].(string); ok {
		return strings.TrimSpace(id)
	}
	return ""
}

// Send sends a message to the remote server.
func (c *PicoClientChannel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}
	if strings.TrimSpace(msg.Content) == "" {
		return nil, fmt.Errorf("pico client message is empty: %w", channels.ErrSendFailed)
	}
	msg = bus.NormalizeOutboundMessage(msg)
	outMsg := newMessage(TypeMessageSend, map[string]any{
		PayloadKeyContent: msg.Content,
	})
	outMsg.ID = msg.ID
	outMsg.SessionID = strings.TrimPrefix(msg.ChatID, "pico_client:")

	c.mu.Lock()
	pc := c.conn
	if pc == nil || pc.closed.Load() {
		c.mu.Unlock()
		return nil, fmt.Errorf("pico client disconnected: %w", channels.ErrTemporary)
	}
	request := picoClientRequest{conn: pc, id: outMsg.ID}
	if c.pending[request] != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("pico client message already awaiting receipt: %w", channels.ErrDeliveryUnknown)
	}
	receipt := make(chan PicoMessage, 1)
	c.pending[request] = receipt
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, request)
		c.mu.Unlock()
	}()

	sendCtx, cancel := context.WithTimeout(ctx, picoClientReceiptTimeout)
	defer cancel()
	if err := sendCtx.Err(); err != nil {
		return nil, fmt.Errorf("pico client send canceled before write: %w", channels.ErrTemporary)
	}
	deadline, _ := sendCtx.Deadline()
	if err := pc.writeJSONBefore(outMsg, deadline); err != nil {
		pc.close()
		return nil, fmt.Errorf("pico client write: %w", channels.ErrDeliveryUnknown)
	}

	var ack PicoMessage
	select {
	case ack = <-receipt:
	case <-sendCtx.Done():
	case <-pc.done:
	}
	if ack.Type == "" {
		// The reader may have seen the receipt immediately before closing.
		select {
		case ack = <-receipt:
		default:
		}
	}
	if ack.Type == TypeMessageAck {
		switch ack.Payload["status"] {
		case "accepted", "duplicate":
			return []string{outMsg.ID}, nil
		}
	}
	if ack.Type == TypeError {
		switch ack.Payload["code"] {
		case "empty_content", "invalid_media", "session_busy", "sender_not_allowed":
			return nil, fmt.Errorf("pico client request rejected (%s): %w", ack.Payload["code"], channels.ErrSendFailed)
		}
	}
	return nil, fmt.Errorf("pico client acceptance unconfirmed: %w", channels.ErrDeliveryUnknown)
}

// StartTyping implements channels.TypingCapable.
func (c *PicoClientChannel) StartTyping(ctx context.Context, chatID string) (func(), error) {
	c.mu.Lock()
	pc := c.conn
	c.mu.Unlock()
	if pc == nil || pc.closed.Load() {
		return func() {}, nil
	}

	startMsg := newMessage(TypeTypingStart, nil)
	startMsg.SessionID = strings.TrimPrefix(chatID, "pico_client:")
	if err := pc.writeJSON(startMsg); err != nil {
		return func() {}, err
	}
	return func() {
		c.mu.Lock()
		currentPC := c.conn
		c.mu.Unlock()
		if currentPC == nil {
			return
		}
		stopMsg := newMessage(TypeTypingStop, nil)
		stopMsg.SessionID = strings.TrimPrefix(chatID, "pico_client:")
		currentPC.writeJSON(stopMsg)
	}, nil
}
