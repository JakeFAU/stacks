package coremigrations

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	"github.com/jackc/pgx/v5"
)

const schemaTestTimeout = 10 * time.Second

func TestCoreManifestStartsWithDocumentsAndEvidence(t *testing.T) {
	t.Parallel()

	manifest, err := Manifest()
	if err != nil {
		t.Fatalf("Manifest() error = %v", err)
	}
	if manifest.Scope != "core" || manifest.Ledger != "core_version" {
		t.Fatalf("manifest identity = (%q, %q), want (core, core_version)", manifest.Scope, manifest.Ledger)
	}
	if len(manifest.Migrations) != 1 {
		t.Fatalf("migration count = %d, want 1", len(manifest.Migrations))
	}
	first := manifest.Migrations[0]
	if first.Version != 1 || first.Name != "documents_evidence" {
		t.Fatalf("first migration = (%d, %q), want (1, documents_evidence)", first.Version, first.Name)
	}

	wantTables := []string{
		"document_sections",
		"document_versions",
		"evidence_spans",
		"source_documents",
		"source_revision_observations",
	}
	var gotTables []string
	for _, grant := range manifest.ApplicationTableGrants {
		if grant.Schema == "stacks_core" {
			gotTables = append(gotTables, grant.Table)
		}
	}
	sort.Strings(gotTables)
	if !reflect.DeepEqual(gotTables, wantTables) {
		t.Fatalf("core application grant tables = %v, want %v", gotTables, wantTables)
	}
}

func TestDocumentsMigrationContainsNoVerticalOrProviderObjects(t *testing.T) {
	database := installCoreDocuments(t)
	ctx, cancel := context.WithTimeout(context.Background(), schemaTestTimeout)
	defer cancel()
	connection := openSchemaTestConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())

	rows, err := connection.Query(ctx, `
		SELECT namespace.nspname, class.relname
		FROM pg_catalog.pg_class AS class
		JOIN pg_catalog.pg_namespace AS namespace
		  ON namespace.oid = class.relnamespace
		WHERE namespace.nspname = 'stacks_core'
		  AND class.relkind IN ('r', 'p')
		ORDER BY class.relname`)
	if err != nil {
		t.Fatalf("inspect installed core tables: %v", err)
	}
	defer rows.Close()
	var gotTables []string
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			t.Fatalf("scan installed core table: %v", err)
		}
		if schema != "stacks_core" {
			t.Fatalf("installed core table schema = %q, want stacks_core", schema)
		}
		gotTables = append(gotTables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate installed core tables: %v", err)
	}
	wantTables := []string{
		"document_sections",
		"document_versions",
		"evidence_spans",
		"source_documents",
		"source_revision_observations",
	}
	if !reflect.DeepEqual(gotTables, wantTables) {
		t.Fatalf("installed core tables = %v, want only %v", gotTables, wantTables)
	}

	var forbiddenObjects int
	if err := connection.QueryRow(ctx, `
		SELECT count(*)
		FROM (
			SELECT namespace.nspname || '.' || class.relname AS object_name
			FROM pg_catalog.pg_class AS class
			JOIN pg_catalog.pg_namespace AS namespace
			  ON namespace.oid = class.relnamespace
			WHERE namespace.nspname LIKE 'stacks_%'
			UNION ALL
			SELECT namespace.nspname || '.' || procedure.proname
			FROM pg_catalog.pg_proc AS procedure
			JOIN pg_catalog.pg_namespace AS namespace
			  ON namespace.oid = procedure.pronamespace
			WHERE namespace.nspname LIKE 'stacks_%'
		) AS installed
		WHERE installed.object_name ~ '(manager|confidence|directory|drive|google|bedrock|anthropic|openai|vector|embedding|model)'`,
	).Scan(&forbiddenObjects); err != nil {
		t.Fatalf("inspect forbidden installed objects: %v", err)
	}
	if forbiddenObjects != 0 {
		t.Fatalf("forbidden vertical/provider object count = %d, want 0", forbiddenObjects)
	}
}

