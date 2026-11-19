package evolution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/skills"
)

var (
	ErrEvolutionBusy = errors.New("evolution is running")
	ErrDraftNotFound = errors.New("draft not found")
	ErrDraftConflict = errors.New("draft or target skill changed; review the current version")
	ErrDraftAction   = errors.New("action is unavailable in the current mode or draft state")
)

const missingSkillVersion = "missing"

var workspaceOperations sync.Map

func (rt *Runtime) lockWorkspace(workspace string) (func(), error) {
	key, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(key); err == nil {
		key = resolved
	}
	entry, _ := workspaceOperations.LoadOrStore(key, &sync.Mutex{})
	lock := entry.(*sync.Mutex)
	if !lock.TryLock() {
		return nil, ErrEvolutionBusy
	}
	return lock.Unlock, nil
}

func (rt *Runtime) importWorkspace(store *Store) error {
	if _, ok := rt.imported.Load(store.paths.Workspace); ok {
		return nil
	}
	if err := store.ImportLegacy(); err != nil {
		return err
	}
	rt.imported.Store(store.paths.Workspace, true)
	return nil
}

func contentVersion(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func skillVersion(body string, exists bool) string {
	if !exists {
		return missingSkillVersion
	}
	return contentVersion(body)
}

func currentSkillVersion(workspace, name string) (string, error) {
	body, exists, err := loadCurrentSkillBody(workspace, name)
	return skillVersion(body, exists), err
}

func evidenceVersion(rule LearningRecord) string {
	ids := slices.Clone(patternEvidenceIDs(rule))
	slices.Sort(ids)
	return contentVersion(strings.Join(ids, "\x00"))
}

func patternEvidenceIDs(rule LearningRecord) []string {
	if len(rule.EvidenceRecordIDs) > 0 {
		return rule.EvidenceRecordIDs
	}
	return rule.TaskRecordIDs
}

func hasCurrentDraftEvidence(draft SkillDraft, rule LearningRecord) bool {
	if rule.ID != draft.SourceRecordID || rule.WorkspaceID != draft.WorkspaceID || rule.Status != "ready" {
		return false
	}
	if draft.EvidenceVersion == "" {
		return rule.UpdatedAt == nil || !rule.UpdatedAt.After(draft.CreatedAt)
	}
	return draft.EvidenceVersion == evidenceVersion(rule)
}

func hasDraftForEvidence(drafts []SkillDraft, workspace, ruleID, version string, updated *time.Time) bool {
	for _, draft := range drafts {
		if draft.WorkspaceID != workspace || draft.SourceRecordID != ruleID {
			continue
		}
		if draft.EvidenceVersion == version ||
			(draft.EvidenceVersion == "" && draft.Status != DraftStatusQuarantined &&
				(updated == nil || !updated.After(draft.CreatedAt))) {
			return true
		}
	}
	return false
}

// A single corroborating success is rarely worth rewriting a published guide.
// Pending candidates still get a current revision so they remain reviewable.
func shouldWaitForMoreDraftEvidence(drafts []SkillDraft, rule LearningRecord) bool {
	var latest SkillDraft
	for _, draft := range drafts {
		if draft.WorkspaceID == rule.WorkspaceID && draft.SourceRecordID == rule.ID &&
			(latest.ID == "" || draft.CreatedAt.After(latest.CreatedAt)) {
			latest = draft
		}
	}
	return latest.Status == DraftStatusAccepted && latest.PublishedVersion != "" &&
		latest.EvidenceCount > 0 && len(patternEvidenceIDs(rule)) == latest.EvidenceCount+1 &&
		len(rule.TaskRecordIDs) == latest.EvidenceSuccesses+1 && latest.EvidenceRoute == draftEvidenceRoute(rule)
}

func draftEvidenceRoute(rule LearningRecord) string {
	matches := slices.Clone(rule.MatchedSkillNames)
	slices.Sort(matches)
	return contentVersion(strings.Join(rule.WinningPath, "\x00") + "\x01" + strings.Join(matches, "\x00"))
}

type DraftReview struct {
	Draft       SkillDraft       `json:"draft"`
	Diff        string           `json:"diff"`
	ReviewToken string           `json:"review_token,omitempty"`
	Issue       string           `json:"issue,omitempty"`
	CanRefresh  bool             `json:"can_refresh,omitempty"`
	CanRollback bool             `json:"can_rollback,omitempty"`
	Shadowed    string           `json:"shadowed_source,omitempty"`
	Evidence    []EvidenceSample `json:"evidence"`
}

type EvidenceSample struct {
	Summary string `json:"summary"`
	Success *bool  `json:"success"`
}

func (rt *Runtime) Drafts(workspace string) ([]SkillDraft, error) {
	store := rt.storeForWorkspace(workspace)
	if err := rt.importWorkspace(store); err != nil {
		return nil, err
	}
	drafts, err := store.LoadDrafts()
	if err != nil {
		return nil, err
	}
	drafts = slices.DeleteFunc(drafts, func(draft SkillDraft) bool { return draft.WorkspaceID != workspace })
	if drafts == nil {
		drafts = []SkillDraft{}
	}
	sortDrafts(drafts)
	return drafts, nil
}

func sortDrafts(drafts []SkillDraft) {
	slices.SortStableFunc(drafts, func(a, b SkillDraft) int {
		if (a.Status == DraftStatusCandidate) != (b.Status == DraftStatusCandidate) {
			if a.Status == DraftStatusCandidate {
				return -1
			}
			return 1
		}
		return b.CreatedAt.Compare(a.CreatedAt)
	})
}

func (rt *Runtime) ReviewDraft(workspace, id string) (DraftReview, error) {
	draft, store, err := rt.findDraft(workspace, id)
	if err != nil {
		return DraftReview{}, err
	}
	review := DraftReview{Draft: draft, Diff: draft.BodyOrPatch, Evidence: []EvidenceSample{}}
	patterns, err := store.LoadPatternRecords()
	if err != nil {
		return review, err
	}
	sourceReady := false
	for _, rule := range patterns {
		if rule.WorkspaceID != workspace || rule.ID != draft.SourceRecordID {
			continue
		}
		sourceReady = hasCurrentDraftEvidence(draft, rule)
		ids := draft.EvidenceIDs
		if len(ids) == 0 {
			ids = patternEvidenceIDs(rule)
		}
		records, err := store.loadTaskEvidence(context.Background(), recentEvidenceIDs(ids))
		if err != nil {
			return review, err
		}
		for _, item := range records {
			if item.WorkspaceID == workspace && slices.Contains(ids, item.ID) {
				succeeded := item.JudgedSuccess
				if succeeded == nil {
					succeeded = item.Success
				}
				review.Evidence = append(review.Evidence, EvidenceSample{item.Summary, succeeded})
			}
		}
		break
	}
	if draft.Status == DraftStatusAccepted && draft.PublishedVersion != "" {
		review.CanRollback, err = isCurrentPublishedDraft(store, draft)
		return review, err
	}
	if draft.Status != DraftStatusCandidate {
		return review, nil
	}
	if !sourceReady && draft.PublishedVersion == "" {
		review.Issue = "source evidence changed or is below the learning threshold"
		return review, nil
	}
	current, exists, err := loadCurrentSkillBody(workspace, draft.TargetSkillName)
	if err != nil {
		return review, err
	}
	base := skillVersion(current, exists)
	if draft.PublishedVersion != "" && base == draft.PublishedVersion {
		review.ReviewToken = reviewToken(draft, draft.BaseVersion)
		return review, nil
	}
	if draft.BaseVersion != "" && draft.BaseVersion != base {
		review.Issue = ErrDraftConflict.Error()
		if draft.PublishedVersion == "" {
			_, renderErr := checkedRenderedDraft(draft, current, exists)
			review.CanRefresh = renderErr == nil
		}
		return review, nil
	}
	rendered, err := checkedRenderedDraft(draft, current, exists)
	if err != nil {
		review.Issue = err.Error()
		return review, nil
	}
	review.Diff = buildLineDiffPreview(current, rendered)
	review.ReviewToken = reviewToken(draft, base)
	if !exists {
		for _, skill := range NewDefaultDraftGenerator(workspace).loader.ListSkills() {
			if skill.Name == draft.TargetSkillName && skill.Source != "workspace" {
				review.Shadowed = skill.Source
				break
			}
		}
	}
	return review, nil
}

func (rt *Runtime) findDraft(workspace, id string) (SkillDraft, *Store, error) {
	drafts, err := rt.Drafts(workspace)
	if err != nil {
		return SkillDraft{}, nil, err
	}
	for _, draft := range drafts {
		if draft.ID == id {
			return draft, rt.storeForWorkspace(workspace), nil
		}
	}
	return SkillDraft{}, nil, ErrDraftNotFound
}

func reviewToken(draft SkillDraft, base string) string {
	return contentVersion(strings.Join([]string{draft.ID, draft.TargetSkillName, string(draft.ChangeKind), base, draft.BodyOrPatch}, "\x00"))
}

func checkedRenderedDraft(draft SkillDraft, current string, exists bool) (string, error) {
	if findings := ReviewDraft(draft).Findings; len(findings) > 0 {
		return "", errors.New(strings.Join(findings, "; "))
	}
	rendered, err := renderAppliedBody(draft, current, exists)
	if err != nil {
		return "", err
	}
	if err := validateAppliedSkillBody(rendered, draft.TargetSkillName, allowsExistingFrontmatterFields(draft.ChangeKind, exists)); err != nil {
		return "", err
	}
	if rendered == current {
		return "", errors.New("skill already has this content")
	}
	return rendered, nil
}

func (rt *Runtime) ActOnDraft(ctx context.Context, workspace, id, action, token string) error {
	unlock, err := rt.lockWorkspace(workspace)
	if err != nil {
		return err
	}
	defer unlock()
	draft, store, err := rt.findDraft(workspace, id)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	mode := rt.cfg.EffectiveMode()
	switch action {
	case "refresh":
		if (mode != "apply" && mode != "draft") || draft.Status != DraftStatusCandidate || draft.PublishedVersion != "" {
			return ErrDraftAction
		}
		current, exists, err := loadCurrentSkillBody(workspace, draft.TargetSkillName)
		if err != nil {
			return err
		}
		if _, err := checkedRenderedDraft(draft, current, exists); err != nil {
			return err
		}
		draft.BaseVersion = skillVersion(current, exists)
		return store.SaveDrafts([]SkillDraft{draft})
	case "reject":
		if (mode != "apply" && mode != "draft") || draft.Status != DraftStatusCandidate || draft.PublishedVersion != "" {
			return ErrDraftAction
		}
		draft.Status = DraftStatusRejected
		return store.SaveDrafts([]SkillDraft{draft})
	case "publish":
		if !rt.cfg.AllowsDraftPublication() || (draft.Status != DraftStatusCandidate && draft.Status != DraftStatusAccepted) {
			return ErrDraftAction
		}
		if draft.PublishedVersion == "" {
			patterns, err := store.LoadPatternRecords()
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(patterns, func(record LearningRecord) bool {
				return hasCurrentDraftEvidence(draft, record)
			}) {
				return ErrDraftAction
			}
		}
		return rt.publishReviewedDraft(ctx, workspace, store, draft, token)
	case "rollback":
		if draft.Status == DraftStatusRolledBack {
			return nil
		}
		if draft.Status != DraftStatusAccepted || draft.PublishedVersion == "" {
			return ErrDraftAction
		}
		return rt.rollbackPublishedDraft(workspace, store, draft)
	default:
		return ErrDraftAction
	}
}

func (rt *Runtime) publishReviewedDraft(ctx context.Context, workspace string, store *Store, draft SkillDraft, token string) error {
	current, exists, err := loadCurrentSkillBody(workspace, draft.TargetSkillName)
	if err != nil {
		return err
	}
	base := skillVersion(current, exists)
	if draft.PublishedVersion != "" && draft.PublishedVersion == base {
		if token != reviewToken(draft, draft.BaseVersion) {
			return ErrDraftConflict
		}
		return rt.finishPublication(workspace, store, draft)
	}
	if draft.Status == DraftStatusAccepted || (draft.BaseVersion != "" && draft.BaseVersion != base) ||
		token != reviewToken(draft, base) {
		return ErrDraftConflict
	}
	rendered, err := checkedRenderedDraft(draft, current, exists)
	if err != nil {
		return err
	}
	draft.BaseVersion = base
	draft.PublishedVersion = contentVersion(rendered)
	if exists {
		draft.BackupPath = filepath.Join(store.paths.BackupsDir, workspaceScopeDir(workspace),
			draft.TargetSkillName, contentVersion(draft.ID), "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(draft.BackupPath), 0o700); err != nil {
			return err
		}
		if err := fileutil.WriteFileAtomic(draft.BackupPath, []byte(current), 0o600); err != nil {
			return err
		}
	}
	// Store the intended version before the file write so a retry can finish a
	// publication interrupted between changing the file and updating SQLite.
	if err := store.SaveDrafts([]SkillDraft{draft}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeSkillVersion(workspace, draft.TargetSkillName, base, rendered, true); err != nil {
		return err
	}
	if err := rt.finishPublication(workspace, store, draft); err != nil {
		if rollbackErr := writeSkillVersion(workspace, draft.TargetSkillName, draft.PublishedVersion, current, exists); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		draft.PublishedVersion = ""
		draft.BackupPath = ""
		return errors.Join(err, store.SaveDrafts([]SkillDraft{draft}))
	}
	return nil
}

func (rt *Runtime) finishPublication(workspace string, store *Store, draft SkillDraft) error {
	if draft.Status == DraftStatusAccepted {
		return nil
	}
	draft.Status = DraftStatusAccepted
	return store.commitSkillVersion(draft, func(profile *SkillProfile, exists bool, _ []SkillDraft) {
		applyDraftProfile(workspace, draft, rt.now(), profile, exists)
	})
}

func (rt *Runtime) rollbackPublishedDraft(workspace string, store *Store, draft SkillDraft) error {
	latest, err := isCurrentPublishedDraft(store, draft)
	if err != nil {
		return err
	}
	if !latest {
		return ErrDraftConflict
	}
	var restored string
	hadOriginal := draft.BaseVersion != missingSkillVersion
	if hadOriginal {
		expected := filepath.Join(store.paths.BackupsDir, workspaceScopeDir(workspace), draft.TargetSkillName,
			contentVersion(draft.ID), "SKILL.md")
		// A draft can predate a change to the configured backup directory.
		path := expected
		if draft.BackupPath != "" {
			path = draft.BackupPath
		}
		backup, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		restored = string(backup)
		if contentVersion(restored) != draft.BaseVersion {
			return ErrDraftConflict
		}
	}
	current, err := currentSkillVersion(workspace, draft.TargetSkillName)
	if err != nil {
		return err
	}
	if current != draft.BaseVersion {
		if err := writeSkillVersion(workspace, draft.TargetSkillName, draft.PublishedVersion, restored, hadOriginal); err != nil {
			return err
		}
	}
	draft.Status = DraftStatusRolledBack
	return store.commitSkillVersion(draft, func(profile *SkillProfile, exists bool, versions []SkillDraft) {
		if !exists || profile.CurrentVersion != draft.ID {
			return
		}
		profile.CurrentVersion = ""
		profile.Status = SkillStatusActive
		if !hadOriginal {
			profile.Status = SkillStatusDeleted
		} else if profile.Origin != "manual" {
			profile.Origin = "manual"
			for _, previous := range versions {
				if previous.WorkspaceID == workspace && previous.TargetSkillName == draft.TargetSkillName &&
					previous.Status == DraftStatusAccepted && previous.PublishedVersion == draft.BaseVersion {
					profile.CurrentVersion = previous.ID
					profile.Origin = "evolved"
					break
				}
			}
		}
		profile.VersionHistory = append(profile.VersionHistory, SkillVersionEntry{
			Version: draft.ID, Action: "rollback", Timestamp: rt.now(), DraftID: draft.ID, Rollback: true,
		})
	})
}

func isCurrentPublishedDraft(store *Store, draft SkillDraft) (bool, error) {
	profile, err := store.LoadProfile(draft.TargetSkillName)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return profile.CurrentVersion == draft.ID, err
}

func writeSkillVersion(workspace, name, expected, body string, exists bool) error {
	if err := skills.ValidateSkillName(name); err != nil {
		return err
	}
	target, err := skillDocumentPath(workspace, name)
	if err != nil {
		return err
	}
	check := func() error {
		version, err := currentSkillVersion(workspace, name)
		if err != nil {
			return err
		}
		if version != expected {
			return ErrDraftConflict
		}
		return nil
	}
	if !exists {
		return fileutil.RemoveFileChecked(target, check)
	}
	return fileutil.WriteFileAtomicChecked(target, []byte(body), 0o644, check)
}

func skillDocumentPath(workspace, name string) (string, error) {
	if err := skills.ValidateSkillName(name); err != nil {
		return "", err
	}
	skillsPath := filepath.Join(workspace, "skills")
	targetDir := filepath.Join(skillsPath, name)
	target := filepath.Join(targetDir, "SKILL.md")
	for _, path := range []string{skillsPath, targetDir, target} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if (path == target && !info.Mode().IsRegular()) || (path != target && !info.IsDir()) {
			return "", fmt.Errorf("unexpected file type for skill %q", name)
		}
	}
	return target, nil
}
