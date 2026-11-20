package photon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	photonv1 "github.com/biaobiaobiao108/gogoclaw/pkg/channels/photon/internal/photon/imessage/v1"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
)

const (
	photonMaxMessageLength = 8000
	photonTypingDuration   = 5 * time.Minute
)

type photonAPI interface {
	Start(context.Context) error
	Close()
	Lines() []photonLine
	ResolveLine(string) (photonLine, error)
	RememberChatLine(string, string)
	CatchUp(context.Context, string, uint64, func(uint64, *photonv1.MessageChangeEvent) error) (uint64, error)
	SubscribeMessages(context.Context, string, uint64, func(uint64, *photonv1.MessageChangeEvent) error) error
	SendText(context.Context, string, string, string, string) (string, error)
	UploadAttachment(context.Context, string, string, string) (string, error)
	SendAttachment(context.Context, string, string, string, string, string, bool) (string, error)
	EditMessage(context.Context, string, string, string) error
	UnsendMessage(context.Context, string, string) error
	SetReaction(context.Context, string, string, string, bool) error
	SetTyping(context.Context, string, bool) error
	MarkRead(context.Context, string) error
	DownloadAttachment(context.Context, string, string, string) (string, *photonv1.AttachmentInfo, error)
}

// PhotonChannel connects GoGoClaw to iMessage through Photon Spectrum's public
// gRPC API.
type PhotonChannel struct {
	*channels.BaseChannel
	bc       *config.Channel
	settings *config.PhotonSettings
	api      photonAPI
	progress *channels.ToolFeedbackAnimator
	state    *photonStateStore

	maxMediaBytes int64

	mentionNames []string
	ctx          context.Context
	cancel       context.CancelFunc
}

func init() {
	channels.RegisterFactory(
		config.ChannelPhoton,
		func(channelName, _ string, cfg *config.Config, messageBus *bus.MessageBus) (channels.Channel, error) {
			bc := cfg.Channels[channelName]
			if bc == nil {
				return nil, fmt.Errorf("photon channel %q: config not found", channelName)
			}
			decoded, err := bc.GetDecoded()
			if err != nil {
				return nil, fmt.Errorf("photon channel %q: decode settings: %w", channelName, err)
			}
			settings, ok := decoded.(*config.PhotonSettings)
			if !ok {
				return nil, fmt.Errorf("photon channel %q: invalid settings type %T", channelName, decoded)
			}
			statePath := filepath.Join(cfg.WorkspacePath(), "sessions", "photon-state.json")
			client := newPhotonClient(settings)
			ch, err := NewPhotonChannel(channelName, bc, settings, cfg, messageBus, client, statePath)
			if err != nil {
				return nil, err
			}
			client.setStateStore(ch.state)
			return ch, nil
		},
	)
}

func NewPhotonChannel(
	channelName string,
	bc *config.Channel,
	settings *config.PhotonSettings,
	cfg *config.Config,
	messageBus *bus.MessageBus,
	api photonAPI,
	statePath string,
) (*PhotonChannel, error) {
	if bc == nil || settings == nil || api == nil {
		return nil, fmt.Errorf("photon channel: config is required")
	}
	if strings.TrimSpace(settings.ProjectID) == "" || strings.TrimSpace(settings.ProjectSecret.String()) == "" {
		return nil, fmt.Errorf("photon channel: project_id and project_secret are required")
	}
	base := channels.NewBaseChannel(
		channelName,
		settings,
		messageBus,
		bc.AllowFrom,
		channels.WithMaxMessageLength(photonMaxMessageLength),
		channels.WithGroupTrigger(bc.GroupTrigger),
		channels.WithReasoningChannelID(bc.ReasoningChannelID),
	)
	ch := &PhotonChannel{
		BaseChannel:  base,
		bc:           bc,
		settings:     settings,
		api:          api,
		state:        newPhotonStateStore(statePath),
		mentionNames: photonMentionNames(cfg),
	}
	if cfg != nil {
		ch.maxMediaBytes = int64(cfg.Agents.Defaults.GetMaxMediaSize())
	}
	ch.progress = channels.NewStaticToolFeedbackAnimator(ch.EditMessage)
	ch.SetOwner(ch)
	return ch, nil
}

