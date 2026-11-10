package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/harness"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

const behaviorReportVersion = 1

// BehaviorReport contains task outcomes from live model behavior checks.
// It intentionally differs from the deterministic harness trajectory report.
type BehaviorReport struct {
	Type       string       `json:"type"`
	Version    int          `json:"version"`
	Provider   string       `json:"provider"`
	Model      string       `json:"model"`
	StartedAt  time.Time    `json:"started_at"`
	DurationMS int64        `json:"duration_ms"`
	Passed     bool         `json:"passed"`
	Error      string       `json:"error,omitempty"`
	Tasks      []TaskResult `json:"tasks"`
}

// TaskResult records observable evidence for one real-model task.
type TaskResult struct {
	ID             string             `json:"id"`
	Passed         bool               `json:"passed"`
	Error          string             `json:"error,omitempty"`
	DurationMS     int64              `json:"duration_ms"`
	Final          string             `json:"final,omitempty"`
	ToolCalls      []string           `json:"tool_calls,omitempty"`
	ArtifactSHA256 string             `json:"artifact_sha256,omitempty"`
	TokenUsage     harness.TokenUsage `json:"token_usage"`
}

// RunModelBehavior executes a small, fixed task set against the supplied live
// provider. Each task gets a new workspace and session, and is scored only from
// observable outputs rather than from provider connectivity or scripted calls.
func RunModelBehavior(ctx context.Context, provider providers.LLMProvider, model string) BehaviorReport {
	started := time.Now().UTC()
	report := BehaviorReport{
		Type:      "model_behavior",
		Version:   behaviorReportVersion,
		Provider:  "openai_compatible",
		Model:     model,
		StartedAt: started,
		Passed:    provider != nil && strings.TrimSpace(model) != "",
	}
	if provider == nil || strings.TrimSpace(model) == "" {
		report.Passed = false
		report.DurationMS = time.Since(started).Milliseconds()
		report.Error = "provider and model are required"
		return report
	}

	for _, run := range []func(context.Context, providers.LLMProvider, string) TaskResult{
		runWriteArtifactTask,
		runSessionRecallTask,
	} {
		result := run(ctx, provider, model)
		report.Tasks = append(report.Tasks, result)
		if !result.Passed {
			report.Passed = false
		}
	}
	report.DurationMS = time.Since(started).Milliseconds()
	return report
}

func runWriteArtifactTask(ctx context.Context, provider providers.LLMProvider, model string) TaskResult {
	started := time.Now()
	result := TaskResult{ID: "tool_write_artifact"}
	workspace, err := os.MkdirTemp("", "gogoclaw-model-eval-artifact-*")
	if err != nil {
		return finishTask(result, started, err)
	}
	defer os.RemoveAll(workspace)

	path := filepath.Join(workspace, "artifacts", "answer.txt")
	run, err := harness.Run(ctx, harness.Scenario{
		Name:              "model-behavior-tool-write-artifact",
		UserMessage:       "Use the write_file tool to create artifacts/answer.txt with exactly this content: 42. After the file is written, reply briefly.",
		Model:             model,
		Provider:          provider,
		Workspace:         workspace,
		RecordPath:        filepath.Join(workspace, "provider-calls.json"),
		ProductionIngress: true,
		Timeout:           90 * time.Second,
		MaxToolIterations: 4,
		Tools:             []tools.Tool{tools.NewWriteFileTool(workspace, true)},
	})
	if err != nil {
		return finishTask(result, started, err)
	}
	result.Final = strings.TrimSpace(run.Content)
	result.TokenUsage = run.TokenUsage()
	for _, call := range run.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, call.Name)
	}

	var failures []string
	if err := run.Verify(harness.Expect{NoError: true, Tools: []string{"write_file"}}); err != nil {
		failures = append(failures, err.Error())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		failures = append(failures, fmt.Sprintf("read artifact: %v", err))
	} else if string(contents) != "42" {
		failures = append(failures, fmt.Sprintf("artifact content = %q, want %q", string(contents), "42"))
	} else {
		digest := sha256.Sum256(contents)
		result.ArtifactSHA256 = hex.EncodeToString(digest[:])
	}
	if len(failures) > 0 {
		return finishTask(result, started, fmt.Errorf("%s", strings.Join(failures, "; ")))
	}
	return finishTask(result, started, nil)
}

func runSessionRecallTask(ctx context.Context, provider providers.LLMProvider, model string) TaskResult {
	started := time.Now()
	result := TaskResult{ID: "multi_turn_session_recall"}
	const marker = "cedar-8041"
	workspace, err := os.MkdirTemp("", "gogoclaw-model-eval-session-*")
	if err != nil {
		return finishTask(result, started, err)
	}
	defer os.RemoveAll(workspace)
	run, err := harness.Run(ctx, harness.Scenario{
		Name:              "model-behavior-session-recall",
		UserMessage:       "Remember this code for my next message: " + marker + ". Acknowledge briefly.",
		Messages:          []string{"What code did I ask you to remember? Reply with only the code."},
		Model:             model,
		Provider:          provider,
		Workspace:         workspace,
		RecordPath:        filepath.Join(workspace, "provider-calls.json"),
		ProductionIngress: true,
		Timeout:           90 * time.Second,
	})
	if err != nil {
		return finishTask(result, started, err)
	}
	result.Final = strings.TrimSpace(run.Content)
	result.TokenUsage = run.TokenUsage()
	var failures []string
	if err := run.Verify(harness.Expect{NoError: true, TurnCount: 2}); err != nil {
		failures = append(failures, err.Error())
	}
	if result.Final != marker {
		failures = append(failures, fmt.Sprintf("final answer = %q, want %q", result.Final, marker))
	}
	if len(failures) > 0 {
		return finishTask(result, started, fmt.Errorf("%s", strings.Join(failures, "; ")))
	}
	return finishTask(result, started, nil)
}

func finishTask(result TaskResult, started time.Time, err error) TaskResult {
	result.DurationMS = time.Since(started).Milliseconds()
	result.Passed = err == nil
	if err != nil {
		result.Error = err.Error()
	}
	return result
}
