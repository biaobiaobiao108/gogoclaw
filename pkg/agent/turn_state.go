// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

// =============================================================================
// Control signals - returned from Pipeline methods to drive runTurn's coordinator loop
// =============================================================================

type Control int

const (
	// ControlContinue tells the coordinator to jump back to the top of the turn loop
	// (equivalent to the original "goto turnLoop").
	ControlContinue Control = iota
	// ControlBreak tells the coordinator to exit the turn loop and proceed to Finalize.
	ControlBreak
	// ControlToolLoop tells the coordinator to execute the tool loop.
	ControlToolLoop
)

// ToolControl signals returned from ExecuteTools to drive tool loop iteration.
type ToolControl int

const (
	// ToolControlContinue tells the tool loop to jump to the next iteration
	// (pendingMessages arrived, SubTurn results, etc.).
	ToolControlContinue ToolControl = iota
	// ToolControlBreak tells the tool loop to exit and return to the coordinator.
	ToolControlBreak
	// ToolControlFinalize tells the coordinator that all tool responses were
	// handled and the turn should finalize without another LLM call.
	ToolControlFinalize
)

// =============================================================================
// turnResult - returned from runTurn
// =============================================================================

type turnResult struct {
	finalContent      string
	modelName         string
	status            TurnEndStatus
	followUps         []bus.InboundMessage
	responsePublished bool
}

// =============================================================================
// ActiveTurnInfo - public info about an active turn
// =============================================================================

type ActiveTurnInfo struct {
	TurnID       string
	AgentID      string
	SessionKey   string
	Channel      string
	ChatID       string
	UserMessage  string
	Phase        TurnPhase
	Iteration    int
	StartedAt    time.Time
	Depth        int
	ParentTurnID string
	ChildTurnIDs []string
	Plan         []planStep
	Activities   []session.TurnActivity
	PhaseHistory []TurnPhaseTransition
}

// TurnCompletionInfo is the latest durable terminal state for a conversation.
type TurnCompletionInfo struct {
	TurnID    string        `json:"turn_id"`
	Status    string        `json:"status"`
	EndReason TurnEndReason `json:"end_reason"`
}

// =============================================================================
// turnExecution - mutable state that persists across turn loop iterations
// =============================================================================

type turnExecution struct {
	// Core message state (accumulates throughout the turn)
	messages         []providers.Message // built from ContextBuilder, grows per-iteration
	pendingMessages  []providers.Message // steering/SubTurn messages awaiting injection
	history          []providers.Message // from ContextManager.Assemble
	summary          string
	currentTurnStart int

	// Turn output
	finalContent string
	executionErr error

	// Iteration tracking
	iteration int

	// Per-iteration state set by Pipeline.PreLLM
	activeCandidates  []providers.FallbackCandidate
	activeModel       string
	activeModelConfig *config.ModelConfig
	activeProvider    providers.LLMProvider
	usedLight         bool

	// LLM call per-iteration state
	response            *providers.LLMResponse
	normalizedResponse  providers.NormalizedResponse
	normalizedToolCalls []providers.ToolCall
	allResponsesHandled bool
	streamingPublisher  *streamingChunkPublisher
	streamingDelivery   *session.TurnDelivery
	streamingFallback   bool
	suppressReasoning   bool
	callMessages        []providers.Message
	providerToolDefs    []providers.ToolDefinition
	llmModel            string
	llmModelName        string
	llmOpts             map[string]any
	thinkingLevel       string
	gracefulTerminal    bool
	useNativeSearch     bool
	ownedProviders      []providers.LLMProvider

	// Abort signaling for coordinator (set by Pipeline methods)
	abortedByHardAbort bool // true when hard abort triggered during LLM/tools
	abortedByHook      bool // true when HookActionAbortTurn triggered
}

func (exec *turnExecution) closeOwnedProviders() {
	if exec == nil {
		return
	}
	closeUniqueStatefulProviders(exec.ownedProviders...)
	exec.ownedProviders = nil
}

// newTurnExecution creates a turnExecution initialized from turnState and options.
func newTurnExecution(
	agent *AgentInstance,
	opts processOptions,
	history []providers.Message,
	summary string,
	messages []providers.Message,
) *turnExecution {
	return &turnExecution{
		history:          history,
		summary:          summary,
		messages:         messages,
		pendingMessages:  append([]providers.Message(nil), opts.InitialSteeringMessages...),
		currentTurnStart: len(messages),
		iteration:        0,
		thinkingLevel:    strings.TrimSpace(opts.ThinkingLevel),
	}
}

