package evolution

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
)

// ImportLegacy imports the file-backed state once. Existing SQLite versions win
// and the original files are kept so the import can be inspected or backed up.
func (s *Store) ImportLegacy() error {
	unlock := lockStoreFile(filepath.Join(s.paths.Workspace, "sessions", "evolution-import"))
	defer unlock()
	db, err := memory.NewSQLiteStore(filepath.Join(s.paths.Workspace, "sessions"))
	if err != nil {
		return err
	}
	defer db.Close()

	const marker = "legacy-import-v1"
	done, err := db.GetAppState(nil, "evolution", marker)
	if err != nil || len(done) != 0 {
		return err
	}
	tx, err := db.BeginAppStateUpdate(nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var tasks, patterns []LearningRecord
	for _, path := range []string{
		filepath.Join(s.paths.RootDir, "learning-records.jsonl"),
		s.paths.TaskRecords,
		s.paths.PatternRecords,
	} {
		records, readErr := readLegacyRecords(path)
		if readErr != nil {
			return fmt.Errorf("read legacy evolution records %s: %w", path, readErr)
		}
		for _, record := range records {
			if record.WorkspaceID == "" {
				if !usesDefaultWorkspaceState(s.paths, s.paths.Workspace) {
					continue
				}
				record.WorkspaceID = s.paths.Workspace
			}
			if record.WorkspaceID != s.paths.Workspace {
				continue
			}
			if isPatternRecordKind(record.Kind) || path == s.paths.PatternRecords {
				patterns = append(patterns, record)
			} else {
				tasks = append(tasks, record)
			}
		}
	}
	for _, collection := range []struct {
		key     string
		records []LearningRecord
	}{
		{s.stateKey(s.paths.TaskRecords), mergeLearningRecordsByID(nil, tasks)},
		{s.stateKey(s.paths.PatternRecords), mergeLearningRecordsByID(nil, patterns)},
	} {
		for _, record := range collection.records {
			data, err := json.Marshal(record)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO evolution_records(collection, payload)
				SELECT ?, ? WHERE NOT EXISTS (
					SELECT 1 FROM evolution_records WHERE collection = ?
					AND json_extract(payload, '$.id') = ?
					AND json_extract(payload, '$.workspace_id') = ?
				)`, collection.key, data, collection.key, record.ID, record.WorkspaceID); err != nil {
				return err
			}
		}
	}

	var drafts []SkillDraft
	if err := readLegacyJSON(s.paths.SkillDrafts, &drafts); err != nil {
		return err
	}
	if len(drafts) > 0 {
		key := s.stateKey(s.paths.SkillDrafts)
		var existingData []byte
		err := tx.QueryRow(`SELECT value FROM app_state WHERE namespace = 'evolution' AND state_key = ?`, key).Scan(&existingData)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var existing []SkillDraft
		if len(existingData) != 0 {
			if err := json.Unmarshal(existingData, &existing); err != nil {
				return err
			}
		}
		seen := make(map[string]bool, len(existing))
		for _, draft := range existing {
			seen[draftKey(draft.WorkspaceID, draft.ID)] = true
		}
		for _, draft := range drafts {
			if draft.WorkspaceID == "" && usesDefaultWorkspaceState(s.paths, s.paths.Workspace) {
				draft.WorkspaceID = s.paths.Workspace
			}
			id := draftKey(draft.WorkspaceID, draft.ID)
			if draft.WorkspaceID == s.paths.Workspace && !seen[id] {
				existing = append(existing, draft)
				seen[id] = true
			}
		}
		data, err := json.Marshal(existing)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO app_state(namespace, state_key, value) VALUES ('evolution', ?, ?)
			ON CONFLICT(namespace,state_key) DO UPDATE SET value = excluded.value`, key, data); err != nil {
			return err
		}
	}
	if err := s.importLegacyProfiles(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO app_state(namespace, state_key, value) VALUES ('evolution', ?, '1')`, marker); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) importLegacyProfiles(tx *sql.Tx) error {
	for _, dir := range []string{
		filepath.Join(s.paths.ProfilesDir, workspaceScopeDir(s.paths.Workspace)),
		s.paths.ProfilesDir,
	} {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			var profile SkillProfile
			if err := readLegacyJSON(filepath.Join(dir, entry.Name()), &profile); err != nil {
				return err
			}
			if profile.WorkspaceID == "" && usesDefaultWorkspaceState(s.paths, s.paths.Workspace) {
				profile.WorkspaceID = s.paths.Workspace
			}
			if profile.WorkspaceID != s.paths.Workspace {
				continue
			}
			path, err := s.profilePath(profile.WorkspaceID, profile.SkillName)
			if err != nil {
				return err
			}
			data, err := json.Marshal(profile)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO app_state(namespace, state_key, value) VALUES ('evolution', ?, ?)`,
				s.stateKey(path), data); err != nil {
				return err
			}
		}
	}
	return nil
}

func readLegacyJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("decode legacy evolution state %s: %w", path, err)
	}
	return nil
}

func readLegacyRecords(path string) ([]LearningRecord, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var records []LearningRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var record LearningRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, scanner.Err()
}
