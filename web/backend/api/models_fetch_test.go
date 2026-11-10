package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{
			name: "empty key",
			key:  "",
			want: "",
		},
		{
			name: "short key fully masked",
			key:  "abcd",
			want: "****",
		},
		{
			name: "length 8 boundary fully masked",
			key:  "12345678",
			want: "****",
		},
		{
			name: "length 9 boundary shows last 2",
			key:  "123456789",
			want: "123****89",
		},
		{
			name: "length 12 boundary shows last 2",
			key:  "abcdefghijkl",
			want: "abc****kl",
		},
		{
			name: "length 13 boundary shows last 4",
			key:  "abcdefghijklm",
			want: "abc****jklm",
		},
		{
			name: "typical api key",
			key:  "sk-1234567890abcd",
			want: "sk-****abcd",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := maskAPIKey(tc.key)
			if got != tc.want {
				t.Fatalf("maskAPIKey(%q) = %q, want %q", tc.key, got, tc.want)
			}

			if tc.key != "" {
				displayed := strings.Replace(tc.want, "****", "", 1)
				if len(tc.key) <= 8 {
					if displayed != "" {
						t.Fatalf("maskAPIKey(%q) displayed part = %q, want empty", tc.key, displayed)
					}
				} else {
					if len(displayed)*10 > len(tc.key)*6 {
						t.Fatalf(
							"maskAPIKey(%q) displayed length = %d, want at most 60%% of %d",
							tc.key,
							len(displayed),
							len(tc.key),
						)
					}
				}
			}
		})
	}
}

func TestFetchOpenAICompatibleModels_ResponseShapes(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		apiKey    string
		wantLen   int
		wantFirst struct {
			id, ownedBy string
		}
		wantSecond struct {
			id, ownedBy string
		}
	}{
		{
			name:     "envelope shape",
			response: `{"data":[{"id":"gpt-4o","owned_by":"openai"},{"id":"gpt-4o-mini","owned_by":"openai"}]}`,
			apiKey:   "test-key",
			wantLen:  2,
			wantFirst: struct {
				id, ownedBy string
			}{id: "gpt-4o", ownedBy: "openai"},
			wantSecond: struct {
				id, ownedBy string
			}{id: "gpt-4o-mini", ownedBy: "openai"},
		},
		{
			name:     "bare array shape",
			response: `[{"id":"qwen-max","owned_by":"qwen"},{"id":"qwen-plus","owned_by":"qwen"}]`,
			apiKey:   "",
			wantLen:  2,
			wantFirst: struct {
				id, ownedBy string
			}{id: "qwen-max", ownedBy: "qwen"},
			wantSecond: struct {
				id, ownedBy string
			}{id: "qwen-plus", ownedBy: "qwen"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tt.response)
			}))
			defer srv.Close()

			models, err := fetchOpenAICompatibleModels(t.Context(), srv.URL+"/models", tt.apiKey, true)
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if len(models) != tt.wantLen {
				t.Fatalf("len(models) = %d, want %d", len(models), tt.wantLen)
			}
			if models[0].ID != tt.wantFirst.id || models[0].OwnedBy != tt.wantFirst.ownedBy {
				t.Fatalf("models[0] = %+v, want {ID:%s OwnedBy:%s}", models[0], tt.wantFirst.id, tt.wantFirst.ownedBy)
			}
			if models[1].ID != tt.wantSecond.id || models[1].OwnedBy != tt.wantSecond.ownedBy {
				t.Fatalf("models[1] = %+v, want {ID:%s OwnedBy:%s}", models[1], tt.wantSecond.id, tt.wantSecond.ownedBy)
			}
		})
	}
}

func TestFetchOpenAICompatibleModels_EmptyEnvelopeReturnsEmptySlice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()

	models, err := fetchOpenAICompatibleModels(t.Context(), srv.URL+"/models", "k", true)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("len(models) = %d, want 0", len(models))
	}
}

func TestFetchOpenAICompatibleModels_EmptyBareArrayReturnsEmptySlice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()

	models, err := fetchOpenAICompatibleModels(t.Context(), srv.URL+"/models", "k", true)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("len(models) = %d, want 0", len(models))
	}
}

