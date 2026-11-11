package harness

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunSpecFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenario.json")
	data := `{
  "name": "json-scenario",
  "user_message": "hello",
  "production_ingress": true,
  "steps": [{"content": "json answer"}],
  "expect": {"final": "json answer", "outbound_contains": ["json answer"]}
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := RunSpecFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || len(report.Scenarios) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestRunSpecSuiteAndDirectory(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	suiteData := `{
  "name": "multi-suite",
  "scenarios": [
    {
      "name": "suite-case-1",
      "user_message": "hi 1",
      "steps": [{"content": "ans 1"}],
      "expect": {"final": "ans 1"}
    },
    {
      "name": "suite-case-2",
      "user_message": "hi 2",
      "steps": [{"content": "ans 2"}],
      "expect": {"final": "ans 2"}
    }
  ]
}`
	if err := os.WriteFile(suitePath, []byte(suiteData), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := RunSpecPath(context.Background(), suitePath)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || len(report.Scenarios) != 2 {
		t.Fatalf("expected 2 passed scenarios, got %+v", report)
	}

	// Now test directory scan
	dirReport, err := RunSpecPath(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !dirReport.Passed || len(dirReport.Scenarios) != 2 {
		t.Fatalf("directory scan failed: %+v", dirReport)
	}
}

func TestRunSpecDirectoryAppliesPerFileSuiteOptions(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.json": `{
  "name": "file-a",
  "filter": "^keep-a$",
  "scenarios": [
    {"name": "keep-a", "user_message": "a", "steps": [{"content": "a"}], "expect": {"final": "a"}},
    {"name": "drop-a", "user_message": "drop", "steps": [{"content": "drop"}], "expect": {"final": "drop"}}
  ]
}`,
		"b.json": `{
  "name": "file-b",
  "filter": "^keep-b$",
  "scenarios": [
    {"name": "keep-b", "user_message": "b", "steps": [{"content": "b"}], "expect": {"final": "b"}},
    {"name": "drop-b", "user_message": "drop", "steps": [{"content": "drop"}], "expect": {"final": "drop"}}
  ]
}`,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	report, err := RunSpecPath(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("directory report failed: %+v", report)
	}
	if len(report.Scenarios) != 2 {
		t.Fatalf("scenario count = %d, want 2", len(report.Scenarios))
	}
	if report.Scenarios[0].Name != "keep-a" || report.Scenarios[1].Name != "keep-b" {
		t.Fatalf("scenario names = %q, %q; want keep-a, keep-b", report.Scenarios[0].Name, report.Scenarios[1].Name)
	}
}

func TestRunSpecWithSessionFault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fault.json")
	data := `{
  "name": "json-fault-scenario",
  "user_message": "test fault",
  "steps": [{"content": "never reached"}],
  "session_faults": [
    {
      "operation": "set_checkpoint",
      "times": 1,
      "error": "disk full"
    }
  ],
  "expect": {
    "turn_status": "error",
    "error_contains": "disk full"
  }
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := RunSpecFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || len(report.Scenarios) != 1 {
		t.Fatalf("unexpected report for fault scenario: %+v", report)
	}
}

func TestRunSpecWithSteeringAndInterrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steering.json")
	data := `{
  "name": "json-steering-scenario",
  "user_message": "start",
  "steps": [
    {
      "tool_calls": [{"id": "c1", "name": "echo_tool"}],
      "inject_steering": "new task"
    },
    {
      "content": "done steering"
    }
  ],
  "tools": [
    {"name": "echo_tool", "kind": "text", "content": "echo result"}
  ],
  "expect": {
    "final": "done steering",
    "history_contains": ["new task"]
  }
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := RunSpecFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || len(report.Scenarios) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
}
