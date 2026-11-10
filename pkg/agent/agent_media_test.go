package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func TestResolveMediaRefs_ImageInjectsPathTag(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	// Create a minimal valid PNG (8-byte header is enough for filetype detection)
	pngPath := filepath.Join(dir, "test.png")
	// PNG magic: 0x89 P N G \r \n 0x1A \n + minimal IHDR
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, // IHDR length
		0x49, 0x48, 0x44, 0x52, // "IHDR"
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02, // 1x1 RGB
		0x00, 0x00, 0x00, // no interlace
		0x90, 0x77, 0x53, 0xDE, // CRC
	}
	if err := os.WriteFile(pngPath, pngHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := store.Store(pngPath, media.MediaMeta{}, "test")
	if err != nil {
		t.Fatal(err)
	}

	messages := []providers.Message{
		{Role: "user", Content: "describe this", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 0)

	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media (images use path tags), got %d", len(result[0].Media))
	}
	localPath, _, _ := store.ResolveWithMeta(ref)
	expectedContent := "describe this [image:" + localPath + "]"
	if result[0].Content != expectedContent {
		t.Fatalf("expected content %q, got %q", expectedContent, result[0].Content)
	}
}

func TestResolveMediaRefs_ToolRoleImageAppendedAsUserMessage(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	pngPath := filepath.Join(dir, "tool-result.png")
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, // IHDR length
		0x49, 0x48, 0x44, 0x52, // "IHDR"
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02, // 1x1 RGB
		0x00, 0x00, 0x00, // no interlace
		0x90, 0x77, 0x53, 0xDE, // CRC
	}
	if err := os.WriteFile(pngPath, pngHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	ref, _ := store.Store(pngPath, media.MediaMeta{}, "test")

	messages := []providers.Message{
		toolResultPromptMessage("Image loaded", "call_tool_result_image", []string{ref}),
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 0)

	// Tool message should have path tag but no base64
	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media in tool message, got %d", len(result[0].Media))
	}
	localPath, _, _ := store.ResolveWithMeta(ref)
	if !strings.Contains(result[0].Content, "[image:"+localPath+"]") {
		t.Fatalf("expected image path tag in tool content, got %q", result[0].Content)
	}

	// A synthetic user message with base64 should follow
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (tool + synthetic user), got %d", len(result))
	}
	if result[1].Role != "user" {
		t.Fatalf("expected synthetic message role=user, got %q", result[1].Role)
	}
	if len(result[1].Media) != 1 {
		t.Fatalf("expected 1 base64 media in synthetic user message, got %d", len(result[1].Media))
	}
	if !strings.HasPrefix(result[1].Media[0], "data:image/png;base64,") {
		t.Fatalf("expected data:image/png;base64, prefix, got %q", result[1].Media[0][:40])
	}
}

func TestResolveMediaRefs_HistoricalToolRoleImageDoesNotAppendAsUserMessage(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	pngPath := filepath.Join(dir, "historical-tool-result.png")
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00,
		0x90, 0x77, 0x53, 0xDE,
	}
	if err := os.WriteFile(pngPath, pngHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	ref, _ := store.Store(pngPath, media.MediaMeta{}, "test")

	messages := []providers.Message{
		toolResultPromptMessage("Image loaded", "call_historical_tool_result_image", []string{ref}),
		{Role: "user", Content: "now summarize it in one sentence"},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 1)

	if len(result) != 2 {
		t.Fatalf("expected historical tool message plus current follow-up, got %d messages", len(result))
	}
	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media in historical tool message, got %d", len(result[0].Media))
	}
	localPath, _, _ := store.ResolveWithMeta(ref)
	if !strings.Contains(result[0].Content, "[image:"+localPath+"]") {
		t.Fatalf("expected image path tag in historical tool content, got %q", result[0].Content)
	}
	if result[1].Role != "user" || result[1].Content != "now summarize it in one sentence" {
		t.Fatalf("expected current follow-up user message to remain untouched, got %#v", result[1])
	}
}

