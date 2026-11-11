package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

type fakeChannel struct{ id string }

func (f *fakeChannel) Name() string                    { return "fake" }
func (f *fakeChannel) Start(ctx context.Context) error { return nil }
func (f *fakeChannel) Stop(ctx context.Context) error  { return nil }
func (f *fakeChannel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	return nil, nil
}
func (f *fakeChannel) IsRunning() bool                            { return true }
func (f *fakeChannel) IsAllowed(string) bool                      { return true }
func (f *fakeChannel) IsAllowedSender(sender bus.SenderInfo) bool { return true }
func (f *fakeChannel) ReasoningChannelID() string                 { return f.id }

type fakeMediaChannel struct {
	fakeChannel
	mu           sync.Mutex
	sentMessages []bus.OutboundMessage
	sentMedia    []bus.OutboundMediaMessage
}

func (f *fakeMediaChannel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	f.mu.Lock()
	f.sentMessages = append(f.sentMessages, msg)
	f.mu.Unlock()
	return nil, nil
}

func (f *fakeMediaChannel) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) ([]string, error) {
	f.mu.Lock()
	f.sentMedia = append(f.sentMedia, msg)
	f.mu.Unlock()
	return nil, nil
}

func (f *fakeMediaChannel) sentMessagesSnapshot() []bus.OutboundMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bus.OutboundMessage(nil), f.sentMessages...)
}

func (f *fakeMediaChannel) sentMediaSnapshot() []bus.OutboundMediaMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bus.OutboundMediaMessage(nil), f.sentMedia...)
}

type recordingChannelManager struct {
	dismissed []string
}

func (m *recordingChannelManager) GetChannel(name string) (channels.Channel, bool) {
	return nil, false
}

func (m *recordingChannelManager) GetEnabledChannels() []string {
	return nil
}

func (m *recordingChannelManager) InvokeTypingStop(channel, chatID string) {}

func (m *recordingChannelManager) SendMessage(ctx context.Context, msg bus.OutboundMessage) error {
	return nil
}

func (m *recordingChannelManager) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) error {
	return nil
}

func (m *recordingChannelManager) SendPlaceholder(ctx context.Context, channel, chatID string) bool {
	return false
}

func (m *recordingChannelManager) DismissToolFeedback(
	ctx context.Context, channel, chatID string, outboundCtx *bus.InboundContext,
) {
	m.dismissed = append(m.dismissed, fmt.Sprintf("%s:%s", channel, chatID))
}

func newStartedTestChannelManager(
	t *testing.T,
	msgBus *bus.MessageBus,
	store media.MediaStore,
	name string,
	ch channels.Channel,
) *channels.Manager {
	t.Helper()

	cm, err := channels.NewManager(&config.Config{}, msgBus, store)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	cm.RegisterChannel(name, ch)
	if err := cm.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	t.Cleanup(func() {
		if err := cm.StopAll(context.Background()); err != nil {
			t.Fatalf("StopAll() error = %v", err)
		}
	})
	return cm
}

type recordingProvider struct {
	lastMessages []providers.Message
	lastModel    string
}

type panicAfterStartProvider struct {
	started chan struct{}
	calls   atomic.Int32
}

func (p *panicAfterStartProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	panic("provider panic after turn registration")
}

func (p *panicAfterStartProvider) GetDefaultModel() string {
	return "panic-after-start"
}

