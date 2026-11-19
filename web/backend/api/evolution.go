package api

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/netbind"
	"github.com/biaobiaobiao108/gogoclaw/pkg/pid"
)

func (h *Handler) handleEvolution(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json" {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	target := h.sanitizeGatewayPidData(pid.ReadPidFileWithCheck(globalConfigDir()), nil)
	if target == nil || target.Token == "" || target.Port <= 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "gateway is unavailable"})
		return
	}
	host := gatewayProbeHost(target.Host)
	if host == "" {
		host = netbind.ResolveAdaptiveLoopbackHost()
	}
	endpoint := url.URL{
		Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(target.Port)),
		Path: "/internal/evolution", RawQuery: r.URL.RawQuery,
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, endpoint.String(), http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	request.Header.Set("Authorization", "Bearer "+target.Token)
	request.Header.Set("Content-Type", "application/json")
	client := http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "gateway evolution request failed"})
		return
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "gateway token changed; try again"})
		return
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}
