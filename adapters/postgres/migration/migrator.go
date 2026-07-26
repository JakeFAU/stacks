package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	advisoryLockName   = "github.com/JakeFAU/stacks/postgres-migrations/v1"
	lockCleanupTimeout = 5 * time.Second
)

// ErrAppliedMigrationMismatch means an applied ledger record does not match
// the immutable name, version, or checksum in its current manifest.
var ErrAppliedMigrationMismatch = errors.New("applied migration does not match manifest")

// Migrator applies independently versioned manifests under one session lock.
type Migrator struct {
	DatabaseURL     string
	ApplicationRole string
	Manifests       []Manifest
}

// ScopeApplyResult describes the versions applied for one scope by this call.
type ScopeApplyResult struct {
	Scope          Scope
	Applied        []int64
	CurrentVersion int64
}

// ApplyResult contains one result for every requested manifest in manifest
// order.
type ApplyResult struct {
	Scopes []ScopeApplyResult
}

type scopePlan struct {
	manifest     Manifest
	pending      []Migration
	current      int64
	resultOffset int
}

// Apply validates all applied records before running any pending SQL, then
// applies each version and its ledger record atomically.
func (migrator Migrator) Apply(ctx context.Context) (result ApplyResult, applyErr error) {
	if ctx == nil {
		return ApplyResult{}, fmt.Errorf("apply PostgreSQL migrations: context is required")
	}
	if strings.TrimSpace(migrator.DatabaseURL) == "" {
		return ApplyResult{}, fmt.Errorf("apply PostgreSQL migrations: database URL is required")
	}
	if err := validateIdentifier("application role", migrator.ApplicationRole); err != nil {
		return ApplyResult{}, fmt.Errorf("apply PostgreSQL migrations: %w", err)
	}
	if len(migrator.Manifests) == 0 {
		return ApplyResult{}, fmt.Errorf("apply PostgreSQL migrations: at least one manifest is required")
	}
	if err := ValidateManifestSet(migrator.Manifests); err != nil {
		return ApplyResult{}, fmt.Errorf("apply PostgreSQL migrations: %w", err)
	}

	connection, err := pgx.Connect(ctx, migrator.DatabaseURL)
	if err != nil {
		return ApplyResult{}, wrapContextError(ctx, "connect for PostgreSQL migrations", err)
	}
	defer func() {
		_ = connection.Close(context.Background())
	}()

	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey()); err != nil {
		return ApplyResult{}, wrapContextError(ctx, "acquire PostgreSQL migration lock", err)
	}
	defer func() {
		if err := unlockMigrationSession(connection); err != nil && applyErr == nil {
			applyErr = err
		}
	}()

	if _, err := connection.Exec(
		ctx,
		"CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{migrationSchemaName}.Sanitize(),
	); err != nil {
		return ApplyResult{}, wrapContextError(ctx, "create PostgreSQL migration schema", err)
	}
	for _, manifest := range migrator.Manifests {
		if err := ensureLedger(ctx, connection, manifest.Ledger); err != nil {
			return ApplyResult{}, wrapContextError(
				ctx,
				fmt.Sprintf("create PostgreSQL migration ledger for scope %q", manifest.Scope),
				err,
			)
		}
	}

	result.Scopes = make([]ScopeApplyResult, len(migrator.Manifests))
	plans := make([]scopePlan, 0, len(migrator.Manifests))
	for index, manifest := range migrator.Manifests {
		applied, err := loadAppliedMigrations(ctx, connection, manifest)
		if err != nil {
			return ApplyResult{}, wrapContextError(
				ctx,
				fmt.Sprintf("validate PostgreSQL migration ledger for scope %q", manifest.Scope),
				err,
			)
		}
		current := int64(0)
		if len(applied) > 0 {
			current = applied[len(applied)-1].Version
		}
		result.Scopes[index] = ScopeApplyResult{
			Scope:          manifest.Scope,
			CurrentVersion: current,
		}
		plans = append(plans, scopePlan{
			manifest:     manifest,
			pending:      manifest.Migrations[len(applied):],
			current:      current,
			resultOffset: index,
		})
	}

	for _, plan := range plans {
		scopeResult := &result.Scopes[plan.resultOffset]
		for _, migration := range plan.pending {
			finalMigration := migration.Version ==
				plan.manifest.Migrations[len(plan.manifest.Migrations)-1].Version
			if err := applyMigration(
				ctx,
				connection,
				migrator.ApplicationRole,
				plan.manifest,
				migration,
				finalMigration,
			); err != nil {
				return result, wrapContextError(
					ctx,
					fmt.Sprintf(
						"apply PostgreSQL migration scope %q version %d",
						plan.manifest.Scope,
						migration.Version,
					),
					err,
				)
			}
			scopeResult.Applied = append(scopeResult.Applied, migration.Version)
			scopeResult.CurrentVersion = migration.Version
		}
	}
	return result, nil
}

