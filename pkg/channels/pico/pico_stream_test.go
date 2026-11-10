package pico

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

func TestBeginStream_CreatesAndUpdatesSameMessage(t *testing.T) {
	ch := newTestPicoChannel(t)
	ch.config.Streaming = config.StreamingConfig{
		Enabled:         true,
		ThrottleSeconds: 1,
		MinGrowthChars:  1,
	}
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	streamer, err := ch.BeginStream(context.Background(), "pico:sess-1")
	if err != nil {
		t.Fatalf("BeginStream() error = %v", err)
	}
	if setter, ok := streamer.(interface{ SetModelName(modelName string) }); ok {
		setter.SetModelName("gpt-5.4")
	}
	if err := streamer.Update(context.Background(), "hello"); err != nil {
		t.Fatalf("Update(first) error = %v", err)
	}
	first := mustReceivePicoMessage(t, received)
	if first.Type != TypeMessageCreate {
		t.Fatalf("first type = %q, want %q", first.Type, TypeMessageCreate)
	}
	msgID, _ := first.Payload["message_id"].(string)
	if msgID == "" {
		t.Fatalf("first message_id = %#v, want non-empty", first.Payload["message_id"])
	}
	if got := first.Payload[PayloadKeyContent]; got != "hello" {
		t.Fatalf("first content = %#v, want hello", got)
	}
	if got := first.Payload[PayloadKeyModelName]; got != "gpt-5.4" {
		t.Fatalf("first model_name = %#v, want %q", got, "gpt-5.4")
	}

	rawStreamer := streamer.(*picoStreamer)
	rawStreamer.mu.Lock()
	rawStreamer.lastAt = time.Now().Add(-2 * time.Second)
	rawStreamer.mu.Unlock()
	secondContent := "hello world with enough growth to pass the default streaming threshold"
	if err := streamer.Update(context.Background(), secondContent); err != nil {
		t.Fatalf("Update(second) error = %v", err)
	}
	sequence := mustReceivePicoContentSequence(t, received, secondContent)
	if len(sequence) < 2 {
		t.Fatalf("paced updates = %d, want multiple frames", len(sequence))
	}
	second := sequence[len(sequence)-1]
	if second.Type != TypeMessageUpdate {
		t.Fatalf("second type = %q, want %q", second.Type, TypeMessageUpdate)
	}
	if got := second.Payload["message_id"]; got != msgID {
		t.Fatalf("second message_id = %#v, want %q", got, msgID)
	}
	if got := second.Payload[PayloadKeyContent]; got != secondContent {
		t.Fatalf("second content = %#v, want %q", got, secondContent)
	}
	if got := second.Payload[PayloadKeyModelName]; got != "gpt-5.4" {
		t.Fatalf("second model_name = %#v, want %q", got, "gpt-5.4")
	}
}

func TestBeginStream_DefaultStreamingShowsSmallIncrements(t *testing.T) {
	ch := newTestPicoChannel(t)
	ch.config.Streaming = config.StreamingConfig{Enabled: true}
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	streamer, err := ch.BeginStream(context.Background(), "pico:sess-1")
	if err != nil {
		t.Fatalf("BeginStream() error = %v", err)
	}
	if err := streamer.Update(context.Background(), "h"); err != nil {
		t.Fatalf("Update(first) error = %v", err)
	}
	first := mustReceivePicoMessage(t, received)
	if first.Type != TypeMessageCreate {
		t.Fatalf("first type = %q, want %q", first.Type, TypeMessageCreate)
	}
	msgID, _ := first.Payload["message_id"].(string)
	if msgID == "" {
		t.Fatalf("first message_id = %#v, want non-empty", first.Payload["message_id"])
	}

	if err := streamer.Update(context.Background(), "he"); err != nil {
		t.Fatalf("Update(second) error = %v", err)
	}
	second := mustReceivePicoMessage(t, received)
	if second.Type != TypeMessageUpdate {
		t.Fatalf("second type = %q, want %q", second.Type, TypeMessageUpdate)
	}
	if got := second.Payload["message_id"]; got != msgID {
		t.Fatalf("second message_id = %#v, want %q", got, msgID)
	}
	if got := second.Payload[PayloadKeyContent]; got != "he" {
		t.Fatalf("second content = %#v, want he", got)
	}
}