func photonMentionNames(cfg *config.Config) []string {
	seen := map[string]struct{}{}
	var names []string
	add := func(name string) {
		name = strings.TrimSpace(strings.TrimPrefix(name, "@"))
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	add("GoGoClaw")
	if cfg != nil {
		for _, agent := range cfg.Agents.List {
			add(agent.Name)
		}
	}
	return names
}

func (c *PhotonChannel) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	if err := c.api.Start(runCtx); err != nil {
		cancel()
		return err
	}
	c.ctx = runCtx
	c.cancel = cancel
	c.SetRunning(true)
	go c.runInbound(runCtx)
	logger.InfoCF("photon", "Photon iMessage channel started", map[string]any{
		"project_id": c.settings.ProjectID,
		"lines":      len(c.api.Lines()),
	})
	return nil
}

func (c *PhotonChannel) Stop(ctx context.Context) error {
	if !c.IsRunning() {
		return nil
	}
	c.SetRunning(false)
	if c.progress != nil {
		c.progress.StopAll()
	}
	if c.cancel != nil {
		c.cancel()
	}
	c.api.Close()
	logger.InfoC("photon", "Photon iMessage channel stopped")
	return nil
}

func (c *PhotonChannel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}
	if strings.TrimSpace(msg.ChatID) == "" {
		return nil, fmt.Errorf("photon: chat id is empty: %w", channels.ErrSendFailed)
	}
	if msg.Content == "" {
		return nil, nil
	}

	if outboundMessageIsToolFeedback(msg) {
		if msgID, handled, err := c.progress.Update(ctx, msg.ChatID, msg.Content); handled {
			if err != nil {
				return nil, classifyPhotonSendError("update tool feedback", err)
			}
			return []string{msgID}, nil
		}
	} else if msgIDs, handled := c.finalizeToolFeedbackMessage(ctx, msg.ChatID, msg.Content); handled {
		return msgIDs, nil
	}

	messageID, err := c.sendText(ctx, msg.ChatID, msg.Content, msg.ReplyToMessageID, msg.ID)
	if err != nil {
		return nil, classifyPhotonSendError("send text", err)
	}
	if messageID == "" {
		return nil, nil
	}
	return []string{messageID}, nil
}

func (c *PhotonChannel) sendText(ctx context.Context, chatID, content, replyTo, requestID string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", nil
	}
	return c.api.SendText(ctx, chatID, content, replyTo, requestID)
}

