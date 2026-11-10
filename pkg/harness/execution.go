package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

func validateScenario(scenario Scenario) error {
	if strings.TrimSpace(scenario.Name) == "" {
		return errors.New("harness: scenario name is required")
	}
	if len(scenarioMessages(scenario)) == 0 {
		return errors.New("harness: user message is required")
	}
	sources := 0
	if scenario.Provider != nil {
		sources++
	}
	if strings.TrimSpace(scenario.ReplayPath) != "" {
		sources++
	}
	if len(scenario.Steps) > 0 {
		sources++
	}
	if sources == 0 {
		return errors.New("harness: provider, replay_path, or model steps are required")
	}
	if sources > 1 {
		return errors.New("harness: provider, replay_path, and model steps are mutually exclusive")
	}
	if scenario.Timeout < 0 {
		return errors.New("harness: timeout cannot be negative")
	}
	if scenario.AsyncSettle < 0 {
		return errors.New("harness: async settle cannot be negative")
	}
	if scenario.MaxAsyncFollowUps < 0 {
		return errors.New("harness: max async follow-ups cannot be negative")
	}
	if scenario.CloseBusBeforeSettle && scenario.AsyncSettle <= 0 {
		return errors.New("harness: close_bus_before_settle requires async_settle")
	}
	return nil
}

