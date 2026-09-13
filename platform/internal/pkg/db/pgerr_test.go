package db

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPgError_unwrapsWrappedErrors(t *testing.T) {
	base := &pgconn.PgError{Code: CodeUniqueViolation, ConstraintName: "users_email_key"}
	wrapped := fmt.Errorf("insert user: %w", base)

	got := PgError(wrapped)
	if got == nil {
		t.Fatal("PgError(wrapped) = nil, want the underlying *pgconn.PgError")
	}
	if got.ConstraintName != "users_email_key" {
		t.Errorf("ConstraintName = %q, want users_email_key", got.ConstraintName)
	}
}

func TestPgError_returnsNilForNonPostgresErrors(t *testing.T) {
	if got := PgError(errors.New("connection refused")); got != nil {
		t.Errorf("PgError(non-pg) = %+v, want nil", got)
	}
	if got := PgError(nil); got != nil {
		t.Errorf("PgError(nil) = %+v, want nil", got)
	}
}

func TestIsUniqueViolation(t *testing.T) {
	unique := &pgconn.PgError{Code: CodeUniqueViolation, ConstraintName: "tenants_slug_key"}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"unique violation", unique, true},
		{"wrapped unique violation", fmt.Errorf("create tenant: %w", unique), true},
		{"foreign key violation", &pgconn.PgError{Code: CodeForeignKeyViolation}, false},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsUniqueViolation(tt.err); got != tt.want {
				t.Errorf("IsUniqueViolation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsForeignKeyViolation(t *testing.T) {
	fk := &pgconn.PgError{Code: CodeForeignKeyViolation}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"foreign key violation", fk, true},
		{"wrapped foreign key violation", fmt.Errorf("create member: %w", fk), true},
		{"unique violation", &pgconn.PgError{Code: CodeUniqueViolation}, false},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsForeignKeyViolation(tt.err); got != tt.want {
				t.Errorf("IsForeignKeyViolation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestViolatedConstraint(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"named constraint", &pgconn.PgError{Code: CodeUniqueViolation, ConstraintName: "users_email_key"}, "users_email_key"},
		{"unnamed", errors.New("boom"), ""},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ViolatedConstraint(tt.err); got != tt.want {
				t.Errorf("ViolatedConstraint() = %q, want %q", got, tt.want)
			}
		})
	}
}