func TestResolveMediaRefs_HistoricalAndCurrentToolImagesOnlyRehydrateCurrentTurn(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	historicalPath := filepath.Join(dir, "historical.png")
	currentPath := filepath.Join(dir, "current.png")
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00,
		0x90, 0x77, 0x53, 0xDE,
	}
	if err := os.WriteFile(historicalPath, pngHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath, pngHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	historicalRef, _ := store.Store(historicalPath, media.MediaMeta{}, "test")
	currentRef, _ := store.Store(currentPath, media.MediaMeta{}, "test")

	messages := []providers.Message{
		toolResultPromptMessage("Historical image loaded", "call_hist_image", []string{historicalRef}),
		{Role: "assistant", Content: "Now I will inspect a new image."},
		toolResultPromptMessage("Current image loaded", "call_current_image", []string{currentRef}),
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 1)

	if len(result) != 4 {
		t.Fatalf("expected 4 messages (historical tool + assistant + current tool + synthetic user), "+
			"got %d", len(result))
	}
	if result[0].Role != "tool" || len(result[0].Media) != 0 {
		t.Fatalf("historical tool message = %#v, want path-tagged tool without media", result[0])
	}
	if !strings.Contains(result[0].Content, "[image:"+historicalPath+"]") {
		t.Fatalf(
			"expected historical tool content to contain %q, got %q",
			"[image:"+historicalPath+"]",
			result[0].Content,
		)
	}
	if result[1].Role != "assistant" || result[1].Content != "Now I will inspect a new image." {
		t.Fatalf("assistant boundary message = %#v, want untouched assistant", result[1])
	}
	if result[2].Role != "tool" || len(result[2].Media) != 0 {
		t.Fatalf("current tool message = %#v, want path-tagged tool without media", result[2])
	}
	if !strings.Contains(result[2].Content, "[image:"+currentPath+"]") {
		t.Fatalf("expected current tool content to contain %q, got %q", "[image:"+currentPath+"]", result[2].Content)
	}
	if result[3].Role != "user" || len(result[3].Media) != 1 {
		t.Fatalf("synthetic follow-up = %#v, want one-media user message", result[3])
	}
	if !strings.HasPrefix(result[3].Media[0], "data:image/png;base64,") {
		t.Fatalf("expected base64 image in synthetic follow-up, got %q", result[3].Media[0][:40])
	}
}

func TestResolveMediaRefs_MultiToolCallPreservesOrdering(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	// Create image for tool #1
	pngPath := filepath.Join(dir, "loaded.png")
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
	os.WriteFile(pngPath, pngHeader, 0o644)
	imgRef, _ := store.Store(pngPath, media.MediaMeta{}, "test")

	// Simulate: assistant called load_image + read_file, two tool results follow
	messages := []providers.Message{
		{Role: "assistant", Content: "Let me load the image and read the file."},
		toolResultPromptMessage("Image loaded [image: photo]", "call_load_image_multi_tool", []string{imgRef}),
		toolResultPromptMessage("file contents here", "call_read_file_multi_tool", nil),
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 0)

	// assistant, tool#1, tool#2 must remain contiguous — no user in between
	if result[0].Role != "assistant" {
		t.Fatalf("result[0] expected assistant, got %q", result[0].Role)
	}
	if result[1].Role != "tool" {
		t.Fatalf("result[1] expected tool, got %q", result[1].Role)
	}
	if result[2].Role != "tool" {
		t.Fatalf("result[2] expected tool, got %q", result[2].Role)
	}

	// Synthetic user message should come AFTER the tool block
	if len(result) != 4 {
		t.Fatalf("expected 4 messages (assistant + 2 tool + synthetic user), got %d", len(result))
	}
	if result[3].Role != "user" {
		t.Fatalf("result[3] expected user, got %q", result[3].Role)
	}
	if len(result[3].Media) != 1 || !strings.HasPrefix(result[3].Media[0], "data:image/png;base64,") {
		t.Fatal("expected synthetic user message to contain base64 image")
	}
}

