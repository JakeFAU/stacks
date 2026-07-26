package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	_ "github.com/JakeFAU/stacks/core/observation"
	"github.com/jackc/pgx/v5"
)

const adapterModulePath = "github.com/JakeFAU/stacks/adapters/postgres"

var errSyntheticRollback = errors.New("synthetic rollback")

func TestDatabaseRejectsBlankURL(t *testing.T) {
	t.Parallel()

	if _, err := postgres.Open(context.Background(), " \t"); err == nil ||
		!strings.Contains(err.Error(), "URL is required") {
		t.Fatalf("postgres.Open() error = %v, want required URL rejection", err)
	}
}

func TestDatabaseTransactionCommitsAndRollsBackCallbackError(t *testing.T) {
	isolated := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, isolated.AdminURL())
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	defer database.Close()

	if err := database.InTransaction(ctx, func(transaction *postgres.Transaction) error {
		_, err := transaction.Exec(
			ctx,
			"CREATE TABLE transaction_records (id bigint PRIMARY KEY)",
		)
		return err
	}); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
	err = database.InTransaction(ctx, func(transaction *postgres.Transaction) error {
		if _, err := transaction.Exec(ctx, "INSERT INTO transaction_records (id) VALUES (1)"); err != nil {
			return err
		}
		return errSyntheticRollback
	})
	if !errors.Is(err, errSyntheticRollback) {
		t.Fatalf("rollback transaction error = %v, want synthetic callback error", err)
	}

	connection, err := pgx.Connect(ctx, isolated.AdminURL())
	if err != nil {
		t.Fatalf("connect to inspect transaction: %v", err)
	}
	defer connection.Close(context.Background())
	var rows int
	if err := connection.QueryRow(ctx, "SELECT count(*) FROM transaction_records").Scan(&rows); err != nil {
		t.Fatalf("count transaction rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("rolled-back transaction rows = %d, want 0", rows)
	}
}

func TestPostgresAdapterProductionImportsStayWithinBoundary(t *testing.T) {
	t.Parallel()

	command := exec.Command("go", "list", "-json", "./...")
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list PostgreSQL adapter packages: %v", err)
	}

	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for {
		var listedPackage struct {
			ImportPath string
			Imports    []string
		}
		if err := decoder.Decode(&listedPackage); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode listed PostgreSQL adapter package: %v", err)
		}
		if !strings.HasPrefix(listedPackage.ImportPath, adapterModulePath) {
			continue
		}
		for _, imported := range listedPackage.Imports {
			if allowedProductionImport(imported) {
				continue
			}
			t.Errorf("PostgreSQL adapter package %q imports forbidden package %q", listedPackage.ImportPath, imported)
		}
	}
}

func allowedProductionImport(importPath string) bool {
	firstElement := importPath
	if slash := strings.IndexByte(importPath, '/'); slash >= 0 {
		firstElement = importPath[:slash]
	}
	if !strings.Contains(firstElement, ".") {
		return true
	}
	return importPath == adapterModulePath ||
		strings.HasPrefix(importPath, adapterModulePath+"/") ||
		importPath == "github.com/JakeFAU/stacks/core" ||
		strings.HasPrefix(importPath, "github.com/JakeFAU/stacks/core/") ||
		importPath == "github.com/jackc/pgx/v5" ||
		strings.HasPrefix(importPath, "github.com/jackc/pgx/v5/")
}
