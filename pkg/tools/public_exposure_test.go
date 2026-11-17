package tools

import "testing"

func TestPublicExposureApprovalReason(t *testing.T) {
	tests := []struct {
		tool string
		args map[string]any
		want bool
	}{
		{tool: "exec", args: map[string]any{"command": "pwd"}, want: true},
		{tool: "write_file", args: map[string]any{"path": "note.md"}, want: true},
		{tool: "install_skill", want: true},
		{tool: "mcp_unknown", want: true},
		{tool: "read_file", want: false},
		{tool: "web_search", want: false},
		{tool: "message", want: true},
		{tool: "send_tts", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := PublicExposureApprovalReason(tt.tool, tt.args)
			if (got != "") != tt.want {
				t.Fatalf("PublicExposureApprovalReason(%q) = %q, want required=%v", tt.tool, got, tt.want)
			}
		})
	}
}
