package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

type managedSessionID struct {
	Dir string `json:"d"`
	Key string `json:"k"`
}
type managedSession struct {
	ID      string               `json:"id"`
	Key     string               `json:"key"`
	Scope   session.SessionScope `json:"scope"`
	Count   int                  `json:"count"`
	Updated string               `json:"updated"`
}

func managedID(dir, key string) string {
	b, _ := json.Marshal(managedSessionID{dir, key})
	return base64.RawURLEncoding.EncodeToString(b)
}
func (h *Handler) managedRef(id string) (managedSessionID, error) {
	var ref managedSessionID
	b, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return ref, err
	}
	if err = json.Unmarshal(b, &ref); err != nil || ref.Key == "" {
		return ref, errors.New("invalid session")
	}
	dirs, err := h.sessionDirs()
	if err != nil {
		return ref, err
	}
	for _, dir := range dirs {
		if filepath.Clean(dir) == filepath.Clean(ref.Dir) {
			return ref, nil
		}
	}
	return ref, errors.New("unknown workspace")
}
func (h *Handler) registerManagedSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/session-management", h.listManagedSessions)
	mux.HandleFunc("GET /api/session-management/{id}", h.getManagedSession)
	mux.HandleFunc("DELETE /api/session-management/{id}", h.deleteManagedSession)
}
func (h *Handler) listManagedSessions(w http.ResponseWriter, r *http.Request) {
	dirs, err := h.sessionDirs()
	if err != nil {
		http.Error(w, "failed to resolve workspaces", 500)
		return
	}
	result := []managedSession{}
	for _, dir := range dirs {
		store, err := memory.OpenSQLiteStore(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			http.Error(w, "failed to open session store", 500)
			return
		}
		metas, err := store.ListSessionMeta(r.Context())
		closeErr := store.Close()
		if err != nil || closeErr != nil {
			http.Error(w, "failed to read sessions", 500)
			return
		}
		for _, meta := range metas {
			var scope session.SessionScope
			_ = json.Unmarshal(meta.Scope, &scope)
			result = append(result, managedSession{managedID(dir, meta.Key), meta.Key, scope, meta.Count, meta.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Updated > result[j].Updated })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
func (h *Handler) getManagedSession(w http.ResponseWriter, r *http.Request) {
	ref, err := h.managedRef(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid session", 400)
		return
	}
	store, err := memory.OpenSQLiteStore(ref.Dir)
	if err != nil {
		http.Error(w, "session store unavailable", 404)
		return
	}
	defer store.Close()
	meta, err := store.GetSessionMeta(r.Context(), ref.Key)
	if err != nil || meta.CreatedAt.IsZero() {
		http.Error(w, "session not found", 404)
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	page, err := store.GetTranscriptPage(r.Context(), ref.Key, 100, before)
	if err != nil {
		http.Error(w, "failed to read session", 500)
		return
	}
	if pico, ok := sessionRefFromMeta(meta); ok {
		for i := range page.Messages {
			for j := range page.Messages[i].Attachments {
				attachment := &page.Messages[i].Attachments[j]
				if url, ok := sessionAttachmentURL(pico.ID, *attachment); ok {
					attachment.URL = url
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"messages": page.Messages, "next_cursor": page.NextCursor, "has_more": page.HasMore})
}
func (h *Handler) deleteManagedSession(w http.ResponseWriter, r *http.Request) {
	ref, err := h.managedRef(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid session", 400)
		return
	}
	if h.gatewayAvailableForProxy() {
		http.Error(w, "stop gateway before deleting sessions", http.StatusConflict)
		return
	}
	// Serialize with launcher-controlled gateway starts while modifying both stores.
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if gateway.cmd != nil && gateway.cmd.Process != nil {
		http.Error(w, "stop gateway before deleting sessions", http.StatusConflict)
		return
	}
	store, err := memory.OpenSQLiteStore(ref.Dir)
	if err != nil {
		http.Error(w, "session store unavailable", 404)
		return
	}
	defer store.Close()
	meta, err := store.GetSessionMeta(r.Context(), ref.Key)
	if err != nil || meta.CreatedAt.IsZero() {
		http.Error(w, "session not found", 404)
		return
	}
	removed, err := store.DeleteSession(r.Context(), ref.Key)
	if err != nil {
		logger.Warnf("failed to delete session %s: %v", ref.Key, err)
		http.Error(w, "failed to delete session", http.StatusInternalServerError)
		return
	}
	if !removed {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if pico, ok := sessionRefFromMeta(meta); ok {
		if err := h.cleanupDeletedSessionData(ref.Dir, pico.ID); err != nil {
			logger.Warnf("failed to clean chat data for deleted session %s: %v", pico.ID, err)
			h.pruneSessionListCache(nil, ref.Dir)
			http.Error(w, "session deleted but failed to clean associated data", http.StatusInternalServerError)
			return
		}
	}
	h.pruneSessionListCache(nil, ref.Dir)
	w.WriteHeader(http.StatusNoContent)
}
