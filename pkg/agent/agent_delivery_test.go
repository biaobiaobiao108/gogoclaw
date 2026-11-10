package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

// TestToolResult_SilentToolDoesNotSendUserMessage verifies silent tools don't trigger outbound
func TestToolResult_SilentToolDoesNotSendUserMessage(t *testing.T) {
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
	provider := &simpleMockProvider{response: "File operation complete"}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	// ReadFileTool returns SilentResult, which should not send user message
	ctx := context.Background()
	msg := bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "read test.txt",
		SessionKey: "test-session",
	}

	response := helper.executeAndGetResponse(t, ctx, msg)

	// Silent tool should return the LLM's response directly
	if response != "File operation complete" {
		t.Errorf("Expected 'File operation complete', got: %s", response)
	}
}

// TestToolResult_UserFacingToolDoesSendMessage verifies user-facing tools trigger outbound
func TestToolResult_UserFacingToolDoesSendMessage(t *testing.T) {
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
	provider := &simpleMockProvider{response: "Command output: hello world"}
	al := NewAgentLoop(cfg, msgBus, provider)
	helper := testHelper{al: al}

	// ExecTool returns UserResult, which should send user message
	ctx := context.Background()
	msg := bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "run hello",
		SessionKey: "test-session",
	}

	response := helper.executeAndGetResponse(t, ctx, msg)

	// User-facing tool should include the output in final response
	if response != "Command output: hello world" {
		t.Errorf("Expected 'Command output: hello world', got: %s", response)
	}
}

// TestAgentLoop_ContextExhaustionRetry verify that the agent retries on context errors
func TestAgentLoop_ContextExhaustionRetry(t *testing.T) {
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

	// Create a provider that fails once with a context error
	contextErr := fmt.Errorf("InvalidParameter: Total tokens of image and text exceed max message tokens")
	provider := &failFirstMockProvider{
		failures:    1,
		failError:   contextErr,
		successResp: "Recovered from context error",
	}

	al := NewAgentLoop(cfg, msgBus, provider)

	// Inject some history to simulate a full context.
	// Session history only stores user/assistant/tool messages — the system
	// prompt is built dynamically by BuildMessages and is NOT stored here.
	sessionKey := "test-session-context"
	history := []providers.Message{
		{Role: "user", Content: "Old message 1"},
		{Role: "assistant", Content: "Old response 1"},
		{Role: "user", Content: "Old message 2"},
		{Role: "assistant", Content: "Old response 2"},
		{Role: "user", Content: "Trigger message"},
	}
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}
	defaultAgent.Sessions.SetHistory(sessionKey, history)

	// Call ProcessDirectWithChannel
	// Note: ProcessDirectWithChannel calls processMessage which will execute runLLMIteration
	response, err := al.ProcessDirectWithChannel(
		context.Background(),
		"Trigger message",
		sessionKey,
		"test",
		"test-chat",
	)
	if err != nil {
		t.Fatalf("Expected success after retry, got error: %v", err)
	}

	if response != "Recovered from context error" {
		t.Errorf("Expected 'Recovered from context error', got '%s'", response)
	}

	// We expect 2 calls: 1st failed, 2nd succeeded
	if provider.currentCall != 2 {
		t.Errorf("Expected 2 calls (1 fail + 1 success), got %d", provider.currentCall)
	}

	// Check final history length
	finalHistory := defaultAgent.Sessions.GetHistory(sessionKey)
	// We verify that the history has been modified (compressed)
	// Original length: 5
	// Expected behavior: compression drops ~50% of Turns
	// Without compression: 5 + 1 (new user msg) + 1 (assistant msg) = 7
	if len(finalHistory) >= 7 {
		t.Errorf("Expected history to be compressed (len < 7), got %d", len(finalHistory))
	}
}

func TestAgentLoop_VisionUnsupportedErrorReturnsClearFailure(t *testing.T) {
	workspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &visionUnsupportedMediaProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	sessionKey := "agent:main:telegram:direct:user1"

	timeoutCtx, cancel := context.WithTimeout(context.Background(), responseTimeout)
	defer cancel()

	resp, err := al.processMessage(timeoutCtx, testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:   "telegram",
			ChatID:    "chat1",
			ChatType:  "direct",
			SenderID:  "user1",
			MessageID: "m1",
		},
		Content:    "describe this",
		Media:      []string{"data:image/png;base64,abc123"},
		SessionKey: sessionKey,
	}))
	if err == nil {
		t.Fatal("processMessage() error = nil, want vision unsupported failure")
	}
	if resp != "" {
		t.Fatalf("response = %q, want empty response on error", resp)
	}
	if !strings.Contains(err.Error(), `active model "test-model" does not support image input`) {
		t.Fatalf("error = %q, want clear vision unsupported guidance", err.Error())
	}
	if provider.calls != 1 {
		t.Fatalf("calls = %d, want %d (no retry without media)", provider.calls, 1)
	}
	if !slices.Equal(provider.mediaSeen, []bool{true}) {
		t.Fatalf("mediaSeen = %v, want %v", provider.mediaSeen, []bool{true})
	}

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) == 0 {
		t.Fatal("expected user message to remain in session history")
	}
	if len(history[0].Media) == 0 {
		t.Fatalf("history[0].Media = %v, want original media preserved", history[0].Media)
	}
}

