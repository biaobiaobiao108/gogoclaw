package state

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
)

// State represents the persistent state for a workspace.
// It includes information about the last active channel/chat.
type State struct {
	// LastChannel is the last channel used for communication
	LastChannel string `json:"last_channel,omitempty"`

	// LastChatID is the last chat ID used for communication
	LastChatID string `json:"last_chat_id,omitempty"`

	// Timestamp is the last time this state was updated
	Timestamp time.Time `json:"timestamp"`
}

// Manager manages persistent state with atomic saves.
type Manager struct {
	workspace string
	state     *State
	mu        sync.RWMutex
	stateDir  string
}

// NewManager creates a new state manager for the given workspace.
func NewManager(workspace string) *Manager {
	sm := &Manager{workspace: workspace, stateDir: filepath.Join(workspace, "sessions"), state: &State{}}
	if err := sm.load(); err != nil {
		logger.WarnCF("state", "failed to load state", map[string]any{"error": err.Error()})
	}
	return sm
}

// SetLastChannel atomically updates the last channel and saves the state.
// SQLite commits the update atomically.
func (sm *Manager) SetLastChannel(channel string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Update state
	sm.state.LastChannel = channel
	sm.state.Timestamp = time.Now()

	// Commit the updated state
	if err := sm.saveAtomic(); err != nil {
		return fmt.Errorf("failed to save state atomically: %w", err)
	}

	return nil
}

// SetLastChatID atomically updates the last chat ID and saves the state.
func (sm *Manager) SetLastChatID(chatID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Update state
	sm.state.LastChatID = chatID
	sm.state.Timestamp = time.Now()

	// Commit the updated state
	if err := sm.saveAtomic(); err != nil {
		return fmt.Errorf("failed to save state atomically: %w", err)
	}

	return nil
}

// GetLastChannel returns the last channel from the state.
func (sm *Manager) GetLastChannel() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.LastChannel
}

// GetLastChatID returns the last chat ID from the state.
func (sm *Manager) GetLastChatID() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.LastChatID
}

// GetTimestamp returns the timestamp of the last state update.
func (sm *Manager) GetTimestamp() time.Time {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.Timestamp
}

// saveAtomic must be called with the lock held.
func (sm *Manager) saveAtomic() error {
	data, err := json.Marshal(sm.state)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	db, err := memory.NewSQLiteStore(sm.stateDir)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.SetAppState(nil, "runtime", "last_channel", data)
}

func (sm *Manager) load() error {
	db, err := memory.NewSQLiteStore(sm.stateDir)
	if err != nil {
		return err
	}
	defer db.Close()
	data, err := db.GetAppState(nil, "runtime", "last_channel")
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, sm.state); err != nil {
		return fmt.Errorf("decode state: %w", err)
	}
	return nil
}
