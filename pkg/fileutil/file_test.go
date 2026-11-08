package fileutil

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestWriteFileAtomicBasic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.txt")
	data := []byte("hello picoclaw")

	if err := WriteFileAtomic(path, data, 0o644); err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("file content = %q, want %q", got, data)
	}
}

func TestWriteFileAtomicPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.txt")

	if err := WriteFileAtomic(path, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteFileAtomicOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overwrite.txt")

	if err := WriteFileAtomic(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("first WriteFileAtomic() error = %v", err)
	}
	if err := WriteFileAtomic(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("second WriteFileAtomic() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("file content = %q, want %q", got, "new")
	}
}

func TestWriteFileAtomicEmptyData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")

	if err := WriteFileAtomic(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("file length = %d, want 0", len(got))
	}
}

func TestWriteFileAtomicCreatesParentDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "c", "deep.txt")

	if err := WriteFileAtomic(path, []byte("deep"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "deep" {
		t.Fatalf("file content = %q, want deep", got)
	}
}

func TestWriteFileAtomicNoTempFileOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.txt")

	if err := WriteFileAtomic(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "clean.txt" {
		t.Fatalf("unexpected directory entries: %#v", entries)
	}
}

func TestWriteFileAtomicLargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.bin")
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := WriteFileAtomic(path, data, 0o644); err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if len(got) != len(data) {
		t.Fatalf("file size = %d, want %d", len(got), len(data))
	}
}

func TestWriteFileAtomicInvalidPath(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("blocked"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := WriteFileAtomic(filepath.Join(parent, "file.txt"), []byte("data"), 0o644)
	if err == nil {
		t.Fatal("WriteFileAtomic() error = nil, want failure")
	}
}

func TestWriteFileAtomicConcurrentWriters(t *testing.T) {
	target := filepath.Join(t.TempDir(), "config.json")
	const writers = 32

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- WriteFileAtomic(target, []byte{byte(i)}, 0o600)
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("WriteFileAtomic() error = %v", err)
		}
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if len(data) != 1 {
		t.Fatalf("file length = %d, want 1", len(data))
	}
}
