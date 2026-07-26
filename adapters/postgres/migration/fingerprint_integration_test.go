package migration

import (
	"context"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	"github.com/jackc/pgx/v5"
)

const fingerprintIntegrationTimeout = 20 * time.Second

func TestReadCatalogIndexOperationalStateChangesFingerprint(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), fingerprintIntegrationTimeout)
	defer cancel()
	connection, err := pgx.Connect(ctx, database.AdminURL())
	if err != nil {
		t.Fatalf("connect to isolated database: %v", err)
	}
	defer connection.Close(context.Background())

	if _, err := connection.Exec(ctx, `
		CREATE SCHEMA synthetic_owned;
		CREATE TABLE synthetic_owned.items (id bigint PRIMARY KEY, value text);
		CREATE INDEX items_value ON synthetic_owned.items (value);
	`); err != nil {
		t.Fatalf("create synthetic owned index: %v", err)
	}
	manifest := Manifest{OwnedSchemaTrees: []string{"synthetic_owned"}}
	before, err := readCatalog(ctx, connection, manifest)
	if err != nil {
		t.Fatalf("read usable index catalog: %v", err)
	}
	if _, err := connection.Exec(ctx, `
		SET allow_system_table_mods = on;
		UPDATE pg_catalog.pg_index
		   SET indisvalid = false,
		       indisready = false
		 WHERE indexrelid = 'synthetic_owned.items_value'::regclass;
	`); err != nil {
		t.Fatalf("make synthetic index unusable: %v", err)
	}
	after, err := readCatalog(ctx, connection, manifest)
	if err != nil {
		t.Fatalf("read unusable index catalog: %v", err)
	}
	if Fingerprint(before) == Fingerprint(after) {
		t.Fatal("unusable/not-ready index retained the usable schema fingerprint")
	}
}

func TestReadCatalogExactFunctionSelectsOnlyDeclaredOverload(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), fingerprintIntegrationTimeout)
	defer cancel()
	connection, err := pgx.Connect(ctx, database.AdminURL())
	if err != nil {
		t.Fatalf("connect to isolated database: %v", err)
	}
	defer connection.Close(context.Background())

	if _, err := connection.Exec(ctx, `
		CREATE SCHEMA synthetic_exact;
		CREATE FUNCTION synthetic_exact.normalize()
		RETURNS integer LANGUAGE sql IMMUTABLE AS 'SELECT 1';
	`); err != nil {
		t.Fatalf("create declared function overload: %v", err)
	}
	manifest := Manifest{OwnedObjects: []OwnedObject{{
		Kind:              ObjectFunction,
		Schema:            "synthetic_exact",
		Name:              "normalize",
		FunctionSignature: "()",
	}}}
	before, err := readCatalog(ctx, connection, manifest)
	if err != nil {
		t.Fatalf("read declared function overload: %v", err)
	}
	if _, err := connection.Exec(ctx, `
		CREATE FUNCTION synthetic_exact.normalize(value integer)
		RETURNS integer LANGUAGE sql IMMUTABLE AS 'SELECT value';
	`); err != nil {
		t.Fatalf("create unowned function overload: %v", err)
	}
	after, err := readCatalog(ctx, connection, manifest)
	if err != nil {
		t.Fatalf("read declared function after unowned overload: %v", err)
	}
	if Fingerprint(before) != Fingerprint(after) {
		t.Fatal("unowned function overload changed exact-owned function fingerprint")
	}
}

func TestReadCatalogSchemaTreeIncludesEveryFunctionOverload(t *testing.T) {
	database := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), fingerprintIntegrationTimeout)
	defer cancel()
	connection, err := pgx.Connect(ctx, database.AdminURL())
	if err != nil {
		t.Fatalf("connect to isolated database: %v", err)
	}
	defer connection.Close(context.Background())

	if _, err := connection.Exec(ctx, `
		CREATE SCHEMA synthetic_tree;
		CREATE FUNCTION synthetic_tree.normalize()
		RETURNS integer LANGUAGE sql IMMUTABLE AS 'SELECT 1';
	`); err != nil {
		t.Fatalf("create first schema-tree function overload: %v", err)
	}
	manifest := Manifest{OwnedSchemaTrees: []string{"synthetic_tree"}}
	before, err := readCatalog(ctx, connection, manifest)
	if err != nil {
		t.Fatalf("read schema-tree function: %v", err)
	}
	if _, err := connection.Exec(ctx, `
		CREATE FUNCTION synthetic_tree.normalize(value integer)
		RETURNS integer LANGUAGE sql IMMUTABLE AS 'SELECT value';
	`); err != nil {
		t.Fatalf("create second schema-tree function overload: %v", err)
	}
	after, err := readCatalog(ctx, connection, manifest)
	if err != nil {
		t.Fatalf("read schema-tree overloads: %v", err)
	}
	if Fingerprint(before) == Fingerprint(after) {
		t.Fatal("owned schema tree omitted a function overload")
	}
}
