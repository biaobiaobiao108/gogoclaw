package instance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
)

const lockFileName = ".gogoclaw.instance.lock"

// DataDirLock prevents two GoGoClaw runtimes from writing the same data dir.
type DataDirLock struct {
	lock *fileutil.FileLock
}

// AcquireDataDirLock acquires the singleton lock for dataDir without waiting.
// The returned lock must be held for the lifetime of the runtime.
func AcquireDataDirLock(dataDir string) (*DataDirLock, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("data directory is empty")
	}

	path, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data directory: %w", err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}

	lock, err := fileutil.TryAcquireFileLock(filepath.Join(path, lockFileName))
	if err != nil {
		return nil, fmt.Errorf("lock data directory %q: %w", path, err)
	}
	return &DataDirLock{lock: lock}, nil
}

// Release releases the data directory lock.
func (l *DataDirLock) Release() error {
	if l == nil || l.lock == nil {
		return nil
	}
	lock := l.lock
	l.lock = nil
	return lock.Unlock()
}
