package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoldenBasicTurn(t *testing.T) {
	result, err := Run(context.Background(), Scenario{
		Name: "golden-basic-turn", UserMessage: "say hello", Steps: []ModelStep{Text("hello from the agent")},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "traces", "basic-turn.json")
	if os.Getenv("UPDATE_HARNESS_GOLDEN") == "1" {
		if err := result.SaveTrace(path); err != nil {
			t.Fatal(err)
		}
	}
	want, err := LoadTrace(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := CompareTrace(result.Snapshot(), want); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalizeRuntimeMetadata(t *testing.T) {
	content := strings.Join([]string{
		"## Current Time",
		"2026-11-10 12:34:56",
		"## Runtime",
		"linux arm64, Go go1.27.4",
		"## Next Section",
	}, "\n")
	want := strings.Join([]string{
		"## Runtime Time",
		"{{runtime-time}}",
		"## Runtime Metadata",
		"{{runtime}}",
		"## Next Section",
	}, "\n")
	if got := canonicalizeRuntimeTime(content); got != want {
		t.Fatalf("canonical runtime metadata = %q, want %q", got, want)
	}
}
