package evolution_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/evolution"
)

func TestRuntime_PublishesNewSkillAndProfileAfterReview(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))

	rule := evolution.LearningRecord{
		ID:          "rule-1",
		Kind:        evolution.RecordKindRule,
		WorkspaceID: root,
		CreatedAt:   time.Unix(1700000000, 0).UTC(),
		Summary:     "weather native-name path",
		Status:      evolution.RecordStatus("ready"),
		EventCount:  4,
	}
	if err := store.AppendLearningRecords([]evolution.LearningRecord{rule}); err != nil {
		t.Fatalf("AppendLearningRecords: %v", err)
	}

	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{Enabled: true, Mode: "apply"},
		Now:    func() time.Time { return time.Unix(1700001000, 0).UTC() },
		Store:  store,
		DraftGenerator: stubDraftGenerator{
			draft: evolution.SkillDraft{
				ID:              "draft-1",
				WorkspaceID:     root,
				SourceRecordID:  "rule-1",
				TargetSkillName: "weather",
				DraftType:       evolution.DraftTypeShortcut,
				ChangeKind:      evolution.ChangeKindCreate,
				HumanSummary:    "weather helper",
				IntendedUseCases: []string{
					"weather native-name path",
				},
				PreferredEntryPath: []string{"weather"},
				AvoidPatterns:      []string{"avoid translating city names before querying weather"},
				BodyOrPatch:        "---\nname: weather\ndescription: weather helper\n---\n# Weather\n## Start Here\nUse native-name query first.\n",
			},
		},
		SkillsRecaller: evolution.NewSkillsRecaller(root),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	if runErr := rt.RunColdPathOnce(context.Background(), root); runErr != nil {
		t.Fatalf("RunColdPathOnce: %v", runErr)
	}
	publishCandidate(t, rt, root)

	skillPath := filepath.Join(root, "skills", "weather", "SKILL.md")
	if _, statErr := os.Stat(skillPath); statErr != nil {
		t.Fatalf("expected skill file: %v", statErr)
	}

	profile, err := store.LoadProfile("weather")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if profile.Status != evolution.SkillStatusActive {
		t.Fatalf("Status = %q, want %q", profile.Status, evolution.SkillStatusActive)
	}
	if profile.CurrentVersion == "" {
		t.Fatal("CurrentVersion should not be empty")
	}
	if profile.ChangeReason != "weather helper" {
		t.Fatalf("ChangeReason = %q, want weather helper", profile.ChangeReason)
	}
	if len(profile.IntendedUseCases) != 1 || profile.IntendedUseCases[0] != "weather native-name path" {
		t.Fatalf("IntendedUseCases = %v, want [weather native-name path]", profile.IntendedUseCases)
	}
	if len(profile.PreferredEntryPath) != 1 || profile.PreferredEntryPath[0] != "weather" {
		t.Fatalf("PreferredEntryPath = %v, want [weather]", profile.PreferredEntryPath)
	}
	if len(profile.AvoidPatterns) != 1 ||
		profile.AvoidPatterns[0] != "avoid translating city names before querying weather" {
		t.Fatalf("AvoidPatterns = %v, want populated metadata", profile.AvoidPatterns)
	}

	drafts, err := store.LoadDrafts()
	if err != nil {
		t.Fatalf("LoadDrafts: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("len(drafts) = %d, want 1", len(drafts))
	}
	if drafts[0].Status != evolution.DraftStatusAccepted {
		t.Fatalf("draft status = %q, want %q", drafts[0].Status, evolution.DraftStatusAccepted)
	}
}

