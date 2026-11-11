package harness

import (
	"context"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

type compactionHarnessProvider struct {
	mainCalls         int
	summaryCalls      int
	continuedMessages []providers.Message
}

func (*compactionHarnessProvider) GetDefaultModel() string { return "harness-model" }

func (p *compactionHarnessProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	if len(messages) > 0 && strings.Contains(messages[0].Content, "Summarize task progress faithfully") {
		p.summaryCalls++
		return &providers.LLMResponse{Content: "The request asks to find marker amber-2851."}, nil
	}

	p.mainCalls++
	p.continuedMessages = append([]providers.Message(nil), messages...)
	return &providers.LLMResponse{Content: "The marker is amber-2851."}, nil
}

func TestHarnessContinuesOversizedRequestAfterContextCompaction(t *testing.T) {
	provider := &compactionHarnessProvider{}
	userMessage := strings.Repeat("Background notes for the requested scan. ", 1500) + "Find marker amber-2851."
	result, err := Run(context.Background(), Scenario{
		Name: "oversized-request-context-compaction", UserMessage: userMessage,
		ContextWindow: 8000, MaxTokens: 500, Provider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{NoError: true, FinalContains: []string{"amber-2851"}}); err != nil {
		t.Fatal(err)
	}
	if provider.summaryCalls == 0 {
		t.Fatal("oversized user request did not trigger active-turn summarization")
	}
	if provider.mainCalls != 1 {
		t.Fatalf("main model calls = %d, want 1", provider.mainCalls)
	}
	foundSummary := false
	for _, message := range provider.continuedMessages {
		if strings.Contains(message.Content, "Summary of the original request") && strings.Contains(message.Content, "amber-2851") {
			foundSummary = true
			break
		}
	}
	if !foundSummary {
		t.Fatal("model request did not receive the compacted original request")
	}
}
