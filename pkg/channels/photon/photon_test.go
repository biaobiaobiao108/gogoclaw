package photon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	photonv1 "github.com/biaobiaobiao108/gogoclaw/pkg/channels/photon/internal/photon/imessage/v1"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
)

type sentTextCall struct {
	chatID, text, replyTo, clientMessageID string
}

type sentAttachmentCall struct {
	chatID, attachmentGUID, name, replyTo, clientMessageID string
	isAudio                                                bool
}

type fakePhotonAPI struct {
	mu sync.Mutex

	lines []photonLine

	startErr error

	sendTextID    string
	sendTextErr   error
	sendTextLog   []sentTextCall
	attachmentID  string
	attachment    []sentAttachmentCall
	attachmentErr error
	markedRead    []string
	typingStates  []bool
	edited        []string
	reactions     []bool
}

func (f *fakePhotonAPI) Start(context.Context) error { return f.startErr }
func (f *fakePhotonAPI) Close()                      {}
func (f *fakePhotonAPI) Lines() []photonLine         { return append([]photonLine(nil), f.lines...) }

func (f *fakePhotonAPI) ResolveLine(string) (photonLine, error) {
	if len(f.lines) == 0 {
		return photonLine{}, errors.New("no lines")
	}
	return f.lines[0], nil
}

func (f *fakePhotonAPI) RememberChatLine(string, string) {}

func (f *fakePhotonAPI) CatchUp(context.Context, string, uint64, func(uint64, *photonv1.MessageChangeEvent) error) (uint64, error) {
	return 0, nil
}

func (f *fakePhotonAPI) SubscribeMessages(context.Context, string, uint64, func(uint64, *photonv1.MessageChangeEvent) error) error {
	return nil
}

func (f *fakePhotonAPI) SendText(_ context.Context, chatID, text, replyTo, clientMessageID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendTextLog = append(f.sendTextLog, sentTextCall{chatID, text, replyTo, clientMessageID})
	return f.sendTextID, f.sendTextErr
}

func (f *fakePhotonAPI) UploadAttachment(context.Context, string, string, string) (string, error) {
	return "upload-guid", nil
}

func (f *fakePhotonAPI) SendAttachment(
	_ context.Context,
	chatID, attachmentGUID, name, replyTo, clientMessageID string,
	isAudio bool,
) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attachment = append(f.attachment, sentAttachmentCall{
		chatID: chatID, attachmentGUID: attachmentGUID, name: name, replyTo: replyTo,
		clientMessageID: clientMessageID, isAudio: isAudio,
	})
	return f.attachmentID, f.attachmentErr
}

func (f *fakePhotonAPI) EditMessage(_ context.Context, _, messageID, content string) error {
	f.mu.Lock()
	f.edited = append(f.edited, messageID+":"+content)
	f.mu.Unlock()
	return nil
}

func (f *fakePhotonAPI) UnsendMessage(context.Context, string, string) error { return nil }

func (f *fakePhotonAPI) SetReaction(_ context.Context, _, _, _ string, isSet bool) error {
	f.mu.Lock()
	f.reactions = append(f.reactions, isSet)
	f.mu.Unlock()
	return nil
}

func (f *fakePhotonAPI) SetTyping(_ context.Context, _ string, typing bool) error {
	f.mu.Lock()
	f.typingStates = append(f.typingStates, typing)
	f.mu.Unlock()
	return nil
}

func (f *fakePhotonAPI) MarkRead(_ context.Context, chatID string) error {
	f.mu.Lock()
	f.markedRead = append(f.markedRead, chatID)
	f.mu.Unlock()
	return nil
}

func (f *fakePhotonAPI) DownloadAttachment(context.Context, string, string, string) (string, *photonv1.AttachmentInfo, error) {
	return "", nil, errors.New("not configured")
}