func TestResolveMediaRefs_MultipleCurrentToolImagesShareSingleSyntheticFollowUp(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	firstPath := filepath.Join(dir, "first.png")
	secondPath := filepath.Join(dir, "second.png")
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00,
		0x90, 0x77, 0x53, 0xDE,
	}
	if err := os.WriteFile(firstPath, pngHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, pngHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	firstRef, _ := store.Store(firstPath, media.MediaMeta{}, "test")
	secondRef, _ := store.Store(secondPath, media.MediaMeta{}, "test")

	messages := []providers.Message{
		{Role: "assistant", Content: "I loaded two images for comparison."},
		toolResultPromptMessage("First image loaded", "call_first_image", []string{firstRef}),
		toolResultPromptMessage("Second image loaded", "call_second_image", []string{secondRef}),
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 0)

	if len(result) != 4 {
		t.Fatalf("expected assistant + 2 tool results + 1 synthetic follow-up, got %d messages", len(result))
	}
	if result[3].Role != "user" {
		t.Fatalf("synthetic follow-up role = %q, want user", result[3].Role)
	}
	if len(result[3].Media) != 2 {
		t.Fatalf("synthetic follow-up media count = %d, want 2", len(result[3].Media))
	}
	for i, ref := range result[3].Media {
		if !strings.HasPrefix(ref, "data:image/png;base64,") {
			t.Fatalf("synthetic follow-up media[%d] missing data URL prefix: %q", i, ref[:40])
		}
	}
}

func TestResolveMediaRefs_OversizedImageSkipsBase64KeepsPathTag(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	bigPath := filepath.Join(dir, "big.png")
	// Write PNG header + padding to exceed limit
	data := make([]byte, 1024+1) // 1KB + 1 byte
	copy(data, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
	if err := os.WriteFile(bigPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	ref, _ := store.Store(bigPath, media.MediaMeta{}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "hi", Media: []string{ref}},
	}
	// Use a tiny limit (1KB) so the file is oversized
	result := resolveMediaRefs(messages, store, 1024, 0)

	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media (oversized), got %d", len(result[0].Media))
	}
	localPath, _, _ := store.ResolveWithMeta(ref)
	expected := "hi [image:" + localPath + "]"
	if result[0].Content != expected {
		t.Fatalf("expected content %q, got %q", expected, result[0].Content)
	}
}

func TestResolveMediaRefs_UnknownTypeInjectsPath(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	txtPath := filepath.Join(dir, "readme.txt")
	if err := os.WriteFile(txtPath, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, _ := store.Store(txtPath, media.MediaMeta{}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "hi", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 0)

	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media entries, got %d", len(result[0].Media))
	}
	expected := "hi [file:" + txtPath + "]"
	if result[0].Content != expected {
		t.Fatalf("expected content %q, got %q", expected, result[0].Content)
	}
}

func TestResolveMediaRefs_PassesThroughNonMediaRefs(t *testing.T) {
	messages := []providers.Message{
		{Role: "user", Content: "hi", Media: []string{"https://example.com/img.png"}},
	}
	result := resolveMediaRefs(messages, nil, config.DefaultMaxMediaSize, 0)

	if len(result[0].Media) != 1 || result[0].Media[0] != "https://example.com/img.png" {
		t.Fatalf("expected passthrough of non-media:// URL, got %v", result[0].Media)
	}
}

func TestResolveMediaRefs_DoesNotMutateOriginal(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "test.png")
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
	os.WriteFile(pngPath, pngHeader, 0o644)
	ref, _ := store.Store(pngPath, media.MediaMeta{}, "test")

	original := []providers.Message{
		{Role: "user", Content: "hi", Media: []string{ref}},
	}
	originalRef := original[0].Media[0]

	resolveMediaRefs(original, store, config.DefaultMaxMediaSize, 0)

	if original[0].Media[0] != originalRef {
		t.Fatal("resolveMediaRefs mutated original message slice")
	}
}

