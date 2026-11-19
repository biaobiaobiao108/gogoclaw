package pico

import (
	"context"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
)

func (c *PicoChannel) startInbox() error {
	store, err := memory.NewSQLiteStore(c.inboxDir)
	if err != nil {
		return err
	}
	if err := store.MigratePicoInbox(c.ctx); err != nil {
		_ = store.Close()
		return err
	}
	c.inboxMu.Lock()
	defer c.inboxMu.Unlock()
	c.inbox = store
	c.inboxWake = make(chan struct{}, 1)
	c.inboxDone = make(chan struct{})
	go c.runInbox(c.ctx, store, c.inboxWake, c.inboxDone)
	return nil
}

func (c *PicoChannel) currentInbox() *memory.SQLiteStore {
	c.inboxMu.RLock()
	defer c.inboxMu.RUnlock()
	return c.inbox
}

func (c *PicoChannel) stopInbox() {
	c.inboxMu.Lock()
	store, done := c.inbox, c.inboxDone
	c.inbox, c.inboxDone, c.inboxWake = nil, nil, nil
	c.inboxMu.Unlock()
	if store != nil {
		<-done
		if err := store.Close(); err != nil {
			logger.Warnf("close Pico inbound store: %v", err)
		}
	}
}

func (c *PicoChannel) wakeInbox() {
	c.inboxMu.RLock()
	defer c.inboxMu.RUnlock()
	select {
	case c.inboxWake <- struct{}{}:
	default:
	}
}

func (c *PicoChannel) runInbox(ctx context.Context, store *memory.SQLiteStore, wake, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var queued sync.Map
	for ctx.Err() == nil {
		if err := c.publishInbox(ctx, store, &queued); err != nil && ctx.Err() == nil {
			logger.Warnf("dispatch Pico inbox: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-wake:
		}
	}
}

func (c *PicoChannel) publishInbox(ctx context.Context, store *memory.SQLiteStore, queued *sync.Map) error {
	for after := int64(0); ; {
		pending, err := store.PendingPicoInbound(ctx, c.Name(), after)
		if err != nil || len(pending) == 0 {
			return err
		}
		for _, item := range pending {
			after = item.ID
			if _, exists := queued.LoadOrStore(item.ID, struct{}{}); exists {
				continue
			}
			msg := item.Message
			msg.Receipt = &bus.InboundReceipt{
				ID:          item.ID,
				Store:       store.DBPath(),
				PersistUser: true,
				Release:     func() { queued.Delete(item.ID) },
				Cancel: func(runCtx context.Context) (bool, error) {
					defer queued.Delete(item.ID)
					return store.CancelPicoInbound(runCtx, item.ID)
				},
			}
			msg.Claim = func(runCtx context.Context) (bool, error) {
				defer queued.Delete(item.ID)
				if ctx.Err() != nil {
					return false, nil
				}
				return store.ClaimPicoInbound(runCtx, item.ID)
			}
			msg.Finish = func(runCtx context.Context) {
				if runCtx.Err() != nil {
					return
				}
				if err := store.FinishPicoInbound(runCtx, item.ID); err != nil {
					logger.Warnf("record Pico inbound completion: %v", err)
				}
			}
			if err := c.PublishPreparedInbound(ctx, msg.ChatID, msg); err != nil {
				queued.Delete(item.ID)
				return err
			}
		}
	}
}
