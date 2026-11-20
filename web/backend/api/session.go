package api

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers/messageutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

// registerSessionRoutes binds session list and detail endpoints to the ServeMux.
func (h *Handler) registerSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions", h.handleListSessions)
	mux.HandleFunc("GET /api/sessions/search", h.handleSearchSessions)
	mux.HandleFunc("GET /api/sessions/{id}/branches", h.handleListBranches)
	mux.HandleFunc("PATCH /api/sessions/{id}", h.handlePatchSession)
	mux.HandleFunc("PUT /api/sessions/{id}/feedback/{message_id}", h.handleSetSessionFeedback)
	mux.HandleFunc("GET /api/sessions/{id}", h.handleGetSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", h.handleDeleteSession)
	mux.HandleFunc("GET /api/uploads/config", h.handleUploadConfig)
	mux.HandleFunc("POST /api/uploads", h.handleUpload)
	mux.HandleFunc("DELETE /api/uploads", h.handleDeleteUpload)
	mux.HandleFunc("GET /api/attachments/{session_id}/{name}", h.handleAttachment)
}

// sessionFile mirrors the on-disk session JSON structure from pkg/session.
type sessionFile struct {
	Key         string                   `json:"key"`
	Messages    []providers.Message      `json:"messages"`
	Summary     string                   `json:"summary,omitempty"`
	Created     time.Time                `json:"created"`
	Updated     time.Time                `json:"updated"`
	Turn        session.TurnCheckpoint   `json:"turn,omitempty"`
	TurnHistory []session.TurnCheckpoint `json:"turn_history,omitempty"`
}

// sessionListItem is a lightweight summary returned by GET /api/sessions.
type sessionListItem struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Preview      string `json:"preview"`
	MessageCount int    `json:"message_count"`
	Created      string `json:"created"`
	Updated      string `json:"updated"`
	Pinned       bool   `json:"pinned,omitempty"`
	Archived     bool   `json:"archived,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
	ProjectName  string `json:"project_name,omitempty"`
	updatedAt    time.Time
}

type sessionListResponse struct {
	Items      []sessionListItem `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
}

type sessionListCursor struct {
	Pinned  bool   `json:"pinned"`
	Updated string `json:"updated"`
	ID      string `json:"id"`
}

type cachedSessionListItem struct {
	item  sessionListItem
	stamp string
}

type cachedSessionRefs struct {
	stamp      string
	jsonlRefs  []picoJSONLSessionRef
	legacyRefs []picoLegacySessionRef
}

type sessionChatMessage struct {
	ID             string                     `json:"message_id,omitempty"`
	TurnID         string                     `json:"turn_id,omitempty"`
	Role           string                     `json:"role"`
	Content        string                     `json:"content"`
	Kind           string                     `json:"kind,omitempty"`
	ModelName      string                     `json:"model_name,omitempty"`
	CreatedAt      *time.Time                 `json:"created_at,omitempty"`
	Media          []string                   `json:"media,omitempty"`
	Attachments    []sessionChatAttachment    `json:"attachments,omitempty"`
	ToolCalls      []utils.VisibleToolCall    `json:"tool_calls,omitempty"`
	Sources        []sessionChatSource        `json:"sources,omitempty"`
	ProjectSources []sessionChatProjectSource `json:"project_sources,omitempty"`
}

type sessionMessagesPage struct {
	Messages   []providers.Message
	NextCursor string
	HasMore    bool
}

