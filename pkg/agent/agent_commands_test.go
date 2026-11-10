package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

func TestSideQuestionResponseContent_FallsBackWhenContentIsWhitespace(t *testing.T) {
	response := &providers.LLMResponse{
		Content:          " \n\t ",
		ReasoningContent: "reasoning fallback",
	}

	if got := sideQuestionResponseContent(response); got != "reasoning fallback" {
		t.Fatalf("sideQuestionResponseContent() = %q, want %q", got, "reasoning fallback")
	}
}

func TestResponseReasoningContent_FallsBackWhenReasoningIsWhitespace(t *testing.T) {
	response := &providers.LLMResponse{
		Reasoning:        " \n\t ",
		ReasoningContent: "structured reasoning fallback",
	}

	if got := responseReasoningContent(response); got != "structured reasoning fallback" {
		t.Fatalf("responseReasoningContent() = %q, want %q", got, "structured reasoning fallback")
	}
}

func TestToolFeedbackExplanationFromResponse_UsesExplicitToolCallExtraContent(t *testing.T) {
	response := &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{
			ID:   "call_1",
			Name: "read_file",
			ExtraContent: &providers.ExtraContent{
				ToolFeedbackExplanation: "Read README.md first to confirm the current project structure.",
			},
		}},
	}
	messages := []providers.Message{
		{Role: "user", Content: "check file"},
		{Role: "assistant", Content: ""},
		{Role: "tool", Content: "tool output", ToolCallID: "call_1"},
	}

	got := toolFeedbackExplanationFromResponse(response, messages)
	if got != "Read README.md first to confirm the current project structure." {
		t.Fatalf("toolFeedbackExplanationFromResponse() = %q, want explicit tool feedback explanation", got)
	}
}

func TestToolFeedbackExplanationForToolCall_PrefersToolSpecificExtraContent(t *testing.T) {
	response := &providers.LLMResponse{
		Content: "Shared explanation",
		ToolCalls: []providers.ToolCall{
			{
				ID:   "call_1",
				Name: "read_file",
				ExtraContent: &providers.ExtraContent{
					ToolFeedbackExplanation: "Read README.md first.",
				},
			},
			{
				ID:   "call_2",
				Name: "edit_file",
				ExtraContent: &providers.ExtraContent{
					ToolFeedbackExplanation: "Update config example after reading it.",
				},
			},
		},
	}

	got1 := toolFeedbackExplanationForToolCall(response, response.ToolCalls[0], nil)
	got2 := toolFeedbackExplanationForToolCall(response, response.ToolCalls[1], nil)
	if got1 != "Read README.md first." {
		t.Fatalf("toolFeedbackExplanationForToolCall() first = %q, want tool-specific explanation", got1)
	}
	if got2 != "Update config example after reading it." {
		t.Fatalf("toolFeedbackExplanationForToolCall() second = %q, want tool-specific explanation", got2)
	}
}

func TestToolFeedbackExplanationForToolCall_DoesNotReuseAnotherToolCallExplanation(t *testing.T) {
	response := &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{
			{
				ID:   "call_1",
				Name: "read_file",
			},
			{
				ID:   "call_2",
				Name: "edit_file",
				ExtraContent: &providers.ExtraContent{
					ToolFeedbackExplanation: "Update config example after reading it.",
				},
			},
		},
	}
	messages := []providers.Message{
		{Role: "user", Content: "inspect the config and update the example"},
	}

	got := toolFeedbackExplanationForToolCall(response, response.ToolCalls[0], messages)
	want := utils.ToolFeedbackContinuationHint + ": inspect the config and update the example"
	if got != want {
		t.Fatalf("toolFeedbackExplanationForToolCall() = %q, want %q", got, want)
	}
}

func TestToolFeedbackExplanationFromResponse_DoesNotUseReasoningContent(t *testing.T) {
	response := &providers.LLMResponse{
		Content:          "",
		ReasoningContent: "hidden reasoning should not be shown",
	}
	messages := []providers.Message{
		{Role: "user", Content: "check file"},
		{Role: "assistant", Content: "Previous turn explanation"},
		{Role: "user", Content: "Inspect README.md and update the config example."},
		{Role: "tool", Content: "tool output", ToolCallID: "call_1"},
	}

	got := toolFeedbackExplanationFromResponse(response, messages)
	want := utils.ToolFeedbackContinuationHint + ": Inspect README.md and update the config example."
	if got != want {
		t.Fatalf("toolFeedbackExplanationFromResponse() = %q, want latest user content fallback", got)
	}
}

