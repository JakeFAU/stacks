package migration

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	"github.com/jackc/pgx/v5"
)

const (
	testApplicationRole  = "stacks_app"
	integrationTimeout   = 10 * time.Second
	cancellationSQLDelay = 30 * time.Second
)

type applyOutcome struct {
	result ApplyResult
	err    error
}

func TestMigratorCreatesIndependentLedgers(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	result, err := testMigrator(database, testCoreManifest(), testDirectoryManifest()).Apply(ctx)
	if err != nil {
		t.Fatalf("Migrator.Apply() error = %v", err)
	}
	if len(result.Scopes) != 2 {
		t.Fatalf("applied scope count = %d, want 2", len(result.Scopes))
	}
	for index, wantScope := range []Scope{"core", "directory"} {
		got := result.Scopes[index]
		if got.Scope != wantScope || got.CurrentVersion != 1 ||
			len(got.Applied) != 1 || got.Applied[0] != 1 {
			t.Fatalf("scope result %d = %#v, want scope %q applied/current version 1", index, got, wantScope)
		}
	}

	connection := openTestConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())
	for _, ledger := range []string{"core_version", "directory_version"} {
		var version int64
		if err := connection.QueryRow(
			ctx,
			"SELECT version FROM "+qualifiedLedger(ledger),
		).Scan(&version); err != nil {
			t.Fatalf("read %s ledger: %v", ledger, err)
		}
		if version != 1 {
			t.Fatalf("%s ledger version = %d, want 1", ledger, version)
		}
	}
}

func TestMigratorExactRetryPerformsNoWrites(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	migrator := testMigrator(database, testCoreManifest())

	if _, err := migrator.Apply(ctx); err != nil {
		t.Fatalf("initial Migrator.Apply() error = %v", err)
	}
	connection := openTestConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())
	var beforeXID string
	var beforeAppliedAt time.Time
	if err := connection.QueryRow(
		ctx,
		"SELECT xmin::text, applied_at FROM "+qualifiedLedger("core_version")+" WHERE version = 1",
	).Scan(&beforeXID, &beforeAppliedAt); err != nil {
		t.Fatalf("read initial ledger identity: %v", err)
	}

	result, err := migrator.Apply(ctx)
	if err != nil {
		t.Fatalf("repeated Migrator.Apply() error = %v", err)
	}
	if len(result.Scopes) != 1 || len(result.Scopes[0].Applied) != 0 ||
		result.Scopes[0].CurrentVersion != 1 {
		t.Fatalf("repeated apply result = %#v, want current version 1 with no applied versions", result)
	}
	var afterXID string
	var afterAppliedAt time.Time
	if err := connection.QueryRow(
		ctx,
		"SELECT xmin::text, applied_at FROM "+qualifiedLedger("core_version")+" WHERE version = 1",
	).Scan(&afterXID, &afterAppliedAt); err != nil {
		t.Fatalf("read repeated ledger identity: %v", err)
	}
	if afterXID != beforeXID || !afterAppliedAt.Equal(beforeAppliedAt) {
		t.Fatalf(
			"repeated apply changed ledger identity from xid=%s at=%s to xid=%s at=%s",
			beforeXID,
			beforeAppliedAt,
			afterXID,
			afterAppliedAt,
		)
	}
}

