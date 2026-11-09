package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/biaobiaobiao108/gogoclaw/pkg/chatmeta"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

const chatUIMetaVersion = 1

const (
	maxSessionAttachmentCount = 200
	maxSessionAttachmentBytes = 256 << 20
	orphanAttachmentAge       = 24 * time.Hour
)

type chatUIMetaFile struct {
	Version  int                          `json:"version"`
	Sessions map[string]chatUISessionMeta `json:"sessions"`
}

type chatUISessionMeta struct {
	Title    string            `json:"title,omitempty"`
	Pinned   bool              `json:"pinned,omitempty"`
	Archived bool              `json:"archived,omitempty"`
	Feedback map[string]string `json:"feedback,omitempty"`
}

type sessionChatSource struct {
	Title   string `json:"title,omitempty"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
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

func chatUIMetaPath(dir string) string {
	return filepath.Join(dir, ".gogoclaw", "chat-ui.json")
}

func chatUIMetaDir(dir string) string {
	return filepath.Join(dir, ".gogoclaw", "chat-ui")
}

func chatUISessionMetaPath(dir, sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return filepath.Join(chatUIMetaDir(dir), fmt.Sprintf("%x.json", sum[:16]))
}

func newChatUIMeta() chatUIMetaFile {
	return chatUIMetaFile{Version: chatUIMetaVersion, Sessions: make(map[string]chatUISessionMeta)}
}

func (h *Handler) loadChatUIMeta(dir string) (chatUIMetaFile, error) {
	meta := newChatUIMeta()
	if err := h.migrateLegacyChatUIMeta(dir); err != nil {
		return chatUIMetaFile{}, err
	}
	entries, err := os.ReadDir(chatUIMetaDir(dir))
	if errors.Is(err, os.ErrNotExist) {
		return meta, nil
	}
	if err != nil {
		return chatUIMetaFile{}, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(chatUIMetaDir(dir), entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return chatUIMetaFile{}, err
		}
		var item struct {
			SessionID string            `json:"session_id"`
			Meta      chatUISessionMeta `json:"meta"`
		}
		if err := json.Unmarshal(data, &item); err != nil {
			return chatUIMetaFile{}, err
		}
		if item.SessionID != "" {
			meta.Sessions[item.SessionID] = item.Meta
		}
	}
	return meta, nil
}

func (h *Handler) migrateLegacyChatUIMeta(dir string) error {
	legacyPath := chatUIMetaPath(dir)
	data, err := os.ReadFile(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var legacy chatUIMetaFile
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	for sessionID, meta := range legacy.Sessions {
		path := chatUISessionMetaPath(dir, sessionID)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := writeChatUISessionMeta(path, sessionID, meta); err != nil {
			return err
		}
	}
	return os.Remove(legacyPath)
}

func writeChatUISessionMeta(path, sessionID string, meta chatUISessionMeta) error {
	if strings.TrimSpace(meta.Title) == "" && !meta.Pinned && !meta.Archived && len(meta.Feedback) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		SessionID string            `json:"session_id"`
		Meta      chatUISessionMeta `json:"meta"`
	}{
		SessionID: sessionID,
		Meta:      meta,
	})
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0o644)
}

func (h *Handler) updateChatUISessionMeta(
	dir, sessionID string,
	update func(*chatUISessionMeta) error,
) error {
	h.sessionMetaMu.Lock()
	defer h.sessionMetaMu.Unlock()
	if err := h.migrateLegacyChatUIMeta(dir); err != nil {
		return err
	}
	path := chatUISessionMetaPath(dir, sessionID)
	var item struct {
		SessionID string            `json:"session_id"`
		Meta      chatUISessionMeta `json:"meta"`
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &item); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := update(&item.Meta); err != nil {
		return err
	}
	return writeChatUISessionMeta(path, sessionID, item.Meta)
}

func (h *Handler) removeChatUISessionMeta(dir, sessionID string) error {
	h.sessionMetaMu.Lock()
	defer h.sessionMetaMu.Unlock()
	if err := h.migrateLegacyChatUIMeta(dir); err != nil {
		return err
	}
	err := os.Remove(chatUISessionMetaPath(dir, sessionID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (h *Handler) readChatUIMeta(dir string) chatUIMetaFile {
	h.sessionMetaMu.Lock()
	defer h.sessionMetaMu.Unlock()
	meta, err := h.loadChatUIMeta(dir)
	if err != nil {
		return newChatUIMeta()
	}
	return meta
}

func (h *Handler) cleanupDeletedSessionData(dir, sessionID string) error {
	var cleanupErrs []error
	if err := h.removeChatUISessionMeta(dir, sessionID); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("remove chat metadata: %w", err))
	}
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
	branchPath := chatmeta.BranchStorePath(chatmeta.BranchStoreRoot(config.GetHome()), sessionID)
	if err := chatmeta.RemoveBranchSession(branchPath, sessionID); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("remove answer branches: %w", err))
	}
	return errors.Join(cleanupErrs...)
}

func applySessionMeta(item *sessionListItem, meta chatUISessionMeta) {
	if item == nil {
		return
	}
	if custom := strings.TrimSpace(meta.Title); custom != "" {
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
	root := chatmeta.BranchStoreRoot(config.GetHome())
	if err := chatmeta.MigrateLegacyBranches(chatmeta.LegacyBranchStorePath(dir), root); err != nil {
		return nil, err
	}
	store, err := chatmeta.LoadBranches(chatmeta.BranchStorePath(root, sessionID))
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
		Title    *string `json:"title"`
		Pinned   *bool   `json:"pinned"`
		Archived *bool   `json:"archived"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&body); err != nil {
		http.Error(w, "invalid session metadata", http.StatusBadRequest)
		return
	}
	if err := h.updateChatUISessionMeta(dir, sessionID, func(item *chatUISessionMeta) error {
		if body.Title != nil {
			item.Title = strings.TrimSpace(*body.Title)
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

	meta := h.readChatUIMeta(dir)
	writeJSON(w, http.StatusOK, map[string]any{"id": sessionID, "title": meta.Sessions[sessionID].Title, "pinned": meta.Sessions[sessionID].Pinned, "archived": meta.Sessions[sessionID].Archived})
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
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&body); err != nil {
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

func (h *Handler) handleSearchSessions(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusOK, []sessionSearchResult{})
		return
	}
	limit := 30
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 {
		limit = min(value, 100)
	}
	dir, toolFeedbackMaxArgsLength, err := h.sessionRuntimeSettings()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	uiMeta := h.readChatUIMeta(dir)
	normalizedQuery := strings.ToLower(query)
	archivedOnly := r.URL.Query().Get("archived") == "1" || r.URL.Query().Get("archived") == "true"
	results := make([]sessionSearchResult, 0)

	refs, refsErr := h.findPicoJSONLSessions(dir)
	if refsErr != nil && !errors.Is(refsErr, os.ErrNotExist) {
		http.Error(w, "failed to scan sessions", http.StatusInternalServerError)
		return
	}
	for _, ref := range refs {
		if r.Context().Err() != nil {
			return
		}
		base := filepath.Join(dir, sanitizeSessionKey(ref.Key))
		cacheKey := "jsonl:" + filepath.Clean(dir) + ":" + ref.ID
		item, cached := h.cachedSessionListItem(cacheKey, base+".jsonl", base+".meta.json")
		if !cached {
			sess, loadErr := h.readJSONLSession(dir, ref.Key)
			if loadErr != nil || isEmptySession(sess) {
				continue
			}
			item = buildSessionListItem(ref.ID, sess, toolFeedbackMaxArgsLength)
			applySessionMeta(&item, uiMeta.Sessions[ref.ID])
			if item.Archived != archivedOnly {
				continue
			}
			if result, ok := searchSessionResultForItem(item, normalizedQuery); ok {
				results = append(results, result)
			} else if result, ok := searchSessionMessages(item, sess.Messages, normalizedQuery, query); ok {
				results = append(results, result)
			}
			continue
		}
		applySessionMeta(&item, uiMeta.Sessions[ref.ID])
		if item.Archived != archivedOnly {
			continue
		}
		if result, ok := searchSessionResultForItem(item, normalizedQuery); ok {
			results = append(results, result)
			continue
		}
		result, ok, searchErr := h.searchJSONLSession(dir, ref.Key, item, normalizedQuery, query, r.Context())
		if searchErr != nil {
			logger.Warnf("failed to search session %s: %v", ref.ID, searchErr)
			continue
		}
		if ok {
			results = append(results, result)
		}
	}

	legacyRefs, legacyErr := h.findLegacyPicoSessions(dir)
	if legacyErr != nil && !errors.Is(legacyErr, os.ErrNotExist) {
		http.Error(w, "failed to scan legacy sessions", http.StatusInternalServerError)
		return
	}
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		seen[result.ID] = struct{}{}
	}
	for _, ref := range legacyRefs {
		if r.Context().Err() != nil {
			return
		}
		if _, exists := seen[ref.ID]; exists {
			continue
		}
		sess, loadErr := h.readLegacySession(ref.Path)
		if loadErr != nil || isEmptySession(sess) {
			continue
		}
		item := buildSessionListItem(ref.ID, sess, toolFeedbackMaxArgsLength)
		applySessionMeta(&item, uiMeta.Sessions[ref.ID])
		if item.Archived != archivedOnly {
			continue
		}
		if result, ok := searchSessionResultForItem(item, normalizedQuery); ok {
			results = append(results, result)
		} else if result, ok := searchSessionMessages(item, sess.Messages, normalizedQuery, query); ok {
			results = append(results, result)
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Pinned != results[j].Pinned {
			return results[i].Pinned
		}
		return results[i].Updated > results[j].Updated
	})
	if len(results) > limit {
		results = results[:limit]
	}
	writeJSON(w, http.StatusOK, results)
}

