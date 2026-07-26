package app

import (
	"context"
	"fmt"
	"io"
	"testing"

	"stacks/internal/cli"
	"stacks/internal/config"
)

func TestExecuteRoutesDBMigrateAndDBStatusThroughLazyCommandProvider(t *testing.T) {
	for _, command := range []config.Command{config.CommandDBMigrate, config.CommandDBStatus} {
		command := command
		t.Run(string(command), func(t *testing.T) {
			settings := config.Settings{Database: config.DatabaseSettings{
				URL:             "postgres://synthetic-app",
				MigrationURL:    "postgres://synthetic-admin",
				ApplicationRole: "stacks_app",
				Scopes:          []config.DatabaseScope{config.DatabaseScopeCore},
			}}
			providerCalls := 0
			commandCalls := 0
			provider := CommandProviderFunc(func(
				context.Context,
				config.Settings,
				io.Writer,
				io.Writer,
			) (map[string]cli.Command, error) {
				providerCalls++
				return map[string]cli.Command{string(command): cli.CommandFunc(func(
					context.Context,
					cli.Invocation,
				) error {
					commandCalls++
					return nil
				})}, nil
			})
			err := executeWithSettings(
				t.Context(),
				[]string{string(command)},
				settings,
				RuntimeFunc(func(context.Context, config.Settings) error {
					return fmt.Errorf("serve should not run")
				}),
				provider,
				io.Discard,
				io.Discard,
			)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if providerCalls != 1 || commandCalls != 1 {
				t.Fatalf("provider/command calls = %d/%d, want 1/1", providerCalls, commandCalls)
			}
		})
	}
}
