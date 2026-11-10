package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func TestHandleSetDefaultModel_RejectsUnknownModelName(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	// First save a valid config with a primary model
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{
		{ModelName: "gpt-4", Model: "openai/gpt-4o"},
	}
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/default", bytes.NewBufferString(`{
		"model_name": "gpt-4o-mini"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := updated.Agents.Defaults.ModelName; got == "gpt-4o-mini" {
		t.Fatalf("default model = %q, want request to leave it unchanged", got)
	}
}

func TestHandleSetDefaultModel_RejectsElevenLabsASRProvider(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{
		{
			ModelName: "elevenlabs-asr",
			Provider:  "elevenlabs",
			Model:     "scribe_v1",
			APIKeys:   config.SimpleSecureStrings("sk_elevenlabs_test"),
		},
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/default", bytes.NewBufferString(`{
		"model_name": "elevenlabs-asr"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cannot be used as the default chat model") {
		t.Fatalf("body = %q, want default chat model rejection", rec.Body.String())
	}
}

func TestHandleSetDefaultModel_RejectsLegacyAliasUsingUnsupportedDefaultProvider(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.Agents.Defaults.Provider = "elevenlabs"
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "legacy-audio",
		Model:     "scribe_v1",
		APIKeys:   config.SimpleSecureStrings("sk_elevenlabs_test"),
	}}
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/default", bytes.NewBufferString(`{
		"model_name":"legacy-audio"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleSetDefaultModel_RejectsUnknownExplicitRawModel(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("primary"),
	}, "primary", nil)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/default", bytes.NewBufferString(`{
		"model_name":"elevenlabs/scribe_v1"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not found in model_list") {
		t.Fatalf("body = %q, want unknown model rejection", rec.Body.String())
	}
}

func TestHandleSetDefaultModel_RemovesDefaultFromFallbackChain(t *testing.T) {
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
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/default", bytes.NewBufferString(`{
		"model_name": "backup-a"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := updated.Agents.Defaults.ModelName; got != "backup-a" {
		t.Fatalf("default model = %q, want %q", got, "backup-a")
	}
	if len(updated.Agents.Defaults.ModelFallbacks) != 1 || updated.Agents.Defaults.ModelFallbacks[0] != "backup-b" {
		t.Fatalf("fallback_chain = %#v, want [backup-b]", updated.Agents.Defaults.ModelFallbacks)
	}
}

func TestHandleSetDefaultModel_NormalizesExistingFallbackChain(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("primary"),
		geminiModelConfig("backup-b"),
		elevenLabsModelConfig("audio-only"),
	}, "primary", []string{"missing", "audio-only", "backup-b", "primary"})

	updated := setDefaultModelAndLoadConfig(t, configPath, "backup-b")

	assertDefaultChain(t, updated, "backup-b", []string{"missing", "primary"})
}

func TestHandleUpdateDefaultChain(t *testing.T) {
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
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/default-chain", bytes.NewBufferString(`{
		"default_model":"primary",
		"fallback_chain":["backup-a","backup-b","backup-a"]
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp defaultChainResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.DefaultModel != "primary" {
		t.Fatalf("default_model = %q, want %q", resp.DefaultModel, "primary")
	}
	if len(resp.FallbackChain) != 2 || resp.FallbackChain[0] != "backup-a" || resp.FallbackChain[1] != "backup-b" {
		t.Fatalf("fallback_chain = %#v, want [backup-a backup-b]", resp.FallbackChain)
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if updated.Agents.Defaults.ModelName != "primary" {
		t.Fatalf("saved default model = %q, want %q", updated.Agents.Defaults.ModelName, "primary")
	}
	if len(updated.Agents.Defaults.ModelFallbacks) != 2 ||
		updated.Agents.Defaults.ModelFallbacks[0] != "backup-a" ||
		updated.Agents.Defaults.ModelFallbacks[1] != "backup-b" {
		t.Fatalf("saved fallback_chain = %#v, want [backup-a backup-b]", updated.Agents.Defaults.ModelFallbacks)
	}
}

func TestHandleUpdateDefaultChain_AllowsRawFallbackReference(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("unrelated"),
		{
			ModelName: "openrouter-template",
			Model:     "openrouter/auto",
			APIKeys:   config.SimpleSecureStrings("sk-or-test"),
		},
	}, "openrouter/primary-model", nil)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/default-chain", bytes.NewBufferString(`{
		"default_model":"openrouter/primary-model",
		"fallback_chain":[" gpt-4o-mini ","openrouter/fallback-model","gpt-4o-mini"]
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	assertDefaultChain(t, updated, "openrouter/primary-model", []string{
		"gpt-4o-mini",
		"openrouter/fallback-model",
	})
}