func (c *PhotonChannel) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}
	store := c.GetMediaStore()
	if store == nil {
		return nil, fmt.Errorf("photon: no media store available: %w", channels.ErrSendFailed)
	}
	line, err := c.api.ResolveLine(msg.ChatID)
	if err != nil {
		return nil, classifyPhotonSendError("resolve media line", err)
	}

	var messageIDs []string
	sent := false
	caption := firstMediaCaption(msg.Parts)
	captionSent := false
	captionAttempted := false
	for index, part := range msg.Parts {
		localPath, resolveErr := store.Resolve(part.Ref)
		if resolveErr != nil {
			logger.WarnCF("photon", "Unable to resolve outbound media", map[string]any{
				"ref":   part.Ref,
				"error": resolveErr.Error(),
			})
			continue
		}
		filename := strings.TrimSpace(part.Filename)
		if filename == "" {
			filename = filepath.Base(localPath)
		}
		if info, statErr := os.Stat(localPath); statErr != nil {
			return messageIDs, fmt.Errorf("photon inspect media: %w: %v", channels.ErrSendFailed, statErr)
		} else if c.maxMediaBytes > 0 && info.Size() > c.maxMediaBytes {
			return messageIDs, fmt.Errorf("photon: media exceeds the configured %d-byte limit: %w", c.maxMediaBytes, channels.ErrSendFailed)
		}
		attachmentGUID, uploadErr := c.api.UploadAttachment(ctx, line.ID, localPath, filename)
		if uploadErr != nil {
			return messageIDs, classifyPhotonSendError("upload media", uploadErr)
		}
		requestID := channels.StableID(msg.ID, "photon", "part", fmt.Sprintf("%d", index))
		messageID, sendErr := c.api.SendAttachment(
			ctx,
			msg.ChatID,
			attachmentGUID,
			filename,
			msg.Context.ReplyToMessageID,
			requestID,
			part.Type == "audio",
		)
		if sendErr != nil {
			return messageIDs, classifyPhotonSendError("send media", sendErr)
		}
		sent = true
		if messageID != "" {
			messageIDs = append(messageIDs, messageID)
		}
		if !captionAttempted && !captionSent && strings.TrimSpace(caption) != "" {
			captionAttempted = true
			captionID := channels.StableID(msg.ID, "photon", "caption")
			if _, captionErr := c.sendText(ctx, msg.ChatID, caption, "", captionID); captionErr != nil {
				logger.WarnCF("photon", "Attachment sent but caption failed", map[string]any{
					"chat_id": msg.ChatID,
					"error":   captionErr.Error(),
				})
			} else {
				captionSent = true
			}
		}
	}
	if !sent && len(msg.Parts) > 0 {
		return nil, fmt.Errorf("photon: none of the media parts could be resolved: %w", channels.ErrSendFailed)
	}
	return messageIDs, nil
}

func firstMediaCaption(parts []bus.MediaPart) string {
	for _, part := range parts {
		if caption := strings.TrimSpace(part.Caption); caption != "" {
			return caption
		}
	}
	return ""
}

func (c *PhotonChannel) StartTyping(ctx context.Context, chatID string) (func(), error) {
	if !c.IsRunning() {
		return func() {}, channels.ErrNotRunning
	}
	if err := c.api.SetTyping(ctx, chatID, true); err != nil {
		return func() {}, err
	}
	typingCtx, cancel := context.WithTimeout(context.Background(), photonTypingDuration)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			_ = c.api.SetTyping(stopCtx, chatID, false)
		})
	}
	go func() {
		<-typingCtx.Done()
		if typingCtx.Err() == context.DeadlineExceeded {
			stop()
		}
	}()
	return stop, nil
}

func (c *PhotonChannel) EditMessage(ctx context.Context, chatID, messageID, content string) error {
	if !c.IsRunning() {
		return channels.ErrNotRunning
	}
	err := c.api.EditMessage(ctx, chatID, messageID, content)
	if errors.Is(err, channels.ErrDeliveryUnknown) {
		return nil
	}
	return err
}

func (c *PhotonChannel) DeleteMessage(ctx context.Context, chatID, messageID string) error {
	if !c.IsRunning() {
		return channels.ErrNotRunning
	}
	return c.api.UnsendMessage(ctx, chatID, messageID)
}

func (c *PhotonChannel) SendPlaceholder(ctx context.Context, chatID string) (string, error) {
	if !c.bc.Placeholder.Enabled {
		return "", nil
	}
	return c.sendText(ctx, chatID, c.bc.Placeholder.GetRandomText(), "", "")
}

func (c *PhotonChannel) ReactToMessage(ctx context.Context, chatID, messageID string) (func(), error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}
	const emoji = "👀"
	if err := c.api.SetReaction(ctx, chatID, messageID, emoji, true); err != nil {
		return nil, err
	}
	var once sync.Once
	undo := func() {
		once.Do(func() {
			undoCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = c.api.SetReaction(undoCtx, chatID, messageID, emoji, false)
		})
	}
	return undo, nil
}

func (c *PhotonChannel) VoiceCapabilities() channels.VoiceCapabilities {
	return channels.VoiceCapabilities{ASR: true, TTS: true}
}

