package api

import (
	"bufio"
	"bytes"
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
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/chatmeta"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

const (
	maxSessionAttachmentCount = 200
	maxSessionAttachmentBytes = 256 << 20
	orphanAttachmentAge       = 24 * time.Hour
)

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
	defer r.MultipartForm.RemoveAll()
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
	dir, err := h.sessionDirForSession(sessionID)
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

	h.attachmentMu.Lock()
	defer h.attachmentMu.Unlock()
	if h.attachmentCleanupAt == nil {
		h.attachmentCleanupAt = make(map[string]time.Time)
	}
	if lastCleanup := h.attachmentCleanupAt[attachmentDir]; time.Since(lastCleanup) >= time.Hour {
		h.attachmentCleanupAt[attachmentDir] = time.Now()
		if err := h.cleanupOrphanedSessionAttachments(dir, sessionID, orphanAttachmentAge); err != nil {
			logger.Warnf("failed to clean orphaned attachments for session %s: %v", sessionID, err)
		}
	}
	count, totalBytes, err := sessionAttachmentUsage(attachmentDir)
	if err != nil {
		http.Error(w, "failed to inspect attachment storage", http.StatusInternalServerError)
		return
	}
	if count >= maxSessionAttachmentCount {
		http.Error(w, "session attachment count limit reached", http.StatusTooManyRequests)
		return
	}
	if totalBytes+written > maxSessionAttachmentBytes {
		http.Error(w, "session attachment storage limit reached", http.StatusInsufficientStorage)
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
		"id": storedName, "type": attachmentType, "url": urlValue,
		"filename": filename, "content_type": contentType,
	})
}

func (h *Handler) handleUploadConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := loadConfigForAPI(h.configPath)
	if err != nil {
		http.Error(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"max_media_size": cfg.Agents.Defaults.GetMaxMediaSize(),
	})
}

func (h *Handler) handleDeleteUpload(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	attachmentID := strings.TrimSpace(r.URL.Query().Get("id"))
	if !validAttachmentSessionID(sessionID) || filepath.Base(attachmentID) != attachmentID || attachmentID == "." || attachmentID == "" {
		http.Error(w, "invalid attachment", http.StatusBadRequest)
		return
	}
	dirs, err := h.sessionDirs()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	h.attachmentMu.Lock()
	defer h.attachmentMu.Unlock()
	for _, dir := range dirs {
		referenced, err := h.attachmentReferencedBySession(dir, sessionID, attachmentID)
		if err != nil {
			http.Error(w, "failed to inspect attachment references", http.StatusInternalServerError)
			return
		}
		if referenced {
			http.Error(w, "attachment is already referenced by a message", http.StatusConflict)
			return
		}
	}
	removed := false
	for _, dir := range dirs {
		path := filepath.Join(dir, ".attachments", sessionID, attachmentID)
		if err := os.Remove(path); err == nil {
			removed = true
		} else if !errors.Is(err, os.ErrNotExist) {
			http.Error(w, "failed to delete attachment", http.StatusInternalServerError)
			return
		}
	}
	if removed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.NotFound(w, r)
}

func (h *Handler) handleAttachment(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	name := filepath.Base(r.PathValue("name"))
	if !validAttachmentSessionID(sessionID) || name == "." || name == "" {
		http.NotFound(w, r)
		return
	}
	dirs, err := h.sessionDirs()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	var path string
	for _, dir := range dirs {
		candidate := filepath.Join(dir, ".attachments", sessionID, name)
		info, statErr := os.Lstat(candidate)
		if statErr == nil && info.Mode().IsRegular() {
			path = candidate
			break
		}
	}
	if path == "" {
		http.NotFound(w, r)
		return
	}
	writeAttachmentResponse(w, r, path, name)
}

func sessionAttachmentUsage(dir string) (int, int64, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	count := 0
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".upload-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		count++
		total += info.Size()
	}
	return count, total, nil
}

