package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/tools"
)

type planStep struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

// turnPlanTool keeps a plan scoped to a single execution, not a shared agent.
type turnPlanTool struct{ turn *turnState }

func (*turnPlanTool) Name() string { return "update_plan" }
func (*turnPlanTool) Description() string {
	return "For multi-step tasks, create or update a concise plan. Keep at most one step in progress. Skip this tool for simple requests."
}
func (*turnPlanTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"steps": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
			"text":   map[string]any{"type": "string"},
			"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
		}, "required": []string{"text", "status"}}},
	}, "required": []string{"steps"}}
}
func (t *turnPlanTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	raw, ok := args["steps"].([]any)
	if !ok || len(raw) == 0 || len(raw) > 20 {
		return tools.ErrorResult("Provide between 1 and 20 plan steps.")
	}
	steps := make([]planStep, 0, len(raw))
	inProgress := 0
	for _, entry := range raw {
		item, ok := entry.(map[string]any)
		if !ok {
			return tools.ErrorResult("Each plan step must be an object.")
		}
		text, _ := item["text"].(string)
		status, _ := item["status"].(string)
		text = strings.TrimSpace(text)
		if text == "" || len([]rune(text)) > 300 {
			return tools.ErrorResult("Each plan step must have 1 to 300 characters.")
		}
		switch status {
		case "pending", "completed":
		case "in_progress":
			inProgress++
		default:
			return tools.ErrorResult("Invalid plan step status.")
		}
		steps = append(steps, planStep{Text: text, Status: status})
	}
	if inProgress > 1 {
		return tools.ErrorResult("Only one plan step can be in progress.")
	}
	t.turn.mu.Lock()
	t.turn.plan = steps
	t.turn.mu.Unlock()
	return tools.SilentResult(fmt.Sprintf("Plan updated with %d steps.", len(steps)))
}
