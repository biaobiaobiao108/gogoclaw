package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
)

func TestConversationContentStaysOutOfStandardDebugLogs(t *testing.T) {
	previousLevel := logger.GetLevel()
	t.Cleanup(func() { logger.SetLevel(previousLevel) })
	t.Setenv("GOGOCLAW_CONTENT_DIAGNOSTICS_UNTIL", "")
	logger.SetLevel(logger.DEBUG)
	path := filepath.Join(t.TempDir(), "gateway.log")
	if err := logger.EnableFileLogging(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(logger.DisableFileLogging)

	const inbound = "private-inbound-sentinel-56819"
	const outbound = "private-response-sentinel-97302"
	result, err := Run(context.Background(), Scenario{
		Name: "log-privacy", UserMessage: inbound,
		Steps: []ModelStep{Text(outbound)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(Expect{NoError: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if !strings.Contains(log, "LLM request") || !strings.Contains(log, "turn_id") {
		t.Fatal("expected to see traceable request logs")
	}
	if strings.Contains(log, inbound) || strings.Contains(log, outbound) {
		t.Fatal("conversation content appeared in standard debug logs")
	}
}
