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
	name    string
	started chan<- struct{}
	release <-chan struct{}
}

func (t *mockReadOnlySleepTool) Name() string        { return t.name }
func (t *mockReadOnlySleepTool) Description() string { return "mock read-only tool" }
func (t *mockReadOnlySleepTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"file": map[string]any{"type": "string"}}}
}
func (t *mockReadOnlySleepTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	t.started <- struct{}{}
	select {
	case <-t.release:
	case <-ctx.Done():
		return tools.ErrorResult(ctx.Err().Error())
	}
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

	started := make(chan struct{}, 3)
	release := make(chan struct{})
	agent.Tools.Register(&mockReadOnlySleepTool{name: "read_file", started: started, release: release})
	ts := newTurnState(agent, makeTestProcessOpts("parallel-tools"), turnEventScope{
		turnID: "parallel-tools", context: newTurnContext(nil, nil, nil),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type outcome struct {
		result turnResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() { result, err := al.runTurn(ctx, ts, NewPipeline(al)); done <- outcome{result, err} }()
	// All tools must enter execution before any can finish. Disk latency and
	// scheduler overhead are deliberately excluded from the concurrency assertion.
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("read-only tools did not execute concurrently")
		}
	}
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.result.finalContent != "Read all 3 files in parallel." {
		t.Fatalf("unexpected result: %s", got.result.finalContent)
	}
}
