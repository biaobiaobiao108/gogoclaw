package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustExternalAccess(t *testing.T, publicURL string, trustedProxyCIDRs []string) ExternalAccess {
	t.Helper()
	access, err := NewExternalAccess(publicURL, trustedProxyCIDRs)
	if err != nil {
		t.Fatalf("NewExternalAccess() error = %v", err)
	}
	return access
}

func TestExternalAccessAllowsConfiguredPublicURLWithoutProxyHeaders(t *testing.T) {
	access := mustExternalAccess(t, "http://gogoclaw.example.test:666", []string{"10.0.0.0/8"})
	req := httptest.NewRequest("POST", "http://gogoclaw:18800/api/config/reset", nil)
	req.RemoteAddr = "10.0.0.8:1234"
	req.Host = "gogoclaw"
	req.Header.Set("Origin", "http://gogoclaw.example.test:666")

	if !access.OriginAllowed(req, req.Header.Get("Origin")) {
		t.Fatal("OriginAllowed() = false, want configured public URL to match")
	}
	if got := access.PreferredOrigin(req).String(); got != "http://gogoclaw.example.test:666" {
		t.Fatalf("PreferredOrigin() = %q, want configured public URL", got)
	}
}

func TestExternalAccessKeepsDirectOriginWhenPublicURLIsConfigured(t *testing.T) {
	access := mustExternalAccess(t, "https://gogoclaw.example.test:8443", []string{"10.0.0.0/8"})
	req := httptest.NewRequest("POST", "http://gogoclaw:18800/api/config/reset", nil)
	req.RemoteAddr = "203.0.113.8:1234"
	req.Host = "gogoclaw:18800"

	if got := access.PreferredOrigin(req).String(); got != "http://gogoclaw:18800" {
		t.Fatalf("PreferredOrigin() = %q, want direct request origin", got)
	}
	if !access.OriginAllowed(req, "http://gogoclaw:18800") {
		t.Fatal("OriginAllowed() = false, want direct request origin to match")
	}
	if access.OriginAllowed(req, "https://gogoclaw.example.test:8443") {
		t.Fatal("OriginAllowed() accepted public URL from a direct request")
	}
}

func TestExternalAccessUsesTrustedXForwardedHeaders(t *testing.T) {
	access := mustExternalAccess(t, "", []string{"10.0.0.0/8"})
	req := httptest.NewRequest("GET", "http://gogoclaw:18800/pico/ws", nil)
	req.RemoteAddr = "10.0.0.8:1234"
	req.Header.Set("X-Forwarded-Host", "chat.example.test")
	req.Header.Set("X-Forwarded-Port", "8443")
	req.Header.Set("X-Forwarded-Proto", "https")

	if got := access.RequestOrigin(req).String(); got != "https://chat.example.test:8443" {
		t.Fatalf("RequestOrigin() = %q, want trusted forwarded origin", got)
	}
	if !access.WebSocketOriginAllowed(withOrigin(req, "https://chat.example.test:8443")) {
		t.Fatal("WebSocketOriginAllowed() = false, want trusted forwarded origin")
	}
}

func TestExternalAccessUsesTrustedRFC7239ForwardedHeader(t *testing.T) {
	access := mustExternalAccess(t, "", []string{"10.0.0.0/8"})
	req := httptest.NewRequest("GET", "http://gogoclaw:18800/", nil)
	req.RemoteAddr = "10.0.0.8:1234"
	req.Header.Set("Forwarded", `for=203.0.113.8;host="chat.example.test:9443";proto=https`)

	if got := access.RequestOrigin(req).String(); got != "https://chat.example.test:9443" {
		t.Fatalf("RequestOrigin() = %q, want RFC 7239 origin", got)
	}
}

func TestExternalAccessIgnoresForwardedHeadersFromUntrustedPeer(t *testing.T) {
	access := mustExternalAccess(t, "", []string{"10.0.0.0/8"})
	req := httptest.NewRequest("GET", "http://gogoclaw:18800/", nil)
	req.RemoteAddr = "203.0.113.8:1234"
	req.Header.Set("X-Forwarded-Host", "chat.example.test")
	req.Header.Set("X-Forwarded-Port", "666")
	req.Header.Set("X-Forwarded-Proto", "https")

	if got := access.RequestOrigin(req).String(); got != "http://gogoclaw:18800" {
		t.Fatalf("RequestOrigin() = %q, want direct origin", got)
	}
	if access.OriginAllowed(req, "https://chat.example.test:666") {
		t.Fatal("OriginAllowed() trusted spoofed forwarding headers")
	}
}

func withOrigin(req *http.Request, origin string) *http.Request {
	req.Header.Set("Origin", origin)
	return req
}
