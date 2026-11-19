package tools

import (
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

// StaticApprovalPolicy reports the instance policy before tool arguments and hooks are evaluated.
func StaticApprovalPolicy(policy config.ApprovalConfig, public bool, tool string, args map[string]any) (required bool, source, reason string) {
	if policy.YOLO {
		return false, "yolo", ""
	}
	if policy.RequiresHuman(tool) {
		return true, "approval", ""
	}
	if public {
		if reason := PublicExposureApprovalReason(tool, args); reason != "" {
			return true, "public", reason
		}
	}
	return false, "default", ""
}

var publicExposureApprovalTools = []string{
	"exec",
	"write_file",
	"edit_file",
	"append_file",
	"spawn",
	"subagent",
	"send_file",
	"message",
	"install_skill",
	"cron",
}

// PublicExposureHighRiskTools returns the built-in state-changing tools that
// require human approval when an instance is reachable by untrusted users.
func PublicExposureHighRiskTools() []string {
	return append([]string(nil), publicExposureApprovalTools...)
}

// PublicExposureApprovalReason returns a non-empty reason when a tool call must
// pass human approval while the instance is publicly exposed.
func PublicExposureApprovalReason(tool string, args map[string]any) string {
	name := strings.ToLower(strings.TrimSpace(tool))
	for _, candidate := range publicExposureApprovalTools {
		if name == candidate {
			if name == "exec" {
				command, _ := args["command"].(string)
				if strings.TrimSpace(command) != "" {
					return "public exposure requires approval before running shell commands"
				}
			}
			return "public exposure requires approval before state-changing tool calls"
		}
	}
	if PolicyForName(name).Destructive {
		return "public exposure requires approval before destructive or unknown tools"
	}
	return ""
}
