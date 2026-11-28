package config

import (
	"net"
	"os"
	"strings"
)

// PublicExposureEnabled reports whether the current process serves an exposed
// surface. Launchers set EnvPublicExposure when their dashboard is reachable
// beyond loopback. Wildcard-enabled external channels are detected directly so
// standalone gateways receive the same conservative treatment.
func PublicExposureEnabled(cfg *Config) bool {
	rawExposure, exposureSet := os.LookupEnv(EnvPublicExposure)
	if envBool(rawExposure) {
		return true
	}
	explicitExposure := exposureSet && strings.TrimSpace(rawExposure) != ""
	if !explicitExposure {
		if bind := strings.TrimSpace(os.Getenv(EnvGatewayBind)); bind != "" {
			if listenAddressExposed(bind) {
				return true
			}
		} else if cfg != nil && listenAddressExposed(cfg.Gateway.Host) {
			return true
		}
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

func listenAddressExposed(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}

	host := raw
	if parsedHost, _, err := net.SplitHostPort(raw); err == nil {
		host = parsedHost
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		// An address such as ":18790" binds every interface.
		return true
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback()
	}
	return true
}

func envBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
