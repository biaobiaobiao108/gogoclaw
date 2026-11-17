package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultLogMaxSizeMB      = 10
	defaultLogMaxBackups     = 5
	defaultLogMaxAgeDays     = 30
	defaultLogRotateHours    = 24
	logMaxSizeEnvName        = "GOGOCLAW_LOG_MAX_SIZE_MB"
	logMaxBackupsEnvName     = "GOGOCLAW_LOG_MAX_BACKUPS"
	logMaxAgeDaysEnvName     = "GOGOCLAW_LOG_MAX_AGE_DAYS"
	logRotateIntervalEnvName = "GOGOCLAW_LOG_ROTATE_HOURS"
)

type rotationConfig struct {
	MaxSizeBytes   int64
	MaxBackups     int
	MaxAge         time.Duration
	RotateInterval time.Duration
}

func defaultRotationConfig() rotationConfig {
	return rotationConfig{
		MaxSizeBytes:   int64(defaultLogMaxSizeMB) << 20,
		MaxBackups:     defaultLogMaxBackups,
		MaxAge:         defaultLogMaxAgeDays * 24 * time.Hour,
		RotateInterval: defaultLogRotateHours * time.Hour,
	}
}

func rotationConfigFromEnv() rotationConfig {
	cfg := defaultRotationConfig()
	cfg.MaxSizeBytes = int64(nonNegativeEnvInt(logMaxSizeEnvName, defaultLogMaxSizeMB)) << 20
	cfg.MaxBackups = nonNegativeEnvInt(logMaxBackupsEnvName, defaultLogMaxBackups)
	cfg.MaxAge = time.Duration(nonNegativeEnvInt(logMaxAgeDaysEnvName, defaultLogMaxAgeDays)) * 24 * time.Hour
	cfg.RotateInterval = time.Duration(nonNegativeEnvInt(logRotateIntervalEnvName, defaultLogRotateHours)) * time.Hour
	return cfg
}

func nonNegativeEnvInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

type rotatingFileWriter struct {
	mu       sync.Mutex
	path     string
	cfg      rotationConfig
	file     *os.File
	openedAt time.Time
	now      func() time.Time
}

func newRotatingFileWriter(path string, cfg rotationConfig) (*rotatingFileWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}
	return &rotatingFileWriter{
		path:     path,
		cfg:      cfg,
		file:     file,
		openedAt: time.Now(),
		now:      time.Now,
	}, nil
}

func (w *rotatingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.now()
	info, err := w.file.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat log file: %w", err)
	}
	sizeRotate := w.cfg.MaxSizeBytes > 0 &&
		info.Size() > 0 &&
		info.Size()+int64(len(p)) > w.cfg.MaxSizeBytes
	timeRotate := w.cfg.RotateInterval > 0 &&
		now.Sub(w.openedAt) >= w.cfg.RotateInterval
	if sizeRotate || timeRotate {
		if err := w.rotate(now); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

func (w *rotatingFileWriter) rotate(now time.Time) error {
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("close log file for rotation: %w", err)
	}
	backupPath := w.nextBackupPath(now)
	if err := os.Rename(w.path, backupPath); err != nil {
		file, openErr := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if openErr == nil {
			w.file = file
		}
		return fmt.Errorf("rotate log file: %w", err)
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open rotated log file: %w", err)
	}
	w.file = file
	w.openedAt = now
	w.pruneBackups(now)
	return nil
}

func (w *rotatingFileWriter) nextBackupPath(now time.Time) string {
	base := w.path + "." + now.Format("20060102-150405.000000000")
	candidate := base
	for suffix := 1; ; suffix++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
		candidate = fmt.Sprintf("%s.%d", base, suffix)
	}
}

func (w *rotatingFileWriter) pruneBackups(now time.Time) {
	paths, err := filepath.Glob(w.path + ".*")
	if err != nil {
		return
	}
	type backup struct {
		path    string
		modTime time.Time
	}
	backups := make([]backup, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if w.cfg.MaxAge > 0 && now.Sub(info.ModTime()) > w.cfg.MaxAge {
			_ = os.Remove(path)
			continue
		}
		backups = append(backups, backup{path: path, modTime: info.ModTime()})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].modTime.Equal(backups[j].modTime) {
			return backups[i].path > backups[j].path
		}
		return backups[i].modTime.After(backups[j].modTime)
	})
	for index := w.cfg.MaxBackups; index < len(backups); index++ {
		_ = os.Remove(backups[index].path)
	}
}

func (w *rotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
