// GoGoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/mcp"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	agenttools "github.com/biaobiaobiao108/gogoclaw/pkg/tools"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func boolPtr(b bool) *bool { return &b }

func TestMCPDiscoveryCallsRealStreamableServer(t *testing.T) {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "fixture", Version: "1"}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "echo", Description: "Echo a message"},
		func(_ context.Context, _ *sdkmcp.CallToolRequest, args map[string]any) (*sdkmcp.CallToolResult, any, error) {
			message, _ := args["message"].(string)
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: message}}}, nil, nil
		})
	httpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, &sdkmcp.StreamableHTTPOptions{JSONResponse: true}))
	defer httpServer.Close()
	provider := &sequenceProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "search", Name: agenttools.BM25SearchToolName, Arguments: map[string]any{"query": "echo message"}}}},
		{ToolCalls: []providers.ToolCall{{ID: "echo", Name: "mcp_fixture_echo", Arguments: map[string]any{"message": "hello mcp"}}}},
		{Content: "done"},
	}}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	al.cfg.Tools.MCP = config.MCPConfig{
		ToolConfig: config.ToolConfig{Enabled: true},
		Discovery:  config.ToolDiscoveryConfig{Enabled: true, UseBM25: true, TTL: 1},
		Servers:    map[string]config.MCPServerConfig{"fixture": {Enabled: true, Type: "streamable-http", URL: httpServer.URL}},
	}
	if err := al.ensureMCPInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	ts := newTurnState(agent, makeTestProcessOpts("real-mcp"), turnEventScope{turnID: "real-mcp", context: newTurnContext(nil, nil, nil)})
	if _, err := al.runTurn(context.Background(), ts, NewPipeline(al)); err != nil {
		t.Fatal(err)
	}
	records := ts.toolExecutionsSnapshot()
	if len(records) != 2 || records[1].Name != "mcp_fixture_echo" || !records[1].Success {
		t.Fatalf("MCP tool did not execute: %+v", records)
	}
	found := false
	for _, msg := range agent.Sessions.GetHistory(ts.sessionKey) {
		if msg.ToolCallID == "echo" && strings.Contains(msg.Content, "hello mcp") {
			found = true
		}
	}
	if !found {
		t.Fatal("MCP response was not saved in the conversation")
	}
}

func TestMCPRuntimeResetClearsState(t *testing.T) {
	var rt mcpRuntime
	manager := mcp.NewManager()
	rt.setManager(manager)
	rt.setInitErr(errors.New("stale init error"))
	rt.initOnce.Do(func() {})

	got := rt.reset()
	if got != manager {
		t.Fatalf("reset() manager = %p, want %p", got, manager)
	}
	if rt.hasManager() {
		t.Fatal("expected manager to be cleared after reset")
	}
	if err := rt.getInitErr(); err != nil {
		t.Fatalf("getInitErr() = %v, want nil", err)
	}

	reran := false
	rt.initOnce.Do(func() { reran = true })
	if !reran {
		t.Fatal("expected initOnce to be reset")
	}
}

func TestSyncMCPToolsForServerAddsAndRemovesChangedTools(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()
	al.cfg.Tools.MCP = config.MCPConfig{
		Servers: map[string]config.MCPServerConfig{
			"fixture": {Enabled: true},
		},
	}
	manager := mcp.NewManager()

	initial := []*sdkmcp.Tool{
		{Name: "echo", Description: "Echo"},
		{Name: "remove_me", Description: "Old tool"},
	}
	if got := al.syncMCPToolsForServer(context.Background(), manager, "fixture", initial); got != 2 {
		t.Fatalf("initial registrations = %d, want 2", got)
	}
	if !agent.Tools.HasRegistered("mcp_fixture_echo") || !agent.Tools.HasRegistered("mcp_fixture_remove_me") {
		t.Fatal("initial MCP tools were not registered")
	}

	updated := []*sdkmcp.Tool{
		{Name: "echo", Description: "Echo v2"},
		{Name: "new_tool", Description: "New tool"},
	}
	if got := al.syncMCPToolsForServer(context.Background(), manager, "fixture", updated); got != 2 {
		t.Fatalf("updated registrations = %d, want 2", got)
	}
	if !agent.Tools.HasRegistered("mcp_fixture_echo") || !agent.Tools.HasRegistered("mcp_fixture_new_tool") {
		t.Fatal("updated MCP tools were not registered")
	}
	if agent.Tools.HasRegistered("mcp_fixture_remove_me") {
		t.Fatal("removed MCP tool is still registered")
	}
}

