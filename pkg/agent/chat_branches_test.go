package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/chatmeta"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

func TestPrepareChatBranchRequestRegeneratesAndStoresPreviousAnswer(t *testing.T) {
	t.Setenv("GOGOCLAW_HOME", t.TempDir())
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
	store, err := chatmeta.LoadBranches(al.branchStorePath("branch-test"))
	if err != nil {
		t.Fatal(err)
	}
	group, version, ok := store.FindVersion("branch-test", "answer-1")
	if !ok || group.RootMessageID != "user-1" || version.Preview != "first answer" {
		t.Fatalf("stored branch = %+v %+v %v", group, version, ok)
	}
}

func TestBranchRollbackPreservesTranscriptAndSummarizedContext(t *testing.T) {
	t.Setenv("GOGOCLAW_HOME", t.TempDir())
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	agent := al.registry.GetDefaultAgent()
	raw, err := memory.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agent.Sessions = session.NewStoreBackend(raw)
	const key = "agent:main:pico:direct:pico:archive-rollback"
	history := []providers.Message{
		{ID: "old-user", Role: "user", Content: "old question"},
		{ID: "old-answer", Role: "assistant", Content: "old answer"},
		{ID: "new-user", Role: "user", Content: "new question"},
		{ID: "new-answer", Role: "assistant", Content: "new answer"},
	}
	if err := session.SetHistoryChecked(agent.Sessions, key, history); err != nil {
		t.Fatal(err)
	}
	if err := session.SetSummaryChecked(agent.Sessions, key, "old summary"); err != nil {
		t.Fatal(err)
	}
	if err := session.TruncateHistoryChecked(agent.Sessions, key, 2); err != nil {
		t.Fatal(err)
	}
	if err := al.recordBranchSnapshotByID(agent, key, "archive-rollback", "old-answer"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	backup, err := al.resetSessionHistory(ctx, agent, key, history[:1])
	if err != nil {
		t.Fatal(err)
	}
	if err := al.restoreSessionHistory(ctx, agent, key, backup); err != nil {
		t.Fatal(err)
	}
	transcript, err := session.TranscriptChecked(agent.Sessions, key)
	if err != nil || len(transcript) != 4 {
		t.Fatalf("transcript lost: %d, %v", len(transcript), err)
	}
	active, err := session.HistoryChecked(agent.Sessions, key)
	if err != nil || len(active) != 2 || active[0].ID != "new-user" {
		t.Fatalf("context offset lost: %+v, %v", active, err)
	}
	summary, err := session.SummaryChecked(agent.Sessions, key)
	if err != nil || summary != "old summary" {
		t.Fatalf("summary lost: %q, %v", summary, err)
	}
}

func TestPrepareChatBranchRequestEditsUserMessage(t *testing.T) {
	t.Setenv("GOGOCLAW_HOME", t.TempDir())
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
	store, err := chatmeta.LoadBranches(al.branchStorePath("edit-test"))
	if err != nil {
		t.Fatal(err)
	}
	group, version, ok := store.FindVersion("edit-test", "answer-1")
	if !ok || group.RootMessageID != "user-1" || version.Preview != "old answer" {
		t.Fatalf("stored edit branch = %+v %+v %v", group, version, ok)
	}
}

func TestNormalChatReplyDoesNotCreateBranchSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOGOCLAW_HOME", home)
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	msg := testInboundMessage(bus.InboundMessage{
		Channel:  "pico",
		ChatID:   "pico:normal-branch",
		SenderID: "user",
		Content:  "hello",
		Context: bus.InboundContext{
			Channel:  "pico",
			ChatID:   "pico:normal-branch",
			ChatType: "direct",
			SenderID: "user",
			Raw:      map[string]string{"session_id": "normal-branch"},
		},
	})
	if _, err := al.processMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(al.branchStorePath("normal-branch")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("normal reply created a branch store: %v", err)
	}
}

