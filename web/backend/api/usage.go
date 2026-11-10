package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/usage"
)

type usageResponse struct {
	Days       int              `json:"days"`
	From       string           `json:"from"`
	To         string           `json:"to"`
	Totals     usageTotals      `json:"totals"`
	Today      usageTotals      `json:"today"`
	Daily      []usageDay       `json:"daily"`
	ByProvider []usageBreakdown `json:"by_provider"`
	ByModel    []usageBreakdown `json:"by_model"`
	ByChannel  []usageBreakdown `json:"by_channel"`
	ByAgent    []usageBreakdown `json:"by_agent"`
	Governance usageGovernance  `json:"governance"`
}

type usageTotals struct {
	Calls        int64   `json:"calls"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	CostNanos    int64   `json:"cost_nanos"`
	CostUSD      float64 `json:"cost_usd"`
}

type usageBreakdown struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	ModelID  string `json:"model_id,omitempty"`
	Channel  string `json:"channel,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`
	usageTotals
}

type usageDay struct {
	Day string `json:"day"`
	usageTotals
}

type usageGovernance struct {
	SessionTokenBudget int    `json:"session_token_budget"`
	DailyTokenBudget   int    `json:"daily_token_budget"`
	OnBudgetExceeded   string `json:"on_budget_exceeded"`
}

func (h *Handler) registerUsageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/usage", h.handleUsage)
}

func (h *Handler) handleUsage(w http.ResponseWriter, r *http.Request) {
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 365 {
			http.Error(w, "days must be between 1 and 365", http.StatusBadRequest)
			return
		}
		days = parsed
	}
	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	toDay := now.Format("2006-01-02")
	from := now.AddDate(0, 0, -(days - 1))
	fromDay := from.Format("2006-01-02")

	response := usageResponse{
		Days:       days,
		From:       fromDay,
		To:         toDay,
		Daily:      make([]usageDay, 0, days),
		ByProvider: []usageBreakdown{},
		ByModel:    []usageBreakdown{},
		ByChannel:  []usageBreakdown{},
		ByAgent:    []usageBreakdown{},
	}
	daily := make(map[string]usage.Totals, days)
	breakdowns := make([]usage.Breakdown, 0)
	store, openErr := memory.OpenSQLiteStore(dir)
	if openErr == nil {
		if rangeStore, ok := any(store).(interface {
			UsageDays(context.Context, string, string) ([]usage.DayTotal, error)
		}); ok {
			items, queryErr := rangeStore.UsageDays(r.Context(), fromDay, toDay)
			if queryErr != nil {
				_ = store.Close()
				http.Error(w, queryErr.Error(), http.StatusInternalServerError)
				return
			}
			for _, item := range items {
				daily[item.Day] = item.Totals
			}
		} else {
			for i := 0; i < days; i++ {
				day := from.AddDate(0, 0, i).Format("2006-01-02")
				totals, queryErr := store.DailyUsage(r.Context(), day)
				if queryErr != nil {
					_ = store.Close()
					http.Error(w, queryErr.Error(), http.StatusInternalServerError)
					return
				}
				daily[day] = totals
			}
		}
		items, queryErr := store.UsageBreakdown(r.Context(), fromDay, toDay)
		_ = store.Close()
		if queryErr != nil {
			http.Error(w, queryErr.Error(), http.StatusInternalServerError)
			return
		}
		breakdowns = items
	} else if !errors.Is(openErr, os.ErrNotExist) {
		http.Error(w, openErr.Error(), http.StatusInternalServerError)
		return
	}
	var aggregate usage.Totals
	for i := 0; i < days; i++ {
		day := from.AddDate(0, 0, i).Format("2006-01-02")
		totals := daily[day]
		aggregate = addUsageTotals(aggregate, totals)
		response.Daily = append(response.Daily, usageDay{Day: day, usageTotals: publicUsageTotals(totals)})
	}
	response.Today = publicUsageTotals(daily[toDay])
	response.ByProvider = groupUsageBreakdown(breakdowns, "provider")
	response.ByModel = groupUsageBreakdown(breakdowns, "model")
	response.ByChannel = groupUsageBreakdown(breakdowns, "channel")
	response.ByAgent = groupUsageBreakdown(breakdowns, "agent")
	response.Totals = publicUsageTotals(aggregate)
	response.Governance = loadUsageGovernance(h.configPath)
	writeJSON(w, http.StatusOK, response)
}

func loadUsageGovernance(configPath string) usageGovernance {
	cfg, err := config.LoadConfig(configPath)
	if err != nil || cfg == nil {
		return usageGovernance{OnBudgetExceeded: "stop"}
	}
	gov := cfg.Agents.Defaults.UsageGovernance
	return usageGovernance{
		SessionTokenBudget: gov.SessionTokenBudget,
		DailyTokenBudget:   gov.DailyTokenBudget,
		OnBudgetExceeded:   gov.EffectiveOnBudgetExceeded(),
	}
}

func addUsageTotals(a, b usage.Totals) usage.Totals {
	return usage.Totals{
		Calls:        a.Calls + b.Calls,
		InputTokens:  a.InputTokens + b.InputTokens,
		OutputTokens: a.OutputTokens + b.OutputTokens,
		TotalTokens:  a.TotalTokens + b.TotalTokens,
		CostNanos:    a.CostNanos + b.CostNanos,
	}
}

func publicUsageTotals(t usage.Totals) usageTotals {
	return usageTotals{
		Calls:        t.Calls,
		InputTokens:  t.InputTokens,
		OutputTokens: t.OutputTokens,
		TotalTokens:  t.TotalTokens,
		CostNanos:    t.CostNanos,
		CostUSD:      float64(t.CostNanos) / 1_000_000_000,
	}
}

func groupUsageBreakdown(items []usage.Breakdown, dimension string) []usageBreakdown {
	grouped := make(map[string]usageBreakdown)
	for _, item := range items {
		key := ""
		switch dimension {
		case "provider":
			key = item.Provider
		case "model":
			key = item.Model + "\x00" + item.ModelID
		case "channel":
			key = item.Channel
		case "agent":
			key = item.AgentID
		}
		existing := grouped[key]
		switch dimension {
		case "provider":
			existing.Provider = item.Provider
		case "model":
			existing.Model = item.Model
			existing.ModelID = item.ModelID
		case "channel":
			existing.Channel = item.Channel
		case "agent":
			existing.AgentID = item.AgentID
		}
		existing.usageTotals = publicUsageTotals(addUsageTotals(usage.Totals{
			Calls:        existing.Calls,
			InputTokens:  existing.InputTokens,
			OutputTokens: existing.OutputTokens,
			TotalTokens:  existing.TotalTokens,
			CostNanos:    existing.CostNanos,
		}, item.Totals))
		grouped[key] = existing
	}
	result := make([]usageBreakdown, 0, len(grouped))
	for _, item := range grouped {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TotalTokens == result[j].TotalTokens {
			return result[i].Provider+result[i].Model+result[i].Channel+result[i].AgentID <
				result[j].Provider+result[j].Model+result[j].Channel+result[j].AgentID
		}
		return result[i].TotalTokens > result[j].TotalTokens
	})
	return result
}
