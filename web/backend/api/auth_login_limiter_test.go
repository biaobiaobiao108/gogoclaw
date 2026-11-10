package api

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPForLimiterUsesTrustedProxyForwarding(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.5:4321"
	req.Header.Set("X-Forwarded-For", "198.51.100.23, 10.0.0.9")

	if got := clientIPForLimiter(req, []string{"10.0.0.0/8"}); got != "198.51.100.23" {
		t.Fatalf("clientIPForLimiter() = %q, want trusted forwarded client", got)
	}
}

func TestClientIPForLimiterIgnoresForwardingFromUntrustedPeer(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/auth/login", nil)
	req.RemoteAddr = "203.0.113.7:4321"
	req.Header.Set("X-Forwarded-For", "198.51.100.23")

	if got := clientIPForLimiter(req, []string{"10.0.0.0/8"}); got != "203.0.113.7" {
		t.Fatalf("clientIPForLimiter() = %q, want immediate peer", got)
	}
}