func (r *recordingProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	r.lastMessages = append([]providers.Message(nil), messages...)
	r.lastModel = model
	return &providers.LLMResponse{
		Content:   "Mock response",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (r *recordingProvider) GetDefaultModel() string {
	return "mock-model"
}

type thinkingRecordingProvider struct {
	lastOptions map[string]any
}

func (r *thinkingRecordingProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	r.lastOptions = maps.Clone(opts)
	return &providers.LLMResponse{
		Content:   "Mock response",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (r *thinkingRecordingProvider) GetDefaultModel() string {
	return "mock-model"
}

func (r *thinkingRecordingProvider) SupportsThinking() bool {
	return true
}

type thinkingOptionRecordingProvider struct {
	lastOptions map[string]any
}

func (r *thinkingOptionRecordingProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	r.lastOptions = maps.Clone(opts)
	return &providers.LLMResponse{
		Content:   "Mock response",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (r *thinkingOptionRecordingProvider) GetDefaultModel() string {
	return "mock-model"
}

type reasoningOptionRecordingProvider struct {
	lastOptions map[string]any
}

func (r *reasoningOptionRecordingProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	r.lastOptions = maps.Clone(opts)
	return &providers.LLMResponse{
		Content:          "final answer",
		ReasoningContent: "thinking trace",
		ToolCalls:        []providers.ToolCall{},
	}, nil
}

func (r *reasoningOptionRecordingProvider) GetDefaultModel() string {
	return "mock-model"
}

type reasoningResponseProvider struct{}

func (p *reasoningResponseProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content:          "Mock response",
		ReasoningContent: "thinking trace",
		ToolCalls:        []providers.ToolCall{},
	}, nil
}

func (p *reasoningResponseProvider) GetDefaultModel() string {
	return "mock-model"
}

type sideQuestionFallbackTestProvider struct {
	model string
}

func (p *sideQuestionFallbackTestProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	if p.model == "test-model" {
		return nil, fmt.Errorf("provider timeout")
	}
	return &providers.LLMResponse{
		ReasoningContent: "thinking trace",
		ToolCalls:        []providers.ToolCall{},
	}, nil
}

func (p *sideQuestionFallbackTestProvider) GetDefaultModel() string {
	return p.model
}

type modelRewriteHook struct {
	model string
}

type clearOptionsHook struct{}

func (clearOptionsHook) BeforeLLM(
	ctx context.Context,
	req *LLMHookRequest,
) (*LLMHookRequest, HookDecision, error) {
	next := req.Clone()
	next.Options = nil
	return next, HookDecision{Action: HookActionModify}, nil
}

func (clearOptionsHook) AfterLLM(
	ctx context.Context,
	resp *LLMHookResponse,
) (*LLMHookResponse, HookDecision, error) {
	return resp.Clone(), HookDecision{Action: HookActionContinue}, nil
}

func (h modelRewriteHook) BeforeLLM(
	ctx context.Context,
	req *LLMHookRequest,
) (*LLMHookRequest, HookDecision, error) {
	next := req.Clone()
	next.Model = h.model
	return next, HookDecision{Action: HookActionModify}, nil
}

func (h modelRewriteHook) AfterLLM(
	ctx context.Context,
	resp *LLMHookResponse,
) (*LLMHookResponse, HookDecision, error) {
	return resp.Clone(), HookDecision{Action: HookActionContinue}, nil
}

func useTestSideQuestionProvider(al *AgentLoop, provider providers.LLMProvider) {
	al.providerFactory = func(mc *config.ModelConfig) (providers.LLMProvider, string, error) {
		model := provider.GetDefaultModel()
		if mc != nil {
			if _, modelID := providers.ExtractProtocol(mc); modelID != "" {
				model = modelID
			}
		}
		return provider, model, nil
	}
}

func newTestAgentLoop(
	t *testing.T,
) (al *AgentLoop, cfg *config.Config, msgBus *bus.MessageBus, provider *mockProvider, cleanup func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	cfg = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	msgBus = bus.NewMessageBus()
	provider = &mockProvider{}
	al = NewAgentLoop(cfg, msgBus, provider)
	return al, cfg, msgBus, provider, func() { os.RemoveAll(tmpDir) }
}

// Mock implementations for testing

type simpleMockProvider struct {
	response string
}

func (m *simpleMockProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content:   m.response,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *simpleMockProvider) GetDefaultModel() string {
	return "mock-model"
}

type reasoningContentProvider struct {
	response         string
	reasoningContent string
}

func (m *reasoningContentProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content:          m.response,
		ReasoningContent: m.reasoningContent,
		ToolCalls:        []providers.ToolCall{},
	}, nil
}

func (m *reasoningContentProvider) GetDefaultModel() string {
	return "reasoning-content-model"
}

type countingMockProvider struct {
	response string
	calls    int
}

func (m *countingMockProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	return &providers.LLMResponse{
		Content:   m.response,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *countingMockProvider) GetDefaultModel() string {
	return "counting-mock-model"
}

type handledMediaProvider struct {
	calls      int
	toolCounts []int
}

func (m *handledMediaProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	m.toolCounts = append(m.toolCounts, len(tools))
	if m.calls == 1 {
		return &providers.LLMResponse{
			Content: "Taking the screenshot now.",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_handled_media",
				Type:      "function",
				Name:      "handled_media_tool",
				Arguments: map[string]any{},
			}},
		}, nil
	}
	return &providers.LLMResponse{}, nil
}

func (m *handledMediaProvider) GetDefaultModel() string {
	return "handled-media-model"
}

type handledUserProvider struct {
	calls int
}

func (m *handledUserProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	if m.calls == 1 {
		return &providers.LLMResponse{
			Content: "Delivering the result now.",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_handled_user",
				Type:      "function",
				Name:      "handled_user_tool",
				Arguments: map[string]any{},
			}},
		}, nil
	}
	return &providers.LLMResponse{}, nil
}

func (m *handledUserProvider) GetDefaultModel() string {
	return "handled-user-model"
}

type messageToolProvider struct {
	calls int
}

func (m *messageToolProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	if m.calls == 1 {
		return &providers.LLMResponse{
			Content: "",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_message",
				Type:      "function",
				Name:      "message",
				Arguments: map[string]any{"content": "direct tool message"},
			}},
		}, nil
	}
	return &providers.LLMResponse{}, nil
}

