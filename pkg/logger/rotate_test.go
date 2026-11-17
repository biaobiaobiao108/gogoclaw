package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotatingFileWriterRotatesBySizeAndRetainsRecentBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.log")
	writer, err := newRotatingFileWriter(path, rotationConfig{
		MaxSizeBytes: 32,
		MaxBackups:   2,
		MaxAge:       time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	for range 4 {
		if _, err := writer.Write([]byte(strings.Repeat("x", 20))); err != nil {
			t.Fatal(err)
		}
	}

	backups, err := filepath.Glob(path + ".*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 2 {
		t.Fatalf("backup count = %d, want 2", len(backups))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 20 {
		t.Fatalf("current log size = %d, want 20", info.Size())
	}
}

func TestRotatingFileWriterPrunesExpiredBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.log")
	writer, err := newRotatingFileWriter(path, rotationConfig{
		MaxBackups: 5,
		MaxAge:     time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	oldBackup := path + ".old"
	if err := os.WriteFile(oldBackup, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldBackup, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	writer.pruneBackups(time.Now())
	if _, err := os.Stat(oldBackup); !os.IsNotExist(err) {
		t.Fatalf("expired backup still exists: %v", err)
	}
}

func TestRotationConfigFromEnv(t *testing.T) {
	t.Setenv(logMaxSizeEnvName, "7")
	t.Setenv(logMaxBackupsEnvName, "3")
	t.Setenv(logMaxAgeDaysEnvName, "14")
	t.Setenv(logRotateIntervalEnvName, "6")

	cfg := rotationConfigFromEnv()
	if cfg.MaxSizeBytes != 7<<20 || cfg.MaxBackups != 3 || cfg.MaxAge != 14*24*time.Hour || cfg.RotateInterval != 6*time.Hour {
		t.Fatalf("rotationConfigFromEnv() = %+v", cfg)
	}
}
