package config

import (
	"testing"
)

func TestPublicExposureEnabled(t *testing.T) {
	t.Setenv(EnvPublicExposure, "")
	t.Setenv(EnvGatewayBind, "")

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

func TestPublicExposureEnabledFromGatewayListenAddress(t *testing.T) {
	t.Setenv(EnvPublicExposure, "")
	t.Setenv(EnvGatewayBind, "")

	cfg := DefaultConfig()
	cfg.Gateway.Host = "0.0.0.0"
	if !PublicExposureEnabled(cfg) {
		t.Fatal("non-loopback gateway host should enable public exposure")
	}

	cfg.Gateway.Host = "localhost"
	if PublicExposureEnabled(cfg) {
		t.Fatal("loopback gateway host should not enable public exposure")
	}

	cfg.Gateway.Host = "0.0.0.0"
	t.Setenv(EnvGatewayBind, "127.0.0.1")
	if PublicExposureEnabled(cfg) {
		t.Fatal("host-side loopback bind should override the container listen address")
	}

	cfg.Gateway.Host = "127.0.0.1"
	t.Setenv(EnvGatewayBind, "0.0.0.0")
	if !PublicExposureEnabled(cfg) {
		t.Fatal("non-loopback host-side bind should enable public exposure")
	}

	t.Setenv(EnvPublicExposure, "false")
	if PublicExposureEnabled(cfg) {
		t.Fatal("explicit false exposure should suppress listener-based inference")
	}
}

func TestListenAddressExposed(t *testing.T) {
	tests := []struct {
		address string
		want    bool
	}{
		{address: "", want: false},
		{address: "127.0.0.1", want: false},
		{address: "127.0.0.1:18790", want: false},
		{address: "localhost:18790", want: false},
		{address: "[::1]:18790", want: false},
		{address: "::1", want: false},
		{address: "0.0.0.0", want: true},
		{address: "0.0.0.0:18790", want: true},
		{address: "::", want: true},
		{address: ":18790", want: true},
		{address: "192.168.1.20", want: true},
		{address: "gateway.internal", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.address, func(t *testing.T) {
			if got := listenAddressExposed(tc.address); got != tc.want {
				t.Fatalf("listenAddressExposed(%q) = %t, want %t", tc.address, got, tc.want)
			}
		})
	}
}
