package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/chatproject"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
)

type chatProjectDetail struct {
	memory.ChatProject
	Files []memory.ChatProjectFile `json:"files"`
}

func (h *Handler) registerChatProjectRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects", h.handleListChatProjects)
	mux.HandleFunc("POST /api/projects", h.handleCreateChatProject)
	mux.HandleFunc("GET /api/projects/{id}", h.handleGetChatProject)
	mux.HandleFunc("PATCH /api/projects/{id}", h.handleUpdateChatProject)
	mux.HandleFunc("DELETE /api/projects/{id}", h.handleDeleteChatProjectData)
	mux.HandleFunc("GET /api/projects/{id}/files", h.handleListChatProjectFiles)
	mux.HandleFunc("POST /api/projects/{id}/files", h.handleUploadChatProjectFile)
	mux.HandleFunc("GET /api/projects/{id}/files/{file_id}", h.handleGetChatProjectFile)
	mux.HandleFunc("DELETE /api/projects/{id}/files/{file_id}", h.handleDeleteChatProjectData)
	mux.HandleFunc("POST /api/projects/{id}/files/{file_id}/reindex", h.handleReindexChatProjectFile)
	mux.HandleFunc("GET /api/projects/{id}/files/{file_id}/content", h.handleChatProjectFileContent)
}

