package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/auth"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

func TestOAuthLoginRejectsUnsupportedMethod(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/login",
		strings.NewReader(`{"provider":"anthropic","method":"browser"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestOAuthBrowserFlowCreatedAndQueried(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)

	oauthGeneratePKCE = func() (auth.PKCECodes, error) {
		return auth.PKCECodes{CodeVerifier: "verifier-1", CodeChallenge: "challenge-1"}, nil
	}
	oauthGenerateState = func() (string, error) { return "state-1", nil }
	oauthBuildAuthorizeURL = func(cfg auth.OAuthProviderConfig, pkce auth.PKCECodes, state, redirectURI string) string {
		return "https://example.com/authorize?state=" + state
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/login",
		strings.NewReader(`{"provider":"openai","method":"browser"}`),
	)
	req.Host = "localhost:18800"
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var loginResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("unmarshal login response: %v", err)
	}
	flowID, _ := loginResp["flow_id"].(string)
	if flowID == "" {
		t.Fatalf("flow_id is empty: %v", loginResp)
	}
	if loginResp["auth_url"] != "https://example.com/authorize?state=state-1" {
		t.Fatalf("unexpected auth_url: %v", loginResp["auth_url"])
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/oauth/flows/"+flowID, nil)
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("flow status code = %d, want %d, body=%s", rec2.Code, http.StatusOK, rec2.Body.String())
	}
	var flowResp oauthFlowResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &flowResp); err != nil {
		t.Fatalf("unmarshal flow response: %v", err)
	}
	if flowResp.Status != oauthFlowPending {
		t.Fatalf("flow status = %q, want %q", flowResp.Status, oauthFlowPending)
	}
	if flowResp.Method != oauthMethodBrowser {
		t.Fatalf("flow method = %q, want %q", flowResp.Method, oauthMethodBrowser)
	}
}

func TestOAuthFlowExpiresWhenQueried(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	oauthNow = func() time.Time { return now }

	h := NewHandler(configPath)
	h.storeOAuthFlow(&oauthFlow{
		ID:        "expired-flow",
		Provider:  oauthProviderOpenAI,
		Method:    oauthMethodBrowser,
		Status:    oauthFlowPending,
		CreatedAt: now.Add(-20 * time.Minute),
		UpdatedAt: now.Add(-20 * time.Minute),
		ExpiresAt: now.Add(-1 * time.Minute),
	})

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/oauth/flows/expired-flow", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var flowResp oauthFlowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &flowResp); err != nil {
		t.Fatalf("unmarshal flow response: %v", err)
	}
	if flowResp.Status != oauthFlowExpired {
		t.Fatalf("flow status = %q, want %q", flowResp.Status, oauthFlowExpired)
	}
}

func TestOAuthCallbackUnknownState(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?state=unknown&code=abc", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "OAuth flow not found") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestOAuthCallbackPageUsesExternalScript(t *testing.T) {
	h := NewHandler(filepath.Join(t.TempDir(), "config.json"))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth/callback?state=unknown", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `type="application/json"`) {
		t.Fatalf("callback page does not use external script: %s", body)
	}
	if strings.Contains(body, "<script>(function") {
		t.Fatalf("callback page still contains inline executable script: %s", body)
	}

	match := regexp.MustCompile(`<script\s+src="([^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("callback page has no external script source: %s", body)
	}
	scriptPath := match[1]

	scriptRec := httptest.NewRecorder()
	mux.ServeHTTP(scriptRec, httptest.NewRequest(http.MethodGet, scriptPath, nil))
	if scriptRec.Code != http.StatusOK {
		t.Fatalf("script %q status = %d, want %d", scriptPath, scriptRec.Code, http.StatusOK)
	}
	if got := scriptRec.Header().Get("Content-Type"); got != "application/javascript; charset=utf-8" {
		t.Fatalf("script Content-Type = %q", got)
	}
}

func TestOAuthRedirectURIOnlyTrustsForwardedProtoFromProxy(t *testing.T) {
	h := NewHandler(filepath.Join(t.TempDir(), "config.json"))
	h.SetServerAccessOptions(false, mustExternalAccess(t, "", []string{"10.0.0.0/8"}))

	request := func(remoteAddr string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "http://launcher.local/", nil)
		req.Host = "launcher.local"
		req.RemoteAddr = remoteAddr
		req.Header.Set("X-Forwarded-Proto", "https")
		return req
	}
	if got := h.buildOAuthRedirectURI(request("10.0.0.8:1234")); got != "https://launcher.local/oauth/callback" {
		t.Fatalf("trusted redirect URI = %q", got)
	}
	if got := h.buildOAuthRedirectURI(request("203.0.113.8:1234")); got != "http://launcher.local/oauth/callback" {
		t.Fatalf("untrusted redirect URI = %q", got)
	}
}

