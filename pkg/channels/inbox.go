package channels

import (
	"context"
	"sync"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
)

// PublishSQLiteInbox delivers pending rows using the same receipt protocol as Pico.
func (c *BaseChannel) PublishSQLiteInbox(ctx context.Context, store *memory.SQLiteStore, source string, queued *sync.Map, prepare func(*bus.InboundMessage)) error {
	for after := int64(0); ; {
		pending, err := store.PendingPicoInbound(ctx, source, after)
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
					logger.Warnf("record %s inbound completion: %v", c.Name(), err)
				}
			}
			if prepare != nil {
				prepare(&msg)
			}
			if err := c.PublishPreparedInbound(ctx, msg.ChatID, msg); err != nil {
				queued.Delete(item.ID)
				return err
			}
		}
	}
}