func TestMigratorRejectsChangedAppliedChecksumBeforePendingWork(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	initialCore := testCoreManifest()
	initialDirectory := testDirectoryManifest()
	if _, err := testMigrator(database, initialCore, initialDirectory).Apply(ctx); err != nil {
		t.Fatalf("initial Migrator.Apply() error = %v", err)
	}

	pendingCore := testCoreManifest()
	pendingCore.Migrations = append(pendingCore.Migrations, testMigration(
		2,
		"pending",
		"CREATE TABLE stacks_core.pending_work (id bigint PRIMARY KEY)",
	))
	changedDirectory := testDirectoryManifest()
	setMigrationSQL(
		&changedDirectory.Migrations[0],
		changedDirectory.Migrations[0].SQL+"\n-- changed applied bytes",
	)
	_, err := testMigrator(database, pendingCore, changedDirectory).Apply(ctx)
	if err == nil {
		t.Fatal("Migrator.Apply() error = nil, want applied checksum mismatch")
	}
	if !errors.Is(err, ErrAppliedMigrationMismatch) {
		t.Fatalf("Migrator.Apply() error = %v, want ErrAppliedMigrationMismatch", err)
	}

	connection := openTestConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())
	var pendingTable *string
	if err := connection.QueryRow(ctx, "SELECT to_regclass('stacks_core.pending_work')::text").Scan(
		&pendingTable,
	); err != nil {
		t.Fatalf("inspect pending table: %v", err)
	}
	if pendingTable != nil {
		t.Fatalf("pending table = %q, want no work after checksum mismatch", *pendingTable)
	}
}

func TestMigratorRollsBackFailedVersionAndLedgerInsert(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	manifest := testCoreManifest()
	setMigrationSQL(&manifest.Migrations[0], `
		CREATE SCHEMA stacks_core;
		CREATE TABLE stacks_core.rolled_back (id bigint PRIMARY KEY);
		SELECT 1 / 0;
	`)

	if _, err := testMigrator(database, manifest).Apply(ctx); err == nil {
		t.Fatal("Migrator.Apply() error = nil, want failing migration")
	} else if !strings.Contains(err.Error(), "division by zero") {
		t.Fatalf("Migrator.Apply() error = %v, want synthetic division-by-zero failure", err)
	}
	connection := openTestConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())
	var rolledBackTable *string
	if err := connection.QueryRow(ctx, "SELECT to_regclass('stacks_core.rolled_back')::text").Scan(
		&rolledBackTable,
	); err != nil {
		t.Fatalf("inspect rolled-back table: %v", err)
	}
	if rolledBackTable != nil {
		t.Fatalf("rolled-back table = %q, want absent", *rolledBackTable)
	}
	var ledgerRows int
	if err := connection.QueryRow(
		ctx,
		"SELECT count(*) FROM "+qualifiedLedger("core_version"),
	).Scan(&ledgerRows); err != nil {
		t.Fatalf("count failed migration ledger rows: %v", err)
	}
	if ledgerRows != 0 {
		t.Fatalf("failed migration ledger rows = %d, want 0", ledgerRows)
	}
}

func TestMigratorAppliesVersionAwareGrantsOnCleanInstall(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	manifest := testVersionedGrantManifest()

	result, err := testMigrator(database, manifest).Apply(ctx)
	if err != nil {
		t.Fatalf("clean Migrator.Apply() error = %v", err)
	}
	if len(result.Scopes) != 1 ||
		len(result.Scopes[0].Applied) != 2 ||
		result.Scopes[0].Applied[0] != 1 ||
		result.Scopes[0].Applied[1] != 2 {
		t.Fatalf("clean versioned apply result = %#v, want versions 1 and 2", result)
	}
	assertApplicationCanUseVersionTwoGrant(t, ctx, database)
}

func TestMigratorAppliesVersionAwareGrantsOnUpgrade(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	initial := testVersionedGrantManifest()
	initial.Migrations = initial.Migrations[:1]
	initial.ApplicationTableGrants = initial.ApplicationTableGrants[:2]
	if _, err := testMigrator(database, initial).Apply(ctx); err != nil {
		t.Fatalf("initial version 1 Migrator.Apply() error = %v", err)
	}

	result, err := testMigrator(database, testVersionedGrantManifest()).Apply(ctx)
	if err != nil {
		t.Fatalf("upgrade Migrator.Apply() error = %v", err)
	}
	if len(result.Scopes) != 1 ||
		len(result.Scopes[0].Applied) != 1 ||
		result.Scopes[0].Applied[0] != 2 {
		t.Fatalf("upgrade apply result = %#v, want only version 2", result)
	}
	assertApplicationCanUseVersionTwoGrant(t, ctx, database)
}

