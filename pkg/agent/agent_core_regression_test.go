package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/approval"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

type coreLoopTestProvider struct {
	systemStarted chan struct{}
	releaseSystem chan struct{}
	startOnce     sync.Once
	panicSystem   bool
}

func (p *coreLoopTestProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	content := ""
	for _, msg := range messages {
		content += msg.Content
	}
	if strings.Contains(content, "system-slow") {
		p.startOnce.Do(func() { close(p.systemStarted) })
		select {
		case <-p.releaseSystem:
			return &providers.LLMResponse{Content: "system response"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.panicSystem && strings.Contains(content, "system-panic") {
		p.startOnce.Do(func() { close(p.systemStarted) })
		panic("system provider panic")
	}
	return &providers.LLMResponse{Content: "normal response"}, nil
}

func (p *coreLoopTestProvider) GetDefaultModel() string { return "core-test" }

type criticalWaitProvider struct {
	started chan struct{}
	once    sync.Once
}

func (p *criticalWaitProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.once.Do(func() { close(p.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *criticalWaitProvider) GetDefaultModel() string { return "critical-wait" }

func newCoreLoopTestAgent(t *testing.T, provider providers.LLMProvider) (*AgentLoop, *bus.MessageBus, context.CancelFunc) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.MaxParallelTurns = 2
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, provider)
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(runCtx) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case err := <-runDone:
			if err != nil {
				t.Errorf("Run() error = %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("timeout waiting for Run to stop")
		}
		msgBus.Close()
	})
	return al, msgBus, cancelRun
}

func waitForTestOutbound(t *testing.T, msgBus *bus.MessageBus, chatID, content string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case outbound := <-msgBus.OutboundChan():
			if outbound.ChatID == chatID && outbound.Content == content {
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting for outbound %s to %s", content, chatID)
		}
	}
}

func TestSystemMessageDoesNotBlockReceiveLoop(t *testing.T) {
	provider := &coreLoopTestProvider{
		systemStarted: make(chan struct{}),
		releaseSystem: make(chan struct{}),
	}
	_, msgBus, _ := newCoreLoopTestAgent(t, provider)

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Context: bus.InboundContext{Channel: "system", ChatID: "test:system-chat", ChatType: "direct", SenderID: "system"},
		Content: "system-slow",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.systemStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("system provider did not start")
	}

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Context: bus.InboundContext{Channel: "test", ChatID: "normal-chat", ChatType: "direct", SenderID: "user"},
		Content: "normal message",
	}); err != nil {
		t.Fatal(err)
	}
	waitForTestOutbound(t, msgBus, "normal-chat", "normal response")
	close(provider.releaseSystem)
}

func TestSystemMessagePanicDoesNotStopReceiveLoop(t *testing.T) {
	provider := &coreLoopTestProvider{
		systemStarted: make(chan struct{}),
		releaseSystem: make(chan struct{}),
		panicSystem:   true,
	}
	_, msgBus, _ := newCoreLoopTestAgent(t, provider)

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Context: bus.InboundContext{Channel: "system", ChatID: "test:panic-chat", ChatType: "direct", SenderID: "system"},
		Content: "system-panic",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.systemStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("panicking system provider did not start")
	}

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Context: bus.InboundContext{Channel: "test", ChatID: "after-panic", ChatType: "direct", SenderID: "user"},
		Content: "normal after panic",
	}); err != nil {
		t.Fatal(err)
	}
	waitForTestOutbound(t, msgBus, "after-panic", "normal response")
}