func TestFetchOpenAICompatibleModels_UnrecognizedShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[],"error":"unsupported"}`)
	}))
	defer srv.Close()

	_, err := fetchOpenAICompatibleModels(t.Context(), srv.URL+"/models", "k", true)
	if err == nil {
		t.Fatal("error = nil, want unrecognized shape error")
	}
	if !strings.Contains(err.Error(), "unrecognized shape") {
		t.Fatalf("error = %q, want it to contain 'unrecognized shape'", err.Error())
	}
}

func TestFetchOpenAICompatibleModels_FiltersEmptyIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[`+
			`{"id":"gpt-4o","owned_by":"openai"},`+
			`{"id":"","owned_by":"openai"},`+
			`{"id":"gpt-4o-mini"}]}`)
	}))
	defer srv.Close()

	models, err := fetchOpenAICompatibleModels(t.Context(), srv.URL+"/models", "k", true)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2 (empty IDs should be filtered)", len(models))
	}
	if models[0].ID != "gpt-4o" {
		t.Fatalf("models[0].ID = %q, want %q", models[0].ID, "gpt-4o")
	}
	if models[1].ID != "gpt-4o-mini" {
		t.Fatalf("models[1].ID = %q, want %q", models[1].ID, "gpt-4o-mini")
	}
}

func TestFetchOpenAICompatibleModels_SetsAuthorizationHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"m1"}]}`)
	}))
	defer srv.Close()

	if _, err := fetchOpenAICompatibleModels(t.Context(), srv.URL+"/models", "my-secret-key", true); err != nil {
		t.Fatalf("error = %v", err)
	}
	if gotAuth != "Bearer my-secret-key" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer my-secret-key")
	}
}

func TestFetchOpenAICompatibleModels_NoAuthHeaderWhenKeyEmpty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":"m1"}]`)
	}))
	defer srv.Close()

	if _, err := fetchOpenAICompatibleModels(t.Context(), srv.URL+"/models", "", true); err != nil {
		t.Fatalf("error = %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization = %q, want empty", gotAuth)
	}
}

func TestHandleFetchModels_SiliconFlowUsesOpenAICompatibleEndpoint(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	var gotPath string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"deepseek-ai/DeepSeek-V3","owned_by":"siliconflow"}]}`)
	}))
	defer srv.Close()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/fetch", bytes.NewBufferString(fmt.Sprintf(`{
		"provider":"siliconflow",
		"api_key":"sk-siliconflow",
		"api_base":"%s"
	}`, srv.URL)))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if gotPath != "/models" {
		t.Fatalf("path = %q, want %q", gotPath, "/models")
	}
	if gotAuth != "Bearer sk-siliconflow" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer sk-siliconflow")
	}

	var resp struct {
		Models []upstreamModel `json:"models"`
		Total  int             `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Total != 1 || len(resp.Models) != 1 {
		t.Fatalf("response = %+v, want one fetched model", resp)
	}
	if resp.Models[0].ID != "deepseek-ai/DeepSeek-V3" {
		t.Fatalf("model id = %q, want %q", resp.Models[0].ID, "deepseek-ai/DeepSeek-V3")
	}
}

func TestHandleFetchModels_NearAIUsesPublicModelListEndpoint(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	var gotPath string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[`+
			`{"modelId":"zai-org/GLM-5.1-FP8","metadata":{"ownedBy":"nearai"}},`+
			`{"modelId":"openai/gpt-oss-120b","metadata":{"ownedBy":"nearai"}},`+
			`{"modelId":""}]}`)
	}))
	defer srv.Close()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/fetch", bytes.NewBufferString(fmt.Sprintf(`{
		"provider":"nearai",
		"api_key":"nearai-key",
		"api_base":"%s"
	}`, srv.URL)))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if gotPath != "/model/list" {
		t.Fatalf("path = %q, want %q", gotPath, "/model/list")
	}
	if gotAuth != "Bearer nearai-key" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer nearai-key")
	}

	var resp struct {
		Models []upstreamModel `json:"models"`
		Total  int             `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Total != 2 || len(resp.Models) != 2 {
		t.Fatalf("response = %+v, want two fetched models", resp)
	}
	if resp.Models[0].ID != "zai-org/GLM-5.1-FP8" || resp.Models[0].OwnedBy != "nearai" {
		t.Fatalf("models[0] = %+v, want GLM model owned by nearai", resp.Models[0])
	}
	if resp.Models[1].ID != "openai/gpt-oss-120b" || resp.Models[1].OwnedBy != "nearai" {
		t.Fatalf("models[1] = %+v, want GPT OSS model owned by nearai", resp.Models[1])
	}
}

