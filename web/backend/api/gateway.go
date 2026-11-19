package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/health"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/netbind"
	ppid "github.com/biaobiaobiao108/gogoclaw/pkg/pid"
)

// gateway holds the state for the managed gateway process.
var gateway = struct {
	mu                  sync.Mutex
	cmd                 *exec.Cmd
	owned               bool // true if we started the process, false if we attached to an existing one
	bootDefaultModel    string
	bootConfigSignature string
	runtimeStatus       string
	startupDeadline     time.Time
	logs                *LogBuffer
	pidData             *ppid.PidFileData // pid file data read from gogoclaw.pid.json
	picoToken           string            // cached raw pico token for upstream gateway proxy injection
}{
	runtimeStatus: "stopped",
	logs:          NewLogBuffer(200),
}

// refreshPicoTokensLocked reads the pico token from config and caches it.
// Caller must hold gateway.mu (or be sole writer).
func refreshPicoTokensLocked(configPath string) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return
	}
	var picoCfg config.PicoSettings
	if bc := cfg.Channels.GetByType(config.ChannelPico); bc != nil {
		decoded, err := bc.GetDecoded()
		if err == nil && decoded != nil {
			if p, ok := decoded.(*config.PicoSettings); ok {
				picoCfg = *p
			}
		}
	}
	gateway.picoToken = picoCfg.Token.String()
}

// ensurePicoTokenCachedLocked lazily fills the in-memory pico token cache when
// the launcher has already discovered a running gateway via pidData, but has
// not yet refreshed the token into memory.
func ensurePicoTokenCachedLocked(configPath string) {
	if gateway.picoToken != "" {
		return
	}
	refreshPicoTokensLocked(configPath)
}

func (h *Handler) gatewayCommandArgs() []string {
	args := []string{"gateway", "-E"}
	if h.debug {
		args = append(args, "-d")
	}
	return args
}

const (
	protocolKey = "Sec-Websocket-Protocol"
	tokenPrefix = "token."
)

// picoGatewayProtocol returns the gateway-facing pico subprotocol that the
// launcher should inject when proxying browser traffic upstream.
func picoGatewayProtocol() string {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if gateway.picoToken == "" {
		return ""
	}
	return tokenPrefix + gateway.picoToken
}

var (
	gatewayStartupWindow          = 15 * time.Second
	gatewayRestartGracePeriod     = 5 * time.Second
	gatewayRestartForceKillWindow = 3 * time.Second
	gatewayRestartPollInterval    = 100 * time.Millisecond
	gatewayExecCommand            = exec.Command
)

var gatewayHealthGet = func(url string, timeout time.Duration) (*http.Response, error) {
	client := http.Client{Timeout: timeout}
	return client.Get(url)
}

var gatewayProcessMatcher = isLikelyGatewayProcess

// getGatewayHealth checks the gateway health endpoint and returns the status response.
// Returns (*health.StatusResponse, statusCode, error). If error is not nil, the other values are not valid.
func (h *Handler) getGatewayHealth(cfg *config.Config, timeout time.Duration) (*health.StatusResponse, int, error) {
	return getGatewayHealthByURL(h.gatewayProbeURL(cfg, "/health"), timeout)
}

func (h *Handler) gatewayProbeURL(cfg *config.Config, path string) string {
	// Prefer port/host from pidData when available.
	var port int
	var host string
	gateway.mu.Lock()
	if d := gateway.pidData; d != nil && d.Port > 0 {
		port = d.Port
		host = gatewayProbeHost(d.Host)
	}
	gateway.mu.Unlock()
	if port == 0 {
		port = 18790
		if cfg != nil && cfg.Gateway.Port != 0 {
			port = cfg.Gateway.Port
		}
	}
	if host == "" {
		host = gatewayProbeHost(h.effectiveGatewayBindHost(cfg))
	}

	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + path
}

func getGatewayHealthByURL(url string, timeout time.Duration) (*health.StatusResponse, int, error) {
	resp, err := gatewayHealthGet(url, timeout)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	var healthResponse health.StatusResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&healthResponse); decErr != nil {
		return nil, resp.StatusCode, decErr
	}

	return &healthResponse, resp.StatusCode, nil
}

