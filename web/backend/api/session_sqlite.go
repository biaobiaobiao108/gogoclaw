package api

import (
	"context"
	"errors"
	"os"
	"strconv"

	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
)

func (h *Handler) scanSQLiteSessionRefs(dir string) ([]picoJSONLSessionRef, error) {
	store, err := memory.OpenSQLiteStore(dir)
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
	store, err := memory.OpenSQLiteStore(dir)
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
	store, err := memory.OpenSQLiteStore(dir)
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
	store, err := memory.OpenSQLiteStore(dir)
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