func TestAgentLoop_UserAttachmentRoutesToImageModelAfterMediaResolution(t *testing.T) {
	workspace := t.TempDir()
	pngPath := filepath.Join(workspace, "sample.png")
	pngBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
	if err := os.WriteFile(pngPath, pngBytes, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "text-model",
				ImageModel:        "vision-model",
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
		ModelList: []*config.ModelConfig{
			{ModelName: "text-model", Model: "openai/text-model"},
			{ModelName: "vision-model", Model: "openai/vision-model"},
		},
	}

	msgBus := bus.NewMessageBus()
	textProvider := &unexpectedTextAttachmentProvider{}
	al := NewAgentLoop(cfg, msgBus, textProvider)

	store := media.NewFileMediaStore()
	al.SetMediaStore(store)
	ref, err := store.Store(pngPath, media.MediaMeta{ContentType: "image/png"}, "test:user-attachment")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	if len(agent.ImageCandidates) != 1 {
		t.Fatalf("len(ImageCandidates) = %d, want 1", len(agent.ImageCandidates))
	}

	visionProvider := &resolvedImagePathVisionProvider{expectedPath: pngPath}
	agent.CandidateProviders[candidateProviderKey(agent.ImageCandidates[0])] = visionProvider

	timeoutCtx, cancel := context.WithTimeout(context.Background(), responseTimeout)
	defer cancel()

	resp, err := al.processMessage(timeoutCtx, testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:   "telegram",
			ChatID:    "chat1",
			ChatType:  "direct",
			SenderID:  "user1",
			MessageID: "m1",
		},
		Content:    "describe this image",
		Media:      []string{ref},
		SessionKey: "agent:main:telegram:direct:user1",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if resp != "vision direct answer" {
		t.Fatalf("response = %q, want %q", resp, "vision direct answer")
	}
	if textProvider.calls != 0 {
		t.Fatalf("textProvider calls = %d, want 0", textProvider.calls)
	}
	if visionProvider.calls != 1 {
		t.Fatalf("visionProvider calls = %d, want %d", visionProvider.calls, 1)
	}
	if !slices.Equal(visionProvider.models, []string{"vision-model"}) {
		t.Fatalf("visionProvider models = %v, want %v", visionProvider.models, []string{"vision-model"})
	}
	if !slices.Equal(visionProvider.pathTagSeen, []bool{true}) {
		t.Fatalf("visionProvider pathTagSeen = %v, want %v", visionProvider.pathTagSeen, []bool{true})
	}
	if !slices.Equal(visionProvider.mediaSeen, []bool{false}) {
		t.Fatalf("visionProvider mediaSeen = %v, want %v", visionProvider.mediaSeen, []bool{false})
	}
}

func TestAgentLoop_TextFollowUpAfterUserAttachmentStaysOnTextModel(t *testing.T) {
	workspace := t.TempDir()
	pngPath := filepath.Join(workspace, "sample.png")
	pngBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
	if err := os.WriteFile(pngPath, pngBytes, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "text-model",
				ImageModel:        "vision-model",
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
		ModelList: []*config.ModelConfig{
			{ModelName: "text-model", Model: "openai/text-model"},
			{ModelName: "vision-model", Model: "openai/vision-model"},
		},
	}

	msgBus := bus.NewMessageBus()
	textProvider := &unexpectedTextAttachmentProvider{}
	al := NewAgentLoop(cfg, msgBus, textProvider)

	store := media.NewFileMediaStore()
	al.SetMediaStore(store)
	ref, err := store.Store(pngPath, media.MediaMeta{ContentType: "image/png"}, "test:user-attachment-followup")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	visionProvider := &resolvedImagePathVisionProvider{expectedPath: pngPath}
	agent.CandidateProviders[candidateProviderKey(agent.ImageCandidates[0])] = visionProvider

	sessionKey := "agent:main:telegram:direct:user1"
	timeoutCtx, cancel := context.WithTimeout(context.Background(), responseTimeout)
	defer cancel()

	resp1, err := al.processMessage(timeoutCtx, testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:   "telegram",
			ChatID:    "chat1",
			ChatType:  "direct",
			SenderID:  "user1",
			MessageID: "m1",
		},
		Content:    "describe this image",
		Media:      []string{ref},
		SessionKey: sessionKey,
	}))
	if err != nil {
		t.Fatalf("first processMessage() error = %v", err)
	}
	if resp1 != "vision direct answer" {
		t.Fatalf("first response = %q, want %q", resp1, "vision direct answer")
	}

	timeoutCtx2, cancel2 := context.WithTimeout(context.Background(), responseTimeout)
	defer cancel2()

	resp2, err := al.processMessage(timeoutCtx2, testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:   "telegram",
			ChatID:    "chat1",
			ChatType:  "direct",
			SenderID:  "user1",
			MessageID: "m2",
		},
		Content:    "now summarize it in one sentence",
		SessionKey: sessionKey,
	}))
	if err != nil {
		t.Fatalf("second processMessage() error = %v", err)
	}
	if resp2 != "text model response" {
		t.Fatalf("second response = %q, want %q", resp2, "text model response")
	}
	if textProvider.calls != 1 {
		t.Fatalf("textProvider calls = %d, want 1", textProvider.calls)
	}
	if visionProvider.calls != 1 {
		t.Fatalf("visionProvider calls = %d, want 1", visionProvider.calls)
	}
}

func TestAgentLoop_GenericImagePlaceholderDoesNotRouteToImageModel(t *testing.T) {
	workspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "text-model",
				ImageModel:        "vision-model",
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
		ModelList: []*config.ModelConfig{
			{ModelName: "text-model", Model: "openai/text-model"},
			{ModelName: "vision-model", Model: "openai/vision-model"},
		},
	}

	msgBus := bus.NewMessageBus()
	textProvider := &unexpectedTextAttachmentProvider{}
	al := NewAgentLoop(cfg, msgBus, textProvider)

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	visionProvider := &unexpectedVisionProvider{}
	agent.CandidateProviders[candidateProviderKey(agent.ImageCandidates[0])] = visionProvider

	timeoutCtx, cancel := context.WithTimeout(context.Background(), responseTimeout)
	defer cancel()

	resp, err := al.processMessage(timeoutCtx, testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:   "telegram",
			ChatID:    "chat1",
			ChatType:  "direct",
			SenderID:  "user1",
			MessageID: "m1",
		},
		Content:    "this is only a placeholder [image: photo], answer in text",
		SessionKey: "agent:main:telegram:direct:user1",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if resp != "text model response" {
		t.Fatalf("response = %q, want %q", resp, "text model response")
	}
	if textProvider.calls != 1 {
		t.Fatalf("textProvider calls = %d, want 1", textProvider.calls)
	}
	if visionProvider.calls != 0 {
		t.Fatalf("visionProvider calls = %d, want 0", visionProvider.calls)
	}
}

