// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

func (al *AgentLoop) transcribeAudioInMessage(ctx context.Context, msg bus.InboundMessage) (bus.InboundMessage, bool) {
	return al.transcribeAudioInMessageWithRuntime(ctx, msg, al.runtimeSnapshot())
}

func (al *AgentLoop) transcribeAudioInMessageWithRuntime(
	ctx context.Context,
	msg bus.InboundMessage,
	runtime runtimeSnapshot,
) (bus.InboundMessage, bool) {
	if runtime.transcriber == nil || runtime.mediaStore == nil || len(msg.Media) == 0 {
		return msg, false
	}

	// Transcribe each audio media ref in order.
	var transcriptions []string
	var keptMedia []string
	for _, ref := range msg.Media {
		path, meta, err := runtime.mediaStore.ResolveWithMeta(ref)
		if err != nil {
			logger.WarnCF("voice", "Failed to resolve media ref", map[string]any{"ref": ref, "error": err})
			keptMedia = append(keptMedia, ref)
			continue
		}
		if !utils.IsAudioFile(meta.Filename, meta.ContentType) {
			keptMedia = append(keptMedia, ref)
			continue
		}
		result, err := runtime.transcriber.Transcribe(ctx, path)
		if err != nil {
			logger.WarnCF("voice", "Transcription failed", map[string]any{"ref": ref, "error": err})
			transcriptions = append(transcriptions, "")
			keptMedia = append(keptMedia, ref)
			continue
		}
		transcriptions = append(transcriptions, result.Text)
	}

	if len(transcriptions) == 0 {
		return msg, false
	}

	al.sendTranscriptionFeedbackWithRuntime(ctx, msg.Channel, msg.ChatID, msg.MessageID, transcriptions, runtime)

	// Replace audio annotations sequentially with transcriptions.
	idx := 0
	newContent := audioAnnotationRe.ReplaceAllStringFunc(msg.Content, func(match string) string {
		if idx >= len(transcriptions) {
			return match
		}
		text := transcriptions[idx]
		idx++
		if text == "" {
			return match
		}
		return "[voice: " + text + "]"
	})

	// Append any remaining transcriptions not matched by an annotation.
	for ; idx < len(transcriptions); idx++ {
		if transcriptions[idx] != "" {
			newContent += "\n[voice: " + transcriptions[idx] + "]"
		}
	}

	msg.Content = newContent
	msg.Media = keptMedia
	return msg, true
}

func (al *AgentLoop) sendTranscriptionFeedback(
	ctx context.Context,
	channel, chatID, messageID string,
	validTexts []string,
) {
	al.sendTranscriptionFeedbackWithRuntime(ctx, channel, chatID, messageID, validTexts, al.runtimeSnapshot())
}

func (al *AgentLoop) sendTranscriptionFeedbackWithRuntime(
	ctx context.Context,
	channel, chatID, messageID string,
	validTexts []string,
	runtime runtimeSnapshot,
) {
	if !runtime.cfg.Voice.EchoTranscription {
		return
	}
	if runtime.channelManager == nil {
		return
	}

	var nonEmpty []string
	for _, t := range validTexts {
		if t != "" {
			nonEmpty = append(nonEmpty, t)
		}
	}

	var feedbackMsg string
	if len(nonEmpty) > 0 {
		feedbackMsg = "Transcript: " + strings.Join(nonEmpty, "\n")
	} else {
		feedbackMsg = "No voice detected in the audio"
	}

	err := runtime.channelManager.SendMessage(ctx, bus.OutboundMessage{
		Context:          bus.NewOutboundContext(channel, chatID, messageID),
		Content:          feedbackMsg,
		ReplyToMessageID: messageID,
	})
	if err != nil {
		logger.WarnCF("voice", "Failed to send transcription feedback", map[string]any{"error": err.Error()})
	}
}
