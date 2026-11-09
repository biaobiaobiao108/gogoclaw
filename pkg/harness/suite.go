package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const reportVersion = 1

// Report is the stable machine-readable output of a scenario suite.
type Report struct {
	Version    int              `json:"version"`
	Passed     bool             `json:"passed"`
	Error      string           `json:"error,omitempty"`
	DurationMS int64            `json:"duration_ms"`
	Scenarios  []ScenarioReport `json:"scenarios"`
}

type ScenarioReport struct {
	Name       string     `json:"name"`
	Passed     bool       `json:"passed"`
	Error      string     `json:"error,omitempty"`
	DurationMS int64      `json:"duration_ms"`
	Trace      Trace      `json:"trace"`
	Usage      TokenUsage `json:"usage"`
}

// TokenUsage aggregates provider-reported usage across a scenario.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// SuiteOptions configures scenario execution across a suite.
type SuiteOptions struct {
	Parallel    bool
	Concurrency int
	Filter      string
	Verbose     bool
	LogProgress func(scenario string, index, total int, passed bool, duration time.Duration, err error)
}

// RunSuite executes scenarios sequentially, evaluates each scenario's optional
// expectation, and returns a structured report instead of stopping at the
// first failure.
func RunSuite(ctx context.Context, scenarios []Scenario) Report {
	return RunSuiteWithOptions(ctx, scenarios, SuiteOptions{})
}

// RunSuiteWithOptions executes scenarios with concurrency, filtering, and
// optional progress callbacks.
func RunSuiteWithOptions(ctx context.Context, scenarios []Scenario, opts SuiteOptions) Report {
	if strings.TrimSpace(opts.Filter) != "" {
		re, err := regexp.Compile(opts.Filter)
		if err != nil {
			return Report{
				Version: reportVersion,
				Passed:  false,
				Error:   fmt.Sprintf("harness: invalid filter regexp %q: %v", opts.Filter, err),
			}
		}
		filtered := make([]Scenario, 0, len(scenarios))
		for _, s := range scenarios {
			if re.MatchString(s.Name) {
				filtered = append(filtered, s)
			}
		}
		scenarios = filtered
	}

	started := time.Now()
	report := Report{Version: reportVersion, Passed: len(scenarios) > 0}
	if len(scenarios) == 0 {
		report.Error = "harness: suite contains no scenarios"
		return report
	}

	total := len(scenarios)
	if opts.Parallel {
		concurrency := opts.Concurrency
		if concurrency <= 0 {
			concurrency = 4
		}
		if concurrency > total {
			concurrency = total
		}
		reports := make([]ScenarioReport, total)
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		for i, sc := range scenarios {
			wg.Add(1)
			go func(idx int, scenario Scenario) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				rep := runOneScenario(ctx, scenario)
				reports[idx] = rep
				if opts.LogProgress != nil {
					var err error
					if rep.Error != "" {
						err = fmt.Errorf("%s", rep.Error)
					}
					opts.LogProgress(rep.Name, idx+1, total, rep.Passed, time.Duration(rep.DurationMS)*time.Millisecond, err)
				}
			}(i, sc)
		}
		wg.Wait()
		report.Scenarios = reports
		for _, scReport := range reports {
			if !scReport.Passed {
				report.Passed = false
			}
		}
	} else {
		for i, scenario := range scenarios {
			scReport := runOneScenario(ctx, scenario)
			if !scReport.Passed {
				report.Passed = false
			}
			if opts.LogProgress != nil {
				var err error
				if scReport.Error != "" {
					err = fmt.Errorf("%s", scReport.Error)
				}
				opts.LogProgress(scReport.Name, i+1, total, scReport.Passed, time.Duration(scReport.DurationMS)*time.Millisecond, err)
			}
			report.Scenarios = append(report.Scenarios, scReport)
		}
	}
	report.DurationMS = time.Since(started).Milliseconds()
	return report
}

func runOneScenario(ctx context.Context, scenario Scenario) ScenarioReport {
	scenarioStarted := time.Now()
	result, runErr := runScenarioSafely(ctx, scenario)
	scenarioReport := ScenarioReport{
		Name:       scenario.Name,
		DurationMS: time.Since(scenarioStarted).Milliseconds(),
	}
	if runErr != nil {
		scenarioReport.Error = runErr.Error()
	} else if scenario.Expectation != nil {
		if err := result.Verify(*scenario.Expectation); err != nil {
			scenarioReport.Error = err.Error()
		}
	} else if problems := result.ValidateTrace(); len(problems) > 0 {
		scenarioReport.Error = "invalid trace: " + strings.Join(problems, "; ")
	}
	if result != nil {
		scenarioReport.Trace = result.Snapshot()
		scenarioReport.Usage = result.TokenUsage()
	}
	scenarioReport.Passed = scenarioReport.Error == ""
	return scenarioReport
}

func (r *Result) TokenUsage() TokenUsage {
	var total TokenUsage
	if r == nil {
		return total
	}
	for _, call := range r.ModelCalls {
		if call.Response == nil || call.Response.Usage == nil {
			continue
		}
		total.PromptTokens += call.Response.Usage.PromptTokens
		total.CompletionTokens += call.Response.Usage.CompletionTokens
		total.TotalTokens += call.Response.Usage.TotalTokens
	}
	return total
}

func runScenarioSafely(ctx context.Context, scenario Scenario) (result *Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("harness: scenario panic: %v", recovered)
		}
	}()
	return Run(ctx, scenario)
}

func (r Report) WriteJSON(writer io.Writer) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(r); err != nil {
		return fmt.Errorf("harness: encode report: %w", err)
	}
	return nil
}

func (r Report) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("harness: create report directory: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("harness: create report: %w", err)
	}
	defer file.Close()
	if err := r.WriteJSON(file); err != nil {
		return err
	}
	return nil
}

// ExitCode allows CLI callers to map suite success to a process code.
func (r Report) ExitCode() int {
	if r.Passed {
		return 0
	}
	return 1
}
