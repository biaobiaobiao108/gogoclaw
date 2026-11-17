// Package appstate provides small SQLite-backed state for components that
// cannot depend on the session store because they are needed during startup.
package appstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "sessions.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		if err := f.Close(); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secure application database: %w", err)
	}
	params := url.Values{"_pragma": {"busy_timeout(5000)", "synchronous(NORMAL)"}}
	db, err := sql.Open("sqlite", path+"?"+params.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxIdleConns(0)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS app_state (namespace TEXT NOT NULL, state_key TEXT NOT NULL, value BLOB NOT NULL, PRIMARY KEY(namespace, state_key))`); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize application state: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Get(namespace, key string) ([]byte, error) {
	var value []byte
	err := s.db.QueryRowContext(context.Background(), `SELECT value FROM app_state WHERE namespace = ? AND state_key = ?`, namespace, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return value, err
}
func (s *Store) Put(namespace, key string, value []byte) error {
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO app_state(namespace, state_key, value) VALUES (?, ?, ?) ON CONFLICT(namespace, state_key) DO UPDATE SET value = excluded.value`, namespace, key, value)
	return err
}
func (s *Store) Delete(namespace, key string) error {
	_, err := s.db.ExecContext(context.Background(), `DELETE FROM app_state WHERE namespace = ? AND state_key = ?`, namespace, key)
	return err
}
