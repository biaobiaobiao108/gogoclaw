package media

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/google/uuid"
)

const TempDirName = "gogoclaw_media"

var processTempDir = fmt.Sprintf("process-%d-%s", os.Getpid(), uuid.NewString())

// TempDir returns the shared temporary directory used for downloaded media.
func TempDir() string {
	return filepath.Join(os.TempDir(), TempDirName, processTempDir)
}

// cleanAbandonedTempDirs only removes directories belonging to dead processes.
// A reused PID is treated as alive, preferring delayed cleanup over data loss.
func cleanAbandonedTempDirs(maxAge time.Duration) {
	root := filepath.Join(os.TempDir(), TempDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.WarnCF("media", "cannot inspect temporary directory", map[string]any{"error": err.Error()})
		}
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "process-") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(entry.Name(), "process-"), "-", 2)
		if len(parts) != 2 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil || pid <= 0 || processAlive(pid) {
			continue
		}
		if _, err := uuid.Parse(parts[1]); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < maxAge {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			logger.WarnCF("media", "cannot remove abandoned temporary directory", map[string]any{"error": err.Error()})
		}
	}
}
