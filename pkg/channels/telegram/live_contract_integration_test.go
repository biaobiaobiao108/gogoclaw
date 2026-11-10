//go:build integration

package telegram

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

func TestLiveContract_SendMessage(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("GOGOCLAW_LIVE_TELEGRAM_BOT_TOKEN"))
	chatID := strings.TrimSpace(os.Getenv("GOGOCLAW_LIVE_TELEGRAM_CHAT_ID"))
	if token == "" || chatID == "" {
		t.Skip("set GOGOCLAW_LIVE_TELEGRAM_BOT_TOKEN and GOGOCLAW_LIVE_TELEGRAM_CHAT_ID to send one live message")
	}

	messageBus := bus.NewMessageBus()
	defer messageBus.Close()
	channel, err := NewTelegramChannel(
		&config.Channel{Type: config.ChannelTelegram, Enabled: true},
		&config.TelegramSettings{Token: *config.NewSecureString(token)},
		messageBus,
	)
	if err != nil {
		t.Fatal(err)
	}
	channel.SetRunning(true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	content := fmt.Sprintf("GoGoClaw Telegram adapter live contract: %s", time.Now().UTC().Format(time.RFC3339))
	ids, err := channel.Send(ctx, bus.OutboundMessage{ChatID: chatID, Content: content})
	if err != nil {
		t.Fatalf("Telegram sendMessage failed: %v", err)
	}
	if len(ids) != 1 || ids[0] == "" {
		t.Fatalf("Telegram returned message IDs %v, want one non-empty ID", ids)
	}
	t.Logf("Telegram accepted one message with ID %s", ids[0])
}
