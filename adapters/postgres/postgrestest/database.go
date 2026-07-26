// Package postgrestest creates isolated temporary PostgreSQL databases for
// live adapter tests. It never mutates the configured source database.
package postgrestest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/adapters/postgres/pgconfig"
	"github.com/jackc/pgx/v5"
)

const (
	applicationDatabaseURLEnvironment = "STACKS_TEST_DATABASE_URL"
	migrationDatabaseURLEnvironment   = "STACKS_TEST_MIGRATION_DATABASE_URL"
	databaseNamePrefix                = "stacks_test_"
	randomNameBytes                   = 12
	cleanupTimeout                    = 10 * time.Second
)

// Database is an exact temporary database and its role-specific connection
// URLs. The unexported fields prevent callers from choosing cleanup targets.
type Database struct {
	name           string
	adminURL       string
	applicationURL string
	baseAdminURL   string
}

// NewDatabase creates an isolated database using the configured migration
// administrator. Tests are skipped unless both role URLs are configured.
func NewDatabase(t testing.TB) Database {
	t.Helper()

	baseAdminURL := os.Getenv(migrationDatabaseURLEnvironment)
	if baseAdminURL == "" {
		t.Skipf("%s is not set", migrationDatabaseURLEnvironment)
	}
	baseApplicationURL := os.Getenv(applicationDatabaseURLEnvironment)
	if baseApplicationURL == "" {
		t.Skipf("%s is not set", applicationDatabaseURLEnvironment)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	adminConfig, applicationConfig, err := validateEnvironment(ctx)
	if err != nil {
		t.Fatalf("validate PostgreSQL test environment: %v", err)
	}

	name := randomDatabaseName(t)
	quotedName := pgx.Identifier{name}.Sanitize()
	connection, err := pgx.ConnectConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect to migration test server: %v", err)
	}
	if _, err := connection.Exec(ctx, "CREATE DATABASE "+quotedName+" TEMPLATE template0"); err != nil {
		_ = connection.Close(context.Background())
		t.Fatalf("create isolated test database: %v", err)
	}
	database := Database{name: name, baseAdminURL: baseAdminURL}
	t.Cleanup(func() {
		database.drop(t)
	})
	if err := connection.Close(ctx); err != nil {
		t.Fatalf("close migration test server connection: %v", err)
	}

	adminConfig.Database = name
	applicationConfig.Database = name
	connection, err = pgx.ConnectConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect to isolated test database: %v", err)
	}
	applicationRole := pgx.Identifier{applicationConfig.User}.Sanitize()
	if _, err := connection.Exec(
		ctx,
		"REVOKE ALL ON DATABASE "+quotedName+" FROM PUBLIC; GRANT CONNECT ON DATABASE "+
			quotedName+" TO "+applicationRole,
		pgx.QueryExecModeSimpleProtocol,
	); err != nil {
		_ = connection.Close(context.Background())
		t.Fatalf("limit isolated test database privileges: %v", err)
	}
	if err := connection.Close(ctx); err != nil {
		t.Fatalf("close isolated test database connection: %v", err)
	}

	database.adminURL = databaseURL(t, baseAdminURL, name)
	database.applicationURL = databaseURL(t, baseApplicationURL, name)
	return database
}

// ValidateEnvironment performs the bounded read-only endpoint and role checks
// required before any integration package may create a temporary database.
func ValidateEnvironment(ctx context.Context) error {
	_, _, err := validateEnvironment(ctx)
	return err
}

func validateEnvironment(
	ctx context.Context,
) (*pgx.ConnConfig, *pgx.ConnConfig, error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("context is required")
	}
	baseAdminURL := os.Getenv(migrationDatabaseURLEnvironment)
	baseApplicationURL := os.Getenv(applicationDatabaseURLEnvironment)
	adminConfig, err := pgconfig.ParseLocal(baseAdminURL)
	if err != nil {
		return nil, nil, fmt.Errorf("migration connection configuration is unsafe")
	}
	applicationConfig, err := pgconfig.ParseLocal(baseApplicationURL)
	if err != nil {
		return nil, nil, fmt.Errorf("application connection configuration is unsafe")
	}
	if adminConfig.Host != applicationConfig.Host ||
		adminConfig.Port != applicationConfig.Port {
		return nil, nil, fmt.Errorf("test connections must address one local PostgreSQL server")
	}
	if _, err := rewrittenDatabaseURL(baseAdminURL, "synthetic_validation"); err != nil {
		return nil, nil, fmt.Errorf("migration connection must be an explicit PostgreSQL URL")
	}
	if _, err := rewrittenDatabaseURL(baseApplicationURL, "synthetic_validation"); err != nil {
		return nil, nil, fmt.Errorf("application connection must be an explicit PostgreSQL URL")
	}
	if adminConfig.User == applicationConfig.User {
		return nil, nil, fmt.Errorf("test application and migration principals must be distinct")
	}
	adminRole, err := inspectRole(ctx, adminConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect migration test principal")
	}
	applicationRole, err := inspectRole(ctx, applicationConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect application test principal")
	}
	if err := validateRoleAttributes(adminRole, applicationRole); err != nil {
		return nil, nil, err
	}
	return adminConfig, applicationConfig, nil
}

