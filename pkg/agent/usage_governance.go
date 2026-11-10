package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	usagepkg "github.com/biaobiaobiao108/gogoclaw/pkg/usage"
)

func costRatesFromModelConfig(modelCfg *config.ModelConfig) usagepkg.CostRates {
	if modelCfg == nil {
		return usagepkg.CostRates{}
	}
	return usagepkg.CostRates{
		InputUSDPerMillion:  modelCfg.InputCostPerMillionUSD,
		OutputUSDPerMillion: modelCfg.OutputCostPerMillionUSD,
	}
}

func executionUsageCall(exec *turnExecution) usageCall {
	if exec == nil {
		return usageCall{}
	}
	provider := resolvedCandidateProvider(exec.activeCandidates, "")
	if provider == "" && exec.activeModelConfig != nil {
		provider = exec.activeModelConfig.Provider
	}
	modelID := resolvedCandidateModel(exec.activeCandidates, exec.llmModel)
	return usageCall{
		Provider:  provider,
		Model:     exec.llmModelName,
		ModelID:   modelID,
		CostRates: costRatesFromModelConfig(exec.activeModelConfig),
	}
}

func usageAccountingUnavailableError() error {
	return fmt.Errorf("usage budget is configured but the session store cannot account for token usage")
}

// enforceUsageBudget checks the worst-case size of the next model call. This is
// deliberately a preflight check: stopping only after a provider response would
// allow the call that exceeded the budget to be billed.
func (ts *turnState) enforceUsageBudget(
	ctx context.Context,
	messages []providers.Message,
	defs []providers.ToolDefinition,
) (blocked bool, message string, reservation *usagepkg.Reservation, err error) {
	if ts == nil || ts.agent == nil || !ts.agent.UsageGovernance.Enabled() {
		return false, "", nil, nil
	}
	store := ts.usageStore()
	ledger := ts.usageLedger()
	if store == nil || ledger == nil {
		return false, "", nil, usageAccountingUnavailableError()
	}
	projected := int64(max(0, ts.agent.MaxTokens))
	for _, msg := range messages {
		projected += int64(max(0, EstimateMessageTokens(msg)))
	}
	projected += int64(max(0, EstimateToolDefsTokens(defs)))

	sessionKey := ts.usageSessionKeyOrDefault()
	day := time.Now().Format("2006-01-02")
	guard := ts.usageGuard()
	if guard == nil {
		guard = usagepkg.NewGuard()
	}
	check, err := guard.CheckAndReserve(
		sessionKey,
		day,
		projected,
		usagepkg.Limits{
			Session: int64(ts.agent.UsageGovernance.SessionTokenBudget),
			Daily:   int64(ts.agent.UsageGovernance.DailyTokenBudget),
		},
		ts.agent.UsageGovernance.EffectiveOnBudgetExceeded() == "warn",
		func() (usagepkg.Totals, usagepkg.Totals, error) {
			sessionUsage, loadErr := store.SessionUsage(ctx, sessionKey)
			if loadErr != nil {
				return usagepkg.Totals{}, usagepkg.Totals{}, fmt.Errorf("read session token usage: %w", loadErr)
			}
			dailyUsage, loadErr := ledger.DailyUsage(ctx, day)
			if loadErr != nil {
				return usagepkg.Totals{}, usagepkg.Totals{}, fmt.Errorf("read daily token usage: %w", loadErr)
			}
			return sessionUsage, dailyUsage, nil
		},
	)
	if err != nil {
		return false, "", nil, err
	}
	if check.Scope == "" {
		return false, "", check.Reservation, nil
	}

	fields := map[string]any{
		"agent_id":         ts.agentID,
		"session_key":      sessionKey,
		"budget_scope":     check.Scope,
		"used_tokens":      check.Used,
		"projected_tokens": projected,
		"budget_tokens":    check.Limit,
	}
	if ts.agent.UsageGovernance.EffectiveOnBudgetExceeded() == "warn" {
		logger.WarnCF("agent", "Token budget exceeded; continuing because usage governance is set to warn", fields)
		return false, "", check.Reservation, nil
	}
	logger.WarnCF("agent", "Token budget exceeded; stopping before the next model call", fields)
	message = fmt.Sprintf(
		"Token budget reached for this %s (%d used, next request reserves %d, limit %d). The task stopped before the next model call. Increase `agents.defaults.usage_governance.%s_token_budget` or start a new session.",
		check.Scope, check.Used, projected, check.Limit, check.Scope,
	)
	if check.Scope == "daily" {
		message = fmt.Sprintf(
			"Daily token budget reached (%d used, next request reserves %d, limit %d). The task stopped before the next model call. Increase `agents.defaults.usage_governance.daily_token_budget` or wait for the next day.",
			check.Used, projected, check.Limit,
		)
	}
	return true, message, nil, nil
}