func TestResolveMediaRefs_UsesMetaContentType(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	// File with JPEG content but stored with explicit content type
	jpegPath := filepath.Join(dir, "photo")
	jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0} // JPEG magic bytes
	os.WriteFile(jpegPath, jpegHeader, 0o644)
	ref, _ := store.Store(jpegPath, media.MediaMeta{ContentType: "image/jpeg"}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "hi", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 0)

	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media (images use path tags), got %d", len(result[0].Media))
	}
	localPath, _, _ := store.ResolveWithMeta(ref)
	expectedContent := "hi [image:" + localPath + "]"
	if result[0].Content != expectedContent {
		t.Fatalf("expected content %q, got %q", expectedContent, result[0].Content)
	}
}

func TestResolveMediaRefs_PDFInjectsFilePath(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	pdfPath := filepath.Join(dir, "report.pdf")
	// PDF magic bytes
	os.WriteFile(pdfPath, []byte("%PDF-1.4 test content"), 0o644)
	ref, _ := store.Store(pdfPath, media.MediaMeta{ContentType: "application/pdf"}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "report.pdf [file]", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 0)

	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media (non-image), got %d", len(result[0].Media))
	}
	expected := "report.pdf [file:" + pdfPath + "]"
	if result[0].Content != expected {
		t.Fatalf("expected content %q, got %q", expected, result[0].Content)
	}
}

func TestResolveMediaRefs_AudioInjectsAudioPath(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	oggPath := filepath.Join(dir, "voice.ogg")
	os.WriteFile(oggPath, []byte("fake audio"), 0o644)
	ref, _ := store.Store(oggPath, media.MediaMeta{ContentType: "audio/ogg"}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "voice.ogg [audio]", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 0)

	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media, got %d", len(result[0].Media))
	}
	expected := "voice.ogg [audio:" + oggPath + "]"
	if result[0].Content != expected {
		t.Fatalf("expected content %q, got %q", expected, result[0].Content)
	}
}

func TestResolveMediaRefs_VideoInjectsVideoPath(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	mp4Path := filepath.Join(dir, "clip.mp4")
	os.WriteFile(mp4Path, []byte("fake video"), 0o644)
	ref, _ := store.Store(mp4Path, media.MediaMeta{ContentType: "video/mp4"}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "clip.mp4 [video]", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 0)

	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media, got %d", len(result[0].Media))
	}
	expected := "clip.mp4 [video:" + mp4Path + "]"
	if result[0].Content != expected {
		t.Fatalf("expected content %q, got %q", expected, result[0].Content)
	}
}

func TestResolveMediaRefs_NoGenericTagAppendsPath(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	csvPath := filepath.Join(dir, "data.csv")
	os.WriteFile(csvPath, []byte("a,b,c"), 0o644)
	ref, _ := store.Store(csvPath, media.MediaMeta{ContentType: "text/csv"}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "here is my data", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 0)

	expected := "here is my data [file:" + csvPath + "]"
	if result[0].Content != expected {
		t.Fatalf("expected content %q, got %q", expected, result[0].Content)
	}
}

