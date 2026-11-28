package agent

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
)

const (
	toolOutputArtifactMaxFiles = 100
	toolOutputArtifactMaxAge   = 7 * 24 * time.Hour
)

var toolOutputArtifactsMu sync.Mutex

func writeToolOutputArtifact(path string, content []byte) error {
	toolOutputArtifactsMu.Lock()
	defer toolOutputArtifactsMu.Unlock()

	if err := fileutil.WriteFileAtomic(path, content, 0o600); err != nil {
		return err
	}
	pruneToolOutputArtifactsLocked(filepath.Dir(path), path)
	return nil
}

func ensureToolOutputArtifact(path string, content []byte) error {
	toolOutputArtifactsMu.Lock()
	defer toolOutputArtifactsMu.Unlock()

	if _, err := os.Stat(path); err == nil {
		pruneToolOutputArtifactsLocked(filepath.Dir(path), path)
		return nil
	}
	if err := fileutil.WriteFileAtomic(path, content, 0o600); err != nil {
		return err
	}
	pruneToolOutputArtifactsLocked(filepath.Dir(path), path)
	return nil
}

func pruneToolOutputArtifacts(dir string) {
	toolOutputArtifactsMu.Lock()
	defer toolOutputArtifactsMu.Unlock()
	pruneToolOutputArtifactsLocked(dir, "")
}

func pruneToolOutputArtifactsLocked(dir, keepPath string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.WarnCF("agent", "Tool output artifacts could not be listed", map[string]any{
				"dir":   dir,
				"error": err.Error(),
			})
		}
		return
	}

	type artifact struct {
		path    string
		modTime time.Time
	}
	items := make([]artifact, 0, len(entries))
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			logger.WarnCF("agent", "Tool output artifact metadata could not be read", map[string]any{
				"path":  filepath.Join(dir, entry.Name()),
				"error": infoErr.Error(),
			})
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if path == keepPath {
			items = append(items, artifact{path: path, modTime: info.ModTime()})
			continue
		}
		if now.Sub(info.ModTime()) > toolOutputArtifactMaxAge {
			removeToolOutputArtifact(path)
			continue
		}
		items = append(items, artifact{path: path, modTime: info.ModTime()})
	}
	if len(items) <= toolOutputArtifactMaxFiles {
		return
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].modTime.Equal(items[j].modTime) {
			return items[i].path < items[j].path
		}
		return items[i].modTime.Before(items[j].modTime)
	})
	for len(items) > toolOutputArtifactMaxFiles {
		removeIndex := -1
		for i, item := range items {
			if item.path != keepPath {
				removeIndex = i
				break
			}
		}
		if removeIndex < 0 {
			return
		}
		removeToolOutputArtifact(items[removeIndex].path)
		items = append(items[:removeIndex], items[removeIndex+1:]...)
	}
}

func removeToolOutputArtifact(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		logger.WarnCF("agent", "Tool output artifact could not be removed", map[string]any{
			"path":  path,
			"error": err.Error(),
		})
	}
}
