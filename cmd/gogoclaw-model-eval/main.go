package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/evaluation"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers/openai_compat"
)

type configuredModelProvider struct {
	*openai_compat.Provider
	model string
}

func (p configuredModelProvider) GetDefaultModel() string {
	return p.model
}

func main() {
	reportPath := flag.String("report", "", "optional path for the model behavior report")
	flag.Parse()

	apiBase := strings.TrimSpace(os.Getenv("GOGOCLAW_EVAL_API_BASE"))
	apiKey := strings.TrimSpace(os.Getenv("GOGOCLAW_EVAL_API_KEY"))
	model := strings.TrimSpace(os.Getenv("GOGOCLAW_EVAL_MODEL"))
	if apiBase == "" || apiKey == "" || model == "" {
		fmt.Fprintln(os.Stderr, "gogoclaw-model-eval: set GOGOCLAW_EVAL_API_BASE, GOGOCLAW_EVAL_API_KEY, and GOGOCLAW_EVAL_MODEL")
		os.Exit(2)
	}

	provider := openai_compat.NewProvider(
		apiKey,
		apiBase,
		"",
		openai_compat.WithProviderName("openai_compatible"),
		openai_compat.WithRequestTimeout(90*time.Second),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	report := evaluation.RunModelBehavior(ctx, configuredModelProvider{Provider: provider, model: model}, model)

	if *reportPath != "" {
		if err := report.Save(*reportPath); err != nil {
			fmt.Fprintf(os.Stderr, "gogoclaw-model-eval: %v\n", err)
			os.Exit(2)
		}
	} else if err := report.WriteJSON(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "gogoclaw-model-eval: %v\n", err)
		os.Exit(2)
	}
	if !report.Passed {
		os.Exit(1)
	}
}