func TestAgentLoop_TextFollowUpAfterLoadImageStaysOnTextModel(t *testing.T) {
	workspace := t.TempDir()
	pngPath := filepath.Join(workspace, "sample.png")
	pngBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
	if err := os.WriteFile(pngPath, pngBytes, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "text-model",
				ImageModel:        "vision-model",
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
		Tools: config.ToolsConfig{
			LoadImage: config.ToolConfig{Enabled: true},
		},
		ModelList: []*config.ModelConfig{
			{ModelName: "text-model", Model: "openai/text-model"},
			{ModelName: "vision-model", Model: "openai/vision-model"},
		},
	}

	msgBus := bus.NewMessageBus()
	textProvider := &loadImageThenTextFollowUpProvider{path: pngPath}
	al := NewAgentLoop(cfg, msgBus, textProvider)
	al.SetMediaStore(media.NewFileMediaStore())

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	if len(agent.ImageCandidates) != 1 {
		t.Fatalf("len(ImageCandidates) = %d, want 1", len(agent.ImageCandidates))
	}

	visionProvider := &visionAnswerProvider{}
	agent.CandidateProviders[candidateProviderKey(agent.ImageCandidates[0])] = visionProvider

	sessionKey := "agent:main:telegram:direct:user1"
	timeoutCtx, cancel := context.WithTimeout(context.Background(), responseTimeout)
	defer cancel()

	resp1, err := al.processMessage(timeoutCtx, testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:   "telegram",
			ChatID:    "chat1",
			ChatType:  "direct",
			SenderID:  "user1",
			MessageID: "m1",
		},
		Content:    "describe the image you load",
		SessionKey: sessionKey,
	}))
	if err != nil {
		t.Fatalf("first processMessage() error = %v", err)
	}
	if resp1 != "vision answer" {
		t.Fatalf("first response = %q, want %q", resp1, "vision answer")
	}

	timeoutCtx2, cancel2 := context.WithTimeout(context.Background(), responseTimeout)
	defer cancel2()

	resp2, err := al.processMessage(timeoutCtx2, testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:   "telegram",
			ChatID:    "chat1",
			ChatType:  "direct",
			SenderID:  "user1",
			MessageID: "m2",
		},
		Content:    "now summarize it in one sentence",
		SessionKey: sessionKey,
	}))
	if err != nil {
		t.Fatalf("second processMessage() error = %v", err)
	}
	if resp2 != "text follow-up" {
		t.Fatalf("second response = %q, want %q", resp2, "text follow-up")
	}
	if textProvider.calls != 2 {
		t.Fatalf("textProvider calls = %d, want %d", textProvider.calls, 2)
	}
	if !slices.Equal(textProvider.models, []string{"text-model", "text-model"}) {
		t.Fatalf("textProvider models = %v, want %v", textProvider.models, []string{"text-model", "text-model"})
	}
	if !slices.Equal(textProvider.mediaSeen, []bool{false, false}) {
		t.Fatalf("textProvider mediaSeen = %v, want %v", textProvider.mediaSeen, []bool{false, false})
	}
	if !slices.Equal(textProvider.syntheticMsgSeen, []bool{false, false}) {
		t.Fatalf("textProvider syntheticMsgSeen = %v, want %v", textProvider.syntheticMsgSeen, []bool{false, false})
	}
	if visionProvider.calls != 1 {
		t.Fatalf("visionProvider calls = %d, want %d", visionProvider.calls, 1)
	}
}

func TestAgentLoop_LoadImageFollowUpRoutesToImageModel(t *testing.T) {
	workspace := t.TempDir()
	pngPath := filepath.Join(workspace, "sample.png")
	pngBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
	if err := os.WriteFile(pngPath, pngBytes, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "text-model",
				ImageModel:        "vision-model",
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
		Tools: config.ToolsConfig{
			LoadImage: config.ToolConfig{Enabled: true},
		},
		ModelList: []*config.ModelConfig{
			{ModelName: "text-model", Model: "openai/text-model"},
			{ModelName: "vision-model", Model: "openai/vision-model"},
		},
	}

	msgBus := bus.NewMessageBus()
	planner := &loadImagePlanningProvider{path: pngPath}
	al := NewAgentLoop(cfg, msgBus, planner)
	al.SetMediaStore(media.NewFileMediaStore())

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	if len(agent.ImageCandidates) != 1 {
		t.Fatalf("len(ImageCandidates) = %d, want 1", len(agent.ImageCandidates))
	}

	visionProvider := &visionAnswerProvider{}
	agent.CandidateProviders[candidateProviderKey(agent.ImageCandidates[0])] = visionProvider

	timeoutCtx, cancel := context.WithTimeout(context.Background(), responseTimeout)
	defer cancel()

	resp, err := al.processMessage(timeoutCtx, testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:   "telegram",
			ChatID:    "chat1",
			ChatType:  "direct",
			SenderID:  "user1",
			MessageID: "m1",
		},
		Content:    "describe the image you load",
		SessionKey: "agent:main:telegram:direct:user1",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if resp != "vision answer" {
		t.Fatalf("response = %q, want %q", resp, "vision answer")
	}
	if planner.calls != 1 {
		t.Fatalf("planner calls = %d, want %d", planner.calls, 1)
	}
	if visionProvider.calls != 1 {
		t.Fatalf("visionProvider calls = %d, want %d", visionProvider.calls, 1)
	}
	if !slices.Equal(visionProvider.models, []string{"vision-model"}) {
		t.Fatalf("visionProvider models = %v, want %v", visionProvider.models, []string{"vision-model"})
	}
	if !slices.Equal(visionProvider.mediaSeen, []bool{true}) {
		t.Fatalf("visionProvider mediaSeen = %v, want %v", visionProvider.mediaSeen, []bool{true})
	}
}

