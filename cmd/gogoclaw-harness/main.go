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
		filter       = flag.String("filter", "", "optional regex filter for scenario names")
		parallel     = flag.Bool("parallel", false, "run scenarios concurrently")
		concurrency  = flag.Int("concurrency", 4, "maximum concurrency when -parallel is enabled")
		timeout      = flag.Duration("timeout", 60*time.Second, "overall suite timeout")
		verbose      = flag.Bool("v", false, "log scenario execution progress to stderr")
	)
	flag.Parse()
	if *scenarioPath == "" {
		fmt.Fprintln(os.Stderr, "gogoclaw-harness: -scenario is required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	opts := harness.SuiteOptions{
		Parallel:    *parallel,
		Concurrency: *concurrency,
		Filter:      *filter,
		Verbose:     *verbose,
	}
	if *verbose {
		opts.LogProgress = func(scenario string, index, total int, passed bool, duration time.Duration, err error) {
			status := "PASS"
			if !passed {
				status = "FAIL"
			}
			msg := fmt.Sprintf("[%s] (%d/%d) %s in %v", status, index, total, scenario, duration.Round(time.Millisecond))
			if err != nil {
				msg += fmt.Sprintf(": %v", err)
			}
			fmt.Fprintln(os.Stderr, msg)
		}
	}
	report, err := harness.RunSpecPathWithOptions(ctx, *scenarioPath, opts)
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
