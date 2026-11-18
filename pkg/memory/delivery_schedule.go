package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const deliveryScheduleSchema = `
CREATE TABLE IF NOT EXISTS session_delivery_schedule (
	session_key TEXT PRIMARY KEY REFERENCES session_meta(session_key) ON DELETE CASCADE,
	due REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS session_delivery_schedule_due ON session_delivery_schedule(due);
`

// Current checkpoints store a list. Older ones have only the delivery field.
const insertDeliverySchedule = `
INSERT OR REPLACE INTO session_delivery_schedule(session_key, due)
SELECT turns.session_key, MIN(
	CASE json_extract(item.value, '$.status')
	WHEN 'dispatching' THEN 0
	WHEN 'streaming' THEN COALESCE(unixepoch(json_extract(item.value, '$.lease_until'), 'subsec'), 0)
	WHEN 'delivered_bus' THEN MAX(
		COALESCE(unixepoch(json_extract(item.value, '$.lease_until'), 'subsec'), 0),
		COALESCE(unixepoch(json_extract(item.value, '$.next_attempt_at'), 'subsec'), 0))
	ELSE COALESCE(unixepoch(json_extract(item.value, '$.next_attempt_at'), 'subsec'), 0)
	END)
FROM %s AS turns, json_each(
	CASE WHEN json_valid(CAST(turns.turn_json AS TEXT)) THEN
		CASE WHEN json_array_length(CAST(turns.turn_json AS TEXT), '$.deliveries') > 0
			THEN json_extract(CAST(turns.turn_json AS TEXT), '$.deliveries')
			ELSE json_array(json_extract(CAST(turns.turn_json AS TEXT), '$.delivery'))
		END
	END) AS item
WHERE json_extract(item.value, '$.status') IN ('queued', 'failed', 'dispatching', 'streaming')
	OR (json_extract(item.value, '$.status') = 'delivered_bus' AND json_extract(item.value, '$.requires_ack') = 1)
GROUP BY turns.session_key;
`

func initializeDeliverySchedule(tx *sql.Tx, version int) error {
	if _, err := tx.Exec(deliveryScheduleSchema); err != nil {
		return err
	}
	const updatedTurn = "(SELECT NEW.session_key AS session_key, NEW.turn_json AS turn_json)"
	const trigger = `CREATE TRIGGER IF NOT EXISTS session_delivery_schedule_update
		AFTER UPDATE OF turn_json ON session_meta BEGIN
		DELETE FROM session_delivery_schedule WHERE session_key = NEW.session_key;
		%s
		END;`
	if _, err := tx.Exec(fmt.Sprintf(trigger, fmt.Sprintf(insertDeliverySchedule, updatedTurn))); err != nil {
		return err
	}
	if version < 3 {
		if _, err := tx.Exec(fmt.Sprintf(insertDeliverySchedule, "session_meta")); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) DueDeliverySessions(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_key FROM session_delivery_schedule
		WHERE due <= ? ORDER BY due, session_key`, float64(now.UnixMilli())/1000)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *SQLiteStore) ReadTurn(ctx context.Context, key string) (json.RawMessage, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, "SELECT turn_json FROM session_meta WHERE session_key = ?", key).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return raw, err
}
