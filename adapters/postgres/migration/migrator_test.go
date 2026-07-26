package migration

import (
	"context"
	"strings"
	"testing"
)

func TestMigratorRejectsUnsafeApplicationRoleBeforeConnecting(t *testing.T) {
	t.Parallel()

	_, err := (Migrator{
		DatabaseURL:     "postgres://unreachable.invalid/example",
		ApplicationRole: `stacks_app"; DROP ROLE stacks_app; --`,
		Manifests:       []Manifest{validManifest("core", "core_version")},
	}).Apply(context.Background())
	if err == nil {
		t.Fatal("Migrator.Apply() error = nil, want unsafe application role rejection")
	}
	if !strings.Contains(err.Error(), "application role") {
		t.Fatalf("Migrator.Apply() error = %q, want application role context", err)
	}
}

func TestMigratorRejectsBlankDatabaseURLBeforeConnecting(t *testing.T) {
	t.Parallel()

	_, err := (Migrator{
		ApplicationRole: "stacks_app",
		Manifests:       []Manifest{validManifest("core", "core_version")},
	}).Apply(context.Background())
	if err == nil {
		t.Fatal("Migrator.Apply() error = nil, want blank database URL rejection")
	}
	if !strings.Contains(err.Error(), "database URL") {
		t.Fatalf("Migrator.Apply() error = %q, want database URL context", err)
	}
}

func TestMigratorDoesNotExposeInvalidDatabaseURL(t *testing.T) {
	t.Parallel()

	const (
		secret      = "synthetic-secret-value"
		databaseURL = "postgres://migration:" + secret + "@%zz/temporary"
	)
	_, err := (Migrator{
		DatabaseURL:     databaseURL,
		ApplicationRole: "stacks_app",
		Manifests:       []Manifest{testCoreManifest()},
	}).Apply(context.Background())
	if err == nil {
		t.Fatal("Migrator.Apply() error = nil, want invalid database URL rejection")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), databaseURL) {
		t.Fatalf("Migrator.Apply() error exposed database credentials: %q", err)
	}
}
