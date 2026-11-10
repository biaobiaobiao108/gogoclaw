package launcherconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadReturnsFallbackWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launcher-config.json")
	fallback := Default()
	fallback.Port = 19999
	fallback.Public = true

	got, err := Load(path, fallback)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Port != fallback.Port || got.Public != fallback.Public {
		t.Fatalf("Load() = %+v, want %+v", got, fallback)
	}
	if !got.AllowLocalhostBypass {
		t.Fatal("allow_localhost_bypass = false, want true")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "launcher-config.json")
	want := Config{
		Port:                  18080,
		Public:                true,
		AllowedCIDRs:          []string{"192.168.1.0/24", "10.0.0.0/8"},
		AllowLocalhostBypass:  false,
		TrustedProxyCIDRs:     []string{"172.16.0.0/12"},
		PublicURL:             "https://gogoclaw.example.test:8443/",
		DashboardPasswordHash: "$2a$12$saved-dashboard-password-hash",
		LegacyLauncherToken:   "legacy-token-should-not-persist",
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := Load(path, Default())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Port != want.Port || got.Public != want.Public {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
	if got.AllowLocalhostBypass != want.AllowLocalhostBypass {
		t.Fatalf("allow_localhost_bypass = %t, want %t", got.AllowLocalhostBypass, want.AllowLocalhostBypass)
	}
	if got.DashboardPasswordHash != want.DashboardPasswordHash {
		t.Fatalf("dashboard_password_hash = %q, want %q", got.DashboardPasswordHash, want.DashboardPasswordHash)
	}
	if got.PublicURL != "https://gogoclaw.example.test:8443" {
		t.Fatalf("public_url = %q, want normalized public URL", got.PublicURL)
	}
	if got.LegacyLauncherToken != "" {
		t.Fatalf("legacy launcher_token = %q, want empty after Save", got.LegacyLauncherToken)
	}
	if len(got.AllowedCIDRs) != len(want.AllowedCIDRs) {
		t.Fatalf("allowed_cidrs len = %d, want %d", len(got.AllowedCIDRs), len(want.AllowedCIDRs))
	}
	for i := range want.AllowedCIDRs {
		if got.AllowedCIDRs[i] != want.AllowedCIDRs[i] {
			t.Fatalf("allowed_cidrs[%d] = %q, want %q", i, got.AllowedCIDRs[i], want.AllowedCIDRs[i])
		}
	}
	if len(got.TrustedProxyCIDRs) != len(want.TrustedProxyCIDRs) {
		t.Fatalf("trusted_proxy_cidrs len = %d, want %d", len(got.TrustedProxyCIDRs), len(want.TrustedProxyCIDRs))
	}
	for i := range want.TrustedProxyCIDRs {
		if got.TrustedProxyCIDRs[i] != want.TrustedProxyCIDRs[i] {
			t.Fatalf("trusted_proxy_cidrs[%d] = %q, want %q", i, got.TrustedProxyCIDRs[i], want.TrustedProxyCIDRs[i])
		}
	}

	stat, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if perm := stat.Mode().Perm(); runtime.GOOS == "windows" {
		if perm&0o200 == 0 {
			t.Fatalf("file perm = %o, want owner-writable on Windows", perm)
		}
	} else if perm != 0o600 {
		t.Fatalf("file perm = %o, want 600", perm)
	}
}

func TestLoadReadsLegacyLauncherTokenForMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launcher-config.json")
	if err := os.WriteFile(path, []byte(`{"port":18800,"launcher_token":"legacy-token"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := Load(path, Default())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.LegacyLauncherToken != "legacy-token" {
		t.Fatalf("legacy launcher_token = %q, want legacy-token", got.LegacyLauncherToken)
	}
}

func TestLoadDefaultsAllowLocalhostBypassForLegacyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launcher-config.json")
	if err := os.WriteFile(path, []byte(`{"port":18800,"allowed_cidrs":["10.0.0.0/8"]}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := Load(path, Default())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !got.AllowLocalhostBypass {
		t.Fatal("allow_localhost_bypass = false, want true for legacy config")
	}
	if got.AllowLocalhostBypassSource != BoolFieldAbsent {
		t.Fatalf("allow_localhost_bypass source = %v, want %v", got.AllowLocalhostBypassSource, BoolFieldAbsent)
	}
}

func TestLoadTracksAllowLocalhostBypassSource(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantValue  bool
		wantSource BoolFieldSource
	}{
		{
			name:       "explicit true",
			body:       `{"port":18800,"allow_localhost_bypass":true}`,
			wantValue:  true,
			wantSource: BoolFieldPresent,
		},
		{
			name:       "explicit false",
			body:       `{"port":18800,"allow_localhost_bypass":false}`,
			wantValue:  false,
			wantSource: BoolFieldPresent,
		},
		{
			name:       "explicit null keeps fallback behavior",
			body:       `{"port":18800,"allow_localhost_bypass":null}`,
			wantValue:  true,
			wantSource: BoolFieldNull,
		},
		{
			name:       "omitted uses fallback",
			body:       `{"port":18800}`,
			wantValue:  true,
			wantSource: BoolFieldAbsent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "launcher-config.json")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			got, err := Load(path, Default())
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.AllowLocalhostBypass != tt.wantValue {
				t.Fatalf("allow_localhost_bypass = %t, want %t", got.AllowLocalhostBypass, tt.wantValue)
			}
			if got.AllowLocalhostBypassSource != tt.wantSource {
				t.Fatalf("allow_localhost_bypass source = %v, want %v", got.AllowLocalhostBypassSource, tt.wantSource)
			}
		})
	}
}

