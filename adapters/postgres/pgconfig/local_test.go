package pgconfig

import "testing"

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