func TestToolFeedbackExplanationForToolCall_DoesNotTruncateLongExplanation(t *testing.T) {
	explanation := "Read README.md first to confirm the current project structure before editing the config example."
	response := &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{
			ID:   "call_1",
			Name: "read_file",
			ExtraContent: &providers.ExtraContent{
				ToolFeedbackExplanation: explanation,
			},
		}},
	}

	got := toolFeedbackExplanationForToolCall(response, response.ToolCalls[0], nil)
	if got != explanation {
		t.Fatalf("toolFeedbackExplanationForToolCall() = %q, want full explanation", got)
	}
}

func TestToolFeedbackArgsPreview_UsesJSONAndTruncates(t *testing.T) {
	got := toolFeedbackArgsPreview(map[string]any{
		"path":  "README.md",
		"limit": 42,
	}, 128)
	want := "{\n  \"limit\": 42,\n  \"path\": \"README.md\"\n}"
	if got != want {
		t.Fatalf("toolFeedbackArgsPreview() = %q, want %q", got, want)
	}
}

func TestProcessMessage_UsesRouteSessionKey(t *testing.T) {
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
	provider := &simpleMockProvider{response: "ok"}
	al := NewAgentLoop(cfg, msgBus, provider)

	msg := bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "telegram",
			ChatID:   "chat1",
			ChatType: "direct",
			SenderID: "user1",
		},
		Content: "hello",
	}

	route := al.registry.ResolveRoute(bus.NormalizeInboundMessage(msg).Context)
	sessionKey := al.allocateRouteSession(route, msg).SessionKey

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}

	helper := testHelper{al: al}
	_ = helper.executeAndGetResponse(t, context.Background(), msg)

	history := defaultAgent.Sessions.GetHistory(sessionKey)
	if len(history) != 2 {
		t.Fatalf("expected session history len=2, got %d", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hello" {
		t.Fatalf("unexpected first message in session: %+v", history[0])
	}
}

func TestProcessMessage_CommandOutcomes(t *testing.T) {
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
		Session: config.SessionConfig{
			Dimensions: []string{"chat"},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &countingMockProvider{response: "LLM reply"}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	baseMsg := bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "whatsapp",
			ChatID:   "chat1",
			ChatType: "direct",
			SenderID: "user1",
		},
	}

	showResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  baseMsg.Context.Channel,
			ChatID:   baseMsg.Context.ChatID,
			ChatType: baseMsg.Context.ChatType,
			SenderID: baseMsg.Context.SenderID,
		},
		Content: "/show channel",
	})
	if showResp != "Current Channel: whatsapp" {
		t.Fatalf("unexpected /show reply: %q", showResp)
	}
	if provider.calls != 0 {
		t.Fatalf("LLM should not be called for handled command, calls=%d", provider.calls)
	}

	fooResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  baseMsg.Context.Channel,
			ChatID:   baseMsg.Context.ChatID,
			ChatType: baseMsg.Context.ChatType,
			SenderID: baseMsg.Context.SenderID,
		},
		Content: "/foo",
	})
	if fooResp != "LLM reply" {
		t.Fatalf("unexpected /foo reply: %q", fooResp)
	}
	if provider.calls != 1 {
		t.Fatalf("LLM should be called exactly once after /foo passthrough, calls=%d", provider.calls)
	}

	newResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  baseMsg.Context.Channel,
			ChatID:   baseMsg.Context.ChatID,
			ChatType: baseMsg.Context.ChatType,
			SenderID: baseMsg.Context.SenderID,
		},
		Content: "/new",
	})
	if newResp != "LLM reply" {
		t.Fatalf("unexpected /new reply: %q", newResp)
	}
	if provider.calls != 2 {
		t.Fatalf("LLM should be called for passthrough /new command, calls=%d", provider.calls)
	}
}