func newTestPhotonChannel(t *testing.T, api *fakePhotonAPI) (*PhotonChannel, *bus.MessageBus) {
	t.Helper()
	cfg := config.DefaultConfig()
	bc := cfg.Channels.Get(config.ChannelPhoton)
	decoded, err := bc.GetDecoded()
	if err != nil {
		t.Fatal(err)
	}
	settings := decoded.(*config.PhotonSettings)
	settings.ProjectID = "sp-test"
	settings.ProjectSecret = *config.NewSecureString("secret")
	bc.AllowFrom = config.FlexibleStringSlice{"*"}
	bc.Placeholder.Enabled = false
	bc.Typing.Enabled = false
	messageBus := bus.NewMessageBus()
	t.Cleanup(messageBus.Close)
	ch, err := NewPhotonChannel(
		config.ChannelPhoton,
		bc,
		settings,
		cfg,
		messageBus,
		api,
		filepath.Join(t.TempDir(), "photon-state.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	ch.SetMediaStore(media.NewFileMediaStore())
	ch.SetRunning(true)
	return ch, messageBus
}

func TestSendTextPreservesDeliveryID(t *testing.T) {
	api := &fakePhotonAPI{lines: []photonLine{{ID: "line-1"}}, sendTextID: "msg-1"}
	ch, _ := newTestPhotonChannel(t, api)

	ids, err := ch.Send(context.Background(), bus.OutboundMessage{
		ID:      "delivery-1",
		ChatID:  "any;-;+15551234567",
		Content: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "msg-1" {
		t.Fatalf("ids = %#v", ids)
	}
	if len(api.sendTextLog) != 1 {
		t.Fatalf("send calls = %d", len(api.sendTextLog))
	}
	if api.sendTextLog[0].clientMessageID != "delivery-1" {
		t.Fatalf("client message id = %q", api.sendTextLog[0].clientMessageID)
	}
}

func TestSendMediaUploadsAndMarksAudio(t *testing.T) {
	api := &fakePhotonAPI{
		lines:        []photonLine{{ID: "line-1"}},
		attachmentID: "media-msg",
	}
	ch, _ := newTestPhotonChannel(t, api)
	path := filepath.Join(t.TempDir(), "voice.m4a")
	if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := ch.GetMediaStore().Store(path, media.MediaMeta{
		Filename:      "voice.m4a",
		ContentType:   "audio/mp4",
		CleanupPolicy: media.CleanupPolicyDeleteOnCleanup,
	}, "test-scope")
	if err != nil {
		t.Fatal(err)
	}

	ids, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "any;-;+15551234567",
		Parts: []bus.MediaPart{{
			Type:        "audio",
			Ref:         ref,
			Filename:    "voice.m4a",
			ContentType: "audio/mp4",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "media-msg" {
		t.Fatalf("ids = %#v", ids)
	}
	if len(api.attachment) != 1 || !api.attachment[0].isAudio {
		t.Fatalf("attachment calls = %#v", api.attachment)
	}
}

func TestHandleMessageChangePublishesInboundAndMarksRead(t *testing.T) {
	api := &fakePhotonAPI{lines: []photonLine{{ID: "line-1", Phone: "+15551234567"}}}
	ch, messageBus := newTestPhotonChannel(t, api)
	message := &photonv1.Message{
		Guid:        "message-1",
		DateCreated: timestamppb.Now(),
		Content:     &photonv1.MessageContent{Text: stringPointer("hello from iMessage")},
		Sender:      &photonv1.SingleServiceAddressInfo{Address: "+15557654321"},
		ChatGuids:   []string{"any;-;+15557654321"},
	}
	event := &photonv1.MessageChangeEvent{
		ChatGuid: "any;-;+15557654321",
		Change: &photonv1.MessageChangeEvent_MessageReceived{
			MessageReceived: &photonv1.MessageReceived{Message: message},
		},
	}

	if err := ch.handleMessageChange(context.Background(), api.lines[0], event); err != nil {
		t.Fatal(err)
	}
	select {
	case inbound := <-messageBus.InboundChan():
		if inbound.Content != "hello from iMessage" {
			t.Fatalf("content = %q", inbound.Content)
		}
		if inbound.Context.ChatID != "any;-;+15557654321" {
			t.Fatalf("chat id = %q", inbound.Context.ChatID)
		}
		if inbound.Sender.CanonicalID != "photon:+15557654321" {
			t.Fatalf("sender = %q", inbound.Sender.CanonicalID)
		}
	case <-time.After(time.Second):
		t.Fatal("inbound message was not published")
	}
	if len(api.markedRead) != 1 || api.markedRead[0] != "any;-;+15557654321" {
		t.Fatalf("marked read = %#v", api.markedRead)
	}
}

func TestHandleMessageChangeHonorsGroupMentionOnly(t *testing.T) {
	api := &fakePhotonAPI{lines: []photonLine{{ID: "line-1", Phone: "+15551234567"}}}
	ch, messageBus := newTestPhotonChannel(t, api)
	ch.BaseChannel = channels.NewBaseChannel(
		ch.Name(), ch.settings, messageBus, []string{"*"}, channels.WithGroupTrigger(config.GroupTriggerConfig{MentionOnly: true}),
	)
	ch.SetOwner(ch)
	message := &photonv1.Message{
		Guid:        "message-2",
		DateCreated: timestamppb.Now(),
		Content:     &photonv1.MessageContent{Text: stringPointer("hello group")},
		Sender:      &photonv1.SingleServiceAddressInfo{Address: "+15557654321"},
	}
	event := &photonv1.MessageChangeEvent{
		ChatGuid: "any;+;chat-1",
		Change: &photonv1.MessageChangeEvent_MessageReceived{
			MessageReceived: &photonv1.MessageReceived{Message: message},
		},
	}
	if err := ch.handleMessageChange(context.Background(), api.lines[0], event); err != nil {
		t.Fatal(err)
	}
	select {
	case inbound := <-messageBus.InboundChan():
		t.Fatalf("unexpected inbound message: %+v", inbound)
	default:
	}
}

func TestIssueTokensUsesProjectBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "sp-test" || password != "secret" {
			t.Fatalf("basic auth = %q/%q/%v", username, password, ok)
		}
		if r.URL.Path != "/projects/sp-test/imessage/tokens" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"succeed": true,
			"data": map[string]any{
				"type": "shared", "token": "token-1", "expiresIn": 3600,
			},
		})
	}))
	defer server.Close()
	settings := &config.PhotonSettings{
		ProjectID: "sp-test", ProjectSecret: *config.NewSecureString("secret"), CloudURL: server.URL,
	}
	client := newPhotonClient(settings)
	tokens, err := client.issueTokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Type != "shared" || tokens.Token != "token-1" || tokens.ExpiresIn != 3600 {
		t.Fatalf("tokens = %+v", tokens)
	}
}

