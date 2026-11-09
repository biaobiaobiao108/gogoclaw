package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/chatmeta"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

func providerAttachmentsFromInbound(attachments []bus.InboundAttachment) []providers.Attachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]providers.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		out = append(out, providers.Attachment{
			Type:        attachment.Type,
			URL:         attachment.URL,
			Ref:         attachment.Path,
			Filename:    attachment.Filename,
			ContentType: attachment.ContentType,
		})
	}
	return out
}

func inboundAttachmentsFromProviders(attachments []providers.Attachment) []bus.InboundAttachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]bus.InboundAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		path := strings.TrimSpace(attachment.Ref)
		if !strings.HasPrefix(path, "/") && !strings.Contains(path, ":\\") {
			path = ""
		}
		out = append(out, bus.InboundAttachment{
			Type: attachment.Type, URL: attachment.URL, Path: path,
			Filename: attachment.Filename, ContentType: attachment.ContentType,
		})
	}
	return out
}

func branchSessionID(ts *turnState) string {
	if ts == nil {
		return ""
	}
	if ts.opts.Dispatch.InboundContext != nil {
		if value := strings.TrimSpace(ts.opts.Dispatch.InboundContext.Raw["session_id"]); value != "" {
			return value
		}
	}
	return sessionIDFromSessionKey(ts.sessionKey)
}

func sessionIDFromSessionKey(key string) string {
	key = strings.TrimSpace(key)
	if index := strings.LastIndex(key, "pico:"); index >= 0 {
		return strings.TrimSpace(key[index+len("pico:"):])
	}
	return ""
}

func branchSessionDir(al *AgentLoop, agent *AgentInstance) string {
	if agent != nil && strings.TrimSpace(agent.Workspace) != "" {
		return filepath.Join(strings.TrimSpace(agent.Workspace), "sessions")
	}
	if al != nil {
		if defaultAgent := al.registry.GetDefaultAgent(); defaultAgent != nil && strings.TrimSpace(defaultAgent.Workspace) != "" {
			return filepath.Join(strings.TrimSpace(defaultAgent.Workspace), "sessions")
		}
	}
	return filepath.Join(config.GetHome(), "workspace", "sessions")
}

func (al *AgentLoop) branchStorePath(agent *AgentInstance) string {
	return chatmeta.BranchStorePath(branchSessionDir(al, agent))
}

func findBranchRoot(history []providers.Message, assistantIndex int) (providers.Message, int) {
	for i := assistantIndex - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return history[i], i
		}
	}
	return providers.Message{}, -1
}

func (al *AgentLoop) recordBranchSnapshot(ts *turnState, assistant providers.Message, history []providers.Message) error {
	if ts == nil || ts.agent == nil || strings.TrimSpace(assistant.ID) == "" {
		return nil
	}
	sessionID := branchSessionID(ts)
	if sessionID == "" {
		return nil
	}
	assistantIndex := -1
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].ID == assistant.ID {
			assistantIndex = i
			break
		}
	}
	if assistantIndex < 0 {
		return fmt.Errorf("assistant message %q not found in history", assistant.ID)
	}
	root, rootIndex := findBranchRoot(history, assistantIndex)
	if rootIndex < 0 {
		return fmt.Errorf("no user message found before assistant message %q", assistant.ID)
	}
	groupID := strings.TrimSpace(root.ID)
	if groupID == "" {
		groupID = assistant.ID
	}
	createdAt := time.Now().UTC()
	if assistant.CreatedAt != nil {
		createdAt = assistant.CreatedAt.UTC()
	}
	version := chatmeta.BranchVersion{
		ID:        assistant.ID,
		Preview:   chatmeta.Preview(assistant.Content, 160),
		ModelName: assistant.ModelName,
		CreatedAt: createdAt,
		Messages:  chatmeta.CloneMessages(history[:assistantIndex+1]),
	}
	path := al.branchStorePath(ts.agent)
	al.branchStoreMu.Lock()
	defer al.branchStoreMu.Unlock()
	store, err := chatmeta.LoadBranches(path)
	if err != nil {
		return err
	}
	store.UpsertVersion(sessionID, groupID, ts.sessionKey, version)
	return chatmeta.SaveBranches(path, store)
}

func (al *AgentLoop) recordBranchSnapshotByID(agent *AgentInstance, sessionKey, sessionID, assistantID string) error {
	if agent == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(assistantID) == "" {
		return nil
	}
	history, err := session.HistoryChecked(agent.Sessions, sessionKey)
	if err != nil {
		return err
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].ID != assistantID {
			continue
		}
		root, rootIndex := findBranchRoot(history, i)
		if rootIndex < 0 {
			return fmt.Errorf("no user message found before assistant message %q", assistantID)
		}
		groupID := strings.TrimSpace(root.ID)
		if groupID == "" {
			groupID = assistantID
		}
		assistant := history[i]
		createdAt := time.Now().UTC()
		if assistant.CreatedAt != nil {
			createdAt = assistant.CreatedAt.UTC()
		}
		path := al.branchStorePath(agent)
		al.branchStoreMu.Lock()
		defer al.branchStoreMu.Unlock()
		store, loadErr := chatmeta.LoadBranches(path)
		if loadErr != nil {
			return loadErr
		}
		store.UpsertVersion(sessionID, groupID, sessionKey, chatmeta.BranchVersion{
			ID: assistant.ID, Preview: chatmeta.Preview(assistant.Content, 160),
			ModelName: assistant.ModelName, CreatedAt: createdAt,
			Messages: chatmeta.CloneMessages(history[:i+1]),
		})
		return chatmeta.SaveBranches(path, store)
	}
	return fmt.Errorf("assistant message %q not found", assistantID)
}

