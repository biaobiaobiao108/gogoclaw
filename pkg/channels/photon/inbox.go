package photon

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
)

func (c *PhotonChannel) inboxSource() string {
	return "photon:" + c.Name() + ":" + c.settings.ProjectID
}

func (c *PhotonChannel) inboxMediaDir() string {
	return filepath.Join(filepath.Dir(c.state.path), ".photon-inbox-media")
}

func (c *PhotonChannel) runInbox(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var queued sync.Map
	for ctx.Err() == nil {
		if err := c.PublishSQLiteInbox(ctx, c.inbox, c.inboxSource(), &queued, c.prepareInboxMedia); err != nil && ctx.Err() == nil {
			logger.Warnf("dispatch Photon inbox: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-c.inboxWake:
		}
	}
}

func (c *PhotonChannel) prepareInboxMedia(msg *bus.InboundMessage) {
	store := c.GetMediaStore()
	if store == nil {
		return
	}
	msg.Media = msg.Media[:0]
	for _, attachment := range msg.Attachments {
		if attachment.Path == "" {
			continue
		}
		ref, err := store.Store(attachment.Path, media.MediaMeta{
			Filename: attachment.Filename, ContentType: attachment.ContentType,
			Source: c.Name(), CleanupPolicy: media.CleanupPolicyDeleteOnCleanup,
		}, msg.MediaScope)
		if err != nil {
			logger.Warnf("register Photon inbox attachment: %v", err)
			continue
		}
		msg.Media = append(msg.Media, ref)
	}
}