func (h *Handler) cleanupOrphanedSessionAttachments(sessionsDir, sessionID string, age time.Duration) error {
	attachmentDir := filepath.Join(sessionsDir, ".attachments", sessionID)
	entries, err := os.ReadDir(attachmentDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	references, err := h.sessionAttachmentReferences(sessionsDir, sessionID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	cutoff := time.Now().Add(-age)
	var cleanupErrs []error
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".upload-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if _, referenced := references[entry.Name()]; referenced {
			continue
		}
		if err := os.Remove(filepath.Join(attachmentDir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	return errors.Join(cleanupErrs...)
}

func (h *Handler) attachmentReferencedBySession(sessionsDir, sessionID, attachmentID string) (bool, error) {
	references, err := h.sessionAttachmentReferences(sessionsDir, sessionID)
	if err != nil {
		return false, err
	}
	_, ok := references[attachmentID]
	return ok, nil
}

func (h *Handler) sessionAttachmentReferences(sessionsDir, sessionID string) (map[string]struct{}, error) {
	references := make(map[string]struct{})
	addMessages := func(messages []providers.Message) {
		for _, message := range messages {
			for _, attachment := range message.Attachments {
				if id := utils.DurableAttachmentID(attachment); id != "" {
					references[id] = struct{}{}
				}
			}
		}
	}
	if ref, err := h.findPicoJSONLSession(sessionsDir, sessionID); err == nil {
		sess, err := h.readJSONLSession(sessionsDir, ref.Key)
		if err != nil {
			return nil, err
		}
		addMessages(sess.Messages)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if ref, err := h.findLegacyPicoSession(sessionsDir, sessionID); err == nil {
		sess, err := h.readLegacySession(ref.Path)
		if err != nil {
			return nil, err
		}
		addMessages(sess.Messages)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// Inactive answers still own their attachments even when the live history
	// has been replaced by a different branch.
	branches, err := chatmeta.LoadBranches(sessionsDir, sessionID)
	if err != nil {
		return nil, err
	}
	for _, group := range branches.Sessions[sessionID].Groups {
		for _, version := range group.Versions {
			addMessages(version.Messages)
		}
	}
	return references, nil
}

func readSessionAttachmentReferences(path string) (map[string]struct{}, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	references := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSessionJSONLLineSize)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(`"attachments"`)) {
			continue
		}
		var message providers.Message
		if err := json.Unmarshal(line, &message); err != nil {
			continue
		}
		for _, attachment := range message.Attachments {
			if id := utils.DurableAttachmentID(attachment); id != "" {
				references[id] = struct{}{}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return references, nil
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
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(filename))) {
	case ".svg", ".svgz", ".html", ".htm", ".xhtml", ".xml", ".js", ".mjs":
		return "file"
	}
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

func writeAttachmentResponse(w http.ResponseWriter, r *http.Request, path, filename string) {
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "failed to open attachment", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.Error(w, "failed to stat attachment", http.StatusInternalServerError)
		return
	}

	contentType := mime.TypeByExtension(filepath.Ext(filename))
	attachmentType := attachmentTypeFor(contentType, filename)
	disposition := "inline"
	if attachmentType == "file" {
		disposition = "attachment"
		if activeAttachmentContent(filename, contentType) {
			contentType = "application/octet-stream"
			w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		}
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if value := mime.FormatMediaType(disposition, map[string]string{"filename": filename}); value != "" {
		w.Header().Set("Content-Disposition", value)
	}
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func activeAttachmentContent(filename, contentType string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(filename))) {
	case ".svg", ".svgz", ".html", ".htm", ".xhtml", ".xml", ".js", ".mjs":
		return true
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	return strings.Contains(contentType, "html") ||
		strings.Contains(contentType, "svg") ||
		strings.Contains(contentType, "xml") ||
		strings.Contains(contentType, "javascript")
}

func loadConfigForAPI(path string) (*config.Config, error) {
	return config.LoadConfig(path)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
