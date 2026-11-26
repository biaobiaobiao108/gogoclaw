package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/chatmeta"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
)

const chatUIMetaVersion = 1

type chatUIMetaFile struct {
	Version  int                          `json:"version"`
	Sessions map[string]chatUISessionMeta `json:"sessions"`
}

type chatUISessionMeta struct {
	Title        string            `json:"title,omitempty"`
	TitleSource  string            `json:"title_source,omitempty"`
	TitleVersion int               `json:"title_version,omitempty"`
	Pinned       bool              `json:"pinned,omitempty"`
	Archived     bool              `json:"archived,omitempty"`
	Feedback     map[string]string `json:"feedback,omitempty"`
}

type sessionChatSource struct {
	Title   string `json:"title,omitempty"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type sessionChatProjectSource struct {
	ProjectID  string `json:"project_id"`
	FileID     string `json:"file_id"`
	Filename   string `json:"filename"`
	Locator    string `json:"locator,omitempty"`
	Snippet    string `json:"snippet,omitempty"`
	ContentURL string `json:"content_url,omitempty"`
}

type sessionSearchResult struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Preview      string `json:"preview"`
	MessageCount int    `json:"message_count"`
	Snippet      string `json:"snippet,omitempty"`
	MessageID    string `json:"message_id,omitempty"`
	MessageRole  string `json:"message_role,omitempty"`
	Created      string `json:"created"`
	Updated      string `json:"updated"`
	Pinned       bool   `json:"pinned,omitempty"`
	Archived     bool   `json:"archived,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
	ProjectName  string `json:"project_name,omitempty"`
}

type sessionBranchView struct {
	RootMessageID   string              `json:"root_message_id"`
	SessionKey      string              `json:"session_key,omitempty"`
	ActiveVersionID string              `json:"active_version_id"`
	Versions        []sessionBranchItem `json:"versions"`
}

