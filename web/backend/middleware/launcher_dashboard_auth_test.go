package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestLauncherSession(t *testing.T, sessions *LauncherDashboardSessions) string {
	t.Helper()
	value, err := sessions.Create()
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if value == "" {
		t.Fatal("session value should be non-empty")
	}
	return value
}

func TestLauncherDashboardSessionsAreIndependentAndRevocable(t *testing.T) {
	sessions := NewLauncherDashboardSessions()
	first := newTestLauncherSession(t, sessions)
	second := newTestLauncherSession(t, sessions)
	if first == second {
		t.Fatal("session values should be random")
	}
	if !sessions.Validate(first) || !sessions.Validate(second) {
		t.Fatal("new sessions should validate")
	}
	sessions.Revoke(first)
	if sessions.Validate(first) {
		t.Fatal("revoked session still validates")
	}
	if !sessions.Validate(second) {
		t.Fatal("revoking one session must not revoke another")
	}
	sessions.RevokeAll()
	if sessions.Validate(second) {
		t.Fatal("RevokeAll did not revoke session")
	}
}

func TestLauncherDashboardSessionsExpire(t *testing.T) {
	sessions := NewLauncherDashboardSessions()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sessions.now = func() time.Time { return now }
	value := newTestLauncherSession(t, sessions)
	now = now.Add(sessions.ttl)
	if sessions.Validate(value) {
		t.Fatal("expired session still validates")
	}
}

func TestLauncherDashboardSessionsGenerationPreventsStaleLogin(t *testing.T) {
	sessions := NewLauncherDashboardSessions()
	generation := sessions.Generation()
	sessions.RevokeAll()
	if _, err := sessions.CreateIfGeneration(generation); !errors.Is(err, ErrLauncherDashboardSessionsChanged) {
		t.Fatalf("CreateIfGeneration() error = %v, want %v", err, ErrLauncherDashboardSessionsChanged)
	}
	if _, err := sessions.CreateIfGeneration(sessions.Generation()); err != nil {
		t.Fatalf("CreateIfGeneration() with current generation error = %v", err)
	}
}

func mustLocalAutoLogin(t *testing.T, ttl time.Duration) *LauncherDashboardLocalAutoLogin {
	t.Helper()
	autoLogin, err := NewLauncherDashboardLocalAutoLogin(ttl)
	if err != nil {
		t.Fatalf("NewLauncherDashboardLocalAutoLogin() error = %v", err)
	}
	return autoLogin
}

