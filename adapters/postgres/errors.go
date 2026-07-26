package postgres

import "errors"

// ErrTransactionCallbackRequired identifies an invalid transaction boundary.
var ErrTransactionCallbackRequired = errors.New("PostgreSQL transaction callback is required")