type sessionBranchItem struct {
	ID        string `json:"id"`
	Preview   string `json:"preview"`
	ModelName string `json:"model_name,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

func newChatUIMeta() chatUIMetaFile {
	return chatUIMetaFile{Version: chatUIMetaVersion, Sessions: make(map[string]chatUISessionMeta)}
}

func (h *Handler) loadChatUIMeta(dir string) (chatUIMetaFile, error) {
	meta := newChatUIMeta()
	store, err := memory.OpenSQLiteStoreReadOnly(dir)
	if errors.Is(err, os.ErrNotExist) {
		return meta, nil
	}
	if err != nil {
		return meta, err
	}
	defer store.Close()
	entries, err := store.ListChatUIMeta(context.Background())
	if err != nil {
		return meta, err
	}
	for id, data := range entries {
		var item chatUISessionMeta
		if err := json.Unmarshal(data, &item); err != nil {
			return meta, fmt.Errorf("decode chat metadata for %s: %w", id, err)
		}
		meta.Sessions[id] = item
	}
	return meta, nil
}

func (h *Handler) updateChatUISessionMeta(dir, sessionID string, update func(*chatUISessionMeta) error) error {
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.UpdateChatUIMeta(context.Background(), sessionID, func(data []byte) ([]byte, error) {
		var meta chatUISessionMeta
		if len(data) > 0 {
			if err := json.Unmarshal(data, &meta); err != nil {
				return nil, err
			}
		}
		if err := update(&meta); err != nil {
			return nil, err
		}
		if strings.TrimSpace(meta.Title) == "" && meta.TitleSource == "" && !meta.Pinned && !meta.Archived && len(meta.Feedback) == 0 {
			return nil, nil
		}
		return json.Marshal(meta)
	})
}

func (h *Handler) removeChatUISessionMeta(dir, sessionID string) error {
	store, err := memory.OpenSQLiteStore(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer store.Close()
	return store.UpdateChatUIMeta(context.Background(), sessionID, func([]byte) ([]byte, error) { return nil, nil })
}

func (h *Handler) readChatUIMeta(dir string) chatUIMetaFile {
	meta, err := h.loadChatUIMeta(dir)
	if err != nil {
		logger.Warnf("failed to read chat metadata: %v", err)
		return newChatUIMeta()
	}
	return meta
}

func (h *Handler) cleanupDeletedSessionData(dir, sessionID string) error {
	return errors.Join(
		h.removeChatUISessionMeta(dir, sessionID),
		h.cleanupDeletedSessionAttachments(dir, sessionID),
		chatmeta.RemoveBranchSession(dir, sessionID),
	)
}

func (h *Handler) cleanupDeletedSessionAttachments(dir, sessionID string) error {
	var cleanupErrs []error
	if validAttachmentSessionID(sessionID) {
		dirs, err := h.sessionDirs()
		if err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("resolve attachment directories: %w", err))
		} else {
			h.attachmentMu.Lock()
			dirs = append([]string{dir}, dirs...)
			seen := make(map[string]struct{}, len(dirs))
			for _, sessionsDir := range dirs {
				clean := filepath.Clean(sessionsDir)
				if _, ok := seen[clean]; ok {
					continue
				}
				seen[clean] = struct{}{}
				if err := os.RemoveAll(filepath.Join(clean, ".attachments", sessionID)); err != nil {
					cleanupErrs = append(cleanupErrs, fmt.Errorf("remove attachments: %w", err))
				}
				delete(h.attachmentCleanupAt, filepath.Join(clean, ".attachments", sessionID))
			}
			h.attachmentMu.Unlock()
		}
	}
	return errors.Join(cleanupErrs...)
}

func applySessionMeta(item *sessionListItem, meta chatUISessionMeta) {
	if item == nil {
		return
	}
	if custom := strings.TrimSpace(meta.Title); custom != "" && meta.TitleSource != "auto" {
		item.Title = custom
	}
	item.Pinned = meta.Pinned
	item.Archived = meta.Archived
}

func (h *Handler) sessionExists(dir, sessionID string) bool {
	if _, err := h.findPicoJSONLSession(dir, sessionID); err == nil {
		return true
	}
	_, err := h.findLegacyPicoSession(dir, sessionID)
	return err == nil
}

func (h *Handler) readSessionBranches(dir, sessionID string) ([]chatmeta.BranchGroup, error) {
	store, err := chatmeta.LoadBranches(dir, sessionID)
	if err != nil {
		return nil, err
	}
	return store.VisibleGroups(sessionID), nil
}

func publicBranchGroups(groups []chatmeta.BranchGroup) []sessionBranchView {
	views := make([]sessionBranchView, 0, len(groups))
	for _, group := range groups {
		view := sessionBranchView{
			RootMessageID: group.RootMessageID, SessionKey: group.SessionKey,
			ActiveVersionID: group.ActiveVersionID,
		}
		for _, version := range group.Versions {
			item := sessionBranchItem{ID: version.ID, Preview: version.Preview, ModelName: version.ModelName}
			if !version.CreatedAt.IsZero() {
				item.CreatedAt = version.CreatedAt.Format(time.RFC3339)
			}
			view.Versions = append(view.Versions, item)
		}
		if len(view.Versions) > 1 {
			views = append(views, view)
		}
	}
	return views
}

func (h *Handler) handlePatchSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if strings.TrimSpace(sessionID) == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	dir, err := h.sessionDirForSession(sessionID)
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	if !h.sessionExists(dir, sessionID) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	var body struct {
		Title     *string         `json:"title"`
		Pinned    *bool           `json:"pinned"`
		Archived  *bool           `json:"archived"`
		ProjectID json.RawMessage `json:"project_id"`
	}
	if err := decodeJSONRequestWithLimit(w, r, 32*1024, &body); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid session metadata", http.StatusBadRequest)
		return
	}
	if err := h.updateChatUISessionMeta(dir, sessionID, func(item *chatUISessionMeta) error {
		if body.Title != nil {
			item.Title = strings.TrimSpace(*body.Title)
			item.TitleSource = "manual"
			item.TitleVersion = 0
		}
		if body.Pinned != nil {
			item.Pinned = *body.Pinned
		}
		if body.Archived != nil {
			item.Archived = *body.Archived
		}
		return nil
	}); err != nil {
		http.Error(w, "failed to save session metadata", http.StatusInternalServerError)
		return
	}
	if body.ProjectID != nil {
		projectID := ""
		if string(bytes.TrimSpace(body.ProjectID)) != "null" {
			if err := json.Unmarshal(body.ProjectID, &projectID); err != nil {
				http.Error(w, "invalid project id", http.StatusBadRequest)
				return
			}
		}
		store, err := memory.NewSQLiteStore(dir)
		if err != nil {
			http.Error(w, "failed to open session store", http.StatusInternalServerError)
			return
		}
		err = store.SetSessionChatProject(r.Context(), sessionID, projectID)
		closeErr := store.Close()
		if errors.Is(err, memory.ErrChatProjectNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		if err != nil || closeErr != nil {
			http.Error(w, "failed to move session", http.StatusInternalServerError)
			return
		}
	}

	meta := h.readChatUIMeta(dir)
	projectID, projectName := "", ""
	if store, openErr := memory.OpenSQLiteStoreReadOnly(dir); openErr == nil {
		if project, projectErr := store.SessionChatProject(r.Context(), sessionID); projectErr == nil && project != nil {
			projectID, projectName = project.ID, project.Name
		}
		_ = store.Close()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": sessionID, "title": meta.Sessions[sessionID].Title, "pinned": meta.Sessions[sessionID].Pinned,
		"archived": meta.Sessions[sessionID].Archived, "project_id": projectID, "project_name": projectName,
	})
}

func (h *Handler) handleSetSessionFeedback(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	messageID := strings.TrimSpace(r.PathValue("message_id"))
	if sessionID == "" || messageID == "" {
		http.Error(w, "missing session or message id", http.StatusBadRequest)
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := decodeJSONRequestWithLimit(w, r, 8*1024, &body); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid feedback", http.StatusBadRequest)
		return
	}
	body.Value = strings.ToLower(strings.TrimSpace(body.Value))
	if body.Value != "" && body.Value != "up" && body.Value != "down" {
		http.Error(w, "feedback value must be up or down", http.StatusBadRequest)
		return
	}
	dir, err := h.sessionDirForSession(sessionID)
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	if !h.sessionExists(dir, sessionID) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if err := h.updateChatUISessionMeta(dir, sessionID, func(item *chatUISessionMeta) error {
		if item.Feedback == nil {
			item.Feedback = make(map[string]string)
		}
		if body.Value == "" {
			delete(item.Feedback, messageID)
		} else {
			item.Feedback[messageID] = body.Value
		}
		return nil
	}); err != nil {
		http.Error(w, "failed to save feedback", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message_id": messageID, "value": body.Value})
}
