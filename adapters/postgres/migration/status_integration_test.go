package migration_test

import (
	"context"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/directorymigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	"github.com/jackc/pgx/v5"
)

const (
	statusApplicationRole = "stacks_app"
	statusTimeout         = 20 * time.Second
)

func TestInspectorApplicationRoleReportsBothKnownScopesCurrent(t *testing.T) {
	database, manifests := installKnownScopes(t)
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()

	statuses, err := (migration.Inspector{
		DatabaseURL: database.ApplicationURL(),
		Manifests:   manifests,
		Configured:  []migration.Scope{"core"},
	}).Status(ctx)
	if err != nil {
		t.Fatalf("Inspector.Status() error = %v", err)
	}
	assertScopeStatus(t, statuses, "core", migration.StateCurrent, 3, 3, true)
	assertScopeStatus(t, statuses, "directory", migration.StateCurrent, 1, 1, false)
}

func TestInspectorSchemaTreeDriftChangesOnlyOwningScope(t *testing.T) {
	database, manifests := installKnownScopes(t)
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	connection := openStatusConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())

	if _, err := connection.Exec(
		ctx,
		"ALTER TABLE stacks_core.source_documents ADD COLUMN synthetic_drift text",
	); err != nil {
		t.Fatalf("mutate core-owned table: %v", err)
	}
	statuses, err := (migration.Inspector{
		DatabaseURL: database.ApplicationURL(),
		Manifests:   manifests,
		Configured:  []migration.Scope{"core", "directory"},
	}).Status(ctx)
	if err != nil {
		t.Fatalf("Inspector.Status() error = %v", err)
	}
	assertScopeStatus(t, statuses, "core", migration.StateSchemaDrift, 3, 3, true)
	assertScopeStatus(t, statuses, "directory", migration.StateCurrent, 1, 1, true)
}

func TestInspectorExactLedgerDriftChangesOnlyOwningScope(t *testing.T) {
	database, manifests := installKnownScopes(t)
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	connection := openStatusConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())

	if _, err := connection.Exec(
		ctx,
		"ALTER TABLE stacks_migrations.directory_version ADD COLUMN synthetic_drift text",
	); err != nil {
		t.Fatalf("mutate directory-owned ledger: %v", err)
	}
	statuses, err := (migration.Inspector{
		DatabaseURL: database.ApplicationURL(),
		Manifests:   manifests,
		Configured:  []migration.Scope{"core"},
	}).Status(ctx)
	if err != nil {
		t.Fatalf("Inspector.Status() error = %v", err)
	}
	assertScopeStatus(t, statuses, "core", migration.StateCurrent, 3, 3, true)
	assertScopeStatus(t, statuses, "directory", migration.StateSchemaDrift, 1, 1, false)
}

func installKnownScopes(
	t *testing.T,
) (postgrestest.Database, []migration.Manifest) {
	t.Helper()
	database := postgrestest.NewDatabase(t)
	core, err := coremigrations.Manifest()
	if err != nil {
		t.Fatalf("load core manifest: %v", err)
	}
	directory, err := directorymigrations.Manifest()
	if err != nil {
		t.Fatalf("load directory manifest: %v", err)
	}
	manifests := []migration.Manifest{core, directory}
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	if _, err := (migration.Migrator{
		DatabaseURL:     database.AdminURL(),
		ApplicationRole: statusApplicationRole,
		Manifests:       manifests,
	}).Apply(ctx); err != nil {
		t.Fatalf("install known manifests: %v", err)
	}
	return database, manifests
}

func assertScopeStatus(
	t *testing.T,
	statuses []migration.ScopeStatus,
	scope migration.Scope,
	state migration.State,
	applied int64,
	expected int64,
	configured bool,
) {
	t.Helper()
	for _, status := range statuses {
		if status.Scope != scope {
			continue
		}
		if status.State != state ||
			status.AppliedVersion != applied ||
			status.ExpectedVersion != expected ||
			status.Configured != configured {
			t.Fatalf("status for scope %q = %#v, want state=%q applied=%d expected=%d configured=%t",
				scope, status, state, applied, expected, configured)
		}
		return
	}
	t.Fatalf("missing status for scope %q", scope)
}

func openStatusConnection(
	t *testing.T,
	ctx context.Context,
	databaseURL string,
) *pgx.Conn {
	t.Helper()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to isolated database: %v", err)
	}
	return connection
}
