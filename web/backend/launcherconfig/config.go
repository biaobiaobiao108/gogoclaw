package launcherconfig

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// FileName is the launcher-specific settings file name.
	FileName = "launcher-config.json"
	// DefaultPort is the default port for the web launcher.
	DefaultPort = 18800
	// EnvLauncherHost overrides launcher listen host.
	EnvLauncherHost = "GOGOCLAW_LAUNCHER_HOST"
)

// Config stores launch parameters for the web backend service.
type Config struct {
	Port                       int             `json:"port"`
	Public                     bool            `json:"public"`
	AllowedCIDRs               []string        `json:"allowed_cidrs,omitempty"`
	AllowLocalhostBypass       bool            `json:"allow_localhost_bypass"`
	AllowLocalhostBypassSource BoolFieldSource `json:"-"`
	TrustedProxyCIDRs          []string        `json:"trusted_proxy_cidrs,omitempty"`
	PublicURL                  string          `json:"public_url,omitempty"`
	DashboardPasswordHash      string          `json:"dashboard_password_hash,omitempty"`
	// LegacyLauncherToken is read only for one-time migration from the removed
	// token login flow. Save always clears it so new configs do not persist it.
	LegacyLauncherToken string `json:"launcher_token,omitempty"`
}

// BoolFieldSource tracks whether a JSON boolean field was omitted, explicitly
// provided, or explicitly set to null. This is only used for diagnostics.
type BoolFieldSource uint8

const (
	BoolFieldAbsent BoolFieldSource = iota
	BoolFieldPresent
	BoolFieldNull
)

// Default returns default launcher settings.
func Default() Config {
	return Config{Port: DefaultPort, Public: false, AllowLocalhostBypass: true}
}

// Validate checks if launcher settings are valid.
func Validate(cfg Config) error {
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("port %d is out of range (1-65535)", cfg.Port)
	}
	for _, cidr := range cfg.AllowedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid CIDR %q", cidr)
		}
	}
	for _, cidr := range cfg.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid trusted proxy CIDR %q", cidr)
		}
	}
	if _, err := NormalizePublicURL(cfg.PublicURL); err != nil {
		return err
	}
	return nil
}

// NormalizePublicURL validates the browser-visible launcher origin and returns
// a canonical value without a trailing slash, query, fragment, or credentials.
func NormalizePublicURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid public_url: %w", err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("public_url must use http or https")
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", fmt.Errorf("public_url must include a host")
	}
	if u.User != nil {
		return "", fmt.Errorf("public_url must not include credentials")
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("public_url must not include a path")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("public_url must not include a query or fragment")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("public_url has an invalid port")
		}
	}

	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	u.ForceQuery = false
	return u.String(), nil
}

// NormalizeCIDRs trims entries, removes empty values, and deduplicates CIDRs.
func NormalizeCIDRs(cidrs []string) []string {
	if len(cidrs) == 0 {
		return nil
	}
	out := make([]string, 0, len(cidrs))
	seen := make(map[string]struct{}, len(cidrs))
	for _, raw := range cidrs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// PathForAppConfig returns launcher-config path near the app config file.
func PathForAppConfig(appConfigPath string) string {
	dir := filepath.Dir(appConfigPath)
	if dir == "" || dir == "." {
		dir = "."
	}
	return filepath.Join(dir, FileName)
}

// Load reads launcher settings; fallback is returned when file does not exist.
func Load(path string, fallback Config) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fallback, nil
		}
		return Config{}, err
	}

	cfg := fallback
	cfg.AllowLocalhostBypassSource = detectBoolFieldSource(data, "allow_localhost_bypass")
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.AllowedCIDRs = NormalizeCIDRs(cfg.AllowedCIDRs)
	cfg.TrustedProxyCIDRs = NormalizeCIDRs(cfg.TrustedProxyCIDRs)
	publicURL, err := NormalizePublicURL(cfg.PublicURL)
	if err != nil {
		return Config{}, err
	}
	cfg.PublicURL = publicURL
	cfg.DashboardPasswordHash = strings.TrimSpace(cfg.DashboardPasswordHash)
	cfg.LegacyLauncherToken = strings.TrimSpace(cfg.LegacyLauncherToken)
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func detectBoolFieldSource(data []byte, field string) BoolFieldSource {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return BoolFieldAbsent
	}

	value, ok := raw[field]
	if !ok {
		return BoolFieldAbsent
	}

	if string(value) == "null" {
		return BoolFieldNull
	}

	return BoolFieldPresent
}

// Save writes launcher settings to disk.
func Save(path string, cfg Config) error {
	cfg.AllowedCIDRs = NormalizeCIDRs(cfg.AllowedCIDRs)
	cfg.TrustedProxyCIDRs = NormalizeCIDRs(cfg.TrustedProxyCIDRs)
	publicURL, err := NormalizePublicURL(cfg.PublicURL)
	if err != nil {
		return err
	}
	cfg.PublicURL = publicURL
	cfg.DashboardPasswordHash = strings.TrimSpace(cfg.DashboardPasswordHash)
	cfg.LegacyLauncherToken = ""
	if err := Validate(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}
