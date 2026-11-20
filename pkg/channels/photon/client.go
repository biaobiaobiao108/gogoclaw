package photon

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	photonv1 "github.com/biaobiaobiao108/gogoclaw/pkg/channels/photon/internal/photon/imessage/v1"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
)

const (
	defaultSpectrumCloudURL    = "https://spectrum.photon.codes"
	defaultSharedGRPCAddress   = "imessage.spectrum.photon.codes:443"
	photonRPCTimeout           = 20 * time.Second
	photonTransferTimeout      = 2 * time.Minute
	photonMaxGRPCMessageSize   = 100 << 20
	photonMaxUploadBytes       = 100 << 20
	photonMaxDownloadBytes     = 100 << 20
	photonTokenRefreshLead     = 30 * time.Second
	photonTokenRetryDelay      = 30 * time.Second
	photonSharedLineID         = "shared"
	photonKeepaliveTime        = 30 * time.Second
	photonKeepaliveTimeout     = 20 * time.Second
	photonTokenResponseMaxSize = 1 << 20
)

type photonLine struct {
	ID      string
	Phone   string
	Address string
}

type cloudTokenData struct {
	Type      string             `json:"type"`
	Token     string             `json:"token"`
	ExpiresIn int                `json:"expiresIn"`
	Auth      map[string]string  `json:"auth"`
	Numbers   map[string]*string `json:"numbers"`
}

