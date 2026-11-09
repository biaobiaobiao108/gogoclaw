package seahorse

import (
	"context"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
	"testing"
)

func TestGrepSearchSummaries(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	conv, _ := s.GetOrCreateConversation(ctx, "test:grep-tool")

	s.CreateSummary(ctx, CreateSummaryInput{
		ConversationID: conv.ConversationID,
		Kind:           SummaryKindLeaf,
		Depth:          0,
		Content:        "database connection pool configuration",
		TokenCount:     50,
	})

	re := &RetrievalEngine{store: s}
	results, err := re.Grep(ctx, GrepInput{
		Pattern: "database",
	})
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if len(results.Summaries) == 0 {
		t.Error("expected at least 1 summary result")
	}
}

func TestGrepSearchMessages(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	conv, _ := s.GetOrCreateConversation(ctx, "test:grep-msg")

	s.AddMessage(ctx, conv.ConversationID, "user", "find this message about testing", 5)
	s.AddMessage(ctx, conv.ConversationID, "user", "unrelated content", 3)

	re := &RetrievalEngine{store: s}
	results, err := re.Grep(ctx, GrepInput{
		Pattern: "testing",
	})
	if err != nil {
		t.Fatalf("Grep messages: %v", err)
	}
	if len(results.Messages) == 0 {
		t.Error("expected at least 1 message result")
	}
}

func TestGrepMissingPattern(t *testing.T) {
	s := openTestStore(t)
	re := &RetrievalEngine{store: s}
	_, err := re.Grep(context.Background(), GrepInput{})
	if err == nil {
		t.Error("expected error for missing pattern")
	}
}

func TestGrepToolSupportsAllConversations(t *testing.T) {
	s := openTestStore(t)
	tool := NewGrepTool(&RetrievalEngine{store: s})
	params := tool.Parameters()
	props := params["properties"].(map[string]any)

	// GrepTool should accept all_conversations parameter
	if _, ok := props["all_conversations"]; !ok {
		t.Error("Parameters missing 'all_conversations' field")
	}
}

func TestRetrievalRespectsCurrentSession(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	victim, _ := s.GetOrCreateConversation(ctx, "victim")
	attacker, _ := s.GetOrCreateConversation(ctx, "attacker")
	id, err := s.AddMessage(ctx, victim.ConversationID, "user", "private secret", 3)
	if err != nil {
		t.Fatal(err)
	}
	_ = attacker
	r := &RetrievalEngine{store: s}
	scoped := tools.WithToolSessionContext(tools.WithToolContext(ctx, "telegram", "chat"), "main", "attacker", nil)
	for _, all := range []bool{false, true} {
		result, err := r.Grep(scoped, GrepInput{Pattern: "%secret%", AllConversations: all})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Messages) != 0 {
			t.Fatal("cross-session search leaked content")
		}
	}
	expanded, err := r.ExpandMessages(scoped, []int64{id.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(expanded.Messages) != 0 {
		t.Fatal("cross-session expansion leaked content")
	}
}
