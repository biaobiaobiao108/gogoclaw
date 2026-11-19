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
	if err != nil {
		return err
	}
	if string(done) == s.paths.Workspace {
		return nil
	}
	tx, err := db.BeginAppStateUpdate(nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if len(done) != 0 {
		previous := string(done)
		if previous == "1" {
			previous, err = s.previousWorkspace(tx)
			if err != nil {
				return err
			}
		}
		if previous != "" && previous != s.paths.Workspace {
			if err := s.moveWorkspaceState(tx, previous); err != nil {
				return err
			}
		}
		return s.commitWorkspaceImport(tx, marker)
	}

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
			if collection.key == s.stateKey(s.paths.TaskRecords) {
				record.DiscoveryKey = discoveryTaskKey(record)
			}
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
	return s.commitWorkspaceImport(tx, marker)
}

func (s *Store) commitWorkspaceImport(tx *sql.Tx, marker string) error {
	if _, err := tx.Exec(`INSERT INTO app_state(namespace, state_key, value) VALUES ('evolution', ?, ?)
		ON CONFLICT(namespace, state_key) DO UPDATE SET value = excluded.value`, marker, s.paths.Workspace); err != nil {
		return err
	}
	return tx.Commit()
}

// Earlier databases recorded only that import ran. The workspace owning a
// moved database can be inferred when its saved state has one workspace.
func (s *Store) previousWorkspace(tx *sql.Tx) (string, error) {
	rows, err := tx.Query(`SELECT json_extract(payload, '$.workspace_id') AS workspace FROM evolution_records
		UNION SELECT json_extract(draft.value, '$.workspace_id') FROM app_state, json_each(app_state.value) AS draft
			WHERE namespace = 'evolution' AND state_key = ?
		UNION SELECT json_extract(value, '$.workspace_id') FROM app_state
			WHERE namespace = 'evolution' AND state_key GLOB ?`,
		s.stateKey(s.paths.SkillDrafts), s.stateKey(s.paths.ProfilesDir)+"/*")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var previous string
	var ambiguous bool
	for rows.Next() {
		var workspace sql.NullString
		if err := rows.Scan(&workspace); err != nil {
			return "", err
		}
		if workspace.String == s.paths.Workspace {
			return s.paths.Workspace, nil
		}
		if workspace.String != "" {
			ambiguous = ambiguous || (previous != "" && previous != workspace.String)
			previous = workspace.String
		}
	}
	if ambiguous {
		return "", errors.New("cannot identify the previous evolution workspace")
	}
	return previous, rows.Err()
}

func (s *Store) moveWorkspaceState(tx *sql.Tx, previous string) error {
	current := s.paths.Workspace
	if _, err := tx.Exec(`UPDATE evolution_records SET payload = json_set(payload, '$.workspace_id', ?)
		WHERE json_extract(payload, '$.workspace_id') = ?`, current, previous); err != nil {
		return err
	}
	draftsKey := s.stateKey(s.paths.SkillDrafts)
	var drafts []SkillDraft
	if err := readEvolutionState(tx, draftsKey, &drafts); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	changed := false
	for i := range drafts {
		if drafts[i].WorkspaceID == previous {
			if drafts[i].BackupPath == "" && drafts[i].PublishedVersion != "" && drafts[i].BaseVersion != missingSkillVersion {
				drafts[i].BackupPath = filepath.ToSlash(filepath.Join(workspaceScopeDir(previous), skillBackupPath(drafts[i])))
			}
			drafts[i].WorkspaceID = current
			changed = true
		}
	}
	if changed {
		if err := writeEvolutionState(tx, draftsKey, drafts); err != nil {
			return err
		}
	}
	profiles := s.stateKey(s.paths.ProfilesDir) + "/"
	newKey := profiles + workspaceScopeDir(current) + "/"
	_, err := tx.Exec(`UPDATE app_state SET value = json_set(value, '$.workspace_id', ?),
		state_key = CASE WHEN state_key = ? || json_extract(value, '$.skill_name') || '.json'
			THEN state_key ELSE ? || json_extract(value, '$.skill_name') || '.json' END
		WHERE namespace = 'evolution' AND state_key GLOB ? AND json_extract(value, '$.workspace_id') = ?`,
		current, profiles, newKey, profiles+"*", previous)
	return err
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
