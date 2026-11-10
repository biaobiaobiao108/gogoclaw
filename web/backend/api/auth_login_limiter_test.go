package api

import (
	"net/http/httptest"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/web/backend/middleware"
)

func mustExternalAccess(t *testing.T, publicURL string, trustedProxyCIDRs []string) middleware.ExternalAccess {
	t.Helper()
	access, err := middleware.NewExternalAccess(publicURL, trustedProxyCIDRs)
	if err != nil {
		t.Fatalf("NewExternalAccess() error = %v", err)
	}
	return access
}

func TestClientIPForLimiterUsesTrustedProxyForwarding(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.5:4321"
	req.Header.Set("X-Forwarded-For", "198.51.100.23, 10.0.0.9")

	if got := clientIPForLimiter(req, mustExternalAccess(t, "", []string{"10.0.0.0/8"})); got != "198.51.100.23" {
		t.Fatalf("clientIPForLimiter() = %q, want trusted forwarded client", got)
	}
}

func TestClientIPForLimiterIgnoresForwardingFromUntrustedPeer(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/auth/login", nil)
	req.RemoteAddr = "203.0.113.7:4321"
	req.Header.Set("X-Forwarded-For", "198.51.100.23")

	if got := clientIPForLimiter(req, mustExternalAccess(t, "", []string{"10.0.0.0/8"})); got != "203.0.113.7" {
		t.Fatalf("clientIPForLimiter() = %q, want immediate peer", got)
	}
}
