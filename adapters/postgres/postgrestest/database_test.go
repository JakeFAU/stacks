package postgrestest

import (
	"context"
	"os"
	"testing"
)

func TestValidateEnvironmentRejectsUnsafeConnectionConfigurationsBeforeDatabaseCreation(t *testing.T) {
	tests := []struct {
		name     string
		appURL   string
		adminURL string
	}{
		{
			name:     "remote application host",
			appURL:   "postgres://app:synthetic@database.example/stacks",
			adminURL: "postgres://admin:synthetic@127.0.0.1/stacks",
		},
		{
			name:     "query host override",
			appURL:   "postgres://app:synthetic@127.0.0.1/stacks?host=database.example",
			adminURL: "postgres://admin:synthetic@127.0.0.1/stacks",
		},
		{
			name:     "service redirector",
			appURL:   "postgres://app:synthetic@127.0.0.1/stacks?service=remote",
			adminURL: "postgres://admin:synthetic@127.0.0.1/stacks",
		},
		{
			name:     "servicefile redirector",
			appURL:   "postgres://app:synthetic@127.0.0.1/stacks?servicefile=/synthetic/config",
			adminURL: "postgres://admin:synthetic@127.0.0.1/stacks",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(applicationDatabaseURLEnvironment, test.appURL)
			t.Setenv(migrationDatabaseURLEnvironment, test.adminURL)
			if err := ValidateEnvironment(context.Background()); err == nil {
				t.Fatal("ValidateEnvironment() error = nil, want unsafe connection rejection")
			}
		})
	}
}

func TestValidateEnvironmentRejectsSamePrincipal(t *testing.T) {
	appURL := os.Getenv(applicationDatabaseURLEnvironment)
	if appURL == "" {
		t.Skipf("%s is not set", applicationDatabaseURLEnvironment)
	}
	t.Setenv(migrationDatabaseURLEnvironment, appURL)
	if err := ValidateEnvironment(context.Background()); err == nil {
		t.Fatal("ValidateEnvironment() error = nil, want same-principal rejection")
	}
}

func TestValidateEnvironmentRejectsAdminAsApplicationPrincipal(t *testing.T) {
	appURL := os.Getenv(applicationDatabaseURLEnvironment)
	adminURL := os.Getenv(migrationDatabaseURLEnvironment)
	if appURL == "" || adminURL == "" {
		t.Skip("both PostgreSQL test URLs are required")
	}
	t.Setenv(applicationDatabaseURLEnvironment, adminURL)
	t.Setenv(migrationDatabaseURLEnvironment, appURL)
	if err := ValidateEnvironment(context.Background()); err == nil {
		t.Fatal("ValidateEnvironment() error = nil, want privileged application-principal rejection")
	}
}

func TestValidateEnvironmentRejectsInsufficientAdministratorPrivilege(t *testing.T) {
	appURL := os.Getenv(applicationDatabaseURLEnvironment)
	if appURL == "" {
		t.Skipf("%s is not set", applicationDatabaseURLEnvironment)
	}
	configuredAdminURL := os.Getenv(migrationDatabaseURLEnvironment)
	if configuredAdminURL == "" {
		t.Skipf("%s is not set", migrationDatabaseURLEnvironment)
	}
	t.Setenv(migrationDatabaseURLEnvironment, appURL)
	t.Setenv(applicationDatabaseURLEnvironment, configuredAdminURL)
	if err := ValidateEnvironment(context.Background()); err == nil {
		t.Fatal("ValidateEnvironment() error = nil, want insufficient administrator rejection")
	}
}

func TestValidateRoleAttributesRejectsUnsafePrincipalAssignments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		admin roleAttributes
		app   roleAttributes
	}{
		{
			name:  "same principal",
			admin: roleAttributes{name: "same", createDatabase: true},
			app:   roleAttributes{name: "same"},
		},
		{
			name:  "admin as application",
			admin: roleAttributes{name: "migration", createDatabase: true},
			app:   roleAttributes{name: "application", superuser: true},
		},
		{
			name:  "application can create databases",
			admin: roleAttributes{name: "migration", createDatabase: true},
			app:   roleAttributes{name: "application", createDatabase: true},
		},
		{
			name:  "application can create roles",
			admin: roleAttributes{name: "migration", createDatabase: true},
			app:   roleAttributes{name: "application", createRole: true},
		},
		{
			name:  "insufficient administrator",
			admin: roleAttributes{name: "migration"},
			app:   roleAttributes{name: "application"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateRoleAttributes(test.admin, test.app); err == nil {
				t.Fatal("validateRoleAttributes() error = nil, want unsafe role rejection")
			}
		})
	}
}