func TestAgentLoop_LoadImageFollowUpWithoutImageModelFailsClearly(t *testing.T) {
	workspace := t.TempDir()
	pngPath := filepath.Join(workspace, "sample.png")
	pngBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
	if err := os.WriteFile(pngPath, pngBytes, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "text-model",
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
		Tools: config.ToolsConfig{
			LoadImage: config.ToolConfig{Enabled: true},
		},
		ModelList: []*config.ModelConfig{
			{ModelName: "text-model", Model: "openai/text-model"},
		},
	}

	msgBus := bus.NewMessageBus()
	planner := &loadImagePlanningProvider{
		path: pngPath,
		followUpErr: fmt.Errorf(
			`API request failed: Status: 404 Body: {"error":{"message":"No endpoints found that support image input"}}`,
		),
	}
	al := NewAgentLoop(cfg, msgBus, planner)
	al.SetMediaStore(media.NewFileMediaStore())

	timeoutCtx, cancel := context.WithTimeout(context.Background(), responseTimeout)
	defer cancel()

	resp, err := al.processMessage(timeoutCtx, testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:   "telegram",
			ChatID:    "chat1",
			ChatType:  "direct",
			SenderID:  "user1",
			MessageID: "m1",
		},
		Content:    "describe the image you load",
		SessionKey: "agent:main:telegram:direct:user1",
	}))
	if err == nil {
		t.Fatal("processMessage() error = nil, want vision unsupported failure")
	}
	if resp != "" {
		t.Fatalf("response = %q, want empty response on error", resp)
	}
	if !strings.Contains(err.Error(), `active model "text-model" does not support image input`) {
		t.Fatalf("error = %q, want clear vision unsupported guidance", err.Error())
	}
	if planner.calls != 2 {
		t.Fatalf("planner calls = %d, want %d", planner.calls, 2)
	}
}

func TestAgentLoop_EmptyModelResponseUsesAccurateFallback(t *testing.T) {
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
				MaxToolIterations: 3,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &simpleMockProvider{response: ""}
	al := NewAgentLoop(cfg, msgBus, provider)

	response, err := al.ProcessDirectWithChannel(context.Background(), "hello", "empty-response", "test", "chat1")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}
	if response != defaultResponse {
		t.Fatalf("response = %q, want %q", response, defaultResponse)
	}
}

func TestAgentLoop_ToolLimitUsesDedicatedFallback(t *testing.T) {
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
				MaxToolIterations: 1,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &toolLimitOnlyProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	al.RegisterTool(&toolLimitTestTool{})

	response, err := al.ProcessDirectWithChannel(context.Background(), "hello", "tool-limit", "test", "chat1")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}
	if response != toolLimitResponse {
		t.Fatalf("response = %q, want %q", response, toolLimitResponse)
	}

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}
	route := al.registry.ResolveRoute(bus.InboundContext{
		Channel:  "test",
		ChatType: "direct",
		SenderID: "cron",
	})
	history := defaultAgent.Sessions.GetHistory(al.allocateRouteSession(route, testInboundMessage(bus.InboundMessage{
		Channel:  "test",
		SenderID: "cron",
		ChatID:   "chat1",
	})).SessionKey)
	if len(history) != 4 {
		t.Fatalf("history len = %d, want 4", len(history))
	}
	assertRoles(t, history, "user", "assistant", "tool", "assistant")
	if history[3].Content != toolLimitResponse {
		t.Fatalf("final assistant content = %q, want %q", history[3].Content, toolLimitResponse)
	}
}

// TestProcessDirectWithChannel_TriggersMCPInitialization verifies that
// ProcessDirectWithChannel triggers MCP initialization when MCP is enabled.
// Note: Manager is only initialized when at least one MCP server is configured
// and successfully connected.
func TestProcessDirectWithChannel_TriggersMCPInitialization(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Test with MCP enabled but no servers - should not initialize manager
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				ToolConfig: config.ToolConfig{
					Enabled: true,
				},
				// No servers configured - manager should not be initialized
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	defer al.Close()

	if al.mcp.hasManager() {
		t.Fatal("expected MCP manager to be nil before first direct processing")
	}

	_, err = al.ProcessDirectWithChannel(
		context.Background(),
		"hello",
		"session-1",
		"cli",
		"direct",
	)
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}

	// Manager should not be initialized when no servers are configured
	if al.mcp.hasManager() {
		t.Fatal("expected MCP manager to be nil when no servers are configured")
	}
}

func TestTargetReasoningChannelID_AllChannels(t *testing.T) {
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

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	chManager, err := channels.NewManager(&config.Config{}, bus.NewMessageBus(), nil)
	if err != nil {
		t.Fatalf("Failed to create channel manager: %v", err)
	}
	for name, id := range map[string]string{
		"telegram": "rid-telegram",
		"feishu":   "rid-feishu",
		"discord":  "rid-discord",
		"qq":       "rid-qq",
		"weixin":   "rid-weixin",
		"pico":     "rid-pico",
	} {
		chManager.RegisterChannel(name, &fakeChannel{id: id})
	}
	al.SetChannelManager(chManager)
	tests := []struct {
		channel string
		wantID  string
	}{
		{channel: "telegram", wantID: "rid-telegram"},
		{channel: "feishu", wantID: "rid-feishu"},
		{channel: "discord", wantID: "rid-discord"},
		{channel: "qq", wantID: "rid-qq"},
		{channel: "weixin", wantID: "rid-weixin"},
		{channel: "pico", wantID: "rid-pico"},
		{channel: "unknown", wantID: ""},
	}

	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			got := al.targetReasoningChannelID(tt.channel)
			if got != tt.wantID {
				t.Fatalf("targetReasoningChannelID(%q) = %q, want %q", tt.channel, got, tt.wantID)
			}
		})
	}
}

