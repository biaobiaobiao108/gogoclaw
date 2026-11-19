package memory

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ChatProjectNameMaxRunes         = 64
	ChatProjectInstructionsMaxRunes = 16000
)

var (
	ErrChatProjectNotFound     = errors.New("chat project not found")
	ErrChatProjectNameExists   = errors.New("chat project name already exists")
	ErrChatProjectFileNotFound = errors.New("chat project file not found")
)

type ChatProject struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Instructions string    `json:"instructions"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	SessionCount int       `json:"session_count,omitempty"`
	FileCount    int       `json:"file_count,omitempty"`
}

type ChatProjectFile struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	OriginalName string    `json:"filename"`
	StoredName   string    `json:"stored_name"`
	ContentType  string    `json:"content_type"`
	SizeBytes    int64     `json:"size_bytes"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	IndexedAt    time.Time `json:"indexed_at,omitempty"`
}

type ChatProjectChunk struct {
	Ordinal    int    `json:"ordinal"`
	Locator    string `json:"locator"`
	Content    string `json:"content"`
	TokenCount int    `json:"token_count"`
}

type ChatProjectChunkMatch struct {
	ChunkID    int64
	FileID     string
	ProjectID  string
	Filename   string
	Ordinal    int
	Locator    string
	Content    string
	TokenCount int
	Rank       float64
}

type ChatProjectTurnSource struct {
	SessionID        string `json:"session_id"`
	TurnID           string `json:"turn_id"`
	ProjectID        string `json:"project_id"`
	FileID           string `json:"file_id"`
	Filename         string `json:"filename"`
	Locator          string `json:"locator,omitempty"`
	Snippet          string `json:"snippet,omitempty"`
	Ordinal          int    `json:"ordinal"`
	ContentAvailable bool   `json:"content_available,omitempty"`
}

func NewChatProjectID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("memory: generate chat project id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func NormalizeChatProjectName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("project name is required")
	}
	if len([]rune(name)) > ChatProjectNameMaxRunes {
		return "", fmt.Errorf("project name must be at most %d characters", ChatProjectNameMaxRunes)
	}
	if strings.ContainsAny(name, "\r\n\x00") {
		return "", fmt.Errorf("project name must be a single line")
	}
	return name, nil
}

func NormalizeChatProjectInstructions(instructions string) (string, error) {
	instructions = strings.TrimSpace(instructions)
	if len([]rune(instructions)) > ChatProjectInstructionsMaxRunes {
		return "", fmt.Errorf("project instructions must be at most %d characters", ChatProjectInstructionsMaxRunes)
	}
	return instructions, nil
}

func (s *SQLiteStore) ListChatProjects(ctx context.Context) ([]ChatProject, error) {
	rows, err := s.db.QueryContext(sqliteContext(ctx), `
		SELECT p.id, p.name, p.instructions, p.created_at, p.updated_at,
			(SELECT count(*) FROM chat_session_projects sp WHERE sp.project_id = p.id),
			(SELECT count(*) FROM chat_project_files pf WHERE pf.project_id = p.id)
		FROM chat_projects p ORDER BY p.name COLLATE NOCASE, p.id`)
	if err != nil {
		return nil, fmt.Errorf("memory: list chat projects: %w", err)
	}
	defer rows.Close()
	projects := make([]ChatProject, 0)
	for rows.Next() {
		project, err := scanChatProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: iterate chat projects: %w", err)
	}
	return projects, nil
}

func (s *SQLiteStore) GetChatProject(ctx context.Context, id string) (ChatProject, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ChatProject{}, ErrChatProjectNotFound
	}
	row := s.db.QueryRowContext(sqliteContext(ctx), `
		SELECT p.id, p.name, p.instructions, p.created_at, p.updated_at,
			(SELECT count(*) FROM chat_session_projects sp WHERE sp.project_id = p.id),
			(SELECT count(*) FROM chat_project_files pf WHERE pf.project_id = p.id)
		FROM chat_projects p WHERE p.id = ?`, id)
	project, err := scanChatProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatProject{}, ErrChatProjectNotFound
	}
	return project, err
}

