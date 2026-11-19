package evolution

import (
	"context"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

type modelUsageKey struct{}

func callEvolutionModel(ctx context.Context, provider providers.LLMProvider, model, stage string,
	system, prompt string, timeout time.Duration, maxTokens int, temperature float64,
) (*providers.LLMResponse, error) {
	callCtx, cancel := withLLMCallTimeout(ctx, timeout)
	defer cancel()
	response, err := provider.Chat(callCtx, []providers.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: prompt},
	}, nil, model, map[string]any{"temperature": temperature, "max_tokens": maxTokens})
	if record, ok := ctx.Value(modelUsageKey{}).(func(string, string, providers.UsageInfo)); ok &&
		response != nil && response.Usage != nil {
		record(stage, model, *response.Usage)
	}
	return response, err
}
