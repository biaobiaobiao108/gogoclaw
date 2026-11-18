package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
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

func hasLegacySessionFiles(entries []os.DirEntry) bool {
	for _, entry := range entries {
		if !entry.IsDir() {
			ext := filepath.Ext(entry.Name())
			if ext == ".jsonl" || ext == ".json" {
				return true
			}
		}
	}
	return false
}

func (h *Handler) readSQLiteSessionListPage(
	ctx context.Context, dir string, maxArgs int, archived bool, cursor *sessionListCursor, limit int,
) (sessionListResponse, error) {
	response := sessionListResponse{Items: []sessionListItem{}}
	store, err := memory.OpenSQLiteStore(dir)
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
	for len(response.Items) <= limit {
		batchSize := limit + 1 - len(response.Items)
		entries, err := store.ListPicoSessionPage(ctx, legacyPicoSessionPrefix, archived, after, batchSize)
		if err != nil {
			return response, err
		}
		if len(entries) == 0 {
			break
		}
		byKey := make(map[string]memory.PicoSessionListEntry, len(entries))
		for _, entry := range entries {
			byKey[entry.Key] = entry
		}
		summaries := make(map[string]sessionListItem, len(entries))
		err = store.VisitPicoSessionTranscripts(ctx, entries, func(key string, messages []providers.Message) {
			entry := byKey[key]
			sess := sessionFile{Created: entry.CreatedAt, Updated: entry.UpdatedAt, Messages: messages}
			summaries[key] = buildSessionListItem(entry.ID, sess, maxArgs)
		})
		if err != nil {
			return response, err
		}
		for _, entry := range entries {
			item, found := summaries[entry.Key]
			if item.MessageCount == 0 && !entry.HasSummary {
				continue
			}
			if !found {
				item = buildSessionListItem(entry.ID, sessionFile{Created: entry.CreatedAt, Updated: entry.UpdatedAt}, maxArgs)
			}
			var ui chatUISessionMeta
			_ = json.Unmarshal(entry.UIMeta, &ui)
			applySessionMeta(&item, ui)
			h.ensureSessionTitle(dir, entry.ID, &item, chatUIMetaFile{Sessions: map[string]chatUISessionMeta{entry.ID: ui}})
			response.Items = append(response.Items, item)
		}
		if len(entries) < batchSize {
			break
		}
		after = &entries[len(entries)-1]
	}
	if len(response.Items) > limit {
		response.HasMore = true
		response.Items = response.Items[:limit]
		response.NextCursor = encodeSessionListCursor(response.Items[limit-1])
	}
	return response, nil
}
