package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

func TestSendUsesConfiguredBotAPIContract(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("request method = %s, want POST", r.Method)
		}
		if got, want := r.URL.Path, "/bot"+testToken+"/sendMessage"; got != want {
			t.Errorf("request path = %q, want %q", got, want)
		}
		var body struct {
			ChatID    json.RawMessage `json:"chat_id"`
			Text      string          `json:"text"`
			ParseMode string          `json:"parse_mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if got := strings.Trim(string(body.ChatID), "\""); got != "12345" {
			t.Errorf("chat_id = %q, want 12345", got)
		}
		if body.Text != "Hello <b>world</b>" {
			t.Errorf("text = %q, want HTML-formatted message", body.Text)
		}
		if body.ParseMode != "HTML" {
			t.Errorf("parse_mode = %q, want HTML", body.ParseMode)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":314}}`))
	}))
	defer server.Close()

	messageBus := bus.NewMessageBus()
	defer messageBus.Close()
	channel, err := NewTelegramChannel(
		&config.Channel{Type: config.ChannelTelegram, Enabled: true},
		&config.TelegramSettings{Token: *config.NewSecureString(testToken), BaseURL: server.URL},
		messageBus,
	)
	if err != nil {
		t.Fatal(err)
	}
	channel.SetRunning(true)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ids, err := channel.Send(ctx, bus.OutboundMessage{ChatID: "12345", Content: "Hello **world**"})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || len(ids) != 1 || ids[0] != "314" {
		t.Fatalf("requests = %d, message IDs = %v; want one request and [314]", requests.Load(), ids)
	}
}
