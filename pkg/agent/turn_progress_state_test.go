package agent

import (
	"sync"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

func TestTurnProgressConcurrentMutation(t *testing.T) {
	var progress turnProgress
	const workers = 16

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			progress.recordToolKind("tool")
			progress.recordToolExecution("tool", worker%2 == 0, "", []string{"skill"})
			progress.recordNoProgress()
			progress.recordEvidence("same-evidence")
			progress.startActivity("turn", "tool", "title", "call", "tool", "detail")
			_ = progress.activitiesSnapshot()
			_ = progress.toolExecutionsSnapshot()
			_ = progress.currentNoProgressStreak()
		}(i)
	}
	wg.Wait()

	if got := progress.toolKindsSnapshot(); len(got) != 1 || got[0] != "tool" {
		t.Fatalf("tool kinds = %v, want one deduplicated tool", got)
	}
	if got := progress.toolExecutionsSnapshot(); len(got) != workers {
		t.Fatalf("tool executions = %d, want %d", len(got), workers)
	}
	if got := progress.activitiesSnapshot(); len(got) != workers {
		t.Fatalf("activities = %d, want %d", len(got), workers)
	}
}

func TestTurnProgressSnapshotsAreIndependentCopies(t *testing.T) {
	var progress turnProgress
	progress.setPlan([]planStep{{Text: "one", Status: "pending"}})
	progress.startActivity("turn", "tool", "title", "call", "exec", "detail")

	plan, activities := progress.snapshot()
	plan[0].Text = "mutated"
	activities[0].Title = "mutated"

	nextPlan, nextActivities := progress.snapshot()
	if nextPlan[0].Text != "one" {
		t.Fatalf("plan snapshot mutated progress: %+v", nextPlan)
	}
	if nextActivities[0].Title != "title" {
		t.Fatalf("activity snapshot mutated progress: %+v", nextActivities)
	}
}

func TestTurnProgressKeepsFirstTerminalReason(t *testing.T) {
	var progress turnProgress
	progress.setEndReason(TurnEndReasonCompleted)
	progress.setEndReason(TurnEndReasonTruncated)
	progress.setEndReason(TurnEndReasonProviderError)

	if got := progress.endReasonSnapshot(); got != TurnEndReasonTruncated {
		t.Fatalf("end reason = %q, want first terminal reason %q", got, TurnEndReasonTruncated)
	}
}

func TestTurnProgressToolItemsRetainBoundedHistory(t *testing.T) {
	var progress turnProgress
	for i := 0; i < maxTurnToolItems+8; i++ {
		progress.beginToolBatch("turn", i, []providers.ToolCall{{ID: "call", Name: "tool"}}, []tools.ToolPolicy{{}})
	}
	items := progress.toolItemsSnapshot()
	if len(items) != maxTurnToolItems {
		t.Fatalf("tool items = %d, want bounded %d", len(items), maxTurnToolItems)
	}
	if items[0].Index != 0 || items[0].BatchID != "turn:tools:8:9" {
		t.Fatalf("oldest retained item = %+v, want overflow trimmed", items[0])
	}
}
