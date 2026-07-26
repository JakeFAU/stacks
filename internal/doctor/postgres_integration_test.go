package doctor

import (
	"context"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/directorymigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
)

const postgresIntegrationTimeout = 20 * time.Second

func TestPostgresProbeReportsCoreOnlyMigrationStatus(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	core, directory := testMigrationManifests(t)
	ctx, cancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
	defer cancel()
	if _, err := (migration.Migrator{
		DatabaseURL:     database.AdminURL(),
		ApplicationRole: "stacks_app",
		Manifests:       []migration.Manifest{core},
	}).Apply(ctx); err != nil {
		t.Fatalf("install core manifest: %v", err)
	}
	connection, err := postgres.Open(ctx, database.ApplicationURL())
	if err != nil {
		t.Fatalf("open canonical database: %v", err)
	}
	defer connection.Close()
	probe := NewPostgresProbeWithScopes(connection, []migration.Scope{"core"})
	if err := probe.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	statuses, err := probe.MigrationStatus(ctx)
	if err != nil {
		t.Fatalf("MigrationStatus() error = %v", err)
	}
	assertMigrationStatus(t, statuses, "core", migration.StateCurrent, true)
	assertMigrationStatus(t, statuses, "directory", migration.StateAbsent, false)
	_ = directory
}

func TestPostgresProbeReportsConfiguredDirectoryMigrationStatus(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	core, directory := testMigrationManifests(t)
	ctx, cancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
	defer cancel()
	if _, err := (migration.Migrator{
		DatabaseURL:     database.AdminURL(),
		ApplicationRole: "stacks_app",
		Manifests:       []migration.Manifest{core, directory},
	}).Apply(ctx); err != nil {
		t.Fatalf("install core and directory manifests: %v", err)
	}
	connection, err := postgres.Open(ctx, database.ApplicationURL())
	if err != nil {
		t.Fatalf("open canonical database: %v", err)
	}
	defer connection.Close()
	probe := NewPostgresProbeWithScopes(
		connection,
		[]migration.Scope{"core", "directory"},
	)
	statuses, err := probe.MigrationStatus(ctx)
	if err != nil {
		t.Fatalf("MigrationStatus() error = %v", err)
	}
	assertMigrationStatus(t, statuses, "core", migration.StateCurrent, true)
	assertMigrationStatus(t, statuses, "directory", migration.StateCurrent, true)
}

func testMigrationManifests(t *testing.T) (migration.Manifest, migration.Manifest) {
	t.Helper()
	core, err := coremigrations.Manifest()
	if err != nil {
		t.Fatalf("load core manifest: %v", err)
	}
	directory, err := directorymigrations.Manifest()
	if err != nil {
		t.Fatalf("load directory manifest: %v", err)
	}
	return core, directory
}

func assertMigrationStatus(
	t *testing.T,
	statuses []migration.ScopeStatus,
	scope migration.Scope,
	state migration.State,
	configured bool,
) {
	t.Helper()
	for _, status := range statuses {
		if status.Scope == scope {
			if status.State != state || status.Configured != configured {
				t.Fatalf("scope %q status = %#v, want state=%q configured=%t",
					scope, status, state, configured)
			}
			return
		}
	}
	t.Fatalf("scope %q status is missing", scope)
}