// Run executes a scenario and returns its trajectory.
func Run(ctx context.Context, scenario Scenario) (*Result, error) {
	if err := validateScenario(scenario); err != nil {
		return nil, err
	}
	messages := scenarioMessages(scenario)

	timeout := scenario.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	collectCtx, collectCancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer collectCancel()

	workspace := strings.TrimSpace(scenario.Workspace)
	removeWorkspace := false
	if workspace == "" {
		var err error
		workspace, err = os.MkdirTemp("", "gogoclaw-harness-*")
		if err != nil {
			return nil, err
		}
		removeWorkspace = true
	} else if err := os.MkdirAll(workspace, 0o755); err != nil {
		return nil, fmt.Errorf("harness: create workspace: %w", err)
	}
	if removeWorkspace {
		defer os.RemoveAll(workspace)
	}

	model := scenario.Model
	if model == "" {
		model = "harness-model"
	}
	provider := scenario.Provider
	if strings.TrimSpace(scenario.ReplayPath) != "" {
		replayTrace, replayErr := LoadTrace(scenario.ReplayPath)
		if replayErr != nil {
			return nil, replayErr
		}
		provider = NewReplayProvider(model, replayTrace)
	}
	if provider == nil {
		provider = NewScriptedProvider(model, scenario.Steps...)
	}
	var recorder *RecordingProvider
	if strings.TrimSpace(scenario.RecordPath) != "" {
		recorder = NewRecordingProvider(provider)
		provider = recorder
	}
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
		Workspace:         workspace,
		ModelName:         model,
		MaxTokens:         scenario.MaxTokens,
		ContextWindow:     scenario.ContextWindow,
		MaxToolIterations: scenario.MaxToolIterations,
	}}}
	if cfg.Agents.Defaults.MaxTokens <= 0 {
		cfg.Agents.Defaults.MaxTokens = 4096
	}
	if cfg.Agents.Defaults.ContextWindow <= 0 {
		cfg.Agents.Defaults.ContextWindow = 32768
	}
	if cfg.Agents.Defaults.MaxToolIterations <= 0 {
		cfg.Agents.Defaults.MaxToolIterations = 8
	}

	loop := agent.NewAgentLoop(cfg, msgBus, provider)
	defer loop.Close()
	sessionKey := harnessSessionKey(scenario)
	if err := configureScenarioSession(loop, workspace, scenario); err != nil {
		return nil, err
	}
	if scenario.Setup != nil {
		if err := scenario.Setup(loop); err != nil {
			return nil, fmt.Errorf("harness: scenario setup: %w", err)
		}
	}
	rt := ToolRuntime{Loop: loop, Bus: msgBus, SessionKey: sessionKey}
	if binder, ok := provider.(interface{ bindRuntime(ToolRuntime) }); ok {
		binder.bindRuntime(rt)
	}
	if recorder != nil {
		if binder, ok := recorder.inner.(interface{ bindRuntime(ToolRuntime) }); ok {
			binder.bindRuntime(rt)
		}
	}
	registeredTools := append([]tools.Tool(nil), scenario.Tools...)
	if scenario.ToolFactory != nil {
		created, factoryErr := scenario.ToolFactory(ToolRuntime{Loop: loop, Bus: msgBus, SessionKey: sessionKey})
		if factoryErr != nil {
			return nil, fmt.Errorf("harness: tool factory: %w", factoryErr)
		}
		registeredTools = append(registeredTools, created...)
	}
	for _, tool := range registeredTools {
		if spawnerAware, ok := tool.(interface{ SetSpawner(tools.SubTurnSpawner) }); ok {
			spawnerAware.SetSpawner(agent.NewSubTurnSpawner(loop))
		}
		loop.RegisterTool(tool)
	}

	eventsCh, stopEvents, err := subscribe(ctx, loop, 1024)
	if err != nil {
		return nil, err
	}
	defer stopEvents()

	var (
		mu       sync.Mutex
		events   []runtimeevents.Event
		outbound []bus.OutboundMessage
	)
	stateChanged := make(chan struct{}, 1)
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		for evt := range eventsCh {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
			notifyStateChange(stateChanged)
		}
	}()
	outboundDone := make(chan struct{})
	go func() {
		defer close(outboundDone)
		for msg := range msgBus.OutboundChan() {
			mu.Lock()
			outbound = append(outbound, msg)
			mu.Unlock()
			notifyStateChange(stateChanged)
		}
	}()
	if scenario.ConcurrentSetup != nil {
		if err := scenario.ConcurrentSetup(ToolRuntime{Loop: loop, Bus: msgBus, SessionKey: sessionKey}); err != nil {
			stopEvents()
			<-eventsDone
			msgBus.Close()
			<-outboundDone
			return nil, fmt.Errorf("harness: concurrent setup: %w", err)
		}
	}

	result := &Result{Name: scenario.Name, workspace: workspace}
	if scenario.ProductionIngress {
		ingressCtx, ingressCancel := context.WithCancel(runCtx)
		ingressDone := make(chan error, 1)
		go func() { ingressDone <- loop.Run(ingressCtx) }()
		defer func() {
			ingressCancel()
			<-ingressDone
		}()
	}
	modelCursor := 0
	for _, userMessage := range messages {
		turn, nextModel, err := runScenarioTurn(
			runCtx, collectCtx, loop, provider, msgBus, scenario, registeredTools, userMessage, sessionKey,
			modelCursor, stateChanged, &mu, &events, &outbound,
		)
		modelCursor = nextModel
		if err != nil {
			stopEvents()
			<-eventsDone
			msgBus.Close()
			<-outboundDone
			return result, err
		}
		result.Turns = append(result.Turns, turn)
	}

	if scenario.AsyncSettle > 0 {
		if scenario.CloseBusBeforeSettle {
			msgBus.Close()
			<-outboundDone
		}
		if err := waitForContext(runCtx, scenario.AsyncSettle); err != nil {
			stopEvents()
			<-eventsDone
			msgBus.Close()
			<-outboundDone
			return result, err
		}
	}
	if err := waitAsyncTools(runCtx, registeredTools, scenario.Timeout); err != nil {
		stopEvents()
		<-eventsDone
		msgBus.Close()
		<-outboundDone
		return result, err
	}

	if !scenario.CloseBusBeforeSettle {
		result.AsyncFollowUps = append(result.AsyncFollowUps, drainInbound(msgBus.InboundChan())...)
	}
	maxFollowUps := scenario.MaxAsyncFollowUps
	if maxFollowUps <= 0 {
		maxFollowUps = 8
	}
	if scenario.ProcessFollowUps {
		processed := 0
		for processed < len(result.AsyncFollowUps) {
			if processed >= maxFollowUps {
				stopEvents()
				<-eventsDone
				msgBus.Close()
				<-outboundDone
				return result, fmt.Errorf(
					"harness: async follow-up limit %d reached with %d unprocessed",
					maxFollowUps,
					len(result.AsyncFollowUps)-processed,
				)
			}
			followUp := result.AsyncFollowUps[processed]
			turn, nextModel, err := runScenarioTurn(
				runCtx, collectCtx, loop, provider, msgBus, scenario, registeredTools, followUp.Content, sessionKey,
				modelCursor, stateChanged, &mu, &events, &outbound,
			)
			modelCursor = nextModel
			if err != nil {
				stopEvents()
				<-eventsDone
				msgBus.Close()
				<-outboundDone
				return result, err
			}
			result.Turns = append(result.Turns, turn)
			processed++
			if scenario.AsyncSettle > 0 {
				if err := waitForContext(runCtx, scenario.AsyncSettle); err != nil {
					stopEvents()
					<-eventsDone
					msgBus.Close()
					<-outboundDone
					return result, err
				}
			}
			if err := waitAsyncTools(runCtx, registeredTools, scenario.Timeout); err != nil {
				stopEvents()
				<-eventsDone
				msgBus.Close()
				<-outboundDone
				return result, err
			}
			result.AsyncFollowUps = append(result.AsyncFollowUps, drainInbound(msgBus.InboundChan())...)
		}
	}

	stopEvents()
	<-eventsDone
	msgBus.Close()
	<-outboundDone

	mu.Lock()
	traceEvents := append([]runtimeevents.Event(nil), events...)
	traceOutbound := make([]string, 0, len(outbound))
	for _, message := range outbound {
		traceOutbound = append(traceOutbound, message.Content)
	}
	mu.Unlock()
	result.ModelCalls = callsFromProvider(provider)
	result.ToolCalls = mergeToolObservations(toolCallsFromEvents(traceEvents), registeredTools)
	result.Events = traceEvents
	result.Outbound = traceOutbound
	for _, turn := range result.Turns {
		result.Duration += turn.Duration
		if result.Err == nil && turn.Err != nil {
			result.Err = turn.Err
		}
		result.Content = turn.Content
		result.TurnEnd = turn.TurnEnd
	}
	if defaultAgent := loop.GetRegistry().GetDefaultAgent(); defaultAgent != nil && defaultAgent.Sessions != nil {
		result.History = snapshotSessionHistory(defaultAgent.Sessions, sessionKey)
	}
	if recorder != nil {
		if err := recorder.Save(scenario.RecordPath); err != nil {
			return result, err
		}
	}
	return result, nil
}

