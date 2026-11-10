package memory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func BenchmarkSQLiteStoreTranscriptPage10000(b *testing.B) {
	store, err := NewSQLiteStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.AddMessage(ctx, "benchmark", "user", "seed"); err != nil {
		b.Fatal(err)
	}
	payload, err := json.Marshal(providers.Message{Role: "user", Content: "history message"})
	if err != nil {
		b.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO session_messages(session_key, role, content, payload_json) VALUES (?, ?, ?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 9_999; i++ {
		if _, err := stmt.ExecContext(ctx, "benchmark", "user", "history message", payload); err != nil {
			b.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, err := store.GetTranscriptPage(ctx, "benchmark", 50, 0)
		if err != nil {
			b.Fatal(err)
		}
		if len(page.Messages) != 50 || !page.HasMore {
			b.Fatalf("unexpected page: messages=%d has_more=%v", len(page.Messages), page.HasMore)
		}
	}
}
