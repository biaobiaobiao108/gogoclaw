package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/commands"
)

func (al *AgentLoop) tryHandleStopCommand(
	ctx context.Context,
	msg bus.InboundMessage,
	sessionKey string,
) bool {
	cmdName, ok := commands.CommandName(msg.Content)
	if !ok || cmdName != "stop" {
		return false
	}

	result, err := al.stopActiveTurnForSession(sessionKey)

	// This function is only called when loaded=true (another turn already
	// claimed this session). If stopActiveTurnForSession found a pending
	// placeholder but didn't stop it, that placeholder belongs to the other
	// message's worker which hasn't started yet — arm a pending stop so the
	// worker will bail when it checks before running.
	if !result.Stopped {
		if ts := al.getActiveTurnState(sessionKey); ts != nil {
			snap := ts.snapshot()
			if strings.HasPrefix(snap.TurnID, pendingTurnPrefix) {
				ts.pendingStop.Store(true)
				if replacement := ts.pendingReplacement.Load(); replacement != nil {
					_ = replacement.requestHardAbort()
				}
				var cancelErr error
				if ts.pendingReceipt != nil {
					_, cancelErr = ts.pendingReceipt.Cancel(ctx)
				}
				if cancelErr == nil {
					result.Stopped = true
				}
				err = errors.Join(err, cancelErr)
			} else if abortErr := al.HardAbort(sessionKey); abortErr == nil {
				result.Stopped = true
			}
		}
	}

	reply := commands.FormatStopReply(result)
	if err != nil {
		reply = "Failed to stop task: " + err.Error()
	}

	if channelManager := al.runtimeSnapshot().channelManager; channelManager != nil {
		channelManager.InvokeTypingStop(msg.Channel, msg.ChatID)
	}
	al.resetMessageToolRound(sessionKey)
	if msg.Context.Raw["task_action"] != "stop" || err != nil {
		al.PublishResponseIfNeeded(ctx, msg.Channel, msg.ChatID, sessionKey, reply)
	}
	return true
}

func (al *AgentLoop) stopActiveTurnForSession(sessionKey string) (commands.StopResult, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return commands.StopResult{}, fmt.Errorf("session key is required")
	}

	result := commands.StopResult{}
	cleared, clearErr := al.clearSteeringMessagesForScope(al.backgroundContext(), sessionKey)
	al.clearPendingSkills(sessionKey)

	ts := al.getActiveTurnState(sessionKey)
	if ts == nil {
		result.Stopped = cleared > 0
		return result, clearErr
	}

	snap := ts.snapshot()
	result.TaskName = snap.UserMessage

	if strings.HasPrefix(snap.TurnID, pendingTurnPrefix) {
		// A pending placeholder means this session is either idle (our own
		// placeholder from the /stop command) or another message is queued but
		// hasn't started yet. In both cases, we don't arm a pending stop here;
		// the caller (tryHandleStopCommand) handles the "another message queued"
		// case explicitly, since it knows loaded=true.
		return result, clearErr
	}

	if err := al.HardAbort(sessionKey); err != nil {
		if al.getActiveTurnState(sessionKey) == nil {
			result.Stopped = cleared > 0
			return result, clearErr
		}
		return commands.StopResult{}, errors.Join(err, clearErr)
	}

	result.Stopped = true
	return result, clearErr
}

func (al *AgentLoop) resetMessageToolRound(sessionKey string) {
	if strings.TrimSpace(sessionKey) == "" {
		return
	}
	if registry := al.GetRegistry(); registry != nil {
		if agent := registry.GetDefaultAgent(); agent != nil {
			if tool, ok := agent.Tools.Get("message"); ok {
				if resetter, ok := tool.(interface{ ResetSentInRound(sessionKey string) }); ok {
					resetter.ResetSentInRound(sessionKey)
				}
			}
		}
	}
}
