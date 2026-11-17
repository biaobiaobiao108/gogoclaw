package config

import (
	"testing"
)

func TestPublicExposureEnabled(t *testing.T) {
	t.Setenv(EnvPublicExposure, "")

	cfg := DefaultConfig()
	if PublicExposureEnabled(cfg) {
		t.Fatal("default config unexpectedly enables public exposure")
	}

	cfg.Channels["telegram"] = &Channel{
		Type:      "telegram",
		Enabled:   true,
		AllowFrom: FlexibleStringSlice{"*"},
	}
	if !PublicExposureEnabled(cfg) {
		t.Fatal("wildcard external channel should enable public exposure")
	}

	cfg.Channels["telegram"].AllowFrom = FlexibleStringSlice{"user-1"}
	if PublicExposureEnabled(cfg) {
		t.Fatal("allowlisted channel should not enable public exposure")
	}

	cfg.Channels["pico"] = &Channel{
		Type:      "pico",
		Enabled:   true,
		AllowFrom: FlexibleStringSlice{"*"},
	}
	if PublicExposureEnabled(cfg) {
		t.Fatal("authenticated Pico transport should not enable public exposure")
	}

	t.Setenv(EnvPublicExposure, "true")
	if !PublicExposureEnabled(cfg) {
		t.Fatal("launcher public exposure flag was ignored")
	}
}
