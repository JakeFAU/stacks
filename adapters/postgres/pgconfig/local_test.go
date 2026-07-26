package pgconfig

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestPostgresConnectionEnvironmentCoversPgxConnectionSurface(t *testing.T) {
	expected := []string{
		"PGHOST",
		"PGHOSTADDR",
		"PGPORT",
		"PGDATABASE",
		"PGUSER",
		"PGPASSWORD",
		"PGPASSFILE",
		"PGAPPNAME",
		"PGCONNECT_TIMEOUT",
		"PGSSLMODE",
		"PGSSLKEY",
		"PGSSLCERT",
		"PGSSLSNI",
		"PGSSLROOTCERT",
		"PGSSLPASSWORD",
		"PGSSLNEGOTIATION",
		"PGTARGETSESSIONATTRS",
		"PGSERVICE",
		"PGSERVICEFILE",
		"PGTZ",
		"PGOPTIONS",
		"PGMINPROTOCOLVERSION",
		"PGMAXPROTOCOLVERSION",
		"PGCHANNELBINDING",
		"PGREQUIREAUTH",
	}
	actual := append([]string(nil), postgresConnectionEnvironment...)
	slices.Sort(expected)
	slices.Sort(actual)
	if !slices.Equal(actual, expected) {
		t.Fatalf(
			"PostgreSQL connection environment = %#v, want full pgx surface %#v",
			actual,
			expected,
		)
	}
}

func TestParseLocalRejectsRedirectionAndNonLoopbackEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		connectionString string
	}{
		{name: "remote authority", connectionString: "postgres://app:synthetic@database.example/stacks"},
		{name: "query host override", connectionString: "postgres://app:synthetic@127.0.0.1/stacks?host=database.example"},
		{name: "remote fallback", connectionString: "host=127.0.0.1,database.example user=app password=synthetic dbname=stacks"},
		{name: "service", connectionString: "postgres://app:synthetic@127.0.0.1/stacks?service=remote"},
		{name: "servicefile", connectionString: "postgres://app:synthetic@127.0.0.1/stacks?servicefile=/synthetic/config"},
		{name: "missing host", connectionString: "postgres:///stacks?user=app"},
		{name: "non PostgreSQL URL", connectionString: "https://127.0.0.1/stacks"},
		{name: "ambiguous empty configuration", connectionString: ""},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseLocal(test.connectionString); err == nil {
				t.Fatal("ParseLocal() error = nil, want local PostgreSQL rejection")
			}
		})
	}
}

func TestParseLocalAcceptsEveryLoopbackEndpoint(t *testing.T) {
	t.Parallel()

	config, err := ParseLocal(
		"host=127.0.0.1,localhost,::1 port=5432,5433,5434 user=app password=synthetic dbname=stacks sslmode=disable",
	)
	if err != nil {
		t.Fatalf("ParseLocal() error = %v", err)
	}
	if config.Host != "127.0.0.1" {
		t.Fatalf("primary host = %q, want loopback primary", config.Host)
	}
	if len(config.Fallbacks) < 2 {
		t.Fatalf("fallback count = %d, want every parsed loopback endpoint", len(config.Fallbacks))
	}
}

func TestParseLocalRejectsAmbientPostgresEnvironmentBeforeParsing(t *testing.T) {
	for _, variable := range postgresConnectionEnvironment {
		variable := variable
		t.Run(variable, func(t *testing.T) {
			clearPostgresEnvironment(t)
			t.Setenv(variable, "synthetic-ambient-value")
			if _, err := ParseLocal("postgres://app:synthetic@127.0.0.1/stacks"); err == nil {
				t.Fatal("ParseLocal() error = nil, want ambient PostgreSQL setting rejection")
			} else if strings.Contains(err.Error(), "synthetic-ambient-value") {
				t.Fatalf("ParseLocal() error exposed ambient value: %q", err)
			}
		})
	}
}

func TestParseLocalRejectsHostlessConfigurationCompletedByAmbientHost(t *testing.T) {
	clearPostgresEnvironment(t)
	t.Setenv("PGHOST", "127.0.0.1")
	if _, err := ParseLocal("user=app password=synthetic dbname=stacks"); err == nil {
		t.Fatal("ParseLocal() error = nil, want ambient host rejection")
	}
}

func TestParseLocalRejectsAmbientServiceWithoutReadingServiceFile(t *testing.T) {
	for _, variable := range []string{"PGSERVICE", "PGSERVICEFILE"} {
		variable := variable
		t.Run(variable, func(t *testing.T) {
			clearPostgresEnvironment(t)
			t.Setenv(variable, "synthetic-do-not-read")
			if _, err := ParseLocal("postgres://app:synthetic@127.0.0.1/stacks"); err == nil {
				t.Fatal("ParseLocal() error = nil, want ambient service rejection")
			}
		})
	}
}

func clearPostgresEnvironment(t *testing.T) {
	t.Helper()
	for _, variable := range postgresConnectionEnvironment {
		value, present := os.LookupEnv(variable)
		if err := os.Unsetenv(variable); err != nil {
			t.Fatalf("unset %s: %v", variable, err)
		}
		t.Cleanup(func() {
			if present {
				if err := os.Setenv(variable, value); err != nil {
					t.Errorf("restore %s: %v", variable, err)
				}
				return
			}
			if err := os.Unsetenv(variable); err != nil {
				t.Errorf("restore %s: %v", variable, err)
			}
		})
	}
}
