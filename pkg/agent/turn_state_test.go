package agent

import (
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func TestGetActiveTurnByConversationIsScoped(t *testing.T) {
	al := &AgentLoop{}
	al.activeTurnStates.Store("one", &turnState{turnID: "one", channel: "pico", chatID: "pico:one", lifecycle: newTurnLifecycleAt(TurnPhaseRunning)})
	al.activeTurnStates.Store("two", &turnState{turnID: "two", channel: "pico", chatID: "pico:two", lifecycle: newTurnLifecycleAt(TurnPhaseTools)})
	got := al.GetActiveTurnByConversation("pico", "pico:two")
	if got == nil || got.TurnID != "two" || got.Phase != TurnPhaseTools {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
	if al.GetActiveTurnByConversation("pico", "pico:missing") != nil {
		t.Fatal("unexpected status for another conversation")
	}
}

func TestConversationInterruptedUsesRoutedSession(t *testing.T) {
	al, _, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()
	msg := bus.InboundMessage{
		Channel: "pico", ChatID: "pico:one", SenderID: "pico-user",
		Context: bus.InboundContext{Channel: "pico", ChatID: "pico:one", SenderID: "pico-user", ChatType: "direct"},
	}
	key, agentID, ok := al.resolveSteeringTarget(msg)
	if !ok {
		t.Fatal("could not resolve session")
	}
	agent, _ := al.registry.GetAgent(agentID)
	agent.Sessions.AddFullMessage(key, providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "pending"}}})
	if interrupted, err := al.ConversationInterrupted("pico", "pico:one", "pico-user"); err != nil || !interrupted {
		t.Fatal("interrupted turn not detected")
	}
	agent.Sessions.AddFullMessage(key, providers.Message{Role: "assistant", Content: "finished"})
	if interrupted, err := al.ConversationInterrupted("pico", "pico:one", "pico-user"); err != nil || interrupted {
		t.Fatal("finished turn detected as interrupted")
	}
}

func TestMatchingTurnMessageTail_IgnoresInternalRuntimeFields(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "question"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "read_file",
						Arguments: `{"path":"/tmp/test"}`,
					},
				},
			},
		},
	}

	persisted := []providers.Message{
		userPromptMessage("question", nil),
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{
					ID:               "call_1",
					Type:             "function",
					Name:             "read_file",
					Arguments:        map[string]any{"path": "/tmp/test"},
					ThoughtSignature: "internal-signature",
					Function: &providers.FunctionCall{
						Name:             "read_file",
						Arguments:        `{"path":"/tmp/test"}`,
						ThoughtSignature: "internal-signature",
					},
				},
			},
		},
	}

	if got := matchingTurnMessageTail(history, persisted); got != 2 {
		t.Fatalf("matchingTurnMessageTail() = %d, want 2", got)
	}
}

func TestSplitHistoryForActiveTurn_ProtectsPersistedTail(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "old question"},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "current question"},
		{Role: "tool", Content: "tool output", ToolCallID: "call_1"},
	}

	persisted := []providers.Message{
		userPromptMessage("current question", nil),
		{Role: "tool", Content: "tool output", ToolCallID: "call_1"},
	}

	stable, protected := splitHistoryForActiveTurn(history, persisted)
	if len(stable) != 2 {
		t.Fatalf("stable history len = %d, want 2", len(stable))
	}
	if len(protected) != 2 {
		t.Fatalf("protected tail len = %d, want 2", len(protected))
	}
	if protected[0].Content != "current question" {
		t.Fatalf("protected[0].Content = %q, want current question", protected[0].Content)
	}
}

func TestTrimHistoryToFitContextWindow_WithProtectedTurnTailKeepsActiveTurn(t *testing.T) {
	current := strings.Repeat("current turn ", 80)
	history := []providers.Message{
		{Role: "user", Content: strings.Repeat("old turn ", 60)},
		{Role: "assistant", Content: strings.Repeat("old reply ", 60)},
		{Role: "user", Content: current},
	}

	stable, protected := splitHistoryForActiveTurn(history, []providers.Message{
		userPromptMessage(current, nil),
	})
	trimmedStable, messages, fit := trimHistoryToFitContextWindow(
		stable,
		func(trimmedHistory []providers.Message) []providers.Message {
			return append(append([]providers.Message(nil), trimmedHistory...), protected...)
		},
		120,
		nil,
		0,
	)

	if fit {
		t.Fatal("expected protected active turn alone to remain over budget")
	}
	if len(trimmedStable) != 0 {
		t.Fatalf("trimmed stable history len = %d, want 0", len(trimmedStable))
	}
	if len(messages) != 1 {
		t.Fatalf("messages len = %d, want 1 protected active-turn message", len(messages))
	}
	if messages[0].Content != current {
		t.Fatalf("messages[0].Content = %q, want protected current turn", messages[0].Content)
	}
}
