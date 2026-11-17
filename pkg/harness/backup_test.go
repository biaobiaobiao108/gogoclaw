package harness

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/credential"
	"github.com/biaobiaobiao108/gogoclaw/pkg/cron"
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
	keyPath := filepath.Join(source, "test-key")
	if err := os.WriteFile(keyPath, []byte("isolated test key"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(credential.SSHKeyPathEnvVar, keyPath)
	t.Setenv(credential.PassphraseEnvVar, "test passphrase")
	encrypted, err := credential.Encrypt("test passphrase", keyPath, "test credential")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	model := &config.ModelConfig{ModelName: "backup-model", Provider: "openai", Model: "test-model"}
	model.SetAPIKey(encrypted)
	cfg.ModelList = config.SecureModelList{model}
	if err := config.SaveConfig(filepath.Join(source, "config.json"), cfg); err != nil {
		t.Fatal(err)
	}
	scheduler := cron.NewCronService(filepath.Join(source, "sessions"), nil)
	at := int64(4102444800000) // A future job must not execute during the test.
	job, err := scheduler.AddJob("backup job", cron.CronSchedule{Kind: "at", AtMS: &at}, "job payload", "test", "recipient")
	if err != nil {
		t.Fatal(err)
	}
	scheduler.Stop()
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
	t.Setenv(credential.SSHKeyPathEnvVar, filepath.Join(restored, "test-key"))
	restoredConfig, err := config.LoadConfig(filepath.Join(restored, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(restoredConfig.ModelList) != 1 || restoredConfig.ModelList[0].ModelName != "backup-model" {
		t.Fatalf("restored model configuration: %+v", restoredConfig.ModelList)
	}
	if len(restoredConfig.ModelList[0].APIKeys) != 1 {
		t.Fatal("restored model credential is missing")
	}
	resolved, err := credential.NewResolver(restored).Resolve(restoredConfig.ModelList[0].APIKeys[0].String())
	if err != nil || resolved != "test credential" {
		t.Fatalf("restored credential: value matches=%t, error=%v", resolved == "test credential", err)
	}
	restoredScheduler := cron.NewCronService(filepath.Join(restored, "sessions"), nil)
	restoredJob, ok := restoredScheduler.GetJob(job.ID)
	if !ok || restoredJob.Name != "backup job" || restoredJob.Payload.Message != "job payload" || restoredJob.Schedule.AtMS == nil || *restoredJob.Schedule.AtMS != at {
		t.Fatalf("restored scheduled job: %+v", restoredJob)
	}
	restoredScheduler.Stop()
}