func TestHandleUpdateDefaultChain_RejectsRuntimeIdentityMatchingDefault(t *testing.T) {
	tests := []struct {
		name         string
		models       []*config.ModelConfig
		defaultModel string
		fallback     string
	}{
		{
			name: "raw references",
			models: []*config.ModelConfig{
				geminiModelConfig("unrelated"),
				openAIModelConfig("openai-template"),
			},
			defaultModel: "openai/gpt-4o",
			fallback:     "gpt-4o",
		},
		{
			name:         "alias and raw reference",
			models:       []*config.ModelConfig{openAIModelConfig("primary")},
			defaultModel: "primary",
			fallback:     "openai/gpt-4o",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, cleanup := setupOAuthTestEnv(t)
			defer cleanup()
			writeModelChainConfig(t, configPath, tt.models, tt.defaultModel, nil)

			h := NewHandler(configPath)
			mux := http.NewServeMux()
			h.RegisterRoutes(mux)

			rec := httptest.NewRecorder()
			body := fmt.Sprintf(
				`{"default_model":%q,"fallback_chain":[%q]}`,
				tt.defaultModel,
				tt.fallback,
			)
			req := httptest.NewRequest(
				http.MethodPut,
				"/api/models/default-chain",
				bytes.NewBufferString(body),
			)
			req.Header.Set("Content-Type", "application/json")
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "same runtime model") {
				t.Fatalf("body = %q, want runtime identity rejection", rec.Body.String())
			}
		})
	}
}

func TestHandleUpdateDefaultChain_DedupesFallbackRuntimeIdentity(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		geminiModelConfig("unrelated"),
		openAIModelConfig("openai-template"),
		{
			ModelName: "openrouter-template",
			Model:     "openrouter/auto",
			APIKeys:   config.SimpleSecureStrings("sk-or-test"),
		},
	}, "openrouter/primary-model", nil)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/default-chain", bytes.NewBufferString(`{
		"default_model":"openrouter/primary-model",
		"fallback_chain":["gpt-4o","openai/gpt-4o"]
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	assertDefaultChain(t, updated, "openrouter/primary-model", []string{"gpt-4o"})
}

func TestHandleUpdateDefaultChain_RejectsUnsupportedFallbackModel(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{
		openAIModelConfig("primary"),
		{
			ModelName: "audio-only",
			Provider:  "elevenlabs",
			Model:     "scribe_v1",
			APIKeys:   config.SimpleSecureStrings("sk_elevenlabs_test"),
		},
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/default-chain", bytes.NewBufferString(`{
		"default_model":"primary",
		"fallback_chain":["audio-only"]
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cannot be used in the fallback chain") {
		t.Fatalf("body = %q, want fallback chain rejection", rec.Body.String())
	}
}

func TestHandleUpdateDefaultChain_DoesNotTreatUnsupportedAliasAsRawFallback(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("primary"),
		{
			ModelName: "openrouter/fallback-model",
			Provider:  "elevenlabs",
			Model:     "scribe_v1",
			APIKeys:   config.SimpleSecureStrings("sk-elevenlabs"),
		},
	}, "primary", nil)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/default-chain", bytes.NewBufferString(`{
		"default_model":"primary",
		"fallback_chain":["openrouter/fallback-model"]
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cannot be used in the fallback chain") {
		t.Fatalf("body = %q, want fallback chain rejection", rec.Body.String())
	}
}

func TestHandleUpdateDefaultChain_RejectsUnsupportedRawFallbackReference(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("primary"),
	}, "primary", nil)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/default-chain", bytes.NewBufferString(`{
		"default_model":"primary",
		"fallback_chain":["elevenlabs/scribe_v1"]
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cannot be used in the fallback chain") {
		t.Fatalf("body = %q, want fallback chain rejection", rec.Body.String())
	}
}