func searchSessionResultForItem(item sessionListItem, normalizedQuery string) (sessionSearchResult, bool) {
	base := sessionSearchResult{
		ID: item.ID, Title: item.Title, Preview: item.Preview, MessageCount: item.MessageCount,
		Created: item.Created, Updated: item.Updated, Pinned: item.Pinned, Archived: item.Archived,
	}
	if strings.Contains(strings.ToLower(item.Title), normalizedQuery) || strings.Contains(strings.ToLower(item.Preview), normalizedQuery) {
		base.Snippet = item.Preview
		return base, true
	}
	return sessionSearchResult{}, false
}

func searchSessionMessages(
	item sessionListItem,
	messages []providers.Message,
	normalizedQuery, rawQuery string,
) (sessionSearchResult, bool) {
	base := sessionSearchResult{
		ID: item.ID, Title: item.Title, Preview: item.Preview, MessageCount: item.MessageCount,
		Created: item.Created, Updated: item.Updated, Pinned: item.Pinned, Archived: item.Archived,
	}
	for _, message := range messages {
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if !strings.Contains(strings.ToLower(message.Content), normalizedQuery) {
			continue
		}
		base.MessageID = message.ID
		base.MessageRole = message.Role
		base.Snippet = searchSnippet(message.Content, rawQuery, 120)
		return base, true
	}
	return sessionSearchResult{}, false
}

