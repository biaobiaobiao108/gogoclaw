package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	ppid "github.com/biaobiaobiao108/gogoclaw/pkg/pid"
	"github.com/biaobiaobiao108/gogoclaw/web/backend/utils"
)

func isCmdProcessAliveLocked(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}

	// Wait() sets ProcessState when the process exits; use it when available.
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return false
	}

	// Attached processes have no waiter that updates ProcessState. Probe the
	// actual PID so Windows can detect termination after Process.Kill().
	if !ppid.IsProcessRunning(cmd.Process.Pid) {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}

	err := cmd.Process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	var errno syscall.Errno
	// EPERM means the process exists but cannot be signaled by this user.
	return errors.As(err, &errno) && errno == syscall.EPERM
}

func setGatewayRuntimeStatusLocked(status string) {
	gateway.runtimeStatus = status
	if status == "starting" || status == "restarting" {
		gateway.startupDeadline = time.Now().Add(gatewayStartupWindow)
		return
	}
	gateway.startupDeadline = time.Time{}
}

// attachToGatewayProcess attaches to an existing gateway process by PID
// and updates the gateway state accordingly.
// Assumes gateway.mu is held by the caller.
func attachToGatewayProcessLocked(pid int, cfg *config.Config) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process for PID %d: %w", pid, err)
	}

	gateway.cmd = &exec.Cmd{Process: process}
	gateway.owned = false // We didn't start this process
	setGatewayRuntimeStatusLocked("running")

	// Update bootDefaultModel and bootConfigSignature from config
	if cfg != nil {
		defaultModelName := strings.TrimSpace(cfg.Agents.Defaults.GetModelName())
		gateway.bootDefaultModel = defaultModelName
		gateway.bootConfigSignature = computeConfigSignature(cfg)
	}

	logger.InfoC("gateway", fmt.Sprintf("Attached to gateway process (PID: %d)", pid))
	return nil
}

func gatewayStatusWithoutHealthLocked() string {
	if gateway.runtimeStatus == "starting" || gateway.runtimeStatus == "restarting" {
		if gateway.startupDeadline.IsZero() || time.Now().Before(gateway.startupDeadline) {
			return gateway.runtimeStatus
		}
		return "error"
	}
	if gateway.runtimeStatus == "running" {
		// For attached processes there is no waiter goroutine; degrade stale
		// running state once the tracked process exits.
		if !isCmdProcessAliveLocked(gateway.cmd) {
			gateway.cmd = nil
			gateway.owned = false
			gateway.bootDefaultModel = ""
			gateway.bootConfigSignature = ""
			return "stopped"
		}
		return "running"
	}
	if gateway.runtimeStatus == "error" {
		return "error"
	}
	return "stopped"
}

func waitForGatewayProcessExit(cmd *exec.Cmd, timeout time.Duration) bool {
	if cmd == nil || cmd.Process == nil {
		return true
	}

	deadline := time.Now().Add(timeout)
	for {
		if !isCmdProcessAliveLocked(cmd) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(gatewayRestartPollInterval)
	}
}

// StopGateway stops the gateway process if it was started by this handler.
// This method is called during application shutdown to ensure the gateway subprocess
// is properly terminated. It only stops processes that were started by this handler,
// not processes that were attached to from existing instances.
func (h *Handler) StopGateway() {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()

	// Only stop if we own the process (started it ourselves)
	if !gateway.owned || gateway.cmd == nil || gateway.cmd.Process == nil {
		return
	}

	pid, err := stopGatewayLocked()
	if err != nil {
		logger.ErrorC("gateway", fmt.Sprintf("Failed to stop gateway (PID %d): %v", pid, err))
		return
	}

	logger.InfoC("gateway", fmt.Sprintf("Gateway stopped (PID: %d)", pid))
}

// stopGatewayLocked sends a stop signal to the gateway process.
// Assumes gateway.mu is held by the caller.
// Returns the PID of the stopped process and any error encountered.
func stopGatewayLocked() (int, error) {
	if gateway.cmd == nil || gateway.cmd.Process == nil {
		return 0, nil
	}

	pid := gateway.cmd.Process.Pid
	if !gateway.owned {
		if isGateway, inspected := gatewayProcessMatcher(pid); inspected && !isGateway {
			return pid, fmt.Errorf("refuse to stop non-gateway process (PID %d)", pid)
		}
	}

	// Send SIGTERM for graceful shutdown (SIGKILL on Windows)
	var sigErr error
	if runtime.GOOS == "windows" {
		sigErr = gateway.cmd.Process.Kill()
	} else {
		sigErr = gateway.cmd.Process.Signal(syscall.SIGTERM)
	}

	if sigErr != nil {
		return pid, sigErr
	}

	logger.InfoC("gateway", fmt.Sprintf("Sent stop signal to gateway (PID: %d)", pid))
	gateway.cmd = nil
	gateway.owned = false
	gateway.bootDefaultModel = ""
	gateway.pidData = nil
	setGatewayRuntimeStatusLocked("stopped")

	return pid, nil
}

