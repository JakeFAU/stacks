package main

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
)

func TestFingerprintCommandPrintsOnlyScopeAndHash(t *testing.T) {
	t.Parallel()

	const (
		databaseURL = "postgres://admin:synthetic-secret@127.0.0.1/stacks"
		role        = "synthetic-role"
	)
	fingerprint := sha256.Sum256([]byte("catalog"))
	var output strings.Builder
	err := run(
		t.Context(),
		[]string{"core"},
		databaseURL,
		&output,
		func(
			_ context.Context,
			gotURL string,
			manifest migration.Manifest,
		) ([sha256.Size]byte, error) {
			if gotURL != databaseURL {
				t.Fatalf("database URL = %q, want injected value", gotURL)
			}
			if manifest.Scope != "core" {
				t.Fatalf("manifest scope = %q, want core", manifest.Scope)
			}
			return fingerprint, nil
		},
	)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	want := "scope=core sha256=" +
		"652f55016243bf1b9f1bbea46d5749ef892dbe394e46de9d66ab1aacf0b4af57\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if strings.Contains(output.String(), databaseURL) ||
		strings.Contains(output.String(), "synthetic-secret") ||
		strings.Contains(output.String(), role) {
		t.Fatalf("output exposed private configuration: %q", output.String())
	}
}
