package evolution_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/evolution"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func TestReviewedEvolutionPublicationAndRollback(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	ctx := context.Background()
	const id = "draft-one"
	const base = "---\nname: weather\ndescription: weather guide\n---\n# Weather\nOriginal instructions.\n"
	skillPath := filepath.Join(root, "skills", "weather", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePatternRecords([]evolution.LearningRecord{{
		ID: "pattern", WorkspaceID: root, Kind: evolution.RecordKindPattern, Status: "ready",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDrafts([]evolution.SkillDraft{{
		ID: id, WorkspaceID: root, SourceRecordID: "pattern", TargetSkillName: "weather",
		DraftType: evolution.DraftTypeWorkflow, ChangeKind: evolution.ChangeKindAppend,
		HumanSummary: "Add current weather", BodyOrPatch: "Collect the latest observation.",
		Status: evolution.DraftStatusCandidate,
	}}); err != nil {
		t.Fatal(err)
	}

	draftRuntime, _ := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{Enabled: true, Mode: "draft"},
	})
	applyRuntime, _ := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{Enabled: true, Mode: "apply"},
	})
	review, err := applyRuntime.ReviewDraft(root, id)
	if err != nil || review.ReviewToken == "" || !strings.Contains(review.Diff, "+Collect the latest observation.") {
		t.Fatalf("preview = %+v, err = %v", review, err)
	}
	if err := draftRuntime.ActOnDraft(ctx, root, id, "publish", review.ReviewToken); !errors.Is(err, evolution.ErrDraftAction) {
		t.Fatalf("draft mode publish error = %v", err)
	}
	const edited = base + "Added outside evolution.\n"
	if err := os.WriteFile(skillPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyRuntime.ActOnDraft(ctx, root, id, "publish", review.ReviewToken); !errors.Is(err, evolution.ErrDraftConflict) {
		t.Fatalf("stale preview approved: %v", err)
	}
	review, err = applyRuntime.ReviewDraft(root, id)
	if err != nil || review.ReviewToken == "" {
		t.Fatalf("fresh preview = %+v, err = %v", review, err)
	}

	// A SQLite finalization error must restore the live file and allow retry.
	db, err := sql.Open("sqlite", filepath.Join(root, "sessions", "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER fail_evolution_profile BEFORE INSERT ON app_state
		WHEN NEW.namespace = 'evolution' AND NEW.state_key LIKE 'profiles/%'
		BEGIN SELECT RAISE(ABORT, 'profile fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyRuntime.ActOnDraft(ctx, root, id, "publish", review.ReviewToken); err == nil {
		t.Fatal("publication succeeded with a failed profile transaction")
	}
	assertStoredSkill(t, skillPath, edited)
	if _, err := db.Exec(`DROP TRIGGER fail_evolution_profile`); err != nil {
		t.Fatal(err)
	}
	if err := applyRuntime.ActOnDraft(ctx, root, id, "publish", review.ReviewToken); err != nil {
		t.Fatal(err)
	}
	published, err := os.ReadFile(skillPath)
	if err != nil || !strings.Contains(string(published), "Collect the latest observation.") {
		t.Fatalf("published = %q, err = %v", published, err)
	}
	if err := os.WriteFile(skillPath, append(published, []byte("\nLater user edit")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyRuntime.ActOnDraft(ctx, root, id, "rollback", ""); !errors.Is(err, evolution.ErrDraftConflict) {
		t.Fatalf("rollback overwrote a later edit: %v", err)
	}
	if err := os.WriteFile(skillPath, published, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := draftRuntime.ActOnDraft(ctx, root, id, "rollback", ""); err != nil {
		t.Fatal(err)
	}
	assertStoredSkill(t, skillPath, edited)
	drafts, err := store.LoadDrafts()
	if err != nil || len(drafts) != 1 || drafts[0].Status != evolution.DraftStatusRolledBack {
		t.Fatalf("drafts = %+v, err = %v", drafts, err)
	}
}

func assertStoredSkill(t *testing.T, path, expected string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil || string(body) != expected {
		t.Fatalf("skill = %q, expected %q, error = %v", body, expected, err)
	}
}

func TestFailureEvidenceBlocksOlderDraftUntilPatternRecovers(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{Enabled: true, Mode: "apply", MinTaskCount: 2, MinSuccessRatio: 0.7},
		Store:  store, SuccessJudge: &stubSuccessJudge{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	appendTasks := func(success bool, ids ...string) {
		t.Helper()
		for _, id := range ids {
			record := evolution.LearningRecord{
				ID: id, WorkspaceID: root, Kind: evolution.RecordKindTask, Status: "new",
				Summary: "weather lookup " + id, Success: &success, CreatedAt: time.Now(),
			}
			if success {
				record.FinalOutput = "The current conditions are sunny."
			}
			if err := store.AppendLearningRecords([]evolution.LearningRecord{record}); err != nil {
				t.Fatal(err)
			}
		}
		if err := rt.RunColdPathOnce(ctx, root); err != nil {
			t.Fatal(err)
		}
	}
	appendTasks(true, "100", "200")
	drafts, err := rt.Drafts(root)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("initial drafts = %+v, err = %v", drafts, err)
	}
	initial := drafts[0]
	review, err := rt.ReviewDraft(root, initial.ID)
	if err != nil || review.ReviewToken == "" {
		t.Fatalf("initial review = %+v, err = %v", review, err)
	}
	appendTasks(false, "300")
	blocked, err := rt.ReviewDraft(root, initial.ID)
	if err != nil || blocked.ReviewToken != "" || blocked.Issue == "" {
		t.Fatalf("review after failure = %+v, err = %v", blocked, err)
	}
	if err := rt.ActOnDraft(ctx, root, initial.ID, "publish", review.ReviewToken); !errors.Is(err, evolution.ErrDraftAction) {
		t.Fatalf("publication after failure = %v", err)
	}
	appendTasks(true, "400")
	drafts, err = rt.Drafts(root)
	if err != nil || len(drafts) != 2 || drafts[0].Status != evolution.DraftStatusCandidate ||
		drafts[1].ID != initial.ID || drafts[1].Status != evolution.DraftStatusSuperseded {
		t.Fatalf("drafts after recovery = %+v, err = %v", drafts, err)
	}
}

func TestDraftColdPathSuggestsArchiveWithoutDeletingSkill(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	const name = "idle-automation"
	now := time.Now()
	if err := store.SaveProfile(evolution.SkillProfile{
		WorkspaceID: root, SkillName: name, Origin: "evolved", Status: evolution.SkillStatusArchived,
		LastUsedAt: now.Add(-370 * 24 * time.Hour), RetentionScore: 0.8,
	}); err != nil {
		t.Fatal(err)
	}
	writeSkillForCombinedShortcutTest(t, root, name, "Original guidance.")
	rt, _ := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{Enabled: true, Mode: "draft"}, Now: func() time.Time { return now },
	})
	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", name, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	suggestions, err := rt.ArchiveSuggestions(root)
	if err != nil || len(suggestions) != 1 || suggestions[0] != name {
		t.Fatalf("suggestions = %v, err = %v", suggestions, err)
	}
}

func TestImportLegacyEvolutionStateIsIdempotent(t *testing.T) {
	root := t.TempDir()
	paths := evolution.NewPaths(root, "")
	store := evolution.NewStore(paths)
	task := evolution.LearningRecord{ID: "legacy-task", WorkspaceID: root, Kind: evolution.RecordKindTask}
	pattern := evolution.LearningRecord{ID: "legacy-pattern", WorkspaceID: root, Kind: evolution.RecordKindPattern}
	profile := evolution.SkillProfile{SkillName: "old-skill", WorkspaceID: root, CurrentVersion: "v1"}
	writeLegacyEvolutionJSON(t, paths.TaskRecords, task)
	writeLegacyEvolutionJSON(t, filepath.Join(paths.RootDir, "learning-records.jsonl"), pattern)
	writeLegacyEvolutionJSON(t, filepath.Join(paths.ProfilesDir, "old-skill.json"), profile)
	writeLegacyEvolutionJSON(t, paths.SkillDrafts, []evolution.SkillDraft{{
		ID: "legacy-draft", WorkspaceID: root, SourceRecordID: pattern.ID,
	}})
	task.Summary = "existing SQLite version"
	if err := store.AppendTaskRecord(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.ImportLegacy(); err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := store.LoadTaskRecords()
	if err != nil || len(tasks) != 1 || tasks[0].Summary != task.Summary {
		t.Fatalf("tasks = %+v, err = %v", tasks, err)
	}
	patterns, err := store.LoadPatternRecords()
	if err != nil || len(patterns) != 1 || patterns[0].ID != pattern.ID {
		t.Fatalf("patterns = %+v, err = %v", patterns, err)
	}
	drafts, err := store.LoadDrafts()
	if err != nil || len(drafts) != 1 || drafts[0].ID != "legacy-draft" {
		t.Fatalf("drafts = %+v, err = %v", drafts, err)
	}
	loaded, err := store.LoadProfile(profile.SkillName)
	if err != nil || loaded.CurrentVersion != profile.CurrentVersion {
		t.Fatalf("profile = %+v, err = %v", loaded, err)
	}
	if _, err := os.Stat(paths.TaskRecords); err != nil {
		t.Fatal(err)
	}
}

func writeLegacyEvolutionJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEvolutionJudgeCachesKnownDecisionAndKeepsUncertainResultsPending(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	provider := &llmDraftRuntimeProvider{response: &providers.LLMResponse{Content: `{"reason":"incomplete JSON"}`}}
	rt, _ := evolution.NewRuntime(evolution.RuntimeOptions{
		Config:       config.EvolutionConfig{Enabled: true, Mode: "draft"},
		SuccessJudge: evolution.NewLLMTaskSuccessJudge(provider, "test"),
	})
	if err := rt.FinalizeTurn(ctx, evolution.TurnCaseInput{
		Workspace: root, Status: "completed", UserMessage: "check inventory", FinalContent: "Done",
	}); err != nil {
		t.Fatal(err)
	}
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	if err := rt.RunColdPathOnce(ctx, root); err != nil {
		t.Fatal(err)
	}
	records, err := store.LoadTaskRecords()
	if err != nil || len(records) != 1 || records[0].JudgedSuccess != nil || records[0].TurnStatus != "completed" {
		t.Fatalf("records = %+v, err = %v", records, err)
	}
	provider.response.Content = `{"success":false,"reason":"no inventory was checked"}`
	for range 2 {
		if err := rt.RunColdPathOnce(ctx, root); err != nil {
			t.Fatal(err)
		}
	}
	records, err = store.LoadTaskRecords()
	if err != nil || len(records) != 1 || records[0].JudgedSuccess == nil || *records[0].JudgedSuccess || provider.calls != 2 {
		t.Fatalf("records = %+v, provider calls = %d, err = %v", records, provider.calls, err)
	}
}
