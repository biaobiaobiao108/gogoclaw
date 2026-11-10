package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
	"github.com/biaobiaobiao108/gogoclaw/pkg/usage"
)

func TestUsageBudgetStopsBeforeReservedCall(t *testing.T) {
	store, backend := newUsageTestBackend(t)
	if err := store.RecordUsage(context.Background(), usage.Record{
		Day:          time.Now().Format("2006-01-02"),
		SessionKeys:  []string{"session"},
		InputTokens:  900,
		OutputTokens: 50,
		TotalTokens:  950,
	}); err != nil {
		t.Fatal(err)
	}
	ts := &turnState{
		agentID:    "main",
		sessionKey: "session",
		agent: &AgentInstance{
			MaxTokens: 100,
			Sessions:  backend,
			UsageGovernance: config.UsageGovernanceConfig{
				SessionTokenBudget: 1000,
			},
		},
	}
	blocked, message, _, err := ts.enforceUsageBudget(context.Background(), []providers.Message{{Role: "user", Content: "continue"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !blocked || !strings.Contains(message, "session") || !strings.Contains(message, "stopped") {
		t.Fatalf("blocked=%v message=%q", blocked, message)
	}
}

func TestUsageBudgetWarnModeDoesNotBlock(t *testing.T) {
	store, backend := newUsageTestBackend(t)
	if err := store.RecordUsage(context.Background(), usage.Record{
		Day:         time.Now().Format("2006-01-02"),
		SessionKeys: []string{"session"},
		InputTokens: 1000,
		TotalTokens: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	ts := &turnState{
		sessionKey: "session",
		agent: &AgentInstance{
			MaxTokens: 100,
			Sessions:  backend,
			UsageGovernance: config.UsageGovernanceConfig{
				DailyTokenBudget: 100,
				OnBudgetExceeded: "warn",
			},
		},
	}
	blocked, _, _, err := ts.enforceUsageBudget(context.Background(), []providers.Message{{Role: "user", Content: "continue"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if blocked {
		t.Fatal("warn mode blocked the model call")
	}
}

func TestUsageBudgetWorksWithEphemeralSubTurnSessions(t *testing.T) {
	store, backend := newUsageTestBackend(t)
	if err := store.RecordUsage(context.Background(), usage.Record{
		Day:         time.Now().Format("2006-01-02"),
		SessionKeys: []string{"parent-session"},
		TotalTokens: 950,
	}); err != nil {
		t.Fatal(err)
	}
	agent := &AgentInstance{
		MaxTokens: 100,
		Sessions:  newEphemeralSession(nil),
		UsageGovernance: config.UsageGovernanceConfig{
			SessionTokenBudget: 1000,
		},
		usageStore:  backend,
		usageLedger: backend,
	}
	ts := &turnState{
		agent:           agent,
		agentID:         "main",
		sessionKey:      "subturn-1",
		usageSessionKey: "parent-session",
	}
	blocked, message, _, err := ts.enforceUsageBudget(
		context.Background(),
		[]providers.Message{{Role: "user", Content: "continue"}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !blocked || !strings.Contains(message, "session") {
		t.Fatalf("blocked=%v message=%q", blocked, message)
	}
}

func TestUsageDailyBudgetUsesSharedLedgerAcrossAgents(t *testing.T) {
	_, firstStore := newUsageTestBackend(t)
	ledgerStore, ledger := newUsageTestBackend(t)
	if err := ledgerStore.RecordUsage(context.Background(), usage.Record{
		Day:         time.Now().Format("2006-01-02"),
		AgentID:     "other",
		TotalTokens: 950,
	}); err != nil {
		t.Fatal(err)
	}
	agent := &AgentInstance{
		MaxTokens: 100,
		Sessions:  firstStore,
		UsageGovernance: config.UsageGovernanceConfig{
			DailyTokenBudget: 1000,
		},
		usageStore:  firstStore,
		usageLedger: ledger,
	}
	ts := &turnState{agent: agent, agentID: "second", sessionKey: "session"}
	blocked, message, _, err := ts.enforceUsageBudget(
		context.Background(),
		[]providers.Message{{Role: "user", Content: "continue"}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !blocked || !strings.Contains(message, "Daily") {
		t.Fatalf("blocked=%v message=%q", blocked, message)
	}
}

func TestSideEffectFailureNoticeOnlyUsesLatestToolOutcome(t *testing.T) {
	ts := &turnState{turnID: "notice"}
	policies := []tools.ToolPolicy{tools.StatePolicy(), tools.StatePolicy(), tools.ReadPolicy()}
	batch := ts.progress.beginToolBatch("notice", 1, []providers.ToolCall{
		{ID: "one", Name: "write_file"},
		{ID: "two", Name: "send_message"},
		{ID: "three", Name: "read_file"},
	}, policies)
	ts.markToolItemTerminal(batch, 0, toolItemStatusFailed, toolItemSideEffectPossible, "failed")
	ts.markToolItemTerminal(batch, 1, toolItemStatusCompleted, toolItemSideEffectPossible, "sent")
	ts.markToolItemTerminal(batch, 2, toolItemStatusFailed, toolItemSideEffectNone, "failed")
	if notice := ts.sideEffectFailureNotice(""); !strings.Contains(notice, "write_file") || strings.Contains(notice, "send_message") {
		t.Fatalf("notice = %q", notice)
	}

	batch = ts.progress.beginToolBatch("notice", 2, []providers.ToolCall{{ID: "four", Name: "write_file"}}, []tools.ToolPolicy{tools.StatePolicy()})
	ts.markToolItemTerminal(batch, 0, toolItemStatusCompleted, toolItemSideEffectPossible, "written")
	if notice := ts.sideEffectFailureNotice(""); notice != "" {
		t.Fatalf("successful retry should clear earlier failure, got %q", notice)
	}
}

func TestSideEffectFailureNoticeIgnoresOperationsThatNeverRan(t *testing.T) {
	ts := &turnState{turnID: "notice-none"}
	policies := []tools.ToolPolicy{tools.StatePolicy(), tools.StatePolicy()}
	batch := ts.progress.beginToolBatch("notice-none", 1, []providers.ToolCall{
		{ID: "write", Name: "write_file"},
		{ID: "delete", Name: "delete_file"},
	}, policies)
	ts.markToolItemTerminal(batch, 0, toolItemStatusCompleted, toolItemSideEffectPossible, "written")
	ts.markToolItemTerminal(batch, 1, toolItemStatusDenied, toolItemSideEffectNone, "denied")

	if notice := ts.sideEffectFailureNotice("denied"); notice != "" {
		t.Fatalf("expected an explained denial to avoid a duplicate notice, got %q", notice)
	}
}

func TestSideEffectFailureNoticeKeepsFailuresForDifferentArguments(t *testing.T) {
	ts := &turnState{turnID: "notice-args"}
	policies := []tools.ToolPolicy{tools.StatePolicy()}
	firstArgs := map[string]any{"path": "first.md"}
	secondArgs := map[string]any{"path": "second.md"}
	batch := ts.progress.beginToolBatch("notice-args", 1, []providers.ToolCall{
		{ID: "first", Name: "write_file", Arguments: firstArgs},
	}, policies)
	ts.progress.updateToolItem(batch, 0, func(item *session.TurnToolItem) {
		item.ArgumentsFingerprint = toolCallFingerprint("write_file", firstArgs)
		item.Status = toolItemStatusFailed
		item.SideEffect = toolItemSideEffectUnknown
	})
	batch = ts.progress.beginToolBatch("notice-args", 2, []providers.ToolCall{
		{ID: "second", Name: "write_file", Arguments: secondArgs},
	}, policies)
	ts.progress.updateToolItem(batch, 0, func(item *session.TurnToolItem) {
		item.ArgumentsFingerprint = toolCallFingerprint("write_file", secondArgs)
		item.Status = toolItemStatusCompleted
		item.SideEffect = toolItemSideEffectPossible
	})

	notice := ts.sideEffectFailureNotice("")
	if !strings.Contains(notice, "write_file") || !strings.Contains(notice, "did not complete successfully") {
		t.Fatalf("expected different-argument failure to remain visible, got %q", notice)
	}
}

func newUsageTestBackend(t *testing.T) (*memory.SQLiteStore, *session.StoreBackend) {
	t.Helper()
	store, err := memory.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := session.NewStoreBackend(store)
	backend.AddMessage("session", "user", "seed")
	return store, backend
}