func TestInjectPathTags_HandlesVariousChannelPlaceholders(t *testing.T) {
	cases := []struct {
		name    string
		content string
		tag     string
		want    string
	}{
		// Telegram / Feishu format
		{"image_photo", "[image: photo]", "[image:/tmp/p.png]", "[image:/tmp/p.png]"},
		// WeCom / WeChat / Line format
		{"bare_image", "[image]", "[image:/tmp/p.png]", "[image:/tmp/p.png]"},
		// QQ / Discord format with filename
		{"image_filename", "[image: pic.jpg]", "[image:/tmp/p.png]", "[image:/tmp/p.png]"},
		{"audio_with_filename", "[audio: voice.m4a]", "[audio:/tmp/a.m4a]", "[audio:/tmp/a.m4a]"},
		{"bare_audio", "[audio]", "[audio:/tmp/a.m4a]", "[audio:/tmp/a.m4a]"},
		{"bare_video", "[video]", "[video:/tmp/v.mp4]", "[video:/tmp/v.mp4]"},
		{"bare_file", "[file]", "[file:/tmp/f.pdf]", "[file:/tmp/f.pdf]"},
		// Mixed surrounding text
		{
			"with_text",
			"hello [image] world",
			"[image:/tmp/p.png]",
			"hello [image:/tmp/p.png] world",
		},
		// No placeholder — append
		{"no_placeholder", "hello world", "[image:/tmp/p.png]", "hello world [image:/tmp/p.png]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := injectPathTags(tc.content, []string{tc.tag})
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestInjectPathTags_DoesNotReplacePathTag(t *testing.T) {
	// If content already contains a path tag, we must not touch it.
	content := "see [image:/already/placed.png] thanks"
	got := injectPathTags(content, []string{"[image:/new/path.png]"})
	want := "see [image:/already/placed.png] thanks [image:/new/path.png]"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestInjectPathTags_PrependsForJSONContent(t *testing.T) {
	jsonContent := `{"schema":"2.0","body":{"elements":[{"tag":"img","img_key":"img_123"}]}}`
	got := injectPathTags(jsonContent, []string{"[image:/tmp/photo.png]"})
	want := "[image:/tmp/photo.png]\n" + jsonContent
	if got != want {
		t.Fatalf("expected tag prepended to JSON, got %q", got)
	}
}

func TestInjectPathTags_BracketTextNotTreatedAsJSON(t *testing.T) {
	content := "[update] see attached report"
	got := injectPathTags(content, []string{"[file:/tmp/report.pdf]"})
	want := "[update] see attached report [file:/tmp/report.pdf]"
	if got != want {
		t.Fatalf("expected tag appended to bracket text, got %q", got)
	}
}

func TestResolveMediaRefs_JSONContentPrependsPathTag(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	pngPath := filepath.Join(dir, "card_img.png")
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
	os.WriteFile(pngPath, pngHeader, 0o644)
	ref, _ := store.Store(pngPath, media.MediaMeta{ContentType: "image/png"}, "test")

	jsonContent := `{"schema":"2.0","body":{"elements":[{"tag":"img","img_key":"img_123"}]}}`
	messages := []providers.Message{
		{Role: "user", Content: jsonContent, Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 0)

	want := "[image:" + pngPath + "]\n" + jsonContent
	if result[0].Content != want {
		t.Fatalf("expected path tag prepended to JSON content, got %q", result[0].Content)
	}
}

func TestResolveMediaRefs_EmptyContentGetsPathTag(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	docPath := filepath.Join(dir, "doc.docx")
	os.WriteFile(docPath, []byte("fake docx"), 0o644)
	docxMIME := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	ref, _ := store.Store(docPath, media.MediaMeta{ContentType: docxMIME}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "", Media: []string{ref}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 0)

	expected := "[file:" + docPath + "]"
	if result[0].Content != expected {
		t.Fatalf("expected content %q, got %q", expected, result[0].Content)
	}
}

func TestResolveMediaRefs_MixedImageAndFile(t *testing.T) {
	store := media.NewFileMediaStore()
	dir := t.TempDir()

	pngPath := filepath.Join(dir, "photo.png")
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
	os.WriteFile(pngPath, pngHeader, 0o644)
	imgRef, _ := store.Store(pngPath, media.MediaMeta{}, "test")

	pdfPath := filepath.Join(dir, "report.pdf")
	os.WriteFile(pdfPath, []byte("%PDF-1.4 test"), 0o644)
	fileRef, _ := store.Store(pdfPath, media.MediaMeta{ContentType: "application/pdf"}, "test")

	messages := []providers.Message{
		{Role: "user", Content: "check these [file]", Media: []string{imgRef, fileRef}},
	}
	result := resolveMediaRefs(messages, store, config.DefaultMaxMediaSize, 0)

	if len(result[0].Media) != 0 {
		t.Fatalf("expected 0 media (all types use path tags), got %d", len(result[0].Media))
	}
	imgLocalPath, _, _ := store.ResolveWithMeta(imgRef)
	pdfLocalPath, _, _ := store.ResolveWithMeta(fileRef)
	expectedContent := "check these [file:" + pdfLocalPath + "] [image:" + imgLocalPath + "]"
	if result[0].Content != expectedContent {
		t.Fatalf("expected content %q, got %q", expectedContent, result[0].Content)
	}
}

func TestProcessMessage_NativeSearchHandlesHookClearingOptions(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxToolIterations = 2
	cfg.Tools.Web.Enabled = true
	cfg.Tools.Web.PreferNative = true

	provider := &nativeSearchProvider{supported: true}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	defer al.Close()
	agent := al.registry.GetDefaultAgent()
	agent.Provider = provider
	agent.Candidates = nil
	if err := al.MountHook(NamedHook("clear-options", clearOptionsHook{})); err != nil {
		t.Fatalf("MountHook() error = %v", err)
	}

	response, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel: "pico",
		ChatID:  "native-search-hook",
		Content: "search",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "ok" {
		t.Fatalf("response = %q, want ok", response)
	}
	if got := provider.lastOpts["native_search"]; got != true {
		t.Fatalf("native_search option = %#v, want true", got)
	}
}

func TestIsNativeSearchProvider_Supported(t *testing.T) {
	if !isNativeSearchProvider(&nativeSearchProvider{supported: true}) {
		t.Fatal("expected true for provider that supports native search")
	}
}

func TestIsNativeSearchProvider_NotSupported(t *testing.T) {
	if isNativeSearchProvider(&nativeSearchProvider{supported: false}) {
		t.Fatal("expected false for provider that does not support native search")
	}
}

func TestIsNativeSearchProvider_NoInterface(t *testing.T) {
	if isNativeSearchProvider(&plainProvider{}) {
		t.Fatal("expected false for provider that does not implement NativeSearchCapable")
	}
}

func TestFilterClientWebSearch_RemovesWebSearch(t *testing.T) {
	defs := []providers.ToolDefinition{
		{Type: "function", Function: providers.ToolFunctionDefinition{Name: "web_search"}},
		{Type: "function", Function: providers.ToolFunctionDefinition{Name: "read_file"}},
		{Type: "function", Function: providers.ToolFunctionDefinition{Name: "exec"}},
	}
	result := filterClientWebSearch(defs)
	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}
	for _, td := range result {
		if td.Function.Name == "web_search" {
			t.Fatal("web_search should be filtered out")
		}
	}
}

func TestFilterClientWebSearch_NoWebSearch(t *testing.T) {
	defs := []providers.ToolDefinition{
		{Type: "function", Function: providers.ToolFunctionDefinition{Name: "read_file"}},
		{Type: "function", Function: providers.ToolFunctionDefinition{Name: "exec"}},
	}
	result := filterClientWebSearch(defs)
	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}
}

func TestFilterClientWebSearch_EmptyInput(t *testing.T) {
	result := filterClientWebSearch(nil)
	if len(result) != 0 {
		t.Fatalf("len(result) = %d, want 0", len(result))
	}
}

func TestProcessMessage_ContextOverflowRecovery(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	_ = cfg

	provider := &overflowProvider{}
	al.registry = NewAgentRegistry(al.cfg, provider)

	sessionKey := "agent:main:test-session"
	agent := al.GetRegistry().GetDefaultAgent()

	for i := 0; i < 5; i++ {
		agent.Sessions.AddFullMessage(sessionKey, providers.Message{Role: "user", Content: "heavy message"})
		agent.Sessions.AddFullMessage(sessionKey, providers.Message{Role: "assistant", Content: "response"})
	}

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:    "test",
		ChatID:     "chat1",
		SenderID:   "user1",
		SessionKey: "test-session",
		Content:    "trigger recovery",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Recovered from overflow" {
		t.Fatalf("response = %q, want %q", response, "Recovered from overflow")
	}

	if provider.calls != 2 {
		t.Fatalf("expected 2 calls, got %d", provider.calls)
	}
}

func TestProcessMessage_ContextOverflow_AnthropicStyle(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	_ = cfg

	provider := &overflowProvider{}
	al.registry = NewAgentRegistry(al.cfg, provider)

	recoveryMsg := "error: status 400: context_window_exceeded"

	provider.chatFunc = func(
		ctx context.Context,
		messages []providers.Message,
		tools []providers.ToolDefinition,
		model string,
		opts map[string]any,
	) (*providers.LLMResponse, error) {
		if provider.calls == 1 {
			return nil, errors.New(recoveryMsg)
		}
		return &providers.LLMResponse{Content: "Anthropic recovery success"}, nil
	}

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "test",
		ChatID:   "chat1",
		SenderID: "user1",
		Content:  "hello",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if !strings.Contains(response, "Anthropic recovery success") {
		t.Fatalf("response = %q, want success message", response)
	}
	if provider.calls != 2 {
		t.Fatalf("expected 2 calls for retry, got %d", provider.calls)
	}
}

func TestParallelMessageProcessing_DifferentSessionsProcessedConcurrently(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Track concurrent executions using a unique ID per turn
	var mu sync.Mutex
	activeTurns := make(map[string]bool)
	maxConcurrent := 0
	turnCounter := 0
	var wg sync.WaitGroup
	wg.Add(3) // Wait for 3 turns to complete

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				MaxParallelTurns:  3, // Allow up to 3 concurrent turns
			},
		},
		Session: config.SessionConfig{
			Dimensions: []string{"chat"},
		},
	}

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	// Create a slow mock provider that tracks concurrency
	provider := &concurrentMockProvider{
		responseFunc: func(callID int) string {
			mu.Lock()
			turnCounter++
			turnID := fmt.Sprintf("turn-%d", turnCounter)
			activeTurns[turnID] = true
			currentActive := len(activeTurns)
			if currentActive > maxConcurrent {
				maxConcurrent = currentActive
			}
			mu.Unlock()

			// Simulate some processing time
			time.Sleep(100 * time.Millisecond)

			mu.Lock()
			delete(activeTurns, turnID)
			mu.Unlock()

			wg.Done()
			return fmt.Sprintf("Response %s", turnID)
		},
	}

	al := NewAgentLoop(cfg, msgBus, provider)
	defer al.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the agent loop
	go func() {
		if err := al.Run(ctx); err != nil {
			t.Logf("Agent loop error: %v", err)
		}
	}()

	// Give the loop time to start
	time.Sleep(50 * time.Millisecond)

	// Send 3 messages from different sessions
	sessions := []string{"user1", "user2", "user3"}
	for i, session := range sessions {
		msg := bus.InboundMessage{
			Context: bus.InboundContext{
				Channel:  "telegram",
				ChatID:   fmt.Sprintf("chat%d", i),
				ChatType: "direct",
				SenderID: session,
			},
			Channel:  "telegram",
			ChatID:   fmt.Sprintf("chat%d", i),
			SenderID: session,
			Content:  fmt.Sprintf("Hello from %s", session),
		}
		if err := msgBus.PublishInbound(context.Background(), msg); err != nil {
			t.Fatalf("PublishInbound failed: %v", err)
		}
	}

	// Wait for all turns to complete with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All turns completed successfully
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for turns to complete")
	}

	// Verify that we had concurrent executions
	mu.Lock()
	defer mu.Unlock()

	if maxConcurrent < 2 {
		t.Errorf("Expected at least 2 concurrent executions, got max %d", maxConcurrent)
	}

	t.Logf("Maximum concurrent executions: %d", maxConcurrent)
}

