package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

func resetModelProbeHooks(t *testing.T) {
	t.Helper()

	origTCPProbe := probeTCPServiceFunc
	origOllamaProbe := probeOllamaModelFunc
	origOpenAIProbe := probeOpenAICompatibleModelFunc
	origCommandProbe := probeCommandAvailableFunc
	origNow := modelProbeNowFunc
	resetModelProbeCache()
	t.Cleanup(func() {
		probeTCPServiceFunc = origTCPProbe
		probeOllamaModelFunc = origOllamaProbe
		probeOpenAICompatibleModelFunc = origOpenAIProbe
		probeCommandAvailableFunc = origCommandProbe
		modelProbeNowFunc = origNow
		resetModelProbeCache()
	})
}

func addModelAndLoadLatest(t *testing.T, configPath string, body string) *config.ModelConfig {
	t.Helper()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(cfg.ModelList) == 0 {
		t.Fatal("model_list should contain the newly added model")
	}

	return cfg.ModelList[len(cfg.ModelList)-1]
}

func writeDuplicateAliasDefaultChainConfig(t *testing.T, configPath string) {
	t.Helper()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{
		{
			ModelName: "shared-alias",
			Provider:  "openai",
			Model:     "gpt-4o",
			APIKeys:   config.SimpleSecureStrings("sk-openai"),
		},
		{
			ModelName: "shared-alias",
			Provider:  "elevenlabs",
			Model:     "scribe_v1",
			APIKeys:   config.SimpleSecureStrings("sk-elevenlabs"),
		},
	}
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
}

func writeModelChainConfig(
	t *testing.T,
	configPath string,
	models []*config.ModelConfig,
	defaultModel string,
	fallbacks []string,
) {
	t.Helper()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = models
	cfg.Agents.Defaults.ModelName = defaultModel
	cfg.Agents.Defaults.ModelFallbacks = fallbacks
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
}

func updateModelAndLoadConfig(
	t *testing.T,
	configPath string,
	index int,
	body string,
) *config.Config {
	t.Helper()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/models/%d", index),
		bytes.NewBufferString(body),
	)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	return cfg
}

func deleteModelAndLoadConfig(t *testing.T, configPath string, index int) *config.Config {
	t.Helper()

	h := NewHandler(configPath)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/models/%d", index), nil)
	req.SetPathValue("index", fmt.Sprintf("%d", index))
	h.handleDeleteModel(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	return cfg
}

func setDefaultModelAndLoadConfig(t *testing.T, configPath string, modelName string) *config.Config {
	t.Helper()

	h := NewHandler(configPath)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/models/default",
		bytes.NewBufferString(fmt.Sprintf(`{"model_name":%q}`, modelName)),
	)
	req.Header.Set("Content-Type", "application/json")
	h.handleSetDefaultModel(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	return cfg
}

func assertDefaultChain(
	t *testing.T,
	cfg *config.Config,
	wantDefault string,
	wantFallbacks []string,
) {
	t.Helper()

	if got := cfg.Agents.Defaults.ModelName; got != wantDefault {
		t.Fatalf("default model = %q, want %q", got, wantDefault)
	}
	if !slices.Equal(cfg.Agents.Defaults.ModelFallbacks, wantFallbacks) {
		t.Fatalf("fallback_chain = %#v, want %#v", cfg.Agents.Defaults.ModelFallbacks, wantFallbacks)
	}
}

func openAIModelConfig(alias string) *config.ModelConfig {
	return &config.ModelConfig{
		ModelName: alias,
		Provider:  "openai",
		Model:     "gpt-4o",
		APIKeys:   config.SimpleSecureStrings("sk-openai"),
	}
}

func anthropicModelConfig(alias string) *config.ModelConfig {
	return &config.ModelConfig{
		ModelName: alias,
		Provider:  "anthropic",
		Model:     "claude-sonnet-4.6",
		APIKeys:   config.SimpleSecureStrings("sk-anthropic"),
	}
}

func geminiModelConfig(alias string) *config.ModelConfig {
	return &config.ModelConfig{
		ModelName: alias,
		Provider:  "gemini",
		Model:     "gemini-2.5-pro",
		APIKeys:   config.SimpleSecureStrings("sk-gemini"),
	}
}

func elevenLabsModelConfig(alias string) *config.ModelConfig {
	return &config.ModelConfig{
		ModelName: alias,
		Provider:  "elevenlabs",
		Model:     "scribe_v1",
		APIKeys:   config.SimpleSecureStrings("sk-elevenlabs"),
	}
}











































































































func TestHandleGetDefaultChain(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{
		openAIModelConfig("primary"),
		anthropicModelConfig("backup-a"),
		geminiModelConfig("backup-b"),
	}
	cfg.Agents.Defaults.ModelName = "primary"
	cfg.Agents.Defaults.ModelFallbacks = []string{"backup-a", "backup-b"}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models/default-chain", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp defaultChainResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.DefaultModel != "primary" {
		t.Fatalf("default_model = %q, want %q", resp.DefaultModel, "primary")
	}
	if len(resp.FallbackChain) != 2 || resp.FallbackChain[0] != "backup-a" || resp.FallbackChain[1] != "backup-b" {
		t.Fatalf("fallback_chain = %#v, want [backup-a backup-b]", resp.FallbackChain)
	}
}
