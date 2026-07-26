package directorymigrations

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	"github.com/jackc/pgx/v5"
)

const directorySchemaTestTimeout = 10 * time.Second

func TestDirectoryManifestIsIndependentFromCoreVersion(t *testing.T) {
	t.Parallel()

	core, err := coremigrations.Manifest()
	if err != nil {
		t.Fatalf("coremigrations.Manifest() error = %v", err)
	}
	directory, err := Manifest()
	if err != nil {
		t.Fatalf("Manifest() error = %v", err)
	}

	if len(core.Migrations) != 3 || core.Migrations[2].Version != 3 {
		t.Fatalf("core current version = %#v, want 3", core.Migrations)
	}
	if directory.Scope != "directory" ||
		directory.Ledger != "directory_version" ||
		len(directory.Migrations) != 1 ||
		directory.Migrations[0].Version != 1 {
		t.Fatalf(
			"directory manifest = scope:%q ledger:%q migrations:%#v, want independent version 1",
			directory.Scope,
			directory.Ledger,
			directory.Migrations,
		)
	}
	if err := migration.ValidateManifestSet([]migration.Manifest{core, directory}); err != nil {
		t.Fatalf("ValidateManifestSet(core, directory) error = %v", err)
	}
}

func TestCoreOnlyInstallContainsNoDirectoryObjects(t *testing.T) {
	database, ctx := installDirectoryTestScopes(t, false)
	connection := openDirectorySchemaConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())

	var directorySchemas, directoryLedgers int
	if err := connection.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE nspname = 'stacks_directory'),
			count(*) FILTER (
				WHERE nspname = 'stacks_migrations'
				  AND relname = 'directory_version'
			)
		FROM pg_catalog.pg_namespace AS namespace
		LEFT JOIN pg_catalog.pg_class AS class
		  ON class.relnamespace = namespace.oid`,
	).Scan(&directorySchemas, &directoryLedgers); err != nil {
		t.Fatalf("inspect core-only directory objects: %v", err)
	}
	if directorySchemas != 0 || directoryLedgers != 0 {
		t.Fatalf(
			"core-only directory schema/ledger counts = %d/%d, want 0/0",
			directorySchemas,
			directoryLedgers,
		)
	}
}

func TestDirectoryInstallReferencesCoreOnlyFromDirectory(t *testing.T) {
	database, ctx := installDirectoryTestScopes(t, true)
	connection := openDirectorySchemaConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())

	var directoryToCore, coreToDirectory int
	if err := connection.QueryRow(ctx, `
		SELECT
			count(*) FILTER (
				WHERE source_schema.nspname = 'stacks_directory'
				  AND target_schema.nspname = 'stacks_core'
			),
			count(*) FILTER (
				WHERE source_schema.nspname = 'stacks_core'
				  AND target_schema.nspname = 'stacks_directory'
			)
		FROM pg_catalog.pg_constraint AS catalog_constraint
		JOIN pg_catalog.pg_class AS source_table
		  ON source_table.oid = catalog_constraint.conrelid
		JOIN pg_catalog.pg_namespace AS source_schema
		  ON source_schema.oid = source_table.relnamespace
		JOIN pg_catalog.pg_class AS target_table
		  ON target_table.oid = catalog_constraint.confrelid
		JOIN pg_catalog.pg_namespace AS target_schema
		  ON target_schema.oid = target_table.relnamespace
		WHERE catalog_constraint.contype = 'f'`,
	).Scan(&directoryToCore, &coreToDirectory); err != nil {
		t.Fatalf("inspect cross-scope foreign keys: %v", err)
	}
	if directoryToCore == 0 || coreToDirectory != 0 {
		t.Fatalf(
			"cross-scope foreign keys directory-to-core/core-to-directory = %d/%d, want positive/0",
			directoryToCore,
			coreToDirectory,
		)
	}

	rows, err := connection.Query(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = 'stacks_directory'
		  AND table_type = 'BASE TABLE'
		ORDER BY table_name`)
	if err != nil {
		t.Fatalf("list directory tables: %v", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan directory table: %v", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate directory tables: %v", err)
	}
	wantTables := []string{
		"entity_links",
		"lookup_attempts",
		"profile_emails",
		"profiles",
		"snapshots",
	}
	if !reflect.DeepEqual(tables, wantTables) {
		t.Fatalf("directory tables = %#v, want exactly %#v", tables, wantTables)
	}
}

