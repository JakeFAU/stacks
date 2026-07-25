package doctor

import (
	"context"
	"os"
	"testing"
	"time"
)

const postgresIntegrationTimeout = 10 * time.Second

func TestPostgresProbeReportsCurrentMigrations(t *testing.T) {
	databaseURL := os.Getenv("STACKS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("STACKS_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
	defer cancel()
	probe := NewPostgresProbe(databaseURL)
	defer probe.Close()

	if err := probe.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	current, err := probe.MigrationsCurrent(ctx)
	if err != nil {
		t.Fatalf("MigrationsCurrent() error = %v", err)
	}
	if !current {
		t.Fatal("MigrationsCurrent() = false, want true")
	}

	var directoryMigrationsApplied bool
	if err := probe.connection.QueryRow(ctx, migrationSetQuery, []int64{11, 12}).Scan(&directoryMigrationsApplied); err != nil {
		t.Fatalf("inspect current-document and directory migrations: %v", err)
	}
	if !directoryMigrationsApplied {
		t.Fatal("migration versions 11 and 12 are not both applied")
	}
}
