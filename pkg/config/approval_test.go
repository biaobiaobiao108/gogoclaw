package config

import (
	"testing"
	"time"
)

func TestApprovalConfigRequires(t *testing.T) {
	if (ApprovalConfig{}).Requires("exec") {
		t.Fatal("disabled approval config must not require a decision")
	}

	defaults := ApprovalConfig{Enabled: true}
	for _, tool := range []string{"exec", "write_file", "edit_file", "append_file", "spawn", "subagent", "install_skill"} {
		if !defaults.Requires(tool) {
			t.Fatalf("default approval policy should require %q", tool)
		}
	}
	if defaults.Requires("read_file") {
		t.Fatal("default approval policy should not require read-only tools")
	}

	custom := ApprovalConfig{Enabled: true, Tools: []string{"mcp_github_*", "exec"}}
	if !custom.Requires("exec") {
		t.Fatal("custom approval policy should require exec")
	}
	if custom.Requires("mcp_github_search") {
		t.Fatal("approval tool matching should remain exact rather than glob-like")
	}
	if (ApprovalConfig{Enabled: true, Tools: []string{"*"}}).Requires("read_file") != true {
		t.Fatal("wildcard approval policy should require every tool")
	}
}

func TestApprovalConfigTimeout(t *testing.T) {
	if got := (ApprovalConfig{}).Timeout(); got != 30*time.Minute {
		t.Fatalf("default approval timeout = %v, want 30m", got)
	}
	if got := (ApprovalConfig{TimeoutSeconds: 5}).Timeout(); got != 5*time.Second {
		t.Fatalf("configured approval timeout = %v, want 5s", got)
	}
}
