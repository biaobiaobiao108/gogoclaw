package agent

import "github.com/biaobiaobiao108/gogoclaw/pkg/session"

// updateTurnCheckpoint serializes read-modify-write updates for one session.
// The mutate callback runs with the latest checkpoint and must validate any
// state transition precondition before the write.
func (al *AgentLoop) updateTurnCheckpoint(
	store session.TurnCheckpointStore,
	sessionKey string,
	mutate func(*session.TurnCheckpoint) error,
) error {
	unlock := al.lockDeliveryCheckpoint(sessionKey)
	defer unlock()

	checkpoint, err := store.GetTurnCheckpoint(sessionKey)
	if err != nil {
		return err
	}
	if err := mutate(&checkpoint); err != nil {
		return err
	}
	return store.SetTurnCheckpoint(sessionKey, checkpoint)
}
