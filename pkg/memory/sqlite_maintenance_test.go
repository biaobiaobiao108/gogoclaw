package memory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func TestSQLiteBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	source := store.DBPath()
	if err := store.AddMessage(ctx, "session-a", "user", "before backup"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	backup := filepath.Join(dir, "backups", "sessions.db")
	if _, err := BackupSQLite(ctx, source, backup); err != nil {
		t.Fatal(err)
	}
	if mode := fileMode(t, backup); runtime.GOOS != "windows" && mode.Perm() != 0o600 {
		t.Fatalf("backup mode = %o, want 600", mode.Perm())
	}
	if _, err := CheckSQLiteIntegrity(ctx, backup, true); err != nil {
		t.Fatal(err)
	}

	store, err = OpenSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(ctx, "session-a", "assistant", "after backup"); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	result, err := RestoreSQLite(ctx, backup, source, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.SafetyBackup == "" {
		t.Fatal("restore did not create a safety backup")
	}
	if _, err := CheckSQLiteIntegrity(ctx, result.SafetyBackup, false); err != nil {
		t.Fatal(err)
	}
	history, err := readAllMessages(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Content != "before backup" {
		t.Fatalf("restored history = %+v", history)
	}
}

func TestRestoreSQLiteIncludesCommittedWAL(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "source.db")
	source, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	source.SetMaxOpenConns(1)
	if _, err := source.ExecContext(ctx, `PRAGMA journal_mode=WAL;
		CREATE TABLE example (value TEXT); INSERT INTO example VALUES ('checkpointed');
		PRAGMA wal_checkpoint(TRUNCATE); INSERT INTO example VALUES ('in WAL');`); err != nil {
		t.Fatal(err)
	}
	if fileSize(path+"-wal") == 0 {
		t.Fatal("fixture has no outstanding WAL")
	}
	target := filepath.Join(t.TempDir(), "restored.db")
	if _, err := RestoreSQLite(ctx, path, target, false); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM example WHERE value IN ('checkpointed', 'in WAL')").Scan(&count); err != nil || count != 2 {
		t.Fatalf("restored committed rows = %d, error = %v", count, err)
	}
}

func TestCheckSQLiteIntegrityRejectsCorruptDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckSQLiteIntegrity(context.Background(), path, false); err == nil {
		t.Fatal("expected corrupt database check to fail")
	}
}

func TestSQLiteStoreRejectsCorruptDatabaseAtStartup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, sqliteSessionFilename), []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSQLiteStore(dir); err == nil || !strings.Contains(strings.ToLower(err.Error()), "integrity") {
		t.Fatalf("OpenSQLiteStore() error = %v, want integrity failure", err)
	}
}

func TestSQLiteLargeDatabaseStartupDrill(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	const sessions = 1000
	const messagesPerSession = 10
	for sessionIndex := 0; sessionIndex < sessions; sessionIndex++ {
		key := fmt.Sprintf("startup-%04d", sessionIndex)
		for messageIndex := 0; messageIndex < messagesPerSession; messageIndex++ {
			msg := providers.Message{
				Role:    "user",
				Content: "startup drill message",
			}
			if err := store.AddFullMessage(ctx, key, msg); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	metas, err := store.ListSessionMeta(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != sessions {
		t.Fatalf("session count = %d, want %d", len(metas), sessions)
	}
	for _, meta := range metas {
		if meta.Count != messagesPerSession {
			t.Fatalf("session %s message count = %d, want %d", meta.Key, meta.Count, messagesPerSession)
		}
	}
}

func readAllMessages(path string) ([]providers.Message, error) {
	store, err := OpenSQLiteStore(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.GetHistory(context.Background(), "session-a")
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}

func TestRestorePreservesDamagedDatabase(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "sessions.db")
	target := filepath.Join(dir, "damaged.db")
	damaged := []byte("corrupt database contents")
	if err := os.WriteFile(target, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := RestoreSQLite(t.Context(), backup, target, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckSQLiteIntegrity(t.Context(), target, false); err != nil {
		t.Fatal(err)
	}
	preserved, err := os.ReadFile(result.SafetyBackup)
	if err != nil || string(preserved) != string(damaged) {
		t.Fatalf("preserved damaged source = %q, %v", preserved, err)
	}
}
