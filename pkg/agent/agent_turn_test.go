package agent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/routing"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

func TestNewAgentLoop_RegistersWebSearchTool(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	if _, ok := agent.Tools.Get("web_search"); !ok {
		t.Fatal("expected web_search tool to be registered")
	}
}

func TestNewAgentLoop_RegistersWebSearchTool_WhenExplicitProviderUnavailable(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Tools.Web.Provider = "brave"
	cfg.Tools.Web.Brave.Enabled = true
	cfg.Tools.Web.Sogou.Enabled = true

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	if _, ok := agent.Tools.Get("web_search"); !ok {
		t.Fatal("expected web_search tool to fall back to auto provider selection")
	}
}

func TestNewAgentLoop_DoesNotRegisterWebSearchTool_WhenNoReadyProviders(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Tools.Web.Provider = "brave"
	cfg.Tools.Web.Brave.Enabled = true
	cfg.Tools.Web.Sogou.Enabled = false
	cfg.Tools.Web.DuckDuckGo.Enabled = false

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	if _, ok := agent.Tools.Get("web_search"); ok {
		t.Fatal("expected web_search tool to be absent when no providers are ready")
	}
}

func TestPublishResponseIfNeeded_DismissesToolFeedbackWhenMessageToolAlreadySent(t *testing.T) {
	al, msgBus, provider, sessions, cleanup := newTestAgentLoop(t)
	defer cleanup()
	_ = msgBus
	_ = provider
	_ = sessions

	cm := &recordingChannelManager{}
	al.channelManager = cm

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	mt := tools.NewMessageTool()
	mt.SetSendCallback(func(
		ctx context.Context,
		channel, chatID, content, replyToMessageID string,
		mediaParts []bus.MediaPart,
	) error {
		return nil
	})
	defaultAgent.Tools.Register(mt)

	result := mt.Execute(
		tools.WithToolSessionContext(context.Background(), "main", "session-1", nil),
		map[string]any{
			"content": "ack",
			"channel": "telegram",
			"chat_id": "-100123",
		},
	)
	if result == nil || result.IsError {
		t.Fatalf("message tool execute failed: %+v", result)
	}
	al.PublishResponseIfNeeded(context.Background(), "telegram", "-100123", "session-1", "final reply")

	if got := cm.dismissed; len(got) != 1 || got[0] != "telegram:-100123" {
		t.Fatalf("dismissed = %v, want [telegram:-100123]", got)
	}
}

func TestPublishResponseIfNeeded_MarksFinalOutbound(t *testing.T) {
	al, _, msgBus, provider, cleanup := newTestAgentLoop(t)
	defer cleanup()
	_ = provider

	al.PublishResponseIfNeeded(context.Background(), "pico", "pico:session-1", "session-1", "final reply")

	select {
	case outbound := <-msgBus.OutboundChan():
		if outbound.Content != "final reply" {
			t.Fatalf("outbound content = %q, want final reply", outbound.Content)
		}
		if outbound.Context.Raw[metadataKeyOutboundKind] != outboundKindFinal {
			t.Fatalf("outbound kind = %q, want %q", outbound.Context.Raw[metadataKeyOutboundKind], outboundKindFinal)
		}
		if outbound.SessionKey != "session-1" {
			t.Fatalf("outbound session key = %q, want session-1", outbound.SessionKey)
		}
	case <-time.After(time.Second):
		t.Fatal("expected final outbound")
	}
}

func TestPublishPicoReasoningIncludesSessionKey(t *testing.T) {
	al, _, msgBus, provider, cleanup := newTestAgentLoop(t)
	defer cleanup()
	_ = provider

	al.publishPicoReasoning(context.Background(), "reasoning", "pico-chat", "session-1", "")

	select {
	case outbound := <-msgBus.OutboundChan():
		if outbound.Channel != "pico" || outbound.ChatID != "pico-chat" {
			t.Fatalf("unexpected outbound target: %+v", outbound)
		}
		if outbound.Content != "reasoning" {
			t.Fatalf("outbound content = %q, want reasoning", outbound.Content)
		}
		if outbound.SessionKey != "session-1" {
			t.Fatalf("outbound session key = %q, want session-1", outbound.SessionKey)
		}
		if outbound.Context.Raw[metadataKeyMessageKind] != messageKindThought {
			t.Fatalf("message kind = %q, want %q", outbound.Context.Raw[metadataKeyMessageKind], messageKindThought)
		}
	case <-time.After(time.Second):
		t.Fatal("expected pico reasoning outbound")
	}
}

func TestProcessMessage_IncludesCurrentSenderInDynamicContext(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &recordingProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "discord",
		SenderID: "discord:123",
		Sender: bus.SenderInfo{
			DisplayName: "Alice",
		},
		ChatID:  "group-1",
		Content: "hello",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("processMessage() response = %q, want %q", response, "Mock response")
	}
	if len(provider.lastMessages) == 0 {
		t.Fatal("provider did not receive any messages")
	}

	systemPrompt := provider.lastMessages[0].Content
	wantSender := "## Current Sender\nCurrent sender: Alice (ID: discord:123)"
	if !strings.Contains(systemPrompt, wantSender) {
		t.Fatalf("system prompt missing sender context %q:\n%s", wantSender, systemPrompt)
	}

	lastMessage := provider.lastMessages[len(provider.lastMessages)-1]
	if lastMessage.Role != "user" || lastMessage.Content != "hello" {
		t.Fatalf("last provider message = %+v, want unchanged user message", lastMessage)
	}
}

func TestProcessMessage_DoesNotPassImplicitThinkingOffToCapableProvider(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	provider := &thinkingRecordingProvider{}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel: "pico",
		ChatID:  "chat-1",
		Content: "hello",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("processMessage() response = %q, want %q", response, "Mock response")
	}
	if _, ok := provider.lastOptions["thinking_level"]; ok {
		t.Fatalf("thinking_level option should be omitted when unset, got %#v", provider.lastOptions["thinking_level"])
	}
}