type sessionChatAttachment struct {
	ID          string `json:"id,omitempty"`
	Type        string `json:"type,omitempty"`
	URL         string `json:"url,omitempty"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

// legacyPicoSessionPrefix is the legacy key prefix used by older Pico JSON/JSONL
// sessions before structured scope metadata existed.
const (
	legacyPicoSessionPrefix = "agent:main:pico:direct:pico:"
	picoSessionPrefix       = legacyPicoSessionPrefix

	// Upper bound for legacy JSONL compatibility reads. SQLite sessions do not
	// use line scanning and are not subject to this limit.
	maxSessionJSONLLineSize = 64 * 1024 * 1024
	maxSessionPreviewRunes  = 60

	handledToolResponseSummaryText = messageutil.HandledToolResponseSummary
)

func defaultToolFeedbackMaxArgsLength() int {
	defaults := config.AgentDefaults{}
	return defaults.GetToolFeedbackMaxArgsLength()
}

// extractLegacyPicoSessionID extracts the session UUID from an old Pico key.
// Returns the UUID and true if the key matches the Pico session pattern.
func extractLegacyPicoSessionID(key string) (string, bool) {
	if strings.HasPrefix(key, legacyPicoSessionPrefix) {
		return strings.TrimPrefix(key, legacyPicoSessionPrefix), true
	}
	return "", false
}

func sanitizeSessionKey(key string) string {
	key = strings.ReplaceAll(key, ":", "_")
	key = strings.ReplaceAll(key, "/", "_")
	key = strings.ReplaceAll(key, "\\", "_")
	return key
}

func pageLegacySessionMessages(messages []providers.Message, limit, before int) sessionMessagesPage {
	end := len(messages)
	if before > 0 {
		end = min(end, before-1)
	}
	if limit <= 0 {
		return sessionMessagesPage{Messages: messages[:end]}
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	page := sessionMessagesPage{
		Messages: messages[start:end],
		HasMore:  start > 0,
	}
	if start > 0 {
		page.NextCursor = strconv.Itoa(start + 1)
	}
	return page
}

func (h *Handler) readLegacySession(path string) (sessionFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionFile{}, err
	}

	var sess sessionFile
	if err := json.Unmarshal(data, &sess); err != nil {
		return sessionFile{}, err
	}
	return sess, nil
}

func (h *Handler) readSessionMeta(path, sessionKey string) (memory.SessionMeta, error) {
	data, err := fileutil.ReadFileShared(path)
	if os.IsNotExist(err) {
		return memory.SessionMeta{Key: sessionKey}, nil
	}
	if err != nil {
		return memory.SessionMeta{}, err
	}

	var meta memory.SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return memory.SessionMeta{}, err
	}
	if meta.Key == "" {
		meta.Key = sessionKey
	}
	return meta, nil
}

func (h *Handler) readSessionMessages(path string, skip int) ([]providers.Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	msgs := make([]providers.Message, 0)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSessionJSONLLineSize)

	seen := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		seen++
		if seen <= skip {
			continue
		}

		var msg providers.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if messageutil.IsTransientAssistantThoughtMessage(msg) {
			continue
		}
		msgs = append(msgs, msg)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return msgs, nil
}

func (h *Handler) readSessionMessagesPage(
	path string,
	skip, limit, beforeLine int,
) (sessionMessagesPage, error) {
	f, err := os.Open(path)
	if err != nil {
		return sessionMessagesPage{}, err
	}
	defer f.Close()

	type lineMessage struct {
		line    int
		message providers.Message
	}
	if limit > 200 {
		limit = 200
	}
	records := make([]lineMessage, 0)
	next := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSessionJSONLLineSize)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if beforeLine > 0 && lineNumber >= beforeLine {
			break
		}
		if lineNumber <= skip {
			continue
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg providers.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if messageutil.IsTransientAssistantThoughtMessage(msg) {
			continue
		}
		record := lineMessage{line: lineNumber, message: msg}
		if limit > 0 && len(records) == limit+1 {
			records[next] = record
			next = (next + 1) % len(records)
		} else {
			records = append(records, record)
		}
	}
	if err := scanner.Err(); err != nil {
		return sessionMessagesPage{}, err
	}
	if next > 0 {
		records = append(append(make([]lineMessage, 0, len(records)), records[next:]...), records[:next]...)
	}

	if limit <= 0 {
		messages := make([]providers.Message, 0, len(records))
		for _, record := range records {
			messages = append(messages, record.message)
		}
		return sessionMessagesPage{Messages: messages}, nil
	}
	start := len(records) - limit
	if start < 0 {
		start = 0
	}
	page := sessionMessagesPage{
		Messages: make([]providers.Message, 0, len(records)-start),
		HasMore:  start > 0,
	}
	for _, record := range records[start:] {
		page.Messages = append(page.Messages, record.message)
	}
	if start > 0 {
		page.NextCursor = strconv.Itoa(records[start].line)
	}
	return page, nil
}

func (h *Handler) readJSONLSessionInfo(dir, sessionKey string) (sessionFile, string, error) {
	if sess, found, err := h.readSQLiteSessionInfo(dir, sessionKey); err != nil {
		return sessionFile{}, "", err
	} else if found {
		return sess, "", nil
	}
	base := filepath.Join(dir, sanitizeSessionKey(sessionKey))
	jsonlPath := base + ".jsonl"
	metaPath := base + ".meta.json"
	if _, err := os.Stat(jsonlPath); err != nil {
		return sessionFile{}, "", err
	}

	meta, err := h.readSessionMeta(metaPath, sessionKey)
	if err != nil {
		return sessionFile{}, "", err
	}

	updated := meta.UpdatedAt
	created := meta.CreatedAt
	if created.IsZero() || updated.IsZero() {
		if info, statErr := os.Stat(jsonlPath); statErr == nil {
			if created.IsZero() {
				created = info.ModTime()
			}
			if updated.IsZero() {
				updated = info.ModTime()
			}
		}
	}

	return sessionFile{
		Key:         meta.Key,
		Summary:     meta.Summary,
		Created:     created,
		Updated:     updated,
		Turn:        decodeTurnCheckpoint(meta.Turn),
		TurnHistory: decodeTurnCheckpointHistory(meta.TurnHistory),
	}, jsonlPath, nil
}

func (h *Handler) readJSONLSession(dir, sessionKey string) (sessionFile, error) {
	if sess, found, err := h.readSQLiteSession(dir, sessionKey); err != nil {
		return sessionFile{}, err
	} else if found {
		return sess, nil
	}
	sess, jsonlPath, err := h.readJSONLSessionInfo(dir, sessionKey)
	if err != nil {
		return sessionFile{}, err
	}
	messages, err := h.readSessionMessages(jsonlPath, 0)
	if err != nil {
		return sessionFile{}, err
	}
	sess.Messages = messages
	return sess, nil
}

func decodeTurnCheckpoint(raw json.RawMessage) session.TurnCheckpoint {
	var checkpoint session.TurnCheckpoint
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &checkpoint)
	}
	return checkpoint
}

func decodeTurnCheckpointHistory(raw []json.RawMessage) []session.TurnCheckpoint {
	if len(raw) == 0 {
		return nil
	}
	items := make([]session.TurnCheckpoint, 0, len(raw))
	for _, item := range raw {
		checkpoint := decodeTurnCheckpoint(item)
		if checkpoint.TurnID != "" {
			items = append(items, checkpoint)
		}
	}
	return items
}

type picoJSONLSessionRef struct {
	ID      string
	Key     string
	Storage string
}

type picoLegacySessionRef struct {
	ID   string
	Path string
}

func extractPicoSessionIDFromScope(scope session.SessionScope) (string, bool) {
	if !strings.EqualFold(strings.TrimSpace(scope.Channel), "pico") {
		return "", false
	}

	candidates := []string{
		strings.TrimSpace(scope.Values["sender"]),
		strings.TrimSpace(scope.Values["chat"]),
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if idx := strings.Index(candidate, "pico:"); idx >= 0 {
			sessionID := strings.TrimSpace(candidate[idx+len("pico:"):])
			if sessionID != "" {
				return sessionID, true
			}
		}
	}
	return "", false
}

func sessionRefFromMeta(meta memory.SessionMeta) (picoJSONLSessionRef, bool) {
	if len(meta.Scope) == 0 {
		if sessionID, ok := extractLegacyPicoSessionID(meta.Key); ok {
			return picoJSONLSessionRef{ID: sessionID, Key: meta.Key}, true
		}
		for _, alias := range meta.Aliases {
			if sessionID, ok := extractLegacyPicoSessionID(alias); ok {
				return picoJSONLSessionRef{ID: sessionID, Key: meta.Key}, true
			}
		}
		return picoJSONLSessionRef{}, false
	}
	var scope session.SessionScope
	if err := json.Unmarshal(meta.Scope, &scope); err != nil {
		return picoJSONLSessionRef{}, false
	}
	sessionID, ok := extractPicoSessionIDFromScope(scope)
	if !ok {
		if legacySessionID, ok := extractLegacyPicoSessionID(meta.Key); ok {
			return picoJSONLSessionRef{ID: legacySessionID, Key: meta.Key}, true
		}
		for _, alias := range meta.Aliases {
			if legacySessionID, ok := extractLegacyPicoSessionID(alias); ok {
				return picoJSONLSessionRef{ID: legacySessionID, Key: meta.Key}, true
			}
		}
		return picoJSONLSessionRef{}, false
	}
	return picoJSONLSessionRef{ID: sessionID, Key: meta.Key}, true
}

func sessionDirectoryStamp(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	hasher := fnv.New64a()
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		fmt.Fprintf(hasher, "%s\x00%d\x00%d\x00", entry.Name(), info.Size(), info.ModTime().UnixNano())
	}
	return strconv.FormatUint(hasher.Sum64(), 16), nil
}

func (h *Handler) findPicoJSONLSessions(dir string) ([]picoJSONLSessionRef, error) {
	cached, err := h.loadSessionRefs(dir)
	if err != nil {
		return nil, err
	}
	return append([]picoJSONLSessionRef(nil), cached.jsonlRefs...), nil
}

func (h *Handler) scanPicoJSONLSessions(dir string) ([]picoJSONLSessionRef, error) {
	sqliteRefs, err := h.scanSQLiteSessionRefs(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	refs := append([]picoJSONLSessionRef(nil), sqliteRefs...)
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		seen[ref.ID] = struct{}{}
	}
	metaBackedBases := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		name := entry.Name()
		metaPath := filepath.Join(dir, name)
		meta, err := h.readSessionMeta(metaPath, "")
		if err != nil {
			continue
		}
		ref, ok := sessionRefFromMeta(meta)
		if !ok || ref.Key == "" || ref.ID == "" {
			continue
		}
		metaBackedBases[strings.TrimSuffix(name, ".meta.json")] = struct{}{}
		if _, exists := seen[ref.ID]; exists {
			continue
		}
		seen[ref.ID] = struct{}{}
		refs = append(refs, ref)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		name := entry.Name()
		base := strings.TrimSuffix(name, ".jsonl")
		if _, ok := metaBackedBases[base]; ok {
			continue
		}
		ref, ok := jsonlSessionRefFromFilename(name)
		if !ok || ref.Key == "" || ref.ID == "" {
			continue
		}
		if _, exists := seen[ref.ID]; exists {
			continue
		}
		seen[ref.ID] = struct{}{}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (h *Handler) findPicoJSONLSession(dir, sessionID string) (picoJSONLSessionRef, error) {
	refs, err := h.findPicoJSONLSessions(dir)
	if err != nil {
		return picoJSONLSessionRef{}, err
	}
	for _, ref := range refs {
		if ref.ID == sessionID {
			return ref, nil
		}
	}
	return picoJSONLSessionRef{}, os.ErrNotExist
}

func (h *Handler) findLegacyPicoSessions(dir string) ([]picoLegacySessionRef, error) {
	cached, err := h.loadSessionRefs(dir)
	if err != nil {
		return nil, err
	}
	return append([]picoLegacySessionRef(nil), cached.legacyRefs...), nil
}

func (h *Handler) loadSessionRefs(dir string) (cachedSessionRefs, error) {
	stamp, err := sessionDirectoryStamp(dir)
	if err != nil {
		return cachedSessionRefs{}, err
	}
	key := filepath.Clean(dir)
	h.sessionListMu.Lock()
	cached, ok := h.sessionRefCache[key]
	h.sessionListMu.Unlock()
	if ok && cached.stamp == stamp {
		return cached, nil
	}

	jsonlRefs, err := h.scanPicoJSONLSessions(dir)
	if err != nil {
		return cachedSessionRefs{}, err
	}
	legacyRefs, err := h.scanLegacyPicoSessions(dir)
	if err != nil {
		return cachedSessionRefs{}, err
	}
	entry := cachedSessionRefs{
		stamp:      stamp,
		jsonlRefs:  append([]picoJSONLSessionRef(nil), jsonlRefs...),
		legacyRefs: append([]picoLegacySessionRef(nil), legacyRefs...),
	}
	h.sessionListMu.Lock()
	h.sessionRefCache[key] = entry
	h.sessionListMu.Unlock()
	return entry, nil
}

func (h *Handler) scanLegacyPicoSessions(dir string) ([]picoLegacySessionRef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	refs := make([]picoLegacySessionRef, 0)
	seen := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".json" || strings.HasSuffix(name, ".meta.json") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		sess, err := h.readLegacySession(path)
		if err != nil || isEmptySession(sess) {
			continue
		}

		sessionID, ok := extractLegacyPicoSessionID(sess.Key)
		if !ok || sessionID == "" {
			continue
		}
		if _, exists := seen[sessionID]; exists {
			continue
		}
		seen[sessionID] = struct{}{}
		refs = append(refs, picoLegacySessionRef{ID: sessionID, Path: path})
	}
	return refs, nil
}

func jsonlSessionRefFromFilename(name string) (picoJSONLSessionRef, bool) {
	if !strings.HasSuffix(name, ".jsonl") {
		return picoJSONLSessionRef{}, false
	}
	base := strings.TrimSuffix(name, ".jsonl")
	if base == "" {
		return picoJSONLSessionRef{}, false
	}

	legacyPrefix := sanitizeSessionKey(legacyPicoSessionPrefix)
	if strings.HasPrefix(base, legacyPrefix) {
		sessionID := strings.TrimPrefix(base, legacyPrefix)
		if sessionID == "" {
			return picoJSONLSessionRef{}, false
		}
		return picoJSONLSessionRef{
			ID:  sessionID,
			Key: legacyPicoSessionPrefix + sessionID,
		}, true
	}

	if session.IsOpaqueSessionKey(base) {
		return picoJSONLSessionRef{
			ID:  base,
			Key: base,
		}, true
	}

	return picoJSONLSessionRef{}, false
}

func (h *Handler) findLegacyPicoSession(dir, sessionID string) (picoLegacySessionRef, error) {
	refs, err := h.findLegacyPicoSessions(dir)
	if err != nil {
		return picoLegacySessionRef{}, err
	}
	for _, ref := range refs {
		if ref.ID == sessionID {
			return ref, nil
		}
	}
	return picoLegacySessionRef{}, os.ErrNotExist
}

func (h *Handler) sessionsDir() (string, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return "", err
	}

	return resolveSessionsDir(cfg.Agents.Defaults.Workspace), nil
}

func (h *Handler) sessionDirs() ([]string, error) {
	dir, err := h.sessionsDir()
	if err != nil {
		return nil, err
	}
	return []string{filepath.Clean(dir)}, nil
}

func (h *Handler) sessionDirForSession(sessionID string) (string, error) {
	dirs, err := h.sessionDirs()
	if err != nil {
		return "", err
	}
	for _, dir := range dirs {
		if h.sessionExists(dir, sessionID) {
			return dir, nil
		}
	}
	if len(dirs) == 0 {
		return "", fmt.Errorf("sessions directory is unavailable")
	}
	return dirs[0], nil
}

func (h *Handler) sessionRuntimeSettings() (string, int, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return "", 0, err
	}

	return resolveSessionsDir(cfg.Agents.Defaults.Workspace), cfg.Agents.Defaults.GetToolFeedbackMaxArgsLength(), nil
}

func resolveSessionsDir(workspace string) string {
	return filepath.Join(resolveWorkspacePath(workspace), "sessions")
}

func resolveWorkspacePath(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		home, _ := os.UserHomeDir()
		workspace = filepath.Join(home, ".gogoclaw", "workspace")
	}
	if workspace == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(workspace, "~/") || strings.HasPrefix(workspace, `~\`) {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, workspace[2:])
	}
	return workspace
}

// handleListSessions returns a list of Pico session summaries.
//
//	GET /api/sessions
func (h *Handler) handleListSessions(w http.ResponseWriter, r *http.Request) {
	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	archivedOnly := r.URL.Query().Get("archived") == "1" || r.URL.Query().Get("archived") == "true"
	projectFilter := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectFilter != "" && projectFilter != "none" && !validAttachmentSessionID(projectFilter) {
		http.Error(w, "invalid project filter", http.StatusBadRequest)
		return
	}
	unpaginated := r.URL.Query().Get("limit") == "" && r.URL.Query().Get("cursor") == ""
	limit := 30
	if val, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && val > 0 {
		limit = min(val, 100)
	}
	cursor, cursorErr := decodeSessionListCursor(r.URL.Query().Get("cursor"))
	if cursorErr != nil {
		http.Error(w, "invalid session cursor", http.StatusBadRequest)
		return
	}
	if unpaginated {
		limit = -1
	}
	response, err := h.readSQLiteSessionListPage(r.Context(), dir, archivedOnly, projectFilter, cursor, limit)
	if err != nil {
		logger.Warnf("failed to page sessions: %v", err)
		http.Error(w, "failed to read sessions", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if unpaginated {
		json.NewEncoder(w).Encode(response.Items)
		return
	}
	json.NewEncoder(w).Encode(response)
}

func encodeSessionListCursor(item sessionListItem) string {
	data, err := json.Marshal(sessionListCursor{
		Pinned:  item.Pinned,
		Updated: item.updatedAt.UTC().Format(time.RFC3339Nano),
		ID:      item.ID,
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeSessionListCursor(raw string) (*sessionListCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var cursor sessionListCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return nil, err
	}
	if cursor.Updated == "" || cursor.ID == "" {
		return nil, fmt.Errorf("incomplete cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.Updated); err != nil {
		if _, fallbackErr := time.Parse(time.RFC3339, cursor.Updated); fallbackErr != nil {
			return nil, err
		}
	}
	return &cursor, nil
}

// handleGetSession returns the full message history for a specific session.
//
//	GET /api/sessions/{id}
func (h *Handler) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	dir, toolFeedbackMaxArgsLength, err := h.sessionRuntimeSettings()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}

	limit := 80
	if value, parseErr := strconv.Atoi(r.URL.Query().Get("limit")); parseErr == nil && value > 0 {
		limit = value
	}
	if limit > 200 {
		limit = 200
	}
	beforeLine := 0
	if rawCursor := strings.TrimSpace(r.URL.Query().Get("cursor")); rawCursor != "" {
		value, parseErr := strconv.Atoi(rawCursor)
		if parseErr != nil || value <= 0 {
			http.Error(w, "invalid message cursor", http.StatusBadRequest)
			return
		}
		beforeLine = value
	}

	ref, refErr := h.findPicoJSONLSession(dir, sessionID)
	var sess sessionFile
	var page sessionMessagesPage
	err = refErr
	if refErr == nil {
		sess, _, err = h.readJSONLSessionInfo(dir, ref.Key)
		if err == nil {
			if ref.Storage == "sqlite" {
				page, err = h.readSQLiteSessionPage(dir, ref.Key, limit, int64(beforeLine))
			} else {
				base := filepath.Join(dir, sanitizeSessionKey(ref.Key))
				page, err = h.readSessionMessagesPage(base+".jsonl", 0, limit, beforeLine)
			}
			sess.Messages = page.Messages
		}
	}
	if err == nil && isEmptySession(sess) {
		err = os.ErrNotExist
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if legacyRef, legacyErr := h.findLegacyPicoSession(dir, sessionID); legacyErr == nil {
				sess, err = h.readLegacySession(legacyRef.Path)
				if err == nil {
					page = pageLegacySessionMessages(sess.Messages, limit, beforeLine)
					sess.Messages = page.Messages
				}
			}
			if err == nil && isEmptySession(sess) {
				err = os.ErrNotExist
			}
		}
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.Error(w, "session not found", http.StatusNotFound)
			} else {
				http.Error(w, "failed to parse session", http.StatusInternalServerError)
			}
			return
		}
	}

	for i := range sess.Messages {
		if sess.Messages[i].CreatedAt == nil {
			sess.Messages[i].CreatedAt = &sess.Updated
		}
	}
	messages := detailSessionMessages(sessionID, sess.Messages, toolFeedbackMaxArgsLength)
	projectSources := h.loadSessionProjectSources(r.Context(), dir, sessionID, messages)
	applyProjectSourcesToMessages(messages, projectSources)
	uiMeta := h.readChatUIMeta(dir)
	item := buildSessionListItem(sessionID, sess, toolFeedbackMaxArgsLength)
	applySessionMeta(&item, uiMeta.Sessions[sessionID])
	projectID, projectName := "", ""
	if store, openErr := memory.OpenSQLiteStoreReadOnly(dir); openErr == nil {
		if project, projectErr := store.SessionChatProject(r.Context(), sessionID); projectErr == nil && project != nil {
			projectID, projectName = project.ID, project.Name
		}
		_ = store.Close()
	}
	branches, branchErr := h.readSessionBranches(dir, sessionID)
	if branchErr != nil {
		logger.Warnf("failed to read chat branches for session %s: %v", sessionID, branchErr)
		branches = nil
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":           sessionID,
		"title":        item.Title,
		"messages":     messages,
		"next_cursor":  page.NextCursor,
		"has_more":     page.HasMore,
		"summary":      sess.Summary,
		"created":      sess.Created.Format(time.RFC3339),
		"updated":      sess.Updated.Format(time.RFC3339),
		"turn":         sess.Turn,
		"turn_history": sess.TurnHistory,
		"feedback":     uiMeta.Sessions[sessionID].Feedback,
		"branches":     publicBranchGroups(branches),
		"project_id":   projectID,
		"project_name": projectName,
	})
}

func (h *Handler) loadSessionProjectSources(ctx context.Context, dir, sessionID string, messages []sessionChatMessage) map[string][]sessionChatProjectSource {
	turnIDs := make([]string, 0)
	seen := make(map[string]struct{})
	for _, message := range messages {
		turnID := strings.TrimSpace(message.TurnID)
		if turnID == "" || message.Role != "assistant" {
			continue
		}
		if _, ok := seen[turnID]; ok {
			continue
		}
		seen[turnID] = struct{}{}
		turnIDs = append(turnIDs, turnID)
	}
	if len(turnIDs) == 0 {
		return nil
	}
	store, err := memory.OpenSQLiteStoreReadOnly(dir)
	if err != nil {
		return nil
	}
	defer store.Close()
	stored, err := store.ChatProjectTurnSources(ctx, sessionID, turnIDs)
	if err != nil {
		return nil
	}
	result := make(map[string][]sessionChatProjectSource, len(stored))
	for turnID, sources := range stored {
		for _, source := range sources {
			contentURL := ""
			if source.ContentAvailable {
				contentURL = projectFileContentURL(source.ProjectID, source.FileID)
			}
			result[turnID] = append(result[turnID], sessionChatProjectSource{
				ProjectID: source.ProjectID, FileID: source.FileID, Filename: source.Filename,
				Locator: source.Locator, Snippet: source.Snippet,
				ContentURL: contentURL,
			})
		}
	}
	return result
}

func applyProjectSourcesToMessages(messages []sessionChatMessage, sources map[string][]sessionChatProjectSource) {
	if len(sources) == 0 {
		return
	}
	lastAssistant := make(map[string]int)
	for i, message := range messages {
		if message.Role == "assistant" && strings.TrimSpace(message.TurnID) != "" {
			lastAssistant[message.TurnID] = i
		}
	}
	for turnID, index := range lastAssistant {
		messages[index].ProjectSources = append([]sessionChatProjectSource(nil), sources[turnID]...)
	}
}

func sessionTurnMayBeActive(status string) bool {
	switch status {
	case "running", "awaiting_approval", "resuming":
		return true
	default:
		return false
	}
}

// handleDeleteSession deletes a specific session.
//
//	DELETE /api/sessions/{id}
func (h *Handler) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}

	removed := false
	ref, err := h.findPicoJSONLSession(dir, sessionID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warnf("failed to find session %s: %v", sessionID, err)
		http.Error(w, "failed to find session", http.StatusInternalServerError)
		return
	}
	refFound := err == nil
	legacyRef, err := h.findLegacyPicoSession(dir, sessionID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warnf("failed to find legacy session %s: %v", sessionID, err)
		http.Error(w, "failed to find session", http.StatusInternalServerError)
		return
	}
	legacyFound := err == nil
	if refFound {
		base := filepath.Join(dir, sanitizeSessionKey(ref.Key))
		if sess, _, err := h.readJSONLSessionInfo(dir, ref.Key); err != nil {
			http.Error(w, "failed to read session", http.StatusInternalServerError)
			return
		} else if sessionTurnMayBeActive(sess.Turn.Status) && h.gatewayAvailableForProxy() {
			http.Error(w, "stop the active task before deleting this session", http.StatusConflict)
			return
		}
		if ref.Storage == "sqlite" {
			store, openErr := memory.OpenSQLiteStore(dir)
			if openErr != nil {
				http.Error(w, "failed to open session store", http.StatusInternalServerError)
				return
			}
			removed, openErr = store.DeletePicoSession(r.Context(), ref.Key, sessionID, h.gatewayAvailableForProxy())
			closeErr := store.Close()
			if errors.Is(openErr, memory.ErrPicoInboundInFlight) {
				http.Error(w, "stop the gateway before deleting a session with an in-flight request", http.StatusConflict)
				return
			}
			if openErr != nil || closeErr != nil {
				http.Error(w, "failed to delete session", http.StatusInternalServerError)
				return
			}
		} else {
			if err := clearSessionSQLite(r, dir, ref.Key, sessionID, h.gatewayAvailableForProxy()); err != nil {
				writeSessionCleanupError(w, err)
				return
			}
			for _, path := range []string{base + ".jsonl", base + ".meta.json"} {
				if err := os.Remove(path); err != nil {
					if os.IsNotExist(err) {
						continue
					}
					http.Error(w, "failed to delete session", http.StatusInternalServerError)
					return
				}
				removed = true
			}
		}
	}

	if legacyFound {
		legacy, err := h.readLegacySession(legacyRef.Path)
		if err != nil {
			http.Error(w, "failed to read session", http.StatusInternalServerError)
			return
		}
		if sessionTurnMayBeActive(legacy.Turn.Status) && h.gatewayAvailableForProxy() {
			http.Error(w, "stop the active task before deleting this session", http.StatusConflict)
			return
		}
		if err := clearSessionSQLite(r, dir, legacy.Key, sessionID, h.gatewayAvailableForProxy()); err != nil {
			writeSessionCleanupError(w, err)
			return
		}
		if err := os.Remove(legacyRef.Path); err != nil {
			if !os.IsNotExist(err) {
				http.Error(w, "failed to delete session", http.StatusInternalServerError)
				return
			}
		} else {
			removed = true
		}
	}

	if !removed {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if cleanupErr := h.cleanupDeletedSessionData(dir, sessionID); cleanupErr != nil {
		logger.Warnf("failed to clean chat data for deleted session %s: %v", sessionID, cleanupErr)
		http.Error(w, "session deleted but failed to clean associated data", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
