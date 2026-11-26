// GoGoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent/interfaces"
	"github.com/biaobiaobiao108/gogoclaw/pkg/audio/asr"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/commands"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers/messageutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/routing"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/state"
)

type AgentLoop struct {
	// Core dependencies
	bus      interfaces.MessageBus
	cfg      *config.Config
	registry *AgentRegistry
	state    *state.Manager

	// Runtime event system
	runtimeEvents      runtimeevents.Bus
	ownsRuntimeEvents  bool
	runtimeEventLogMu  sync.RWMutex
	runtimeEventLogger *runtimeEventLogger
	runtimeEventLogSub runtimeevents.Subscription
	hooks              *HookManager
	humanApprovals     *humanApprovalManager
	publicExposure     bool

	// Runtime state
	running        atomic.Bool
	contextManager ContextManager
	fallback       *providers.FallbackChain
	channelManager interfaces.ChannelManager
	mediaStore     media.MediaStore
	transcriber    asr.Transcriber
	cmdRegistry    *commands.Registry
	mcp            mcpRuntime
	evolution      *evolutionBridge
	hookRuntime    hookRuntime
	steering       *steeringQueue
	followUpQueue  *steeringQueue
	pendingSkills  sync.Map
	mu             sync.RWMutex

	// workerSem limits concurrent turn processing workers.
	workerSem chan struct{}
	// backgroundWG tracks workers and the delivery outbox so shutdown does not
	// return while sessions or delivery checkpoints are still being written.
	backgroundMu       sync.Mutex
	backgroundWG       sync.WaitGroup
	backgroundStopping bool
	backgroundTimedOut bool

	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc
	runMu           sync.Mutex
	runCtx          context.Context
	runCancel       context.CancelFunc

	systemMu           sync.Mutex
	systemQueue        []bus.InboundMessage
	systemNotify       chan struct{}
	systemWorkerActive atomic.Bool

	// activeTurnStates tracks active turns per session to prevent duplicates.
	activeTurnStates  sync.Map
	subTurnCounter    atomic.Int64
	deliveryLocations sync.Map
	deliveryLocksMu   sync.Mutex
	deliveryLocks     map[string]*deliveryCheckpointLock
	branchLocks       sync.Map

	turnSeq atomic.Uint64

	// activeReqMu/activeReqCond/activeReqCount replace sync.WaitGroup to
	// avoid the "WaitGroup is reused before previous Wait has returned" panic
	// that occurs when Add(1) races with a goroutine-launched Wait().
	activeReqMu    sync.Mutex
	activeReqCond  *sync.Cond
	activeReqCount int
	providerRefs   map[providers.LLMProvider]int

	reloadFunc func() error

	providerFactory func(*config.ModelConfig) (providers.LLMProvider, string, error)
}

// processOptions configures how a message is processed
type processOptions struct {
	Dispatch                DispatchRequest // Normalized routed request boundary for this turn
	SessionKey              string          // Session identifier for history/context
	SessionAliases          []string        // Compatibility aliases for the session key
	Channel                 string          // Target channel for tool execution
	ChatID                  string          // Target chat ID for tool execution
	MessageID               string          // Current inbound platform message ID
	ReplyToMessageID        string          // Current inbound reply target message ID
	SenderID                string          // Current sender ID for dynamic context
	SenderDisplayName       string          // Current sender display name for dynamic context
	UserMessage             string          // User message content (may include prefix)
	ForcedSkills            []string        // Skills explicitly requested for this message
	TurnProfile             config.EffectiveTurnProfile
	SystemPromptOverride    string                  // Override the default system prompt (Used by SubTurns)
	Media                   []string                // media:// refs from inbound message
	Attachments             []bus.InboundAttachment // durable inbound attachments
	ThinkingLevel           string                  // per-turn thinking override
	InitialSteeringMessages []providers.Message     // Steering messages from refactor/agent
	DefaultResponse         string                  // Response when LLM returns empty
	EnableSummary           bool                    // Whether to trigger summarization
	SendResponse            bool                    // Whether to send response via bus
	AllowInterimPublish     bool                    // Whether tool-call interim text can be published for an inbound chat turn
	SuppressToolFeedback    bool                    // Whether to suppress inline tool feedback messages
	NoHistory               bool                    // If true, don't load session history (for heartbeat)
	ResumeAnswer            bool                    // Finish an interrupted answer from saved context
	ResumeReplyTurnID       string
	ResumeIterationOffset   int
	SkipInitialSteeringPoll bool                   // If true, skip the steering poll at loop start (used by Continue)
	InboundContext          *bus.InboundContext    // Normalized inbound facts for events/hooks
	RouteResult             *routing.ResolvedRoute // Route decision snapshot for events/hooks
	SessionScope            *session.SessionScope  // Session scope snapshot for events/hooks
	runtime                 *runtimeSnapshot       // Dependency version captured at message intake
	picoUserReceipt         *bus.InboundReceipt
}