func validateRoleAttributes(adminRole, applicationRole roleAttributes) error {
	if adminRole.name == applicationRole.name {
		return fmt.Errorf("test application and migration principals must be distinct")
	}
	if applicationRole.superuser ||
		applicationRole.createDatabase ||
		applicationRole.createRole {
		return fmt.Errorf("application test principal has administrative privilege")
	}
	if !adminRole.superuser && !adminRole.createDatabase {
		return fmt.Errorf("migration test principal cannot create temporary databases")
	}
	return nil
}

type roleAttributes struct {
	name           string
	superuser      bool
	createDatabase bool
	createRole     bool
}

func inspectRole(
	ctx context.Context,
	config *pgx.ConnConfig,
) (roleAttributes, error) {
	connection, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return roleAttributes{}, err
	}
	defer func() {
		_ = connection.Close(context.Background())
	}()
	var attributes roleAttributes
	err = connection.QueryRow(ctx, `
		SELECT rolname, rolsuper, rolcreatedb, rolcreaterole
		  FROM pg_catalog.pg_roles
		 WHERE rolname = current_user
	`).Scan(
		&attributes.name,
		&attributes.superuser,
		&attributes.createDatabase,
		&attributes.createRole,
	)
	return attributes, err
}

// Name returns the internally generated temporary database name.
func (database Database) Name() string {
	return database.name
}

// AdminURL returns a schema-capable connection URL for the temporary database.
func (database Database) AdminURL() string {
	return database.adminURL
}

// ApplicationURL returns the application-role connection URL for the
// temporary database.
func (database Database) ApplicationURL() string {
	return database.applicationURL
}

func (database Database) drop(t testing.TB) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	connection, err := pgx.Connect(ctx, database.baseAdminURL)
	if err != nil {
		t.Errorf("connect for isolated test database cleanup: %v", err)
		return
	}
	defer func() {
		_ = connection.Close(context.Background())
	}()

	if _, err := connection.Exec(
		ctx,
		`SELECT pg_terminate_backend(pid)
		 FROM pg_stat_activity
		 WHERE datname = $1
		   AND pid <> pg_backend_pid()`,
		database.name,
	); err != nil {
		t.Errorf("terminate exact isolated test database connections: %v", err)
		return
	}
	quotedName := pgx.Identifier{database.name}.Sanitize()
	if _, err := connection.Exec(ctx, "DROP DATABASE "+quotedName); err != nil {
		t.Errorf("drop exact isolated test database: %v", err)
	}
}

func randomDatabaseName(t testing.TB) string {
	t.Helper()

	random := make([]byte, randomNameBytes)
	if _, err := rand.Read(random); err != nil {
		t.Fatalf("generate isolated test database name: %v", err)
	}
	return fmt.Sprintf("%s%s", databaseNamePrefix, hex.EncodeToString(random))
}

func databaseURL(t testing.TB, baseURL, databaseName string) string {
	t.Helper()

	result, err := rewrittenDatabaseURL(baseURL, databaseName)
	if err != nil {
		t.Fatal("PostgreSQL test database URL must be an explicit postgres URL")
	}
	return result
}

func rewrittenDatabaseURL(baseURL, databaseName string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if parsed.Host == "" ||
		(parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		strings.TrimSpace(databaseName) == "" {
		return "", fmt.Errorf("explicit PostgreSQL URL is required")
	}
	parsed.Path = "/" + databaseName
	parsed.RawPath = ""
	query := parsed.Query()
	query.Del("database")
	query.Del("dbname")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
