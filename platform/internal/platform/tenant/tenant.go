// Package tenant manages tenant lifecycle, status transitions, and database provisioning.
package tenant

import (
	"fmt"
)

// Status represents the lifecycle state of a tenant.
type Status string

const (
	StatusProvisioning Status = "provisioning"
	StatusActive       Status = "active"
	StatusSuspended    Status = "suspended"
	StatusClosed       Status = "closed"
)

// ValidStatus checks whether a status string is a recognised tenant status.
func ValidStatus(s string) bool {
	switch Status(s) {
	case StatusProvisioning, StatusActive, StatusSuspended, StatusClosed:
		return true
	}
	return false
}

// CanTransition returns true if a tenant can move from one status to another.
func CanTransition(from, to string) bool {
	switch Status(from) {
	case StatusProvisioning:
		return Status(to) == StatusActive
	case StatusActive:
		return Status(to) == StatusSuspended || Status(to) == StatusClosed
	case StatusSuspended:
		return Status(to) == StatusActive
	case StatusClosed:
		return false
	}
	return false
}

// DBName returns the physical PostgreSQL database name for a tenant.
func DBName(tenantID string) string {
	return fmt.Sprintf("yuqing_t_%s", tenantID)
}
