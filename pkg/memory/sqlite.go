package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers/messageutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/usage"
)

const sqliteSessionFilename = "sessions.db"

const sqliteSessionSchema = `
CREATE TABLE IF NOT EXISTS session_meta (
	session_key TEXT PRIMARY KEY,
	summary TEXT NOT NULL DEFAULT '',
	skip_count INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	scope_json BLOB,
	turn_json BLOB,
	turn_history_json BLOB NOT NULL DEFAULT '[]'
);

CREATE TABLE IF NOT EXISTS session_messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_key TEXT NOT NULL REFERENCES session_meta(session_key) ON DELETE CASCADE,
	role TEXT NOT NULL,
	content TEXT NOT NULL,
	payload_json BLOB NOT NULL
);

CREATE INDEX IF NOT EXISTS session_messages_session_id
	ON session_messages(session_key, id);

CREATE TABLE IF NOT EXISTS session_aliases (
	alias TEXT PRIMARY KEY,
	session_key TEXT NOT NULL REFERENCES session_meta(session_key) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS session_aliases_session_key
	ON session_aliases(session_key);

CREATE TABLE IF NOT EXISTS usage_daily (
	day TEXT NOT NULL,
	agent_id TEXT NOT NULL DEFAULT '',
	channel TEXT NOT NULL DEFAULT '',
	provider TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	model_id TEXT NOT NULL DEFAULT '',
	calls INTEGER NOT NULL DEFAULT 0,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	total_tokens INTEGER NOT NULL DEFAULT 0,
	cost_nanos INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY(day, agent_id, channel, provider, model, model_id)
);

CREATE INDEX IF NOT EXISTS usage_daily_day
	ON usage_daily(day);

CREATE TABLE IF NOT EXISTS usage_session (
	session_key TEXT PRIMARY KEY,
	updated_at INTEGER NOT NULL,
	calls INTEGER NOT NULL DEFAULT 0,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	total_tokens INTEGER NOT NULL DEFAULT 0,
	cost_nanos INTEGER NOT NULL DEFAULT 0
);

PRAGMA user_version = 2;
`

// SQLiteStore persists session state in a single SQLite database. Logical
// truncation advances skip_count so the full transcript remains available to
// the UI and branch operations. Deleted pages are left on SQLite's freelist for
// reuse by later writes; Compact deliberately does not run VACUUM.
type SQLiteStore struct {
	db   *sql.DB
	path string
}

var _ Store = (*SQLiteStore)(nil)
var _ usage.Store = (*SQLiteStore)(nil)

func sqliteContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// MessagePage is a stable, id-based page from a session transcript.
type MessagePage struct {
	Messages   []providers.Message
	NextCursor int64
	HasMore    bool
}

// NewSQLiteStore opens or creates sessions.db under dir.
func NewSQLiteStore(dir string) (*SQLiteStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("memory: create sessions directory: %w", err)
	}
	return openSQLiteStore(filepath.Join(dir, sqliteSessionFilename))
}

// OpenSQLiteStore opens an existing sessions.db under dir without creating it.
func OpenSQLiteStore(dir string) (*SQLiteStore, error) {
	path := filepath.Join(dir, sqliteSessionFilename)
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return openSQLiteStore(path)
}

