package agent

import (
	"fmt"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

func TestToolProgressDistinguishesSourcesWithMatchingContent(t *testing.T) {
	ts := &turnState{}
	policy := tools.ReadPolicy()
	for i := 0; i < noProgressWarningAfter; i++ {
		outcome := ts.recordToolProgress(
			fmt.Sprintf("read-file-%d", i),
			policy,
			tools.SilentResult("same content"),
			false,
		)
		if outcome.NoProgressStreak != 0 {
			t.Fatalf("distinct source %d reset the progress streak: %+v", i, outcome)
		}
	}
}

func TestToolProgressAccumulatesInvalidResults(t *testing.T) {
	ts := &turnState{}
	policy := tools.ReadPolicy()
	for i := 0; i < noProgressStopAfter; i++ {
		outcome := ts.recordToolProgress(
			fmt.Sprintf("empty-%d", i),
			policy,
			tools.SilentResult("No results for: query"),
			false,
		)
		if got, want := outcome.NoProgressStreak, i+1; got != want {
			t.Fatalf("no progress streak = %d, want %d", got, want)
		}
	}
}

func TestToolProgressTreatsDuplicateEvidenceAsNoProgress(t *testing.T) {
	ts := &turnState{}
	policy := tools.ReadPolicy()
	result := tools.SilentResult("unchanged")
	first := ts.recordToolProgress("same-call", policy, result, false)
	if first.NoProgressStreak != 0 {
		t.Fatalf("first evidence = %+v, want progress", first)
	}

	var last toolProgressOutcome
	for i := 0; i < noProgressWarningAfter; i++ {
		last = ts.recordToolProgress("same-call", policy, result, false)
	}
	if last.NoProgressStreak != noProgressWarningAfter {
		t.Fatalf("duplicate evidence streak = %d, want %d", last.NoProgressStreak, noProgressWarningAfter)
	}
}