func (m *messageToolProvider) GetDefaultModel() string {
	return "message-tool-model"
}

type reasoningVisibleToolProvider struct {
	filePath string
	calls    int
}

func (m *reasoningVisibleToolProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	if m.calls == 1 {
		return &providers.LLMResponse{
			Content:          "I'll inspect that file now.",
			ReasoningContent: "Read the file before answering.",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_read_file",
				Type:      "function",
				Name:      "read_file",
				Arguments: map[string]any{"path": m.filePath},
			}},
		}, nil
	}
	return &providers.LLMResponse{Content: "DONE"}, nil
}

func (m *reasoningVisibleToolProvider) GetDefaultModel() string {
	return "reasoning-visible-tool-model"
}

type artifactThenSendProvider struct {
	calls int
}

func (m *artifactThenSendProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	if m.calls == 1 {
		return &providers.LLMResponse{
			Content: "Taking the screenshot now.",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_artifact_media",
				Type:      "function",
				Name:      "media_artifact_tool",
				Arguments: map[string]any{},
			}},
		}, nil
	}

	var artifactPath string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "tool" {
			continue
		}
		for _, prefix := range []string{"[image:", "[file:", "[audio:", "[video:"} {
			start := strings.Index(messages[i].Content, prefix)
			if start < 0 {
				continue
			}
			rest := messages[i].Content[start+len(prefix):]
			end := strings.Index(rest, "]")
			if end < 0 {
				continue
			}
			artifactPath = rest[:end]
			break
		}
		if artifactPath != "" {
			break
		}
	}
	if artifactPath == "" {
		return nil, fmt.Errorf("provider did not receive artifact path in tool result")
	}

	return &providers.LLMResponse{
		Content: "",
		ToolCalls: []providers.ToolCall{{
			ID:        "call_send_file",
			Type:      "function",
			Name:      "send_file",
			Arguments: map[string]any{"path": artifactPath},
		}},
	}, nil
}

func (m *artifactThenSendProvider) GetDefaultModel() string {
	return "artifact-then-send-model"
}

type toolFeedbackProvider struct {
	filePath string
	calls    int
}

func (m *toolFeedbackProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	if m.calls == 1 {
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:        "call_heartbeat_read_file",
				Type:      "function",
				Name:      "read_file",
				Arguments: map[string]any{"path": m.filePath},
			}},
		}, nil
	}

	return &providers.LLMResponse{
		Content:   "HEARTBEAT_OK",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *toolFeedbackProvider) GetDefaultModel() string {
	return "heartbeat-tool-feedback-model"
}

type toolFeedbackReasoningProvider struct {
	filePath string
	calls    int
}

func (m *toolFeedbackReasoningProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	if m.calls == 1 {
		return &providers.LLMResponse{
			ReasoningContent: "Read README.md first to confirm the context that needs to be changed.",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_reasoning_read_file",
				Type:      "function",
				Name:      "read_file",
				Arguments: map[string]any{"path": m.filePath},
			}},
		}, nil
	}

	return &providers.LLMResponse{
		Content:   "DONE",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *toolFeedbackReasoningProvider) GetDefaultModel() string {
	return "tool-feedback-reasoning-model"
}

type picoInterleavedContentProvider struct {
	calls int
}

