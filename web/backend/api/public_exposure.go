package api

import (
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

// PublicExposureInput contains launcher listener properties that are not part
// of the gateway configuration.
type PublicExposureInput struct {
	LauncherExposed bool
	PublicURL       string
}

// PublicExposureStatus describes why the instance is considered exposed and
// which high-risk controls remain unreviewed.
type PublicExposureStatus struct {
	Public                  bool     `json:"public"`
	Launcher                bool     `json:"launcher"`
	PublicURL               bool     `json:"public_url"`
	PublicChannels          []string `json:"public_channels,omitempty"`
	UnapprovedHighRiskTools []string `json:"unapproved_high_risk_tools,omitempty"`
	ExecGuardDisabled       bool     `json:"exec_guard_disabled,omitempty"`
}

// EvaluatePublicExposure derives the effective exposure posture from listener
// settings and the current gateway configuration.
func EvaluatePublicExposure(cfg *config.Config, input PublicExposureInput) PublicExposureStatus {
	status := PublicExposureStatus{
		Launcher:  input.LauncherExposed,
		PublicURL: strings.TrimSpace(input.PublicURL) != "",
	}
	if cfg != nil {
		for name, channel := range cfg.Channels {
			if channel == nil || !channel.Enabled {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(channel.Type)) {
			case "pico", "pico_client", "cli":
				continue
			}
			for _, sender := range channel.AllowFrom {
				if strings.TrimSpace(sender) == "*" {
					status.PublicChannels = append(status.PublicChannels, name)
					break
				}
			}
		}
		for _, name := range tools.PublicExposureHighRiskTools() {
			if !cfg.Tools.Approval.Requires(name) {
				status.UnapprovedHighRiskTools = append(status.UnapprovedHighRiskTools, name)
			}
		}
		status.ExecGuardDisabled = !cfg.Tools.Exec.EnableDenyPatterns
	}
	status.Public = status.Launcher || status.PublicURL || len(status.PublicChannels) > 0
	if !status.Public {
		status.UnapprovedHighRiskTools = nil
		status.ExecGuardDisabled = false
	}
	return status
}
