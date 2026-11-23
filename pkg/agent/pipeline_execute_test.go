package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/approval"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
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

type rejectingMediaChannelManager struct {
	recordingChannelManager
}

func (*rejectingMediaChannelManager) SendMedia(context.Context, bus.OutboundMediaMessage) error {
	return errors.New("channel rejected audio")
}

func TestExecuteToolsRecordsFailedMediaDelivery(t *testing.T) {
	provider := &sequenceProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "tts-1", Name: "send_tts"}}},
		{Content: "Unable to send the audio."},
	}}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	agent.Tools.Register(&countingHarnessTool{name: "send_tts", result: func(map[string]any) *tools.ToolResult {
		return &tools.ToolResult{
			ForLLM: "TTS audio sent", Media: []string{"media:audio"}, ResponseHandled: true,
		}
	}})
	opts := makeTestProcessOpts("failed-media-delivery")
	opts.Channel = "telegram"
	ts := newTurnState(agent, normalizeProcessOptions(opts), turnEventScope{
		turnID: "failed-media-delivery", context: newTurnContext(nil, nil, nil),
	})
	pipeline := NewPipeline(al)
	pipeline.ChannelManager = &rejectingMediaChannelManager{}
	result, err := al.runTurn(t.Context(), ts, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := agent.Sessions.(session.TurnCheckpointStore).GetTurnCheckpoint(ts.sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	items := checkpoint.ToolItems
	if len(items) != 1 || items[0].Status != toolItemStatusFailed ||
		!strings.Contains(items[0].ResultSummary, "channel rejected audio") {
		t.Fatalf("expected attachment failure in checkpoint, got %+v", items)
	}
	if !strings.Contains(result.finalContent, "Action verification notice") {
		t.Fatalf("expected failure notice, got %q", result.finalContent)
	}
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

func TestPublicExposureApprovesExecutionButNotReads(t *testing.T) {
	provider := &sequenceProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{
			{ID: "exec-1", Name: "exec", Arguments: map[string]any{"command": "printf safe"}},
			{ID: "read-1", Name: "read_file", Arguments: map[string]any{"path": "notes.md"}},
		}},
		{Content: "操作已完成。"},
	}}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	al.publicExposure = true

	execTool := &countingHarnessTool{name: "exec", result: func(map[string]any) *tools.ToolResult {
		return tools.SilentResult("safe")
	}}
	readTool := &countingHarnessTool{name: "read_file", result: func(map[string]any) *tools.ToolResult {
		return tools.SilentResult("notes")
	}}
	agent.Tools.Register(execTool)
	agent.Tools.Register(readTool)

	approvalRequests := make(chan approval.Request, 1)
	al.SetApprovalNotifier(func(_ context.Context, req approval.Request) error {
		approvalRequests <- req
		return nil
	})
	ts := newTurnState(agent, makeTestProcessOpts("public-exposure"), turnEventScope{
		turnID: "public-exposure", context: newTurnContext(nil, nil, nil),
	})

	done := make(chan error, 1)
	go func() {
		_, err := al.runTurn(context.Background(), ts, NewPipeline(al))
		done <- err
	}()

	var req approval.Request
	select {
	case req = <-approvalRequests:
	case <-time.After(3 * time.Second):
		t.Fatal("public exposure did not request approval for exec")
	}
	if req.Tool != "exec" || !strings.Contains(req.Reason, "public exposure") {
		t.Fatalf("unexpected approval request: %+v", req)
	}
	if err := al.RespondApproval(context.Background(), approval.Response{
		ID: req.ID, SessionKey: req.SessionKey, Approve: true,
	}); err != nil {
		t.Fatalf("RespondApproval: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if execTool.calls != 1 || readTool.calls != 1 {
		t.Fatalf("exec calls=%d, read calls=%d; want 1 and 1", execTool.calls, readTool.calls)
	}
	select {
	case extra := <-approvalRequests:
		t.Fatalf("read-only tool unexpectedly required approval: %+v", extra)
	default:
	}
}

func TestYOLOSkipsHumanApprovalForAnyChannel(t *testing.T) {
	for _, channel := range []string{"telegram", "pico", "discord"} {
		t.Run(channel, func(t *testing.T) {
			provider := &sequenceProvider{responses: []*providers.LLMResponse{
				{ToolCalls: []providers.ToolCall{{ID: "exec-1", Name: "exec", Arguments: map[string]any{"command": "printf safe"}}}},
				{Content: "done"},
			}}
			al, agent, cleanup := newTurnCoordTestLoop(t, provider)
			defer cleanup()
			al.cfg.Tools.Approval.Enabled = true
			al.cfg.Tools.Approval.YOLO = true
			al.publicExposure = true
			execTool := &countingHarnessTool{name: "exec", result: func(map[string]any) *tools.ToolResult { return tools.SilentResult("safe") }}
			agent.Tools.Register(execTool)
			al.SetApprovalNotifier(func(context.Context, approval.Request) error { t.Error("unexpected approval request"); return nil })
			opts := makeTestProcessOpts("yolo-" + channel)
			opts.Channel = channel
			ts := newTurnState(agent, opts, turnEventScope{turnID: "yolo-" + channel, context: newTurnContext(nil, nil, nil)})
			if _, err := al.runTurn(context.Background(), ts, NewPipeline(al)); err != nil {
				t.Fatal(err)
			}
			if execTool.calls != 1 {
				t.Fatalf("exec calls = %d, want 1", execTool.calls)
			}
		})
	}
}

