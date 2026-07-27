package query

import (
	"context"
	"errors"
	"testing"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	"github.com/jackc/pgx/v5"
)

func TestPostgresRepositoryPostgresCancellation(t *testing.T) {
	isolated := postgrestest.NewDatabase(t)
	manifest, err := coremigrations.Manifest()
	if err != nil {
		t.Fatalf("coremigrations.Manifest() error = %v", err)
	}
	applicationConfig, err := pgx.ParseConfig(isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("parse application database URL: %v", err)
	}
	if _, err := (migration.Migrator{
		DatabaseURL:     isolated.AdminURL(),
		ApplicationRole: applicationConfig.User,
		Manifests:       []migration.Manifest{manifest},
	}).Apply(context.Background()); err != nil {
		t.Fatalf("apply core migrations: %v", err)
	}
	database, err := postgres.Open(
		context.Background(),
		isolated.ApplicationURL(),
	)
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	t.Cleanup(database.Close)
	repository := PostgresRepository{Database: database}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = repository.Read(ctx, postgresTestReadSelection(t))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() error = %v, want context.Canceled", err)
	}
}