func TestHandleFetchModels_ModelIndexUsesStoredKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-4o","owned_by":"openai"}]}`)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	oldHome := os.Getenv("GOGOCLAW_HOME")
	t.Setenv("GOGOCLAW_HOME", filepath.Join(tmp, ".gogoclaw"))
	defer func() {
		if oldHome != "" {
			os.Setenv("GOGOCLAW_HOME", oldHome)
		} else {
			os.Unsetenv("GOGOCLAW_HOME")
		}
	}()

	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{
		{
			ModelName: "my-openai",
			Provider:  "openai",
			Model:     "gpt-4o",
			APIKeys:   config.SimpleSecureStrings("sk-stored-secret"),
			APIBase:   srv.URL,
		},
	}
	configPath := filepath.Join(tmp, "config.json")
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	idx := 0
	body := fmt.Sprintf(`{"provider":"openai","api_base":"%s","model_index":%d}`, srv.URL, idx)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/fetch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotAuth != "Bearer sk-stored-secret" {
		t.Fatalf("Authorization = %q, want stored key to be used", gotAuth)
	}

	var resp struct {
		Models []upstreamModel `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if len(resp.Models) != 1 || resp.Models[0].ID != "gpt-4o" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestHandleFetchModels_ModelIndexProviderMismatchRejectsKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("stored key should NOT be sent to mismatched provider")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	t.Setenv("GOGOCLAW_HOME", filepath.Join(tmp, ".gogoclaw"))

	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{
		{
			ModelName: "my-openai",
			Provider:  "openai",
			Model:     "gpt-4o",
			APIKeys:   config.SimpleSecureStrings("sk-openai-secret"),
			APIBase:   "https://api.openai.com/v1",
		},
	}
	configPath := filepath.Join(tmp, "config.json")
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := fmt.Sprintf(`{"provider":"siliconflow","api_base":"%s","model_index":0}`, srv.URL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/fetch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
}

func TestFetchOpenAICompatibleModelsBlocksPrivateTargetUnlessExplicitlyAllowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	if _, err := fetchOpenAICompatibleModels(t.Context(), server.URL+"/models", "", false); err == nil {
		t.Fatal("expected private target to be blocked")
	}
	if _, err := fetchOpenAICompatibleModels(t.Context(), server.URL+"/models", "", true); err != nil {
		t.Fatalf("explicit local target should be allowed: %v", err)
	}
}

func TestHasLocalAPIBase(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		wanted bool
	}{
		{name: "localhost", raw: "http://localhost:11434/v1", wanted: true},
		{name: "IPv4 loopback", raw: "http://127.0.0.1:11434/v1", wanted: true},
		{name: "IPv6 loopback", raw: "http://[::1]:11434/v1", wanted: true},
		{name: "wildcard bind", raw: "http://0.0.0.0:11434/v1", wanted: true},
		{name: "private 10/8", raw: "http://10.0.0.1:8000/v1", wanted: true},
		{name: "docker bridge gateway", raw: "http://172.17.0.1:3425", wanted: true},
		{name: "private 172/12 upper bound", raw: "http://172.31.255.254:3425", wanted: true},
		{name: "private 192.168/16", raw: "http://192.168.1.10:8000/v1", wanted: true},
		{name: "IPv6 ULA", raw: "http://[fd12:3456::1]:8000/v1", wanted: true},
		{name: "link local", raw: "http://169.254.1.1:8000/v1", wanted: false},
		{name: "CGNAT", raw: "http://100.64.0.1:8000/v1", wanted: false},
		{name: "public IP", raw: "http://172.32.0.1:8000/v1", wanted: false},
		{name: "public hostname", raw: "https://api.example.com/v1", wanted: false},
		{name: "empty", raw: "", wanted: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasLocalAPIBase(tt.raw); got != tt.wanted {
				t.Fatalf("hasLocalAPIBase(%q) = %v, want %v", tt.raw, got, tt.wanted)
			}
		})
	}
}

func TestProbeOllamaModelBlocksLinkLocalTargets(t *testing.T) {
	if probeOllamaModel("http://169.254.1.1:11434/v1", "llama3") {
		t.Fatal("expected link-local Ollama target to be blocked")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3:latest"}]}`))
	}))
	defer server.Close()
	if !probeOllamaModel(server.URL+"/v1", "llama3") {
		t.Fatal("expected loopback Ollama target to be allowed")
	}
}

func TestProbeTCPServiceBlocksLinkLocalTargets(t *testing.T) {
	if probeTCPService("http://169.254.1.1:4321") {
		t.Fatal("expected link-local TCP target to be blocked")
	}

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	if !probeTCPService(server.URL) {
		t.Fatal("expected loopback TCP target to be allowed")
	}
}

func TestFetchOpenAICompatibleModelsRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("x"), maxModelResponseBytes+1))
	}))
	defer server.Close()

	_, err := fetchOpenAICompatibleModels(t.Context(), server.URL+"/models", "", true)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}
