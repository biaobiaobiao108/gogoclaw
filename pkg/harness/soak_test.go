package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

func TestProductSoak(t *testing.T) {
	duration := soakDuration(t)
	if duration == 0 {
		t.Skip("set GOGOCLAW_SOAK_DURATION (for example 20m) to run the soak test")
	}
	workspace := t.TempDir()
	sessionDir := filepath.Join(workspace, "sessions")
	logPath := filepath.Join(workspace, "logs", "gateway.log")
	if err := logger.EnableFileLogging(logPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(logger.DisableFileLogging)

	reportPath := strings.TrimSpace(os.Getenv("GOGOCLAW_SOAK_REPORT"))
	if reportPath == "" {
		reportPath = filepath.Join(workspace, "soak.csv")
	}
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatal(err)
	}
	report, err := os.OpenFile(reportPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer report.Close()
	if _, err := fmt.Fprintln(report, "timestamp,round,heap_bytes,sys_bytes,rss_bytes,goroutines,db_bytes,wal_bytes,log_bytes"); err != nil {
		t.Fatal(err)
	}

	const sessions = 4
	started := time.Now()
	deadline := started.Add(duration)
	warmupDeadline := started.Add(min(duration/4, 30*time.Second))
	maxRSSGrowth := soakEnvInt("GOGOCLAW_SOAK_MAX_RSS_GROWTH_MB", 128) << 20
	maxGoroutineGrowth := soakEnvInt("GOGOCLAW_SOAK_MAX_GOROUTINE_GROWTH", 16)
	var baseline soakMetrics
	round := 0
	for time.Now().Before(deadline) {
		round++
		store, err := memory.NewSQLiteStore(sessionDir)
		if err != nil {
			t.Fatalf("round %d open store: %v", round, err)
		}
		backend := session.NewStoreBackend(store)
		result, runErr := Run(context.Background(), Scenario{
			Name:        "product-soak",
			Workspace:   workspace,
			Session:     backend,
			UserMessage: "soak",
			Provider:    &restartEchoProvider{},
			Timeout:     30 * time.Second,
			ConcurrentSetup: func(runtime ToolRuntime) error {
				var wg sync.WaitGroup
				errs := make(chan error, sessions)
				for i := 0; i < sessions; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						key := fmt.Sprintf("agent:main:soak-%d", i)
						message := fmt.Sprintf("round-%d-session-%d", round, i)
						ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
						defer cancel()
						answer, err := runtime.Loop.ProcessDirectWithChannel(ctx, message, key, "harness", key)
						if err == nil && answer != "echo "+message {
							err = fmt.Errorf("unexpected answer %q", answer)
						}
						errs <- err
					}(i)
				}
				wg.Wait()
				close(errs)
				for err := range errs {
					if err != nil {
						return err
					}
				}
				return nil
			},
		})
		closeErr := backend.Close()
		if runErr != nil {
			t.Fatalf("round %d run: %v", round, runErr)
		}
		if err := result.Verify(Expect{NoError: true}); err != nil {
			t.Fatalf("round %d verify: %v", round, err)
		}
		if closeErr != nil {
			t.Fatalf("round %d close store: %v", round, closeErr)
		}

		metrics := collectSoakMetrics(sessionDir, logPath)
		metrics.round = round
		if _, err := fmt.Fprintf(report, "%s,%d,%d,%d,%d,%d,%d,%d,%d\n",
			time.Now().UTC().Format(time.RFC3339), round,
			metrics.heapBytes, metrics.sysBytes, metrics.rssBytes,
			metrics.goroutines, metrics.dbBytes, metrics.walBytes, metrics.logBytes,
		); err != nil {
			t.Fatal(err)
		}
		if baseline.round == 0 && time.Now().After(warmupDeadline) {
			baseline = metrics
			t.Logf("soak warmup complete: round=%d rss=%d goroutines=%d", round, baseline.rssBytes, baseline.goroutines)
		}
		if baseline.round > 0 && round%20 == 0 {
			if maxRSSGrowth > 0 && baseline.rssBytes > 0 && metrics.rssBytes > baseline.rssBytes+int64(maxRSSGrowth) {
				t.Fatalf("RSS grew from %d to %d bytes", baseline.rssBytes, metrics.rssBytes)
			}
			if maxGoroutineGrowth > 0 && metrics.goroutines > baseline.goroutines+maxGoroutineGrowth {
				t.Fatalf("goroutines grew from %d to %d", baseline.goroutines, metrics.goroutines)
			}
		}
	}
	if round == 0 {
		t.Fatal("soak duration ended before any round completed")
	}
	t.Logf("soak complete: duration=%s rounds=%d report=%s", time.Since(started).Round(time.Second), round, reportPath)
}

type soakMetrics struct {
	round      int
	heapBytes  uint64
	sysBytes   uint64
	rssBytes   int64
	goroutines int
	dbBytes    int64
	walBytes   int64
	logBytes   int64
}

func collectSoakMetrics(sessionDir, logPath string) soakMetrics {
	runtime.GC()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return soakMetrics{
		heapBytes:  mem.HeapAlloc,
		sysBytes:   mem.Sys,
		rssBytes:   processRSSBytes(),
		goroutines: runtime.NumGoroutine(),
		dbBytes:    fileOccupancy(filepath.Join(sessionDir, "sessions.db")),
		walBytes:   fileOccupancy(filepath.Join(sessionDir, "sessions.db-wal")),
		logBytes:   logOccupancy(logPath),
	}
}

func processRSSBytes() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kib, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kib << 10
	}
	return 0
}

func fileOccupancy(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func logOccupancy(path string) int64 {
	total := fileOccupancy(path)
	backups, _ := filepath.Glob(path + ".*")
	for _, backup := range backups {
		total += fileOccupancy(backup)
	}
	return total
}

func soakDuration(t *testing.T) time.Duration {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("GOGOCLAW_SOAK_DURATION"))
	if raw == "" {
		return 0
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 || duration > 24*time.Hour {
		t.Fatal("GOGOCLAW_SOAK_DURATION must be a positive duration no longer than 24h")
	}
	return duration
}

func soakEnvInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