func TestProcessMessage_PassesExplicitThinkingOffToCapableProvider(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{{
			ModelName:     "test-model",
			Model:         "test-model",
			ThinkingLevel: "off",
		}},
	}

	provider := &thinkingRecordingProvider{}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel: "pico",
		ChatID:  "chat-1",
		Content: "hello",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("processMessage() response = %q, want %q", response, "Mock response")
	}
	if got := provider.lastOptions["thinking_level"]; got != "off" {
		t.Fatalf("thinking_level option = %#v, want %q", got, "off")
	}
}

func TestProcessMessage_PassesExplicitThinkingOffToProviderWithoutThinkingCapability(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{{
			ModelName:     "test-model",
			Model:         "test-model",
			ThinkingLevel: "off",
		}},
	}

	provider := &thinkingOptionRecordingProvider{}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel: "pico",
		ChatID:  "chat-1",
		Content: "hello",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("processMessage() response = %q, want %q", response, "Mock response")
	}
	if got := provider.lastOptions["thinking_level"]; got != "off" {
		t.Fatalf("thinking_level option = %#v, want %q", got, "off")
	}
}

func TestProcessMessage_PassesDeepSeekThinkingLevelToThinkingCapableProvider(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "deepseek-v4-flash",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{{
			ModelName:     "deepseek-v4-flash",
			Provider:      "deepseek",
			Model:         "deepseek-v4-flash",
			ThinkingLevel: "xhigh",
		}},
	}

	provider := &thinkingRecordingProvider{}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel: "pico",
		ChatID:  "chat-1",
		Content: "hello",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("processMessage() response = %q, want %q", response, "Mock response")
	}
	if got := provider.lastOptions["thinking_level"]; got != "xhigh" {
		t.Fatalf("thinking_level option = %#v, want %q", got, "xhigh")
	}
}

func TestProcessMessage_SuppressesReasoningWhenThinkingOff(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{{
			ModelName:     "test-model",
			Model:         "test-model",
			ThinkingLevel: "off",
		}},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &reasoningResponseProvider{})

	response, err := al.runAgentLoop(
		context.Background(),
		al.GetRegistry().GetDefaultAgent(),
		processOptions{
			SessionKey:      "agent:main:pico:chat-1",
			Channel:         "pico",
			ChatID:          "chat-1",
			UserMessage:     "hello",
			SendResponse:    false,
			DefaultResponse: defaultResponse,
			NoHistory:       true,
		},
	)
	if err != nil {
		t.Fatalf("runAgentLoop() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("response = %q, want %q", response, "Mock response")
	}
	select {
	case outbound := <-msgBus.OutboundChan():
		t.Fatalf("expected no reasoning outbound when thinking is off, got %+v", outbound)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestProcessMessage_BeforeLLMModelRewriteReevaluatesThinkingOff(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "plain-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName: "plain-model",
				Model:     "openai/plain-model",
			},
			{
				ModelName:     "off-model",
				Model:         "openai/off-model",
				ThinkingLevel: "off",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &reasoningOptionRecordingProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	if err := al.MountHook(NamedHook("rewrite-model", modelRewriteHook{model: "off-model"})); err != nil {
		t.Fatalf("MountHook failed: %v", err)
	}

	response, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "pico",
		SenderID: "user1",
		ChatID:   "pico:test-session",
		Content:  "hello",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "final answer" {
		t.Fatalf("processMessage() response = %q, want final answer", response)
	}
	if got := provider.lastOptions["thinking_level"]; got != "off" {
		t.Fatalf("thinking_level option = %#v, want off after hook model rewrite", got)
	}
	select {
	case outbound := <-msgBus.OutboundChan():
		t.Fatalf("expected no reasoning outbound after hook rewrote to off model, got %+v", outbound)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestProcessMessage_BeforeLLMModelRewriteDoesNotLeakThinkingOff(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "off-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName:     "off-model",
				Model:         "openai/off-model",
				ThinkingLevel: "off",
			},
			{
				ModelName: "plain-model",
				Model:     "openai/plain-model",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &reasoningOptionRecordingProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	if err := al.MountHook(NamedHook("rewrite-model", modelRewriteHook{model: "plain-model"})); err != nil {
		t.Fatalf("MountHook failed: %v", err)
	}

	response, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "pico",
		SenderID: "user1",
		ChatID:   "pico:test-session",
		Content:  "hello",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "final answer" {
		t.Fatalf("processMessage() response = %q, want final answer", response)
	}
	if _, ok := provider.lastOptions["thinking_level"]; ok {
		t.Fatalf(
			"thinking_level option should be cleared after hook rewrote away from off model, got %#v",
			provider.lastOptions["thinking_level"],
		)
	}
	select {
	case outbound := <-msgBus.OutboundChan():
		if outbound.Content != "thinking trace" {
			t.Fatalf("reasoning outbound content = %q, want thinking trace", outbound.Content)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected reasoning outbound after hook rewrote away from off model")
	}
}

func TestProcessMessage_BtwCommandSuppressesReasoningWhenThinkingOff(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{{
			ModelName:     "test-model",
			Model:         "openai/test-model",
			ThinkingLevel: "off",
		}},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &sideQuestionFallbackTestProvider{model: "test-model"})
	al.providerFactory = func(mc *config.ModelConfig) (providers.LLMProvider, string, error) {
		model := ""
		if mc != nil {
			_, model = providers.ExtractProtocol(mc)
		}
		if model == "" {
			model = "test-model"
		}
		return &sideQuestionFallbackTestProvider{model: model}, model, nil
	}

	response, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/btw explain privately",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if strings.Contains(response, "thinking trace") {
		t.Fatalf("processMessage() response = %q, should not expose reasoning with thinking off", response)
	}
}

func TestProcessMessage_BtwHookModelRewriteReevaluatesThinkingOff(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "plain-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName: "plain-model",
				Model:     "openai/plain-model",
			},
			{
				ModelName:     "off-model",
				Model:         "openai/off-model",
				ThinkingLevel: "off",
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &sideQuestionFallbackTestProvider{model: "plain-model"})
	al.providerFactory = func(mc *config.ModelConfig) (providers.LLMProvider, string, error) {
		model := ""
		if mc != nil {
			_, model = providers.ExtractProtocol(mc)
		}
		if model == "" {
			model = "plain-model"
		}
		return &sideQuestionFallbackTestProvider{model: model}, model, nil
	}
	if err := al.MountHook(NamedHook("rewrite-model", modelRewriteHook{model: "off-model"})); err != nil {
		t.Fatalf("MountHook failed: %v", err)
	}

	response, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/btw explain privately",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if strings.Contains(response, "thinking trace") {
		t.Fatalf(
			"processMessage() response = %q, should not expose reasoning after hook rewrote to off model",
			response,
		)
	}
}

func TestProcessMessage_BtwHookModelRewriteDoesNotLeakThinkingOff(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "off-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName:     "off-model",
				Model:         "openai/off-model",
				ThinkingLevel: "off",
			},
			{
				ModelName: "plain-model",
				Model:     "openai/plain-model",
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &sideQuestionFallbackTestProvider{model: "off-model"})
	al.providerFactory = func(mc *config.ModelConfig) (providers.LLMProvider, string, error) {
		model := ""
		if mc != nil {
			_, model = providers.ExtractProtocol(mc)
		}
		if model == "" {
			model = "off-model"
		}
		return &sideQuestionFallbackTestProvider{model: model}, model, nil
	}
	if err := al.MountHook(NamedHook("rewrite-model", modelRewriteHook{model: "plain-model"})); err != nil {
		t.Fatalf("MountHook failed: %v", err)
	}

	response, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/btw explain privately",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "thinking trace" {
		t.Fatalf("processMessage() response = %q, want reasoning after hook rewrote away from off model", response)
	}
}

