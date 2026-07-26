package postgres

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Database is a validated PostgreSQL connection pool. Callers own its
// lifecycle.
type Database struct {
	pool      *pgxpool.Pool
	closeOnce sync.Once
}

// Open establishes and validates a PostgreSQL connection pool.
func Open(ctx context.Context, databaseURL string) (*Database, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("open PostgreSQL database: URL is required")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL database: %w", err)
	}
	return &Database{pool: pool}, nil
}

// Close releases all pooled database connections.
func (database *Database) Close() {
	if database == nil || database.pool == nil {
		return
	}
	database.closeOnce.Do(database.pool.Close)
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
	if database == nil || database.pool == nil {
		return fmt.Errorf("run PostgreSQL transaction: database is closed")
	}
	err := pgx.BeginFunc(ctx, database.pool, func(transaction pgx.Tx) error {
		return callback(&Transaction{transaction: transaction})
	})
	if err != nil {
		return fmt.Errorf("run PostgreSQL transaction: %w", err)
	}
	return nil
}