func TestCleanCoreDocumentsInstallUsesTextIDsAndTimestampSix(t *testing.T) {
	database := installCoreDocuments(t)
	ctx, cancel := context.WithTimeout(context.Background(), schemaTestTimeout)
	defer cancel()
	connection := openSchemaTestConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())

	rows, err := connection.Query(ctx, `
		SELECT table_name, column_name, data_type, datetime_precision
		FROM information_schema.columns
		WHERE table_schema = 'stacks_core'
		  AND (
			column_name = 'id'
			OR column_name LIKE '%\_id' ESCAPE '\'
			OR data_type = 'timestamp with time zone'
		  )
		ORDER BY table_name, ordinal_position`)
	if err != nil {
		t.Fatalf("inspect core identifier and timestamp columns: %v", err)
	}
	defer rows.Close()
	var identifiers, timestamps int
	for rows.Next() {
		var table, column, dataType string
		var precision *int32
		if err := rows.Scan(&table, &column, &dataType, &precision); err != nil {
			t.Fatalf("scan core identifier or timestamp column: %v", err)
		}
		if dataType == "timestamp with time zone" {
			timestamps++
			if precision == nil || *precision != 6 {
				t.Errorf("%s.%s timestamp precision = %v, want 6", table, column, precision)
			}
			continue
		}
		identifiers++
		if dataType != "text" {
			t.Errorf("%s.%s identifier type = %q, want text", table, column, dataType)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate core identifier and timestamp columns: %v", err)
	}
	if identifiers == 0 || timestamps == 0 {
		t.Fatalf("inspected identifier/timestamp columns = %d/%d, want both nonzero", identifiers, timestamps)
	}

	assertCatalogConstraint(t, ctx, connection, "document_versions", "document_versions_content_digest_check")
	assertCatalogConstraint(t, ctx, connection, "evidence_spans", "evidence_spans_digest_check")
	assertCatalogConstraint(t, ctx, connection, "source_documents", "source_documents_current_version_fkey")
	assertCatalogConstraint(t, ctx, connection, "document_sections", "document_sections_pkey")
	assertCatalogConstraint(t, ctx, connection, "document_sections", "document_sections_document_version_id_section_order_key")
	assertCatalogConstraint(t, ctx, connection, "evidence_spans", "evidence_spans_offsets_check")

	application := openSchemaTestConnection(t, ctx, database.ApplicationURL())
	defer application.Close(context.Background())
	var appliedVersion int64
	if err := application.QueryRow(
		ctx,
		`SELECT version FROM stacks_migrations.core_version`,
	).Scan(&appliedVersion); err != nil {
		t.Fatalf("application role read core migration ledger: %v", err)
	}
	if appliedVersion != 1 {
		t.Fatalf("application-visible core ledger version = %d, want 1", appliedVersion)
	}
}

func installCoreDocuments(t testing.TB) postgrestest.Database {
	t.Helper()
	database := postgrestest.NewDatabase(t)
	manifest, err := Manifest()
	if err != nil {
		t.Fatalf("Manifest() error = %v", err)
	}
	applicationConfig, err := pgx.ParseConfig(database.ApplicationURL())
	if err != nil {
		t.Fatalf("parse application test database URL: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), schemaTestTimeout)
	defer cancel()
	if _, err := (migration.Migrator{
		DatabaseURL:     database.AdminURL(),
		ApplicationRole: applicationConfig.User,
		Manifests:       []migration.Manifest{manifest},
	}).Apply(ctx); err != nil {
		t.Fatalf("install core documents migration: %v", err)
	}
	return database
}

func openSchemaTestConnection(t testing.TB, ctx context.Context, databaseURL string) *pgx.Conn {
	t.Helper()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to schema test database: %v", err)
	}
	return connection
}

func assertCatalogConstraint(
	t testing.TB,
	ctx context.Context,
	connection *pgx.Conn,
	table string,
	constraint string,
) {
	t.Helper()
	var found bool
	if err := connection.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_catalog.pg_constraint AS catalog_constraint
			JOIN pg_catalog.pg_class AS class
			  ON class.oid = catalog_constraint.conrelid
			JOIN pg_catalog.pg_namespace AS namespace
			  ON namespace.oid = class.relnamespace
			WHERE namespace.nspname = 'stacks_core'
			  AND class.relname = $1
			  AND catalog_constraint.conname = $2
		)`,
		table,
		constraint,
	).Scan(&found); err != nil {
		t.Fatalf("inspect constraint %s.%s: %v", table, constraint, err)
	}
	if !found {
		t.Errorf("constraint %s.%s is absent", table, constraint)
	}
}