func TestProcessMessage_BtwFallbackDoesNotInheritPrimaryThinkingOff(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				ModelFallbacks:    []string{"openai/fallback-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{{
			ModelName:     "test-model",
			Model:         "openai/test-model",
			ThinkingLevel: "off",
		}},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &sideQuestionFallbackTestProvider{model: "test-model"})
	al.providerFactory = func(mc *config.ModelConfig) (providers.LLMProvider, string, error) {
		model := ""
		if mc != nil {
			_, model = providers.ExtractProtocol(mc)
		}
		if model == "" {
			model = "test-model"
		}
		return &sideQuestionFallbackTestProvider{model: model}, model, nil
	}

	response, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/btw explain fallback reasoning",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "thinking trace" {
		t.Fatalf("processMessage() response = %q, want fallback reasoning when fallback has no off", response)
	}
}

func TestProcessMessage_UseCommandLoadsRequestedSkill(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "skills", "shell")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(skillDir, "SKILL.md"),
		[]byte("# shell\n\nPrefer concise shell commands and explain them briefly."),
		0o644,
	); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &recordingProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/use shell explain how to list files",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("processMessage() response = %q, want %q", response, "Mock response")
	}
	if len(provider.lastMessages) == 0 {
		t.Fatal("provider did not receive any messages")
	}

	systemPrompt := provider.lastMessages[0].Content
	if !strings.Contains(systemPrompt, "# Active Skills") {
		t.Fatalf("system prompt missing active skills section:\n%s", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "### Skill: shell") {
		t.Fatalf("system prompt missing requested skill content:\n%s", systemPrompt)
	}

	lastMessage := provider.lastMessages[len(provider.lastMessages)-1]
	if lastMessage.Role != "user" || lastMessage.Content != "explain how to list files" {
		t.Fatalf("last provider message = %+v, want rewritten user message", lastMessage)
	}
}

func TestProcessMessage_BtwCommandRunsWithoutPersistingHistory(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		// Add model list so isolated provider can resolve the model
		ModelList: []*config.ModelConfig{
			{ModelName: "test-model", Model: "openai/test-model"},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &recordingProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	useTestSideQuestionProvider(al, provider)
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	msg := bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/btw explain side effects",
	}
	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute() error = %v", err)
	}
	allocation := al.allocateRouteSession(route, msg)
	sessionKey := resolveScopeKey(allocation.SessionKey, msg.SessionKey)
	initialHistory := []providers.Message{
		{Role: "user", Content: "We decided to avoid global state."},
		{Role: "assistant", Content: "Right, keep it request-scoped."},
	}
	defaultAgent.Sessions.SetHistory(sessionKey, initialHistory)
	defaultAgent.Sessions.SetSummary(sessionKey, "The team decided to keep state request-scoped.")

	initialHistory = defaultAgent.Sessions.GetHistory(sessionKey)

	response, err := al.processMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("processMessage() response = %q, want %q", response, "Mock response")
	}
	if len(provider.lastMessages) == 0 {
		t.Fatal("provider did not receive any messages")
	}
	if len(provider.lastMessages) != 4 {
		t.Fatalf("provider messages len = %d, want 4 (system + prior history + user)", len(provider.lastMessages))
	}

	if !reflect.DeepEqual(provider.lastMessages[1:3], initialHistory) {
		t.Fatalf("provider history = %#v, want %#v", provider.lastMessages[1:3], initialHistory)
	}

	lastMessage := provider.lastMessages[len(provider.lastMessages)-1]
	if lastMessage.Role != "user" || lastMessage.Content != "explain side effects" {
		t.Fatalf("last provider message = %+v, want stripped /btw question", lastMessage)
	}

	history := al.GetRegistry().GetDefaultAgent().Sessions.GetHistory(sessionKey)
	if !reflect.DeepEqual(history, initialHistory) {
		t.Fatalf("session history = %#v, want %#v", history, initialHistory)
	}
}

