package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

func TestHandleUpdateModel_CustomHeadersPreserveAndClear(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName:     "editable",
		Model:         "openai/gpt-4o-mini",
		APIKeys:       config.SimpleSecureStrings("sk-existing"),
		CustomHeaders: map[string]string{"X-Source": "coding-plan"},
	}}
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// Omitted custom_headers should preserve existing value.
	recPreserve := httptest.NewRecorder()
	reqPreserve := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"editable",
		"model":"openai/gpt-4o-mini"
	}`))
	reqPreserve.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recPreserve, reqPreserve)
	if recPreserve.Code != http.StatusOK {
		t.Fatalf("preserve status = %d, want %d, body=%s", recPreserve.Code, http.StatusOK, recPreserve.Body.String())
	}

	afterPreserve, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() after preserve error = %v", err)
	}
	if got := afterPreserve.ModelList[0].CustomHeaders["X-Source"]; got != "coding-plan" {
		t.Fatalf("preserved custom_headers[X-Source] = %q, want %q", got, "coding-plan")
	}

	// Empty object should clear custom_headers.
	recClear := httptest.NewRecorder()
	reqClear := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"editable",
		"model":"openai/gpt-4o-mini",
		"custom_headers":{}
	}`))
	reqClear.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recClear, reqClear)
	if recClear.Code != http.StatusOK {
		t.Fatalf("clear status = %d, want %d, body=%s", recClear.Code, http.StatusOK, recClear.Body.String())
	}

	afterClear, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() after clear error = %v", err)
	}
	if afterClear.ModelList[0].CustomHeaders != nil {
		t.Fatalf("custom_headers = %#v, want nil", afterClear.ModelList[0].CustomHeaders)
	}
}

func TestHandleUpdateModel_ToolSchemaTransformPreserveAndClear(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName:           "editable",
		Model:               "openai/gpt-4o-mini",
		APIKeys:             config.SimpleSecureStrings("sk-existing"),
		ToolSchemaTransform: "simple",
	}}
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	recPreserve := httptest.NewRecorder()
	reqPreserve := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"editable",
		"model":"openai/gpt-4o-mini"
	}`))
	reqPreserve.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recPreserve, reqPreserve)
	if recPreserve.Code != http.StatusOK {
		t.Fatalf("preserve status = %d, want %d, body=%s", recPreserve.Code, http.StatusOK, recPreserve.Body.String())
	}

	afterPreserve, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() after preserve error = %v", err)
	}
	if got := afterPreserve.ModelList[0].ToolSchemaTransform; got != "simple" {
		t.Fatalf("preserved tool_schema_transform = %q, want %q", got, "simple")
	}

	recClear := httptest.NewRecorder()
	reqClear := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"editable",
		"model":"openai/gpt-4o-mini",
		"tool_schema_transform":""
	}`))
	reqClear.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recClear, reqClear)
	if recClear.Code != http.StatusOK {
		t.Fatalf("clear status = %d, want %d, body=%s", recClear.Code, http.StatusOK, recClear.Body.String())
	}

	afterClear, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() after clear error = %v", err)
	}
	if afterClear.ModelList[0].ToolSchemaTransform != "" {
		t.Fatalf("tool_schema_transform = %q, want empty", afterClear.ModelList[0].ToolSchemaTransform)
	}
}

func TestHandleUpdateModel_StreamingPreserveAndChange(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "editable",
		Provider:  "openai",
		Model:     "gpt-4o-mini",
		APIKeys:   config.SimpleSecureStrings("sk-existing"),
		Streaming: config.ModelStreamingConfig{Enabled: true},
	}}
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	recPreserve := httptest.NewRecorder()
	reqPreserve := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"editable",
		"provider":"openai",
		"model":"gpt-4o-mini"
	}`))
	reqPreserve.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recPreserve, reqPreserve)
	if recPreserve.Code != http.StatusOK {
		t.Fatalf("preserve status = %d, want %d, body=%s", recPreserve.Code, http.StatusOK, recPreserve.Body.String())
	}

	afterPreserve, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() after preserve error = %v", err)
	}
	if !afterPreserve.ModelList[0].Streaming.Enabled {
		t.Fatal("preserved streaming.enabled = false, want true")
	}

	recChange := httptest.NewRecorder()
	reqChange := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"editable",
		"provider":"openai",
		"model":"gpt-4o-mini",
		"streaming":{"enabled":false}
	}`))
	reqChange.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recChange, reqChange)
	if recChange.Code != http.StatusOK {
		t.Fatalf("change status = %d, want %d, body=%s", recChange.Code, http.StatusOK, recChange.Body.String())
	}

	afterChange, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() after change error = %v", err)
	}
	if afterChange.ModelList[0].Streaming.Enabled {
		t.Fatal("streaming.enabled = true, want false after explicit update")
	}
}

