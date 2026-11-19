package evolution

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/skills"
)

type Store struct {
	paths Paths
}

func NewStore(paths Paths) *Store {
	return &Store{paths: paths}
}

var storeFileLocks sync.Map

func (s *Store) AppendLearningRecord(ctx context.Context, record LearningRecord) error {
	switch record.Kind {
	case RecordKindPattern, legacyRecordKindRule:
		return s.AppendPatternRecords([]LearningRecord{record})
	default:
		return s.AppendTaskRecord(ctx, record)
	}
}

func (s *Store) AppendLearningRecords(records []LearningRecord) error {
	taskRecords := make([]LearningRecord, 0, len(records))
	patternRecords := make([]LearningRecord, 0, len(records))
	for _, record := range records {
		switch record.Kind {
		case RecordKindPattern, legacyRecordKindRule:
			patternRecords = append(patternRecords, record)
		default:
			taskRecords = append(taskRecords, record)
		}
	}
	if err := s.AppendTaskRecords(context.Background(), taskRecords); err != nil {
		return err
	}
	return s.AppendPatternRecords(patternRecords)
}

func (s *Store) AppendTaskRecord(ctx context.Context, record LearningRecord) error {
	return s.AppendTaskRecords(ctx, []LearningRecord{record})
}

func (s *Store) AppendTaskRecords(ctx context.Context, records []LearningRecord) error {
	return s.appendRecords(ctx, s.paths.TaskRecords, records)
}

func (s *Store) AppendPatternRecords(records []LearningRecord) error {
	return s.appendRecords(context.Background(), s.paths.PatternRecords, records)
}

func (s *Store) stateKey(path string) string {
	key, err := filepath.Rel(s.paths.RootDir, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(key)
}

func (s *Store) readState(path string) ([]byte, error) {
	db, err := memory.NewSQLiteStore(filepath.Join(s.paths.Workspace, "sessions"))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.GetAppState(nil, "evolution", s.stateKey(path))
}

func (s *Store) writeState(path string, data []byte) error {
	db, err := memory.NewSQLiteStore(filepath.Join(s.paths.Workspace, "sessions"))
	if err != nil {
		return err
	}
	defer db.Close()
	return db.SetAppState(nil, "evolution", s.stateKey(path), data)
}

func (s *Store) appendRecords(ctx context.Context, path string, records []LearningRecord) error {
	if len(records) == 0 {
		return nil
	}
	unlock := lockStoreFile(path)
	defer unlock()
	return s.writeRecords(ctx, path, records, false)
}

func (s *Store) writeRecords(ctx context.Context, path string, records []LearningRecord, replace bool) error {
	payloads := make([][]byte, 0, len(records))
	for _, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			return err
		}
		payloads = append(payloads, data)
	}
	db, err := memory.NewSQLiteStore(filepath.Join(s.paths.Workspace, "sessions"))
	if err != nil {
		return err
	}
	defer db.Close()
	return db.WriteEvolutionRecords(ctx, s.stateKey(path), payloads, replace)
}

func (s *Store) LoadLearningRecords() ([]LearningRecord, error) {
	taskRecords, err := s.LoadTaskRecords()
	if err != nil {
		return nil, err
	}
	patternRecords, err := s.LoadPatternRecords()
	if err != nil {
		return nil, err
	}
	return append(taskRecords, patternRecords...), nil
}

func (s *Store) LoadTaskRecords() ([]LearningRecord, error) {
	records, err := s.loadRecordsFromPath(s.paths.TaskRecords)
	if err != nil {
		return nil, err
	}
	return mergeLearningRecordsByID(nil, records), nil
}

func (s *Store) loadPendingTasks(ctx context.Context, limit int) ([]LearningRecord, error) {
	db, err := memory.NewSQLiteStore(filepath.Join(s.paths.Workspace, "sessions"))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	payloads, err := db.ListPendingEvolutionTasks(ctx, s.stateKey(s.paths.TaskRecords), s.paths.Workspace, limit)
	if err != nil {
		return nil, err
	}
	return decodeLearningRecords(payloads)
}