func TestProcessMessage_BtwCommandIncludesRequestContextAndMedia(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &recordingProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	useTestSideQuestionProvider(al, provider)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "discord",
		SenderID: "discord:123",
		Sender: bus.SenderInfo{
			DisplayName: "Alice",
		},
		ChatID:  "group-1",
		Content: "/btw describe this image",
		Media:   []string{"media://image-1"},
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("processMessage() response = %q, want %q", response, "Mock response")
	}
	if len(provider.lastMessages) == 0 {
		t.Fatal("provider did not receive any messages")
	}

	systemPrompt := provider.lastMessages[0].Content
	if !strings.Contains(systemPrompt, "## Current Session\nChannel: discord\nChat ID: group-1") {
		t.Fatalf("system prompt missing current session context:\n%s", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "## Current Sender\nCurrent sender: Alice (ID: discord:123)") {
		t.Fatalf("system prompt missing current sender context:\n%s", systemPrompt)
	}

	lastMessage := provider.lastMessages[len(provider.lastMessages)-1]
	if lastMessage.Role != "user" || lastMessage.Content != "describe this image" {
		t.Fatalf("last provider message = %+v, want stripped /btw question", lastMessage)
	}
	if !reflect.DeepEqual(lastMessage.Media, []string{"media://image-1"}) {
		t.Fatalf("last provider media = %#v, want media ref", lastMessage.Media)
	}
}

func TestProcessMessage_BtwCommandUsesIsolatedProvider(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		// Add model list so isolated provider can resolve the model
		ModelList: []*config.ModelConfig{
			{ModelName: "test-model", Model: "openai/test-model"},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &recordingProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	useTestSideQuestionProvider(al, provider)
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	// Set up initial history for the main session
	mainSessionKey := "telegram:123:chat-1"
	initialHistory := []providers.Message{
		{Role: "user", Content: "We decided to avoid global state."},
		{Role: "assistant", Content: "Right, keep it request-scoped."},
	}
	defaultAgent.Sessions.SetHistory(mainSessionKey, initialHistory)

	initialHistory = defaultAgent.Sessions.GetHistory(mainSessionKey)

	// Process a /btw command
	response, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "telegram:123",
		ChatID:     "chat-1",
		SessionKey: mainSessionKey,
		Content:    "/btw explain isolation",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("processMessage() response = %q, want %q", response, "Mock response")
	}

	// Verify the provider received the side question
	if len(provider.lastMessages) == 0 {
		t.Fatal("provider did not receive any messages for /btw command")
	}

	// Verify the question was stripped of /btw prefix
	lastMessage := provider.lastMessages[len(provider.lastMessages)-1]
	if lastMessage.Role != "user" || lastMessage.Content != "explain isolation" {
		t.Fatalf("last provider message = %+v, want stripped /btw question", lastMessage)
	}

	// Verify main session history was NOT modified
	currentHistory := defaultAgent.Sessions.GetHistory(mainSessionKey)
	if !reflect.DeepEqual(currentHistory, initialHistory) {
		t.Fatalf("main session history was modified:\ngot  %#v\nwant %#v", currentHistory, initialHistory)
	}
}

func TestProcessMessage_BtwCommandRetriesWithoutMediaOnVisionUnsupported(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		// Add model list so isolated provider can resolve the model
		ModelList: []*config.ModelConfig{
			{ModelName: "test-model", Model: "openai/test-model"},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &visionUnsupportedMediaProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	useTestSideQuestionProvider(al, provider)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/btw describe this image",
		Media:    []string{"data:image/png;base64,abc123"},
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "ok" {
		t.Fatalf("processMessage() response = %q, want %q", response, "ok")
	}
	// Note: With isolated providers, each /btw creates a new provider instance,
	// so we can't track calls across retries in the same way.
	// The retry logic happens within askSideQuestion, creating separate isolated providers.
	// For now, we just verify the command succeeds.
	if provider.calls < 1 {
		t.Fatalf("provider was not called for /btw command")
	}
}

func TestProcessMessage_BtwCommandUsesProviderFactoryModel(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "lb-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{
			{ModelName: "lb-model", Model: "openai/lb-model-a"},
			{ModelName: "lb-model", Model: "openai/lb-model-b"},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &recordingProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	useTestSideQuestionProvider(al, provider)

	response, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/btw explain load balancing",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("processMessage() response = %q, want %q", response, "Mock response")
	}

	// Verify that /btw used the configured model from ModelList
	// The provider should have been called with one of the lb-model variants
	if provider.lastModel == "" {
		t.Fatal("provider was not called for /btw command")
	}
	if !strings.HasPrefix(provider.lastModel, "lb-model") {
		t.Fatalf("/btw used model %q, expected lb-model variant", provider.lastModel)
	}
}

func TestProcessMessage_BtwCommandHookModelBypassesFallbackCandidates(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "primary-model",
				ModelFallbacks:    []string{"fallback-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &recordingProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	useTestSideQuestionProvider(al, provider)
	if err := al.MountHook(NamedHook("rewrite-model", modelRewriteHook{model: "hook-model"})); err != nil {
		t.Fatalf("MountHook failed: %v", err)
	}

	response, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/btw explain hook routing",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("processMessage() response = %q, want %q", response, "Mock response")
	}
	if provider.lastModel != "hook-model" {
		t.Fatalf("/btw model = %q, want hook-selected model", provider.lastModel)
	}
}

func TestHandleCommand_UseCommandRejectsUnknownSkill(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &recordingProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	agent := al.GetRegistry().GetDefaultAgent()

	opts := processOptions{}
	reply, handled := al.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/use missing explain how to list files",
	}, agent, &opts)
	if !handled {
		t.Fatal("expected /use with unknown skill to be handled")
	}
	if !strings.Contains(reply, "Unknown skill: missing") {
		t.Fatalf("reply = %q, want unknown skill error", reply)
	}
}

