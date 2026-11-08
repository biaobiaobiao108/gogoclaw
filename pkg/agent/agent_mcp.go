// GoGoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/mcp"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

type mcpRuntime struct {
	initOnce       sync.Once
	mu             sync.Mutex
	manager        *mcp.Manager
	initErr        error
	registrationMu sync.Mutex
	registered     map[string]map[string]map[string]struct{}
}

func (r *mcpRuntime) reset() *mcp.Manager {
	r.mu.Lock()
	manager := r.manager
	r.manager = nil
	r.initErr = nil
	r.initOnce = sync.Once{}
	r.mu.Unlock()
	r.registrationMu.Lock()
	r.registered = nil
	r.registrationMu.Unlock()
	return manager
}

func (r *mcpRuntime) setManager(manager *mcp.Manager) {
	r.mu.Lock()
	r.manager = manager
	r.initErr = nil
	r.mu.Unlock()
}

func (r *mcpRuntime) setInitErr(err error) {
	r.mu.Lock()
	r.initErr = err
	r.mu.Unlock()
}

func (r *mcpRuntime) getInitErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.initErr
}

func (r *mcpRuntime) takeManager() *mcp.Manager {
	r.mu.Lock()
	defer r.mu.Unlock()
	manager := r.manager
	r.manager = nil
	return manager
}

func (r *mcpRuntime) hasManager() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manager != nil
}

func (r *mcpRuntime) getManager() *mcp.Manager {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manager
}

// ensureMCPInitialized loads MCP servers/tools once so both Run() and direct
// agent mode share the same initialization path.
func (al *AgentLoop) ensureMCPInitialized(ctx context.Context) error {
	if !al.cfg.Tools.IsToolEnabled("mcp") {
		return nil
	}

	if al.cfg.Tools.MCP.Servers == nil || len(al.cfg.Tools.MCP.Servers) == 0 {
		logger.WarnCF("agent", "MCP is enabled but no servers are configured, skipping MCP initialization", nil)
		return nil
	}

	mcpCfg := filterMCPConfigServers(al.cfg.Tools.MCP, al.registry.allowedMCPServers())
	if mcpCfg.Servers == nil || len(mcpCfg.Servers) == 0 {
		logger.InfoCF(
			"agent",
			"No MCP servers selected after applying per-agent mcpServers allowlists",
			nil,
		)
		return nil
	}

	findValidServer := false
	for _, serverCfg := range mcpCfg.Servers {
		if serverCfg.Enabled {
			findValidServer = true
		}
	}
	if !findValidServer {
		logger.WarnCF("agent", "MCP is enabled but no valid servers are configured, skipping MCP initialization", nil)
		return nil
	}

	al.mcp.initOnce.Do(func() {
		mcpManager := mcp.NewManager(
			mcp.WithRuntimeEvents(al.runtimeEvents),
			mcp.WithToolListChanged(al.handleMCPToolListChanged),
		)

		defaultAgent := al.registry.GetDefaultAgent()
		workspacePath := al.cfg.WorkspacePath()
		if defaultAgent != nil && defaultAgent.Workspace != "" {
			workspacePath = defaultAgent.Workspace
		}

		if err := mcpManager.LoadFromMCPConfig(ctx, mcpCfg, workspacePath); err != nil {
			al.mcp.setInitErr(fmt.Errorf("failed to load MCP servers: %w", err))
			logger.WarnCF("agent", "Failed to load MCP servers, MCP tools will not be available",
				map[string]any{
					"error": err.Error(),
				})
			if closeErr := mcpManager.Close(); closeErr != nil {
				logger.ErrorCF("agent", "Failed to close MCP manager",
					map[string]any{
						"error": closeErr.Error(),
					})
			}
			return
		}

		// Register MCP tools for all agents
		servers := mcpManager.GetServers()
		uniqueTools := 0
		totalRegistrations := 0
		agentIDs := al.registry.ListAgentIDs()
		agentCount := len(agentIDs)

		for serverName, conn := range servers {
			uniqueTools += len(conn.Tools)
			totalRegistrations += al.syncMCPToolsForServer(ctx, mcpManager, serverName, conn.Tools)
		}
		logger.InfoCF("agent", "MCP tools registered successfully",
			map[string]any{
				"server_count":        len(servers),
				"unique_tools":        uniqueTools,
				"total_registrations": totalRegistrations,
				"agent_count":         agentCount,
			})

		for _, agentID := range agentIDs {
			agent, ok := al.registry.GetAgent(agentID)
			if ok && agentHasDiscoverableMCPServers(al.cfg, agent.MCPServerAllowlist) {
				agent.Tools.Register(tools.NewToolSearchTool(agent.Tools))
			}
		}

		al.mcp.setManager(mcpManager)
	})

	return al.mcp.getInitErr()
}

func (al *AgentLoop) handleMCPToolListChanged(ctx context.Context, serverName string, tools []*sdkmcp.Tool) {
	if al == nil {
		return
	}
	registrations := al.syncMCPToolsForServer(ctx, al.mcp.getManager(), serverName, tools)
	logger.InfoCF("agent", "Refreshed MCP tools after server list change", map[string]any{
		"server": serverName, "tools": len(tools), "registrations": registrations,
	})
}

