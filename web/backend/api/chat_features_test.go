package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/chatmeta"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	ppid "github.com/biaobiaobiao108/gogoclaw/pkg/pid"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
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

type uploadedAttachmentResponse struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Filename string `json:"filename"`
}

func uploadChatAttachment(
	t *testing.T,
	mux http.Handler,
	sessionID, filename, contentType, content string,
) uploadedAttachmentResponse {
	t.Helper()
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	headers := make(textproto.MIMEHeader)
	headers.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	if contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	part, err := writer.CreatePart(headers)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/uploads?session_id="+url.QueryEscape(sessionID),
		&form,
	)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var uploaded uploadedAttachmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	return uploaded
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
		ID       string `json:"id"`
		Type     string `json:"type"`
		URL      string `json:"url"`
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	if uploaded.ID == "" || uploaded.Type != "file" || uploaded.Filename != "notes.md" {
		t.Fatalf("uploaded = %+v", uploaded)
	}
	var rawUpload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rawUpload); err != nil {
		t.Fatal(err)
	}
	if _, ok := rawUpload["path"]; ok {
		t.Fatalf("upload response leaked a filesystem path: %s", rec.Body.String())
	}
	attachmentRoot := filepath.Join(dir, ".attachments", "upload-session")
	if _, err := os.Stat(filepath.Join(attachmentRoot, uploaded.ID)); err != nil {
		t.Fatalf("stored attachment = %q: %v", uploaded.ID, err)
	}

	rec = performJSONRequest(t, mux, http.MethodGet, uploaded.URL, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("attachment status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "# Durable attachment\n" {
		t.Fatalf("attachment body = %q", got)
	}
	if strings.Contains(rec.Body.String(), dir) {
		t.Fatal("attachment response leaked the server filesystem path")
	}
}

