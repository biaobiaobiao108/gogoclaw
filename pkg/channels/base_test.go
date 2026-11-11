package channels

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
)

func TestInboundMediaIsProtectedWhileQueued(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	store := media.NewFileMediaStoreWithCleanup(media.MediaCleanerConfig{MaxAge: time.Nanosecond})
	path := filepath.Join(t.TempDir(), "attachment")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := store.Store(path, media.MediaMeta{}, "scope")
	if err != nil {
		t.Fatal(err)
	}
	ch := NewBaseChannel("test", nil, msgBus, []string{"*"})
	ch.SetMediaStore(store)
	ctx := bus.InboundContext{ChatID: "chat", SenderID: "sender", MessageID: "message"}
	if err := ch.HandleInboundContext(context.Background(), "chat", "hello", []string{ref}, ctx); err != nil {
		t.Fatal(err)
	}
	if got := store.CleanExpired(); got != 0 {
		t.Fatalf("queued media expired: %d", got)
	}
	msg := <-msgBus.InboundChan()
	media.Unpin(store, msg.Media)
}

func TestBaseChannelIsAllowed(t *testing.T) {
	tests := []struct {
		name      string
		allowList []string
		senderID  string
		want      bool
	}{
		{
			name:      "empty allowlist denies all",
			allowList: nil,
			senderID:  "anyone",
			want:      false,
		},
		{
			name:      "compound sender matches numeric allowlist",
			allowList: []string{"123456"},
			senderID:  "123456|alice",
			want:      true,
		},
		{
			name:      "compound sender matches username allowlist",
			allowList: []string{"@alice"},
			senderID:  "123456|alice",
			want:      true,
		},
		{
			name:      "numeric sender matches legacy compound allowlist",
			allowList: []string{"123456|alice"},
			senderID:  "123456",
			want:      true,
		},
		{
			name:      "wildcard explicitly allows everyone",
			allowList: []string{"*"},
			senderID:  "anyone",
			want:      true,
		},
		{
			name:      "non matching sender is denied",
			allowList: []string{"123456"},
			senderID:  "654321|bob",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := NewBaseChannel("test", nil, nil, tt.allowList)
			if got := ch.IsAllowed(tt.senderID); got != tt.want {
				t.Fatalf("IsAllowed(%q) = %v, want %v", tt.senderID, got, tt.want)
			}
		})
	}
}

func TestShouldRespondInGroup(t *testing.T) {
	tests := []struct {
		name        string
		gt          config.GroupTriggerConfig
		isMentioned bool
		content     string
		wantRespond bool
		wantContent string
	}{
		{
			name:        "no config - permissive default",
			gt:          config.GroupTriggerConfig{},
			isMentioned: false,
			content:     "hello world",
			wantRespond: true,
			wantContent: "hello world",
		},
		{
			name:        "no config - mentioned",
			gt:          config.GroupTriggerConfig{},
			isMentioned: true,
			content:     "hello world",
			wantRespond: true,
			wantContent: "hello world",
		},
		{
			name:        "mention_only - not mentioned",
			gt:          config.GroupTriggerConfig{MentionOnly: true},
			isMentioned: false,
			content:     "hello world",
			wantRespond: false,
			wantContent: "hello world",
		},
		{
			name:        "mention_only - mentioned",
			gt:          config.GroupTriggerConfig{MentionOnly: true},
			isMentioned: true,
			content:     "hello world",
			wantRespond: true,
			wantContent: "hello world",
		},
		{
			name:        "prefix match",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"/ask"}},
			isMentioned: false,
			content:     "/ask hello",
			wantRespond: true,
			wantContent: "hello",
		},
		{
			name:        "prefix no match - not mentioned",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"/ask"}},
			isMentioned: false,
			content:     "hello world",
			wantRespond: false,
			wantContent: "hello world",
		},
		{
			name:        "prefix no match - but mentioned",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"/ask"}},
			isMentioned: true,
			content:     "hello world",
			wantRespond: true,
			wantContent: "hello world",
		},
		{
			name:        "multiple prefixes - second matches",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"/ask", "/bot"}},
			isMentioned: false,
			content:     "/bot help me",
			wantRespond: true,
			wantContent: "help me",
		},
		{
			name:        "mention_only with prefixes - mentioned overrides",
			gt:          config.GroupTriggerConfig{MentionOnly: true, Prefixes: []string{"/ask"}},
			isMentioned: true,
			content:     "hello",
			wantRespond: true,
			wantContent: "hello",
		},
		{
			name:        "mention_only with prefixes - not mentioned, no prefix",
			gt:          config.GroupTriggerConfig{MentionOnly: true, Prefixes: []string{"/ask"}},
			isMentioned: false,
			content:     "hello",
			wantRespond: false,
			wantContent: "hello",
		},
		{
			name:        "empty prefix in list is skipped",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"", "/ask"}},
			isMentioned: false,
			content:     "/ask test",
			wantRespond: true,
			wantContent: "test",
		},
		{
			name:        "prefix strips leading whitespace after prefix",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"/ask "}},
			isMentioned: false,
			content:     "/ask hello",
			wantRespond: true,
			wantContent: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := NewBaseChannel("test", nil, nil, nil, WithGroupTrigger(tt.gt))
			gotRespond, gotContent := ch.ShouldRespondInGroup(tt.isMentioned, tt.content)
			if gotRespond != tt.wantRespond {
				t.Errorf("ShouldRespondInGroup() respond = %v, want %v", gotRespond, tt.wantRespond)
			}
			if gotContent != tt.wantContent {
				t.Errorf("ShouldRespondInGroup() content = %q, want %q", gotContent, tt.wantContent)
			}
		})
	}
}