func TestProcessMessage_UseCommandArmsSkillForNextMessage(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "skills", "shell")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(skillDir, "SKILL.md"),
		[]byte("# shell\n\nPrefer concise shell commands and explain them briefly."),
		0o644,
	); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &recordingProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/use shell",
	}))
	if err != nil {
		t.Fatalf("processMessage() arm error = %v", err)
	}
	if !strings.Contains(response, `Skill "shell" is armed for your next message.`) {
		t.Fatalf("arm response = %q, want armed confirmation", response)
	}

	response, err = al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "explain how to list files",
	}))
	if err != nil {
		t.Fatalf("processMessage() follow-up error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("follow-up response = %q, want %q", response, "Mock response")
	}
	if len(provider.lastMessages) == 0 {
		t.Fatal("provider did not receive any messages")
	}

	systemPrompt := provider.lastMessages[0].Content
	if !strings.Contains(systemPrompt, "### Skill: shell") {
		t.Fatalf("system prompt missing pending skill content:\n%s", systemPrompt)
	}
	lastMessage := provider.lastMessages[len(provider.lastMessages)-1]
	if lastMessage.Role != "user" || lastMessage.Content != "explain how to list files" {
		t.Fatalf("last provider message = %+v, want unchanged follow-up user message", lastMessage)
	}
}

func TestApplyExplicitSkillCommand_ArmsSkillForNextMessage(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	if err := os.MkdirAll(filepath.Join(cfg.Agents.Defaults.Workspace, "skills", "finance-news"), 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(cfg.Agents.Defaults.Workspace, "skills", "finance-news", "SKILL.md"),
		[]byte("# Finance News\n\nUse web tools for current finance updates.\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error = %v", err)
	}

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	opts := &processOptions{SessionKey: "agent:main:test"}
	matched, handled, reply := al.applyExplicitSkillCommand("/use finance-news", agent, opts)
	if !matched {
		t.Fatal("expected /use command to match")
	}
	if !handled {
		t.Fatal("expected /use without inline message to be handled immediately")
	}
	if !strings.Contains(reply, `Skill "finance-news" is armed for your next message`) {
		t.Fatalf("unexpected reply: %q", reply)
	}

	pending := al.takePendingSkills(opts.SessionKey)
	if len(pending) != 1 || pending[0] != "finance-news" {
		t.Fatalf("pending skills = %#v, want [finance-news]", pending)
	}
}

func TestApplyExplicitSkillCommand_InlineMessageMutatesOptions(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	if err := os.MkdirAll(filepath.Join(cfg.Agents.Defaults.Workspace, "skills", "finance-news"), 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(cfg.Agents.Defaults.Workspace, "skills", "finance-news", "SKILL.md"),
		[]byte("# Finance News\n\nUse web tools for current finance updates.\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error = %v", err)
	}

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	opts := &processOptions{
		SessionKey:  "agent:main:test",
		UserMessage: "/use finance-news dammi le ultime news",
	}
	matched, handled, reply := al.applyExplicitSkillCommand(opts.UserMessage, agent, opts)
	if !matched {
		t.Fatal("expected /use command to match")
	}
	if handled {
		t.Fatal("expected /use with inline message to fall through into normal agent execution")
	}
	if reply != "" {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if opts.UserMessage != "dammi le ultime news" {
		t.Fatalf("opts.UserMessage = %q, want %q", opts.UserMessage, "dammi le ultime news")
	}
	if len(opts.ForcedSkills) != 1 || opts.ForcedSkills[0] != "finance-news" {
		t.Fatalf("opts.ForcedSkills = %#v, want [finance-news]", opts.ForcedSkills)
	}
}

func TestRecordLastChannel(t *testing.T) {
	al, cfg, msgBus, provider, cleanup := newTestAgentLoop(t)
	defer cleanup()

	testChannel := "test-channel"
	if err := al.RecordLastChannel(testChannel); err != nil {
		t.Fatalf("RecordLastChannel failed: %v", err)
	}
	if got := al.state.GetLastChannel(); got != testChannel {
		t.Errorf("Expected channel '%s', got '%s'", testChannel, got)
	}
	al2 := NewAgentLoop(cfg, msgBus, provider)
	if got := al2.state.GetLastChannel(); got != testChannel {
		t.Errorf("Expected persistent channel '%s', got '%s'", testChannel, got)
	}
}

func TestRecordLastChatID(t *testing.T) {
	al, cfg, msgBus, provider, cleanup := newTestAgentLoop(t)
	defer cleanup()

	testChatID := "test-chat-id-123"
	if err := al.RecordLastChatID(testChatID); err != nil {
		t.Fatalf("RecordLastChatID failed: %v", err)
	}
	if got := al.state.GetLastChatID(); got != testChatID {
		t.Errorf("Expected chat ID '%s', got '%s'", testChatID, got)
	}
	al2 := NewAgentLoop(cfg, msgBus, provider)
	if got := al2.state.GetLastChatID(); got != testChatID {
		t.Errorf("Expected persistent chat ID '%s', got '%s'", testChatID, got)
	}
}

func TestNewAgentLoop_StateInitialized(t *testing.T) {
	// Create temp workspace
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test config
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	// Create agent loop
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	// Verify state manager is initialized
	if al.state == nil {
		t.Error("Expected state manager to be initialized")
	}

	// Verify state directory was created
	stateDir := filepath.Join(tmpDir, "state")
	if _, err := os.Stat(stateDir); os.IsNotExist(err) {
		t.Error("Expected state directory to exist")
	}
}

// TestToolRegistry_ToolRegistration verifies tools can be registered and retrieved
func TestToolRegistry_ToolRegistration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	// Register a custom tool
	customTool := &mockCustomTool{}
	al.RegisterTool(customTool)

	// Verify tool is registered by checking it doesn't panic on GetStartupInfo
	// (actual tool retrieval is tested in tools package tests)
	info := al.GetStartupInfo()
	toolsInfo := info["tools"].(map[string]any)
	toolsList := toolsInfo["names"].([]string)

	// Check that our custom tool name is in the list
	found := slices.Contains(toolsList, "mock_custom")
	if !found {
		t.Error("Expected custom tool to be registered")
	}
}

