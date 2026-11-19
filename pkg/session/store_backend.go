package session

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/usage"
)

// StoreBackend adapts a memory.Store into the SessionStore interface.
// Write errors are logged rather than returned, matching the fire-and-forget
// contract that the agent loop relies on.
type StoreBackend struct {
	store memory.Store
}

var _ usage.Store = (*StoreBackend)(nil)

func (b *StoreBackend) RecordUsage(ctx context.Context, record usage.Record) error {
	store, ok := b.store.(usage.Store)
	if !ok {
		return fmt.Errorf("session store does not support usage accounting")
	}
	for i, key := range record.SessionKeys {
		resolved, err := b.resolveSessionKeyChecked(key)
		if err != nil {
			return err
		}
		record.SessionKeys[i] = resolved
	}
	return store.RecordUsage(ctx, record)
}

func (b *StoreBackend) SessionUsage(ctx context.Context, key string) (usage.Totals, error) {
	store, ok := b.store.(usage.Store)
	if !ok {
		return usage.Totals{}, fmt.Errorf("session store does not support usage accounting")
	}
	resolved, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return usage.Totals{}, err
	}
	return store.SessionUsage(ctx, resolved)
}

func (b *StoreBackend) DailyUsage(ctx context.Context, day string) (usage.Totals, error) {
	store, ok := b.store.(usage.Store)
	if !ok {
		return usage.Totals{}, fmt.Errorf("session store does not support usage accounting")
	}
	return store.DailyUsage(ctx, day)
}

func (b *StoreBackend) UsageBreakdown(ctx context.Context, fromDay, toDay string) ([]usage.Breakdown, error) {
	store, ok := b.store.(usage.Store)
	if !ok {
		return nil, fmt.Errorf("session store does not support usage accounting")
	}
	return store.UsageBreakdown(ctx, fromDay, toDay)
}

func (b *StoreBackend) UpdateTurnUsage(key, turnID string, usage *bus.ContextUsage) error {
	key, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return err
	}
	store, ok := b.store.(TurnUsageStore)
	if !ok {
		return fmt.Errorf("session store does not support turn usage")
	}
	return store.UpdateTurnUsage(key, turnID, usage)
}

type metaAwareStore interface {
	GetSessionMeta(ctx context.Context, sessionKey string) (memory.SessionMeta, error)
	UpsertSessionMeta(ctx context.Context, sessionKey string, scope json.RawMessage, aliases []string) error
	ResolveSessionKey(ctx context.Context, sessionKey string) (string, bool, error)
}

type aliasPromotingStore interface {
	PromoteAliasHistory(ctx context.Context, sessionKey string, scope json.RawMessage, aliases []string) (bool, error)
}

type turnMetaStore interface {
	GetSessionMeta(context.Context, string) (memory.SessionMeta, error)
	SetTurn(context.Context, string, json.RawMessage) error
	ClearTurns(context.Context, string) error
}

func (b *StoreBackend) ClearTurnCheckpoints(key string) error {
	store, ok := b.store.(turnMetaStore)
	if !ok {
		return fmt.Errorf("session store does not support turn checkpoints")
	}
	key, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return err
	}
	return store.ClearTurns(context.Background(), key)
}

func (b *StoreBackend) SetTurnCheckpoint(key string, checkpoint TurnCheckpoint) error {
	store, ok := b.store.(turnMetaStore)
	if !ok {
		return fmt.Errorf("session store does not support turn checkpoints")
	}
	key, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return err
	}
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	return store.SetTurn(context.Background(), key, data)
}

func (b *StoreBackend) GetTurnCheckpoint(key string) (TurnCheckpoint, error) {
	var checkpoint TurnCheckpoint
	store, ok := b.store.(turnMetaStore)
	if !ok {
		return checkpoint, fmt.Errorf("session store does not support turn checkpoints")
	}
	key, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return checkpoint, err
	}
	var raw json.RawMessage
	if sqlite, ok := b.store.(*memory.SQLiteStore); ok {
		raw, err = sqlite.ReadTurn(context.Background(), key)
	} else {
		var meta memory.SessionMeta
		meta, err = store.GetSessionMeta(context.Background(), key)
		raw = meta.Turn
	}
	if err != nil || len(raw) == 0 {
		return checkpoint, err
	}
	err = json.Unmarshal(raw, &checkpoint)
	return checkpoint, err
}

// MetadataAwareSessionStore exposes structured session metadata operations.
type MetadataAwareSessionStore interface {
	EnsureSessionMetadata(sessionKey string, scope *SessionScope, aliases []string)
	ResolveSessionKey(sessionKey string) string
	GetSessionScope(sessionKey string) *SessionScope
}

