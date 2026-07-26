package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Transaction is the SQL boundary supplied to an InTransaction callback.
type Transaction struct {
	transaction pgx.Tx
}

// Exec executes a command in the transaction.
func (transaction *Transaction) Exec(
	ctx context.Context,
	sql string,
	arguments ...any,
) (pgconn.CommandTag, error) {
	return transaction.transaction.Exec(ctx, sql, arguments...)
}

// Query executes a row-returning command in the transaction.
func (transaction *Transaction) Query(
	ctx context.Context,
	sql string,
	arguments ...any,
) (pgx.Rows, error) {
	return transaction.transaction.Query(ctx, sql, arguments...)
}

// QueryRow executes a command expected to return at most one row.
func (transaction *Transaction) QueryRow(
	ctx context.Context,
	sql string,
	arguments ...any,
) pgx.Row {
	return transaction.transaction.QueryRow(ctx, sql, arguments...)
}
