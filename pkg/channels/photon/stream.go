package photon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// photonStreamer uses native iMessage edits with conservative throttling
// because Messages applies its own edit-count and time window limits.
type photonStreamer struct {
	channel *PhotonChannel
	chatID  string

	mu               sync.Mutex
	messageID        string
	deliveryID       string
	lastLen          int
	lastAt           time.Time
	throttleInterval time.Duration
	minGrowth        int
}

func (s *photonStreamer) SetDeliveryID(deliveryID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.deliveryID = strings.TrimSpace(deliveryID)
	s.mu.Unlock()
}

func (s *photonStreamer) Update(ctx context.Context, content string) error {
	if s == nil || s.channel == nil {
		return fmt.Errorf("photon streamer is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateLocked(ctx, content, false)
}

func (s *photonStreamer) Finalize(ctx context.Context, content string) error {
	if s == nil || s.channel == nil {
		return fmt.Errorf("photon streamer is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateLocked(ctx, content, true)
}

func (s *photonStreamer) updateLocked(ctx context.Context, content string, force bool) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	if s.messageID == "" {
		messageID, err := s.channel.sendText(ctx, s.chatID, content, "", s.deliveryID)
		if err != nil {
			return err
		}
		s.messageID = messageID
		s.lastLen = len([]rune(content))
		s.lastAt = time.Now()
		return nil
	}

	now := time.Now()
	growth := len([]rune(content)) - s.lastLen
	if !force && now.Sub(s.lastAt) < s.throttleInterval && growth < s.minGrowth {
		return nil
	}
	if err := s.channel.EditMessage(ctx, s.chatID, s.messageID, content); err != nil {
		return err
	}
	s.lastLen = len([]rune(content))
	s.lastAt = now
	return nil
}

func (s *photonStreamer) Cancel(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	messageID := s.messageID
	s.messageID = ""
	s.mu.Unlock()
	if messageID == "" || s.channel == nil {
		return
	}
	_ = s.channel.DeleteMessage(ctx, s.chatID, messageID)
}
