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

func TestLoadDatabaseScopesRejectsBlankEnvironmentMembers(t *testing.T) {
	tests := []struct {
		name   string
		scopes string
	}{
		{name: "blank middle member", scopes: "core,,directory"},
		{name: "whitespace-only middle member", scopes: "core,  ,directory"},
		{name: "blank leading member", scopes: ",core"},
		{name: "blank trailing member", scopes: "core,"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			clearConfigurationEnvironment(t)
			t.Setenv(DatabaseScopesEnvironmentVariable, testCase.scopes)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() error = nil, want blank database scope rejection")
			}
			if !strings.Contains(err.Error(), DatabaseScopesEnvironmentVariable) {
				t.Fatalf("Load() error = %v, want bounded database scope setting name", err)
			}
			if strings.Contains(err.Error(), testCase.scopes) {
				t.Fatalf("Load() error exposed loaded database scopes: %v", err)
			}
		})
	}
}

func TestDatabaseScopeValidationDoesNotExposeLoadedScope(t *testing.T) {
	const secretLikeScope = "synthetic-secret-scope-marker"

	err := validateDatabaseScopes([]DatabaseScope{DatabaseScopeCore, DatabaseScope(secretLikeScope)})
	if err == nil {
		t.Fatal("validateDatabaseScopes() error = nil, want unknown scope rejection")
	}
	if !strings.Contains(err.Error(), DatabaseScopesEnvironmentVariable) {
		t.Fatalf("validateDatabaseScopes() error = %v, want bounded database scope setting name", err)
	}
	if strings.Contains(err.Error(), secretLikeScope) {
		t.Fatalf("validateDatabaseScopes() error exposed loaded scope: %v", err)
	}
}

func TestDirectoryProviderRequiresDirectoryDatabaseScope(t *testing.T) {
	settings := Settings{
		Database:    DatabaseSettings{Scopes: []DatabaseScope{DatabaseScopeCore}},
		Application: ApplicationSettings{Directory: GoogleDirectorySettings{Enabled: true}},
	}
	if err := settings.Validate(CommandSync); err == nil ||
		!strings.Contains(err.Error(), DatabaseScopesEnvironmentVariable) {
		t.Fatalf("Settings.Validate() error = %v, want missing directory scope", err)
	}
}

func TestDisabledDirectoryAcceptsCoreOnlyDatabaseScope(t *testing.T) {
	settings := Settings{
		Database:    DatabaseSettings{Scopes: []DatabaseScope{DatabaseScopeCore}},
		Application: ApplicationSettings{Directory: GoogleDirectorySettings{Enabled: false}},
	}
	if err := settings.Validate(CommandServe); err != nil {
		t.Fatalf("Settings.Validate() error = %v, want core-only disabled-directory configuration", err)
	}
}

func TestApplicationDatabaseCommandsRequireCanonicalDatabaseURL(t *testing.T) {
	t.Parallel()

	for _, command := range []Command{
		CommandDoctor,
		CommandSync,
		CommandEntities,
		CommandReview,
		CommandAnalyze,
	} {
		command := command
		t.Run(string(command), func(t *testing.T) {
			t.Parallel()
			settings := Settings{Database: DatabaseSettings{
				Scopes: []DatabaseScope{DatabaseScopeCore},
			}}
			if err := settings.Validate(command); err == nil ||
				!strings.Contains(err.Error(), DatabaseURLEnvironmentVariable) {
				t.Fatalf("Settings.Validate(%q) error = %v, want canonical database URL rejection", command, err)
			}
		})
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
