package middleware

import (
	"crypto/subtle"
	"errors"
	"sync"
	"time"
)

const launcherDashboardSessionTTL = 31 * 24 * time.Hour

// LauncherDashboardSessions stores revocable dashboard sessions in memory.
type LauncherDashboardSessions struct {
	mu         sync.Mutex
	sessions   map[string]time.Time
	generation uint64
	ttl        time.Duration
	now        func() time.Time
}

// ErrLauncherDashboardSessionsChanged reports that all sessions were revoked
// while a login was being verified.
var ErrLauncherDashboardSessionsChanged = errors.New("launcher dashboard sessions changed")

func NewLauncherDashboardSessions() *LauncherDashboardSessions {
	return &LauncherDashboardSessions{
		sessions: make(map[string]time.Time),
		ttl:      launcherDashboardSessionTTL,
		now:      time.Now,
	}
}

func (s *LauncherDashboardSessions) Create() (string, error) {
	return s.create(0, false)
}

// Generation returns a snapshot used to prevent a login verified with an old
// password from creating a session after a password change revoked all sessions.
func (s *LauncherDashboardSessions) Generation() uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

// CreateIfGeneration creates a session only if no RevokeAll happened since the
// supplied generation was observed.
func (s *LauncherDashboardSessions) CreateIfGeneration(generation uint64) (string, error) {
	return s.create(generation, true)
}

func (s *LauncherDashboardSessions) create(expectedGeneration uint64, checkGeneration bool) (string, error) {
	if s == nil {
		return "", ErrInvalidLauncherDashboardGrant
	}

	token, err := randomURLToken(launcherSessionCookieBytes)
	if err != nil {
		return "", err
	}

	now := s.now()
	s.mu.Lock()
	if checkGeneration && s.generation != expectedGeneration {
		s.mu.Unlock()
		return "", ErrLauncherDashboardSessionsChanged
	}
	for value, expires := range s.sessions {
		if !now.Before(expires) {
			delete(s.sessions, value)
		}
	}
	s.sessions[token] = now.Add(s.ttl)
	s.mu.Unlock()
	return token, nil
}

func (s *LauncherDashboardSessions) Validate(token string) bool {
	if s == nil || token == "" {
		return false
	}

	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, ok := s.sessions[token]
	if !ok {
		return false
	}
	if !now.Before(expires) {
		delete(s.sessions, token)
		return false
	}
	return true
}

func (s *LauncherDashboardSessions) Revoke(token string) {
	if s == nil || token == "" {
		return
	}
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (s *LauncherDashboardSessions) RevokeAll() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.generation++
	clear(s.sessions)
	s.mu.Unlock()
}

func NewLauncherDashboardSetupToken() (string, error) {
	return randomURLToken(launcherSessionCookieBytes)
}

func LauncherDashboardSetupTokenMatches(expected, provided string) bool {
	if expected == "" || provided == "" || len(expected) != len(provided) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}