func TestIsAllowedSender(t *testing.T) {
	tests := []struct {
		name      string
		allowList []string
		sender    bus.SenderInfo
		want      bool
	}{
		{
			name:      "empty allowlist denies all",
			allowList: nil,
			sender:    bus.SenderInfo{PlatformID: "anyone"},
			want:      false,
		},
		{
			name:      "wildcard explicitly allows everyone",
			allowList: []string{"*"},
			sender:    bus.SenderInfo{PlatformID: "anyone"},
			want:      true,
		},
		{
			name:      "numeric ID matches PlatformID",
			allowList: []string{"123456"},
			sender: bus.SenderInfo{
				Platform:    "telegram",
				PlatformID:  "123456",
				CanonicalID: "telegram:123456",
			},
			want: true,
		},
		{
			name:      "canonical format matches",
			allowList: []string{"telegram:123456"},
			sender: bus.SenderInfo{
				Platform:    "telegram",
				PlatformID:  "123456",
				CanonicalID: "telegram:123456",
			},
			want: true,
		},
		{
			name:      "canonical format wrong platform",
			allowList: []string{"discord:123456"},
			sender: bus.SenderInfo{
				Platform:    "telegram",
				PlatformID:  "123456",
				CanonicalID: "telegram:123456",
			},
			want: false,
		},
		{
			name:      "@username matches",
			allowList: []string{"@alice"},
			sender: bus.SenderInfo{
				Platform:    "telegram",
				PlatformID:  "123456",
				CanonicalID: "telegram:123456",
				Username:    "alice",
			},
			want: true,
		},
		{
			name:      "compound id|username matches by ID",
			allowList: []string{"123456|alice"},
			sender: bus.SenderInfo{
				Platform:    "telegram",
				PlatformID:  "123456",
				CanonicalID: "telegram:123456",
				Username:    "alice",
			},
			want: true,
		},
		{
			name:      "non matching sender denied",
			allowList: []string{"654321"},
			sender: bus.SenderInfo{
				Platform:    "telegram",
				PlatformID:  "123456",
				CanonicalID: "telegram:123456",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := NewBaseChannel("test", nil, nil, tt.allowList)
			if got := ch.IsAllowedSender(tt.sender); got != tt.want {
				t.Fatalf("IsAllowedSender(%+v) = %v, want %v", tt.sender, got, tt.want)
			}
		})
	}
}

