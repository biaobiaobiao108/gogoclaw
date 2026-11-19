package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func TestIndexProjectFileFinishesAfterRequestCancellation(t *testing.T) {
	dir := t.TempDir()
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateChatProject(t.Context(), "Review", "")
	if err != nil {
		t.Fatal(err)
	}
	file, err := store.CreateChatProjectFile(t.Context(), memory.ChatProjectFile{
		ID: "review", ProjectID: project.ID, OriginalName: "review.txt", StoredName: "review.txt", Status: "processing",
	})
	if err != nil {
		t.Fatal(err)
	}
	filesDir := projectFilesDir(dir, project.ID)
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filesDir, file.StoredName), []byte("The shipment is ready."), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if indexed, err := indexChatProjectFile(ctx, dir, store, file); err != nil || indexed.Status != "ready" {
		t.Fatalf("index result = %#v, error = %v", indexed, err)
	}
	saved, err := store.GetChatProjectFile(t.Context(), project.ID, file.ID)
	if err != nil || saved.Status != "ready" || saved.IndexedAt.IsZero() {
		t.Fatalf("saved file = %#v, error = %v", saved, err)
	}
	chunks, err := store.ChatProjectFileChunks(t.Context(), project.ID, file.ID)
	if err != nil || len(chunks) != 1 {
		t.Fatalf("saved chunks = %#v, error = %v", chunks, err)
	}
}

func TestProjectFilesAndSessionOwnership(t *testing.T) {
	_, mux, dir := setupChatFeatureTest(t)
	rec := performJSONRequest(t, mux, http.MethodPost, "/api/projects", map[string]any{
		"name": "Launch Notes", "instructions": "Answer with evidence from project files.",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var project memoryChatProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}

	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, err := writer.CreateFormFile("file", "brief.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("# Launch\nThe launch codename is seabird.\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+project.ID+"/files", &form)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	fileRec := httptest.NewRecorder()
	mux.ServeHTTP(fileRec, req)
	if fileRec.Code != http.StatusCreated {
		t.Fatalf("upload project file status = %d, body = %s", fileRec.Code, fileRec.Body.String())
	}
	var file map[string]any
	if err := json.Unmarshal(fileRec.Body.Bytes(), &file); err != nil {
		t.Fatal(err)
	}
	if file["status"] != "ready" {
		t.Fatalf("project file = %#v", file)
	}

	const sessionID = "project-owned-session"
	seedChatFeatureSession(t, dir, sessionID)
	rec = performJSONRequest(t, mux, http.MethodPatch, "/api/sessions/"+sessionID, map[string]any{
		"project_id": project.ID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("assign session status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = performJSONRequest(t, mux, http.MethodGet, "/api/sessions?limit=20&project_id="+project.ID, nil)
	var page sessionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != sessionID || page.Items[0].ProjectName != "Launch Notes" {
		t.Fatalf("project page = %#v", page)
	}
	turnID := "project-turn-123e4567-e89b-12d3-a456-426614174000"
	sourceStore, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.AddFullMessage(t.Context(), legacyPicoSessionPrefix+sessionID, providers.Message{
		ID: turnID + "-llm-1", Role: "assistant", Content: "Use the blue failover procedure.",
	}); err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.ReplaceChatProjectTurnSources(t.Context(), sessionID, turnID, []memory.ChatProjectTurnSource{{
		ProjectID: project.ID, FileID: file["id"].(string), Filename: "brief.md", Locator: "Lines 1-2",
		Snippet: "The launch codename is seabird.", Ordinal: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	_ = sourceStore.Close()
	assertProjectSource := func() {
		t.Helper()
		rec := performJSONRequest(t, mux, http.MethodGet, "/api/sessions/"+sessionID+"?limit=20", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("session detail status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var detail struct {
			Messages []sessionChatMessage `json:"messages"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, message := range detail.Messages {
			if message.TurnID == turnID && len(message.ProjectSources) == 1 && message.ProjectSources[0].Filename == "brief.md" {
				found = true
			}
		}
		if !found {
			t.Fatalf("project sources missing from session detail: %s", rec.Body.String())
		}
	}
	assertProjectSource()
	rec = performJSONRequest(t, mux, http.MethodGet, "/api/sessions/search?q=flux", nil)
	var search []sessionSearchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &search); err != nil {
		t.Fatal(err)
	}
	if len(search) != 1 || search[0].ProjectID != project.ID {
		t.Fatalf("search = %#v", search)
	}

	rec = performJSONRequest(t, mux, http.MethodDelete, "/api/projects/"+project.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete project status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = performJSONRequest(t, mux, http.MethodGet, "/api/projects/"+project.ID, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted project status = %d, body = %s", rec.Code, rec.Body.String())
	}
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := store.ListChatProjects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := store.SessionChatProject(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	if len(projects) != 0 || assigned != nil {
		t.Fatalf("projects=%#v assigned=%#v", projects, assigned)
	}
	rec = performJSONRequest(t, mux, http.MethodGet, "/api/sessions?limit=20&project_id=none", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("uncategorized page status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var uncategorizedPage sessionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &uncategorizedPage); err != nil {
		t.Fatal(err)
	}
	if len(uncategorizedPage.Items) != 1 || uncategorizedPage.Items[0].ID != sessionID || uncategorizedPage.Items[0].ProjectID != "" {
		t.Fatalf("uncategorized page after project delete = %#v", uncategorizedPage)
	}
	assertProjectSource()
}

type memoryChatProjectResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
}
