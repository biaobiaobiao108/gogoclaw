package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindGogoclawBinaryInAncestorsUsesRepositoryBuild(t *testing.T) {
	repoDir := t.TempDir()
	nestedDir := filepath.Join(repoDir, "web", "backend")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module example.com/test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}

	binaryName := "gogoclaw-test"
	binaryPath := filepath.Join(repoDir, "build", binaryName)
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(build) error = %v", err)
	}
	if err := os.WriteFile(binaryPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile(binary) error = %v", err)
	}

	if got := findGogoclawBinaryInAncestors(nestedDir, binaryName); got != binaryPath {
		t.Fatalf("findGogoclawBinaryInAncestors() = %q, want %q", got, binaryPath)
	}
}