type cloudEnvelope struct {
	Succeed bool            `json:"succeed"`
	Data    json.RawMessage `json:"data"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
}

type photonLineClient struct {
	line        photonLine
	conn        *grpc.ClientConn
	messages    photonv1.MessageServiceClient
	chats       photonv1.ChatServiceClient
	attachments photonv1.AttachmentServiceClient
	events      photonv1.EventServiceClient
}

func (c *photonLineClient) close() {
	if c != nil && c.conn != nil {
		_ = c.conn.Close()
	}
}

type photonClient struct {
	settings *config.PhotonSettings
	http     *http.Client

	tokenMu     sync.Mutex
	tokens      *cloudTokenData
	expiresAt   time.Time
	refreshWake chan struct{}
	refreshCtx  context.Context
	refreshStop context.CancelFunc

	linesMu   sync.RWMutex
	lines     map[string]*photonLineClient
	chatLines map[string]string
	state     *photonStateStore
}

func (c *photonClient) setStateStore(state *photonStateStore) {
	c.linesMu.Lock()
	c.state = state
	c.linesMu.Unlock()
}

func newPhotonClient(settings *config.PhotonSettings) *photonClient {
	return &photonClient{
		settings:    settings,
		http:        &http.Client{Timeout: photonRPCTimeout},
		refreshWake: make(chan struct{}, 1),
		lines:       make(map[string]*photonLineClient),
		chatLines:   make(map[string]string),
	}
}

func (c *photonClient) Start(ctx context.Context) error {
	c.tokenMu.Lock()
	err := c.refreshTokensLocked(ctx)
	c.tokenMu.Unlock()
	if err != nil {
		return err
	}

	c.refreshCtx, c.refreshStop = context.WithCancel(ctx)
	go c.runTokenRefresh()
	return nil
}

func (c *photonClient) Close() {
	if c.refreshStop != nil {
		c.refreshStop()
	}
	c.linesMu.Lock()
	for id, line := range c.lines {
		line.close()
		delete(c.lines, id)
	}
	c.linesMu.Unlock()
}

func (c *photonClient) Lines() []photonLine {
	c.linesMu.RLock()
	defer c.linesMu.RUnlock()
	lines := make([]photonLine, 0, len(c.lines))
	for _, line := range c.lines {
		lines = append(lines, line.line)
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].ID < lines[j].ID })
	return lines
}

func (c *photonClient) ResolveLine(chatID string) (photonLine, error) {
	line, err := c.lineClientForChat(chatID)
	if err != nil {
		return photonLine{}, err
	}
	return line.line, nil
}

func (c *photonClient) lineClientForChat(chatID string) (*photonLineClient, error) {
	chatID = strings.TrimSpace(chatID)
	c.linesMu.Lock()
	if c.state != nil && chatID != "" {
		if lineID := c.state.ChatLine(chatID); lineID != "" {
			c.chatLines[chatID] = lineID
		}
	}
	c.linesMu.Unlock()

	c.linesMu.RLock()
	defer c.linesMu.RUnlock()
	if len(c.lines) == 0 {
		return nil, errors.New("photon: no iMessage lines are available")
	}
	if lineID := c.chatLines[chatID]; lineID != "" {
		if line := c.lines[lineID]; line != nil {
			return line, nil
		}
	}
	address := directAddressFromChatID(chatID)
	for _, line := range c.lines {
		if address != "" && strings.EqualFold(address, line.line.Phone) {
			return line, nil
		}
	}
	if len(c.lines) == 1 {
		for _, line := range c.lines {
			return line, nil
		}
	}
	ids := make([]string, 0, len(c.lines))
	for id := range c.lines {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return c.lines[ids[0]], nil
}

func (c *photonClient) RememberChatLine(chatID, lineID string) {
	chatID = strings.TrimSpace(chatID)
	lineID = strings.TrimSpace(lineID)
	if chatID == "" || lineID == "" {
		return
	}
	c.linesMu.Lock()
	c.chatLines[chatID] = lineID
	state := c.state
	c.linesMu.Unlock()
	if state != nil {
		if err := state.SetChatLine(chatID, lineID); err != nil {
			logger.WarnCF("photon", "Failed to persist iMessage chat line", map[string]any{
				"chat_id": chatID,
				"line_id": lineID,
				"error":   err.Error(),
			})
		}
	}
}

func (c *photonClient) lineByID(lineID string) (*photonLineClient, error) {
	c.linesMu.RLock()
	line := c.lines[lineID]
	c.linesMu.RUnlock()
	if line == nil {
		return nil, fmt.Errorf("photon: iMessage line %q is not available", lineID)
	}
	return line, nil
}

func (c *photonClient) runTokenRefresh() {
	delay := photonTokenRetryDelay
	c.tokenMu.Lock()
	if !c.expiresAt.IsZero() {
		delay = max(time.Until(c.expiresAt.Add(-photonTokenRefreshLead)), 5*time.Second)
	}
	c.tokenMu.Unlock()

	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-c.refreshCtx.Done():
			return
		case <-c.refreshWake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(0)
		case <-timer.C:
		}

		refreshCtx, cancel := context.WithTimeout(c.refreshCtx, photonRPCTimeout)
		c.tokenMu.Lock()
		err := c.refreshTokensLocked(refreshCtx)
		c.tokenMu.Unlock()
		cancel()

		if err != nil {
			logger.WarnCF("photon", "Failed to refresh iMessage token", map[string]any{"error": err.Error()})
			delay = photonTokenRetryDelay
		} else {
			c.tokenMu.Lock()
			delay = max(time.Until(c.expiresAt.Add(-photonTokenRefreshLead)), 5*time.Second)
			c.tokenMu.Unlock()
		}
		timer.Reset(delay)
	}
}

func (c *photonClient) refreshTokensLocked(ctx context.Context) error {
	tokens, err := c.issueTokens(ctx)
	if err != nil {
		return err
	}
	if tokens.ExpiresIn <= 0 {
		return errors.New("photon: token response has no lifetime")
	}
	if err := c.reconcileLines(tokens); err != nil {
		return err
	}
	c.tokens = tokens
	c.expiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	return nil
}

func (c *photonClient) issueTokens(ctx context.Context) (*cloudTokenData, error) {
	baseURL := strings.TrimSpace(c.settings.CloudURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("SPECTRUM_CLOUD_URL"))
	}
	if baseURL == "" {
		baseURL = defaultSpectrumCloudURL
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/projects/" + c.settings.ProjectID + "/imessage/tokens"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("photon: build token request: %w", err)
	}
	req.SetBasicAuth(c.settings.ProjectID, c.settings.ProjectSecret.String())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("photon: issue iMessage token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, photonTokenResponseMaxSize))
	if err != nil {
		return nil, fmt.Errorf("photon: read token response: %w", err)
	}

	var envelope cloudEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("photon: token HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("photon: decode token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !envelope.Succeed {
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = strings.TrimSpace(string(body))
		}
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("photon: token request failed: %s", message)
	}
	var tokens cloudTokenData
	if err := json.Unmarshal(envelope.Data, &tokens); err != nil {
		return nil, fmt.Errorf("photon: decode iMessage token data: %w", err)
	}
	switch tokens.Type {
	case "shared":
		if strings.TrimSpace(tokens.Token) == "" {
			return nil, errors.New("photon: shared token is empty")
		}
	case "dedicated":
		if len(tokens.Auth) == 0 {
			return nil, errors.New("photon: dedicated token response has no lines")
		}
	default:
		return nil, fmt.Errorf("photon: unsupported token type %q", tokens.Type)
	}
	return &tokens, nil
}

func (c *photonClient) reconcileLines(tokens *cloudTokenData) error {
	desired := make(map[string]photonLine)
	if tokens.Type == "shared" {
		address := strings.TrimSpace(os.Getenv("SPECTRUM_IMESSAGE_ADDRESS"))
		if address == "" {
			address = defaultSharedGRPCAddress
		}
		desired[photonSharedLineID] = photonLine{ID: photonSharedLineID, Address: address}
	} else {
		for instanceID := range tokens.Auth {
			phone := ""
			if tokens.Numbers != nil && tokens.Numbers[instanceID] != nil {
				phone = strings.TrimSpace(*tokens.Numbers[instanceID])
			}
			if phone == "" {
				logger.WarnCF("photon", "Skipping iMessage line without an assigned number", map[string]any{
					"instance_id": instanceID,
				})
				continue
			}
			desired[instanceID] = photonLine{
				ID:      instanceID,
				Phone:   phone,
				Address: instanceID + ".imsg.photon.codes:443",
			}
		}
	}
	if len(desired) == 0 {
		return errors.New("photon: project has no usable iMessage lines")
	}

	c.linesMu.Lock()
	defer c.linesMu.Unlock()
	for id, line := range c.lines {
		if _, keep := desired[id]; keep {
			continue
		}
		line.close()
		delete(c.lines, id)
	}
	for id, line := range desired {
		existing := c.lines[id]
		if existing != nil {
			existing.line = line
			continue
		}
		created, err := c.newLineClient(line)
		if err != nil {
			return err
		}
		c.lines[id] = created
	}
	return nil
}

func (c *photonClient) newLineClient(line photonLine) (*photonLineClient, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	conn, err := grpc.NewClient(
		line.Address,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                photonKeepaliveTime,
			Timeout:             photonKeepaliveTimeout,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(photonMaxGRPCMessageSize),
			grpc.MaxCallSendMsgSize(photonMaxGRPCMessageSize),
		),
		grpc.WithUnaryInterceptor(c.unaryAuthInterceptor(line.ID)),
		grpc.WithStreamInterceptor(c.streamAuthInterceptor(line.ID)),
		grpc.WithUserAgent("gogoclaw-photon/1"),
	)
	if err != nil {
		return nil, fmt.Errorf("photon: create gRPC client for %s: %w", line.ID, err)
	}
	return &photonLineClient{
		line:        line,
		conn:        conn,
		messages:    photonv1.NewMessageServiceClient(conn),
		chats:       photonv1.NewChatServiceClient(conn),
		attachments: photonv1.NewAttachmentServiceClient(conn),
		events:      photonv1.NewEventServiceClient(conn),
	}, nil
}

func (c *photonClient) unaryAuthInterceptor(lineID string) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req any,
		reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		callCtx := ctx
		cancel := func() {}
		if _, ok := callCtx.Deadline(); !ok {
			callCtx, cancel = context.WithTimeout(callCtx, photonRPCTimeout)
		}
		defer cancel()

		token, err := c.tokenForLine(callCtx, lineID)
		if err != nil {
			return err
		}
		callCtx = metadata.AppendToOutgoingContext(callCtx, "authorization", "Bearer "+token)
		if isMutatingRPCMethod(method) {
			callCtx = metadata.AppendToOutgoingContext(callCtx, "x-idempotency-key", idempotencyKey(req))
		}
		err = invoker(callCtx, method, req, reply, cc, opts...)
		if isAuthRPCError(err) {
			c.invalidateToken()
		}
		return err
	}
}

func (c *photonClient) streamAuthInterceptor(lineID string) grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		token, err := c.tokenForLine(ctx, lineID)
		if err != nil {
			return nil, err
		}
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		stream, err := streamer(ctx, desc, cc, method, opts...)
		if isAuthRPCError(err) {
			c.invalidateToken()
		}
		return stream, err
	}
}

func (c *photonClient) tokenForLine(ctx context.Context, lineID string) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.tokens == nil || time.Now().After(c.expiresAt.Add(-photonTokenRefreshLead)) {
		if err := c.refreshTokensLocked(ctx); err != nil {
			return "", err
		}
	}
	if lineID == photonSharedLineID && c.tokens.Type == "shared" {
		return c.tokens.Token, nil
	}
	if token := c.tokens.Auth[lineID]; token != "" {
		return token, nil
	}
	return "", fmt.Errorf("photon: no token for iMessage line %q", lineID)
}

func (c *photonClient) invalidateToken() {
	c.tokenMu.Lock()
	c.expiresAt = time.Time{}
	c.tokenMu.Unlock()
	select {
	case c.refreshWake <- struct{}{}:
	default:
	}
}

func (c *photonClient) CatchUp(
	ctx context.Context,
	lineID string,
	after uint64,
	handle func(uint64, *photonv1.MessageChangeEvent) error,
) (uint64, error) {
	line, err := c.lineByID(lineID)
	if err != nil {
		return after, err
	}
	req := &photonv1.CatchUpEventsRequest{}
	if after > 0 {
		req.AfterSequence = &after
	}
	stream, err := line.events.CatchUpEvents(ctx, req)
	if err != nil {
		return after, err
	}
	highest := after
	for {
		frame, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return highest, nil
		}
		if recvErr != nil {
			if isAuthRPCError(recvErr) {
				c.invalidateToken()
			}
			return highest, recvErr
		}
		if complete := frame.GetComplete(); complete != nil {
			if complete.HeadSequence > highest {
				highest = complete.HeadSequence
			}
			return highest, nil
		}
		sequence := frame.GetSequence()
		if sequence > highest {
			highest = sequence
		}
		if event := frame.GetMessageChanged(); event != nil && sequence > after {
			if err := handle(sequence, event); err != nil {
				return highest, err
			}
		}
	}
}

func (c *photonClient) SubscribeMessages(
	ctx context.Context,
	lineID string,
	after uint64,
	handle func(uint64, *photonv1.MessageChangeEvent) error,
) error {
	line, err := c.lineByID(lineID)
	if err != nil {
		return err
	}
	stream, err := line.messages.SubscribeMessageEvents(ctx, &photonv1.SubscribeMessageEventsRequest{})
	if err != nil {
		return err
	}
	for {
		frame, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return nil
		}
		if recvErr != nil {
			if isAuthRPCError(recvErr) {
				c.invalidateToken()
			}
			return recvErr
		}
		sequence := frame.GetSequence()
		event := frame.GetMessageChanged()
		if event == nil || sequence == 0 || sequence <= after {
			continue
		}
		if err := handle(sequence, event); err != nil {
			return err
		}
		after = sequence
	}
}

func (c *photonClient) SendText(
	ctx context.Context,
	chatID, text, replyTo, clientMessageID string,
) (string, error) {
	line, err := c.lineClientForChat(chatID)
	if err != nil {
		return "", err
	}
	req := &photonv1.SendTextMessageRequest{
		ChatGuid: normalizeChatID(chatID),
		Text:     text,
	}
	if strings.TrimSpace(replyTo) != "" {
		req.ReplyTo = &photonv1.ReplyTarget{MessageGuid: strings.TrimSpace(replyTo)}
	}
	if strings.TrimSpace(clientMessageID) != "" {
		req.ClientMessageId = stringPointer(strings.TrimSpace(clientMessageID))
	}
	resp, err := line.messages.SendTextMessage(ctx, req)
	if err != nil {
		return "", err
	}
	return messageIDFromResponse(resp), nil
}

func (c *photonClient) UploadAttachment(ctx context.Context, lineID, path, name string) (string, error) {
	line, err := c.lineByID(lineID)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > photonMaxUploadBytes {
		return "", fmt.Errorf("photon: attachment is too large: %d bytes", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	uploadCtx, cancel := context.WithTimeout(ctx, photonTransferTimeout)
	defer cancel()
	resp, err := line.attachments.UploadAttachment(uploadCtx, &photonv1.UploadAttachmentRequest{
		FileName: name,
		Data:     data,
	})
	if err != nil {
		return "", err
	}
	if resp.GetAttachment() == nil || strings.TrimSpace(resp.GetAttachment().GetGuid()) == "" {
		return "", errors.New("photon: upload response has no attachment GUID")
	}
	return resp.GetAttachment().GetGuid(), nil
}

func (c *photonClient) SendAttachment(
	ctx context.Context,
	chatID, attachmentGUID, name, replyTo, clientMessageID string,
	isAudio bool,
) (string, error) {
	line, err := c.lineClientForChat(chatID)
	if err != nil {
		return "", err
	}
	req := &photonv1.SendAttachmentMessageRequest{
		ChatGuid: normalizeChatID(chatID),
		Attachment: &photonv1.AttachmentRef{
			Source: &photonv1.AttachmentRef_AttachmentGuid{AttachmentGuid: attachmentGUID},
		},
	}
	if strings.TrimSpace(name) != "" {
		req.Attachment.AttachmentName = stringPointer(name)
	}
	if isAudio {
		req.IsAudioMessage = boolPointer(true)
	}
	if strings.TrimSpace(replyTo) != "" {
		req.ReplyTo = &photonv1.ReplyTarget{MessageGuid: strings.TrimSpace(replyTo)}
	}
	if strings.TrimSpace(clientMessageID) != "" {
		req.ClientMessageId = stringPointer(strings.TrimSpace(clientMessageID))
	}
	resp, err := line.messages.SendAttachmentMessage(ctx, req)
	if err != nil {
		return "", err
	}
	return messageIDFromResponse(resp), nil
}

func (c *photonClient) EditMessage(ctx context.Context, chatID, messageID, text string) error {
	line, err := c.lineClientForChat(chatID)
	if err != nil {
		return err
	}
	_, err = line.messages.EditMessage(ctx, &photonv1.EditMessageRequest{
		Target:  &photonv1.MessageTarget{ChatGuid: normalizeChatID(chatID), MessageGuid: messageID},
		NewText: text,
	})
	return err
}

func (c *photonClient) UnsendMessage(ctx context.Context, chatID, messageID string) error {
	line, err := c.lineClientForChat(chatID)
	if err != nil {
		return err
	}
	_, err = line.messages.UnsendMessage(ctx, &photonv1.UnsendMessageRequest{
		Target: &photonv1.MessageTarget{ChatGuid: normalizeChatID(chatID), MessageGuid: messageID},
	})
	return err
}

func (c *photonClient) SetReaction(ctx context.Context, chatID, messageID, emoji string, isSet bool) error {
	line, err := c.lineClientForChat(chatID)
	if err != nil {
		return err
	}
	_, err = line.messages.SetReaction(ctx, &photonv1.SetReactionRequest{
		Target: &photonv1.MessageTarget{ChatGuid: normalizeChatID(chatID), MessageGuid: messageID},
		Reaction: &photonv1.MessageReaction{
			Kind:  photonv1.MessageReactionKind_MESSAGE_REACTION_KIND_EMOJI,
			Emoji: stringPointer(emoji),
		},
		IsSet: isSet,
	})
	return err
}

func (c *photonClient) SetTyping(ctx context.Context, chatID string, typing bool) error {
	line, err := c.lineClientForChat(chatID)
	if err != nil {
		return err
	}
	_, err = line.chats.SetTyping(ctx, &photonv1.SetTypingRequest{
		ChatGuid: normalizeChatID(chatID),
		IsTyping: typing,
	})
	return err
}

func (c *photonClient) MarkRead(ctx context.Context, chatID string) error {
	line, err := c.lineClientForChat(chatID)
	if err != nil {
		return err
	}
	_, err = line.chats.MarkChatRead(ctx, &photonv1.MarkChatReadRequest{
		ChatGuid: normalizeChatID(chatID),
	})
	return err
}

func (c *photonClient) DownloadAttachment(
	ctx context.Context,
	lineID, attachmentGUID, tempDir string,
) (string, *photonv1.AttachmentInfo, error) {
	line, err := c.lineByID(lineID)
	if err != nil {
		return "", nil, err
	}
	downloadCtx, cancel := context.WithTimeout(ctx, photonTransferTimeout)
	defer cancel()
	stream, err := line.attachments.DownloadAttachment(downloadCtx, &photonv1.DownloadAttachmentRequest{
		AttachmentGuid: attachmentGUID,
	})
	if err != nil {
		return "", nil, err
	}
	first, err := stream.Recv()
	if err != nil {
		return "", nil, err
	}
	header := first.GetHeader()
	if header == nil || header.GetAttachment() == nil {
		return "", nil, errors.New("photon: attachment stream did not start with a header")
	}
	attachment := header.GetAttachment()
	if attachment.GetTotalBytes() > photonMaxDownloadBytes {
		return "", nil, fmt.Errorf("photon: attachment is too large: %d bytes", attachment.GetTotalBytes())
	}
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return "", nil, err
	}
	ext := filepath.Ext(filepath.Base(attachment.GetFileName()))
	if len(ext) > 16 || strings.ContainsAny(ext, `\/:*?"<>|`) {
		ext = ""
	}
	file, err := os.CreateTemp(tempDir, "photon-*"+ext)
	if err != nil {
		return "", nil, err
	}
	path := file.Name()
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = os.Remove(path)
		}
	}()

	var written int64
	for {
		frame, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			if isAuthRPCError(recvErr) {
				c.invalidateToken()
			}
			return "", nil, recvErr
		}
		chunk := frame.GetPrimaryChunk()
		if len(chunk) == 0 {
			continue
		}
		written += int64(len(chunk))
		if written > photonMaxDownloadBytes {
			return "", nil, errors.New("photon: attachment download exceeded size limit")
		}
		if _, err := file.Write(chunk); err != nil {
			return "", nil, err
		}
	}
	if err := file.Close(); err != nil {
		return "", nil, err
	}
	cleanup = false
	return path, attachment, nil
}