func TestProcessMessage_ClearCommandClearsRoutedAgentSession(t *testing.T) {
	workspace := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         filepath.Join(workspace, "default"),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{
					ID:        "main",
					Default:   true,
					Workspace: filepath.Join(workspace, "main"),
				},
				{
					ID:        "support",
					Workspace: filepath.Join(workspace, "support"),
				},
			},
			Dispatch: &config.DispatchConfig{
				Rules: []config.DispatchRule{
					{
						Name:  "support-dingtalk",
						Agent: "support",
						When: config.DispatchSelector{
							Channel: "dingtalk",
						},
					},
				},
			},
		},
		Session: config.SessionConfig{
			Dimensions: []string{"chat"},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &countingMockProvider{response: "LLM reply"})
	mainAgent, ok := al.registry.GetAgent("main")
	if !ok {
		t.Fatal("expected main agent")
	}
	supportAgent, ok := al.registry.GetAgent("support")
	if !ok {
		t.Fatal("expected support agent")
	}

	msg := testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "dingtalk",
			ChatID:   "chat1",
			ChatType: "direct",
			SenderID: "user1",
		},
		Content: "/clear",
	})
	route, routedAgent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute() error = %v", err)
	}
	if routedAgent != supportAgent {
		t.Fatalf("routed agent = %s, want support", routedAgent.ID)
	}
	sessionKey := al.allocateRouteSession(route, msg).SessionKey

	mainHistory := []providers.Message{{Role: "user", Content: "main history"}}
	supportHistory := []providers.Message{{Role: "user", Content: "support history"}}
	mainAgent.Sessions.SetHistory(sessionKey, mainHistory)
	mainAgent.Sessions.SetSummary(sessionKey, "main summary")
	supportAgent.Sessions.SetHistory(sessionKey, supportHistory)
	supportAgent.Sessions.SetSummary(sessionKey, "support summary")

	response, err := al.processMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Chat history cleared!" {
		t.Fatalf("response = %q, want clear confirmation", response)
	}

	if got := supportAgent.Sessions.GetHistory(sessionKey); len(got) != 0 {
		t.Fatalf("support history len = %d, want 0", len(got))
	}
	if got := supportAgent.Sessions.GetSummary(sessionKey); got != "" {
		t.Fatalf("support summary = %q, want empty", got)
	}
	if got := mainAgent.Sessions.GetHistory(sessionKey); len(got) != len(mainHistory) {
		t.Fatalf("main history len = %d, want %d", len(got), len(mainHistory))
	} else if got[0].Role != mainHistory[0].Role {
		t.Fatalf("main history[0].Role = %q, want %q", got[0].Role, mainHistory[0].Role)
	} else if got[0].Content != mainHistory[0].Content {
		t.Fatalf("main history[0].Content = %q, want %q", got[0].Content, mainHistory[0].Content)
	}
	if got := mainAgent.Sessions.GetSummary(sessionKey); got != "main summary" {
		t.Fatalf("main summary = %q, want %q", got, "main summary")
	}
}

func TestProcessMessage_MCPCommandsHandledWithoutLLMCall(t *testing.T) {
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
		Session: config.SessionConfig{
			Dimensions: []string{"chat"},
		},
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				ToolConfig: config.ToolConfig{Enabled: true},
				Servers: map[string]config.MCPServerConfig{
					"github": {
						Enabled: true,
					},
				},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &countingMockProvider{response: "LLM reply"}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	baseContext := bus.InboundContext{
		Channel:  "whatsapp",
		ChatID:   "chat1",
		ChatType: "direct",
		SenderID: "user1",
	}

	listResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Context: baseContext,
		Content: "/list mcp",
	})
	if !strings.Contains(listResp, "- `github`") || !strings.Contains(listResp, "Deferred: yes") {
		t.Fatalf("unexpected /list mcp reply: %q", listResp)
	}
	if provider.calls != 0 {
		t.Fatalf("LLM should not be called for /list mcp, calls=%d", provider.calls)
	}

	showResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Context: baseContext,
		Content: "/show mcp github",
	})
	if showResp != "MCP server 'github' is configured but not connected" {
		t.Fatalf("unexpected /show mcp reply: %q", showResp)
	}
	if provider.calls != 0 {
		t.Fatalf("LLM should not be called for /show mcp, calls=%d", provider.calls)
	}
}