func (h *Handler) searchJSONLSession(
	dir, sessionKey string,
	item sessionListItem,
	normalizedQuery, rawQuery string,
	ctx context.Context,
) (sessionSearchResult, bool, error) {
	base := filepath.Join(dir, sanitizeSessionKey(sessionKey))
	meta, err := h.readSessionMeta(base+".meta.json", sessionKey)
	if err != nil {
		return sessionSearchResult{}, false, err
	}
	file, err := os.Open(base + ".jsonl")
	if err != nil {
		return sessionSearchResult{}, false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSessionJSONLLineSize)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if lineNumber <= meta.Skip {
			continue
		}
		if lineNumber%256 == 0 && ctx.Err() != nil {
			return sessionSearchResult{}, false, ctx.Err()
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var message providers.Message
		if err := json.Unmarshal(line, &message); err != nil {
			continue
		}
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if !strings.Contains(strings.ToLower(message.Content), normalizedQuery) {
			continue
		}
		result := sessionSearchResult{
			ID: item.ID, Title: item.Title, Preview: item.Preview, MessageCount: item.MessageCount,
			Created: item.Created, Updated: item.Updated, Pinned: item.Pinned, Archived: item.Archived,
			MessageID: message.ID, MessageRole: message.Role,
			Snippet: searchSnippet(message.Content, rawQuery, 120),
		}
		return result, true, nil
	}
	if err := scanner.Err(); err != nil {
		return sessionSearchResult{}, false, err
	}
	return sessionSearchResult{}, false, nil
}

