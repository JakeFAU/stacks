package postgres

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
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

// Ping verifies connectivity through the caller-owned pool.
func (database *Database) Ping(ctx context.Context) error {
	if database == nil || database.pool == nil {
		return fmt.Errorf("ping PostgreSQL database: database is closed")
	}
	if err := database.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping PostgreSQL database: %w", err)
	}
	return nil
}

// InspectMigrationStatus performs read-only scoped migration inspection
// through one connection acquired from the caller-owned pool.
func (database *Database) InspectMigrationStatus(
	ctx context.Context,
	manifests []migration.Manifest,
	configured []migration.Scope,
) ([]migration.ScopeStatus, error) {
	if database == nil || database.pool == nil {
		return nil, fmt.Errorf("inspect PostgreSQL migrations: database is closed")
	}
	connection, err := database.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire PostgreSQL migration inspection connection: %w", err)
	}
	defer connection.Release()
	return (migration.Inspector{
		Manifests:  manifests,
		Configured: configured,
	}).StatusWithConnection(ctx, connection.Conn())
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
