// Package errors provides typed sentinel errors and API error envelope mapping.
package errors

import (
	"errors"
	"fmt"
	"net/http"
)

// Sentinel errors for mapping to API error codes.
var (
	ErrBudgetExceeded  = newSentinel("BUDGET_EXCEEDED", http.StatusTooManyRequests)
	ErrQuotaExceeded   = newSentinel("QUOTA_EXCEEDED", http.StatusTooManyRequests)
	ErrNoCredits       = newSentinel("NO_CREDITS", http.StatusPaymentRequired)
	ErrTenantSuspended = newSentinel("TENANT_SUSPENDED", http.StatusForbidden)
	ErrNotFound        = newSentinel("NOT_FOUND", http.StatusNotFound)
	ErrForbidden       = newSentinel("FORBIDDEN", http.StatusForbidden)
	ErrUnauthorized    = newSentinel("UNAUTHORIZED", http.StatusUnauthorized)
	ErrConflict        = newSentinel("CONFLICT", http.StatusConflict)
	ErrInternal        = newSentinel("INTERNAL", http.StatusInternalServerError)
)

// sentinelError is an error with an API code and HTTP status.
type sentinelError struct {
	apiCode   string
	httpStatus int
}

func newSentinel(code string, httpStatus int) *sentinelError {
	return &sentinelError{apiCode: code, httpStatus: httpStatus}
}

func (s *sentinelError) Error() string { return s.apiCode }

// wrappedError carries a sentinel + a human-readable message + optional details.
type wrappedError struct {
	cause   *sentinelError
	msg     string
	details any
}

func (w *wrappedError) Error() string {
	if w.msg != "" {
		return w.msg
	}
	return w.cause.Error()
}

func (w *wrappedError) Unwrap() error { return w.cause }

// Wrap attaches a human-readable message to a sentinel error.
func Wrap(cause error, msg string) error {
	s, ok := cause.(*sentinelError)
	if !ok {
		return fmt.Errorf("%s: %w", msg, cause)
	}
	return &wrappedError{cause: s, msg: msg}
}

// WithDetails attaches structured details to a wrapped error.
func WithDetails(err error, details any) error {
	if w, ok := err.(*wrappedError); ok {
		w.details = details
		return w
	}
	return err
}

// CodeFor extracts the API error code and HTTP status from an error.
func CodeFor(err error) (code string, httpStatus int) {
	if err == nil {
		return "", 0
	}
	var s *sentinelError
	if errors.As(err, &s) {
		return s.apiCode, s.httpStatus
	}
	return "INTERNAL", http.StatusInternalServerError
}

// Is is a thin wrapper around errors.Is for convenience.
func Is(err, target error) bool {
	return errors.Is(err, target)
}

// APIError represents the standard API error envelope.
type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Details   any    `json:"details,omitempty"`
	RequestID string `json:"request_id"`
}

// ToEnvelope maps a Go error to the API error envelope.
func ToEnvelope(err error, requestID string) APIError {
	code, _ := CodeFor(err)
	env := APIError{
		Code:      code,
		Message:   err.Error(),
		RequestID: requestID,
	}
	if w, ok := err.(*wrappedError); ok && w.details != nil {
		env.Details = w.details
	}
	return env
}