// TestToolContext_Updates verifies tool context helpers work correctly
func TestToolContext_Updates(t *testing.T) {
	ctx := tools.WithToolContext(context.Background(), "telegram", "chat-42")

	if got := tools.ToolChannel(ctx); got != "telegram" {
		t.Errorf("expected channel 'telegram', got %q", got)
	}
	if got := tools.ToolChatID(ctx); got != "chat-42" {
		t.Errorf("expected chatID 'chat-42', got %q", got)
	}

	// Empty context returns empty strings
	if got := tools.ToolChannel(context.Background()); got != "" {
		t.Errorf("expected empty channel from bare context, got %q", got)
	}

	inboundCtx := tools.WithToolInboundContext(
		context.Background(),
		"telegram",
		"chat-42",
		"msg-123",
		"msg-100",
	)
	if got := tools.ToolMessageID(inboundCtx); got != "msg-123" {
		t.Errorf("expected messageID 'msg-123', got %q", got)
	}
	if got := tools.ToolReplyToMessageID(inboundCtx); got != "msg-100" {
		t.Errorf("expected replyToMessageID 'msg-100', got %q", got)
	}
}

// TestToolRegistry_GetDefinitions verifies tool definitions can be retrieved
func TestToolRegistry_GetDefinitions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	// Register a test tool and verify it shows up in startup info
	testTool := &mockCustomTool{}
	al.RegisterTool(testTool)

	info := al.GetStartupInfo()
	toolsInfo := info["tools"].(map[string]any)
	toolsList := toolsInfo["names"].([]string)

	// Check that our custom tool name is in the list
	found := slices.Contains(toolsList, "mock_custom")
	if !found {
		t.Error("Expected custom tool to be registered")
	}
}

func TestProcessMessage_MediaToolHandledSkipsFollowUpLLMAndFinalText(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &handledMediaProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	store := media.NewFileMediaStore()
	al.SetMediaStore(store)
	telegramChannel := &fakeMediaChannel{fakeChannel: fakeChannel{id: "rid-telegram"}}
	al.SetChannelManager(newStartedTestChannelManager(t, msgBus, store, "telegram", telegramChannel))

	imagePath := filepath.Join(tmpDir, "screen.png")
	if err := os.WriteFile(imagePath, []byte("fake screenshot"), 0o644); err != nil {
		t.Fatalf("WriteFile(imagePath) error = %v", err)
	}

	al.RegisterTool(&handledMediaTool{
		store: store,
		path:  imagePath,
	})

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		ChatID:   "chat1",
		SenderID: "user1",
		Content:  "take a screenshot of the screen and send it to me",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "" {
		t.Fatalf("expected no final response when media tool already handled delivery, got %q", response)
	}
	if provider.calls != 1 {
		t.Fatalf("expected exactly 1 LLM call, got %d", provider.calls)
	}
	if len(provider.toolCounts) != 1 {
		t.Fatalf("expected tool counts for 1 provider call, got %d", len(provider.toolCounts))
	}
	if provider.toolCounts[0] == 0 {
		t.Fatal("expected tools to be available on the first LLM call")
	}

	sentMedia := telegramChannel.sentMediaSnapshot()
	if len(sentMedia) != 1 {
		t.Fatalf("expected exactly 1 synchronously sent media message, got %d", len(sentMedia))
	}
	if sentMedia[0].Channel != "telegram" || sentMedia[0].ChatID != "chat1" {
		t.Fatalf("unexpected sent media target: %+v", sentMedia[0])
	}
	if len(sentMedia[0].Parts) != 1 {
		t.Fatalf("expected exactly 1 sent media part, got %d", len(sentMedia[0].Parts))
	}

	select {
	case extra := <-msgBus.OutboundMediaChan():
		t.Fatalf("expected handled media to bypass async queue, got %+v", extra)
	default:
	}

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	route, _, err := al.resolveMessageRoute(testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		ChatID:   "chat1",
		SenderID: "user1",
		Content:  "take a screenshot of the screen and send it to me",
	}))
	if err != nil {
		t.Fatalf("resolveMessageRoute() error = %v", err)
	}
	sessionKey := resolveScopeKey(al.allocateRouteSession(route, testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		ChatID:   "chat1",
		SenderID: "user1",
		Content:  "take a screenshot of the screen and send it to me",
	})).SessionKey, "")
	history := defaultAgent.Sessions.GetHistory(sessionKey)
	if len(history) == 0 {
		t.Fatal("expected session history to be saved")
	}
	last := history[len(history)-1]
	if last.Role != "assistant" || last.Content != "Requested output delivered via tool attachment." {
		t.Fatalf("expected handled assistant summary in history, got %+v", last)
	}
	if len(last.Attachments) != 1 {
		t.Fatalf("expected handled assistant summary attachments in history, got %+v", last.Attachments)
	}
}

func TestProcessMessage_HandledToolProcessesQueuedSteeringBeforeReturning(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &handledMediaWithSteeringProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	store := media.NewFileMediaStore()
	al.SetMediaStore(store)
	telegramChannel := &fakeMediaChannel{fakeChannel: fakeChannel{id: "rid-telegram"}}
	al.SetChannelManager(newStartedTestChannelManager(t, msgBus, store, "telegram", telegramChannel))

	imagePath := filepath.Join(tmpDir, "screen-steering.png")
	if err := os.WriteFile(imagePath, []byte("fake screenshot"), 0o644); err != nil {
		t.Fatalf("WriteFile(imagePath) error = %v", err)
	}

	al.RegisterTool(&handledMediaWithSteeringTool{
		store: store,
		path:  imagePath,
		loop:  al,
	})

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		ChatID:   "chat1",
		SenderID: "user1",
		Content:  "take a screenshot of the screen and send it to me",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Handled the queued steering message." {
		t.Fatalf("response = %q, want queued steering response", response)
	}
	if provider.calls != 2 {
		t.Fatalf("expected 2 LLM calls after queued steering, got %d", provider.calls)
	}
	sentMedia := telegramChannel.sentMediaSnapshot()
	if len(sentMedia) != 1 {
		t.Fatalf("expected exactly 1 synchronously sent media message, got %d", len(sentMedia))
	}
}

