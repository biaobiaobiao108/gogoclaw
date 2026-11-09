package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	Name       string `json:"name"`
	Passed     bool   `json:"passed"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Trace      Trace  `json:"trace"`
}

// RunSuite executes scenarios sequentially, evaluates each scenario's optional
// expectation, and returns a structured report instead of stopping at the
// first failure.
func RunSuite(ctx context.Context, scenarios []Scenario) Report {
	started := time.Now()
	report := Report{Version: reportVersion, Passed: len(scenarios) > 0}
	if len(scenarios) == 0 {
		report.Error = "harness: suite contains no scenarios"
	}
	for _, scenario := range scenarios {
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
		}
		scenarioReport.Passed = scenarioReport.Error == ""
		if !scenarioReport.Passed {
			report.Passed = false
		}
		report.Scenarios = append(report.Scenarios, scenarioReport)
	}
	report.DurationMS = time.Since(started).Milliseconds()
	return report
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