func stopGatewayProcessForRestart(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil || !isCmdProcessAliveLocked(cmd) {
		return nil
	}

	var stopErr error
	if runtime.GOOS == "windows" {
		stopErr = cmd.Process.Kill()
	} else {
		stopErr = cmd.Process.Signal(syscall.SIGTERM)
	}
	if stopErr != nil && isCmdProcessAliveLocked(cmd) {
		return fmt.Errorf("failed to stop existing gateway: %w", stopErr)
	}

	if waitForGatewayProcessExit(cmd, gatewayRestartGracePeriod) {
		return nil
	}

	if runtime.GOOS != "windows" {
		killErr := cmd.Process.Signal(syscall.SIGKILL)
		if killErr != nil && isCmdProcessAliveLocked(cmd) {
			return fmt.Errorf("failed to force-stop existing gateway: %w", killErr)
		}
		if waitForGatewayProcessExit(cmd, gatewayRestartForceKillWindow) {
			return nil
		}
	}

	return fmt.Errorf("existing gateway did not exit before restart")
}

func (h *Handler) startGatewayLocked(initialStatus string, existingPid int) (int, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return 0, fmt.Errorf("failed to load config: %w", err)
	}
	defaultModelName := strings.TrimSpace(cfg.Agents.Defaults.GetModelName())

	var cmd *exec.Cmd
	var pid int

	if existingPid > 0 {
		// Attach to existing process
		pid = existingPid
		gateway.cmd = nil // Clear first to ensure clean state
		if err = attachToGatewayProcessLocked(pid, cfg); err != nil {
			logger.ErrorC("gateway", fmt.Sprintf("Failed to attach to existing gateway (PID %d): %v", pid, err))
			return 0, err
		}

		return pid, nil
	}

	// Start new process
	// Locate the gogoclaw executable
	execPath := utils.FindGogoclawBinary()
	logger.InfoC("gateway", fmt.Sprintf("Starting gateway process (%s)", execPath))

	cmd = gatewayExecCommand(execPath, h.gatewayCommandArgs()...)
	applyLauncherProcAttrs(cmd)
	cmd.Env = os.Environ()
	// Forward the launcher's config path via the environment variable that
	// GetConfigPath() already reads, so the gateway sub-process uses the same
	// config file without requiring a --config flag on the gateway subcommand.
	if h.configPath != "" {
		cmd.Env = append(cmd.Env, config.EnvConfig+"="+h.configPath)
	}
	gatewayHostOverride := h.gatewayHostOverride()
	if gatewayHostOverride != "" {
		cmd.Env = append(cmd.Env, config.EnvGatewayHost+"="+gatewayHostOverride)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return 0, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Clear old logs for this new run
	gateway.logs.Reset()

	// Ensure Pico Channel is configured before starting gateway
	changed, err := h.EnsurePicoChannel()
	if err != nil {
		logger.ErrorC("gateway", fmt.Sprintf("Warning: failed to ensure pico channel: %v", err))
		// Non-fatal: gateway can still start without pico channel
	}
	// Refresh cached pico token in case EnsurePicoChannel generated a new one.
	// Already holding gateway.mu from caller.
	if changed {
		refreshPicoTokensLocked(h.configPath)
		cfg, err = config.LoadConfig(h.configPath)
		if err != nil {
			return 0, fmt.Errorf("failed to reload config after ensuring pico channel: %w", err)
		}
		defaultModelName = strings.TrimSpace(cfg.Agents.Defaults.GetModelName())
	}

	exposure := EvaluatePublicExposure(cfg, h.publicExposureInput)
	cmd.Env = append(cmd.Env, config.EnvPublicExposure+"="+strconv.FormatBool(exposure.Public))
	if exposure.Public {
		logger.WarnCF("security", "Public exposure enabled", map[string]any{
			"launcher":                   exposure.Launcher,
			"public_url":                 exposure.PublicURL,
			"public_channels":            exposure.PublicChannels,
			"unapproved_high_risk_tools": exposure.UnapprovedHighRiskTools,
			"exec_guard_disabled":        exposure.ExecGuardDisabled,
		})
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("failed to start gateway: %w", err)
	}

	gateway.cmd = cmd
	gateway.owned = true // We started this process
	gateway.bootDefaultModel = defaultModelName
	gateway.bootConfigSignature = computeConfigSignature(cfg)
	setGatewayRuntimeStatusLocked(initialStatus)
	pid = cmd.Process.Pid
	logger.InfoC("gateway", fmt.Sprintf("Started gogoclaw gateway (PID: %d) from %s", pid, execPath))

	// Capture stdout/stderr in background
	go scanPipe(stdoutPipe, gateway.logs)
	go scanPipe(stderrPipe, gateway.logs)

	// Wait for exit in background and clean up
	go func() {
		if err := cmd.Wait(); err != nil {
			logger.ErrorC("gateway", fmt.Sprintf("Gateway process exited: %v", err))
		} else {
			logger.InfoC("gateway", "Gateway process exited normally")
		}

		gateway.mu.Lock()
		if gateway.cmd == cmd {
			gateway.cmd = nil
			gateway.bootDefaultModel = ""
			gateway.bootConfigSignature = ""
			if gateway.runtimeStatus != "restarting" {
				setGatewayRuntimeStatusLocked("stopped")
			}
		}
		gateway.mu.Unlock()
	}()

	// Start a goroutine to probe pidFile and health, update runtime state once ready.
	go func() {
		healthConfirmed := false
		for i := 0; i < 30; i++ { // try for up to 15 seconds
			time.Sleep(500 * time.Millisecond)
			gateway.mu.Lock()
			stillOurs := gateway.cmd == cmd
			gateway.mu.Unlock()
			if !stillOurs {
				return
			}

			// Poll for pidFile first — once available we have port/host/token.
			if pd := ppid.ReadPidFileWithCheck(globalConfigDir()); pd != nil && pd.PID == pid {
				gateway.mu.Lock()
				if gateway.cmd == cmd {
					gateway.pidData = pd
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
					setGatewayRuntimeStatusLocked("running")
				}
				gateway.mu.Unlock()
				logger.InfoC("gateway", fmt.Sprintf("Gateway pidFile detected (PID: %d, port: %d)", pd.PID, pd.Port))
				return
			}

			// Fallback: probe health endpoint to confirm liveness.
			cfg, err := config.LoadConfig(h.configPath)
			if err != nil {
				continue
			}
			_, statusCode, err := h.getGatewayHealth(cfg, 1*time.Second)
			if err == nil && statusCode == http.StatusOK {
				gateway.mu.Lock()
				if gateway.cmd == cmd {
					setGatewayRuntimeStatusLocked("running")
				}
				gateway.mu.Unlock()
				if !healthConfirmed {
					healthConfirmed = true
					logger.InfoC("gateway", "Gateway health endpoint reachable; waiting for pid file")
				}
				continue
			}
		}
	}()

	return pid, nil
}

