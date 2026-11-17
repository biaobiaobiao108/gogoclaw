package storage

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
)

func TestStorageCommandsBackupCheckAndRestore(t *testing.T) {
	dir := t.TempDir()
	store, err := memory.NewSQLiteStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(context.Background(), "session", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	dbPath := store.DBPath()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	backupPath := filepath.Join(dir, "state-backup.db")
	cmd := NewStorageCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"backup", "--db", dbPath, "--output", backupPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("backup command: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "Backup created") {
		t.Fatalf("backup output = %q", output.String())
	}

	output.Reset()
	cmd = NewStorageCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"check", "--db", backupPath, "--full"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("check command: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "Database OK") {
		t.Fatalf("check output = %q", output.String())
	}

	output.Reset()
	cmd = NewStorageCommand()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"restore", "--db", dbPath, "--force", backupPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restore command: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "sessions.db.before-restore-") {
		t.Fatalf("restore output = %q", output.String())
	}
}
