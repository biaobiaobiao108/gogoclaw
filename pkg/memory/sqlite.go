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
	"runtime"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers/messageutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/seahorse/indexstore"
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

CREATE INDEX IF NOT EXISTS session_meta_updated_key
	ON session_meta(updated_at DESC, session_key DESC);

CREATE TABLE IF NOT EXISTS pico_inbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel TEXT NOT NULL,
    session_id TEXT NOT NULL,
    request_id TEXT NOT NULL,
    received_at INTEGER NOT NULL,
    state TEXT NOT NULL,
    payload BLOB,
    UNIQUE(channel, session_id, request_id)
);
CREATE INDEX IF NOT EXISTS idx_pico_inbox_state ON pico_inbox(channel, state, id);
CREATE INDEX IF NOT EXISTS idx_pico_inbox_session ON pico_inbox(session_id, state);
CREATE INDEX IF NOT EXISTS idx_pico_inbox_expiry ON pico_inbox(received_at) WHERE state != 'pending';
CREATE TABLE IF NOT EXISTS app_state (
	namespace TEXT NOT NULL,
	state_key TEXT NOT NULL,
	value BLOB NOT NULL,
	PRIMARY KEY (namespace, state_key)
);

CREATE TABLE IF NOT EXISTS evolution_records (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	collection TEXT NOT NULL,
	payload BLOB NOT NULL
);

CREATE INDEX IF NOT EXISTS evolution_records_collection_id ON evolution_records(collection, id);
CREATE INDEX IF NOT EXISTS evolution_records_discovery ON evolution_records(
	collection, json_extract(payload, '$.workspace_id'), json_extract(payload, '$.discovery_key')
);

