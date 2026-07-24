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

func TestAnalysisMigrationAddsAuditedMentionLinksAndBoundedRunMetadata(t *testing.T) {
	path := filepath.Join("..", "..", "db", "migrations", "00004_temporal_pair_analysis.sql")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %q: %v", path, err)
	}
	migration := string(contents)
	for _, required := range []string{
		"subject_mention_id uuid REFERENCES stacks.mentions(id)",
		"object_mention_id uuid REFERENCES stacks.mentions(id)",
		"bedrock_region text",
		"model_id text",
		"max_output_tokens integer",
		"report_json jsonb",
		"'source_document'",
	} {
		if !strings.Contains(migration, required) {
			t.Fatalf("analysis migration is missing %q", required)
		}
	}
	if strings.Contains(migration, "-- +goose Down") {
		t.Fatal("analysis migration is not forward-only")
	}
}

func TestFinalIntegrationMigrationAddsImmutableSourceProvenance(t *testing.T) {
	migration := readFinalIntegrationMigration(t)
	for _, required := range []string{
		"title text NOT NULL",
		"locator text NOT NULL",
		"provider_version text NOT NULL",
		"provider_revision text NOT NULL",
		"provider_modified_at timestamptz",
		"source_meeting_time timestamptz",
	} {
		if !strings.Contains(migration, required) {
			t.Fatalf("final integration migration is missing source provenance %q", required)
		}
	}
}

func TestFinalIntegrationMigrationLinksAliasesToDecisionLifecycle(t *testing.T) {
	migration := readFinalIntegrationMigration(t)
	for _, required := range []string{
		"normalized_name text NOT NULL",
		"normalized_email text NOT NULL",
		"CREATE TABLE stacks.entity_alias_assertions",
		"decision_id uuid NOT NULL REFERENCES stacks.resolution_decisions(id)",
		"UNIQUE (decision_id, normalized_value, alias_type)",
	} {
		if !strings.Contains(migration, required) {
			t.Fatalf("final integration migration is missing alias lifecycle %q", required)
		}
	}
}

func TestFinalIntegrationMigrationSeparatesSourceVersionsFromExtractionRuns(t *testing.T) {
	migration := readFinalIntegrationMigration(t)
	for _, required := range []string{
		"CREATE TABLE stacks.extraction_runs",
		"document_version_id uuid NOT NULL REFERENCES stacks.document_versions(id)",
		"derivation_digest bytea NOT NULL",
		"model_id text NOT NULL",
		"bedrock_region text NOT NULL",
		"max_output_tokens integer NOT NULL",
		"prompt_version text NOT NULL",
		"schema_digest bytea NOT NULL",
		"UNIQUE (document_version_id, derivation_digest)",
	} {
		if !strings.Contains(migration, required) {
			t.Fatalf("final integration migration is missing extraction identity %q", required)
		}
	}
}

func TestFinalIntegrationMigrationAddsOwnedExtractionLeases(t *testing.T) {
	migration := readFinalIntegrationMigration(t)
	for _, required := range []string{
		"lease_owner uuid",
		"lease_expires_at timestamptz",
		"completed_by_owner uuid",
		"extraction_runs_state_ownership",
		"processing_status = 'pending' AND lease_owner IS NOT NULL",
		"processing_status = 'complete' AND lease_owner IS NULL",
		"processing_status IN ('incomplete', 'failed') AND lease_owner IS NULL",
	} {
		if !strings.Contains(migration, required) {
			t.Fatalf("final integration migration is missing lease ownership %q", required)
		}
	}
}

func TestFinalIntegrationMigrationPreservesLegacyModelNarrativeForAudit(t *testing.T) {
	migration := readFinalIntegrationMigration(t)
	for _, forbidden := range []string{
		"UPDATE stacks.interaction_signals SET rationale = ''",
		"SET hypothesis = '', report_json = NULL",
	} {
		if strings.Contains(migration, forbidden) {
			t.Fatalf("final integration migration destructively rewrites immutable audit payload %q", forbidden)
		}
	}
}

