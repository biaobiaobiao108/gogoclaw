package agent

import (
	"context"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/chatmeta"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

func TestPrepareChatBranchRequestRegeneratesAndStoresPreviousAnswer(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("default agent is unavailable")
	}
	sessionKey := "agent:main:pico:direct:pico:branch-test"
	history := []providers.Message{
		{ID: "user-1", Role: "user", Content: "first question"},
		{ID: "answer-1", Role: "assistant", Content: "first answer", ModelName: "test-model"},
	}
	if err := session.SetHistoryChecked(agent.Sessions, sessionKey, history); err != nil {
		t.Fatal(err)
	}
	if err := agent.Sessions.Save(sessionKey); err != nil {
		t.Fatal(err)
	}

	msg := bus.InboundMessage{
		SessionKey: sessionKey,
		Context: bus.InboundContext{Raw: map[string]string{
			"session_id":                 "branch-test",
			"regenerate_from_message_id": "answer-1",
		}},
	}
	prepared, handled, err := al.prepareChatBranchRequest(context.Background(), msg, agent, sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("regenerate request was not handled")
	}
	if prepared.Content != "first question" || prepared.MessageID != "user-1" {
		t.Fatalf("prepared request = %+v", prepared)
	}

	remaining, err := session.HistoryChecked(agent.Sessions, sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("history before regenerated answer = %+v, want empty", remaining)
	}
	store, err := chatmeta.LoadBranches(al.branchStorePath(agent))
	if err != nil {
		t.Fatal(err)
	}
	group, version, ok := store.FindVersion("branch-test", "answer-1")
	if !ok || group.RootMessageID != "user-1" || version.Preview != "first answer" {
		t.Fatalf("stored branch = %+v %+v %v", group, version, ok)
	}
}

func TestPrepareChatBranchRequestEditsUserMessage(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("default agent is unavailable")
	}
	sessionKey := "agent:main:pico:direct:pico:edit-test"
	history := []providers.Message{
		{ID: "user-1", Role: "user", Content: "old question"},
		{ID: "answer-1", Role: "assistant", Content: "old answer"},
		{ID: "user-2", Role: "user", Content: "follow up"},
	}
	if err := session.SetHistoryChecked(agent.Sessions, sessionKey, history); err != nil {
		t.Fatal(err)
	}
	if err := agent.Sessions.Save(sessionKey); err != nil {
		t.Fatal(err)
	}

	msg := bus.InboundMessage{
		SessionKey: sessionKey,
		Content:    "new question",
		Context: bus.InboundContext{Raw: map[string]string{
			"session_id":      "edit-test",
			"edit_message_id": "user-1",
		}},
	}
	prepared, handled, err := al.prepareChatBranchRequest(context.Background(), msg, agent, sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("edit request was not handled")
	}
	if prepared.Content != "new question" || prepared.MessageID != "user-1" {
		t.Fatalf("prepared request = %+v", prepared)
	}
	remaining, err := session.HistoryChecked(agent.Sessions, sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("history after edit reset = %+v, want empty", remaining)
	}
}
