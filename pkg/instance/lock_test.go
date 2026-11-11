package instance

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
)

func TestDataDirLockAcrossProcesses(t *testing.T) {
	if dir := os.Getenv("GOGOCLAW_TEST_INSTANCE_LOCK_DIR"); dir != "" {
		lock, err := AcquireDataDirLock(dir)
		if os.Getenv("GOGOCLAW_TEST_INSTANCE_LOCK_EXPECT_HELD") == "1" {
			if !errors.Is(err, fileutil.ErrFileLockHeld) {
				t.Fatalf("child acquisition error = %v, want lock-held error", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		// Exit without releasing: the OS must release the lock on process exit.
		_ = lock
		return
	}

	dir := t.TempDir()
	child := func(expectHeld bool) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestDataDirLockAcrossProcesses$")
		cmd.Env = append(os.Environ(), "GOGOCLAW_TEST_INSTANCE_LOCK_DIR="+dir)
		if expectHeld {
			cmd.Env = append(cmd.Env, "GOGOCLAW_TEST_INSTANCE_LOCK_EXPECT_HELD=1")
		}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("lock child failed: %v\n%s", err, output)
		}
	}
	lock, err := AcquireDataDirLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	child(true)
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	child(false)
	lock, err = AcquireDataDirLock(dir)
	if err != nil {
		t.Fatalf("acquire after child exit: %v", err)
	}
	defer lock.Release()
}

func TestAcquireDataDirLockIsExclusiveUntilReleased(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	first, err := AcquireDataDirLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	if second, err := AcquireDataDirLock(dir); !errors.Is(err, fileutil.ErrFileLockHeld) {
		if second != nil {
			_ = second.Release()
		}
		t.Fatalf("second acquisition = (%v, %v), want lock-held error", second, err)
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	third, err := AcquireDataDirLock(dir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := third.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireDataDirLockResolvesDirectoryAlias(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "data")
	alias := filepath.Join(root, "alias")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	lock, err := AcquireDataDirLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := AcquireDataDirLock(alias); !errors.Is(err, fileutil.ErrFileLockHeld) {
		t.Fatalf("alias acquisition error = %v, want lock-held error", err)
	}
}
