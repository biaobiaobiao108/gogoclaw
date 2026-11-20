package memory

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"time"

	"modernc.org/sqlite"
)

func init() {
	// SQLite's built-in lower() only folds ASCII, while session identifiers may
	// contain names supplied by messaging channels.
	sqlite.MustRegisterDeterministicScalarFunction("gogoclaw_lower", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			value, _ := args[0].(string)
			return strings.ToLower(value), nil
		},
	)
}

// ListManagedSessionPage reads only the metadata needed for the console list.
// The last row of the previous page is the exclusive (updated_at, key) bound.
func (s *SQLiteStore) ListManagedSessionPage(ctx context.Context, search string, after *SessionMeta, limit int) ([]SessionMeta, error) {
	query := `WITH page AS MATERIALIZED (
		SELECT session_key, updated_at, scope_json FROM session_meta WHERE 1 = 1`
	var args []any
	if after != nil {
		query += ` AND (updated_at, session_key) < (?, ?)`
		args = append(args, after.UpdatedAt.UnixNano(), after.Key)
	}
	if search = strings.ToLower(strings.TrimSpace(search)); search != "" {
		query += ` AND (instr(gogoclaw_lower(session_key), ?) > 0 OR instr(gogoclaw_lower(CAST(scope_json AS TEXT)), ?) > 0)`
		args = append(args, search, search)
	}
	query += ` ORDER BY updated_at DESC, session_key DESC LIMIT ?)
		SELECT session_key, updated_at, scope_json,
			(SELECT count(*) FROM session_messages WHERE session_key = page.session_key)
		FROM page ORDER BY updated_at DESC, session_key DESC`
	rows, err := s.db.QueryContext(sqliteContext(ctx), query, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []SessionMeta
	for rows.Next() {
		var entry SessionMeta
		var updated int64
		var scope []byte
		if err := rows.Scan(&entry.Key, &updated, &scope, &entry.Count); err != nil {
			return nil, err
		}
		entry.UpdatedAt = time.Unix(0, updated)
		entry.Scope = scope
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// ManagedSessionDetailMeta supplies attachment scope and delivery state without
// counting the entire transcript on every detail page.
func (s *SQLiteStore) ManagedSessionDetailMeta(ctx context.Context, key string) (SessionMeta, error) {
	ctx = sqliteContext(ctx)
	var scope, turn []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT scope_json, turn_json FROM session_meta WHERE session_key = ?`, key,
	).Scan(&scope, &turn)
	if err != nil {
		return SessionMeta{}, err
	}
	aliases, err := sqliteAliases(ctx, s.db, key)
	return SessionMeta{Key: key, Scope: scope, Turn: turn, Aliases: aliases}, err
}

type PicoSessionListEntry struct {
	Key            string
	ID             string
	Pinned         bool
	UpdatedAt      time.Time
	CreatedAt      time.Time
	VisibleCount   int
	PreviewMessage json.RawMessage
	UIMeta         json.RawMessage
	ProjectID      string
	ProjectName    string
}

// ListPicoSessionPage reads counts from stored statistics and fetches at most
// the first preview message for each selected session.
func (s *SQLiteStore) ListPicoSessionPage(ctx context.Context, legacyPrefix string, archived bool, projectFilter string, after *PicoSessionListEntry, limit int) ([]PicoSessionListEntry, error) {
	var pinned, updated, id any
	if after != nil {
		pinned, updated, id = after.Pinned, after.UpdatedAt.UnixNano(), after.ID
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH scoped AS (
			SELECT m.session_key, m.updated_at, m.created_at, m.summary, m.skip_count,
				CASE WHEN json_valid(CAST(m.scope_json AS TEXT)) THEN CAST(m.scope_json AS TEXT) END AS scope
			FROM session_meta m
		), scoped_ids AS (
			SELECT *, CASE WHEN lower(trim(json_extract(scope, '$.channel'))) = 'pico'
				THEN trim(json_extract(scope, '$.values.sender')) END AS sender,
				CASE WHEN lower(trim(json_extract(scope, '$.channel'))) = 'pico'
				THEN trim(json_extract(scope, '$.values.chat')) END AS chat
			FROM scoped
		), identified AS (
			SELECT session_key, updated_at, created_at, summary, skip_count, coalesce(
				CASE WHEN instr(sender, 'pico:') > 0 THEN nullif(trim(substr(sender, instr(sender, 'pico:') + 5)), '') END,
				CASE WHEN instr(chat, 'pico:') > 0 THEN nullif(trim(substr(chat, instr(chat, 'pico:') + 5)), '') END,
				CASE WHEN substr(session_key, 1, length(?1)) = ?1 THEN substr(session_key, length(?1) + 1) END,
				(SELECT substr(a.alias, length(?1) + 1) FROM session_aliases a
				 WHERE a.session_key = scoped_ids.session_key AND substr(a.alias, 1, length(?1)) = ?1
				 ORDER BY a.alias LIMIT 1)
			) AS id FROM scoped_ids
		), dedup AS (
			SELECT i.session_key, i.updated_at, i.created_at, i.summary != '' AS has_summary, i.id,
				coalesce(stats.visible_count, 0) AS visible_count, stats.first_preview_id, row_number() OVER (
				PARTITION BY i.id ORDER BY i.updated_at DESC, i.session_key
			) AS rank
			FROM identified i LEFT JOIN session_list_stats stats ON stats.session_key = i.session_key
			WHERE length(i.id) > 0 AND (i.summary != '' OR i.skip_count < coalesce(stats.row_count, 0))
		), decorated AS (
			SELECT d.session_key, d.updated_at, d.created_at, d.id, d.visible_count, d.first_preview_id,
				CASE WHEN json_valid(CAST(u.metadata_json AS TEXT)) THEN CAST(u.metadata_json AS TEXT) ELSE '{}' END AS ui,
				coalesce(sp.project_id, '') AS project_id, coalesce(p.name, '') AS project_name
			FROM dedup d LEFT JOIN chat_ui_meta u ON u.session_id = d.id
			LEFT JOIN chat_session_projects sp ON sp.session_id = d.id
			LEFT JOIN chat_projects p ON p.id = sp.project_id
			WHERE d.rank = 1 AND (d.visible_count > 0 OR d.has_summary)
		), ordered AS (
			SELECT *, coalesce(json_extract(ui, '$.pinned'), 0) = 1 AS pinned
			FROM decorated WHERE (coalesce(json_extract(ui, '$.archived'), 0) = 1) = ?2
				AND (?7 = '' OR (?7 = 'none' AND project_id = '') OR project_id = ?7)
		), page AS MATERIALIZED (
			SELECT * FROM ordered
			WHERE ?3 IS NULL OR (pinned, updated_at, id) < (?3, ?4, ?5)
			ORDER BY pinned DESC, updated_at DESC, id DESC LIMIT ?6
		)
		SELECT session_key, updated_at, created_at, visible_count, id, pinned, ui, project_id, project_name,
			(SELECT payload_json FROM session_messages WHERE id = page.first_preview_id)
		FROM page ORDER BY pinned DESC, updated_at DESC, id DESC
	`, legacyPrefix, archived, pinned, updated, id, limit, projectFilter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PicoSessionListEntry
	for rows.Next() {
		var entry PicoSessionListEntry
		var updated, created int64
		var ui string
		var preview []byte
		if err := rows.Scan(&entry.Key, &updated, &created, &entry.VisibleCount, &entry.ID, &entry.Pinned, &ui,
			&entry.ProjectID, &entry.ProjectName, &preview); err != nil {
			return nil, err
		}
		entry.UpdatedAt = time.Unix(0, updated)
		entry.CreatedAt = time.Unix(0, created)
		entry.UIMeta = json.RawMessage(ui)
		entry.PreviewMessage = preview
		result = append(result, entry)
	}
	return result, rows.Err()
}
