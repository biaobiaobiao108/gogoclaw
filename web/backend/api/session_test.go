package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

func sessionsTestDir(t *testing.T, configPath string) string {
	t.Helper()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	dir := filepath.Join(cfg.Agents.Defaults.Workspace, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	return dir
}

func assertVisibleToolCallMessage(
	t *testing.T,
	msg sessionChatMessage,
	toolName string,
) utils.VisibleToolCall {
	t.Helper()

	if msg.Role != "assistant" || msg.Kind != "tool_calls" {
		t.Fatalf("message = %#v, want assistant/tool_calls", msg)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("len(message.ToolCalls) = %d, want 1", len(msg.ToolCalls))
	}
	if got := msg.ToolCalls[0].Function; got == nil || got.Name != toolName {
		t.Fatalf("tool call = %#v, want function %q", msg.ToolCalls[0], toolName)
	}
	return msg.ToolCalls[0]
}

func TestHandleListSessions_SQLiteStorage(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, storeErr := memory.NewSQLiteStore(dir)
	if storeErr != nil {
		t.Fatalf("NewSQLiteStore() error = %v", storeErr)
	}

	sessionKey := legacyPicoSessionPrefix + "history-jsonl"
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{
		Role:    "user",
		Content: "Explain why the history API is empty after migration.",
	}); err != nil {
		t.Fatalf("AddFullMessage(user) error = %v", err)
	}
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{
		Role:    "assistant",
		Content: "Because the API still reads only legacy JSON session files.",
	}); err != nil {
		t.Fatalf("AddFullMessage(assistant) error = %v", err)
	}
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{
		Role:    "tool",
		Content: "ignored",
	}); err != nil {
		t.Fatalf("AddFullMessage(tool) error = %v", err)
	}
	if err := store.SetSummary(nil, sessionKey, "JSONL-backed session"); err != nil {
		t.Fatalf("SetSummary() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var items []sessionListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].ID != "history-jsonl" {
		t.Fatalf("items[0].ID = %q, want %q", items[0].ID, "history-jsonl")
	}
	if items[0].MessageCount != 2 {
		t.Fatalf("items[0].MessageCount = %d, want 2", items[0].MessageCount)
	}
	expectedTitle := session.FallbackTitle("Explain why the history API is empty after migration.")
	if items[0].Title != expectedTitle {
		t.Fatalf(
			"items[0].Title = %q, want %q",
			items[0].Title,
			expectedTitle,
		)
	}
	if items[0].Preview != "Explain why the history API is empty after migration." {
		t.Fatalf("items[0].Preview = %q", items[0].Preview)
	}
}

func TestHandleSessions_SQLiteStorageListDetailAndDelete(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	sessionKey := legacyPicoSessionPrefix + "sqlite-main"
	if err := store.AddMessage(nil, sessionKey, "user", "SQLite session title"); err != nil {
		t.Fatalf("AddMessage(user) error = %v", err)
	}
	if err := store.AddMessage(nil, sessionKey, "assistant", "SQLite session answer"); err != nil {
		t.Fatalf("AddMessage(assistant) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", listRec.Code, listRec.Body.String())
	}
	var items []sessionListItem
	if err := json.Unmarshal(listRec.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "sqlite-main" || items[0].MessageCount != 2 {
		t.Fatalf("items = %+v", items)
	}

	detailRec := httptest.NewRecorder()
	mux.ServeHTTP(detailRec, httptest.NewRequest(http.MethodGet, "/api/sessions/sqlite-main", nil))
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body=%s", detailRec.Code, detailRec.Body.String())
	}

	deleteRec := httptest.NewRecorder()
	mux.ServeHTTP(deleteRec, httptest.NewRequest(http.MethodDelete, "/api/sessions/sqlite-main", nil))
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	reopened, err := memory.OpenSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	exists, err := reopened.SessionExists(context.Background(), sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("session row still exists after delete")
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions.db")); err != nil {
		t.Fatalf("sessions database should remain after delete: %v", err)
	}
}