func TestEnsureBranchStoreMigratesEveryAgentWorkspace(t *testing.T) {
	t.Setenv("GOGOCLAW_HOME", t.TempDir())
	root := t.TempDir()
	mainWorkspace := filepath.Join(root, "workspace")
	supportWorkspace := filepath.Join(root, "workspace-support")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: mainWorkspace,
				ModelName: "test-model",
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Workspace: mainWorkspace},
				{ID: "Support", Workspace: supportWorkspace},
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})

	legacyPath := chatmeta.LegacyBranchStorePath(filepath.Join(supportWorkspace, "sessions"))
	legacy := chatmeta.BranchStore{
		Version: 1,
		Sessions: map[string]chatmeta.BranchSession{
			"migrated-session": {},
		},
	}
	if err := chatmeta.SaveBranches(legacyPath, legacy); err != nil {
		t.Fatal(err)
	}
	if err := al.ensureBranchStoreMigrated(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy branch store still exists: %v", err)
	}
	store, err := chatmeta.LoadBranches(al.branchStorePath("migrated-session"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Sessions["migrated-session"]; !ok {
		t.Fatal("support workspace branch was not migrated")
	}
}

func TestSelectBranchRejectsActiveTurnAndRestoresHistory(t *testing.T) {
	t.Setenv("GOGOCLAW_HOME", t.TempDir())
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	agent := al.registry.GetDefaultAgent()
	sessionID := "select-branch"
	sessionKey := "agent:main:pico:direct:pico:" + sessionID
	versionMessages := []providers.Message{
		{ID: "user-1", Role: "user", Content: "question"},
		{ID: "answer-1", Role: "assistant", Content: "old answer"},
	}
	store, err := chatmeta.LoadBranches(al.branchStorePath(sessionID))
	if err != nil {
		t.Fatal(err)
	}
	store.UpsertVersion(sessionID, "user-1", sessionKey, chatmeta.BranchVersion{
		ID:       "answer-1",
		Preview:  "old answer",
		Messages: versionMessages,
	})
	store.UpsertVersion(sessionID, "user-1", sessionKey, chatmeta.BranchVersion{
		ID:      "answer-2",
		Preview: "new answer",
		Messages: append(append([]providers.Message(nil), versionMessages...), providers.Message{
			ID: "answer-2", Role: "assistant", Content: "new answer",
		}),
	})
	if err := chatmeta.SaveBranches(al.branchStorePath(sessionID), store); err != nil {
		t.Fatal(err)
	}
	if err := session.SetHistoryChecked(agent.Sessions, sessionKey, store.Sessions[sessionID].Groups["user-1"].Versions[1].Messages); err != nil {
		t.Fatal(err)
	}

	al.activeTurnStates.Store(sessionKey, &turnState{
		sessionKey: sessionKey,
		channel:    "pico",
		chatID:     "pico:" + sessionID,
		lifecycle:  newTurnLifecycleAt(TurnPhaseRunning),
	})
	if err := al.SelectBranch(context.Background(), sessionID, "answer-1"); err == nil {
		t.Fatal("branch switch succeeded during an active turn")
	}
	al.activeTurnStates.Delete(sessionKey)

	if err := al.SelectBranch(context.Background(), sessionID, "answer-1"); err != nil {
		t.Fatal(err)
	}
	history, err := session.HistoryChecked(agent.Sessions, sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != len(versionMessages) || history[1].Content != "old answer" {
		t.Fatalf("restored history = %+v", history)
	}
	reloaded, err := chatmeta.LoadBranches(al.branchStorePath(sessionID))
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Sessions[sessionID].Groups["user-1"].ActiveVersionID; got != "answer-1" {
		t.Fatalf("active branch = %q, want answer-1", got)
	}
}

func TestRestoreSessionHistoryRollsBackTruncatedContext(t *testing.T) {
	t.Setenv("GOGOCLAW_HOME", t.TempDir())
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	agent := al.registry.GetDefaultAgent()
	sessionKey := "agent:main:pico:direct:pico:rollback-test"
	history := []providers.Message{
		{ID: "user-1", Role: "user", Content: "keep this"},
		{ID: "answer-1", Role: "assistant", Content: "and this"},
	}
	if err := session.SetHistoryChecked(agent.Sessions, sessionKey, history); err != nil {
		t.Fatal(err)
	}
	if err := session.SetSummaryChecked(agent.Sessions, sessionKey, "kept summary"); err != nil {
		t.Fatal(err)
	}
	backup, err := al.captureSessionHistory(agent, sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(backup.history) != len(history) {
		t.Fatalf("captured history = %+v, want %+v", backup.history, history)
	}
	if _, err := al.resetSessionHistory(context.Background(), agent, sessionKey, nil); err != nil {
		t.Fatal(err)
	}
	if err := al.restoreSessionHistory(context.Background(), agent, sessionKey, backup); err != nil {
		t.Fatal(err)
	}
	restored, err := session.HistoryChecked(agent.Sessions, sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != len(history) || restored[1].Content != "and this" {
		t.Fatalf("restored history = %+v", restored)
	}
	summary, err := session.SummaryChecked(agent.Sessions, sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if summary != "kept summary" {
		t.Fatalf("restored summary = %q", summary)
	}
}
