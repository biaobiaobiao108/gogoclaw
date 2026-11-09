package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/chatmeta"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers/messageutil"
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

func isChatBranchMutation(msg bus.InboundMessage) bool {
	if msg.Context.Raw == nil {
		return false
	}
	return strings.TrimSpace(msg.Context.Raw["regenerate_from_message_id"]) != "" ||
		strings.TrimSpace(msg.Context.Raw["edit_message_id"]) != ""
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

func (al *AgentLoop) branchStorePath(sessionID string) string {
	return chatmeta.BranchStorePath(
		chatmeta.BranchStoreRoot(config.GetHome()),
		sessionID,
	)
}

func (al *AgentLoop) legacyBranchStorePaths() []string {
	paths := []string{chatmeta.LegacyBranchStorePath(branchSessionDir(al, nil))}
	seen := map[string]struct{}{filepath.Clean(paths[0]): {}}
	if al == nil || al.registry == nil {
		return paths
	}
	for _, agentID := range al.registry.ListAgentIDs() {
		agent, ok := al.registry.GetAgent(agentID)
		if !ok || agent == nil || strings.TrimSpace(agent.Workspace) == "" {
			continue
		}
		path := chatmeta.LegacyBranchStorePath(filepath.Join(strings.TrimSpace(agent.Workspace), "sessions"))
		clean := filepath.Clean(path)
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func (al *AgentLoop) ensureBranchStoreMigrated() error {
	root := chatmeta.BranchStoreRoot(config.GetHome())
	var migrationErrs []error
	for _, legacyPath := range al.legacyBranchStorePaths() {
		if err := chatmeta.MigrateLegacyBranches(legacyPath, root); err != nil {
			migrationErrs = append(migrationErrs, err)
		}
	}
	return errors.Join(migrationErrs...)
}

func (al *AgentLoop) branchLock(sessionID string) *sync.Mutex {
	value, _ := al.branchLocks.LoadOrStore(sessionID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func findBranchRoot(history []providers.Message, assistantIndex int) (providers.Message, int) {
	for i := assistantIndex - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return history[i], i
		}
	}
	return providers.Message{}, -1
}

func (al *AgentLoop) recordBranchSnapshot(
	ts *turnState,
	groupID string,
	assistant providers.Message,
	history []providers.Message,
) error {
	groupID = strings.TrimSpace(groupID)
	if ts == nil || ts.agent == nil || strings.TrimSpace(assistant.ID) == "" || groupID == "" {
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
	_, rootIndex := findBranchRoot(history, assistantIndex)
	if rootIndex < 0 {
		return fmt.Errorf("no user message found before assistant message %q", assistant.ID)
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
	if err := al.ensureBranchStoreMigrated(); err != nil {
		return err
	}
	path := al.branchStorePath(sessionID)
	lock := al.branchLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	store, err := chatmeta.LoadBranches(path)
	if err != nil {
		return err
	}
	if !store.UpsertVersion(sessionID, groupID, ts.sessionKey, version) {
		return nil
	}
	return chatmeta.SaveBranches(path, store)
}

func (al *AgentLoop) recordBranchSnapshotByIDLocked(
	agent *AgentInstance,
	sessionKey, sessionID, assistantID string,
	history []providers.Message,
) error {
	if agent == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(assistantID) == "" {
		return nil
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
		if err := al.ensureBranchStoreMigrated(); err != nil {
			return err
		}
		path := al.branchStorePath(sessionID)
		store, loadErr := chatmeta.LoadBranches(path)
		if loadErr != nil {
			return loadErr
		}
		if !store.UpsertVersion(sessionID, groupID, sessionKey, chatmeta.BranchVersion{
			ID: assistant.ID, Preview: chatmeta.Preview(assistant.Content, 160),
			ModelName: assistant.ModelName, CreatedAt: createdAt,
			Messages: chatmeta.CloneMessages(history[:i+1]),
		}) {
			return nil
		}
		return chatmeta.SaveBranches(path, store)
	}
	return fmt.Errorf("assistant message %q not found", assistantID)
}

func (al *AgentLoop) recordBranchSnapshotByID(
	agent *AgentInstance,
	sessionKey, sessionID, assistantID string,
) error {
	history, err := session.TranscriptChecked(agent.Sessions, sessionKey)
	if err != nil {
		return err
	}
	lock := al.branchLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	return al.recordBranchSnapshotByIDLocked(agent, sessionKey, sessionID, assistantID, history)
}

type sessionHistoryBackup struct {
	history       []providers.Message
	contextLength int
	summary       string
	checkpoint    session.TurnCheckpoint
}

func (al *AgentLoop) captureSessionHistory(
	agent *AgentInstance,
	sessionKey string,
) (sessionHistoryBackup, error) {
	history, err := session.TranscriptChecked(agent.Sessions, sessionKey)
	if err != nil {
		return sessionHistoryBackup{}, err
	}
	summary, err := session.SummaryChecked(agent.Sessions, sessionKey)
	if err != nil {
		return sessionHistoryBackup{}, err
	}
	backup := sessionHistoryBackup{
		history: chatmeta.CloneMessages(history),
		summary: summary,
	}
	active, err := session.HistoryChecked(agent.Sessions, sessionKey)
	if err != nil {
		return sessionHistoryBackup{}, err
	}
	backup.contextLength = len(active)
	if store, ok := agent.Sessions.(session.TurnCheckpointStore); ok {
		checkpoint, err := store.GetTurnCheckpoint(sessionKey)
		if err != nil {
			return sessionHistoryBackup{}, err
		}
		backup.checkpoint = checkpoint
	}
	return backup, nil
}

func (al *AgentLoop) replaceContextHistory(
	ctx context.Context,
	sessionKey string,
	history []providers.Message,
) error {
	if al.contextManager == nil {
		return nil
	}
	if err := al.contextManager.Clear(ctx, sessionKey); err != nil {
		return fmt.Errorf("clear context manager: %w", err)
	}
	for _, message := range history {
		if err := al.contextManager.Ingest(ctx, &IngestRequest{SessionKey: sessionKey, Message: message}); err != nil {
			return fmt.Errorf("restore branch context: %w", err)
		}
	}
	return nil
}

func (al *AgentLoop) restoreSessionHistory(
	ctx context.Context,
	agent *AgentInstance,
	sessionKey string,
	backup sessionHistoryBackup,
) error {
	var restoreErrs []error
	// Context managers may clear the owning session store as part of Clear,
	// so restore the context first and write the durable history last.
	if err := al.replaceContextHistory(ctx, sessionKey, backup.history); err != nil {
		restoreErrs = append(restoreErrs, err)
	}
	if err := al.restoreStoredSessionHistory(agent, sessionKey, backup); err != nil {
		restoreErrs = append(restoreErrs, err)
	}
	return errors.Join(restoreErrs...)
}

func (al *AgentLoop) restoreStoredSessionHistory(
	agent *AgentInstance,
	sessionKey string,
	backup sessionHistoryBackup,
) error {
	var restoreErrs []error
	if err := session.SetHistoryChecked(agent.Sessions, sessionKey, backup.history); err != nil {
		restoreErrs = append(restoreErrs, fmt.Errorf("restore session history: %w", err))
	}
	if backup.contextLength < len(backup.history) {
		if err := session.TruncateHistoryChecked(agent.Sessions, sessionKey, backup.contextLength); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("restore context offset: %w", err))
		}
	}
	if err := session.SetSummaryChecked(agent.Sessions, sessionKey, backup.summary); err != nil {
		restoreErrs = append(restoreErrs, fmt.Errorf("restore session summary: %w", err))
	}
	if store, ok := agent.Sessions.(session.TurnCheckpointStore); ok {
		if err := store.SetTurnCheckpoint(sessionKey, backup.checkpoint); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("restore turn checkpoint: %w", err))
		}
	}
	if err := agent.Sessions.Save(sessionKey); err != nil {
		restoreErrs = append(restoreErrs, fmt.Errorf("save restored context: %w", err))
	}
	return errors.Join(restoreErrs...)
}

func (al *AgentLoop) resetSessionHistory(
	ctx context.Context,
	agent *AgentInstance,
	sessionKey string,
	history []providers.Message,
) (sessionHistoryBackup, error) {
	if agent == nil || agent.Sessions == nil {
		return sessionHistoryBackup{}, fmt.Errorf("session store is unavailable")
	}
	backup, err := al.captureSessionHistory(agent, sessionKey)
	if err != nil {
		return sessionHistoryBackup{}, fmt.Errorf("snapshot session history: %w", err)
	}
	rollback := func(cause error) (sessionHistoryBackup, error) {
		restoreErr := al.restoreSessionHistory(ctx, agent, sessionKey, backup)
		if restoreErr != nil {
			return sessionHistoryBackup{}, fmt.Errorf("%w (rollback failed: %v)", cause, restoreErr)
		}
		return sessionHistoryBackup{}, cause
	}
	replacement := chatmeta.CloneMessages(history)
	if err := al.replaceContextHistory(ctx, sessionKey, replacement); err != nil {
		restoreErr := al.restoreSessionHistory(ctx, agent, sessionKey, backup)
		if restoreErr != nil {
			return sessionHistoryBackup{}, fmt.Errorf("%w (rollback failed: %v)", err, restoreErr)
		}
		return sessionHistoryBackup{}, err
	}
	if err := session.SetHistoryChecked(agent.Sessions, sessionKey, replacement); err != nil {
		return rollback(fmt.Errorf("replace session history: %w", err))
	}
	if err := session.SetSummaryChecked(agent.Sessions, sessionKey, ""); err != nil {
		return rollback(fmt.Errorf("clear session summary: %w", err))
	}
	if store, ok := agent.Sessions.(session.TurnCheckpointStore); ok {
		if err := store.SetTurnCheckpoint(sessionKey, session.TurnCheckpoint{}); err != nil {
			return rollback(fmt.Errorf("clear active turn checkpoint: %w", err))
		}
	}
	if err := agent.Sessions.Save(sessionKey); err != nil {
		return rollback(fmt.Errorf("save branch context: %w", err))
	}
	return backup, nil
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
	sessionID := strings.TrimSpace(msg.Context.Raw["session_id"])
	if sessionID == "" {
		sessionID = sessionIDFromSessionKey(sessionKey)
	}
	lock := al.branchLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	history, err := session.TranscriptChecked(agent.Sessions, sessionKey)
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
		if err := al.recordBranchSnapshotByIDLocked(agent, sessionKey, sessionID, regenerateID, history); err != nil {
			return msg, true, err
		}
		user, userIndex := findBranchRoot(history, assistantIndex)
		if userIndex < 0 {
			return msg, true, fmt.Errorf("no user message before answer %q", regenerateID)
		}
		if _, err := al.resetSessionHistory(ctx, agent, sessionKey, history[:userIndex]); err != nil {
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
	if answer, ok := findBranchAnswer(history, targetIndex); ok {
		if err := al.recordBranchSnapshotByIDLocked(agent, sessionKey, sessionID, answer.ID, history); err != nil {
			return msg, true, err
		}
	}
	if _, err := al.resetSessionHistory(ctx, agent, sessionKey, history[:targetIndex]); err != nil {
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

func findBranchAnswer(history []providers.Message, userIndex int) (providers.Message, bool) {
	var answer providers.Message
	for index := userIndex + 1; index < len(history); index++ {
		if history[index].Role == "user" {
			break
		}
		if history[index].Role != "assistant" || messageutil.IsTransientAssistantThoughtMessage(history[index]) {
			continue
		}
		answer = history[index]
	}
	return answer, strings.TrimSpace(answer.ID) != ""
}

// SelectBranch switches the live agent context to a recorded answer snapshot.
func (al *AgentLoop) SelectBranch(ctx context.Context, sessionID, versionID string) error {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(versionID) == "" {
		return fmt.Errorf("session and branch id are required")
	}
	if err := al.ensureBranchStoreMigrated(); err != nil {
		return err
	}
	path := al.branchStorePath(sessionID)
	lock := al.branchLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	store, err := chatmeta.LoadBranches(path)
	if err != nil {
		return err
	}
	group, version, ok := store.FindVersion(sessionID, versionID)
	if !ok {
		return fmt.Errorf("branch version %q not found", versionID)
	}
	if al.GetActiveTurnBySession(group.SessionKey) != nil {
		return fmt.Errorf("stop the active task before switching answers")
	}
	agent := al.agentForSession(group.SessionKey)
	if agent == nil {
		return fmt.Errorf("session %q is unavailable", group.SessionKey)
	}
	if len(version.Messages) == 0 {
		return fmt.Errorf("branch version %q has no history", versionID)
	}
	backup, err := al.resetSessionHistory(ctx, agent, group.SessionKey, version.Messages)
	if err != nil {
		return err
	}
	if err := store.SetActive(sessionID, group.RootMessageID, versionID); err != nil {
		return err
	}
	if err := chatmeta.SaveBranches(path, store); err != nil {
		rollbackErr := al.restoreSessionHistory(ctx, agent, group.SessionKey, backup)
		return errors.Join(err, rollbackErr)
	}
	return nil
}
