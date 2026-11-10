package session

import (
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/approval"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

type TurnCheckpoint struct {
	Usage      *bus.ContextUsage     `json:"usage,omitempty"`
	TurnID     string                `json:"turn_id"`
	Status     string                `json:"status"`
	EndReason  string                `json:"end_reason,omitempty"`
	Iteration  int                   `json:"iteration"`
	Tool       string                `json:"tool,omitempty"`
	StartedAt  time.Time             `json:"started_at,omitempty"`
	UpdatedAt  time.Time             `json:"updated_at,omitempty"`
	Plan       []TurnPlanStep        `json:"plan,omitempty"`
	Activities []TurnActivity        `json:"activities,omitempty"`
	Approval   *approval.Request     `json:"approval,omitempty"`
	Delivery   *TurnDelivery         `json:"delivery,omitempty"`
	Deliveries []TurnDelivery        `json:"deliveries,omitempty"`
	ToolItems  []TurnToolItem        `json:"tool_items,omitempty"`
	Events     []TurnCheckpointEvent `json:"events,omitempty"`
}

// TurnActivity is the durable user-facing execution timeline for a turn.
// It intentionally stores bounded summaries rather than full tool payloads.
type TurnActivity struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	Title       string     `json:"title"`
	ToolCallID  string     `json:"tool_call_id,omitempty"`
	Tool        string     `json:"tool,omitempty"`
	Detail      string     `json:"detail,omitempty"`
	Output      string     `json:"output,omitempty"`
	DurationMS  int64      `json:"duration_ms,omitempty"`
	ExitCode    *int       `json:"exit_code,omitempty"`
	Diff        string     `json:"diff,omitempty"`
	Artifacts   []string   `json:"artifacts,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type TurnDelivery struct {
	ID             string              `json:"id"`
	MessageID      string              `json:"message_id,omitempty"`
	Status         string              `json:"status"`
	Channel        string              `json:"channel,omitempty"`
	ChatID         string              `json:"chat_id,omitempty"`
	AgentID        string              `json:"agent_id,omitempty"`
	Context        *bus.InboundContext `json:"context,omitempty"`
	Scope          *bus.OutboundScope  `json:"scope,omitempty"`
	Content        string              `json:"content,omitempty"`
	ContextUsage   *bus.ContextUsage   `json:"context_usage,omitempty"`
	RequiresAck    bool                `json:"requires_ack,omitempty"`
	Attempts       int                 `json:"attempts,omitempty"`
	LastError      string              `json:"last_error,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
	NextAttemptAt  *time.Time          `json:"next_attempt_at,omitempty"`
	LeaseUntil     *time.Time          `json:"lease_until,omitempty"`
	AcknowledgedAt *time.Time          `json:"acknowledged_at,omitempty"`
	DeadLetteredAt *time.Time          `json:"dead_lettered_at,omitempty"`
}

type TurnCheckpointEvent struct {
	Status    string         `json:"status"`
	Iteration int            `json:"iteration"`
	Tool      string         `json:"tool,omitempty"`
	At        time.Time      `json:"at"`
	Plan      []TurnPlanStep `json:"plan,omitempty"`
}

// TurnToolItem is the durable lifecycle record for one model-requested tool
// call. It stores bounded summaries rather than raw arguments or results.
type TurnToolItem struct {
	BatchID              string     `json:"batch_id"`
	Index                int        `json:"index"`
	CallID               string     `json:"call_id"`
	Tool                 string     `json:"tool"`
	ArgumentsFingerprint string     `json:"arguments_fingerprint,omitempty"`
	Status               string     `json:"status"`
	SideEffect           string     `json:"side_effect,omitempty"`
	ReadOnly             bool       `json:"read_only,omitempty"`
	Idempotent           bool       `json:"idempotent,omitempty"`
	Destructive          bool       `json:"destructive,omitempty"`
	ResultSummary        string     `json:"result_summary,omitempty"`
	ErrorSummary         string     `json:"error_summary,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	StartedAt            *time.Time `json:"started_at,omitempty"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
}

type TurnPlanStep struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

