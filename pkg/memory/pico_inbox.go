package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
)

type PicoInboxMessage struct {
	ID      int64
	Message bus.InboundMessage
}

// QueuePicoInbound persists the payload and deduplication key in the same row.
func (s *SQLiteStore) QueuePicoInbound(ctx context.Context, sessionID, requestID string, msg bus.InboundMessage) (string, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	tx, err := s.BeginAppStateUpdate(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM pico_inbox WHERE state != 'pending' AND received_at < ?", time.Now().Add(-24*time.Hour).UnixMilli()); err != nil {
		return "", err
	}
	res, err := tx.ExecContext(ctx, "INSERT INTO pico_inbox(channel, session_id, request_id, received_at, state, payload) VALUES(?, ?, ?, ?, 'pending', ?) ON CONFLICT(channel, session_id, request_id) DO NOTHING", msg.Channel, sessionID, requestID, time.Now().UnixMilli(), payload)
	if err != nil {
		return "", err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	status := "accepted"
	if count == 0 {
		var state string
		if err := tx.QueryRowContext(ctx, "SELECT state FROM pico_inbox WHERE channel = ? AND session_id = ? AND request_id = ?", msg.Channel, sessionID, requestID).Scan(&state); err != nil {
			return "", err
		}
		switch state {
		case "processing":
			status = "unknown"
		case "done":
			status = "duplicate"
		}
	}
	return status, tx.Commit()
}

func (s *SQLiteStore) PendingPicoInbound(ctx context.Context, channel string, after int64) ([]PicoInboxMessage, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, payload FROM pico_inbox WHERE channel = ? AND state = 'pending' AND id > ? ORDER BY id LIMIT 32", channel, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []PicoInboxMessage
	for rows.Next() {
		var item PicoInboxMessage
		var payload []byte
		if err := rows.Scan(&item.ID, &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &item.Message); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// ClaimPicoInbound fences dispatch across a restart. A processing record keeps
// its payload for inspection and is never replayed after possible side effects.
func (s *SQLiteStore) ClaimPicoInbound(ctx context.Context, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, "UPDATE pico_inbox SET state = 'processing' WHERE id = ? AND state = 'pending'", id)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *SQLiteStore) FinishPicoInbound(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE pico_inbox SET state = 'done', payload = NULL WHERE id = ? AND state = 'processing'", id)
	return err
}

// MigratePicoInbox keeps old deduplication keys as uncertain deliveries, since
// their payloads were never recorded and cannot be replayed safely.
func (s *SQLiteStore) MigratePicoInbox(ctx context.Context) error {
	tx, err := s.BeginAppStateUpdate(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var data []byte
	if err := tx.QueryRowContext(ctx, "SELECT value FROM app_state WHERE namespace = 'pico' AND state_key = 'inbound_dedupe'").Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	var entries []struct {
		Key  string    `json:"key"`
		Seen time.Time `json:"seen_at"`
	}
	keys := make(map[string]time.Time)
	if err := json.Unmarshal(data, &entries); err == nil {
		for _, entry := range entries {
			keys[entry.Key] = entry.Seen
		}
	} else if err := json.Unmarshal(data, &keys); err != nil {
		// Preserve unreadable legacy state while allowing the new inbox to start.
		return nil
	}
	for key, seen := range keys {
		sessionID, requestID, _ := strings.Cut(key, "\x00")
		if sessionID == "" || requestID == "" || time.Since(seen) >= 24*time.Hour {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO pico_inbox(channel, session_id, request_id, received_at, state) VALUES('pico', ?, ?, ?, 'processing') ON CONFLICT(channel, session_id, request_id) DO NOTHING", sessionID, requestID, seen.UnixMilli()); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM app_state WHERE namespace = 'pico' AND state_key = 'inbound_dedupe'"); err != nil {
		return err
	}
	return tx.Commit()
}