func TestSyncMCPToolsForServerHonorsAllowlistAndDeferredMode(t *testing.T) {
	al, agent, cleanup := newTurnCoordTestLoop(t, &simpleConvProvider{})
	defer cleanup()
	al.cfg.Tools.MCP = config.MCPConfig{
		Discovery: config.ToolDiscoveryConfig{Enabled: true},
		Servers: map[string]config.MCPServerConfig{
			"fixture": {Enabled: true},
		},
	}
	manager := mcp.NewManager()
	serverTools := []*sdkmcp.Tool{{Name: "echo", Description: "Echo"}}

	agent.MCPServerAllowlist = map[string]struct{}{"other": {}}
	if got := al.syncMCPToolsForServer(context.Background(), manager, "fixture", serverTools); got != 0 {
		t.Fatalf("blocked registrations = %d, want 0", got)
	}
	if agent.Tools.HasRegistered("mcp_fixture_echo") {
		t.Fatal("MCP tool was registered despite the server allowlist")
	}

	agent.MCPServerAllowlist = nil
	if got := al.syncMCPToolsForServer(context.Background(), manager, "fixture", serverTools); got != 1 {
		t.Fatalf("allowed registrations = %d, want 1", got)
	}
	if !agent.Tools.HasRegistered("mcp_fixture_echo") {
		t.Fatal("deferred MCP tool was not indexed")
	}
	if _, callable := agent.Tools.Get("mcp_fixture_echo"); callable {
		t.Fatal("deferred MCP tool should not be directly callable before discovery")
	}
}

func TestReloadProviderAndConfig_ResetsMCPRuntime(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	defer al.Close()

	manager := mcp.NewManager()
	al.mcp.setManager(manager)
	al.mcp.setInitErr(errors.New("stale init error"))
	al.mcp.initOnce.Do(func() {})

	if !al.mcp.hasManager() {
		t.Fatal("expected MCP manager to exist before reload")
	}

	if err := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, cfg); err != nil {
		t.Fatalf("ReloadProviderAndConfig() error = %v", err)
	}

	if al.mcp.hasManager() {
		t.Fatal("expected MCP manager to be cleared when reloaded config has MCP disabled")
	}
	if err := al.mcp.getInitErr(); err != nil {
		t.Fatalf("getInitErr() = %v, want nil", err)
	}

	reran := false
	al.mcp.initOnce.Do(func() { reran = true })
	if !reran {
		t.Fatal("expected MCP initOnce to be reset after reload")
	}
}

func TestServerIsDeferred(t *testing.T) {
	tests := []struct {
		name             string
		discoveryEnabled bool
		serverDeferred   *bool
		want             bool
	}{
		// --- global false always wins: per-server deferred is ignored ---
		{
			name:             "global false: per-server deferred=true is ignored",
			discoveryEnabled: false,
			serverDeferred:   boolPtr(true),
			want:             false,
		},
		{
			name:             "global false: per-server deferred=false stays false",
			discoveryEnabled: false,
			serverDeferred:   boolPtr(false),
			want:             false,
		},
		// --- global true: per-server override applies ---
		{
			name:             "global true: per-server deferred=false opts out",
			discoveryEnabled: true,
			serverDeferred:   boolPtr(false),
			want:             false,
		},
		{
			name:             "global true: per-server deferred=true stays true",
			discoveryEnabled: true,
			serverDeferred:   boolPtr(true),
			want:             true,
		},
		// --- no per-server override: fall back to global ---
		{
			name:             "no per-server field, global discovery enabled",
			discoveryEnabled: true,
			serverDeferred:   nil,
			want:             true,
		},
		{
			name:             "no per-server field, global discovery disabled",
			discoveryEnabled: false,
			serverDeferred:   nil,
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serverCfg := config.MCPServerConfig{Deferred: tt.serverDeferred}
			got := serverIsDeferred(tt.discoveryEnabled, serverCfg)
			if got != tt.want {
				t.Errorf("serverIsDeferred(discoveryEnabled=%v, deferred=%v) = %v, want %v",
					tt.discoveryEnabled, tt.serverDeferred, got, tt.want)
			}
		})
	}
}

func TestRegisterMCPServerPromptContributorUsesActualRegisteredToolCount(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	agent := &AgentInstance{ContextBuilder: cb}

	registerMCPServerPromptContributor("research", agent, "github", 0, false)
	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{CurrentMessage: "hello"})
	if prompt := messages[0].Content; strings.Contains(prompt, "MCP server `github`") {
		t.Fatalf("expected no MCP prompt when no tools were registered, got %q", prompt)
	}

	registerMCPServerPromptContributor("research", agent, "github", 2, false)
	messages = cb.BuildMessagesFromPrompt(PromptBuildRequest{CurrentMessage: "hello"})
	prompt := messages[0].Content
	if strings.Contains(prompt, "MCP server `github` is connected") {
		t.Fatalf("tool schemas should carry connected MCP tools without duplicating prompt text, got %q", prompt)
	}
}