func TestValidateRejectsInvalidPort(t *testing.T) {
	if err := Validate(Config{Port: 0, Public: false}); err == nil {
		t.Fatal("Validate() expected error for port 0")
	}
	if err := Validate(Config{Port: 65536, Public: false}); err == nil {
		t.Fatal("Validate() expected error for port 65536")
	}
}

func TestValidateRejectsInvalidCIDR(t *testing.T) {
	err := Validate(Config{
		Port:         18800,
		AllowedCIDRs: []string{"192.168.1.0/24", "not-a-cidr"},
	})
	if err == nil {
		t.Fatal("Validate() expected error for invalid CIDR")
	}
}

func TestValidateRejectsInvalidTrustedProxyCIDR(t *testing.T) {
	err := Validate(Config{
		Port:              18800,
		TrustedProxyCIDRs: []string{"not-a-cidr"},
	})
	if err == nil {
		t.Fatal("Validate() expected error for invalid trusted proxy CIDR")
	}
}

func TestNormalizePublicURL(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty", input: "", want: ""},
		{name: "http with port", input: " HTTP://gogoclaw.example.test:666/ ", want: "http://gogoclaw.example.test:666"},
		{name: "https", input: "https://gogoclaw.example.test", want: "https://gogoclaw.example.test"},
		{name: "ipv6", input: "http://[fd00::1]:666", want: "http://[fd00::1]:666"},
		{name: "relative", input: "/gogoclaw", wantErr: true},
		{name: "credentials", input: "https://user:pass@example.test", wantErr: true},
		{name: "path", input: "https://example.test/gogoclaw", wantErr: true},
		{name: "query", input: "https://example.test?x=1", wantErr: true},
		{name: "fragment", input: "https://example.test#x", wantErr: true},
		{name: "unsupported scheme", input: "ftp://example.test", wantErr: true},
		{name: "invalid port", input: "https://example.test:70000", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizePublicURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizePublicURL(%q) error = nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizePublicURL(%q) error = %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizePublicURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidateRejectsInvalidPublicURL(t *testing.T) {
	err := Validate(Config{
		Port:      18800,
		PublicURL: "https://example.test/path",
	})
	if err == nil {
		t.Fatal("Validate() expected error for invalid public URL")
	}
}

func TestNormalizeCIDRs(t *testing.T) {
	got := NormalizeCIDRs([]string{" 192.168.1.0/24 ", "", "10.0.0.0/8", "192.168.1.0/24"})
	want := []string{"192.168.1.0/24", "10.0.0.0/8"}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