func TestHandleInboundContext_PublishesNormalizedContext(t *testing.T) {
	tests := []struct {
		name       string
		inbound    bus.InboundContext
		wantChat   string
		wantSender string
	}{
		{
			name: "direct uses sender as peer",
			inbound: bus.InboundContext{
				Channel:   "test",
				ChatID:    "chat-1",
				ChatType:  "direct",
				SenderID:  "user-1",
				MessageID: "msg-1",
			},
			wantChat:   "chat-1",
			wantSender: "user-1",
		},
		{
			name: "group uses chat as peer",
			inbound: bus.InboundContext{
				Channel:   "test",
				ChatID:    "group-1",
				ChatType:  "group",
				SenderID:  "user-2",
				MessageID: "msg-2",
			},
			wantChat:   "group-1",
			wantSender: "user-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgBus := bus.NewMessageBus()
			defer msgBus.Close()

			ch := NewBaseChannel("test", nil, msgBus, []string{"*"})
			ch.HandleInboundContext(context.Background(), tt.inbound.ChatID, "hello", nil, tt.inbound)

			msg := <-msgBus.InboundChan()
			if msg.ChatID != tt.wantChat {
				t.Fatalf("ChatID = %q, want %q", msg.ChatID, tt.wantChat)
			}
			if msg.SenderID != tt.wantSender {
				t.Fatalf("SenderID = %q, want %q", msg.SenderID, tt.wantSender)
			}
			if msg.Context.ChatType != tt.inbound.ChatType {
				t.Fatalf("ChatType = %q, want %q", msg.Context.ChatType, tt.inbound.ChatType)
			}
		})
	}
}

func TestHandleInboundContext_DeduplicatesPlatformMessage(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	ch := NewBaseChannel("test", nil, msgBus, []string{"*"})
	inbound := bus.InboundContext{
		Channel:   "test",
		Account:   "bot-1",
		ChatID:    "chat-1",
		ChatType:  "direct",
		SenderID:  "user-1",
		MessageID: "msg-1",
	}

	if err := ch.HandleInboundContext(context.Background(), "chat-1", "hello", nil, inbound); err != nil {
		t.Fatalf("first HandleInboundContext() error = %v", err)
	}
	if err := ch.HandleInboundContext(context.Background(), "chat-1", "hello", nil, inbound); err != nil {
		t.Fatalf("duplicate HandleInboundContext() error = %v", err)
	}

	select {
	case msg := <-msgBus.InboundChan():
		if msg.MessageID != "msg-1" {
			t.Fatalf("MessageID = %q, want msg-1", msg.MessageID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for inbound message")
	}
	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("unexpected duplicate inbound message: %+v", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHandleInboundContext_MessageIDIsScopedByChatAndAccount(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	ch := NewBaseChannel("test", nil, msgBus, []string{"*"})
	base := bus.InboundContext{
		Channel:   "test",
		Account:   "bot-1",
		ChatID:    "chat-1",
		ChatType:  "direct",
		SenderID:  "user-1",
		MessageID: "msg-1",
	}
	if err := ch.HandleInboundContext(context.Background(), base.ChatID, "first", nil, base); err != nil {
		t.Fatalf("first HandleInboundContext() error = %v", err)
	}

	otherChat := base
	otherChat.ChatID = "chat-2"
	if err := ch.HandleInboundContext(context.Background(), otherChat.ChatID, "second", nil, otherChat); err != nil {
		t.Fatalf("other chat HandleInboundContext() error = %v", err)
	}
	otherAccount := base
	otherAccount.Account = "bot-2"
	if err := ch.HandleInboundContext(context.Background(), base.ChatID, "third", nil, otherAccount); err != nil {
		t.Fatalf("other account HandleInboundContext() error = %v", err)
	}

	want := map[string]bool{"first": true, "second": true, "third": true}
	for i := 0; i < 3; i++ {
		select {
		case msg := <-msgBus.InboundChan():
			if !want[msg.Content] {
				t.Fatalf("unexpected message content %q", msg.Content)
			}
			delete(want, msg.Content)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for remaining messages: %v", want)
		}
	}
}

func TestInboundDeduplicator_ReleaseAllowsRetry(t *testing.T) {
	var dedup inboundDeduplicator
	now := time.Now()
	if !dedup.reserve("key", now) {
		t.Fatal("first reserve() = false, want true")
	}
	if dedup.reserve("key", now) {
		t.Fatal("duplicate reserve() = true, want false")
	}
	dedup.release("key")
	if !dedup.reserve("key", now) {
		t.Fatal("reserve() after release = false, want true")
	}
}
