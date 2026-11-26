package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

const maxModelResponseBytes = 4 << 20

// registerModelRoutes binds model list management endpoints to the ServeMux.
func (h *Handler) registerModelRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/models", h.handleListModels)
	mux.HandleFunc("GET /api/models/default-chain", h.handleGetDefaultChain)
	mux.HandleFunc("POST /api/models/fetch", h.handleFetchModels)
	mux.HandleFunc("GET /api/models/catalog", h.handleListCatalogs)
	mux.HandleFunc("DELETE /api/models/catalog/{id}", h.handleDeleteCatalog)
	mux.HandleFunc("POST /api/models", h.handleAddModel)
	mux.HandleFunc("POST /api/models/default", h.handleSetDefaultModel)
	mux.HandleFunc("PUT /api/models/default-chain", h.handleUpdateDefaultChain)
	mux.HandleFunc("PUT /api/models/{index}", h.handleUpdateModel)
	mux.HandleFunc("DELETE /api/models/{index}", h.handleDeleteModel)
	mux.HandleFunc("POST /api/models/{index}/test", h.handleTestModel)
	mux.HandleFunc("POST /api/models/test-inline", h.handleTestInlineModel)
}

// modelResponse is the JSON structure returned for each model in the list.
// All ModelConfig fields are included so the frontend can display and edit them.
type modelResponse struct {
	Index      int    `json:"index"`
	ModelName  string `json:"model_name"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model"`
	APIBase    string `json:"api_base,omitempty"`
	APIKey     string `json:"api_key"`
	Proxy      string `json:"proxy,omitempty"`
	AuthMethod string `json:"auth_method,omitempty"`
	// Advanced fields
	ConnectMode             string                      `json:"connect_mode,omitempty"`
	Workspace               string                      `json:"workspace,omitempty"`
	RPM                     int                         `json:"rpm,omitempty"`
	MaxTokensField          string                      `json:"max_tokens_field,omitempty"`
	RequestTimeout          int                         `json:"request_timeout,omitempty"`
	InputCostPerMillionUSD  float64                     `json:"input_cost_per_million_usd,omitempty"`
	OutputCostPerMillionUSD float64                     `json:"output_cost_per_million_usd,omitempty"`
	ThinkingLevel           string                      `json:"thinking_level,omitempty"`
	ToolSchemaTransform     string                      `json:"tool_schema_transform,omitempty"`
	Streaming               config.ModelStreamingConfig `json:"streaming,omitempty"`
	ExtraBody               map[string]any              `json:"extra_body,omitempty"`
	CustomHeaders           map[string]string           `json:"custom_headers,omitempty"`
	// Meta
	Enabled             bool   `json:"enabled"`
	Available           bool   `json:"available"`
	Status              string `json:"status"`
	IsDefault           bool   `json:"is_default"`
	IsVirtual           bool   `json:"is_virtual"`
	DefaultModelAllowed bool   `json:"default_model_allowed"`
}

type defaultChainResponse struct {
	DefaultModel  string   `json:"default_model"`
	FallbackChain []string `json:"fallback_chain"`
}

type defaultChainRequest struct {
	DefaultModel  string   `json:"default_model"`
	FallbackChain []string `json:"fallback_chain"`
}