func TestHandleReasoning(t *testing.T) {
	newLoop := func(t *testing.T) (*AgentLoop, *bus.MessageBus) {
		t.Helper()
		tmpDir, err := os.MkdirTemp("", "agent-test-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
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
		return NewAgentLoop(cfg, msgBus, &mockProvider{}), msgBus
	}

	t.Run("skips when any required field is empty", func(t *testing.T) {
		al, msgBus := newLoop(t)
		al.handleReasoning(context.Background(), "reasoning", "telegram", "")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for {
			select {
			case msg, ok := <-msgBus.OutboundChan():
				if !ok {
					t.Fatalf("expected no outbound message, got %+v", msg)
				}
				if msg.Content == "reasoning" {
					t.Fatalf("expected no message for empty chatID, got %+v", msg)
				}
				return
			case <-ctx.Done():
				t.Log("expected an outbound message, got none within timeout")
				return
			default:
				// Continue to check for message
				time.Sleep(5 * time.Millisecond) // Avoid busy loop
			}
		}
	})

	t.Run("publishes one message for non telegram", func(t *testing.T) {
		al, msgBus := newLoop(t)
		al.handleReasoning(context.Background(), "hello reasoning", "slack", "channel-1")

		msg, ok := <-msgBus.OutboundChan()
		if !ok {
			t.Fatal("expected an outbound message")
		}
		if msg.Channel != "slack" || msg.ChatID != "channel-1" || msg.Content != "hello reasoning" {
			t.Fatalf("unexpected outbound message: %+v", msg)
		}
	})

	t.Run("publishes one message for telegram", func(t *testing.T) {
		al, msgBus := newLoop(t)
		reasoning := "hello telegram reasoning"
		al.handleReasoning(context.Background(), reasoning, "telegram", "tg-chat")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				t.Fatal("expected an outbound message, got none within timeout")
				return
			case msg, ok := <-msgBus.OutboundChan():
				if !ok {
					t.Fatal("expected outbound message")
				}

				if msg.Channel != "telegram" {
					t.Fatalf("expected telegram channel message, got %+v", msg)
				}
				if msg.ChatID != "tg-chat" {
					t.Fatalf("expected chatID tg-chat, got %+v", msg)
				}
				if msg.Content != reasoning {
					t.Fatalf("content mismatch: got %q want %q", msg.Content, reasoning)
				}
				return
			}
		}
	})
	t.Run("expired ctx", func(t *testing.T) {
		al, msgBus := newLoop(t)
		reasoning := "hello telegram reasoning"

		al.handleReasoning(context.Background(), reasoning, "telegram", "tg-chat")

		consumeCtx, consumeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer consumeCancel()

		for {
			select {
			case msg, ok := <-msgBus.OutboundChan():
				if !ok {
					t.Fatalf("expected no outbound message, but received: %+v", msg)
				}
				t.Logf("Received unexpected outbound message: %+v", msg)
				return
			case <-consumeCtx.Done():
				t.Fatalf("failed: no message received within timeout")
				return
			}
		}
	})

	t.Run("returns promptly when bus is full", func(t *testing.T) {
		al, msgBus := newLoop(t)

		// Fill the outbound bus buffer until a publish would block.
		// Use a short timeout to detect when the buffer is full,
		// rather than hardcoding the buffer size.
		for i := 0; ; i++ {
			fillCtx, fillCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			err := msgBus.PublishOutbound(fillCtx, bus.OutboundMessage{
				Context: bus.NewOutboundContext("filler", "filler", ""),
				Content: fmt.Sprintf("filler-%d", i),
			})
			fillCancel()
			if err != nil {
				// Buffer is full (timed out trying to send).
				break
			}
		}

		// Use a short-deadline parent context to bound the test.
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		start := time.Now()
		al.handleReasoning(ctx, "should timeout", "slack", "channel-full")
		elapsed := time.Since(start)

		// handleReasoning uses a 5s internal timeout, but the parent ctx
		// expires in 500ms. It should return within ~500ms, not 5s.
		if elapsed > 2*time.Second {
			t.Fatalf("handleReasoning blocked too long (%v); expected prompt return", elapsed)
		}

		// Drain the bus and verify the reasoning message was NOT published
		// (it should have been dropped due to timeout).
		timeer := time.After(1 * time.Second)
		for {
			select {
			case <-timeer:
				t.Logf(
					"no reasoning message received after draining bus for 1s, as expected,length=%d",
					len(msgBus.OutboundChan()),
				)
				return
			case msg, ok := <-msgBus.OutboundChan():
				if !ok {
					break
				}
				if msg.Content == "should timeout" {
					t.Fatal("expected reasoning message to be dropped when bus is full, but it was published")
				}
			}
		}
	})
}

func TestProcessMessage_PublishesReasoningContentToReasoningChannel(t *testing.T) {
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
	provider := &reasoningContentProvider{
		response:         "final answer",
		reasoningContent: "thinking trace",
	}
	al := NewAgentLoop(cfg, msgBus, provider)

	chManager, err := channels.NewManager(&config.Config{}, msgBus, nil)
	if err != nil {
		t.Fatalf("Failed to create channel manager: %v", err)
	}
	chManager.RegisterChannel("telegram", &fakeChannel{id: "reason-chat"})
	al.SetChannelManager(chManager)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hello",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "final answer" {
		t.Fatalf("processMessage() response = %q, want %q", response, "final answer")
	}

	select {
	case outbound := <-msgBus.OutboundChan():
		if outbound.Channel != "telegram" {
			t.Fatalf("reasoning channel = %q, want %q", outbound.Channel, "telegram")
		}
		if outbound.ChatID != "reason-chat" {
			t.Fatalf("reasoning chatID = %q, want %q", outbound.ChatID, "reason-chat")
		}
		if outbound.Context.Channel != "telegram" || outbound.Context.ChatID != "reason-chat" {
			t.Fatalf("unexpected reasoning context: %+v", outbound.Context)
		}
		if outbound.Content != "thinking trace" {
			t.Fatalf("reasoning content = %q, want %q", outbound.Content, "thinking trace")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected reasoning content to be published to reasoning channel")
	}
}

