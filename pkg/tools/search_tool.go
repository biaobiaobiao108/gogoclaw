package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

const BM25SearchToolName = "tool_search"

// Discovered tools stay visible until the per-task registry is discarded.
const taskToolLifetime = -1

func NewToolSearchTool(r *ToolRegistry) *BM25SearchTool {
	return NewBM25SearchTool(r, taskToolLifetime, 8)
}

type BM25SearchTool struct {
	registry         *ToolRegistry
	ttl              int
	maxSearchResults int

	// Cache: rebuilt only when the registry version changes.
	cacheMu      sync.Mutex
	cachedEngine *bm25CachedEngine
	cacheVersion uint64
}

func NewBM25SearchTool(r *ToolRegistry, ttl int, maxSearchResults int) *BM25SearchTool {
	return &BM25SearchTool{registry: r, ttl: ttl, maxSearchResults: maxSearchResults}
}

func (t *BM25SearchTool) Name() string {
	return BM25SearchToolName
}

func (t *BM25SearchTool) Policy() ToolPolicy {
	policy := ReadPolicy()
	policy.Idempotent = false // discovery changes hidden-tool visibility
	return policy
}

func (t *BM25SearchTool) Description() string {
	return "Search available hidden tools on-demand using natural language query describing the action you need to perform. Exposes matching tools for the next model call. Search by the action and English tool identifiers, then use the original user query as the discovered tool’s input."
}

func (t *BM25SearchTool) PromptMetadata() PromptMetadata {
	return PromptMetadata{
		Layer:  ToolPromptLayerCapability,
		Slot:   ToolPromptSlotTooling,
		Source: ToolPromptSourceDiscovery,
	}
}

func (t *BM25SearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
			"limit": map[string]any{
				"type": "integer", "minimum": 1,
				"description": "Maximum number of tools to return. Defaults to 8.",
			},
		},
		"required": []string{"query"},
	}
}

func (t *BM25SearchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	limit := t.maxSearchResults
	if value, exists := args["limit"]; exists {
		var number float64
		switch value := value.(type) {
		case float64:
			number = value
		case int:
			number = float64(value)
		case json.Number:
			var err error
			number, err = value.Float64()
			if err != nil {
				return ErrorResult("limit must be a positive integer")
			}
		default:
			return ErrorResult("limit must be a positive integer")
		}
		if number < 1 || math.IsNaN(number) || math.IsInf(number, 0) || number != math.Trunc(number) || number >= float64(math.MaxInt) {
			return ErrorResult("limit must be a positive integer")
		}
		limit = int(number)
	}
	query, ok := args["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		// An empty string query will match every hidden tool,
		// dumping massive payloads into the context and burning tokens.
		return ErrorResult("Missing or invalid 'query' argument. Must be a non-empty string.")
	}

	logger.DebugCF("discovery", "BM25 search", map[string]any{"query": query})

	cached := t.getOrBuildEngine()
	if cached == nil {
		logger.DebugCF("discovery", "BM25 search: no hidden tools available", nil)
		return SilentResult("No tools found matching the query.")
	}

	ranked := cached.engine.Search(query, limit)
	if len(ranked) == 0 {
		logger.DebugCF("discovery", "BM25 search: no matches", map[string]any{"query": query})
		return SilentResult("No tools found matching the query.")
	}

	results := make([]ToolSearchResult, len(ranked))
	for i, r := range ranked {
		results[i] = ToolSearchResult{
			Name:        r.Document.Name,
			Description: r.Document.Description,
		}
	}

	logger.InfoCF("discovery", "BM25 search completed", map[string]any{"query": query, "results": len(results)})
	return formatDiscoveryResponse(t.registry, results, t.ttl)
}

// ToolSearchResult represents the result returned to the LLM.
// Parameters are omitted from the JSON response to save context tokens;
// the LLM will see full schemas via ToProviderDefs after promotion.
type ToolSearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func formatDiscoveryResponse(registry *ToolRegistry, results []ToolSearchResult, ttl int) *ToolResult {
	if len(results) == 0 {
		return SilentResult("No tools found matching the query.")
	}

	names := make([]string, len(results))
	for i, r := range results {
		names[i] = r.Name
	}
	registry.PromoteTools(names, ttl)
	logger.InfoCF("discovery", "Promoted tools", map[string]any{"tools": names, "ttl": ttl})

	b, err := json.Marshal(results)
	if err != nil {
		return ErrorResult("Failed to format search results: " + err.Error())
	}

	msg := fmt.Sprintf(
		"Found %d tools:\n%s\n\nMatching tools are now available as native tools for this task. Call them directly using their parameter schemas.",
		len(results),
		string(b),
	)

	return SilentResult(msg)
}

