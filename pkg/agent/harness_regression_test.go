package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/seahorse"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

func TestSummaryCommitPreservesConcurrentAppendAndRejectsReset(t *testing.T) {
	for _, backend := range []string{"json", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			var store session.SessionStore = session.NewSessionManager(t.TempDir())
			if backend == "jsonl" {
				raw, err := memory.NewSQLiteStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				store = session.NewStoreBackend(raw)
			}
			defer store.Close()
			for i := 0; i < 6; i++ {
				store.AddMessage("s", "user", fmt.Sprintf("message-%d", i))
			}
			snapshot := store.GetHistory("s")
			store.AddMessage("s", "user", "new message")
			commit := store.(session.SummaryCommitStore)
			ok, err := commit.CommitSummary("s", snapshot[:2], "", "summary")
			if err != nil || !ok {
				t.Fatalf("commit: %v %v", ok, err)
			}
			got := store.GetHistory("s")
			if len(got) != 5 || got[0].Content != "message-2" || got[4].Content != "new message" {
				t.Fatalf("lost unsummarized messages: %+v", got)
			}
			store.SetHistory("s", []providers.Message{{Role: "user", Content: "reset"}})
			ok, err = commit.CommitSummary("s", got[:2], "summary", "stale summary")
			if err != nil || ok || store.GetSummary("s") != "summary" {
				t.Fatalf("stale summary accepted: %v %v", ok, err)
			}
			checkpoints := store.(session.TurnCheckpointStore)
			if err := checkpoints.SetTurnCheckpoint("s", session.TurnCheckpoint{TurnID: "one", Usage: &bus.ContextUsage{TaskInputTokens: 100}}); err != nil {
				t.Fatal(err)
			}
			if err := store.(session.TurnUsageStore).UpdateTurnUsage("s", "one", &bus.ContextUsage{TaskInputTokens: 150, SummaryInputTokens: 50}); err != nil {
				t.Fatal(err)
			}
			if err := checkpoints.SetTurnCheckpoint("s", session.TurnCheckpoint{TurnID: "one", Usage: &bus.ContextUsage{TaskInputTokens: 100}}); err != nil {
				t.Fatal(err)
			}
			checkpoint, err := checkpoints.GetTurnCheckpoint("s")
			if err != nil || checkpoint.Usage.TaskInputTokens != 150 || checkpoint.Usage.SummaryInputTokens != 50 {
				t.Fatalf("lost concurrent usage: %+v, %v", checkpoint, err)
			}
			if err := checkpoints.SetTurnCheckpoint("s", session.TurnCheckpoint{TurnID: "two"}); err != nil {
				t.Fatal(err)
			}
			if err := store.(session.TurnUsageStore).UpdateTurnUsage("s", "one", &bus.ContextUsage{TaskInputTokens: 180}); err != nil {
				t.Fatal(err)
			}
			checkpoint, err = checkpoints.GetTurnCheckpoint("s")
			if err != nil || checkpoint.TurnID != "two" || checkpoint.Usage != nil {
				t.Fatalf("old summary overwrote new turn: %+v", checkpoint)
			}
		})
	}
}

