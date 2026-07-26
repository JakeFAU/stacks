package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"stacks/internal/cli"
	"stacks/internal/config"
)

type commandMigrationApplier struct{}

func (commandMigrationApplier) Apply(context.Context) (migration.ApplyResult, error) {
	return migration.ApplyResult{}, nil
}

func TestDatabaseCommandsEmitOnlyBoundedTelemetry(t *testing.T) {
	t.Parallel()

	const secret = "synthetic-secret"
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
	})
	settings := config.Settings{Database: config.DatabaseSettings{
		URL:             "postgres://app:" + secret + "@127.0.0.1/stacks",
		MigrationURL:    "postgres://admin:" + secret + "@127.0.0.1/stacks",
		ApplicationRole: "synthetic_role",
		Scopes:          []config.DatabaseScope{config.DatabaseScopeCore},
	}}
	runtime := pocCommandRuntime{
		newMigrationApplier: func(config.DatabaseSettings) cli.MigrationApplier {
			return commandMigrationApplier{}
		},
		newMigrationInspector: func(config.DatabaseSettings) cli.MigrationInspector {
			return commandMigrationInspector{}
		},
		newDatabaseResetter: func(config.DatabaseSettings) cli.DatabaseResetter {
			return commandDatabaseResetter{}
		},
	}
	commands, err := pocCommandProviderWithRuntime(
		t.Context(),
		settings,
		io.Discard,
		io.Discard,
		provider.Tracer("test"),
		nil,
		nil,
		runtime,
	)
	if err != nil {
		t.Fatalf("pocCommandProviderWithRuntime() error = %v", err)
	}
	for _, invocation := range []struct {
		command config.Command
		args    []string
	}{
		{command: config.CommandDBMigrate},
		{command: config.CommandDBStatus},
		{command: config.CommandDBReset, args: []string{"delete-local-stacks-postgres"}},
	} {
		if err := commands[string(invocation.command)].Run(t.Context(), invocation.args); err != nil {
			t.Fatalf("%s Run() error = %v", invocation.command, err)
		}
	}
	spans := recorder.Ended()
	if len(spans) != 3 {
		t.Fatalf("ended span count = %d, want 3", len(spans))
	}
	for _, span := range spans {
		rendered := fmt.Sprintf("%s %#v", span.Name(), span.Attributes())
		for _, forbidden := range []string{
			secret,
			settings.Database.URL,
			settings.Database.MigrationURL,
			settings.Database.ApplicationRole,
			"postgres_data",
			"/var/lib/postgresql",
		} {
			if strings.Contains(rendered, forbidden) {
				t.Fatalf("database span exposed forbidden value %q: %s", forbidden, rendered)
			}
		}
		for _, attribute := range span.Attributes() {
			if string(attribute.Key) != "stacks.outcome" {
				t.Fatalf("database span attribute %q is outside bounded vocabulary", attribute.Key)
			}
		}
	}
}

type commandMigrationInspector struct{}

func (commandMigrationInspector) Status(context.Context) ([]migration.ScopeStatus, error) {
	return nil, nil
}

type commandDatabaseResetter struct{}

func (commandDatabaseResetter) Reset(context.Context, string, io.Writer) error {
	return nil
}

func TestDatabaseCommandsConstructNoProviders(t *testing.T) {
	t.Parallel()

	settings := config.Settings{Database: config.DatabaseSettings{
		URL:             "postgres://synthetic-app",
		MigrationURL:    "postgres://synthetic-admin",
		ApplicationRole: "stacks_app",
		Scopes:          []config.DatabaseScope{config.DatabaseScopeCore},
	}}
	migrateConstructed := 0
	statusConstructed := 0
	resetConstructed := 0
	runtime := pocCommandRuntime{
		newMigrationApplier: func(config.DatabaseSettings) cli.MigrationApplier {
			migrateConstructed++
			return commandMigrationApplier{}
		},
		newMigrationInspector: func(config.DatabaseSettings) cli.MigrationInspector {
			statusConstructed++
			return commandMigrationInspector{}
		},
		newDatabaseResetter: func(config.DatabaseSettings) cli.DatabaseResetter {
			resetConstructed++
			return commandDatabaseResetter{}
		},
	}
	commands, err := pocCommandProviderWithRuntime(
		t.Context(),
		settings,
		io.Discard,
		io.Discard,
		nil,
		nil,
		nil,
		runtime,
	)
	if err != nil {
		t.Fatalf("pocCommandProviderWithRuntime() error = %v", err)
	}
	if err := commands[string(config.CommandDBMigrate)].Run(t.Context(), nil); err != nil {
		t.Fatalf("db-migrate Run() error = %v", err)
	}
	if err := commands[string(config.CommandDBStatus)].Run(t.Context(), nil); err != nil {
		t.Fatalf("db-status Run() error = %v", err)
	}
	if err := commands[string(config.CommandDBReset)].Run(
		t.Context(),
		[]string{"delete-local-stacks-postgres"},
	); err != nil {
		t.Fatalf("db-reset Run() error = %v", err)
	}
	if migrateConstructed != 1 || statusConstructed != 1 || resetConstructed != 1 {
		t.Fatalf("database constructor calls = migrate:%d status:%d reset:%d, want 1 each",
			migrateConstructed, statusConstructed, resetConstructed)
	}
}
