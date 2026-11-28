// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

func toolCallFingerprint(name string, args map[string]any) string {
	argsJSON, _ := json.Marshal(args)
	return strings.ToLower(strings.TrimSpace(name)) + "\x00" + string(argsJSON)
}

func canExecuteInParallel(al *AgentLoop, cfg *config.Config, ts *turnState, toolCalls []providers.ToolCall) bool {
	if al == nil || cfg == nil || ts == nil {
		return false
	}
	if al.hooks != nil && al.hooks.HasActiveHooks() {
		return false
	}
	seenFingerprints := make(map[string]struct{}, len(toolCalls))
	for _, tc := range toolCalls {
		policy := ts.toolPolicy(tc.Name)
		if !policy.ReadOnly || !policy.Idempotent {
			return false
		}
		fingerprint := toolCallFingerprint(tc.Name, tc.Arguments)
		if _, duplicate := seenFingerprints[fingerprint]; duplicate || ts.readOnlyCallSeen(fingerprint) {
			return false
		}
		seenFingerprints[fingerprint] = struct{}{}
		if required, _, _ := tools.StaticApprovalPolicy(cfg.Tools.Approval, al.publicExposure, tc.Name, tc.Arguments); required {
			return false
		}
		if !turnProfileToolAllowed(ts.profile, tc.Name) {
			return false
		}
	}
	return true
}

func toolResultHasUsableContent(result *tools.ToolResult) bool {
	if result == nil || result.IsError {
		return false
	}
	content := strings.TrimSpace(result.ContentForLLM())
	if content == "" {
		return false
	}
	lower := strings.ToLower(content)
	if strings.HasPrefix(lower, "no results for:") || strings.HasPrefix(lower, "no results found") {
		return false
	}

	var fetched struct {
		Status int    `json:"status"`
		Text   string `json:"text"`
	}
	if strings.HasPrefix(content, "{") && json.Unmarshal([]byte(content), &fetched) == nil && fetched.Status != 0 {
		return fetched.Status >= 200 && fetched.Status < 400 && strings.TrimSpace(fetched.Text) != ""
	}
	return true
}

func toolErrorSummary(cfg *config.Config, result *tools.ToolResult) string {
	if result == nil || !result.IsError {
		return ""
	}
	content := strings.TrimSpace(result.ContentForLLM())
	if content == "" && result.Err != nil {
		content = strings.TrimSpace(result.Err.Error())
	}
	return utils.Truncate(cfg.RedactDiagnostic(content), 200)
}

func toolItemResultFields(
	al *AgentLoop,
	policy tools.ToolPolicy,
	result *tools.ToolResult,
	duplicate bool,
) (status, sideEffect, summary, errorSummary string) {
	runtime := al.runtimeSnapshot()
	status = toolItemStatusCompleted
	sideEffect = toolItemSideEffectPossible
	if policy.ReadOnly {
		sideEffect = toolItemSideEffectNone
	}
	if duplicate {
		status = toolItemStatusSkipped
		sideEffect = toolItemSideEffectNone
	} else if result != nil && result.IsError {
		status = toolItemStatusFailed
		if result.NotExecuted {
			sideEffect = toolItemSideEffectNone
		} else if !policy.ReadOnly {
			sideEffect = toolItemSideEffectUnknown
		}
	}
	if result != nil {
		content := runtime.cfg.RedactDiagnostic(result.ContentForLLM())
		summary = utils.Truncate(content, 300)
		errorSummary = toolErrorSummary(runtime.cfg, result)
	}
	return status, sideEffect, summary, errorSummary
}

func (ts *turnState) finishToolItemFromResult(
	al *AgentLoop,
	batchID string,
	index int,
	policy tools.ToolPolicy,
	result *tools.ToolResult,
	duplicate bool,
) {
	status, sideEffect, summary, errorSummary := toolItemResultFields(al, policy, result, duplicate)
	ts.finishToolItem(batchID, index, status, sideEffect, summary, errorSummary)
}

func inferSkillNamesFromToolCall(ts *turnState, toolName string, toolArgs map[string]any) []string {
	if ts == nil || toolName != "read_file" {
		return nil
	}

	rawPath, ok := toolArgs["path"].(string)
	if !ok {
		return nil
	}
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return nil
	}

	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(ts.workspace, cleanPath)
	}
	if filepath.Base(cleanPath) != "SKILL.md" {
		return nil
	}

	var roots []string
	if ts.agent != nil && ts.agent.ContextBuilder != nil {
		roots = ts.agent.ContextBuilder.skillRoots()
	}
	if len(roots) == 0 && strings.TrimSpace(ts.workspace) != "" {
		roots = []string{filepath.Join(ts.workspace, "skills")}
	}

	found := make(map[string]struct{})
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(root), cleanPath)
		if err != nil {
			continue
		}
		if rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) != 2 || parts[1] != "SKILL.md" {
			continue
		}

		skillName := strings.TrimSpace(parts[0])
		if skillName == "" {
			continue
		}
		if ts.agent != nil && ts.agent.ContextBuilder != nil {
			if canonical, ok := ts.agent.ContextBuilder.ResolveSkillName(skillName); ok {
				skillName = canonical
			}
		}
		found[skillName] = struct{}{}
	}

	if len(found) == 0 {
		return nil
	}

	names := make([]string, 0, len(found))
	for skillName := range found {
		names = append(names, skillName)
	}
	sort.Strings(names)
	return names
}