func (s *Store) loadTaskEvidence(ctx context.Context, ids []string) ([]LearningRecord, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	db, err := memory.NewSQLiteStore(filepath.Join(s.paths.Workspace, "sessions"))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	payloads, err := db.ListEvolutionTasksByIDs(ctx, s.stateKey(s.paths.TaskRecords), s.paths.Workspace, data)
	if err != nil {
		return nil, err
	}
	return decodeLearningRecords(payloads)
}

func (s *Store) SaveTaskDecisions(records []LearningRecord) error {
	if len(records) == 0 {
		return nil
	}
	type decision struct {
		ID           string `json:"id"`
		Workspace    string `json:"workspace"`
		ConsideredAt any    `json:"considered_at"`
		JudgedBy     string `json:"judged_by"`
		Success      *bool  `json:"success"`
	}
	updates := make([]decision, 0, len(records))
	for _, record := range records {
		updates = append(updates, decision{
			record.ID, record.WorkspaceID, record.ConsideredAt, record.JudgedBy, record.JudgedSuccess,
		})
	}
	data, err := json.Marshal(updates)
	if err != nil {
		return err
	}
	unlock := lockStoreFile(s.paths.TaskRecords)
	defer unlock()
	db, err := memory.NewSQLiteStore(filepath.Join(s.paths.Workspace, "sessions"))
	if err != nil {
		return err
	}
	defer db.Close()
	return db.UpdateEvolutionDecisions(s.stateKey(s.paths.TaskRecords), data)
}

func (s *Store) LoadPatternRecords() ([]LearningRecord, error) {
	records, err := s.loadRecordsFromPath(s.paths.PatternRecords)
	if err != nil {
		return nil, err
	}
	return mergeLearningRecordsByID(nil, records), nil
}

func (s *Store) loadRecordsFromPath(path string) ([]LearningRecord, error) {
	db, err := memory.NewSQLiteStore(filepath.Join(s.paths.Workspace, "sessions"))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	payloads, err := db.ListEvolutionRecords(nil, s.stateKey(path))
	if err != nil {
		return nil, err
	}
	return decodeLearningRecords(payloads)
}