func searchSnippet(content, query string, limit int) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= limit {
		return string(runes)
	}
	normalizedContent := strings.ToLower(string(runes))
	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	byteIndex := strings.Index(normalizedContent, normalizedQuery)
	index := -1
	if byteIndex >= 0 {
		index = utf8.RuneCountInString(normalizedContent[:byteIndex])
	}
	if index < 0 {
		return string(runes[:limit]) + "…"
	}
	start := max(0, index-limit/3)
	end := min(len(runes), start+limit)
	if end-start < limit {
		start = max(0, end-limit)
	}
	prefix := ""
	suffix := ""
	if start > 0 {
		prefix = "…"
	}
	if end < len(runes) {
		suffix = "…"
	}
	return prefix + string(runes[start:end]) + suffix
}

var searchSourcePattern = regexp.MustCompile(`(?m)^\s*\d+\.\s+([^\n]+)\r?\n\s*(https?://[^\s]+)\s*$`)

func extractSessionSources(content string) []sessionChatSource {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	sources := make([]sessionChatSource, 0)
	for _, match := range searchSourcePattern.FindAllStringSubmatch(content, -1) {
		sources = append(sources, sessionChatSource{Title: strings.TrimSpace(match[1]), URL: strings.TrimRight(match[2], ".,;)")})
	}
	return dedupeSessionSources(sources)
}

func mergeSessionSources(groups ...[]sessionChatSource) []sessionChatSource {
	merged := make([]sessionChatSource, 0)
	for _, group := range groups {
		merged = append(merged, group...)
	}
	return dedupeSessionSources(merged)
}

func dedupeSessionSources(sources []sessionChatSource) []sessionChatSource {
	seen := make(map[string]struct{}, len(sources))
	out := make([]sessionChatSource, 0, len(sources))
	for _, source := range sources {
		source.URL = strings.TrimSpace(source.URL)
		parsed, err := url.Parse(source.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}
		if _, exists := seen[source.URL]; exists {
			continue
		}
		seen[source.URL] = struct{}{}
		if strings.TrimSpace(source.Title) == "" {
			source.Title = parsed.Host
		}
		out = append(out, source)
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func (h *Handler) handleListBranches(w http.ResponseWriter, r *http.Request) {
	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	groups, err := h.readSessionBranches(dir, r.PathValue("id"))
	if err != nil {
		http.Error(w, "failed to read branches", http.StatusInternalServerError)
		return
	}
	if groups == nil {
		groups = []chatmeta.BranchGroup{}
	}
	writeJSON(w, http.StatusOK, groups)
}

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" || !validAttachmentSessionID(sessionID) {
		http.Error(w, "invalid session id", http.StatusBadRequest)
		return
	}
	cfg, err := loadConfigForAPI(h.configPath)
	if err != nil {
		http.Error(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}
	limit := int64(cfg.Agents.Defaults.GetMaxMediaSize())
	if limit <= 0 {
		limit = 20 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+1<<20)
	if err := r.ParseMultipartForm(limit + 1<<20); err != nil {
		http.Error(w, "invalid upload", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	filename := filepath.Base(strings.TrimSpace(header.Filename))
	if filename == "." || filename == "" {
		filename = "attachment"
	}
	dir, err := h.sessionDirForSession(sessionID)
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	attachmentDir := filepath.Join(dir, ".attachments", sessionID)
	if err := os.MkdirAll(attachmentDir, 0o755); err != nil {
		http.Error(w, "failed to create attachment directory", http.StatusInternalServerError)
		return
	}
	storedName := randomAttachmentID() + "-" + sanitizeAttachmentFilename(filename)
	path := filepath.Join(attachmentDir, storedName)
	temp, err := os.CreateTemp(attachmentDir, ".upload-*")
	if err != nil {
		http.Error(w, "failed to create attachment", http.StatusInternalServerError)
		return
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	written, copyErr := io.Copy(temp, io.LimitReader(file, limit+1))
	if copyErr != nil {
		_ = temp.Close()
		http.Error(w, "failed to save attachment", http.StatusInternalServerError)
		return
	}
	if written > limit {
		_ = temp.Close()
		http.Error(w, "attachment exceeds size limit", http.StatusRequestEntityTooLarge)
		return
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		http.Error(w, "failed to save attachment", http.StatusInternalServerError)
		return
	}
	if err := temp.Close(); err != nil {
		http.Error(w, "failed to save attachment", http.StatusInternalServerError)
		return
	}

	h.attachmentMu.Lock()
	defer h.attachmentMu.Unlock()
	if h.attachmentCleanupAt == nil {
		h.attachmentCleanupAt = make(map[string]time.Time)
	}
	if lastCleanup := h.attachmentCleanupAt[attachmentDir]; time.Since(lastCleanup) >= time.Hour {
		h.attachmentCleanupAt[attachmentDir] = time.Now()
		if err := h.cleanupOrphanedSessionAttachments(dir, sessionID, orphanAttachmentAge); err != nil {
			logger.Warnf("failed to clean orphaned attachments for session %s: %v", sessionID, err)
		}
	}
	count, totalBytes, err := sessionAttachmentUsage(attachmentDir)
	if err != nil {
		http.Error(w, "failed to inspect attachment storage", http.StatusInternalServerError)
		return
	}
	if count >= maxSessionAttachmentCount {
		http.Error(w, "session attachment count limit reached", http.StatusTooManyRequests)
		return
	}
	if totalBytes+written > maxSessionAttachmentBytes {
		http.Error(w, "session attachment storage limit reached", http.StatusInsufficientStorage)
		return
	}
	if err := os.Rename(tempPath, path); err != nil {
		http.Error(w, "failed to save attachment", http.StatusInternalServerError)
		return
	}
	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(filename))
	}
	attachmentType := attachmentTypeFor(contentType, filename)
	urlValue := "/api/attachments/" + url.PathEscape(sessionID) + "/" + url.PathEscape(storedName)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": storedName, "type": attachmentType, "url": urlValue,
		"filename": filename, "content_type": contentType,
	})
}