func TestHandleListSessions_SQLiteSurvivesNewHandler(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sessionKey := legacyPicoSessionPrefix + "persistent-index"
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{
		Role:    "user",
		Content: "Keep this session available through the metadata index.",
	}); err != nil {
		t.Fatal(err)
	}

	list := func(h *Handler) []sessionListItem {
		t.Helper()
		mux := http.NewServeMux()
		h.RegisterRoutes(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var items []sessionListItem
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}

	if items := list(NewHandler(configPath)); len(items) != 1 {
		t.Fatalf("initial items = %d, want 1", len(items))
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// A new handler process must recover the same row directly from SQLite.
	if items := list(NewHandler(configPath)); len(items) != 1 || items[0].ID != "persistent-index" {
		t.Fatalf("reopened items = %+v, want persistent-index", items)
	}
}

func TestHandleListSessions_TransientThoughtDoesNotInflateMessageCount(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	sessionKey := legacyPicoSessionPrefix + "history-transient"
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, msg := range []providers.Message{
		{Role: "user", Content: "keep me"},
		{Role: "assistant", ReasoningContent: "dangling thought"},
		{Role: "assistant", Content: "and me"},
	} {
		if err := store.AddFullMessage(t.Context(), sessionKey, msg); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var items []sessionListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].ID != "history-transient" {
		t.Fatalf("items[0].ID = %q, want %q", items[0].ID, "history-transient")
	}
	if items[0].MessageCount != 2 {
		t.Fatalf("items[0].MessageCount = %d, want 2 after dropping transient thought", items[0].MessageCount)
	}
}

func TestHandleListSessions_TitleUsesFirstUserMessage(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, storeErr := memory.NewSQLiteStore(dir)
	if storeErr != nil {
		t.Fatalf("NewSQLiteStore() error = %v", storeErr)
	}

	sessionKey := legacyPicoSessionPrefix + "summary-title"
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{
		Role:    "user",
		Content: "fallback preview",
	}); err != nil {
		t.Fatalf("AddFullMessage() error = %v", err)
	}
	if err := store.SetSummary(
		nil,
		sessionKey,
		"  This summary is intentionally longer than sixty characters so it must be truncated in the history menu.  ",
	); err != nil {
		t.Fatalf("SetSummary() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var items []sessionListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	expectedTitle := session.FallbackTitle("fallback preview")
	if items[0].Title != expectedTitle {
		t.Fatalf("items[0].Title = %q", items[0].Title)
	}
	if items[0].Preview != "fallback preview" {
		t.Fatalf("items[0].Preview = %q, want %q", items[0].Preview, "fallback preview")
	}
}

func TestBuildSessionListItemUsesCompactFallbackTitle(t *testing.T) {
	message := "请帮我看看 Bun 前端构建迁移后样式和开发服务器不一致的问题，并给出修复方案"
	item := buildSessionListItem("test", sessionFile{
		Messages: []providers.Message{{Role: "user", Content: message}},
	}, 0)
	if utf8.RuneCountInString(item.Title) > session.MaxTitleRunes {
		t.Fatalf("title has %d runes, want at most %d: %q", utf8.RuneCountInString(item.Title), session.MaxTitleRunes, item.Title)
	}
	if strings.HasPrefix(item.Title, "请帮我") {
		t.Fatalf("title retained request framing: %q", item.Title)
	}
	if item.Preview != message {
		t.Fatalf("preview = %q, want original message", item.Preview)
	}
}

func TestHandleListSessions_SQLiteDoesNotWriteFallbackTitle(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	sessionKey := legacyPicoSessionPrefix + "sqlite-title-cache"
	base := filepath.Join(dir, sanitizeSessionKey(sessionKey))
	if err := os.WriteFile(base+".jsonl", []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+".meta.json", []byte(`{"key":"`+sessionKey+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(nil, sessionKey, "user", "first question"); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	list := func() []sessionListItem {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var items []sessionListItem
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}
	first := list()
	if len(first) != 1 || first[0].Title != "first question" {
		t.Fatalf("initial items = %+v", first)
	}
	uiMeta := h.readChatUIMeta(dir).Sessions["sqlite-title-cache"]
	if uiMeta.Title != "" || uiMeta.TitleSource != "" {
		t.Fatalf("session title metadata = %+v", uiMeta)
	}

	if err := store.AddMessage(nil, sessionKey, "assistant", "answer"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	second := list()
	if len(second) != 1 || second[0].MessageCount != 2 {
		t.Fatalf("updated items = %+v", second)
	}
}

func TestSessionMetadataPreservesGeneratedTitleVersion(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	h := NewHandler(configPath)
	if err := h.updateChatUISessionMeta(dir, "versioned-session", func(meta *chatUISessionMeta) error {
		meta.Title = "Bun 前端构建迁移"
		meta.TitleSource = "generated"
		meta.TitleVersion = session.TitleVersion
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.updateChatUISessionMeta(dir, "versioned-session", func(meta *chatUISessionMeta) error {
		meta.Pinned = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	meta := h.readChatUIMeta(dir).Sessions["versioned-session"]
	if meta.TitleVersion != session.TitleVersion {
		t.Fatalf("title version = %d, want %d", meta.TitleVersion, session.TitleVersion)
	}
}

func TestApplySessionMetaKeepsDerivedTitleWhenStoredTitleIsAutomatic(t *testing.T) {
	item := sessionListItem{Title: "edited first question"}
	applySessionMeta(&item, chatUISessionMeta{
		Title:       "old automatic title",
		TitleSource: "auto",
	})
	if item.Title != "edited first question" {
		t.Fatalf("title = %q, want derived title", item.Title)
	}

	applySessionMeta(&item, chatUISessionMeta{
		Title:       "generated title",
		TitleSource: "generated",
	})
	if item.Title != "generated title" {
		t.Fatalf("title = %q, want generated title", item.Title)
	}

	applySessionMeta(&item, chatUISessionMeta{
		Title:       "manual title",
		TitleSource: "manual",
	})
	if item.Title != "manual title" {
		t.Fatalf("title = %q, want manual title", item.Title)
	}
}

func TestHandleGetSession_SQLiteStorage(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := legacyPicoSessionPrefix + "detail-jsonl"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
		{Role: "tool", Content: "ignored"},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}
	if err := store.SetSummary(nil, sessionKey, "detail summary"); err != nil {
		t.Fatalf("SetSummary() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/detail-jsonl", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		ID       string `json:"id"`
		Summary  string `json:"summary"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.ID != "detail-jsonl" {
		t.Fatalf("resp.ID = %q, want %q", resp.ID, "detail-jsonl")
	}
	if resp.Summary != "detail summary" {
		t.Fatalf("resp.Summary = %q, want %q", resp.Summary, "detail summary")
	}
	if len(resp.Messages) != 2 {
		t.Fatalf("len(resp.Messages) = %d, want 2", len(resp.Messages))
	}
	if resp.Messages[0].Role != "user" || resp.Messages[0].Content != "first" {
		t.Fatalf("first message = %#v, want user/first", resp.Messages[0])
	}
	if resp.Messages[1].Role != "assistant" || resp.Messages[1].Content != "second" {
		t.Fatalf("second message = %#v, want assistant/second", resp.Messages[1])
	}
}

func TestHandleGetSession_HidesHandledToolAttachmentsBackedByMediaRefs(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := legacyPicoSessionPrefix + "attachment-history"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "send me the report"},
		{
			Role:    "assistant",
			Content: handledToolResponseSummaryText,
			Attachments: []providers.Attachment{{
				Type:        "file",
				Ref:         "media://attachment-1",
				Filename:    "report.txt",
				ContentType: "text/plain",
			}},
		},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/attachment-history", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []sessionChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("len(resp.Messages) = %d, want 1", len(resp.Messages))
	}
	if resp.Messages[0].Role != "user" || resp.Messages[0].Content != "send me the report" {
		t.Fatalf("message = %#v, want only user request", resp.Messages[0])
	}
}

func TestHandleGetSession_ExposesHandledToolAttachmentsWithDurableURL(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := legacyPicoSessionPrefix + "attachment-history-durable"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "send me the report"},
		{
			Role:    "assistant",
			Content: handledToolResponseSummaryText,
			Attachments: []providers.Attachment{{
				Type:        "file",
				URL:         "https://example.com/report.txt",
				Filename:    "report.txt",
				ContentType: "text/plain",
			}},
		},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/attachment-history-durable", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []sessionChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(resp.Messages) != 2 {
		t.Fatalf("len(resp.Messages) = %d, want 2", len(resp.Messages))
	}

	assistant := resp.Messages[1]
	if assistant.Role != "assistant" {
		t.Fatalf("assistant role = %q, want assistant", assistant.Role)
	}
	if assistant.Content != "" {
		t.Fatalf("assistant content = %q, want empty string", assistant.Content)
	}
	if len(assistant.Attachments) != 1 {
		t.Fatalf("len(assistant.Attachments) = %d, want 1", len(assistant.Attachments))
	}
	if assistant.Attachments[0].URL != "https://example.com/report.txt" {
		t.Fatalf(
			"attachment url = %q, want %q",
			assistant.Attachments[0].URL,
			"https://example.com/report.txt",
		)
	}
	if assistant.Attachments[0].Filename != "report.txt" {
		t.Fatalf("attachment filename = %q, want %q", assistant.Attachments[0].Filename, "report.txt")
	}
}

func TestHandleSessions_SQLiteScopeDiscovery(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, storeErr := memory.NewSQLiteStore(dir)
	if storeErr != nil {
		t.Fatalf("NewSQLiteStore() error = %v", storeErr)
	}

	sessionKey := "sk_v1_scope_discovery"
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{
		Role:    "user",
		Content: "scope discovered session",
	}); err != nil {
		t.Fatalf("AddFullMessage() error = %v", err)
	}
	if err := store.SetSummary(nil, sessionKey, "scope summary"); err != nil {
		t.Fatalf("SetSummary() error = %v", err)
	}

	scopeData, err := json.Marshal(session.SessionScope{
		Version:    session.ScopeVersionV1,
		AgentID:    "main",
		Channel:    "pico",
		Account:    "default",
		Dimensions: []string{"sender"},
		Values: map[string]string{
			"sender": "pico:scope-jsonl",
		},
	})
	if err != nil {
		t.Fatalf("Marshal(scope) error = %v", err)
	}
	if err := store.UpsertSessionMeta(nil, sessionKey, scopeData, nil); err != nil {
		t.Fatalf("UpsertSessionMeta() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body=%s", listRec.Code, http.StatusOK, listRec.Body.String())
	}

	var items []sessionListItem
	if err := json.Unmarshal(listRec.Body.Bytes(), &items); err != nil {
		t.Fatalf("Unmarshal(list) error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].ID != "scope-jsonl" {
		t.Fatalf("items[0].ID = %q, want %q", items[0].ID, "scope-jsonl")
	}

	detailRec := httptest.NewRecorder()
	detailReq := httptest.NewRequest(http.MethodGet, "/api/sessions/scope-jsonl", nil)
	mux.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want %d, body=%s", detailRec.Code, http.StatusOK, detailRec.Body.String())
	}

	deleteRec := httptest.NewRecorder()
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/sessions/scope-jsonl", nil)
	mux.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d, body=%s", deleteRec.Code, http.StatusNoContent, deleteRec.Body.String())
	}
}

func TestHandleGetSession_SkipsTransientThoughtMessages(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "detail-transient-thought"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", ReasoningContent: "internal chain of thought"},
		{Role: "assistant", Content: "final visible answer"},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/detail-transient-thought", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []sessionChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Messages) != 2 {
		t.Fatalf("len(resp.Messages) = %d, want 2", len(resp.Messages))
	}
	if resp.Messages[0].Role != "user" || resp.Messages[0].Content != "hello" {
		t.Fatalf("first message = %#v, want user/hello", resp.Messages[0])
	}
	if resp.Messages[1].Role != "assistant" || resp.Messages[1].Content != "final visible answer" {
		t.Fatalf("second message = %#v, want assistant/final visible answer", resp.Messages[1])
	}
}

func TestHandleGetSession_ReconstructsThoughtFromAssistantReasoningContent(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "detail-reasoning-content"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "final visible answer", ModelName: "gpt-5.4", ReasoningContent: "internal chain of thought"},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/detail-reasoning-content", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []sessionChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Messages) != 3 {
		t.Fatalf("len(resp.Messages) = %d, want 3", len(resp.Messages))
	}
	if resp.Messages[1].Role != "assistant" ||
		resp.Messages[1].Content != "internal chain of thought" ||
		resp.Messages[1].Kind != "thought" {
		t.Fatalf("thought message = %#v, want assistant thought/internal chain of thought", resp.Messages[1])
	}
	if resp.Messages[1].ModelName != "gpt-5.4" {
		t.Fatalf("thought model_name = %q, want %q", resp.Messages[1].ModelName, "gpt-5.4")
	}
	if resp.Messages[2].Role != "assistant" || resp.Messages[2].Content != "final visible answer" {
		t.Fatalf("final message = %#v, want assistant/final visible answer", resp.Messages[2])
	}
	if resp.Messages[2].ModelName != "gpt-5.4" {
		t.Fatalf("final model_name = %q, want %q", resp.Messages[2].ModelName, "gpt-5.4")
	}
}

func TestHandleGetSession_ReconstructsRefreshMatrixForThoughtAndToolSummary(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "detail-refresh-matrix"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "turn1"},
		{Role: "assistant", Content: "plain visible", ReasoningContent: "plain thought"},
		{Role: "user", Content: "turn2"},
		{
			Role:             "assistant",
			ReasoningContent: "tool thought",
			ToolCalls: []providers.ToolCall{{
				ID:   "call_read_file",
				Type: "function",
				Function: &providers.FunctionCall{
					Name:      "read_file",
					Arguments: `{"path":"README.md"}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "call_read_file", Content: "file result"},
		{Role: "user", Content: "turn3"},
		{
			Role:    "assistant",
			Content: "tool visible only",
			ToolCalls: []providers.ToolCall{{
				ID:   "call_list_dir",
				Type: "function",
				Function: &providers.FunctionCall{
					Name:      "list_dir",
					Arguments: `{"path":"."}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "call_list_dir", Content: "dir result"},
		{Role: "user", Content: "turn4"},
		{
			Role:             "assistant",
			Content:          "tool visible and thought",
			ReasoningContent: "tool mixed thought",
			ToolCalls: []providers.ToolCall{{
				ID:   "call_exec",
				Type: "function",
				Function: &providers.FunctionCall{
					Name:      "exec",
					Arguments: `{"command":"pwd"}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "call_exec", Content: "pwd result"},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/detail-refresh-matrix", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []sessionChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(resp.Messages) != 13 {
		t.Fatalf("len(resp.Messages) = %d, want 13", len(resp.Messages))
	}

	assertMessage := func(index int, role, kind, content string) {
		t.Helper()
		msg := resp.Messages[index]
		if msg.Role != role || msg.Kind != kind || msg.Content != content {
			t.Fatalf("messages[%d] = %#v, want role=%q kind=%q content=%q", index, msg, role, kind, content)
		}
	}

	assertMessage(0, "user", "", "turn1")
	assertMessage(1, "assistant", "thought", "plain thought")
	assertMessage(2, "assistant", "", "plain visible")
	assertMessage(3, "user", "", "turn2")
	assertMessage(4, "assistant", "thought", "tool thought")
	assertVisibleToolCallMessage(t, resp.Messages[5], "read_file")
	assertMessage(6, "user", "", "turn3")
	assertMessage(7, "assistant", "", "tool visible only")
	assertVisibleToolCallMessage(t, resp.Messages[8], "list_dir")
	assertMessage(9, "user", "", "turn4")
	assertMessage(10, "assistant", "thought", "tool mixed thought")
	assertMessage(11, "assistant", "", "tool visible and thought")
	assertVisibleToolCallMessage(t, resp.Messages[12], "exec")
}

func TestHandleGetSession_ReconstructsVisibleMessageToolOutputWithoutDuplicateSummary(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "detail-message-tool"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "test"},
		{
			Role:      "assistant",
			Content:   "",
			ModelName: "gpt-5.4-mini",
			ToolCalls: []providers.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "message",
						Arguments: `{"content":"visible tool output"}`,
					},
				},
			},
		},
		{Role: "tool", Content: "Message sent to pico:pico:detail-message-tool", ToolCallID: "call_1"},
		{Role: "assistant", Content: handledToolResponseSummaryText},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/detail-message-tool", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []sessionChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Messages) != 3 {
		t.Fatalf("len(resp.Messages) = %d, want 3", len(resp.Messages))
	}
	if resp.Messages[0].Role != "user" || resp.Messages[0].Content != "test" {
		t.Fatalf("first message = %#v, want user/test", resp.Messages[0])
	}
	assertVisibleToolCallMessage(t, resp.Messages[1], "message")
	if resp.Messages[1].ModelName != "gpt-5.4-mini" {
		t.Fatalf("tool_calls model_name = %q, want %q", resp.Messages[1].ModelName, "gpt-5.4-mini")
	}
	if resp.Messages[2].Role != "assistant" || resp.Messages[2].Content != "visible tool output" {
		t.Fatalf("assistant message = %#v, want visible tool output", resp.Messages[2])
	}
	if resp.Messages[2].ModelName != "gpt-5.4-mini" {
		t.Fatalf("visible tool output model_name = %q, want %q", resp.Messages[2].ModelName, "gpt-5.4-mini")
	}
}

func TestHandleGetSession_PreservesFinalAssistantReplyAfterMessageToolOutput(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "detail-message-tool-final-reply"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "test"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "message",
						Arguments: `{"content":"visible tool output"}`,
					},
				},
			},
		},
		{Role: "tool", Content: "Message sent to pico:pico:detail-message-tool-final-reply", ToolCallID: "call_1"},
		{Role: "assistant", Content: "final assistant reply"},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/detail-message-tool-final-reply", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []sessionChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Messages) != 4 {
		t.Fatalf("len(resp.Messages) = %d, want 4", len(resp.Messages))
	}
	if resp.Messages[0].Role != "user" || resp.Messages[0].Content != "test" {
		t.Fatalf("first message = %#v, want user/test", resp.Messages[0])
	}
	assertVisibleToolCallMessage(t, resp.Messages[1], "message")
	if resp.Messages[2].Role != "assistant" || resp.Messages[2].Content != "visible tool output" {
		t.Fatalf("interim assistant message = %#v, want visible tool output", resp.Messages[2])
	}
	if resp.Messages[3].Role != "assistant" || resp.Messages[3].Content != "final assistant reply" {
		t.Fatalf("final assistant message = %#v, want final assistant reply", resp.Messages[3])
	}
}

func TestHandleListSessions_MessageCountUsesVisibleTranscript(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "list-visible-count"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "test"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "message",
						Arguments: `{"content":"visible tool output"}`,
					},
				},
			},
		},
		{Role: "tool", Content: "Message sent to pico:pico:list-visible-count", ToolCallID: "call_1"},
		{Role: "assistant", Content: handledToolResponseSummaryText},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var items []sessionListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].MessageCount != 3 {
		t.Fatalf("items[0].MessageCount = %d, want 3", items[0].MessageCount)
	}
}