func (s *SQLiteStore) CreateChatProject(ctx context.Context, name, instructions string) (ChatProject, error) {
	name, err := NormalizeChatProjectName(name)
	if err != nil {
		return ChatProject{}, err
	}
	instructions, err = NormalizeChatProjectInstructions(instructions)
	if err != nil {
		return ChatProject{}, err
	}
	id, err := NewChatProjectID()
	if err != nil {
		return ChatProject{}, err
	}
	now := time.Now()
	_, err = s.db.ExecContext(sqliteContext(ctx), `
		INSERT INTO chat_projects(id, name, instructions, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, id, name, instructions, now.UnixNano(), now.UnixNano())
	if err != nil {
		if isChatProjectNameConflict(err) {
			return ChatProject{}, ErrChatProjectNameExists
		}
		return ChatProject{}, fmt.Errorf("memory: create chat project: %w", err)
	}
	return ChatProject{ID: id, Name: name, Instructions: instructions, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *SQLiteStore) UpdateChatProject(ctx context.Context, id, name, instructions string) (ChatProject, error) {
	id = strings.TrimSpace(id)
	current, err := s.GetChatProject(ctx, id)
	if err != nil {
		return ChatProject{}, err
	}
	if name != "" {
		current.Name, err = NormalizeChatProjectName(name)
		if err != nil {
			return ChatProject{}, err
		}
	}
	current.Instructions, err = NormalizeChatProjectInstructions(instructions)
	if err != nil {
		return ChatProject{}, err
	}
	now := time.Now()
	_, err = s.db.ExecContext(sqliteContext(ctx), `
		UPDATE chat_projects SET name = ?, instructions = ?, updated_at = ? WHERE id = ?`,
		current.Name, current.Instructions, now.UnixNano(), id)
	if err != nil {
		if isChatProjectNameConflict(err) {
			return ChatProject{}, ErrChatProjectNameExists
		}
		return ChatProject{}, fmt.Errorf("memory: update chat project: %w", err)
	}
	current.UpdatedAt = now
	return current, nil
}

func (s *SQLiteStore) DeleteChatProject(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(sqliteContext(ctx), `DELETE FROM chat_projects WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("memory: delete chat project: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("memory: inspect chat project delete: %w", err)
	}
	if n == 0 {
		return ErrChatProjectNotFound
	}
	return nil
}

func (s *SQLiteStore) SetSessionChatProject(ctx context.Context, sessionID, projectID string) error {
	sessionID = strings.TrimSpace(sessionID)
	projectID = strings.TrimSpace(projectID)
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	if projectID == "" {
		if _, err := s.db.ExecContext(sqliteContext(ctx),
			`DELETE FROM chat_session_projects WHERE session_id = ?`, sessionID); err != nil {
			return fmt.Errorf("memory: clear session chat project: %w", err)
		}
		return nil
	}
	if _, err := s.GetChatProject(ctx, projectID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(sqliteContext(ctx), `
		INSERT INTO chat_session_projects(session_id, project_id, created_at) VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET project_id = excluded.project_id, created_at = excluded.created_at`,
		sessionID, projectID, time.Now().UnixNano()); err != nil {
		return fmt.Errorf("memory: assign session chat project: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SessionChatProject(ctx context.Context, sessionID string) (*ChatProject, error) {
	row := s.db.QueryRowContext(sqliteContext(ctx), `
		SELECT p.id, p.name, p.instructions, p.created_at, p.updated_at,
			(SELECT count(*) FROM chat_session_projects sp2 WHERE sp2.project_id = p.id),
			(SELECT count(*) FROM chat_project_files pf WHERE pf.project_id = p.id)
		FROM chat_session_projects sp JOIN chat_projects p ON p.id = sp.project_id
		WHERE sp.session_id = ?`, strings.TrimSpace(sessionID))
	project, err := scanChatProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &project, nil
}

func (s *SQLiteStore) SessionChatProjectIDs(ctx context.Context, sessionIDs []string) (map[string]ChatProject, error) {
	result := make(map[string]ChatProject)
	if len(sessionIDs) == 0 {
		return result, nil
	}
	placeholders := make([]string, len(sessionIDs))
	args := make([]any, len(sessionIDs))
	for i, id := range sessionIDs {
		placeholders[i] = "?"
		args[i] = strings.TrimSpace(id)
	}
	query := `SELECT sp.session_id, p.id, p.name, p.instructions, p.created_at, p.updated_at, 0, 0
		FROM chat_session_projects sp JOIN chat_projects p ON p.id = sp.project_id
		WHERE sp.session_id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := s.db.QueryContext(sqliteContext(ctx), query, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: load session chat projects: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sessionID string
		var project ChatProject
		var created, updated int64
		if err := rows.Scan(&sessionID, &project.ID, &project.Name, &project.Instructions, &created, &updated,
			&project.SessionCount, &project.FileCount); err != nil {
			return nil, fmt.Errorf("memory: scan session chat project: %w", err)
		}
		project.CreatedAt = time.Unix(0, created)
		project.UpdatedAt = time.Unix(0, updated)
		result[sessionID] = project
	}
	return result, rows.Err()
}

func (s *SQLiteStore) CreateChatProjectFile(ctx context.Context, file ChatProjectFile) (ChatProjectFile, error) {
	if strings.TrimSpace(file.ProjectID) == "" || strings.TrimSpace(file.ID) == "" || strings.TrimSpace(file.OriginalName) == "" {
		return ChatProjectFile{}, fmt.Errorf("project file metadata is incomplete")
	}
	if file.Status == "" {
		file.Status = "processing"
	}
	now := time.Now()
	if file.CreatedAt.IsZero() {
		file.CreatedAt = now
	}
	_, err := s.db.ExecContext(sqliteContext(ctx), `
		INSERT INTO chat_project_files(
			id, project_id, original_name, stored_name, content_type, size_bytes, status, error, created_at, indexed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, file.ID, file.ProjectID, file.OriginalName, file.StoredName,
		file.ContentType, file.SizeBytes, file.Status, file.Error, file.CreatedAt.UnixNano(), timeToNanos(file.IndexedAt))
	if err != nil {
		return ChatProjectFile{}, fmt.Errorf("memory: create project file: %w", err)
	}
	return file, nil
}

func (s *SQLiteStore) UpdateChatProjectFileStatus(ctx context.Context, id, status, message string, indexedAt time.Time) error {
	result, err := s.db.ExecContext(sqliteContext(ctx), `
		UPDATE chat_project_files SET status = ?, error = ?, indexed_at = ? WHERE id = ?`,
		status, strings.TrimSpace(message), timeToNanos(indexedAt), strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("memory: update project file status: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrChatProjectFileNotFound
	}
	return nil
}

func (s *SQLiteStore) ListChatProjectFiles(ctx context.Context, projectID string) ([]ChatProjectFile, error) {
	rows, err := s.db.QueryContext(sqliteContext(ctx), `
		SELECT id, project_id, original_name, stored_name, content_type, size_bytes, status, error, created_at, indexed_at
		FROM chat_project_files WHERE project_id = ? ORDER BY created_at, id`, strings.TrimSpace(projectID))
	if err != nil {
		return nil, fmt.Errorf("memory: list project files: %w", err)
	}
	defer rows.Close()
	files := make([]ChatProjectFile, 0)
	for rows.Next() {
		file, err := scanChatProjectFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

func (s *SQLiteStore) GetChatProjectFile(ctx context.Context, projectID, fileID string) (ChatProjectFile, error) {
	row := s.db.QueryRowContext(sqliteContext(ctx), `
		SELECT id, project_id, original_name, stored_name, content_type, size_bytes, status, error, created_at, indexed_at
		FROM chat_project_files WHERE project_id = ? AND id = ?`, strings.TrimSpace(projectID), strings.TrimSpace(fileID))
	file, err := scanChatProjectFile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatProjectFile{}, ErrChatProjectFileNotFound
	}
	return file, err
}

func (s *SQLiteStore) DeleteChatProjectFile(ctx context.Context, projectID, fileID string) error {
	result, err := s.db.ExecContext(sqliteContext(ctx),
		`DELETE FROM chat_project_files WHERE project_id = ? AND id = ?`, strings.TrimSpace(projectID), strings.TrimSpace(fileID))
	if err != nil {
		return fmt.Errorf("memory: delete project file: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrChatProjectFileNotFound
	}
	return nil
}

func (s *SQLiteStore) ReplaceChatProjectFileChunks(ctx context.Context, projectID, fileID string, chunks []ChatProjectChunk) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin project file index: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM chat_project_file_chunks WHERE file_id = ?`, fileID); err != nil {
		return fmt.Errorf("memory: clear project file chunks: %w", err)
	}
	for i, chunk := range chunks {
		if strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		ordinal := chunk.Ordinal
		if ordinal == 0 {
			ordinal = i + 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO chat_project_file_chunks(file_id, project_id, ordinal, locator, content, token_count)
			VALUES (?, ?, ?, ?, ?, ?)`, fileID, projectID, ordinal, chunk.Locator, chunk.Content, chunk.TokenCount); err != nil {
			return fmt.Errorf("memory: insert project file chunk: %w", err)
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) ChatProjectFileChunks(ctx context.Context, projectID, fileID string) ([]ChatProjectChunk, error) {
	rows, err := s.db.QueryContext(sqliteContext(ctx), `
		SELECT ordinal, locator, content, token_count FROM chat_project_file_chunks
		WHERE project_id = ? AND file_id = ? ORDER BY ordinal, id`, strings.TrimSpace(projectID), strings.TrimSpace(fileID))
	if err != nil {
		return nil, fmt.Errorf("memory: load project file chunks: %w", err)
	}
	defer rows.Close()
	chunks := make([]ChatProjectChunk, 0)
	for rows.Next() {
		var chunk ChatProjectChunk
		if err := rows.Scan(&chunk.Ordinal, &chunk.Locator, &chunk.Content, &chunk.TokenCount); err != nil {
			return nil, fmt.Errorf("memory: scan project file chunk: %w", err)
		}
		chunks = append(chunks, chunk)
	}
	return chunks, rows.Err()
}

func (s *SQLiteStore) SearchChatProjectChunks(ctx context.Context, projectID, query string, limit int) ([]ChatProjectChunkMatch, error) {
	projectID = strings.TrimSpace(projectID)
	query = strings.TrimSpace(query)
	if projectID == "" || query == "" {
		return []ChatProjectChunkMatch{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 40
	}
	matches := make([]ChatProjectChunkMatch, 0)
	seen := make(map[int64]struct{})
	ftsQuery := chatProjectFTSQuery(query)
	if ftsQuery != "" {
		rows, err := s.db.QueryContext(sqliteContext(ctx), `
			SELECT c.id, c.file_id, c.project_id, f.original_name, c.ordinal, c.locator, c.content, c.token_count,
				bm25(chat_project_file_chunks_fts) AS rank
			FROM chat_project_file_chunks_fts
			JOIN chat_project_file_chunks c ON c.id = chat_project_file_chunks_fts.chunk_id
			JOIN chat_project_files f ON f.id = c.file_id
			WHERE chat_project_file_chunks_fts MATCH ? AND c.project_id = ? AND f.status = 'ready'
			ORDER BY rank, c.id LIMIT ?`, ftsQuery, projectID, limit)
		if err == nil {
			for rows.Next() {
				match, err := scanChatProjectChunkMatch(rows)
				if err != nil {
					rows.Close()
					return nil, err
				}
				seen[match.ChunkID] = struct{}{}
				matches = append(matches, match)
			}
			if err := rows.Close(); err != nil {
				return nil, err
			}
		}
	}
	if len(matches) >= min(limit, 8) {
		return matches, nil
	}
	rows, err := s.db.QueryContext(sqliteContext(ctx), `
		SELECT c.id, c.file_id, c.project_id, f.original_name, c.ordinal, c.locator, c.content, c.token_count, 0
		FROM chat_project_file_chunks c JOIN chat_project_files f ON f.id = c.file_id
		WHERE c.project_id = ? AND f.status = 'ready' AND instr(lower(c.content), lower(?)) > 0
		ORDER BY c.file_id, c.ordinal LIMIT ?`, projectID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("memory: fallback project chunk search: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		match, err := scanChatProjectChunkMatch(rows)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[match.ChunkID]; ok {
			continue
		}
		matches = append(matches, match)
		if len(matches) >= limit {
			break
		}
	}
	return matches, rows.Err()
}

func (s *SQLiteStore) ReplaceChatProjectTurnSources(ctx context.Context, sessionID, turnID string, sources []ChatProjectTurnSource) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin turn project sources: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM chat_project_turn_sources WHERE session_id = ? AND turn_id = ?`, sessionID, turnID); err != nil {
		return fmt.Errorf("memory: clear turn project sources: %w", err)
	}
	for i, source := range sources {
		ordinal := source.Ordinal
		if ordinal == 0 {
			ordinal = i + 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO chat_project_turn_sources(
				session_id, turn_id, ordinal, project_id, file_id, filename, locator, snippet
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, sessionID, turnID, ordinal, source.ProjectID, source.FileID,
			source.Filename, source.Locator, source.Snippet); err != nil {
			return fmt.Errorf("memory: save turn project source: %w", err)
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) ChatProjectTurnSources(ctx context.Context, sessionID string, turnIDs []string) (map[string][]ChatProjectTurnSource, error) {
	result := make(map[string][]ChatProjectTurnSource)
	if len(turnIDs) == 0 {
		return result, nil
	}
	placeholders := make([]string, len(turnIDs))
	args := make([]any, 0, len(turnIDs)+1)
	args = append(args, strings.TrimSpace(sessionID))
	for i, turnID := range turnIDs {
		placeholders[i] = "?"
		args = append(args, strings.TrimSpace(turnID))
	}
	rows, err := s.db.QueryContext(sqliteContext(ctx), `
		SELECT s.session_id, s.turn_id, s.ordinal, s.project_id, s.file_id, s.filename, s.locator, s.snippet,
			CASE WHEN f.status = 'ready' AND p.id IS NOT NULL THEN 1 ELSE 0 END
		FROM chat_project_turn_sources s
		LEFT JOIN chat_projects p ON p.id = s.project_id
		LEFT JOIN chat_project_files f ON f.project_id = s.project_id AND f.id = s.file_id
		WHERE s.session_id = ? AND s.turn_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY s.turn_id, s.ordinal`, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: load turn project sources: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var source ChatProjectTurnSource
		var contentAvailable int
		if err := rows.Scan(&source.SessionID, &source.TurnID, &source.Ordinal, &source.ProjectID, &source.FileID,
			&source.Filename, &source.Locator, &source.Snippet, &contentAvailable); err != nil {
			return nil, fmt.Errorf("memory: scan turn project source: %w", err)
		}
		source.ContentAvailable = contentAvailable == 1
		result[source.TurnID] = append(result[source.TurnID], source)
	}
	return result, rows.Err()
}

type chatProjectScanner interface {
	Scan(dest ...any) error
}

func scanChatProject(scanner chatProjectScanner) (ChatProject, error) {
	var project ChatProject
	var created, updated int64
	if err := scanner.Scan(&project.ID, &project.Name, &project.Instructions, &created, &updated,
		&project.SessionCount, &project.FileCount); err != nil {
		return ChatProject{}, err
	}
	project.CreatedAt = time.Unix(0, created)
	project.UpdatedAt = time.Unix(0, updated)
	return project, nil
}

func scanChatProjectFile(scanner chatProjectScanner) (ChatProjectFile, error) {
	var file ChatProjectFile
	var created, indexed int64
	if err := scanner.Scan(&file.ID, &file.ProjectID, &file.OriginalName, &file.StoredName, &file.ContentType,
		&file.SizeBytes, &file.Status, &file.Error, &created, &indexed); err != nil {
		return ChatProjectFile{}, err
	}
	file.CreatedAt = time.Unix(0, created)
	if indexed > 0 {
		file.IndexedAt = time.Unix(0, indexed)
	}
	return file, nil
}

func scanChatProjectChunkMatch(scanner chatProjectScanner) (ChatProjectChunkMatch, error) {
	var match ChatProjectChunkMatch
	if err := scanner.Scan(&match.ChunkID, &match.FileID, &match.ProjectID, &match.Filename, &match.Ordinal,
		&match.Locator, &match.Content, &match.TokenCount, &match.Rank); err != nil {
		return ChatProjectChunkMatch{}, fmt.Errorf("memory: scan project chunk match: %w", err)
	}
	return match, nil
}

func chatProjectFTSQuery(query string) string {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return ""
	}
	escaped := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(strings.ReplaceAll(term, `"`, `""`))
		if len([]rune(term)) < 3 {
			continue
		}
		escaped = append(escaped, `"`+term+`"`)
	}
	return strings.Join(escaped, " OR ")
}

func isChatProjectNameConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") && strings.Contains(message, "chat_projects.name")
}

func timeToNanos(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixNano()
}