func decodeLearningRecords(payloads [][]byte) ([]LearningRecord, error) {
	records := make([]LearningRecord, 0, len(payloads))
	for _, payload := range payloads {
		var record LearningRecord
		if err := json.Unmarshal(payload, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (s *Store) SaveTaskRecords(records []LearningRecord) error {
	return s.saveRecords(s.paths.TaskRecords, records)
}

func (s *Store) MarkTaskRecordsClustered(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	target := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		target[id] = struct{}{}
	}
	if len(target) == 0 {
		return nil
	}

	unlock := lockStoreFile(s.paths.TaskRecords)
	defer unlock()

	keys := make([]string, 0, len(target))
	for id := range target {
		keys = append(keys, id)
	}
	data, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	db, err := memory.NewSQLiteStore(filepath.Join(s.paths.Workspace, "sessions"))
	if err != nil {
		return err
	}
	defer db.Close()
	return db.MarkEvolutionTasksClustered(s.stateKey(s.paths.TaskRecords), s.paths.Workspace, data)
}

func (s *Store) SavePatternRecords(records []LearningRecord) error {
	return s.saveRecords(s.paths.PatternRecords, records)
}

func (s *Store) MergePatternRecords(records []LearningRecord) error {
	if len(records) == 0 {
		return nil
	}

	unlock := lockStoreFile(s.paths.PatternRecords)
	defer unlock()

	current, err := s.loadRecordsFromPath(s.paths.PatternRecords)
	if err != nil {
		return err
	}
	merged := mergeLearningRecordsByID(mergeLearningRecordsByID(nil, current), records)
	return s.saveRecordsLocked(s.paths.PatternRecords, merged)
}

func (s *Store) saveRecords(path string, records []LearningRecord) error {
	unlock := lockStoreFile(path)
	defer unlock()

	return s.saveRecordsLocked(path, records)
}

func (s *Store) saveRecordsLocked(path string, records []LearningRecord) error {
	return s.writeRecords(context.Background(), path, records, true)
}

func mergeLearningRecordsByID(base, updates []LearningRecord) []LearningRecord {
	out := append([]LearningRecord(nil), base...)
	indexByID := make(map[string]int, len(out)+len(updates))
	for i, record := range out {
		key := learningRecordMergeKey(record)
		if key == "" {
			continue
		}
		indexByID[key] = i
	}
	for _, record := range updates {
		key := learningRecordMergeKey(record)
		if key == "" {
			out = append(out, record)
			continue
		}
		if idx, ok := indexByID[key]; ok {
			out[idx] = record
			continue
		}
		indexByID[key] = len(out)
		out = append(out, record)
	}
	return out
}

func learningRecordMergeKey(record LearningRecord) string {
	id := strings.TrimSpace(record.ID)
	if id == "" {
		return ""
	}
	return strings.TrimSpace(record.WorkspaceID) + "\x00" + id
}

func (s *Store) SaveDrafts(drafts []SkillDraft) error {
	unlock := lockStoreFile(s.paths.SkillDrafts)
	defer unlock()

	existing, err := s.LoadDrafts()
	if err != nil {
		return err
	}

	indexByKey := make(map[string]int, len(existing))
	for i, draft := range existing {
		indexByKey[draftKey(draft.WorkspaceID, draft.ID)] = i
	}

	for _, draft := range drafts {
		key := draftKey(draft.WorkspaceID, draft.ID)
		if idx, ok := indexByKey[key]; ok {
			existing[idx] = draft
			continue
		}
		indexByKey[key] = len(existing)
		existing = append(existing, draft)
	}

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return s.writeState(s.paths.SkillDrafts, data)
}

func (s *Store) LoadDrafts() ([]SkillDraft, error) {
	data, err := s.readState(s.paths.SkillDrafts)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}

	var drafts []SkillDraft
	if err := json.Unmarshal(data, &drafts); err != nil {
		return nil, err
	}
	return drafts, nil
}

func (s *Store) SaveProfile(profile SkillProfile) error {
	path, err := s.profilePath(profile.WorkspaceID, profile.SkillName)
	if err != nil {
		return err
	}
	unlock := lockStoreFile(path)
	defer unlock()

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	return s.writeState(path, data)
}

func (s *Store) LoadProfile(skillName string) (SkillProfile, error) {
	return s.loadProfileForWorkspace(strings.TrimSpace(s.paths.Workspace), skillName)
}

func (s *Store) UpdateProfile(
	workspaceID, skillName string,
	update func(profile *SkillProfile, exists bool) error,
) error {
	targetPath, err := s.profilePath(workspaceID, skillName)
	if err != nil {
		return err
	}

	unlock := lockStoreFile(targetPath)
	defer unlock()

	profile, err := s.loadProfileForWorkspace(workspaceID, skillName)
	exists := err == nil
	if errors.Is(err, os.ErrNotExist) {
		profile = SkillProfile{}
	} else if err != nil {
		return err
	}

	if updateErr := update(&profile, exists); updateErr != nil {
		return updateErr
	}
	if !exists && isZeroSkillProfile(profile) {
		return nil
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	return s.writeState(targetPath, data)
}

func (s *Store) loadProfileForWorkspace(workspaceID, skillName string) (SkillProfile, error) {
	paths, err := s.profileLookupPaths(workspaceID, skillName)
	if err != nil {
		return SkillProfile{}, err
	}
	for _, path := range paths {
		profile, loadErr := s.loadProfileFromPath(path)
		if errors.Is(loadErr, os.ErrNotExist) {
			continue
		}
		if loadErr != nil {
			return SkillProfile{}, loadErr
		}
		return profile, nil
	}
	return SkillProfile{}, os.ErrNotExist
}

func isZeroSkillProfile(profile SkillProfile) bool {
	return profile.SkillName == "" &&
		profile.WorkspaceID == "" &&
		profile.CurrentVersion == "" &&
		profile.Status == "" &&
		profile.Origin == "" &&
		profile.HumanSummary == "" &&
		profile.ChangeReason == "" &&
		len(profile.IntendedUseCases) == 0 &&
		len(profile.PreferredEntryPath) == 0 &&
		len(profile.AvoidPatterns) == 0 &&
		profile.LastUsedAt.IsZero() &&
		profile.UseCount == 0 &&
		profile.RetentionScore == 0 &&
		len(profile.VersionHistory) == 0
}

func (s *Store) LoadProfiles() ([]SkillProfile, error) {
	db, err := memory.NewSQLiteStore(filepath.Join(s.paths.Workspace, "sessions"))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	values, err := db.ListAppState(nil, "evolution")
	if err != nil {
		return nil, err
	}
	prefix := s.stateKey(s.paths.ProfilesDir) + "/"
	var profiles []SkillProfile
	for key, data := range values {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		var profile SkillProfile
		if err := json.Unmarshal(data, &profile); err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].SkillName != profiles[j].SkillName {
			return profiles[i].SkillName < profiles[j].SkillName
		}
		return profiles[i].WorkspaceID < profiles[j].WorkspaceID
	})
	return profiles, nil
}

