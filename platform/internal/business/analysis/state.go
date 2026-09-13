package analysis

// State represents the lifecycle state of an analysis.
type State string

const (
	StateDraft             State = "draft"
	StateQueued            State = "queued"
	StateAcquiringBudget   State = "acquiring_budget"
	StateFetching          State = "fetching"
	StateAnalyzing         State = "analyzing"
	StateGeneratingReport  State = "generating_report"
	StateCompleted         State = "completed"
	StateFailed            State = "failed"
	StateCanceled          State = "canceled"
)

// validStates is the set of recognised analysis states.
var validStates = map[State]bool{
	StateDraft:            true,
	StateQueued:           true,
	StateAcquiringBudget:  true,
	StateFetching:         true,
	StateAnalyzing:        true,
	StateGeneratingReport: true,
	StateCompleted:        true,
	StateFailed:           true,
	StateCanceled:         true,
}

// terminalStates are end states that cannot transition further.
var terminalStates = map[State]bool{
	StateCompleted: true,
	StateFailed:    true,
	StateCanceled:  true,
}

// transitions defines allowed state transitions.
// Any active state → failed | canceled is handled generically.
var transitions = map[State][]State{
	StateDraft:            {StateQueued, StateFailed, StateCanceled},
	StateQueued:           {StateAcquiringBudget, StateFailed, StateCanceled},
	StateAcquiringBudget:  {StateFetching, StateFailed, StateCanceled},
	StateFetching:         {StateAnalyzing, StateFailed, StateCanceled},
	StateAnalyzing:        {StateGeneratingReport, StateFailed, StateCanceled},
	StateGeneratingReport: {StateCompleted, StateFailed, StateCanceled},
}

// IsValidState returns true if s is a recognised analysis state.
func IsValidState(s string) bool {
	return validStates[State(s)]
}

// CanTransition returns true if moving from one state to another is legal.
func CanTransition(from, to string) bool {
	f := State(from)
	t := State(to)

	if terminalStates[f] {
		return false
	}

	allowed, ok := transitions[f]
	if !ok {
		return false
	}

	for _, a := range allowed {
		if a == t {
			return true
		}
	}
	return false
}

// IsTerminal returns true if the state is an end state.
func IsTerminal(s string) bool {
	return terminalStates[State(s)]
}

// IsActive returns true if the state represents in-progress work (not terminal, not draft).
func IsActive(s string) bool {
	st := State(s)
	if terminalStates[st] || st == StateDraft {
		return false
	}
	return validStates[st]
}