type requiresHumanApprovalHook struct{}

func (h *requiresHumanApprovalHook) ApproveTool(context.Context, *ToolApprovalRequest) (ApprovalDecision, error) {
	return ApprovalDecision{
		Approved:      true,
		RequiresHuman: true,
		Reason:        "hook requires human review",
	}, nil
}

func TestYOLOSkipsHookAndPublicExposureApproval(t *testing.T) {
	provider := &sequenceProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "write-1", Name: "write_file", Arguments: map[string]any{"path": "notes.md", "content": "hello"}}}},
		{Content: "done"},
	}}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	al.cfg.Tools.Approval.YOLO = true
	al.publicExposure = true
	writeTool := &countingHarnessTool{name: "write_file", result: func(map[string]any) *tools.ToolResult { return tools.SilentResult("written") }}
	agent.Tools.Register(writeTool)
	if err := al.MountHook(NamedHook("require-human", &requiresHumanApprovalHook{})); err != nil {
		t.Fatal(err)
	}
	al.SetApprovalNotifier(func(context.Context, approval.Request) error {
		t.Error("unexpected approval request")
		return nil
	})
	ts := newTurnState(agent, makeTestProcessOpts("yolo-hook"), turnEventScope{
		turnID: "yolo-hook", context: newTurnContext(nil, nil, nil),
	})

	if _, err := al.runTurn(context.Background(), ts, NewPipeline(al)); err != nil {
		t.Fatal(err)
	}
	if writeTool.calls != 1 {
		t.Fatalf("write calls = %d, want 1", writeTool.calls)
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
	if want := ts.budget.Progress.RetrievalStopAfter; searchTool.calls != want {
		t.Fatalf("search calls=%d, want %d", searchTool.calls, want)
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

func TestCompatibilityPolicyAllowsKnownReadOnlyTools(t *testing.T) {
	for _, name := range []string{"web_fetch", "find_skills", "read_file", "web_search"} {
		policy := tools.PolicyFor(&countingHarnessTool{name: name})
		if !policy.ReadOnly || !policy.Idempotent {
			t.Fatalf("%s policy = %+v, want replay-safe read-only", name, policy)
		}
	}
	policy := tools.PolicyFor(&countingHarnessTool{name: "tool_search"})
	if !policy.ReadOnly || policy.Idempotent {
		t.Fatalf("tool_search policy = %+v, want read-only but stateful", policy)
	}
}

func TestParallelExecutionRejectsStatefulReadOnlyTools(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &sequenceProvider{})
	defer cleanup()
	agent.Tools.Register(&countingHarnessTool{name: "tool_search"})
	ts := newTurnState(agent, makeTestProcessOpts("stateful-read-only"), turnEventScope{
		turnID: "stateful-read-only", context: newTurnContext(nil, nil, nil),
	})
	calls := []providers.ToolCall{
		{ID: "search-1", Name: "tool_search", Arguments: map[string]any{"query": "calendar"}},
		{ID: "search-2", Name: "tool_search", Arguments: map[string]any{"query": "notes"}},
	}
	if canExecuteInParallel(al, al.cfg, ts, calls) {
		t.Fatal("stateful read-only tools must execute sequentially")
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
func (t *mockReadOnlySleepTool) Policy() tools.ToolPolicy {
	return tools.ReadPolicy()
}
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

func TestPublicExposureApprovalAlsoAppliesToParallelTools(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &sequenceProvider{})
	defer cleanup()
	agent.Tools.Register(&mockReadOnlySleepTool{name: "custom_reader"})
	ts := newTurnState(agent, makeTestProcessOpts("public-read-only"), turnEventScope{
		turnID: "public-read-only", context: newTurnContext(nil, nil, nil),
	})
	calls := []providers.ToolCall{
		{ID: "read-1", Name: "custom_reader", Arguments: map[string]any{"file": "notes"}},
		{ID: "read-2", Name: "custom_reader", Arguments: map[string]any{"file": "calendar"}},
	}
	if !canExecuteInParallel(al, al.cfg, ts, calls) {
		t.Fatal("trusted instance should allow parallel read-only tools")
	}
	al.publicExposure = true
	if canExecuteInParallel(al, al.cfg, ts, calls) {
		t.Fatal("public exposure approval must run before parallel execution")
	}
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