func TestMigratorRollsBackFinalVersionWhenDeclaredGrantIsMissing(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		columns    []string
		versionSQL string
	}{
		{
			name:       "table",
			target:     "missing_records",
			versionSQL: "CREATE TABLE stacks_core.version_two_marker (id bigint PRIMARY KEY)",
		},
		{
			name:    "update column",
			target:  "version_two_marker",
			columns: []string{"missing_column"},
			versionSQL: `
				CREATE TABLE stacks_core.version_two_marker (
					id bigint PRIMARY KEY,
					present_column text NOT NULL
				)`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			database := postgrestest.NewDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
			defer cancel()
			initial := testCoreManifest()
			if _, err := testMigrator(database, initial).Apply(ctx); err != nil {
				t.Fatalf("initial Migrator.Apply() error = %v", err)
			}

			manifest := testCoreManifest()
			manifest.Migrations = append(
				manifest.Migrations,
				testMigration(2, "final_grant", test.versionSQL),
			)
			privileges := []Privilege{PrivilegeSelect}
			if len(test.columns) > 0 {
				privileges = append(privileges, PrivilegeUpdate)
			}
			manifest.ApplicationTableGrants = append(
				manifest.ApplicationTableGrants,
				TableGrant{
					Schema:        "stacks_core",
					Table:         test.target,
					Privileges:    privileges,
					UpdateColumns: test.columns,
				},
			)

			if _, err := testMigrator(database, manifest).Apply(ctx); err == nil {
				t.Fatal("final Migrator.Apply() error = nil, want missing grant target rejection")
			}
			connection := openTestConnection(t, ctx, database.AdminURL())
			defer connection.Close(context.Background())
			var marker *string
			if err := connection.QueryRow(
				ctx,
				"SELECT to_regclass('stacks_core.version_two_marker')::text",
			).Scan(&marker); err != nil {
				t.Fatalf("inspect rolled-back final table: %v", err)
			}
			if marker != nil {
				t.Fatalf("final migration table = %q, want rollback", *marker)
			}
			var versions []int64
			rows, err := connection.Query(
				ctx,
				"SELECT version FROM "+qualifiedLedger("core_version")+" ORDER BY version",
			)
			if err != nil {
				t.Fatalf("query final migration ledger: %v", err)
			}
			for rows.Next() {
				var version int64
				if err := rows.Scan(&version); err != nil {
					rows.Close()
					t.Fatalf("scan final migration ledger: %v", err)
				}
				versions = append(versions, version)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatalf("iterate final migration ledger: %v", err)
			}
			rows.Close()
			if len(versions) != 1 || versions[0] != 1 {
				t.Fatalf("versions after rejected final grant = %v, want [1]", versions)
			}
		})
	}
}

func TestMigratorSerializesConcurrentApplyWithoutSleep(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	lockConnection := openTestConnection(t, ctx, database.AdminURL())
	defer lockConnection.Close(context.Background())
	if _, err := lockConnection.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey()); err != nil {
		t.Fatalf("hold migration advisory lock: %v", err)
	}
	lockHeld := true
	t.Cleanup(func() {
		if lockHeld {
			_, _ = lockConnection.Exec(
				context.Background(),
				"SELECT pg_advisory_unlock($1)",
				advisoryLockKey(),
			)
		}
	})

	const applicationName = "stacks_migrator_waiter"
	migrator := testMigrator(database, testCoreManifest())
	migrator.DatabaseURL = withApplicationName(t, migrator.DatabaseURL, applicationName)
	completed := make(chan applyOutcome, 1)
	go func() {
		result, err := migrator.Apply(ctx)
		completed <- applyOutcome{result: result, err: err}
	}()

	observer := openTestConnection(t, ctx, database.AdminURL())
	defer observer.Close(context.Background())
	waitForAdvisoryLockState(t, ctx, observer, applicationName, false, completed)
	if _, err := lockConnection.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey()); err != nil {
		t.Fatalf("release migration advisory lock: %v", err)
	}
	lockHeld = false

	outcome := <-completed
	if outcome.err != nil {
		t.Fatalf("concurrent Migrator.Apply() error = %v", outcome.err)
	}
	if len(outcome.result.Scopes) != 1 ||
		len(outcome.result.Scopes[0].Applied) != 1 ||
		outcome.result.Scopes[0].Applied[0] != 1 {
		t.Fatalf("concurrent apply result = %#v, want one application", outcome.result)
	}
}

