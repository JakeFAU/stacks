package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"

	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/directorymigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/migration"
)

const migrationDatabaseURLEnvironment = "STACKS_MIGRATION_DATABASE_URL"

type fingerprintInspector func(
	context.Context,
	string,
	migration.Manifest,
) ([sha256.Size]byte, error)

func main() {
	if err := run(
		context.Background(),
		os.Args[1:],
		os.Getenv(migrationDatabaseURLEnvironment),
		os.Stdout,
		migration.InspectFingerprint,
	); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "schema fingerprint failed: %v\n", err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	args []string,
	databaseURL string,
	stdout io.Writer,
	inspect fingerprintInspector,
) error {
	if len(args) != 1 {
		return fmt.Errorf("exactly one scope is required")
	}
	var (
		manifest migration.Manifest
		err      error
	)
	switch migration.Scope(args[0]) {
	case "core":
		manifest, err = coremigrations.Manifest()
	case "directory":
		manifest, err = directorymigrations.Manifest()
	default:
		return fmt.Errorf("scope %q is unsupported", args[0])
	}
	if err != nil {
		return fmt.Errorf("load %s manifest: %w", args[0], err)
	}
	fingerprint, err := inspect(ctx, databaseURL, manifest)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "scope=%s sha256=%x\n", manifest.Scope, fingerprint)
	return err
}
