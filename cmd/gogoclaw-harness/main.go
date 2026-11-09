package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/harness"
)

func main() {
	var (
		scenarioPath = flag.String("scenario", "", "path to a JSON scenario file, suite file, or directory")
		reportPath   = flag.String("report", "", "optional path for the JSON report")
		timeout      = flag.Duration("timeout", 30*time.Second, "overall suite timeout")
	)
	flag.Parse()
	if *scenarioPath == "" {
		fmt.Fprintln(os.Stderr, "gogoclaw-harness: -scenario is required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report, err := harness.RunSpecPath(ctx, *scenarioPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gogoclaw-harness: %v\n", err)
		os.Exit(2)
	}
	if *reportPath != "" {
		if err := report.Save(*reportPath); err != nil {
			fmt.Fprintf(os.Stderr, "gogoclaw-harness: %v\n", err)
			os.Exit(2)
		}
	} else if err := report.WriteJSON(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "gogoclaw-harness: %v\n", err)
		os.Exit(2)
	}
	os.Exit(report.ExitCode())
}