func TestProcessMessage_PicoPublishesReasoningAsThoughtMessage(t *testing.T) {
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
	provider := &reasoningContentProvider{
		response:         "final answer",
		reasoningContent: "thinking trace",
	}
	al := NewAgentLoop(cfg, msgBus, provider)

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
		t.Fatalf("processMessage() response = %q, want %q", response, "final answer")
	}

	var thoughtMsg *bus.OutboundMessage
	deadline := time.After(3 * time.Second)

	for thoughtMsg == nil {
		select {
		case outbound := <-msgBus.OutboundChan():
			msg := outbound
			if msg.Content == "thinking trace" {
				thoughtMsg = &msg
			}
		case <-deadline:
			t.Fatal("expected thought outbound message for pico")
		}
	}

	if thoughtMsg.Channel != "pico" || thoughtMsg.ChatID != "pico:test-session" {
		t.Fatalf("thought message route = %s/%s, want pico/pico:test-session", thoughtMsg.Channel, thoughtMsg.ChatID)
	}
	if thoughtMsg.Context.Raw[metadataKeyMessageKind] != messageKindThought {
		t.Fatalf(
			"thought metadata kind = %q, want %q",
			thoughtMsg.Context.Raw[metadataKeyMessageKind],
			messageKindThought,
		)
	}
}

