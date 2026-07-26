package migration_test

import (
	"context"
	"crypto/sha256"
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

func TestCleanInstallExpectedFingerprintsAreReproducibleAcrossTwoDatabases(t *testing.T) {
	var first map[migration.Scope][sha256.Size]byte
	for install := 0; install < 2; install++ {
		database, manifests := installKnownScopes(t)
		ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
		got := make(map[migration.Scope][sha256.Size]byte, len(manifests))
		for _, manifest := range manifests {
			fingerprint, err := migration.InspectFingerprint(
				ctx,
				database.AdminURL(),
				manifest,
			)
			if err != nil {
				cancel()
				t.Fatalf("inspect clean %s fingerprint: %v", manifest.Scope, err)
			}
			got[manifest.Scope] = fingerprint
			if fingerprint != manifest.ExpectedFingerprint {
				cancel()
				t.Fatalf(
					"clean %s fingerprint = %x, expected %x",
					manifest.Scope,
					fingerprint,
					manifest.ExpectedFingerprint,
				)
			}
		}
		cancel()
		if install == 0 {
			first = got
			continue
		}
		for scope, fingerprint := range first {
			if got[scope] != fingerprint {
				t.Fatalf(
					"clean %s fingerprints differ across installs: %x != %x",
					scope,
					fingerprint,
					got[scope],
				)
			}
		}
	}
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

func TestInspectorReportsEveryOwnedSemanticMutationAsSchemaDrift(t *testing.T) {
	tests := []struct {
		name     string
		mutation string
	}{
		{
			name:     "column type",
			mutation: "ALTER TABLE stacks_core.source_documents ALTER COLUMN provider TYPE varchar(255)",
		},
		{
			name:     "column nullability",
			mutation: "ALTER TABLE stacks_core.source_documents ALTER COLUMN provider DROP NOT NULL",
		},
		{
			name:     "column default",
			mutation: "ALTER TABLE stacks_core.source_documents ALTER COLUMN current_version_id SET DEFAULT ''",
		},
		{
			name:     "constraint",
			mutation: "ALTER TABLE stacks_core.source_documents ADD CONSTRAINT synthetic_provider_check CHECK (length(provider) < 256)",
		},
		{
			name: "index definition",
			mutation: `
				DROP INDEX stacks_core.extraction_attempts_one_active;
				CREATE UNIQUE INDEX extraction_attempts_one_active
				    ON stacks_core.extraction_attempts (run_id, claimed_at)
				    WHERE state = 'active'`,
		},
		{
			name: "index operational state",
			mutation: `
				SET allow_system_table_mods = on;
				UPDATE pg_catalog.pg_index
				   SET indisvalid = false,
				       indisready = false
				 WHERE indexrelid = 'stacks_core.extraction_attempts_one_active'::regclass`,
		},
		{
			name: "function",
			mutation: `
				CREATE OR REPLACE FUNCTION stacks_core.enforce_observation_cited()
				RETURNS trigger LANGUAGE plpgsql AS $function$
				BEGIN
				    RETURN NULL;
				END;
				$function$`,
		},
		{
			name:     "trigger",
			mutation: "ALTER TABLE stacks_core.observations DISABLE TRIGGER observations_require_evidence",
		},
		{
			name:     "exact ledger",
			mutation: "ALTER TABLE stacks_migrations.core_version ADD COLUMN synthetic_drift text",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			database, manifests := installKnownScopes(t)
			ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
			defer cancel()
			connection := openStatusConnection(t, ctx, database.AdminURL())
			defer connection.Close(context.Background())

			if _, err := connection.Exec(ctx, test.mutation); err != nil {
				t.Fatalf("apply semantic mutation: %v", err)
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
		})
	}
}

func TestInspectorIgnoresUnownedCatalogObjects(t *testing.T) {
	database, manifests := installKnownScopes(t)
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	connection := openStatusConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())

	if _, err := connection.Exec(ctx, `
		CREATE SCHEMA synthetic_unowned;
		CREATE TABLE synthetic_unowned.items (id bigint PRIMARY KEY);
	`); err != nil {
		t.Fatalf("create unowned catalog objects: %v", err)
	}
	statuses, err := (migration.Inspector{
		DatabaseURL: database.ApplicationURL(),
		Manifests:   manifests,
		Configured:  []migration.Scope{"core", "directory"},
	}).Status(ctx)
	if err != nil {
		t.Fatalf("Inspector.Status() error = %v", err)
	}
	assertScopeStatus(t, statuses, "core", migration.StateCurrent, 3, 3, true)
	assertScopeStatus(t, statuses, "directory", migration.StateCurrent, 1, 1, true)
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
