package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/instance"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/seahorse"
)

func TestManagedSessionsChannelHistoryAndDelete(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	scope := json.RawMessage(`{"version":1,"agent_id":"main","channel":"telegram","account":"default","values":{"chat":"123"}}`)
	if err := store.UpsertSessionMeta(nil, "test-channel", scope, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(nil, "test-channel", "user", "hello telegram"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerManagedSessionRoutes(mux)
	call := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}
	rec := call("GET", "/api/session-management")
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var page managedSessionPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].Scope.Channel != "telegram" {
		t.Fatalf("list: %+v %v", page, err)
	}
	path := "/api/session-management/" + page.Items[0].ID
	rec = call("GET", path)
	if rec.Code != 200 || !containsManaged(rec.Body.String(), "hello telegram") {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}
	rec = call("DELETE", path)
	if rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = call("GET", path)
	if rec.Code != 404 {
		t.Fatalf("deleted detail: %d", rec.Code)
	}
}
func containsManaged(s, sub string) bool { return strings.Contains(s, sub) }

func TestManagedSessionsCursorAndSearch(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		key, user string
	}{
		{"z", "other"},
		{"y", "É用户_100%"},
		{"x", "é用户_100%"},
		{"w", "é用户X100Y"},
	} {
		scope, err := json.Marshal(map[string]any{"values": map[string]string{"user": entry.user}})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertSessionMeta(nil, entry.key, scope, nil); err != nil {
			t.Fatal(err)
		}
		if err := store.AddMessage(nil, entry.key, "user", "hello"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// Make the cursor cross sessions whose timestamps are identical.
	db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE session_meta SET updated_at = 1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	NewHandler(configPath).registerManagedSessionRoutes(mux)
	read := func(query url.Values) managedSessionPage {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/session-management?"+query.Encode(), nil))
		var page managedSessionPage
		if err := json.Unmarshal(rec.Body.Bytes(), &page); rec.Code != 200 || err != nil {
			t.Fatalf("list: %d %s (%v)", rec.Code, rec.Body.String(), err)
		}
		return page
	}
	query := url.Values{"limit": {"1"}}
	for i, key := range []string{"z", "y", "x", "w"} {
		page := read(query)
		if len(page.Items) != 1 || page.Items[0].Key != key || page.Items[0].Count != 1 || (page.NextCursor != "") != (i < 3) {
			t.Fatalf("unexpected page for %q: %+v", key, page)
		}
		query.Set("cursor", page.NextCursor)
	}
	query.Set("q", "É用户_100%")
	for i, key := range []string{"y", "x"} {
		page := read(query)
		if len(page.Items) != 1 || page.Items[0].Key != key || (page.NextCursor != "") != (i == 0) {
			t.Fatalf("unexpected search page for %q: %+v", key, page)
		}
		query.Set("cursor", page.NextCursor)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/session-management?cursor=invalid", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor: %d %s", rec.Code, rec.Body.String())
	}
}

func TestManagedDeleteFailurePreservesIndex(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(nil, "channel", "user", "history"); err != nil {
		t.Fatal(err)
	}
	store.Close()
	engine, err := seahorse.NewEngine(seahorse.Config{DBPath: filepath.Join(dir, "sessions.db")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Ingest(context.Background(), "channel", []seahorse.Message{{Role: "user", Content: "history"}}); err != nil {
		t.Fatal(err)
	}
	engine.Close()
	db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER deny_session_delete BEFORE DELETE ON session_meta BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerManagedSessionRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/session-management/"+managedID(dir, "channel"), nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	index, err := sql.Open("sqlite", filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	var count int
	if err := index.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("index lost: count=%d err=%v", count, err)
	}
}

func TestManagedDeleteWaitsForRuntimeLock(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const key = "cli:default"
	if err := store.AddMessage(nil, key, "user", "history"); err != nil {
		t.Fatal(err)
	}
	lock, err := instance.AcquireDataDirLock(globalConfigDir())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	mux := http.NewServeMux()
	NewHandler(configPath).registerManagedSessionRoutes(mux)
	path := "/api/session-management/" + managedID(dir, key)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path, nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete while runtime held lock: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("session lost while runtime held lock: %d", rec.Code)
	}
}

func TestManagedDeleteRetriesCleanup(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const key, id = "session-key", "review-session"
	scope := json.RawMessage(`{"version":1,"agent_id":"main","channel":"pico","values":{"sender":"pico:review-session"}}`)
	if err := store.UpsertSessionMeta(nil, key, scope, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(nil, key, "user", "history"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateChatUIMeta(nil, id, func([]byte) ([]byte, error) {
		return []byte(`{"title":"test"}`), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAppState(nil, "chat_branches", id, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", store.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER deny_ui_delete BEFORE DELETE ON chat_ui_meta
		BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewHandler(configPath).registerManagedSessionRoutes(mux)
	path := "/api/session-management/" + managedID(dir, key)
	call := func(method string, want int) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		if rec.Code != want {
			t.Fatalf("%s: status %d, want %d: %s", method, rec.Code, want, rec.Body.String())
		}
	}
	call(http.MethodDelete, http.StatusInternalServerError)
	call(http.MethodGet, http.StatusOK)
	if _, err := db.Exec("DROP TRIGGER deny_ui_delete"); err != nil {
		t.Fatal(err)
	}

	// Prevent completing the cleanup record even after the filesystem work.
	if _, err := db.Exec(`CREATE TRIGGER deny_cleanup_completion BEFORE DELETE ON app_state
		WHEN OLD.namespace = 'session_attachment_cleanup'
		BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	attachmentDir := filepath.Join(dir, ".attachments", id)
	if err := os.MkdirAll(attachmentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	call(http.MethodDelete, http.StatusInternalServerError)
	call(http.MethodGet, http.StatusNotFound)
	if pending, err := store.PendingSessionCleanup(nil, key); err != nil || pending != id {
		t.Fatalf("cleanup record: %q, %v", pending, err)
	}
	if meta, err := store.ChatUIMeta(nil, id); err != nil || meta != nil {
		t.Fatalf("session UI metadata remained: %s, %v", meta, err)
	}
	if branches, err := store.GetAppState(nil, "chat_branches", id); err != nil || branches != nil {
		t.Fatalf("session branches remained: %s, %v", branches, err)
	}
	if _, err := db.Exec("DROP TRIGGER deny_cleanup_completion"); err != nil {
		t.Fatal(err)
	}
	call(http.MethodDelete, http.StatusNoContent)
	if _, err := os.Stat(attachmentDir); !os.IsNotExist(err) {
		t.Fatalf("attachment directory still exists: %v", err)
	}
	if pending, err := store.PendingSessionCleanup(nil, key); err != nil || pending != "" {
		t.Fatalf("cleanup record remains: %q, %v", pending, err)
	}
}
