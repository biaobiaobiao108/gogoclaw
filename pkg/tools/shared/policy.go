package toolshared

// ToolProgress describes the durable progress a successful tool call can add
// to a turn. It is intentionally conservative: unknown tools are treated as
// potentially state-changing.
type ToolProgress string

const (
	ToolProgressNone     ToolProgress = "none"
	ToolProgressEvidence ToolProgress = "evidence"
	ToolProgressState    ToolProgress = "state"
)

// ToolPolicy is execution metadata used by the agent loop. It describes what a
// tool can change and how its successful result contributes to turn progress.
type ToolPolicy struct {
	ReadOnly    bool
	Idempotent  bool
	Destructive bool
	Retrieval   bool
	Progress    ToolProgress
}

// ToolPolicyProvider lets a tool declare its own semantics. Tools without a
// policy use the registry's compatibility policy.
type ToolPolicyProvider interface {
	Policy() ToolPolicy
}

func ReadPolicy() ToolPolicy {
	return ToolPolicy{
		ReadOnly:   true,
		Idempotent: true,
		Progress:   ToolProgressEvidence,
	}
}

func RetrievalPolicy() ToolPolicy {
	policy := ReadPolicy()
	policy.Retrieval = true
	return policy
}

func StatePolicy() ToolPolicy {
	return ToolPolicy{
		Destructive: true,
		Progress:    ToolProgressState,
	}
}