func (m *picoInterleavedContentProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	if m.calls == 1 {
		return &providers.LLMResponse{
			Content: "intermediate model text",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_tool_limit_test",
				Type:      "function",
				Name:      "tool_limit_test_tool",
				Arguments: map[string]any{"value": "x"},
			}},
		}, nil
	}

	return &providers.LLMResponse{
		Content:   "final model text",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *picoInterleavedContentProvider) GetDefaultModel() string {
	return "pico-interleaved-content-model"
}

type picoDistinctToolCallContentProvider struct {
	calls int
}

func (m *picoDistinctToolCallContentProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	if m.calls == 1 {
		return &providers.LLMResponse{
			Content: "intermediate model text",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_tool_limit_test",
				Type:      "function",
				Name:      "tool_limit_test_tool",
				Arguments: map[string]any{"value": "x"},
				ExtraContent: &providers.ExtraContent{
					ToolFeedbackExplanation: "Read the file before replying.",
				},
			}},
		}, nil
	}

	return &providers.LLMResponse{
		Content:   "final model text",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *picoDistinctToolCallContentProvider) GetDefaultModel() string {
	return "pico-distinct-tool-call-content-model"
}

type toolLimitOnlyProvider struct{}

func (m *toolLimitOnlyProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{
			ID:        "call_tool_limit_test",
			Type:      "function",
			Name:      "tool_limit_test_tool",
			Arguments: map[string]any{"value": "x"},
		}},
	}, nil
}

func (m *toolLimitOnlyProvider) GetDefaultModel() string {
	return "tool-limit-only-model"
}

// mockCustomTool is a simple mock tool for registration testing
type mockCustomTool struct{}

func (m *mockCustomTool) Name() string {
	return "mock_custom"
}

func (m *mockCustomTool) Description() string {
	return "Mock custom tool for testing"
}

func (m *mockCustomTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": true,
	}
}

func (m *mockCustomTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	return tools.SilentResult("Custom tool executed")
}

type handledMediaTool struct {
	store media.MediaStore
	path  string
}

func (m *handledMediaTool) Name() string { return "handled_media_tool" }
func (m *handledMediaTool) Description() string {
	return "Returns a media attachment and fully handles the user response"
}

func (m *handledMediaTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (m *handledMediaTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	ref, err := m.store.Store(m.path, media.MediaMeta{
		Filename:    filepath.Base(m.path),
		ContentType: "image/png",
		Source:      "test:handled_media_tool",
	}, "test:handled_media")
	if err != nil {
		return tools.ErrorResult(err.Error()).WithError(err)
	}
	return tools.MediaResult("Attachment delivered by tool.", []string{ref}).WithResponseHandled()
}

type handledUserTool struct{}

func (m *handledUserTool) Name() string { return "handled_user_tool" }
func (m *handledUserTool) Description() string {
	return "Returns a user-visible result and marks delivery as handled"
}

func (m *handledUserTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (m *handledUserTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	return tools.UserResult("Handled user output from tool.").WithResponseHandled()
}

type handledMediaWithSteeringProvider struct {
	calls int
}

func (m *handledMediaWithSteeringProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	if m.calls == 1 {
		return &providers.LLMResponse{
			Content: "Taking the screenshot now.",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_handled_media_steering",
				Type:      "function",
				Name:      "handled_media_with_steering_tool",
				Arguments: map[string]any{},
			}},
		}, nil
	}

	for _, msg := range messages {
		if msg.Role == "user" && msg.Content == "what about this instead?" {
			return &providers.LLMResponse{Content: "Handled the queued steering message."}, nil
		}
	}

	return nil, fmt.Errorf("provider did not receive queued steering message")
}

func (m *handledMediaWithSteeringProvider) GetDefaultModel() string {
	return "handled-media-with-steering-model"
}

type handledMediaWithSteeringTool struct {
	store media.MediaStore
	path  string
	loop  *AgentLoop
}

func (m *handledMediaWithSteeringTool) Name() string { return "handled_media_with_steering_tool" }
func (m *handledMediaWithSteeringTool) Description() string {
	return "Returns handled media and enqueues a steering message during execution"
}