func TestHandleListSessions_CountsInterimAssistantContentInVisibleTranscript(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "list-deduped-tool-content"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "check file"},
		{
			Role:    "assistant",
			Content: "Read the file before replying.",
			ToolCalls: []providers.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "read_file",
						Arguments: `{"path":"README.md"}`,
					},
					ExtraContent: &providers.ExtraContent{
						ToolFeedbackExplanation: "Read the file before replying.",
					},
				},
			},
		},
		{Role: "tool", Content: "raw read_file result", ToolCallID: "call_1"},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var items []sessionListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].MessageCount != 3 {
		t.Fatalf("items[0].MessageCount = %d, want 3", items[0].MessageCount)
	}
}

func TestHandleGetSession_DoesNotDuplicateAssistantToolCallContent(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "detail-tool-summary-and-content"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "check file"},
		{
			Role:    "assistant",
			Content: "Read the file before replying.",
			ToolCalls: []providers.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "read_file",
						Arguments: `{"path":"README.md","start_line":1,"end_line":10}`,
					},
					ExtraContent: &providers.ExtraContent{
						ToolFeedbackExplanation: "Read the file before replying.",
					},
				},
			},
		},
		{Role: "tool", Content: "raw read_file result", ToolCallID: "call_1"},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/detail-tool-summary-and-content", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []sessionChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Messages) != 3 {
		t.Fatalf("len(resp.Messages) = %d, want 3", len(resp.Messages))
	}
	if resp.Messages[0].Role != "user" || resp.Messages[0].Content != "check file" {
		t.Fatalf("first message = %#v, want user/check file", resp.Messages[0])
	}
	if resp.Messages[1].Content != "Read the file before replying." || resp.Messages[1].Kind == "tool_calls" {
		t.Fatalf("assistant message = %#v, want the interim text as a normal message", resp.Messages[1])
	}
	toolCall := assertVisibleToolCallMessage(t, resp.Messages[2], "read_file")
	if toolCall.ExtraContent != nil {
		t.Fatalf("tool call = %#v, want duplicate explanation omitted", toolCall)
	}
}

