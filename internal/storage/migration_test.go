package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagerConfidenceMigrationUsesPortableFiniteFloatChecks(t *testing.T) {
	migration := readManagerConfidenceMigration(t)
	if strings.Contains(migration, "isfinite(") {
		t.Fatal("migration uses unsupported PostgreSQL isfinite(double precision)")
	}
	if got := strings.Count(migration, "< 'Infinity'::double precision"); got != 3 {
		t.Fatalf("finite upper-bound checks = %d, want 3", got)
	}
	if got := strings.Count(migration, "> '-Infinity'::double precision"); got != 3 {
		t.Fatalf("finite lower-bound checks = %d, want 3", got)
	}
}

func TestManagerConfidenceMigrationProtectsPLpgSQLAndUpdatedSignalEvidence(t *testing.T) {
	migration := readManagerConfidenceMigration(t)
	functionStart := strings.Index(migration, "CREATE FUNCTION stacks.require_transcript_signal_evidence()")
	if functionStart < 0 {
		t.Fatal("transcript-evidence function is missing")
	}
	statementBegin := strings.LastIndex(migration[:functionStart], "-- +goose StatementBegin")
	statementEnd := strings.Index(migration[functionStart:], "-- +goose StatementEnd")
	if statementBegin < 0 || statementEnd < 0 {
		t.Fatal("PL/pgSQL function is not protected by Goose statement directives")
	}
	if !strings.Contains(migration, "validate_transcript_signal_evidence(OLD.signal_id)") ||
		!strings.Contains(migration, "validate_transcript_signal_evidence(NEW.signal_id)") {
		t.Fatal("signal evidence updates do not validate both old and new signals")
	}
}

func TestIngestionMigrationAddsOnlyBoundedForwardProcessingState(t *testing.T) {
	path := filepath.Join("..", "..", "db", "migrations", "00003_ingestion_processing_state.sql")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %q: %v", path, err)
	}
	migration := string(contents)
	for _, required := range []string{"failure_code text", "retry_count integer NOT NULL DEFAULT 0", "failure_code IN (", "retry_count >= 0"} {
		if !strings.Contains(migration, required) {
			t.Fatalf("ingestion migration is missing %q", required)
		}
	}
	if strings.Contains(migration, "-- +goose Down") {
		t.Fatal("ingestion migration is not forward-only")
	}
}

func readManagerConfidenceMigration(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "db", "migrations", "00002_manager_confidence_poc.sql")
	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %q: %v", path, err)
	}
	return string(migration)
}