func (h *Handler) handleUploadConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := loadConfigForAPI(h.configPath)
	if err != nil {
		http.Error(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"max_media_size": cfg.Agents.Defaults.GetMaxMediaSize(),
	})
}

func (h *Handler) handleDeleteUpload(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	attachmentID := strings.TrimSpace(r.URL.Query().Get("id"))
	if !validAttachmentSessionID(sessionID) || filepath.Base(attachmentID) != attachmentID || attachmentID == "." || attachmentID == "" {
		http.Error(w, "invalid attachment", http.StatusBadRequest)
		return
	}
	dirs, err := h.sessionDirs()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	h.attachmentMu.Lock()
	defer h.attachmentMu.Unlock()
	for _, dir := range dirs {
		referenced, err := h.attachmentReferencedBySession(dir, sessionID, attachmentID)
		if err != nil {
			http.Error(w, "failed to inspect attachment references", http.StatusInternalServerError)
			return
		}
		if referenced {
			http.Error(w, "attachment is already referenced by a message", http.StatusConflict)
			return
		}
	}
	removed := false
	for _, dir := range dirs {
		path := filepath.Join(dir, ".attachments", sessionID, attachmentID)
		if err := os.Remove(path); err == nil {
			removed = true
		} else if !errors.Is(err, os.ErrNotExist) {
			http.Error(w, "failed to delete attachment", http.StatusInternalServerError)
			return
		}
	}
	if removed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.NotFound(w, r)
}

func (h *Handler) handleAttachment(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	name := filepath.Base(r.PathValue("name"))
	if !validAttachmentSessionID(sessionID) || name == "." || name == "" {
		http.NotFound(w, r)
		return
	}
	dirs, err := h.sessionDirs()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	var path string
	for _, dir := range dirs {
		candidate := filepath.Join(dir, ".attachments", sessionID, name)
		info, statErr := os.Lstat(candidate)
		if statErr == nil && info.Mode().IsRegular() {
			path = candidate
			break
		}
	}
	if path == "" {
		http.NotFound(w, r)
		return
	}
	writeAttachmentResponse(w, r, path, name)
}

func sessionAttachmentUsage(dir string) (int, int64, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	count := 0
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".upload-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		count++
		total += info.Size()
	}
	return count, total, nil
}

