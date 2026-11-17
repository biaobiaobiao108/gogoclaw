package memory

import "context"

func (s *SQLiteStore) ListEvolutionRecords(ctx context.Context, collection string) ([][]byte, error) {
	rows, err := s.db.QueryContext(sqliteContext(ctx), `SELECT payload FROM evolution_records WHERE collection = ? ORDER BY id`, collection)
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