// NewStoreBackend wraps a memory.Store for use as a SessionStore.
func NewStoreBackend(store memory.Store) *StoreBackend {
	return &StoreBackend{store: store}
}

func (b *StoreBackend) SQLiteStore() *memory.SQLiteStore {
	store, _ := b.store.(*memory.SQLiteStore)
	return store
}

func (b *StoreBackend) resolveSessionKey(sessionKey string) string {
	resolved, err := b.resolveSessionKeyChecked(sessionKey)
	if err != nil {
		log.Printf("session: resolve session key: %v", err)
		return sessionKey
	}
	return resolved
}

func (b *StoreBackend) resolveSessionKeyChecked(sessionKey string) (string, error) {
	metaStore, ok := b.store.(metaAwareStore)
	if !ok {
		return sessionKey, nil
	}
	resolved, found, err := metaStore.ResolveSessionKey(context.Background(), sessionKey)
	if err != nil {
		return "", err
	}
	if found && resolved != "" {
		return resolved, nil
	}
	return sessionKey, nil
}

// ResolveSessionKey maps aliases onto their canonical session key when the
// underlying store supports structured metadata. Unknown aliases fall back to
// the original input so existing callers remain compatible.
func (b *StoreBackend) ResolveSessionKey(sessionKey string) string {
	return b.resolveSessionKey(sessionKey)
}

// EnsureSessionMetadata persists scope and alias metadata for a session.
func (b *StoreBackend) EnsureSessionMetadata(sessionKey string, scope *SessionScope, aliases []string) {
	metaStore, ok := b.store.(metaAwareStore)
	if !ok {
		return
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}

	var rawScope json.RawMessage
	if scope != nil {
		data, err := json.Marshal(scope)
		if err != nil {
			log.Printf("session: encode session scope: %v", err)
			return
		}
		rawScope = data
	}
	ctx := context.Background()
	if err := metaStore.UpsertSessionMeta(ctx, sessionKey, rawScope, aliases); err != nil {
		log.Printf("session: upsert session metadata: %v", err)
		return
	}

	if promotingStore, ok := b.store.(aliasPromotingStore); ok {
		if _, err := promotingStore.PromoteAliasHistory(ctx, sessionKey, rawScope, aliases); err != nil {
			log.Printf("session: promote alias history: %v", err)
		}
	}
}

// GetSessionScope reads structured scope metadata for a session key or alias.
func (b *StoreBackend) GetSessionScope(sessionKey string) *SessionScope {
	metaStore, ok := b.store.(metaAwareStore)
	if !ok {
		return nil
	}
	sessionKey = b.resolveSessionKey(sessionKey)
	meta, err := metaStore.GetSessionMeta(context.Background(), sessionKey)
	if err != nil {
		log.Printf("session: get session metadata: %v", err)
		return nil
	}
	if len(meta.Scope) == 0 {
		return nil
	}
	var scope SessionScope
	if err := json.Unmarshal(meta.Scope, &scope); err != nil {
		log.Printf("session: decode session scope: %v", err)
		return nil
	}
	return CloneScope(&scope)
}

func (b *StoreBackend) AddMessage(sessionKey, role, content string) {
	sessionKey = b.resolveSessionKey(sessionKey)
	if err := b.store.AddMessage(context.Background(), sessionKey, role, content); err != nil {
		log.Printf("session: add message: %v", err)
	}
}

func (b *StoreBackend) AddFullMessage(sessionKey string, msg providers.Message) {
	if err := b.AddFullMessageChecked(sessionKey, msg); err != nil {
		log.Printf("session: add full message: %v", err)
	}
}

func (b *StoreBackend) AddFullMessageChecked(key string, msg providers.Message) error {
	key, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return err
	}
	return b.store.AddFullMessage(context.Background(), key, msg)
}

func (b *StoreBackend) GetHistoryChecked(key string) ([]providers.Message, error) {
	key, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return nil, err
	}
	return b.store.GetHistory(context.Background(), key)
}

func (b *StoreBackend) GetSummaryChecked(key string) (string, error) {
	key, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return "", err
	}
	return b.store.GetSummary(context.Background(), key)
}

func (b *StoreBackend) SetHistoryChecked(key string, history []providers.Message) error {
	key, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return err
	}
	return b.store.SetHistory(context.Background(), key, history)
}

func (b *StoreBackend) SetSummaryChecked(key, summary string) error {
	key, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return err
	}
	return b.store.SetSummary(context.Background(), key, summary)
}

func (b *StoreBackend) CommitSummary(key string, prefix []providers.Message, previous, summary string) (bool, error) {
	key, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return false, err
	}
	store, ok := b.store.(SummaryCommitStore)
	if !ok {
		return false, fmt.Errorf("session store does not support atomic summary commits")
	}
	return store.CommitSummary(key, prefix, previous, summary)
}