// =============================================================================
// turnState - the full state for a turn, constructed once per turn
// =============================================================================

type turnState struct {
	mu sync.RWMutex

	agent   *AgentInstance
	tools   *tools.ToolRegistry
	opts    processOptions
	profile config.EffectiveTurnProfile
	scope   turnEventScope
	budget  TurnBudget

	turnID            string
	agentID           string
	sessionKey        string
	originAgentID     string
	originSessionKey  string
	activeSkills      []string
	attemptedSkills   []string
	skillContextTrace []SkillContextSnapshot
	toolKinds         []string
	toolExecutions    []ToolExecutionRecord
	toolItems         []session.TurnToolItem
	toolBatchSeq      int
	readOnlyCalls     map[string]struct{}
	readOnlyRepeats   map[string]int
	retrievalStalls   int
	evidenceSeen      map[string]struct{}
	noProgressStreak  int
	activities        []session.TurnActivity
	activitySeq       int
	plan              []planStep
	lastFailedCall    string
	repeatedFailures  int
	turnCtx           *TurnContext

	channel       string
	chatID        string
	workspace     string
	userMessage   string
	media         []string
	steeringMedia []string

	lifecycle    *turnLifecycle
	iteration    int
	startedAt    time.Time
	finalContent string

	followUps []bus.InboundMessage

	gracefulInterrupt     bool
	gracefulInterruptHint string
	gracefulTerminalUsed  bool
	hardAbort             bool
	providerCancel        context.CancelFunc
	turnCancel            context.CancelFunc

	persistedMessages []providers.Message

	// SubTurn support (from HEAD)
	depth                int                  // SubTurn depth (0 for root turn)
	parentTurnID         string               // Parent turn ID (empty for root turn)
	childTurnIDs         []string             // Child turn IDs
	deliveries           *asyncDeliveryQueue  // Unified queue for async SubTurn/tool results
	concurrencySem       chan struct{}        // Semaphore for limiting concurrent SubTurns
	isFinished           atomic.Bool          // Whether this turn has finished
	session              session.SessionStore // Session store reference
	initialHistoryLength int                  // Snapshot of history length at turn start

	// Additional SubTurn fields
	ctx               context.Context    // Context for this turn
	cancelFunc        context.CancelFunc // Cancel function for this turn's context
	critical          bool               // Whether this SubTurn should continue after parent ends
	parentTurnState   *turnState         // Reference to parent turnState
	parentEnded       atomic.Bool        // Whether parent has ended
	closeOnce         sync.Once          // Ensures finishedChan is closed once
	finishOnce        sync.Once          // Ensures the full finish sequence runs once
	finishedChan      chan struct{}      // Closed when turn finishes
	maxContextRunes   int                // Optional soft rune limit for SubTurns
	deferredFollowUps []asyncDelivery    // Async results that arrived after the turn stopped consuming
	followUpsFlushed  bool               // True once runTurn has handed deferred results back to the bus

	// Token budget tracking
	tokenBudget      *atomic.Int64 // Shared token budget counter
	lastFinishReason string        // Last LLM finish_reason
	endReason        TurnEndReason
	usage            providers.UsageInfo
	summaryUsage     providers.UsageInfo
	requestUsage     *bus.ContextUsage
	lastUsage        *providers.UsageInfo // Last LLM usage info

	// Back-reference to the owning AgentLoop (set for SubTurns only, used for hard abort cascade)
	al *AgentLoop
}

func (ts *turnState) recordRepeatedFailure(key string, failed bool) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if !failed {
		ts.lastFailedCall = ""
		ts.repeatedFailures = 0
		return 0
	}
	if ts.lastFailedCall != key {
		ts.lastFailedCall = key
		ts.repeatedFailures = 0
	}
	ts.repeatedFailures++
	return ts.repeatedFailures
}

func (ts *turnState) readOnlyCallSeen(key string) bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	_, ok := ts.readOnlyCalls[key]
	return ok
}

func (ts *turnState) recordReadOnlyCall(key string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.readOnlyCalls == nil {
		ts.readOnlyCalls = make(map[string]struct{})
	}
	ts.readOnlyCalls[key] = struct{}{}
}

func (ts *turnState) recordReadOnlyRepeat(key string) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.readOnlyRepeats == nil {
		ts.readOnlyRepeats = make(map[string]int)
	}
	ts.readOnlyRepeats[key]++
	return ts.readOnlyRepeats[key]
}

func (ts *turnState) invalidateReadOnlyCalls() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.readOnlyCalls = nil
	ts.readOnlyRepeats = nil
}

