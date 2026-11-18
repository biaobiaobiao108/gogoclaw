package api

import (
	"bufio"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers/messageutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

func buildSessionListItem(sessionID string, sess sessionFile, toolFeedbackMaxArgsLength int) sessionListItem {
	transcript := visibleSessionMessages(sessionID, sess.Messages, toolFeedbackMaxArgsLength)

	previewText := ""
	for _, msg := range transcript {
		if msg.Role == "user" {
			previewText = sessionChatMessagePreview(msg)
		}
		if previewText != "" {
			break
		}
	}
	preview := truncateRunes(previewText, maxSessionPreviewRunes)

	if preview == "" {
		preview = "(empty)"
	}
	title := session.FallbackTitle(previewText)
	if title == "" {
		title = preview
	}

	return sessionListItem{
		ID:           sessionID,
		Title:        title,
		Preview:      preview,
		MessageCount: len(transcript),
		Created:      sess.Created.Format(time.RFC3339),
		Updated:      sess.Updated.Format(time.RFC3339),
		updatedAt:    sess.Updated,
	}
}

// Build the sidebar index without retaining an entire archived transcript.
func (h *Handler) readJSONLSessionListItem(dir, key, id string, maxArgs int) (sessionListItem, error) {
	if item, found, err := h.readSQLiteSessionListItem(dir, key, id, maxArgs); err != nil {
		return sessionListItem{}, err
	} else if found {
		return item, nil
	}
	sess, path, err := h.readJSONLSessionInfo(dir, key)
	if err != nil {
		return sessionListItem{}, err
	}
	item := buildSessionListItem(id, sess, maxArgs)
	file, err := os.Open(path)
	if err != nil {
		return sessionListItem{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSessionJSONLLineSize)
	hasMessages, hasTitle := false, false
	for scanner.Scan() {
		var message providers.Message
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil || messageutil.IsTransientAssistantThoughtMessage(message) {
			continue
		}
		hasMessages = true
		visible := visibleSessionMessages(id, []providers.Message{message}, maxArgs)
		item.MessageCount += len(visible)
		if !hasTitle {
			for _, msg := range visible {
				if msg.Role != "user" {
					continue
				}
				previewText := sessionChatMessagePreview(msg)
				preview := truncateRunes(previewText, maxSessionPreviewRunes)
				if preview != "" {
					item.Title = session.FallbackTitle(previewText)
					if item.Title == "" {
						item.Title = preview
					}
					item.Preview = preview
					hasTitle = true
					break
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return sessionListItem{}, err
	}
	if !hasMessages && strings.TrimSpace(sess.Summary) == "" {
		return sessionListItem{}, os.ErrNotExist
	}
	return item, nil
}

func isEmptySession(sess sessionFile) bool {
	return len(sess.Messages) == 0 && strings.TrimSpace(sess.Summary) == ""
}

func truncateRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= maxLen {
		return string(runes)
	}
	return string(runes[:maxLen]) + "..."
}

func sessionChatMessageVisible(msg sessionChatMessage) bool {
	return strings.TrimSpace(msg.Content) != "" ||
		len(msg.Media) > 0 ||
		len(msg.Attachments) > 0 ||
		len(msg.ToolCalls) > 0
}

func sessionChatMessagePreview(msg sessionChatMessage) string {
	if content := strings.TrimSpace(msg.Content); content != "" {
		return content
	}
	if len(msg.Attachments) > 0 {
		if strings.EqualFold(strings.TrimSpace(msg.Attachments[0].Type), "image") {
			return "[image]"
		}
		return "[attachment]"
	}
	if len(msg.Media) > 0 {
		if strings.HasPrefix(strings.TrimSpace(msg.Media[0]), "data:image/") {
			return "[image]"
		}
		return "[attachment]"
	}
	if len(msg.ToolCalls) > 0 {
		return "[tool call]"
	}
	return ""
}

func visibleSessionMessages(sessionID string, messages []providers.Message, toolFeedbackMaxArgsLength int) []sessionChatMessage {
	return sessionTranscriptMessages(sessionID, messages, toolFeedbackMaxArgsLength, false)
}

func detailSessionMessages(sessionID string, messages []providers.Message, toolFeedbackMaxArgsLength int) []sessionChatMessage {
	return sessionTranscriptMessages(sessionID, messages, toolFeedbackMaxArgsLength, true)
}

func sessionTranscriptMessages(
	sessionID string,
	messages []providers.Message,
	toolFeedbackMaxArgsLength int,
	includeThoughts bool,
) []sessionChatMessage {
	transcript := make([]sessionChatMessage, 0, len(messages))
	pendingSources := make([]sessionChatSource, 0)

	for _, msg := range messages {
		attachments := sessionAttachments(sessionID, msg)

		switch msg.Role {
		case "tool":
			pendingSources = append(pendingSources, extractSessionSources(msg.Content)...)
			continue

		case "user":
			pendingSources = pendingSources[:0]
			chatMsg := sessionChatMessage{
				ID:          msg.ID,
				Role:        "user",
				TurnID:      msg.SteeringTurnID,
				Content:     msg.Content,
				ModelName:   msg.ModelName,
				CreatedAt:   msg.CreatedAt,
				Media:       append([]string(nil), msg.Media...),
				Attachments: attachments,
			}
			if sessionChatMessageVisible(chatMsg) {
				transcript = append(transcript, chatMsg)
			}

		case "assistant":
			if messageutil.IsTransientAssistantThoughtMessage(msg) {
				continue
			}
			turnID := messageutil.AssistantTurnID(msg.ID)
			if includeThoughts {
				if thoughtMsg, ok := assistantThoughtMessage(msg); ok {
					thoughtMsg.TurnID = turnID
					transcript = append(transcript, thoughtMsg)
				}
			}

			// Keep visible message-tool output and assistant text in the transcript,
			// while excluding the internal summary that marks handled delivery.
			content := msg.Content
			internalOnly := assistantMessageInternalOnly(msg)
			if internalOnly {
				content = ""
			}
			toolCallsMsg, hasToolCallsMsg := assistantToolCallsMessage(
				msg.ToolCalls,
				msg.ModelName,
				toolFeedbackMaxArgsLength,
				msg.CreatedAt,
				content,
			)
			if msg.ID != "" {
				toolCallsMsg.ID = msg.ID + "-tools"
			}
			toolCallsMsg.TurnID = turnID
			visibleToolMessages := visibleAssistantToolMessages(msg.ToolCalls, msg.ModelName, msg.CreatedAt)
			for i := range visibleToolMessages {
				visibleToolMessages[i].TurnID = turnID
			}

			chatMsg := sessionChatMessage{
				ID:          msg.ID,
				Role:        "assistant",
				TurnID:      turnID,
				Content:     content,
				ModelName:   msg.ModelName,
				CreatedAt:   msg.CreatedAt,
				Media:       append([]string(nil), msg.Media...),
				Attachments: attachments,
				Sources:     mergeSessionSources(pendingSources, extractSessionSources(content)),
			}
			if (internalOnly && len(attachments) == 0) || !sessionChatMessageVisible(chatMsg) {
				if hasToolCallsMsg {
					transcript = append(transcript, toolCallsMsg)
				}
				if len(visibleToolMessages) > 0 {
					transcript = append(transcript, visibleToolMessages...)
				}
				continue
			}

			transcript = append(transcript, chatMsg)
			pendingSources = pendingSources[:0]
			if hasToolCallsMsg {
				transcript = append(transcript, toolCallsMsg)
			}
			if len(visibleToolMessages) > 0 {
				transcript = append(transcript, visibleToolMessages...)
			}
		}
	}

	return filterSessionChatMessages(transcript)
}

func filterSessionChatMessages(messages []sessionChatMessage) []sessionChatMessage {
	filtered := messages[:0]
	for _, msg := range messages {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

func sessionAttachments(sessionID string, msg providers.Message) []sessionChatAttachment {
	if len(msg.Attachments) == 0 {
		return nil
	}

	attachments := make([]sessionChatAttachment, 0, len(msg.Attachments))
	for _, attachment := range msg.Attachments {
		urlValue, ok := sessionAttachmentURL(sessionID, attachment)
		if !ok {
			continue
		}
		attachmentType := normalizedSessionAttachmentType(attachment)
		attachments = append(attachments, sessionChatAttachment{
			ID:          durableAttachmentID(attachment),
			Type:        attachmentType,
			URL:         urlValue,
			Filename:    strings.TrimSpace(attachment.Filename),
			ContentType: strings.TrimSpace(attachment.ContentType),
		})
	}

	if len(attachments) == 0 {
		return nil
	}
	return attachments
}

func durableAttachmentID(attachment providers.Attachment) string {
	path := strings.TrimSpace(attachment.Ref)
	if path == "" || (!strings.HasPrefix(path, "/") && !strings.Contains(path, ":\\")) {
		return ""
	}
	return filepath.Base(path)
}

func sessionAttachmentURL(sessionID string, attachment providers.Attachment) (string, bool) {
	if id := durableAttachmentID(attachment); id != "" && validAttachmentSessionID(sessionID) {
		return "/api/attachments/" + url.PathEscape(sessionID) + "/" + url.PathEscape(id), true
	}
	if rawURL := strings.TrimSpace(attachment.URL); rawURL != "" {
		parsed, err := url.Parse(rawURL)
		if strings.HasPrefix(rawURL, "data:image/") ||
			(err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http")) ||
			(err == nil && parsed.Scheme == "" && strings.HasPrefix(rawURL, "/") && !strings.HasPrefix(rawURL, "//")) {
			return rawURL, true
		}
		return "", false
	}

	ref := strings.TrimSpace(attachment.Ref)
	if ref == "" || strings.HasPrefix(ref, "media://") {
		return "", false
	}
	return "", false
}

func sessionAttachmentType(attachment providers.Attachment) string {
	contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
	filename := strings.ToLower(strings.TrimSpace(attachment.Filename))
	rawRef := strings.ToLower(strings.TrimSpace(attachment.Ref))
	rawURL := strings.ToLower(strings.TrimSpace(attachment.URL))
	switch filepath.Ext(filename) {
	case ".svg", ".svgz", ".html", ".htm", ".xhtml", ".xml", ".js", ".mjs":
		return "file"
	}

	switch {
	case strings.HasPrefix(contentType, "image/"),
		strings.HasPrefix(rawRef, "data:image/"),
		strings.HasPrefix(rawURL, "data:image/"):
		return "image"
	case strings.HasPrefix(contentType, "audio/"):
		return "audio"
	case strings.HasPrefix(contentType, "video/"):
		return "video"
	}

	switch ext := filepath.Ext(filename); ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".avif":
		return "image"
	case ".mp3", ".wav", ".ogg", ".m4a", ".flac", ".aac", ".wma", ".opus":
		return "audio"
	case ".mp4", ".avi", ".mov", ".webm", ".mkv":
		return "video"
	default:
		return "file"
	}
}

func normalizedSessionAttachmentType(attachment providers.Attachment) string {
	inferred := sessionAttachmentType(attachment)
	declared := strings.ToLower(strings.TrimSpace(attachment.Type))
	if inferred == "file" {
		return "file"
	}
	switch declared {
	case "image", "audio", "video", "file":
		return declared
	default:
		return inferred
	}
}

func assistantMessageInternalOnly(msg providers.Message) bool {
	return strings.TrimSpace(msg.Content) == handledToolResponseSummaryText
}

func assistantThoughtMessage(msg providers.Message) (sessionChatMessage, bool) {
	reasoning := strings.TrimSpace(msg.ReasoningContent)
	if reasoning == "" {
		return sessionChatMessage{}, false
	}
	if reasoning == strings.TrimSpace(msg.Content) {
		return sessionChatMessage{}, false
	}
	id := ""
	if msg.ID != "" {
		id = msg.ID + "-thought"
	}
	return sessionChatMessage{
		Role:      "assistant",
		Content:   reasoning,
		Kind:      "thought",
		ID:        id,
		ModelName: msg.ModelName,
		CreatedAt: msg.CreatedAt,
	}, true
}

func assistantToolCallsMessage(
	toolCalls []providers.ToolCall,
	modelName string,
	toolFeedbackMaxArgsLength int,
	createdAt *time.Time,
	assistantContent string,
) (sessionChatMessage, bool) {
	if len(toolCalls) == 0 {
		return sessionChatMessage{}, false
	}
	if toolFeedbackMaxArgsLength <= 0 {
		toolFeedbackMaxArgsLength = defaultToolFeedbackMaxArgsLength()
	}

	visibleToolCalls := utils.BuildVisibleToolCalls(toolCalls, toolFeedbackMaxArgsLength, assistantContent)
	if len(visibleToolCalls) == 0 {
		return sessionChatMessage{}, false
	}

	return sessionChatMessage{
		Role:      "assistant",
		Kind:      "tool_calls",
		ModelName: modelName,
		CreatedAt: createdAt,
		ToolCalls: visibleToolCalls,
	}, true
}

func visibleAssistantToolArgsPreview(
	tc providers.ToolCall,
	toolFeedbackMaxArgsLength int,
) string {
	return utils.VisibleToolCallArgumentsPreview(tc, toolFeedbackMaxArgsLength)
}

func visibleAssistantToolMessages(
	toolCalls []providers.ToolCall,
	modelName string,
	createdAt *time.Time,
) []sessionChatMessage {
	if len(toolCalls) == 0 {
		return nil
	}

	messages := make([]sessionChatMessage, 0, len(toolCalls))
	for _, tc := range toolCalls {
		name, argsJSON := utils.VisibleToolCallNameAndArguments(tc)
		if name != "message" {
			continue
		}
		content, ok := parseMessageToolContent(argsJSON)
		if !ok {
			continue
		}
		messages = append(messages, sessionChatMessage{
			Role:      "assistant",
			Content:   content,
			ModelName: modelName,
			CreatedAt: createdAt,
		})
	}

	return messages
}

func parseMessageToolContent(argsJSON string) (string, bool) {
	var args struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", false
	}
	if strings.TrimSpace(args.Content) == "" {
		return "", false
	}
	return args.Content, true
}

// sessionsDir resolves the path to the gateway's session storage directory.
// It reads the workspace from config, falling back to ~/.gogoclaw/workspace.