func TestHandleUpdateModel_PersistsProvider(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "editable",
		Model:     "gpt-4o",
		Provider:  "openai",
	}}
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"editable",
		"provider":"openrouter",
		"model":"openai/gpt-4o"
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
	if got := updated.ModelList[0].Provider; got != "openrouter" {
		t.Fatalf("provider = %q, want %q", got, "openrouter")
	}
}

func TestHandleUpdateModel_PreservesExplicitProviderPrefixedModel(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "editable",
		Model:     "gpt-4o",
		Provider:  "openai",
	}}
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"editable",
		"provider":"openai",
		"model":"openai/gpt-5.4"
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
	if got := updated.ModelList[0].Provider; got != "openai" {
		t.Fatalf("provider = %q, want %q", got, "openai")
	}
	if got := updated.ModelList[0].Model; got != "openai/gpt-5.4" {
		t.Fatalf("model = %q, want %q", got, "openai/gpt-5.4")
	}
}

func TestHandleUpdateModel_PreservesLegacyModelPrefixWhenProviderOmitted(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "legacy-openrouter",
		Model:     "openrouter/openai/gpt-5.4",
	}}
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// Simulate an older client: it reads GET /api/models, ignores the new
	// provider field, then PUTs the visible model string back unchanged.
	recList := httptest.NewRecorder()
	reqList := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(recList, reqList)

	if recList.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body=%s", recList.Code, http.StatusOK, recList.Body.String())
	}

	var listResp struct {
		Models []modelResponse `json:"models"`
	}
	if err = json.Unmarshal(recList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(listResp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(listResp.Models))
	}
	if got := listResp.Models[0].Provider; got != "openrouter" {
		t.Fatalf("provider = %q, want %q", got, "openrouter")
	}
	if got := listResp.Models[0].Model; got != "openai/gpt-5.4" {
		t.Fatalf("model = %q, want %q", got, "openai/gpt-5.4")
	}

	recUpdate := httptest.NewRecorder()
	reqUpdate := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"legacy-openrouter",
		"model":"openai/gpt-5.4"
	}`))
	reqUpdate.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recUpdate, reqUpdate)

	if recUpdate.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d, body=%s", recUpdate.Code, http.StatusOK, recUpdate.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := updated.ModelList[0].Provider; got != "openrouter" {
		t.Fatalf("provider = %q, want %q", got, "openrouter")
	}
	if got := updated.ModelList[0].Model; got != "openai/gpt-5.4" {
		t.Fatalf("model = %q, want %q", got, "openai/gpt-5.4")
	}
}

func TestHandleUpdateModel_MigratesLegacyElevenLabsASRWhenProviderOmitted(t *testing.T) {
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

	recList := httptest.NewRecorder()
	reqList := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(recList, reqList)

	if recList.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body=%s", recList.Code, http.StatusOK, recList.Body.String())
	}

	var listResp struct {
		Models []modelResponse `json:"models"`
	}
	if err = json.Unmarshal(recList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(listResp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(listResp.Models))
	}
	if got := listResp.Models[0].Provider; got != "elevenlabs" {
		t.Fatalf("provider = %q, want %q", got, "elevenlabs")
	}
	if got := listResp.Models[0].Model; got != "scribe_v1" {
		t.Fatalf("model = %q, want %q", got, "scribe_v1")
	}

	recUpdate := httptest.NewRecorder()
	reqUpdate := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"elevenlabs-asr",
		"model":"scribe_v1",
		"api_base":"https://api.elevenlabs.io"
	}`))
	reqUpdate.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recUpdate, reqUpdate)

	if recUpdate.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d, body=%s", recUpdate.Code, http.StatusOK, recUpdate.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := updated.ModelList[0].Provider; got != "elevenlabs" {
		t.Fatalf("provider = %q, want %q", got, "elevenlabs")
	}
	if got := updated.ModelList[0].Model; got != "scribe_v1" {
		t.Fatalf("model = %q, want %q", got, "scribe_v1")
	}
	if got := updated.ModelList[0].APIBase; got != "https://api.elevenlabs.io" {
		t.Fatalf("api_base = %q, want %q", got, "https://api.elevenlabs.io")
	}
}

