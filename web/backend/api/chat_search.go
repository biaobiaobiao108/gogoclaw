package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/biaobiaobiao108/gogoclaw/pkg/chatmeta"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

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
	ctx := r.Context()
	normalizedQuery := strings.ToLower(query)
	archivedOnly := r.URL.Query().Get("archived") == "1" || r.URL.Query().Get("archived") == "true"
	results := make([]sessionSearchResult, 0)
	store, err := memory.OpenSQLiteStoreReadOnly(dir)
	if err == nil {
		defer store.Close()
		results, err = searchSQLiteSessions(ctx, store, normalizedQuery, query, archivedOnly, limit)
	}
	if ctx.Err() != nil {
		return
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warnf("failed to search SQLite sessions: %v", err)
		http.Error(w, "failed to search sessions", http.StatusInternalServerError)
		return
	}
	files, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		http.Error(w, "failed to scan legacy sessions", http.StatusInternalServerError)
		return
	}
	if !slices.ContainsFunc(files, func(file os.DirEntry) bool {
		name := file.Name()
		return !file.IsDir() && (strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".json"))
	}) {
		writeJSON(w, http.StatusOK, results)
		return
	}
	uiMeta := h.readChatUIMeta(dir)

	refs, refsErr := h.findPicoJSONLSessions(dir)
	if refsErr != nil && !errors.Is(refsErr, os.ErrNotExist) {
		http.Error(w, "failed to scan sessions", http.StatusInternalServerError)
		return
	}
	for _, ref := range refs {
		if ref.Storage == "sqlite" {
			continue
		}
		if r.Context().Err() != nil {
			return
		}
		cacheKey := "jsonl:" + filepath.Clean(dir) + ":" + ref.ID
		cachePaths := sessionCachePaths(dir, ref)
		item, cached := h.cachedSessionListItem(cacheKey, cachePaths...)
		if !cached {
			var loadErr error
			item, loadErr = h.readJSONLSessionListItem(dir, ref.Key, ref.ID, toolFeedbackMaxArgsLength)
			if loadErr != nil {
				continue
			}
			h.storeCachedSessionListItem(cacheKey, item, cachePaths...)
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
	if store != nil {
		ids := make([]string, 0, len(results))
		for _, result := range results {
			ids = append(ids, result.ID)
		}
		if projects, projectErr := store.SessionChatProjectIDs(r.Context(), ids); projectErr == nil {
			for i := range results {
				if project, ok := projects[results[i].ID]; ok {
					results[i].ProjectID, results[i].ProjectName = project.ID, project.Name
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, results)
}

func searchSQLiteSessions(
	ctx context.Context, store *memory.SQLiteStore, normalizedQuery, query string, archived bool, limit int,
) ([]sessionSearchResult, error) {
	results := make([]sessionSearchResult, 0, limit)
	for entry, err := range store.SearchPicoSessions(ctx, legacyPicoSessionPrefix, archived, normalizedQuery) {
		if err != nil {
			return nil, err
		}
		item, err := sqliteSessionListItem(entry.PicoSessionListEntry)
		if err != nil {
			return nil, err
		}
		if result, ok := searchSessionResultForItem(item, normalizedQuery); ok {
			results = append(results, result)
		} else if len(entry.Message) != 0 {
			var message providers.Message
			if err := json.Unmarshal(entry.Message, &message); err != nil {
				return nil, err
			}
			results = append(results, searchSessionMessageResult(item, message, query))
		}
		if len(results) == limit {
			break
		}
	}
	return results, nil
}

func sessionSearchResultFromItem(item sessionListItem) sessionSearchResult {
	return sessionSearchResult{
		ID: item.ID, Title: item.Title, Preview: item.Preview, MessageCount: item.MessageCount,
		Created: item.Created, Updated: item.Updated, Pinned: item.Pinned, Archived: item.Archived,
		ProjectID: item.ProjectID, ProjectName: item.ProjectName,
	}
}

func searchSessionMessageResult(item sessionListItem, message providers.Message, query string) sessionSearchResult {
	result := sessionSearchResultFromItem(item)
	result.MessageID = message.ID
	result.MessageRole = message.Role
	result.Snippet = searchSnippet(message.Content, query, 120)
	return result
}

func searchSessionResultForItem(item sessionListItem, normalizedQuery string) (sessionSearchResult, bool) {
	if strings.Contains(strings.ToLower(item.Title), normalizedQuery) || strings.Contains(strings.ToLower(item.Preview), normalizedQuery) {
		base := sessionSearchResultFromItem(item)
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
	for _, message := range messages {
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if !strings.Contains(strings.ToLower(message.Content), normalizedQuery) {
			continue
		}
		return searchSessionMessageResult(item, message, rawQuery), true
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
		return searchSessionMessageResult(item, message, rawQuery), true, nil
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
