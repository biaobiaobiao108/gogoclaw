package photon

import (
	"context"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	photonv1 "github.com/biaobiaobiao108/gogoclaw/pkg/channels/photon/internal/photon/imessage/v1"
	"github.com/biaobiaobiao108/gogoclaw/pkg/identity"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
)

const photonLineReconcileInterval = 30 * time.Second

func (c *PhotonChannel) runInbound(ctx context.Context) {
	active := make(map[string]context.CancelFunc)
	ticker := time.NewTicker(photonLineReconcileInterval)
	defer ticker.Stop()
	defer func() {
		for _, cancel := range active {
			cancel()
		}
	}()

	for {
		lines := c.api.Lines()
		current := make(map[string]struct{}, len(lines))
		for _, line := range lines {
			current[line.ID] = struct{}{}
			if _, running := active[line.ID]; running {
				continue
			}
			lineCtx, cancel := context.WithCancel(ctx)
			active[line.ID] = cancel
			go c.runLine(lineCtx, line)
		}
		for lineID, cancel := range active {
			if _, exists := current[lineID]; exists {
				continue
			}
			cancel()
			delete(active, lineID)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *PhotonChannel) runLine(ctx context.Context, line photonLine) {
	backoff := time.Second
	for ctx.Err() == nil {
		cursor := c.state.Get(line.ID)
		if cursor > 0 {
			head, err := c.api.CatchUp(ctx, line.ID, cursor, func(sequence uint64, event *photonv1.MessageChangeEvent) error {
				if err := c.handleMessageChange(ctx, line, event); err != nil {
					return err
				}
				return c.advanceCursor(line.ID, sequence)
			})
			if err != nil {
				if ctx.Err() == nil {
					logger.WarnCF("photon", "iMessage catch-up failed", map[string]any{
						"line_id": line.ID,
						"error":   err.Error(),
					})
					if !waitContext(ctx, backoff) {
						return
					}
					backoff = min(backoff*2, 30*time.Second)
				}
				continue
			}
			cursor = max(cursor, head)
			if err := c.state.Set(line.ID, cursor); err != nil {
				logger.WarnCF("photon", "Failed to persist iMessage catch-up cursor", map[string]any{
					"line_id": line.ID,
					"error":   err.Error(),
				})
			}
		}

		err := c.api.SubscribeMessages(ctx, line.ID, cursor, func(sequence uint64, event *photonv1.MessageChangeEvent) error {
			if err := c.handleMessageChange(ctx, line, event); err != nil {
				return err
			}
			return c.advanceCursor(line.ID, sequence)
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logger.WarnCF("photon", "iMessage event stream interrupted", map[string]any{
				"line_id": line.ID,
				"error":   err.Error(),
			})
		}
		if !waitContext(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (c *PhotonChannel) advanceCursor(lineID string, sequence uint64) error {
	if sequence == 0 {
		return nil
	}
	if err := c.state.Set(lineID, sequence); err != nil {
		logger.WarnCF("photon", "Failed to persist iMessage cursor", map[string]any{
			"line_id":  lineID,
			"sequence": sequence,
			"error":    err.Error(),
		})
	}
	return nil
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (c *PhotonChannel) handleMessageChange(
	ctx context.Context,
	line photonLine,
	event *photonv1.MessageChangeEvent,
) error {
	received := event.GetMessageReceived()
	if received == nil || received.GetMessage() == nil {
		return nil
	}
	message := received.GetMessage()
	chatID := firstNonEmpty(event.GetChatGuid(), firstString(message.GetChatGuids()))
	if chatID == "" {
		return nil
	}
	c.api.RememberChatLine(chatID, line.ID)
	if event.GetIsFromMe() || message.GetIsFromMe() {
		return nil
	}
	if message.GetIsSystemMessage() || message.GetIsAutoReply() || message.GetIsServiceMessage() {
		return nil
	}
	if message.GetItemType() != photonv1.MessageItemType_MESSAGE_ITEM_TYPE_NORMAL {
		return nil
	}
	if strings.TrimSpace(message.GetGuid()) == "" {
		return fmt.Errorf("photon: inbound message has no GUID")
	}

	senderAddress := ""
	if sender := message.GetSender(); sender != nil {
		senderAddress = strings.TrimSpace(sender.GetAddress())
	}
	if senderAddress == "" && event.GetActor() != nil {
		senderAddress = strings.TrimSpace(event.GetActor().GetAddress())
	}
	if senderAddress == "" {
		senderAddress = "unknown"
	}
	sender := bus.SenderInfo{
		Platform:    "photon",
		PlatformID:  senderAddress,
		CanonicalID: identity.BuildCanonicalID("photon", senderAddress),
		Username:    senderAddress,
		DisplayName: senderAddress,
	}
	if !c.IsAllowedSender(sender) {
		return nil
	}

	content := strings.TrimSpace(message.GetContent().GetText())
	mentionedByStructure := messageMentionsAddress(message.GetContent().GetMentions(), line)
	mentionedByName, cleaned := detectPhotonMention(content, c.mentionNames)
	mentioned := mentionedByStructure || mentionedByName
	if mentionedByName {
		content = cleaned
	}

	chatType := "direct"
	if isGroupChatID(chatID) {
		chatType = "group"
		respond, cleanedGroupText := c.ShouldRespondInGroup(mentioned, content)
		if !respond {
			return nil
		}
		content = cleanedGroupText
	}

	if appText := photonMiniAppText(message.GetContent().GetMiniApp()); appText != "" {
		if content != "" {
			content += "\n"
		}
		content += appText
	}
	if subject := strings.TrimSpace(message.GetSubject()); subject != "" && !strings.Contains(content, subject) {
		content = subject + "\n" + content
	}

	mediaRefs, attachments, marker := c.materializeInboundAttachments(ctx, line, chatID, message)
	if marker != "" {
		if content != "" {
			content += "\n"
		}
		content += marker
	}
	if strings.TrimSpace(content) == "" && len(mediaRefs) == 0 {
		content = "[empty message]"
	}

	if !c.settings.DisableReadReceipts {
		readCtx, cancel := context.WithTimeout(ctx, photonRPCTimeout)
		if err := c.api.MarkRead(readCtx, chatID); err != nil {
			logger.DebugCF("photon", "Failed to mark iMessage chat as read", map[string]any{
				"chat_id": chatID,
				"error":   err.Error(),
			})
		}
		cancel()
	}

	var timestamp time.Time
	if createdAt := message.GetDateCreated(); createdAt != nil {
		timestamp = createdAt.AsTime()
	} else if occurredAt := event.GetOccurredAt(); occurredAt != nil {
		timestamp = occurredAt.AsTime()
	}
	raw := map[string]string{
		"platform":    "photon",
		"chat_guid":   chatID,
		"line_id":     line.ID,
		"line_number": line.Phone,
	}
	if !timestamp.IsZero() {
		raw["timestamp"] = timestamp.UTC().Format(time.RFC3339Nano)
	}
	if service := message.GetSender().GetService(); service != photonv1.ChatServiceType_CHAT_SERVICE_TYPE_UNSPECIFIED {
		raw["service"] = service.String()
	}
	if message.GetThreadOriginatorGuid() != "" {
		raw["thread_originator_guid"] = message.GetThreadOriginatorGuid()
	}

	inboundCtx := bus.InboundContext{
		Channel:          c.Name(),
		Account:          c.settings.ProjectID,
		ChatID:           chatID,
		ChatType:         chatType,
		SpaceID:          chatID,
		SpaceType:        chatType,
		SenderID:         sender.CanonicalID,
		MessageID:        message.GetGuid(),
		Mentioned:        mentioned,
		ReplyToMessageID: message.GetReplyTargetGuid(),
		Raw:              raw,
	}
	return c.HandleInboundContextWithAttachments(ctx, chatID, content, mediaRefs, attachments, inboundCtx, sender)
}

func (c *PhotonChannel) materializeInboundAttachments(
	ctx context.Context,
	line photonLine,
	chatID string,
	message *photonv1.Message,
) ([]string, []bus.InboundAttachment, string) {
	var mediaRefs []string
	var attachments []bus.InboundAttachment
	var markers []string
	scope := channels.BuildMediaScope(c.Name(), chatID, message.GetGuid())

	for index, info := range message.GetContent().GetAttachments() {
		if info == nil || info.GetIsHidden() || info.GetIsSticker() {
			continue
		}
		oversized := c.maxMediaBytes > 0 && info.GetTotalBytes() > c.maxMediaBytes
		if oversized {
			logger.WarnCF("photon", "Skipping oversized inbound attachment", map[string]any{
				"message_id": message.GetGuid(),
				"attachment": info.GetGuid(),
				"bytes":      info.GetTotalBytes(),
				"limit":      c.maxMediaBytes,
			})
		}
		filename := inboundAttachmentFilename(info, message, index)
		attachmentType := photonMediaType(info.GetMimeType(), message.GetIsAudioMessage())
		markers = append(markers, photonAttachmentMarker(attachmentType, filename))
		attachments = append(attachments, bus.InboundAttachment{
			Type:        attachmentType,
			Filename:    filename,
			ContentType: info.GetMimeType(),
		})
		if oversized {
			continue
		}

		path, downloaded, err := c.api.DownloadAttachment(ctx, line.ID, info.GetGuid(), media.TempDir())
		if err != nil {
			logger.WarnCF("photon", "Failed to download inbound attachment", map[string]any{
				"message_id": message.GetGuid(),
				"attachment": info.GetGuid(),
				"error":      err.Error(),
			})
			continue
		}
		if downloaded != nil {
			filename = firstNonEmpty(downloaded.GetFileName(), filename)
			attachmentType = photonMediaType(downloaded.GetMimeType(), message.GetIsAudioMessage())
			attachments[len(attachments)-1].Type = attachmentType
			attachments[len(attachments)-1].Filename = filename
			attachments[len(attachments)-1].ContentType = downloaded.GetMimeType()
			markers[len(markers)-1] = photonAttachmentMarker(attachmentType, filename)
		}
		attachments[len(attachments)-1].Path = path

		ref := path
		if store := c.GetMediaStore(); store != nil {
			stored, storeErr := store.Store(path, media.MediaMeta{
				Filename:      filename,
				ContentType:   info.GetMimeType(),
				Source:        "photon:" + line.ID,
				CleanupPolicy: media.CleanupPolicyDeleteOnCleanup,
			}, scope)
			if storeErr != nil {
				logger.WarnCF("photon", "Failed to register inbound attachment", map[string]any{
					"message_id": message.GetGuid(),
					"error":      storeErr.Error(),
				})
			} else {
				ref = stored
			}
		}
		mediaRefs = append(mediaRefs, ref)
	}
	return mediaRefs, attachments, strings.Join(markers, "\n")
}

func inboundAttachmentFilename(info *photonv1.AttachmentInfo, message *photonv1.Message, index int) string {
	name := strings.TrimSpace(info.GetFileName())
	if name == "" {
		name = fmt.Sprintf("attachment-%d", index+1)
	}
	name = sanitizeFilename(name)
	if filepath.Ext(name) == "" {
		if extensions, err := mime.ExtensionsByType(info.GetMimeType()); err == nil && len(extensions) > 0 {
			name += extensions[0]
		}
	}
	if name == "" {
		name = sanitizeFilenamePart(message.GetGuid()) + ".bin"
	}
	return name
}

func photonMediaType(mimeType string, isAudioMessage bool) string {
	if isAudioMessage {
		return "audio"
	}
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	default:
		return "file"
	}
}

func photonAttachmentMarker(attachmentType, filename string) string {
	switch attachmentType {
	case "image":
		return fmt.Sprintf("[image: %s]", filename)
	case "audio":
		return "[voice]"
	case "video":
		return fmt.Sprintf("[video: %s]", filename)
	default:
		return fmt.Sprintf("[file: %s]", filename)
	}
}

func photonMiniAppText(app *photonv1.MiniAppContent) string {
	if app == nil {
		return ""
	}
	var parts []string
	name := strings.TrimSpace(app.GetAppName())
	if name != "" {
		parts = append(parts, "[app: "+name+"]")
	}
	if layout := app.GetLayout(); layout != nil {
		for _, value := range []string{
			layout.GetCaption(),
			layout.GetSubcaption(),
			layout.GetImageTitle(),
			layout.GetImageSubtitle(),
			layout.GetSummary(),
		} {
			if value = strings.TrimSpace(value); value != "" && !containsString(parts, value) {
				parts = append(parts, value)
			}
		}
	}
	if url := strings.TrimSpace(app.GetUrl()); url != "" && !containsString(parts, url) {
		parts = append(parts, url)
	}
	return strings.Join(parts, "\n")
}

func messageMentionsAddress(mentions []*photonv1.MessageMention, line photonLine) bool {
	for _, mention := range mentions {
		address := strings.TrimSpace(mention.GetAddress())
		if address != "" && line.Phone != "" && strings.EqualFold(address, line.Phone) {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func firstString(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sanitizeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
	name = strings.Trim(name, "._-")
	if name == "" {
		return "attachment.bin"
	}
	return name
}

func sanitizeFilenamePart(value string) string {
	value = sanitizeFilename(value)
	if value == "attachment.bin" {
		return "message"
	}
	return value
}
