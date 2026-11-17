package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

// CatalogModel represents a single model entry in a saved catalog.
type CatalogModel struct {
	ID      string         `json:"id"`
	OwnedBy string         `json:"owned_by,omitempty"`
	Extra   map[string]any `json:"extra,omitempty"`
}

// CatalogEntry is a saved list of upstream models fetched for a specific provider+key combination.
type CatalogEntry struct {
	ID         string         `json:"id"`
	Provider   string         `json:"provider"`
	APIBase    string         `json:"api_base"`
	APIKeyMask string         `json:"api_key_mask"`
	Models     []CatalogModel `json:"models"`
	FetchedAt  string         `json:"fetched_at"`
}

// CatalogStore holds all saved model catalogs.
type CatalogStore struct {
	Entries map[string]*CatalogEntry `json:"entries"`
}

// generateCatalogKey creates a deterministic key for a provider+base+key combination.
func generateCatalogKey(provider, apiBase, apiKey string) string {
	provider = providers.NormalizeProvider(provider)
	apiBase = strings.TrimRight(strings.TrimSpace(apiBase), "/")
	hash := sha256.Sum256([]byte(apiKey))
	return fmt.Sprintf("%s|%s|%x", provider, apiBase, hash[:6])
}

// maskAPIKeyValue masks an API key for display.
// Keys longer than 12 chars show prefix + last 4 chars: "sk-****abcd".
// Keys 9-12 chars show prefix + last 2 chars: "sk-****cd".
// Shorter keys are fully masked as "****".
// Empty keys return empty string.
// Ensure at least 40% of the key will not be displayed.
func maskAPIKeyValue(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	if len(key) <= 12 {
		return key[:3] + "****" + key[len(key)-2:]
	}
	return key[:3] + "****" + key[len(key)-4:]
}

func (h *Handler) loadCatalogs() (*CatalogStore, error) {
	dir, err := h.sessionsDir()
	if err != nil {
		return nil, err
	}
	db, err := memory.NewSQLiteStore(dir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	data, err := db.GetAppState(nil, "model_catalog", "entries")
	if err != nil {
		return nil, err
	}
	store := &CatalogStore{Entries: make(map[string]*CatalogEntry)}
	if len(data) > 0 {
		if err := json.Unmarshal(data, store); err != nil {
			return nil, err
		}
	}
	if store.Entries == nil {
		store.Entries = make(map[string]*CatalogEntry)
	}
	return store, nil
}

func (h *Handler) saveCatalogs(store *CatalogStore) error {
	data, err := json.Marshal(store)
	if err != nil {
		return err
	}
	dir, err := h.sessionsDir()
	if err != nil {
		return err
	}
	db, err := memory.NewSQLiteStore(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.SetAppState(nil, "model_catalog", "entries", data)
}

// SaveCatalog persists a fetched model list for a given provider+key combination.
// If a catalog with the same key already exists, it is updated.
func (h *Handler) SaveCatalog(provider, apiBase, apiKey string, models []CatalogModel) error {
	h.catalogMu.Lock()
	defer h.catalogMu.Unlock()
	store, err := h.loadCatalogs()
	if err != nil {
		return err
	}
	key := generateCatalogKey(provider, apiBase, apiKey)
	provider = providers.NormalizeProvider(provider)
	store.Entries[key] = &CatalogEntry{
		ID:         key,
		Provider:   provider,
		APIBase:    strings.TrimRight(strings.TrimSpace(apiBase), "/"),
		APIKeyMask: maskAPIKeyValue(apiKey),
		Models:     models,
		FetchedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	return h.saveCatalogs(store)
}

// handleListCatalogs returns all saved model catalogs.
//
//	GET /api/models/catalog
func (h *Handler) handleListCatalogs(w http.ResponseWriter, r *http.Request) {
	store, err := h.loadCatalogs()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load catalogs: %v", err), http.StatusInternalServerError)
		return
	}

	entries := make([]*CatalogEntry, 0, len(store.Entries))
	for _, e := range store.Entries {
		entries = append(entries, e)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"entries": entries,
		"total":   len(entries),
	})
}

// handleDeleteCatalog deletes a saved model catalog by ID.
//
//	DELETE /api/models/catalog/{id}
func (h *Handler) handleDeleteCatalog(w http.ResponseWriter, r *http.Request) {
	h.catalogMu.Lock()
	defer h.catalogMu.Unlock()
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	store, err := h.loadCatalogs()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load catalogs: %v", err), http.StatusInternalServerError)
		return
	}

	if _, ok := store.Entries[id]; !ok {
		http.Error(w, "catalog not found", http.StatusNotFound)
		return
	}

	delete(store.Entries, id)
	if err := h.saveCatalogs(store); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save catalogs: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