func (al *AgentLoop) syncMCPToolsForServer(
	_ context.Context,
	mcpManager *mcp.Manager,
	serverName string,
	serverTools []*sdkmcp.Tool,
) int {
	if al == nil || al.registry == nil || strings.TrimSpace(serverName) == "" {
		return 0
	}
	if mcpManager == nil {
		return 0
	}
	registerAsHidden := true
	toolsByName := make(map[string]*sdkmcp.Tool, len(serverTools))
	for _, tool := range serverTools {
		if tool != nil {
			toolsByName[tools.NewMCPTool(mcpManager, serverName, tool).Name()] = tool
		}
	}

	al.mcp.registrationMu.Lock()
	defer al.mcp.registrationMu.Unlock()
	if al.mcp.registered == nil {
		al.mcp.registered = make(map[string]map[string]map[string]struct{})
	}

	totalRegistrations := 0
	for _, agentID := range al.registry.ListAgentIDs() {
		agent, ok := al.registry.GetAgent(agentID)
		if !ok {
			continue
		}
		previous := al.mcp.registered[agentID][serverName]
		if !agent.AllowsMCPServer(serverName) {
			for toolName := range previous {
				agent.Tools.Unregister(toolName)
			}
			if al.mcp.registered[agentID] != nil {
				delete(al.mcp.registered[agentID], serverName)
			}
			continue
		}

		for toolName := range previous {
			if _, stillPresent := toolsByName[toolName]; !stillPresent {
				agent.Tools.Unregister(toolName)
			}
		}
		registeredNames := make(map[string]struct{}, len(toolsByName))
		for toolName, tool := range toolsByName {
			mcpTool := tools.NewMCPTool(mcpManager, serverName, tool)
			mcpTool.SetWorkspace(agent.Workspace)
			mcpTool.SetMaxInlineTextRunes(al.cfg.Tools.MCP.GetMaxInlineTextChars())
			mcpTool.SetEventPublisher(al.runtimeEvents)
			if registerAsHidden {
				agent.Tools.RegisterHidden(mcpTool)
			} else {
				agent.Tools.Register(mcpTool)
			}
			if toolRegistryIncludes(agent.Tools, toolName) {
				registeredNames[toolName] = struct{}{}
			}
		}
		if al.mcp.registered[agentID] == nil {
			al.mcp.registered[agentID] = make(map[string]map[string]struct{})
		}
		al.mcp.registered[agentID][serverName] = registeredNames
		totalRegistrations += len(registeredNames)
		registerMCPServerPromptContributor(agentID, agent, serverName, len(registeredNames), registerAsHidden)
	}
	return totalRegistrations
}

func registerMCPServerPromptContributor(
	agentID string,
	agent *AgentInstance,
	serverName string,
	toolCount int,
	registerAsHidden bool,
) {
	if agent == nil || agent.ContextBuilder == nil || toolCount <= 0 {
		return
	}
	if err := agent.ContextBuilder.RegisterPromptContributor(mcpServerPromptContributor{
		serverName: serverName,
		toolCount:  toolCount,
		deferred:   registerAsHidden,
	}); err != nil {
		logger.WarnCF("agent", "Failed to register MCP prompt contributor",
			map[string]any{
				"agent_id": agentID,
				"server":   serverName,
				"error":    err.Error(),
			})
	}
}

func recordRegisteredMCPTool(
	registeredToolsByAgent map[string]map[string]struct{},
	agentID, toolName string,
) {
	if registeredToolsByAgent[agentID] == nil {
		registeredToolsByAgent[agentID] = make(map[string]struct{})
	}
	registeredToolsByAgent[agentID][toolName] = struct{}{}
}

func toolRegistryIncludes(registry *tools.ToolRegistry, name string) bool {
	if registry == nil {
		return false
	}
	return registry.HasRegistered(name)
}

func filterMCPConfigServers(
	mcpCfg config.MCPConfig,
	allowed map[string]struct{},
) config.MCPConfig {
	if allowed == nil {
		return mcpCfg
	}

	filtered := mcpCfg
	filtered.Servers = make(map[string]config.MCPServerConfig)
	normalizedAllowed := make(map[string]struct{}, len(allowed))
	for serverName := range allowed {
		name := normalizeMCPServerName(serverName)
		if name == "" {
			continue
		}
		normalizedAllowed[name] = struct{}{}
	}
	for serverName, serverCfg := range mcpCfg.Servers {
		if _, ok := normalizedAllowed[normalizeMCPServerName(serverName)]; ok {
			filtered.Servers[serverName] = serverCfg
		}
	}

	return filtered
}

func agentHasDiscoverableMCPServers(cfg *config.Config, allowed map[string]struct{}) bool {
	if cfg == nil || !cfg.Tools.MCP.Enabled {
		return false
	}

	filtered := filterMCPConfigServers(cfg.Tools.MCP, allowed)
	for _, serverCfg := range filtered.Servers {
		if serverCfg.Enabled {
			return true
		}
	}

	return false
}