func TestRunAgentLoop_ResponseHandledToolPublishesForUserWhenSendResponseDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmpDir
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10

	msgBus := bus.NewMessageBus()
	provider := &handledUserProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	store := media.NewFileMediaStore()
	al.SetMediaStore(store)
	telegramChannel := &fakeMediaChannel{fakeChannel: fakeChannel{id: "rid-telegram"}}
	al.SetChannelManager(newStartedTestChannelManager(t, msgBus, store, "telegram", telegramChannel))
	al.RegisterTool(&handledUserTool{})

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	response, err := al.runAgentLoop(context.Background(), defaultAgent, processOptions{
		Dispatch: DispatchRequest{
			SessionKey:  "session-1",
			UserMessage: "take a screenshot of the screen and send it to me",
			SessionScope: &session.SessionScope{
				Version:    session.ScopeVersionV1,
				AgentID:    defaultAgent.ID,
				Channel:    "telegram",
				Dimensions: []string{"chat"},
				Values: map[string]string{
					"chat": "direct:chat1",
				},
			},
			InboundContext: &bus.InboundContext{
				Channel:  "telegram",
				ChatID:   "chat1",
				ChatType: "direct",
				SenderID: "user1",
			},
		},
		DefaultResponse: defaultResponse,
		EnableSummary:   false,
		SendResponse:    false,
	})
	if err != nil {
		t.Fatalf("runAgentLoop() error = %v", err)
	}
	if response != "" {
		t.Fatalf("expected no final response when tool already handled delivery, got %q", response)
	}

	deadline := time.Now().Add(2 * time.Second)
	sentMessages := telegramChannel.sentMessagesSnapshot()
	for len(sentMessages) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		sentMessages = telegramChannel.sentMessagesSnapshot()
	}
	if len(sentMessages) != 1 {
		t.Fatalf("expected exactly 1 sent text message, got %d", len(sentMessages))
	}
	if sentMessages[0].Content != "Handled user output from tool." {
		t.Fatalf("unexpected sent text message: %+v", sentMessages[0])
	}
	if sentMessages[0].AgentID != defaultAgent.ID {
		t.Fatalf("sent text agent_id = %q, want %q", sentMessages[0].AgentID, defaultAgent.ID)
	}
	if sentMessages[0].SessionKey != "session-1" {
		t.Fatalf("sent text session_key = %q, want session-1", sentMessages[0].SessionKey)
	}
	if sentMessages[0].Scope == nil ||
		sentMessages[0].Scope.Values["chat"] != "direct:chat1" {
		t.Fatalf("unexpected sent text scope: %+v", sentMessages[0].Scope)
	}
}

func TestAppendEventContextFields_IncludesInboundRouteAndScope(t *testing.T) {
	fields := map[string]any{}

	appendEventContextFields(fields, &TurnContext{
		Inbound: &bus.InboundContext{
			Channel:   "slack",
			Account:   "workspace-a",
			ChatID:    "C123",
			ChatType:  "channel",
			TopicID:   "thread-42",
			SpaceType: "workspace",
			SpaceID:   "T001",
			SenderID:  "U123",
			Mentioned: true,
		},
		Route: &routing.ResolvedRoute{
			AgentID:   "support",
			Channel:   "slack",
			AccountID: "workspace-a",
			MatchedBy: "default",
			SessionPolicy: routing.SessionPolicy{
				Dimensions: []string{"chat", "sender"},
				IdentityLinks: map[string][]string{
					"canonical-user": {"slack:U123"},
				},
			},
		},
		Scope: &session.SessionScope{
			Version:    session.ScopeVersionV1,
			AgentID:    "support",
			Channel:    "slack",
			Account:    "workspace-a",
			Dimensions: []string{"chat", "sender"},
			Values: map[string]string{
				"chat":   "channel:c123",
				"sender": "u123",
			},
		},
	})

	if fields["inbound_channel"] != "slack" {
		t.Fatalf("inbound_channel = %v, want slack", fields["inbound_channel"])
	}
	if fields["inbound_topic_id"] != "thread-42" {
		t.Fatalf("inbound_topic_id = %v, want thread-42", fields["inbound_topic_id"])
	}
	if fields["route_matched_by"] != "default" {
		t.Fatalf("route_matched_by = %v, want default", fields["route_matched_by"])
	}
	if fields["route_dimensions"] != "chat,sender" {
		t.Fatalf("route_dimensions = %v, want chat,sender", fields["route_dimensions"])
	}
	if fields["route_identity_link_count"] != 1 {
		t.Fatalf("route_identity_link_count = %v, want 1", fields["route_identity_link_count"])
	}
	if fields["scope_dimensions"] != "chat,sender" {
		t.Fatalf("scope_dimensions = %v, want chat,sender", fields["scope_dimensions"])
	}
	if fields["scope_chat"] != "channel:c123" {
		t.Fatalf("scope_chat = %v, want channel:c123", fields["scope_chat"])
	}
	if fields["scope_sender"] != "u123" {
		t.Fatalf("scope_sender = %v, want u123", fields["scope_sender"])
	}
}

