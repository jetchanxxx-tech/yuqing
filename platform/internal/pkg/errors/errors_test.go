package errors

import (
	"errors"
	"net/http"
	"testing"
)

func TestSentinelErrors_areDistinct(t *testing.T) {
	errs := []struct {
		name string
		err  error
	}{
		{"ErrBudgetExceeded", ErrBudgetExceeded},
		{"ErrQuotaExceeded", ErrQuotaExceeded},
		{"ErrTenantSuspended", ErrTenantSuspended},
		{"ErrNotFound", ErrNotFound},
		{"ErrForbidden", ErrForbidden},
		{"ErrUnauthorized", ErrUnauthorized},
		{"ErrConflict", ErrConflict},
		{"ErrInternal", ErrInternal},
	}

	for i, e1 := range errs {
		if e1.err == nil {
			t.Fatalf("sentinel error %q is nil", e1.name)
		}
		for j, e2 := range errs {
			if i != j && errors.Is(e1.err, e2.err) {
				t.Fatalf("sentinel errors %q and %q should be distinct", e1.name, e2.name)
			}
		}
	}
}

func TestSentinelErrors_wrapping(t *testing.T) {
	err := Wrap(ErrNotFound, "user not found: 01HERE")
	if !Is(err, ErrNotFound) {
		t.Fatal("wrapped error should match sentinel via Is")
	}
}

func TestCodeFor(t *testing.T) {
	tests := []struct {
		err      error
		wantCode string
		wantHTTP int
	}{
		{ErrBudgetExceeded, "BUDGET_EXCEEDED", http.StatusTooManyRequests},
		{ErrQuotaExceeded, "QUOTA_EXCEEDED", http.StatusTooManyRequests},
		{ErrTenantSuspended, "TENANT_SUSPENDED", http.StatusForbidden},
		{ErrNotFound, "NOT_FOUND", http.StatusNotFound},
		{ErrForbidden, "FORBIDDEN", http.StatusForbidden},
		{ErrUnauthorized, "UNAUTHORIZED", http.StatusUnauthorized},
		{ErrConflict, "CONFLICT", http.StatusConflict},
		{ErrInternal, "INTERNAL", http.StatusInternalServerError},
	}

	for _, tt := range tests {
		code, httpStatus := CodeFor(tt.err)
		if code != tt.wantCode {
			t.Errorf("CodeFor(%v) code = %q, want %q", tt.err, code, tt.wantCode)
		}
		if httpStatus != tt.wantHTTP {
			t.Errorf("CodeFor(%v) http = %d, want %d", tt.err, httpStatus, tt.wantHTTP)
		}
	}
}

func TestCodeFor_unknownError(t *testing.T) {
	code, httpStatus := CodeFor(errors.New("some random error"))
	if code != "INTERNAL" {
		t.Errorf("unknown error code = %q, want INTERNAL", code)
	}
	if httpStatus != http.StatusInternalServerError {
		t.Errorf("unknown error http = %d, want 500", httpStatus)
	}
}

func TestCodeFor_nil(t *testing.T) {
	code, httpStatus := CodeFor(nil)
	if code != "" || httpStatus != 0 {
		t.Errorf("CodeFor(nil) = (%q, %d), want ('', 0)", code, httpStatus)
	}
}

func TestToEnvelope(t *testing.T) {
	env := ToEnvelope(Wrap(ErrNotFound, "task 01XYZ"), "req-123")
	if env.Code != "NOT_FOUND" {
		t.Errorf("Code = %q, want NOT_FOUND", env.Code)
	}
	if env.Message != "task 01XYZ" {
		t.Errorf("Message = %q, want 'task 01XYZ'", env.Message)
	}
	if env.RequestID != "req-123" {
		t.Errorf("RequestID = %q, want req-123", env.RequestID)
	}
	if env.Details != nil {
		t.Errorf("Details should be nil for simple error")
	}
}

func TestToEnvelope_withDetails(t *testing.T) {
	env := ToEnvelope(
		WithDetails(Wrap(ErrForbidden, "access denied"), map[string]any{"resource": "report", "action": "delete"}),
		"req-456",
	)
	if env.Code != "FORBIDDEN" {
		t.Errorf("Code = %q, want FORBIDDEN", env.Code)
	}
	if env.Details == nil {
		t.Fatal("Details should not be nil")
	}
	if env.Details.(map[string]any)["resource"] != "report" {
		t.Errorf("Details[resource] = %v, want report", env.Details.(map[string]any)["resource"])
	}
}
