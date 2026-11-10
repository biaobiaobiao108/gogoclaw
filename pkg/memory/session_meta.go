package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

const maxStoredTurnHistory = 50

// SessionMeta describes the durable session row independent of the storage
// engine. Scope and turn payloads stay as raw JSON so pkg/memory does not depend
// on the higher-level session package.
type SessionMeta struct {
	Key         string            `json:"key"`
	Summary     string            `json:"summary"`
	Skip        int               `json:"skip"`
	Count       int               `json:"count"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Scope       json.RawMessage   `json:"scope,omitempty"`
	Turn        json.RawMessage   `json:"turn,omitempty"`
	TurnHistory []json.RawMessage `json:"turn_history,omitempty"`
	Aliases     []string          `json:"aliases,omitempty"`
}

func cloneRawJSON(data json.RawMessage) json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), data...)
}

func normalizeAliases(canonicalKey string, aliases []string) []string {
	if len(aliases) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	canonicalKey = strings.TrimSpace(canonicalKey)
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || alias == canonicalKey {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		normalized = append(normalized, alias)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func shouldShortCircuitSessionResolve(sessionKey string) bool {
	sessionKey = strings.TrimSpace(strings.ToLower(sessionKey))
	if sessionKey == "" {
		return false
	}
	return !strings.ContainsAny(sessionKey, ":/\\")
}

// isMainSessionAlias reports whether alias is the legacy or opaque main-session
// key. The main session is a shared fallback and must not be promoted into
// individual Web UI sessions.
func isMainSessionAlias(alias string) bool {
	if alias == "" {
		return false
	}
	if strings.HasPrefix(alias, "agent:") && strings.HasSuffix(alias, ":main") {
		parts := strings.SplitN(alias, ":", 4)
		if len(parts) == 3 {
			return true
		}
	}
	if strings.HasPrefix(alias, "sk_v1_") {
		for _, agentID := range []string{"main", "Main", "MAIN"} {
			legacy := "agent:" + agentID + ":main"
			hash := sha256.Sum256([]byte(legacy))
			if "sk_v1_"+hex.EncodeToString(hash[:]) == alias {
				return true
			}
		}
	}
	return false
}