func TestProcessMessage_SwitchModelShowModelConsistency(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Provider:          "openai",
				ModelName:         "local",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName: "local",
				Model:     "openai/local-model",
				APIBase:   "https://local.example.invalid/v1",
				APIKeys:   config.SimpleSecureStrings("test-key"),
			},
			{
				ModelName: "deepseek",
				Model:     "openrouter/deepseek/deepseek-v3.2",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeys:   config.SimpleSecureStrings("test-key"),
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &countingMockProvider{response: "LLM reply"}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	switchResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/switch model to deepseek",
	})
	if !strings.Contains(switchResp, "Switched model from local to deepseek") {
		t.Fatalf("unexpected /switch reply: %q", switchResp)
	}

	showResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/show model",
	})
	if !strings.Contains(showResp, "Current Model: deepseek (Provider: openrouter)") {
		t.Fatalf("unexpected /show model reply after switch: %q", showResp)
	}

	if provider.calls != 0 {
		t.Fatalf("LLM should not be called for /switch and /show, calls=%d", provider.calls)
	}
}

func TestProcessMessage_SwitchModelRejectsUnknownAlias(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Provider:          "openai",
				ModelName:         "local",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName: "local",
				Model:     "openai/local-model",
				APIBase:   "https://local.example.invalid/v1",
				APIKeys:   config.SimpleSecureStrings("test-key"),
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &countingMockProvider{response: "LLM reply"}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	switchResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/switch model to missing",
	})
	if switchResp != `model "missing" not found in model_list or providers` {
		t.Fatalf("unexpected /switch error reply: %q", switchResp)
	}

	showResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/show model",
	})
	if !strings.Contains(showResp, "Current Model: local (Provider: openai)") {
		t.Fatalf("unexpected /show model reply after rejected switch: %q", showResp)
	}

	if provider.calls != 0 {
		t.Fatalf("LLM should not be called for rejected /switch and /show, calls=%d", provider.calls)
	}
}

func TestProcessMessage_SwitchModelRoutesSubsequentRequestsToSelectedProvider(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	localCalls := 0
	localModel := ""
	localServer := newChatCompletionTestServer(t, "local", "local reply", &localCalls, &localModel)
	defer localServer.Close()

	remoteCalls := 0
	remoteModel := ""
	remoteServer := newChatCompletionTestServer(t, "remote", "remote reply", &remoteCalls, &remoteModel)
	defer remoteServer.Close()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Provider:          "openai",
				ModelName:         "local",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName: "local",
				Model:     "openai/Qwen3.5-35B-A3B",
				APIBase:   localServer.URL,
				APIKeys:   config.SimpleSecureStrings("local-key"),
			},
			{
				ModelName: "deepseek",
				Model:     "openrouter/deepseek/deepseek-v3.2",
				APIBase:   remoteServer.URL,
				APIKeys:   config.SimpleSecureStrings("remote-key"),
			},
			{
				ModelName: "deepseek",
				Model:     "openrouter/deepseek/deepseek-v3.2",
				APIBase:   remoteServer.URL,
				APIKeys:   config.SimpleSecureStrings("remote-key-secondary"),
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	firstResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hello before switch",
	})
	if firstResp != "local reply" {
		t.Fatalf("unexpected response before switch: %q", firstResp)
	}
	if localCalls != 1 {
		t.Fatalf("local calls before switch = %d, want 1", localCalls)
	}
	if remoteCalls != 0 {
		t.Fatalf("remote calls before switch = %d, want 0", remoteCalls)
	}
	if localModel != "Qwen3.5-35B-A3B" {
		t.Fatalf("local model before switch = %q, want %q", localModel, "Qwen3.5-35B-A3B")
	}

	switchResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/switch model to deepseek",
	})
	if !strings.Contains(switchResp, "Switched model from local to deepseek") {
		t.Fatalf("unexpected /switch reply: %q", switchResp)
	}
	agent := al.registry.GetDefaultAgent()
	for _, candidate := range agent.Candidates[1:] {
		if agent.CandidateProviders[candidateProviderKey(candidate)] == nil {
			t.Fatalf("candidate provider %q was not initialized after model switch", candidate.DisplayName)
		}
	}

	secondResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hello after switch",
	})
	if secondResp != "remote reply" {
		t.Fatalf("unexpected response after switch: %q", secondResp)
	}
	if localCalls != 1 {
		t.Fatalf("local calls after switch = %d, want 1", localCalls)
	}
	if remoteCalls != 1 {
		t.Fatalf("remote calls after switch = %d, want 1", remoteCalls)
	}
	if remoteModel != "deepseek-v3.2" {
		t.Fatalf(
			"remote model after switch = %q, want %q",
			remoteModel,
			"deepseek-v3.2",
		)
	}
}