func TestRuntime_RunColdPathOnce_DraftModeKeepsCandidateDraft(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))

	rule := evolution.LearningRecord{
		ID:          "rule-1",
		Kind:        evolution.RecordKindRule,
		WorkspaceID: root,
		CreatedAt:   time.Unix(1700000000, 0).UTC(),
		Summary:     "weather native-name path",
		Status:      evolution.RecordStatus("ready"),
		EventCount:  4,
	}
	if err := store.AppendLearningRecords([]evolution.LearningRecord{rule}); err != nil {
		t.Fatalf("AppendLearningRecords: %v", err)
	}

	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{Enabled: true, Mode: "draft"},
		Now:    func() time.Time { return time.Unix(1700001000, 0).UTC() },
		Store:  store,
		DraftGenerator: stubDraftGenerator{
			draft: evolution.SkillDraft{
				ID:              "draft-1",
				WorkspaceID:     root,
				SourceRecordID:  "rule-1",
				TargetSkillName: "weather",
				DraftType:       evolution.DraftTypeShortcut,
				ChangeKind:      evolution.ChangeKindCreate,
				HumanSummary:    "weather helper",
				BodyOrPatch:     "---\nname: weather\ndescription: weather helper\n---\n# Weather\n## Start Here\nUse native-name query first.\n",
			},
		},
		SkillsRecaller: evolution.NewSkillsRecaller(root),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	if runErr := rt.RunColdPathOnce(context.Background(), root); runErr != nil {
		t.Fatalf("RunColdPathOnce: %v", runErr)
	}

	if _, statErr := os.Stat(filepath.Join(root, "skills", "weather", "SKILL.md")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no applied skill file, got err=%v", statErr)
	}
	if _, loadErr := store.LoadProfile("weather"); !os.IsNotExist(loadErr) {
		t.Fatalf("expected no profile, got err=%v", loadErr)
	}

	drafts, err := store.LoadDrafts()
	if err != nil {
		t.Fatalf("LoadDrafts: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("len(drafts) = %d, want 1", len(drafts))
	}
	if drafts[0].Status != evolution.DraftStatusCandidate {
		t.Fatalf("draft status = %q, want %q", drafts[0].Status, evolution.DraftStatusCandidate)
	}
}