func TestHandleGetSession_PreservesDistinctAssistantToolCallContent(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "detail-tool-summary-distinct-content"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "check file"},
		{
			Role:    "assistant",
			Content: "I will summarize the findings after reading the file.",
			ToolCalls: []providers.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "read_file",
						Arguments: `{"path":"README.md","start_line":1,"end_line":10}`,
					},
					ExtraContent: &providers.ExtraContent{
						ToolFeedbackExplanation: "Read the file before replying.",
					},
				},
			},
		},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/detail-tool-summary-distinct-content", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []sessionChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Messages) != 3 {
		t.Fatalf("len(resp.Messages) = %d, want 3", len(resp.Messages))
	}
	if resp.Messages[1].Role != "assistant" ||
		resp.Messages[1].Content != "I will summarize the findings after reading the file." {
		t.Fatalf("assistant content = %#v, want preserved distinct content", resp.Messages[1])
	}
	assertVisibleToolCallMessage(t, resp.Messages[2], "read_file")
}

func TestHandleGetSession_PreservesMediaWhenAssistantToolCallContentDuplicatesSummary(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "detail-tool-summary-duplicate-content-with-media"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "check screenshot"},
		{
			Role:    "assistant",
			Content: "Reviewing the generated screenshot.",
			Media:   []string{"data:image/png;base64,abc123"},
			ToolCalls: []providers.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "view_image",
						Arguments: `{"path":"artifact.png"}`,
					},
					ExtraContent: &providers.ExtraContent{
						ToolFeedbackExplanation: "Reviewing the generated screenshot.",
					},
				},
			},
		},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/detail-tool-summary-duplicate-content-with-media", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []sessionChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Messages) != 3 {
		t.Fatalf("len(resp.Messages) = %d, want 3", len(resp.Messages))
	}
	if resp.Messages[1].Role != "assistant" {
		t.Fatalf("assistant message role = %q, want assistant", resp.Messages[1].Role)
	}
	if resp.Messages[1].Content != "Reviewing the generated screenshot." {
		t.Fatalf("assistant content = %q, want preserved interim content", resp.Messages[1].Content)
	}
	if len(resp.Messages[1].Media) != 1 || resp.Messages[1].Media[0] != "data:image/png;base64,abc123" {
		t.Fatalf("assistant media = %#v, want preserved media", resp.Messages[1].Media)
	}
	assertVisibleToolCallMessage(t, resp.Messages[2], "view_image")
}

