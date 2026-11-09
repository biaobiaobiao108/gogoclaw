package chatmeta

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/fileutil"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

const branchStoreVersion = 1

// BranchStore is the durable, web-only history needed to switch between
// regenerated answers. Agent sessions remain the source of truth for the
// active context; this file keeps the inactive snapshots available.
type BranchStore struct {
	Version  int                      `json:"version"`
	Sessions map[string]BranchSession `json:"sessions"`
}

type BranchSession struct {
	Groups map[string]BranchGroup `json:"groups"`
}

type BranchGroup struct {
	RootMessageID   string          `json:"root_message_id"`
	SessionKey      string          `json:"session_key"`
	ActiveVersionID string          `json:"active_version_id"`
	Versions        []BranchVersion `json:"versions"`
}

type BranchVersion struct {
	ID        string              `json:"id"`
	Preview   string              `json:"preview"`
	ModelName string              `json:"model_name,omitempty"`
	CreatedAt time.Time           `json:"created_at"`
	Messages  []providers.Message `json:"messages"`
}

func BranchStorePath(sessionsDir string) string {
	return filepath.Join(sessionsDir, ".gogoclaw", "chat-branches.json")
}

func LoadBranches(path string) (BranchStore, error) {
	store := BranchStore{Version: branchStoreVersion, Sessions: make(map[string]BranchSession)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return BranchStore{}, err
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return BranchStore{}, err
	}
	if store.Sessions == nil {
		store.Sessions = make(map[string]BranchSession)
	}
	if store.Version == 0 {
		store.Version = branchStoreVersion
	}
	return store, nil
}

func SaveBranches(path string, store BranchStore) error {
	if store.Version == 0 {
		store.Version = branchStoreVersion
	}
	if store.Sessions == nil {
		store.Sessions = make(map[string]BranchSession)
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0o644)
}

func RemoveBranchSession(path, sessionID string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	store, err := LoadBranches(path)
	if err != nil {
		return err
	}
	if _, ok := store.Sessions[sessionID]; !ok {
		return nil
	}
	delete(store.Sessions, sessionID)
	if len(store.Sessions) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return SaveBranches(path, store)
}

// UpsertVersion records one answer snapshot. Re-recording an existing answer
// replaces its metadata instead of creating a duplicate version.
func (s *BranchStore) UpsertVersion(sessionID, groupID, sessionKey string, version BranchVersion) {
	if s.Sessions == nil {
		s.Sessions = make(map[string]BranchSession)
	}
	session := s.Sessions[sessionID]
	if session.Groups == nil {
		session.Groups = make(map[string]BranchGroup)
	}
	group := session.Groups[groupID]
	group.RootMessageID = groupID
	group.SessionKey = sessionKey
	if group.ActiveVersionID == "" {
		group.ActiveVersionID = version.ID
	}
	replaced := false
	for i := range group.Versions {
		if group.Versions[i].ID == version.ID {
			group.Versions[i] = version
			replaced = true
			break
		}
	}
	if !replaced {
		group.Versions = append(group.Versions, version)
	}
	group.ActiveVersionID = version.ID
	session.Groups[groupID] = group
	s.Sessions[sessionID] = session
}

func (s *BranchStore) FindVersion(sessionID, versionID string) (BranchGroup, BranchVersion, bool) {
	session, ok := s.Sessions[sessionID]
	if !ok {
		return BranchGroup{}, BranchVersion{}, false
	}
	for groupID, group := range session.Groups {
		for _, version := range group.Versions {
			if version.ID == versionID {
				if group.RootMessageID == "" {
					group.RootMessageID = groupID
				}
				return group, version, true
			}
		}
	}
	return BranchGroup{}, BranchVersion{}, false
}

func (s *BranchStore) SetActive(sessionID, groupID, versionID string) error {
	session, ok := s.Sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %q not found in branch store", sessionID)
	}
	group, ok := session.Groups[groupID]
	if !ok {
		return fmt.Errorf("branch group %q not found", groupID)
	}
	found := false
	for _, version := range group.Versions {
		if version.ID == versionID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("branch version %q not found", versionID)
	}
	group.ActiveVersionID = versionID
	session.Groups[groupID] = group
	s.Sessions[sessionID] = session
	return nil
}

func (s BranchStore) VisibleGroups(sessionID string) []BranchGroup {
	session, ok := s.Sessions[sessionID]
	if !ok {
		return nil
	}
	groups := make([]BranchGroup, 0, len(session.Groups))
	for _, group := range session.Groups {
		if len(group.Versions) > 1 {
			groups = append(groups, group)
		}
	}
	return groups
}

func CloneMessages(messages []providers.Message) []providers.Message {
	if len(messages) == 0 {
		return nil
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return append([]providers.Message(nil), messages...)
	}
	var cloned []providers.Message
	if err := json.Unmarshal(data, &cloned); err != nil {
		return append([]providers.Message(nil), messages...)
	}
	return cloned
}

func Preview(content string, limit int) string {
	content = strings.Join(strings.Fields(content), " ")
	runes := []rune(content)
	if limit <= 0 || len(runes) <= limit {
		return content
	}
	return string(runes[:limit]) + "…"
}
