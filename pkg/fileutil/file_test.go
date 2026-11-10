package fileutil

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
)

func TestWriteFileAtomicBasic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.txt")
	data := []byte("hello gogoclaw")

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

func TestWriteFileAtomicReplaceFailureLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	if err := WriteFileAtomic(target, []byte("replacement"), 0o644); err == nil {
		t.Fatal("WriteFileAtomic() error = nil, want replace failure")
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("target changed after failed replace: info=%v err=%v", info, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, entry := range entries {
		if len(entry.Name()) >= len(".tmp-") && entry.Name()[:len(".tmp-")] == ".tmp-" {
			t.Fatalf("temporary file left after failed replace: %s", entry.Name())
		}
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

func TestWriteFileAtomicCrossProcessWriters(t *testing.T) {
	target := filepath.Join(t.TempDir(), "shared.txt")
	const workers = 4
	commands := make([]*exec.Cmd, 0, workers)
	for worker := 0; worker < workers; worker++ {
		cmd := exec.Command(os.Args[0], "-test.run=TestWriteFileAtomicCrossProcessHelper")
		cmd.Env = append(os.Environ(),
			"GOGOCLAW_ATOMIC_HELPER=1",
			"GOGOCLAW_ATOMIC_TARGET="+target,
			"GOGOCLAW_ATOMIC_WORKER="+strconv.Itoa(worker),
		)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper: %v", err)
		}
		commands = append(commands, cmd)
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper failed: %v", err)
		}
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if len(data) == 0 {
		t.Fatal("target is empty")
	}
	for _, b := range data[1:] {
		if b != data[0] {
			t.Fatal("cross-process atomic write mixed payloads")
		}
	}
}

func TestWriteFileAtomicCrossProcessHelper(t *testing.T) {
	if os.Getenv("GOGOCLAW_ATOMIC_HELPER") != "1" {
		return
	}
	target := os.Getenv("GOGOCLAW_ATOMIC_TARGET")
	worker, err := strconv.Atoi(os.Getenv("GOGOCLAW_ATOMIC_WORKER"))
	if err != nil || target == "" {
		os.Exit(2)
	}
	payload := bytes.Repeat([]byte{byte('A' + worker)}, 64*1024)
	for i := 0; i < 10; i++ {
		if err := WriteFileAtomic(target, payload, 0o600); err != nil {
			os.Exit(3)
		}
	}
	os.Exit(0)
}