func (b *StoreBackend) GetHistory(key string) []providers.Message {
	key = b.resolveSessionKey(key)
	msgs, err := b.store.GetHistory(context.Background(), key)
	if err != nil {
		log.Printf("session: get history: %v", err)
		return []providers.Message{}
	}
	return msgs
}

func (b *StoreBackend) GetSummary(key string) string {
	key = b.resolveSessionKey(key)
	summary, err := b.store.GetSummary(context.Background(), key)
	if err != nil {
		log.Printf("session: get summary: %v", err)
		return ""
	}
	return summary
}

func (b *StoreBackend) SetSummary(key, summary string) {
	key = b.resolveSessionKey(key)
	if err := b.store.SetSummary(context.Background(), key, summary); err != nil {
		log.Printf("session: set summary: %v", err)
	}
}

func (b *StoreBackend) SetHistory(key string, history []providers.Message) {
	key = b.resolveSessionKey(key)
	if err := b.store.SetHistory(context.Background(), key, history); err != nil {
		log.Printf("session: set history: %v", err)
	}
}

func (b *StoreBackend) TruncateHistory(key string, keepLast int) {
	if err := b.TruncateHistoryChecked(key, keepLast); err != nil {
		log.Printf("session: truncate history: %v", err)
	}
}

func (b *StoreBackend) TruncateHistoryChecked(key string, keepLast int) error {
	key, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return err
	}
	return b.store.TruncateHistory(context.Background(), key, keepLast)
}

// Save has no pending writes: each mutation is already synced. Keep logically
// summarized messages in the transcript; Skip only controls model context.
func (b *StoreBackend) Save(key string) error {
	_, err := b.resolveSessionKeyChecked(key)
	return err
}

func (b *StoreBackend) GetTranscriptChecked(key string) ([]providers.Message, error) {
	key, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return nil, err
	}
	if store, ok := b.store.(interface {
		GetTranscript(context.Context, string) ([]providers.Message, error)
	}); ok {
		return store.GetTranscript(context.Background(), key)
	}
	return b.store.GetHistory(context.Background(), key)
}

// ChatUIMeta reads the UI metadata from the shared SQLite store.
func (b *StoreBackend) ChatUIMeta(ctx context.Context, sessionID string) ([]byte, error) {
	store, ok := b.store.(*memory.SQLiteStore)
	if !ok {
		return nil, fmt.Errorf("session store does not support chat metadata")
	}
	return store.ChatUIMeta(ctx, sessionID)
}

func (b *StoreBackend) FirstUserText(ctx context.Context, key string) (string, error) {
	store, ok := b.store.(*memory.SQLiteStore)
	if !ok {
		return "", fmt.Errorf("session store does not support title inputs")
	}
	resolved, err := b.resolveSessionKeyChecked(key)
	if err != nil {
		return "", err
	}
	return store.FirstUserText(ctx, resolved)
}

// Close releases resources held by the underlying store.
func (b *StoreBackend) Close() error {
	return b.store.Close()
}

func (b *StoreBackend) AppendPicoSteering(ctx context.Context, key string, msg providers.Message) (bool, error) {
	store, resolved, err := b.resolvePicoReceipt(key, msg.InboundReceipt)
	if err != nil {
		return false, err
	}
	return store.AppendPicoSteering(ctx, msg.InboundReceipt.ID, resolved, msg)
}

func (b *StoreBackend) ClaimPicoUser(ctx context.Context, key string, msg providers.Message, receipt *bus.InboundReceipt) (bool, error) {
	store, resolved, err := b.resolvePicoReceipt(key, receipt)
	if err != nil {
		return false, err
	}
	return store.ClaimPicoUser(ctx, receipt.ID, resolved, msg)
}

func (b *StoreBackend) resolvePicoReceipt(key string, receipt *bus.InboundReceipt) (*memory.SQLiteStore, string, error) {
	store, ok := b.store.(*memory.SQLiteStore)
	if !ok {
		return nil, "", fmt.Errorf("session store does not support Pico inbox")
	}
	if receipt.Store != store.DBPath() {
		return nil, "", fmt.Errorf("Pico inbox and session history use different workspaces")
	}
	resolved, err := b.resolveSessionKeyChecked(key)
	return store, resolved, err
}

// ListSessions returns all known session keys.
func (b *StoreBackend) ListSessions() []string {
	return b.store.ListSessions()
}

func (b *StoreBackend) DueDeliverySessions(ctx context.Context, now time.Time) ([]string, error) {
	if store, ok := b.store.(*memory.SQLiteStore); ok {
		return store.DueDeliverySessions(ctx, now)
	}
	return b.ListSessions(), nil
}