func TestHandleGetSession_PreservesAttachmentsWhenAssistantToolCallContentDuplicatesSummary(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "detail-tool-summary-duplicate-content-with-attachments"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "check report"},
		{
			Role:    "assistant",
			Content: "Reviewing the generated report.",
			Attachments: []providers.Attachment{{
				Type:        "file",
				URL:         "https://example.com/report.txt",
				Filename:    "report.txt",
				ContentType: "text/plain",
			}},
			ToolCalls: []providers.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "read_file",
						Arguments: `{"path":"report.txt"}`,
					},
					ExtraContent: &providers.ExtraContent{
						ToolFeedbackExplanation: "Reviewing the generated report.",
					},
				},
			},
		},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/sessions/detail-tool-summary-duplicate-content-with-attachments",
		nil,
	)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []sessionChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Messages) != 3 {
		t.Fatalf("len(resp.Messages) = %d, want 3", len(resp.Messages))
	}
	if resp.Messages[1].Role != "assistant" {
		t.Fatalf("assistant message role = %q, want assistant", resp.Messages[1].Role)
	}
	if resp.Messages[1].Content != "Reviewing the generated report." {
		t.Fatalf("assistant content = %q, want preserved interim content", resp.Messages[1].Content)
	}
	if len(resp.Messages[1].Attachments) != 1 {
		t.Fatalf("len(assistant.Attachments) = %d, want 1", len(resp.Messages[1].Attachments))
	}
	if resp.Messages[1].Attachments[0].URL != "https://example.com/report.txt" {
		t.Fatalf("attachment url = %q, want report URL", resp.Messages[1].Attachments[0].URL)
	}
	assertVisibleToolCallMessage(t, resp.Messages[2], "read_file")
}

