package db

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL error codes the stores translate into platform sentinels.
// See https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	// CodeUniqueViolation is raised when a PRIMARY KEY / UNIQUE constraint
	// (or a unique index) is violated.
	CodeUniqueViolation = "23505"
	// CodeForeignKeyViolation is raised when a FOREIGN KEY constraint rejects
	// a row because the referenced row does not exist.
	CodeForeignKeyViolation = "23503"
)

// PgError unwraps the *pgconn.PgError behind err, or returns nil when err is
// not a PostgreSQL server error (transport failures, context cancellation and
// application errors all answer nil).
//
// 各 pgx store 用它把数据库错误映射成平台 sentinel（ErrConflict/ErrNotFound）：
// 直接比较错误字符串会随语言环境与版本漂移，错误码才是稳定契约。
func PgError(err error) *pgconn.PgError {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr
	}
	return nil
}

// IsUniqueViolation reports whether err is a PostgreSQL unique violation
// (SQLSTATE 23505) — a duplicate row conflicting with an existing constraint.
func IsUniqueViolation(err error) bool {
	return PgError(err) != nil && PgError(err).Code == CodeUniqueViolation
}

// IsForeignKeyViolation reports whether err is a PostgreSQL foreign key
// violation (SQLSTATE 23503) — a reference to a row that does not exist.
func IsForeignKeyViolation(err error) bool {
	return PgError(err) != nil && PgError(err).Code == CodeForeignKeyViolation
}

// ViolatedConstraint returns the constraint name the server reported, or ""
// when err carries no constraint (including non-PostgreSQL errors). Stores use
// it to tell "duplicate email" from "duplicate id" on the same table.
func ViolatedConstraint(err error) string {
	if pgErr := PgError(err); pgErr != nil {
		return pgErr.ConstraintName
	}
	return ""
}
