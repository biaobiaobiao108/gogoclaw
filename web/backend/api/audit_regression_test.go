package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/seahorse"
)

func TestRegressionDeleteClearsSQLiteContext(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := legacyPicoSessionPrefix + "delete-sqlite"
	if err := store.AddFullMessage(context.Background(), key, providers.Message{Role: "user", Content: "private deleted content"}); err != nil {
		t.Fatal(err)
	}
	engine, err := seahorse.NewEngine(seahorse.Config{DBPath: filepath.Join(dir, "seahorse.db")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.Ingest(context.Background(), key, []seahorse.Message{{Role: "user", Content: "private deleted content", TokenCount: 5}}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/sessions/delete-sqlite", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	result, err := engine.GetRetrieval().Grep(context.Background(), seahorse.GrepInput{Pattern: "%private deleted content%", Scope: "message"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) > 0 {
		t.Fatalf("deleted content remains searchable in SQLite: %+v", result.Messages)
	}
}