func (h *Handler) handleListModels(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	// Normalize legacy provider/model storage in memory so GET can round-trip
	// through the current API shape without mutating the on-disk config.
	normalizeStoredModelProviders(cfg)
	normalizeDefaultChainReferences(cfg)

	defaultModel := cfg.Agents.Defaults.GetModelName()
	fallbackChain := cfg.Agents.Defaults.ModelFallbacks
	modelStatuses := make([]modelConfigurationSummary, len(cfg.ModelList))

	var wg sync.WaitGroup
	wg.Add(len(cfg.ModelList))
	for i, m := range cfg.ModelList {
		go func(i int, m *config.ModelConfig) {
			defer wg.Done()
			modelStatuses[i] = modelConfigurationStatus(m)
		}(i, m)
	}
	wg.Wait()

	models := make([]modelResponse, 0, len(cfg.ModelList))
	for i, m := range cfg.ModelList {
		provider, modelID := providers.ExtractProtocol(m)
		models = append(models, modelResponse{
			Index:                   i,
			ModelName:               m.ModelName,
			Provider:                provider,
			Model:                   modelID,
			APIBase:                 m.APIBase,
			APIKey:                  maskAPIKey(m.APIKey()),
			Proxy:                   m.Proxy,
			AuthMethod:              m.AuthMethod,
			ConnectMode:             m.ConnectMode,
			Workspace:               m.Workspace,
			RPM:                     m.RPM,
			MaxTokensField:          m.MaxTokensField,
			RequestTimeout:          m.RequestTimeout,
			InputCostPerMillionUSD:  m.InputCostPerMillionUSD,
			OutputCostPerMillionUSD: m.OutputCostPerMillionUSD,
			ThinkingLevel:           m.ThinkingLevel,
			ToolSchemaTransform:     m.ToolSchemaTransform,
			Streaming:               m.Streaming,
			ExtraBody:               m.ExtraBody,
			CustomHeaders:           m.CustomHeaders,
			Enabled:                 m.IsEnabled(),
			Available:               modelStatuses[i].Available,
			Status:                  modelStatuses[i].Status,
			IsDefault:               m.ModelName == defaultModel,
			IsVirtual:               m.IsVirtual(),
			DefaultModelAllowed:     defaultModelAllowedForModelConfig(m, defaultChainProvider(cfg)),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"models":           models,
		"total":            len(models),
		"default_model":    defaultModel,
		"default_provider": defaultChainProvider(cfg),
		"fallback_chain":   fallbackChain,
		"provider_options": modelProviderOptionsForResponse(),
	})
}

// handleGetDefaultChain returns the persisted default model and fallback chain.
//
//	GET /api/models/default-chain
func (h *Handler) handleGetDefaultChain(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	normalizeStoredModelProviders(cfg)
	normalizeDefaultChainReferences(cfg)

	resp := defaultChainResponse{
		DefaultModel:  strings.TrimSpace(cfg.Agents.Defaults.GetModelName()),
		FallbackChain: trimModelNameList(cfg.Agents.Defaults.ModelFallbacks),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleAddModel appends a new model configuration entry.
//
//	POST /api/models
func (h *Handler) handleAddModel(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONRequestBody(w, r, maxJSONRequestBodyBytes)
	if err != nil {
		writeJSONReadError(w, err)
		return
	}

	type custom struct {
		config.ModelConfig
		APIKey       string `json:"api_key"`
		SetAsDefault bool   `json:"set_as_default"`
	}

	var mc custom
	if err = json.Unmarshal(body, &mc); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	var rawFields map[string]json.RawMessage
	if err = json.Unmarshal(body, &rawFields); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if mc.APIKey != "" {
		mc.ModelConfig.SetAPIKey(mc.APIKey)
	}
	if _, ok := rawFields["enabled"]; ok {
		mc.ModelConfig.SetEnabled(mc.ModelConfig.Enabled)
	} else {
		mc.ModelConfig.SetEnabled(true)
	}
	if _, ok := rawFields["streaming"]; !ok {
		mc.ModelConfig.Streaming.Enabled = true
	}

	h.configMu.Lock()
	defer h.configMu.Unlock()

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	normalizeIncomingModelConfig(&mc.ModelConfig, defaultChainProvider(cfg))
	if err = validateIncomingModelConfig(&mc.ModelConfig, nil); err != nil {
		http.Error(w, fmt.Sprintf("Validation error: %v", err), http.StatusBadRequest)
		return
	}

	cfg.ModelList = append(cfg.ModelList, &mc.ModelConfig)
	normalizeStoredModelProviders(cfg)
	if mc.SetAsDefault {
		name := mc.ModelConfig.ModelName
		if err := validateDefaultChainModelSet(name, modelConfigsByName(cfg)[name], true, defaultChainProvider(cfg)); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cfg.Agents.Defaults.ModelName = name
		cfg.Agents.Defaults.ModelFallbacks = removeModelName(cfg.Agents.Defaults.ModelFallbacks, name)
	}
	normalizeDefaultChainReferences(cfg)

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"index":  len(cfg.ModelList) - 1,
	})
}

// handleUpdateModel replaces a model configuration entry at the given index.
// If the request body omits api_key (or sends an empty string), the existing
// stored key is preserved so callers can update only api_base / proxy without
// exposing or clearing the secret.
//
//	PUT /api/models/{index}
func (h *Handler) handleUpdateModel(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		http.Error(w, "Invalid index", http.StatusBadRequest)
		return
	}

	body, err := readJSONRequestBody(w, r, maxJSONRequestBodyBytes)
	if err != nil {
		writeJSONReadError(w, err)
		return
	}

	var rawFields map[string]json.RawMessage
	if err = json.Unmarshal(body, &rawFields); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	type custom struct {
		config.ModelConfig
		APIKey string `json:"api_key"`
	}

	var mc custom
	if err = json.Unmarshal(body, &mc); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	h.configMu.Lock()
	defer h.configMu.Unlock()

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	normalizeStoredModelProviders(cfg)

	if idx < 0 || idx >= len(cfg.ModelList) {
		http.Error(w, fmt.Sprintf("Index %d out of range (0-%d)", idx, len(cfg.ModelList)-1), http.StatusNotFound)
		return
	}
	oldModelName := strings.TrimSpace(cfg.ModelList[idx].ModelName)

	// Preserve the existing API key when the caller omits it (empty string).
	// This lets the UI update api_base / proxy without clearing the stored secret.
	if mc.APIKey == "" {
		mc.ModelConfig.SetAPIKey(cfg.ModelList[idx].APIKey())
	} else {
		mc.ModelConfig.SetAPIKey(mc.APIKey)
	}
	// Preserve existing ExtraBody when omitted (nil), but clear it when
	// the frontend sends an empty object {} to indicate the field should
	// be removed.
	if mc.ExtraBody == nil {
		mc.ExtraBody = cfg.ModelList[idx].ExtraBody
	} else if len(mc.ExtraBody) == 0 {
		mc.ExtraBody = nil
	}
	// Preserve existing CustomHeaders when omitted (nil), but clear it when
	// the frontend sends an empty object {} to indicate the field should
	// be removed.
	if mc.CustomHeaders == nil {
		mc.CustomHeaders = cfg.ModelList[idx].CustomHeaders
	} else if len(mc.CustomHeaders) == 0 {
		mc.CustomHeaders = nil
	}
	if _, ok := rawFields["tool_schema_transform"]; !ok {
		mc.ToolSchemaTransform = cfg.ModelList[idx].ToolSchemaTransform
	}
	if _, ok := rawFields["streaming"]; !ok {
		mc.Streaming = cfg.ModelList[idx].Streaming
	}
	if _, ok := rawFields["input_cost_per_million_usd"]; !ok {
		mc.InputCostPerMillionUSD = cfg.ModelList[idx].InputCostPerMillionUSD
	}
	if _, ok := rawFields["output_cost_per_million_usd"]; !ok {
		mc.OutputCostPerMillionUSD = cfg.ModelList[idx].OutputCostPerMillionUSD
	}
	if _, ok := rawFields["auth_method"]; !ok {
		mc.AuthMethod = cfg.ModelList[idx].AuthMethod
	}
	if _, ok := rawFields["enabled"]; !ok {
		mc.ModelConfig.SetEnabled(cfg.ModelList[idx].IsEnabled())
	} else {
		mc.ModelConfig.SetEnabled(mc.ModelConfig.Enabled)
	}
	// Preserve the existing Provider when the caller omits it. This keeps the
	// update API backward-compatible for clients that haven't started sending
	// the new field yet, while still allowing explicit clearing via "".
	if _, ok := rawFields["provider"]; !ok {
		mc.Provider = cfg.ModelList[idx].Provider
		// Older clients still round-trip the legacy model field only. When the
		// stored config encodes provider/model in Model and has no explicit
		// Provider field yet, continue preserving that hidden provider prefix.
		// This keeps provider-omitted updates backward-compatible even when an
		// older client edits the visible model ID.
		if strings.TrimSpace(cfg.ModelList[idx].Provider) == "" {
			existingRawModel := strings.TrimSpace(cfg.ModelList[idx].Model)
			incomingModel := strings.TrimSpace(mc.Model)
			existingProtocol, existingModelID := providers.ExtractProtocol(cfg.ModelList[idx])
			if existingRawModel != "" && existingRawModel != existingModelID && incomingModel != "" {
				if incomingModel == existingModelID {
					mc.Model = existingRawModel
				} else if strings.Contains(incomingModel, "/") && !strings.Contains(existingModelID, "/") {
					// Older clients never saw the hidden provider prefix for simple
					// legacy entries such as "openai/gpt-4o". If they now send an
					// explicit provider/model string, treat it as the caller's full
					// intent instead of re-applying the old hidden prefix.
					mc.Model = incomingModel
				} else if !strings.HasPrefix(incomingModel, existingProtocol+"/") {
					mc.Model = existingProtocol + "/" + incomingModel
				}
			}
		}
	}

	normalizeIncomingModelConfig(&mc.ModelConfig, defaultChainProvider(cfg))
	if err = validateIncomingModelConfig(&mc.ModelConfig, cfg.ModelList[idx]); err != nil {
		http.Error(w, fmt.Sprintf("Validation error: %v", err), http.StatusBadRequest)
		return
	}
	newModelName := strings.TrimSpace(mc.ModelConfig.ModelName)
	defaultReferenceRenamed := false
	fallbackReferenceRenamed := false
	if oldModelName != "" && newModelName != "" && oldModelName != newModelName {
		remainingOldNameModels := modelConfigsByNameExceptIndex(cfg, oldModelName, idx)
		if err := validateDefaultChainModelSet(
			oldModelName,
			remainingOldNameModels,
			true,
			defaultChainProvider(cfg),
		); err != nil {
			defaultReferenceRenamed = true
			if strings.TrimSpace(cfg.Agents.Defaults.ModelName) == oldModelName {
				cfg.Agents.Defaults.ModelName = newModelName
			}
		}
		if err := validateDefaultChainModelSet(
			oldModelName,
			remainingOldNameModels,
			false,
			defaultChainProvider(cfg),
		); err != nil {
			fallbackReferenceRenamed = true
			cfg.Agents.Defaults.ModelFallbacks = replaceModelName(
				cfg.Agents.Defaults.ModelFallbacks,
				oldModelName,
				newModelName,
			)
		}
	}
	cfg.ModelList[idx] = &mc.ModelConfig
	normalizeStoredModelProviders(cfg)
	normalizeDefaultChainReferences(cfg)

	logger.Debugf("update model config: %#v", mc.ModelConfig)

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	response := map[string]any{"status": "ok"}
	if oldModelName != "" && newModelName != "" && oldModelName != newModelName {
		response["reference_rename"] = map[string]any{
			"from":     oldModelName,
			"to":       newModelName,
			"default":  defaultReferenceRenamed,
			"fallback": fallbackReferenceRenamed,
		}
	}
	json.NewEncoder(w).Encode(response)
}

