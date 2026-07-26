package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
)

type fakeMigrationApplier struct {
	result migration.ApplyResult
}

func (fake fakeMigrationApplier) Apply(context.Context) (migration.ApplyResult, error) {
	return fake.result, nil
}

type fakeMigrationInspector struct {
	statuses []migration.ScopeStatus
}

func (fake fakeMigrationInspector) Status(context.Context) ([]migration.ScopeStatus, error) {
	return fake.statuses, nil
}

func TestDBMigratePrintsOnlyBoundedScopeVersions(t *testing.T) {
	t.Parallel()

	var output strings.Builder
	err := (DBMigrateCommand{
		Migrator: fakeMigrationApplier{result: migration.ApplyResult{Scopes: []migration.ScopeApplyResult{{
			Scope: "core", Applied: []int64{2, 3}, CurrentVersion: 3,
		}}}},
		Output: &output,
	}).Run(t.Context(), Invocation{Command: CommandDBMigrate})
	if err != nil {
		t.Fatalf("DBMigrateCommand.Run() error = %v", err)
	}
	if got, want := output.String(), "scope=core applied=2,3 current=3\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestDBStatusPrintsFixedScopeStateVersionConfiguredRecords(t *testing.T) {
	t.Parallel()

	var output strings.Builder
	err := (DBStatusCommand{
		Inspector: fakeMigrationInspector{statuses: []migration.ScopeStatus{
			{
				Scope: "core", State: migration.StateCurrent,
				AppliedVersion: 3, ExpectedVersion: 3, Configured: true,
			},
			{
				Scope: "directory", State: migration.StateAbsent,
				AppliedVersion: 0, ExpectedVersion: 1, Configured: false,
			},
		}},
		Output: &output,
	}).Run(t.Context(), Invocation{Command: CommandDBStatus})
	if err != nil {
		t.Fatalf("DBStatusCommand.Run() error = %v", err)
	}
	want := "" +
		"scope=core state=current applied=3 expected=3 configured=true\n" +
		"scope=directory state=absent applied=0 expected=1 configured=false\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
