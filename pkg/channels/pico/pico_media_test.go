package pico

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/channels"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
)

func TestSendMedia_ResolvesMediaBeforeDelivery(t *testing.T) {
	ch := newTestPicoChannel(t)
	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	localPath := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(localPath, []byte("attachment body"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	ref, err := store.Store(localPath, media.MediaMeta{
		Filename:    "report.txt",
		ContentType: "text/plain",
	}, "test-scope")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	closedConn := &picoConn{id: "closed", sessionID: "sess-1"}
	closedConn.closed.Store(true)
	ch.addConnForTest(closedConn)

	_, err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "pico:sess-1",
		Parts: []bus.MediaPart{{
			Ref:         ref,
			Type:        "file",
			Filename:    "report.txt",
			ContentType: "text/plain",
		}},
	})
	if !errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v, want ErrSendFailed", err)
	}
}

func TestSendMedia_DismissesTrackedToolFeedbackMessage(t *testing.T) {
	ch := newTestPicoChannel(t)
	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	localPath := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(localPath, []byte("attachment body"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	ref, err := store.Store(localPath, media.MediaMeta{
		Filename:    "report.txt",
		ContentType: "text/plain",
	}, "test-scope")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	ch.RecordToolFeedbackMessage("pico:sess-1", "msg-progress", "🔧 `read_file`")

	var deleted struct {
		chatID    string
		messageID string
	}
	ch.deleteMessageFn = func(_ context.Context, chatID string, messageID string) error {
		deleted.chatID = chatID
		deleted.messageID = messageID
		return nil
	}

	_, err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "pico:sess-1",
		Parts: []bus.MediaPart{{
			Ref:         ref,
			Type:        "file",
			Filename:    "report.txt",
			ContentType: "text/plain",
		}},
	})
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}

	select {
	case msg := <-received:
		if msg.Type != TypeMessageCreate {
			t.Fatalf("message type = %q, want %q", msg.Type, TypeMessageCreate)
		}
	case <-time.After(time.Second):
		t.Fatal("expected media message to be delivered")
	}

	if deleted.chatID != "pico:sess-1" || deleted.messageID != "msg-progress" {
		t.Fatalf("unexpected delete target: %+v", deleted)
	}
	if _, ok := ch.currentToolFeedbackMessage("pico:sess-1"); ok {
		t.Fatal("expected tracked tool feedback to be cleared after media delivery")
	}
}

func TestSendMedia_IncludesCaptionAndAttachmentsInSinglePayload(t *testing.T) {
	ch := newTestPicoChannel(t)
	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	localPath := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(localPath, []byte("png-body"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	ref, err := store.Store(localPath, media.MediaMeta{
		Filename:    "photo.png",
		ContentType: "image/png",
	}, "test-scope")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	_, err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "pico:sess-1",
		Parts: []bus.MediaPart{{
			Ref:         ref,
			Type:        "image",
			Filename:    "photo.png",
			ContentType: "image/png",
			Caption:     "recipe translation",
		}},
	})
	if err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}

	select {
	case msg := <-received:
		if msg.Type != TypeMessageCreate {
			t.Fatalf("message type = %q, want %q", msg.Type, TypeMessageCreate)
		}
		payload := msg.Payload
		if got := payload[PayloadKeyContent]; got != "recipe translation" {
			t.Fatalf("content = %#v, want %q", got, "recipe translation")
		}
		rawAttachments, ok := payload["attachments"].([]any)
		if !ok || len(rawAttachments) != 1 {
			t.Fatalf("attachments = %#v, want 1 attachment", payload["attachments"])
		}
		attachment, ok := rawAttachments[0].(map[string]any)
		if !ok {
			t.Fatalf("attachment = %#v, want map", rawAttachments[0])
		}
		if got := attachment["type"]; got != "image" {
			t.Fatalf("attachment type = %#v, want image", got)
		}
		if got := attachment["filename"]; got != "photo.png" {
			t.Fatalf("attachment filename = %#v, want photo.png", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected media payload to be delivered")
	}
}

func TestPicoDownloadURLForRef(t *testing.T) {
	got, err := picoDownloadURLForRef("media://attachment-1")
	if err != nil {
		t.Fatalf("picoDownloadURLForRef() error = %v", err)
	}
	if got != "/pico/media/attachment-1" {
		t.Fatalf("picoDownloadURLForRef() = %q, want %q", got, "/pico/media/attachment-1")
	}
}

func TestHandleMediaDownload_ServesStoredFile(t *testing.T) {
	ch := newTestPicoChannel(t)
	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	localPath := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(localPath, []byte("downloadable"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	ref, err := store.Store(localPath, media.MediaMeta{
		Filename:    "report.txt",
		ContentType: "text/plain",
	}, "test-scope")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	refID := strings.TrimPrefix(ref, "media://")
	req := httptest.NewRequest("GET", "/pico/media/"+refID, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	ch.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "downloadable" {
		t.Fatalf("body = %q, want %q", body, "downloadable")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain" {
		t.Fatalf("Content-Type = %q, want %q", got, "text/plain")
	}
}

func TestParsePicoAttachmentPayloadRejectsClientPathsAndCrossSessionURLs(t *testing.T) {
	resolve := func(sessionID, attachmentID string) (string, int64, error) {
		if sessionID != "session-1" || attachmentID != "managed.png" {
			t.Fatalf("resolve(%q, %q)", sessionID, attachmentID)
		}
		return `/managed/attachments/managed.png`, 12, nil
	}

	if _, _, err := parsePicoAttachmentPayload("session-1", map[string]any{
		"attachments": []any{map[string]any{
			"id": "managed.png", "path": `C:\secret.txt`,
		}},
	}, resolve); err == nil {
		t.Fatal("client filesystem path was accepted")
	}
	if _, _, err := parsePicoAttachmentPayload("session-1", map[string]any{
		"attachments": []any{map[string]any{
			"id": "../../secret.txt", "type": "file", "filename": "secret.txt",
		}},
	}, resolve); err == nil {
		t.Fatal("attachment path traversal was accepted")
	}
	if _, _, err := parsePicoAttachmentPayload("session-1", map[string]any{
		"attachments": []any{map[string]any{
			"url": "/api/attachments/session-2/managed.png", "type": "image",
		}},
	}, resolve); err == nil {
		t.Fatal("cross-session attachment URL was accepted")
	}
}

func TestParsePicoAttachmentPayloadUsesCanonicalManagedURL(t *testing.T) {
	payload := map[string]any{
		"attachments": []any{
			map[string]any{
				"id": "managed.png", "type": "image",
				"url":      "https://attacker.example/file.png",
				"filename": "managed.png", "content_type": "image/png",
			},
			map[string]any{
				"id": "diagram.svg", "type": "image",
				"filename": "diagram.svg", "content_type": "image/svg+xml",
			},
		},
	}
	resolve := func(sessionID, attachmentID string) (string, int64, error) {
		if sessionID != "session-1" {
			t.Fatalf("session = %q", sessionID)
		}
		return filepath.Join(t.TempDir(), attachmentID), 12, nil
	}
	_, attachments, err := parsePicoAttachmentPayload("session-1", payload, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 2 {
		t.Fatalf("attachments = %+v", attachments)
	}
	if attachments[0].URL != "/api/attachments/session-1/managed.png" {
		t.Fatalf("canonical URL = %q", attachments[0].URL)
	}
	if attachments[0].Type != "image" || attachments[1].Type != "file" {
		t.Fatalf("attachment types = %+v", attachments)
	}
}