func ensureLedger(ctx context.Context, connection *pgx.Conn, ledger string) error {
	qualifiedLedger := pgx.Identifier{migrationSchemaName, ledger}.Sanitize()
	_, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS `+qualifiedLedger+` (
			version bigint PRIMARY KEY,
			name text NOT NULL,
			checksum bytea NOT NULL CHECK (octet_length(checksum) = 32),
			applied_at timestamptz NOT NULL DEFAULT transaction_timestamp()
		)`)
	return err
}

func loadAppliedMigrations(
	ctx context.Context,
	connection *pgx.Conn,
	manifest Manifest,
) ([]Migration, error) {
	qualifiedLedger := pgx.Identifier{migrationSchemaName, manifest.Ledger}.Sanitize()
	rows, err := connection.Query(
		ctx,
		"SELECT version, name, checksum FROM "+qualifiedLedger+" ORDER BY version",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make([]Migration, 0, len(manifest.Migrations))
	for rows.Next() {
		var version int64
		var name string
		var checksum []byte
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			return nil, err
		}
		index := len(applied)
		if index >= len(manifest.Migrations) {
			return nil, fmt.Errorf(
				"%w: scope %q has unknown applied version %d",
				ErrAppliedMigrationMismatch,
				manifest.Scope,
				version,
			)
		}
		expected := manifest.Migrations[index]
		if version != expected.Version || name != expected.Name ||
			!bytes.Equal(checksum, expected.Checksum[:]) {
			return nil, fmt.Errorf(
				"%w: scope %q version %d",
				ErrAppliedMigrationMismatch,
				manifest.Scope,
				version,
			)
		}
		applied = append(applied, expected)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return applied, nil
}

func applyMigration(
	ctx context.Context,
	connection *pgx.Conn,
	applicationRole string,
	manifest Manifest,
	migration Migration,
	finalMigration bool,
) error {
	return pgx.BeginFunc(ctx, connection, func(transaction pgx.Tx) error {
		if _, err := transaction.Exec(
			ctx,
			migration.SQL,
			pgx.QueryExecModeSimpleProtocol,
		); err != nil {
			return err
		}
		if err := applyApplicationGrants(
			ctx,
			transaction,
			applicationRole,
			manifest.ApplicationSchemaGrants,
			manifest.ApplicationTableGrants,
			finalMigration,
		); err != nil {
			return err
		}
		qualifiedLedger := pgx.Identifier{migrationSchemaName, manifest.Ledger}.Sanitize()
		_, err := transaction.Exec(
			ctx,
			"INSERT INTO "+qualifiedLedger+" (version, name, checksum) VALUES ($1, $2, $3)",
			migration.Version,
			migration.Name,
			migration.Checksum[:],
		)
		return err
	})
}

func applyApplicationGrants(
	ctx context.Context,
	transaction pgx.Tx,
	applicationRole string,
	schemaGrants []SchemaGrant,
	tableGrants []TableGrant,
	finalMigration bool,
) error {
	role := pgx.Identifier{applicationRole}.Sanitize()
	for _, grant := range schemaGrants {
		exists, err := schemaExists(ctx, transaction, grant.Schema)
		if err != nil {
			return err
		}
		if !exists {
			if finalMigration {
				return fmt.Errorf("declared application grant schema %q does not exist", grant.Schema)
			}
			continue
		}
		privileges := make([]string, len(grant.Privileges))
		for index, privilege := range grant.Privileges {
			privileges[index] = string(privilege)
		}
		statement := "GRANT " + strings.Join(privileges, ", ") + " ON SCHEMA " +
			pgx.Identifier{grant.Schema}.Sanitize() + " TO " + role
		if _, err := transaction.Exec(ctx, statement); err != nil {
			return err
		}
	}
	for _, grant := range tableGrants {
		exists, err := tableExists(ctx, transaction, grant.Schema, grant.Table)
		if err != nil {
			return err
		}
		if !exists {
			if finalMigration {
				return fmt.Errorf(
					"declared application grant table %q.%q does not exist",
					grant.Schema,
					grant.Table,
				)
			}
			continue
		}
		columnsExist, err := tableColumnsExist(
			ctx,
			transaction,
			grant.Schema,
			grant.Table,
			grant.UpdateColumns,
		)
		if err != nil {
			return err
		}
		if !columnsExist {
			if finalMigration {
				return fmt.Errorf(
					"declared application grant columns on %q.%q do not exist",
					grant.Schema,
					grant.Table,
				)
			}
			continue
		}
		table := pgx.Identifier{grant.Schema, grant.Table}.Sanitize()
		var ordinaryPrivileges []string
		for _, privilege := range grant.Privileges {
			if privilege == PrivilegeUpdate {
				continue
			}
			ordinaryPrivileges = append(ordinaryPrivileges, string(privilege))
		}
		if len(ordinaryPrivileges) > 0 {
			statement := "GRANT " + strings.Join(ordinaryPrivileges, ", ") + " ON TABLE " +
				table + " TO " + role
			if _, err := transaction.Exec(ctx, statement); err != nil {
				return err
			}
		}
		if len(grant.UpdateColumns) > 0 {
			columns := make([]string, len(grant.UpdateColumns))
			for index, column := range grant.UpdateColumns {
				columns[index] = pgx.Identifier{column}.Sanitize()
			}
			statement := "GRANT UPDATE (" + strings.Join(columns, ", ") + ") ON TABLE " +
				table + " TO " + role
			if _, err := transaction.Exec(ctx, statement); err != nil {
				return err
			}
		}
	}
	return nil
}

func schemaExists(ctx context.Context, transaction pgx.Tx, schema string) (bool, error) {
	var exists bool
	err := transaction.QueryRow(
		ctx,
		`SELECT EXISTS (
			SELECT 1
			FROM pg_catalog.pg_namespace
			WHERE nspname = $1
		)`,
		schema,
	).Scan(&exists)
	return exists, err
}

func tableExists(
	ctx context.Context,
	transaction pgx.Tx,
	schema string,
	table string,
) (bool, error) {
	var exists bool
	err := transaction.QueryRow(
		ctx,
		`SELECT EXISTS (
			SELECT 1
			FROM pg_catalog.pg_class AS class
			JOIN pg_catalog.pg_namespace AS namespace
			  ON namespace.oid = class.relnamespace
			WHERE namespace.nspname = $1
			  AND class.relname = $2
			  AND class.relkind IN ('r', 'p')
		)`,
		schema,
		table,
	).Scan(&exists)
	return exists, err
}

func tableColumnsExist(
	ctx context.Context,
	transaction pgx.Tx,
	schema string,
	table string,
	columns []string,
) (bool, error) {
	if len(columns) == 0 {
		return true, nil
	}
	var count int
	err := transaction.QueryRow(
		ctx,
		`SELECT count(*)
		 FROM pg_catalog.pg_attribute AS attribute
		 JOIN pg_catalog.pg_class AS class
		   ON class.oid = attribute.attrelid
		 JOIN pg_catalog.pg_namespace AS namespace
		   ON namespace.oid = class.relnamespace
		 WHERE namespace.nspname = $1
		   AND class.relname = $2
		   AND class.relkind IN ('r', 'p')
		   AND attribute.attnum > 0
		   AND NOT attribute.attisdropped
		   AND attribute.attname = ANY($3::text[])`,
		schema,
		table,
		columns,
	).Scan(&count)
	return count == len(columns), err
}

func advisoryLockKey() int64 {
	digest := sha256.Sum256([]byte(advisoryLockName))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func unlockMigrationSession(connection *pgx.Conn) error {
	ctx, cancel := context.WithTimeout(context.Background(), lockCleanupTimeout)
	defer cancel()
	var unlocked bool
	err := connection.QueryRow(
		ctx,
		"SELECT pg_advisory_unlock($1)",
		advisoryLockKey(),
	).Scan(&unlocked)
	if err != nil {
		waitForConnectionCleanup(ctx, connection)
		return fmt.Errorf("release PostgreSQL migration lock: %w", err)
	}
	if !unlocked {
		waitForConnectionCleanup(ctx, connection)
		return fmt.Errorf("release PostgreSQL migration lock: session did not own lock")
	}
	return nil
}

func waitForConnectionCleanup(ctx context.Context, connection *pgx.Conn) {
	_ = connection.Close(context.Background())
	select {
	case <-connection.PgConn().CleanupDone():
	case <-ctx.Done():
	}
}

func wrapContextError(ctx context.Context, operation string, err error) error {
	if contextError := ctx.Err(); contextError != nil {
		return fmt.Errorf("%s: %w", operation, contextError)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
