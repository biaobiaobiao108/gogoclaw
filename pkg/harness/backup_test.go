package harness

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

// A stopped instance is copied to a fresh data directory, as in the documented backup procedure.
func TestStoppedDataBackupRestoresSessionAndWorkspace(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	restored := filepath.Join(root, "restored")
	if err := os.MkdirAll(filepath.Join(source, "workspace"), 0700); err != nil {
		t.Fatal(err)
	}
	const filename = "artifact.txt"
	const content = "backup artifact"
	if err := os.WriteFile(filepath.Join(source, "workspace", filename), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := memory.NewSQLiteStore(filepath.Join(source, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	backend := session.NewStoreBackend(store)
	const key = "agent:main:backup-test"
	result, err := Run(context.Background(), Scenario{Name: "backup", SessionKey: key, Workspace: filepath.Join(source, "workspace"), Session: backend, UserMessage: "backup marker 482", Steps: []ModelStep{Text("saved marker 482")}})
	if err != nil {
		t.Fatal(err)
	}
	if err = result.Verify(Expect{NoError: true}); err != nil {
		t.Fatal(err)
	}
	if err = backend.Close(); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(restored, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !entry.Type().IsRegular() {
			t.Fatalf("unexpected file type: %s", path)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		t.Fatal(err)
	}
	restoredStore, err := memory.NewSQLiteStore(filepath.Join(restored, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	restoredBackend := session.NewStoreBackend(restoredStore)
	defer restoredBackend.Close()
	history, err := restoredBackend.GetHistoryChecked(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Content != "backup marker 482" || history[1].Content != "saved marker 482" {
		t.Fatalf("restored history: %+v", history)
	}
	data, err := os.ReadFile(filepath.Join(restored, "workspace", filename))
	if err != nil || string(data) != content {
		t.Fatalf("restored artifact %q: %v", data, err)
	}
}
