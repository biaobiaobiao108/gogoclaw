package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/web/backend/api"
)

type titleTestProvider struct {
	started chan struct{}
	release chan struct{}
}

func (p *titleTestProvider) GetDefaultModel() string { return "model" }
func (p *titleTestProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	close(p.started)
	select {
	case <-p.release:
		return &providers.LLMResponse{Content: "测试标题"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestSessionTitleRespectsConcurrentChanges(t *testing.T) {
	for _, scenario := range []string{"generated", "manual", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			store, err := memory.NewSQLiteStore(filepath.Join(root, "sessions"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			key, id := "agent:main:pico:direct:pico:test", "test"
			msg := providers.Message{ID: "turn-user", Role: "user", Content: "请帮我研究这个问题"}
			if err := store.AddFullMessage(context.Background(), key, msg); err != nil {
				t.Fatal(err)
			}
			provider := &titleTestProvider{started: make(chan struct{}), release: make(chan struct{})}
			agent := &AgentInstance{ID: "main", Workspace: root, Sessions: session.NewStoreBackend(store)}
			al := &AgentLoop{}
			ts := &turnState{agent: agent, agentID: "main", sessionKey: key, turnID: "turn", channel: "pico", chatID: "pico:" + id, userMessage: msg.Content}
			al.startSessionTitle(ts, &turnExecution{activeProvider: provider, activeModel: "model"})
			select {
			case <-provider.started:
			case <-time.After(5 * time.Second):
				t.Fatal("title generation did not start")
			}
			if scenario == "manual" {
				if err := store.UpdateChatUIMeta(context.Background(), id, func([]byte) ([]byte, error) { return []byte(`{"title":"手动标题","title_source":"manual"}`), nil }); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "deleted" {
				if _, err := store.DeleteSession(context.Background(), key); err != nil {
					t.Fatal(err)
				}
			}
			close(provider.release)
			if !al.waitForBackgroundWorkers(5 * time.Second) {
				t.Fatal("title worker did not finish")
			}
			all, err := store.ListChatUIMeta(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "deleted" {
				if _, ok := all[id]; ok {
					t.Fatal("deleted session acquired metadata")
				}
				return
			}
			var meta struct {
				Title  string `json:"title"`
				Source string `json:"title_source"`
			}
			if err := json.Unmarshal(all[id], &meta); err != nil {
				t.Fatal(err)
			}
			if scenario == "generated" && (meta.Title != "测试标题" || meta.Source != "generated") {
				t.Fatalf("metadata = %+v", meta)
			}
			if scenario == "manual" && (meta.Title != "手动标题" || meta.Source != "manual") {
				t.Fatalf("metadata = %+v", meta)
			}
		})
	}
}

func TestSessionTitleThroughPicoTurn(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Workspace: root, ModelName: "test-model", MaxTokens: 4096, MaxToolIterations: 10, ContextManager: "seahorse"}}}
	p := &simpleMockProvider{response: "生成的标题"}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), p)
	defer al.Close()
	ctx := bus.InboundContext{Channel: "pico", ChatID: "pico:sample", ChatType: "direct", SenderID: "pico-user", MessageID: "first-id"}
	_, err := al.processMessage(context.Background(), bus.InboundMessage{Context: ctx, Content: "测试第一个问题"})
	if err != nil {
		t.Fatal(err)
	}
	if !al.waitForBackgroundWorkers(5 * time.Second) {
		t.Fatal("background timeout")
	}
	store, err := memory.OpenSQLiteStore(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	meta, err := store.ListChatUIMeta(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(meta["sample"]) == 0 {
		t.Fatalf("missing title metadata: %#v", meta)
	}
	configPath := filepath.Join(root, "config.json")
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api.NewHandler(configPath).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	var items []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "生成的标题" {
		t.Fatalf("sessions = %s", rec.Body.String())
	}

}