func runScenarioTurn(
	runCtx context.Context,
	collectCtx context.Context,
	loop *agent.AgentLoop,
	provider providers.LLMProvider,
	msgBus *bus.MessageBus,
	scenario Scenario,
	scenarioTools []tools.Tool,
	userMessage string,
	sessionKey string,
	modelCursor int,
	stateChanged <-chan struct{},
	mu *sync.Mutex,
	events *[]runtimeevents.Event,
	outbound *[]bus.OutboundMessage,
) (TurnResult, int, error) {
	started := time.Now()
	mu.Lock()
	eventStart := len(*events)
	outboundStart := len(*outbound)
	mu.Unlock()

	var content string
	var runErr error
	if scenario.ProductionIngress {
		runErr = msgBus.PublishInbound(runCtx, bus.InboundMessage{
			Context:    bus.InboundContext{Channel: "pico", ChatID: "harness", SenderID: "harness-user"},
			Content:    userMessage,
			SessionKey: sessionKey,
		})
	} else {
		content, runErr = processTurnSafely(runCtx, loop, userMessage, sessionKey)
	}
	duration := time.Since(started)

	if scenario.ProductionIngress && runErr != nil {
		return TurnResult{}, modelCursor, runErr
	}
	turnEndIndex, err := waitForRootTurnEnd(collectCtx, sessionKey, eventStart, stateChanged, mu, events)
	if err != nil {
		if runErr != nil {
			return TurnResult{}, modelCursor, fmt.Errorf("harness: turn ended without root TurnEnd event: %w", runErr)
		}
		return TurnResult{}, modelCursor, err
	}
	if scenario.ProductionIngress && runErr == nil {
		if err := waitForSessionRelease(collectCtx, loop, sessionKey); err != nil {
			return TurnResult{}, modelCursor, err
		}
		if _, err := waitForFinalOutbound(collectCtx, sessionKey, outboundStart, stateChanged, mu, outbound); err != nil {
			return TurnResult{}, modelCursor, err
		}
	}

	calls := callsFromProvider(provider)
	if modelCursor > len(calls) {
		modelCursor = len(calls)
	}
	turnCalls := append([]ModelCall(nil), calls[modelCursor:]...)
	modelCursor = len(calls)

	mu.Lock()
	turnEvents := eventsForTurnRange(*events, eventStart, turnEndIndex)
	mu.Unlock()
	turnEndPayload := lastTurnEnd(turnEvents)
	if scenario.ProductionIngress && turnEndPayload != nil {
		content = turnEndPayload.FinalContent
	}
	if defaultAgent := loop.GetRegistry().GetDefaultAgent(); defaultAgent != nil && defaultAgent.Sessions != nil {
		turnHistory := snapshotSessionHistory(defaultAgent.Sessions, sessionKey)
		_ = msgBus
		return TurnResult{
			UserMessage: userMessage,
			Content:     content,
			Err:         runErr,
			Duration:    duration,
			ModelCalls:  turnCalls,
			ToolCalls:   mergeToolObservations(toolCallsFromEvents(turnEvents), scenarioTools),
			Events:      turnEvents,
			TurnEnd:     turnEndPayload,
			History:     turnHistory,
		}, modelCursor, nil
	}
	return TurnResult{
		UserMessage: userMessage,
		Content:     content,
		Err:         runErr,
		Duration:    duration,
		ModelCalls:  turnCalls,
		ToolCalls:   mergeToolObservations(toolCallsFromEvents(turnEvents), scenarioTools),
		Events:      turnEvents,
		TurnEnd:     turnEndPayload,
	}, modelCursor, nil
}

