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

func TestDatabaseSupportsOverlappingTransactions(t *testing.T) {
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
			"CREATE TABLE pool_records (id bigint PRIMARY KEY)",
		)
		return err
	}); err != nil {
		t.Fatalf("create overlapping transaction fixture: %v", err)
	}

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- database.InTransaction(ctx, func(transaction *postgres.Transaction) error {
			close(firstStarted)
			<-releaseFirst
			_, err := transaction.Exec(ctx, "INSERT INTO pool_records (id) VALUES (1)")
			return err
		})
	}()
	<-firstStarted

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- database.InTransaction(ctx, func(transaction *postgres.Transaction) error {
			close(secondStarted)
			_, err := transaction.Exec(ctx, "INSERT INTO pool_records (id) VALUES (2)")
			return err
		})
	}()

	select {
	case <-secondStarted:
	case err := <-secondDone:
		close(releaseFirst)
		<-firstDone
		t.Fatalf("second transaction completed before overlapping callback: %v", err)
	case <-ctx.Done():
		close(releaseFirst)
		<-firstDone
		t.Fatalf("wait for overlapping transaction: %v", ctx.Err())
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first overlapping transaction: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second overlapping transaction: %v", err)
	}

	connection, err := pgx.Connect(ctx, isolated.AdminURL())
	if err != nil {
		t.Fatalf("connect to inspect overlapping transactions: %v", err)
	}
	defer connection.Close(context.Background())
	var rows int
	if err := connection.QueryRow(ctx, "SELECT count(*) FROM pool_records").Scan(&rows); err != nil {
		t.Fatalf("count overlapping transaction rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("overlapping transaction rows = %d, want 2", rows)
	}
}

func TestDatabaseCancellationDoesNotPoisonFutureUse(t *testing.T) {
	isolated := postgrestest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, isolated.AdminURL())
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	defer database.Close()

	transactionContext, cancelTransaction := context.WithCancel(ctx)
	queryStarted := make(chan struct{})
	transactionDone := make(chan error, 1)
	go func() {
		transactionDone <- database.InTransaction(
			transactionContext,
			func(transaction *postgres.Transaction) error {
				close(queryStarted)
				_, err := transaction.Exec(transactionContext, "SELECT pg_sleep(30)")
				return err
			},
		)
	}()
	<-queryStarted
	cancelTransaction()
	if err := <-transactionDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled transaction error = %v, want context.Canceled", err)
	}

	if err := database.InTransaction(ctx, func(transaction *postgres.Transaction) error {
		_, err := transaction.Exec(ctx, "SELECT 1")
		return err
	}); err != nil {
		t.Fatalf("transaction after cancellation: %v", err)
	}
}

func TestDependencyPostgresAdapterProductionImportsStayWithinBoundary(t *testing.T) {
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
