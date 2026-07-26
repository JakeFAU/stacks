package doctor

import (
	"context"
	"testing"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
)

type migrationStatusDatabase struct {
	statuses []migration.ScopeStatus
}

func (database migrationStatusDatabase) Ping(context.Context) error {
	return nil
}

func (database migrationStatusDatabase) MigrationStatus(
	context.Context,
) ([]migration.ScopeStatus, error) {
	return database.statuses, nil
}

func TestMigrationStatusReportsStableCoreAndDirectoryChecks(t *testing.T) {
	t.Parallel()

	report := (Service{Database: migrationStatusDatabase{statuses: []migration.ScopeStatus{
		{
			Scope: "core", State: migration.StateCurrent,
			AppliedVersion: 3, ExpectedVersion: 3, Configured: true,
		},
		{
			Scope: "directory", State: migration.StateAbsent,
			ExpectedVersion: 1, Configured: false,
		},
	}}}).Check(t.Context())
	core := findCheck(t, report, CheckDatabaseMigrationsCore)
	if core.Status != StatusOK {
		t.Fatalf("core migration check = %#v, want ok", core)
	}
	directory := findCheck(t, report, CheckDatabaseMigrationsDirectory)
	if directory.Status != StatusOK {
		t.Fatalf("directory migration check = %#v, want optional absence healthy", directory)
	}
}
