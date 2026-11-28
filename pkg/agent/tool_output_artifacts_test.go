package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneToolOutputArtifactsRemovesExpiredAndExcessFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	expiredPath := filepath.Join(dir, "expired.txt")
	if err := os.WriteFile(expiredPath, []byte("expired"), 0o600); err != nil {
		t.Fatal(err)
	}
	expiredAt := now.Add(-toolOutputArtifactMaxAge - time.Hour)
	if err := os.Chtimes(expiredPath, expiredAt, expiredAt); err != nil {
		t.Fatal(err)
	}

	paths := make([]string, 0, toolOutputArtifactMaxFiles+4)
	for i := 0; i < toolOutputArtifactMaxFiles+4; i++ {
		path := filepath.Join(dir, fmt.Sprintf("artifact-%03d.txt", i))
		if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}
		modTime := now.Add(-time.Duration(toolOutputArtifactMaxFiles+4-i) * time.Minute)
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}

	pruneToolOutputArtifacts(dir)

	if _, err := os.Stat(expiredPath); !os.IsNotExist(err) {
		t.Fatalf("expired artifact still exists or stat failed: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != toolOutputArtifactMaxFiles {
		t.Fatalf("artifact count = %d, want %d", len(entries), toolOutputArtifactMaxFiles)
	}
	for i := 0; i < 4; i++ {
		if _, err := os.Stat(paths[i]); !os.IsNotExist(err) {
			t.Fatalf("oldest artifact %q still exists or stat failed: %v", paths[i], err)
		}
	}
	if _, err := os.Stat(paths[4]); err != nil {
		t.Fatalf("newer artifact %q was removed: %v", paths[4], err)
	}
}