func (ts *turnState) recordToolStateChange() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.readOnlyCalls = nil
	ts.readOnlyRepeats = nil
	ts.noProgressStreak = 0
}

func (ts *turnState) setEndReason(reason TurnEndReason) {
	if ts == nil || reason == "" {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.endReason == "" || ts.endReason == TurnEndReasonCompleted {
		ts.endReason = reason
	}
}

func (ts *turnState) endReasonSnapshot() TurnEndReason {
	if ts == nil {
		return TurnEndReasonCompleted
	}
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	if ts.endReason == "" {
		return TurnEndReasonCompleted
	}
	return ts.endReason
}

func (ts *turnState) recordRetrievalOutcome(usable bool) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if usable {
		ts.retrievalStalls = 0
		return 0
	}
	ts.retrievalStalls++
	return ts.retrievalStalls
}

func (ts *turnState) recordNoProgress() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.noProgressStreak++
	return ts.noProgressStreak
}

func (ts *turnState) recordEvidence(hash string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.evidenceSeen == nil {
		ts.evidenceSeen = make(map[string]struct{})
	}
	if _, exists := ts.evidenceSeen[hash]; exists {
		ts.noProgressStreak++
		return
	}
	ts.evidenceSeen[hash] = struct{}{}
	ts.noProgressStreak = 0
}

func (ts *turnState) toolRegistry() *tools.ToolRegistry {
	if ts.tools != nil {
		return ts.tools
	}
	return ts.agent.Tools
}

// =============================================================================
// turnState constructors and active turn management
// =============================================================================

func newTurnState(agent *AgentInstance, opts processOptions, scope turnEventScope) *turnState {
	ts := &turnState{
		agent:            agent,
		opts:             opts,
		profile:          opts.TurnProfile,
		scope:            scope,
		turnID:           scope.turnID,
		agentID:          agent.ID,
		sessionKey:       opts.Dispatch.SessionKey,
		originAgentID:    agent.ID,
		originSessionKey: opts.Dispatch.SessionKey,
		activeSkills:     activeSkillNames(agent, opts),
		turnCtx:          cloneTurnContext(scope.context),
		channel:          opts.Dispatch.Channel(),
		chatID:           opts.Dispatch.ChatID(),
		workspace:        agent.Workspace,
		userMessage:      opts.Dispatch.UserMessage,
		media:            append([]string(nil), opts.Dispatch.Media...),
		lifecycle:        newTurnLifecycle(),
		startedAt:        time.Now(),
		endReason:        TurnEndReasonCompleted,
		budget:           defaultTurnBudget(agent.MaxIterations),
		deliveries:       newAsyncDeliveryQueue(defaultAsyncDeliveryCapacity),
	}

	// Bind session store and capture initial history length for rollback logic
	if agent != nil && agent.Sessions != nil {
		ts.session = agent.Sessions
		history := agent.Sessions.GetHistory(opts.Dispatch.SessionKey)
		ts.initialHistoryLength = len(history)
	}

	return ts
}

func (al *AgentLoop) registerActiveTurn(ts *turnState) {
	al.activeTurnStates.Store(ts.sessionKey, ts)
}

func (al *AgentLoop) clearActiveTurn(ts *turnState) {
	al.releaseSessionTurnState(ts.sessionKey, ts)
}

func (al *AgentLoop) releaseSessionTurnState(sessionKey string, expected *turnState) {
	if expected == nil {
		al.activeTurnStates.Delete(sessionKey)
		return
	}
	if actual, ok := al.activeTurnStates.Load(sessionKey); ok && actual == expected {
		al.activeTurnStates.Delete(sessionKey)
	}
}

func (al *AgentLoop) getActiveTurnState(sessionKey string) *turnState {
	if val, ok := al.activeTurnStates.Load(sessionKey); ok {
		if ts, ok := val.(*turnState); ok {
			return ts
		}
		// Unexpected non-*turnState value — treat as "no active turn" to avoid
		// panics. This should not happen under normal operation.
	}
	return nil
}

// getAnyActiveTurnState returns any active turn state (for backward compatibility)
func (al *AgentLoop) getAnyActiveTurnState() *turnState {
	var firstTS *turnState
	al.activeTurnStates.Range(func(key, value any) bool {
		if ts, ok := value.(*turnState); ok {
			firstTS = ts
			return false
		}
		return true
	})
	return firstTS
}

