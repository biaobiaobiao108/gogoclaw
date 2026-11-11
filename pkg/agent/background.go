package agent

import (
	"context"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/media"
)

const backgroundDrainTimeout = 10 * time.Second

func (al *AgentLoop) beginBackground() bool {
	al.backgroundMu.Lock()
	defer al.backgroundMu.Unlock()
	if al.backgroundStopping {
		return false
	}
	al.backgroundWG.Add(1)
	return true
}

func (al *AgentLoop) endBackground() {
	al.backgroundWG.Done()
}

func (al *AgentLoop) startBackground(fn func()) bool {
	if fn == nil || !al.beginBackground() {
		return false
	}
	go func() {
		defer al.endBackground()
		fn()
	}()
	return true
}

func (al *AgentLoop) waitForBackgroundWorkers(timeout time.Duration) bool {
	al.backgroundMu.Lock()
	al.backgroundStopping = true
	if al.backgroundTimedOut {
		al.backgroundMu.Unlock()
		logger.ErrorCF("agent", "Background workers are still draining after prior hard timeout", nil)
		return false
	}
	al.backgroundMu.Unlock()

	done := make(chan struct{})
	go func() {
		al.backgroundWG.Wait()
		close(done)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		al.backgroundMu.Lock()
		al.backgroundTimedOut = true
		al.backgroundMu.Unlock()
		logger.ErrorCF("agent", "Background worker shutdown exceeded hard timeout", map[string]any{
			"timeout": timeout.String(),
		})
		return false
	}
}

func (al *AgentLoop) setRunContext(ctx context.Context, cancel context.CancelFunc) {
	al.runMu.Lock()
	al.runCtx = ctx
	al.runCancel = cancel
	al.runMu.Unlock()
}

func (al *AgentLoop) clearRunContext() {
	al.runMu.Lock()
	al.runCtx = nil
	al.runCancel = nil
	al.runMu.Unlock()
}

func (al *AgentLoop) backgroundContext() context.Context {
	al.runMu.Lock()
	ctx := al.runCtx
	al.runMu.Unlock()
	if ctx != nil {
		return ctx
	}
	if al.lifecycleCtx == nil {
		return context.Background()
	}
	return al.lifecycleCtx
}

func (al *AgentLoop) cancelBackground() {
	if al.lifecycleCancel != nil {
		al.lifecycleCancel()
	}
	al.runMu.Lock()
	cancel := al.runCancel
	al.runMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (al *AgentLoop) enqueueSystemMessage(msg bus.InboundMessage) {
	al.systemMu.Lock()
	al.systemQueue = append(al.systemQueue, msg)
	al.systemMu.Unlock()
	select {
	case al.systemNotify <- struct{}{}:
	default:
	}
}

func (al *AgentLoop) popSystemMessage() (bus.InboundMessage, bool) {
	al.systemMu.Lock()
	defer al.systemMu.Unlock()
	if len(al.systemQueue) == 0 {
		return bus.InboundMessage{}, false
	}
	msg := al.systemQueue[0]
	al.systemQueue[0] = bus.InboundMessage{}
	al.systemQueue = al.systemQueue[1:]
	return msg, true
}

func (al *AgentLoop) startSystemMessageWorker(ctx context.Context) {
	if !al.systemWorkerActive.CompareAndSwap(false, true) {
		return
	}
	if !al.startBackground(func() {
		defer al.systemWorkerActive.Store(false)
		al.runSystemMessageWorker(ctx)
	}) {
		al.systemWorkerActive.Store(false)
	}
}

func (al *AgentLoop) runSystemMessageWorker(ctx context.Context) {
	for {
		if msg, ok := al.popSystemMessage(); ok {
			al.processMessageSync(ctx, msg)
			continue
		}
		select {
		case <-ctx.Done():
			pending := 0
			for {
				msg, ok := al.popSystemMessage()
				if !ok {
					break
				}
				pending++
				media.Unpin(al.runtimeSnapshot().mediaStore, msg.Media)
			}
			if pending > 0 {
				logger.WarnCF("agent", "System message worker stopped with queued messages", map[string]any{
					"pending": pending,
				})
			}
			return
		case <-al.systemNotify:
		}
	}
}
