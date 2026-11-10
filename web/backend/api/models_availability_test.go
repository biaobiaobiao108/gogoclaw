package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/auth"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func TestHandleListModels_AvailabilityUsesRuntimeProbesForLocalModels(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)
	resetModelProbeHooks(t)

	var mu sync.Mutex
	var openAIProbes []string
	var ollamaProbes []string
	var tcpProbes []string

	probeOpenAICompatibleModelFunc = func(apiBase, modelID, apiKey string) bool {
		mu.Lock()
		openAIProbes = append(openAIProbes, apiBase+"|"+modelID+"|"+apiKey)
		mu.Unlock()
		return apiBase == "http://127.0.0.1:8000/v1" && modelID == "custom-model" && apiKey == ""
	}
	probeOllamaModelFunc = func(apiBase, modelID string) bool {
		mu.Lock()
		ollamaProbes = append(ollamaProbes, apiBase+"|"+modelID)
		mu.Unlock()
		return apiBase == "http://localhost:11434/v1" && modelID == "llama3"
	}
	probeTCPServiceFunc = func(apiBase string) bool {
		mu.Lock()
		tcpProbes = append(tcpProbes, apiBase)
		mu.Unlock()
		return apiBase == "http://127.0.0.1:4321"
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{
		{
			ModelName:  "openai-oauth",
			Model:      "openai/gpt-5.4",
			AuthMethod: "oauth",
		},
		{
			ModelName: "vllm-local",
			Model:     "vllm/custom-model",
			APIBase:   "http://127.0.0.1:8000/v1",
		},
		{
			ModelName: "ollama-default",
			Model:     "ollama/llama3",
		},
		{
			ModelName: "vllm-remote",
			Model:     "vllm/custom-model",
			APIBase:   "https://models.example.com/v1",
			APIKeys:   config.SimpleSecureStrings("remote-key"),
		},
		{
			ModelName:  "copilot-gpt-5.4",
			Model:      "github-copilot/gpt-5.4",
			APIBase:    "http://127.0.0.1:4321",
			AuthMethod: "oauth",
		},
	}
	cfg.Agents.Defaults.ModelName = "openai-oauth"
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models []modelResponse `json:"models"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	gotAvailable := make(map[string]bool, len(resp.Models))
	gotStatus := make(map[string]string, len(resp.Models))
	for _, model := range resp.Models {
		gotAvailable[model.ModelName] = model.Available
		gotStatus[model.ModelName] = model.Status
	}

	if gotAvailable["openai-oauth"] {
		t.Fatalf("openai oauth model available = true, want false without stored credential")
	}
	if !gotAvailable["vllm-local"] {
		t.Fatalf("vllm local model available = false, want true when local probe succeeds")
	}
	if !gotAvailable["ollama-default"] {
		t.Fatalf("ollama default model available = false, want true when default local probe succeeds")
	}
	if !gotAvailable["vllm-remote"] {
		t.Fatalf("remote vllm model available = false, want true with api_key")
	}
	if !gotAvailable["copilot-gpt-5.4"] {
		t.Fatalf("copilot model available = false, want true when local bridge probe succeeds")
	}
	if gotStatus["openai-oauth"] != modelStatusUnconfigured {
		t.Fatalf("openai oauth model status = %q, want %q", gotStatus["openai-oauth"], modelStatusUnconfigured)
	}
	if gotStatus["vllm-local"] != modelStatusAvailable {
		t.Fatalf("vllm local model status = %q, want %q", gotStatus["vllm-local"], modelStatusAvailable)
	}
	if gotStatus["ollama-default"] != modelStatusAvailable {
		t.Fatalf("ollama default model status = %q, want %q", gotStatus["ollama-default"], modelStatusAvailable)
	}
	if gotStatus["vllm-remote"] != modelStatusAvailable {
		t.Fatalf("remote vllm model status = %q, want %q", gotStatus["vllm-remote"], modelStatusAvailable)
	}
	if gotStatus["copilot-gpt-5.4"] != modelStatusAvailable {
		t.Fatalf("copilot model status = %q, want %q", gotStatus["copilot-gpt-5.4"], modelStatusAvailable)
	}
	if len(openAIProbes) != 1 || openAIProbes[0] != "http://127.0.0.1:8000/v1|custom-model|" {
		t.Fatalf("openAI probes = %#v, want only local vllm probe", openAIProbes)
	}
	if len(ollamaProbes) != 1 || ollamaProbes[0] != "http://localhost:11434/v1|llama3" {
		t.Fatalf("ollama probes = %#v, want default local probe", ollamaProbes)
	}
	if len(tcpProbes) != 1 || tcpProbes[0] != "http://127.0.0.1:4321" {
		t.Fatalf("tcp probes = %#v, want only local copilot probe", tcpProbes)
	}
}

func TestHandleListModels_AvailabilityForOAuthModelWithCredential(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)
	resetModelProbeHooks(t)

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName:  "claude-oauth",
		Model:      "anthropic/claude-sonnet-4.6",
		AuthMethod: "oauth",
	}}
	cfg.Agents.Defaults.ModelName = "claude-oauth"
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	if setCredentialErr := auth.SetCredential(oauthProviderAnthropic, &auth.AuthCredential{
		AccessToken: "anthropic-token",
		Provider:    oauthProviderAnthropic,
		AuthMethod:  "oauth",
	}); setCredentialErr != nil {
		t.Fatalf("SetCredential() error = %v", setCredentialErr)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models []modelResponse `json:"models"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(resp.Models))
	}
	if !resp.Models[0].Available {
		t.Fatalf("oauth model available = false, want true with stored credential")
	}
}