// Lightweight internal type used as corpus document for BM25.
type searchDoc struct {
	Name        string
	Description string
	Schema      string
}

// bm25CachedEngine wraps a BM25Engine with its corpus snapshot.
type bm25CachedEngine struct {
	engine *utils.BM25Engine[searchDoc]
}

// snapshotToSearchDocs converts a HiddenToolSnapshot to BM25 searchDoc slice.
func snapshotToSearchDocs(snap HiddenToolSnapshot) []searchDoc {
	docs := make([]searchDoc, len(snap.Docs))
	for i, d := range snap.Docs {
		docs[i] = searchDoc{Name: d.Name, Description: d.Description, Schema: d.Schema}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
	return docs
}

// Index parameter names and descriptions rather than JSON punctuation/schema boilerplate.
func toolSearchSchemaText(schema map[string]any) string {
	var parts []string
	if description, ok := schema["description"].(string); ok {
		parts = append(parts, description)
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		names := make([]string, 0, len(properties))
		for name := range properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			parts = append(parts, name, strings.ReplaceAll(name, "_", " "))
			if child, ok := properties[name].(map[string]any); ok {
				parts = append(parts, toolSearchSchemaText(child))
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		parts = append(parts, toolSearchSchemaText(items))
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if variants, ok := schema[key].([]any); ok {
			for _, variant := range variants {
				if child, ok := variant.(map[string]any); ok {
					parts = append(parts, toolSearchSchemaText(child))
				}
			}
		}
	}
	return strings.Join(parts, " ")
}

// buildBM25Engine creates a BM25Engine from a slice of searchDocs.
func buildBM25Engine(docs []searchDoc) *utils.BM25Engine[searchDoc] {
	return utils.NewBM25Engine(
		docs,
		func(doc searchDoc) string {
			return doc.Name + " " + strings.ReplaceAll(doc.Name, "_", " ") + " " + doc.Description + " " + doc.Schema
		},
	)
}

// getOrBuildEngine returns a cached BM25 engine, rebuilding it only when
// the registry version has changed (new tools registered).
func (t *BM25SearchTool) getOrBuildEngine() *bm25CachedEngine {
	t.cacheMu.Lock()
	defer t.cacheMu.Unlock()

	// Snapshot + version are read under a single registry RLock,
	// guaranteeing consistency (no TOCTOU).
	snap := t.registry.SnapshotHiddenTools()

	// Re-check: another goroutine may have rebuilt while we waited for cacheMu.
	if t.cachedEngine != nil && t.cacheVersion == snap.Version {
		return t.cachedEngine
	}

	docs := snapshotToSearchDocs(snap)
	if len(docs) == 0 {
		t.cachedEngine = nil
		t.cacheVersion = snap.Version
		return nil
	}

	cached := &bm25CachedEngine{engine: buildBM25Engine(docs)}
	t.cachedEngine = cached
	t.cacheVersion = snap.Version
	logger.DebugCF("discovery", "BM25 engine rebuilt", map[string]any{"docs": len(docs), "version": snap.Version})
	return cached
}

func isToolDiscoveryToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case BM25SearchToolName:
		return true
	default:
		return false
	}
}

// SearchBM25 ranks hidden tools against query using BM25 via utils.BM25Engine.
// This non-cached variant rebuilds the engine on every call. Used by tests
// and any code that doesn't hold a BM25SearchTool instance.
func (r *ToolRegistry) SearchBM25(query string, maxSearchResults int) []ToolSearchResult {
	snap := r.SnapshotHiddenTools()
	docs := snapshotToSearchDocs(snap)
	if len(docs) == 0 {
		return nil
	}

	ranked := buildBM25Engine(docs).Search(query, maxSearchResults)
	if len(ranked) == 0 {
		return nil
	}

	out := make([]ToolSearchResult, len(ranked))
	for i, r := range ranked {
		out[i] = ToolSearchResult{
			Name:        r.Document.Name,
			Description: r.Document.Description,
		}
	}
	return out
}