func TestHandleGetSession_UsesConfiguredToolFeedbackMaxArgsLength(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.Agents.Defaults.ToolFeedback.MaxArgsLength = 20
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	argsJSON := `{"path":"README.md","start_line":1,"end_line":10,"extra":"abcdefghijklmnopqrstuvwxyz"}`
	explanation := "Read README.md first to confirm the current project structure before editing the config example."
	sessionKey := picoSessionPrefix + "detail-tool-summary-max-args"
	err = store.AddFullMessage(nil, sessionKey, providers.Message{Role: "user", Content: "check file"})
	if err != nil {
		t.Fatalf("AddFullMessage(user) error = %v", err)
	}
	err = store.AddFullMessage(nil, sessionKey, providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: &providers.FunctionCall{
				Name:      "read_file",
				Arguments: argsJSON,
			},
			ExtraContent: &providers.ExtraContent{
				ToolFeedbackExplanation: explanation,
			},
		}},
	})
	if err != nil {
		t.Fatalf("AddFullMessage(assistant) error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/detail-tool-summary-max-args", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []sessionChatMessage `json:"messages"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Messages) < 2 {
		t.Fatalf("len(resp.Messages) = %d, want at least 2", len(resp.Messages))
	}

	wantArgsPreview := visibleAssistantToolArgsPreview(providers.ToolCall{
		Function: &providers.FunctionCall{Arguments: argsJSON},
	}, 20)
	toolCall := assertVisibleToolCallMessage(t, resp.Messages[1], "read_file")
	if toolCall.ExtraContent == nil || toolCall.ExtraContent.ToolFeedbackExplanation != explanation {
		t.Fatalf("tool call = %#v, want full explanation %q", toolCall, explanation)
	}
	if toolCall.Function == nil || toolCall.Function.Arguments != wantArgsPreview {
		t.Fatalf("tool call = %#v, want args preview %q", toolCall, wantArgsPreview)
	}
}

func TestHandleGetSession_FallsBackToLegacyToolArgumentsWhenExplanationMissing(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.Agents.Defaults.ToolFeedback.MaxArgsLength = 20
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	argsJSON := `{"path":"README.md","start_line":1,"end_line":10,"extra":"abcdefghijklmnopqrstuvwxyz"}`
	sessionKey := picoSessionPrefix + "detail-tool-summary-legacy-args"
	if err := store.AddFullMessage(
		nil,
		sessionKey,
		providers.Message{Role: "user", Content: "check file"},
	); err != nil {
		t.Fatalf("AddFullMessage(user) error = %v", err)
	}
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: &providers.FunctionCall{
				Name:      "read_file",
				Arguments: argsJSON,
			},
		}},
	}); err != nil {
		t.Fatalf("AddFullMessage(assistant) error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/detail-tool-summary-legacy-args", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []sessionChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Messages) < 2 {
		t.Fatalf("len(resp.Messages) = %d, want at least 2", len(resp.Messages))
	}

	wantPreview := visibleAssistantToolArgsPreview(providers.ToolCall{
		Function: &providers.FunctionCall{Arguments: argsJSON},
	}, 20)
	toolCall := assertVisibleToolCallMessage(t, resp.Messages[1], "read_file")
	if toolCall.Function == nil || toolCall.Function.Arguments != wantPreview {
		t.Fatalf("tool call = %#v, want legacy args preview %q", toolCall, wantPreview)
	}
}

func TestHandleGetSession_IncludesMediaOnlyMessages(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "detail-media-only"
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{
		Role:  "user",
		Media: []string{"data:image/png;base64,abc123"},
	}); err != nil {
		t.Fatalf("AddFullMessage(user) error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/detail-media-only", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Messages []struct {
			Role    string   `json:"role"`
			Content string   `json:"content"`
			Media   []string `json:"media"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Messages) != 1 {
		t.Fatalf("len(resp.Messages) = %d, want 1", len(resp.Messages))
	}
	if resp.Messages[0].Role != "user" || len(resp.Messages[0].Media) != 1 {
		t.Fatalf("message = %#v, want user message with media", resp.Messages[0])
	}
}

func TestHandleSessions_SupportsLargeMessages(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "detail-large-jsonl"
	largeContent := strings.Repeat("x", 9*1024*1024)
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{
		Role:    "user",
		Content: largeContent,
	}); err != nil {
		t.Fatalf("AddFullMessage() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	mux.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body=%s", listRec.Code, http.StatusOK, listRec.Body.String())
	}

	var items []sessionListItem
	if err := json.Unmarshal(listRec.Body.Bytes(), &items); err != nil {
		t.Fatalf("list Unmarshal() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}

	detailRec := httptest.NewRecorder()
	detailReq := httptest.NewRequest(http.MethodGet, "/api/sessions/detail-large-jsonl", nil)
	mux.ServeHTTP(detailRec, detailReq)

	if detailRec.Code != http.StatusOK {
		t.Fatalf(
			"detail status = %d, want %d, body=%s",
			detailRec.Code,
			http.StatusOK,
			detailRec.Body.String(),
		)
	}

	var resp struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("detail Unmarshal() error = %v", err)
	}
	if len(resp.Messages) != 1 {
		t.Fatalf("len(resp.Messages) = %d, want 1", len(resp.Messages))
	}
	if resp.Messages[0].Role != "user" {
		t.Fatalf("resp.Messages[0].Role = %q, want %q", resp.Messages[0].Role, "user")
	}
	if got := len(resp.Messages[0].Content); got != len(largeContent) {
		t.Fatalf("len(resp.Messages[0].Content) = %d, want %d", got, len(largeContent))
	}
}

