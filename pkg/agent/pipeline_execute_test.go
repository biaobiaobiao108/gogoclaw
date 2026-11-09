package agent

import (
	"context"
	"fmt"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
	"os"
	"path/filepath"
	"strings"
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

type countingHarnessTool struct {
	name   string
	calls  int
	result func(map[string]any) *tools.ToolResult
}

func (t *countingHarnessTool) Name() string        { return t.name }
func (t *countingHarnessTool) Description() string { return "counting harness tool" }
func (t *countingHarnessTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *countingHarnessTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	t.calls++
	if t.result != nil {
		return t.result(args)
	}
	return tools.SilentResult("ok")
}

func TestExecuteToolsSkipsDuplicateSuccessfulReadOnlyCall(t *testing.T) {
	provider := &sequenceProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "read-1", Name: "read_file", Arguments: map[string]any{"path": "notes.md"}}}},
		{ToolCalls: []providers.ToolCall{{ID: "read-2", Name: "read_file", Arguments: map[string]any{"path": "notes.md"}}}},
		{Content: "已根据前面的内容完成回答。"},
	}}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	tool := &countingHarnessTool{name: "read_file", result: func(map[string]any) *tools.ToolResult {
		return tools.SilentResult("笔记内容")
	}}
	agent.Tools.Register(tool)
	ts := newTurnState(agent, makeTestProcessOpts("duplicate-read"), turnEventScope{
		turnID: "duplicate-read", context: newTurnContext(nil, nil, nil),
	})

	result, err := al.runTurn(context.Background(), ts, NewPipeline(al))
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls != 1 {
		t.Fatalf("duplicate read-only call executed %d times, want 1", tool.calls)
	}
	if result.finalContent != "已根据前面的内容完成回答。" {
		t.Fatalf("unexpected final response: %q", result.finalContent)
	}
	duplicateMarked := false
	for _, msg := range agent.Sessions.GetHistory(ts.sessionKey) {
		if msg.Role == "tool" && strings.Contains(msg.Content, "already ran successfully") {
			duplicateMarked = true
		}
	}
	if !duplicateMarked {
		t.Fatal("duplicate read-only call was not marked in tool history")
	}
}

func TestExecuteToolsAllowsReadAfterMutation(t *testing.T) {
	provider := &sequenceProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "read-1", Name: "read_file", Arguments: map[string]any{"path": "notes.md"}}}},
		{ToolCalls: []providers.ToolCall{{ID: "write-1", Name: "write_file", Arguments: map[string]any{"path": "notes.md", "content": "new"}}}},
		{ToolCalls: []providers.ToolCall{{ID: "read-2", Name: "read_file", Arguments: map[string]any{"path": "notes.md"}}}},
		{Content: "完成。"},
	}}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	readTool := &countingHarnessTool{name: "read_file", result: func(map[string]any) *tools.ToolResult {
		return tools.SilentResult("notes")
	}}
	writeTool := &countingHarnessTool{name: "write_file", result: func(map[string]any) *tools.ToolResult {
		return tools.SilentResult("written")
	}}
	agent.Tools.Register(readTool)
	agent.Tools.Register(writeTool)
	ts := newTurnState(agent, makeTestProcessOpts("read-after-write"), turnEventScope{
		turnID: "read-after-write", context: newTurnContext(nil, nil, nil),
	})

	if _, err := al.runTurn(context.Background(), ts, NewPipeline(al)); err != nil {
		t.Fatal(err)
	}
	if readTool.calls != 2 || writeTool.calls != 1 {
		t.Fatalf("read calls=%d, write calls=%d; want 2 and 1", readTool.calls, writeTool.calls)
	}
}

func TestExecuteToolsStopsRepeatedEmptyRetrieval(t *testing.T) {
	responses := make([]*providers.LLMResponse, 0, 6)
	for i := 0; i < 5; i++ {
		responses = append(responses, &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID: fmt.Sprintf("search-%d", i), Name: "web_search", Arguments: map[string]any{"query": fmt.Sprintf("query-%d", i)},
		}}})
	}
	responses = append(responses, &providers.LLMResponse{Content: "检索没有找到可用结果。"})
	provider := &sequenceProvider{responses: responses}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	searchTool := &countingHarnessTool{name: "web_search", result: func(args map[string]any) *tools.ToolResult {
		return tools.SilentResult(fmt.Sprintf("No results for: %v", args["query"]))
	}}
	agent.Tools.Register(searchTool)
	ts := newTurnState(agent, makeTestProcessOpts("retrieval-stall"), turnEventScope{
		turnID: "retrieval-stall", context: newTurnContext(nil, nil, nil),
	})

	result, err := al.runTurn(context.Background(), ts, NewPipeline(al))
	if err != nil {
		t.Fatal(err)
	}
	if searchTool.calls != retrievalStallStopAfter {
		t.Fatalf("search calls=%d, want %d", searchTool.calls, retrievalStallStopAfter)
	}
	if result.finalContent != "检索没有找到可用结果。" {
		t.Fatalf("unexpected final response: %q", result.finalContent)
	}
	warned := false
	for _, msg := range agent.Sessions.GetHistory(ts.sessionKey) {
		if msg.Role == "tool" && strings.Contains(msg.Content, "Three consecutive web retrieval attempts") {
			warned = true
		}
	}
	if !warned {
		t.Fatal("retrieval stall warning was not persisted")
	}
}

func TestReadOnlyToolNameAllowsWebFetchAndFindSkills(t *testing.T) {
	for _, name := range []string{"web_fetch", "find_skills", "read_file", "web_search"} {
		if !isReadOnlyToolName(name) {
			t.Fatalf("%s should be read-only", name)
		}
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
