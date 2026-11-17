package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GetAppState returns nil when the named state has not been saved.
func (s *SQLiteStore) GetAppState(ctx context.Context, namespace, key string) ([]byte, error) {
	var value []byte
	err := s.db.QueryRowContext(sqliteContext(ctx), `SELECT value FROM app_state WHERE namespace = ? AND state_key = ?`, namespace, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memory: read %s state: %w", namespace, err)
	}
	return value, nil
}

func (s *SQLiteStore) SetAppState(ctx context.Context, namespace, key string, value []byte) error {
	_, err := s.db.ExecContext(sqliteContext(ctx), `INSERT INTO app_state(namespace, state_key, value) VALUES (?, ?, ?) ON CONFLICT(namespace, state_key) DO UPDATE SET value = excluded.value`, namespace, key, value)
	if err != nil {
		return fmt.Errorf("memory: write %s state: %w", namespace, err)
	}
	return nil
}

// BeginAppStateUpdate holds SQLite's write lock across a state read and update.
func (s *SQLiteStore) BeginAppStateUpdate(ctx context.Context) (*sql.Tx, error) {
	return s.db.BeginTx(sqliteContext(ctx), nil)
}

func (s *SQLiteStore) DeleteAppState(ctx context.Context, namespace, key string) error {
	_, err := s.db.ExecContext(sqliteContext(ctx), `DELETE FROM app_state WHERE namespace = ? AND state_key = ?`, namespace, key)
	if err != nil {
		return fmt.Errorf("memory: delete %s state: %w", namespace, err)
	}
	return nil
}

// ListAppState returns all values in a namespace.
func (s *SQLiteStore) ListAppState(ctx context.Context, namespace string) (map[string][]byte, error) {
	rows, err := s.db.QueryContext(sqliteContext(ctx), `SELECT state_key, value FROM app_state WHERE namespace = ?`, namespace)
	if err != nil {
		return nil, fmt.Errorf("memory: list %s state: %w", namespace, err)
	}
	defer rows.Close()
	values := make(map[string][]byte)
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		values[key] = value
	}
	return values, rows.Err()
}