func notifyStateChange(ch chan<- struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func waitForRootTurnEnd(
	ctx context.Context,
	sessionKey string,
	start int,
	stateChanged <-chan struct{},
	mu *sync.Mutex,
	events *[]runtimeevents.Event,
) (int, error) {
	for {
		mu.Lock()
		for i := start; i < len(*events); i++ {
			evt := (*events)[i]
			if evt.Kind == runtimeevents.KindAgentTurnEnd &&
				evt.Correlation.ParentTurnID == "" &&
				evt.Scope.SessionKey == sessionKey {
				mu.Unlock()
				return i, nil
			}
		}
		mu.Unlock()

		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("harness: turn ended without root TurnEnd event: %w", ctx.Err())
		case <-stateChanged:
		}
	}
}

func waitForSessionRelease(ctx context.Context, loop *agent.AgentLoop, sessionKey string) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for loop.GetActiveTurnBySession(sessionKey) != nil {
		select {
		case <-ctx.Done():
			return fmt.Errorf("harness: inbound turn did not release its session: %w", ctx.Err())
		case <-ticker.C:
		}
	}
	return nil
}

func waitForFinalOutbound(
	ctx context.Context,
	sessionKey string,
	start int,
	stateChanged <-chan struct{},
	mu *sync.Mutex,
	outbound *[]bus.OutboundMessage,
) (int, error) {
	for {
		mu.Lock()
		for i := start; i < len(*outbound); i++ {
			msg := (*outbound)[i]
			if msg.SessionKey == sessionKey && msg.Context.Raw["outbound_kind"] == "final" {
				mu.Unlock()
				return i, nil
			}
		}
		mu.Unlock()

		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("harness: production ingress did not publish a final outbound: %w", ctx.Err())
		case <-stateChanged:
		}
	}
}

