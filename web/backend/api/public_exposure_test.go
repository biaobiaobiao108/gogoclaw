package api

import (
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

func TestEvaluatePublicExposure(t *testing.T) {
	cfg := config.DefaultConfig()
	status := EvaluatePublicExposure(cfg, PublicExposureInput{})
	if status.Public {
		t.Fatal("default loopback configuration should not be public")
	}

	cfg.Channels["telegram"] = &config.Channel{
		Type:      "telegram",
		Enabled:   true,
		AllowFrom: config.FlexibleStringSlice{"*"},
	}
	status = EvaluatePublicExposure(cfg, PublicExposureInput{})
	if !status.Public || len(status.PublicChannels) != 1 || status.PublicChannels[0] != "telegram" {
		t.Fatalf("public channel status = %+v", status)
	}
	if len(status.UnapprovedHighRiskTools) == 0 {
		t.Fatal("public status should report unapproved high-risk tools")
	}

	cfg.Tools.Approval = config.ApprovalConfig{
		Enabled: true,
		Tools:   []string{"*"},
	}
	status = EvaluatePublicExposure(cfg, PublicExposureInput{})
	if len(status.UnapprovedHighRiskTools) != 0 {
		t.Fatalf("approved tools still reported: %v", status.UnapprovedHighRiskTools)
	}

	cfg.Channels["telegram"].AllowFrom = config.FlexibleStringSlice{"user-1"}
	status = EvaluatePublicExposure(cfg, PublicExposureInput{
		LauncherExposed: true,
	})
	if !status.Public || !status.Launcher {
		t.Fatalf("launcher exposure status = %+v", status)
	}

	cfg.Channels["pico"] = &config.Channel{
		Type:      "pico",
		Enabled:   true,
		AllowFrom: config.FlexibleStringSlice{"*"},
	}
	status = EvaluatePublicExposure(cfg, PublicExposureInput{})
	if status.Public {
		t.Fatal("authenticated Pico transport must not be treated as a public channel")
	}
}
