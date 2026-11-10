package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// FileLock is an exclusive advisory lock backed by an operating-system file
// lock. The kernel releases it automatically if the process exits or crashes.
type FileLock struct {
	file *os.File
}

// AcquireFileLock blocks until the exclusive lock for path is available.
func AcquireFileLock(path string) (*FileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := lockFileHandle(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquire lock file: %w", err)
	}
	return &FileLock{file: f}, nil
}

// Unlock releases the lock. It is safe to call more than once.
func (l *FileLock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	unlockErr := unlockFileHandle(f)
	closeErr := f.Close()
	if unlockErr != nil {
		return fmt.Errorf("release lock file: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close lock file: %w", closeErr)
	}
	return nil
}
