package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
)

func TestProjectTurnContextUsesSavedProjectAndPersistsSources(t *testing.T) {
	workspace := t.TempDir()
	store, err := memory.NewSQLiteStore(filepath.Join(workspace, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	project, err := store.CreateChatProject(ctx, "Operations", "Use the runbook for every answer.")
	if err != nil {
		t.Fatal(err)
	}
	file, err := store.CreateChatProjectFile(ctx, memory.ChatProjectFile{
		ID: "runbook", ProjectID: project.ID, OriginalName: "runbook.md", StoredName: "runbook-file.md",
		Status: "ready", SizeBytes: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceChatProjectFileChunks(ctx, project.ID, file.ID, []memory.ChatProjectChunk{{
		Ordinal: 1, Locator: "Lines 1-3", Content: "If the cache alarm fires, use the blue failover procedure.", TokenCount: 12,
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionChatProject(ctx, "session-one", project.ID); err != nil {
		t.Fatal(err)
	}
	ts := &turnState{
		agent: &AgentInstance{Workspace: workspace, Sessions: session.NewStoreBackend(store)}, channel: "pico", chatID: "pico:session-one",
		userMessage: "What should I do when the cache alarm fires?", turnID: "turn-one",
		opts: processOptions{Dispatch: DispatchRequest{InboundContext: &bus.InboundContext{Raw: map[string]string{}}}},
	}
	projectContext, err := (&Pipeline{}).loadProjectTurnContext(ctx, ts, true)
	if err != nil {
		t.Fatal(err)
	}
	if projectContext == nil || projectContext.project.ID != project.ID || len(projectContext.chunks) != 1 {
		t.Fatalf("project context = %#v", projectContext)
	}
	parts := projectContextPromptParts(projectContext)
	combined := ""
	for _, part := range parts {
		combined += part.Content + "\n"
	}
	if !strings.Contains(combined, "Use the runbook for every answer.") ||
		!strings.Contains(combined, "blue failover procedure") ||
		!strings.Contains(combined, "untrusted reference data") {
		t.Fatalf("project prompt = %s", combined)
	}
	sources, err := store.ChatProjectTurnSources(ctx, "session-one", []string{"turn-one"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources["turn-one"]) != 1 || sources["turn-one"][0].Filename != "runbook.md" {
		t.Fatalf("sources = %#v", sources)
	}
}

func TestProjectTurnContextIgnoresStaleProjectFromInbox(t *testing.T) {
	workspace := t.TempDir()
	store, err := memory.NewSQLiteStore(filepath.Join(workspace, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ts := &turnState{
		agent: &AgentInstance{Workspace: workspace, Sessions: session.NewStoreBackend(store)}, channel: "pico", chatID: "pico:missing-project",
		userMessage: "hello", turnID: "turn-missing",
		opts: processOptions{Dispatch: DispatchRequest{InboundContext: &bus.InboundContext{Raw: map[string]string{
			memory.PicoProjectMetadataKey: "deleted-project",
		}}}},
	}
	if project, err := (&Pipeline{}).loadProjectTurnContext(t.Context(), ts, true); project != nil || err != nil {
		t.Fatalf("project = %#v, error = %v", project, err)
	}
}