func TestMigratorPreservesCancellationAndReleasesSessionLock(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	ctx, cancelDeadline := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancelDeadline()
	const applicationName = "stacks_migrator_canceled"
	manifest := testCoreManifest()
	setMigrationSQL(&manifest.Migrations[0], fmt.Sprintf(
		"CREATE SCHEMA stacks_core; SELECT pg_sleep(%d)",
		int(cancellationSQLDelay/time.Second),
	))
	migrator := testMigrator(database, manifest)
	migrator.DatabaseURL = withApplicationName(t, migrator.DatabaseURL, applicationName)
	applyContext, cancelApply := context.WithCancel(ctx)
	completed := make(chan error, 1)
	go func() {
		_, err := migrator.Apply(applyContext)
		completed <- err
	}()

	observer := openTestConnection(t, ctx, database.AdminURL())
	defer observer.Close(context.Background())
	waitForGrantedAdvisoryLock(t, ctx, observer, applicationName, completed)
	cancelApply()
	err := <-completed
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Migrator.Apply() error = %v, want context.Canceled", err)
	}

	var acquired bool
	if err := observer.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey()).Scan(
		&acquired,
	); err != nil {
		t.Fatalf("try migration advisory lock after cancellation: %v", err)
	}
	if !acquired {
		t.Fatal("migration advisory lock remained held after cancellation")
	}
	if _, err := observer.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey()); err != nil {
		t.Fatalf("release observed migration advisory lock: %v", err)
	}
}

func TestApplicationRoleCanInspectButCannotMigrate(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	manifest := testCoreManifest()
	if _, err := testMigrator(database, manifest).Apply(ctx); err != nil {
		t.Fatalf("admin Migrator.Apply() error = %v", err)
	}

	application := openTestConnection(t, ctx, database.ApplicationURL())
	defer application.Close(context.Background())
	var version int64
	if err := application.QueryRow(
		ctx,
		"SELECT version FROM "+qualifiedLedger("core_version"),
	).Scan(&version); err != nil {
		t.Fatalf("application role inspect migration ledger: %v", err)
	}
	if version != 1 {
		t.Fatalf("application-visible migration version = %d, want 1", version)
	}
	if _, err := application.Exec(
		ctx,
		"INSERT INTO "+qualifiedLedger("core_version")+
			" (version, name, checksum) VALUES (2, 'forbidden', decode(repeat('00', 32), 'hex'))",
	); err == nil {
		t.Fatal("application role inserted migration ledger row")
	}
	if _, err := application.Exec(
		ctx,
		"UPDATE "+qualifiedLedger("core_version")+" SET name = 'forbidden' WHERE version = 1",
	); err == nil {
		t.Fatal("application role updated migration ledger row")
	}
	if _, err := application.Exec(
		ctx,
		"DELETE FROM "+qualifiedLedger("core_version")+" WHERE version = 1",
	); err == nil {
		t.Fatal("application role deleted migration ledger row")
	}
	if _, err := application.Exec(ctx, "CREATE TABLE stacks_core.forbidden (id bigint)"); err == nil {
		t.Fatal("application role created a scope-owned table")
	}

	applicationMigrator := Migrator{
		DatabaseURL:     database.ApplicationURL(),
		ApplicationRole: testApplicationRole,
		Manifests:       []Manifest{manifest},
	}
	if _, err := applicationMigrator.Apply(ctx); err == nil {
		t.Fatal("application-role Migrator.Apply() succeeded")
	}
}