func TestRuntime_RunColdPathOnce_DraftModeRefreshesExistingCandidateWithEvidence(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	for _, source := range []struct {
		name string
		body string
	}{
		{name: "three-one-theorem", body: "Add 31 to the input value."},
		{name: "four-two-theorem", body: "Add 42 to the current value."},
		{name: "five-three-theorem", body: "Subtract 53 from the current value."},
	} {
		skillPath := filepath.Join(root, "skills", source.name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		content := "---\nname: " + source.name + "\ndescription: theorem helper\n---\n# " + source.name + "\n" + source.body + "\n"
		if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	success := true
	if err := store.SaveTaskRecords([]evolution.LearningRecord{{
		ID:             "task-1",
		Kind:           evolution.RecordKindTask,
		WorkspaceID:    root,
		CreatedAt:      time.Unix(1700000000, 0).UTC(),
		Summary:        "调用三一定理计算100",
		FinalOutput:    "100 + 31 = 131; 131 + 42 = 173; 173 - 53 = 120",
		Status:         evolution.RecordStatus("clustered"),
		Success:        &success,
		UsedSkillNames: []string{"three-one-theorem", "four-two-theorem", "five-three-theorem"},
	}}); err != nil {
		t.Fatalf("SaveTaskRecords: %v", err)
	}
	if err := store.SavePatternRecords([]evolution.LearningRecord{{
		ID:            "pattern-1",
		Kind:          evolution.RecordKindPattern,
		WorkspaceID:   root,
		CreatedAt:     time.Unix(1700000001, 0).UTC(),
		Summary:       "调用三一定理计算100",
		Status:        evolution.RecordStatus("ready"),
		TaskRecordIDs: []string{"task-1"},
		UpdatedAt:     func() *time.Time { value := time.Unix(1700000002, 0).UTC(); return &value }(),
	}}); err != nil {
		t.Fatalf("SavePatternRecords: %v", err)
	}
	if err := store.SaveDrafts([]evolution.SkillDraft{
		{
			ID:              "draft-pattern-1",
			WorkspaceID:     root,
			SourceRecordID:  "pattern-1",
			TargetSkillName: "learned-skill",
			DraftType:       evolution.DraftTypeShortcut,
			ChangeKind:      evolution.ChangeKindCreate,
			HumanSummary:    "old generic draft",
			BodyOrPatch:     "---\nname: learned-skill\ndescription: old\n---\n# Learned Skill\n\nNo explicit winning path was recorded.\n",
			Status:          evolution.DraftStatusCandidate,
		},
	}); err != nil {
		t.Fatalf("SaveDrafts: %v", err)
	}

	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{Enabled: true, Mode: "draft"},
		Now:    func() time.Time { return time.Unix(1700001000, 0).UTC() },
		Store:  store,
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	if runErr := rt.RunColdPathOnce(context.Background(), root); runErr != nil {
		t.Fatalf("RunColdPathOnce: %v", runErr)
	}

	drafts, err := store.LoadDrafts()
	if err != nil {
		t.Fatalf("LoadDrafts: %v", err)
	}
	if len(drafts) != 2 || drafts[0].Status != evolution.DraftStatusSuperseded {
		t.Fatalf("len(drafts) = %d, want one superseded and one candidate", len(drafts))
	}
	if drafts[1].Status != evolution.DraftStatusCandidate {
		t.Fatalf("draft status = %q, want candidate", drafts[0].Status)
	}
	for _, want := range []string{
		"calculate-100-via-theorems",
		"Add 31 to the input value",
		"Subtract 53 from the current value",
		"100 + 31 = 131",
		"task-1",
	} {
		if !strings.Contains(drafts[1].BodyOrPatch, want) && drafts[1].TargetSkillName != want {
			t.Fatalf("refreshed draft missing %q:\nname=%s\n%s", want, drafts[1].TargetSkillName, drafts[1].BodyOrPatch)
		}
	}
}

func TestRuntime_PublishesExistingCandidateAfterReview(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))

	rule := evolution.LearningRecord{
		ID:          "rule-1",
		Kind:        evolution.RecordKindRule,
		WorkspaceID: root,
		CreatedAt:   time.Unix(1700000000, 0).UTC(),
		Summary:     "weather native-name path",
		Status:      evolution.RecordStatus("ready"),
		EventCount:  4,
	}
	if err := store.AppendLearningRecords([]evolution.LearningRecord{rule}); err != nil {
		t.Fatalf("AppendLearningRecords: %v", err)
	}
	if err := store.SaveDrafts([]evolution.SkillDraft{
		{
			ID:              "draft-1",
			WorkspaceID:     root,
			SourceRecordID:  "rule-1",
			TargetSkillName: "weather",
			DraftType:       evolution.DraftTypeShortcut,
			ChangeKind:      evolution.ChangeKindCreate,
			HumanSummary:    "weather helper",
			BodyOrPatch:     "---\nname: weather\ndescription: weather helper\n---\n# Weather\n## Start Here\nUse native-name query first.\n",
			Status:          evolution.DraftStatusCandidate,
		},
	}); err != nil {
		t.Fatalf("SaveDrafts: %v", err)
	}

	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{Enabled: true, Mode: "apply"},
		Now:    func() time.Time { return time.Unix(1700001000, 0).UTC() },
		Store:  store,
		DraftGenerator: stubDraftGenerator{
			draft: evolution.SkillDraft{
				ID:              "draft-unused",
				WorkspaceID:     root,
				SourceRecordID:  "rule-1",
				TargetSkillName: "unused-weather",
				DraftType:       evolution.DraftTypeShortcut,
				ChangeKind:      evolution.ChangeKindCreate,
				HumanSummary:    "unused",
				BodyOrPatch:     "---\nname: unused-weather\ndescription: unused\n---\n# Unused\n",
			},
		},
		SkillsRecaller: evolution.NewSkillsRecaller(root),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	if runErr := rt.RunColdPathOnce(context.Background(), root); runErr != nil {
		t.Fatalf("RunColdPathOnce: %v", runErr)
	}
	publishCandidate(t, rt, root)

	if _, statErr := os.Stat(filepath.Join(root, "skills", "weather", "SKILL.md")); statErr != nil {
		t.Fatalf("expected existing candidate to be applied: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "skills", "unused-weather", "SKILL.md")); !os.IsNotExist(statErr) {
		t.Fatalf("expected source rule to stay skipped after applying existing draft, got err=%v", statErr)
	}
	profile, err := store.LoadProfile("weather")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if profile.CurrentVersion != "draft-1" {
		t.Fatalf("CurrentVersion = %q, want draft-1", profile.CurrentVersion)
	}

	drafts, err := store.LoadDrafts()
	if err != nil {
		t.Fatalf("LoadDrafts: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("len(drafts) = %d, want 1", len(drafts))
	}
	if drafts[0].Status != evolution.DraftStatusAccepted {
		t.Fatalf("draft status = %q, want %q", drafts[0].Status, evolution.DraftStatusAccepted)
	}
}

func TestRuntime_OrphanCandidateDoesNotBlockReadyDraft(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))

	rule := evolution.LearningRecord{
		ID:          "rule-1",
		Kind:        evolution.RecordKindRule,
		WorkspaceID: root,
		CreatedAt:   time.Unix(1700000000, 0).UTC(),
		Summary:     "weather native-name path",
		Status:      evolution.RecordStatus("ready"),
		EventCount:  4,
	}
	if err := store.AppendLearningRecords([]evolution.LearningRecord{rule}); err != nil {
		t.Fatalf("AppendLearningRecords: %v", err)
	}
	if err := store.SaveDrafts([]evolution.SkillDraft{
		{
			ID:              "draft-orphan",
			WorkspaceID:     root,
			SourceRecordID:  "missing-rule",
			TargetSkillName: "orphan-weather",
			DraftType:       evolution.DraftTypeShortcut,
			ChangeKind:      evolution.ChangeKindCreate,
			HumanSummary:    "orphan weather helper",
			BodyOrPatch:     "---\nname: orphan-weather\ndescription: orphan weather helper\n---\n# Orphan Weather\n## Start Here\nUse stale guidance.\n",
			Status:          evolution.DraftStatusCandidate,
		},
	}); err != nil {
		t.Fatalf("SaveDrafts: %v", err)
	}

	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{Enabled: true, Mode: "apply"},
		Now:    func() time.Time { return time.Unix(1700001000, 0).UTC() },
		Store:  store,
		DraftGenerator: stubDraftGenerator{
			draft: evolution.SkillDraft{
				ID:              "draft-valid",
				WorkspaceID:     root,
				SourceRecordID:  "rule-1",
				TargetSkillName: "valid-weather",
				DraftType:       evolution.DraftTypeShortcut,
				ChangeKind:      evolution.ChangeKindCreate,
				HumanSummary:    "valid weather helper",
				BodyOrPatch:     "---\nname: valid-weather\ndescription: valid weather helper\n---\n# Valid Weather\n",
			},
		},
		SkillsRecaller: evolution.NewSkillsRecaller(root),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	if runErr := rt.RunColdPathOnce(context.Background(), root); runErr != nil {
		t.Fatalf("RunColdPathOnce: %v", runErr)
	}
	publishCandidate(t, rt, root)

	if _, statErr := os.Stat(filepath.Join(root, "skills", "orphan-weather", "SKILL.md")); !os.IsNotExist(statErr) {
		t.Fatalf("orphan candidate draft should not be applied, got err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "skills", "valid-weather", "SKILL.md")); statErr != nil {
		t.Fatalf("expected current ready rule draft to be applied: %v", statErr)
	}
	drafts, err := store.LoadDrafts()
	if err != nil {
		t.Fatalf("LoadDrafts: %v", err)
	}
	statusByID := map[string]evolution.DraftStatus{}
	for _, draft := range drafts {
		statusByID[draft.ID] = draft.Status
	}
	if statusByID["draft-orphan"] != evolution.DraftStatusCandidate {
		t.Fatalf("orphan draft status = %q, want candidate", statusByID["draft-orphan"])
	}
}

func TestRuntime_RunColdPathOnce_ApplyModeRetargetsStableMultiSkillPathIntoCombinedShortcut(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	writeSkillForCombinedShortcutTest(t, root, "three-one-theorem", "Add 31 to the input before continuing.")
	writeSkillForCombinedShortcutTest(t, root, "four-two-theorem", "Add 42 to the intermediate result.")
	writeSkillForCombinedShortcutTest(t, root, "five-three-theorem", "Subtract 53 to produce the final result.")

	rule := evolution.LearningRecord{
		ID:          "rule-1",
		Kind:        evolution.RecordKindRule,
		WorkspaceID: root,
		CreatedAt:   time.Unix(1700000000, 0).UTC(),
		Summary:     "calculate 100",
		Status:      evolution.RecordStatus("ready"),
		EventCount:  4,
		SuccessRate: 1,
		WinningPath: []string{"three-one-theorem", "four-two-theorem", "five-three-theorem"},
	}
	if err := store.AppendLearningRecords([]evolution.LearningRecord{rule}); err != nil {
		t.Fatalf("AppendLearningRecords: %v", err)
	}

	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{Enabled: true, Mode: "apply"},
		Now:    func() time.Time { return time.Unix(1700001000, 0).UTC() },
		Store:  store,
		DraftGenerator: stubDraftGenerator{
			draft: evolution.SkillDraft{
				ID:              "draft-1",
				WorkspaceID:     root,
				SourceRecordID:  "rule-1",
				TargetSkillName: "five-three-theorem",
				DraftType:       evolution.DraftTypeShortcut,
				ChangeKind:      evolution.ChangeKindAppend,
				HumanSummary:    "combine the theorem chain into one shortcut skill",
				BodyOrPatch: strings.Join([]string{
					"Prefer the full theorem chain directly.",
					"",
					"## Component Skill Breakdown",
					"messy raw component dump should be removed.",
					"",
					"## Learned Shortcut",
					"Net effect: input + 20.",
				}, "\n"),
			},
		},
		SkillsRecaller: evolution.NewSkillsRecaller(root),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	if runErr := rt.RunColdPathOnce(context.Background(), root); runErr != nil {
		t.Fatalf("RunColdPathOnce: %v", runErr)
	}
	publishCandidate(t, rt, root)

	skillPath := filepath.Join(root, "skills", "calculate-100-via-theorems", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "name: calculate-100-via-theorems") {
		t.Fatalf("unexpected content:\n%s", content)
	}
	if !strings.Contains(content, "# Calculate 100 Via Theorems") {
		t.Fatalf("missing synthesized heading:\n%s", content)
	}
	if !strings.Contains(content, "Prefer the full theorem chain directly.") {
		t.Fatalf("missing learned content:\n%s", content)
	}
	if !strings.Contains(content, "## Procedure") {
		t.Fatalf("missing compact procedure:\n%s", content)
	}
	if !strings.Contains(content, "## Procedure Details") {
		t.Fatalf("missing source skill summary:\n%s", content)
	}
	if strings.Contains(content, "Learned") || strings.Contains(content, "Source Evidence") {
		t.Fatalf("deployed skill should not expose learning traces:\n%s", content)
	}
	if !strings.Contains(content, "Add 31 to the input") ||
		!strings.Contains(content, "Subtract 53 to produce the final result") {
		t.Fatalf("missing extracted component skill content:\n%s", content)
	}
	if strings.Contains(content, "Extracted guidance") {
		t.Fatalf("component content should be concise, not raw extracted guidance:\n%s", content)
	}
	if strings.Contains(content, "messy raw component dump") {
		t.Fatalf("learned context should remove verbose component dumps:\n%s", content)
	}
	if !strings.Contains(content, "Use `calculate-100-via-theorems` directly") {
		t.Fatalf("missing direct shortcut guidance:\n%s", content)
	}

	drafts, err := store.LoadDrafts()
	if err != nil {
		t.Fatalf("LoadDrafts: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("len(drafts) = %d, want 1", len(drafts))
	}
	if drafts[0].Status != evolution.DraftStatusAccepted {
		t.Fatalf("draft status = %q, want %q", drafts[0].Status, evolution.DraftStatusAccepted)
	}
	if drafts[0].ChangeKind != evolution.ChangeKindCreate {
		t.Fatalf("ChangeKind = %q, want %q", drafts[0].ChangeKind, evolution.ChangeKindCreate)
	}
	if drafts[0].TargetSkillName != "calculate-100-via-theorems" {
		t.Fatalf("TargetSkillName = %q, want calculate-100-via-theorems", drafts[0].TargetSkillName)
	}
	if len(drafts[0].PreferredEntryPath) != 1 || drafts[0].PreferredEntryPath[0] != "calculate-100-via-theorems" {
		t.Fatalf("PreferredEntryPath = %v, want [calculate-100-via-theorems]", drafts[0].PreferredEntryPath)
	}
	if len(drafts[0].ReviewNotes) == 0 {
		t.Fatal("expected normalization review notes")
	}
}

func TestRuntime_RunColdPathOnce_CombinedShortcutKeepsReadableLongGuidance(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	longOperation := strings.Join([]string{
		"Step 1: normalize the incoming number and keep the original input available for reporting.",
		"Step 2: add 31 to the normalized value and record the intermediate value.",
		"Step 3: add 42 to the intermediate value and verify that arithmetic was performed exactly once.",
		"Step 4: subtract 53 from the second intermediate value and return only the final value.",
		"Step 5: if the user asks for explanation, include the compact arithmetic chain without unrelated context.",
	}, " ")
	writeSkillForCombinedShortcutTest(t, root, "three-one-theorem", longOperation)
	writeSkillForCombinedShortcutTest(t, root, "four-two-theorem", "Add 42 to the intermediate result.")

	rule := evolution.LearningRecord{
		ID:          "rule-1",
		Kind:        evolution.RecordKindRule,
		WorkspaceID: root,
		CreatedAt:   time.Unix(1700000000, 0).UTC(),
		Summary:     "calculate with theorem chain",
		Status:      evolution.RecordStatus("ready"),
		EventCount:  4,
		SuccessRate: 1,
		WinningPath: []string{"three-one-theorem", "four-two-theorem"},
	}
	if err := store.AppendLearningRecords([]evolution.LearningRecord{rule}); err != nil {
		t.Fatalf("AppendLearningRecords: %v", err)
	}

	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{Enabled: true, Mode: "apply"},
		Now:    func() time.Time { return time.Unix(1700001000, 0).UTC() },
		Store:  store,
		DraftGenerator: stubDraftGenerator{draft: evolution.SkillDraft{
			ID:              "draft-1",
			WorkspaceID:     root,
			SourceRecordID:  "rule-1",
			TargetSkillName: "three-one-theorem",
			DraftType:       evolution.DraftTypeShortcut,
			ChangeKind:      evolution.ChangeKindAppend,
			HumanSummary:    "combine theorem chain",
			BodyOrPatch: strings.Join([]string{
				"Prefer the theorem chain directly.",
				"Include Step A, Step B, Step C, Step D, Step E, Step F, Step G, Step H, Step I, Step J, Step K, Step L, Step M, Step N, Step O, Step P, Step Q, Step R, Step S, Step T, Step U, Step V, Step W, Step X, Step Y, Step Z, and then return the answer.",
				"Finish with a short arithmetic explanation.",
			}, " "),
		}},
		SkillsRecaller: evolution.NewSkillsRecaller(root),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	if runErr := rt.RunColdPathOnce(context.Background(), root); runErr != nil {
		t.Fatalf("RunColdPathOnce: %v", runErr)
	}
	publishCandidate(t, rt, root)

	data, err := os.ReadFile(filepath.Join(root, "skills", "calculate-with-theorem-chain-via-theorems", "SKILL.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Step 5: if the user asks for explanation") {
		t.Fatalf("procedure details were cut too aggressively:\n%s", content)
	}
	if !strings.Contains(content, "Step Z, and then return the answer.") {
		t.Fatalf("procedure notes were cut too aggressively:\n%s", content)
	}
}

func writeSkillForCombinedShortcutTest(t *testing.T, root, name, body string) {
	t.Helper()

	skillPath := filepath.Join(root, "skills", name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	content := strings.Join([]string{
		"---",
		"name: " + name,
		"description: test component skill",
		"---",
		"# " + name,
		body,
		"",
	}, "\n")
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func publishCandidate(t *testing.T, runtime *evolution.Runtime, workspace string) {
	t.Helper()
	drafts, err := runtime.Drafts(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, draft := range drafts {
		review, err := runtime.ReviewDraft(workspace, draft.ID)
		if err != nil {
			t.Fatal(err)
		}
		if review.ReviewToken == "" {
			continue
		}
		path := filepath.Join(workspace, "skills", draft.TargetSkillName, "SKILL.md")
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cold path changed live skill before approval: %v", err)
		}
		if err := runtime.ActOnDraft(context.Background(), workspace, draft.ID, "publish", review.ReviewToken); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("no publishable candidate in %v", drafts)
}