type continuationTarget struct {
	SessionKey string
	Channel    string
	ChatID     string
}

type agentLoopResult struct {
	content           string
	responsePublished bool
}

const (
	defaultResponse            = "The model returned an empty response. This may indicate a provider error or token limit."
	toolLimitResponse          = "I've reached `max_tool_iterations` without a final response. Increase `max_tool_iterations` in config.json if this task needs more tool steps."
	handledToolResponseSummary = messageutil.HandledToolResponseSummary
	sessionKeyAgentPrefix      = "agent:"
	pendingTurnPrefix          = "pending-"
	providerReloadGracePeriod  = 30 * time.Second
	metadataKeyMessageKind     = "message_kind"
	metadataKeyToolCalls       = "tool_calls"
	metadataKeyOutboundKind    = "outbound_kind"
	messageKindThought         = "thought"
	messageKindToolFeedback    = "tool_feedback"
	messageKindToolCalls       = "tool_calls"
	outboundKindFinal          = "final"
	outboundKindInterim        = "interim"
	metadataKeyAccountID       = "account_id"
	metadataKeyGuildID         = "guild_id"
	metadataKeyTeamID          = "team_id"
	metadataKeyReplyToMessage  = "reply_to_message_id"
	metadataKeyParentPeerKind  = "parent_peer_kind"
	metadataKeyParentPeerID    = "parent_peer_id"
)

// registerSharedTools registers tools that are shared across all agents (web, message, spawn).

