package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
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
	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	store, err := memory.OpenSQLiteStoreReadOnly(dir)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, []sessionSearchResult{})
		return
	}
	if err == nil {
		defer store.Close()
		ctx := r.Context()
		archived := r.URL.Query().Get("archived")
		results, searchErr := searchSQLiteSessions(ctx, store, strings.ToLower(query), query, archived == "1" || archived == "true", limit)
		if ctx.Err() != nil {
			return
		}
		if searchErr == nil {
			writeJSON(w, http.StatusOK, results)
			return
		}
		err = searchErr
	}
	logger.Warnf("failed to search SQLite sessions: %v", err)
	http.Error(w, "failed to search sessions", http.StatusInternalServerError)
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