func openSQLiteStore(path string) (*SQLiteStore, error) {
	params := url.Values{
		"_pragma": {
			"busy_timeout(5000)",
			"synchronous(NORMAL)",
			"foreign_keys(1)",
		},
		// Session mutations are read-modify-write operations. Taking the write
		// lock at BeginTx avoids WAL snapshot-upgrade failures under contention.
		"_txlock": {"immediate"},
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	db, err := sql.Open("sqlite", path+separator+params.Encode())
	if err != nil {
		return nil, fmt.Errorf("memory: open sessions database: %w", err)
	}
	db.SetMaxOpenConns(4)
	// Do not hold an idle handle on Windows. This keeps backups and workspace
	// maintenance possible while the agent is idle and lets SQLite close its
	// WAL handles between turns.
	db.SetMaxIdleConns(0)

	store := &SQLiteStore{db: db, path: path}
	if err := store.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) initialize() error {
	if _, err := s.db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		return fmt.Errorf("memory: enable sessions WAL: %w", err)
	}
	if _, err := s.db.Exec(sqliteSessionSchema); err != nil {
		return fmt.Errorf("memory: initialize sessions schema: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DBPath() string { return s.path }

func (s *SQLiteStore) AddMessage(ctx context.Context, sessionKey, role, content string) error {
	return s.AddFullMessage(ctx, sessionKey, providers.Message{Role: role, Content: content})
}

func (s *SQLiteStore) AddFullMessage(ctx context.Context, sessionKey string, msg providers.Message) error {
	ctx = sqliteContext(ctx)
	if messageutil.IsTransientAssistantThoughtMessage(msg) {
		return nil
	}
	now := time.Now()
	if msg.CreatedAt == nil {
		msg.CreatedAt = &now
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("memory: marshal message: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin add message: %w", err)
	}
	defer tx.Rollback()
	if err := ensureSQLiteSession(ctx, tx, sessionKey, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO session_messages(session_key, role, content, payload_json) VALUES (?, ?, ?, ?)`,
		sessionKey, msg.Role, msg.Content, payload,
	); err != nil {
		return fmt.Errorf("memory: append message: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE session_meta SET updated_at = ? WHERE session_key = ?`,
		now.UnixNano(), sessionKey,
	); err != nil {
		return fmt.Errorf("memory: update session timestamp: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory: commit message: %w", err)
	}
	return nil
}

func ensureSQLiteSession(ctx context.Context, tx *sql.Tx, sessionKey string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO session_meta(session_key, created_at, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(session_key) DO NOTHING`,
		sessionKey, now.UnixNano(), now.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("memory: create session metadata: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetHistory(ctx context.Context, sessionKey string) ([]providers.Message, error) {
	return s.readMessages(ctx, sessionKey, true)
}

func (s *SQLiteStore) GetTranscript(ctx context.Context, sessionKey string) ([]providers.Message, error) {
	return s.readMessages(ctx, sessionKey, false)
}

func (s *SQLiteStore) readMessages(ctx context.Context, sessionKey string, activeOnly bool) ([]providers.Message, error) {
	ctx = sqliteContext(ctx)
	query := `SELECT payload_json FROM session_messages WHERE session_key = ? ORDER BY id`
	args := []any{sessionKey}
	if activeOnly {
		query = `
			SELECT payload_json FROM session_messages
			WHERE session_key = ? ORDER BY id
			LIMIT -1 OFFSET COALESCE((SELECT skip_count FROM session_meta WHERE session_key = ?), 0)`
		args = append(args, sessionKey)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: read session messages: %w", err)
	}
	defer rows.Close()

	messages := make([]providers.Message, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("memory: scan session message: %w", err)
		}
		var msg providers.Message
		if err := json.Unmarshal(payload, &msg); err != nil {
			return nil, fmt.Errorf("memory: decode session message: %w", err)
		}
		if !messageutil.IsTransientAssistantThoughtMessage(msg) {
			messages = append(messages, msg)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: iterate session messages: %w", err)
	}
	return messages, nil
}

// GetTranscriptPage returns the newest messages before beforeID. The cursor is
// the database message id and remains valid across edits and deletes.
func (s *SQLiteStore) GetTranscriptPage(ctx context.Context, sessionKey string, limit int, beforeID int64) (MessagePage, error) {
	ctx = sqliteContext(ctx)
	if limit <= 0 {
		messages, err := s.GetTranscript(ctx, sessionKey)
		return MessagePage{Messages: messages}, err
	}
	query := `SELECT id, payload_json FROM session_messages WHERE session_key = ?`
	args := []any{sessionKey}
	if beforeID > 0 {
		query += ` AND id < ?`
		args = append(args, beforeID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return MessagePage{}, fmt.Errorf("memory: read session page: %w", err)
	}
	defer rows.Close()

	type record struct {
		id      int64
		message providers.Message
	}
	records := make([]record, 0, limit+1)
	for rows.Next() {
		var id int64
		var payload []byte
		if err := rows.Scan(&id, &payload); err != nil {
			return MessagePage{}, fmt.Errorf("memory: scan session page: %w", err)
		}
		var msg providers.Message
		if err := json.Unmarshal(payload, &msg); err != nil {
			return MessagePage{}, fmt.Errorf("memory: decode session page: %w", err)
		}
		if messageutil.IsTransientAssistantThoughtMessage(msg) {
			continue
		}
		records = append(records, record{id: id, message: msg})
	}
	if err := rows.Err(); err != nil {
		return MessagePage{}, fmt.Errorf("memory: iterate session page: %w", err)
	}

	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	page := MessagePage{HasMore: hasMore}
	for i := len(records) - 1; i >= 0; i-- {
		page.Messages = append(page.Messages, records[i].message)
	}
	if hasMore && len(records) > 0 {
		page.NextCursor = records[len(records)-1].id
	}
	return page, nil
}

func (s *SQLiteStore) GetSummary(ctx context.Context, sessionKey string) (string, error) {
	meta, err := s.GetSessionMeta(ctx, sessionKey)
	if err != nil {
		return "", err
	}
	return meta.Summary, nil
}

func (s *SQLiteStore) SetSummary(ctx context.Context, sessionKey, summary string) error {
	ctx = sqliteContext(ctx)
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin set summary: %w", err)
	}
	defer tx.Rollback()
	if err := ensureSQLiteSession(ctx, tx, sessionKey, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE session_meta SET summary = ?, updated_at = ? WHERE session_key = ?`,
		summary, now.UnixNano(), sessionKey,
	); err != nil {
		return fmt.Errorf("memory: set summary: %w", err)
	}
	return tx.Commit()
}

func (s *SQLiteStore) CommitSummary(key string, prefix []providers.Message, previous, summary string) (bool, error) {
	if len(prefix) == 0 {
		return false, nil
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("memory: begin summary commit: %w", err)
	}
	defer tx.Rollback()

	var currentSummary string
	var skip int
	if err := tx.QueryRowContext(ctx,
		`SELECT summary, skip_count FROM session_meta WHERE session_key = ?`, key,
	).Scan(&currentSummary, &skip); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("memory: read summary state: %w", err)
	}
	if currentSummary != previous {
		return false, nil
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT payload_json FROM session_messages WHERE session_key = ? ORDER BY id LIMIT ? OFFSET ?`,
		key, len(prefix), skip,
	)
	if err != nil {
		return false, fmt.Errorf("memory: read summary prefix: %w", err)
	}
	messages := make([]providers.Message, 0, len(prefix))
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return false, fmt.Errorf("memory: scan summary prefix: %w", err)
		}
		var msg providers.Message
		if err := json.Unmarshal(payload, &msg); err != nil {
			rows.Close()
			return false, fmt.Errorf("memory: decode summary prefix: %w", err)
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, fmt.Errorf("memory: iterate summary prefix: %w", err)
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if len(messages) != len(prefix) {
		return false, nil
	}
	want, err := json.Marshal(prefix)
	if err != nil {
		return false, err
	}
	got, err := json.Marshal(messages)
	if err != nil {
		return false, err
	}
	if string(want) != string(got) {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_meta
		SET summary = ?, skip_count = ?, updated_at = ?
		WHERE session_key = ?`,
		summary, skip+len(prefix), time.Now().UnixNano(), key,
	); err != nil {
		return false, fmt.Errorf("memory: commit summary: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("memory: commit summary transaction: %w", err)
	}
	return true, nil
}

func (s *SQLiteStore) TruncateHistory(ctx context.Context, sessionKey string, keepLast int) error {
	ctx = sqliteContext(ctx)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin truncate history: %w", err)
	}
	defer tx.Rollback()
	now := time.Now()
	if err := ensureSQLiteSession(ctx, tx, sessionKey, now); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_messages WHERE session_key = ?`, sessionKey,
	).Scan(&count); err != nil {
		return fmt.Errorf("memory: count history for truncate: %w", err)
	}
	skip := count
	if keepLast > 0 && keepLast < count {
		skip = count - keepLast
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE session_meta SET skip_count = ?, updated_at = ? WHERE session_key = ?`,
		skip, now.UnixNano(), sessionKey,
	); err != nil {
		return fmt.Errorf("memory: truncate history: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory: commit truncated history: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SetHistory(ctx context.Context, sessionKey string, history []providers.Message) error {
	ctx = sqliteContext(ctx)
	history = messageutil.FilterInvalidHistoryMessages(history)
	now := time.Now()
	for i := range history {
		if history[i].CreatedAt == nil {
			history[i].CreatedAt = &now
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin set history: %w", err)
	}
	defer tx.Rollback()
	if err := ensureSQLiteSession(ctx, tx, sessionKey, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_messages WHERE session_key = ?`, sessionKey); err != nil {
		return fmt.Errorf("memory: clear session history: %w", err)
	}
	for _, msg := range history {
		if messageutil.IsTransientAssistantThoughtMessage(msg) {
			continue
		}
		payload, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("memory: marshal history message: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO session_messages(session_key, role, content, payload_json) VALUES (?, ?, ?, ?)`,
			sessionKey, msg.Role, msg.Content, payload,
		); err != nil {
			return fmt.Errorf("memory: insert history message: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_meta SET skip_count = 0, updated_at = ? WHERE session_key = ?`,
		now.UnixNano(), sessionKey,
	); err != nil {
		return fmt.Errorf("memory: update history metadata: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory: commit history: %w", err)
	}
	return nil
}

// Compact is a no-op for SQLite. Deleted pages remain on the freelist and are
// reused by later writes, avoiding the I/O cost of repeatedly rewriting the DB.
func (s *SQLiteStore) Compact(context.Context, string) error { return nil }

func (s *SQLiteStore) GetSessionMeta(ctx context.Context, sessionKey string) (SessionMeta, error) {
	ctx = sqliteContext(ctx)
	meta, err := s.getSessionMeta(ctx, s.db, sessionKey)
	if err != nil {
		return SessionMeta{}, err
	}
	return meta, nil
}

type sqliteQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *SQLiteStore) getSessionMeta(ctx context.Context, q sqliteQueryer, sessionKey string) (SessionMeta, error) {
	var createdAt, updatedAt int64
	var scope, turn, turnHistory []byte
	meta := SessionMeta{Key: sessionKey}
	err := q.QueryRowContext(ctx, `
		SELECT summary, skip_count, created_at, updated_at,
		       scope_json, turn_json, turn_history_json
		FROM session_meta WHERE session_key = ?`, sessionKey,
	).Scan(&meta.Summary, &meta.Skip, &createdAt, &updatedAt, &scope, &turn, &turnHistory)
	if errors.Is(err, sql.ErrNoRows) {
		return meta, nil
	}
	if err != nil {
		return SessionMeta{}, fmt.Errorf("memory: read session metadata: %w", err)
	}
	meta.CreatedAt = time.Unix(0, createdAt)
	meta.UpdatedAt = time.Unix(0, updatedAt)
	meta.Scope = cloneRawJSON(scope)
	meta.Turn = cloneRawJSON(turn)
	if len(turnHistory) > 0 {
		if err := json.Unmarshal(turnHistory, &meta.TurnHistory); err != nil {
			return SessionMeta{}, fmt.Errorf("memory: decode turn history: %w", err)
		}
	}
	if err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_messages WHERE session_key = ?`, sessionKey,
	).Scan(&meta.Count); err != nil {
		return SessionMeta{}, fmt.Errorf("memory: count session messages: %w", err)
	}
	aliases, err := sqliteAliases(ctx, q, sessionKey)
	if err != nil {
		return SessionMeta{}, err
	}
	meta.Aliases = aliases
	return meta, nil
}

func sqliteAliases(ctx context.Context, q sqliteQueryer, sessionKey string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT alias FROM session_aliases WHERE session_key = ? ORDER BY alias`, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("memory: list session aliases: %w", err)
	}
	defer rows.Close()
	var aliases []string
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, fmt.Errorf("memory: scan session alias: %w", err)
		}
		aliases = append(aliases, alias)
	}
	return aliases, rows.Err()
}

// ListSessionMeta returns all session metadata for management and UI reads.
func (s *SQLiteStore) ListSessionMeta(ctx context.Context) ([]SessionMeta, error) {
	ctx = sqliteContext(ctx)
	rows, err := s.db.QueryContext(ctx, `SELECT session_key FROM session_meta ORDER BY updated_at DESC, session_key`)
	if err != nil {
		return nil, fmt.Errorf("memory: list sessions: %w", err)
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, fmt.Errorf("memory: scan session key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	metas := make([]SessionMeta, 0, len(keys))
	for _, key := range keys {
		meta, err := s.GetSessionMeta(ctx, key)
		if err != nil {
			return nil, err
		}
		metas = append(metas, meta)
	}
	return metas, nil
}

func (s *SQLiteStore) SessionExists(ctx context.Context, sessionKey string) (bool, error) {
	ctx = sqliteContext(ctx)
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM session_meta WHERE session_key = ?)`, sessionKey).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("memory: check session: %w", err)
	}
	return exists == 1, nil
}

func (s *SQLiteStore) UpsertSessionMeta(ctx context.Context, sessionKey string, scope json.RawMessage, aliases []string) error {
	ctx = sqliteContext(ctx)
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin metadata update: %w", err)
	}
	defer tx.Rollback()
	if err := ensureSQLiteSession(ctx, tx, sessionKey, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_meta SET scope_json = ?, updated_at = ? WHERE session_key = ?`,
		nilIfEmpty(scope), now.UnixNano(), sessionKey,
	); err != nil {
		return fmt.Errorf("memory: update session scope: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_aliases WHERE session_key = ?`, sessionKey); err != nil {
		return fmt.Errorf("memory: clear session aliases: %w", err)
	}
	for _, alias := range normalizeAliases(sessionKey, aliases) {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO session_aliases(alias, session_key) VALUES (?, ?)
			ON CONFLICT(alias) DO UPDATE SET session_key = excluded.session_key`,
			alias, sessionKey,
		); err != nil {
			return fmt.Errorf("memory: upsert session alias: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory: commit metadata update: %w", err)
	}
	return nil
}

func nilIfEmpty(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

func (s *SQLiteStore) ResolveSessionKey(ctx context.Context, sessionKey string) (string, bool, error) {
	ctx = sqliteContext(ctx)
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return "", false, nil
	}
	direct, err := s.SessionExists(ctx, sessionKey)
	if err != nil {
		return "", false, err
	}
	if direct && shouldShortCircuitSessionResolve(sessionKey) {
		return sessionKey, true, nil
	}
	var canonical string
	err = s.db.QueryRowContext(ctx,
		`SELECT session_key FROM session_aliases WHERE alias = ?`, sessionKey).Scan(&canonical)
	if err == nil {
		return canonical, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("memory: resolve session alias: %w", err)
	}
	if direct {
		return sessionKey, true, nil
	}
	return "", false, nil
}

func (s *SQLiteStore) PromoteAliasHistory(ctx context.Context, sessionKey string, scope json.RawMessage, aliases []string) (bool, error) {
	ctx = sqliteContext(ctx)
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("memory: begin alias promotion: %w", err)
	}
	defer tx.Rollback()
	now := time.Now()
	if err := ensureSQLiteSession(ctx, tx, sessionKey, now); err != nil {
		return false, err
	}
	canonical, err := s.getSessionMeta(ctx, tx, sessionKey)
	if err != nil {
		return false, err
	}
	if canonical.Count > canonical.Skip || strings.TrimSpace(canonical.Summary) != "" {
		return false, nil
	}

	for _, alias := range normalizeAliases(sessionKey, aliases) {
		if isMainSessionAlias(alias) {
			continue
		}
		aliasMeta, err := s.getSessionMeta(ctx, tx, alias)
		if err != nil {
			return false, err
		}
		if aliasMeta.Count == 0 && strings.TrimSpace(aliasMeta.Summary) == "" {
			continue
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT role, content, payload_json FROM session_messages WHERE session_key = ? ORDER BY id`, alias)
		if err != nil {
			return false, fmt.Errorf("memory: read alias history: %w", err)
		}
		type aliasMessage struct {
			role    string
			content string
			payload []byte
		}
		aliasMessages := make([]aliasMessage, 0)
		for rows.Next() {
			var role, content string
			var payload []byte
			if err := rows.Scan(&role, &content, &payload); err != nil {
				rows.Close()
				return false, fmt.Errorf("memory: scan alias history: %w", err)
			}
			aliasMessages = append(aliasMessages, aliasMessage{role: role, content: content, payload: payload})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, fmt.Errorf("memory: iterate alias history: %w", err)
		}
		if err := rows.Close(); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM session_messages WHERE session_key = ?`, sessionKey); err != nil {
			return false, fmt.Errorf("memory: clear canonical history: %w", err)
		}
		for _, message := range aliasMessages {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO session_messages(session_key, role, content, payload_json) VALUES (?, ?, ?, ?)`,
				sessionKey, message.role, message.content, message.payload,
			); err != nil {
				return false, fmt.Errorf("memory: promote alias message: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE session_meta
			SET summary = ?, skip_count = 0, scope_json = ?, updated_at = ?
			WHERE session_key = ?`,
			aliasMeta.Summary, nilIfEmpty(scope), now.UnixNano(), sessionKey,
		); err != nil {
			return false, fmt.Errorf("memory: update promoted metadata: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("memory: commit alias promotion: %w", err)
		}
		return true, nil
	}
	return false, nil
}

func (s *SQLiteStore) SetTurn(ctx context.Context, key string, turn json.RawMessage) error {
	ctx = sqliteContext(ctx)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin turn update: %w", err)
	}
	defer tx.Rollback()
	now := time.Now()
	if err := ensureSQLiteSession(ctx, tx, key, now); err != nil {
		return err
	}
	meta, err := s.getSessionMeta(ctx, tx, key)
	if err != nil {
		return err
	}
	turn = cloneRawJSON(turn)
	if len(meta.Turn) > 0 {
		var old, next struct {
			TurnID string            `json:"turn_id"`
			Usage  *bus.ContextUsage `json:"usage"`
		}
		if err := json.Unmarshal(meta.Turn, &old); err != nil {
			return err
		}
		if err := json.Unmarshal(turn, &next); err != nil {
			return err
		}
		if old.TurnID == next.TurnID && old.Usage != nil {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(turn, &fields); err != nil {
				return err
			}
			fields["usage"], err = json.Marshal(bus.PreserveUsageTotals(next.Usage, old.Usage))
			if err != nil {
				return err
			}
			turn, err = json.Marshal(fields)
			if err != nil {
				return err
			}
		}
		if old.TurnID != "" && old.TurnID != next.TurnID {
			meta.TurnHistory = append(meta.TurnHistory, cloneRawJSON(meta.Turn))
			if overflow := len(meta.TurnHistory) - maxStoredTurnHistory; overflow > 0 {
				meta.TurnHistory = append([]json.RawMessage(nil), meta.TurnHistory[overflow:]...)
			}
		}
	}
	turnHistory, err := json.Marshal(meta.TurnHistory)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_meta
		SET turn_json = ?, turn_history_json = ?, updated_at = ?
		WHERE session_key = ?`,
		nilIfEmpty(turn), turnHistory, now.UnixNano(), key,
	); err != nil {
		return fmt.Errorf("memory: store turn checkpoint: %w", err)
	}
	return tx.Commit()
}

func (s *SQLiteStore) ClearTurns(ctx context.Context, key string) error {
	ctx = sqliteContext(ctx)
	_, err := s.db.ExecContext(ctx,
		`UPDATE session_meta SET turn_json = NULL, turn_history_json = '[]' WHERE session_key = ?`, key)
	if err != nil {
		return fmt.Errorf("memory: clear turn checkpoints: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateTurnUsage(key, turnID string, usage *bus.ContextUsage) error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin turn usage: %w", err)
	}
	defer tx.Rollback()
	meta, err := s.getSessionMeta(ctx, tx, key)
	if err != nil {
		return err
	}
	update := func(raw *json.RawMessage) error {
		if len(*raw) == 0 {
			return nil
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(*raw, &fields); err != nil {
			return err
		}
		var id string
		if err := json.Unmarshal(fields["turn_id"], &id); err != nil {
			return err
		}
		if id != turnID {
			return nil
		}
		var current *bus.ContextUsage
		if rawUsage := fields["usage"]; len(rawUsage) > 0 {
			if err := json.Unmarshal(rawUsage, &current); err != nil {
				return err
			}
		}
		fields["usage"], err = json.Marshal(bus.PreserveUsageTotals(current, usage))
		if err != nil {
			return err
		}
		*raw, err = json.Marshal(fields)
		return err
	}
	if err := update(&meta.Turn); err != nil {
		return err
	}
	for i := range meta.TurnHistory {
		if err := update(&meta.TurnHistory[i]); err != nil {
			return err
		}
	}
	turnHistory, err := json.Marshal(meta.TurnHistory)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE session_meta SET turn_json = ?, turn_history_json = ? WHERE session_key = ?`,
		nilIfEmpty(meta.Turn), turnHistory, key,
	); err != nil {
		return fmt.Errorf("memory: update turn usage: %w", err)
	}
	return tx.Commit()
}

// RecordUsage atomically updates daily dimensional totals and every session
// budget bucket that inherits this model call.
func (s *SQLiteStore) RecordUsage(ctx context.Context, record usage.Record) error {
	ctx = sqliteContext(ctx)
	day := strings.TrimSpace(record.Day)
	if day == "" {
		day = time.Now().Format("2006-01-02")
	}
	inputTokens := maxInt64(0, record.InputTokens)
	outputTokens := maxInt64(0, record.OutputTokens)
	totalTokens := maxInt64(0, record.TotalTokens)
	if totalTokens == 0 {
		totalTokens = inputTokens + outputTokens
	}
	if totalTokens == 0 {
		return nil
	}
	costNanos := maxInt64(0, record.CostNanos)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin usage record: %w", err)
	}
	defer tx.Rollback()
	if !record.SessionOnly {
		if _, err := tx.ExecContext(ctx, `
		INSERT INTO usage_daily(
			day, agent_id, channel, provider, model, model_id,
			calls, input_tokens, output_tokens, total_tokens, cost_nanos
		) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)
		ON CONFLICT(day, agent_id, channel, provider, model, model_id) DO UPDATE SET
			calls = calls + 1,
			input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens,
			total_tokens = total_tokens + excluded.total_tokens,
			cost_nanos = cost_nanos + excluded.cost_nanos`,
			day,
			strings.TrimSpace(record.AgentID),
			strings.TrimSpace(record.Channel),
			strings.TrimSpace(record.Provider),
			strings.TrimSpace(record.Model),
			strings.TrimSpace(record.ModelID),
			inputTokens,
			outputTokens,
			totalTokens,
			costNanos,
		); err != nil {
			return fmt.Errorf("memory: record daily usage: %w", err)
		}
	}

	now := time.Now().UnixNano()
	seen := make(map[string]struct{}, len(record.SessionKeys))
	for _, sessionKey := range record.SessionKeys {
		sessionKey = strings.TrimSpace(sessionKey)
		if sessionKey == "" {
			continue
		}
		if _, ok := seen[sessionKey]; ok {
			continue
		}
		seen[sessionKey] = struct{}{}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO usage_session(
				session_key, updated_at, calls, input_tokens, output_tokens, total_tokens, cost_nanos
			) VALUES (?, ?, 1, ?, ?, ?, ?)
			ON CONFLICT(session_key) DO UPDATE SET
				updated_at = excluded.updated_at,
				calls = calls + 1,
				input_tokens = input_tokens + excluded.input_tokens,
				output_tokens = output_tokens + excluded.output_tokens,
				total_tokens = total_tokens + excluded.total_tokens,
				cost_nanos = cost_nanos + excluded.cost_nanos`,
			sessionKey, now, inputTokens, outputTokens, totalTokens, costNanos,
		); err != nil {
			return fmt.Errorf("memory: record session usage: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory: commit usage record: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SessionUsage(ctx context.Context, sessionKey string) (usage.Totals, error) {
	ctx = sqliteContext(ctx)
	var total usage.Totals
	err := s.db.QueryRowContext(ctx, `
		SELECT calls, input_tokens, output_tokens, total_tokens, cost_nanos
		FROM usage_session WHERE session_key = ?`, strings.TrimSpace(sessionKey),
	).Scan(&total.Calls, &total.InputTokens, &total.OutputTokens, &total.TotalTokens, &total.CostNanos)
	if errors.Is(err, sql.ErrNoRows) {
		return usage.Totals{}, nil
	}
	if err != nil {
		return usage.Totals{}, fmt.Errorf("memory: read session usage: %w", err)
	}
	return total, nil
}

func (s *SQLiteStore) DailyUsage(ctx context.Context, day string) (usage.Totals, error) {
	ctx = sqliteContext(ctx)
	var total usage.Totals
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(calls), 0), COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0), COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(cost_nanos), 0)
		FROM usage_daily WHERE day = ?`, strings.TrimSpace(day),
	).Scan(&total.Calls, &total.InputTokens, &total.OutputTokens, &total.TotalTokens, &total.CostNanos)
	if err != nil {
		return usage.Totals{}, fmt.Errorf("memory: read daily usage: %w", err)
	}
	return total, nil
}

// UsageDays returns one row per day with usage, ordered from oldest to newest.
func (s *SQLiteStore) UsageDays(ctx context.Context, fromDay, toDay string) ([]usage.DayTotal, error) {
	ctx = sqliteContext(ctx)
	rows, err := s.db.QueryContext(ctx, `
		SELECT day, SUM(calls), SUM(input_tokens), SUM(output_tokens),
			SUM(total_tokens), SUM(cost_nanos)
		FROM usage_daily
		WHERE day >= ? AND day <= ?
		GROUP BY day
		ORDER BY day`,
		strings.TrimSpace(fromDay), strings.TrimSpace(toDay),
	)
	if err != nil {
		return nil, fmt.Errorf("memory: query daily usage: %w", err)
	}
	defer rows.Close()
	result := make([]usage.DayTotal, 0)
	for rows.Next() {
		var item usage.DayTotal
		if err := rows.Scan(
			&item.Day, &item.Calls, &item.InputTokens, &item.OutputTokens,
			&item.TotalTokens, &item.CostNanos,
		); err != nil {
			return nil, fmt.Errorf("memory: scan daily usage: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: iterate daily usage: %w", err)
	}
	return result, nil
}

func (s *SQLiteStore) UsageBreakdown(ctx context.Context, fromDay, toDay string) ([]usage.Breakdown, error) {
	ctx = sqliteContext(ctx)
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_id, channel, provider, model, model_id,
			SUM(calls), SUM(input_tokens), SUM(output_tokens), SUM(total_tokens), SUM(cost_nanos)
		FROM usage_daily
		WHERE day >= ? AND day <= ?
		GROUP BY agent_id, channel, provider, model, model_id
		ORDER BY SUM(total_tokens) DESC, provider, model, channel`,
		strings.TrimSpace(fromDay), strings.TrimSpace(toDay),
	)
	if err != nil {
		return nil, fmt.Errorf("memory: query usage breakdown: %w", err)
	}
	defer rows.Close()
	result := make([]usage.Breakdown, 0)
	for rows.Next() {
		var item usage.Breakdown
		if err := rows.Scan(
			&item.AgentID, &item.Channel, &item.Provider, &item.Model, &item.ModelID,
			&item.Calls, &item.InputTokens, &item.OutputTokens, &item.TotalTokens, &item.CostNanos,
		); err != nil {
			return nil, fmt.Errorf("memory: scan usage breakdown: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: iterate usage breakdown: %w", err)
	}
	return result, nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// DeleteSession removes the session and cascades to messages and aliases. It
// intentionally does not VACUUM; the freed pages remain available for reuse.
func (s *SQLiteStore) DeleteSession(ctx context.Context, sessionKey string) (bool, error) {
	ctx = sqliteContext(ctx)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("memory: begin delete session: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_session WHERE session_key = ?`, sessionKey); err != nil {
		return false, fmt.Errorf("memory: delete session usage: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM session_meta WHERE session_key = ?`, sessionKey)
	if err != nil {
		return false, fmt.Errorf("memory: delete session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("memory: delete session rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("memory: commit session deletion: %w", err)
	}
	return rows > 0, nil
}

func (s *SQLiteStore) ListSessions() []string {
	rows, err := s.db.Query(`SELECT session_key FROM session_meta ORDER BY session_key`)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if rows.Scan(&key) == nil {
			keys = append(keys, key)
		}
	}
	return keys
}

func (s *SQLiteStore) Close() error { return s.db.Close() }
