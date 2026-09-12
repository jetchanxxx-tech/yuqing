package tenant

import (
	"testing"
)

func TestTenantStatus_isValid(t *testing.T) {
	tests := []struct {
		status string
		valid  bool
	}{
		{"provisioning", true},
		{"active", true},
		{"suspended", true},
		{"closed", true},
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := ValidStatus(tt.status); got != tt.valid {
			t.Errorf("ValidStatus(%q) = %v, want %v", tt.status, got, tt.valid)
		}
	}
}

func TestTenantStatus_transitions(t *testing.T) {
	tests := []struct {
		from   string
		to     string
		expect bool
	}{
		// Valid transitions.
		{"provisioning", "active", true},
		{"active", "suspended", true},
		{"suspended", "active", true},
		{"active", "closed", true},
		// Invalid transitions.
		{"closed", "active", false},
		{"provisioning", "suspended", false},
		{"active", "provisioning", false},
	}

	for _, tt := range tests {
		got := CanTransition(tt.from, tt.to)
		if got != tt.expect {
			t.Errorf("CanTransition(%q, %q) = %v, want %v",
				tt.from, tt.to, got, tt.expect)
		}
	}
}

func TestDBName(t *testing.T) {
	if got := DBName("01HERE"); got != "yuqing_t_01HERE" {
		t.Errorf("DBName = %q, want yuqing_t_01HERE", got)
	}
}