func TestHasModelConfiguration_OAuthWithoutMappedCredentialFallsBackToAPIKey(t *testing.T) {
	noKey := &config.ModelConfig{
		Provider:   "gemini",
		Model:      "gemini-2.5-flash",
		AuthMethod: "oauth",
	}
	if hasModelConfiguration(noKey) {
		t.Fatal("oauth model without credential mapping and api key should be unconfigured")
	}

	withKey := &config.ModelConfig{
		Provider:   "gemini",
		Model:      "gemini-2.5-flash",
		AuthMethod: "oauth",
		APIKeys:    config.SimpleSecureStrings("gemini-key"),
	}
	if !hasModelConfiguration(withKey) {
		t.Fatal("oauth model without credential mapping should fall back to api key configuration")
	}
}

func TestHandleListModels_AntigravityImplicitOAuthAvailability(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)
	resetModelProbeHooks(t)

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "gemini-flash",
		Provider:  "antigravity",
		Model:     "gemini-3-flash",
	}}
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	err = auth.SetCredential(oauthProviderGoogleAntigravity, &auth.AuthCredential{
		AccessToken: "antigravity-token",
		Provider:    oauthProviderGoogleAntigravity,
		AuthMethod:  "oauth",
	})
	if err != nil {
		t.Fatalf("SetCredential() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models []modelResponse `json:"models"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(resp.Models))
	}
	if !resp.Models[0].Available {
		t.Fatal("antigravity model available = false, want true with stored credential even without auth_method")
	}
}