func TestLauncherDashboardAuth_AllowsPublicPaths(t *testing.T) {
	sessions := NewLauncherDashboardSessions()
	cfg := LauncherDashboardAuthConfig{Sessions: sessions}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := LauncherDashboardAuth(cfg, next)

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/launcher-login", http.StatusTeapot},
		{http.MethodGet, "/launcher-setup", http.StatusTeapot},
		{http.MethodGet, "/theme-init.js", http.StatusTeapot},
		{http.MethodGet, "/assets/index.js", http.StatusTeapot},
		{http.MethodPost, "/api/auth/login", http.StatusTeapot},
		{http.MethodGet, "/api/auth/status", http.StatusTeapot},
		{http.MethodPost, "/api/auth/setup", http.StatusTeapot},
		{http.MethodPost, "/api/auth/logout", http.StatusTeapot},
		{http.MethodGet, "/oauth/callback", http.StatusTeapot},
		{http.MethodGet, "/oauth/callback.js", http.StatusTeapot},
		{http.MethodPost, "/oauth/callback.js", http.StatusFound},
		{http.MethodGet, "/api/auth/logout", http.StatusUnauthorized},
		{http.MethodGet, "/api/config", http.StatusUnauthorized},
		{http.MethodGet, "/pico/ws", http.StatusUnauthorized},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s %s: status = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

func TestLauncherDashboardAuth_QueryTokenDoesNotAuthenticate(t *testing.T) {
	cfg := LauncherDashboardAuthConfig{Sessions: NewLauncherDashboardSessions()}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not run without session cookie")
	})
	h := LauncherDashboardAuth(cfg, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/?token=secret", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/launcher-login" {
		t.Fatalf("GET /?token=secret: code=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLauncherDashboardAuth_LocalAutoLogin(t *testing.T) {
	autoLogin := mustLocalAutoLogin(t, time.Minute)
	cfg := LauncherDashboardAuthConfig{
		Sessions:       NewLauncherDashboardSessions(),
		LocalAutoLogin: autoLogin,
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := LauncherDashboardAuth(cfg, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, LauncherDashboardLocalAutoLoginPath, nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/launcher-login" ||
		len(rec.Result().Cookies()) != 0 {
		t.Fatalf(
			"auto-login without nonce code=%d loc=%q cookies=%#v",
			rec.Code,
			rec.Header().Get("Location"),
			rec.Result().Cookies(),
		)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, LauncherDashboardLocalAutoLoginPath+"?nonce=wrong", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/launcher-login" ||
		len(rec.Result().Cookies()) != 0 {
		t.Fatalf(
			"auto-login with wrong nonce code=%d loc=%q cookies=%#v",
			rec.Code,
			rec.Header().Get("Location"),
			rec.Result().Cookies(),
		)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodHead, autoLogin.URLPath(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/launcher-login" ||
		len(rec.Result().Cookies()) != 0 {
		t.Fatalf(
			"auto-login HEAD code=%d loc=%q cookies=%#v",
			rec.Code,
			rec.Header().Get("Location"),
			rec.Result().Cookies(),
		)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, autoLogin.URLPath(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("local auto-login code=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != LauncherDashboardCookieName || !cfg.Sessions.Validate(cookies[0].Value) {
		t.Fatalf("cookies = %#v", cookies)
	}
	if cookies[0].MaxAge != 31*24*3600 {
		t.Fatalf("session cookie MaxAge = %d, want 31 days", cookies[0].MaxAge)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie auth after auto-login status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, autoLogin.URLPath(), nil)
	req.AddCookie(cookies[0])
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("auto-login path with existing session code=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, autoLogin.URLPath(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/launcher-login" {
		t.Fatalf("consumed auto-login code=%d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLauncherDashboardAuth_LocalAutoLoginRequiresValidNonceAndUnexpired(t *testing.T) {
	newHandler := func(autoLogin *LauncherDashboardLocalAutoLogin) http.Handler {
		return LauncherDashboardAuth(LauncherDashboardAuthConfig{
			Sessions:       NewLauncherDashboardSessions(),
			LocalAutoLogin: autoLogin,
		}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
	}

	autoLogin := mustLocalAutoLogin(t, time.Minute)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, autoLogin.URLPath(), nil)
	req.RemoteAddr = "192.168.1.50:12345"
	req.Host = "192.168.1.50:18800"
	newHandler(autoLogin).ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || len(rec.Result().Cookies()) != 1 {
		t.Fatalf("capability auto-login code=%d cookies=%#v", rec.Code, rec.Result().Cookies())
	}

	expired := mustLocalAutoLogin(t, -time.Second)
	h := newHandler(expired)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, expired.URLPath(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("expired auto-login code=%d cookies=%#v", rec.Code, rec.Result().Cookies())
	}
}

func TestLauncherDashboardAuth_DotDotCannotBypass(t *testing.T) {
	cfg := LauncherDashboardAuthConfig{Sessions: NewLauncherDashboardSessions()}
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not run without auth")
	})
	h := LauncherDashboardAuth(cfg, next)

	for _, p := range []string{
		"/assets/../api/config",
		"/launcher-login/../api/config",
		"/./api/config",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, p, nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%q: status = %d, want %d", p, rec.Code, http.StatusUnauthorized)
		}
	}
}

func TestLauncherDashboardAuth_CookieOnly(t *testing.T) {
	sessions := NewLauncherDashboardSessions()
	cookieVal := newTestLauncherSession(t, sessions)
	cfg := LauncherDashboardAuthConfig{Sessions: sessions}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := LauncherDashboardAuth(cfg, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: LauncherDashboardCookieName, Value: cookieVal})
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie auth: status = %d", rec.Code)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req2.Header.Set("Authorization", "Bearer dashboard-secret-9")
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("bearer auth should not be accepted: status = %d", rec2.Code)
	}
}

func TestLauncherDashboardAuth_RejectsCrossOriginMutation(t *testing.T) {
	sessions := NewLauncherDashboardSessions()
	session := newTestLauncherSession(t, sessions)
	nextCalls := 0
	h := LauncherDashboardAuth(LauncherDashboardAuthConfig{Sessions: sessions}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalls++
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		name       string
		origin     string
		fetchSite  string
		wantStatus int
	}{
		{name: "cross-origin sibling", origin: "https://other.example.test", fetchSite: "same-site", wantStatus: http.StatusForbidden},
		{name: "same-origin", origin: "http://launcher.local", fetchSite: "same-origin", wantStatus: http.StatusNoContent},
		{name: "non-browser client", wantStatus: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://launcher.local/api/config/reset", nil)
			req.AddCookie(&http.Cookie{Name: LauncherDashboardCookieName, Value: session})
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tc.wantStatus)
			}
		})
	}
	if nextCalls != 2 {
		t.Fatalf("next handler called %d times, want 2", nextCalls)
	}
}

func TestLauncherDashboardAuth_AllowsConfiguredPublicURLForMutationAndWebSocket(t *testing.T) {
	sessions := NewLauncherDashboardSessions()
	session := newTestLauncherSession(t, sessions)
	access := mustExternalAccess(t, "http://gogoclaw.example.test:666", []string{"10.0.0.0/8"})
	h := LauncherDashboardAuth(LauncherDashboardAuthConfig{
		Sessions:       sessions,
		ExternalAccess: access,
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pico/ws" {
			w.WriteHeader(http.StatusSwitchingProtocols)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "http://gogoclaw:18800/api/config/reset", nil)
	req.Host = "gogoclaw"
	req.RemoteAddr = "10.0.0.8:1234"
	req.AddCookie(&http.Cookie{Name: LauncherDashboardCookieName, Value: session})
	req.Header.Set("Origin", "http://gogoclaw.example.test:666")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("mutation status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	ws := httptest.NewRequest(http.MethodGet, "http://gogoclaw:18800/pico/ws", nil)
	ws.Host = "gogoclaw"
	ws.RemoteAddr = "10.0.0.8:1234"
	ws.AddCookie(&http.Cookie{Name: LauncherDashboardCookieName, Value: session})
	ws.Header.Set("Origin", "http://gogoclaw.example.test:666")
	wsRec := httptest.NewRecorder()
	h.ServeHTTP(wsRec, ws)
	if wsRec.Code != http.StatusSwitchingProtocols {
		t.Fatalf("websocket status = %d, want %d", wsRec.Code, http.StatusSwitchingProtocols)
	}
}

func TestLauncherDashboardAuth_AllowsTrustedForwardedOrigin(t *testing.T) {
	sessions := NewLauncherDashboardSessions()
	session := newTestLauncherSession(t, sessions)
	h := LauncherDashboardAuth(LauncherDashboardAuthConfig{
		Sessions:       sessions,
		ExternalAccess: mustExternalAccess(t, "", []string{"10.0.0.0/8"}),
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "http://gogoclaw:18800/api/config/reset", nil)
	req.RemoteAddr = "10.0.0.8:1234"
	req.AddCookie(&http.Cookie{Name: LauncherDashboardCookieName, Value: session})
	req.Header.Set("Origin", "https://chat.example.test:8443")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Forwarded-Host", "chat.example.test")
	req.Header.Set("X-Forwarded-Port", "8443")
	req.Header.Set("X-Forwarded-Proto", "https")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestLauncherDashboardAuth_RejectsSpoofedForwardedOrigin(t *testing.T) {
	sessions := NewLauncherDashboardSessions()
	session := newTestLauncherSession(t, sessions)
	h := LauncherDashboardAuth(LauncherDashboardAuthConfig{
		Sessions:       sessions,
		ExternalAccess: mustExternalAccess(t, "", []string{"10.0.0.0/8"}),
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "http://gogoclaw:18800/api/config/reset", nil)
	req.RemoteAddr = "203.0.113.8:1234"
	req.AddCookie(&http.Cookie{Name: LauncherDashboardCookieName, Value: session})
	req.Header.Set("Origin", "https://chat.example.test:8443")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Forwarded-Host", "chat.example.test")
	req.Header.Set("X-Forwarded-Port", "8443")
	req.Header.Set("X-Forwarded-Proto", "https")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestDefaultLauncherDashboardSecureCookieIgnoresSpoofedForwardedProto(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://launcher.local/", nil)
	req.RemoteAddr = "203.0.113.8:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	if DefaultLauncherDashboardSecureCookie(req) {
		t.Fatal("untrusted X-Forwarded-Proto enabled Secure cookie")
	}

	req.RemoteAddr = "10.0.0.8:1234"
	if got := RequestScheme(req, []string{"10.0.0.0/8"}); got != "https" {
		t.Fatalf("trusted proxy scheme = %q, want https", got)
	}
}

func TestExternalAccessUsesHTTPSFromConfiguredPublicURLForSecureCookie(t *testing.T) {
	access := mustExternalAccess(t, "https://gogoclaw.example.test:8443", []string{"10.0.0.0/8"})
	req := httptest.NewRequest(http.MethodGet, "http://gogoclaw:18800/", nil)
	req.RemoteAddr = "10.0.0.8:1234"

	if got := access.Scheme(req); got != "https" {
		t.Fatalf("scheme = %q, want https from public URL", got)
	}
}

func TestLauncherDashboardAuth_WebSocketUnauthorizedDoesNotRedirect(t *testing.T) {
	cfg := LauncherDashboardAuthConfig{Sessions: NewLauncherDashboardSessions()}
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not run without auth")
	})
	h := LauncherDashboardAuth(cfg, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pico/ws", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("Location = %q, want empty", got)
	}
}

func TestLauncherDashboardAuth_WebSocketRejectsCrossOrigin(t *testing.T) {
	sessions := NewLauncherDashboardSessions()
	value := newTestLauncherSession(t, sessions)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	h := LauncherDashboardAuth(LauncherDashboardAuthConfig{Sessions: sessions}, next)
	cookie := &http.Cookie{Name: LauncherDashboardCookieName, Value: value}

	req := httptest.NewRequest(http.MethodGet, "http://launcher.local/pico/ws", nil)
	req.Host = "launcher.local"
	req.AddCookie(cookie)
	req.Header.Set("Origin", "http://evil.local")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin websocket status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	req = httptest.NewRequest(http.MethodGet, "http://launcher.local/pico/ws", nil)
	req.Host = "launcher.local"
	req.AddCookie(cookie)
	req.Header.Set("Origin", "http://launcher.local")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSwitchingProtocols {
		t.Fatalf("same-origin websocket status = %d, want %d", rec.Code, http.StatusSwitchingProtocols)
	}
}