func TestProcessMessage_SwitchModelPreservesPrimaryProviderFallbackInheritance(t *testing.T) {
	workspace := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				Provider:          "openai",
				ModelName:         "first",
				ModelFallbacks:    []string{"fallback"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName: "first",
				Provider:  "openai",
				Model:     "first",
				APIKeys:   config.SimpleSecureStrings("sk-first"),
			},
			{
				ModelName: "second",
				Provider:  "openai",
				Model:     "second",
				APIKeys:   config.SimpleSecureStrings("sk-second"),
			},
			{
				ModelName: "fallback",
				Provider:  "openai",
				Model:     "fallback",
			},
		},
	}

	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	helper := testHelper{al: al}

	switchResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/switch model to second",
	})
	if !strings.Contains(switchResp, "Switched model from first to second") {
		t.Fatalf("unexpected /switch reply: %q", switchResp)
	}

	agent := al.registry.GetDefaultAgent()
	if len(agent.Candidates) != 2 {
		t.Fatalf("len(agent.Candidates) = %d, want 2", len(agent.Candidates))
	}
	fallbackProvider := agent.CandidateProviders[candidateProviderKey(agent.Candidates[1])]
	if fallbackProvider != agent.Provider {
		t.Fatalf(
			"fallback provider = %T, want switched primary provider %T",
			fallbackProvider,
			agent.Provider,
		)
	}
}

func TestProcessMessage_ModelRoutingUsesLightProvider(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	heavyCalls := 0
	heavyServer := newStrictChatCompletionTestServer(
		t,
		"heavy",
		"gemini-2.5-flash",
		"heavy reply",
		&heavyCalls,
	)
	defer heavyServer.Close()

	lightCalls := 0
	lightServer := newStrictChatCompletionTestServer(
		t,
		"light",
		"qwen2.5:0.5b",
		"light reply",
		&lightCalls,
	)
	defer lightServer.Close()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "gemini-main",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				Routing: &config.RoutingConfig{
					Enabled:    true,
					LightModel: "qwen-light",
					Threshold:  0.99,
				},
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName: "gemini-main",
				Model:     "gemini/gemini-2.5-flash",
				APIBase:   heavyServer.URL,
				APIKeys:   config.SimpleSecureStrings("heavy-key"),
			},
			{
				ModelName: "qwen-light",
				Model:     "ollama/qwen2.5:0.5b",
				APIBase:   lightServer.URL,
				APIKeys:   config.SimpleSecureStrings("light-key"),
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	resp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hi",
	})
	if resp != "light reply" {
		t.Fatalf("response = %q, want %q", resp, "light reply")
	}
	if heavyCalls != 0 {
		t.Fatalf("heavy calls = %d, want 0", heavyCalls)
	}
	if lightCalls != 1 {
		t.Fatalf("light calls = %d, want 1", lightCalls)
	}
}