func TestHandleListSessions_UsesImagePreviewForMediaOnlyMessage(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := picoSessionPrefix + "preview-media-only"
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{
		Role:  "user",
		Media: []string{"data:image/png;base64,abc123"},
	}); err != nil {
		t.Fatalf("AddFullMessage() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var items []sessionListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Preview != "[image]" {
		t.Fatalf("items[0].Preview = %q, want %q", items[0].Preview, "[image]")
	}
	if items[0].MessageCount != 1 {
		t.Fatalf("items[0].MessageCount = %d, want 1", items[0].MessageCount)
	}
}

func TestHandleDeleteSession_SQLiteStorage(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	sessionKey := legacyPicoSessionPrefix + "delete-jsonl"
	defer store.Close()
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{
		Role:    "user",
		Content: "delete me",
	}); err != nil {
		t.Fatalf("AddFullMessage() error = %v", err)
	}
	if err := store.SetSummary(nil, sessionKey, "delete summary"); err != nil {
		t.Fatalf("SetSummary() error = %v", err)
	}
	for _, sessionID := range []string{"delete-jsonl", "keep-inbox"} {
		if _, err := store.QueuePicoInbound(context.Background(), sessionID, "request",
			bus.InboundMessage{Channel: "pico", Content: sessionID}); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/delete-jsonl", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	pending, err := store.PendingPicoInbound(context.Background(), "pico", 0)
	if err != nil || len(pending) != 1 || pending[0].Message.Content != "keep-inbox" {
		t.Fatalf("unexpected inbox after session deletion: %v (%v)", pending, err)
	}
	base := filepath.Join(dir, sanitizeSessionKey(sessionKey))
	for _, path := range []string{base + ".jsonl", base + ".meta.json"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, stat err = %v", path, err)
		}
	}
}

