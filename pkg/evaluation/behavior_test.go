package evaluation

import (
	"context"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/harness"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

type behaviorFixtureProvider struct{}

func (*behaviorFixtureProvider) GetDefaultModel() string { return "fixture-model" }

func (*behaviorFixtureProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	lastUser := ""
	hasToolResult := false
	for _, message := range messages {
		if message.Role == "user" {
			lastUser = message.Content
		}
		if message.Role == "tool" {
			hasToolResult = true
		}
	}
	if strings.Contains(lastUser, "artifacts/answer.txt") && !hasToolResult {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{harness.ToolCall(
			"eval-write",
			"write_file",
			map[string]any{"path": "artifacts/answer.txt", "content": "42"},
		)}}, nil
	}
	if strings.Contains(lastUser, "Reply with only the code") {
		return &providers.LLMResponse{Content: "cedar-8041"}, nil
	}
	if hasToolResult {
		return &providers.LLMResponse{Content: "Created the artifact."}, nil
	}
	return &providers.LLMResponse{Content: "Stored."}, nil
}

func TestRunModelBehaviorProducesSeparateObservableReport(t *testing.T) {
	report := RunModelBehavior(context.Background(), &behaviorFixtureProvider{}, "fixture-model")
	if !report.Passed {
		t.Fatalf("behavior evaluation failed: %+v", report)
	}
	if report.Type != "model_behavior" || report.Version != behaviorReportVersion {
		t.Fatalf("report type/version = %q/%d", report.Type, report.Version)
	}
	if len(report.Tasks) != 2 {
		t.Fatalf("task count = %d, want 2", len(report.Tasks))
	}
	if report.Tasks[0].ID != "tool_write_artifact" || len(report.Tasks[0].ToolCalls) != 1 || report.Tasks[0].ArtifactSHA256 == "" {
		t.Fatalf("tool task did not record observable artifact evidence: %+v", report.Tasks[0])
	}
	if report.Tasks[1].ID != "multi_turn_session_recall" || report.Tasks[1].Final != "cedar-8041" {
		t.Fatalf("session recall task evidence = %+v", report.Tasks[1])
	}
}

func TestRunModelBehaviorRejectsMissingProviderInputs(t *testing.T) {
	report := RunModelBehavior(context.Background(), nil, "")
	if report.Passed || report.Error == "" || len(report.Tasks) != 0 {
		t.Fatalf("invalid evaluation inputs produced a passing report: %+v", report)
	}
}