func TestParallelMessageProcessing_SameSessionProcessedSequentially(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	var mu sync.Mutex
	turnIDs := make(map[string]bool)
	var wg sync.WaitGroup
	var firstResponse sync.Once
	wg.Add(1) // Only 1 turn should be created for same session

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				MaxParallelTurns:  3,
			},
		},
		Session: config.SessionConfig{
			Dimensions: []string{"chat"},
		},
	}

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	al := NewAgentLoop(cfg, msgBus, &concurrentMockProvider{
		responseFunc: func(callID int) string {
			firstResponse.Do(func() {
				wg.Done()
			})
			return "ok"
		},
	})
	defer al.Close()

	runtimeCh, closeRuntimeEvents := subscribeRuntimeEventsForTest(
		t,
		al,
		64,
		runtimeevents.KindAgentTurnStart,
	)
	defer closeRuntimeEvents()

	go func() {
		for evt := range runtimeCh {
			if evt.Kind == runtimeevents.KindAgentTurnStart {
				mu.Lock()
				turnIDs[evt.Scope.TurnID] = true
				mu.Unlock()
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := al.Run(ctx); err != nil {
			t.Logf("Agent loop error: %v", err)
		}
	}()

	time.Sleep(50 * time.Millisecond)

	// Send 3 messages from the SAME session - only one turn should be created;
	// subsequent messages should be enqueued to the steering queue and processed
	// within the same turn (not as separate concurrent turns).
	for i := 0; i < 3; i++ {
		msg := bus.InboundMessage{
			Context: bus.InboundContext{
				Channel:  "telegram",
				ChatID:   "chat1",
				ChatType: "direct",
				SenderID: "user1",
			},
			Channel:  "telegram",
			SenderID: "user1",
			ChatID:   "chat1",
			Content:  fmt.Sprintf("Message %d", i+1),
		}
		if err := msgBus.PublishInbound(context.Background(), msg); err != nil {
			t.Fatalf("PublishInbound failed: %v", err)
		}
	}

	// Wait for turn to complete with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Turn completed successfully
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for turn to complete")
	}

	mu.Lock()
	defer mu.Unlock()

	// Only 1 turn ID should have been created — proving messages were
	// serialized into a single turn rather than spawning concurrent turns.
	if len(turnIDs) != 1 {
		t.Errorf("Expected 1 turn (others queued to steering), got %d: %v", len(turnIDs), turnIDs)
	}
}

