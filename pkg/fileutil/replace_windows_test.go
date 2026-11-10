//go:build windows

package fileutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteFileAtomicWaitsForOpenReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.meta.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	closed := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = reader.Close()
		close(closed)
	}()

	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}
	<-closed
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("file content = %q, want new", data)
	}
}
