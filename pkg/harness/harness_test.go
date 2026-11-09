package harness

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

func TestTrajectoryToolOrderAndFinalAnswer(t *testing.T) {
	tool := TextTool("read_file", "file contents")
	result, err := Run(context.Background(), Scenario{
		Name:        "tool-order",
		UserMessage: "read the file",
		Steps: []ModelStep{
			Calls(ToolCall("call-1", "read_file", map[string]any{"path": "a.txt"})),
			Text("finished"),
		},
		Tools: []tools.Tool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		Final: "finished",
		Tools: []string{"read_file"},
		ToolCalls: []ToolCallExpectation{{
			CallID:         "call-1",
			Name:           "read_file",
			Arguments:      map[string]any{"path": "a.txt"},
			Status:         "completed",
			ResultContains: "file contents",
		}},
		ModelCalls:    2,
		TurnStatus:    agent.TurnEndStatusCompleted,
		Phases:        []agent.TurnPhase{agent.TurnPhaseCreated, agent.TurnPhaseSetup, agent.TurnPhaseRunning, agent.TurnPhaseTools, agent.TurnPhaseRunning, agent.TurnPhaseFinalizing, agent.TurnPhaseCompleted},
		Events:        []runtimeevents.Kind{runtimeevents.KindAgentToolExecStart, runtimeevents.KindAgentToolExecEnd},
		EventSequence: []runtimeevents.Kind{runtimeevents.KindAgentTurnStart, runtimeevents.KindAgentToolExecStart, runtimeevents.KindAgentTurnEnd},
	}); err != nil {
		t.Fatal(err)
	}
	if len(tool.Calls()) != 1 || tool.Calls()[0].Arguments["path"] != "a.txt" {
		t.Fatalf("unexpected tool calls: %+v", tool.Calls())
	}
}

func TestToolPanicFaultIsRecovered(t *testing.T) {
	result, err := Run(context.Background(), Scenario{
		Name:        "tool-panic",
		UserMessage: "run the dangerous tool",
		Steps: []ModelStep{
			Calls(ToolCall("call-1", "explode", nil)),
			Text("recovered"),
		},
		Tools: []tools.Tool{PanicTool("explode", "boom")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		Final:      "recovered",
		Tools:      []string{"explode"},
		ModelCalls: 2,
		TurnStatus: agent.TurnEndStatusCompleted,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestToolErrorLoopStopsAfterRepeatedFailures(t *testing.T) {
	steps := make([]ModelStep, 0, 5)
	for i := 0; i < 4; i++ {
		steps = append(steps, Calls(ToolCall(string(rune('a'+i)), "flaky", nil)))
	}
	steps = append(steps, Text("the tool is unavailable"))

	result, err := Run(context.Background(), Scenario{
		Name:        "tool-errors",
		UserMessage: "use the flaky tool",
		Steps:       steps,
		Tools:       []tools.Tool{ErrorTool("flaky", "network timeout")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		Final:      "the tool is unavailable",
		Tools:      []string{"flaky", "flaky", "flaky", "flaky"},
		ModelCalls: 5,
		TurnStatus: agent.TurnEndStatusCompleted,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderFaultFailsTurn(t *testing.T) {
	result, err := Run(context.Background(), Scenario{
		Name:        "provider-fault",
		UserMessage: "hello",
		Steps:       []ModelStep{Fail(errors.New("provider unavailable"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		ErrorContains: "provider unavailable",
		ModelCalls:    1,
		TurnStatus:    agent.TurnEndStatusError,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderWaitForContextFault(t *testing.T) {
	result, err := Run(context.Background(), Scenario{
		Name:        "provider-timeout",
		UserMessage: "wait forever",
		Timeout:     150 * time.Millisecond,
		Steps:       []ModelStep{{WaitForContext: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		ErrorContains: "deadline exceeded",
		ModelCalls:    1,
		TurnStatus:    agent.TurnEndStatusError,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyReportsTrajectoryMismatch(t *testing.T) {
	result, err := Run(context.Background(), Scenario{
		Name:        "mismatch",
		UserMessage: "hello",
		Steps:       []ModelStep{Text("done")},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = result.Verify(Expect{Tools: []string{"never_called"}, Final: "wrong"})
	if err == nil || !strings.Contains(err.Error(), "tool sequence") || !strings.Contains(err.Error(), "final content") {
		t.Fatalf("expected trajectory mismatch, got %v", err)
	}
}