func TestHandleListModels_BedrockUsesAmbientCredentialStatus(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)
	resetModelProbeHooks(t)

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "bedrock-claude",
		Provider:  "bedrock",
		Model:     "us.anthropic.claude-sonnet-4-20250514-v1:0",
	}}
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models []modelResponse `json:"models"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(resp.Models))
	}
	if !resp.Models[0].Available {
		t.Fatal("bedrock model available = false, want true because Bedrock uses ambient AWS credentials")
	}
	if resp.Models[0].Status != modelStatusAvailable {
		t.Fatalf("bedrock model status = %q, want %q", resp.Models[0].Status, modelStatusAvailable)
	}
}

func TestHandleListModels_ExposesOnlyGeneralProtocols(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)
	resetModelProbeHooks(t)

	probeCommandAvailableFunc = func(command string) bool {
		switch command {
		case "claude":
			return false
		case "codex":
			return true
		default:
			return false
		}
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{
		{
			ModelName: "claude-cli-model",
			Provider:  "claude-cli",
			Model:     "claude-cli",
		},
		{
			ModelName: "codex-cli-model",
			Provider:  "codex-cli",
			Model:     "codex-cli",
		},
	}
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models          []modelResponse                 `json:"models"`
		ProviderOptions []providers.ModelProviderOption `json:"provider_options"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	modelsByName := make(map[string]modelResponse, len(resp.Models))
	for _, model := range resp.Models {
		modelsByName[model.ModelName] = model
	}
	if model := modelsByName["claude-cli-model"]; model.Available || model.Status != modelStatusUnreachable {
		t.Fatalf(
			"claude-cli status = (%t, %q), want (%t, %q)",
			model.Available,
			model.Status,
			false,
			modelStatusUnreachable,
		)
	}
	if model := modelsByName["codex-cli-model"]; !model.Available || model.Status != modelStatusAvailable {
		t.Fatalf(
			"codex-cli status = (%t, %q), want (%t, %q)",
			model.Available,
			model.Status,
			true,
			modelStatusAvailable,
		)
	}

	optionsByID := make(map[string]providers.ModelProviderOption, len(resp.ProviderOptions))
	for _, option := range resp.ProviderOptions {
		optionsByID[option.ID] = option
	}
	if len(optionsByID) != 4 {
		t.Fatalf("provider_options count = %d, want 4", len(optionsByID))
	}
	for _, providerID := range []string{"openai", "openai-responses", "anthropic-messages", "gemini"} {
		if _, ok := optionsByID[providerID]; !ok {
			t.Fatalf("%s provider option missing", providerID)
		}
	}
}

func TestHandleListModels_ProbesLocalModelsConcurrently(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)
	resetModelProbeHooks(t)

	started := make(chan string, 2)
	release := make(chan struct{})

	probeOpenAICompatibleModelFunc = func(apiBase, modelID, apiKey string) bool {
		started <- apiBase + "|" + modelID
		<-release
		return true
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{
		{
			ModelName: "local-vllm-a",
			Model:     "vllm/custom-a",
			APIBase:   "http://127.0.0.1:8000/v1",
		},
		{
			ModelName: "local-vllm-b",
			Model:     "vllm/custom-b",
			APIBase:   "http://127.0.0.1:8001/v1",
		},
	}
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	recCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
		mux.ServeHTTP(rec, req)
		recCh <- rec
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("expected both local probes to start before the first one completed")
		}
	}
	close(release)

	rec := <-recCh
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestHandleListModels_NormalizesWildcardLocalAPIBaseForProbe(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)
	resetModelProbeHooks(t)

	var gotProbe string
	probeOpenAICompatibleModelFunc = func(apiBase, modelID, apiKey string) bool {
		gotProbe = apiBase + "|" + modelID + "|" + apiKey
		return apiBase == "http://127.0.0.1:8000/v1" && modelID == "custom-model" && apiKey == ""
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "vllm-local",
		Model:     "vllm/custom-model",
		APIBase:   "http://0.0.0.0:8000/v1",
	}}

	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models []modelResponse `json:"models"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(resp.Models))
	}
	if !resp.Models[0].Available {
		t.Fatal("wildcard-bound local model available = false, want true after probe host normalization")
	}
	if gotProbe != "http://127.0.0.1:8000/v1|custom-model|" {
		t.Fatalf("probe api base = %q, want %q", gotProbe, "http://127.0.0.1:8000/v1|custom-model|")
	}
}

func TestHandleListModels_StatusMarksUnreachableLocalModel(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)
	resetModelProbeHooks(t)

	probeOpenAICompatibleModelFunc = func(apiBase, modelID, apiKey string) bool {
		return false
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "vllm-local-down",
		Model:     "vllm/custom-model",
		APIBase:   "http://127.0.0.1:8000/v1",
		APIKeys:   config.SimpleSecureStrings("test-key"),
	}}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models []modelResponse `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(resp.Models))
	}

	if resp.Models[0].Available {
		t.Fatal("unreachable local model available = true, want false")
	}
	if resp.Models[0].Status != modelStatusUnreachable {
		t.Fatalf("unreachable local model status = %q, want %q", resp.Models[0].Status, modelStatusUnreachable)
	}
	if resp.Models[0].APIKey == "" {
		t.Fatal("masked API key preview should still be returned when API key is configured")
	}
}

