package memory

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/usage"
)

func newSQLiteTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSQLiteStoreLifecycleAndPersistence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(ctx, "session", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddFullMessage(ctx, "session", providers.Message{
		Role: "assistant", Content: "world", ModelName: "test-model",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSummary(ctx, "session", "summary"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	history, err := reopened.GetTranscript(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[1].ModelName != "test-model" {
		t.Fatalf("history = %+v", history)
	}
	if got, _ := reopened.GetSummary(ctx, "session"); got != "summary" {
		t.Fatalf("summary = %q", got)
	}
	if got := reopened.ListSessions(); len(got) != 1 || got[0] != "session" {
		t.Fatalf("sessions = %v", got)
	}
}

func TestSQLiteStoreLogicalTruncationKeepsTranscript(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	for _, content := range []string{"one", "two", "three"} {
		if err := store.AddMessage(ctx, "session", "user", content); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.TruncateHistory(ctx, "session", 1); err != nil {
		t.Fatal(err)
	}
	history, err := store.GetHistory(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Content != "three" {
		t.Fatalf("history = %+v", history)
	}
	transcript, err := store.GetTranscript(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript) != 3 {
		t.Fatalf("transcript length = %d", len(transcript))
	}
	meta, err := store.GetSessionMeta(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Skip != 2 || meta.Count != 3 {
		t.Fatalf("meta skip/count = %d/%d", meta.Skip, meta.Count)
	}
}

func TestPromoteAliasHistoryPreservesTruncatedHistory(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	for _, content := range []string{"summarized", "active"} {
		if err := store.AddMessage(ctx, "legacy", "user", content); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetSummary(ctx, "legacy", "earlier summary"); err != nil {
		t.Fatal(err)
	}
	if err := store.TruncateHistory(ctx, "legacy", 1); err != nil {
		t.Fatal(err)
	}
	if promoted, err := store.PromoteAliasHistory(ctx, "canonical", nil, []string{"legacy"}); err != nil || !promoted {
		t.Fatalf("PromoteAliasHistory = %v, %v", promoted, err)
	}
	history, err := store.GetHistory(ctx, "canonical")
	if err != nil || len(history) != 1 || history[0].Content != "active" {
		t.Fatalf("history = %+v, %v", history, err)
	}
	transcript, err := store.GetTranscript(ctx, "canonical")
	if err != nil || len(transcript) != 2 || transcript[0].Content != "summarized" {
		t.Fatalf("transcript = %+v, %v", transcript, err)
	}
	if summary, err := store.GetSummary(ctx, "canonical"); err != nil || summary != "earlier summary" {
		t.Fatalf("summary = %q, %v", summary, err)
	}
}

func TestSQLiteStoreCommitSummaryRejectsChangedPrefix(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	created := time.Unix(1, 0)
	first := providers.Message{Role: "user", Content: "first", CreatedAt: &created}
	second := providers.Message{Role: "assistant", Content: "second", CreatedAt: &created}
	if err := store.SetHistory(ctx, "session", []providers.Message{first, second}); err != nil {
		t.Fatal(err)
	}
	stale := []providers.Message{{Role: "user", Content: "different"}}
	if committed, err := store.CommitSummary("session", stale, "", "summary"); err != nil || committed {
		t.Fatalf("stale commit = %v, %v", committed, err)
	}
	if err := store.AddMessage(ctx, "session", "user", "latest"); err != nil {
		t.Fatal(err)
	}
	if committed, err := store.CommitSummary("session", []providers.Message{first}, "", "summary"); err != nil || !committed {
		t.Fatalf("commit = %v, %v", committed, err)
	}
	history, err := store.GetHistory(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Content != "second" || history[1].Content != "latest" {
		t.Fatalf("history after summary = %+v", history)
	}
}

func TestSQLiteStoreTranscriptPagination(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	for _, content := range []string{"one", "two", "three", "four", "five"} {
		if err := store.AddMessage(ctx, "session", "user", content); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.GetTranscriptPage(ctx, "session", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || len(first.Messages) != 2 || first.Messages[0].Content != "four" || first.Messages[1].Content != "five" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := store.GetTranscriptPage(ctx, "session", 2, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if !second.HasMore || len(second.Messages) != 2 || second.Messages[0].Content != "two" || second.Messages[1].Content != "three" {
		t.Fatalf("second page = %+v", second)
	}
	last, err := store.GetTranscriptPage(ctx, "session", 2, second.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if last.HasMore || len(last.Messages) != 1 || last.Messages[0].Content != "one" {
		t.Fatalf("last page = %+v", last)
	}
}

func TestSQLiteStoreAliasesAndTurnCheckpoints(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	scope := json.RawMessage(`{"channel":"pico","values":{"sender":"pico:web-1"}}`)
	if err := store.UpsertSessionMeta(ctx, "canonical", scope, []string{"legacy"}); err != nil {
		t.Fatal(err)
	}
	resolved, found, err := store.ResolveSessionKey(ctx, "legacy")
	if err != nil || !found || resolved != "canonical" {
		t.Fatalf("resolve = %q, %v, %v", resolved, found, err)
	}
	first := json.RawMessage(`{"turn_id":"turn-1","status":"running","usage":{"task_input_tokens":100}}`)
	if err := store.SetTurn(ctx, "canonical", first); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTurnUsage("canonical", "turn-1", &bus.ContextUsage{TaskInputTokens: 150, SummaryInputTokens: 20}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTurn(ctx, "canonical", json.RawMessage(`{"turn_id":"turn-2","status":"done"}`)); err != nil {
		t.Fatal(err)
	}
	meta, err := store.GetSessionMeta(ctx, "canonical")
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.TurnHistory) != 1 || len(meta.Aliases) != 1 || meta.Aliases[0] != "legacy" {
		t.Fatalf("meta = %+v", meta)
	}
	var previous struct {
		Usage bus.ContextUsage `json:"usage"`
	}
	if err := json.Unmarshal(meta.TurnHistory[0], &previous); err != nil {
		t.Fatal(err)
	}
	if previous.Usage.TaskInputTokens != 150 || previous.Usage.SummaryInputTokens != 20 {
		t.Fatalf("previous usage = %+v", previous.Usage)
	}
}

func TestSQLiteStoreDeletesSessionAndReusesFreePages(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	messages := make([]providers.Message, 300)
	for i := range messages {
		messages[i] = providers.Message{
			Role:    "assistant",
			Content: strings.Repeat("payload-", 2048),
			CreatedAt: func() *time.Time {
				created := time.Unix(int64(i), 0)
				return &created
			}(),
		}
	}
	if err := store.SetHistory(ctx, "delete-me", messages); err != nil {
		t.Fatal(err)
	}
	pageCount := sqlitePragmaInt(t, store, "page_count")
	if pageCount < 10 {
		t.Fatalf("page_count = %d, want a non-trivial database", pageCount)
	}
	before, err := os.Stat(store.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	removed, err := store.DeleteSession(ctx, "delete-me")
	if err != nil || !removed {
		t.Fatalf("DeleteSession = %v, %v", removed, err)
	}
	freeAfterDelete := sqlitePragmaInt(t, store, "freelist_count")
	if freeAfterDelete == 0 {
		t.Fatal("expected deleted pages on the SQLite freelist")
	}
	afterDelete, err := os.Stat(store.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	if afterDelete.Size() != before.Size() {
		t.Fatalf("delete unexpectedly rewrote database: before=%d after=%d", before.Size(), afterDelete.Size())
	}

	if err := store.SetHistory(ctx, "replacement", messages); err != nil {
		t.Fatal(err)
	}
	freeAfterReuse := sqlitePragmaInt(t, store, "freelist_count")
	if freeAfterReuse >= freeAfterDelete {
		t.Fatalf("freelist did not shrink after reuse: after delete=%d after reuse=%d", freeAfterDelete, freeAfterReuse)
	}
	if got := sqlitePragmaInt(t, store, "page_count"); got > pageCount+8 {
		t.Fatalf("replacement grew page count beyond reuse headroom: before=%d after=%d", pageCount, got)
	}
}

func TestSQLiteStoreUsageAggregatesDimensionsAndSessionInheritance(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteTestStore(t)
	if err := store.AddMessage(ctx, "child", "user", "child task"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(ctx, "parent", "user", "parent task"); err != nil {
		t.Fatal(err)
	}
	day := time.Now().Format("2006-01-02")
	record := usage.Record{
		Day:          day,
		SessionKeys:  []string{"child", "parent"},
		AgentID:      "main",
		Channel:      "pico",
		Provider:     "openai",
		Model:        "fast-model",
		ModelID:      "gpt-test",
		InputTokens:  120,
		OutputTokens: 30,
		TotalTokens:  150,
		CostNanos:    45,
	}
	if err := store.RecordUsage(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordUsage(ctx, record); err != nil {
		t.Fatal(err)
	}

	daily, err := store.DailyUsage(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if daily.Calls != 2 || daily.TotalTokens != 300 || daily.CostNanos != 90 {
		t.Fatalf("daily usage = %+v", daily)
	}
	for _, key := range []string{"child", "parent"} {
		totals, err := store.SessionUsage(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if totals.Calls != 2 || totals.InputTokens != 240 || totals.OutputTokens != 60 {
			t.Fatalf("session %s usage = %+v", key, totals)
		}
	}
	breakdown, err := store.UsageBreakdown(ctx, day, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(breakdown) != 1 || breakdown[0].Provider != "openai" || breakdown[0].Model != "fast-model" || breakdown[0].Channel != "pico" {
		t.Fatalf("breakdown = %+v", breakdown)
	}
	days, err := store.UsageDays(ctx, day, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || days[0].Day != day || days[0].TotalTokens != 300 {
		t.Fatalf("usage days = %+v", days)
	}

	removed, err := store.DeleteSession(ctx, "child")
	if err != nil || !removed {
		t.Fatalf("DeleteSession child = %v, %v", removed, err)
	}
	childUsage, err := store.SessionUsage(ctx, "child")
	if err != nil {
		t.Fatal(err)
	}
	if childUsage.TotalTokens != 0 {
		t.Fatalf("deleted session usage retained: %+v", childUsage)
	}
	daily, err = store.DailyUsage(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if daily.TotalTokens != 300 {
		t.Fatalf("daily aggregate should survive session deletion: %+v", daily)
	}
}

func sqlitePragmaInt(t *testing.T, store *SQLiteStore, name string) int {
	t.Helper()
	var value int
	if err := store.db.QueryRow("PRAGMA " + name).Scan(&value); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return value
}