// getSoleActiveTurnState returns the only active turn. When more than one turn
// is active the caller must use a session-scoped API instead of guessing.
func (al *AgentLoop) getSoleActiveTurnState() (*turnState, bool) {
	var firstTS *turnState
	count := 0
	al.activeTurnStates.Range(func(_, value any) bool {
		ts, ok := value.(*turnState)
		if !ok {
			return true
		}
		count++
		if count == 1 {
			firstTS = ts
		}
		return count < 2
	})
	return firstTS, count <= 1
}

func (al *AgentLoop) GetActiveTurn() *ActiveTurnInfo {
	// For backward compatibility, return the first active turn found
	// In the new architecture, there can be multiple concurrent turns
	var firstTS *turnState
	al.activeTurnStates.Range(func(key, value any) bool {
		if ts, ok := value.(*turnState); ok {
			firstTS = ts
			return false
		}
		return true
	})
	if firstTS == nil {
		return nil
	}
	info := firstTS.snapshot()
	return &info
}

func (al *AgentLoop) GetActiveTurnBySession(sessionKey string) *ActiveTurnInfo {
	ts := al.getActiveTurnState(sessionKey)
	if ts == nil {
		return nil
	}
	info := ts.snapshot()
	return &info
}

// GetActiveTurnByConversation finds the active root turn for a channel chat.
func (al *AgentLoop) GetActiveTurnByConversation(channel, chatID string) *ActiveTurnInfo {
	var found *ActiveTurnInfo
	al.activeTurnStates.Range(func(_, value any) bool {
		ts, ok := value.(*turnState)
		if !ok {
			return true
		}
		info := ts.snapshot()
		if info.Depth == 0 && info.Channel == channel && info.ChatID == chatID {
			found = &info
			return false
		}
		return true
	})
	return found
}

// ConversationInterrupted reports whether persisted history ends in an unfinished turn.
func (al *AgentLoop) ConversationInterrupted(channel, chatID, senderID string) (bool, error) {
	msg := bus.InboundMessage{
		Channel: channel, ChatID: chatID, SenderID: senderID,
		Context: bus.InboundContext{Channel: channel, ChatID: chatID, SenderID: senderID, ChatType: "direct"},
	}
	key, agentID, ok := al.resolveSteeringTarget(msg)
	if !ok {
		return false, nil
	}
	agent, ok := al.registry.GetAgent(agentID)
	if !ok || agent.Sessions == nil {
		return false, nil
	}
	if store, ok := agent.Sessions.(session.TurnCheckpointStore); ok {
		checkpoint, err := store.GetTurnCheckpoint(key)
		if err != nil {
			return false, err
		}
		if checkpoint.Status != "" {
			return checkpoint.Status != "completed", nil
		}
	}
	history, err := session.HistoryChecked(agent.Sessions, key)
	if err != nil {
		return false, err
	}
	if len(history) == 0 {
		return false, nil
	}
	last := history[len(history)-1]
	return last.Role == "user" || last.Role == "tool" ||
		(last.Role == "assistant" && (len(last.ToolCalls) > 0 || strings.HasPrefix(last.Content, "Task stopped. Completed work is retained;"))), nil
}

// ConversationTurnCompletion returns the latest durable completion metadata
// for a conversation. It is available after the active turn has been cleared.
func (al *AgentLoop) ConversationTurnCompletion(channel, chatID, senderID string) (TurnCompletionInfo, bool, error) {
	msg := bus.InboundMessage{
		Channel: channel, ChatID: chatID, SenderID: senderID,
		Context: bus.InboundContext{Channel: channel, ChatID: chatID, SenderID: senderID, ChatType: "direct"},
	}
	key, agentID, ok := al.resolveSteeringTarget(msg)
	if !ok {
		return TurnCompletionInfo{}, false, nil
	}
	agent, ok := al.registry.GetAgent(agentID)
	if !ok || agent.Sessions == nil {
		return TurnCompletionInfo{}, false, nil
	}
	store, ok := agent.Sessions.(session.TurnCheckpointStore)
	if !ok {
		return TurnCompletionInfo{}, false, nil
	}
	checkpoint, err := store.GetTurnCheckpoint(key)
	if err != nil {
		return TurnCompletionInfo{}, false, err
	}
	if checkpoint.TurnID == "" {
		return TurnCompletionInfo{}, false, nil
	}
	reason := TurnEndReason(checkpoint.EndReason)
	if reason == "" {
		reason = TurnEndReasonCompleted
	}
	return TurnCompletionInfo{
		TurnID:    checkpoint.TurnID,
		Status:    checkpoint.Status,
		EndReason: reason,
	}, true, nil
}