func (h *Handler) cleanupOrphanedSessionAttachments(sessionsDir, sessionID string, age time.Duration) error {
	attachmentDir := filepath.Join(sessionsDir, ".attachments", sessionID)
	entries, err := os.ReadDir(attachmentDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	references, err := h.sessionAttachmentReferences(sessionsDir, sessionID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	cutoff := time.Now().Add(-age)
	var cleanupErrs []error
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".upload-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if _, referenced := references[entry.Name()]; referenced {
			continue
		}
		if err := os.Remove(filepath.Join(attachmentDir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	return errors.Join(cleanupErrs...)
}

func (h *Handler) attachmentReferencedBySession(sessionsDir, sessionID, attachmentID string) (bool, error) {
	references, err := h.sessionAttachmentReferences(sessionsDir, sessionID)
	if err != nil {
		return false, err
	}
	_, ok := references[attachmentID]
	return ok, nil
}

func (h *Handler) sessionAttachmentReferences(sessionsDir, sessionID string) (map[string]struct{}, error) {
	references := make(map[string]struct{})
	addMessages := func(messages []providers.Message) {
		for _, message := range messages {
			for _, attachment := range message.Attachments {
				if id := durableAttachmentID(attachment); id != "" {
					references[id] = struct{}{}
				}
			}
		}
	}
	if ref, err := h.findPicoJSONLSession(sessionsDir, sessionID); err == nil {
		base := filepath.Join(sessionsDir, sanitizeSessionKey(ref.Key))
		stored, err := readSessionAttachmentReferences(base + ".jsonl")
		if err != nil {
			return nil, err
		}
		for id := range stored {
			references[id] = struct{}{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if ref, err := h.findLegacyPicoSession(sessionsDir, sessionID); err == nil {
		sess, err := h.readLegacySession(ref.Path)
		if err != nil {
			return nil, err
		}
		addMessages(sess.Messages)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// Inactive answers still own their attachments even when the live history
	// has been replaced by a different branch.
	root := chatmeta.BranchStoreRoot(config.GetHome())
	if err := chatmeta.MigrateLegacyBranches(chatmeta.LegacyBranchStorePath(sessionsDir), root); err != nil {
		return nil, err
	}
	branches, err := chatmeta.LoadBranches(chatmeta.BranchStorePath(root, sessionID))
	if err != nil {
		return nil, err
	}
	for _, group := range branches.Sessions[sessionID].Groups {
		for _, version := range group.Versions {
			addMessages(version.Messages)
		}
	}
	return references, nil
}

func readSessionAttachmentReferences(path string) (map[string]struct{}, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	references := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSessionJSONLLineSize)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(`"attachments"`)) {
			continue
		}
		var message providers.Message
		if err := json.Unmarshal(line, &message); err != nil {
			continue
		}
		for _, attachment := range message.Attachments {
			if id := durableAttachmentID(attachment); id != "" {
				references[id] = struct{}{}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return references, nil
}

func validAttachmentSessionID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return value != "." && value != ".."
}

func sanitizeAttachmentFilename(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	if value == "" || value == "." || value == ".." {
		return "attachment"
	}
	return value
}

func randomAttachmentID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func attachmentTypeFor(contentType, filename string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(filename))) {
	case ".svg", ".svgz", ".html", ".htm", ".xhtml", ".xml", ".js", ".mjs":
		return "file"
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(contentType, "image/") {
		return "image"
	}
	if strings.HasPrefix(contentType, "audio/") {
		return "audio"
	}
	if strings.HasPrefix(contentType, "video/") {
		return "video"
	}
	return "file"
}

func writeAttachmentResponse(w http.ResponseWriter, r *http.Request, path, filename string) {
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "failed to open attachment", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.Error(w, "failed to stat attachment", http.StatusInternalServerError)
		return
	}

	contentType := mime.TypeByExtension(filepath.Ext(filename))
	attachmentType := attachmentTypeFor(contentType, filename)
	disposition := "inline"
	if attachmentType == "file" {
		disposition = "attachment"
		if activeAttachmentContent(filename, contentType) {
			contentType = "application/octet-stream"
			w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		}
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if value := mime.FormatMediaType(disposition, map[string]string{"filename": filename}); value != "" {
		w.Header().Set("Content-Disposition", value)
	}
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func activeAttachmentContent(filename, contentType string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(filename))) {
	case ".svg", ".svgz", ".html", ".htm", ".xhtml", ".xml", ".js", ".mjs":
		return true
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	return strings.Contains(contentType, "html") ||
		strings.Contains(contentType, "svg") ||
		strings.Contains(contentType, "xml") ||
		strings.Contains(contentType, "javascript")
}

func loadConfigForAPI(path string) (*config.Config, error) {
	return config.LoadConfig(path)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