// isLikelyGatewayProcess returns whether PID appears to be a gogoclaw gateway
// process plus whether inspection was conclusive on this platform/environment.
func isLikelyGatewayProcess(pid int) (bool, bool) {
	if pid <= 0 {
		return false, true
	}

	if runtime.GOOS == "windows" {
		psCmd := fmt.Sprintf(
			`$p=Get-CimInstance Win32_Process -Filter "ProcessId = %d"; if ($null -eq $p) { "" } else { $p.CommandLine }`,
			pid,
		)
		out, err := launcherExecCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd).Output()
		if err == nil {
			cmdline := strings.TrimSpace(string(out))
			if cmdline != "" {
				return looksLikeGatewayCommandLine(cmdline), true
			}
		}

		// Fallback: determine only whether the process still exists.
		out, err = launcherExecCommand("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH").Output()
		if err != nil {
			return false, false
		}
		line := strings.ToLower(strings.TrimSpace(string(out)))
		if line == "" {
			return false, true
		}
		// A CSV row means the process exists, but may have a custom executable
		// name we cannot classify here.
		if strings.HasPrefix(line, "\"") {
			if strings.Contains(line, "\"gogoclaw.exe\"") {
				return true, true
			}
			return false, true
		}
		if strings.Contains(line, "no tasks are running") {
			return false, true
		}
		return false, true
	}

	out, err := launcherExecCommand("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false, false
	}
	cmdline := strings.ToLower(strings.TrimSpace(string(out)))
	if cmdline == "" {
		return false, true
	}
	return looksLikeGatewayCommandLine(cmdline), true
}

// looksLikeGatewayCommandLine checks whether a process command line likely
// represents "gogoclaw gateway ..." regardless of executable filename.
func looksLikeGatewayCommandLine(cmdline string) bool {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(cmdline)))
	if len(fields) == 0 {
		return false
	}
	for _, f := range fields {
		token := strings.Trim(f, `"'`)
		if token == "gateway" || strings.HasSuffix(token, "/gateway") || strings.HasSuffix(token, `\gateway`) {
			return true
		}
	}
	return false
}

func (h *Handler) getGatewayHealthForPidData(
	pidData *ppid.PidFileData,
	cfg *config.Config,
	timeout time.Duration,
) (*health.StatusResponse, int, error) {
	if pidData == nil {
		return nil, 0, errors.New("nil pid data")
	}

	port := pidData.Port
	if port == 0 {
		port = 18790
		if cfg != nil && cfg.Gateway.Port != 0 {
			port = cfg.Gateway.Port
		}
	}

	host := gatewayProbeHost(strings.TrimSpace(pidData.Host))
	if host == "" {
		host = gatewayProbeHost(h.effectiveGatewayBindHost(cfg))
	}
	if host == "" {
		host = netbind.ResolveAdaptiveLoopbackHost()
	}

	url := "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/health"
	return getGatewayHealthByURL(url, timeout)
}

func (h *Handler) validateGatewayPidData(
	pidData *ppid.PidFileData,
	cfg *config.Config,
) (ok bool, decisive bool, reason string) {
	if pidData == nil || pidData.PID <= 0 {
		return false, true, "invalid pid data"
	}

	if gatewayProcess, inspected := gatewayProcessMatcher(pidData.PID); inspected {
		if !gatewayProcess {
			return false, true, "pid process command is not gogoclaw gateway"
		}
		return true, true, ""
	}

	healthResp, statusCode, err := h.getGatewayHealthForPidData(pidData, cfg, 800*time.Millisecond)
	if err != nil {
		return false, false, fmt.Sprintf("health probe failed: %v", err)
	}
	if statusCode != http.StatusOK {
		return false, false, fmt.Sprintf("health endpoint returned status %d", statusCode)
	}
	if healthResp.PID > 0 && healthResp.PID != pidData.PID {
		return false, true, fmt.Sprintf("health pid mismatch: pidFile=%d, health=%d", pidData.PID, healthResp.PID)
	}
	return true, true, ""
}

func (h *Handler) sanitizeGatewayPidData(pidData *ppid.PidFileData, cfg *config.Config) *ppid.PidFileData {
	if pidData == nil {
		return nil
	}

	ok, decisive, reason := h.validateGatewayPidData(pidData, cfg)
	if ok {
		return pidData
	}

	logger.Warnf("ignore pid file for PID %d: %s", pidData.PID, reason)
	if decisive && ppid.RemovePidFileIfPID(globalConfigDir(), pidData.PID) {
		logger.Warnf("removed stale pid file for PID %d", pidData.PID)
	}
	return nil
}

// registerGatewayRoutes binds gateway lifecycle endpoints to the ServeMux.
func (h *Handler) registerGatewayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/gateway/status", h.handleGatewayStatus)
	mux.HandleFunc("GET /api/gateway/readiness", h.handleGatewayReadiness)
	mux.HandleFunc("GET /api/gateway/logs", h.handleGatewayLogs)
	mux.HandleFunc("POST /api/gateway/logs/clear", h.handleGatewayClearLogs)
	mux.HandleFunc("POST /api/gateway/start", h.handleGatewayStart)
	mux.HandleFunc("POST /api/gateway/stop", h.handleGatewayStop)
	mux.HandleFunc("POST /api/gateway/restart", h.handleGatewayRestart)
}

