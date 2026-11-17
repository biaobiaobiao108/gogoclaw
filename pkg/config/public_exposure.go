package config

import (
	"os"
	"strings"
)

// PublicExposureEnabled reports whether the current process serves an exposed
// surface. Launchers set EnvPublicExposure when their dashboard is reachable
// beyond loopback. Wildcard-enabled external channels are detected directly so
// standalone gateways receive the same conservative treatment.
func PublicExposureEnabled(cfg *Config) bool {
	if envBool(os.Getenv(EnvPublicExposure)) {
		return true
	}
	if cfg == nil {
		return false
	}
	for _, channel := range cfg.Channels {
		if channel == nil || !channel.Enabled {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(channel.Type)) {
		case "pico", "pico_client", "cli":
			continue
		}
		for _, sender := range channel.AllowFrom {
			if strings.TrimSpace(sender) == "*" {
				return true
			}
		}
	}
	return false
}

func envBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