func (al *AgentLoop) Run(ctx context.Context) error {
	al.running.Store(true)
	runCtx, cancelRun := context.WithCancel(ctx)
	al.setRunContext(runCtx, cancelRun)
	stopLifecycle := context.AfterFunc(al.lifecycleCtx, cancelRun)
	defer func() {
		stopLifecycle()
		cancelRun()
		al.waitForBackgroundWorkers(backgroundDrainTimeout)
		al.clearRunContext()
	}()

	if err := al.ensureHooksInitialized(ctx); err != nil {
		return err
	}
	if err := al.ensureMCPInitialized(ctx); err != nil {
		return err
	}
	al.startBackground(func() { al.runDeliveryOutbox(runCtx) })
	al.startSystemMessageWorker(runCtx)

	idleTicker := time.NewTicker(100 * time.Millisecond)
	defer idleTicker.Stop()

	for {
		select {
		case <-runCtx.Done():
			return nil
		case <-idleTicker.C:
			if !al.running.Load() {
				return nil
			}
		case msg, ok := <-al.bus.InboundChan():
			if !ok {
				return nil
			}

			runtime := al.runtimeSnapshot()
			mediaStore := runtime.mediaStore
			channelManager := runtime.channelManager

			// Resolve the session key for this message
			sessionKey, agentID, ok := al.resolveSteeringTarget(msg)
			if !ok {
				// A single ordered worker keeps the receive loop responsive while
				// preserving non-routable message ordering.
				if claimInboundMessage(runCtx, msg) {
					al.enqueueSystemMessage(msg)
				} else {
					media.Unpin(mediaStore, msg.Media)
				}
				continue
			}

			// Atomically claim the session key with a unique placeholder sentinel
			// to prevent a TOCTOU race where multiple messages for the same session
			// pass the Load check before either registers.
			// The placeholder ensures GetActiveTurnBySession() never returns nil
			// during turn setup. Each placeholder has a unique turnID to prevent
			// cross-worker cleanup issues.
			placeholder := &turnState{
				turnID:         makePendingTurnID(sessionKey, al.turnSeq.Add(1)),
				lifecycle:      newTurnLifecycleAt(TurnPhaseSetup),
				channel:        msg.Channel,
				chatID:         msg.ChatID,
				sessionKey:     sessionKey,
				agentID:        agentID,
				pendingReceipt: msg.Receipt,
			}
			if _, loaded := al.activeTurnStates.LoadOrStore(sessionKey, placeholder); loaded {
				command, _ := commands.CommandName(msg.Content)
				isBranchAction := isChatBranchMutation(msg) || msg.Context.Raw["task_action"] == "resume_answer"
				durableSteering := msg.Receipt != nil && command != "stop" && !isBranchAction
				if !durableSteering && !claimInboundMessage(runCtx, msg) {
					media.Unpin(mediaStore, msg.Media)
					continue
				}
				if al.tryHandleStopCommand(ctx, msg, sessionKey) {
					finishInboundMessage(runCtx, msg)
					media.Unpin(mediaStore, msg.Media)
					continue
				}

				originalMedia := msg.Media
				msg = al.prepareInboundMessageForAgent(ctx, msg)
				remainingMedia := make(map[string]int, len(msg.Media))
				for _, ref := range msg.Media {
					remainingMedia[ref]++
				}
				for _, ref := range originalMedia {
					if remainingMedia[ref] > 0 {
						remainingMedia[ref]--
					} else {
						media.Unpin(mediaStore, []string{ref})
					}
				}
				if isBranchAction {
					finishInboundMessage(runCtx, msg)
					media.Unpin(mediaStore, msg.Media)
					al.maybePublishError(
						ctx,
						msg.Channel,
						msg.ChatID,
						sessionKey,
						fmt.Errorf("stop the active task before editing or regenerating an answer"),
					)
					continue
				}

				// Another turn is already active (or reserved) for this session — enqueue
				if err := al.enqueueSteeringMessage(sessionKey, agentID, providers.Message{
					ID:             msg.Context.MessageID,
					Role:           "user",
					Content:        msg.Content,
					Media:          append([]string(nil), msg.Media...),
					Attachments:    providerAttachmentsFromInbound(msg.Attachments),
					InboundReceipt: msg.Receipt,
				}); err != nil {
					if durableSteering {
						if _, cancelErr := msg.Receipt.Cancel(runCtx); cancelErr != nil {
							logger.Warnf("cancel rejected inbound message: %v", cancelErr)
						}
					}
					media.Unpin(mediaStore, msg.Media)
					logger.WarnCF("agent", "Failed to enqueue steering message",
						map[string]any{
							"error":       err.Error(),
							"channel":     msg.Channel,
							"chat_id":     msg.ChatID,
							"session_key": sessionKey,
						})
					al.PublishResponseIfNeeded(
						ctx,
						msg.Channel,
						msg.ChatID,
						sessionKey,
						"消息未加入队列：当前会话待处理消息已满。请等待当前任务结束后重试。",
					)
				} else {
					media.Unpin(mediaStore, msg.Media)
				}
				if !durableSteering {
					finishInboundMessage(runCtx, msg)
				}
				continue
			}

			// Session claimed — spawn a worker goroutine that acquires a semaphore
			// slot. The goroutine is spawned immediately so the main loop keeps
			// draining the inbound channel. The goroutine blocks on the semaphore.
			if !al.beginBackground() {
				al.releaseSessionTurnState(sessionKey, nil)
				media.Unpin(mediaStore, msg.Media)
				continue
			}
			go func(m bus.InboundMessage, ph *turnState, mediaStore media.MediaStore, channelManager interfaces.ChannelManager) {
				defer al.endBackground()
				defer media.Unpin(mediaStore, m.Media)
				var releaseSession bool
				// Acquire semaphore slot (blocks if at capacity)
				select {
				case al.workerSem <- struct{}{}:
					// Got slot, start worker
				case <-runCtx.Done():
					// Context canceled while waiting for a slot — clean up the
					// placeholder to prevent session-level deadlock.
					al.releaseSessionTurnState(sessionKey, nil)
					return
				}

				// Safety-net cleanup: if the placeholder was never replaced by a real
				// turnState (e.g., error before runTurn), delete it here. When runTurn
				// completes normally, clearActiveTurn deletes the real turnState and
				// this becomes a no-op (the key is already gone).
				defer func() {
					if releaseSession {
						// Conditional delete: only remove the entry if it still points
						// to our placeholder. A new message may have claimed the slot
						// between the panic and this defer.
						if actual, ok := al.activeTurnStates.Load(sessionKey); ok {
							if ts, ok := actual.(*turnState); ok && ts == ph {
								al.releaseSessionTurnState(sessionKey, ts)
							}
						}
						return
					}
					if actual, ok := al.activeTurnStates.Load(sessionKey); ok {
						if ts, ok := actual.(*turnState); ok && strings.HasPrefix(ts.turnID, pendingTurnPrefix) {
							// Placeholder still present — runTurn never replaced it.
							al.releaseSessionTurnState(sessionKey, ts)
						}
					}
				}()

				defer func() {
					if r := recover(); r != nil {
						releaseSession = true
						logger.RecoverPanicNoExit(r)
						logger.ErrorCF("agent", "Worker goroutine panicked",
							map[string]any{
								"session_key": sessionKey,
								"channel":     m.Channel,
								"chat_id":     m.ChatID,
								"panic":       fmt.Sprintf("%v", r),
							})
						al.maybePublishError(runCtx, m.Channel, m.ChatID, sessionKey,
							fmt.Errorf("turn panicked: %v", r))
					}
				}()
				defer func() { <-al.workerSem }() // Release slot

				if channelManager != nil {
					defer channelManager.InvokeTypingStop(m.Channel, m.ChatID)
				}

				deferredClaim := al.deferPicoUserClaim(m)
				if deferredClaim {
					defer m.Receipt.Release()
				}
				if !ph.pendingStop.Load() && (deferredClaim || claimInboundMessage(runCtx, m)) {
					al.runTurnWithSteering(runCtx, m)
					finishInboundMessage(runCtx, m)
					return
				}
				if ph.pendingStop.Load() {
					if m.Receipt != nil {
						if _, err := m.Receipt.Cancel(runCtx); err != nil && runCtx.Err() == nil {
							logger.Warnf("cancel stopped inbound message: %v", err)
						}
					}
					al.releaseSessionTurnState(sessionKey, ph)
					target := &continuationTarget{
						SessionKey: sessionKey,
						Channel:    m.Channel,
						ChatID:     m.ChatID,
					}
					continued, continueErr := al.drainQueuedSteeringContinuations(runCtx, target)
					if continueErr != nil {
						al.maybePublishError(runCtx, m.Channel, m.ChatID, sessionKey, continueErr)
						return
					}
					if continued != "" {
						al.PublishResponseIfNeeded(runCtx, target.Channel, target.ChatID, target.SessionKey, continued)
					}
				}
			}(msg, placeholder, mediaStore, channelManager)

		}
	}
}

