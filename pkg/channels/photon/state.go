package photon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type photonStateData struct {
	Cursors   map[string]uint64 `json:"cursors"`
	ChatLines map[string]string `json:"chat_lines,omitempty"`
}

type photonStateStore struct {
	path string
	mu   sync.Mutex
	data photonStateData
}

func newPhotonStateStore(path string) *photonStateStore {
	store := &photonStateStore{
		path: path,
		data: photonStateData{
			Cursors:   make(map[string]uint64),
			ChatLines: make(map[string]string),
		},
	}
	store.load()
	return store
}

func (s *photonStateStore) Get(lineID string) uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Cursors[lineID]
}

func (s *photonStateStore) Set(lineID string, sequence uint64) error {
	if s == nil || s.path == "" || lineID == "" || sequence == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sequence <= s.data.Cursors[lineID] {
		return nil
	}
	s.data.Cursors[lineID] = sequence
	return s.saveLocked()
}

func (s *photonStateStore) ChatLine(chatID string) string {
	if s == nil || chatID == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.ChatLines[chatID]
}

func (s *photonStateStore) SetChatLine(chatID, lineID string) error {
	if s == nil || s.path == "" || chatID == "" || lineID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.ChatLines[chatID] == lineID {
		return nil
	}
	s.data.ChatLines[chatID] = lineID
	return s.saveLocked()
}

func (s *photonStateStore) load() {
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil || len(data) == 0 {
		return
	}
	var state photonStateData
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	for lineID, sequence := range state.Cursors {
		if lineID != "" && sequence > 0 {
			s.data.Cursors[lineID] = sequence
		}
	}
	for chatID, lineID := range state.ChatLines {
		if chatID != "" && lineID != "" {
			s.data.ChatLines[chatID] = lineID
		}
	}
}

func (s *photonStateStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}
