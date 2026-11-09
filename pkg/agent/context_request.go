package agent

import (
	"context"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

// fitContextForRequest is the single request-time authority for fitting an
// active turn into the model context. It keeps trimming and active-turn
// compaction out of provider adapters and the turn state machine.
func fitContextForRequest(
	ctx context.Context,
	provider providers.LLMProvider,
	model string,
	messages []providers.Message,
	currentTurnStart int,
	contextWindow int,
	maxTokens int,
	toolDefs []providers.ToolDefinition,
) ([]providers.Message, int, error) {
	trimmed, nextStart, fit := fitActiveTurn(
		messages,
		currentTurnStart,
		contextWindow,
		toolDefs,
		maxTokens,
	)
	if fit {
		return trimmed, nextStart, nil
	}

	compacted, err := compactActiveTurn(
		ctx,
		provider,
		model,
		trimmed,
		nextStart,
		contextWindow,
		maxTokens,
		toolDefs,
	)
	if err != nil {
		return nil, currentTurnStart, err
	}
	return compacted, nextStart, nil
}
