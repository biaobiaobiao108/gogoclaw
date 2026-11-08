package tokenizer

import (
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func TestEstimateTextTokensDistinguishesASCIIAndCJK(t *testing.T) {
	ascii := EstimateTextTokens(strings.Repeat("a", 700))
	cjk := EstimateTextTokens(strings.Repeat("云", 700))

	if ascii < 190 || ascii > 230 {
		t.Fatalf("ASCII estimate = %d, want roughly 200", ascii)
	}
	if cjk < 700 {
		t.Fatalf("CJK estimate = %d, want at least one token per rune", cjk)
	}
}

func TestEstimateMessageTokensIncludesStructuredFields(t *testing.T) {
	plain := providers.Message{Role: "assistant", Content: "done"}
	withTool := providers.Message{
		Role:    "assistant",
		Content: "done",
		ToolCalls: []providers.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: &providers.FunctionCall{
				Name:      "exec",
				Arguments: `{"command":"go test ./..."}`,
			},
		}},
	}

	if EstimateMessageTokens(withTool) <= EstimateMessageTokens(plain) {
		t.Fatal("tool call metadata should contribute to the estimate")
	}
}