// =============================================================================
// turnState - getters and setters
// =============================================================================

func (ts *turnState) snapshot() ActiveTurnInfo {
	ts.mu.RLock()
	lifecycle := ts.lifecycle
	ts.mu.RUnlock()
	phase := TurnPhaseCreated
	var phaseHistory []TurnPhaseTransition
	if lifecycle != nil {
		phase = lifecycle.Phase()
		phaseHistory = lifecycle.History()
	}

	ts.mu.RLock()
	defer ts.mu.RUnlock()

	return ActiveTurnInfo{
		TurnID:       ts.turnID,
		AgentID:      ts.agentID,
		SessionKey:   ts.sessionKey,
		Channel:      ts.channel,
		ChatID:       ts.chatID,
		UserMessage:  ts.userMessage,
		Phase:        phase,
		Iteration:    ts.iteration,
		StartedAt:    ts.startedAt,
		Depth:        ts.depth,
		ParentTurnID: ts.parentTurnID,
		ChildTurnIDs: append([]string(nil), ts.childTurnIDs...),
		Plan:         append([]planStep(nil), ts.plan...),
		Activities:   append([]session.TurnActivity(nil), ts.activities...),
		PhaseHistory: phaseHistory,
	}
}

// transitionPhase moves the turn through its explicit lifecycle. Invalid
// transitions are implementation bugs; record them on the turn so callers can
// return a useful error instead of silently drifting into a terminal state.
func (ts *turnState) transitionPhase(phase TurnPhase) error {
	if ts == nil {
		return fmt.Errorf("turn state is nil")
	}
	ts.mu.Lock()
	if ts.lifecycle == nil {
		ts.lifecycle = newTurnLifecycle()
	}
	lifecycle := ts.lifecycle
	ts.mu.Unlock()
	return lifecycle.Transition(phase)
}

func (ts *turnState) phaseSnapshot() TurnPhase {
	if ts == nil {
		return TurnPhaseCreated
	}
	ts.mu.RLock()
	lifecycle := ts.lifecycle
	ts.mu.RUnlock()
	if lifecycle == nil {
		return TurnPhaseCreated
	}
	return lifecycle.Phase()
}

func (ts *turnState) phaseHistory() []TurnPhaseTransition {
	if ts == nil {
		return nil
	}
	ts.mu.RLock()
	lifecycle := ts.lifecycle
	ts.mu.RUnlock()
	if lifecycle == nil {
		return nil
	}
	return lifecycle.History()
}

func (ts *turnState) setIteration(iteration int) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.iteration = iteration
}

func (ts *turnState) currentIteration() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.iteration
}

func (ts *turnState) setFinalContent(content string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.finalContent = content
}

func (ts *turnState) finalContentLen() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return len(ts.finalContent)
}

func (ts *turnState) finalContentSnapshot() string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.finalContent
}

func (ts *turnState) recordToolKind(tool string) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	for _, existing := range ts.toolKinds {
		if existing == tool {
			return
		}
	}
	ts.toolKinds = append(ts.toolKinds, tool)
}

func (ts *turnState) toolKindsSnapshot() []string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return append([]string(nil), ts.toolKinds...)
}

func (ts *turnState) recordToolExecution(tool string, success bool, errorSummary string, skillNames []string) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return
	}

	ts.recordToolKind(tool)

	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.toolExecutions = append(ts.toolExecutions, ToolExecutionRecord{
		Name:         tool,
		Success:      success,
		ErrorSummary: strings.TrimSpace(errorSummary),
		SkillNames:   append([]string(nil), skillNames...),
	})
}

func (ts *turnState) toolExecutionsSnapshot() []ToolExecutionRecord {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	if len(ts.toolExecutions) == 0 {
		return nil
	}

	out := make([]ToolExecutionRecord, 0, len(ts.toolExecutions))
	for _, exec := range ts.toolExecutions {
		out = append(out, ToolExecutionRecord{
			Name:         exec.Name,
			Success:      exec.Success,
			ErrorSummary: exec.ErrorSummary,
			SkillNames:   append([]string(nil), exec.SkillNames...),
		})
	}
	return out
}

