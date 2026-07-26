package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Database is one validated PostgreSQL connection. Callers own its lifecycle.
type Database struct {
	connection *pgx.Conn
}

// Open establishes and validates a PostgreSQL connection.
func Open(ctx context.Context, databaseURL string) (*Database, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("open PostgreSQL database: URL is required")
	}
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL database: %w", err)
	}
	if err := connection.Ping(ctx); err != nil {
		_ = connection.Close(context.Background())
		return nil, fmt.Errorf("ping PostgreSQL database: %w", err)
	}
	return &Database{connection: connection}, nil
}

// Close releases the database connection.
func (database *Database) Close() {
	if database == nil || database.connection == nil {
		return
	}
	_ = database.connection.Close(context.Background())
}

// InTransaction executes callback in one PostgreSQL transaction, committing on
// nil and rolling back while preserving the callback error otherwise.
func (database *Database) InTransaction(
	ctx context.Context,
	callback func(*Transaction) error,
) error {
	if callback == nil {
		return ErrTransactionCallbackRequired
	}
	if database == nil || database.connection == nil {
		return fmt.Errorf("run PostgreSQL transaction: database is closed")
	}
	err := pgx.BeginFunc(ctx, database.connection, func(transaction pgx.Tx) error {
		return callback(&Transaction{transaction: transaction})
	})
	if err != nil {
		return fmt.Errorf("run PostgreSQL transaction: %w", err)
	}
	return nil
}
