// Package usage defines the durable accounting vocabulary shared by the agent
// runtime, SQLite store, and dashboard.
package usage

import (
	"context"
	"math"
	"sync"
)

// CostRates are expressed in USD per one million tokens. Cost is accumulated
// as integer nano-USD so per-call rounding does not erase low-cost requests and
// long-running totals do not drift through float sums.
type CostRates struct {
	InputUSDPerMillion  float64
	OutputUSDPerMillion float64
}

// Record is one model call as it should be reflected in aggregate accounting.
// SessionKeys contains the primary session first and any parent sessions that
// should also inherit the task cost.
type Record struct {
	Day          string
	SessionKeys  []string
	AgentID      string
	Channel      string
	Provider     string
	Model        string
	ModelID      string
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	CostNanos    int64
	// SessionOnly updates per-session budget buckets without counting the same
	// call again in the daily dimensional aggregate. Parent agents use this when
	// they inherit a child task's cost from a different store.
	SessionOnly bool
}

// Totals is an aggregate over the selected accounting window.
type Totals struct {
	Calls        int64 `json:"calls"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
	CostNanos    int64 `json:"cost_nanos"`
}

// Breakdown is an aggregate for one provider, model, channel, or agent.
type Breakdown struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	ModelID  string `json:"model_id,omitempty"`
	Channel  string `json:"channel,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`
	Totals
}

// DayTotal is one natural day in a range query.
type DayTotal struct {
	Day string `json:"day"`
	Totals
}

// Store is implemented by the canonical session database. Budget checks fail
// closed when a configured budget cannot use this interface.
type Store interface {
	RecordUsage(context.Context, Record) error
	SessionUsage(context.Context, string) (Totals, error)
	DailyUsage(context.Context, string) (Totals, error)
	UsageBreakdown(context.Context, string, string) ([]Breakdown, error)
}

// CostNanos converts token counts and per-million-token prices to integer
// nano-USD. For token count t and price p USD/M, the result is t*p*1000
// nano-USD.
func CostNanos(inputTokens, outputTokens int64, rates CostRates) int64 {
	input := float64(max64(0, inputTokens)) * maxFloat(0, rates.InputUSDPerMillion)
	output := float64(max64(0, outputTokens)) * maxFloat(0, rates.OutputUSDPerMillion)
	nanos := (input + output) * 1000
	if nanos >= math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(math.Round(nanos))
}

// Limits contains the enabled token budgets for one check. Zero disables a
// dimension.
type Limits struct {
	Session int64
	Daily   int64
}

// BudgetCheck is the result of reserving capacity for one model call.
type BudgetCheck struct {
	Scope       string
	Used        int64
	Limit       int64
	Reservation *Reservation
}

type reservation struct {
	sessionKey string
	day        string
	tokens     int64
}

// Guard serializes budget checks across the single process and accounts for
// model calls that are already in flight but not yet in the durable totals.
type Guard struct {
	mu           sync.Mutex
	nextID       uint64
	reservations map[uint64]reservation
}

func NewGuard() *Guard {
	return &Guard{reservations: make(map[uint64]reservation)}
}

// Reservation keeps projected tokens unavailable until the model call settles.
type Reservation struct {
	guard *Guard
	id    uint64
}

// CheckAndReserve reads durable totals and outstanding reservations while
// holding one process-local lock. If a budget is exceeded and continueAnyway is
// false, no reservation is created.
func (g *Guard) CheckAndReserve(
	sessionKey string,
	day string,
	projected int64,
	limits Limits,
	continueAnyway bool,
	load func() (Totals, Totals, error),
) (BudgetCheck, error) {
	if g == nil {
		g = NewGuard()
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	sessionUsage, dailyUsage, err := load()
	if err != nil {
		return BudgetCheck{}, err
	}
	var sessionOutstanding, dailyOutstanding int64
	for _, item := range g.reservations {
		if item.sessionKey == sessionKey {
			sessionOutstanding += item.tokens
		}
		if item.day == day {
			dailyOutstanding += item.tokens
		}
	}

	check := BudgetCheck{}
	if limits.Session > 0 && sessionUsage.TotalTokens+sessionOutstanding+projected > limits.Session {
		check.Scope = "session"
		check.Used = sessionUsage.TotalTokens + sessionOutstanding
		check.Limit = limits.Session
	} else if limits.Daily > 0 && dailyUsage.TotalTokens+dailyOutstanding+projected > limits.Daily {
		check.Scope = "daily"
		check.Used = dailyUsage.TotalTokens + dailyOutstanding
		check.Limit = limits.Daily
	}
	if check.Scope != "" && !continueAnyway {
		return check, nil
	}

	if projected > 0 {
		g.nextID++
		id := g.nextID
		g.reservations[id] = reservation{sessionKey: sessionKey, day: day, tokens: projected}
		check.Reservation = &Reservation{guard: g, id: id}
	}
	return check, nil
}

// Release removes this reservation. It is safe to call more than once.
func (r *Reservation) Release() {
	if r == nil || r.guard == nil || r.id == 0 {
		return
	}
	r.guard.mu.Lock()
	delete(r.guard.reservations, r.id)
	r.guard.mu.Unlock()
	r.id = 0
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
