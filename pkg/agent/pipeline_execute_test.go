package agent

import (
	"context"
	"fmt"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInferSkillNamesFromToolCall_ReadFileSkillMarkdown(t *testing.T) {
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, "skills", "three-one")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: three-one\ndescription: test\n---\n# Three One\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cb := NewContextBuilder(workspace)
	ts := &turnState{
		workspace: workspace,
		agent: &AgentInstance{
			Workspace:      workspace,
			ContextBuilder: cb,
		},
	}

	got := inferSkillNamesFromToolCall(ts, "read_file", map[string]any{
		"path": filepath.Join(workspace, "skills", "three-one", "SKILL.md"),
	})
	if len(got) != 1 || got[0] != "three-one" {
		t.Fatalf("inferSkillNamesFromToolCall = %v, want [three-one]", got)
	}
}

func TestInferSkillNamesFromToolCall_NonSkillFileIgnored(t *testing.T) {
	workspace := t.TempDir()
	ts := &turnState{workspace: workspace}

	got := inferSkillNamesFromToolCall(ts, "read_file", map[string]any{
		"path": filepath.Join(workspace, "README.md"),
	})
	if len(got) != 0 {
		t.Fatalf("inferSkillNamesFromToolCall = %v, want empty", got)
	}
}

type mockReadOnlySleepTool struct {
	name  string
	delay time.Duration
}

func (t *mockReadOnlySleepTool) Name() string        { return t.name }
func (t *mockReadOnlySleepTool) Description() string { return "mock read-only tool" }
func (t *mockReadOnlySleepTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"file": map[string]any{"type": "string"}}}
}
func (t *mockReadOnlySleepTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	time.Sleep(t.delay)
	return tools.SilentResult(fmt.Sprintf("content of %v", args["file"]))
}

func TestExecuteTools_ParallelReadOnly(t *testing.T) {
	provider := &sequenceProvider{
		responses: []*providers.LLMResponse{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "call-1", Name: "read_file", Arguments: map[string]any{"file": "a.txt"}},
					{ID: "call-2", Name: "read_file", Arguments: map[string]any{"file": "b.txt"}},
					{ID: "call-3", Name: "read_file", Arguments: map[string]any{"file": "c.txt"}},
				},
			},
			{Content: "Read all 3 files in parallel."},
		},
	}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()

	agent.Tools.Register(&mockReadOnlySleepTool{name: "read_file", delay: 80 * time.Millisecond})
	ts := newTurnState(agent, makeTestProcessOpts("parallel-tools"), turnEventScope{
		turnID: "parallel-tools", context: newTurnContext(nil, nil, nil),
	})

	start := time.Now()
	result, err := al.runTurn(context.Background(), ts, NewPipeline(al))
	duration := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if result.finalContent != "Read all 3 files in parallel." {
		t.Fatalf("unexpected result: %s", result.finalContent)
	}
	// Serial execution of 3 tools with 80ms delay would take >= 240ms in tools alone.
	// Parallel execution runs them concurrently in ~80ms, total turn finishes well below 230ms.
	if duration >= 230*time.Millisecond {
		t.Errorf("expected parallel execution to complete faster than serial (230ms threshold), took %v", duration)
	}
}
