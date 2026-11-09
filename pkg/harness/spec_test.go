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
