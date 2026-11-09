// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"fmt"
	"sync"
	"time"
)

// TurnPhase is the single source of truth for a turn's lifecycle stage.
// The coordinator loop and every pipeline stage must move through this state
// machine; no stage may jump directly to a terminal phase.
type TurnPhase string

const (
	TurnPhaseCreated    TurnPhase = "created"
	TurnPhaseSetup      TurnPhase = "setup"
	TurnPhaseRunning    TurnPhase = "running"
	TurnPhaseTools      TurnPhase = "tools"
	TurnPhaseFinalizing TurnPhase = "finalizing"
	TurnPhaseCompleted  TurnPhase = "completed"
	TurnPhaseFailed     TurnPhase = "failed"
	TurnPhaseAborted    TurnPhase = "aborted"
)

func (p TurnPhase) terminal() bool {
	return p == TurnPhaseCompleted || p == TurnPhaseFailed || p == TurnPhaseAborted
}

// turnPhaseTransitions is the complete allowlist of legal edges. A transition
// to the current phase is a no-op so repeated loop iterations stay explicit
// without producing history noise.
var turnPhaseTransitions = map[TurnPhase]map[TurnPhase]struct{}{
	TurnPhaseCreated: {
		TurnPhaseSetup:   {},
		TurnPhaseFailed:  {},
		TurnPhaseAborted: {},
	},
	TurnPhaseSetup: {
		TurnPhaseRunning: {},
		TurnPhaseFailed:  {},
		TurnPhaseAborted: {},
	},
	TurnPhaseRunning: {
		TurnPhaseTools:      {},
		TurnPhaseFinalizing: {},
		TurnPhaseFailed:     {},
		TurnPhaseAborted:    {},
	},
	TurnPhaseTools: {
		TurnPhaseRunning:    {},
		TurnPhaseFinalizing: {},
		TurnPhaseFailed:     {},
		TurnPhaseAborted:    {},
	},
	TurnPhaseFinalizing: {
		TurnPhaseCompleted: {},
		TurnPhaseFailed:    {},
		TurnPhaseAborted:   {},
	},
	TurnPhaseCompleted: {},
	TurnPhaseFailed:    {},
	TurnPhaseAborted:   {},
}

const maxTurnPhaseHistory = 64

// TurnPhaseTransition records one accepted lifecycle edge for diagnostics and
// trajectory-level evaluation.
type TurnPhaseTransition struct {
	From TurnPhase `json:"from"`
	To   TurnPhase `json:"to"`
	At   time.Time `json:"at"`
}

type turnLifecycle struct {
	mu      sync.RWMutex
	phase   TurnPhase
	history []TurnPhaseTransition
}

func newTurnLifecycle() *turnLifecycle {
	return &turnLifecycle{phase: TurnPhaseCreated}
}

func newTurnLifecycleAt(phase TurnPhase) *turnLifecycle {
	return &turnLifecycle{phase: phase}
}

func (l *turnLifecycle) Phase() TurnPhase {
	if l == nil {
		return TurnPhaseCreated
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.phase
}

func (l *turnLifecycle) Transition(to TurnPhase) error {
	if l == nil {
		return fmt.Errorf("turn lifecycle is nil")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.phase == "" {
		l.phase = TurnPhaseCreated
	}

	if l.phase == to {
		return nil
	}
	if _, ok := turnPhaseTransitions[l.phase][to]; !ok {
		return fmt.Errorf("illegal turn phase transition %s -> %s", l.phase, to)
	}
	from := l.phase
	l.phase = to
	l.history = append(l.history, TurnPhaseTransition{From: from, To: to, At: time.Now().UTC()})
	if overflow := len(l.history) - maxTurnPhaseHistory; overflow > 0 {
		l.history = append([]TurnPhaseTransition(nil), l.history[overflow:]...)
	}
	return nil
}

func (l *turnLifecycle) History() []TurnPhaseTransition {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]TurnPhaseTransition(nil), l.history...)
}

// finishLifecycle drives a turn to a terminal phase from wherever it stopped.
// Normal completion always passes through Finalizing; failures and aborts can
// terminate from an active phase directly.
func (ts *turnState) finishLifecycle(target TurnPhase) error {
	if ts == nil {
		return fmt.Errorf("turn state is nil")
	}
	if !target.terminal() {
		return fmt.Errorf("turn finish phase %s is not terminal", target)
	}
	for attempts := 0; attempts < 2; attempts++ {
		current := ts.phaseSnapshot()
		if current.terminal() {
			return nil
		}
		if target == TurnPhaseCompleted && current != TurnPhaseFinalizing {
			if current == TurnPhaseCreated || current == TurnPhaseSetup {
				return fmt.Errorf("cannot complete turn from phase %s", current)
			}
			if err := ts.transitionPhase(TurnPhaseFinalizing); err != nil {
				return err
			}
			continue
		}
		return ts.transitionPhase(target)
	}
	return nil
}
