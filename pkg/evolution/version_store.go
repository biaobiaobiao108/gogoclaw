package evolution

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
)

// commitSkillVersion updates both SQLite objects in one transaction. A pending
// draft remains recoverable if the process exits after writing the skill file.
func (s *Store) commitSkillVersion(draft SkillDraft, update func(*SkillProfile, bool, []SkillDraft)) error {
	path, err := s.profilePath(draft.WorkspaceID, draft.TargetSkillName)
	if err != nil {
		return err
	}
	unlockDrafts := lockStoreFile(s.paths.SkillDrafts)
	defer unlockDrafts()
	unlockProfile := lockStoreFile(path)
	defer unlockProfile()
	db, err := memory.NewSQLiteStore(filepath.Join(s.paths.Workspace, "sessions"))
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginAppStateUpdate(nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	draftsKey := s.stateKey(s.paths.SkillDrafts)
	var drafts []SkillDraft
	if err := readEvolutionState(tx, draftsKey, &drafts); err != nil {
		return err
	}
	var profile SkillProfile
	err = readEvolutionState(tx, s.stateKey(path), &profile)
	if errors.Is(err, os.ErrNotExist) && usesDefaultWorkspaceState(s.paths, draft.WorkspaceID) {
		legacyPath, pathErr := s.profilePath("", draft.TargetSkillName)
		if pathErr != nil {
			return pathErr
		}
		err = readEvolutionState(tx, s.stateKey(legacyPath), &profile)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	update(&profile, err == nil, drafts)
	if !isZeroSkillProfile(profile) {
		if err := writeEvolutionState(tx, s.stateKey(path), profile); err != nil {
			return err
		}
	}
	for i := range drafts {
		if drafts[i].ID == draft.ID && drafts[i].WorkspaceID == draft.WorkspaceID {
			drafts[i] = draft
			if err := writeEvolutionState(tx, draftsKey, drafts); err != nil {
				return err
			}
			return tx.Commit()
		}
	}
	return ErrDraftNotFound
}

func readEvolutionState(tx *sql.Tx, key string, dst any) error {
	var data []byte
	err := tx.QueryRow(`SELECT value FROM app_state WHERE namespace = 'evolution' AND state_key = ?`, key).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return os.ErrNotExist
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func writeEvolutionState(tx *sql.Tx, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO app_state(namespace, state_key, value) VALUES ('evolution', ?, ?)
		ON CONFLICT(namespace, state_key) DO UPDATE SET value = excluded.value`, key, data)
	return err
}