func TestBeginStream_StreamsReasoningAsThoughtUpdates(t *testing.T) {
	ch := newTestPicoChannel(t)
	ch.config.Streaming = config.StreamingConfig{Enabled: true}
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	streamer, err := ch.BeginStream(context.Background(), "pico:sess-1")
	if err != nil {
		t.Fatalf("BeginStream() error = %v", err)
	}
	reasoningStreamer, ok := streamer.(bus.ReasoningStreamer)
	if !ok {
		t.Fatal("pico stream should support reasoning updates")
	}
	if setter, ok := streamer.(interface{ SetModelName(modelName string) }); ok {
		setter.SetModelName("gpt-5.4-mini")
	}
	if err := reasoningStreamer.UpdateReasoning(context.Background(), "thinking"); err != nil {
		t.Fatalf("UpdateReasoning(first) error = %v", err)
	}
	first := mustReceivePicoMessage(t, received)
	if first.Type != TypeMessageCreate {
		t.Fatalf("first type = %q, want %q", first.Type, TypeMessageCreate)
	}
	msgID, _ := first.Payload["message_id"].(string)
	if msgID == "" {
		t.Fatalf("first message_id = %#v, want non-empty", first.Payload["message_id"])
	}
	if got := first.Payload[PayloadKeyKind]; got != MessageKindThought {
		t.Fatalf("first kind = %#v, want %q", got, MessageKindThought)
	}
	if got := first.Payload[PayloadKeyContent]; got != "thinking" {
		t.Fatalf("first content = %#v, want thinking", got)
	}
	if got := first.Payload[PayloadKeyModelName]; got != "gpt-5.4-mini" {
		t.Fatalf("first model_name = %#v, want %q", got, "gpt-5.4-mini")
	}

	if err := reasoningStreamer.UpdateReasoning(context.Background(), "thinking more"); err != nil {
		t.Fatalf("UpdateReasoning(second) error = %v", err)
	}
	second := mustReceivePicoMessage(t, received)
	if second.Type != TypeMessageUpdate {
		t.Fatalf("second type = %q, want %q", second.Type, TypeMessageUpdate)
	}
	if got := second.Payload["message_id"]; got != msgID {
		t.Fatalf("second message_id = %#v, want %q", got, msgID)
	}
	if got := second.Payload[PayloadKeyKind]; got != MessageKindThought {
		t.Fatalf("second kind = %#v, want %q", got, MessageKindThought)
	}
	if got := second.Payload[PayloadKeyContent]; got != "thinking more" {
		t.Fatalf("second content = %#v, want thinking more", got)
	}
	if got := second.Payload[PayloadKeyModelName]; got != "gpt-5.4-mini" {
		t.Fatalf("second model_name = %#v, want %q", got, "gpt-5.4-mini")
	}
}

func TestBeginStream_PacesLargeReasoningUpdate(t *testing.T) {
	ch := newTestPicoChannel(t)
	ch.config.Streaming = config.StreamingConfig{Enabled: true}
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	streamer, err := ch.BeginStream(context.Background(), "pico:sess-1")
	if err != nil {
		t.Fatalf("BeginStream() error = %v", err)
	}
	reasoningStreamer, ok := streamer.(bus.ReasoningStreamer)
	if !ok {
		t.Fatal("pico stream should support reasoning updates")
	}

	content := strings.Repeat("思考内容", 188)
	if err := reasoningStreamer.UpdateReasoning(context.Background(), content); err != nil {
		t.Fatalf("UpdateReasoning() error = %v", err)
	}
	sequence := mustReceivePicoContentSequence(t, received, content)
	if len(sequence) < 5 {
		t.Fatalf("reasoning paced frames = %d, want at least 5", len(sequence))
	}
	previousLen := 0
	for _, message := range sequence {
		got, _ := message.Payload[PayloadKeyContent].(string)
		gotLen := len([]rune(got))
		if gotLen < previousLen {
			t.Fatalf("reasoning content length regressed from %d to %d", previousLen, gotLen)
		}
		previousLen = gotLen
	}
}

