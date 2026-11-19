package agent

import (
	"context"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func TestEvolutionUsageRecorderWritesSeparateLedgerRows(t *testing.T) {
	store, err := memory.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := config.DefaultConfig()
	registry := &AgentRegistry{
		cfg: cfg,
		agents: map[string]*AgentInstance{
			"main": {ID: "main", usageLedger: store},
		},
	}
	record := evolutionUsageRecorder(registry, cfg)
	for _, stage := range []string{"judge", "cluster", "draft"} {
		record(stage, "fixture", providers.UsageInfo{PromptTokens: 80, CompletionTokens: 20, TotalTokens: 100})
	}
	ctx := context.Background()
	day := time.Now().Format("2006-01-02")
	rows, err := store.UsageBreakdown(ctx, day, day)
	if err != nil || len(rows) != 3 {
		t.Fatalf("usage rows = %+v, err = %v", rows, err)
	}
	channels := map[string]bool{"evolution-judge": true, "evolution-cluster": true, "evolution-draft": true}
	for _, row := range rows {
		if !channels[row.Channel] || row.AgentID != "main" || row.Calls != 1 ||
			row.InputTokens != 80 || row.OutputTokens != 20 || row.TotalTokens != 100 {
			t.Fatalf("unexpected usage row: %+v", row)
		}
		delete(channels, row.Channel)
	}
}
