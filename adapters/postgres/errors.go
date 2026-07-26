package postgres

import "errors"

var (
	// ErrTransactionCallbackRequired identifies an invalid transaction boundary.
	ErrTransactionCallbackRequired = errors.New("PostgreSQL transaction callback is required")

	// ErrConflict identifies an immutable logical identity whose stored payload
	// differs from the canonical value supplied by the caller.
	ErrConflict = errors.New("PostgreSQL immutable identity conflict")
)