func TestHandleUpdateDefaultChain_TrimsStoredLegacyAlias(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{{
		ModelName: "  primary  ",
		Provider:  "openai",
		Model:     "gpt-4o",
		APIKeys:   config.SimpleSecureStrings("test-key"),
	}}, "", nil)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/models/default-chain",
		bytes.NewBufferString(`{"default_model":"primary","fallback_chain":[]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := updated.ModelList[0].ModelName; got != "primary" {
		t.Fatalf("model_name = %q, want primary", got)
	}
}

func TestHandleUpdateDefaultChain_ValidatesRawMatchUsingConfiguredProvider(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("primary"),
		{
			ModelName: "misconfigured-audio",
			Provider:  "elevenlabs",
			Model:     "openai/gpt-4o",
			APIKeys:   config.SimpleSecureStrings("sk-elevenlabs"),
		},
	}, "primary", nil)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, body := range []string{
		"{\"default_model\":\"openai/gpt-4o\",\"fallback_chain\":[]}",
		"{\"default_model\":\"primary\",\"fallback_chain\":[\"openai/gpt-4o\"]}",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodPut,
			"/api/models/default-chain",
			bytes.NewBufferString(body),
		)
		req.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	}
}

func TestValidateDefaultChainReference_RejectsRawWithoutProviderTemplate(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Provider: "openai"},
		},
		ModelList: []*config.ModelConfig{{
			ModelName: "openai-template",
			Model:     "openai/gpt-4o-mini",
		}},
	}

	err := validateDefaultChainReference(cfg, "openrouter/new-model", nil, false)
	if err == nil || !strings.Contains(err.Error(), "has no configured provider") {
		t.Fatalf("error = %v, want missing configured provider error", err)
	}
}

func TestValidateDefaultChainReference_RejectsUnconfiguredProviderTemplate(t *testing.T) {
	cfg := &config.Config{ModelList: []*config.ModelConfig{{
		ModelName: "openrouter-template",
		Model:     "openrouter/auto",
	}}}

	err := validateDefaultChainReference(cfg, "openrouter/new-model", nil, false)
	if err == nil || !strings.Contains(err.Error(), "has no configured provider") {
		t.Fatalf("error = %v, want unconfigured provider error", err)
	}
}

func TestValidateDefaultChainReferenceRejectsUnconfiguredAlias(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Provider: "openai"}},
		ModelList: []*config.ModelConfig{{
			ModelName: "unconfigured",
			Provider:  "openai",
			Model:     "gpt-4o",
		}},
	}
	err := validateDefaultChainReference(cfg, "unconfigured", cfg.ModelList, true)
	if err == nil || !strings.Contains(err.Error(), "has no configured provider") {
		t.Fatalf("error = %v, want unconfigured alias rejection", err)
	}
}

func TestValidateDefaultChainReferenceRejectsUnconfiguredLegacyAliasUsingDefaultProvider(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Provider: "openrouter"}},
		ModelList: []*config.ModelConfig{{
			ModelName: "unconfigured",
			Model:     "auto",
			APIBase:   providers.DefaultAPIBaseForProtocol("openrouter"),
		}},
	}

	err := validateDefaultChainReference(cfg, "unconfigured", cfg.ModelList, true)
	if err == nil || !strings.Contains(err.Error(), "has no configured provider") {
		t.Fatalf("error = %v, want unconfigured legacy alias rejection", err)
	}
}

func TestHandleUpdateDefaultChain_RejectsDuplicateAliasWithUnsupportedEntry(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	writeDuplicateAliasDefaultChainConfig(t, configPath)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/default-chain", bytes.NewBufferString(`{
		"default_model":"shared-alias",
		"fallback_chain":[]
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cannot be used as the default chat model") {
		t.Fatalf("body = %q, want duplicated alias default rejection", rec.Body.String())
	}
}

func TestHandleSetDefaultModel_RejectsDuplicateAliasWithUnsupportedEntry(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	writeDuplicateAliasDefaultChainConfig(t, configPath)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/default", bytes.NewBufferString(`{
		"model_name":"shared-alias"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cannot be used as the default chat model") {
		t.Fatalf("body = %q, want duplicated alias default rejection", rec.Body.String())
	}
}

func TestHandleDeleteModel_RemovesDeletedModelFromDefaultChain(t *testing.T) {
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
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/models/1", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if updated.Agents.Defaults.ModelName != "primary" {
		t.Fatalf("saved default model = %q, want %q", updated.Agents.Defaults.ModelName, "primary")
	}
	if len(updated.Agents.Defaults.ModelFallbacks) != 1 || updated.Agents.Defaults.ModelFallbacks[0] != "backup-b" {
		t.Fatalf("saved fallback_chain = %#v, want [backup-b]", updated.Agents.Defaults.ModelFallbacks)
	}
}

func TestHandleDeleteModel_ClearsFallbackChainWhenDefaultDeleted(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("primary"),
		anthropicModelConfig("backup-a"),
	}, "primary", []string{"backup-a"})

	updated := deleteModelAndLoadConfig(t, configPath, 0)

	assertDefaultChain(t, updated, "", nil)
}

func TestHandleDeleteModel_PreservesDefaultChainReferenceWhenAliasStillExists(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

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
			Provider:  "anthropic",
			Model:     "claude-sonnet-4.6",
			APIKeys:   config.SimpleSecureStrings("sk-anthropic"),
		},
		{
			ModelName: "backup-b",
			Provider:  "gemini",
			Model:     "gemini-2.5-pro",
			APIKeys:   config.SimpleSecureStrings("sk-backup-b"),
		},
	}
	cfg.Agents.Defaults.ModelName = "shared-alias"
	cfg.Agents.Defaults.ModelFallbacks = []string{"backup-b"}
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/models/1", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := updated.Agents.Defaults.ModelName; got != "shared-alias" {
		t.Fatalf("default model = %q, want %q", got, "shared-alias")
	}
	if len(updated.Agents.Defaults.ModelFallbacks) != 1 ||
		updated.Agents.Defaults.ModelFallbacks[0] != "backup-b" {
		t.Fatalf("fallback_chain = %#v, want [backup-b]", updated.Agents.Defaults.ModelFallbacks)
	}
}