// TryAutoStartGateway checks whether gateway start preconditions are met and
// starts it when possible. Intended to be called by the backend at startup.
func (h *Handler) TryAutoStartGateway() {
	// Check PID file first to detect an already-running gateway.
	pidData := h.sanitizeGatewayPidData(ppid.ReadPidFileWithCheck(globalConfigDir()), nil)
	if pidData != nil {
		gateway.mu.Lock()
		ready, reason, err := h.gatewayStartReady()
		if err != nil {
			logger.ErrorC("gateway", fmt.Sprintf("Skip auto-starting gateway: %v", err))
			gateway.mu.Unlock()
			return
		}
		logger.Infof("ready: %v, reason: %s", ready, reason)
		if !ready {
			logger.InfoC("gateway", fmt.Sprintf("Skip auto-starting gateway: %s", reason))
			gateway.mu.Unlock()
			return
		}
		pid := pidData.PID
		_, err = h.startGatewayLocked("starting", pid)
		if err != nil {
			logger.ErrorC("gateway", fmt.Sprintf("Failed to attach to running gateway (PID: %d): %v", pid, err))
		} else {
			gateway.pidData = pidData
			refreshPicoTokensLocked(h.configPath)
			logger.InfoC("gateway", fmt.Sprintf("Attached to running gateway via PID file (PID: %d)", pid))
		}
		gateway.mu.Unlock()
		return
	}

	gateway.mu.Lock()
	defer gateway.mu.Unlock()

	if gateway.cmd != nil && gateway.cmd.Process != nil {
		gateway.cmd = nil
	}

	ready, reason, err := h.gatewayStartReady()
	if err != nil {
		logger.ErrorC("gateway", fmt.Sprintf("Skip auto-starting gateway: %v", err))
		return
	}
	if !ready {
		logger.InfoC("gateway", fmt.Sprintf("Skip auto-starting gateway: %s", reason))
		return
	}

	pid, err := h.startGatewayLocked("starting", 0)
	if err != nil {
		logger.ErrorC("gateway", fmt.Sprintf("Failed to auto-start gateway: %v", err))
		return
	}
	logger.InfoC("gateway", fmt.Sprintf("Gateway auto-started (PID: %d)", pid))
}

// gatewayStartReady validates whether current config can start the gateway.
func (h *Handler) gatewayStartReady() (bool, string, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return false, "", fmt.Errorf("failed to load config: %w", err)
	}
	normalizeStoredModelNames(cfg)

	modelName := strings.TrimSpace(cfg.Agents.Defaults.GetModelName())
	if modelName == "" {
		return false, "no default model configured", nil
	}
	if aliasModels := modelConfigsByName(cfg)[modelName]; len(aliasModels) > 1 {
		if err := validateDefaultChainModelSet(
			modelName,
			aliasModels,
			true,
			defaultChainProvider(cfg),
		); err != nil {
			// Validation failures are readiness reasons, not handler errors.
			return false, err.Error(), nil
		}
	}

	modelCfg := lookupModelConfig(cfg, modelName)
	if modelCfg == nil {
		return false, fmt.Sprintf("default model %q is invalid", modelName), nil
	}
	if !defaultModelAllowedForModelConfig(modelCfg, defaultChainProvider(cfg)) {
		return false, fmt.Sprintf("default model %q is not usable for chat", modelName), nil
	}

	if !rawTemplateHasConfiguration(modelCfg) {
		return false, fmt.Sprintf("default model %q has no credentials configured", modelName), nil
	}
	if requiresRuntimeProbe(modelCfg) && !probeLocalModelAvailability(modelCfg) {
		return false, fmt.Sprintf("default model %q is not reachable", modelName), nil
	}

	return true, "", nil
}

