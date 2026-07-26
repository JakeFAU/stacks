package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoadDatabaseScopesAndRole(t *testing.T) {
	t.Setenv(DatabaseURLEnvironmentVariable, "postgres://app:synthetic@127.0.0.1/stacks")
	t.Setenv(MigrationDatabaseURLEnvironmentVariable, "postgres://admin:synthetic@127.0.0.1/stacks")
	t.Setenv(DatabaseScopesEnvironmentVariable, "core,directory")
	t.Setenv(DatabaseAppRoleEnvironmentVariable, "stacks_app")

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	wantScopes := []DatabaseScope{DatabaseScopeCore, DatabaseScopeDirectory}
	if !reflect.DeepEqual(settings.Database.Scopes, wantScopes) {
		t.Fatalf("database scopes = %#v, want %#v", settings.Database.Scopes, wantScopes)
	}
	if settings.Database.MigrationURL == "" || settings.Database.ApplicationRole != "stacks_app" {
		t.Fatalf("database settings = %#v, want migration URL and typed app role", settings.Database)
	}
}

func TestDatabaseScopeValidationRejectsUnknownDuplicateAndMissingCore(t *testing.T) {
	tests := []struct {
		name   string
		scopes string
	}{
		{name: "unknown", scopes: "core,other"},
		{name: "duplicate core", scopes: "core,core"},
		{name: "duplicate directory", scopes: "core,directory,directory"},
		{name: "missing core", scopes: "directory"},
		{name: "empty", scopes: ","},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(DatabaseScopesEnvironmentVariable, test.scopes)
			if _, err := Load(); err == nil ||
				!strings.Contains(err.Error(), DatabaseScopesEnvironmentVariable) {
				t.Fatalf("Load() error = %v, want database scope rejection", err)
			}
		})
	}
}

func TestDirectoryProviderRequiresDirectoryDatabaseScope(t *testing.T) {
	settings := Settings{
		Database: DatabaseSettings{Scopes: []DatabaseScope{DatabaseScopeCore}},
		PoC:      PoCSettings{Directory: GoogleDirectorySettings{Enabled: true}},
	}
	if err := settings.Validate(CommandSync); err == nil ||
		!strings.Contains(err.Error(), DatabaseScopesEnvironmentVariable) {
		t.Fatalf("Settings.Validate() error = %v, want missing directory scope", err)
	}
}

func TestDisabledDirectoryAcceptsCoreOnlyDatabaseScope(t *testing.T) {
	settings := Settings{
		Database: DatabaseSettings{Scopes: []DatabaseScope{DatabaseScopeCore}},
		PoC:      PoCSettings{Directory: GoogleDirectorySettings{Enabled: false}},
	}
	if err := settings.Validate(CommandServe); err != nil {
		t.Fatalf("Settings.Validate() error = %v, want core-only disabled-directory configuration", err)
	}
}

func TestDBMigrateRejectsUnsafeApplicationRoleBeforeConstruction(t *testing.T) {
	t.Parallel()

	settings := Settings{Database: DatabaseSettings{
		MigrationURL:    "postgres://synthetic-admin",
		ApplicationRole: `stacks_app"; DROP ROLE stacks_app; --`,
		Scopes:          []DatabaseScope{DatabaseScopeCore},
	}}
	if err := settings.Validate(CommandDBMigrate); err == nil ||
		!strings.Contains(err.Error(), DatabaseAppRoleEnvironmentVariable) {
		t.Fatalf("Settings.Validate() error = %v, want unsafe application role rejection", err)
	}
}

func TestDBResetRequiresApplicationAndMigrationURLs(t *testing.T) {
	t.Parallel()

	settings := Settings{Database: DatabaseSettings{
		MigrationURL:    "postgres://synthetic-admin",
		ApplicationRole: "stacks_app",
		Scopes:          []DatabaseScope{DatabaseScopeCore},
	}}
	if err := settings.Validate(CommandDBReset); err == nil ||
		!strings.Contains(err.Error(), DatabaseURLEnvironmentVariable) {
		t.Fatalf("Settings.Validate() error = %v, want missing app URL rejection", err)
	}
}