func TestRunWorkerPanicReleasesSessionTurnState(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.MaxParallelTurns = 1

	msgBus := bus.NewMessageBus()
	provider := &panicAfterStartProvider{started: make(chan struct{})}
	al := NewAgentLoop(cfg, msgBus, provider)
	defer al.Close()

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- al.Run(runCtx)
	}()
	defer func() {
		cancelRun()
		select {
		case err := <-runDone:
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for Run() to exit")
		}
	}()

	msg := bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "test",
			ChatID:   "panic-chat",
			ChatType: "direct",
			SenderID: "user1",
		},
		Content:    "trigger panic",
		SessionKey: "panic-session",
	}
	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute() error = %v", err)
	}
	scopeKey := resolveScopeKey(al.allocateRouteSession(route, msg).SessionKey, msg.SessionKey)

	if err := msgBus.PublishInbound(context.Background(), msg); err != nil {
		t.Fatalf("PublishInbound(first) error = %v", err)
	}

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first turn to start")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if al.getActiveTurnState(scopeKey) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session turn state remained stuck after worker panic")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := msgBus.PublishInbound(context.Background(), msg); err != nil {
		t.Fatalf("PublishInbound(second) error = %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		if provider.calls.Load() >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second message did not start a new turn after panic cleanup")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