func TestDeleteSessionReportsStoreFailure(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()
	dir := sessionsTestDir(t, configPath)
	if err := os.WriteFile(filepath.Join(dir, "sessions.db"), []byte("invalid sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewHandler(configPath).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/sessions/example", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteSessionReportsCleanupFailure(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()
	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(nil, legacyPicoSessionPrefix+"cleanup-error", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO app_state (namespace, state_key, value) VALUES ('chat_branches', 'cleanup-error', '{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_cleanup BEFORE DELETE ON app_state BEGIN SELECT RAISE(ABORT, 'cleanup failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewHandler(configPath).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/sessions/cleanup-error", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "session deleted") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetSession_LegacyJSONFallback(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	manager := session.NewSessionManager(dir)
	sessionKey := legacyPicoSessionPrefix + "legacy-json"
	manager.AddMessage(sessionKey, "user", "legacy user")
	manager.AddMessage(sessionKey, "assistant", "legacy assistant")
	if err := manager.Save(sessionKey); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/legacy-json", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestHandleSessions_FiltersEmptyJSONLFiles(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	base := filepath.Join(dir, sanitizeSessionKey(legacyPicoSessionPrefix+"empty-jsonl"))
	if err := os.WriteFile(base+".jsonl", []byte{}, 0o644); err != nil {
		t.Fatalf("WriteFile(jsonl) error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	mux.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body=%s", listRec.Code, http.StatusOK, listRec.Body.String())
	}

	var items []sessionListItem
	if err := json.Unmarshal(listRec.Body.Bytes(), &items); err != nil {
		t.Fatalf("Unmarshal(list) error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len(items) = %d, want 0", len(items))
	}

	detailRec := httptest.NewRecorder()
	detailReq := httptest.NewRequest(http.MethodGet, "/api/sessions/empty-jsonl", nil)
	mux.ServeHTTP(detailRec, detailReq)

	if detailRec.Code != http.StatusNotFound {
		t.Fatalf("detail status = %d, want %d, body=%s", detailRec.Code, http.StatusNotFound, detailRec.Body.String())
	}
}

func TestHandleGetSession_ReadsLegacyJSONLByID(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	sessionKey := legacyPicoSessionPrefix + "missing-meta"
	base := filepath.Join(dir, sanitizeSessionKey(sessionKey))
	line, err := json.Marshal(providers.Message{Role: "user", Content: "recover me"})
	if err != nil {
		t.Fatalf("Marshal(message) error = %v", err)
	}
	if err := os.WriteFile(base+".jsonl", append(line, '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile(jsonl) error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	mux.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body=%s", listRec.Code, http.StatusOK, listRec.Body.String())
	}

	var items []sessionListItem
	if err := json.Unmarshal(listRec.Body.Bytes(), &items); err != nil {
		t.Fatalf("Unmarshal(list) error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("legacy JSONL must be excluded from the session list: %#v", items)
	}

	searchRec := httptest.NewRecorder()
	mux.ServeHTTP(searchRec, httptest.NewRequest(http.MethodGet, "/api/sessions/search?q=recover%20me", nil))
	var results []sessionSearchResult
	if searchRec.Code != http.StatusOK || json.Unmarshal(searchRec.Body.Bytes(), &results) != nil ||
		len(results) != 0 {
		t.Fatalf("SQLite search should exclude standalone JSONL: %d %s", searchRec.Code, searchRec.Body.String())
	}

	detailRec := httptest.NewRecorder()
	detailReq := httptest.NewRequest(http.MethodGet, "/api/sessions/missing-meta", nil)
	mux.ServeHTTP(detailRec, detailReq)

	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want %d, body=%s", detailRec.Code, http.StatusOK, detailRec.Body.String())
	}
}

func TestHandleSessions_IgnoresMetaJSONWithoutDatabase(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	metaOnly := filepath.Join(dir, "agent_main_pico_direct_pico_meta-only.meta.json")
	metaOnlyContent := []byte(`{"key":"agent:main:pico:direct:pico:meta-only","summary":"meta only"}`)
	if err := os.WriteFile(metaOnly, metaOnlyContent, 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	mux.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body=%s", listRec.Code, http.StatusOK, listRec.Body.String())
	}

	var items []sessionListItem
	if err := json.Unmarshal(listRec.Body.Bytes(), &items); err != nil {
		t.Fatalf("Unmarshal(list) error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len(items) = %d, want 0", len(items))
	}
}

func TestSQLiteSessionListPagination(t *testing.T) {
	configPath, cleanup := setupModelTestEnv(t)
	defer cleanup()
	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, fixture := range []struct {
		id string
		ui string
	}{
		{"pinned", `{"pinned":true,"title":"Pinned title"}`},
		{"older", `{}`},
		{"archived", `{"archived":true}`},
		{"newer", `{}`},
		{"hidden-thought", `{}`},
	} {
		content := fixture.id
		role := "user"
		if fixture.id == "hidden-thought" {
			role, content = "tool", ""
		}
		err := store.AddFullMessage(nil, legacyPicoSessionPrefix+fixture.id, providers.Message{Role: role, Content: content})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateChatUIMeta(context.Background(), fixture.id, func([]byte) ([]byte, error) {
			return []byte(fixture.ui), nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	for suffix, payload := range map[string]string{".json": "{}", ".jsonl": `{"role":"user","content":"legacy"}` + "\n"} {
		extra := sanitizeSessionKey(legacyPicoSessionPrefix+"ignored") + suffix
		if err := os.WriteFile(filepath.Join(dir, extra), []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandler(configPath)
	get := func(query string) sessionListResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		h.handleListSessions(rec, httptest.NewRequest(http.MethodGet, "/api/sessions?limit=1"+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("list %s = %d %s", query, rec.Code, rec.Body)
		}
		var response sessionListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}

	query := ""
	for i, id := range []string{"pinned", "newer", "older"} {
		page := get(query)
		if len(page.Items) != 1 || page.Items[0].ID != id || page.Items[0].MessageCount != 1 || page.HasMore != (i < 2) {
			t.Fatalf("page for %s = %+v", id, page)
		}
		if i == 0 && (!page.Items[0].Pinned || page.Items[0].Title != "Pinned title") {
			t.Fatalf("pinned metadata = %+v", page.Items[0])
		}
		query = "&cursor=" + page.NextCursor
	}
	if page := get("&archived=1"); len(page.Items) != 1 || page.Items[0].ID != "archived" || page.HasMore {
		t.Fatalf("archived page = %+v", page)
	}
}

func BenchmarkSQLiteSessionListPage(b *testing.B) {
	root := b.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = root
	configPath := filepath.Join(root, "config.json")
	if err := config.SaveConfig(configPath, cfg); err != nil {
		b.Fatal(err)
	}

	dir := filepath.Join(root, "sessions")
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	for i := range 300 {
		id := strconv.Itoa(i)
		for j := range 12 {
			err := store.AddFullMessage(ctx, legacyPicoSessionPrefix+id, providers.Message{
				Role: "user", Content: "benchmark message " + strconv.Itoa(j),
			})
			if err != nil {
				b.Fatal(err)
			}
		}
		if err := store.UpdateChatUIMeta(ctx, id, func([]byte) ([]byte, error) {
			return []byte(`{"title":"benchmark message 0","title_source":"auto"}`), nil
		}); err != nil {
			b.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		b.Fatal(err)
	}

	for _, extraFile := range []bool{false, true} {
		name := "SQLite"
		if extraFile {
			marker := filepath.Join(dir, "unrelated.json")
			if err := os.WriteFile(marker, []byte("{}"), 0o600); err != nil {
				b.Fatal(err)
			}
			name = "WithUnrelatedFile"
		}
		for _, cold := range []bool{true, false} {
			mode := "Hot"
			if cold {
				mode = "ColdHandler"
			}
			b.Run(name+"/"+mode, func(b *testing.B) {
				h := NewHandler(configPath)
				req := httptest.NewRequest(http.MethodGet, "/api/sessions?limit=30", nil)
				samples := make([]time.Duration, 0, b.N)
				check := func(rec *httptest.ResponseRecorder) {
					b.Helper()
					var page sessionListResponse
					if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &page) != nil ||
						len(page.Items) != 30 || !page.HasMore {
						b.Fatalf("unexpected page: %d %s", rec.Code, rec.Body)
					}
				}
				if !cold {
					rec := httptest.NewRecorder()
					h.handleListSessions(rec, req)
					check(rec)
				}

				b.ResetTimer()
				for range b.N {
					if cold {
						h = NewHandler(configPath)
					}
					rec := httptest.NewRecorder()
					start := time.Now()
					h.handleListSessions(rec, req)
					samples = append(samples, time.Since(start))
					check(rec)
				}
				b.StopTimer()
				slices.Sort(samples)
				p95 := samples[(len(samples)*95+99)/100-1]
				b.ReportMetric(float64(p95)/float64(time.Millisecond), "ms/p95")
			})
		}
	}
}
