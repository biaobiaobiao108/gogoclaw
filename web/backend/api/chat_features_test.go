package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/chatmeta"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func setupChatFeatureTest(t *testing.T) (*Handler, *http.ServeMux, string) {
	t.Helper()
	configPath, cleanup := setupOAuthTestEnv(t)
	t.Cleanup(cleanup)
	dir := sessionsTestDir(t, configPath)
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return h, mux, dir
}

func seedChatFeatureSession(t *testing.T, dir, sessionID string) {
	t.Helper()
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := legacyPicoSessionPrefix + sessionID
	for _, message := range []providers.Message{
		{Role: "user", Content: "Explain the flux capacitor"},
		{Role: "assistant", Content: "It makes time travel possible."},
	} {
		if err := store.AddFullMessage(nil, key, message); err != nil {
			t.Fatal(err)
		}
	}
}

func performJSONRequest(
	t *testing.T,
	mux http.Handler,
	method, target string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, target, &payload)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestChatSessionMetadataSearchFeedbackAndArchive(t *testing.T) {
	_, mux, dir := setupChatFeatureTest(t)
	seedChatFeatureSession(t, dir, "feature-search")

	rec := performJSONRequest(t, mux, http.MethodPatch, "/api/sessions/feature-search", map[string]any{
		"title":  "Saved model notes",
		"pinned": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = performJSONRequest(t, mux, http.MethodGet, "/api/sessions/search?q=saved", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var results []sessionSearchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("search results = %+v, want one", results)
	}
	if results[0].Title != "Saved model notes" || !results[0].Pinned {
		t.Fatalf("search result = %+v", results[0])
	}
	if results[0].MessageCount != 2 {
		t.Fatalf("message_count = %d, want 2", results[0].MessageCount)
	}

	rec = performJSONRequest(t, mux, http.MethodGet, "/api/sessions/search?q=time%20travel", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("content search status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].MessageRole != "assistant" ||
		!strings.Contains(results[0].Snippet, "time travel") {
		t.Fatalf("content search results = %+v", results)
	}

	rec = performJSONRequest(
		t,
		mux,
		http.MethodPut,
		"/api/sessions/feature-search/feedback/message-1",
		map[string]string{"value": "up"},
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("feedback status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = performJSONRequest(t, mux, http.MethodGet, "/api/sessions/feature-search", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	feedback, _ := detail["feedback"].(map[string]any)
	if feedback["message-1"] != "up" {
		t.Fatalf("feedback = %+v", feedback)
	}

	rec = performJSONRequest(t, mux, http.MethodPatch, "/api/sessions/feature-search", map[string]any{
		"archived": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("archive status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = performJSONRequest(t, mux, http.MethodGet, "/api/sessions?limit=20", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("active list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var active sessionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &active); err != nil {
		t.Fatal(err)
	}
	if len(active.Items) != 0 {
		t.Fatalf("active items = %+v, want none", active.Items)
	}
	rec = performJSONRequest(t, mux, http.MethodGet, "/api/sessions?limit=20&archived=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("archived list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var archived sessionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &archived); err != nil {
		t.Fatal(err)
	}
	if len(archived.Items) != 1 || archived.Items[0].ID != "feature-search" {
		t.Fatalf("archived items = %+v", archived.Items)
	}
}

func TestUploadAttachmentServesDurableFile(t *testing.T) {
	_, mux, dir := setupChatFeatureTest(t)

	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, err := writer.CreateFormFile("file", "notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("# Durable attachment\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/uploads?session_id=upload-session",
		&form,
	)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var uploaded struct {
		Type     string `json:"type"`
		URL      string `json:"url"`
		Path     string `json:"path"`
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	if uploaded.Type != "file" || uploaded.Filename != "notes.md" {
		t.Fatalf("uploaded = %+v", uploaded)
	}
	attachmentRoot := filepath.Join(dir, ".attachments", "upload-session")
	if !strings.HasPrefix(filepath.Clean(uploaded.Path), filepath.Clean(attachmentRoot)+string(filepath.Separator)) {
		t.Fatalf("attachment path = %q, want within %q", uploaded.Path, attachmentRoot)
	}

	rec = performJSONRequest(t, mux, http.MethodGet, uploaded.URL, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("attachment status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "# Durable attachment\n" {
		t.Fatalf("attachment body = %q", got)
	}
}

func TestDeleteSessionRemovesChatData(t *testing.T) {
	h, mux, dir := setupChatFeatureTest(t)
	seedChatFeatureSession(t, dir, "delete-chat-data")

	attachmentDir := filepath.Join(dir, ".attachments", "delete-chat-data")
	if err := os.MkdirAll(attachmentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attachmentDir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchPath := chatmeta.BranchStorePath(dir)
	branchStore := chatmeta.BranchStore{Sessions: map[string]chatmeta.BranchSession{
		"delete-chat-data": {},
	}}
	if err := chatmeta.SaveBranches(branchPath, branchStore); err != nil {
		t.Fatal(err)
	}
	if err := h.updateChatUIMeta(dir, func(meta *chatUIMetaFile) error {
		meta.Sessions["delete-chat-data"] = chatUISessionMeta{Pinned: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rec := performJSONRequest(
		t,
		mux,
		http.MethodDelete,
		"/api/sessions/delete-chat-data",
		nil,
	)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(attachmentDir); !os.IsNotExist(err) {
		t.Fatalf("attachment directory still exists: %v", err)
	}
	branches, err := chatmeta.LoadBranches(branchPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := branches.Sessions["delete-chat-data"]; ok {
		t.Fatal("branch session was not removed")
	}
	meta, err := h.loadChatUIMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := meta.Sessions["delete-chat-data"]; ok {
		t.Fatal("chat metadata was not removed")
	}
}

func TestExtractSessionSourcesDeduplicatesAndFiltersURLs(t *testing.T) {
	sources := extractSessionSources(strings.Join([]string{
		"[Project docs](https://example.com/docs)",
		"1. Search result",
		"https://example.org/result",
		"https://example.com/docs",
		"mailto:test@example.com",
	}, "\n"))
	if len(sources) != 2 {
		t.Fatalf("sources = %+v, want two", sources)
	}
	if sources[0].URL != "https://example.com/docs" || sources[0].Title != "Project docs" {
		t.Fatalf("first source = %+v", sources[0])
	}
	if sources[1].URL != "https://example.org/result" || sources[1].Title != "Search result" {
		t.Fatalf("second source = %+v", sources[1])
	}
}

func TestSearchSnippetUsesRuneIndexes(t *testing.T) {
	content := "前文很长的一段说明，目标关键字在中间位置，后面还有内容。"
	snippet := searchSnippet(content, "目标关键字", 18)
	if !strings.Contains(snippet, "目标关键字") {
		t.Fatalf("snippet = %q", snippet)
	}
}
