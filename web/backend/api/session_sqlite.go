package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

// InitializeSessionStore upgrades an existing workspace before read-only routes
// run, including when Launcher starts before Gateway.
func (h *Handler) InitializeSessionStore() error {
	dir, err := h.sessionsDir()
	if err != nil {
		return err
	}
	store, err := memory.OpenSQLiteStore(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.InterruptChatProjectIndexes(ctx); err != nil {
		return err
	}
	pending, err := store.PendingChatProjectCleanup(ctx)
	if err != nil {
		return err
	}
	for key, target := range pending {
		if err := cleanChatProjectData(ctx, dir, store, key, string(target)); err != nil {
			logger.Warnf("retry deleted project data cleanup %s: %v", key, err)
		}
	}
	return nil
}

func (h *Handler) scanSQLiteSessionRefs(dir string) ([]picoJSONLSessionRef, error) {
	store, err := memory.OpenSQLiteStoreReadOnly(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer store.Close()

	metas, err := store.ListSessionMeta(context.Background())
	if err != nil {
		return nil, err
	}
	refs := make([]picoJSONLSessionRef, 0, len(metas))
	seen := make(map[string]struct{}, len(metas))
	for _, meta := range metas {
		if meta.Count <= meta.Skip && meta.Summary == "" {
			continue
		}
		ref, ok := sessionRefFromMeta(meta)
		if !ok || ref.ID == "" || ref.Key == "" {
			continue
		}
		if _, exists := seen[ref.ID]; exists {
			continue
		}
		seen[ref.ID] = struct{}{}
		ref.Storage = "sqlite"
		refs = append(refs, ref)
	}
	return refs, nil
}

func (h *Handler) readSQLiteSessionInfo(dir, sessionKey string) (sessionFile, bool, error) {
	store, err := memory.OpenSQLiteStoreReadOnly(dir)
	if errors.Is(err, os.ErrNotExist) {
		return sessionFile{}, false, nil
	}
	if err != nil {
		return sessionFile{}, false, err
	}
	defer store.Close()

	meta, err := store.GetSessionMeta(context.Background(), sessionKey)
	if err != nil {
		return sessionFile{}, false, err
	}
	exists, err := store.SessionExists(context.Background(), sessionKey)
	if err != nil {
		return sessionFile{}, false, err
	}
	if !exists || (meta.Count <= meta.Skip && meta.Summary == "") {
		return sessionFile{}, false, nil
	}
	return sessionFileFromMeta(meta), true, nil
}

func (h *Handler) readSQLiteSession(dir, sessionKey string) (sessionFile, bool, error) {
	sess, found, err := h.readSQLiteSessionInfo(dir, sessionKey)
	if err != nil || !found {
		return sess, found, err
	}
	store, err := memory.OpenSQLiteStoreReadOnly(dir)
	if err != nil {
		return sessionFile{}, false, err
	}
	defer store.Close()
	messages, err := store.GetTranscript(context.Background(), sessionKey)
	if err != nil {
		return sessionFile{}, false, err
	}
	sess.Messages = messages
	return sess, true, nil
}

func (h *Handler) readSQLiteSessionListItem(dir, sessionKey, sessionID string, maxArgs int) (sessionListItem, bool, error) {
	sess, found, err := h.readSQLiteSession(dir, sessionKey)
	if err != nil || !found {
		return sessionListItem{}, found, err
	}
	item := buildSessionListItem(sessionID, sess, maxArgs)
	if item.MessageCount == 0 && sess.Summary == "" {
		return sessionListItem{}, false, nil
	}
	return item, true, nil
}

func (h *Handler) readSQLiteSessionPage(dir, sessionKey string, limit int, beforeID int64) (sessionMessagesPage, error) {
	store, err := memory.OpenSQLiteStoreReadOnly(dir)
	if err != nil {
		return sessionMessagesPage{}, err
	}
	defer store.Close()
	page, err := store.GetTranscriptPage(context.Background(), sessionKey, limit, beforeID)
	if err != nil {
		return sessionMessagesPage{}, err
	}
	result := sessionMessagesPage{Messages: page.Messages, HasMore: page.HasMore}
	if page.NextCursor > 0 {
		result.NextCursor = strconv.FormatInt(page.NextCursor, 10)
	}
	return result, nil
}

func sessionFileFromMeta(meta memory.SessionMeta) sessionFile {
	return sessionFile{
		Key:         meta.Key,
		Summary:     meta.Summary,
		Created:     meta.CreatedAt,
		Updated:     meta.UpdatedAt,
		Turn:        decodeTurnCheckpoint(meta.Turn),
		TurnHistory: decodeTurnCheckpointHistory(meta.TurnHistory),
	}
}

func (h *Handler) readSQLiteSessionListPage(
	ctx context.Context, dir string, archived bool, projectFilter string, cursor *sessionListCursor, limit int,
) (sessionListResponse, error) {
	response := sessionListResponse{Items: []sessionListItem{}}
	store, err := memory.OpenSQLiteStoreReadOnly(dir)
	if errors.Is(err, os.ErrNotExist) {
		return response, nil
	}
	if err != nil {
		return response, err
	}
	defer store.Close()

	var after *memory.PicoSessionListEntry
	if cursor != nil {
		updated, err := time.Parse(time.RFC3339Nano, cursor.Updated)
		if err != nil {
			return response, err
		}
		after = &memory.PicoSessionListEntry{ID: cursor.ID, Pinned: cursor.Pinned, UpdatedAt: updated}
	}
	fetchLimit := limit
	if limit > 0 {
		fetchLimit++
	}
	entries, err := store.ListPicoSessionPage(ctx, legacyPicoSessionPrefix, archived, projectFilter, after, fetchLimit)
	if err != nil {
		return response, err
	}
	if limit > 0 && len(entries) > limit {
		response.HasMore = true
		entries = entries[:limit]
	}
	for _, entry := range entries {
		sess := sessionFile{Created: entry.CreatedAt, Updated: entry.UpdatedAt}
		if len(entry.PreviewMessage) != 0 {
			var preview providers.Message
			if err := json.Unmarshal(entry.PreviewMessage, &preview); err != nil {
				return response, err
			}
			sess.Messages = []providers.Message{preview}
		}
		item := buildSessionListItem(entry.ID, sess, 0)
		item.MessageCount = entry.VisibleCount
		var ui chatUISessionMeta
		_ = json.Unmarshal(entry.UIMeta, &ui)
		applySessionMeta(&item, ui)
		item.ProjectID = entry.ProjectID
		item.ProjectName = entry.ProjectName
		h.ensureSessionTitle(dir, entry.ID, &item, chatUIMetaFile{Sessions: map[string]chatUISessionMeta{entry.ID: ui}})
		response.Items = append(response.Items, item)
	}
	if response.HasMore {
		response.NextCursor = encodeSessionListCursor(response.Items[limit-1])
	}
	return response, nil
}
