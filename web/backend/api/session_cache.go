package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/seahorse"
)

func sessionCachePaths(dir string, ref picoJSONLSessionRef) []string {
	if ref.Storage == "sqlite" {
		dbPath := filepath.Join(dir, "sessions.db")
		return []string{dbPath, dbPath + "-wal"}
	}
	base := filepath.Join(dir, sanitizeSessionKey(ref.Key))
	return []string{base + ".jsonl", base + ".meta.json"}
}

func sessionFilesStamp(paths ...string) (string, bool) {
	var builder strings.Builder
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if strings.HasSuffix(path, "-wal") && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", false
		}
		fmt.Fprintf(&builder, "%s:%d:%d;", path, info.Size(), info.ModTime().UnixNano())
	}
	return builder.String(), true
}

func (h *Handler) cachedSessionListItem(key string, paths ...string) (sessionListItem, bool) {
	stamp, ok := sessionFilesStamp(paths...)
	if !ok {
		return sessionListItem{}, false
	}
	h.sessionListMu.Lock()
	defer h.sessionListMu.Unlock()
	entry, ok := h.sessionListCache[key]
	if !ok || entry.stamp != stamp {
		delete(h.sessionListCache, key)
		return sessionListItem{}, false
	}
	return entry.item, true
}

func (h *Handler) storeCachedSessionListItem(key string, item sessionListItem, paths ...string) {
	stamp, ok := sessionFilesStamp(paths...)
	if !ok {
		return
	}
	h.sessionListMu.Lock()
	h.sessionListCache[key] = cachedSessionListItem{item: item, stamp: stamp}
	h.sessionListMu.Unlock()
}

func (h *Handler) pruneSessionListCache(valid map[string]struct{}, dir string) {
	h.sessionListMu.Lock()
	defer h.sessionListMu.Unlock()
	for key := range h.sessionListCache {
		if !sessionCacheKeyMatchesDir(key, dir) {
			continue
		}
		if _, ok := valid[key]; !ok {
			delete(h.sessionListCache, key)
		}
	}
}

func sessionCacheKeyMatchesDir(key, dir string) bool {
	cleanDir := filepath.Clean(dir)
	return strings.HasPrefix(key, "jsonl:"+cleanDir+":") ||
		strings.HasPrefix(key, "legacy:"+cleanDir+":")
}

func clearSessionSQLite(r *http.Request, dir, key string) error {
	dbPath := filepath.Join(dir, "sessions.db")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	engine, err := seahorse.NewEngine(seahorse.Config{DBPath: dbPath}, nil)
	if err != nil {
		return err
	}
	clearErr := engine.ClearSession(r.Context(), key)
	return errors.Join(clearErr, engine.Close())
}