func TestApplicationRoleReceivesOnlyDeclaredUpdateColumns(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	manifest := testCoreManifest()
	setMigrationSQL(&manifest.Migrations[0], `
		CREATE SCHEMA stacks_core;
		CREATE TABLE stacks_core.records (
			id bigint PRIMARY KEY,
			status text NOT NULL,
			note text NOT NULL,
			protected text NOT NULL
		);
		INSERT INTO stacks_core.records (id, status, note, protected)
		VALUES (1, 'pending', 'initial', 'immutable');
	`)
	manifest.ApplicationTableGrants[1] = TableGrant{
		Schema:        "stacks_core",
		Table:         "records",
		Privileges:    []Privilege{PrivilegeSelect, PrivilegeUpdate},
		UpdateColumns: []string{"status", "note"},
	}
	if _, err := testMigrator(database, manifest).Apply(ctx); err != nil {
		t.Fatalf("admin Migrator.Apply() error = %v", err)
	}

	application := openTestConnection(t, ctx, database.ApplicationURL())
	defer application.Close(context.Background())
	if _, err := application.Exec(
		ctx,
		"UPDATE stacks_core.records SET status = 'complete', note = 'updated' WHERE id = 1",
	); err != nil {
		t.Fatalf("application role update declared columns: %v", err)
	}
	if _, err := application.Exec(
		ctx,
		"UPDATE stacks_core.records SET protected = 'changed' WHERE id = 1",
	); err == nil {
		t.Fatal("application role updated undeclared column")
	}
	if _, err := application.Exec(
		ctx,
		"INSERT INTO stacks_core.records (id, status, note, protected) VALUES (2, 'new', '', '')",
	); err == nil {
		t.Fatal("application role inserted without INSERT grant")
	}
}

func testMigrator(database postgrestest.Database, manifests ...Manifest) Migrator {
	return Migrator{
		DatabaseURL:     database.AdminURL(),
		ApplicationRole: testApplicationRole,
		Manifests:       manifests,
	}
}

func testCoreManifest() Manifest {
	manifest := validManifest("core", "core_version")
	setMigrationSQL(&manifest.Migrations[0], `
		CREATE SCHEMA stacks_core;
		CREATE TABLE stacks_core.records (id bigint PRIMARY KEY);
	`)
	manifest.OwnedSchemaTrees = []string{"stacks_core"}
	manifest.OwnedObjects = []OwnedObject{
		{Kind: ObjectSchema, Schema: "stacks_migrations", Name: "stacks_migrations"},
		{Kind: ObjectTable, Schema: "stacks_migrations", Name: "core_version"},
	}
	manifest.ApplicationSchemaGrants = []SchemaGrant{
		{Schema: "stacks_migrations", Privileges: []Privilege{PrivilegeUsage}},
		{Schema: "stacks_core", Privileges: []Privilege{PrivilegeUsage}},
	}
	manifest.ApplicationTableGrants = []TableGrant{
		{Schema: "stacks_migrations", Table: "core_version", Privileges: []Privilege{PrivilegeSelect}},
		{Schema: "stacks_core", Table: "records", Privileges: []Privilege{PrivilegeSelect}},
	}
	return manifest
}

func testDirectoryManifest() Manifest {
	manifest := validManifest("directory", "directory_version")
	setMigrationSQL(&manifest.Migrations[0], `
		CREATE SCHEMA stacks_directory;
		CREATE TABLE stacks_directory.profiles (id bigint PRIMARY KEY);
	`)
	manifest.OwnedSchemaTrees = []string{"stacks_directory"}
	manifest.OwnedObjects = []OwnedObject{{
		Kind: ObjectTable, Schema: "stacks_migrations", Name: "directory_version",
	}}
	manifest.ApplicationSchemaGrants = []SchemaGrant{{
		Schema: "stacks_directory", Privileges: []Privilege{PrivilegeUsage},
	}}
	manifest.ApplicationTableGrants = []TableGrant{
		{Schema: "stacks_migrations", Table: "directory_version", Privileges: []Privilege{PrivilegeSelect}},
		{Schema: "stacks_directory", Table: "profiles", Privileges: []Privilege{PrivilegeSelect}},
	}
	return manifest
}

