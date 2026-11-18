package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
)

// A rollback record is durable before either member of the pair changes.
// Its removal commits the save. Readers and writers recover it under the same lock.
type configRollback struct {
	Filename string `json:"filename"`
	Config   []byte `json:"config"`
	Security []byte `json:"security"`
}

func readConfigFiles(path string) (data, security []byte, err error) {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err != nil {
		return nil, nil, err
	}
	lock, err := fileutil.AcquireFileLock(filepath.Join(dir, ".config-pair.lock"))
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if unlockErr := lock.Unlock(); unlockErr != nil {
			err = errors.Join(err, unlockErr)
		}
	}()
	if err := recoverConfigFiles(dir); err != nil {
		return nil, nil, err
	}
	security, err = readOptionalConfigFile(securityPath(path))
	if err != nil {
		return nil, nil, err
	}
	data, err = os.ReadFile(path)
	return data, security, err
}

func writeConfigFiles(path string, data, security []byte) (err error) {
	dir := filepath.Dir(path)
	lock, err := fileutil.AcquireFileLock(filepath.Join(dir, ".config-pair.lock"))
	if err != nil {
		return err
	}
	defer func() {
		if unlockErr := lock.Unlock(); unlockErr != nil {
			err = errors.Join(err, unlockErr)
		}
	}()
	if err := recoverConfigFiles(dir); err != nil {
		return err
	}
	oldData, err := readOptionalConfigFile(path)
	if err != nil {
		return err
	}
	oldSecurity, err := readOptionalConfigFile(securityPath(path))
	if err != nil {
		return err
	}
	pending := filepath.Join(dir, ".config-rollback.json")
	record, err := json.Marshal(configRollback{filepath.Base(path), oldData, oldSecurity})
	if err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(pending, record, 0o600); err != nil {
		return err
	}
	commit := func() error {
		if err := fileutil.WriteFileAtomic(securityPath(path), security, 0o600); err != nil {
			return err
		}
		if err := fileutil.WriteFileAtomic(path, data, 0o600); err != nil {
			return err
		}
		return os.Remove(pending)
	}
	if err := commit(); err != nil {
		return errors.Join(err, recoverConfigFiles(dir))
	}
	return nil
}

func recoverConfigFiles(dir string) error {
	pending := filepath.Join(dir, ".config-rollback.json")
	data, err := readOptionalConfigFile(pending)
	if err != nil || data == nil {
		return err
	}
	var record configRollback
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("read config rollback: %w", err)
	}
	if record.Filename == "" || record.Filename == "." || record.Filename == ".." || filepath.Base(record.Filename) != record.Filename {
		return fmt.Errorf("invalid config rollback filename")
	}
	path := filepath.Join(dir, record.Filename)
	for target, original := range map[string][]byte{path: record.Config, securityPath(path): record.Security} {
		if original == nil {
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("roll back %s: %w", target, err)
			}
		} else if err := fileutil.WriteFileAtomic(target, original, 0o600); err != nil {
			return fmt.Errorf("roll back %s: %w", target, err)
		}
	}
	return os.Remove(pending)
}

func readOptionalConfigFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}
