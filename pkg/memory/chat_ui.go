package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ListChatUIMeta returns the UI metadata stored for each public session ID.
func (s *SQLiteStore) ListChatUIMeta(ctx context.Context) (map[string][]byte, error) {
	rows, err := s.db.QueryContext(sqliteContext(ctx), `SELECT session_id, metadata_json FROM chat_ui_meta`)
	if err != nil {
		return nil, fmt.Errorf("memory: list chat metadata: %w", err)
	}
	defer rows.Close()
	result := make(map[string][]byte)
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return nil, fmt.Errorf("memory: read chat metadata: %w", err)
		}
		result[id] = data
	}
	return result, rows.Err()
}

// UpdateChatUIMeta serializes the read-modify-write cycle across connections.
func (s *SQLiteStore) UpdateChatUIMeta(ctx context.Context, sessionID string, update func([]byte) ([]byte, error)) error {
	ctx = sqliteContext(ctx)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin chat metadata update: %w", err)
	}
	defer tx.Rollback()
	var data []byte
	err = tx.QueryRowContext(ctx, `SELECT metadata_json FROM chat_ui_meta WHERE session_id = ?`, sessionID).Scan(&data)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("memory: read chat metadata: %w", err)
	}
	data, err = update(data)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		_, err = tx.ExecContext(ctx, `DELETE FROM chat_ui_meta WHERE session_id = ?`, sessionID)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO chat_ui_meta(session_id, metadata_json) VALUES (?, ?) ON CONFLICT(session_id) DO UPDATE SET metadata_json = excluded.metadata_json`, sessionID, data)
	}
	if err != nil {
		return fmt.Errorf("memory: write chat metadata: %w", err)
	}
	return tx.Commit()
}
