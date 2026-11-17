// Package indexstore contains operations shared by session and retrieval storage.
package indexstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ClearSession removes a session's derived index within the caller's transaction.
// The index schema is initialized lazily and may not exist yet.
func ClearSession(ctx context.Context, tx *sql.Tx, key string) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'conversations'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT conversation_id FROM conversations WHERE session_key = ?`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ClearConversation(ctx, tx, id)
}

// ClearConversation removes messages, summaries and FTS entries in one transaction.
func ClearConversation(ctx context.Context, tx *sql.Tx, id int64) error {
	queries := []string{
		`DELETE FROM context_items WHERE conversation_id = ?`,
		`DELETE FROM summary_messages WHERE summary_id IN (SELECT summary_id FROM summaries WHERE conversation_id = ?)`,
		`DELETE FROM summary_parents WHERE summary_id IN (SELECT summary_id FROM summaries WHERE conversation_id = ?) OR parent_summary_id IN (SELECT summary_id FROM summaries WHERE conversation_id = ?)`,
		`DELETE FROM summaries WHERE conversation_id = ?`,
		`DELETE FROM message_parts WHERE message_id IN (SELECT message_id FROM messages WHERE conversation_id = ?)`,
		`DELETE FROM messages WHERE conversation_id = ?`,
	}
	for i, query := range queries {
		args := []any{id}
		if i == 2 {
			args = append(args, id)
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("clear index: %w", err)
		}
	}
	return nil
}
