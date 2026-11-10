//go:build integration

package openai_compat

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveContract_OpenAICompatibleChatCompletion(t *testing.T) {
	apiBase := strings.TrimSpace(os.Getenv("GOGOCLAW_LIVE_LLM_API_BASE"))
	apiKey := strings.TrimSpace(os.Getenv("GOGOCLAW_LIVE_LLM_API_KEY"))
	model := strings.TrimSpace(os.Getenv("GOGOCLAW_LIVE_LLM_MODEL"))
	if apiBase == "" || apiKey == "" || model == "" {
		t.Skip("set GOGOCLAW_LIVE_LLM_API_BASE, GOGOCLAW_LIVE_LLM_API_KEY, and GOGOCLAW_LIVE_LLM_MODEL to run the live contract")
	}

	provider := NewProvider(apiKey, apiBase, "", WithRequestTimeout(45*time.Second))
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Second)
	defer cancel()

	response, err := provider.Chat(
		ctx,
		[]Message{{Role: "user", Content: "Reply with exactly: gogoclaw-contract-ok"}},
		nil,
		model,
		map[string]any{"max_tokens": 16},
	)
	if err != nil {
		t.Fatalf("live OpenAI-compatible chat request failed: %v", err)
	}
	if response == nil || strings.TrimSpace(response.Content) == "" {
		t.Fatalf("live provider returned an empty response: %+v", response)
	}
}
