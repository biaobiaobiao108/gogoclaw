// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"github.com/biaobiaobiao108/gogoclaw/pkg/agent/interfaces"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

// Pipeline holds the runtime dependencies used by Pipeline methods.
// It is constructed by runTurn via NewPipeline and passed to sub-methods
// so that the coordinator can delegate phase execution.
type Pipeline struct {
	Bus            interfaces.MessageBus
	Cfg            *config.Config
	ContextManager ContextManager
	Hooks          *HookManager
	Fallback       *providers.FallbackChain
	ChannelManager interfaces.ChannelManager
	MediaStore     media.MediaStore
	Steering       *steeringQueue
	al             *AgentLoop
}

// NewPipeline creates a Pipeline from an AgentLoop instance.
func NewPipeline(al *AgentLoop) *Pipeline {
	return newPipelineWithRuntime(al, al.runtimeSnapshot())
}

func newPipelineWithRuntime(al *AgentLoop, runtime runtimeSnapshot) *Pipeline {
	return &Pipeline{
		Bus:            runtime.bus,
		Cfg:            runtime.cfg,
		ContextManager: runtime.contextManager,
		Hooks:          al.hooks,
		Fallback:       runtime.fallback,
		ChannelManager: runtime.channelManager,
		MediaStore:     runtime.mediaStore,
		Steering:       runtime.steering,
		al:             al,
	}
}
