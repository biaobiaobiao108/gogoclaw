package middleware

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/web/backend/launcherconfig"
)

// ExternalOrigin is the browser-visible origin of a request after applying an
// explicit public URL or trusted reverse-proxy headers.
type ExternalOrigin struct {
	Scheme string
	Host   string
}

func (o ExternalOrigin) String() string {
	return o.Scheme + "://" + o.Host
}

func (o ExternalOrigin) WebSocketScheme() string {
	if o.Scheme == "https" {
		return "wss"
	}
	return "ws"
}

// ExternalAccess centralizes how browser-visible origins are resolved.
type ExternalAccess struct {
	publicOrigin     *url.URL
	trustedProxyNets []*net.IPNet
}

// NewExternalAccess validates launcher reverse-proxy settings once at startup.
func NewExternalAccess(publicURL string, trustedProxyCIDRs []string) (ExternalAccess, error) {
	normalizedPublicURL, err := launcherconfig.NormalizePublicURL(publicURL)
	if err != nil {
		return ExternalAccess{}, err
	}
	nets, err := parseCIDRNets(trustedProxyCIDRs)
	if err != nil {
		return ExternalAccess{}, err
	}

	var publicOrigin *url.URL
	if normalizedPublicURL != "" {
		publicOrigin, err = url.Parse(normalizedPublicURL)
		if err != nil {
			return ExternalAccess{}, fmt.Errorf("parse public_url: %w", err)
		}
	}

	return ExternalAccess{
		publicOrigin:     publicOrigin,
		trustedProxyNets: nets,
	}, nil
}

func (a ExternalAccess) requestFromTrustedProxy(r *http.Request) bool {
	if r == nil {
		return false
	}
	peerIP := clientIPFromRemoteAddr(r.RemoteAddr)
	return peerIP != nil && containsIP(a.trustedProxyNets, peerIP)
}

// ClientIP returns the original client address when the immediate peer is a
// configured reverse proxy, otherwise it returns the immediate peer address.
func (a ExternalAccess) ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	peerIP := clientIPFromRemoteAddr(r.RemoteAddr)
	if peerIP == nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	if containsIP(a.trustedProxyNets, peerIP) {
		return clientIPFromXForwardedFor(r.Header.Get("X-Forwarded-For"), a.trustedProxyNets, peerIP).String()
	}
	return peerIP.String()
}

// RequestOrigin resolves the origin represented by the current HTTP request.
func (a ExternalAccess) RequestOrigin(r *http.Request) ExternalOrigin {
	if r == nil {
		return ExternalOrigin{Scheme: "http"}
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if r.URL != nil && strings.EqualFold(r.URL.Scheme, "https") {
		scheme = "https"
	}
	host := r.Host
	if host == "" && r.URL != nil {
		host = r.URL.Host
	}

	if a.requestFromTrustedProxy(r) {
		if proto := firstForwardedValue(r.Header.Get("X-Forwarded-Proto")); validScheme(proto) {
			scheme = strings.ToLower(proto)
		} else if proto := forwardedParameter(r, "proto"); validScheme(proto) {
			scheme = strings.ToLower(proto)
		}

		if forwardedHost := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
			host = forwardedHost
		} else if forwardedHost := forwardedParameter(r, "host"); forwardedHost != "" {
			host = forwardedHost
		}
		if port := firstForwardedValue(r.Header.Get("X-Forwarded-Port")); validPort(port) {
			host = hostWithPort(host, port)
		}
	}

	return ExternalOrigin{Scheme: scheme, Host: canonicalOriginHost(scheme, host)}
}

// PreferredOrigin returns the configured public origin for trusted proxy
// requests, or when the request itself already uses that public origin.
// Direct LAN/local access keeps its own origin.
func (a ExternalAccess) PreferredOrigin(r *http.Request) ExternalOrigin {
	origin := a.RequestOrigin(r)
	if a.publicOrigin == nil {
		return origin
	}

	publicOrigin := ExternalOrigin{
		Scheme: strings.ToLower(a.publicOrigin.Scheme),
		Host:   canonicalOriginHost(a.publicOrigin.Scheme, a.publicOrigin.Host),
	}
	if a.requestFromTrustedProxy(r) ||
		(strings.EqualFold(origin.Scheme, publicOrigin.Scheme) && strings.EqualFold(origin.Host, publicOrigin.Host)) {
		return publicOrigin
	}
	return origin
}

func (a ExternalAccess) Scheme(r *http.Request) string {
	return a.PreferredOrigin(r).Scheme
}

// OriginAllowed checks the browser-visible origin selected for this request.
func (a ExternalAccess) OriginAllowed(r *http.Request, raw string) bool {
	scheme, host, ok := parseBrowserOrigin(raw)
	if !ok {
		return false
	}
	want := a.PreferredOrigin(r)
	return scheme == want.Scheme && host == want.Host
}

func (a ExternalAccess) WebSocketOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	return origin == "" || a.OriginAllowed(r, origin)
}

func parseBrowserOrigin(raw string) (scheme, host string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return "", "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || !validScheme(u.Scheme) {
		return "", "", false
	}
	scheme = strings.ToLower(u.Scheme)
	host = canonicalOriginHost(scheme, u.Host)
	if host == "" {
		return "", "", false
	}
	return scheme, host, true
}

func canonicalOriginHost(scheme, host string) string {
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, " \t\r\n") {
		return ""
	}

	hostName := host
	port := ""
	if parsedHost, parsedPort, err := net.SplitHostPort(host); err == nil {
		hostName, port = parsedHost, parsedPort
	} else {
		hostName = strings.Trim(host, "[]")
	}
	if port != "" {
		if !validPort(port) {
			return ""
		}
		if isDefaultOriginPort(scheme, port) {
			port = ""
		}
	}
	if ip := net.ParseIP(hostName); ip != nil && ip.To4() == nil {
		hostName = "[" + strings.ToLower(hostName) + "]"
	} else {
		hostName = strings.ToLower(hostName)
	}
	if port != "" {
		return net.JoinHostPort(strings.Trim(hostName, "[]"), port)
	}
	return hostName
}

func hostWithPort(host, port string) string {
	if host == "" || port == "" {
		return host
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}

func validScheme(raw string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), "http") || strings.EqualFold(strings.TrimSpace(raw), "https")
}

func validPort(raw string) bool {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	return err == nil && n >= 1 && n <= 65535
}

func firstForwardedValue(raw string) string {
	if i := strings.IndexByte(raw, ','); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(strings.Trim(raw, `"`))
}

func forwardedParameter(r *http.Request, key string) string {
	if r == nil {
		return ""
	}
	forwarded := strings.TrimSpace(r.Header.Get("Forwarded"))
	if forwarded == "" {
		return ""
	}

	first := strings.TrimSpace(strings.Split(forwarded, ",")[0])
	for _, part := range strings.Split(first, ";") {
		part = strings.TrimSpace(part)
		name, value, ok := strings.Cut(part, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), key) {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		return value
	}
	return ""
}

func isDefaultOriginPort(scheme, port string) bool {
	return (strings.EqualFold(scheme, "http") && port == "80") ||
		(strings.EqualFold(scheme, "https") && port == "443")
}

// RequestScheme is a compatibility helper for direct and trusted-proxy scheme
// detection where the host does not need to be resolved.
func RequestScheme(r *http.Request, trustedProxyCIDRs []string) string {
	access, err := NewExternalAccess("", trustedProxyCIDRs)
	if err != nil {
		if r != nil && r.TLS != nil {
			return "https"
		}
		return "http"
	}
	return access.Scheme(r)
}