// TestProcessMessage_FallbackUsesPerCandidateProvider is the loop-level test for
// bug #2140. It verifies that when the primary model returns a rate-limit error
// the fallback closure routes the retry to the fallback model's own provider
// (its own api_base), not back to the primary provider's endpoint.
func TestProcessMessage_FallbackUsesPerCandidateProvider(t *testing.T) {
	workspace := t.TempDir()

	primaryCalls := 0
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls++
		// Return 429 so FallbackChain classifies this as retriable and moves on.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "rate limit exceeded",
				"type":    "rate_limit_error",
			},
		})
	}))
	defer primaryServer.Close()

	fallbackCalls := 0
	fallbackServer := newStrictChatCompletionTestServer(
		t, "fallback", "gemma-3-27b-it", "fallback reply", &fallbackCalls,
	)
	defer fallbackServer.Close()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "mistral-primary",
				ModelFallbacks:    []string{"gemma-fallback"},
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName: "mistral-primary",
				Model:     "openrouter/mistralai/mistral-small-3.1",
				APIBase:   primaryServer.URL,
				APIKeys:   config.SimpleSecureStrings("primary-key"),
				Workspace: workspace,
			},
			{
				ModelName: "gemma-fallback",
				Model:     "openrouter/gemma-3-27b-it",
				APIBase:   fallbackServer.URL,
				APIKeys:   config.SimpleSecureStrings("fallback-key"),
				Workspace: workspace,
			},
		},
	}

	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	resp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hi",
	})

	if resp != "fallback reply" {
		t.Fatalf("response = %q, want %q (fallback provider)", resp, "fallback reply")
	}
	if primaryCalls == 0 {
		t.Fatal("primary server was never called; expected at least one attempt")
	}
	if fallbackCalls != 1 {
		t.Fatalf("fallback server calls = %d, want 1", fallbackCalls)
	}
}

func TestProcessMessage_FallbackReceivesExplicitThinkingOff(t *testing.T) {
	workspace := t.TempDir()

	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "rate limit exceeded",
				"type":    "rate_limit_error",
			},
		})
	}))
	defer primaryServer.Close()

	fallbackCalls := 0
	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalls++
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("fallback server path = %q, want /chat/completions", r.URL.Path)
		}
		defer r.Body.Close()

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode fallback request: %v", err)
		}
		if got := req["model"]; got != "doubao-seed-1-6-flash-250828" {
			t.Fatalf("fallback request model = %#v, want doubao-seed-1-6-flash-250828", got)
		}
		thinking, ok := req["thinking"].(map[string]any)
		if !ok {
			t.Fatalf("fallback request thinking = %#v, want map", req["thinking"])
		}
		if got := thinking["type"]; got != "disabled" {
			t.Fatalf("fallback request thinking.type = %#v, want disabled", got)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "fallback reply"},
					"finish_reason": "stop",
				},
			},
		}); err != nil {
			t.Fatalf("encode fallback response: %v", err)
		}
	}))
	defer fallbackServer.Close()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "primary-model",
				ModelFallbacks:    []string{"doubao-fallback"},
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName: "primary-model",
				Model:     "openrouter/primary-model",
				APIBase:   primaryServer.URL,
				APIKeys:   config.SimpleSecureStrings("primary-key"),
				Workspace: workspace,
			},
			{
				ModelName:     "doubao-fallback",
				Model:         "openai/doubao-seed-1-6-flash-250828",
				APIBase:       fallbackServer.URL,
				APIKeys:       config.SimpleSecureStrings("fallback-key"),
				ThinkingLevel: "off",
				Workspace:     workspace,
			},
		},
	}

	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	helper := testHelper{al: al}

	resp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hi",
	})

	if resp != "fallback reply" {
		t.Fatalf("response = %q, want fallback reply", resp)
	}
	if fallbackCalls != 1 {
		t.Fatalf("fallback server calls = %d, want 1", fallbackCalls)
	}
}

func TestProcessMessage_PrimaryThinkingOffDoesNotLeakToFallback(t *testing.T) {
	workspace := t.TempDir()

	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "rate limit exceeded",
				"type":    "rate_limit_error",
			},
		})
	}))
	defer primaryServer.Close()

	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("fallback server path = %q, want /chat/completions", r.URL.Path)
		}
		defer r.Body.Close()

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode fallback request: %v", err)
		}
		if _, ok := req["thinking"]; ok {
			t.Fatalf("fallback request should not inherit primary thinking off, got thinking=%#v", req["thinking"])
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "fallback reply"},
					"finish_reason": "stop",
				},
			},
		}); err != nil {
			t.Fatalf("encode fallback response: %v", err)
		}
	}))
	defer fallbackServer.Close()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "primary-model",
				ModelFallbacks:    []string{"doubao-fallback"},
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName:     "primary-model",
				Model:         "openrouter/primary-model",
				APIBase:       primaryServer.URL,
				APIKeys:       config.SimpleSecureStrings("primary-key"),
				ThinkingLevel: "off",
				Workspace:     workspace,
			},
			{
				ModelName: "doubao-fallback",
				Model:     "openai/doubao-seed-1-6-flash-250828",
				APIBase:   fallbackServer.URL,
				APIKeys:   config.SimpleSecureStrings("fallback-key"),
				Workspace: workspace,
			},
		},
	}

	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	helper := testHelper{al: al}

	resp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hi",
	})
	if resp != "fallback reply" {
		t.Fatalf("response = %q, want fallback reply", resp)
	}
}