func draftKey(workspaceID, id string) string {
	return workspaceID + "\x00" + id
}

func lockStoreFile(path string) func() {
	for {
		actual, _ := storeFileLocks.LoadOrStore(path, &sync.Mutex{})
		mu, ok := actual.(*sync.Mutex)
		if !ok || mu == nil {
			// Corrupted entry (wrong type or nil *sync.Mutex).
			// Atomically swap in a fresh mutex via CompareAndSwap.
			// If CAS fails, another goroutine already replaced it —
			// just retry the loop to pick up the valid entry.
			storeFileLocks.CompareAndSwap(path, actual, &sync.Mutex{})
			continue
		}
		mu.Lock()
		return mu.Unlock
	}
}

func (s *Store) profilePath(workspaceID, skillName string) (string, error) {
	if err := skills.ValidateSkillName(skillName); err != nil {
		return "", err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return filepath.Join(s.paths.ProfilesDir, skillName+".json"), nil
	}
	return filepath.Join(s.paths.ProfilesDir, workspaceScopeDir(workspaceID), skillName+".json"), nil
}

func (s *Store) loadProfileFromPath(path string) (SkillProfile, error) {
	data, err := s.readState(path)
	if err != nil {
		return SkillProfile{}, err
	}
	if len(data) == 0 {
		return SkillProfile{}, os.ErrNotExist
	}
	var profile SkillProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return SkillProfile{}, err
	}
	return profile, nil
}

func (s *Store) profileLookupPaths(workspaceID, skillName string) ([]string, error) {
	if err := skills.ValidateSkillName(skillName); err != nil {
		return nil, err
	}

	paths := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	appendPath := func(path string) {
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		paths = append(paths, path)
		seen[path] = struct{}{}
	}

	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID != "" {
		path, err := s.profilePath(workspaceID, skillName)
		if err != nil {
			return nil, err
		}
		appendPath(path)
		if !usesDefaultWorkspaceState(s.paths, workspaceID) {
			return paths, nil
		}
	}

	legacyPath, err := s.profilePath("", skillName)
	if err != nil {
		return nil, err
	}
	appendPath(legacyPath)
	return paths, nil
}

func workspaceScopeDir(workspaceID string) string {
	sum := sha1.Sum([]byte(workspaceID))
	base := filepath.Base(filepath.Clean(workspaceID))
	base = sanitizeWorkspaceComponent(base)
	if base == "" || base == "." {
		base = "workspace"
	}
	return base + "-" + hex.EncodeToString(sum[:6])
}

func sanitizeWorkspaceComponent(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