func TestBackgroundSummaryRetainsExistingContextAndToolEvidence(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var prompt string
	provider := &seahorseTestProvider{chatFn: func(_ context.Context, m []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
		prompt = m[1].Content
		close(entered)
		<-release
		return &providers.LLMResponse{Content: "summary"}, nil
	}}
	al, a, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	history := []providers.Message{{Role: "user", Content: "q1"}, {Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "c", Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"important.txt"}`}}}}, {Role: "tool", ToolCallID: "c", Content: "actual failure"}, {Role: "assistant", Content: "a1"}, {Role: "user", Content: "q2"}, {Role: "assistant", Content: "a2"}, {Role: "user", Content: "q3"}, {Role: "assistant", Content: "a3"}}
	a.Sessions.SetHistory("s", history)
	a.Sessions.SetSummary("s", "old constraint")
	done := make(chan struct{})
	go func() { defer close(done); (&legacyContextManager{al: al}).summarizeSession(a, "s") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("summary did not start")
	}
	a.Sessions.AddMessage("s", "user", "q4")
	close(release)
	<-done
	for _, text := range []string{"old constraint", "actual failure", "important.txt"} {
		if !strings.Contains(prompt, text) {
			t.Errorf("summary lost %q", text)
		}
	}
	got := a.Sessions.GetHistory("s")
	if len(got) != 5 || got[0].Content != "q2" || got[4].Content != "q4" {
		t.Fatalf("unexpected history: %+v", got)
	}
}

func TestSeahorseProviderMessageStorageRoundTrip(t *testing.T) {
	e, err := seahorse.NewEngine(seahorse.Config{DBPath: filepath.Join(t.TempDir(), "context.db")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	want := providers.Message{ID: "stable", Role: "assistant", Content: "Only inspect; do not modify.", Attachments: []providers.Attachment{{URL: "file-url", Filename: "result.txt", Type: "file"}}, ToolCalls: []providers.ToolCall{{ID: "c", Name: "read_file", Arguments: map[string]any{"path": "a.txt"}, ThoughtSignature: "signature", ExtraContent: &providers.ExtraContent{Google: &providers.GoogleExtra{ThoughtSignature: "signature"}}}}}
	_, err = e.Ingest(context.Background(), "s", []seahorse.Message{providerToSeahorseMessage(want)})
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Assemble(context.Background(), "s", seahorse.AssembleInput{Budget: 10000})
	if err != nil {
		t.Fatal(err)
	}
	got := seahorseToProviderMessages(r)[0]
	if got.ID != want.ID || got.Content != want.Content || len(got.Attachments) != 1 || got.Attachments[0] != want.Attachments[0] || got.ToolCalls[0].Function.ThoughtSignature != "signature" || got.ToolCalls[0].ExtraContent.Google.ThoughtSignature != "signature" {
		t.Fatalf("lossy round trip: %+v", got)
	}
}

func TestHistoricalPruningBoundsSingleLineUTF8(t *testing.T) {
	for _, input := range []string{strings.Repeat("x", 10000), strings.Repeat("中文", 3000) + "[file:/tmp/output.txt]"} {
		got := pruneHistoricalToolContent(input, 800)
		if len(got) > 800 || !utf8.ValidString(got) {
			t.Fatalf("invalid pruning: bytes=%d", len(got))
		}
		if strings.Contains(input, "[file:") && !strings.Contains(got, "[file:/tmp/output.txt]") {
			t.Fatal("artifact pointer lost")
		}
	}
}

func TestHistoricalPruningKeepsRecoverableArtifact(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	content := strings.Repeat("one-line-output", 1000)
	input := []providers.Message{{Role: "tool", ToolCallID: "c", Content: content}}
	got := cb.pruneHistoricalToolOutputs(input)
	if len(got[0].Content) > 800 || !strings.Contains(got[0].Content, "[file:") {
		t.Fatalf("no bounded pointer: %s", got[0].Content)
	}
	start := strings.Index(got[0].Content, "[file:") + 6
	path := strings.SplitN(got[0].Content[start:], "]", 2)[0]
	data, err := os.ReadFile(path)
	if err != nil || string(data) != content {
		t.Fatalf("original output unavailable: %v", err)
	}
	if input[0].Content != content {
		t.Fatal("stored history was mutated")
	}
}

func TestActiveSummaryKeepsRecentToolEvidenceAndUsesWindow(t *testing.T) {
	calls := 0
	p := &seahorseTestProvider{chatFn: func(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
		calls++
		return &providers.LLMResponse{Content: "Older work completed."}, nil
	}}
	messages := []providers.Message{{Role: "system", Content: "system"}, {Role: "user", Content: "task"}, {Role: "assistant", Content: strings.Repeat("completed work ", 3000)}, {Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "recent", Name: "read_file"}}}, {Role: "tool", ToolCallID: "recent", Content: "exact recent evidence"}}
	out, err := compactActiveTurn(context.Background(), p, "test", messages, 1, 16000, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls > 2 {
		t.Fatalf("unnecessarily fragmented summary: %d calls", calls)
	}
	if out[len(out)-1].Content != "exact recent evidence" || out[len(out)-2].ToolCalls[0].ID != "recent" {
		t.Fatalf("recent tool group lost: %+v", out)
	}
}

type boundedReadTool struct {
	active, peak atomic.Int32
	once         sync.Once
	steer        func()
	t            *testing.T
}

func (*boundedReadTool) Name() string               { return "read_file" }
func (*boundedReadTool) Description() string        { return "read" }
func (*boundedReadTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (tool *boundedReadTool) Execute(ctx context.Context, _ map[string]any) *tools.ToolResult {
	n := tool.active.Add(1)
	defer tool.active.Add(-1)
	for old := tool.peak.Load(); n > old; old = tool.peak.Load() {
		if tool.peak.CompareAndSwap(old, n) {
			break
		}
	}
	turn := turnStateFromContext(ctx)
	if len(turn.activitiesSnapshot()) != 9 {
		tool.t.Error("tool ran before activities were registered")
	}
	tool.once.Do(tool.steer)
	time.Sleep(10 * time.Millisecond)
	return tools.SilentResult("actual result")
}

func TestParallelReadsBoundConcurrencyAndRetainSteeredResults(t *testing.T) {
	var calls []providers.ToolCall
	for i := 0; i < 9; i++ {
		calls = append(calls, providers.ToolCall{ID: fmt.Sprint(i), Name: "read_file", Arguments: map[string]any{"path": fmt.Sprintf("file-%d.txt", i)}})
	}
	p := &sequenceProvider{responses: []*providers.LLMResponse{{ToolCalls: calls}, {Content: "done"}}}
	al, a, cleanup := newTurnCoordTestLoop(t, p)
	defer cleanup()
	tool := &boundedReadTool{t: t, steer: func() {
		if err := al.enqueueSteeringMessage("s", a.ID, providers.Message{ID: "client-steering", Role: "user", Content: "please continue"}); err != nil {
			t.Error(err)
		}
	}}
	a.Tools.Register(tool)
	opts := makeTestProcessOpts("s")
	opts.Dispatch = DispatchRequest{SessionKey: "s", UserMessage: "task"}
	ts := newTurnState(a, opts, turnEventScope{turnID: "parallel", context: newTurnContext(nil, nil, nil)})
	_, err := al.runTurn(context.Background(), ts, NewPipeline(al))
	if err != nil {
		t.Fatal(err)
	}
	if peak := tool.peak.Load(); peak < 2 || peak > 4 {
		t.Fatalf("parallelism=%d", peak)
	}
	results := 0
	steeringFound := false
	for _, msg := range a.Sessions.GetHistory(ts.sessionKey) {
		if msg.ID == "client-steering" && msg.Content == "please continue" {
			steeringFound = true
		}
		if msg.Role == "tool" {
			results++
			if msg.Content != "actual result" {
				t.Errorf("completed result replaced: %s", msg.Content)
			}
		}
	}
	if results != 9 {
		t.Fatalf("results=%d", results)
	}
	if !steeringFound {
		t.Fatal("steering message lost its client identity")
	}
}

func TestTurnUsageAccumulatesCallsAndSummarySeparately(t *testing.T) {
	ts := &turnState{agent: &AgentInstance{ContextWindow: 10000, MaxTokens: 1000}}
	ts.recordRequestContext([]providers.Message{{Role: "user", Content: "task"}}, nil)
	ts.recordUsage(&providers.UsageInfo{PromptTokens: 100, CompletionTokens: 20}, false)
	ts.recordUsage(&providers.UsageInfo{PromptTokens: 50, CompletionTokens: 10}, true)
	ts.recordUsage(&providers.UsageInfo{PromptTokens: 200, CompletionTokens: 30}, false)
	u := ts.contextUsageSnapshot()
	if u.UsedTokens != 200 || u.TaskInputTokens != 350 || u.TaskOutputTokens != 60 || u.SummaryInputTokens != 50 || u.SummaryOutputTokens != 10 {
		t.Fatalf("bad usage: %+v", u)
	}
	child := &turnState{parentTurnState: ts}
	child.recordUsage(&providers.UsageInfo{PromptTokens: 40, CompletionTokens: 5}, false)
	u = ts.contextUsageSnapshot()
	if u.TaskInputTokens != 390 || u.TaskOutputTokens != 65 || u.UsedTokens != 200 {
		t.Fatalf("child usage replaced parent occupancy: %+v", u)
	}
}

func TestStreamingFallbackBeforeVisibleOutputKeepsCandidateChain(t *testing.T) {
	cfg := newConfiguredStreamingTestConfig(t, true, true, []string{"fallback-model"})
	provider := &configuredStreamingProvider{streamPlan: []configuredStreamingCall{{err: errors.New("unavailable")}}}
	b := bus.NewMessageBus()
	b.SetStreamDelegate(configuredStreamingDelegate{streamer: &recordingStreamer{}})
	al := NewAgentLoop(cfg, b, provider)
	defer al.Close()
	runConfiguredStreamingTurn(t, al, "pico")
	if provider.streamCalls != 1 || provider.chatCalls != 1 {
		t.Fatalf("stream=%d chat=%d", provider.streamCalls, provider.chatCalls)
	}
	select {
	case outbound := <-b.OutboundChan():
		if outbound.Content != "chat response" {
			t.Fatalf("wrong fallback delivery: %s", outbound.Content)
		}
	default:
		t.Fatal("candidate fallback returned a response but did not deliver it")
	}

}