func TestProcessMessage_FallbackThinkingOffUsesCandidateIdentity(t *testing.T) {
	workspace := t.TempDir()

	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "rate limit exceeded",
				"type":    "rate_limit_error",
			},
		})
	}))
	defer primaryServer.Close()

	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("fallback server path = %q, want /chat/completions", r.URL.Path)
		}
		defer r.Body.Close()

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode fallback request: %v", err)
		}
		thinking, ok := req["thinking"].(map[string]any)
		if !ok {
			t.Fatalf("fallback request thinking = %#v, want map", req["thinking"])
		}
		if got := thinking["type"]; got != "disabled" {
			t.Fatalf("fallback request thinking.type = %#v, want disabled", got)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "fallback reply"},
					"finish_reason": "stop",
				},
			},
		}); err != nil {
			t.Fatalf("encode fallback response: %v", err)
		}
	}))
	defer fallbackServer.Close()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "primary-model",
				ModelFallbacks:    []string{"doubao-off"},
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName: "primary-model",
				Model:     "openrouter/primary-model",
				APIBase:   primaryServer.URL,
				APIKeys:   config.SimpleSecureStrings("primary-key"),
				Workspace: workspace,
			},
			{
				ModelName: "doubao-default",
				Model:     "openai/doubao-seed-1-6-flash-250828",
				APIBase:   fallbackServer.URL,
				APIKeys:   config.SimpleSecureStrings("fallback-key"),
				Workspace: workspace,
			},
			{
				ModelName:     "doubao-off",
				Model:         "openai/doubao-seed-1-6-flash-250828",
				APIBase:       fallbackServer.URL,
				APIKeys:       config.SimpleSecureStrings("fallback-key"),
				ThinkingLevel: "off",
				Workspace:     workspace,
			},
		},
	}

	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	helper := testHelper{al: al}

	resp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hi",
	})
	if resp != "fallback reply" {
		t.Fatalf("response = %q, want fallback reply", resp)
	}
}

// TestProcessMessage_FallbackUsesActiveProviderWhenCandidateNotRegistered verifies
// that when a candidate has no model_list entry it is absent from CandidateProviders
// and the fallback closure falls back to activeProvider instead of panicking.
func TestProcessMessage_FallbackUsesActiveProviderWhenCandidateNotRegistered(t *testing.T) {
	workspace := t.TempDir()

	// Primary server: returns 429 on first call, succeeds on second.
	// Both the primary and the unregistered fallback share this server
	// (same api_base) so activeProvider routes both calls here.
	callCount := 0
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "rate limit", "type": "rate_limit_error"},
			})
			return
		}
		// Second call (fallback via activeProvider) succeeds.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "active provider reply"}, "finish_reason": "stop"},
			},
		})
	}))
	defer primaryServer.Close()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "primary-model",
				MaxTokens:         4096,
				MaxToolIterations: 3,
				// No model_list entry for this alias — absent from CandidateProviders.
				ModelFallbacks: []string{"openrouter/fallback-model"},
			},
		},
		ModelList: []*config.ModelConfig{
			{
				ModelName: "primary-model",
				Model:     "openrouter/primary-model",
				APIBase:   primaryServer.URL,
				APIKeys:   config.SimpleSecureStrings("primary-key"),
				Workspace: workspace,
			},
		},
	}

	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, provider)

	helper := testHelper{al: al}
	resp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hi",
	})

	if resp != "active provider reply" {
		t.Fatalf("response = %q, want %q", resp, "active provider reply")
	}
	if callCount < 2 {
		t.Fatalf("primary server calls = %d, want >= 2 (one 429 + one success via activeProvider)", callCount)
	}
}