func (m *handledMediaWithSteeringTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (m *handledMediaWithSteeringTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	if err := m.loop.Steer(providers.Message{Role: "user", Content: "what about this instead?"}); err != nil {
		return tools.ErrorResult(err.Error()).WithError(err)
	}

	ref, err := m.store.Store(m.path, media.MediaMeta{
		Filename:    filepath.Base(m.path),
		ContentType: "image/png",
		Source:      "test:handled_media_with_steering_tool",
	}, "test:handled_media_with_steering")
	if err != nil {
		return tools.ErrorResult(err.Error()).WithError(err)
	}
	return tools.MediaResult("Attachment delivered by tool.", []string{ref}).WithResponseHandled()
}

type mediaArtifactTool struct {
	store media.MediaStore
	path  string
}

func (m *mediaArtifactTool) Name() string { return "media_artifact_tool" }
func (m *mediaArtifactTool) Description() string {
	return "Returns a media artifact that the agent can forward or save later"
}

func (m *mediaArtifactTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (m *mediaArtifactTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	ref, err := m.store.Store(m.path, media.MediaMeta{
		Filename:    filepath.Base(m.path),
		ContentType: "image/png",
		Source:      "test:media_artifact_tool",
	}, "test:media_artifact")
	if err != nil {
		return tools.ErrorResult(err.Error()).WithError(err)
	}
	return tools.MediaResult("Artifact created.", []string{ref})
}

type toolLimitTestTool struct{}

func (m *toolLimitTestTool) Name() string {
	return "tool_limit_test_tool"
}

func (m *toolLimitTestTool) Description() string {
	return "Tool used to exhaust the iteration budget in tests"
}

func (m *toolLimitTestTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"value": map[string]any{"type": "string"},
		},
	}
}

func (m *toolLimitTestTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	return tools.SilentResult("tool limit test result")
}

// testHelper executes a message and returns the response
type testHelper struct {
	al *AgentLoop
}

func newChatCompletionTestServer(
	t *testing.T,
	label string,
	response string,
	calls *int,
	model *string,
) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("%s server path = %q, want /chat/completions", label, r.URL.Path)
		}
		*calls = *calls + 1
		defer r.Body.Close()

		var req struct {
			Model string `json:"model"`
		}
		decodeErr := json.NewDecoder(r.Body).Decode(&req)
		if decodeErr != nil {
			t.Fatalf("decode %s request: %v", label, decodeErr)
		}
		*model = req.Model

		w.Header().Set("Content-Type", "application/json")
		encodeErr := json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": response},
					"finish_reason": "stop",
				},
			},
		})
		if encodeErr != nil {
			t.Fatalf("encode %s response: %v", label, encodeErr)
		}
	}))
}

func newStrictChatCompletionTestServer(
	t *testing.T,
	label string,
	expectedModel string,
	response string,
	calls *int,
) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("%s server path = %q, want /chat/completions", label, r.URL.Path)
		}
		*calls = *calls + 1
		defer r.Body.Close()

		var req struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode %s request: %v", label, err)
		}
		if req.Model != expectedModel {
			t.Fatalf("%s server model = %q, want %q", label, req.Model, expectedModel)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": response},
					"finish_reason": "stop",
				},
			},
		}); err != nil {
			t.Fatalf("encode %s response: %v", label, err)
		}
	}))
}

func (h testHelper) executeAndGetResponse(tb testing.TB, ctx context.Context, msg bus.InboundMessage) string {
	// Use a short timeout to avoid hanging
	timeoutCtx, cancel := context.WithTimeout(ctx, responseTimeout)
	defer cancel()

	response, err := h.al.processMessage(timeoutCtx, testInboundMessage(msg))
	if err != nil {
		tb.Fatalf("processMessage failed: %v", err)
	}
	return response
}

func testInboundMessage(msg bus.InboundMessage) bus.InboundMessage {
	if msg.Context.Channel == "" &&
		msg.Context.Account == "" &&
		msg.Context.ChatID == "" &&
		msg.Context.ChatType == "" &&
		msg.Context.TopicID == "" &&
		msg.Context.SpaceID == "" &&
		msg.Context.SpaceType == "" &&
		msg.Context.SenderID == "" &&
		msg.Context.MessageID == "" &&
		!msg.Context.Mentioned &&
		msg.Context.ReplyToMessageID == "" &&
		msg.Context.ReplyToSenderID == "" &&
		len(msg.Context.ReplyHandles) == 0 &&
		len(msg.Context.Raw) == 0 {
		msg.Context = bus.InboundContext{
			Channel:   msg.Channel,
			ChatID:    msg.ChatID,
			ChatType:  "direct",
			SenderID:  msg.SenderID,
			MessageID: msg.MessageID,
		}
	}
	return bus.NormalizeInboundMessage(msg)
}

const responseTimeout = 3 * time.Second

