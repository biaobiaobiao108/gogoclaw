package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadConfigFilesRollsBackInterruptedSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	oldConfig, oldSecurity := []byte("old config"), []byte("old security")
	if err := writeConfigFiles(path, oldConfig, oldSecurity); err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(configRollback{filepath.Base(path), oldConfig, oldSecurity})
	if err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(dir, ".config-rollback.json")
	if err := os.WriteFile(pending, record, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(securityPath(path), []byte("partially committed security"), 0o600); err != nil {
		t.Fatal(err)
	}
	gotConfig, gotSecurity, err := readConfigFiles(path)
	if err != nil || string(gotConfig) != string(oldConfig) || string(gotSecurity) != string(oldSecurity) {
		t.Fatalf("read pair = %q / %q, %v", gotConfig, gotSecurity, err)
	}
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Fatalf("rollback record still present: %v", err)
	}
}