func (c *photonClient) MediaTempDir() string {
	return media.TempDir()
}

func normalizeChatID(chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if strings.HasPrefix(chatID, "any;-;") || strings.HasPrefix(chatID, "any;+;") {
		return chatID
	}
	if strings.HasPrefix(chatID, "iMessage;-;") {
		return "any;-;" + strings.TrimPrefix(chatID, "iMessage;-;")
	}
	if strings.HasPrefix(chatID, "iMessage;+;") {
		return "any;+;" + strings.TrimPrefix(chatID, "iMessage;+;")
	}
	if strings.HasPrefix(chatID, "SMS;-;") || strings.HasPrefix(chatID, "RCS;-;") {
		if index := strings.Index(chatID, ";-;"); index >= 0 {
			return "any;-;" + chatID[index+3:]
		}
	}
	if strings.Contains(chatID, ";+;") {
		if index := strings.Index(chatID, ";+;"); index >= 0 {
			return "any;+;" + chatID[index+3:]
		}
	}
	return "any;-;" + chatID
}

func directAddressFromChatID(chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if index := strings.Index(chatID, ";-;"); index >= 0 {
		return chatID[index+3:]
	}
	if !strings.Contains(chatID, ";") {
		return chatID
	}
	return ""
}

func isGroupChatID(chatID string) bool {
	return strings.Contains(strings.TrimSpace(chatID), ";+;")
}

func isMutatingRPCMethod(method string) bool {
	if strings.Contains(method, "Download") || strings.Contains(method, "Subscribe") || strings.Contains(method, "CatchUp") {
		return false
	}
	lastSlash := strings.LastIndexByte(method, '/')
	name := method
	if lastSlash >= 0 {
		name = method[lastSlash+1:]
	}
	return !strings.HasPrefix(name, "Get") && !strings.HasPrefix(name, "List") && !strings.HasPrefix(name, "Has") && !strings.HasPrefix(name, "Watch")
}

func idempotencyKey(req any) string {
	message, ok := req.(proto.Message)
	if !ok {
		return uuid.NewString()
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return uuid.NewString()
	}
	digest := sha256.Sum256(data)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(hex.EncodeToString(digest[:]))).String()
}

func isAuthRPCError(err error) bool {
	code := status.Code(err)
	return code == codes.Unauthenticated || code == codes.PermissionDenied
}

func messageIDFromResponse(resp *photonv1.MessageResponse) string {
	if resp == nil || resp.GetMessage() == nil {
		return ""
	}
	return resp.GetMessage().GetGuid()
}

func boolPointer(value bool) *bool {
	return &value
}

func stringPointer(value string) *string {
	return &value
}