// failFirstMockProvider fails on the first N calls with a specific error
type failFirstMockProvider struct {
	failures    int
	currentCall int
	failError   error
	successResp string
}

func (m *failFirstMockProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.currentCall++
	if m.currentCall <= m.failures {
		return nil, m.failError
	}
	return &providers.LLMResponse{
		Content:   m.successResp,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *failFirstMockProvider) GetDefaultModel() string {
	return "mock-fail-model"
}

type visionUnsupportedMediaProvider struct {
	calls     int
	mediaSeen []bool
}

func (p *visionUnsupportedMediaProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++

	hasMedia := false
	for _, msg := range messages {
		for _, ref := range msg.Media {
			if strings.TrimSpace(ref) != "" {
				hasMedia = true
				break
			}
		}
		if hasMedia {
			break
		}
	}
	p.mediaSeen = append(p.mediaSeen, hasMedia)

	if hasMedia {
		return nil, fmt.Errorf("API request failed: " +
			"Status: 404 Body: {\"error\":{\"message\":\"No endpoints found that support image input\"}}")
	}

	return &providers.LLMResponse{
		Content:   "ok",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (p *visionUnsupportedMediaProvider) GetDefaultModel() string {
	return "mock-fail-model"
}

type loadImagePlanningProvider struct {
	path        string
	followUpErr error
	calls       int
	models      []string
}

func (p *loadImagePlanningProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	p.models = append(p.models, model)

	if p.calls == 1 {
		return &providers.LLMResponse{
			Content: "Let me inspect the image.",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_load_image_test",
				Type:      "function",
				Name:      "load_image",
				Arguments: map[string]any{"path": p.path},
			}},
		}, nil
	}

	if p.followUpErr != nil {
		return nil, p.followUpErr
	}

	return nil, fmt.Errorf("load_image follow-up should not be handled by the text model")
}

func (p *loadImagePlanningProvider) GetDefaultModel() string {
	return "load-image-planner"
}

type visionAnswerProvider struct {
	calls     int
	models    []string
	mediaSeen []bool
}

func (p *visionAnswerProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	p.models = append(p.models, model)

	hasMedia := false
	for _, msg := range messages {
		for _, ref := range msg.Media {
			if strings.TrimSpace(ref) != "" {
				hasMedia = true
				break
			}
		}
		if hasMedia {
			break
		}
	}
	p.mediaSeen = append(p.mediaSeen, hasMedia)
	if !hasMedia {
		return nil, fmt.Errorf("vision provider expected image media in follow-up request")
	}

	return &providers.LLMResponse{
		Content:   "vision answer",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (p *visionAnswerProvider) GetDefaultModel() string {
	return "vision-answer-model"
}

type resolvedImagePathVisionProvider struct {
	expectedPath string
	calls        int
	models       []string
	pathTagSeen  []bool
	mediaSeen    []bool
}

func (p *resolvedImagePathVisionProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	p.models = append(p.models, model)

	pathTag := "[image:" + p.expectedPath + "]"
	hasPathTag := false
	hasMedia := false
	for _, msg := range messages {
		if strings.Contains(msg.Content, pathTag) {
			hasPathTag = true
		}
		for _, ref := range msg.Media {
			if strings.TrimSpace(ref) != "" {
				hasMedia = true
				break
			}
		}
	}
	p.pathTagSeen = append(p.pathTagSeen, hasPathTag)
	p.mediaSeen = append(p.mediaSeen, hasMedia)

	if !hasPathTag {
		return nil, fmt.Errorf("vision provider expected resolved image path tag %q", pathTag)
	}
	if hasMedia {
		return nil, fmt.Errorf("vision provider expected resolved attachment turn without raw media refs")
	}

	return &providers.LLMResponse{
		Content:   "vision direct answer",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (p *resolvedImagePathVisionProvider) GetDefaultModel() string {
	return "resolved-image-path-vision-model"
}

type unexpectedTextAttachmentProvider struct {
	calls int
}

func (p *unexpectedTextAttachmentProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	return &providers.LLMResponse{
		Content:   "text model response",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (p *unexpectedTextAttachmentProvider) GetDefaultModel() string {
	return "unexpected-text-attachment-model"
}

type unexpectedVisionProvider struct {
	calls  int
	models []string
}

func (p *unexpectedVisionProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	p.models = append(p.models, model)
	return nil, fmt.Errorf("vision provider should not be called for this turn")
}

func (p *unexpectedVisionProvider) GetDefaultModel() string {
	return "unexpected-vision-model"
}

type loadImageThenTextFollowUpProvider struct {
	path             string
	calls            int
	models           []string
	mediaSeen        []bool
	syntheticMsgSeen []bool
}

func (p *loadImageThenTextFollowUpProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	p.models = append(p.models, model)

	hasMedia := false
	hasSynthetic := false
	for _, msg := range messages {
		if msg.Content == "[Loaded image from tool result above]" {
			hasSynthetic = true
		}
		for _, ref := range msg.Media {
			if strings.TrimSpace(ref) != "" {
				hasMedia = true
				break
			}
		}
	}
	p.mediaSeen = append(p.mediaSeen, hasMedia)
	p.syntheticMsgSeen = append(p.syntheticMsgSeen, hasSynthetic)

	switch p.calls {
	case 1:
		return &providers.LLMResponse{
			Content: "Let me inspect the image.",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_load_image_regression",
				Type:      "function",
				Name:      "load_image",
				Arguments: map[string]any{"path": p.path},
			}},
		}, nil
	case 2:
		if hasMedia {
			return nil, fmt.Errorf("text-only follow-up unexpectedly retained media from prior turn")
		}
		if hasSynthetic {
			return nil, fmt.Errorf("text-only follow-up unexpectedly rebuilt synthetic tool image message from history")
		}
		return &providers.LLMResponse{
			Content:   "text follow-up",
			ToolCalls: []providers.ToolCall{},
		}, nil
	default:
		return nil, fmt.Errorf("unexpected extra text-model call %d", p.calls)
	}
}

func (p *loadImageThenTextFollowUpProvider) GetDefaultModel() string {
	return "load-image-then-text-follow-up-model"
}

func assertToolFeedbackNotPublishedWhenDisabled(t *testing.T, channel string) {
	t.Helper()

	tmpDir := t.TempDir()
	heartbeatFile := filepath.Join(tmpDir, "tool-feedback-"+channel+".txt")
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
		Channel:  channel,
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
		t.Fatalf("expected no outbound tool feedback for %s when disabled, got %+v", channel, outbound)
	case <-time.After(200 * time.Millisecond):
	}
}

// --- Native search helper tests ---

type nativeSearchProvider struct {
	supported bool
	lastOpts  map[string]any
}

func (p *nativeSearchProvider) Chat(
	ctx context.Context, msgs []providers.Message, tools []providers.ToolDefinition,
	model string, opts map[string]any,
) (*providers.LLMResponse, error) {
	p.lastOpts = shallowCloneLLMOptions(opts)
	return &providers.LLMResponse{Content: "ok"}, nil
}

func (p *nativeSearchProvider) GetDefaultModel() string { return "test-model" }

func (p *nativeSearchProvider) SupportsNativeSearch() bool { return p.supported }

type plainProvider struct{}

func (p *plainProvider) Chat(
	ctx context.Context, msgs []providers.Message, tools []providers.ToolDefinition,
	model string, opts map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "ok"}, nil
}

func (p *plainProvider) GetDefaultModel() string { return "test-model" }

type overflowProvider struct {
	calls        int
	lastMessages []providers.Message
	chatFunc     func(
		ctx context.Context,
		messages []providers.Message,
		tools []providers.ToolDefinition,
		model string,
		opts map[string]any,
	) (*providers.LLMResponse, error)
}

func (p *overflowProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	p.lastMessages = append([]providers.Message(nil), messages...)

	if p.chatFunc != nil {
		return p.chatFunc(ctx, messages, tools, model, opts)
	}

	if p.calls == 1 {
		return nil, errors.New("context_window_exceeded")
	}

	return &providers.LLMResponse{
		Content: "Recovered from overflow",
	}, nil
}

func (p *overflowProvider) GetDefaultModel() string {
	return "test-model"
}

// concurrentMockProvider is a mock provider that allows tracking concurrency
type concurrentMockProvider struct {
	responseFunc func(callID int) string
}

func (p *concurrentMockProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	// Use an atomic counter to assign unique call IDs for concurrency tracking.
	// This avoids relying on sessionKey derivation from message content, which
	// is not deterministic across concurrent calls.
	response := "Mock response"
	if p.responseFunc != nil {
		response = p.responseFunc(len(messages))
	}

	return &providers.LLMResponse{
		Content:   response,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (p *concurrentMockProvider) GetDefaultModel() string {
	return "test-model"
}
