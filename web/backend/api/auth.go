package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/web/backend/middleware"
)

const launcherPasswordMaxBytes = 72

// PasswordStore is the interface for dashboard password persistence.
// Implemented by dashboardauth.Store.
type PasswordStore interface {
	IsInitialized(ctx context.Context) (bool, error)
	SetPassword(ctx context.Context, plain string) error
	SetPasswordIfUninitialized(ctx context.Context, plain string) (bool, error)
	VerifyPassword(ctx context.Context, plain string) (bool, error)
}

// LauncherAuthRouteOpts configures dashboard auth handlers.
type LauncherAuthRouteOpts struct {
	Sessions          *middleware.LauncherDashboardSessions
	SetupToken        string
	SecureCookie      func(*http.Request) bool
	TrustedProxyCIDRs []string
	// PasswordStore enables password login. It must be non-nil for auth to work.
	PasswordStore PasswordStore
}

type launcherAuthLoginBody struct {
	Password string `json:"password"`
}

type launcherAuthSetupBody struct {
	Password        string `json:"password"`
	Confirm         string `json:"confirm"`
	CurrentPassword string `json:"current_password"`
}

type launcherAuthStatusResponse struct {
	Authenticated bool `json:"authenticated"`
	Initialized   bool `json:"initialized"`
}

// RegisterLauncherAuthRoutes registers /api/auth/login|logout|status|setup.
func RegisterLauncherAuthRoutes(mux *http.ServeMux, opts LauncherAuthRouteOpts) {
	secure := opts.SecureCookie
	if secure == nil {
		secure = middleware.DefaultLauncherDashboardSecureCookie
	}
	h := &launcherAuthHandlers{
		sessions:          opts.Sessions,
		setupToken:        opts.SetupToken,
		secureCookie:      secure,
		store:             opts.PasswordStore,
		loginLimit:        newLoginRateLimiter(),
		setupLimit:        newLoginRateLimiter(),
		trustedProxyCIDRs: append([]string(nil), opts.TrustedProxyCIDRs...),
	}
	mux.HandleFunc("POST /api/auth/login", h.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", h.handleLogout)
	mux.HandleFunc("GET /api/auth/status", h.handleStatus)
	mux.HandleFunc("POST /api/auth/setup", h.handleSetup)
}

type launcherAuthHandlers struct {
	sessions          *middleware.LauncherDashboardSessions
	setupToken        string
	secureCookie      func(*http.Request) bool
	store             PasswordStore
	loginLimit        *loginRateLimiter
	setupLimit        *loginRateLimiter
	trustedProxyCIDRs []string
}

// isStoreInitialized safely queries the store.
// Returns (false, err) on store errors — callers must treat this as a 5xx, not as
// "uninitialized", to keep auth fail-closed.
func (h *launcherAuthHandlers) isStoreInitialized(ctx context.Context) (bool, error) {
	if h.store == nil {
		return false, fmt.Errorf("password store not configured")
	}
	return h.store.IsInitialized(ctx)
}

func launcherSetupCrossSite(r *http.Request) bool {
	fetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
	if fetchSite == "cross-site" {
		return true
	}

	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return !sameLauncherRequestOrigin(r, origin)
	}

	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return !sameLauncherRequestOrigin(r, referer)
	}

	return false
}

func sameLauncherRequestOrigin(r *http.Request, raw string) bool {
	if strings.ContainsAny(raw, " \t\r\n") {
		return false
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}

	wantScheme := launcherRequestScheme(r)
	wantHost := r.Host
	if wantHost == "" {
		wantHost = r.URL.Host
	}
	return strings.EqualFold(u.Scheme, wantScheme) && strings.EqualFold(u.Host, wantHost)
}

func launcherRequestScheme(r *http.Request) string {
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		if i := strings.IndexByte(proto, ','); i >= 0 {
			proto = proto[:i]
		}
		proto = strings.ToLower(strings.TrimSpace(proto))
		if proto == "http" || proto == "https" {
			return proto
		}
	}
	if r.TLS != nil {
		return "https"
	}
	if r.URL != nil && r.URL.Scheme != "" {
		return r.URL.Scheme
	}
	return "http"
}

