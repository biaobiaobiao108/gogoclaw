package api

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/instance"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
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

type managedSessionCursor struct {
	Updated int64  `json:"u"`
	Key     string `json:"k"`
}

type managedSessionPage struct {
	Items      []managedSession `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

const managedSessionRuntimeConflict = "stop the running GoGoClaw instance before deleting sessions"

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
	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve workspaces", 500)
		return
	}
	query := r.URL.Query()
	limit := 30
	if value, err := strconv.Atoi(query.Get("limit")); err == nil && value > 0 {
		limit = min(value, 100)
	}
	var after *memory.SessionMeta
	if encoded := query.Get("cursor"); encoded != "" {
		raw, err := base64.RawURLEncoding.DecodeString(encoded)
		var cursor managedSessionCursor
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Key == "" {
			http.Error(w, "invalid session cursor", http.StatusBadRequest)
			return
		}
		after = &memory.SessionMeta{Key: cursor.Key, UpdatedAt: time.Unix(0, cursor.Updated)}
	}
	result := managedSessionPage{Items: []managedSession{}}
	store, err := memory.OpenSQLiteStoreReadOnly(dir)
	if errors.Is(err, os.ErrNotExist) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	if err != nil {
		http.Error(w, "failed to open session store", 500)
		return
	}
	defer store.Close()
	metas, err := store.ListManagedSessionPage(r.Context(), query.Get("q"), after, limit+1)
	if err != nil {
		http.Error(w, "failed to read sessions", 500)
		return
	}
	if len(metas) > limit {
		metas = metas[:limit]
		last := metas[len(metas)-1]
		raw, _ := json.Marshal(managedSessionCursor{last.UpdatedAt.UnixNano(), last.Key})
		result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	for _, meta := range metas {
		var scope session.SessionScope
		_ = json.Unmarshal(meta.Scope, &scope)
		result.Items = append(result.Items, managedSession{managedID(dir, meta.Key), meta.Key, scope, meta.Count, meta.UpdatedAt.Format(time.RFC3339)})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
func (h *Handler) getManagedSession(w http.ResponseWriter, r *http.Request) {
	ref, err := h.managedRef(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid session", 400)
		return
	}
	store, err := memory.OpenSQLiteStoreReadOnly(ref.Dir)
	if err != nil {
		http.Error(w, "session store unavailable", 404)
		return
	}
	defer store.Close()
	meta, err := store.ManagedSessionDetailMeta(r.Context(), ref.Key)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "session not found", 404)
		return
	}
	if err != nil {
		http.Error(w, "failed to read session", 500)
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
	turn := decodeTurnCheckpoint(meta.Turn)
	var cfg *config.Config
	if len(turn.Deliveries) > 0 {
		cfg, _ = config.LoadConfig(h.configPath)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"messages": page.Messages, "next_cursor": page.NextCursor, "has_more": page.HasMore,
		"deliveries": recentPlatformDeliveries(turn, cfg),
	})
}

type managedDelivery struct {
	ID            string     `json:"id"`
	MessageID     string     `json:"message_id,omitempty"`
	Preview       string     `json:"preview,omitempty"`
	Channel       string     `json:"channel"`
	Status        string     `json:"status"`
	UpdatedAt     time.Time  `json:"updated_at"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
}

func recentPlatformDeliveries(turn session.TurnCheckpoint, cfg *config.Config) []managedDelivery {
	deliveries := make([]managedDelivery, 0, 5)
	for i := len(turn.Deliveries) - 1; i >= 0 && len(deliveries) < 5; i-- {
		d := turn.Deliveries[i]
		if d.PlatformTracked {
			deliveries = append(deliveries, managedDelivery{
				ID: d.ID, MessageID: d.MessageID, Channel: d.Channel, Status: d.Status,
				UpdatedAt: d.UpdatedAt, NextAttemptAt: d.NextAttemptAt,
				Preview: utils.Truncate(strings.Join(strings.Fields(cfg.RedactDiagnostic(d.Content)), " "), 120),
			})
		}
	}
	return deliveries
}

func (h *Handler) deleteManagedSession(w http.ResponseWriter, r *http.Request) {
	ref, err := h.managedRef(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid session", 400)
		return
	}
	if h.gatewayAvailableForProxy() {
		http.Error(w, managedSessionRuntimeConflict, http.StatusConflict)
		return
	}
	// Cover launcher starts before the child has acquired the runtime lock.
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if gateway.cmd != nil && gateway.cmd.Process != nil {
		http.Error(w, managedSessionRuntimeConflict, http.StatusConflict)
		return
	}
	lock, err := instance.AcquireDataDirLock(globalConfigDir())
	if errors.Is(err, fileutil.ErrFileLockHeld) {
		http.Error(w, managedSessionRuntimeConflict, http.StatusConflict)
		return
	}
	if err != nil {
		logger.Warnf("failed to lock the data directory for session deletion: %v", err)
		http.Error(w, "failed to lock the data directory", http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := lock.Release(); err != nil {
			logger.Warnf("failed to release the data directory lock: %v", err)
		}
	}()
	store, err := memory.OpenSQLiteStore(ref.Dir)
	if err != nil {
		http.Error(w, "session store unavailable", 404)
		return
	}
	defer store.Close()
	meta, err := store.GetSessionMeta(r.Context(), ref.Key)
	if err != nil {
		http.Error(w, "failed to read session", http.StatusInternalServerError)
		return
	}
	var cleanupID string
	var removed bool
	if meta.CreatedAt.IsZero() {
		cleanupID, err = store.PendingSessionCleanup(r.Context(), ref.Key)
	} else {
		pico, _ := sessionRefFromMeta(meta)
		cleanupID = pico.ID
		removed, err = store.DeleteManagedSession(r.Context(), ref.Key, cleanupID)
	}
	if err != nil {
		logger.Warnf("failed to delete session %s: %v", ref.Key, err)
		http.Error(w, "failed to delete session", http.StatusInternalServerError)
		return
	}
	if !removed && cleanupID == "" {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if cleanupID != "" {
		err := h.cleanupDeletedSessionAttachments(ref.Dir, cleanupID)
		if err == nil {
			err = store.CompleteSessionCleanup(r.Context(), ref.Key)
		}
		if err != nil {
			logger.Warnf("failed to clean chat data for deleted session %s: %v", cleanupID, err)
			h.pruneSessionListCache(nil, ref.Dir)
			http.Error(w, "session deleted but failed to clean associated data", http.StatusInternalServerError)
			return
		}
	}
	h.pruneSessionListCache(nil, ref.Dir)
	w.WriteHeader(http.StatusNoContent)
}