func (al *AgentLoop) deferPicoUserClaim(msg bus.InboundMessage) bool {
	if msg.Receipt == nil || !msg.Receipt.PersistUser || msg.Context.Raw["task_action"] != "" || isChatBranchMutation(msg) {
		return false
	}
	command, _ := commands.CommandName(msg.Content)
	if command == "use" || command == "stop" {
		return false
	}
	if al.cmdRegistry != nil {
		if _, registered := al.cmdRegistry.Lookup(command); registered {
			return false
		}
	}
	return true
}

func claimInboundMessage(ctx context.Context, msg bus.InboundMessage) bool {
	if msg.Claim == nil {
		return true
	}
	claimed, err := msg.Claim(ctx)
	if err != nil && ctx.Err() == nil {
		logger.Warnf("claim inbound message: %v", err)
	}
	return claimed
}

func finishInboundMessage(ctx context.Context, msg bus.InboundMessage) {
	if msg.Finish != nil {
		msg.Finish(ctx)
	}
}

// processMessageSync processes a message synchronously (for non-routable/system messages).

// runTurnWithSteering runs a complete turn for a message and drains its steering queue.

// maybePublishError publishes an error response unless the error is context.Canceled.
// Returns true if processing should continue (non-cancellation error or no error),
// false if context was canceled and the caller should return.

// publishResponseOrError publishes the response, or an error message if processing failed.

func (al *AgentLoop) Stop() {
	al.running.Store(false)
	al.cancelBackground()
	al.waitForBackgroundWorkers(backgroundDrainTimeout)
}

// Close releases resources held by agent session stores. Call after Stop.
func (al *AgentLoop) Close() {
	al.Stop()
	mcpManager := al.mcp.takeManager()

	if mcpManager != nil {
		if err := mcpManager.Close(); err != nil {
			logger.ErrorCF("agent", "Failed to close MCP manager",
				map[string]any{
					"error": err.Error(),
				})
		}
	}
	evolution := al.currentEvolutionBridge()
	if evolution != nil {
		if err := evolution.Close(); err != nil {
			logger.ErrorCF("agent", "Failed to close evolution bridge",
				map[string]any{
					"error": err.Error(),
				})
		}
	}

	contextManager := al.runtimeSnapshot().contextManager
	if closer, ok := contextManager.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			logger.ErrorCF("agent", "Failed to close context manager",
				map[string]any{"error": err.Error()})
		}
	}
	al.mu.Lock()
	al.contextManager = nil
	al.mu.Unlock()

	al.GetRegistry().Close()
	if al.hooks != nil {
		al.hooks.Close()
	}
	al.closeRuntimeEventLogger()
	if al.runtimeEvents != nil && al.ownsRuntimeEvents {
		if err := al.runtimeEvents.Close(); err != nil {
			logger.ErrorCF("agent", "Failed to close runtime event bus",
				map[string]any{
					"error": err.Error(),
				})
		}
	}
}

// MountHook registers an in-process hook on the agent loop.

// UnmountHook removes a previously registered in-process hook.

type turnEventScope struct {
	agentID    string
	sessionKey string
	turnID     string
	context    *TurnContext
}

var audioAnnotationRe = regexp.MustCompile(`\[(voice|audio)(?::[^\]]*)?\]`)