func TestNormalizeChatID(t *testing.T) {
	tests := map[string]string{
		"any;-;+15551234567":           "any;-;+15551234567",
		"iMessage;+;chat-1":            "any;+;chat-1",
		"+15551234567":                 "any;-;+15551234567",
		"alice@example.com":            "any;-;alice@example.com",
		"iMessage;-;alice@example.com": "any;-;alice@example.com",
	}
	for input, want := range tests {
		if got := normalizeChatID(input); got != want {
			t.Errorf("normalizeChatID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIdempotencyKeyIsStable(t *testing.T) {
	req := &photonv1.SendTextMessageRequest{ChatGuid: "any;-;a", Text: "hello"}
	if got, want := idempotencyKey(req), idempotencyKey(req); got != want {
		t.Fatalf("idempotency keys differ: %q != %q", got, want)
	}
}

func TestClassifyPhotonSendError(t *testing.T) {
	if !errors.Is(classifyPhotonSendError("send", status.Error(codes.ResourceExhausted, "limit")), channels.ErrRateLimit) {
		t.Fatal("resource exhausted should be a rate limit")
	}
	if !errors.Is(classifyPhotonSendError("send", status.Error(codes.InvalidArgument, "bad")), channels.ErrSendFailed) {
		t.Fatal("invalid argument should be permanent")
	}
	if !errors.Is(classifyPhotonSendError("send", status.Error(codes.Unavailable, "down")), channels.ErrTemporary) {
		t.Fatal("unavailable should be retryable")
	}
}

func TestCursorStorePersistsHighestSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := newPhotonStateStore(path)
	if err := store.Set("line-1", 10); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("line-1", 9); err != nil {
		t.Fatal(err)
	}
	if err := store.SetChatLine("chat-1", "line-2"); err != nil {
		t.Fatal(err)
	}
	reloaded := newPhotonStateStore(path)
	if got := reloaded.Get("line-1"); got != 10 {
		t.Fatalf("cursor = %d, want 10", got)
	}
	if got := reloaded.ChatLine("chat-1"); got != "line-2" {
		t.Fatalf("chat line = %q, want line-2", got)
	}
}

func TestPhotonMiniAppText(t *testing.T) {
	text := photonMiniAppText(&photonv1.MiniAppContent{
		AppName: stringPointer("Maps"),
		Url:     stringPointer("https://example.com/place"),
		Layout: &photonv1.MiniAppLayoutInfo{
			Caption: stringPointer("Coffee"), Summary: stringPointer("Nearby cafe"),
		},
	})
	for _, want := range []string{"[app: Maps]", "Coffee", "Nearby cafe", "https://example.com/place"} {
		if !strings.Contains(text, want) {
			t.Fatalf("mini app text %q missing %q", text, want)
		}
	}
}