CREATE TABLE IF NOT EXISTS chat_ui_meta (
	session_id TEXT PRIMARY KEY,
	metadata_json BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS chat_projects (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL COLLATE NOCASE UNIQUE,
	instructions TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS chat_projects_name
	ON chat_projects(name COLLATE NOCASE, id);

CREATE TABLE IF NOT EXISTS chat_session_projects (
	session_id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES chat_projects(id) ON DELETE CASCADE,
	created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS chat_session_projects_project
	ON chat_session_projects(project_id, session_id);

CREATE TABLE IF NOT EXISTS chat_project_files (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES chat_projects(id) ON DELETE CASCADE,
	original_name TEXT NOT NULL,
	stored_name TEXT NOT NULL,
	content_type TEXT NOT NULL DEFAULT '',
	size_bytes INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	indexed_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS chat_project_files_project
	ON chat_project_files(project_id, created_at, id);

CREATE TABLE IF NOT EXISTS chat_project_file_chunks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	file_id TEXT NOT NULL REFERENCES chat_project_files(id) ON DELETE CASCADE,
	project_id TEXT NOT NULL REFERENCES chat_projects(id) ON DELETE CASCADE,
	ordinal INTEGER NOT NULL,
	locator TEXT NOT NULL DEFAULT '',
	content TEXT NOT NULL,
	token_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS chat_project_file_chunks_project
	ON chat_project_file_chunks(project_id, file_id, ordinal);

CREATE VIRTUAL TABLE IF NOT EXISTS chat_project_file_chunks_fts USING fts5(
	chunk_id,
	content,
	tokenize="trigram"
);

CREATE TRIGGER IF NOT EXISTS chat_project_file_chunks_ai AFTER INSERT ON chat_project_file_chunks BEGIN
	INSERT INTO chat_project_file_chunks_fts(chunk_id, content) VALUES (new.id, new.content);
END;
CREATE TRIGGER IF NOT EXISTS chat_project_file_chunks_ad AFTER DELETE ON chat_project_file_chunks BEGIN
	DELETE FROM chat_project_file_chunks_fts WHERE chunk_id = old.id;
END;
CREATE TRIGGER IF NOT EXISTS chat_project_file_chunks_au AFTER UPDATE ON chat_project_file_chunks BEGIN
	DELETE FROM chat_project_file_chunks_fts WHERE chunk_id = old.id;
	INSERT INTO chat_project_file_chunks_fts(chunk_id, content) VALUES (new.id, new.content);
END;

CREATE TABLE IF NOT EXISTS chat_project_turn_sources (
	session_id TEXT NOT NULL,
	turn_id TEXT NOT NULL,
	ordinal INTEGER NOT NULL,
	project_id TEXT NOT NULL,
	file_id TEXT NOT NULL,
	filename TEXT NOT NULL,
	locator TEXT NOT NULL DEFAULT '',
	snippet TEXT NOT NULL DEFAULT '',
	PRIMARY KEY(session_id, turn_id, ordinal)
);

CREATE INDEX IF NOT EXISTS chat_project_turn_sources_session
	ON chat_project_turn_sources(session_id, turn_id);

CREATE TABLE IF NOT EXISTS session_messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_key TEXT NOT NULL REFERENCES session_meta(session_key) ON DELETE CASCADE,
	role TEXT NOT NULL,
	content TEXT NOT NULL,
	payload_json BLOB NOT NULL
);

CREATE INDEX IF NOT EXISTS session_messages_session_id
	ON session_messages(session_key, id);

CREATE TABLE IF NOT EXISTS session_list_stats (
	session_key TEXT PRIMARY KEY REFERENCES session_meta(session_key) ON DELETE CASCADE,
	row_count INTEGER NOT NULL,
	visible_count INTEGER NOT NULL,
	first_preview_id INTEGER
);

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

PRAGMA user_version = 5;
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
	path := filepath.Join(dir, sqliteSessionFilename)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		if err := file.Close(); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("memory: create sessions database: %w", err)
	}
	return openSQLiteStore(path)
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
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("memory: secure sessions database: %w", err)
	}
	synchronous := "NORMAL"
	maxIdleConns := 0 // On Windows, idle file handles can prevent workspace maintenance.
	if runtime.GOOS == "linux" {
		// Reuse a connection between gateway polls. Since it keeps the WAL
		// open, sync commits without relying on a checkpoint at last close.
		synchronous = "FULL"
		maxIdleConns = 1
	}
	params := url.Values{
		"_pragma": {
			"busy_timeout(5000)",
			"synchronous(" + synchronous + ")",
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
	store, err := connectSQLiteStore(path, path+separator+params.Encode(), maxIdleConns)
	if err != nil {
		return nil, err
	}
	if err := store.initialize(); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

// OpenSQLiteStoreReadOnly reads an existing store without running startup checks
// or taking a write lock to initialize the schema on each page request.
func OpenSQLiteStoreReadOnly(dir string) (*SQLiteStore, error) {
	path := filepath.Join(dir, sqliteSessionFilename)
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	dsn, err := sqliteFileDSN(path, url.Values{
		"mode":    {"ro"},
		"_pragma": {"busy_timeout(5000)"},
	})
	if err != nil {
		return nil, err
	}
	return connectSQLiteStore(path, dsn, 0)
}

func sqliteFileDSN(path string, params url.Values) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	uriPath := filepath.ToSlash(absolute)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: params.Encode()}
	return uri.String(), nil
}

func connectSQLiteStore(path, dsn string, maxIdleConns int) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("memory: open sessions database: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(maxIdleConns)

	return &SQLiteStore{db: db, path: path}, nil
}

var sqliteInitMu sync.Mutex

func (s *SQLiteStore) initialize() error {
	sqliteInitMu.Lock()
	defer sqliteInitMu.Unlock()
	rows, err := s.db.Query("PRAGMA quick_check(1)")
	if err != nil {
		return fmt.Errorf("memory: sessions database integrity check failed: %w", err)
	}
	var integrityMessages []string
	for rows.Next() {
		var message string
		if err := rows.Scan(&message); err != nil {
			rows.Close()
			return fmt.Errorf("memory: scan sessions database quick check: %w", err)
		}
		integrityMessages = append(integrityMessages, message)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("memory: close sessions database quick check: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("memory: iterate sessions database quick check: %w", err)
	}
	if len(integrityMessages) != 1 || !strings.EqualFold(strings.TrimSpace(integrityMessages[0]), "ok") {
		return fmt.Errorf("memory: sessions database integrity check failed: %s", strings.Join(integrityMessages, "; "))
	}
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("memory: read sessions journal mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		deadline := time.Now().Add(5 * time.Second)
		for {
			_, err := s.db.Exec("PRAGMA journal_mode = WAL")
			if err == nil {
				break
			}
			var sqliteErr *sqlite.Error
			if !errors.As(err, &sqliteErr) || (sqliteErr.Code()&0xff != 5 && sqliteErr.Code()&0xff != 6) || time.Now().After(deadline) {
				return fmt.Errorf("memory: enable sessions WAL: %w", err)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("memory: begin sessions schema transaction: %w", err)
	}
	defer tx.Rollback()
	var schemaVersion int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&schemaVersion); err != nil {
		return err
	}
	if _, err := tx.Exec(sqliteSessionSchema); err != nil {
		return fmt.Errorf("memory: initialize sessions schema: %w", err)
	}
	if err := initializeDeliverySchedule(tx, schemaVersion); err != nil {
		return fmt.Errorf("memory: initialize delivery schedule: %w", err)
	}
	if schemaVersion < 5 {
		if err := backfillSessionListStats(tx); err != nil {
			return fmt.Errorf("memory: initialize session list statistics: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory: commit sessions schema: %w", err)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin add message: %w", err)
	}
	defer tx.Rollback()
	if err := appendSQLiteMessage(ctx, tx, sessionKey, msg); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory: commit message: %w", err)
	}
	return nil
}

func appendSQLiteMessage(ctx context.Context, tx *sql.Tx, sessionKey string, msg providers.Message) error {
	now := time.Now()
	if msg.CreatedAt == nil {
		msg.CreatedAt = &now
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("memory: marshal message: %w", err)
	}

	if err := ensureSQLiteSession(ctx, tx, sessionKey, now); err != nil {
		return err
	}
	if err := insertSQLiteMessage(ctx, tx, sessionKey, msg, payload); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE session_meta SET updated_at = ? WHERE session_key = ?`,
		now.UnixNano(), sessionKey,
	); err != nil {
		return fmt.Errorf("memory: update session timestamp: %w", err)
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

// FirstUserText reads the earliest nonblank user message in the transcript.
func (s *SQLiteStore) FirstUserText(ctx context.Context, sessionKey string) (string, error) {
	rows, err := s.db.QueryContext(sqliteContext(ctx),
		"SELECT content FROM session_messages WHERE session_key = ? AND role = 'user' ORDER BY id", sessionKey)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return "", err
		}
		if strings.TrimSpace(content) != "" {
			return content, nil
		}
	}
	return "", rows.Err()
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

// GetTranscriptPage returns the newest messages before beforeID. It extends a
// page when the oldest turn crosses the limit. The cursor is a database row ID.
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
	query += ` ORDER BY id DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return MessagePage{}, fmt.Errorf("memory: read session page: %w", err)
	}
	defer rows.Close()

	type record struct {
		id      int64
		message providers.Message
	}
	records := make([]record, 0, limit)
	var olderToolResults []record
	var boundaryTurn string
	var boundaryNeedsToolCall, hasMore bool
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
		entry := record{id: id, message: msg}
		turnID := msg.SteeringTurnID
		if msg.Role == "assistant" {
			turnID = messageutil.AssistantTurnID(msg.ID)
		}
		if len(records) < limit {
			records = append(records, entry)
			if len(records) == limit {
				boundaryTurn = turnID
				boundaryNeedsToolCall = msg.Role == "tool"
			}
			continue
		}
		if (boundaryTurn != "" || boundaryNeedsToolCall) && msg.Role == "tool" {
			olderToolResults = append(olderToolResults, entry)
			continue
		}
		if (msg.Role == "assistant" && boundaryNeedsToolCall) || (turnID != "" && turnID == boundaryTurn) {
			records = append(records, olderToolResults...)
			records = append(records, entry)
			olderToolResults = olderToolResults[:0]
			boundaryTurn = turnID
			boundaryNeedsToolCall = false
			continue
		}
		if boundaryTurn != "" && msg.Role == "user" && turnID == "" && len(olderToolResults) == 0 {
			records = append(records, entry)
			boundaryTurn = ""
			continue
		}
		hasMore = true
		break
	}
	if err := rows.Err(); err != nil {
		return MessagePage{}, fmt.Errorf("memory: iterate session page: %w", err)
	}

	hasMore = hasMore || len(olderToolResults) > 0
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
	if err := clearSQLiteMessages(ctx, tx, sessionKey); err != nil {
		return err
	}
	for _, msg := range history {
		payload, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("memory: marshal history message: %w", err)
		}
		if err := insertSQLiteMessage(ctx, tx, sessionKey, msg, payload); err != nil {
			return err
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.session_key, m.summary, m.skip_count, m.created_at, m.updated_at,
		       m.scope_json, m.turn_json, m.turn_history_json,
		       (SELECT COUNT(*) FROM session_messages msg WHERE msg.session_key = m.session_key)
		FROM session_meta m
		ORDER BY m.updated_at DESC, m.session_key`)
	if err != nil {
		return nil, fmt.Errorf("memory: list sessions: %w", err)
	}
	metas := make([]SessionMeta, 0)
	for rows.Next() {
		var createdAt, updatedAt int64
		var scope, turn, turnHistory []byte
		meta := SessionMeta{}
		if err := rows.Scan(
			&meta.Key,
			&meta.Summary,
			&meta.Skip,
			&createdAt,
			&updatedAt,
			&scope,
			&turn,
			&turnHistory,
			&meta.Count,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("memory: scan session metadata: %w", err)
		}
		meta.CreatedAt = time.Unix(0, createdAt)
		meta.UpdatedAt = time.Unix(0, updatedAt)
		meta.Scope = cloneRawJSON(scope)
		meta.Turn = cloneRawJSON(turn)
		if len(turnHistory) > 0 {
			if err := json.Unmarshal(turnHistory, &meta.TurnHistory); err != nil {
				rows.Close()
				return nil, fmt.Errorf("memory: decode turn history: %w", err)
			}
		}
		metas = append(metas, meta)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: iterate session metadata: %w", err)
	}
	if len(metas) == 0 {
		return metas, nil
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT session_key, alias
		FROM session_aliases
		ORDER BY session_key, alias`)
	if err != nil {
		return nil, fmt.Errorf("memory: list session aliases: %w", err)
	}
	defer rows.Close()
	aliasIndex := make(map[string]int, len(metas))
	for i := range metas {
		aliasIndex[metas[i].Key] = i
	}
	for rows.Next() {
		var sessionKey, alias string
		if err := rows.Scan(&sessionKey, &alias); err != nil {
			return nil, fmt.Errorf("memory: scan session alias: %w", err)
		}
		if index, ok := aliasIndex[sessionKey]; ok {
			metas[index].Aliases = append(metas[index].Aliases, alias)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: iterate session aliases: %w", err)
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
			`SELECT payload_json FROM session_messages WHERE session_key = ? ORDER BY id`, alias)
		if err != nil {
			return false, fmt.Errorf("memory: read alias history: %w", err)
		}
		var aliasMessages [][]byte
		for rows.Next() {
			var payload []byte
			if err := rows.Scan(&payload); err != nil {
				rows.Close()
				return false, fmt.Errorf("memory: scan alias history: %w", err)
			}
			aliasMessages = append(aliasMessages, payload)
		}
		if err := rows.Close(); err != nil {
			return false, err
		}
		if err := rows.Err(); err != nil {
			return false, fmt.Errorf("memory: iterate alias history: %w", err)
		}
		if err := clearSQLiteMessages(ctx, tx, sessionKey); err != nil {
			return false, err
		}
		for _, payload := range aliasMessages {
			var msg providers.Message
			if err := json.Unmarshal(payload, &msg); err != nil {
				return false, fmt.Errorf("memory: decode alias message: %w", err)
			}
			if err := insertSQLiteMessage(ctx, tx, sessionKey, msg, payload); err != nil {
				return false, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE session_meta
			SET summary = ?, skip_count = ?, scope_json = ?, updated_at = ?
			WHERE session_key = ?`,
			aliasMeta.Summary, aliasMeta.Skip, nilIfEmpty(scope), now.UnixNano(), sessionKey,
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
	var previous []byte
	var turnHistory any
	if err := tx.QueryRowContext(ctx, "SELECT turn_json FROM session_meta WHERE session_key = ?", key).Scan(&previous); err != nil {
		return err
	}
	turn = cloneRawJSON(turn)
	if len(previous) > 0 {
		var old, next struct {
			TurnID string            `json:"turn_id"`
			Usage  *bus.ContextUsage `json:"usage"`
		}
		if err := json.Unmarshal(previous, &old); err != nil {
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
			var archived map[string]json.RawMessage
			if err := json.Unmarshal(previous, &archived); err != nil {
				return err
			}
			delete(archived, "delivery")
			delete(archived, "deliveries")
			history, err := json.Marshal(archived)
			if err != nil {
				return err
			}
			var rawHistory []byte
			if err := tx.QueryRowContext(ctx, "SELECT turn_history_json FROM session_meta WHERE session_key = ?", key).Scan(&rawHistory); err != nil {
				return err
			}
			var snapshots []json.RawMessage
			if err := json.Unmarshal(rawHistory, &snapshots); err != nil {
				return err
			}
			snapshots = append(snapshots, history)
			if overflow := len(snapshots) - maxStoredTurnHistory; overflow > 0 {
				snapshots = snapshots[overflow:]
			}
			turnHistory, err = json.Marshal(snapshots)
			if err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_meta
		SET turn_json = ?, turn_history_json = COALESCE(?, turn_history_json), updated_at = ?
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
	return s.DeletePicoSession(ctx, sessionKey, "", false)
}

// DeletePicoSession also cancels accepted requests for the browser session ID.
func (s *SQLiteStore) DeletePicoSession(ctx context.Context, sessionKey, picoSessionID string, gatewayRunning bool) (bool, error) {
	return s.deleteSession(ctx, sessionKey, picoSessionID, gatewayRunning, false)
}

const sessionCleanupNamespace = "session_attachment_cleanup"

func (s *SQLiteStore) DeleteManagedSession(ctx context.Context, sessionKey, picoSessionID string) (bool, error) {
	return s.deleteSession(ctx, sessionKey, picoSessionID, false, true)
}

func (s *SQLiteStore) PendingSessionCleanup(ctx context.Context, sessionKey string) (string, error) {
	data, err := s.GetAppState(ctx, sessionCleanupNamespace, sessionKey)
	return string(data), err
}

func (s *SQLiteStore) CompleteSessionCleanup(ctx context.Context, sessionKey string) error {
	return s.DeleteAppState(ctx, sessionCleanupNamespace, sessionKey)
}

func (s *SQLiteStore) deleteSession(ctx context.Context, sessionKey, picoSessionID string, gatewayRunning, managed bool) (bool, error) {
	ctx = sqliteContext(ctx)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("memory: begin delete session: %w", err)
	}
	defer tx.Rollback()
	if picoSessionID != "" && gatewayRunning {
		var processing bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM pico_inbox WHERE session_id = ? AND state = 'processing'
		)`, picoSessionID).Scan(&processing); err != nil {
			return false, err
		}
		if processing {
			return false, ErrPicoInboundInFlight
		}
	}
	if err := indexstore.ClearSession(ctx, tx, sessionKey); err != nil {
		return false, fmt.Errorf("memory: delete session index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_session WHERE session_key = ?`, sessionKey); err != nil {
		return false, fmt.Errorf("memory: delete session usage: %w", err)
	}
	if picoSessionID != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_session_projects WHERE session_id = ?`, picoSessionID); err != nil {
			return false, fmt.Errorf("memory: delete session project assignment: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_project_turn_sources WHERE session_id = ?`, picoSessionID); err != nil {
			return false, fmt.Errorf("memory: delete session project sources: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM session_meta WHERE session_key = ?`, sessionKey)
	if err != nil {
		return false, fmt.Errorf("memory: delete session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("memory: delete session rows: %w", err)
	}
	if picoSessionID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE pico_inbox SET state = 'done', payload = NULL
			WHERE session_id = ? AND state != 'done'`, picoSessionID); err != nil {
			return false, fmt.Errorf("memory: cancel deleted session inbox: %w", err)
		}
		if managed && rows > 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM chat_ui_meta WHERE session_id = ?`, picoSessionID); err != nil {
				return false, fmt.Errorf("memory: delete session UI metadata: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM app_state WHERE namespace = 'chat_branches' AND state_key = ?`, picoSessionID); err != nil {
				return false, fmt.Errorf("memory: delete session branches: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_state(namespace, state_key, value)
				VALUES (?, ?, ?) ON CONFLICT(namespace, state_key) DO UPDATE SET value = excluded.value`,
				sessionCleanupNamespace, sessionKey, []byte(picoSessionID)); err != nil {
				return false, fmt.Errorf("memory: record pending session attachment cleanup: %w", err)
			}
		}
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