func (h *Handler) gatewayStatusData() map[string]any {
	data := map[string]any{}
	var configDefaultModel string
	cfg, cfgErr := config.LoadConfig(h.configPath)
	if cfgErr == nil && cfg != nil {
		configDefaultModel = strings.TrimSpace(cfg.Agents.Defaults.GetModelName())
		if configDefaultModel != "" {
			data["config_default_model"] = configDefaultModel
		}
	}
	// Primary detection: read PID file and check if process is alive.
	pidData := h.sanitizeGatewayPidData(ppid.ReadPidFileWithCheck(globalConfigDir()), cfg)
	if pidData != nil {
		gateway.mu.Lock()
		gateway.pidData = pidData
		if pidData.Version != "" {
			data["gateway_version"] = pidData.Version
		}
		setGatewayRuntimeStatusLocked("running")

		// Attach if we don't already track this PID.
		if gateway.cmd == nil || gateway.cmd.Process == nil || gateway.cmd.Process.Pid != pidData.PID {
			_ = attachToGatewayProcessLocked(pidData.PID, cfg)
		}

		bootDefaultModel := gateway.bootDefaultModel
		if bootDefaultModel != "" {
			data["boot_default_model"] = bootDefaultModel
		}
		data["gateway_status"] = "running"
		data["pid"] = pidData.PID
		gateway.mu.Unlock()
	} else {
		// Intentionally skip health probe here; the startup goroutine
		// (startGatewayLocked) already handles liveness detection via
		// pidFile polling and health fallback.
		gateway.mu.Lock()
		status := gatewayStatusWithoutHealthLocked()
		data["gateway_status"] = status
		// Keep last known pidData while gateway is still in a transient
		// running state; otherwise websocket proxy may lose auth token
		// during short pid-file races.
		if status == "stopped" || status == "error" {
			gateway.pidData = nil
		}
		gateway.mu.Unlock()
	}

	gatewayStatus, _ := data["gateway_status"].(string)
	currentConfigSignature := computeConfigSignature(cfg)
	gateway.mu.Lock()
	bootConfigSignature := gateway.bootConfigSignature
	pidData = gateway.pidData
	gateway.mu.Unlock()
	restartRequired := gatewayRestartRequiredBySignature(
		bootConfigSignature,
		currentConfigSignature,
		gatewayStatus,
	)
	if gatewayStatus == "running" && pidData != nil && pidData.Token != "" {
		if readiness, _, err := getGatewayHealthByURL(h.gatewayProbeURL(cfg, "/ready"), 800*time.Millisecond); err == nil {
			switch gatewayConfigState(cfg, pidData.Token, readiness.Checks) {
			case "applied":
				restartRequired = readiness.Status != "ready"
			case "pending":
				restartRequired = true
			}
		}
	}
	data["gateway_restart_required"] = restartRequired

	ready, reason, readyErr := h.gatewayStartReady()
	if readyErr != nil {
		data["gateway_start_allowed"] = false
		data["gateway_start_reason"] = readyErr.Error()
	} else {
		data["gateway_start_allowed"] = ready
		if !ready {
			data["gateway_start_reason"] = reason
		}
	}

	return data
}

func (h *Handler) handleGatewayReadiness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	cfg, _ := config.LoadConfig(h.configPath)
	gateway.mu.Lock()
	token := ""
	if gateway.pidData != nil {
		token = gateway.pidData.Token
	}
	gateway.mu.Unlock()
	status, code, probeErr := getGatewayHealthByURL(h.gatewayProbeURL(cfg, "/ready"), 800*time.Millisecond)
	if probeErr == nil && ((code == http.StatusOK && status.Status == "ready") ||
		(code == http.StatusServiceUnavailable && status.Status == "not ready")) {
		match := gatewayConfigState(cfg, token, status.Checks)
		delete(status.Checks, "config_revision")
		delete(status.Checks, "config_boot_settings")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(struct {
			*health.StatusResponse
			ConfigState string `json:"config_state"`
		}{status, match})
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(health.StatusResponse{Status: "unavailable"})
}

func gatewayConfigState(cfg *config.Config, token string, checks map[string]health.Check) string {
	if bootSettings, ok := checks["config_boot_settings"]; ok && cfg != nil && cfg.Gateway.BootSettings() != bootSettings.Message {
		return "pending"
	}
	desired := cfg.FileRevisionProof(token)
	running := checks["config_revision"].Message
	if desired == "" || running == "" {
		return "unknown"
	}
	if desired != running {
		return "pending"
	}
	return "applied"
}

// handleGatewayLogs returns buffered gateway logs, optionally incrementally.
//
//	GET /api/gateway/logs
func (h *Handler) handleGatewayLogs(w http.ResponseWriter, r *http.Request) {
	data := gatewayLogsData(r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// gatewayLogsData reads log_offset and log_run_id query params from the request
// and returns incremental log lines.
func gatewayLogsData(r *http.Request) map[string]any {
	data := map[string]any{}
	clientOffset := 0
	clientRunID := -1

	if v := r.URL.Query().Get("log_offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			clientOffset = n
		}
	}

	if v := r.URL.Query().Get("log_run_id"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			clientRunID = n
		}
	}

	runID := gateway.logs.RunID()

	if runID == 0 {
		data["logs"] = []string{}
		data["log_total"] = 0
		data["log_run_id"] = 0
		return data
	}

	// If runID changed, reset offset to get all logs from new run
	offset := clientOffset
	if clientRunID != runID {
		offset = 0
	}

	lines, total, runID := gateway.logs.LinesSince(offset)
	if lines == nil {
		lines = []string{}
	}

	data["logs"] = lines
	data["log_total"] = total
	data["log_run_id"] = runID
	return data
}

// scanPipe reads lines from r and appends them to buf. Returns when r reaches EOF.
func scanPipe(r io.Reader, buf *LogBuffer) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		buf.Append(scanner.Text())
	}
}
