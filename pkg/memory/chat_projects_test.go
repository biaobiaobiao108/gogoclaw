package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func TestConcurrentProjectFileQuota(t *testing.T) {
	for _, test := range []struct {
		name  string
		count int
		size  int64
		want  error
	}{
		{name: "count", count: ChatProjectMaxFiles - 1, want: ErrChatProjectFileCount},
		{name: "bytes", count: 1, size: ChatProjectMaxBytes - 1, want: ErrChatProjectStorage},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := NewSQLiteStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			other, err := OpenSQLiteStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			ctx := t.Context()
			project, err := store.CreateChatProject(ctx, "Files", "")
			if err != nil {
				t.Fatal(err)
			}
			for i := range test.count {
				_, err := store.CreateChatProjectFile(ctx, ChatProjectFile{
					ID: fmt.Sprintf("existing-%d", i), ProjectID: project.ID, OriginalName: "existing.txt", SizeBytes: test.size,
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			for i, conn := range []*SQLiteStore{store, other} {
				go func() {
					<-start
					_, err := conn.CreateChatProjectFile(ctx, ChatProjectFile{
						ID: fmt.Sprintf("new-%d", i), ProjectID: project.ID, OriginalName: "new.txt", SizeBytes: 1,
					})
					results <- err
				}()
			}
			close(start)
			first, second := <-results, <-results
			if first == nil {
				first, second = second, first
			}
			if !errors.Is(first, test.want) || second != nil {
				t.Fatalf("concurrent results = %v, %v, want one success and %v", first, second, test.want)
			}
		})
	}
}

func TestChatProjectsAssignmentChunksAndSourceProvenance(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	project, err := store.CreateChatProject(ctx, "Product Notes", "Use the project files.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateChatProject(ctx, "product notes", ""); !errors.Is(err, ErrChatProjectNameExists) {
		t.Fatalf("duplicate project error = %v", err)
	}
	const sessionID = "project-session"
	if err := store.SetSessionChatProject(ctx, sessionID, project.ID); err != nil {
		t.Fatal(err)
	}
	file, err := store.CreateChatProjectFile(ctx, ChatProjectFile{
		ID: "file-one", ProjectID: project.ID, OriginalName: "notes.md", StoredName: "file-one.md",
		ContentType: "text/markdown", SizeBytes: 42, Status: "ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceChatProjectFileChunks(ctx, project.ID, file.ID, []ChatProjectChunk{
		{Ordinal: 1, Locator: "Lines 1-4", Content: "The launch codename is seabird retrieval.", TokenCount: 9},
		{Ordinal: 2, Locator: "Lines 5-8", Content: "The support window closes on Friday.", TokenCount: 8},
		{Ordinal: 3, Locator: "Lines 9-12", Content: "项目验收标准为核心用例全部通过。", TokenCount: 16},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	matches, err := store.SearchChatProjectChunks(ctx, project.ID, "seabird", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Filename != "notes.md" || matches[0].Locator != "Lines 1-4" {
		t.Fatalf("matches = %#v", matches)
	}
	matches, err = store.SearchChatProjectChunks(ctx, project.ID, "项目验收标准是什么？", 20)
	if err != nil || len(matches) != 1 || matches[0].Ordinal != 3 {
		t.Fatalf("Chinese question matches = %#v, error = %v", matches, err)
	}
	if started, err := store.BeginChatProjectFileReindex(ctx, project.ID, file.ID); err != nil || !started {
		t.Fatalf("begin reindex = %t, %v", started, err)
	}
	if started, err := store.BeginChatProjectFileReindex(ctx, project.ID, file.ID); err != nil || started {
		t.Fatalf("concurrent reindex = %t, %v", started, err)
	}
	if err := store.InterruptChatProjectIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	if recovered, err := store.GetChatProjectFile(ctx, project.ID, file.ID); err != nil || recovered.Status != "interrupted" || recovered.IndexedAt.IsZero() {
		t.Fatalf("recovered file = %#v, %v", recovered, err)
	}
	if results, err := store.SearchChatProjectChunks(ctx, project.ID, "seabird", 20); err != nil || len(results) != 1 {
		t.Fatalf("preserved index matches = %#v, %v", results, err)
	}
	if err := store.ReplaceChatProjectTurnSources(ctx, sessionID, "turn-one", []ChatProjectTurnSource{{
		ProjectID: project.ID, FileID: file.ID, Filename: file.OriginalName, Locator: matches[0].Locator,
		Snippet: "The launch codename is seabird.", Ordinal: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteChatProject(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	if assigned, err := store.SessionChatProject(ctx, sessionID); err != nil || assigned != nil {
		t.Fatalf("assignment after delete = %#v, %v", assigned, err)
	}
	sources, err := store.ChatProjectTurnSources(ctx, sessionID, []string{"turn-one"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources["turn-one"]) != 1 || sources["turn-one"][0].Filename != "notes.md" {
		t.Fatalf("sources after project delete = %#v", sources)
	}
}

func TestListPicoSessionPageFiltersByProject(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	project, err := store.CreateChatProject(ctx, "Research", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, sessionID := range []string{"in-project", "outside"} {
		if err := store.AddFullMessage(ctx, legacyListPrefix+sessionID, providers.Message{Role: "user", Content: "hello " + sessionID}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetSessionChatProject(ctx, "in-project", project.ID); err != nil {
		t.Fatal(err)
	}
	assigned, err := store.ListPicoSessionPage(ctx, legacyListPrefix, false, project.ID, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(assigned) != 1 || assigned[0].ID != "in-project" || assigned[0].ProjectName != "Research" {
		t.Fatalf("assigned page = %#v", assigned)
	}
	uncategorized, err := store.ListPicoSessionPage(ctx, legacyListPrefix, false, "none", nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(uncategorized) != 1 || uncategorized[0].ID != "outside" {
		t.Fatalf("uncategorized page = %#v", uncategorized)
	}
}

const legacyListPrefix = "agent:main:pico:direct:pico:"
