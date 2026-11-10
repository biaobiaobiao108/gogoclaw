package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
