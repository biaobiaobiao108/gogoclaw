//go:build integration

package photon

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	photonv1 "github.com/biaobiaobiao108/gogoclaw/pkg/channels/photon/internal/photon/imessage/v1"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
)

func TestLiveContract_PhotonReadOnly(t *testing.T) {
	projectID := firstNonEmpty(os.Getenv("GOGOCLAW_LIVE_PHOTON_PROJECT_ID"), os.Getenv("SPECTRUM_PROJECT_ID"))
	projectSecret := firstNonEmpty(os.Getenv("GOGOCLAW_LIVE_PHOTON_PROJECT_SECRET"), os.Getenv("SPECTRUM_PROJECT_SECRET"))
	if projectID == "" || projectSecret == "" {
		t.Skip("set GOGOCLAW_LIVE_PHOTON_PROJECT_ID and GOGOCLAW_LIVE_PHOTON_PROJECT_SECRET")
	}

	settings := &config.PhotonSettings{
		ProjectID:     strings.TrimSpace(projectID),
		ProjectSecret: *config.NewSecureString(strings.TrimSpace(projectSecret)),
	}
	client := newPhotonClient(settings)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Photon token or line discovery failed: %v", err)
	}
	defer client.Close()

	lines := client.Lines()
	if len(lines) == 0 {
		t.Fatal("Photon project returned no usable iMessage lines")
	}
	t.Logf("Photon authenticated for %d iMessage line(s)", len(lines))

	line := lines[0]
	var events int
	if _, err := client.CatchUp(ctx, line.ID, 0, func(uint64, *photonv1.MessageChangeEvent) error {
		events++
		return nil
	}); err != nil {
		t.Fatalf("Photon catch-up RPC failed: %v", err)
	}
	t.Logf("Photon catch-up read completed with %d message event(s)", events)

	streamCtx, streamCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer streamCancel()
	err := client.SubscribeMessages(streamCtx, line.ID, 0, func(uint64, *photonv1.MessageChangeEvent) error {
		return nil
	})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("Photon live subscription failed: %v", err)
	}
}

func TestLiveContract_PhotonSendEditUnsend(t *testing.T) {
	projectID := firstNonEmpty(os.Getenv("GOGOCLAW_LIVE_PHOTON_PROJECT_ID"), os.Getenv("SPECTRUM_PROJECT_ID"))
	projectSecret := firstNonEmpty(os.Getenv("GOGOCLAW_LIVE_PHOTON_PROJECT_SECRET"), os.Getenv("SPECTRUM_PROJECT_SECRET"))
	chatID := strings.TrimSpace(os.Getenv("GOGOCLAW_LIVE_PHOTON_CHAT_ID"))
	if projectID == "" || projectSecret == "" || chatID == "" {
		t.Skip("set Photon project credentials and GOGOCLAW_LIVE_PHOTON_CHAT_ID to send one disposable message")
	}

	settings := &config.PhotonSettings{
		ProjectID:     strings.TrimSpace(projectID),
		ProjectSecret: *config.NewSecureString(strings.TrimSpace(projectSecret)),
	}
	cfg := config.DefaultConfig()
	cfg.Channels[config.ChannelPhoton].AllowFrom = config.FlexibleStringSlice{"*"}
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()
	client := newPhotonClient(settings)
	ch, err := NewPhotonChannel(
		"photon-live",
		cfg.Channels[config.ChannelPhoton],
		settings,
		cfg,
		messageBus,
		client,
		filepath.Join(t.TempDir(), "photon-state.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Photon channel start failed: %v", err)
	}
	defer ch.Stop(context.Background())
	stopTyping, err := ch.StartTyping(ctx, chatID)
	if err != nil {
		t.Fatalf("Photon typing start failed: %v", err)
	}
	stopTyping()
	if err := ch.api.MarkRead(ctx, chatID); err != nil {
		t.Fatalf("Photon mark-read failed: %v", err)
	}

	content := fmt.Sprintf("GoGoClaw Photon live contract: %s", time.Now().UTC().Format(time.RFC3339))
	ids, err := ch.Send(ctx, bus.OutboundMessage{
		ID:      channels.StableID("photon-live", time.Now().UTC().Format(time.RFC3339Nano)),
		ChatID:  chatID,
		Content: content,
	})
	if err != nil {
		t.Fatalf("Photon send failed: %v", err)
	}
	if len(ids) != 1 || ids[0] == "" {
		t.Fatalf("Photon returned message IDs %v, want one non-empty ID", ids)
	}
	messageID := ids[0]
	cleanupID := messageID
	defer func() {
		if cleanupID == "" {
			return
		}
		if err := ch.DeleteMessage(context.Background(), chatID, cleanupID); err != nil {
			t.Logf("Photon cleanup unsend failed: %v", err)
		}
	}()

	if err := ch.EditMessage(ctx, chatID, messageID, content+" (edited)"); err != nil {
		t.Fatalf("Photon edit failed: %v", err)
	}
	if err := ch.DeleteMessage(ctx, chatID, messageID); err != nil {
		t.Fatalf("Photon unsend failed: %v", err)
	}
	cleanupID = ""
}