func (ts *turnState) startTurnActivity(kind, title, toolCallID, tool, detail string) string {
	if ts == nil {
		return ""
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.activitySeq++
	id := fmt.Sprintf("%s-activity-%d", ts.turnID, ts.activitySeq)
	ts.activities = append(ts.activities, session.TurnActivity{
		ID: id, Kind: kind, Status: "running", Title: title,
		ToolCallID: toolCallID, Tool: tool, Detail: detail, StartedAt: time.Now().UTC(),
	})
	return id
}

func (ts *turnState) finishTurnActivity(id string, update func(*session.TurnActivity)) {
	if ts == nil || id == "" {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for i := range ts.activities {
		if ts.activities[i].ID != id {
			continue
		}
		update(&ts.activities[i])
		now := time.Now().UTC()
		if ts.activities[i].CompletedAt == nil {
			ts.activities[i].CompletedAt = &now
			ts.activities[i].DurationMS = now.Sub(ts.activities[i].StartedAt).Milliseconds()
		}
		return
	}
}

func (ts *turnState) finishRunningActivities(status, detail string) {
	if ts == nil {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	now := time.Now().UTC()
	for i := range ts.activities {
		activity := &ts.activities[i]
		if activity.Status != "running" {
			continue
		}
		activity.Status = status
		if detail != "" {
			activity.Detail = strings.TrimSpace(activity.Detail + "\n" + detail)
		}
		activity.CompletedAt = &now
		activity.DurationMS = now.Sub(activity.StartedAt).Milliseconds()
	}
}

func (ts *turnState) activitiesSnapshot() []session.TurnActivity {
	if ts == nil {
		return nil
	}
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	out := make([]session.TurnActivity, 0, len(ts.activities))
	for _, activity := range ts.activities {
		activity.Artifacts = append([]string(nil), activity.Artifacts...)
		out = append(out, activity)
	}
	return out
}

func (ts *turnState) recordAttemptedSkills(skillNames []string) {
	if len(skillNames) == 0 {
		return
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	for _, skillName := range skillNames {
		skillName = strings.TrimSpace(skillName)
		if skillName == "" {
			continue
		}
		seen := false
		for _, existing := range ts.attemptedSkills {
			if existing == skillName {
				seen = true
				break
			}
		}
		if seen {
			continue
		}
		ts.attemptedSkills = append(ts.attemptedSkills, skillName)
	}
}

func (ts *turnState) attemptedSkillsSnapshot() []string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return append([]string(nil), ts.attemptedSkills...)
}

func (ts *turnState) recordSkillContextSnapshot(trigger string, skillNames []string) {
	if len(skillNames) == 0 {
		return
	}

	filtered := make([]string, 0, len(skillNames))
	for _, skillName := range skillNames {
		skillName = strings.TrimSpace(skillName)
		if skillName == "" {
			continue
		}
		filtered = append(filtered, skillName)
	}
	if len(filtered) == 0 {
		return
	}

	ts.recordAttemptedSkills(filtered)

	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.skillContextTrace = append(ts.skillContextTrace, SkillContextSnapshot{
		Sequence:   len(ts.skillContextTrace) + 1,
		Trigger:    trigger,
		SkillNames: append([]string(nil), filtered...),
	})
}

func (ts *turnState) latestSkillContextSnapshot() []string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	if len(ts.skillContextTrace) == 0 {
		return nil
	}
	return append([]string(nil), ts.skillContextTrace[len(ts.skillContextTrace)-1].SkillNames...)
}

func (ts *turnState) skillContextSnapshotsSnapshot() []SkillContextSnapshot {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	if len(ts.skillContextTrace) == 0 {
		return nil
	}

	snapshots := make([]SkillContextSnapshot, 0, len(ts.skillContextTrace))
	for _, snapshot := range ts.skillContextTrace {
		snapshots = append(snapshots, SkillContextSnapshot{
			Sequence:   snapshot.Sequence,
			Trigger:    snapshot.Trigger,
			SkillNames: append([]string(nil), snapshot.SkillNames...),
		})
	}
	return snapshots
}

func (ts *turnState) setTurnCancel(cancel context.CancelFunc) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.turnCancel = cancel
}

func (ts *turnState) setProviderCancel(cancel context.CancelFunc) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.providerCancel = cancel
}

func (ts *turnState) clearProviderCancel(_ context.CancelFunc) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.providerCancel = nil
}

func (ts *turnState) requestGracefulInterrupt(hint string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.hardAbort {
		return false
	}
	ts.gracefulInterrupt = true
	ts.gracefulInterruptHint = hint
	return true
}

func (ts *turnState) gracefulInterruptRequested() (bool, string) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.gracefulInterrupt && !ts.gracefulTerminalUsed, ts.gracefulInterruptHint
}

func (ts *turnState) markGracefulTerminalUsed() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.gracefulTerminalUsed = true
}