// handleDeleteModel removes a model configuration entry at the given index.
//
//	DELETE /api/models/{index}
func (h *Handler) handleDeleteModel(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		http.Error(w, "Invalid index", http.StatusBadRequest)
		return
	}

	h.configMu.Lock()
	defer h.configMu.Unlock()

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	normalizeStoredModelNames(cfg)

	if idx < 0 || idx >= len(cfg.ModelList) {
		http.Error(w, fmt.Sprintf("Index %d out of range (0-%d)", idx, len(cfg.ModelList)-1), http.StatusNotFound)
		return
	}

	deletedModelName := strings.TrimSpace(cfg.ModelList[idx].ModelName)

	cfg.ModelList = append(cfg.ModelList[:idx], cfg.ModelList[idx+1:]...)

	if deletedModelName != "" {
		remainingModels := modelConfigsByName(cfg)[deletedModelName]
		if strings.TrimSpace(cfg.Agents.Defaults.ModelName) == deletedModelName {
			if err := validateDefaultChainModelSet(
				deletedModelName,
				remainingModels,
				true,
				defaultChainProvider(cfg),
			); err != nil {
				cfg.Agents.Defaults.ModelName = ""
			}
		}
		if err := validateDefaultChainModelSet(
			deletedModelName,
			remainingModels,
			false,
			defaultChainProvider(cfg),
		); err != nil {
			cfg.Agents.Defaults.ModelFallbacks = removeModelName(
				cfg.Agents.Defaults.ModelFallbacks,
				deletedModelName,
			)
		}
	}
	normalizeDefaultChainReferences(cfg)

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func removeModelName(names []string, target string) []string {
	target = strings.TrimSpace(target)
	if target == "" || len(names) == 0 {
		return names
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == target {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

func replaceModelName(names []string, oldName string, newName string) []string {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || oldName == newName || len(names) == 0 {
		return names
	}
	replaced := make([]string, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == oldName {
			replaced = append(replaced, newName)
			continue
		}
		replaced = append(replaced, name)
	}
	return replaced
}

// handleSetDefaultModel sets the default model for all agents.
//
//	POST /api/models/default
func (h *Handler) handleSetDefaultModel(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONRequestBody(w, r, maxJSONRequestBodyBytes)
	if err != nil {
		writeJSONReadError(w, err)
		return
	}

	var req struct {
		ModelName string `json:"model_name"`
	}
	if err = json.Unmarshal(body, &req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	modelName := strings.TrimSpace(req.ModelName)
	if modelName == "" {
		http.Error(w, "model_name is required", http.StatusBadRequest)
		return
	}

	h.configMu.Lock()
	defer h.configMu.Unlock()

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	normalizeStoredModelNames(cfg)

	// Keep this legacy endpoint alias-only. Raw references are managed by the
	// default-chain endpoint and may already exist in persisted configuration.
	if err := validateDefaultChainModelSet(
		modelName,
		modelConfigsByName(cfg)[modelName],
		true,
		defaultChainProvider(cfg),
	); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found in model_list") {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}

	cfg.Agents.Defaults.ModelName = modelName
	cfg.Agents.Defaults.ModelFallbacks = removeModelName(cfg.Agents.Defaults.ModelFallbacks, modelName)
	normalizeDefaultChainReferences(cfg)

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":        "ok",
		"default_model": modelName,
	})
}

// handleUpdateDefaultChain updates the persisted default model and fallback chain.
//
//	PUT /api/models/default-chain
func (h *Handler) handleUpdateDefaultChain(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONRequestBody(w, r, maxJSONRequestBodyBytes)
	if err != nil {
		writeJSONReadError(w, err)
		return
	}

	var req defaultChainRequest
	if err = json.Unmarshal(body, &req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	h.configMu.Lock()
	defer h.configMu.Unlock()

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	normalizeStoredModelNames(cfg)

	defaultModel := strings.TrimSpace(req.DefaultModel)
	fallbacks, err := validateDefaultModelChain(cfg, defaultModel, req.FallbackChain)
	if err != nil {
		http.Error(w, fmt.Sprintf("Validation error: %v", err), http.StatusBadRequest)
		return
	}

	cfg.Agents.Defaults.ModelName = defaultModel
	cfg.Agents.Defaults.ModelFallbacks = fallbacks

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(defaultChainResponse{
		DefaultModel:  defaultModel,
		FallbackChain: fallbacks,
	})
}