func TestDirectoryAbsenceLeavesCoreMigrationCurrent(t *testing.T) {
	database, ctx := installDirectoryTestScopes(t, false)
	connection := openDirectorySchemaConnection(t, ctx, database.ApplicationURL())
	defer connection.Close(context.Background())

	var currentVersion int64
	if err := connection.QueryRow(
		ctx,
		`SELECT max(version) FROM stacks_migrations.core_version`,
	).Scan(&currentVersion); err != nil {
		t.Fatalf("load core migration version through application role: %v", err)
	}
	if currentVersion != 3 {
		t.Fatalf("core migration version = %d, want current version 3", currentVersion)
	}
}

func TestDirectoryEntityLinksPreserveStagedAndDecisionProofs(t *testing.T) {
	database, ctx := installDirectoryTestScopes(t, true)
	connection := openDirectorySchemaConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())

	var candidateNullable bool
	if err := connection.QueryRow(ctx, `
		SELECT NOT attribute.attnotnull
		FROM pg_catalog.pg_attribute AS attribute
		JOIN pg_catalog.pg_class AS relation
		  ON relation.oid = attribute.attrelid
		JOIN pg_catalog.pg_namespace AS namespace
		  ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = 'stacks_directory'
		  AND relation.relname = 'entity_links'
		  AND attribute.attname = 'candidate_id'`,
	).Scan(&candidateNullable); err != nil {
		t.Fatalf("inspect directory candidate nullability: %v", err)
	}
	if !candidateNullable {
		t.Fatal("directory decision proof candidate_id is not nullable")
	}

	rows, err := connection.Query(ctx, `
		SELECT indexname
		FROM pg_catalog.pg_indexes
		WHERE schemaname = 'stacks_directory'
		  AND tablename = 'entity_links'
		  AND (
		    indexdef LIKE '%WHERE (decision_id IS NULL)%'
		    OR indexdef LIKE '%WHERE (decision_id IS NOT NULL)%'
		  )
		ORDER BY indexname`)
	if err != nil {
		t.Fatalf("inspect directory proof indexes: %v", err)
	}
	defer rows.Close()
	var indexes []string
	for rows.Next() {
		var index string
		if err := rows.Scan(&index); err != nil {
			t.Fatalf("scan directory proof index: %v", err)
		}
		indexes = append(indexes, index)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate directory proof indexes: %v", err)
	}
	if len(indexes) != 2 {
		t.Fatalf("directory proof partial indexes = %#v, want staged and decision indexes", indexes)
	}
}

func installDirectoryTestScopes(
	t testing.TB,
	includeDirectory bool,
) (postgrestest.Database, context.Context) {
	t.Helper()

	database := postgrestest.NewDatabase(t)
	core, err := coremigrations.Manifest()
	if err != nil {
		t.Fatalf("coremigrations.Manifest() error = %v", err)
	}
	manifests := []migration.Manifest{core}
	if includeDirectory {
		directory, err := Manifest()
		if err != nil {
			t.Fatalf("Manifest() error = %v", err)
		}
		manifests = append(manifests, directory)
	}
	applicationConfig, err := pgx.ParseConfig(database.ApplicationURL())
	if err != nil {
		t.Fatalf("parse application test database URL: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), directorySchemaTestTimeout)
	t.Cleanup(cancel)
	if _, err := (migration.Migrator{
		DatabaseURL:     database.AdminURL(),
		ApplicationRole: applicationConfig.User,
		Manifests:       manifests,
	}).Apply(ctx); err != nil {
		t.Fatalf("install directory test scopes: %v", err)
	}
	return database, ctx
}

func openDirectorySchemaConnection(
	t testing.TB,
	ctx context.Context,
	databaseURL string,
) *pgx.Conn {
	t.Helper()

	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to directory schema test database: %v", err)
	}
	return connection
}
