// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"github.com/biaobiaobiao108/gogoclaw/pkg/agent/interfaces"
	"github.com/biaobiaobiao108/gogoclaw/pkg/audio/asr"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

// runtimeSnapshot is the dependency set captured by one turn. Reloading must
// replace the set under AgentLoop.mu so a turn cannot mix old and new values.
type runtimeSnapshot struct {
	bus            interfaces.MessageBus
	cfg            *config.Config
	registry       *AgentRegistry
	fallback       *providers.FallbackChain
	channelManager interfaces.ChannelManager
	mediaStore     media.MediaStore
	transcriber    asr.Transcriber
	contextManager ContextManager
	steering       *steeringQueue
}

func (al *AgentLoop) runtimeSnapshot() runtimeSnapshot {
	if al == nil {
		return runtimeSnapshot{}
	}
	al.mu.RLock()
	defer al.mu.RUnlock()
	return runtimeSnapshot{
		bus:            al.bus,
		cfg:            al.cfg,
		registry:       al.registry,
		fallback:       al.fallback,
		channelManager: al.channelManager,
		mediaStore:     al.mediaStore,
		transcriber:    al.transcriber,
		contextManager: al.contextManager,
		steering:       al.steering,
	}
}

func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	registry := al.GetRegistry()
	for _, agentID := range registry.ListAgentIDs() {
		if agent, ok := registry.GetAgent(agentID); ok {
			agent.Tools.Register(tool)
		}
	}
}

func (al *AgentLoop) SetChannelManager(cm *channels.Manager) {
	al.mu.Lock()
	al.channelManager = cm
	al.mu.Unlock()
}

func (al *AgentLoop) GetRegistry() *AgentRegistry {
	return al.runtimeSnapshot().registry
}

func (al *AgentLoop) GetConfig() *config.Config {
	return al.runtimeSnapshot().cfg
}

func (al *AgentLoop) SetMediaStore(s media.MediaStore) {
	al.mu.Lock()
	al.mediaStore = s
	registry := al.registry
	al.mu.Unlock()
	al.propagateMediaStore(registry, s)
}

// SetRuntimeDependencies replaces the channel and media dependencies as one
// version so a concurrently created Pipeline cannot observe a mixed pair.
func (al *AgentLoop) SetRuntimeDependencies(cm *channels.Manager, s media.MediaStore) {
	al.mu.Lock()
	al.channelManager = cm
	al.mediaStore = s
	registry := al.registry
	al.mu.Unlock()
	al.propagateMediaStore(registry, s)
}

func (al *AgentLoop) propagateMediaStore(registry *AgentRegistry, s media.MediaStore) {
	if registry == nil {
		return
	}
	// Propagate store to all registered tools that can emit media.
	for _, agentID := range registry.ListAgentIDs() {
		if agent, ok := registry.GetAgent(agentID); ok {
			agent.Tools.SetMediaStore(s)
		}
	}
	registry.ForEachTool("send_tts", func(t tools.Tool) {
		if st, ok := t.(*tools.SendTTSTool); ok {
			st.SetMediaStore(s)
		}
	})
}

func (al *AgentLoop) SetTranscriber(t asr.Transcriber) {
	al.mu.Lock()
	al.transcriber = t
	al.mu.Unlock()
}

func (al *AgentLoop) SetReloadFunc(fn func() error) {
	al.mu.Lock()
	al.reloadFunc = fn
	al.mu.Unlock()
}

func (al *AgentLoop) currentReloadFunc() func() error {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.reloadFunc
}

func (al *AgentLoop) RecordLastChannel(channel string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChannel(channel)
}

func (al *AgentLoop) RecordLastChatID(chatID string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChatID(chatID)
}

func (al *AgentLoop) GetStartupInfo() map[string]any {
	info := make(map[string]any)

	registry := al.GetRegistry()
	agent := registry.GetDefaultAgent()
	if agent == nil {
		return info
	}

	// Tools info
	toolsList := agent.Tools.List()
	info["tools"] = map[string]any{
		"count": len(toolsList),
		"names": toolsList,
	}

	// Skills info
	info["skills"] = agent.ContextBuilder.GetSkillsInfo()

	// Agents info
	info["agents"] = map[string]any{
		"count": len(registry.ListAgentIDs()),
		"ids":   registry.ListAgentIDs(),
	}

	return info
}