func TestUploadAttachmentSanitizesActiveContentAndDraftDelete(t *testing.T) {
	_, mux, _ := setupChatFeatureTest(t)
	uploaded := uploadChatAttachment(t, mux, "active-content", "diagram.svg", "image/svg+xml", `<svg onload="alert(1)"></svg>`)
	if uploaded.Type != "file" || !strings.HasPrefix(uploaded.URL, "/api/attachments/active-content/") {
		t.Fatalf("uploaded SVG = %+v", uploaded)
	}
	rec := performJSONRequest(t, mux, http.MethodGet, uploaded.URL, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("attachment status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("SVG content type = %q", got)
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("SVG disposition = %q", rec.Header().Get("Content-Disposition"))
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("SVG CSP = %q", rec.Header().Get("Content-Security-Policy"))
	}

	rec = performJSONRequest(
		t,
		mux,
		http.MethodDelete,
		"/api/uploads?session_id=active-content&id="+uploaded.ID,
		nil,
	)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = performJSONRequest(t, mux, http.MethodGet, uploaded.URL, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted attachment status = %d", rec.Code)
	}
}

func TestUploadAttachmentEnforcesSessionQuota(t *testing.T) {
	_, mux, dir := setupChatFeatureTest(t)
	attachmentDir := filepath.Join(dir, ".attachments", "quota-session")
	if err := os.MkdirAll(attachmentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxSessionAttachmentCount; i++ {
		name := filepath.Join(attachmentDir, "existing-"+strconv.Itoa(i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, err := writer.CreateFormFile("file", "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/uploads?session_id=quota-session", &form)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("quota status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCleanupOrphanedAttachmentsStreamsReferences(t *testing.T) {
	h, _, dir := setupChatFeatureTest(t)
	sessionID := "orphan-attachments"
	attachmentDir := filepath.Join(dir, ".attachments", sessionID)
	if err := os.MkdirAll(attachmentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	referenced := "111111111111111111111111-referenced.txt"
	orphan := "222222222222222222222222-orphan.txt"
	referencedPath := filepath.Join(attachmentDir, referenced)
	orphanPath := filepath.Join(attachmentDir, orphan)
	for _, path := range []string{referencedPath, orphanPath} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-48 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddFullMessage(nil, legacyPicoSessionPrefix+sessionID, providers.Message{
		Role:    "user",
		Content: "attached",
		Attachments: []providers.Attachment{{
			Ref: referencedPath, Filename: "referenced.txt", Type: "file",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.cleanupOrphanedSessionAttachments(dir, sessionID, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(referencedPath); err != nil {
		t.Fatalf("referenced attachment was removed: %v", err)
	}
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("orphan attachment still exists: %v", err)
	}
}

func TestSessionAttachmentURLUsesManagedPathInsteadOfStoredURL(t *testing.T) {
	attachments := sessionAttachments("managed-session", providers.Message{
		Attachments: []providers.Attachment{{
			Ref:      filepath.Join(t.TempDir(), ".attachments", "managed-session", "stored.txt"),
			URL:      "https://attacker.example/file.txt",
			Filename: "stored.txt",
			Type:     "file",
		}},
	})
	if len(attachments) != 1 {
		t.Fatalf("attachments = %+v", attachments)
	}
	if attachments[0].URL != "/api/attachments/managed-session/stored.txt" {
		t.Fatalf("URL = %q", attachments[0].URL)
	}
	untrusted := sessionAttachments("managed-session", providers.Message{
		Attachments: []providers.Attachment{{
			URL: "javascript:alert(1)",
		}},
	})
	if len(untrusted) != 0 {
		t.Fatalf("unsafe attachment URL was returned: %+v", untrusted)
	}
}

func TestArchivedSessionSearchSeparatesResults(t *testing.T) {
	_, mux, dir := setupChatFeatureTest(t)
	seedChatFeatureSession(t, dir, "active-search")
	seedChatFeatureSession(t, dir, "archived-search")
	rec := performJSONRequest(t, mux, http.MethodPatch, "/api/sessions/archived-search", map[string]any{
		"archived": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("archive status = %d, body = %s", rec.Code, rec.Body.String())
	}

	for _, test := range []struct {
		query    string
		wantID   string
		archived bool
	}{
		{query: "/api/sessions/search?q=time%20travel", wantID: "active-search"},
		{query: "/api/sessions/search?q=time%20travel&archived=1", wantID: "archived-search", archived: true},
	} {
		rec = performJSONRequest(t, mux, http.MethodGet, test.query, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("search status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var results []sessionSearchResult
		if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].ID != test.wantID || results[0].Archived != test.archived {
			t.Fatalf("search results = %+v, want %s archived=%v", results, test.wantID, test.archived)
		}
	}
}

func TestChatMetadataUpdatesMigrateLegacyFileFirst(t *testing.T) {
	h, _, dir := setupChatFeatureTest(t)
	legacy := chatUIMetaFile{
		Version: 1,
		Sessions: map[string]chatUISessionMeta{
			"legacy-meta": {
				Title:  "Kept title",
				Pinned: true,
				Feedback: map[string]string{
					"message-1": "up",
				},
			},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := chatUIMetaPath(dir)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.updateChatUISessionMeta(dir, "legacy-meta", func(meta *chatUISessionMeta) error {
		meta.Archived = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy metadata file still exists: %v", err)
	}
	meta, err := h.loadChatUIMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := meta.Sessions["legacy-meta"]
	if got.Title != "Kept title" || !got.Pinned || !got.Archived || got.Feedback["message-1"] != "up" {
		t.Fatalf("migrated metadata = %+v", got)
	}
}

func TestSessionDirsMatchesRoutedAgentWorkspaces(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	testHome := filepath.Dir(configPath)
	t.Setenv("USERPROFILE", testHome)
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Agents.Defaults.Workspace = "~/default-workspace"
	cfg.Agents.List = []config.AgentConfig{
		{ID: "Support Agent"},
		{ID: "custom", Workspace: "~/custom-workspace"},
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(configPath)
	dirs, err := h.sessionDirs()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		filepath.Clean(filepath.Join(testHome, "default-workspace", "sessions")):       false,
		filepath.Clean(filepath.Join(testHome, "workspace-support-agent", "sessions")): false,
		filepath.Clean(filepath.Join(testHome, "custom-workspace", "sessions")):        false,
	}
	for _, dir := range dirs {
		if _, ok := want[filepath.Clean(dir)]; ok {
			want[filepath.Clean(dir)] = true
		}
	}
	for path, found := range want {
		if !found {
			t.Fatalf("session dirs = %+v, missing %s", dirs, path)
		}
	}
}

func TestDeleteUploadRemovesDuplicateManagedFilesAcrossAgents(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	mainWorkspace := filepath.Join(filepath.Dir(configPath), "main-workspace")
	supportWorkspace := filepath.Join(filepath.Dir(configPath), "support-workspace")
	cfg.Agents.Defaults.Workspace = mainWorkspace
	cfg.Agents.List = []config.AgentConfig{
		{ID: "main", Default: true, Workspace: mainWorkspace},
		{ID: "support", Workspace: supportWorkspace},
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	const attachmentID = "333333333333333333333333-duplicate.txt"
	paths := []string{
		filepath.Join(mainWorkspace, "sessions", ".attachments", "duplicate-session", attachmentID),
		filepath.Join(supportWorkspace, "sessions", ".attachments", "duplicate-session", attachmentID),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := performJSONRequest(
		t,
		mux,
		http.MethodDelete,
		"/api/uploads?session_id=duplicate-session&id="+attachmentID,
		nil,
	)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("duplicate attachment still exists at %s: %v", path, err)
		}
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
	branchPath := chatmeta.BranchStorePath(chatmeta.BranchStoreRoot(config.GetHome()), "delete-chat-data")
	branchStore := chatmeta.BranchStore{Sessions: map[string]chatmeta.BranchSession{
		"delete-chat-data": {},
	}}
	if err := chatmeta.SaveBranches(branchPath, branchStore); err != nil {
		t.Fatal(err)
	}
	if err := h.updateChatUISessionMeta(dir, "delete-chat-data", func(meta *chatUISessionMeta) error {
		meta.Pinned = true
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

func TestExtractSessionSourcesOnlyUsesStructuredSearchResults(t *testing.T) {
	sources := extractSessionSources(strings.Join([]string{
		"[Project docs](https://example.com/docs)",
		"See https://example.com/ordinary-link for context.",
		"1. Search result",
		"https://example.org/result",
		"mailto:test@example.com",
	}, "\n"))
	if len(sources) != 1 {
		t.Fatalf("sources = %+v, want one", sources)
	}
	if sources[0].URL != "https://example.org/result" || sources[0].Title != "Search result" {
		t.Fatalf("first source = %+v", sources[0])
	}
}

func TestSearchSnippetUsesRuneIndexes(t *testing.T) {
	content := "前文很长的一段说明，目标关键字在中间位置，后面还有内容。"
	snippet := searchSnippet(content, "目标关键字", 18)
	if !strings.Contains(snippet, "目标关键字") {
		t.Fatalf("snippet = %q", snippet)
	}
}

func TestUploadPreservesMediaTypes(t *testing.T) {
	_, mux, _ := setupChatFeatureTest(t)
	for _, tc := range []struct{ filename, contentType, kind string }{
		{"photo.png", "image/png", "image"},
		{"recording.mp3", "audio/mpeg", "audio"},
		{"movie.mp4", "video/mp4", "video"},
		{"fallback.png", "", "image"},
		{"diagram.svg", "image/svg+xml", "file"},
		{"page.html", "text/html", "file"},
	} {
		t.Run(tc.filename, func(t *testing.T) {
			uploaded := uploadChatAttachment(t, mux, "media-types", tc.filename, tc.contentType, "content")
			if uploaded.Type != tc.kind {
				t.Fatalf("type = %q, want %q", uploaded.Type, tc.kind)
			}
		})
	}
}

func TestBranchAttachmentsSurviveCleanupAndCannotBeDeleted(t *testing.T) {
	h, mux, dir := setupChatFeatureTest(t)
	const sessionID = "branch-attachment"
	seedChatFeatureSession(t, dir, sessionID)
	uploaded := uploadChatAttachment(t, mux, sessionID, "photo.png", "image/png", "image")
	path := filepath.Join(dir, ".attachments", sessionID, uploaded.ID)
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	branchPath := chatmeta.BranchStorePath(chatmeta.BranchStoreRoot(config.GetHome()), sessionID)
	branches := chatmeta.BranchStore{Sessions: map[string]chatmeta.BranchSession{
		sessionID: {Groups: map[string]chatmeta.BranchGroup{
			"root": {Versions: []chatmeta.BranchVersion{{ID: "old-answer", Messages: []providers.Message{{
				Role: "user", Attachments: []providers.Attachment{{Ref: path, Type: "image"}},
			}}}}},
		}},
	}}
	if err := chatmeta.SaveBranches(branchPath, branches); err != nil {
		t.Fatal(err)
	}
	if err := h.cleanupOrphanedSessionAttachments(dir, sessionID, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("branch attachment was removed: %v", err)
	}
	rec := performJSONRequest(t, mux, http.MethodDelete, "/api/uploads?session_id="+sessionID+"&id="+uploaded.ID, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Unreadable branch data must not authorize deletion of its attachments.
	if err := os.WriteFile(branchPath, []byte("invalid JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec = performJSONRequest(t, mux, http.MethodDelete, "/api/uploads?session_id="+sessionID+"&id="+uploaded.ID, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("corrupt branch delete status = %d", rec.Code)
	}
	if err := h.cleanupOrphanedSessionAttachments(dir, sessionID, 24*time.Hour); err == nil {
		t.Fatal("cleanup must report unreadable branch references")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("attachment was removed after reference read failure: %v", err)
	}
}

func TestCachedSessionSearchMatchesEscapedContent(t *testing.T) {
	_, mux, dir := setupChatFeatureTest(t)
	const sessionID = "escaped-search"
	seedChatFeatureSession(t, dir, sessionID)
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddFullMessage(nil, legacyPicoSessionPrefix+sessionID, providers.Message{
		Role: "assistant", Content: "Example: <div> & \"quoted\" C:\\path\nnext line",
	}); err != nil {
		t.Fatal(err)
	}
	// Populate the session list cache so search uses its streaming reader.
	rec := performJSONRequest(t, mux, http.MethodGet, "/api/sessions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	for _, query := range []string{"<div>", "&", `"quoted"`, `C:\path`, "path\nnext"} {
		rec := performJSONRequest(t, mux, http.MethodGet, "/api/sessions/search?q="+url.QueryEscape(query), nil)
		var results []sessionSearchResult
		if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].ID != sessionID {
			t.Fatalf("search %q = %s", query, rec.Body.String())
		}
	}
}

func TestDeleteSessionProtectsLiveTurnStates(t *testing.T) {
	resetGatewayTestState(t)
	_, mux, dir := setupChatFeatureTest(t)
	cmd := startLongRunningProcess(t)
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	gateway.mu.Lock()
	gateway.cmd = cmd
	gateway.pidData = &ppid.PidFileData{PID: cmd.Process.Pid}
	gateway.mu.Unlock()
	for _, legacy := range []bool{false, true} {
		for _, status := range []string{"running", "awaiting_approval", "resuming", "completed", "interrupted"} {
			t.Run(fmt.Sprintf("legacy=%t/%s", legacy, status), func(t *testing.T) {
				id := fmt.Sprintf("delete-%t-%s", legacy, status)
				key := legacyPicoSessionPrefix + id
				checkpoint := session.TurnCheckpoint{TurnID: "turn", Status: status}
				var path string
				if legacy {
					manager := session.NewSessionManager(dir)
					manager.AddMessage(key, "user", "keep this history")
					if err := manager.SetTurnCheckpoint(key, checkpoint); err != nil {
						t.Fatal(err)
					}
					if err := manager.Save(key); err != nil {
						t.Fatal(err)
					}
					path = filepath.Join(dir, sanitizeSessionKey(key)+".json")
				} else {
					seedChatFeatureSession(t, dir, id)
					store, err := memory.NewJSONLStore(dir)
					if err != nil {
						t.Fatal(err)
					}
					backend := session.NewJSONLBackend(store)
					if err := backend.SetTurnCheckpoint(key, checkpoint); err != nil {
						t.Fatal(err)
					}
					path = filepath.Join(dir, sanitizeSessionKey(key)+".jsonl")
				}
				rec := performJSONRequest(t, mux, http.MethodDelete, "/api/sessions/"+id, nil)
				want := http.StatusConflict
				if status == "completed" || status == "interrupted" {
					want = http.StatusNoContent
				}
				if rec.Code != want {
					t.Fatalf("status = %d, want %d, body = %s", rec.Code, want, rec.Body.String())
				}
				if want == http.StatusConflict {
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("active history removed: %v", err)
					}
				}
			})
		}
	}
}
