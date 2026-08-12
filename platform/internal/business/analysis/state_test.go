package analysis

import (
	"testing"
)

func TestValidStates(t *testing.T) {
	valid := []string{"draft", "queued", "acquiring_budget", "fetching", "analyzing", "generating_report", "completed", "failed", "canceled"}
	for _, s := range valid {
		if !IsValidState(s) {
			t.Errorf("IsValidState(%q) = false, want true", s)
		}
	}
	if IsValidState("invalid") {
		t.Error("IsValidState(invalid) = true, want false")
	}
}

func TestStateTransitions(t *testing.T) {
	tests := []struct {
		from, to string
		want     bool
	}{
		// Happy path.
		{"draft", "queued", true},
		{"queued", "acquiring_budget", true},
		{"acquiring_budget", "fetching", true},
		{"fetching", "analyzing", true},
		{"analyzing", "generating_report", true},
		{"generating_report", "completed", true},
		// Failure from any active.
		{"draft", "failed", true},
		{"queued", "failed", true},
		{"acquiring_budget", "failed", true},
		{"fetching", "failed", true},
		{"analyzing", "failed", true},
		{"generating_report", "failed", true},
		// Cancel from any active.
		{"draft", "canceled", true},
		{"queued", "canceled", true},
		{"fetching", "canceled", true},
		// Terminal states cannot transition.
		{"completed", "anything", false},
		{"failed", "completed", false},
		{"canceled", "completed", false},
		// Cannot go backwards.
		{"analyzing", "queued", false},
		{"completed", "analyzing", false},
	}

	for _, tt := range tests {
		got := CanTransition(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	if !IsTerminal("completed") {
		t.Error("completed should be terminal")
	}
	if !IsTerminal("failed") {
		t.Error("failed should be terminal")
	}
	if !IsTerminal("canceled") {
		t.Error("canceled should be terminal")
	}
	if IsTerminal("analyzing") {
		t.Error("analyzing should not be terminal")
	}
}
