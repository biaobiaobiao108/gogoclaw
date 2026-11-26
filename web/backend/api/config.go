package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
)

// registerConfigRoutes binds configuration management endpoints to the ServeMux.
func (h *Handler) registerConfigRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/config", h.handleGetConfig)
	mux.HandleFunc("PUT /api/config", h.handleUpdateConfig)
	mux.HandleFunc("PATCH /api/config", h.handlePatchConfig)
	mux.HandleFunc("POST /api/config/reset", h.handleResetConfig)
	mux.HandleFunc("POST /api/config/test-command-patterns", h.handleTestCommandPatterns)
}

func (h *Handler) applyRuntimeLogLevel() {
	if h.debug {
		logger.SetLevel(logger.DEBUG)
		return
	}
	logger.SetLevelFromString(config.ResolveGatewayLogLevel(h.configPath))
}

// handleGetConfig returns the complete system configuration.
//
//	GET /api/config
func (h *Handler) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cfg); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// handleUpdateConfig updates the complete system configuration.
//
//	PUT /api/config
func (h *Handler) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONRequestBody(w, r, maxJSONRequestBodyBytes)
	if err != nil {
		writeJSONReadError(w, err)
		return
	}

	var raw map[string]any
	if err = json.Unmarshal(body, &raw); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	if err = normalizeChannelArrayFields(raw); err != nil {
		http.Error(w, fmt.Sprintf("Invalid channel array field: %v", err), http.StatusBadRequest)
		return
	}
	normalizedBody, err := json.Marshal(raw)
	if err != nil {
		http.Error(w, "Failed to normalize config payload", http.StatusBadRequest)
		return
	}
	var cfg config.Config
	if err = json.Unmarshal(normalizedBody, &cfg); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	cfg.Session.ApplyDmScope()
	cfg.Session.DeriveDmScope()
	if execAllowRemoteOmitted(body) {
		cfg.Tools.Exec.AllowRemote = config.DefaultConfig().Tools.Exec.AllowRemote
	}

	h.configMu.Lock()
	defer h.configMu.Unlock()

	// Load existing config and copy security credentials before validation,
	// so that security-managed fields (e.g. pico token) are available.
	err = cfg.SecurityCopyFrom(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to apply security config: %v", err), http.StatusInternalServerError)
		return
	}
	applyConfigSecretsFromMap(&cfg, raw)

	if errs := validateConfig(&cfg); len(errs) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"status": "validation_error",
			"errors": errs,
		})
		return
	}

	if err := config.SaveConfig(h.configPath, &cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	h.applyRuntimeLogLevel()
	logger.Infof("configuration updated successfully")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func execAllowRemoteOmitted(body []byte) bool {
	var raw struct {
		Tools *struct {
			Exec *struct {
				AllowRemote *bool `json:"allow_remote"`
			} `json:"exec"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return false
	}
	return raw.Tools == nil || raw.Tools.Exec == nil || raw.Tools.Exec.AllowRemote == nil
}

// handlePatchConfig partially updates the system configuration using JSON Merge Patch (RFC 7396).
// Only the fields present in the request body will be updated; all other fields remain unchanged.
//
//	PATCH /api/config
func (h *Handler) handlePatchConfig(w http.ResponseWriter, r *http.Request) {
	patchBody, err := readJSONRequestBody(w, r, maxJSONRequestBodyBytes)
	if err != nil {
		writeJSONReadError(w, err)
		return
	}

	// Validate the patch is valid JSON
	var patch map[string]any
	if err = json.Unmarshal(patchBody, &patch); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	h.configMu.Lock()
	defer h.configMu.Unlock()

	// Load existing config and marshal to a map for merging
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	existing, err := json.Marshal(cfg)
	if err != nil {
		http.Error(w, "Failed to serialize current config", http.StatusInternalServerError)
		return
	}

	var base map[string]any
	if err = json.Unmarshal(existing, &base); err != nil {
		http.Error(w, "Failed to parse current config", http.StatusInternalServerError)
		return
	}

	// Recursively merge patch into base
	mergeMap(base, patch)

	// When the patch updates dm_scope, the old derived dimensions from the
	// base must be cleared so that ApplyDmScope() can re-derive them from
	// the new dm_scope value. Otherwise the stale dimensions survive the
	// merge and ApplyDmScope() exits early due to its precedence guard.
	if sess, ok := base["session"].(map[string]any); ok {
		if patchSess, patchHasSession := patch["session"].(map[string]any); patchHasSession {
			if _, hasDmScope := patchSess["dm_scope"]; hasDmScope {
				if _, hasDimsInPatch := patchSess["dimensions"]; !hasDimsInPatch {
					delete(sess, "dimensions")
				}
			}
		}
	}
	if err = normalizeChannelArrayFields(base); err != nil {
		http.Error(w, fmt.Sprintf("Invalid channel array field: %v", err), http.StatusBadRequest)
		return
	}

	// Convert merged map back to Config struct
	merged, err := json.Marshal(base)
	if err != nil {
		http.Error(w, "Failed to serialize merged config", http.StatusInternalServerError)
		return
	}

	var newCfg config.Config
	if err = json.Unmarshal(merged, &newCfg); err != nil {
		http.Error(w, fmt.Sprintf("Merged config is invalid: %v", err), http.StatusBadRequest)
		return
	}
	newCfg.Session.ApplyDmScope()
	newCfg.Session.DeriveDmScope()

	// Restore security fields (tokens/keys) from the loaded config before validation,
	// because private fields are lost during JSON round-trip.
	// Approval is present in the security YAML as well as the main config.
	// Preserve the merged policy so the previous YAML value cannot undo a patch.
	approvalPolicy := newCfg.Tools.Approval
	if err = newCfg.SecurityCopyFrom(h.configPath); err != nil {
		http.Error(w, fmt.Sprintf("Failed to apply security config: %v", err), http.StatusInternalServerError)
		return
	}
	newCfg.Tools.Approval = approvalPolicy
	applyConfigSecretsFromMap(&newCfg, base)

	if errs := validateConfig(&newCfg); len(errs) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"status": "validation_error",
			"errors": errs,
		})
		return
	}

	if err := config.SaveConfig(h.configPath, &newCfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	h.applyRuntimeLogLevel()
	logger.Infof("configuration updated successfully")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleResetConfig resets the configuration to factory defaults.
// API keys and security credentials are preserved.
//
//	POST /api/config/reset
func (h *Handler) handleResetConfig(w http.ResponseWriter, r *http.Request) {
	h.configMu.Lock()

	if err := config.ResetToDefaults(h.configPath); err != nil {
		h.configMu.Unlock()
		http.Error(w, fmt.Sprintf("Failed to reset config: %v", err), http.StatusInternalServerError)
		return
	}
	h.configMu.Unlock()

	h.applyRuntimeLogLevel()
	logger.Infof("configuration reset to factory defaults")

	// Restart gateway if running
	status := h.gatewayStatusData()
	gatewayStatus, _ := status["gateway_status"].(string)
	if gatewayStatus == "running" {
		if _, err := h.RestartGateway(); err != nil {
			logger.ErrorF("failed to restart gateway after config reset", map[string]any{"error": err.Error()})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleTestCommandPatterns tests a command against whitelist and blacklist patterns.
//
//	POST /api/config/test-command-patterns
func (h *Handler) handleTestCommandPatterns(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONRequestBody(w, r, maxJSONRequestBodyBytes)
	if err != nil {
		writeJSONReadError(w, err)
		return
	}

	var req struct {
		AllowPatterns []string `json:"allow_patterns"`
		DenyPatterns  []string `json:"deny_patterns"`
		Command       string   `json:"command"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	lower := strings.ToLower(strings.TrimSpace(req.Command))

	type result struct {
		Allowed          bool    `json:"allowed"`
		Blocked          bool    `json:"blocked"`
		MatchedWhitelist *string `json:"matched_whitelist,omitempty"`
		MatchedBlacklist *string `json:"matched_blacklist,omitempty"`
	}

	resp := result{Allowed: false, Blocked: false}

	// The runtime guard checks deny patterns first. The tester must report the
	// same outcome so an allow rule never appears to override a blocked command.
	for _, pattern := range req.DenyPatterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue // skip invalid patterns
		}
		if re.MatchString(lower) {
			resp.Blocked = true
			resp.MatchedBlacklist = &pattern
			break
		}
	}

	for _, pattern := range req.AllowPatterns {
		if resp.Blocked {
			break
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		if re.MatchString(lower) {
			resp.Allowed = true
			resp.MatchedWhitelist = &pattern
			break
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