func TestOAuthRedirectURIUsesConfiguredPublicURL(t *testing.T) {
	h := NewHandler(filepath.Join(t.TempDir(), "config.json"))
	h.SetServerAccessOptions(false, mustExternalAccess(t, "https://gogoclaw.example.test:8443", []string{"10.0.0.0/8"}))

	req := httptest.NewRequest(http.MethodGet, "http://gogoclaw:18800/", nil)
	req.Host = "gogoclaw:18800"
	req.RemoteAddr = "10.0.0.8:1234"

	if got := h.buildOAuthRedirectURI(req); got != "https://gogoclaw.example.test:8443/oauth/callback" {
		t.Fatalf("redirect URI = %q, want configured public URL", got)
	}
}

func TestOAuthLogoutClearsCredentialAndConfig(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	cfg.ModelList = append(cfg.ModelList, &config.ModelConfig{
		ModelName:  "gpt-5.4",
		Model:      "openai/gpt-5.4",
		AuthMethod: "oauth",
	})
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}
	if err = auth.SetCredential(oauthProviderOpenAI, &auth.AuthCredential{
		AccessToken: "token-before-logout",
		Provider:    oauthProviderOpenAI,
		AuthMethod:  "oauth",
	}); err != nil {
		t.Fatalf("SetCredential error: %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/logout", bytes.NewBufferString(`{"provider":"openai"}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	cred, err := auth.GetCredential(oauthProviderOpenAI)
	if err != nil {
		t.Fatalf("GetCredential error: %v", err)
	}
	if cred != nil {
		t.Fatalf("expected credential deleted, got %#v", cred)
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	for _, m := range updated.ModelList {
		if strings.HasPrefix(m.Model, "openai/") && m.AuthMethod != "" {
			t.Fatalf("openai model auth_method = %q, want empty", m.AuthMethod)
		}
	}
}

func TestOAuthLogoutClearsAuthMethodForExplicitProviderField(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	resetOAuthHooks(t)

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	cfg.ModelList = append(cfg.ModelList, &config.ModelConfig{
		ModelName:  "gpt-5.4",
		Provider:   "openai",
		Model:      "gpt-5.4",
		AuthMethod: "oauth",
	})
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}
	if err = auth.SetCredential(oauthProviderOpenAI, &auth.AuthCredential{
		AccessToken: "token-before-logout",
		Provider:    oauthProviderOpenAI,
		AuthMethod:  "oauth",
	}); err != nil {
		t.Fatalf("SetCredential error: %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/logout", bytes.NewBufferString(`{"provider":"openai"}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if got := updated.ModelList[len(updated.ModelList)-1].AuthMethod; got != "" {
		t.Fatalf("auth_method = %q, want empty", got)
	}
}

func setupOAuthTestEnv(t *testing.T) (string, func()) {
	t.Helper()

	tmp := t.TempDir()
	oldHome := os.Getenv("HOME")
	oldPicoHome := os.Getenv("GOGOCLAW_HOME")

	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	if err := os.Setenv("GOGOCLAW_HOME", filepath.Join(tmp, ".gogoclaw")); err != nil {
		t.Fatalf("set GOGOCLAW_HOME: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "custom-default",
		Model:     "openai/gpt-4o",
		APIKeys:   config.SimpleSecureStrings("sk-default"),
	}}
	cfg.Agents.Defaults.ModelName = "custom-default"

	configPath := filepath.Join(tmp, "config.json")
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}

	cleanup := func() {
		_ = os.Setenv("HOME", oldHome)
		if oldPicoHome == "" {
			_ = os.Unsetenv("GOGOCLAW_HOME")
		} else {
			_ = os.Setenv("GOGOCLAW_HOME", oldPicoHome)
		}
	}
	return configPath, cleanup
}

func resetOAuthHooks(t *testing.T) {
	t.Helper()

	origNow := oauthNow
	origGeneratePKCE := oauthGeneratePKCE
	origGenerateState := oauthGenerateState
	origBuildAuthorizeURL := oauthBuildAuthorizeURL
	origRequestDeviceCode := oauthRequestDeviceCode
	origPollDeviceCodeOnce := oauthPollDeviceCodeOnce
	origExchangeCodeForTokens := oauthExchangeCodeForTokens
	origGetCredential := oauthGetCredential
	origSetCredential := oauthSetCredential
	origDeleteCredential := oauthDeleteCredential
	origLoadConfig := oauthLoadConfig
	origSaveConfig := oauthSaveConfig
	origFetchProject := oauthFetchAntigravityProject
	origFetchGoogleEmail := oauthFetchGoogleUserEmailFunc

	t.Cleanup(func() {
		oauthNow = origNow
		oauthGeneratePKCE = origGeneratePKCE
		oauthGenerateState = origGenerateState
		oauthBuildAuthorizeURL = origBuildAuthorizeURL
		oauthRequestDeviceCode = origRequestDeviceCode
		oauthPollDeviceCodeOnce = origPollDeviceCodeOnce
		oauthExchangeCodeForTokens = origExchangeCodeForTokens
		oauthGetCredential = origGetCredential
		oauthSetCredential = origSetCredential
		oauthDeleteCredential = origDeleteCredential
		oauthLoadConfig = origLoadConfig
		oauthSaveConfig = origSaveConfig
		oauthFetchAntigravityProject = origFetchProject
		oauthFetchGoogleUserEmailFunc = origFetchGoogleEmail
	})
}
