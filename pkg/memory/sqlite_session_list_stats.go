package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers/messageutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

// Count rendered chat messages, including the tool-call row and any text
// delivered by the message tool. Tool results and reasoning are detail-only.
func sessionListMessageStats(msg providers.Message) (visible int, preview bool) {
	if msg.Role != "user" && msg.Role != "assistant" {
		return 0, false
	}
	hasAttachment := false
	for _, attachment := range msg.Attachments {
		if utils.DurableAttachmentID(attachment) != "" || utils.DisplayableAttachmentURL(attachment) != "" {
			hasAttachment = true
			break
		}
	}
	content := strings.TrimSpace(msg.Content)
	if msg.Role == "user" {
		if content != "" || len(msg.Media) > 0 || hasAttachment {
			return 1, true
		}
		return 0, false
	}
	if messageutil.IsTransientAssistantThoughtMessage(msg) {
		return 0, false
	}
	if content == messageutil.HandledToolResponseSummary {
		content = ""
		if hasAttachment {
			visible++
		}
	} else if content != "" || len(msg.Media) > 0 || hasAttachment {
		visible++
	}
	if len(utils.BuildVisibleToolCalls(msg.ToolCalls, 1, content)) > 0 {
		visible++
	}
	for _, call := range msg.ToolCalls {
		name, args := utils.VisibleToolCallNameAndArguments(call)
		if name == "message" {
			if _, ok := utils.ParseMessageToolContent(args); ok {
				visible++
			}
		}
	}
	return visible, false
}

func insertSQLiteMessage(ctx context.Context, tx *sql.Tx, key string, msg providers.Message, payload []byte) error {
	result, err := tx.ExecContext(ctx,
		`INSERT INTO session_messages(session_key, role, content, payload_json) VALUES (?, ?, ?, ?)`,
		key, msg.Role, msg.Content, payload)
	if err != nil {
		return fmt.Errorf("memory: insert message: %w", err)
	}
	visible, preview := sessionListMessageStats(msg)
	var firstPreview any
	if preview {
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		firstPreview = id
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_list_stats(session_key, row_count, visible_count, first_preview_id)
		VALUES (?, 1, ?, ?)
		ON CONFLICT(session_key) DO UPDATE SET
			row_count = row_count + 1,
			visible_count = visible_count + excluded.visible_count,
			first_preview_id = coalesce(first_preview_id, excluded.first_preview_id)`,
		key, visible, firstPreview)
	if err != nil {
		return fmt.Errorf("memory: update session list statistics: %w", err)
	}
	return nil
}

func clearSQLiteMessages(ctx context.Context, tx *sql.Tx, key string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_messages WHERE session_key = ?`, key); err != nil {
		return fmt.Errorf("memory: clear session history: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_list_stats WHERE session_key = ?`, key); err != nil {
		return fmt.Errorf("memory: clear session list statistics: %w", err)
	}
	return nil
}

func backfillSessionListStats(tx *sql.Tx) error {
	if _, err := tx.Exec(`DELETE FROM session_list_stats`); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT session_key, id, payload_json FROM session_messages ORDER BY session_key, id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var previous string
	var rowCount, visibleCount int
	var firstPreview any
	save := func() error {
		_, err := tx.Exec(`
			INSERT INTO session_list_stats(session_key, row_count, visible_count, first_preview_id)
			VALUES (?, ?, ?, ?)`, previous, rowCount, visibleCount, firstPreview)
		return err
	}
	for rows.Next() {
		var key string
		var id int64
		var payload []byte
		if err := rows.Scan(&key, &id, &payload); err != nil {
			return err
		}
		if rowCount > 0 && key != previous {
			if err := save(); err != nil {
				return err
			}
			rowCount, visibleCount, firstPreview = 0, 0, nil
		}
		previous = key
		rowCount++
		var msg providers.Message
		if err := json.Unmarshal(payload, &msg); err != nil {
			return fmt.Errorf("decode session message %d: %w", id, err)
		}
		visible, preview := sessionListMessageStats(msg)
		visibleCount += visible
		if preview && firstPreview == nil {
			firstPreview = id
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if rowCount > 0 {
		return save()
	}
	return nil
}