// handleGatewayStart starts the gogoclaw gateway subprocess.
//
//	POST /api/gateway/start
func (h *Handler) handleGatewayStart(w http.ResponseWriter, r *http.Request) {
	// Check PID file first to detect an already-running gateway.
	pidData := h.sanitizeGatewayPidData(ppid.ReadPidFileWithCheck(globalConfigDir()), nil)
	if pidData != nil {
		pid := pidData.PID
		gateway.mu.Lock()
		ready, reason, err := h.gatewayStartReady()
		if err != nil {
			gateway.mu.Unlock()
			http.Error(
				w,
				fmt.Sprintf("Failed to validate gateway start conditions: %v", err),
				http.StatusInternalServerError,
			)
			return
		}
		if !ready {
			gateway.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{
				"status":  "precondition_failed",
				"message": reason,
			})
			return
		}
		_, err = h.startGatewayLocked("starting", pid)
		if err != nil {
			gateway.mu.Unlock()
			logger.ErrorC("gateway", fmt.Sprintf("Failed to attach to running gateway (PID: %d): %v", pid, err))
			http.Error(w, fmt.Sprintf("Failed to attach to gateway: %v", err), http.StatusInternalServerError)
			return
		}
		gateway.pidData = pidData
		gateway.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"pid":    pid,
		})
		return
	}

	gateway.mu.Lock()
	defer gateway.mu.Unlock()

	if gateway.cmd != nil && gateway.cmd.Process != nil {
		gateway.cmd = nil
		setGatewayRuntimeStatusLocked("stopped")
	}

	ready, reason, err := h.gatewayStartReady()
	if err != nil {
		http.Error(
			w,
			fmt.Sprintf("Failed to validate gateway start conditions: %v", err),
			http.StatusInternalServerError,
		)
		return
	}
	if !ready {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "precondition_failed",
			"message": reason,
		})
		return
	}

	pid, err := h.startGatewayLocked("starting", 0)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to start gateway: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"pid":    pid,
	})
}