func (c *PhotonChannel) RecordToolFeedbackMessage(chatID, messageID, content string) {
	if c.progress != nil {
		c.progress.Record(chatID, messageID, content)
	}
}

func (c *PhotonChannel) ClearToolFeedbackMessage(chatID string) {
	if c.progress != nil {
		c.progress.Clear(chatID)
	}
}

func (c *PhotonChannel) DismissToolFeedbackMessage(ctx context.Context, chatID string) {
	if c.progress == nil {
		return
	}
	messageID, ok := c.progress.Current(chatID)
	if !ok {
		return
	}
	c.ClearToolFeedbackMessage(chatID)
	_ = c.DeleteMessage(ctx, chatID, messageID)
}

func (c *PhotonChannel) FinalizeToolFeedbackMessage(ctx context.Context, msg bus.OutboundMessage) ([]string, bool) {
	if outboundMessageIsToolFeedback(msg) {
		return nil, false
	}
	return c.finalizeToolFeedbackMessage(ctx, msg.ChatID, msg.Content)
}

func (c *PhotonChannel) finalizeToolFeedbackMessage(ctx context.Context, chatID, content string) ([]string, bool) {
	if c.progress == nil {
		return nil, false
	}
	messageID, baseContent, ok := c.progress.Take(chatID)
	if !ok {
		return nil, false
	}
	if err := c.EditMessage(ctx, chatID, messageID, content); err != nil {
		c.progress.Record(chatID, messageID, baseContent)
		return nil, false
	}
	return []string{messageID}, true
}

func (c *PhotonChannel) BeginStream(ctx context.Context, chatID string) (channels.Streamer, error) {
	if c == nil || c.settings == nil || !c.settings.Streaming.Enabled {
		return nil, fmt.Errorf("photon streaming disabled in config")
	}
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}
	streamCfg := c.settings.Streaming.WithDefaults(5, 200)
	return &photonStreamer{
		channel:          c,
		chatID:           chatID,
		throttleInterval: time.Duration(streamCfg.ThrottleSeconds) * time.Second,
		minGrowth:        streamCfg.MinGrowthChars,
	}, nil
}

func outboundMessageIsToolFeedback(msg bus.OutboundMessage) bool {
	if len(msg.Context.Raw) == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(msg.Context.Raw["message_kind"]), "tool_feedback")
}

func detectPhotonMention(content string, names []string) (bool, string) {
	cleaned := content
	mentioned := false
	for _, name := range names {
		name = strings.TrimSpace(strings.TrimPrefix(name, "@"))
		if name == "" {
			continue
		}
		pattern := regexp.MustCompile(`(?i)(^|[\s,])@?` + regexp.QuoteMeta(name) + `\b[,:\-]?`)
		if !pattern.MatchString(cleaned) {
			continue
		}
		mentioned = true
		cleaned = pattern.ReplaceAllString(cleaned, "$1")
	}
	return mentioned, strings.TrimSpace(cleaned)
}

func classifyPhotonSendError(action string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, channels.ErrRateLimit) || errors.Is(err, channels.ErrTemporary) ||
		errors.Is(err, channels.ErrDeliveryUnknown) || errors.Is(err, channels.ErrSendFailed) {
		return err
	}
	switch status.Code(err) {
	case codes.ResourceExhausted:
		return fmt.Errorf("photon %s: %w: %v", action, channels.ErrRateLimit, err)
	case codes.InvalidArgument, codes.NotFound, codes.FailedPrecondition, codes.OutOfRange:
		return fmt.Errorf("photon %s: %w: %v", action, channels.ErrSendFailed, err)
	case codes.Canceled, codes.DeadlineExceeded, codes.Unavailable, codes.Internal,
		codes.Unknown, codes.Aborted, codes.Unauthenticated, codes.PermissionDenied:
		return fmt.Errorf("photon %s: %w: %v", action, channels.ErrTemporary, err)
	default:
		return fmt.Errorf("photon %s: %w: %v", action, channels.ErrTemporary, err)
	}
}