func TestHandleListModels_RuntimeProbeUsesExplicitProviderField(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)
	resetModelProbeHooks(t)

	var gotProbe string
	probeOpenAICompatibleModelFunc = func(apiBase, modelID, apiKey string) bool {
		gotProbe = apiBase + "|" + modelID + "|" + apiKey
		return true
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "vllm-local",
		Provider:  "vllm",
		Model:     "custom-model",
		APIBase:   "http://127.0.0.1:8000/v1",
	}}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if gotProbe != "http://127.0.0.1:8000/v1|custom-model|" {
		t.Fatalf("probe = %q, want %q", gotProbe, "http://127.0.0.1:8000/v1|custom-model|")
	}
}

func TestHandleListModels_ReturnsStreamingConfig(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "streaming-model",
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

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models []modelResponse `json:"models"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(resp.Models))
	}
	if !resp.Models[0].Streaming.Enabled {
		t.Fatal("streaming.enabled = false, want true")
	}
}

func TestHandleListModels_PreservesExplicitProviderPrefixedModel(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "openrouter-auto-explicit",
		Provider:  "openrouter",
		Model:     "openrouter/auto",
	}}
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models []modelResponse `json:"models"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(resp.Models))
	}
	if got := resp.Models[0].Provider; got != "openrouter" {
		t.Fatalf("provider = %q, want %q", got, "openrouter")
	}
	if got := resp.Models[0].Model; got != "openrouter/auto" {
		t.Fatalf("model = %q, want %q", got, "openrouter/auto")
	}
}

func TestHandleListModels_ExposesElevenLabsASRProvider(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models []modelResponse `json:"models"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(resp.Models))
	}
	if got := resp.Models[0].Provider; got != "elevenlabs" {
		t.Fatalf("provider = %q, want %q", got, "elevenlabs")
	}
	if got := resp.Models[0].Model; got != "scribe_v1" {
		t.Fatalf("model = %q, want %q", got, "scribe_v1")
	}
	if resp.Models[0].DefaultModelAllowed {
		t.Fatal("elevenlabs ASR model should not be allowed as the default chat model")
	}
}

func TestHandleListModels_ReturnsProviderOptionsWithoutPersistingLegacyMigration(t *testing.T) {
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
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models          []modelResponse                 `json:"models"`
		ProviderOptions []providers.ModelProviderOption `json:"provider_options"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(resp.Models))
	}
	if got := resp.Models[0].Provider; got != "openrouter" {
		t.Fatalf("provider = %q, want %q", got, "openrouter")
	}
	if got := resp.Models[0].Model; got != "openai/gpt-5.4" {
		t.Fatalf("model = %q, want %q", got, "openai/gpt-5.4")
	}

	optionsByID := make(map[string]providers.ModelProviderOption, len(resp.ProviderOptions))
	for _, option := range resp.ProviderOptions {
		optionsByID[option.ID] = option
	}
	if len(optionsByID) != 4 {
		t.Fatalf("provider_options count = %d, want 4", len(optionsByID))
	}
	if option := optionsByID["openai"]; option.DisplayName != "OpenAI Chat Completions" {
		t.Fatalf("openai display_name = %q", option.DisplayName)
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := updated.ModelList[0].Provider; got != "" {
		t.Fatalf("persisted provider = %q, want unchanged empty provider", got)
	}
	if got := updated.ModelList[0].Model; got != "openrouter/openai/gpt-5.4" {
		t.Fatalf("persisted model = %q, want unchanged legacy model", got)
	}
}

