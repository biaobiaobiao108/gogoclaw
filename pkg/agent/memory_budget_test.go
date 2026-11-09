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

func TestMemoryPromptBudgetPreservesFilesAndRecentNotes(t *testing.T) {
	store := NewMemoryStore(t.TempDir())
	longTerm := strings.Repeat("长期记忆 abc 🌟\n", 10000)
	if err := store.WriteLongTerm(longTerm); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendToday(strings.Repeat("today's recent fact\n", 10000)); err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{1, 64, 512, 2048} {
		context := store.GetMemoryContextWithBudget(budget)
		if !utf8.ValidString(context) || tokenizer.EstimateTextTokens(context) > budget {
			t.Fatalf("invalid excerpt for budget %d", budget)
		}
		if budget >= 512 && (!strings.Contains(context, "长期记忆") || !strings.Contains(context, "today's recent fact")) {
			t.Fatalf("missing memory source at budget %d", budget)
		}
	}
	if store.ReadLongTerm() != longTerm {
		t.Fatal("prompt trimming modified original memory")
	}
	date := time.Now().Format("20060102")
	if _, err := os.Stat(filepath.Join(store.memoryDir, date[:6], date+".md")); err != nil {
		t.Fatal(err)
	}
}