func processTurnSafely(
	ctx context.Context,
	loop *agent.AgentLoop,
	userMessage string,
	sessionKey string,
) (content string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("harness: recovered turn panic: %v", recovered)
		}
	}()
	return loop.ProcessDirectWithChannel(ctx, userMessage, sessionKey, "harness", "harness")
}

func workspacePath(workspace, path string) (string, error) {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) {
		return "", fmt.Errorf("workspace path %q must be a non-empty relative path", path)
	}
	full := filepath.Join(workspace, filepath.Clean(path))
	rel, err := filepath.Rel(workspace, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("workspace path %q escapes the scenario workspace", path)
	}
	return full, nil
}

func subscribe(ctx context.Context, loop *agent.AgentLoop, buffer int) (<-chan runtimeevents.Event, func(), error) {
	channel := loop.RuntimeEvents()
	if channel == nil {
		return nil, nil, errors.New("harness: runtime event bus is unavailable")
	}
	sub, events, err := channel.SubscribeChan(ctx, runtimeevents.SubscribeOptions{
		Name:         "harness",
		Buffer:       buffer,
		Concurrency:  runtimeevents.Locked,
		Backpressure: runtimeevents.Block,
	})
	if err != nil {
		return nil, nil, err
	}
	var once sync.Once
	stop := func() { once.Do(func() { _ = sub.Close() }) }
	return events, stop, nil
}

func scenarioMessages(scenario Scenario) []string {
	messages := make([]string, 0, len(scenario.Messages)+1)
	if strings.TrimSpace(scenario.UserMessage) != "" {
		messages = append(messages, scenario.UserMessage)
	}
	for _, message := range scenario.Messages {
		if strings.TrimSpace(message) != "" {
			messages = append(messages, message)
		}
	}
	return messages
}

func harnessSessionKey(scenario Scenario) string {
	sessionKey := strings.TrimSpace(scenario.SessionKey)
	if sessionKey == "" {
		sessionKey = "agent:main:harness:" + sanitizeSessionKey(scenario.Name)
	}
	return sessionKey
}

func configureScenarioSession(loop *agent.AgentLoop, workspace string, scenario Scenario) error {
	if scenario.Session == nil && scenario.SessionFault == nil {
		return nil
	}
	registry := loop.GetRegistry()
	defaultAgent := registry.GetDefaultAgent()
	if defaultAgent == nil {
		return errors.New("harness: default agent is unavailable")
	}
	store := scenario.Session
	if store == nil {
		store = session.NewSessionManager(workspace)
	}
	if scenario.SessionFault != nil {
		store = NewSessionFaultStore(store, scenario.SessionFault)
	}
	old := defaultAgent.Sessions
	defaultAgent.Sessions = store
	if old != nil && old != store {
		_ = old.Close()
	}
	return nil
}

func waitForContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func waitAsyncTools(ctx context.Context, scenarioTools []tools.Tool, scenarioTimeout time.Duration) error {
	timeout := scenarioTimeout
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for _, tool := range scenarioTools {
		waiter, ok := tool.(interface{ Wait(context.Context) error })
		if !ok {
			continue
		}
		if err := waiter.Wait(ctx); err != nil {
			return fmt.Errorf("harness: wait for async tool %s: %w", tool.Name(), err)
		}
	}
	return nil
}

func drainInbound(channel <-chan bus.InboundMessage) []bus.InboundMessage {
	var messages []bus.InboundMessage
	for {
		select {
		case message, ok := <-channel:
			if !ok {
				return messages
			}
			messages = append(messages, message)
		default:
			return messages
		}
	}
}