func (al *AgentLoop) resetSessionHistory(ctx context.Context, agent *AgentInstance, sessionKey string, history []providers.Message) error {
	if agent == nil || agent.Sessions == nil {
		return fmt.Errorf("session store is unavailable")
	}
	if al.contextManager != nil {
		if err := al.contextManager.Clear(ctx, sessionKey); err != nil {
			return fmt.Errorf("clear context manager: %w", err)
		}
	}
	if err := session.SetHistoryChecked(agent.Sessions, sessionKey, history); err != nil {
		return fmt.Errorf("replace session history: %w", err)
	}
	if err := session.SetSummaryChecked(agent.Sessions, sessionKey, ""); err != nil {
		return fmt.Errorf("clear session summary: %w", err)
	}
	if store, ok := agent.Sessions.(session.TurnCheckpointStore); ok {
		if err := store.ClearTurnCheckpoints(sessionKey); err != nil {
			return fmt.Errorf("clear turn checkpoints: %w", err)
		}
	}
	if err := agent.Sessions.Save(sessionKey); err != nil {
		return fmt.Errorf("save branch context: %w", err)
	}
	if al.contextManager != nil {
		for _, message := range history {
			if err := al.contextManager.Ingest(ctx, &IngestRequest{SessionKey: sessionKey, Message: message}); err != nil {
				return fmt.Errorf("restore branch context: %w", err)
			}
		}
	}
	return nil
}

func (al *AgentLoop) prepareChatBranchRequest(ctx context.Context, msg bus.InboundMessage, agent *AgentInstance, sessionKey string) (bus.InboundMessage, bool, error) {
	if msg.Context.Raw == nil || agent == nil {
		return msg, false, nil
	}
	regenerateID := strings.TrimSpace(msg.Context.Raw["regenerate_from_message_id"])
	editID := strings.TrimSpace(msg.Context.Raw["edit_message_id"])
	if regenerateID == "" && editID == "" {
		return msg, false, nil
	}
	history, err := session.HistoryChecked(agent.Sessions, sessionKey)
	if err != nil {
		return msg, true, err
	}

	if regenerateID != "" {
		assistantIndex := -1
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].ID == regenerateID {
				assistantIndex = i
				break
			}
		}
		if assistantIndex < 0 {
			return msg, true, fmt.Errorf("answer %q not found", regenerateID)
		}
		sessionID := strings.TrimSpace(msg.Context.Raw["session_id"])
		if sessionID == "" {
			sessionID = sessionIDFromSessionKey(sessionKey)
		}
		if err := al.recordBranchSnapshotByID(agent, sessionKey, sessionID, regenerateID); err != nil {
			return msg, true, err
		}
		user, userIndex := findBranchRoot(history, assistantIndex)
		if userIndex < 0 {
			return msg, true, fmt.Errorf("no user message before answer %q", regenerateID)
		}
		if err := al.resetSessionHistory(ctx, agent, sessionKey, history[:userIndex]); err != nil {
			return msg, true, err
		}
		msg.Content = user.Content
		msg.Media = append([]string(nil), user.Media...)
		msg.Attachments = inboundAttachmentsFromProviders(user.Attachments)
		if strings.TrimSpace(user.ID) != "" {
			msg.Context.MessageID = user.ID
			msg.MessageID = user.ID
			msg.Context.Raw["branch_group_id"] = user.ID
		}
		return msg, true, nil
	}

	targetIndex := -1
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].ID == editID {
			targetIndex = i
			break
		}
	}
	if targetIndex < 0 || history[targetIndex].Role != "user" {
		return msg, true, fmt.Errorf("user message %q not found", editID)
	}
	target := history[targetIndex]
	if err := al.resetSessionHistory(ctx, agent, sessionKey, history[:targetIndex]); err != nil {
		return msg, true, err
	}
	if len(msg.Media) == 0 && len(msg.Attachments) == 0 {
		msg.Media = append([]string(nil), target.Media...)
		msg.Attachments = inboundAttachmentsFromProviders(target.Attachments)
	}
	msg.Context.MessageID = target.ID
	msg.MessageID = target.ID
	msg.Context.Raw["branch_group_id"] = target.ID
	return msg, true, nil
}

// SelectBranch switches the live agent context to a recorded answer snapshot.
func (al *AgentLoop) SelectBranch(ctx context.Context, sessionID, versionID string) error {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(versionID) == "" {
		return fmt.Errorf("session and branch id are required")
	}
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		return fmt.Errorf("default agent is unavailable")
	}
	path := al.branchStorePath(defaultAgent)
	al.branchStoreMu.Lock()
	defer al.branchStoreMu.Unlock()
	store, err := chatmeta.LoadBranches(path)
	if err != nil {
		return err
	}
	group, version, ok := store.FindVersion(sessionID, versionID)
	if !ok {
		return fmt.Errorf("branch version %q not found", versionID)
	}
	agent := al.agentForSession(group.SessionKey)
	if agent == nil {
		return fmt.Errorf("session %q is unavailable", group.SessionKey)
	}
	if err := al.resetSessionHistory(ctx, agent, group.SessionKey, version.Messages); err != nil {
		return err
	}
	if err := store.SetActive(sessionID, group.RootMessageID, versionID); err != nil {
		return err
	}
	return chatmeta.SaveBranches(path, store)
}
