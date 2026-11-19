package evolution_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/evolution"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func TestPendingEvidenceCanClusterAcrossWindows(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	completed := true
	now := time.Now().UTC()
	var records []evolution.LearningRecord
	const goal = "Summarize the latest weather observations"
	for i := 0; i < 96; i++ {
		summary := fmt.Sprintf("Review outcome %c%c", 'a'+rune(i/26), 'a'+rune(i%26))
		if i == 0 || i == 48 {
			summary = goal
		}
		records = append(records, evolution.LearningRecord{
			ID: fmt.Sprint(i), Kind: evolution.RecordKindTask, WorkspaceID: root,
			Status: "new", Success: &completed, Summary: summary, FinalOutput: "Report completed.", CreatedAt: now,
		})
	}
	if err := store.AppendTaskRecords(ctx, records); err != nil {
		t.Fatal(err)
	}
	// Simulate a backlog written before the discovery key was stored.
	db, err := sql.Open("sqlite", filepath.Join(root, "sessions", "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE evolution_records SET payload = json_remove(payload, '$.discovery_key')`)
	if closeErr := errors.Join(err, db.Close()); closeErr != nil {
		t.Fatal(closeErr)
	}
	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{Enabled: true, Mode: "draft", MinTaskCount: 2},
		Now: func() time.Time {
			now = now.Add(time.Second)
			return now
		},
		PatternClusterer: evolution.NewLLMPatternClusterer(nil, "", nil, 2, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := rt.RunColdPathOnce(ctx, root); err != nil {
			t.Fatal(err)
		}
	}
	patterns, err := store.LoadPatternRecords()
	if err != nil || len(patterns) != 1 || len(patterns[0].EvidenceRecordIDs) != 2 {
		t.Fatalf("expected one pattern with both successes, got %+v (%v)", patterns, err)
	}
}

type efficiencyProvider struct {
	t       *testing.T
	cluster string
	judge   string
	calls   []string
}

func (*efficiencyProvider) GetDefaultModel() string { return "fixture" }

func (p *efficiencyProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition,
	_ string, options map[string]any,
) (*providers.LLMResponse, error) {
	p.t.Helper()
	if options["max_tokens"] == nil {
		p.t.Fatal("missing generation token limit")
	}
	system, prompt := messages[0].Content, messages[1].Content
	var stage, result string
	switch {
	case strings.HasPrefix(system, "Group recurring"):
		stage, result = "cluster", p.cluster
		if strings.Contains(prompt, "private reply excerpt") || strings.Contains(prompt, "final_output_excerpt") {
			p.t.Fatalf("clustering included reply evidence: %s", prompt)
		}
	case strings.HasPrefix(system, "Judge each"):
		stage = "judge"
		var verdicts []map[string]any
		for i := 1; i <= strings.Count(prompt, "\nCase "); i++ {
			verdicts = append(verdicts, map[string]any{"i": i, "success": true})
		}
		data, _ := json.Marshal(map[string]any{"results": verdicts})
		result = string(data)
		if p.judge != "" {
			result = p.judge
		}
	case strings.HasPrefix(system, "Return JSON with success"):
		stage, result = "judge", `{"success":true}`
	default:
		stage = "draft"
		result = `{"target_skill_name":"weather-report","draft_type":"workflow","change_kind":"create","human_summary":"Check the weather","body_or_patch":"---\nname: weather-report\ndescription: Report current weather conditions.\n---\n# Weather\nCheck current conditions and report the result."}`
	}
	p.calls = append(p.calls, stage)
	return &providers.LLMResponse{
		Content: result, Usage: &providers.UsageInfo{PromptTokens: 90, CompletionTokens: 10, TotalTokens: 100},
	}, nil
}

func TestEvolutionKeepsMissingAndDuplicateBatchVerdictsUnknown(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	provider := &efficiencyProvider{
		t: t, judge: `{"results":[{"i":1,"success":true},{"i":1,"success":true},{"i":2,"success":false}]}`,
	}
	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config:           config.EvolutionConfig{Enabled: true, Mode: "draft"},
		PatternClusterer: evolution.NewLLMPatternClusterer(provider, "", nil, 2, nil),
		SuccessJudge:     evolution.NewLLMTaskSuccessJudge(provider, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two", "three"} {
		if err := rt.FinalizeTurn(ctx, evolution.TurnCaseInput{
			Workspace: workspace, TurnID: id, Status: "completed", UserMessage: "weather report",
			FinalContent: "private reply excerpt",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.RunColdPathOnce(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	records, err := evolution.NewStore(evolution.NewPaths(workspace, "")).LoadTaskRecords()
	if err != nil || len(records) != 3 || records[0].JudgedSuccess != nil ||
		records[1].JudgedSuccess == nil || *records[1].JudgedSuccess || records[2].JudgedSuccess != nil {
		t.Fatalf("judged records = %+v, err = %v", records, err)
	}
	if strings.Join(provider.calls, ",") != "judge" {
		t.Fatalf("ambiguous results triggered additional calls: %v", provider.calls)
	}
}

func TestEvolutionDiscoversCandidateBeforeJudgingAndTracksStageUsage(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	provider := &efficiencyProvider{
		t: t, cluster: `{"clusters":[{"label":"weather-report","summary":"weather report 200","task_record_ids":["2","3"]}]}`,
	}
	var metered []string
	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config:           config.EvolutionConfig{Enabled: true, Mode: "apply"},
		PatternClusterer: evolution.NewLLMPatternClusterer(provider, "", nil, 2, nil),
		SuccessJudge:     evolution.NewLLMTaskSuccessJudge(provider, ""),
		DraftGenerator:   evolution.NewDraftGeneratorForWorkspace(workspace, provider, ""),
		RecordModelUsage: func(stage, model string, usage providers.UsageInfo) {
			if model != "fixture" || usage.TotalTokens != 100 {
				t.Fatalf("unexpected usage %s, %+v", model, usage)
			}
			metered = append(metered, stage)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	record := func(id, goal, status string) {
		t.Helper()
		input := evolution.TurnCaseInput{Workspace: workspace, TurnID: id, UserMessage: goal, Status: status}
		if status == "completed" {
			input.FinalContent = "private reply excerpt"
		}
		if err := rt.FinalizeTurn(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	run := func() {
		t.Helper()
		if err := rt.RunColdPathOnce(ctx, workspace); err != nil {
			t.Fatal(err)
		}
	}
	record("case-a", "inventory status 100", "completed")
	run()
	if len(provider.calls) != 0 {
		t.Fatalf("single task made model calls: %v", provider.calls)
	}
	record("case-b", "weather report 200", "completed")
	record("case-c", "forecast for tomorrow", "completed")
	run()
	if strings.Join(provider.calls, ",") != "cluster,judge,draft" || strings.Join(metered, ",") != "cluster,judge,draft" {
		t.Fatalf("model calls %v, metered %v", provider.calls, metered)
	}
	drafts, err := rt.Drafts(workspace)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("drafts = %+v, err = %v", drafts, err)
	}
	draft := drafts[0]
	preview, err := rt.ReviewDraft(workspace, draft.ID)
	if err != nil || preview.ReviewToken == "" {
		t.Fatalf("preview = %+v, err = %v", preview, err)
	}
	provider.cluster = `{"clusters":[]}`
	record("case-d", "weather report 999", "failed")
	run()
	run()
	run()
	if strings.Join(provider.calls, ",") != "cluster,judge,draft,cluster,cluster" {
		t.Fatalf("known failure should update the pattern without judging when semantic clustering omitted it: %v", provider.calls)
	}
	if err := rt.ActOnDraft(ctx, workspace, draft.ID, "publish", preview.ReviewToken); !errors.Is(err, evolution.ErrDraftAction) {
		t.Fatalf("stale draft publication returned %v", err)
	}
}

func TestEvolutionJudgesTenLocalMatchesInTwoBatches(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	provider := &efficiencyProvider{t: t}
	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config:           config.EvolutionConfig{Enabled: true, Mode: "apply"},
		PatternClusterer: evolution.NewLLMPatternClusterer(provider, "", nil, 2, nil),
		SuccessJudge:     evolution.NewLLMTaskSuccessJudge(provider, ""),
		DraftGenerator:   evolution.NewDraftGeneratorForWorkspace(workspace, provider, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	record := func(i int) {
		t.Helper()
		if err := rt.FinalizeTurn(ctx, evolution.TurnCaseInput{
			TurnID: fmt.Sprint(i), Workspace: workspace, Status: "completed",
			UserMessage: fmt.Sprintf("weather report %d", i), FinalContent: "private reply excerpt",
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 10 {
		record(i)
	}
	for range 2 {
		if err := rt.RunColdPathOnce(ctx, workspace); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(provider.calls, ",") != "judge,judge,draft" {
		t.Fatalf("unexpected calls for ten local matches: %v", provider.calls)
	}
	drafts, err := rt.Drafts(workspace)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("drafts = %+v, err = %v", drafts, err)
	}
	review, err := rt.ReviewDraft(workspace, drafts[0].ID)
	if err != nil || review.ReviewToken == "" {
		t.Fatalf("review = %+v, err = %v", review, err)
	}
	if err := rt.ActOnDraft(ctx, workspace, drafts[0].ID, "publish", review.ReviewToken); err != nil {
		t.Fatal(err)
	}
	record(10)
	if err := rt.RunColdPathOnce(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if strings.Join(provider.calls, ",") != "judge,judge,draft,judge" {
		t.Fatalf("one corroborating success generated another draft: %v", provider.calls)
	}
	record(11)
	if err := rt.RunColdPathOnce(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if strings.Join(provider.calls, ",") != "judge,judge,draft,judge,judge,draft" {
		t.Fatalf("two new successes did not trigger a new draft: %v", provider.calls)
	}
}
