package harness

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

func TestAsyncToolResultDeliveredExactlyOnce(t *testing.T) {
	tool := NewInlineAsyncTool("background_lookup", func(
		_ context.Context,
		_ map[string]any,
		_ int,
		complete func(*tools.ToolResult),
	) {
		complete(tools.SilentResult("background payload"))
	})
	result, err := Run(context.Background(), Scenario{
		Name:        "async-live-delivery",
		UserMessage: "start background lookup",
		Steps: []ModelStep{
			Calls(ToolCall("call-async", "background_lookup", nil)),
			Text("finished"),
		},
		Tools: []tools.Tool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		Final:      "finished",
		ModelCalls: 2,
		Tools:      []string{"background_lookup"},
		TurnStatus: agent.TurnEndStatusCompleted,
		EventDetails: []EventExpectation{{
			Kind:            runtimeevents.KindAgentAsyncResultDelivered,
			PayloadContains: []string{"background_lookup"},
		}},
		ModelDetails: []ModelCallExpectation{
			{Contains: []string{"start background lookup"}},
			{Contains: []string{"[Background task background_lookup completed] background payload"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if len(tool.Calls()) != 1 || tool.Calls()[0].CallID != "call-async" {
		t.Fatalf("unexpected async tool calls: %+v", tool.Calls())
	}
}

func TestLateAsyncResultBecomesFollowUp(t *testing.T) {
	result, err := Run(context.Background(), Scenario{
		Name:        "async-follow-up",
		UserMessage: "start late work",
		Steps: []ModelStep{
			Calls(ToolCall("call-late", "late_work", nil)),
			Text("first turn done"),
		},
		ToolFactory: afterTurnAsyncToolFactory("late_work", "late payload"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		Final:            "first turn done",
		AsyncFollowUps:   1,
		FollowUpContains: []string{"late payload"},
		Events:           []runtimeevents.Kind{runtimeevents.KindAgentFollowUpQueued},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncFollowUpStartsNextTurn(t *testing.T) {
	result, err := Run(context.Background(), Scenario{
		Name:             "async-follow-up-turn",
		UserMessage:      "start work",
		ProcessFollowUps: true,
		Steps: []ModelStep{
			Calls(ToolCall("call-follow-up", "late_work", nil)),
			Text("initial answer"),
			Text("follow-up answer"),
		},
		ToolFactory: afterTurnAsyncToolFactory("late_work", "later result"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		Final:          "follow-up answer",
		TurnCount:      2,
		ModelCalls:     3,
		AsyncFollowUps: 1,
	}); err != nil {
		t.Fatal(err)
	}
}

func afterTurnAsyncToolFactory(name, content string) func(ToolRuntime) ([]tools.Tool, error) {
	return func(runtime ToolRuntime) ([]tools.Tool, error) {
		tool := NewAsyncToolStub(name, func(
			_ context.Context,
			_ map[string]any,
			_ int,
			complete func(*tools.ToolResult),
		) {
			for runtime.Loop.GetActiveTurnBySession(runtime.SessionKey) != nil {
				time.Sleep(time.Millisecond)
			}
			complete(tools.SilentResult(content))
		})
		return []tools.Tool{tool}, nil
	}
}

func TestHardAbortAsyncResultBecomesOrphan(t *testing.T) {
	result, err := Run(context.Background(), Scenario{
		Name:        "async-hard-abort",
		UserMessage: "abort this work",
		Steps: []ModelStep{
			Calls(ToolCall("call-abort", "abort_work", nil)),
			Text("unused"),
		},
		Tools:       []tools.Tool{NewAbortAsyncTool("abort_work", "orphan payload", 30*time.Millisecond)},
		AsyncSettle: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		TurnStatus: agent.TurnEndStatusAborted,
		EventDetails: []EventExpectation{{
			Kind:            runtimeevents.KindAgentError,
			PayloadContains: []string{"turn_hard_aborted"},
		}},
	}); err != nil {
		for _, event := range result.Events {
			t.Logf("event=%s payload=%+v", event.Kind, event.Payload)
		}
		t.Fatal(err)
	}
}

func TestAsyncQueueOverflowFallsBackToFollowUps(t *testing.T) {
	const count = 20
	calls := make([]providers.ToolCall, 0, count)
	for i := 0; i < count; i++ {
		calls = append(calls, ToolCall("overflow-"+string(rune('a'+i)), "burst", nil))
	}
	result, err := Run(context.Background(), Scenario{
		Name:        "async-queue-overflow",
		UserMessage: "run a burst",
		Steps: []ModelStep{
			Calls(calls...),
			Text("burst complete"),
		},
		Tools: []tools.Tool{NewInlineAsyncTool("burst", func(
			_ context.Context,
			_ map[string]any,
			_ int,
			complete func(*tools.ToolResult),
		) {
			complete(tools.SilentResult("burst result"))
		})},
		AsyncSettle:   100 * time.Millisecond,
		ContextWindow: 1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		Final:             "burst complete",
		MinAsyncFollowUps: 4,
		ModelCalls:        2,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderPanicProducesFailedTrace(t *testing.T) {
	result, err := Run(context.Background(), Scenario{
		Name:        "provider-panic",
		UserMessage: "panic",
		Steps:       []ModelStep{Panic("provider exploded")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		ErrorContains: "provider exploded",
		TurnStatus:    agent.TurnEndStatusError,
		ModelCalls:    1,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCheckpointFaultFailsTurn(t *testing.T) {
	fault := NewSessionFault().Fail(SessionSetCheckpoint, 1, errors.New("checkpoint unavailable"))
	result, err := Run(context.Background(), Scenario{
		Name:         "session-fault",
		UserMessage:  "do work",
		Steps:        []ModelStep{Text("unused")},
		SessionFault: fault,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		ErrorContains: "checkpoint unavailable",
		TurnStatus:    agent.TurnEndStatusError,
	}); err != nil {
		t.Fatal(err)
	}
	if fault.Calls(SessionSetCheckpoint) == 0 {
		t.Fatal("checkpoint fault was not exercised")
	}
}

func TestClosedBusFollowUpPublishesErrorEvent(t *testing.T) {
	result, err := Run(context.Background(), Scenario{
		Name:        "closed-bus",
		UserMessage: "start work",
		Steps: []ModelStep{
			Calls(ToolCall("call-bus", "late_work", nil)),
			Text("done"),
		},
		ToolFactory: func(runtime ToolRuntime) ([]tools.Tool, error) {
			tool := NewAsyncToolStub("late_work", func(
				_ context.Context,
				_ map[string]any,
				_ int,
				complete func(*tools.ToolResult),
			) {
				for runtime.Loop.GetActiveTurnBySession(runtime.SessionKey) != nil {
					time.Sleep(time.Millisecond)
				}
				runtime.Bus.Close()
				complete(tools.SilentResult("late result"))
			})
			return []tools.Tool{tool}, nil
		},
		AsyncSettle: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		EventDetails: []EventExpectation{{
			Kind:            runtimeevents.KindAgentError,
			PayloadContains: []string{"follow_up_publish_failed"},
		}},
	}); err != nil {
		for _, event := range result.Events {
			t.Logf("event=%s payload=%+v", event.Kind, event.Payload)
		}
		t.Fatal(err)
	}
}

func TestMultiTurnSessionContinuity(t *testing.T) {
	result, err := Run(context.Background(), Scenario{
		Name:        "multi-turn",
		UserMessage: "first question",
		Messages:    []string{"second question"},
		Steps:       []ModelStep{Text("first answer"), Text("second answer")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		Final:      "second answer",
		TurnCount:  2,
		ModelCalls: 2,
		ModelDetails: []ModelCallExpectation{
			{Contains: []string{"first question"}},
			{Contains: []string{"first question", "first answer", "second question"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTracePersistenceAndGoldenComparison(t *testing.T) {
	result, err := Run(context.Background(), Scenario{
		Name:        "trace-round-trip",
		UserMessage: "hello",
		Steps:       []ModelStep{Text("world")},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "trace.json")
	if err := result.SaveTrace(path); err != nil {
		t.Fatal(err)
	}
	want, err := LoadTrace(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := CompareTrace(result.Snapshot(), want); err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{GoldenTrace: path}); err != nil {
		t.Fatal(err)
	}
	want.Content = "different"
	if err := CompareTrace(result.Snapshot(), want); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected trace mismatch, got %v", err)
	}
}

func TestProviderRecordAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider.json")
	first, err := Run(context.Background(), Scenario{
		Name:        "record-provider",
		UserMessage: "record me",
		RecordPath:  path,
		Steps:       []ModelStep{Text("recorded answer")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Verify(Expect{Final: "recorded answer"}); err != nil {
		t.Fatal(err)
	}
	replayed, err := Run(context.Background(), Scenario{
		Name:        "replay-provider",
		UserMessage: "record me",
		ReplayPath:  path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := replayed.Verify(Expect{Final: "recorded answer"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunSuiteProducesStructuredReport(t *testing.T) {
	report := RunSuite(context.Background(), []Scenario{
		{
			Name:        "suite-pass",
			UserMessage: "hello",
			Steps:       []ModelStep{Text("ok")},
			Expectation: &Expect{Final: "ok"},
		},
		{
			Name:        "suite-fail",
			UserMessage: "hello",
			Steps:       []ModelStep{Text("actual")},
			Expectation: &Expect{Final: "expected"},
		},
	})
	if report.Passed {
		t.Fatal("expected suite failure")
	}
	if len(report.Scenarios) != 2 || report.Scenarios[0].Error != "" || report.Scenarios[1].Error == "" {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestSteeringInjectedMidTurnViaHarness(t *testing.T) {
	tool := TextTool("search", "search result")
	result, err := Run(context.Background(), Scenario{
		Name:        "steering-mid-turn",
		UserMessage: "initial prompt",
		Steps: []ModelStep{
			SteerOnCall("change task to focus on logs", Calls(ToolCall("call-s1", "search", nil))),
			Text("handled redirected task"),
		},
		Tools: []tools.Tool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		Final:      "handled redirected task",
		ModelCalls: 2,
		TurnStatus: agent.TurnEndStatusCompleted,
		EventDetails: []EventExpectation{{
			Kind: runtimeevents.KindAgentSteeringInjected,
		}},
		HistoryContains: []string{"change task to focus on logs"},
		ModelDetails: []ModelCallExpectation{
			{Contains: []string{"initial prompt"}},
			{Contains: []string{"change task to focus on logs"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGracefulInterruptMidTurnViaHarness(t *testing.T) {
	tool := TextTool("long_search", "search result")
	result, err := Run(context.Background(), Scenario{
		Name:        "interrupt-mid-turn",
		UserMessage: "start work",
		Steps: []ModelStep{
			InterruptOnCall("stop immediately after search", Calls(ToolCall("call-i1", "long_search", nil))),
			Text("graceful summary"),
		},
		Tools: []tools.Tool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{
		Final:      "graceful summary",
		TurnStatus: agent.TurnEndStatusCompleted,
		TurnReason: agent.TurnEndReasonUserInterrupted,
		EventDetails: []EventExpectation{{
			Kind:            runtimeevents.KindAgentInterruptReceived,
			PayloadContains: []string{"graceful"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestParallelSuiteWithOptions(t *testing.T) {
	scenarios := []Scenario{
		{
			Name:        "par-1",
			UserMessage: "hello 1",
			Steps:       []ModelStep{Text("ans 1")},
			Expectation: &Expect{Final: "ans 1"},
		},
		{
			Name:        "par-2",
			UserMessage: "hello 2",
			Steps:       []ModelStep{Text("ans 2")},
			Expectation: &Expect{Final: "ans 2"},
		},
		{
			Name:        "par-3",
			UserMessage: "hello 3",
			Steps:       []ModelStep{Text("ans 3")},
			Expectation: &Expect{Final: "ans 3"},
		},
	}
	report := RunSuiteWithOptions(context.Background(), scenarios, SuiteOptions{
		Parallel:    true,
		Concurrency: 3,
	})
	if !report.Passed || len(report.Scenarios) != 3 {
		t.Fatalf("unexpected parallel report: %+v", report)
	}
	if report.Scenarios[0].Name != "par-1" || report.Scenarios[1].Name != "par-2" || report.Scenarios[2].Name != "par-3" {
		t.Fatalf("scenario order mismatch in parallel report: %+v", report.Scenarios)
	}

	// Filter test
	filteredReport := RunSuiteWithOptions(context.Background(), scenarios, SuiteOptions{
		Filter: "par-[13]",
	})
	if !filteredReport.Passed || len(filteredReport.Scenarios) != 2 {
		t.Fatalf("filter failed: %+v", filteredReport)
	}
	if filteredReport.Scenarios[0].Name != "par-1" || filteredReport.Scenarios[1].Name != "par-3" {
		t.Fatalf("filter result mismatch: %+v", filteredReport.Scenarios)
	}
}