func TestLegacyAdmissionMigrationMarksPreFixDerivedRowsNonAdmissible(t *testing.T) {
	migration := readLegacyAdmissionMigration(t)
	for _, required := range []string{
		"ALTER TABLE stacks.extraction_runs",
		"ALTER TABLE stacks.mentions",
		"ALTER TABLE stacks.resolution_decisions",
		"ALTER TABLE stacks.observations",
		"ALTER TABLE stacks.interaction_signals",
		"ALTER TABLE stacks.analysis_runs",
		"ADD COLUMN currently_admissible boolean NOT NULL DEFAULT false",
		"ALTER COLUMN currently_admissible SET DEFAULT true",
	} {
		if !strings.Contains(migration, required) {
			t.Fatalf("legacy admission migration is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"SET rationale =",
		"SET hypothesis =",
		"SET report_json =",
		"DELETE FROM",
	} {
		if strings.Contains(migration, forbidden) {
			t.Fatalf("legacy admission migration destructively rewrites audit history with %q", forbidden)
		}
	}
}

func TestCompatibilityAdmissionMigrationInvalidatesSupersededSemanticsWithoutRewritingAudit(t *testing.T) {
	migration := readCompatibilityAdmissionMigration(t)
	for _, required := range []string{
		"ADD COLUMN content_digest_v2 bytea",
		"ADD COLUMN proposed_email text NOT NULL DEFAULT ''",
		"ADD COLUMN proposed_email_evidence_span_id uuid REFERENCES stacks.evidence_spans(id)",
		"UPDATE stacks.extraction_runs",
		"UPDATE stacks.mentions",
		"UPDATE stacks.resolution_decisions",
		"UPDATE stacks.observations",
		"UPDATE stacks.interaction_signals",
		"UPDATE stacks.analysis_runs",
		"SET currently_admissible = false",
	} {
		if !strings.Contains(migration, required) {
			t.Fatalf("compatibility admission migration is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"SET rationale =",
		"SET hypothesis =",
		"SET report_json =",
		"SET normalized_email =",
		"DELETE FROM",
		"-- +goose Down",
	} {
		if strings.Contains(migration, forbidden) {
			t.Fatalf("compatibility admission migration rewrites immutable audit history with %q", forbidden)
		}
	}
}

func TestSnapshotCoherenceAdmissionMigrationRetiresPriorCurrentStateWithoutRewritingAudit(t *testing.T) {
	migration := readSnapshotCoherenceAdmissionMigration(t)
	for _, required := range []string{
		"UPDATE stacks.extraction_runs",
		"UPDATE stacks.mentions",
		"UPDATE stacks.resolution_decisions",
		"UPDATE stacks.observations",
		"UPDATE stacks.interaction_signals",
		"UPDATE stacks.analysis_runs",
		"SET currently_admissible = false",
	} {
		if !strings.Contains(migration, required) {
			t.Fatalf("snapshot-coherence admission migration is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"SET source_meeting_time =",
		"SET title =",
		"SET rationale =",
		"SET hypothesis =",
		"SET report_json =",
		"DELETE FROM",
		"-- +goose Down",
	} {
		if strings.Contains(migration, forbidden) {
			t.Fatalf("snapshot-coherence admission migration rewrites immutable audit history with %q", forbidden)
		}
	}
}

func TestDoctorInspectionMigrationGrantsOnlyMigrationStatusReadAccess(t *testing.T) {
	path := filepath.Join("..", "..", "db", "migrations", "00009_doctor_migration_inspection.sql")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %q: %v", path, err)
	}
	migration := string(contents)
	for _, required := range []string{
		"GRANT USAGE ON SCHEMA public TO stacks_app",
		"GRANT SELECT ON TABLE public.goose_db_version TO stacks_app",
	} {
		if !strings.Contains(migration, required) {
			t.Fatalf("doctor inspection migration is missing %q", required)
		}
	}
	for _, forbidden := range []string{"GRANT CREATE", "GRANT ALL", "-- +goose Down"} {
		if strings.Contains(migration, forbidden) {
			t.Fatalf("doctor inspection migration broadens privileges with %q", forbidden)
		}
	}
}

func TestModelProviderProvenanceMigrationBackfillsAndConstrainsRunMetadata(t *testing.T) {
	migration := readModelProviderProvenanceMigration(t)
	for _, required := range []string{
		"ADD COLUMN model_provider text",
		"ADD COLUMN data_mode text",
		"SET model_provider = 'bedrock'",
		"data_mode = 'legacy'",
		"ALTER COLUMN bedrock_region DROP NOT NULL",
		"ALTER COLUMN model_provider SET NOT NULL",
		"ALTER COLUMN data_mode SET NOT NULL",
		"model_provider IN ('bedrock', 'openai', 'anthropic')",
		"data_mode IN ('personal', 'restricted', 'legacy')",
		"model_provider = 'bedrock'",
		"model_provider IN ('openai', 'anthropic')",
		"bedrock_region IS NULL",
		"report_json IS NOT NULL",
	} {
		if !strings.Contains(migration, required) {
			t.Fatalf("model-provider provenance migration is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"UPDATE stacks.document_versions",
		"SET derivation_digest =",
		"SET input_digest =",
		"-- +goose Down",
	} {
		if strings.Contains(migration, forbidden) {
			t.Fatalf("model-provider provenance migration rewrites immutable history with %q", forbidden)
		}
	}
}

func TestCurrentDocumentVersionMigrationAddsOwnedCurrentPointerWithoutRewritingHistory(t *testing.T) {
	migration := readCurrentDocumentVersionMigration(t)
	for _, required := range []string{
		"ADD COLUMN current_document_version_id uuid",
		"CREATE UNIQUE INDEX document_versions_source_document_identity",
		"FOREIGN KEY (id, current_document_version_id)",
		"REFERENCES stacks.document_versions (source_document_id, id)",
		"processing_status = 'complete'",
		"extraction_run.currently_admissible",
	} {
		if !strings.Contains(migration, required) {
			t.Fatalf("current-document-version migration is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"DELETE FROM",
		"UPDATE stacks.document_versions",
		"UPDATE stacks.extraction_runs",
		"-- +goose Down",
	} {
		if strings.Contains(migration, forbidden) {
			t.Fatalf("current-document-version migration rewrites immutable history with %q", forbidden)
		}
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

func readFinalIntegrationMigration(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "db", "migrations", "00005_manager_confidence_final_fixes.sql")
	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %q: %v", path, err)
	}
	return string(migration)
}

func readLegacyAdmissionMigration(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "db", "migrations", "00006_legacy_admission_boundary.sql")
	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %q: %v", path, err)
	}
	return string(migration)
}

func readCompatibilityAdmissionMigration(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "db", "migrations", "00007_compatibility_admission_boundary.sql")
	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %q: %v", path, err)
	}
	return string(migration)
}

func readSnapshotCoherenceAdmissionMigration(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "db", "migrations", "00008_snapshot_coherence_admission_boundary.sql")
	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %q: %v", path, err)
	}
	return string(migration)
}

func readModelProviderProvenanceMigration(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "db", "migrations", "00010_model_provider_provenance.sql")
	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %q: %v", path, err)
	}
	return string(migration)
}

func readCurrentDocumentVersionMigration(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "db", "migrations", "00011_current_document_version.sql")
	migration, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %q: %v", path, err)
	}
	return string(migration)
}