func testVersionedGrantManifest() Manifest {
	manifest := testCoreManifest()
	manifest.Migrations = append(manifest.Migrations, testMigration(
		2,
		"future_records",
		`CREATE TABLE stacks_core.future_records (
			id bigint PRIMARY KEY,
			status text NOT NULL,
			protected text NOT NULL
		);
		INSERT INTO stacks_core.future_records (id, status, protected)
		VALUES (1, 'pending', 'immutable')`,
	))
	manifest.ApplicationTableGrants = append(
		manifest.ApplicationTableGrants,
		TableGrant{
			Schema:        "stacks_core",
			Table:         "future_records",
			Privileges:    []Privilege{PrivilegeSelect, PrivilegeUpdate},
			UpdateColumns: []string{"status"},
		},
	)
	return manifest
}

func assertApplicationCanUseVersionTwoGrant(
	t *testing.T,
	ctx context.Context,
	database postgrestest.Database,
) {
	t.Helper()
	application := openTestConnection(t, ctx, database.ApplicationURL())
	defer application.Close(context.Background())
	if _, err := application.Exec(
		ctx,
		"UPDATE stacks_core.future_records SET status = 'complete' WHERE id = 1",
	); err != nil {
		t.Fatalf("application update version 2 declared column: %v", err)
	}
	if _, err := application.Exec(
		ctx,
		"UPDATE stacks_core.future_records SET protected = 'changed' WHERE id = 1",
	); err == nil {
		t.Fatal("application updated version 2 protected column")
	}
	var status string
	if err := application.QueryRow(
		ctx,
		"SELECT status FROM stacks_core.future_records WHERE id = 1",
	).Scan(&status); err != nil {
		t.Fatalf("application inspect version 2 table: %v", err)
	}
	if status != "complete" {
		t.Fatalf("version 2 status = %q, want complete", status)
	}
}

func openTestConnection(t *testing.T, ctx context.Context, databaseURL string) *pgx.Conn {
	t.Helper()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to isolated test database: %v", err)
	}
	return connection
}

func qualifiedLedger(ledger string) string {
	return pgx.Identifier{"stacks_migrations", ledger}.Sanitize()
}

func withApplicationName(t *testing.T, databaseURL, applicationName string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse isolated database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("application_name", applicationName)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func waitForAdvisoryLockState(
	t *testing.T,
	ctx context.Context,
	observer *pgx.Conn,
	applicationName string,
	granted bool,
	completed <-chan applyOutcome,
) {
	t.Helper()
	for {
		select {
		case outcome := <-completed:
			t.Fatalf("Migrator.Apply() completed before lock observation: result=%#v error=%v", outcome.result, outcome.err)
		default:
		}
		var observed bool
		err := observer.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_locks AS lock
				JOIN pg_stat_activity AS activity USING (pid)
				WHERE activity.application_name = $1
				  AND lock.locktype = 'advisory'
				  AND lock.granted = $2
			)`,
			applicationName,
			granted,
		).Scan(&observed)
		if err != nil {
			t.Fatalf("observe advisory lock waiter: %v", err)
		}
		if observed {
			return
		}
	}
}

func waitForGrantedAdvisoryLock(
	t *testing.T,
	ctx context.Context,
	observer *pgx.Conn,
	applicationName string,
	completed <-chan error,
) {
	t.Helper()
	for {
		select {
		case err := <-completed:
			t.Fatalf("Migrator.Apply() completed before granted lock observation: %v", err)
		default:
		}
		var observed bool
		err := observer.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_locks AS lock
				JOIN pg_stat_activity AS activity USING (pid)
				WHERE activity.application_name = $1
				  AND lock.locktype = 'advisory'
				  AND lock.granted
			)`,
			applicationName,
		).Scan(&observed)
		if err != nil {
			t.Fatalf("observe granted advisory lock: %v", err)
		}
		if observed {
			return
		}
	}
}