func TestBeginStream_ThrottlesIntermediateUpdatesAndFinalFlushes(t *testing.T) {
	ch := newTestPicoChannel(t)
	ch.config.Streaming = config.StreamingConfig{
		Enabled:         true,
		ThrottleSeconds: 60,
		MinGrowthChars:  100,
	}
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	streamer, err := ch.BeginStream(context.Background(), "pico:sess-1")
	if err != nil {
		t.Fatalf("BeginStream() error = %v", err)
	}
	if err := streamer.Update(context.Background(), "first"); err != nil {
		t.Fatalf("Update(first) error = %v", err)
	}
	if err := streamer.Update(context.Background(), "first plus short growth"); err != nil {
		t.Fatalf("Update(throttled) error = %v", err)
	}
	if err := streamer.Update(context.Background(), "first"+strings.Repeat("x", 120)); err != nil {
		t.Fatalf("Update(enough growth too soon) error = %v", err)
	}

	first := mustReceivePicoMessage(t, received)
	if first.Type != TypeMessageCreate {
		t.Fatalf("first type = %q, want %q", first.Type, TypeMessageCreate)
	}
	msgID, _ := first.Payload["message_id"].(string)
	assertNoPicoMessage(t, received)

	rawStreamer := streamer.(*picoStreamer)
	rawStreamer.mu.Lock()
	rawStreamer.lastAt = time.Now().Add(-61 * time.Second)
	rawStreamer.mu.Unlock()
	if err := streamer.Update(context.Background(), "first plus small growth"); err != nil {
		t.Fatalf("Update(enough time too little growth) error = %v", err)
	}
	assertNoPicoMessage(t, received)

	if err := streamer.Finalize(context.Background(), "first plus final text"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	finalContent := "first plus final text"
	sequence := mustReceivePicoContentSequence(t, received, finalContent)
	if len(sequence) < 2 {
		t.Fatalf("final paced updates = %d, want multiple frames", len(sequence))
	}
	final := sequence[len(sequence)-1]
	if final.Type != TypeMessageUpdate {
		t.Fatalf("final type = %q, want %q", final.Type, TypeMessageUpdate)
	}
	if got := final.Payload["message_id"]; got != msgID {
		t.Fatalf("final message_id = %#v, want %q", got, msgID)
	}
	if got := final.Payload[PayloadKeyContent]; got != finalContent {
		t.Fatalf("final content = %#v, want final text", got)
	}
	assertNoPicoMessage(t, received)
}

func TestBeginStream_FinalizeIncludesContextUsage(t *testing.T) {
	ch := newTestPicoChannel(t)
	ch.config.Streaming = config.StreamingConfig{
		Enabled:         true,
		ThrottleSeconds: 0,
		MinGrowthChars:  0,
	}
	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(context.Background())

	clientConn, received, cleanup := newTestPicoWebSocket(t)
	defer cleanup()
	ch.addConnForTest(&picoConn{id: "conn-1", conn: clientConn, sessionID: "sess-1"})

	streamer, err := ch.BeginStream(context.Background(), "pico:sess-1")
	if err != nil {
		t.Fatalf("BeginStream() error = %v", err)
	}
	if setter, ok := streamer.(interface{ SetModelName(modelName string) }); ok {
		setter.SetModelName("gpt-5.4")
	}
	if err := streamer.Update(context.Background(), "partial"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	first := mustReceivePicoMessage(t, received)
	msgID, _ := first.Payload["message_id"].(string)

	contextStreamer, ok := streamer.(interface {
		FinalizeWithContext(ctx context.Context, content string, usage *bus.ContextUsage) error
	})
	if !ok {
		t.Fatal("streamer should support FinalizeWithContext")
	}
	if err := contextStreamer.FinalizeWithContext(context.Background(), "final", &bus.ContextUsage{
		UsedTokens:        10,
		TotalTokens:       100,
		HistoryTokens:     5,
		CompressAtTokens:  80,
		SummarizeAtTokens: 60,
		UsedPercent:       10,
	}); err != nil {
		t.Fatalf("FinalizeWithContext() error = %v", err)
	}

	final := mustReceivePicoMessage(t, received)
	if final.Type != TypeMessageUpdate {
		t.Fatalf("final type = %q, want %q", final.Type, TypeMessageUpdate)
	}
	if got := final.Payload["message_id"]; got != msgID {
		t.Fatalf("final message_id = %#v, want %q", got, msgID)
	}
	if got := final.Payload[PayloadKeyModelName]; got != "gpt-5.4" {
		t.Fatalf("final model_name = %#v, want %q", got, "gpt-5.4")
	}
	rawUsage, ok := final.Payload["context_usage"].(map[string]any)
	if !ok {
		t.Fatalf("final context_usage = %#v, want map", final.Payload["context_usage"])
	}
	if got := rawUsage["used_tokens"]; got != float64(10) {
		t.Fatalf("used_tokens = %#v, want 10", got)
	}
	if got := rawUsage["history_tokens"]; got != float64(5) {
		t.Fatalf("history_tokens = %#v, want 5", got)
	}
	if got := rawUsage["summarize_at_tokens"]; got != float64(60) {
		t.Fatalf("summarize_at_tokens = %#v, want 60", got)
	}
}