func TestToolRegistryIncludesReportsOnlyRegisteredTools(t *testing.T) {
	registry := agenttools.NewToolRegistry()
	registry.SetAllowlist([]string{"mcp_github_search"})

	registry.RegisterHidden(&allowlistTestTool{name: "mcp_github_search"})
	registry.RegisterHidden(&allowlistTestTool{name: "mcp_github_create_issue"})

	if !toolRegistryIncludes(registry, "mcp_github_search") {
		t.Fatal("expected hidden registered MCP tool to be included")
	}
	if toolRegistryIncludes(registry, "mcp_github_create_issue") {
		t.Fatal("blocked MCP tool should not be included")
	}
}

func TestFilterMCPConfigServersCaseInsensitivePreservesOriginalKeys(t *testing.T) {
	mcpCfg := config.MCPConfig{
		Servers: map[string]config.MCPServerConfig{
			"GitHub":     {Enabled: true},
			"filesystem": {Enabled: true},
			"Slack":      {Enabled: true},
		},
	}
	allowed := map[string]struct{}{
		"github":     {},
		"FILESYSTEM": {},
	}

	filtered := filterMCPConfigServers(mcpCfg, allowed)

	if len(filtered.Servers) != 2 {
		t.Fatalf("filtered.Servers = %v, want 2 entries", filtered.Servers)
	}
	if _, ok := filtered.Servers["GitHub"]; !ok {
		t.Fatal("expected original GitHub config key to be preserved")
	}
	if _, ok := filtered.Servers["filesystem"]; !ok {
		t.Fatal("expected filesystem config key to be preserved")
	}
	if _, ok := filtered.Servers["github"]; ok {
		t.Fatal("did not expect normalized github key to replace original config key")
	}
	if _, ok := filtered.Servers["Slack"]; ok {
		t.Fatal("did not expect unallowed Slack server")
	}
}

func TestAgentHasDiscoverableMCPServers(t *testing.T) {
	deferredFalse := false
	cfg := &config.Config{
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				ToolConfig: config.ToolConfig{Enabled: true},
				Discovery: config.ToolDiscoveryConfig{
					Enabled:  true,
					UseBM25:  true,
					UseRegex: false,
				},
				Servers: map[string]config.MCPServerConfig{
					"github":     {Enabled: true},
					"filesystem": {Enabled: true, Deferred: &deferredFalse},
				},
			},
		},
	}

	tests := []struct {
		name    string
		allowed map[string]struct{}
		want    bool
	}{
		{
			name: "nil allowlist includes discoverable enabled server",
			want: true,
		},
		{
			name:    "empty allowlist denies all servers",
			allowed: map[string]struct{}{},
			want:    false,
		},
		{
			name: "selected server discoverable",
			allowed: map[string]struct{}{
				"github": {},
			},
			want: true,
		},
		{
			name: "selected server opted out of discovery",
			allowed: map[string]struct{}{
				"filesystem": {},
			},
			want: false,
		},
		{
			name: "unknown allowlist server matches nothing",
			allowed: map[string]struct{}{
				"slack": {},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentHasDiscoverableMCPServers(cfg, tt.allowed); got != tt.want {
				t.Fatalf("agentHasDiscoverableMCPServers() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnsureMCPInitialized_LoadFailureSetsInitErr(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	defer al.Close()

	cfg.Tools = config.ToolsConfig{
		MCP: config.MCPConfig{
			ToolConfig: config.ToolConfig{Enabled: true},
			Servers: map[string]config.MCPServerConfig{
				"broken": {
					Enabled: true,
					Command: "gogoclaw-command-that-does-not-exist-for-mcp-tests",
				},
			},
		},
	}

	err := al.ensureMCPInitialized(context.Background())
	if err == nil {
		t.Fatal("ensureMCPInitialized() error = nil, want load failure")
	}
	if !strings.Contains(err.Error(), "failed to load MCP servers") {
		t.Fatalf("ensureMCPInitialized() error = %q, want wrapped load failure", err.Error())
	}

	initErr := al.mcp.getInitErr()
	if initErr == nil {
		t.Fatal("getInitErr() = nil, want cached load failure")
	}
	if !strings.Contains(initErr.Error(), "failed to load MCP servers") {
		t.Fatalf("getInitErr() = %q, want wrapped load failure", initErr.Error())
	}
	if al.mcp.getManager() != nil {
		t.Fatal("expected MCP manager to remain nil after load failure")
	}

	err = al.ensureMCPInitialized(context.Background())
	if err == nil {
		t.Fatal("second ensureMCPInitialized() error = nil, want cached load failure")
	}
	if !strings.Contains(err.Error(), "failed to load MCP servers") {
		t.Fatalf("second ensureMCPInitialized() error = %q, want wrapped load failure", err.Error())
	}
}