// handleGatewayStop stops the running gateway subprocess gracefully.
// Note: Unlike StopGateway (which only stops self-started processes), this API endpoint
// stops any gateway process, including attached ones. This is intentional for user control.
//
//	POST /api/gateway/stop
func (h *Handler) handleGatewayStop(w http.ResponseWriter, r *http.Request) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()

	if gateway.cmd == nil || gateway.cmd.Process == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "not_running",
		})
		return
	}

	pid, err := stopGatewayLocked()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to stop gateway (PID %d): %v", pid, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"pid":    pid,
	})
}

// RestartGateway restarts the gateway process. This is a non-blocking operation
// that stops the current gateway (if running) and starts a new one.
// Returns the PID of the new gateway process or an error.
func (h *Handler) RestartGateway() (int, error) {
	ready, reason, err := h.gatewayStartReady()
	if err != nil {
		return 0, fmt.Errorf("failed to validate gateway start conditions: %w", err)
	}
	if !ready {
		return 0, &preconditionFailedError{reason: reason}
	}

	gateway.mu.Lock()
	previousCmd := gateway.cmd
	previousOwned := gateway.owned
	setGatewayRuntimeStatusLocked("restarting")
	gateway.mu.Unlock()

	if previousCmd != nil && previousCmd.Process != nil && !previousOwned {
		if isGateway, inspected := gatewayProcessMatcher(previousCmd.Process.Pid); inspected && !isGateway {
			logger.Warnf("refuse restarting non-gateway process (PID: %d)", previousCmd.Process.Pid)
			gateway.mu.Lock()
			if gateway.cmd == previousCmd {
				setGatewayRuntimeStatusLocked("running")
			}
			gateway.mu.Unlock()
			return 0, fmt.Errorf("refuse to restart non-gateway process (PID %d)", previousCmd.Process.Pid)
		}
	}

	if err = stopGatewayProcessForRestart(previousCmd); err != nil {
		gateway.mu.Lock()
		if gateway.cmd == previousCmd {
			if isCmdProcessAliveLocked(previousCmd) {
				setGatewayRuntimeStatusLocked("running")
			} else {
				gateway.cmd = nil
				gateway.bootDefaultModel = ""
				setGatewayRuntimeStatusLocked("error")
			}
		}
		gateway.mu.Unlock()
		return 0, fmt.Errorf("failed to stop gateway: %w", err)
	}

	gateway.mu.Lock()
	if gateway.cmd == previousCmd {
		gateway.cmd = nil
		gateway.bootDefaultModel = ""
	}
	pid, err := h.startGatewayLocked("restarting", 0)
	if err != nil {
		gateway.cmd = nil
		gateway.bootDefaultModel = ""
		setGatewayRuntimeStatusLocked("error")
	}
	gateway.mu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("failed to start gateway: %w", err)
	}

	return pid, nil
}

// preconditionFailedError is returned when gateway restart preconditions are not met
type preconditionFailedError struct {
	reason string
}

func (e *preconditionFailedError) Error() string {
	return e.reason
}

// IsBadRequest returns true if the error should result in a 400 Bad Request status
func (e *preconditionFailedError) IsBadRequest() bool {
	return true
}

// handleGatewayRestart stops the gateway (if running) and starts a new instance.
//
//	POST /api/gateway/restart
func (h *Handler) handleGatewayRestart(w http.ResponseWriter, r *http.Request) {
	pid, err := h.RestartGateway()
	if err != nil {
		// Check if it's a precondition failed error
		var precondErr *preconditionFailedError
		if errors.As(err, &precondErr) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{
				"status":  "precondition_failed",
				"message": precondErr.reason,
			})
			return
		}
		http.Error(w, fmt.Sprintf("Failed to restart gateway: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"pid":    pid,
	})
}

// handleGatewayClearLogs clears the in-memory gateway log buffer.
//
//	POST /api/gateway/logs/clear
func (h *Handler) handleGatewayClearLogs(w http.ResponseWriter, r *http.Request) {
	gateway.logs.Clear()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":     "cleared",
		"log_total":  0,
		"log_run_id": gateway.logs.RunID(),
	})
}

// handleGatewayStatus returns the gateway run status and health info.
//
//	GET /api/gateway/status
func (h *Handler) handleGatewayStatus(w http.ResponseWriter, r *http.Request) {
	data := h.gatewayStatusData()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}
