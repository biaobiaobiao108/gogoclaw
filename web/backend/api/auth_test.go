package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/web/backend/middleware"
)

const testLauncherSetupToken = "test-setup-token"

type fakePasswordStore struct {
	mu          sync.Mutex
	initialized bool
	password    string
	err         error
}

func (s *fakePasswordStore) IsInitialized(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	return s.initialized, nil
}

func (s *fakePasswordStore) SetPassword(_ context.Context, plain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.password = plain
	s.initialized = true
	return nil
}

func (s *fakePasswordStore) SetPasswordIfUninitialized(_ context.Context, plain string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	if s.initialized {
		return false, nil
	}
	s.password = plain
	s.initialized = true
	return true, nil
}

func (s *fakePasswordStore) VerifyPassword(_ context.Context, plain string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	return s.initialized && plain == s.password, nil
}

func newLauncherAuthTestHandler(
	t *testing.T,
	store PasswordStore,
) (*http.ServeMux, *middleware.LauncherDashboardSessions) {
	t.Helper()
	sessions := middleware.NewLauncherDashboardSessions()
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		Sessions:      sessions,
		SetupToken:    testLauncherSetupToken,
		PasswordStore: store,
	})
	return mux, sessions
}

func issueLauncherTestSession(
	t *testing.T,
	sessions *middleware.LauncherDashboardSessions,
) *http.Cookie {
	t.Helper()
	value, err := sessions.Create()
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return &http.Cookie{Name: middleware.LauncherDashboardCookieName, Value: value}
}

func launcherAuthStatus(t *testing.T, mux http.Handler, cookie *http.Cookie) (int, bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	mux.ServeHTTP(rec, req)
	var body struct {
		Authenticated bool `json:"authenticated"`
		Initialized   bool `json:"initialized"`
	}
	if rec.Code == http.StatusOK {
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, body.Authenticated
}

func TestLauncherAuthLoginStatusAndLogout(t *testing.T) {
	const password = "dashboard-test-password"
	store := &fakePasswordStore{initialized: true, password: password}
	mux, sessions := newLauncherAuthTestHandler(t, store)

	if code, authed := launcherAuthStatus(t, mux, nil); code != http.StatusOK || authed {
		t.Fatalf("initial status = %d, authenticated=%t", code, authed)
	}

	login := func() *http.Cookie {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"`+password+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:12345"
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("login code = %d body=%s", rec.Code, rec.Body.String())
		}
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != middleware.LauncherDashboardCookieName {
			t.Fatalf("cookies = %#v", cookies)
		}
		if !sessions.Validate(cookies[0].Value) {
			t.Fatal("issued session is not valid")
		}
		return cookies[0]
	}

	first := login()
	second := login()
	if first.Value == second.Value {
		t.Fatal("login must issue independent session values")
	}
	if code, authed := launcherAuthStatus(t, mux, first); code != http.StatusOK || !authed {
		t.Fatalf("authenticated status = %d, authenticated=%t", code, authed)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(first)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout code = %d body=%s", rec.Code, rec.Body.String())
	}
	if sessions.Validate(first.Value) {
		t.Fatal("logged-out session remains valid")
	}
	if !sessions.Validate(second.Value) {
		t.Fatal("logout must not revoke other sessions")
	}
}

