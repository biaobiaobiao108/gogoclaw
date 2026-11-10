package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

func TestHandleAddModel_PersistsAPIKey(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewBufferString(`{
		"model_name":"new-model",
		"model":"openai/gpt-4o-mini",
		"api_key":"sk-new-model-key"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(cfg.ModelList) != 2 {
		t.Fatalf("len(model_list) = %d, want 2", len(cfg.ModelList))
	}

	added := cfg.ModelList[1]
	if added.ModelName != "new-model" {
		t.Fatalf("model_name = %q, want %q", added.ModelName, "new-model")
	}
	if added.APIKey() != "sk-new-model-key" {
		t.Fatalf("api_key = %q, want %q", added.APIKey(), "sk-new-model-key")
	}
}

func TestHandleAddModel_PersistsProvider(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewBufferString(`{
		"model_name":"nvidia-glm",
		"provider":"nvidia",
		"model":"z-ai/glm-5.1",
		"api_key":"nv-key"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	added := cfg.ModelList[len(cfg.ModelList)-1]
	if added.Provider != "nvidia" {
		t.Fatalf("provider = %q, want %q", added.Provider, "nvidia")
	}
	if added.Model != "z-ai/glm-5.1" {
		t.Fatalf("model = %q, want %q", added.Model, "z-ai/glm-5.1")
	}
}

func TestHandleAddModel_TrimsModelName(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewBufferString(`{
		"model_name":"  trimmed-alias  ",
		"provider":"openai",
		"model":"gpt-4o",
		"api_key":"test-key"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := cfg.ModelList[len(cfg.ModelList)-1].ModelName; got != "trimmed-alias" {
		t.Fatalf("model_name = %q, want trimmed-alias", got)
	}
}

func TestHandleAddModel_NormalizesDefaultChainWhenDuplicateAliasUnsupported(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("primary"),
		geminiModelConfig("backup-b"),
	}, "primary", []string{"backup-b"})

	addModelAndLoadLatest(t, configPath, `{
		"model_name":"primary",
		"provider":"elevenlabs",
		"model":"scribe_v1",
		"api_key":"sk-elevenlabs"
	}`)

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	assertDefaultChain(t, updated, "", nil)
}

func TestModelWriteHandlers_PreserveRawDefaultChain(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, configPath string) *config.Config
	}{
		{
			name: "add",
			mutate: func(t *testing.T, configPath string) *config.Config {
				addModelAndLoadLatest(t, configPath, `{
					"model_name":"added",
					"provider":"anthropic",
					"model":"claude-sonnet-4.6",
					"api_key":"sk-added"
				}`)
				cfg, err := config.LoadConfig(configPath)
				if err != nil {
					t.Fatalf("LoadConfig() error = %v", err)
				}
				return cfg
			},
		},
		{
			name: "update",
			mutate: func(t *testing.T, configPath string) *config.Config {
				return updateModelAndLoadConfig(t, configPath, 1, `{
					"model_name":"other",
					"provider":"anthropic",
					"model":"claude-haiku-4.5",
					"api_key":"sk-unrelated"
				}`)
			},
		},
		{
			name: "delete",
			mutate: func(t *testing.T, configPath string) *config.Config {
				return deleteModelAndLoadConfig(t, configPath, 1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, cleanup := setupOAuthTestEnv(t)
			defer cleanup()

			writeModelChainConfig(t, configPath, []*config.ModelConfig{
				geminiModelConfig("unrelated"),
				anthropicModelConfig("other"),
				openAIModelConfig("openai-template"),
				{
					ModelName: "openrouter-template",
					Model:     "openrouter/auto",
					APIKeys:   config.SimpleSecureStrings("sk-or-test"),
				},
			}, "openrouter/primary-model", []string{
				"gpt-4o-mini",
				"openai/gpt-4o-mini",
				"openrouter/fallback-model",
			})

			updated := tt.mutate(t, configPath)
			assertDefaultChain(t, updated, "openrouter/primary-model", []string{
				"gpt-4o-mini",
				"openrouter/fallback-model",
			})
		})
	}
}

func TestHandleAddModel_RejectsUnsupportedProvider(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewBufferString(`{
		"model_name":"bad-provider",
		"provider":"not-supported",
		"model":"gpt-4o-mini"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `provider "not-supported" is not supported`) {
		t.Fatalf("body = %q, want unsupported provider error", rec.Body.String())
	}
}

func TestHandleAddModel_AllowsBedrockProvider(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewBufferString(`{
		"model_name":"bedrock-claude",
		"provider":"bedrock",
		"model":"us.anthropic.claude-sonnet-4-20250514-v1:0"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	added := cfg.ModelList[len(cfg.ModelList)-1]
	if got := added.Provider; got != "bedrock" {
		t.Fatalf("provider = %q, want %q", got, "bedrock")
	}
	if got := added.Model; got != "us.anthropic.claude-sonnet-4-20250514-v1:0" {
		t.Fatalf("model = %q, want bedrock model ID", got)
	}
}

func TestHandleAddModel_NormalizesLegacyElevenLabsASRConfig(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "elevenlabs-asr",
		Model:     "elevenlabs/scribe_v1",
		APIKeys:   config.SimpleSecureStrings("sk_elevenlabs_test"),
	}}
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewBufferString(`{
		"model_name":"new-model",
		"provider":"openai",
		"model":"gpt-4o-mini",
		"api_key":"sk-new-model-key"
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
	if len(updated.ModelList) != 2 {
		t.Fatalf("len(model_list) = %d, want 2", len(updated.ModelList))
	}
	if got := updated.ModelList[0].Provider; got != "elevenlabs" {
		t.Fatalf("provider = %q, want %q after normalization", got, "elevenlabs")
	}
	if got := updated.ModelList[0].Model; got != "scribe_v1" {
		t.Fatalf("model = %q, want %q after normalization", got, "scribe_v1")
	}
}

func TestHandleAddModel_NormalizesExplicitElevenLabsUnsupportedModelID(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "elevenlabs-asr",
		Provider:  "elevenlabs",
		Model:     "scribe_v2",
		APIKeys:   config.SimpleSecureStrings("sk_elevenlabs_test"),
	}}
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewBufferString(`{
		"model_name":"new-model",
		"provider":"openai",
		"model":"gpt-4o-mini",
		"api_key":"sk-new-model-key"
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
	if got := updated.ModelList[0].Provider; got != "elevenlabs" {
		t.Fatalf("provider = %q, want %q after normalization", got, "elevenlabs")
	}
	if got := updated.ModelList[0].Model; got != "scribe_v1" {
		t.Fatalf("model = %q, want %q after normalization", got, "scribe_v1")
	}
}

func TestHandleAddModel_RejectsMissingCLIProviderCommand(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)
	resetModelProbeHooks(t)

	probeCommandAvailableFunc = func(command string) bool {
		return false
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewBufferString(`{
		"model_name":"claude-cli-model",
		"provider":"claude-cli",
		"model":"claude-cli"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `provider "claude-cli" is not available for new models`) {
		t.Fatalf("body = %q, want missing cli command error", rec.Body.String())
	}
}

func TestHandleAddModel_DefaultsAntigravityToOAuth(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	added := addModelAndLoadLatest(t, configPath, `{
		"model_name":"gemini-flash",
		"provider":"antigravity",
		"model":"gemini-3-flash"
	}`)
	if got := added.AuthMethod; got != "oauth" {
		t.Fatalf("auth_method = %q, want %q", got, "oauth")
	}
}

func TestHandleAddModel_DefaultsEnabledWhenOmitted(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	added := addModelAndLoadLatest(t, configPath, `{
		"model_name":"implicit-enabled",
		"provider":"openai",
		"model":"gpt-4o",
		"api_key":"sk-test"
	}`)
	if !added.IsEnabled() {
		t.Fatal("new model should be enabled when enabled is omitted")
	}
}

func TestHandleAddModel_PreservesExplicitDisabled(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	added := addModelAndLoadLatest(t, configPath, `{
		"model_name":"explicit-disabled",
		"provider":"openai",
		"model":"gpt-4o",
		"api_key":"sk-test",
		"enabled":false
	}`)
	if added.IsEnabled() {
		t.Fatal("new model should remain disabled when enabled=false is explicit")
	}
}

func TestHandleAddModel_NormalizesMixedCaseAuthMethod(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	added := addModelAndLoadLatest(t, configPath, `{
		"model_name":"openai-oauth",
		"provider":"openai",
		"model":"gpt-5.4",
		"auth_method":"OAuth"
	}`)
	if got := added.AuthMethod; got != "oauth" {
		t.Fatalf("auth_method = %q, want %q", got, "oauth")
	}
}

func TestHandleAddModel_PreservesExplicitProviderPrefixedModel(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewBufferString(`{
		"model_name":"openai-gpt",
		"provider":"openai",
		"model":"openai/gpt-4o-mini",
		"api_key":"sk-openai"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	added := cfg.ModelList[len(cfg.ModelList)-1]
	if got := added.Provider; got != "openai" {
		t.Fatalf("provider = %q, want %q", got, "openai")
	}
	if got := added.Model; got != "openai/gpt-4o-mini" {
		t.Fatalf("model = %q, want %q", got, "openai/gpt-4o-mini")
	}
}

func TestHandleAddModel_PersistsCustomHeaders(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewBufferString(`{
		"model_name":"new-model-headers",
		"model":"openai/gpt-4o-mini",
		"custom_headers":{"X-Source":"coding-plan","X-Agent":"openclaw"}
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(cfg.ModelList) != 2 {
		t.Fatalf("len(model_list) = %d, want 2", len(cfg.ModelList))
	}

	added := cfg.ModelList[1]
	if added.CustomHeaders == nil {
		t.Fatal("custom_headers should not be nil")
	}
	if got := added.CustomHeaders["X-Source"]; got != "coding-plan" {
		t.Fatalf("custom_headers[X-Source] = %q, want %q", got, "coding-plan")
	}
	if got := added.CustomHeaders["X-Agent"]; got != "openclaw" {
		t.Fatalf("custom_headers[X-Agent] = %q, want %q", got, "openclaw")
	}
}

func TestHandleAddModel_PersistsToolSchemaTransform(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewBufferString(`{
		"model_name":"new-model-transform",
		"model":"openai/gpt-4o-mini",
		"tool_schema_transform":"simple"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	added := cfg.ModelList[len(cfg.ModelList)-1]
	if got := added.ToolSchemaTransform; got != "simple" {
		t.Fatalf("tool_schema_transform = %q, want %q", got, "simple")
	}
}

func TestHandleAddModel_RejectsUnsupportedElevenLabsModelID(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewBufferString(`{
		"model_name":"elevenlabs-asr",
		"provider":"elevenlabs",
		"model":"scribe_v2"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `provider "elevenlabs" only supports model "scribe_v1"`) {
		t.Fatalf("body = %q, want elevenlabs model validation error", rec.Body.String())
	}
}
