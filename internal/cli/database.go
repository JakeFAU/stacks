package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
)

// MigrationApplier applies selected embedded migration manifests.
type MigrationApplier interface {
	Apply(context.Context) (migration.ApplyResult, error)
}

// MigrationInspector performs read-only status inspection of known manifests.
type MigrationInspector interface {
	Status(context.Context) ([]migration.ScopeStatus, error)
}

// DatabaseResetter performs the guarded local PostgreSQL reset.
type DatabaseResetter interface {
	Reset(context.Context, string, io.Writer) error
}

// DBMigrateCommand applies configured embedded migration scopes.
type DBMigrateCommand struct {
	Migrator MigrationApplier
	Output   io.Writer
}

// Run applies migrations without positional arguments.
func (command DBMigrateCommand) Run(ctx context.Context, _ Invocation) error {
	if command.Migrator == nil {
		return errors.New("db-migrate command: migrator is not configured")
	}
	result, err := command.Migrator.Apply(ctx)
	if err != nil {
		return err
	}
	output := command.Output
	if output == nil {
		output = io.Discard
	}
	for _, scope := range result.Scopes {
		applied := make([]string, len(scope.Applied))
		for index, version := range scope.Applied {
			applied[index] = strconv.FormatInt(version, 10)
		}
		if _, err := fmt.Fprintf(
			output,
			"scope=%s applied=%s current=%d\n",
			scope.Scope,
			strings.Join(applied, ","),
			scope.CurrentVersion,
		); err != nil {
			return fmt.Errorf("write db-migrate status: %w", err)
		}
	}
	return nil
}

// DBStatusCommand renders fixed, bounded records for every known scope.
type DBStatusCommand struct {
	Inspector MigrationInspector
	Output    io.Writer
}

// Run inspects status without positional arguments or database writes.
func (command DBStatusCommand) Run(ctx context.Context, _ Invocation) error {
	if command.Inspector == nil {
		return errors.New("db-status command: inspector is not configured")
	}
	statuses, err := command.Inspector.Status(ctx)
	if err != nil {
		return err
	}
	output := command.Output
	if output == nil {
		output = io.Discard
	}
	for _, status := range statuses {
		if _, err := fmt.Fprintf(
			output,
			"scope=%s state=%s applied=%d expected=%d configured=%t\n",
			status.Scope,
			status.State,
			status.AppliedVersion,
			status.ExpectedVersion,
			status.Configured,
		); err != nil {
			return fmt.Errorf("write db-status record: %w", err)
		}
	}
	return nil
}

// DBResetCommand delegates the exact confirmation to the guarded resetter.
type DBResetCommand struct {
	Resetter DatabaseResetter
	Output   io.Writer
}

// Run requires exactly one confirmation argument.
func (command DBResetCommand) Run(ctx context.Context, invocation Invocation) error {
	if command.Resetter == nil {
		return errors.New("db-reset command: resetter is not configured")
	}
	if len(invocation.Arguments) != 1 {
		return errors.New("db-reset command: invocation is invalid")
	}
	return command.Resetter.Reset(ctx, invocation.Arguments[0], command.Output)
}