func TestLiveContract_PhotonSendMedia(t *testing.T) {
	projectID := firstNonEmpty(os.Getenv("GOGOCLAW_LIVE_PHOTON_PROJECT_ID"), os.Getenv("SPECTRUM_PROJECT_ID"))
	projectSecret := firstNonEmpty(os.Getenv("GOGOCLAW_LIVE_PHOTON_PROJECT_SECRET"), os.Getenv("SPECTRUM_PROJECT_SECRET"))
	chatID := strings.TrimSpace(os.Getenv("GOGOCLAW_LIVE_PHOTON_CHAT_ID"))
	if projectID == "" || projectSecret == "" || chatID == "" {
		t.Skip("set Photon project credentials and GOGOCLAW_LIVE_PHOTON_CHAT_ID to send one disposable image")
	}

	settings := &config.PhotonSettings{
		ProjectID:     strings.TrimSpace(projectID),
		ProjectSecret: *config.NewSecureString(strings.TrimSpace(projectSecret)),
	}
	cfg := config.DefaultConfig()
	cfg.Channels[config.ChannelPhoton].AllowFrom = config.FlexibleStringSlice{"*"}
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()
	client := newPhotonClient(settings)
	ch, err := NewPhotonChannel(
		"photon-live",
		cfg.Channels[config.ChannelPhoton],
		settings,
		cfg,
		messageBus,
		client,
		filepath.Join(t.TempDir(), "photon-state.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	ch.SetMediaStore(media.NewFileMediaStore())
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Photon channel start failed: %v", err)
	}
	defer ch.Stop(context.Background())

	pngData, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gogoclaw-live.png")
	if err := os.WriteFile(path, pngData, 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := ch.GetMediaStore().Store(path, media.MediaMeta{
		Filename:      "gogoclaw-live.png",
		ContentType:   "image/png",
		CleanupPolicy: media.CleanupPolicyDeleteOnCleanup,
	}, "photon-live")
	if err != nil {
		t.Fatal(err)
	}

	ids, err := ch.SendMedia(ctx, bus.OutboundMediaMessage{
		ID:     channels.StableID("photon-live-media", time.Now().UTC().Format(time.RFC3339Nano)),
		ChatID: chatID,
		Parts: []bus.MediaPart{{
			Type:        "image",
			Ref:         ref,
			Filename:    "gogoclaw-live.png",
			ContentType: "image/png",
		}},
	})
	if err != nil {
		t.Fatalf("Photon media send failed: %v", err)
	}
	if len(ids) != 1 || ids[0] == "" {
		t.Fatalf("Photon returned media message IDs %v, want one non-empty ID", ids)
	}
	if err := ch.DeleteMessage(ctx, chatID, ids[0]); err != nil {
		t.Fatalf("Photon media unsend failed: %v", err)
	}
}