func TestLauncherAuthSecureCookieUsesTrustedProxyOnly(t *testing.T) {
	store := &fakePasswordStore{initialized: true, password: "dashboard-test-password"}
	sessions := middleware.NewLauncherDashboardSessions()
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		Sessions:       sessions,
		PasswordStore:  store,
		ExternalAccess: mustExternalAccess(t, "", []string{"10.0.0.0/8"}),
	})

	for _, tc := range []struct {
		name       string
		remoteAddr string
		wantSecure bool
	}{
		{name: "trusted proxy", remoteAddr: "10.0.0.8:1234", wantSecure: true},
		{name: "untrusted peer", remoteAddr: "203.0.113.8:1234", wantSecure: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"dashboard-test-password"}`))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = tc.remoteAddr
			req.Header.Set("X-Forwarded-Proto", "https")
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("login status = %d, body=%s", rec.Code, rec.Body.String())
			}
			cookies := rec.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Secure != tc.wantSecure {
				t.Fatalf("cookies = %#v, want Secure=%t", cookies, tc.wantSecure)
			}
		})
	}
}

func TestLauncherAuthSecureCookieUsesHTTPSPublicURL(t *testing.T) {
	store := &fakePasswordStore{initialized: true, password: "dashboard-test-password"}
	sessions := middleware.NewLauncherDashboardSessions()
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		Sessions:       sessions,
		PasswordStore:  store,
		ExternalAccess: mustExternalAccess(t, "https://gogoclaw.example.test:8443", []string{"10.0.0.0/8"}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"dashboard-test-password"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.8:1234"
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("cookies = %#v, want Secure=true from public URL", cookies)
	}
}

func TestLauncherAuthUninitializedStoreRequiresSetupToken(t *testing.T) {
	store := &fakePasswordStore{}
	mux, _ := newLauncherAuthTestHandler(t, store)

	if code, authed := launcherAuthStatus(t, mux, nil); code != http.StatusOK || authed {
		t.Fatalf("status before setup = %d, authenticated=%t", code, authed)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"not-set-yet"}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("login before setup code = %d body=%s", rec.Code, rec.Body.String())
	}

	setup := func(token, password string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/auth/setup",
			strings.NewReader(fmt.Sprintf(`{"password":%q,"confirm":%q}`, password, password)),
		)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-Launcher-Setup-Token", token)
		}
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := setup("", "12345678"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("setup without token code = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := setup("wrong", "12345678"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("setup with wrong token code = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := setup(testLauncherSetupToken, "12345678"); rec.Code != http.StatusOK {
		t.Fatalf("setup code = %d body=%s", rec.Code, rec.Body.String())
	}
	if !store.initialized || store.password != "12345678" {
		t.Fatalf("setup store state: initialized=%t password=%q", store.initialized, store.password)
	}
}

func TestLauncherAuthConcurrentFirstSetupHasSingleWinner(t *testing.T) {
	store := &fakePasswordStore{}
	mux, _ := newLauncherAuthTestHandler(t, store)

	const requests = 8
	start := make(chan struct{})
	results := make(chan int, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			password := fmt.Sprintf("password-%02d", index)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(
				http.MethodPost,
				"/api/auth/setup",
				strings.NewReader(fmt.Sprintf(`{"password":%q,"confirm":%q}`, password, password)),
			)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Launcher-Setup-Token", testLauncherSetupToken)
			req.RemoteAddr = fmt.Sprintf("127.0.0.1:%d", 20000+index)
			mux.ServeHTTP(rec, req)
			results <- rec.Code
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	okCount := 0
	conflictCount := 0
	for code := range results {
		switch code {
		case http.StatusOK:
			okCount++
		case http.StatusConflict:
			conflictCount++
		default:
			t.Fatalf("unexpected setup status %d", code)
		}
	}
	if okCount != 1 || conflictCount != requests-1 {
		t.Fatalf("ok=%d conflict=%d, want 1/%d", okCount, conflictCount, requests-1)
	}
}

func TestLauncherAuthSetupWhenInitializedRequiresSessionAndCurrentPassword(t *testing.T) {
	store := &fakePasswordStore{initialized: true, password: "old-password"}
	mux, sessions := newLauncherAuthTestHandler(t, store)

	setup := func(cookie *http.Cookie, currentPassword, newPassword string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/auth/setup",
			strings.NewReader(fmt.Sprintf(
				`{"password":%q,"confirm":%q,"current_password":%q}`,
				newPassword,
				newPassword,
				currentPassword,
			)),
		)
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := setup(nil, "old-password", "new-password"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("password change without session code = %d body=%s", rec.Code, rec.Body.String())
	}

	oldSession := issueLauncherTestSession(t, sessions)
	if rec := setup(oldSession, "wrong-password", "new-password"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("password change with wrong current password code = %d body=%s", rec.Code, rec.Body.String())
	}

	rec := setup(oldSession, "old-password", "new-password")
	if rec.Code != http.StatusOK {
		t.Fatalf("password change code = %d body=%s", rec.Code, rec.Body.String())
	}
	if store.password != "new-password" {
		t.Fatalf("password = %q, want new-password", store.password)
	}
	if sessions.Validate(oldSession.Value) {
		t.Fatal("old session remains valid after password change")
	}
	newCookies := rec.Result().Cookies()
	if len(newCookies) != 1 || !sessions.Validate(newCookies[0].Value) {
		t.Fatalf("password change did not issue a fresh session: %#v", newCookies)
	}
}

func TestLauncherAuthPasswordChangeRateLimit(t *testing.T) {
	store := &fakePasswordStore{initialized: true, password: "old-password"}
	mux, sessions := newLauncherAuthTestHandler(t, store)
	cookie := issueLauncherTestSession(t, sessions)

	for i := 0; i < loginAttemptsPerIP; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/auth/setup",
			strings.NewReader(`{"password":"new-password","confirm":"new-password","current_password":"wrong"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.168.5.6:9999"
		req.AddCookie(cookie)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code = %d body=%s", i, rec.Code, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/setup",
		strings.NewReader(`{"password":"new-password","confirm":"new-password","current_password":"wrong"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.5.6:9999"
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited code = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLauncherAuthStoreUnavailableFailsClosed(t *testing.T) {
	mux, _ := newLauncherAuthTestHandler(t, nil)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "status", method: http.MethodGet, path: "/api/auth/status"},
		{name: "login", method: http.MethodPost, path: "/api/auth/login", body: `{"password":"password"}`},
		{name: "setup", method: http.MethodPost, path: "/api/auth/setup", body: `{"password":"12345678","confirm":"12345678"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tc.path == "/api/auth/setup" {
				req.Header.Set("X-Launcher-Setup-Token", testLauncherSetupToken)
			}
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLauncherAuthLogoutRequiresPostAndJSON(t *testing.T) {
	mux, _ := newLauncherAuthTestHandler(t, nil)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/logout", nil))
	if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Fatalf("GET logout: code = %d (expected 404 or 405)", rec.Code)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content-type: code = %d body=%s", rec2.Code, rec2.Body.String())
	}

	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/api/auth/logout", strings.NewReader(`{}`))
	req3.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("POST json logout: code = %d", rec3.Code)
	}
}

func TestLauncherAuthLoginRateLimit(t *testing.T) {
	store := &fakePasswordStore{initialized: true, password: "correct-password"}
	mux, _ := newLauncherAuthTestHandler(t, store)

	wrongBody := `{"password":"wrong"}`
	for i := 0; i < loginAttemptsPerIP; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(wrongBody))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.168.5.5:9999"
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("iter %d: want 401 got %d %s", i, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(wrongBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.5.5:9999"
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th attempt: want 429 got %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoginRateLimiterWindow(t *testing.T) {
	l := newLoginRateLimiter()
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return t0 }
	for i := 0; i < loginAttemptsPerIP; i++ {
		if !l.allow("ip") {
			t.Fatalf("want allow at %d", i)
		}
	}
	if l.allow("ip") {
		t.Fatal("want deny on 11th")
	}
	l.now = func() time.Time { return t0.Add(loginAttemptWindow + time.Second) }
	if !l.allow("ip") {
		t.Fatal("want allow after window")
	}
}

func TestLoginRateLimiterCleansExpiredIPs(t *testing.T) {
	l := newLoginRateLimiter()
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return t0 }
	if !l.allow("old-ip") {
		t.Fatal("want first attempt allowed")
	}

	t0 = t0.Add(2 * loginAttemptWindow)
	if !l.allow("new-ip") {
		t.Fatal("want new-ip allowed")
	}
	if _, ok := l.byIP["old-ip"]; ok {
		t.Fatal("expired IP entry was not removed")
	}
}

func TestReferrerPolicyMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := middleware.ReferrerPolicyNoReferrer(next)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q", got)
	}
}

func TestLauncherAuthLogoutEmptyBody(t *testing.T) {
	mux, _ := newLauncherAuthTestHandler(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = http.NoBody
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestLauncherAuthLogoutRejectsTrailingJSON(t *testing.T) {
	mux, _ := newLauncherAuthTestHandler(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", strings.NewReader(`{}{}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d %s", rec.Code, rec.Body.String())
	}
}

func TestLauncherAuthStatusDoesNotExposeTokenHelp(t *testing.T) {
	store := &fakePasswordStore{initialized: true, password: "password"}
	mux, sessions := newLauncherAuthTestHandler(t, store)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	req.AddCookie(issueLauncherTestSession(t, sessions))
	mux.ServeHTTP(rec, req)
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"authenticated":true`)) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "token_help") {
		t.Fatalf("authenticated response should omit token_help: %s", rec.Body.String())
	}
}