func (h *Handler) handleListChatProjects(w http.ResponseWriter, r *http.Request) {
	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	store, err := memory.OpenSQLiteStoreReadOnly(dir)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, []memory.ChatProject{})
		return
	}
	if err != nil {
		http.Error(w, "failed to open project store", http.StatusInternalServerError)
		return
	}
	defer store.Close()
	projects, err := store.ListChatProjects(r.Context())
	if err != nil {
		http.Error(w, "failed to list projects", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (h *Handler) handleCreateChatProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string `json:"name"`
		Instructions string `json:"instructions"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}
	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		http.Error(w, "failed to open project store", http.StatusInternalServerError)
		return
	}
	defer store.Close()
	project, err := store.CreateChatProject(r.Context(), body.Name, body.Instructions)
	if errors.Is(err, memory.ErrChatProjectNameExists) {
		http.Error(w, "a project with this name already exists", http.StatusConflict)
		return
	}
	if err != nil {
		if isProjectValidationError(err) {
			http.Error(w, err.Error(), http.StatusBadRequest)
		} else {
			http.Error(w, "failed to create project", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (h *Handler) handleGetChatProject(w http.ResponseWriter, r *http.Request) {
	_, store, project, ok := h.loadChatProjectForRequest(w, r)
	if !ok {
		return
	}
	defer store.Close()
	files, err := store.ListChatProjectFiles(r.Context(), project.ID)
	if err != nil {
		http.Error(w, "failed to list project files", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, chatProjectDetail{ChatProject: project, Files: files})
}

func (h *Handler) handleUpdateChatProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string `json:"name"`
		Instructions string `json:"instructions"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}
	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		http.Error(w, "failed to open project store", http.StatusInternalServerError)
		return
	}
	defer store.Close()
	project, err := store.UpdateChatProject(r.Context(), r.PathValue("id"), body.Name, body.Instructions)
	if errors.Is(err, memory.ErrChatProjectNotFound) {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, memory.ErrChatProjectNameExists) {
		http.Error(w, "a project with this name already exists", http.StatusConflict)
		return
	}
	if err != nil {
		if isProjectValidationError(err) {
			http.Error(w, err.Error(), http.StatusBadRequest)
		} else {
			http.Error(w, "failed to update project", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (h *Handler) handleDeleteChatProjectData(w http.ResponseWriter, r *http.Request) {
	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	projectID := strings.TrimSpace(r.PathValue("id"))
	rawFileID := r.PathValue("file_id")
	fileID := strings.TrimSpace(rawFileID)
	if !validAttachmentSessionID(projectID) || (rawFileID != "" && !validAttachmentSessionID(fileID)) {
		http.Error(w, "invalid project or file ID", http.StatusBadRequest)
		return
	}
	h.projectFilesMu.Lock()
	defer h.projectFilesMu.Unlock()
	store, err := memory.OpenSQLiteStore(dir)
	if errors.Is(err, os.ErrNotExist) {
		http.Error(w, "project or file not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to open project store", http.StatusInternalServerError)
		return
	}
	defer store.Close()

	ctx := r.Context()
	key := projectID
	if fileID == "" {
		err = store.DeleteChatProject(ctx, projectID)
	} else {
		key += "/" + fileID
		err = store.DeleteChatProjectFile(ctx, projectID, fileID)
	}
	if err != nil && !errors.Is(err, memory.ErrChatProjectNotFound) && !errors.Is(err, memory.ErrChatProjectFileNotFound) {
		logger.Warnf("failed to delete project data %s: %v", key, err)
		http.Error(w, "failed to delete project data", http.StatusInternalServerError)
		return
	}
	target, err := store.ChatProjectCleanup(ctx, key)
	if err == nil && target == "" {
		http.Error(w, "project or file not found", http.StatusNotFound)
		return
	}
	if err == nil {
		err = cleanChatProjectData(ctx, dir, store, key, target)
	}
	if err != nil {
		logger.Warnf("failed to clean deleted project data %s: %v", key, err)
		http.Error(w, "failed to clean deleted project data", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func cleanChatProjectData(ctx context.Context, dir string, store *memory.SQLiteStore, key, target string) error {
	projectID, _, isFile := strings.Cut(key, "/")
	if !validAttachmentSessionID(projectID) {
		return errors.New("invalid project cleanup ID")
	}
	var err error
	if isFile {
		if !filepath.IsLocal(target) || target == "." || filepath.Base(target) != target {
			return errors.New("invalid project cleanup filename")
		}
		err = os.Remove(filepath.Join(projectFilesDir(dir, projectID), target))
	} else {
		err = os.RemoveAll(projectFilesRoot(dir, projectID))
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return store.CompleteChatProjectCleanup(ctx, key)
}

func (h *Handler) handleListChatProjectFiles(w http.ResponseWriter, r *http.Request) {
	_, store, project, ok := h.loadChatProjectForRequest(w, r)
	if !ok {
		return
	}
	defer store.Close()
	files, err := store.ListChatProjectFiles(r.Context(), project.ID)
	if err != nil {
		http.Error(w, "failed to list project files", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, files)
}

func (h *Handler) handleUploadChatProjectFile(w http.ResponseWriter, r *http.Request) {
	dir, store, project, ok := h.loadChatProjectForRequest(w, r)
	if !ok {
		return
	}
	defer store.Close()
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
	input, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file is required", http.StatusBadRequest)
		return
	}
	defer input.Close()
	filename := sanitizeAttachmentFilename(header.Filename)
	if !chatproject.Supported(filename) {
		// Keep unsupported files so the settings UI can show a recoverable failure.
		logger.DebugCF("projects", "Uploading unsupported project file", map[string]any{"filename": filename})
	}
	// Keep project deletion from passing an upload that has published its file
	// but has yet to register it. Parsing and indexing can run independently.
	h.projectFilesMu.Lock()
	locked := true
	defer func() {
		if locked {
			h.projectFilesMu.Unlock()
		}
	}()
	if _, err := store.GetChatProject(r.Context(), project.ID); err != nil {
		if errors.Is(err, memory.ErrChatProjectNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
		} else {
			http.Error(w, "failed to read project", http.StatusInternalServerError)
		}
		return
	}
	projectDir := projectFilesDir(dir, project.ID)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		http.Error(w, "failed to create project file directory", http.StatusInternalServerError)
		return
	}
	fileID, err := memory.NewChatProjectID()
	if err != nil {
		http.Error(w, "failed to create project file", http.StatusInternalServerError)
		return
	}
	storedName := fileID + strings.ToLower(filepath.Ext(filename))
	target := filepath.Join(projectDir, storedName)
	temp, err := os.CreateTemp(projectDir, ".upload-*")
	if err != nil {
		http.Error(w, "failed to create project file", http.StatusInternalServerError)
		return
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	written, copyErr := io.Copy(temp, io.LimitReader(input, limit+1))
	if copyErr != nil {
		_ = temp.Close()
		http.Error(w, "failed to save project file", http.StatusInternalServerError)
		return
	}
	if written > limit {
		_ = temp.Close()
		http.Error(w, "project file exceeds size limit", http.StatusRequestEntityTooLarge)
		return
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		http.Error(w, "failed to save project file", http.StatusInternalServerError)
		return
	}
	if err := temp.Close(); err != nil {
		http.Error(w, "failed to save project file", http.StatusInternalServerError)
		return
	}
	if err := os.Rename(tempPath, target); err != nil {
		http.Error(w, "failed to save project file", http.StatusInternalServerError)
		return
	}
	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(filename))
	}
	file, err := store.CreateChatProjectFile(r.Context(), memory.ChatProjectFile{
		ID: fileID, ProjectID: project.ID, OriginalName: filename, StoredName: storedName,
		ContentType: contentType, SizeBytes: written, Status: "processing", CreatedAt: time.Now(),
	})
	if err != nil {
		if removeErr := os.Remove(target); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			logger.Warnf("remove unregistered project file: %v", removeErr)
		}
		switch {
		case errors.Is(err, memory.ErrChatProjectFileCount):
			http.Error(w, err.Error(), http.StatusTooManyRequests)
		case errors.Is(err, memory.ErrChatProjectStorage):
			http.Error(w, err.Error(), http.StatusInsufficientStorage)
		default:
			http.Error(w, "failed to record project file", http.StatusInternalServerError)
		}
		return
	}
	locked = false
	h.projectFilesMu.Unlock()
	file, err = indexChatProjectFile(r.Context(), dir, store, file)
	if err != nil {
		logger.Warnf("failed to index project file %s: %v", filename, err)
		http.Error(w, "failed to index project file", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, file)
}

func (h *Handler) handleGetChatProjectFile(w http.ResponseWriter, r *http.Request) {
	_, store, project, ok := h.loadChatProjectForRequest(w, r)
	if !ok {
		return
	}
	defer store.Close()
	file, err := store.GetChatProjectFile(r.Context(), project.ID, r.PathValue("file_id"))
	if errors.Is(err, memory.ErrChatProjectFileNotFound) {
		http.Error(w, "project file not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to read project file", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, file)
}

func (h *Handler) handleReindexChatProjectFile(w http.ResponseWriter, r *http.Request) {
	dir, store, project, ok := h.loadChatProjectForRequest(w, r)
	if !ok {
		return
	}
	defer store.Close()
	file, err := store.GetChatProjectFile(r.Context(), project.ID, r.PathValue("file_id"))
	if errors.Is(err, memory.ErrChatProjectFileNotFound) {
		http.Error(w, "project file not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to read project file", http.StatusInternalServerError)
		return
	}
	started, err := store.BeginChatProjectFileReindex(r.Context(), project.ID, file.ID)
	if err != nil {
		http.Error(w, "failed to start project file indexing", http.StatusInternalServerError)
		return
	}
	if !started {
		http.Error(w, "project file is already indexing or was removed", http.StatusConflict)
		return
	}
	file, err = indexChatProjectFile(r.Context(), dir, store, file)
	if err != nil {
		http.Error(w, "failed to reindex project file", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, file)
}

func (h *Handler) handleChatProjectFileContent(w http.ResponseWriter, r *http.Request) {
	_, store, project, ok := h.loadChatProjectForRequest(w, r)
	if !ok {
		return
	}
	defer store.Close()
	file, err := store.GetChatProjectFile(r.Context(), project.ID, r.PathValue("file_id"))
	if errors.Is(err, memory.ErrChatProjectFileNotFound) {
		http.Error(w, "project file not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to read project file", http.StatusInternalServerError)
		return
	}
	if file.Status != "ready" && file.IndexedAt.IsZero() {
		http.Error(w, "project file has no extracted text", http.StatusConflict)
		return
	}
	chunks, err := store.ChatProjectFileChunks(r.Context(), project.ID, file.ID)
	if err != nil {
		http.Error(w, "failed to read project file content", http.StatusInternalServerError)
		return
	}
	parts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		parts = append(parts, chunk.Content)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, strings.Join(parts, "\n\n"))
}

func (h *Handler) loadChatProjectForRequest(w http.ResponseWriter, r *http.Request) (string, *memory.SQLiteStore, memory.ChatProject, bool) {
	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return "", nil, memory.ChatProject{}, false
	}
	openStore := memory.OpenSQLiteStore
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		openStore = memory.OpenSQLiteStoreReadOnly
	}
	store, err := openStore(dir)
	if errors.Is(err, os.ErrNotExist) {
		http.Error(w, "project not found", http.StatusNotFound)
		return "", nil, memory.ChatProject{}, false
	}
	if err != nil {
		http.Error(w, "failed to open project store", http.StatusInternalServerError)
		return "", nil, memory.ChatProject{}, false
	}
	project, err := store.GetChatProject(r.Context(), r.PathValue("id"))
	if errors.Is(err, memory.ErrChatProjectNotFound) {
		_ = store.Close()
		http.Error(w, "project not found", http.StatusNotFound)
		return "", nil, memory.ChatProject{}, false
	}
	if err != nil {
		_ = store.Close()
		http.Error(w, "failed to read project", http.StatusInternalServerError)
		return "", nil, memory.ChatProject{}, false
	}
	return dir, store, project, true
}

func indexChatProjectFile(requestContext context.Context, dir string, store *memory.SQLiteStore, file memory.ChatProjectFile) (memory.ChatProjectFile, error) {
	path := filepath.Join(projectFilesDir(dir, file.ProjectID), file.StoredName)
	sections, err := chatproject.Extract(path, file.OriginalName)
	chunks := chatproject.ChunkSections(sections, chatproject.DefaultChunkTokens, chatproject.DefaultOverlapTokens)
	stored := make([]memory.ChatProjectChunk, 0, len(chunks))
	for _, chunk := range chunks {
		stored = append(stored, memory.ChatProjectChunk{
			Ordinal: chunk.Ordinal, Locator: chunk.Locator, Content: chunk.Content, TokenCount: chunk.TokenCount,
		})
	}
	// The original is already durable. Finish recording its index state even
	// when the uploading browser disconnects.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(requestContext), 30*time.Second)
	defer cancel()
	if err == nil && len(stored) == 0 {
		err = errors.New("file contains no extractable text")
	}
	now := time.Now()
	if err == nil {
		err = store.ReplaceChatProjectFileChunks(ctx, file.ProjectID, file.ID, stored, now)
	}
	if err != nil {
		failed, statusErr := store.FailChatProjectFileIndex(ctx, file.ID, err.Error())
		if statusErr != nil {
			return file, errors.Join(err, statusErr)
		}
		return failed, nil
	}
	file.Status, file.Error, file.IndexedAt = "ready", "", now
	return file, nil
}

func projectFilesRoot(sessionsDir, projectID string) string {
	return filepath.Join(sessionsDir, ".projects", projectID)
}

func projectFilesDir(sessionsDir, projectID string) string {
	return filepath.Join(projectFilesRoot(sessionsDir, projectID), "files")
}

func isProjectValidationError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "project name") || strings.Contains(message, "project instructions")
}

func projectFileContentURL(projectID, fileID string) string {
	return fmt.Sprintf("/api/projects/%s/files/%s/content", projectID, fileID)
}