type TurnCheckpointStore interface {
	SetTurnCheckpoint(string, TurnCheckpoint) error
	GetTurnCheckpoint(string) (TurnCheckpoint, error)
	ClearTurnCheckpoints(string) error
}

// CheckedStore exposes persistence errors to execution-critical checkpoints.
type CheckedStore interface {
	AddFullMessageChecked(string, providers.Message) error
	GetHistoryChecked(string) ([]providers.Message, error)
	GetSummaryChecked(string) (string, error)
	SetHistoryChecked(string, []providers.Message) error
	SetSummaryChecked(string, string) error
}

// SummaryCommitStore atomically replaces a summarized prefix while retaining
// messages appended during the model call. A changed prefix invalidates the work.
type SummaryCommitStore interface {
	CommitSummary(string, []providers.Message, string, string) (bool, error)
}

type TurnUsageStore interface {
	UpdateTurnUsage(string, string, *bus.ContextUsage) error
}

func SetHistoryChecked(store SessionStore, key string, history []providers.Message) error {
	if checked, ok := store.(CheckedStore); ok {
		return checked.SetHistoryChecked(key, history)
	}
	store.SetHistory(key, history)
	return nil
}

func SetSummaryChecked(store SessionStore, key, summary string) error {
	if checked, ok := store.(CheckedStore); ok {
		return checked.SetSummaryChecked(key, summary)
	}
	store.SetSummary(key, summary)
	return nil
}

func TruncateHistoryChecked(store SessionStore, key string, keepLast int) error {
	if checked, ok := store.(interface{ TruncateHistoryChecked(string, int) error }); ok {
		return checked.TruncateHistoryChecked(key, keepLast)
	}
	store.TruncateHistory(key, keepLast)
	return nil
}

func AppendChecked(store SessionStore, key string, msg providers.Message) error {
	if checked, ok := store.(CheckedStore); ok {
		return checked.AddFullMessageChecked(key, msg)
	}
	store.AddFullMessage(key, msg)
	return nil
}

func HistoryChecked(store SessionStore, key string) ([]providers.Message, error) {
	if checked, ok := store.(CheckedStore); ok {
		return checked.GetHistoryChecked(key)
	}
	return store.GetHistory(key), nil
}

// TranscriptChecked reads the durable chat log rather than the active model context.
func TranscriptChecked(store SessionStore, key string) ([]providers.Message, error) {
	if transcript, ok := store.(interface {
		GetTranscriptChecked(string) ([]providers.Message, error)
	}); ok {
		return transcript.GetTranscriptChecked(key)
	}
	return HistoryChecked(store, key)
}

func SummaryChecked(store SessionStore, key string) (string, error) {
	if checked, ok := store.(CheckedStore); ok {
		return checked.GetSummaryChecked(key)
	}
	return store.GetSummary(key), nil
}

// SessionStore defines the persistence operations used by the agent loop.
// StoreBackend is the production adapter; SessionManager remains only as a
// legacy in-memory/file fallback for initialization failures and benchmarks.
//
// Write methods (Add*, Set*, Truncate*) are fire-and-forget: they do not
// return errors. Implementations should log failures internally. This
// matches the original SessionManager contract that the agent loop relies on.
type SessionStore interface {
	// AddMessage appends a simple role/content message to the session.
	AddMessage(sessionKey, role, content string)
	// AddFullMessage appends a complete message including tool calls.
	AddFullMessage(sessionKey string, msg providers.Message)
	// GetHistory returns the active model context; use TranscriptChecked for the chat log.
	GetHistory(key string) []providers.Message
	// GetSummary returns the conversation summary, or "" if none.
	GetSummary(key string) string
	// SetSummary replaces the conversation summary.
	SetSummary(key, summary string)
	// SetHistory replaces the full message history.
	SetHistory(key string, history []providers.Message)
	// TruncateHistory keeps only the last keepLast messages.
	TruncateHistory(key string, keepLast int)
	// Save persists any pending state to durable storage.
	Save(key string) error
	// ListSessions returns all known session keys.
	ListSessions() []string
	// Close releases resources held by the store.
	Close() error
}
