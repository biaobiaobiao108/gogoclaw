package memory

import "context"

func (s *SQLiteStore) UpdateEvolutionDecisions(collection string, decisions []byte) error {
	_, err := s.db.Exec(`UPDATE evolution_records AS record
		SET payload = json_patch(record.payload, json_object(
			'considered_at', json_extract(decision.value, '$.considered_at'),
			'judged_by', json_extract(decision.value, '$.judged_by'),
			'discovery_key', json_extract(decision.value, '$.discovery_key'),
			'judged_success', json(decision.value -> '$.success')
		))
		FROM json_each(?) AS decision
		WHERE record.collection = ?
		AND json_extract(record.payload, '$.id') = json_extract(decision.value, '$.id')
		AND json_extract(record.payload, '$.workspace_id') = json_extract(decision.value, '$.workspace')`, string(decisions), collection)
	return err
}

func (s *SQLiteStore) ListEvolutionRecords(ctx context.Context, collection string) ([][]byte, error) {
	return s.listEvolutionRecords(ctx, `SELECT payload FROM evolution_records WHERE collection = ? ORDER BY id`, collection)
}

const pendingEvolutionTaskFilter = `json_extract(payload, '$.kind') IN ('task', 'case')
		AND coalesce(json_extract(payload, '$.status'), '') IN ('', 'new')
		AND json_extract(payload, '$.success') IS NOT NULL
		AND lower(trim(coalesce(json_extract(payload, '$.session_key'), ''))) <> 'heartbeat'
		AND lower(trim(coalesce(json_extract(payload, '$.final_output'), ''))) <> 'heartbeat_ok'
		AND trim(coalesce(json_extract(payload, '$.summary'), '')) <> ''
		AND (json_extract(payload, '$.success') = 0
			OR trim(coalesce(json_extract(payload, '$.final_output'), '')) <> ''
			OR json_array_length(payload, '$.tool_executions') > 0)`

func (s *SQLiteStore) ListPendingEvolutionTasks(ctx context.Context, collection, workspace string, limit int) ([][]byte, error) {
	return s.listEvolutionRecords(ctx, `SELECT payload FROM evolution_records
		WHERE collection = ? AND json_extract(payload, '$.workspace_id') = ?
		AND `+pendingEvolutionTaskFilter+`
		ORDER BY json_extract(payload, '$.considered_at'), id LIMIT ?`, collection, workspace, limit)
}

func (s *SQLiteStore) ListRelatedEvolutionTasks(ctx context.Context, collection, workspace, keys, excludedIDs string, limit int) ([][]byte, error) {
	return s.listEvolutionRecords(ctx, `WITH related AS (
		SELECT id, payload, row_number() OVER (
			PARTITION BY json_extract(payload, '$.discovery_key')
			ORDER BY json_extract(payload, '$.considered_at'), id
		) AS rank
		FROM evolution_records
		WHERE collection = ? AND json_extract(payload, '$.workspace_id') = ?
		AND json_extract(payload, '$.discovery_key') IN (SELECT value FROM json_each(?))
		AND json_extract(payload, '$.id') NOT IN (SELECT value FROM json_each(?))
		AND `+pendingEvolutionTaskFilter+`
	)
	SELECT payload FROM related ORDER BY rank, id LIMIT ?`, collection, workspace, keys, excludedIDs, limit)
}

func (s *SQLiteStore) ListEvolutionTasksByIDs(ctx context.Context, collection, workspace string, ids []byte) ([][]byte, error) {
	return s.listEvolutionRecords(ctx, `SELECT payload FROM evolution_records
		WHERE collection = ? AND json_extract(payload, '$.workspace_id') = ?
		AND json_extract(payload, '$.id') IN (SELECT value FROM json_each(?))
		ORDER BY id`, collection, workspace, string(ids))
}

func (s *SQLiteStore) MarkEvolutionTasksClustered(collection, workspace string, ids []byte) error {
	_, err := s.db.Exec(`UPDATE evolution_records AS task
		SET payload = json_set(task.payload, '$.status', 'clustered')
		WHERE task.collection = ?
		AND json_extract(task.payload, '$.id') IN (SELECT value FROM json_each(?))
		AND (json_extract(task.payload, '$.workspace_id') = ? OR NOT EXISTS (
			SELECT 1 FROM evolution_records AS scoped
			WHERE scoped.collection = task.collection
			AND json_extract(scoped.payload, '$.id') = json_extract(task.payload, '$.id')
			AND json_extract(scoped.payload, '$.workspace_id') = ?
		))`, collection, string(ids), workspace, workspace)
	return err
}

func (s *SQLiteStore) listEvolutionRecords(ctx context.Context, query string, args ...any) ([][]byte, error) {
	rows, err := s.db.QueryContext(sqliteContext(ctx), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records [][]byte
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		records = append(records, payload)
	}
	return records, rows.Err()
}

func (s *SQLiteStore) WriteEvolutionRecords(ctx context.Context, collection string, records [][]byte, replace bool) error {
	ctx = sqliteContext(ctx)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if replace {
		if _, err := tx.ExecContext(ctx, `DELETE FROM evolution_records WHERE collection = ?`, collection); err != nil {
			return err
		}
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO evolution_records(collection, payload) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, record := range records {
		if _, err := stmt.ExecContext(ctx, collection, record); err != nil {
			return err
		}
	}
	return tx.Commit()
}