func (h *launcherAuthHandlers) handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body launcherAuthLoginBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid JSON"}`))
		return
	}
	ip := clientIPForLimiter(r, h.trustedProxyCIDRs)
	if !h.loginLimit.allow(ip) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"too many login attempts"}`))
		return
	}
	in := strings.TrimSpace(body.Password)

	initialized, initErr := h.isStoreInitialized(r.Context())
	if initErr != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeErrorf(w, "%v", initErr)
		return
	}
	if !initialized {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"password has not been set"}`))
		return
	}

	sessionGeneration := h.sessions.Generation()
	ok, err := h.store.VerifyPassword(r.Context(), in)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		writeErrorf(w, "password verification failed: %v", err)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid password"}`))
		return
	}

	sessionValue, err := h.sessions.CreateIfGeneration(sessionGeneration)
	if err != nil {
		if errors.Is(err, middleware.ErrLauncherDashboardSessionsChanged) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"credentials changed; login again"}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"session unavailable"}`))
		return
	}
	middleware.SetLauncherDashboardSessionCookie(w, r, sessionValue, h.secureCookie)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (h *launcherAuthHandlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":"method not allowed"}`))
		return
	}
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if !strings.HasPrefix(ct, "application/json") {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		_, _ = w.Write([]byte(`{"error":"Content-Type must be application/json"}`))
		return
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, logoutBodyMaxBytes))
	if err := dec.Decode(&struct{}{}); err != nil && err != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid JSON body"}`))
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid JSON body"}`))
		return
	}

	if c, err := r.Cookie(middleware.LauncherDashboardCookieName); err == nil {
		h.sessions.Revoke(c.Value)
	}
	middleware.ClearLauncherDashboardSessionCookie(w, r, h.secureCookie)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (h *launcherAuthHandlers) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	authed := false
	if c, err := r.Cookie(middleware.LauncherDashboardCookieName); err == nil {
		authed = h.sessions.Validate(c.Value)
	}
	initialized, initErr := h.isStoreInitialized(r.Context())
	if initErr != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeErrorf(w, "%v", initErr)
		return
	}
	resp := launcherAuthStatusResponse{
		Authenticated: authed,
		Initialized:   initialized,
	}
	enc, err := json.Marshal(resp)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		writeErrorf(w, "marshal response failed: %v", err)
		return
	}
	_, _ = w.Write(enc)
}

// handleSetup sets or changes the dashboard password.
//
// Rules:
//   - First-time setup requires the launcher-provided bootstrap token and is atomic.
//   - Password changes require a valid session and the current password.
func (h *launcherAuthHandlers) handleSetup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if launcherSetupCrossSite(r) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"cross-site setup request rejected"}`))
		return
	}

	if h.store == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"password store not configured"}`))
		return
	}
	if !h.setupLimit.allow(clientIPForLimiter(r, h.trustedProxyCIDRs)) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"too many setup attempts"}`))
		return
	}

	var body launcherAuthSetupBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid JSON"}`))
		return
	}

	pw := strings.TrimSpace(body.Password)
	if pw == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"password must not be empty"}`))
		return
	}
	if pw != strings.TrimSpace(body.Confirm) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"passwords do not match"}`))
		return
	}
	if len([]rune(pw)) < 8 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"password must be at least 8 characters"}`))
		return
	}
	if len([]byte(pw)) > launcherPasswordMaxBytes {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"password must not exceed 72 bytes"}`))
		return
	}

	validSession := false
	if c, err := r.Cookie(middleware.LauncherDashboardCookieName); err == nil {
		validSession = h.sessions.Validate(c.Value)
	}

	if validSession {
		initialized, initErr := h.isStoreInitialized(r.Context())
		if initErr != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			writeErrorf(w, "%v", initErr)
			return
		}
		if !initialized {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"dashboard password is not initialized"}`))
			return
		}
		currentOK, err := h.store.VerifyPassword(r.Context(), body.CurrentPassword)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writeErrorf(w, "password verification failed: %v", err)
			return
		}
		if !currentOK {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"current password is incorrect"}`))
			return
		}
		if err := h.store.SetPassword(r.Context(), pw); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writeErrorf(w, "failed to save password: %v", err)
			return
		}
		h.sessions.RevokeAll()
		sessionValue, err := h.sessions.Create()
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"session unavailable"}`))
			return
		}
		middleware.SetLauncherDashboardSessionCookie(w, r, sessionValue, h.secureCookie)
	} else {
		providedToken := strings.TrimSpace(r.Header.Get("X-Launcher-Setup-Token"))
		if !middleware.LauncherDashboardSetupTokenMatches(h.setupToken, providedToken) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid setup token"}`))
			return
		}
		created, err := h.store.SetPasswordIfUninitialized(r.Context(), pw)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writeErrorf(w, "failed to save password: %v", err)
			return
		}
		if !created {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"dashboard password was already initialized"}`))
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// writeErrorf writes a JSON error response with a formatted message.
// json.Marshal is used to safely escape the message string.
func writeErrorf(w http.ResponseWriter, format string, args ...any) {
	msg, _ := json.Marshal(fmt.Sprintf(format, args...))
	_, _ = w.Write([]byte(`{"error":` + string(msg) + `}`))
}