func TestRuntimeSnapshotStaysCoherentDuringReload(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &mockProvider{})
	t.Cleanup(func() {
		al.Close()
		msgBus.Close()
	})

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			next := config.DefaultConfig()
			next.Agents.Defaults.Workspace = cfg.Agents.Defaults.Workspace
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := al.ReloadProviderAndConfig(ctx, &mockProvider{}, next)
			cancel()
			if err != nil {
				errCh <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			runtime := al.runtimeSnapshot()
			if runtime.cfg == nil || runtime.registry == nil || runtime.fallback == nil {
				errCh <- fmt.Errorf("incomplete runtime snapshot: %+v", runtime)
				return
			}
			if runtime.registry.cfg != runtime.cfg {
				errCh <- errors.New("config and registry came from different reload versions")
				return
			}
			pipeline := NewPipeline(al)
			if pipeline.Cfg == nil || pipeline.Fallback == nil {
				errCh <- errors.New("pipeline captured incomplete runtime")
				return
			}
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func TestMessageToolAllowedTargetsFollowReload(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Tools.Message.AllowedTargets = []config.MessageTarget{{Channel: "telegram", ChatID: "old"}}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close(); msgBus.Close() })

	execute := func(chatID string) bool {
		mt, ok := al.GetRegistry().GetDefaultAgent().Tools.Get("message")
		if !ok {
			t.Fatal("message tool is not registered")
		}
		result := mt.Execute(
			tools.WithToolSessionContext(context.Background(), "main", "session", nil),
			map[string]any{"content": "ping", "channel": "telegram", "chat_id": chatID},
		)
		return result != nil && result.IsError
	}
	if execute("old") {
		t.Fatal("initial allowed target was rejected")
	}

	next := config.DefaultConfig()
	next.Agents.Defaults.Workspace = cfg.Agents.Defaults.Workspace
	next.Tools.Message.AllowedTargets = []config.MessageTarget{{Channel: "telegram", ChatID: "new"}}
	if err := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, next); err != nil {
		t.Fatal(err)
	}
	if !execute("old") {
		t.Fatal("old target remained allowed after reload")
	}
	if execute("new") {
		t.Fatal("new target was rejected after reload")
	}
}

func TestStopCancelsAndWaitsForBackgroundWork(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { msgBus.Close() })

	var completed atomic.Bool
	done := make(chan struct{})
	NewSubTurnSpawner(al).RunBackground(func() {
		<-al.backgroundContext().Done()
		completed.Store(true)
		close(done)
	})

	al.Stop()
	select {
	case <-done:
	default:
		t.Fatal("Stop returned before background work completed")
	}
	if !completed.Load() {
		t.Fatal("background work did not observe cancellation")
	}
	if al.beginBackground() {
		t.Fatal("background work accepted after shutdown started")
	}
}

func TestStopCancelsCriticalSubTurn(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	msgBus := bus.NewMessageBus()
	provider := &criticalWaitProvider{started: make(chan struct{})}
	al := NewAgentLoop(cfg, msgBus, provider)
	t.Cleanup(func() { msgBus.Close() })

	parent := &turnState{
		ctx:        context.Background(),
		turnID:     "critical-parent",
		session:    newEphemeralSession(nil),
		deliveries: newAsyncDeliveryQueue(1),
	}
	parent.ctx, parent.cancelFunc = context.WithCancel(parent.ctx)
	defer parent.cancelFunc()

	done := make(chan struct{})
	NewSubTurnSpawner(al).RunBackground(func() {
		defer close(done)
		_, _ = spawnSubTurn(parent.ctx, al, parent, SubTurnConfig{
			Model:    "critical-model",
			Critical: true,
		})
	})
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("critical SubTurn did not start")
	}

	al.Stop()
	select {
	case <-done:
	default:
		t.Fatal("Stop returned before critical SubTurn drained")
	}
}

func TestRespondApprovalConcurrentCAS(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close(); msgBus.Close() })

	store := session.NewSessionManager(t.TempDir())
	agent := al.GetRegistry().GetDefaultAgent()
	agent.Sessions = store
	key := "approval-race"
	now := time.Now().UTC()
	if err := store.SetTurnCheckpoint(key, session.TurnCheckpoint{
		TurnID: "turn", Status: "awaiting_approval", UpdatedAt: now,
		Approval: &approval.Request{ID: "approval-1", SessionKey: key, Status: approval.StatusPending, ExpiresAt: now.Add(time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}

	const callers = 20
	var wg sync.WaitGroup
	errCh := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- al.RespondApproval(context.Background(), approval.Response{ID: "approval-1", SessionKey: key, Approve: true})
		}()
	}
	wg.Wait()
	close(errCh)
	successes := 0
	alreadyClosed := 0
	for err := range errCh {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, errApprovalAlreadyClosed):
			alreadyClosed++
		default:
			t.Fatalf("unexpected approval error: %v", err)
		}
	}
	if successes != 1 || alreadyClosed != callers-1 {
		t.Fatalf("successes=%d alreadyClosed=%d, want 1/%d", successes, alreadyClosed, callers-1)
	}
	checkpoint, err := store.GetTurnCheckpoint(key)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Status != "ready_to_resume" || checkpoint.Approval == nil || checkpoint.Approval.Status != approval.StatusApproved {
		t.Fatalf("checkpoint = %+v", checkpoint)
	}
}