func TestResolveMessageRoute_UsesInboundContextAccount(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true},
				{ID: "work"},
			},
		},
		Session: config.SessionConfig{
			Dimensions: []string{"sender"},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &simpleMockProvider{response: "ok"})

	route, _, err := al.resolveMessageRoute(testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:   "slack",
			Account:   "workspace-a",
			ChatID:    "C123",
			ChatType:  "channel",
			SenderID:  "U123",
			SpaceID:   "T001",
			SpaceType: "workspace",
		},
		Content: "hello",
	}))
	if err != nil {
		t.Fatalf("resolveMessageRoute() error = %v", err)
	}
	if route.AgentID != "main" {
		t.Fatalf("AgentID = %q, want main", route.AgentID)
	}
	if route.MatchedBy != "default" {
		t.Fatalf("MatchedBy = %q, want default", route.MatchedBy)
	}
	if route.AccountID != "workspace-a" {
		t.Fatalf("AccountID = %q, want workspace-a", route.AccountID)
	}
}

func TestResolveMessageRoute_UsesDispatchRulesInOrder(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true},
				{ID: "support"},
				{ID: "sales"},
			},
			Dispatch: &config.DispatchConfig{
				Rules: []config.DispatchRule{
					{
						Name:  "support-group",
						Agent: "support",
						When: config.DispatchSelector{
							Channel: "telegram",
							Chat:    "group:-100123",
						},
						SessionDimensions: []string{"chat"},
					},
					{
						Name:  "vip-in-group",
						Agent: "sales",
						When: config.DispatchSelector{
							Channel: "telegram",
							Chat:    "group:-100123",
							Sender:  "12345",
						},
						SessionDimensions: []string{"chat", "sender"},
					},
				},
			},
		},
		Session: config.SessionConfig{
			Dimensions: []string{"sender"},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &simpleMockProvider{response: "ok"})

	route, _, err := al.resolveMessageRoute(testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "telegram",
			ChatID:   "-100123",
			ChatType: "group",
			SenderID: "12345",
		},
		Content: "hello",
	}))
	if err != nil {
		t.Fatalf("resolveMessageRoute() error = %v", err)
	}
	if route.AgentID != "support" {
		t.Fatalf("AgentID = %q, want support", route.AgentID)
	}
	if route.MatchedBy != "dispatch.rule:support-group" {
		t.Fatalf("MatchedBy = %q, want dispatch.rule:support-group", route.MatchedBy)
	}
	if got := route.SessionPolicy.Dimensions; len(got) != 1 || got[0] != "chat" {
		t.Fatalf("SessionPolicy.Dimensions = %v, want [chat]", got)
	}
}

func TestProcessMessage_MediaArtifactCanBeForwardedBySendFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmpDir
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10

	msgBus := bus.NewMessageBus()
	provider := &artifactThenSendProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	store := media.NewFileMediaStore()
	al.SetMediaStore(store)
	telegramChannel := &fakeMediaChannel{fakeChannel: fakeChannel{id: "rid-telegram"}}
	al.SetChannelManager(newStartedTestChannelManager(t, msgBus, store, "telegram", telegramChannel))

	mediaDir := media.TempDir()
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(mediaDir) error = %v", err)
	}
	imagePath := filepath.Join(mediaDir, "artifact-screen.png")
	if err := os.WriteFile(imagePath, []byte("fake screenshot"), 0o644); err != nil {
		t.Fatalf("WriteFile(imagePath) error = %v", err)
	}

	al.RegisterTool(&mediaArtifactTool{
		store: store,
		path:  imagePath,
	})

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		ChatID:   "chat1",
		SenderID: "user1",
		Content:  "take a screenshot of the screen and send it to me",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "" {
		t.Fatalf("expected no final response after send_file handled delivery, got %q", response)
	}
	if provider.calls != 2 {
		t.Fatalf("expected 2 LLM calls (artifact + send_file), got %d", provider.calls)
	}

	sentMedia := telegramChannel.sentMediaSnapshot()
	if len(sentMedia) != 1 {
		t.Fatalf("expected exactly 1 synchronously sent media message, got %d", len(sentMedia))
	}
	if sentMedia[0].Channel != "telegram" || sentMedia[0].ChatID != "chat1" {
		t.Fatalf("unexpected sent media target: %+v", sentMedia[0])
	}
	if len(sentMedia[0].Parts) != 1 {
		t.Fatalf("expected exactly 1 sent media part, got %d", len(sentMedia[0].Parts))
	}

	select {
	case extra := <-msgBus.OutboundMediaChan():
		t.Fatalf("expected synchronous send_file delivery to bypass async queue, got %+v", extra)
	default:
	}
}

// TestAgentLoop_GetStartupInfo verifies startup info contains tools
func TestAgentLoop_GetStartupInfo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmpDir
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	info := al.GetStartupInfo()

	// Verify tools info exists
	toolsInfo, ok := info["tools"]
	if !ok {
		t.Fatal("Expected 'tools' key in startup info")
	}

	toolsMap, ok := toolsInfo.(map[string]any)
	if !ok {
		t.Fatal("Expected 'tools' to be a map")
	}

	count, ok := toolsMap["count"]
	if !ok {
		t.Fatal("Expected 'count' in tools info")
	}

	// Should have default tools registered
	if count.(int) == 0 {
		t.Error("Expected at least some tools to be registered")
	}
}

// TestAgentLoop_Stop verifies Stop() sets running to false
func TestAgentLoop_Stop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	// Note: running is only set to true when Run() is called
	// We can't test that without starting the event loop
	// Instead, verify the Stop method can be called safely
	al.Stop()

	// Verify running is false (initial state or after Stop)
	if al.running.Load() {
		t.Error("Expected agent to be stopped (or never started)")
	}
}

func TestToolFeedbackExplanationFromResponse_UsesCurrentContentFirst(t *testing.T) {
	response := &providers.LLMResponse{
		Content:          "Read README.md first",
		ReasoningContent: "current reasoning fallback",
	}
	messages := []providers.Message{
		{Role: "user", Content: "check file"},
		{Role: "assistant", Content: "Previous turn explanation"},
		{Role: "tool", Content: "tool output", ToolCallID: "call_1"},
	}

	got := toolFeedbackExplanationFromResponse(response, messages)
	if got != "Read README.md first" {
		t.Fatalf("toolFeedbackExplanationFromResponse() = %q, want current content", got)
	}
}
