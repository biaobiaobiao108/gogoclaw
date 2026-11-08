package approval

import "time"

type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusDenied   Status = "denied"
	StatusExpired  Status = "expired"
)

type Scope string

const (
	ScopeOnce    Scope = "once"
	ScopeSession Scope = "session"
)

// Request is the durable description of one human approval gate.
type Request struct {
	ID          string         `json:"id"`
	TurnID      string         `json:"turn_id,omitempty"`
	SessionKey  string         `json:"session_key"`
	Channel     string         `json:"channel,omitempty"`
	ChatID      string         `json:"chat_id,omitempty"`
	Tool        string         `json:"tool"`
	ToolCallID  string         `json:"tool_call_id,omitempty"`
	Arguments   map[string]any `json:"arguments,omitempty"`
	Status      Status         `json:"status"`
	Scope       Scope          `json:"scope,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	ExpiresAt   time.Time      `json:"expires_at,omitempty"`
	ResolvedAt  *time.Time     `json:"resolved_at,omitempty"`
	Reason      string         `json:"reason,omitempty"`
	RiskSummary string         `json:"risk_summary,omitempty"`
}

type Response struct {
	ID      string `json:"id"`
	Approve bool   `json:"approve"`
	Scope   Scope  `json:"scope,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type Decision struct {
	Approved bool
	Scope    Scope
	Reason   string
}
