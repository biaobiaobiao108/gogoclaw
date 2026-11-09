package agent

// ToolProgressPolicy owns the limits for repetitive or unproductive tool work.
// Keeping these values separate from execution makes the safety policy visible
// and prevents ad-hoc thresholds from spreading through the tool pipeline.
type ToolProgressPolicy struct {
	RetrievalWarningAfter       int
	RetrievalStopAfter          int
	NoProgressWarningAfter      int
	NoProgressStopAfter         int
	RepeatedFailureWarningAfter int
	RepeatedFailureStopAfter    int
	ReadOnlyReplayStopAfter     int
}

type TurnBudget struct {
	MaxIterations int
	Progress      ToolProgressPolicy
}

func defaultTurnBudget(maxIterations int) TurnBudget {
	return TurnBudget{
		MaxIterations: maxIterations,
		Progress: ToolProgressPolicy{
			RetrievalWarningAfter:       3,
			RetrievalStopAfter:          5,
			NoProgressWarningAfter:      5,
			NoProgressStopAfter:         8,
			RepeatedFailureWarningAfter: 3,
			RepeatedFailureStopAfter:    4,
			ReadOnlyReplayStopAfter:     2,
		},
	}
}

func (ts *turnState) canStartModelCall(hasPendingMessages bool) bool {
	if ts == nil || ts.budget.MaxIterations <= 0 {
		return false
	}
	graceful, _ := ts.gracefulInterruptRequested()
	return ts.currentIteration() < ts.budget.MaxIterations ||
		hasPendingMessages ||
		graceful
}

func (ts *turnState) iterationBudgetReached() bool {
	return ts != nil && ts.budget.MaxIterations > 0 && ts.currentIteration() >= ts.budget.MaxIterations
}