func TestHandleUpdateModel_RoundTripsExplicitLegacyElevenLabsModelID(t *testing.T) {
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

	recList := httptest.NewRecorder()
	reqList := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(recList, reqList)

	if recList.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body=%s", recList.Code, http.StatusOK, recList.Body.String())
	}

	var listResp struct {
		Models []modelResponse `json:"models"`
	}
	if err = json.Unmarshal(recList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(listResp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(listResp.Models))
	}
	if got := listResp.Models[0].Provider; got != "elevenlabs" {
		t.Fatalf("provider = %q, want %q", got, "elevenlabs")
	}
	if got := listResp.Models[0].Model; got != "scribe_v1" {
		t.Fatalf("model = %q, want %q after GET normalization", got, "scribe_v1")
	}

	recUpdate := httptest.NewRecorder()
	reqUpdate := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"elevenlabs-asr",
		"provider":"elevenlabs",
		"model":"scribe_v1",
		"api_base":"https://api.elevenlabs.io"
	}`))
	reqUpdate.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recUpdate, reqUpdate)

	if recUpdate.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d, body=%s", recUpdate.Code, http.StatusOK, recUpdate.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := updated.ModelList[0].Provider; got != "elevenlabs" {
		t.Fatalf("provider = %q, want %q", got, "elevenlabs")
	}
	if got := updated.ModelList[0].Model; got != "scribe_v1" {
		t.Fatalf("model = %q, want %q", got, "scribe_v1")
	}
	if got := updated.ModelList[0].APIBase; got != "https://api.elevenlabs.io" {
		t.Fatalf("api_base = %q, want %q", got, "https://api.elevenlabs.io")
	}
}

func TestHandleUpdateModel_ClearsDefaultWhenSavingASROnlyModel(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "elevenlabs-asr",
		Provider:  "elevenlabs",
		Model:     "scribe_v1",
		APIKeys:   config.SimpleSecureStrings("sk_elevenlabs_test"),
	}}
	cfg.Agents.Defaults.ModelName = "elevenlabs-asr"
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"elevenlabs-asr",
		"provider":"elevenlabs",
		"model":"scribe_v1",
		"api_base":"https://api.elevenlabs.io"
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
	if got := updated.Agents.Defaults.ModelName; got != "" {
		t.Fatalf("default model = %q, want cleared default", got)
	}
}

func TestHandleUpdateModel_PreservesLegacyModelPrefixWhenProviderOmittedAndModelChanges(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "legacy-openrouter",
		Model:     "openrouter/openai/gpt-5.4",
	}}
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"legacy-openrouter",
		"model":"openai/gpt-5.5"
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
	if got := updated.ModelList[0].Provider; got != "openrouter" {
		t.Fatalf("provider = %q, want %q", got, "openrouter")
	}
	if got := updated.ModelList[0].Model; got != "openai/gpt-5.5" {
		t.Fatalf("model = %q, want %q", got, "openai/gpt-5.5")
	}
}

func TestHandleUpdateModel_AllowsExistingBedrockProvider(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "bedrock-claude",
		Provider:  "bedrock",
		Model:     "us.anthropic.claude-sonnet-4-20250514-v1:0",
		APIBase:   "us-west-2",
	}}
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"bedrock-claude",
		"provider":"bedrock",
		"model":"us.anthropic.claude-3-7-sonnet-20250219-v1:0",
		"api_base":"us-east-1"
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
	if got := updated.ModelList[0].Provider; got != "bedrock" {
		t.Fatalf("provider = %q, want %q", got, "bedrock")
	}
	if got := updated.ModelList[0].Model; got != "us.anthropic.claude-3-7-sonnet-20250219-v1:0" {
		t.Fatalf("model = %q, want updated bedrock model", got)
	}
	if got := updated.ModelList[0].APIBase; got != "us-east-1" {
		t.Fatalf("api_base = %q, want %q", got, "us-east-1")
	}
}

func TestHandleUpdateModel_ReturnsReferenceRenameRoles(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("old-name"),
	}, "openrouter/primary-model", nil)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/0", bytes.NewBufferString(`{
		"model_name":"new-name",
		"provider":"openai",
		"model":"gpt-4o",
		"api_key":"sk-primary"
	}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response struct {
		ReferenceRename struct {
			From     string `json:"from"`
			To       string `json:"to"`
			Default  bool   `json:"default"`
			Fallback bool   `json:"fallback"`
		} `json:"reference_rename"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.ReferenceRename.From != "old-name" || response.ReferenceRename.To != "new-name" {
		t.Fatalf("reference_rename = %#v, want old-name -> new-name", response.ReferenceRename)
	}
	if !response.ReferenceRename.Default || !response.ReferenceRename.Fallback {
		t.Fatalf("reference_rename roles = %#v, want both roles", response.ReferenceRename)
	}
}

func TestHandleUpdateModel_RenamesDefaultModelReference(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("primary"),
		anthropicModelConfig("backup-a"),
	}, "primary", []string{"backup-a"})

	updated := updateModelAndLoadConfig(t, configPath, 0, `{
		"model_name":"primary-renamed",
		"provider":"openai",
		"model":"gpt-4o",
		"api_key":"sk-primary"
	}`)

	assertDefaultChain(t, updated, "primary-renamed", []string{"backup-a"})
}

func TestHandleUpdateModel_RenamesFallbackModelReference(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("primary"),
		anthropicModelConfig("backup-a"),
		geminiModelConfig("backup-b"),
	}, "primary", []string{"backup-a", "backup-b"})

	updated := updateModelAndLoadConfig(t, configPath, 1, `{
		"model_name":"backup-a-renamed",
		"provider":"anthropic",
		"model":"claude-sonnet-4.6",
		"api_key":"sk-backup-a"
	}`)

	assertDefaultChain(t, updated, "primary", []string{"backup-a-renamed", "backup-b"})
}

func TestHandleUpdateModel_RemovesFallbackWhenRenamedToDefaultModel(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("primary"),
		anthropicModelConfig("backup-a"),
	}, "primary", []string{"backup-a"})

	updated := updateModelAndLoadConfig(t, configPath, 1, `{
		"model_name":"primary",
		"provider":"anthropic",
		"model":"claude-sonnet-4.6",
		"api_key":"sk-backup-a"
	}`)

	assertDefaultChain(t, updated, "primary", nil)
}

func TestHandleUpdateModel_RemovesFallbackWhenDefaultRenamedToFallbackModel(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("primary"),
		anthropicModelConfig("backup-a"),
	}, "primary", []string{"backup-a"})

	updated := updateModelAndLoadConfig(t, configPath, 0, `{
		"model_name":"backup-a",
		"provider":"openai",
		"model":"gpt-4o",
		"api_key":"sk-primary"
	}`)

	assertDefaultChain(t, updated, "backup-a", nil)
}

func TestHandleUpdateModel_DoesNotRenameDefaultChainReferenceWhenAliasStillExists(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("shared-alias"),
		anthropicModelConfig("shared-alias"),
		geminiModelConfig("backup-b"),
	}, "shared-alias", []string{"backup-b"})

	updated := updateModelAndLoadConfig(t, configPath, 1, `{
		"model_name":"renamed-alias",
		"provider":"anthropic",
		"model":"claude-sonnet-4.6",
		"api_key":"sk-anthropic"
	}`)

	assertDefaultChain(t, updated, "shared-alias", []string{"backup-b"})
}

func TestHandleUpdateModel_ClearsDefaultWhenRenamedAliasGroupUnsupported(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("source-alias"),
		elevenLabsModelConfig("target-alias"),
		geminiModelConfig("backup-b"),
	}, "source-alias", []string{"backup-b"})

	updated := updateModelAndLoadConfig(t, configPath, 0, `{
		"model_name":"target-alias",
		"provider":"openai",
		"model":"gpt-4o",
		"api_key":"sk-openai"
	}`)

	assertDefaultChain(t, updated, "", nil)
}

func TestHandleUpdateModel_RenamesDefaultChainReferenceWhenRemainingAliasUnsupported(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("shared-alias"),
		elevenLabsModelConfig("shared-alias"),
		geminiModelConfig("backup-b"),
	}, "shared-alias", []string{"backup-b"})

	updated := updateModelAndLoadConfig(t, configPath, 0, `{
		"model_name":"renamed-alias",
		"provider":"openai",
		"model":"gpt-4o",
		"api_key":"sk-openai"
	}`)

	assertDefaultChain(t, updated, "renamed-alias", []string{"backup-b"})
}

func TestHandleUpdateModel_RemovesRenamedUnsupportedModelFromDefaultChain(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("primary"),
		anthropicModelConfig("backup-a"),
	}, "primary", []string{"backup-a"})

	updated := updateModelAndLoadConfig(t, configPath, 1, `{
		"model_name":"audio-only",
		"provider":"elevenlabs",
		"model":"scribe_v1",
		"api_key":"sk-elevenlabs"
	}`)

	assertDefaultChain(t, updated, "primary", nil)
}
