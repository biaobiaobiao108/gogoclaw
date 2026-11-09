package chatmeta

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func TestBranchStoreRoundTripAndSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-branches.json")
	store, err := LoadBranches(path)
	if err != nil {
		t.Fatal(err)
	}
	store.UpsertVersion("session-1", "user-1", "pico:session-1", BranchVersion{
		ID:        "answer-1",
		Preview:   "first",
		CreatedAt: time.Unix(1, 0),
		Messages:  []providers.Message{{ID: "answer-1", Role: "assistant", Content: "first"}},
	})
	store.UpsertVersion("session-1", "user-1", "pico:session-1", BranchVersion{
		ID:        "answer-2",
		Preview:   "second",
		CreatedAt: time.Unix(2, 0),
		Messages:  []providers.Message{{ID: "answer-2", Role: "assistant", Content: "second"}},
	})
	if err := SaveBranches(path, store); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadBranches(path)
	if err != nil {
		t.Fatal(err)
	}
	groups := loaded.VisibleGroups("session-1")
	if len(groups) != 1 || len(groups[0].Versions) != 2 {
		t.Fatalf("groups = %+v", groups)
	}
	if groups[0].ActiveVersionID != "answer-2" {
		t.Fatalf("active version = %q", groups[0].ActiveVersionID)
	}
	if err := loaded.SetActive("session-1", "user-1", "answer-1"); err != nil {
		t.Fatal(err)
	}
	group, version, ok := loaded.FindVersion("session-1", "answer-1")
	if !ok || group.RootMessageID != "user-1" || version.Preview != "first" {
		t.Fatalf("FindVersion() = %+v %+v %v", group, version, ok)
	}

	if err := SaveBranches(path, loaded); err != nil {
		t.Fatal(err)
	}
	if err := RemoveBranchSession(path, "session-1"); err != nil {
		t.Fatal(err)
	}
	removed, err := LoadBranches(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Sessions) != 0 {
		t.Fatalf("sessions after removal = %+v", removed.Sessions)
	}
}

func TestCloneMessagesDoesNotShareSlices(t *testing.T) {
	original := []providers.Message{{
		ID:      "message-1",
		Role:    "assistant",
		Content: "answer",
		Media:   []string{"media-1"},
	}}
	cloned := CloneMessages(original)
	cloned[0].Content = "changed"
	cloned[0].Media[0] = "changed"
	if original[0].Content != "answer" || original[0].Media[0] != "media-1" {
		t.Fatalf("original mutated: %+v", original[0])
	}
}

func TestPreviewCollapsesWhitespaceAndTruncatesRunes(t *testing.T) {
	got := Preview("  第一行\n\n第二行 第三行  ", 6)
	if got != "第一行 第二…" {
		t.Fatalf("Preview() = %q", got)
	}
}
