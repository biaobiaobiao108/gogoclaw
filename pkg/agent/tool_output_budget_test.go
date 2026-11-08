package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tokenizer"
)

func TestCompactToolResultForPromptPersistsOversizedOutput(t *testing.T) {
	workspace := t.TempDir()
	al := &AgentLoop{}
	ts := &turnState{agent: &AgentInstance{Workspace: workspace}}
	content := strings.Repeat("head-data ", 5000) +
		strings.Repeat("middle-data ", 5000) +
		strings.Repeat("tail-data ", 5000)

	result := al.compactToolResultForPrompt(ts, "exec", "call-1", content)
	if result.Artifact == "" {
		t.Fatal("expected compacted output artifact")
	}
	if !strings.Contains(result.Content, "Tool result compacted") {
		t.Fatalf("expected compaction notice, got %q", result.Content[:min(len(result.Content), 200)])
	}
	if !strings.Contains(result.Content, "head-data") || !strings.Contains(result.Content, "tail-data") {
		t.Fatal("compacted content should retain both the beginning and end")
	}
	if strings.Contains(result.Content, "middle-data") {
		t.Fatal("compacted content should omit the oversized middle")
	}
	data, err := os.ReadFile(result.Artifact)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if string(data) != content {
		t.Fatal("artifact should preserve the complete tool output")
	}
}

func TestCompactToolResultForPromptUsesSmallerBudgetForSmallContext(t *testing.T) {
	workspace := t.TempDir()
	al := &AgentLoop{}
	content := strings.Repeat("x", 20000)

	largeContext := al.compactToolResultForPrompt(&turnState{
		agent: &AgentInstance{Workspace: workspace, ContextWindow: 128000},
	}, "exec", "large", content)
	smallContext := al.compactToolResultForPrompt(&turnState{
		agent: &AgentInstance{Workspace: workspace, ContextWindow: 32000},
	}, "exec", "small", content)

	if strings.Contains(largeContext.Content, "Tool result compacted") {
		t.Fatal("large context should retain a 20K-character tool result")
	}
	if !strings.Contains(smallContext.Content, "Tool result compacted") {
		t.Fatal("small context should compact the same tool result")
	}
	if smallContext.Artifact == "" {
		t.Fatal("compacted result should persist the full artifact")
	}
}

func TestCompactToolResultForPromptBudgetsCJKByTokens(t *testing.T) {
	workspace := t.TempDir()
	al := &AgentLoop{}
	content := strings.Repeat("云", 12000)
	result := al.compactToolResultForPrompt(&turnState{
		agent: &AgentInstance{Workspace: workspace, ContextWindow: 32000},
	}, "exec", "cjk", content)

	if !strings.Contains(result.Content, "Tool result compacted") {
		t.Fatal("expected CJK output to be compacted")
	}
	if tokens := tokenizer.EstimateTextTokens(result.Content); tokens > toolResultPromptTokenBudget(32000) {
		t.Fatalf("compacted CJK output uses %d tokens, want at most %d", tokens, toolResultPromptTokenBudget(32000))
	}
}

func TestCompactToolResultForPromptReducesEstimatedTokens(t *testing.T) {
	workspace := t.TempDir()
	al := &AgentLoop{}
	content := strings.Repeat("0123456789", 30000)
	result := al.compactToolResultForPrompt(&turnState{
		agent: &AgentInstance{Workspace: workspace, ContextWindow: 32000},
	}, "exec", "tokens", content)

	before := EstimateMessageTokens(providers.Message{Role: "tool", Content: content})
	after := EstimateMessageTokens(providers.Message{Role: "tool", Content: result.Content})
	if after*2 >= before {
		t.Fatalf("compaction should more than halve prompt tokens: before=%d after=%d", before, after)
	}
	t.Logf("tool output prompt tokens: %d -> %d (%.1f%% reduction)", before, after, 100*(1-float64(after)/float64(before)))
}
