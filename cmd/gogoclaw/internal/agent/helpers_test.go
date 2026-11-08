package agent

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	"github.com/biaobiaobiao108/gogoclaw/pkg/approval"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
)

func TestCLIApprovalResponse(t *testing.T) {
	tests := []struct {
		answer     string
		approved   bool
		scope      approval.Scope
		wantReason string
	}{
		{answer: "o", approved: true, scope: approval.ScopeOnce},
		{answer: " session\n", approved: true, scope: approval.ScopeSession},
		{answer: "d", approved: false, scope: approval.ScopeOnce, wantReason: "denied from CLI"},
		{answer: "", approved: false, scope: approval.ScopeOnce, wantReason: "denied from CLI"},
	}

	for _, tt := range tests {
		t.Run(tt.answer, func(t *testing.T) {
			response := cliApprovalResponse("approval-1", tt.answer)
			assert.Equal(t, "approval-1", response.ID)
			assert.Equal(t, tt.approved, response.Approve)
			assert.Equal(t, tt.scope, response.Scope)
			assert.Equal(t, tt.wantReason, response.Reason)
		})
	}
}

func TestPrintActivityEvent(t *testing.T) {
	var output bytes.Buffer
	events := []runtimeevents.Event{
		{Payload: agent.ToolExecStartPayload{Tool: "exec"}},
		{Payload: agent.ToolExecEndPayload{Tool: "exec", Duration: 12 * time.Millisecond}},
		{Payload: agent.ContextCompressPayload{RemainingMessages: 7}},
		{Payload: agent.LLMRetryPayload{Reason: "rate limited"}},
	}
	for _, event := range events {
		printActivityEvent(&output, event)
	}

	text := output.String()
	for _, want := range []string{"→ exec", "✓ exec · 12ms", "context compacted · 7 messages kept", "retrying model · rate limited"} {
		assert.True(t, strings.Contains(text, want), "activity output missing %q: %q", want, text)
	}
}