func TestHandleListModels_ReturnsProviderField(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "nvidia-glm",
		Provider:  "nvidia",
		Model:     "z-ai/glm-5.1",
		APIKeys:   config.SimpleSecureStrings("nv-key"),
	}}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models []modelResponse `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(resp.Models))
	}
	if got := resp.Models[0].Provider; got != "nvidia" {
		t.Fatalf("provider = %q, want %q", got, "nvidia")
	}
}

func TestHandleListModels_PreservesKnownProviderInCatalog(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "bedrock-claude",
		Model:     "bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0",
	}}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models          []modelResponse                 `json:"models"`
		ProviderOptions []providers.ModelProviderOption `json:"provider_options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(resp.Models))
	}
	if got := resp.Models[0].Provider; got != "bedrock" {
		t.Fatalf("provider = %q, want %q", got, "bedrock")
	}
	if got := resp.Models[0].Model; got != "us.anthropic.claude-sonnet-4-20250514-v1:0" {
		t.Fatalf("model = %q, want %q", got, "us.anthropic.claude-sonnet-4-20250514-v1:0")
	}
	optionCount := 0
	for _, option := range resp.ProviderOptions {
		optionCount++
		switch option.ID {
		case "openai", "openai-responses", "anthropic-messages", "gemini":
		default:
			t.Fatalf("unexpected provider option %q", option.ID)
		}
	}
	if optionCount != 4 {
		t.Fatalf("provider_options count = %d, want 4", optionCount)
	}
}

func TestHandleListModels_ReturnsEffectiveProviderField(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = []*config.ModelConfig{
		{
			ModelName: "plain-openai",
			Model:     "gpt-4o",
		},
		{
			ModelName: "explicit-google",
			Provider:  "google",
			Model:     "gemini-2.5-pro",
		},
		{
			ModelName: "explicit-qwen-intl",
			Provider:  "qwen-international",
			Model:     "qwen3-coder-plus",
		},
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Models []modelResponse `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(resp.Models) != 3 {
		t.Fatalf("len(models) = %d, want 3", len(resp.Models))
	}

	if got := resp.Models[0].Provider; got != "openai" {
		t.Fatalf("provider[0] = %q, want %q", got, "openai")
	}
	if got := resp.Models[0].Model; got != "gpt-4o" {
		t.Fatalf("model[0] = %q, want %q", got, "gpt-4o")
	}
	if got := resp.Models[1].Provider; got != "gemini" {
		t.Fatalf("provider[1] = %q, want %q", got, "gemini")
	}
	if got := resp.Models[1].Model; got != "gemini-2.5-pro" {
		t.Fatalf("model[1] = %q, want %q", got, "gemini-2.5-pro")
	}
	if got := resp.Models[2].Provider; got != "qwen-intl" {
		t.Fatalf("provider[2] = %q, want %q", got, "qwen-intl")
	}
	if got := resp.Models[2].Model; got != "qwen3-coder-plus" {
		t.Fatalf("model[2] = %q, want %q", got, "qwen3-coder-plus")
	}
}

func TestNormalizeIncomingModelConfigUsesConfiguredDefaultProvider(t *testing.T) {
	modelCfg := &config.ModelConfig{ModelName: "legacy", Model: "gpt-4o"}
	normalizeIncomingModelConfig(modelCfg, "openrouter")
	if modelCfg.Provider != "openrouter" || modelCfg.Model != "gpt-4o" {
		t.Fatalf("normalized model = %#v, want openrouter/gpt-4o", modelCfg)
	}
}

func TestModelGetHandlers_NormalizeDefaultChainSnapshot(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	writeModelChainConfig(t, configPath, []*config.ModelConfig{
		openAIModelConfig("primary"),
		geminiModelConfig("backup-b"),
		elevenLabsModelConfig("audio-only"),
	}, "primary", []string{"audio-only", "elevenlabs/scribe_v1", "backup-b"})

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, path := range []string{"/api/models", "/api/models/default-chain"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
			}
			var response defaultChainResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if response.DefaultModel != "primary" {
				t.Fatalf("default_model = %q, want primary", response.DefaultModel)
			}
			if !slices.Equal(response.FallbackChain, []string{"backup-b"}) {
				t.Fatalf("fallback_chain = %#v, want [backup-b]", response.FallbackChain)
			}
		})
	}
}

func TestHandleListModels_PreservesRawDefaultWithMultiKeyModel(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	writeModelChainConfig(t, configPath, []*config.ModelConfig{{
		ModelName: "primary",
		Provider:  "openai",
		Model:     "gpt-4o",
		APIKeys:   config.SimpleSecureStrings("sk-one", "sk-two"),
	}}, "openai/gpt-4o", nil)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		DefaultModel string `json:"default_model"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.DefaultModel != "openai/gpt-4o" {
		t.Fatalf("default_model = %q, want openai/gpt-4o", response.DefaultModel)
	}
}
