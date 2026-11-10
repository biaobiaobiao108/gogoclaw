package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/usage"
)

func TestHandleUsageAggregatesSQLiteAccounting(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Agents.Defaults.UsageGovernance = config.UsageGovernanceConfig{
		DailyTokenBudget: 2000,
		OnBudgetExceeded: "stop",
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	store.AddMessage(context.Background(), "session", "user", "hello")
	if err := store.RecordUsage(context.Background(), usage.Record{
		Day:          time.Now().Format("2006-01-02"),
		SessionKeys:  []string{"session"},
		AgentID:      "main",
		Channel:      "pico",
		Provider:     "openai",
		Model:        "fast-model",
		ModelID:      "gpt-test",
		InputTokens:  120,
		OutputTokens: 30,
		TotalTokens:  150,
		CostNanos:    45,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/usage?days=7", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response usageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Totals.TotalTokens != 150 || math.Abs(response.Totals.CostUSD-0.000000045) > 1e-15 {
		t.Fatalf("totals = %+v", response.Totals)
	}
	if len(response.ByProvider) != 1 || response.ByProvider[0].Provider != "openai" {
		t.Fatalf("provider breakdown = %+v", response.ByProvider)
	}
	if math.Abs(response.ByProvider[0].CostUSD-0.000000045) > 1e-15 {
		t.Fatalf("provider cost = %+v", response.ByProvider[0])
	}
	if response.Governance.DailyTokenBudget != 2000 || response.Governance.OnBudgetExceeded != "stop" {
		t.Fatalf("governance = %+v", response.Governance)
	}
}