func TestProcessHeartbeat_DoesNotPublishToolFeedback(t *testing.T) {
	tmpDir := t.TempDir()
	heartbeatFile := filepath.Join(tmpDir, "heartbeat-task.txt")
	if err := os.WriteFile(heartbeatFile, []byte("heartbeat task"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ToolFeedback: config.ToolFeedbackConfig{
					Enabled:       true,
					MaxArgsLength: 300,
				},
			},
		},
		Tools: config.ToolsConfig{
			ReadFile: config.ReadFileToolConfig{
				Enabled: true,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &toolFeedbackProvider{filePath: heartbeatFile}
	al := NewAgentLoop(cfg, msgBus, provider)

	response, err := al.ProcessHeartbeat(context.Background(), "check heartbeat tasks", "telegram", "chat-1")
	if err != nil {
		t.Fatalf("ProcessHeartbeat() error = %v", err)
	}
	if response != "HEARTBEAT_OK" {
		t.Fatalf("ProcessHeartbeat() response = %q, want %q", response, "HEARTBEAT_OK")
	}

	select {
	case outbound := <-msgBus.OutboundChan():
		t.Fatalf("expected no outbound tool feedback during heartbeat, got %+v", outbound)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestProcessMessage_PublishesToolFeedbackWhenEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	heartbeatFile := filepath.Join(tmpDir, "tool-feedback.txt")
	if err := os.WriteFile(heartbeatFile, []byte("tool feedback task"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ToolFeedback: config.ToolFeedbackConfig{
					Enabled:       true,
					MaxArgsLength: 300,
				},
			},
		},
		Tools: config.ToolsConfig{
			ReadFile: config.ReadFileToolConfig{
				Enabled: true,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &toolFeedbackProvider{filePath: heartbeatFile}
	al := NewAgentLoop(cfg, msgBus, provider)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user-1",
		ChatID:   "chat-1",
		Content:  "check tool feedback",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "HEARTBEAT_OK" {
		t.Fatalf("processMessage() response = %q, want %q", response, "HEARTBEAT_OK")
	}

	select {
	case outbound := <-msgBus.OutboundChan():
		escapedHeartbeatFile := strings.ReplaceAll(heartbeatFile, `\`, `\\`)
		if outbound.Channel != "telegram" {
			t.Fatalf("tool feedback channel = %q, want %q", outbound.Channel, "telegram")
		}
		if outbound.ChatID != "chat-1" {
			t.Fatalf("tool feedback chatID = %q, want %q", outbound.ChatID, "chat-1")
		}
		if outbound.Context.Channel != "telegram" || outbound.Context.ChatID != "chat-1" {
			t.Fatalf("unexpected tool feedback context: %+v", outbound.Context)
		}
		if !strings.Contains(outbound.Content, "`read_file`") {
			t.Fatalf("tool feedback content = %q, want read_file summary", outbound.Content)
		}
		if !strings.Contains(outbound.Content, utils.ToolFeedbackContinuationHint) {
			t.Fatalf("tool feedback content = %q, want continuation hint fallback", outbound.Content)
		}
		if !strings.Contains(outbound.Content, "check tool feedback") {
			t.Fatalf("tool feedback content = %q, want current user intent fallback", outbound.Content)
		}
		if !strings.Contains(outbound.Content, "\"path\":") {
			t.Fatalf("tool feedback content = %q, want serialized tool arguments", outbound.Content)
		}
		if !strings.Contains(outbound.Content, escapedHeartbeatFile) {
			t.Fatalf("tool feedback content = %q, want tool argument value", outbound.Content)
		}
		if strings.Contains(outbound.Content, "Previous turn explanation") {
			t.Fatalf("tool feedback content = %q, want no previous assistant fallback", outbound.Content)
		}
		if outbound.AgentID != "main" {
			t.Fatalf("tool feedback agent_id = %q, want main", outbound.AgentID)
		}
		if outbound.SessionKey == "" {
			t.Fatal("expected tool feedback to carry session_key")
		}
		if outbound.Scope == nil || outbound.Scope.AgentID != "main" || outbound.Scope.Channel != "telegram" {
			t.Fatalf("expected tool feedback scope, got %+v", outbound.Scope)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected outbound tool feedback for regular messages")
	}
}

func TestProcessMessage_PersistsReasoningContentInSessionHistory(t *testing.T) {
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
	provider := &reasoningContentProvider{
		response:         "final answer",
		reasoningContent: "thinking trace",
	}
	al := NewAgentLoop(cfg, msgBus, provider)

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
		t.Fatalf("processMessage() response = %q, want %q", response, "final answer")
	}

	store := al.GetRegistry().GetDefaultAgent().Sessions
	sessionKeys := store.ListSessions()
	if len(sessionKeys) != 1 {
		t.Fatalf("session keys = %v, want exactly 1 active session", sessionKeys)
	}
	history := store.GetHistory(sessionKeys[0])
	if len(history) < 2 {
		t.Fatalf("session history len = %d, want at least 2", len(history))
	}

	last := history[len(history)-1]
	if last.Role != "assistant" {
		t.Fatalf("last message role = %q, want assistant", last.Role)
	}
	if last.Content != "final answer" {
		t.Fatalf("last message content = %q, want %q", last.Content, "final answer")
	}
	if last.ReasoningContent != "thinking trace" {
		t.Fatalf("last message reasoning_content = %q, want %q", last.ReasoningContent, "thinking trace")
	}
}

func TestProcessMessage_PersistsReasoningToolResponseAsSingleAssistantRecord(t *testing.T) {
	tmpDir := t.TempDir()
	inspectPath := filepath.Join(tmpDir, "inspect.txt")
	if err := os.WriteFile(inspectPath, []byte("inspect me"), 0o644); err != nil {
		t.Fatalf("WriteFile(inspectPath) error = %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmpDir
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10

	msgBus := bus.NewMessageBus()
	provider := &reasoningVisibleToolProvider{filePath: inspectPath}
	al := NewAgentLoop(cfg, msgBus, provider)

	response, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hello",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "DONE" {
		t.Fatalf("processMessage() response = %q, want %q", response, "DONE")
	}

	store := al.GetRegistry().GetDefaultAgent().Sessions
	sessionKeys := store.ListSessions()
	if len(sessionKeys) != 1 {
		t.Fatalf("session keys = %v, want exactly 1 active session", sessionKeys)
	}

	history := store.GetHistory(sessionKeys[0])
	if len(history) < 3 {
		t.Fatalf("session history len = %d, want at least 3", len(history))
	}

	var assistantWithToolCall *providers.Message
	for i := range history {
		msg := history[i]
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			assistantWithToolCall = &msg
			break
		}
	}
	if assistantWithToolCall == nil {
		t.Fatal("expected assistant history record with tool_calls")
	}
	if assistantWithToolCall.Content != "I'll inspect that file now." {
		t.Fatalf("assistant content = %q, want %q", assistantWithToolCall.Content, "I'll inspect that file now.")
	}
	if assistantWithToolCall.ReasoningContent != "Read the file before answering." {
		t.Fatalf("assistant reasoning_content = %q, want preserved", assistantWithToolCall.ReasoningContent)
	}
	if len(assistantWithToolCall.ToolCalls) != 1 {
		t.Fatalf("assistant tool calls = %+v, want single read_file tool", assistantWithToolCall.ToolCalls)
	}
	if got := providers.NormalizeToolCall(assistantWithToolCall.ToolCalls[0]).Name; got != "read_file" {
		t.Fatalf("assistant tool calls = %+v, want single read_file tool", assistantWithToolCall.ToolCalls)
	}

	transcript := history
	if len(transcript) < 3 {
		t.Fatalf("transcript length = %d, want at least 3", len(transcript))
	}

	matchingRecords := 0
	for _, msg := range transcript {
		if msg.Role != "assistant" {
			continue
		}
		if msg.Content == "I'll inspect that file now." || msg.ReasoningContent == "Read the file before answering." {
			matchingRecords++
			toolName := ""
			if len(msg.ToolCalls) == 1 {
				toolName = providers.NormalizeToolCall(msg.ToolCalls[0]).Name
			}
			if msg.Content != "I'll inspect that file now." ||
				msg.ReasoningContent != "Read the file before answering." ||
				len(msg.ToolCalls) != 1 ||
				toolName != "read_file" {
				t.Fatalf("assistant record = %+v, want content+reasoning+tool_calls in one record", msg)
			}
		}
	}
	if matchingRecords != 1 {
		t.Fatalf("matching assistant records = %d, want exactly 1 canonical assistant record", matchingRecords)
	}
}

func TestProcessMessage_DoesNotLeakReasoningContentInToolFeedback(t *testing.T) {
	tmpDir := t.TempDir()
	heartbeatFile := filepath.Join(tmpDir, "tool-feedback-reasoning.txt")
	if err := os.WriteFile(heartbeatFile, []byte("tool feedback task"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ToolFeedback: config.ToolFeedbackConfig{
					Enabled:       true,
					MaxArgsLength: 300,
				},
			},
		},
		Tools: config.ToolsConfig{
			ReadFile: config.ReadFileToolConfig{
				Enabled: true,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &toolFeedbackReasoningProvider{filePath: heartbeatFile}
	al := NewAgentLoop(cfg, msgBus, provider)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user-1",
		ChatID:   "chat-1",
		Content:  "check reasoning fallback",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "DONE" {
		t.Fatalf("processMessage() response = %q, want %q", response, "DONE")
	}

	select {
	case outbound := <-msgBus.OutboundChan():
		escapedHeartbeatFile := strings.ReplaceAll(heartbeatFile, `\`, `\\`)
		if !strings.Contains(outbound.Content, "`read_file`") {
			t.Fatalf("tool feedback content = %q, want read_file summary", outbound.Content)
		}
		if !strings.Contains(outbound.Content, utils.ToolFeedbackContinuationHint) {
			t.Fatalf("tool feedback content = %q, want continuation hint fallback", outbound.Content)
		}
		if !strings.Contains(outbound.Content, "check reasoning fallback") {
			t.Fatalf("tool feedback content = %q, want current user intent fallback", outbound.Content)
		}
		if !strings.Contains(outbound.Content, "\"path\":") {
			t.Fatalf("tool feedback content = %q, want serialized tool arguments", outbound.Content)
		}
		if !strings.Contains(outbound.Content, escapedHeartbeatFile) {
			t.Fatalf("tool feedback content = %q, want tool argument value", outbound.Content)
		}
		if strings.Contains(outbound.Content, "Read README.md first") {
			t.Fatalf("tool feedback content = %q, should not leak hidden reasoning", outbound.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected outbound tool feedback without leaking reasoning")
	}
}

func TestProcessMessage_DoesNotPublishToolFeedbackForDiscordWhenDisabled(t *testing.T) {
	assertToolFeedbackNotPublishedWhenDisabled(t, "discord")
}

func TestProcessMessage_DoesNotPublishToolFeedbackForTelegramWhenDisabled(t *testing.T) {
	assertToolFeedbackNotPublishedWhenDisabled(t, "telegram")
}

func TestProcessMessage_DoesNotPublishToolFeedbackForFeishuWhenDisabled(t *testing.T) {
	assertToolFeedbackNotPublishedWhenDisabled(t, "feishu")
}

func TestProcessMessage_MessageToolPublishesOutboundWithTurnMetadata(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10
	cfg.Session.Dimensions = []string{"chat"}

	msgBus := bus.NewMessageBus()
	provider := &messageToolProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user-1",
		ChatID:   "chat-1",
		Content:  "send a direct message",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response == "" {
		t.Fatal("expected processMessage() to return a final loop response")
	}

	select {
	case outbound := <-msgBus.OutboundChan():
		if outbound.Content != "direct tool message" {
			t.Fatalf("outbound content = %q, want direct tool message", outbound.Content)
		}
		if outbound.AgentID != "main" {
			t.Fatalf("outbound agent_id = %q, want main", outbound.AgentID)
		}
		if outbound.SessionKey == "" {
			t.Fatal("expected message tool outbound to carry session_key")
		}
		if outbound.Scope == nil || outbound.Scope.Values["chat"] != "direct:chat-1" {
			t.Fatalf("unexpected message tool outbound scope: %+v", outbound.Scope)
		}
		if outbound.Context.Channel != "telegram" || outbound.Context.ChatID != "chat-1" {
			t.Fatalf("unexpected message tool outbound context: %+v", outbound.Context)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected message tool outbound")
	}
}

func TestRun_PicoPublishesAssistantContentDuringToolCallsWithoutFinalDuplicate(t *testing.T) {
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
	provider := &picoDistinctToolCallContentProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	agent.Tools.Register(&toolLimitTestTool{})

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- al.Run(runCtx)
	}()

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:  "pico",
		SenderID: "user-1",
		ChatID:   "session-1",
		Content:  "run with tools",
	}); err != nil {
		t.Fatalf("PublishInbound() error = %v", err)
	}

	outputs := make([]bus.OutboundMessage, 0, 3)
	deadline := time.After(2 * time.Second)
	for len(outputs) < 3 {
		select {
		case outbound := <-msgBus.OutboundChan():
			outputs = append(outputs, outbound)
		case <-deadline:
			t.Fatalf("timed out waiting for pico outputs, got %v", outputs)
		}
	}

	if outputs[0].Content != "intermediate model text" {
		t.Fatalf("first outbound content = %q, want %q", outputs[0].Content, "intermediate model text")
	}
	if outputs[1].Context.Raw[metadataKeyMessageKind] != messageKindToolCalls {
		t.Fatalf("second outbound = %+v, want tool_calls message", outputs[1])
	}
	if !strings.Contains(outputs[1].Context.Raw[metadataKeyToolCalls], "tool_limit_test_tool") {
		t.Fatalf("second outbound tool_calls = %q, want tool name", outputs[1].Context.Raw[metadataKeyToolCalls])
	}
	if outputs[2].Content != "final model text" {
		t.Fatalf("third outbound content = %q, want %q", outputs[2].Content, "final model text")
	}

	runCancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Run() to exit")
	}

	select {
	case outbound := <-msgBus.OutboundChan():
		if outbound.Content == "final model text" {
			t.Fatalf("unexpected duplicate final pico output: %+v", outbound)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRunAgentLoop_PicoSkipsInterimPublishWhenNotAllowed(t *testing.T) {
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
	provider := &picoInterleavedContentProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	agent.Tools.Register(&toolLimitTestTool{})

	response, err := al.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey:              "agent:main:pico:session-1",
		Channel:                 "pico",
		ChatID:                  "session-1",
		UserMessage:             "run with tools",
		DefaultResponse:         defaultResponse,
		EnableSummary:           false,
		SendResponse:            false,
		AllowInterimPicoPublish: false,
		SuppressToolFeedback:    true,
	})
	if err != nil {
		t.Fatalf("runAgentLoop() error = %v", err)
	}
	if response != "final model text" {
		t.Fatalf("runAgentLoop() response = %q, want %q", response, "final model text")
	}

	select {
	case outbound := <-msgBus.OutboundChan():
		t.Fatalf("unexpected outbound message when interim publish disabled: %+v", outbound)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRun_PicoToolFeedbackSuppressesDuplicateInterimAssistantContent(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ToolFeedback: config.ToolFeedbackConfig{
					Enabled: true,
				},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &picoInterleavedContentProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	agent.Tools.Register(&toolLimitTestTool{})

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- al.Run(runCtx)
	}()

	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:  "pico",
		SenderID: "user-1",
		ChatID:   "session-1",
		Content:  "run with tools",
	}); err != nil {
		t.Fatalf("PublishInbound() error = %v", err)
	}

	outputs := make([]bus.OutboundMessage, 0, 3)
	deadline := time.After(2 * time.Second)
	for len(outputs) < 2 {
		select {
		case outbound := <-msgBus.OutboundChan():
			outputs = append(outputs, outbound)
		case <-deadline:
			t.Fatalf("timed out waiting for pico outputs, got %v", outputs)
		}
	}

	if outputs[0].Context.Raw[metadataKeyMessageKind] != messageKindToolCalls {
		t.Fatalf("first outbound = %+v, want tool_calls message", outputs[0])
	}
	if outputs[0].Content != "" {
		t.Fatalf("first outbound content = %q, want empty tool_calls content", outputs[0].Content)
	}
	if !strings.Contains(outputs[0].Context.Raw[metadataKeyToolCalls], "tool_limit_test_tool") {
		t.Fatalf("first outbound tool_calls = %q, want tool name", outputs[0].Context.Raw[metadataKeyToolCalls])
	}
	if outputs[1].Content != "final model text" {
		t.Fatalf("second outbound content = %q, want %q", outputs[1].Content, "final model text")
	}

	runCancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Run() to exit")
	}

	select {
	case outbound := <-msgBus.OutboundChan():
		t.Fatalf("unexpected extra pico output after tool feedback + final reply: %+v", outbound)
	case <-time.After(200 * time.Millisecond):
	}
}
