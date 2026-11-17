package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/biaobiaobiao108/gogoclaw/pkg/tokenizer"
)

func TestMemoryPromptBudgetIncludesOnlyLongTermMemory(t *testing.T) {
	workspace := t.TempDir()
	store := NewMemoryStore(workspace)
	longTerm := strings.Repeat("长期记忆 abc 🌟\n", 10000)
	if err := os.WriteFile(store.memoryFile, []byte(longTerm), 0o600); err != nil {
		t.Fatal(err)
	}
	date := time.Now().Format("20060102")
	dailyDir := filepath.Join(workspace, "memory", date[:6])
	if err := os.MkdirAll(dailyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dailyDir, date+".md"), []byte("daily note sentinel\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{1, 64, 512, 2048} {
		context := store.GetMemoryContextWithBudget(budget)
		if !utf8.ValidString(context) || tokenizer.EstimateTextTokens(context) > budget {
			t.Fatalf("invalid excerpt for budget %d", budget)
		}
		if strings.Contains(context, "daily note sentinel") {
			t.Fatalf("daily note was injected at budget %d", budget)
		}
		if budget >= 512 && !strings.Contains(context, "长期记忆") {
			t.Fatalf("missing long-term memory at budget %d", budget)
		}
	}
	if got, err := os.ReadFile(store.memoryFile); err != nil || string(got) != longTerm {
		t.Fatal("prompt trimming modified original memory")
	}
}