func (ts *turnState) requestHardAbort() bool {
	ts.mu.Lock()
	if ts.hardAbort {
		ts.mu.Unlock()
		return false
	}
	ts.hardAbort = true
	if ts.endReason == "" || ts.endReason == TurnEndReasonCompleted {
		ts.endReason = TurnEndReasonUserInterrupted
	}
	turnCancel := ts.turnCancel
	providerCancel := ts.providerCancel
	ts.mu.Unlock()

	if providerCancel != nil {
		providerCancel()
	}
	if turnCancel != nil {
		turnCancel()
	}
	return true
}

func (ts *turnState) hardAbortRequested() bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.hardAbort
}

func (ts *turnState) eventMeta(source, tracePath string) HookMeta {
	snap := ts.snapshot()
	return HookMeta{
		AgentID:      snap.AgentID,
		TurnID:       snap.TurnID,
		ParentTurnID: snap.ParentTurnID,
		SessionKey:   snap.SessionKey,
		Iteration:    snap.Iteration,
		Source:       source,
		TracePath:    tracePath,
		turnContext:  cloneTurnContext(ts.turnCtx),
	}
}

func (ts *turnState) recordPersistedMessage(msg providers.Message) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.persistedMessages = append(ts.persistedMessages, msg)
}

func (ts *turnState) persistedMessagesSnapshot() []providers.Message {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return append([]providers.Message(nil), ts.persistedMessages...)
}

// ingestMessage records a persisted message in the configured context manager.
func (ts *turnState) ingestMessage(ctx context.Context, al *AgentLoop, msg providers.Message) error {
	if al.contextManager == nil {
		return nil
	}
	if err := al.contextManager.Ingest(ctx, &IngestRequest{
		SessionKey: ts.sessionKey,
		Message:    msg,
	}); err != nil {
		return fmt.Errorf("context manager ingest: %w", err)
	}
	return nil
}

func matchingTurnMessageTail(history, persisted []providers.Message) int {
	maxMatch := min(len(history), len(persisted))
	for size := maxMatch; size > 0; size-- {
		if messageSlicesEquivalent(history[len(history)-size:], persisted[len(persisted)-size:]) {
			return size
		}
	}
	return 0
}

func splitHistoryForActiveTurn(
	history []providers.Message,
	persisted []providers.Message,
) ([]providers.Message, []providers.Message) {
	matched := matchingTurnMessageTail(history, persisted)
	if matched <= 0 {
		return append([]providers.Message(nil), history...), nil
	}

	stable := append([]providers.Message(nil), history[:len(history)-matched]...)
	protected := append([]providers.Message(nil), history[len(history)-matched:]...)
	return stable, protected
}

func messageSlicesEquivalent(a, b []providers.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !messagesEquivalent(a[i], b[i]) {
			return false
		}
	}
	return true
}

func messagesEquivalent(a, b providers.Message) bool {
	return reflect.DeepEqual(normalizeMessageForComparison(a), normalizeMessageForComparison(b))
}

func normalizeMessageForComparison(msg providers.Message) providers.Message {
	// Session stores may assign a timestamp while persisting a message.
	msg.CreatedAt = nil
	msg.PromptLayer = ""
	msg.PromptSlot = ""
	msg.PromptSource = ""

	if len(msg.Media) == 0 {
		msg.Media = nil
	}
	if len(msg.Attachments) == 0 {
		msg.Attachments = nil
	}
	if len(msg.SystemParts) == 0 {
		msg.SystemParts = nil
	} else {
		msg.SystemParts = append([]providers.ContentBlock(nil), msg.SystemParts...)
		for i := range msg.SystemParts {
			msg.SystemParts[i].PromptLayer = ""
			msg.SystemParts[i].PromptSlot = ""
			msg.SystemParts[i].PromptSource = ""
		}
	}
	if len(msg.ToolCalls) == 0 {
		msg.ToolCalls = nil
	} else {
		msg.ToolCalls = append([]providers.ToolCall(nil), msg.ToolCalls...)
		for i := range msg.ToolCalls {
			msg.ToolCalls[i].Name = ""
			msg.ToolCalls[i].Arguments = nil
			msg.ToolCalls[i].ThoughtSignature = ""
			if msg.ToolCalls[i].Function != nil {
				fn := *msg.ToolCalls[i].Function
				fn.ThoughtSignature = ""
				msg.ToolCalls[i].Function = &fn
			}
		}
	}

	return msg
}

