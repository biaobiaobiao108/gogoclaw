package api

import (
	"crypto/rand"
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

	"github.com/biaobiaobiao108/gogoclaw/pkg/chatmeta"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
)

const chatUIMetaVersion = 1

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

func newChatUIMeta() chatUIMetaFile {
	return chatUIMetaFile{Version: chatUIMetaVersion, Sessions: make(map[string]chatUISessionMeta)}
}

func (h *Handler) loadChatUIMeta(dir string) (chatUIMetaFile, error) {
	data, err := os.ReadFile(chatUIMetaPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return newChatUIMeta(), nil
	}
	if err != nil {
		return chatUIMetaFile{}, err
	}
	meta := newChatUIMeta()
	if err := json.Unmarshal(data, &meta); err != nil {
		return chatUIMetaFile{}, err
	}
	if meta.Sessions == nil {
		meta.Sessions = make(map[string]chatUISessionMeta)
	}
	if meta.Version == 0 {
		meta.Version = chatUIMetaVersion
	}
	return meta, nil
}

func (h *Handler) saveChatUIMeta(dir string, meta chatUIMetaFile) error {
	if meta.Version == 0 {
		meta.Version = chatUIMetaVersion
	}
	if meta.Sessions == nil {
		meta.Sessions = make(map[string]chatUISessionMeta)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	path := chatUIMetaPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0o644)
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

func (h *Handler) updateChatUIMeta(dir string, update func(*chatUIMetaFile) error) error {
	h.sessionMetaMu.Lock()
	defer h.sessionMetaMu.Unlock()
	meta, err := h.loadChatUIMeta(dir)
	if err != nil {
		return err
	}
	if err := update(&meta); err != nil {
		return err
	}
	return h.saveChatUIMeta(dir, meta)
}

func (h *Handler) cleanupDeletedSessionData(dir, sessionID string) error {
	var cleanupErrs []error
	if err := h.updateChatUIMeta(dir, func(meta *chatUIMetaFile) error {
		delete(meta.Sessions, sessionID)
		return nil
	}); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("remove chat metadata: %w", err))
	}
	if validAttachmentSessionID(sessionID) {
		if err := os.RemoveAll(filepath.Join(dir, ".attachments", sessionID)); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove attachments: %w", err))
		}
	}
	if err := chatmeta.RemoveBranchSession(chatmeta.BranchStorePath(dir), sessionID); err != nil {
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
	store, err := chatmeta.LoadBranches(chatmeta.BranchStorePath(dir))
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
	dir, err := h.sessionsDir()
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
	if err := h.updateChatUIMeta(dir, func(meta *chatUIMetaFile) error {
		item := meta.Sessions[sessionID]
		if body.Title != nil {
			item.Title = strings.TrimSpace(*body.Title)
		}
		if body.Pinned != nil {
			item.Pinned = *body.Pinned
		}
		if body.Archived != nil {
			item.Archived = *body.Archived
		}
		meta.Sessions[sessionID] = item
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
	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	if !h.sessionExists(dir, sessionID) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if err := h.updateChatUIMeta(dir, func(meta *chatUIMetaFile) error {
		item := meta.Sessions[sessionID]
		if item.Feedback == nil {
			item.Feedback = make(map[string]string)
		}
		if body.Value == "" {
			delete(item.Feedback, messageID)
		} else {
			item.Feedback[messageID] = body.Value
		}
		meta.Sessions[sessionID] = item
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
	results := make([]sessionSearchResult, 0)

	refs, refsErr := h.findPicoJSONLSessions(dir)
	if refsErr != nil && !errors.Is(refsErr, os.ErrNotExist) {
		http.Error(w, "failed to scan sessions", http.StatusInternalServerError)
		return
	}
	for _, ref := range refs {
		sess, loadErr := h.readJSONLSession(dir, ref.Key)
		if loadErr != nil || isEmptySession(sess) {
			continue
		}
		item := buildSessionListItem(ref.ID, sess, toolFeedbackMaxArgsLength)
		applySessionMeta(&item, uiMeta.Sessions[ref.ID])
		result, ok := searchSessionMatch(item, sess, normalizedQuery, query)
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
		if _, exists := seen[ref.ID]; exists {
			continue
		}
		sess, loadErr := h.readLegacySession(ref.Path)
		if loadErr != nil || isEmptySession(sess) {
			continue
		}
		item := buildSessionListItem(ref.ID, sess, toolFeedbackMaxArgsLength)
		applySessionMeta(&item, uiMeta.Sessions[ref.ID])
		result, ok := searchSessionMatch(item, sess, normalizedQuery, query)
		if ok {
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

func searchSessionMatch(item sessionListItem, sess sessionFile, normalizedQuery, rawQuery string) (sessionSearchResult, bool) {
	base := sessionSearchResult{
		ID: item.ID, Title: item.Title, Preview: item.Preview, MessageCount: item.MessageCount,
		Created: item.Created, Updated: item.Updated, Pinned: item.Pinned, Archived: item.Archived,
	}
	if strings.Contains(strings.ToLower(item.Title), normalizedQuery) || strings.Contains(strings.ToLower(item.Preview), normalizedQuery) {
		base.Snippet = item.Preview
		return base, true
	}
	for _, message := range sess.Messages {
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

func searchSnippet(content, query string, limit int) string {
	runes := []rune(strings.TrimSpace(content))
	queryRunes := []rune(strings.TrimSpace(query))
	if len(runes) <= limit {
		return string(runes)
	}
	index := -1
	for i := 0; i+len(queryRunes) <= len(runes); i++ {
		if strings.EqualFold(string(runes[i:i+len(queryRunes)]), query) {
			index = i
			break
		}
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

var (
	markdownSourcePattern = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^\s)]+)\)`)
	searchSourcePattern   = regexp.MustCompile(`(?m)^\s*\d+\.\s+([^\n]+)\r?\n\s*(https?://[^\s]+)\s*$`)
	rawSourceURLPattern   = regexp.MustCompile(`https?://[^\s<>"']+`)
)

func extractSessionSources(content string) []sessionChatSource {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	sources := make([]sessionChatSource, 0)
	for _, match := range markdownSourcePattern.FindAllStringSubmatch(content, -1) {
		sources = append(sources, sessionChatSource{Title: strings.TrimSpace(match[1]), URL: strings.TrimRight(match[2], ".,;)")})
	}
	for _, match := range searchSourcePattern.FindAllStringSubmatch(content, -1) {
		sources = append(sources, sessionChatSource{Title: strings.TrimSpace(match[1]), URL: strings.TrimRight(match[2], ".,;)")})
	}
	for _, rawURL := range rawSourceURLPattern.FindAllString(content, -1) {
		sources = append(sources, sessionChatSource{URL: strings.TrimRight(rawURL, ".,;)")})
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
	dir, err := h.sessionsDir()
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
		"type": attachmentType, "url": urlValue, "path": path, "filename": filename, "content_type": contentType,
	})
}

func (h *Handler) handleAttachment(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	name := filepath.Base(r.PathValue("name"))
	if !validAttachmentSessionID(sessionID) || name == "." || name == "" {
		http.NotFound(w, r)
		return
	}
	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	path := filepath.Join(dir, ".attachments", sessionID, name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	if attachmentTypeFor(contentType, name) == "file" {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+strings.ReplaceAll(name, "\"", "")+"\"")
	}
	http.ServeFile(w, r, path)
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

func loadConfigForAPI(path string) (*config.Config, error) {
	return config.LoadConfig(path)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
