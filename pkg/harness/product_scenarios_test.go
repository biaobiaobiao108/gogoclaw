package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

func TestProductFlowWriteFileProducesExpectedArtifact(t *testing.T) {
	workspace := t.TempDir()
	tool := tools.NewWriteFileTool(workspace, true)
	result, err := Run(context.Background(), Scenario{
		Name:              "product-write-file-artifact",
		UserMessage:       "Create the requested report.",
		Workspace:         workspace,
		ProductionIngress: true,
		Steps: []ModelStep{
			Calls(ToolCall("artifact-write", "write_file", map[string]any{
				"path":    "artifacts/result.txt",
				"content": "total: 42\n",
			})),
			Text("artifact saved"),
		},
		Tools: []tools.Tool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		NoError:          true,
		Tools:            []string{"write_file"},
		OutboundContains: []string{"artifact saved"},
		ToolCalls: []ToolCallExpectation{{
			CallID: "artifact-write",
			Name:   "write_file",
			Status: "completed",
			Arguments: map[string]any{
				"path": "artifacts/result.txt",
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(workspace, "artifacts", "result.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "total: 42\n" {
		t.Fatalf("artifact = %q, want exact content %q", contents, "total: 42\n")
	}
}

func TestProductFlowSQLiteSessionSurvivesAgentLoopRestart(t *testing.T) {
	workspace := t.TempDir()
	sessionDir := filepath.Join(workspace, "sessions")
	const sessionKey = "agent:main:product-restart"

	firstStore, err := memory.NewSQLiteStore(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	firstBackend := session.NewStoreBackend(firstStore)
	first, err := Run(context.Background(), Scenario{
		Name:        "product-session-restart",
		SessionKey:  sessionKey,
		Workspace:   workspace,
		Session:     firstBackend,
		UserMessage: "Remember the project code amber-2851 for my next message.",
		Steps:       []ModelStep{Text("I will remember amber-2851.")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Verify(Expect{NoError: true, HistoryContains: []string{"amber-2851"}}); err != nil {
		t.Fatal(err)
	}
	if err := firstBackend.Close(); err != nil {
		t.Fatal(err)
	}

	secondStore, err := memory.NewSQLiteStore(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	secondBackend := session.NewStoreBackend(secondStore)
	t.Cleanup(func() { _ = secondBackend.Close() })
	second, err := Run(context.Background(), Scenario{
		Name:        "product-session-restart",
		SessionKey:  sessionKey,
		Workspace:   workspace,
		Session:     secondBackend,
		UserMessage: "What project code did I ask you to remember?",
		Steps:       []ModelStep{Text("amber-2851")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Verify(Expect{
		NoError:         true,
		FinalExact:      stringPointer("amber-2851"),
		HistoryContains: []string{"amber-2851"},
		ModelDetails:    []ModelCallExpectation{{Contains: []string{"amber-2851"}}},
	}); err != nil {
		t.Fatal(err)
	}
}

// Exercise multiple independent sessions against the same store across loop restarts.
func TestProductFlowConcurrentSessionsSurviveRestarts(t *testing.T) {
	workspace := t.TempDir()
	sessionDir := filepath.Join(workspace, "sessions")
	const sessions = 8
	rounds := 3
	if value := os.Getenv("GOGOCLAW_HARNESS_RESTART_ROUNDS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100000 {
			t.Fatal("GOGOCLAW_HARNESS_RESTART_ROUNDS must be an integer between 1 and 100000")
		}
		rounds = parsed
	}
	started := time.Now()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	t.Logf("restart stress: sessions=%d rounds=%d initial_goroutines=%d initial_heap_bytes=%d", sessions, rounds, runtime.NumGoroutine(), before.HeapAlloc)
	defer func() {
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		t.Logf("restart stress complete: elapsed=%s goroutines=%d heap_bytes=%d", time.Since(started), runtime.NumGoroutine(), after.HeapAlloc)
	}()

	for round := 0; round < rounds; round++ {
		store, err := memory.NewSQLiteStore(sessionDir)
		if err != nil {
			t.Fatal(err)
		}
		backend := session.NewStoreBackend(store)
		result, runErr := Run(context.Background(), Scenario{
			Name: "concurrent-restart", Workspace: workspace, Session: backend,
			UserMessage: "main", Provider: &restartEchoProvider{},
			ConcurrentSetup: func(runtime ToolRuntime) error {
				var wg sync.WaitGroup
				errs := make(chan error, sessions)
				for i := 0; i < sessions; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						key := fmt.Sprintf("agent:main:restart-load-%d", i)
						message := fmt.Sprintf("session-%d-round-%d", i, round)
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						answer, err := runtime.Loop.ProcessDirectWithChannel(ctx, message, key, "harness", key)
						if err == nil && answer != "echo "+message {
							err = fmt.Errorf("%s: unexpected answer %q", key, answer)
						}
						errs <- err
					}(i)
				}
				wg.Wait()
				close(errs)
				for err := range errs {
					if err != nil {
						return err
					}
				}
				return nil
			},
		})
		closeErr := backend.Close()
		if runErr != nil {
			t.Fatal(runErr)
		}
		if err := result.Verify(Expect{NoError: true}); err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if rounds > 3 && (round+1)%10 == 0 {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			t.Logf("restart stress: round=%d/%d elapsed=%s goroutines=%d heap_bytes=%d", round+1, rounds, time.Since(started), runtime.NumGoroutine(), stats.HeapAlloc)
		}
	}

	store, err := memory.NewSQLiteStore(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	backend := session.NewStoreBackend(store)
	t.Cleanup(func() { _ = backend.Close() })
	for i := 0; i < sessions; i++ {
		key := fmt.Sprintf("agent:main:restart-load-%d", i)
		history, err := backend.GetTranscriptChecked(key)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != rounds*2 {
			t.Fatalf("%s: got %d messages, want %d", key, len(history), rounds*2)
		}
		for round := 0; round < rounds; round++ {
			message := fmt.Sprintf("session-%d-round-%d", i, round)
			if history[round*2].Role != "user" || history[round*2].Content != message ||
				history[round*2+1].Role != "assistant" || history[round*2+1].Content != "echo "+message {
				t.Fatalf("%s round %d: unexpected history %+v", key, round, history[round*2:round*2+2])
			}
		}
	}
}

type restartEchoProvider struct{}

func (*restartEchoProvider) GetDefaultModel() string { return "harness-model" }

func (*restartEchoProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "echo " + messages[len(messages)-1].Content}, nil
}

func TestProductFlowSpawnDeliversAsyncResultToFollowUp(t *testing.T) {
	const sessionKey = "agent:main:product-async-spawn"
	provider := &spawnFlowProvider{parentSessionKey: sessionKey}
	manager := tools.NewSubagentManager(provider, "harness-model", t.TempDir())
	spawn := tools.NewSpawnTool(manager)
	result, err := Run(context.Background(), Scenario{
		Name:             "product-spawn-async-result",
		SessionKey:       sessionKey,
		UserMessage:      "Start a background task and report its result when it finishes.",
		AsyncSettle:      100 * time.Millisecond,
		ProcessFollowUps: true,
		Timeout:          5 * time.Second,
		Provider:         provider,
		Tools:            []tools.Tool{spawn},
		Setup: func(loop *agent.AgentLoop) error {
			provider.loop = loop
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		NoError:          true,
		TurnCount:        2,
		Tools:            []string{"spawn"},
		AsyncFollowUps:   1,
		FollowUpContains: []string{"async-result-731"},
		FinalContains:    []string{"async-result-731"},
	}); err != nil {
		t.Fatal(err)
	}
}

func stringPointer(value string) *string { return &value }

type spawnFlowProvider struct {
	mu               sync.Mutex
	parentCalls      int
	parentSessionKey string
	loop             *agent.AgentLoop
}

func (*spawnFlowProvider) GetDefaultModel() string { return "harness-model" }

func (p *spawnFlowProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	child := false
	followUp := false
	for _, message := range messages {
		if strings.Contains(message.Content, "You are a spawned subagent running in the background") {
			child = true
		}
		if strings.Contains(message.Content, "async-result-731") {
			followUp = true
		}
	}
	if child {
		for p.loop != nil && p.loop.GetActiveTurnBySession(p.parentSessionKey) != nil {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
		return &providers.LLMResponse{Content: "async-result-731"}, nil
	}
	if followUp {
		return &providers.LLMResponse{Content: "Reported async-result-731"}, nil
	}

	p.mu.Lock()
	p.parentCalls++
	call := p.parentCalls
	p.mu.Unlock()
	switch call {
	case 1:
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{ToolCall(
			"spawn-731",
			"spawn",
			map[string]any{"task": "Return a one-word completion token after the parent turn completes."},
		)}}, nil
	case 2:
		return &providers.LLMResponse{Content: "The background task has started."}, nil
	default:
		return nil, errors.New("unexpected parent model call")
	}
}