func (ts *turnState) interruptHintMessage() providers.Message {
	_, hint := ts.gracefulInterruptRequested()
	content := "Interrupt requested. Stop scheduling tools and provide a short final summary."
	if hint != "" {
		content += "\n\nInterrupt hint: " + hint
	}
	return interruptPromptMessage(content)
}

// =============================================================================
// SubTurn-related methods
// =============================================================================

// Finish marks the turn as finished and signals all waiting SubTurns.
// It is safe to call more than once; only the first call performs cleanup.
func (ts *turnState) Finish(isHardAbort bool) {
	if ts == nil {
		return
	}
	ts.finishOnce.Do(func() {
		if isHardAbort {
			ts.requestHardAbort()
		}
		ts.isFinished.Store(true)
		// Both graceful completion and hard abort end the parent from a child's
		// point of view. Non-critical children stop; critical children may still
		// run, but must not mistake a completed parent for a live one.
		ts.parentEnded.Store(true)
		if isHardAbort {
			ts.finishRunningActivities("effects_unknown", "The turn was stopped before the tool result could be verified.")
		} else {
			ts.finishRunningActivities("cancelled", "The turn ended before this activity completed.")
		}

		// Signal completion exactly once. The delivery queue accepts no new items
		// after this point; producers that race with Finish fall back to a follow-up
		// inbound turn instead of blocking or panicking.
		ts.closeOnce.Do(func() {
			ts.mu.Lock()
			if ts.finishedChan == nil {
				ts.finishedChan = make(chan struct{})
			}
			close(ts.finishedChan)
			ts.mu.Unlock()
		})

		// Results already buffered when the parent ends can no longer reach this
		// turn's model. Graceful completion turns them into follow-up turns; a
		// hard abort reports them as orphans because the user stopped the work.
		ts.drainDeliveriesOnFinish(isHardAbort)

		// Cancel the turn context
		if ts.cancelFunc != nil {
			ts.cancelFunc()
		}

		// Hard abort cascades to all child turns
		if isHardAbort && ts.al != nil {
			ts.mu.RLock()
			children := append([]string(nil), ts.childTurnIDs...)
			ts.mu.RUnlock()
			for _, childID := range children {
				if val, ok := ts.al.activeTurnStates.Load(childID); ok {
					if child, ok := val.(*turnState); ok {
						child.Finish(true)
					}
				}
			}
		}
	})
}

func (ts *turnState) drainDeliveriesOnFinish(isHardAbort bool) {
	if ts == nil {
		return
	}
	ts.mu.Lock()
	if ts.deliveries == nil {
		ts.deliveries = newAsyncDeliveryQueue(defaultAsyncDeliveryCapacity)
	}
	queue := ts.deliveries
	ts.mu.Unlock()
	for _, delivery := range queue.close() {
		if ts.al == nil {
			continue
		}
		if !isHardAbort && ts.deferAsyncFollowUp(delivery) {
			continue
		}
		ts.al.emitAsyncOrphan(ts, delivery, "parent_finished")
	}
}

// Finished returns whether the turn has finished
func (ts *turnState) Finished() chan struct{} {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.finishedChan == nil {
		ts.finishedChan = make(chan struct{})
	}
	return ts.finishedChan
}

// IsParentEnded checks if the parent turn has ended
func (ts *turnState) IsParentEnded() bool {
	if ts.parentTurnState == nil {
		return false
	}
	return ts.parentTurnState.parentEnded.Load()
}

// GetLastFinishReason returns the last LLM finish_reason
func (ts *turnState) GetLastFinishReason() string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.lastFinishReason
}

// SetLastFinishReason sets the last LLM finish_reason
func (ts *turnState) SetLastFinishReason(reason string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.lastFinishReason = reason
}

// GetLastUsage returns the last LLM usage info
func (ts *turnState) GetLastUsage() *providers.UsageInfo {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.lastUsage
}

// SetLastUsage sets the last LLM usage info
func (ts *turnState) SetLastUsage(usage *providers.UsageInfo) { ts.recordUsage(usage, false) }

// =============================================================================
// Context helper functions for turnState
// =============================================================================

type turnStateKeyType struct{}

var turnStateKey = turnStateKeyType{}

func withTurnState(ctx context.Context, ts *turnState) context.Context {
	return context.WithValue(ctx, turnStateKey, ts)
}

func turnStateFromContext(ctx context.Context) *turnState {
	ts, _ := ctx.Value(turnStateKey).(*turnState)
	return ts
}

// TurnStateFromContext retrieves turnState from context (exported for tools)
func TurnStateFromContext(ctx context.Context) *turnState {
	return turnStateFromContext(ctx)
}
