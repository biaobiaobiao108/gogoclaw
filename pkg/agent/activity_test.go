package agent

import (
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

func TestFinishRunningActivitiesTracksCancellation(t *testing.T) {
	ts := &turnState{turnID: "activity-cancel"}
	id := ts.startTurnActivity("tool", "Run tests", "call-1", "exec", "go test ./...")
	ts.Finish(false)

	activities := ts.activitiesSnapshot()
	if len(activities) != 1 {
		t.Fatalf("activities = %d, want 1", len(activities))
	}
	activity := activities[0]
	if activity.ID != id || activity.Status != "cancelled" {
		t.Fatalf("activity = %+v, want cancelled id %q", activity, id)
	}
	if activity.CompletedAt == nil || activity.DurationMS < 0 {
		t.Fatalf("completion metadata = completed_at %v duration_ms %d", activity.CompletedAt, activity.DurationMS)
	}
	if !strings.Contains(activity.Detail, "ended before this activity completed") {
		t.Fatalf("cancellation detail = %q", activity.Detail)
	}
}

func TestFinishRunningActivitiesMarksEffectsUnknown(t *testing.T) {
	ts := &turnState{turnID: "activity-abort"}
	finished := ts.startTurnActivity("tool", "Read file", "call-1", "read_file", "")
	unknown := ts.startTurnActivity("tool", "Write file", "call-2", "write_file", "")
	ts.finishTurnActivity(finished, func(activity *session.TurnActivity) {
		activity.Status = "completed"
	})
	ts.Finish(true)

	activities := ts.activitiesSnapshot()
	if len(activities) != 2 {
		t.Fatalf("activities = %d, want 2", len(activities))
	}
	if activities[0].ID != finished || activities[0].Status != "completed" {
		t.Fatalf("completed activity was overwritten: %+v", activities[0])
	}
	if activities[1].ID != unknown || activities[1].Status != "effects_unknown" {
		t.Fatalf("unverified activity = %+v, want effects_unknown", activities[1])
	}
	if !strings.Contains(activities[1].Detail, "tool result could be verified") {
		t.Fatalf("effects unknown detail = %q", activities[1].Detail)
	}
	if activities[1].CompletedAt == nil {
		t.Fatal("effects_unknown activity has no completion timestamp")
	}
	if activities[1].DurationMS < 0 || activities[1].CompletedAt.Before(activities[1].StartedAt) {
		t.Fatalf("invalid activity duration: %+v", activities[1])
	}
}
