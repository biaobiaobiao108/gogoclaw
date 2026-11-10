package api

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/netbind"
)

func (h *Handler) effectiveLauncherPublic() bool {
	if h.serverHostExplicit {
		// -host takes precedence over -public and launcher-config public setting.
		return false
	}

	if h.serverPublicExplicit {
		return h.serverPublic
	}

	cfg, err := h.loadLauncherConfig()
	if err == nil {
		return cfg.Public
	}

	return h.serverPublic
}

func (h *Handler) gatewayHostOverride() string {
	if h.serverHostExplicit {
		return strings.TrimSpace(h.serverHostInput)
	}
	if h.effectiveLauncherPublic() {
		return "*"
	}
	return ""
}

func (h *Handler) effectiveGatewayBindHost(cfg *config.Config) string {
	if override := h.gatewayHostOverride(); override != "" {
		return override
	}
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Gateway.Host)
}

func gatewayProbeHost(bindHost string) string {
	plan, err := netbind.BuildPlan(bindHost, netbind.DefaultLoopback)
	if err != nil || strings.TrimSpace(plan.ProbeHost) == "" {
		return netbind.ResolveAdaptiveLoopbackHost()
	}
	return plan.ProbeHost
}

func (h *Handler) gatewayProxyURL() *url.URL {
	cfg, err := config.LoadConfig(h.configPath)
	port := 18790
	bindHost := ""
	if err == nil && cfg != nil {
		if cfg.Gateway.Port != 0 {
			port = cfg.Gateway.Port
		}
		bindHost = h.effectiveGatewayBindHost(cfg)
	}

	return &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(gatewayProbeHost(bindHost), strconv.Itoa(port)),
	}
}

func (h *Handler) buildWsURL(r *http.Request) string {
	origin := h.serverExternalAccess.PreferredOrigin(r)
	return origin.WebSocketScheme() + "://" + origin.Host + "/pico/ws"
}

func (h *Handler) buildPicoEventsURL(r *http.Request) string {
	origin := h.serverExternalAccess.PreferredOrigin(r)
	return origin.String() + "/pico/events"
}

func (h *Handler) buildPicoSendURL(r *http.Request) string {
	origin := h.serverExternalAccess.PreferredOrigin(r)
	return origin.String() + "/pico/send"
}
