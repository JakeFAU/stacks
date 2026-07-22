package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	analysisdomain "stacks/internal/analysis"
	"stacks/internal/entity"
	"stacks/internal/extract"
	"stacks/internal/ingest"
	"stacks/internal/knowledge"
	"stacks/internal/source"
)

const testDatabaseURLEnvironmentVariable = "STACKS_TEST_DATABASE_URL"

func TestIngestionRepositoryResumesVersionAndCompletesAtomically(t *testing.T) {
	pool := openIntegrationDatabase(t)
	repository := NewIngestionRepository(pool)
	ctx := context.Background()
	version := testDocumentVersion(t, testIdentifier("document-ingestion-state"))
	derivation := testExtractionDerivation(t, version)

	first, err := repository.PrepareVersion(ctx, version, derivation, 5*time.Minute)
	if err != nil {
		t.Fatalf("prepare first ingestion attempt: %v", err)
	}
	if first.Status != ingest.VersionStatusPending || first.RetryCount != 0 {
		t.Fatalf("first state = %#v, want pending retry_count=0", first)
	}
	if err := repository.RecordFailure(ctx, first.DerivationID, first.LeaseOwner, ingest.VersionStatusIncomplete, ingest.FailureStorage); err != nil {
		t.Fatalf("record incomplete attempt: %v", err)
	}
	second, err := repository.PrepareVersion(ctx, version, derivation, 5*time.Minute)
	if err != nil {
		t.Fatalf("prepare retry: %v", err)
	}
	if second.ID != first.ID || second.Status != ingest.VersionStatusPending || second.RetryCount != 1 || second.FailureCode != "" {
		t.Fatalf("retry state = %#v, want same pending version with retry_count=1", second)
	}
	if err := repository.CompleteVersion(ctx, ingest.Completion{VersionID: second.ID, DerivationID: second.DerivationID, LeaseOwner: second.LeaseOwner}); err != nil {
		t.Fatalf("complete retry: %v", err)
	}
	complete, err := repository.PrepareVersion(ctx, version, derivation, 5*time.Minute)
	if err != nil {
		t.Fatalf("prepare completed version: %v", err)
	}
	if complete.Status != ingest.VersionStatusComplete || complete.RetryCount != 1 {
		t.Fatalf("completed state = %#v, want complete retry_count=1", complete)
	}
}

func TestConcurrentExtractionClaimAllowsOneActiveOwner(t *testing.T) {
	pool := openIntegrationDatabase(t)
	version := testDocumentVersion(t, testIdentifier("document-concurrent-claim"))
	derivation := testExtractionDerivation(t, version)
	start := make(chan struct{})
	type claimResult struct {
		state ingest.VersionState
		err   error
	}
	results := make(chan claimResult, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			state, err := NewIngestionRepository(pool).PrepareVersion(context.Background(), version, derivation, 5*time.Minute)
			results <- claimResult{state: state, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	counts := map[ingest.VersionStatus]int{}
	for result := range results {
		if result.err != nil {
			t.Fatalf("claim extraction derivation: %v", result.err)
		}
		counts[result.state.Status]++
		if result.state.Status == ingest.VersionStatusPending && result.state.LeaseOwner == "" {
			t.Fatal("winning extraction claim has no owner")
		}
		if result.state.Status == ingest.VersionStatusBusy && result.state.LeaseOwner != "" {
			t.Fatal("busy extraction claim disclosed another worker owner")
		}
	}
	if counts[ingest.VersionStatusPending] != 1 || counts[ingest.VersionStatusBusy] != 1 {
		t.Fatalf("claim statuses = %#v, want one pending owner and one busy worker", counts)
	}
}

func TestExtractionCompletionAndFailureRejectNonOwner(t *testing.T) {
	pool := openIntegrationDatabase(t)
	repository := NewIngestionRepository(pool)
	version := testDocumentVersion(t, testIdentifier("document-lease-owner"))
	state, err := repository.PrepareVersion(context.Background(), version, testExtractionDerivation(t, version), 5*time.Minute)
	if err != nil {
		t.Fatalf("claim extraction derivation: %v", err)
	}
	if err := repository.RecordFailure(context.Background(), state.DerivationID, uuid.NewString(), ingest.VersionStatusIncomplete, ingest.FailureStorage); err == nil {
		t.Fatal("non-owner failure update error = nil")
	}
	if err := repository.CompleteVersion(context.Background(), ingest.Completion{
		VersionID: state.ID, DerivationID: state.DerivationID, LeaseOwner: uuid.NewString(),
	}); err == nil {
		t.Fatal("non-owner completion error = nil")
	}
	if err := repository.CompleteVersion(context.Background(), ingest.Completion{
		VersionID: state.ID, DerivationID: state.DerivationID, LeaseOwner: state.LeaseOwner,
	}); err != nil {
		t.Fatalf("owner completion: %v", err)
	}
}

func TestExpiredExtractionClaimCanBeRecoveredByNewOwner(t *testing.T) {
	pool := openIntegrationDatabase(t)
	repository := NewIngestionRepository(pool)
	version := testDocumentVersion(t, testIdentifier("document-expired-claim"))
	derivation := testExtractionDerivation(t, version)
	first, err := repository.PrepareVersion(context.Background(), version, derivation, 5*time.Minute)
	if err != nil {
		t.Fatalf("claim extraction derivation: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE stacks.extraction_runs
		SET lease_expires_at = clock_timestamp() - interval '1 second'
		WHERE id = $1`, first.DerivationID); err != nil {
		t.Fatalf("expire synthetic extraction claim: %v", err)
	}

	recovered, err := repository.PrepareVersion(context.Background(), version, derivation, 5*time.Minute)
	if err != nil {
		t.Fatalf("recover expired extraction claim: %v", err)
	}
	if recovered.Status != ingest.VersionStatusPending || recovered.RetryCount != 1 ||
		recovered.LeaseOwner == "" || recovered.LeaseOwner == first.LeaseOwner {
		t.Fatalf("recovered state = %#v, want retry with a new active owner", recovered)
	}
}

func TestPre00005LegacyRowsRemainAuditableButNotCurrentlyAdmissible(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	entities := NewEntityRepository(pool)
	ingestion := NewIngestionRepository(pool)
	analysisRepository := NewAnalysisRepository(pool)

	employee, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Legacy Employee"})
	if err != nil {
		t.Fatalf("create legacy employee: %v", err)
	}
	manager, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Legacy Manager"})
	if err != nil {
		t.Fatalf("create legacy manager: %v", err)
	}
	version := testDocumentVersion(t, testIdentifier("document-pre-00005-upgrade"))
	documents := NewDocumentRepository(pool)
	if _, _, err := documents.PutDocumentVersion(ctx, version); err != nil {
		t.Fatalf("put legacy source version: %v", err)
	}
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, TabID: "tab-synthetic", StartOffset: 0,
		EndOffset: len("Synthetic"), Quote: "Synthetic",
	})
	if err != nil {
		t.Fatalf("new legacy evidence span: %v", err)
	}
	storedSpan, err := documents.PutEvidenceSpan(ctx, span)
	if err != nil {
		t.Fatalf("put legacy evidence span: %v", err)
	}

	managerMentionID := uuid.NewString()
	employeeMentionID := uuid.NewString()
	for _, mention := range []struct {
		id      string
		surface string
		role    string
	}{
		{id: managerMentionID, surface: "Unsafe Model Manager", role: "speaker"},
		{id: employeeMentionID, surface: "Unsafe Model Employee", role: "reference"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO stacks.mentions
				(id, evidence_span_id, surface, normalized_name, normalized_email, role, recorded_at, currently_admissible)
			VALUES ($1, $2, $3, $4, '', $5, $6, false)`,
			mention.id, storedSpan.ID, mention.surface, entity.NormalizeName(mention.surface), mention.role, time.Now().UTC()); err != nil {
			t.Fatalf("seed pre-00005 mention: %v", err)
		}
	}

	managerProposalID := uuid.NewString()
	employeeProposalID := uuid.NewString()
	managerDecisionID := uuid.NewString()
	employeeDecisionID := uuid.NewString()
	for _, resolution := range []struct {
		proposalID string
		mentionID  string
		decisionID string
		entityID   string
	}{
		{managerProposalID, managerMentionID, managerDecisionID, manager.ID},
		{employeeProposalID, employeeMentionID, employeeDecisionID, employee.ID},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO stacks.resolution_proposals (id, mention_id, status, derivation, recorded_at)
			VALUES ($1, $2, 'resolved', 'legacy_model_extraction', $3)`,
			resolution.proposalID, resolution.mentionID, time.Now().UTC()); err != nil {
			t.Fatalf("seed pre-00005 proposal: %v", err)
		}
		digest := sha256.Sum256([]byte(resolution.decisionID))
		if _, err := pool.Exec(ctx, `
			INSERT INTO stacks.resolution_decisions
				(id, proposal_id, outcome, entity_id, digest, recorded_at, currently_admissible)
			VALUES ($1, $2, 'accepted', $3, $4, $5, false)`,
			resolution.decisionID, resolution.proposalID, resolution.entityID, digest[:], time.Now().UTC()); err != nil {
			t.Fatalf("seed pre-00005 decision: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO stacks.entity_alias_assertions
				(decision_id, entity_id, normalized_value, alias_type, recorded_at)
			VALUES ($1, $2, $3, 'name', $4)`,
			resolution.decisionID, resolution.entityID, entity.NormalizeName("Unsafe Model Alias"), time.Now().UTC()); err != nil {
			t.Fatalf("seed pre-00005 alias assertion: %v", err)
		}
	}

	observationID := uuid.NewString()
	observationDigest := sha256.Sum256([]byte("legacy observation " + observationID))
	validTime := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
		INSERT INTO stacks.observations
			(id, subject_mention_id, object_mention_id, predicate, valid_start, recorded_at,
			 derivation, epistemic_status, digest, currently_admissible)
		VALUES ($1, $2, $3, 'interaction_signal', $4, $5,
		        'legacy_model_extraction', 'inferred', $6, false)`,
		observationID, managerMentionID, employeeMentionID, validTime, time.Now().UTC(), observationDigest[:]); err != nil {
		t.Fatalf("seed pre-00005 observation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO stacks.observation_evidence (observation_id, evidence_span_id) VALUES ($1, $2)`,
		observationID, storedSpan.ID); err != nil {
		t.Fatalf("seed pre-00005 observation evidence: %v", err)
	}

	const unsafeRationale = "The manager secretly distrusts the employee."
	signalID := uuid.NewString()
	signalDigest := sha256.Sum256([]byte("legacy signal " + signalID))
	transaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("start legacy signal seed: %v", err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO stacks.interaction_signals
			(id, observation_id, category, direction, extraction_model_id, prompt_version,
			 rationale, confidence, digest, currently_admissible)
		VALUES ($1, $2, 'delegation_autonomy', 'weakening', 'legacy-model', 'extract-legacy',
		        $3, 0.9, $4, false)`, signalID, observationID, unsafeRationale, signalDigest[:]); err != nil {
		_ = transaction.Rollback(ctx)
		t.Fatalf("seed pre-00005 signal: %v", err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO stacks.signal_evidence (signal_id, evidence_span_id, role)
		VALUES ($1, $2, 'supporting')`, signalID, storedSpan.ID); err != nil {
		_ = transaction.Rollback(ctx)
		t.Fatalf("seed pre-00005 signal evidence: %v", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatalf("commit pre-00005 signal: %v", err)
	}

	legacyAnalysisDigest := sha256.Sum256([]byte("legacy analysis " + uuid.NewString()))
	legacyAnalysisID := uuid.NewString()
	const unsafeHypothesis = "The manager has lost confidence."
	const unsafeReport = `{"rationale":"private hidden-state assertion"}`
	if _, err := pool.Exec(ctx, `
		INSERT INTO stacks.analysis_runs
			(id, employee_entity_id, manager_entity_id, input_digest, analysis_prompt_version,
			 policy_version, state, recorded_at, completed_at, hypothesis, report_state, report_json,
			 currently_admissible)
		VALUES ($1, $2, $3, $4, 'analyze-legacy', 'policy-legacy', 'complete', $5, $5,
		        $6, 'possible declining-confidence signal', $7::jsonb, false)`,
		legacyAnalysisID, employee.ID, manager.ID, legacyAnalysisDigest[:], time.Now().UTC(), unsafeHypothesis, unsafeReport); err != nil {
		t.Fatalf("seed pre-00005 analysis: %v", err)
	}

	var storedRationale, storedHypothesis string
	var storedReport []byte
	if err := pool.QueryRow(ctx, `
		SELECT signal.rationale, run.hypothesis, run.report_json
		FROM stacks.interaction_signals AS signal
		CROSS JOIN stacks.analysis_runs AS run
		WHERE signal.id = $1 AND run.id = $2`, signalID, legacyAnalysisID).Scan(
		&storedRationale, &storedHypothesis, &storedReport); err != nil {
		t.Fatalf("load preserved legacy audit payload: %v", err)
	}
	if storedRationale != unsafeRationale || storedHypothesis != unsafeHypothesis || len(storedReport) == 0 {
		t.Fatalf("legacy audit payload = %q/%q/%q, want unchanged", storedRationale, storedHypothesis, storedReport)
	}

	snapshots := snapshotsForTest(t, ingestion)
	assertSnapshotAlias(t, snapshots, manager.ID, entity.NormalizeName("Unsafe Model Alias"), false)
	pair, err := analysisRepository.LoadPairInputs(ctx, employee.ID, manager.ID)
	if err != nil {
		t.Fatalf("load legacy pair inputs: %v", err)
	}
	if pair.Accepted || len(pair.Signals) != 0 {
		t.Fatalf("legacy pair = %#v, want no currently admitted identities or signals", pair)
	}
	if _, found, err := findCompletedAnalysis(ctx, pool, legacyAnalysisDigest); err != nil || found {
		t.Fatalf("find legacy analysis = (found %t, error %v), want preserved but non-renderable", found, err)
	}

	for _, correction := range []struct {
		decisionID string
		entityID   string
	}{
		{managerDecisionID, manager.ID},
		{employeeDecisionID, employee.ID},
	} {
		if _, err := entities.CorrectReviewDecision(ctx, correction.decisionID, ResolutionDecisionInput{
			Outcome: ResolutionOutcomeAccepted, EntityID: correction.entityID,
		}); err != nil {
			t.Fatalf("explicitly re-review legacy identity: %v", err)
		}
	}
	assertSnapshotAlias(t, snapshotsForTest(t, ingestion), manager.ID, entity.NormalizeName("Unsafe Model Manager"), false)
	pair, err = analysisRepository.LoadPairInputs(ctx, employee.ID, manager.ID)
	if err != nil {
		t.Fatalf("load explicitly reviewed legacy pair: %v", err)
	}
	if !pair.Accepted || len(pair.Signals) != 0 {
		t.Fatalf("explicitly reviewed legacy pair = %#v, want identities admitted but unsafe derived signals excluded", pair)
	}
}

func TestLegacyAdmissionMigrationUpgradesPre00005RowsWithoutRewritingPayload(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	schemaName := "stacks_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := `"` + schemaName + `"`
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create isolated upgrade schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	})

	for _, migration := range []string{
		"00002_manager_confidence_poc.sql",
		"00003_ingestion_processing_state.sql",
		"00004_temporal_pair_analysis.sql",
	} {
		applyMigrationToSchema(t, pool, quotedSchema, migration)
	}

	legacy := seedPre00005AuditRows(t, pool, quotedSchema)
	applyMigrationToSchema(t, pool, quotedSchema, "00005_manager_confidence_final_fixes.sql")
	applyMigrationToSchema(t, pool, quotedSchema, "00006_legacy_admission_boundary.sql")

	var rationalePreserved, analysisPreserved bool
	if err := pool.QueryRow(ctx, `
		SELECT signal.rationale = $3,
		       run.hypothesis = $4 AND run.report_json = $5::jsonb
		FROM `+quotedSchema+`.interaction_signals AS signal
		CROSS JOIN `+quotedSchema+`.analysis_runs AS run
		WHERE signal.id = $1 AND run.id = $2`,
		legacy.signalID, legacy.analysisID, legacy.rationale, legacy.hypothesis, legacy.reportJSON,
	).Scan(&rationalePreserved, &analysisPreserved); err != nil {
		t.Fatalf("load upgraded audit payload: %v", err)
	}
	if !rationalePreserved || !analysisPreserved {
		t.Fatal("pre-00005 rationale, hypothesis, or report payload was rewritten")
	}

	for _, row := range []struct {
		table string
		id    string
	}{
		{table: "mentions", id: legacy.mentionID},
		{table: "resolution_decisions", id: legacy.decisionID},
		{table: "observations", id: legacy.observationID},
		{table: "interaction_signals", id: legacy.signalID},
		{table: "analysis_runs", id: legacy.analysisID},
	} {
		var admissible bool
		if err := pool.QueryRow(ctx, "SELECT currently_admissible FROM "+quotedSchema+`."`+row.table+`" WHERE id = $1`, row.id).Scan(&admissible); err != nil {
			t.Fatalf("load upgraded %s admission state: %v", row.table, err)
		}
		if admissible {
			t.Fatalf("pre-00005 %s row remained currently admissible", row.table)
		}
	}

	var totalAliases, admittedAliases int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE decision.currently_admissible)
		FROM `+quotedSchema+`.entity_alias_assertions AS assertion
		JOIN `+quotedSchema+`.resolution_decisions AS decision ON decision.id = assertion.decision_id
		WHERE assertion.normalized_value IN ($1, $2)`, legacy.normalizedMention, legacy.normalizedAlias,
	).Scan(&totalAliases, &admittedAliases); err != nil {
		t.Fatalf("load upgraded alias admission state: %v", err)
	}
	if totalAliases == 0 || admittedAliases != 0 {
		t.Fatalf("upgraded aliases total/admitted = %d/%d, want preserved but non-admissible", totalAliases, admittedAliases)
	}

	for _, table := range []string{
		"extraction_runs", "mentions", "resolution_decisions", "observations", "interaction_signals", "analysis_runs",
	} {
		var columnDefault string
		if err := pool.QueryRow(ctx, `
			SELECT column_default
			FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = $2 AND column_name = 'currently_admissible'`,
			schemaName, table).Scan(&columnDefault); err != nil {
			t.Fatalf("load post-fix %s admission default: %v", table, err)
		}
		if columnDefault != "true" {
			t.Fatalf("post-fix %s admission default = %q, want true", table, columnDefault)
		}
	}
}

type pre00005AuditRows struct {
	mentionID         string
	decisionID        string
	observationID     string
	signalID          string
	analysisID        string
	normalizedMention string
	normalizedAlias   string
	rationale         string
	hypothesis        string
	reportJSON        string
}

func seedPre00005AuditRows(t *testing.T, pool *pgxpool.Pool, quotedSchema string) pre00005AuditRows {
	t.Helper()
	ctx := context.Background()
	legacy := pre00005AuditRows{
		mentionID:         uuid.NewString(),
		decisionID:        uuid.NewString(),
		observationID:     uuid.NewString(),
		signalID:          uuid.NewString(),
		analysisID:        uuid.NewString(),
		normalizedMention: entity.NormalizeName("Unsafe Legacy Surface"),
		normalizedAlias:   entity.NormalizeName("Unsafe Legacy Alias"),
		rationale:         "Legacy private hidden-state rationale.",
		hypothesis:        "Legacy private hidden-state hypothesis.",
		reportJSON:        `{"rationale":"legacy private hidden-state report"}`,
	}
	sourceID := uuid.NewString()
	versionID := uuid.NewString()
	tabID := uuid.NewString()
	spanID := uuid.NewString()
	entityID := uuid.NewString()
	proposalID := uuid.NewString()
	recordedAt := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)
	digest := func(value string) []byte {
		sum := sha256.Sum256([]byte(value))
		return sum[:]
	}

	statements := []struct {
		operation string
		query     string
		arguments []any
	}{
		{"source document", "INSERT INTO " + quotedSchema + `.source_documents (id, provider, provider_document_id, recorded_at) VALUES ($1, 'drive', 'legacy-document', $2)`, []any{sourceID, recordedAt}},
		{"document version", "INSERT INTO " + quotedSchema + `.document_versions (id, source_document_id, digest, recorded_at) VALUES ($1, $2, $3, $4)`, []any{versionID, sourceID, digest("version"), recordedAt}},
		{"document tab", "INSERT INTO " + quotedSchema + `.document_tabs (id, document_version_id, provider_tab_id, title, title_path, display_order, role, content, content_digest) VALUES ($1, $2, 'legacy-tab', 'Transcript', ARRAY['Transcript'], 0, 'transcript', 'Unsafe Legacy Surface', $3)`, []any{tabID, versionID, digest("tab")}},
		{"evidence span", "INSERT INTO " + quotedSchema + `.evidence_spans (id, document_tab_id, start_offset, end_offset, quote) VALUES ($1, $2, 0, 21, 'Unsafe Legacy Surface')`, []any{spanID, tabID}},
		{"entity", "INSERT INTO " + quotedSchema + `.entities (id, kind, display_name, recorded_at) VALUES ($1, 'person', 'Synthetic Person', $2)`, []any{entityID, recordedAt}},
		{"standalone alias", "INSERT INTO " + quotedSchema + `.entity_aliases (entity_id, normalized_value, alias_type, recorded_at) VALUES ($1, $2, 'name', $3)`, []any{entityID, legacy.normalizedAlias, recordedAt}},
		{"mention", "INSERT INTO " + quotedSchema + `.mentions (id, evidence_span_id, surface, role, recorded_at) VALUES ($1, $2, 'Unsafe Legacy Surface', 'speaker', $3)`, []any{legacy.mentionID, spanID, recordedAt}},
		{"proposal", "INSERT INTO " + quotedSchema + `.resolution_proposals (id, mention_id, status, derivation, recorded_at) VALUES ($1, $2, 'resolved', 'legacy_model', $3)`, []any{proposalID, legacy.mentionID, recordedAt}},
		{"decision", "INSERT INTO " + quotedSchema + `.resolution_decisions (id, proposal_id, outcome, entity_id, digest, recorded_at) VALUES ($1, $2, 'accepted', $3, $4, $5)`, []any{legacy.decisionID, proposalID, entityID, digest("decision"), recordedAt}},
		{"observation", "INSERT INTO " + quotedSchema + `.observations (id, subject_entity_id, subject_mention_id, predicate, recorded_at, derivation, epistemic_status, digest) VALUES ($1, $2, $3, 'interaction_signal', $4, 'legacy_model', 'inferred', $5)`, []any{legacy.observationID, entityID, legacy.mentionID, recordedAt, digest("observation")}},
		{"observation evidence", "INSERT INTO " + quotedSchema + `.observation_evidence (observation_id, evidence_span_id) VALUES ($1, $2)`, []any{legacy.observationID, spanID}},
		{"analysis", "INSERT INTO " + quotedSchema + `.analysis_runs (id, employee_entity_id, manager_entity_id, input_digest, analysis_prompt_version, policy_version, state, recorded_at, completed_at, hypothesis, report_state, report_json) VALUES ($1, $2, $2, $3, 'legacy-prompt', 'legacy-policy', 'complete', $4, $4, $5, 'possible declining-confidence signal', $6::jsonb)`, []any{legacy.analysisID, entityID, digest("analysis"), recordedAt, legacy.hypothesis, legacy.reportJSON}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.arguments...); err != nil {
			t.Fatalf("seed pre-00005 %s: %v", statement.operation, err)
		}
	}

	transaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("start pre-00005 signal seed: %v", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.
	if _, err := transaction.Exec(ctx, "INSERT INTO "+quotedSchema+`.interaction_signals (id, observation_id, category, direction, extraction_model_id, prompt_version, rationale, confidence, digest) VALUES ($1, $2, 'delegation_autonomy', 'weakening', 'legacy-model', 'legacy-prompt', $3, 0.9, $4)`, legacy.signalID, legacy.observationID, legacy.rationale, digest("signal")); err != nil {
		t.Fatalf("seed pre-00005 signal: %v", err)
	}
	if _, err := transaction.Exec(ctx, "INSERT INTO "+quotedSchema+`.signal_evidence (signal_id, evidence_span_id, role) VALUES ($1, $2, 'supporting')`, legacy.signalID, spanID); err != nil {
		t.Fatalf("seed pre-00005 signal evidence: %v", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatalf("commit pre-00005 signal seed: %v", err)
	}
	return legacy
}

func applyMigrationToSchema(t *testing.T, pool *pgxpool.Pool, quotedSchema, filename string) {
	t.Helper()
	path := filepath.Join("..", "..", "db", "migrations", filename)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %q: %v", filename, err)
	}
	migration := strings.ReplaceAll(string(contents), "stacks.", quotedSchema+".")
	if _, err := pool.Exec(context.Background(), migration, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("apply migration %q to isolated schema: %v", filename, err)
	}
}

func TestPutDocumentVersionIsIdempotent(t *testing.T) {
	pool := openIntegrationDatabase(t)
	documents := NewDocumentRepository(pool)
	version := testDocumentVersion(t, testIdentifier("document-idempotent"))

	first, created, err := documents.PutDocumentVersion(context.Background(), version)
	if err != nil {
		t.Fatalf("put first document version: %v", err)
	}
	if !created {
		t.Fatal("first document version insertion created = false, want true")
	}

	second, created, err := documents.PutDocumentVersion(context.Background(), version)
	if err != nil {
		t.Fatalf("put repeated document version: %v", err)
	}
	if created {
		t.Fatal("repeated document version insertion created = true, want false")
	}
	if second.ID != first.ID {
		t.Fatalf("repeated document version ID = %q, want %q", second.ID, first.ID)
	}
}

func TestDocumentTransactionRollsBackVersion(t *testing.T) {
	pool := openIntegrationDatabase(t)
	documents := NewDocumentRepository(pool)
	version := testDocumentVersion(t, testIdentifier("document-rollback"))

	err := documents.InTransaction(context.Background(), func(transaction *DocumentRepository) error {
		_, _, err := transaction.PutDocumentVersion(context.Background(), version)
		if err != nil {
			return err
		}
		return errors.New("rollback requested")
	})
	if err == nil {
		t.Fatal("document transaction error = nil, want rollback error")
	}

	_, created, err := documents.PutDocumentVersion(context.Background(), version)
	if err != nil {
		t.Fatalf("put document version after rollback: %v", err)
	}
	if !created {
		t.Fatal("document version after rollback created = false, want true")
	}
}

func TestCorrectionLeavesOneEffectiveDecision(t *testing.T) {
	pool := openIntegrationDatabase(t)
	entities := NewEntityRepository(pool)
	ctx := context.Background()

	firstEntity, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Person A"})
	if err != nil {
		t.Fatalf("create first entity: %v", err)
	}
	if firstEntity.RecordedAt.IsZero() {
		t.Fatal("first entity recorded time is zero")
	}
	secondEntity, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Person B"})
	if err != nil {
		t.Fatalf("create second entity: %v", err)
	}
	proposal, err := entities.CreateResolutionProposal(ctx, ResolutionProposalInput{MentionID: createSyntheticMention(t, pool)})
	if err != nil {
		t.Fatalf("create resolution proposal: %v", err)
	}
	first, err := entities.RecordDecision(ctx, ResolutionDecisionInput{
		ProposalID: proposal.ID,
		Outcome:    ResolutionOutcomeAccepted,
		EntityID:   firstEntity.ID,
	})
	if err != nil {
		t.Fatalf("record initial decision: %v", err)
	}
	second, err := entities.CorrectDecision(ctx, first.ID, ResolutionDecisionInput{
		Outcome:  ResolutionOutcomeAccepted,
		EntityID: secondEntity.ID,
	})
	if err != nil {
		t.Fatalf("correct decision: %v", err)
	}
	if second.SupersedesID != first.ID {
		t.Fatalf("correction supersedes %q, want %q", second.SupersedesID, first.ID)
	}
	repeatedCorrection, err := entities.CorrectDecision(ctx, first.ID, ResolutionDecisionInput{
		Outcome:  ResolutionOutcomeAccepted,
		EntityID: secondEntity.ID,
	})
	if err != nil {
		t.Fatalf("repeat correction: %v", err)
	}
	if repeatedCorrection.ID != second.ID {
		t.Fatalf("repeated correction ID = %q, want %q", repeatedCorrection.ID, second.ID)
	}

	effective, err := entities.EffectiveDecision(ctx, proposal.ID)
	if err != nil {
		t.Fatalf("load effective decision: %v", err)
	}
	if effective.ID != second.ID {
		t.Fatalf("effective decision ID = %q, want %q", effective.ID, second.ID)
	}

	var decisionCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stacks.resolution_decisions WHERE proposal_id = $1`, proposal.ID).Scan(&decisionCount); err != nil {
		t.Fatalf("count decision history: %v", err)
	}
	if decisionCount != 2 {
		t.Fatalf("decision history count = %d, want 2", decisionCount)
	}
	firstDetail, err := entities.ShowEntityDetail(ctx, firstEntity.ID)
	if err != nil {
		t.Fatalf("show superseded entity: %v", err)
	}
	if firstDetail.MentionCount != 0 || len(firstDetail.Evidence) != 0 {
		t.Fatalf("superseded entity detail = %#v, want no effective mentions or evidence", firstDetail)
	}
	secondDetail, err := entities.ShowEntityDetail(ctx, secondEntity.ID)
	if err != nil {
		t.Fatalf("show effective entity: %v", err)
	}
	if secondDetail.MentionCount != 1 || len(secondDetail.Evidence) != 1 {
		t.Fatalf("effective entity detail = %#v, want one effective mention and evidence", secondDetail)
	}
}

func TestInteractiveReviewActionsAppendHistoryAndRejectStaleState(t *testing.T) {
	pool := openIntegrationDatabase(t)
	repository := NewEntityRepository(pool)
	ctx := context.Background()

	firstEntity, err := repository.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Review Person A"})
	if err != nil {
		t.Fatalf("create first review entity: %v", err)
	}
	secondEntity, err := repository.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Review Person B"})
	if err != nil {
		t.Fatalf("create second review entity: %v", err)
	}
	proposal, err := repository.CreateResolutionProposal(ctx, ResolutionProposalInput{MentionID: createSyntheticMention(t, pool)})
	if err != nil {
		t.Fatalf("create review proposal: %v", err)
	}
	accepted, err := repository.RecordReviewDecision(ctx, ResolutionDecisionInput{
		ProposalID: proposal.ID,
		Outcome:    ResolutionOutcomeAccepted,
		EntityID:   firstEntity.ID,
	})
	if err != nil {
		t.Fatalf("accept review proposal: %v", err)
	}
	if _, err := repository.RecordReviewDecision(ctx, ResolutionDecisionInput{
		ProposalID: proposal.ID,
		Outcome:    ResolutionOutcomeAccepted,
		EntityID:   firstEntity.ID,
	}); err == nil {
		t.Fatal("repeat review acceptance error = nil, want effective-state error")
	}
	correction, err := repository.CorrectReviewDecision(ctx, accepted.ID, ResolutionDecisionInput{
		Outcome:  ResolutionOutcomeAccepted,
		EntityID: secondEntity.ID,
	})
	if err != nil {
		t.Fatalf("correct review decision: %v", err)
	}
	if correction.SupersedesID != accepted.ID {
		t.Fatalf("correction supersedes %q, want %q", correction.SupersedesID, accepted.ID)
	}
	if _, err := repository.CorrectReviewDecision(ctx, accepted.ID, ResolutionDecisionInput{
		Outcome:  ResolutionOutcomeAccepted,
		EntityID: firstEntity.ID,
	}); err == nil {
		t.Fatal("correct stale decision error = nil, want not effective error")
	}

	createProposal, err := repository.CreateResolutionProposal(ctx, ResolutionProposalInput{MentionID: createSyntheticMention(t, pool)})
	if err != nil {
		t.Fatalf("create new-person proposal: %v", err)
	}
	createdEntity, createdDecision, err := repository.CreateReviewPerson(ctx, CreateReviewPersonInput{
		ProposalID:  createProposal.ID,
		EntityID:    uuid.NewString(),
		Kind:        "person",
		DisplayName: "Synthetic Created Person",
		Aliases:     []AliasInput{{Type: "name", NormalizedValue: "synthetic created person"}},
	})
	if err != nil {
		t.Fatalf("create review person: %v", err)
	}
	if createdEntity.ID == "" || createdDecision.Outcome != ResolutionOutcomeCreated || createdDecision.EntityID != createdEntity.ID {
		t.Fatalf("created review result = (%#v, %#v), want created entity decision", createdEntity, createdDecision)
	}

	rejectedProposal, err := repository.CreateResolutionProposal(ctx, ResolutionProposalInput{MentionID: createSyntheticMention(t, pool)})
	if err != nil {
		t.Fatalf("create rejected proposal: %v", err)
	}
	if _, err := repository.RecordReviewDecision(ctx, ResolutionDecisionInput{ProposalID: rejectedProposal.ID, Outcome: ResolutionOutcomeRejected}); err != nil {
		t.Fatalf("reject review proposal: %v", err)
	}
}

func TestAcceptedAliasFollowsEffectiveDecisionLifecycle(t *testing.T) {
	pool := openIntegrationDatabase(t)
	entities := NewEntityRepository(pool)
	ingestion := NewIngestionRepository(pool)
	ctx := context.Background()
	first, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Alias Owner A"})
	if err != nil {
		t.Fatalf("create first alias owner: %v", err)
	}
	second, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Alias Owner B"})
	if err != nil {
		t.Fatalf("create second alias owner: %v", err)
	}
	proposal, err := entities.CreateResolutionProposal(ctx, ResolutionProposalInput{MentionID: createSyntheticMention(t, pool)})
	if err != nil {
		t.Fatalf("create alias proposal: %v", err)
	}
	accepted, err := entities.RecordReviewDecision(ctx, ResolutionDecisionInput{
		ProposalID: proposal.ID, Outcome: ResolutionOutcomeAccepted, EntityID: first.ID,
	})
	if err != nil {
		t.Fatalf("accept alias proposal: %v", err)
	}
	assertSnapshotAlias(t, snapshotsForTest(t, ingestion), first.ID, "synthetic", true)

	if _, err := entities.CorrectReviewDecision(ctx, accepted.ID, ResolutionDecisionInput{
		Outcome: ResolutionOutcomeAccepted, EntityID: second.ID,
	}); err != nil {
		t.Fatalf("correct alias decision: %v", err)
	}
	snapshots := snapshotsForTest(t, ingestion)
	assertSnapshotAlias(t, snapshots, first.ID, "synthetic", false)
	assertSnapshotAlias(t, snapshots, second.ID, "synthetic", true)
}

func snapshotsForTest(t *testing.T, repository *IngestionRepository) []entity.EntitySnapshot {
	t.Helper()
	snapshots, err := repository.EntitySnapshots(context.Background())
	if err != nil {
		t.Fatalf("list entity snapshots: %v", err)
	}
	return snapshots
}

func assertSnapshotAlias(t *testing.T, snapshots []entity.EntitySnapshot, entityID, value string, want bool) {
	t.Helper()
	for _, snapshot := range snapshots {
		if snapshot.ID != entityID {
			continue
		}
		for _, alias := range snapshot.Aliases {
			if alias.Value == value {
				if !want {
					t.Fatalf("entity %q retained superseded alias %q", entityID, value)
				}
				return
			}
		}
		if want {
			t.Fatalf("entity %q is missing effective alias %q", entityID, value)
		}
		return
	}
	t.Fatalf("entity snapshot %q was not found", entityID)
}

func TestCompleteAnalysisDeduplicatesStableInput(t *testing.T) {
	pool := openIntegrationDatabase(t)
	entities := NewEntityRepository(pool)
	analysis := NewAnalysisRepository(pool)
	ctx := context.Background()

	employee, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Employee"})
	if err != nil {
		t.Fatalf("create employee: %v", err)
	}
	manager, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Manager"})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	version := testDocumentVersion(t, testIdentifier("document-analysis"))
	documents := NewDocumentRepository(pool)
	storedVersion, _, err := documents.PutDocumentVersion(ctx, version)
	if err != nil {
		t.Fatalf("put analysis document version: %v", err)
	}
	digest := version.Digest()
	input := AnalysisInput{
		EmployeeEntityID:      employee.ID,
		ManagerEntityID:       manager.ID,
		AnalysisPromptVersion: "analyze-test-v1",
		PolicyVersion:         "policy-test-v1",
		Inputs: []AnalysisInputReference{{
			Kind:   AnalysisInputKindDocumentVersion,
			ID:     storedVersion.ID,
			Digest: digest[:],
		}},
	}

	first, created, err := analysis.Complete(ctx, input)
	if err != nil {
		t.Fatalf("complete first analysis: %v", err)
	}
	if !created {
		t.Fatal("first analysis completion created = false, want true")
	}
	second, created, err := analysis.Complete(ctx, input)
	if err != nil {
		t.Fatalf("complete repeated analysis: %v", err)
	}
	if created {
		t.Fatal("repeated analysis completion created = true, want false")
	}
	if second.ID != first.ID {
		t.Fatalf("repeated analysis ID = %q, want %q", second.ID, first.ID)
	}

	var inputCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stacks.analysis_inputs WHERE analysis_run_id = $1`, first.ID).Scan(&inputCount); err != nil {
		t.Fatalf("count stored analysis inputs: %v", err)
	}
	if inputCount != len(input.Inputs) {
		t.Fatalf("stored analysis input count = %d, want %d", inputCount, len(input.Inputs))
	}
	wrongDigestInput := input
	wrongDigestInput.Inputs = append([]AnalysisInputReference(nil), input.Inputs...)
	wrongDigestInput.Inputs[0].Digest = make([]byte, len(digest))
	if _, _, err := analysis.Complete(ctx, wrongDigestInput); err == nil {
		t.Fatal("analysis accepted an input digest that does not belong to its document version")
	}
	typeConfusedInput := input
	typeConfusedInput.Inputs = append([]AnalysisInputReference(nil), input.Inputs...)
	typeConfusedInput.Inputs[0].Kind = AnalysisInputKindSignal
	if _, _, err := analysis.Complete(ctx, typeConfusedInput); err == nil {
		t.Fatal("analysis accepted a document version ID as a signal input")
	}
}

func TestPairAnalysisEligibilityFollowsEffectiveMentionDecisionsWithoutReingest(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	entities := NewEntityRepository(pool)
	repository := NewAnalysisRepository(pool)

	employee, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Pair Employee"})
	if err != nil {
		t.Fatalf("create employee: %v", err)
	}
	manager, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Pair Manager"})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	replacement, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Replacement Manager"})
	if err != nil {
		t.Fatalf("create replacement manager: %v", err)
	}

	pending, err := repository.LoadPairInputs(ctx, employee.ID, manager.ID)
	if err != nil {
		t.Fatalf("load pending pair inputs: %v", err)
	}
	if pending.Accepted || len(pending.Signals) != 0 {
		t.Fatalf("pending pair snapshot = %#v, want unaccepted configured identities", pending)
	}

	acceptSyntheticIdentity(t, pool, employee.ID)
	acceptSyntheticIdentity(t, pool, manager.ID)
	acceptedEmpty, err := repository.LoadPairInputs(ctx, employee.ID, manager.ID)
	if err != nil {
		t.Fatalf("load accepted empty pair inputs: %v", err)
	}
	if !acceptedEmpty.Accepted || len(acceptedEmpty.Signals) != 0 || countInputKind(acceptedEmpty.Inputs, analysisdomain.InputResolutionDecision) != 2 {
		t.Fatalf("accepted empty pair snapshot = %#v, want two audited identity inputs and no signals", acceptedEmpty)
	}
	emptyIdentity := analysisdomain.AnalysisIdentity{
		EmployeeEntityID: employee.ID, ManagerEntityID: manager.ID,
		PromptVersion: "analyze-test-v1", PolicyVersion: "policy-test-v1",
		Region: "us-east-1", ModelID: "synthetic-model", MaxTokens: 256, Inputs: acceptedEmpty.Inputs,
	}
	emptyIdentity.InputDigest, err = analysisdomain.ComputeInputDigest(emptyIdentity)
	if err != nil {
		t.Fatalf("compute accepted empty analysis identity: %v", err)
	}

	firstSubject, firstObject := createPendingPairSignal(t, pool, time.Date(2026, time.June, 3, 0, 0, 0, 0, time.UTC), "strengthening")
	secondSubject, secondObject := createPendingPairSignal(t, pool, time.Date(2026, time.July, 8, 0, 0, 0, 0, time.UTC), "weakening")
	acceptedWithPendingSignals, err := repository.LoadPairInputs(ctx, employee.ID, manager.ID)
	if err != nil {
		t.Fatalf("load accepted pair with pending signals: %v", err)
	}
	if !acceptedWithPendingSignals.Accepted || len(acceptedWithPendingSignals.Signals) != 0 {
		t.Fatalf("accepted pair with pending signals = %#v, want accepted pair and no eligible guessed signals", acceptedWithPendingSignals)
	}

	firstManagerDecision, err := entities.RecordDecision(ctx, ResolutionDecisionInput{ProposalID: firstSubject, Outcome: ResolutionOutcomeAccepted, EntityID: manager.ID})
	if err != nil {
		t.Fatalf("accept first manager mention: %v", err)
	}
	for _, decision := range []ResolutionDecisionInput{
		{ProposalID: firstObject, Outcome: ResolutionOutcomeAccepted, EntityID: employee.ID},
		{ProposalID: secondSubject, Outcome: ResolutionOutcomeAccepted, EntityID: manager.ID},
		{ProposalID: secondObject, Outcome: ResolutionOutcomeAccepted, EntityID: employee.ID},
	} {
		if _, err := entities.RecordDecision(ctx, decision); err != nil {
			t.Fatalf("accept pair mention: %v", err)
		}
	}
	accepted, err := repository.LoadPairInputs(ctx, employee.ID, manager.ID)
	if err != nil {
		t.Fatalf("load accepted pair inputs: %v", err)
	}
	if len(accepted.Signals) != 2 || countInputKind(accepted.Inputs, analysisdomain.InputResolutionDecision) != 6 ||
		countInputKind(accepted.Inputs, analysisdomain.InputSourceDocument) != 2 {
		t.Fatalf("accepted pair signals/decision/meeting inputs = %d/%d/%d, want 2/6/2",
			len(accepted.Signals), countInputKind(accepted.Inputs, analysisdomain.InputResolutionDecision), countInputKind(accepted.Inputs, analysisdomain.InputSourceDocument))
	}
	if accepted.Signals[0].MeetingID == "" || accepted.Signals[0].MeetingID == accepted.Signals[1].MeetingID {
		t.Fatalf("meeting IDs = %q/%q, want stable distinct source-document identities", accepted.Signals[0].MeetingID, accepted.Signals[1].MeetingID)
	}
	acceptedIdentity := analysisdomain.AnalysisIdentity{
		EmployeeEntityID: employee.ID, ManagerEntityID: manager.ID,
		PromptVersion: "analyze-test-v1", PolicyVersion: "policy-test-v1",
		Region: "us-east-1", ModelID: "synthetic-model", MaxTokens: 256, Inputs: accepted.Inputs,
	}
	acceptedIdentity.InputDigest, err = analysisdomain.ComputeInputDigest(acceptedIdentity)
	if err != nil {
		t.Fatalf("compute accepted analysis identity: %v", err)
	}
	if acceptedIdentity.InputDigest == emptyIdentity.InputDigest {
		t.Fatal("later accepted signals reused the accepted-empty pair identity")
	}
	acceptedCompletion := analysisdomain.Completion{
		Identity: acceptedIdentity,
		Report: analysisdomain.Report{
			Status: analysisdomain.StatusMixedOrConflicting, Rationale: "Synthetic bounded report.",
			Chronology: accepted.Signals, RecordedAt: time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
			ModelID: "synthetic-model", Region: "us-east-1", MaxTokens: 256,
			PromptVersion: "analyze-test-v1", PolicyVersion: "policy-test-v1",
		},
	}
	acceptedReport, err := repository.CompleteAnalysis(ctx, acceptedCompletion)
	if err != nil {
		t.Fatalf("complete accepted pair analysis: %v", err)
	}
	if _, err := entities.CorrectDecision(ctx, firstManagerDecision.ID, ResolutionDecisionInput{Outcome: ResolutionOutcomeAccepted, EntityID: replacement.ID}); err != nil {
		t.Fatalf("correct first manager mention between load and completion: %v", err)
	}
	if _, found, err := repository.FindCompleted(ctx, acceptedIdentity); !errors.Is(err, analysisdomain.ErrStaleAnalysisInput) || found {
		t.Fatalf("find stale cached pair analysis = (found %t, error %v), want retryable stale-input result", found, err)
	}
	if _, err := repository.CompleteAnalysis(ctx, acceptedCompletion); !errors.Is(err, analysisdomain.ErrStaleAnalysisInput) {
		t.Fatalf("complete stale pair analysis error = %v, want retryable stale-input error", err)
	}
	corrected, err := repository.LoadPairInputs(ctx, employee.ID, manager.ID)
	if err != nil {
		t.Fatalf("load corrected pair inputs: %v", err)
	}
	if len(corrected.Signals) != 1 || len(corrected.Inputs) >= len(accepted.Inputs) {
		t.Fatalf("corrected pair signals/inputs = %d/%d, want one signal and reduced current provenance", len(corrected.Signals), len(corrected.Inputs))
	}
	correctedIdentity := analysisdomain.AnalysisIdentity{
		EmployeeEntityID: employee.ID, ManagerEntityID: manager.ID,
		PromptVersion: "analyze-test-v1", PolicyVersion: "policy-test-v1",
		Region: "us-east-1", ModelID: "synthetic-model", MaxTokens: 256, Inputs: corrected.Inputs,
	}
	correctedIdentity.InputDigest, err = analysisdomain.ComputeInputDigest(correctedIdentity)
	if err != nil {
		t.Fatalf("compute corrected analysis identity: %v", err)
	}
	if correctedIdentity.InputDigest == acceptedIdentity.InputDigest {
		t.Fatal("identity correction did not change completed analysis digest")
	}
	if _, err := repository.CompleteAnalysis(ctx, analysisdomain.Completion{
		Identity: correctedIdentity,
		Report: analysisdomain.Report{
			Status: analysisdomain.StatusInsufficientEvidence, Rationale: "Synthetic insufficient report.",
			Chronology: corrected.Signals, RecordedAt: time.Date(2026, time.July, 21, 13, 0, 0, 0, time.UTC),
			ModelID: "synthetic-model", Region: "us-east-1", MaxTokens: 256,
			PromptVersion: "analyze-test-v1", PolicyVersion: "policy-test-v1",
		},
	}); err != nil {
		t.Fatalf("complete corrected pair analysis: %v", err)
	}
	var priorID string
	var priorJSON []byte
	if err := pool.QueryRow(ctx, `
		SELECT id::text, report_json
		FROM stacks.analysis_runs
		WHERE input_digest = $1`, acceptedIdentity.InputDigest[:]).Scan(&priorID, &priorJSON); err != nil {
		t.Fatalf("load historical analysis row after correction: %v", err)
	}
	if priorID != acceptedReport.ID || len(priorJSON) == 0 {
		t.Fatalf("historical analysis row = (%q, %d bytes), want preserved report %q", priorID, len(priorJSON), acceptedReport.ID)
	}
}

func acceptSyntheticIdentity(t *testing.T, pool *pgxpool.Pool, entityID string) {
	t.Helper()
	repository := NewEntityRepository(pool)
	proposal, err := repository.CreateResolutionProposal(context.Background(), ResolutionProposalInput{
		MentionID: createSyntheticMention(t, pool),
	})
	if err != nil {
		t.Fatalf("create synthetic identity proposal: %v", err)
	}
	_, err = repository.RecordDecision(context.Background(), ResolutionDecisionInput{
		ProposalID: proposal.ID, Outcome: ResolutionOutcomeAccepted, EntityID: entityID,
	})
	if err != nil {
		t.Fatalf("accept synthetic identity: %v", err)
	}
}

func createPendingPairSignal(t *testing.T, pool *pgxpool.Pool, validTime time.Time, direction string) (string, string) {
	t.Helper()
	ctx := context.Background()
	documents := NewDocumentRepository(pool)
	version := testDocumentVersion(t, testIdentifier("document-pair-analysis"))
	if _, _, err := documents.PutDocumentVersion(ctx, version); err != nil {
		t.Fatalf("put pair-analysis document: %v", err)
	}
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, TabID: "tab-synthetic", StartOffset: 0, EndOffset: len("Synthetic"), Quote: "Synthetic",
	})
	if err != nil {
		t.Fatalf("new pair-analysis evidence span: %v", err)
	}
	storedSpan, err := documents.PutEvidenceSpan(ctx, span)
	if err != nil {
		t.Fatalf("put pair-analysis evidence span: %v", err)
	}
	entities := NewEntityRepository(pool)
	managerMention, err := entities.CreateMention(ctx, MentionInput{EvidenceSpanID: storedSpan.ID, Surface: testIdentifier("Synthetic Manager Mention"), Role: "speaker"})
	if err != nil {
		t.Fatalf("create manager mention: %v", err)
	}
	employeeMention, err := entities.CreateMention(ctx, MentionInput{EvidenceSpanID: storedSpan.ID, Surface: testIdentifier("Synthetic Employee Mention"), Role: "reference"})
	if err != nil {
		t.Fatalf("create employee mention: %v", err)
	}
	managerProposal, err := entities.CreateResolutionProposal(ctx, ResolutionProposalInput{MentionID: managerMention.ID})
	if err != nil {
		t.Fatalf("create manager proposal: %v", err)
	}
	employeeProposal, err := entities.CreateResolutionProposal(ctx, ResolutionProposalInput{MentionID: employeeMention.ID})
	if err != nil {
		t.Fatalf("create employee proposal: %v", err)
	}
	graph := NewGraphRepository(pool)
	observation, err := graph.CompleteObservation(ctx, ObservationInput{
		ID: uuid.NewString(), SubjectMentionID: managerMention.ID, ObjectMentionID: employeeMention.ID,
		Predicate: "interaction_signal", ValidStart: &validTime, Derivation: "model_extraction", EpistemicStatus: "inferred",
	}, []string{storedSpan.ID})
	if err != nil {
		t.Fatalf("complete pair observation: %v", err)
	}
	if _, err := graph.CompleteSignal(ctx, SignalInput{
		ID: uuid.NewString(), ObservationID: observation.ID, Category: "delegation_autonomy", Direction: direction,
		ExtractionModelID: "synthetic-model", PromptVersion: "extract-v1", Rationale: "Synthetic observable pair rationale.", Confidence: 0.5,
	}, []SignalEvidenceInput{{EvidenceSpanID: storedSpan.ID, Role: "supporting"}}); err != nil {
		t.Fatalf("complete pair signal: %v", err)
	}
	return managerProposal.ID, employeeProposal.ID
}

func countInputKind(inputs []analysisdomain.InputReference, kind analysisdomain.InputKind) int {
	count := 0
	for _, input := range inputs {
		if input.Kind == kind {
			count++
		}
	}
	return count
}

func TestStorageRetriesDoNotDuplicateGraphRecords(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	entities := NewEntityRepository(pool)
	graph := NewGraphRepository(pool)

	entity, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Retry Person"})
	if err != nil {
		t.Fatalf("create retry entity: %v", err)
	}
	aliasInput := AliasInput{EntityID: entity.ID, NormalizedValue: "synthetic retry person", Type: "name"}
	firstAlias, err := entities.PutAlias(ctx, aliasInput)
	if err != nil {
		t.Fatalf("put first alias: %v", err)
	}
	secondAlias, err := entities.PutAlias(ctx, aliasInput)
	if err != nil {
		t.Fatalf("put repeated alias: %v", err)
	}
	if secondAlias.ID != firstAlias.ID {
		t.Fatalf("repeated alias ID = %q, want %q", secondAlias.ID, firstAlias.ID)
	}

	mentionID, spanID := createSyntheticMentionAndSpan(t, pool)
	mentionInput := MentionInput{EvidenceSpanID: spanID, Surface: "Synthetic", Role: "speaker"}
	firstMention, err := entities.CreateMention(ctx, mentionInput)
	if err != nil {
		t.Fatalf("load first mention: %v", err)
	}
	secondMention, err := entities.CreateMention(ctx, mentionInput)
	if err != nil {
		t.Fatalf("load repeated mention: %v", err)
	}
	if firstMention.ID != mentionID || secondMention.ID != mentionID {
		t.Fatal("repeated mention did not return its stable ID")
	}

	proposalInput := ResolutionProposalInput{MentionID: mentionID}
	proposal, err := entities.CreateResolutionProposal(ctx, proposalInput)
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	repeatedProposal, err := entities.CreateResolutionProposal(ctx, proposalInput)
	if err != nil {
		t.Fatalf("create repeated proposal: %v", err)
	}
	if repeatedProposal.ID != proposal.ID {
		t.Fatalf("repeated proposal ID = %q, want %q", repeatedProposal.ID, proposal.ID)
	}
	confidence := 0.75
	candidateInput := ResolutionCandidateInput{ProposalID: proposal.ID, EntityID: entity.ID, Rank: 0, Confidence: &confidence, Reason: "synthetic"}
	firstCandidate, err := entities.PutCandidate(ctx, candidateInput)
	if err != nil {
		t.Fatalf("put candidate: %v", err)
	}
	secondCandidate, err := entities.PutCandidate(ctx, candidateInput)
	if err != nil {
		t.Fatalf("put repeated candidate: %v", err)
	}
	if secondCandidate.ID != firstCandidate.ID {
		t.Fatalf("repeated candidate ID = %q, want %q", secondCandidate.ID, firstCandidate.ID)
	}

	observationInput := ObservationInput{
		ID:              uuid.NewString(),
		SubjectEntityID: entity.ID,
		Predicate:       "interacted_with",
		Derivation:      "synthetic",
		EpistemicStatus: "inferred",
	}
	firstObservation, err := graph.CompleteObservation(ctx, observationInput, []string{spanID})
	if err != nil {
		t.Fatalf("complete observation: %v", err)
	}
	secondObservation, err := graph.CompleteObservation(ctx, observationInput, []string{spanID})
	if err != nil {
		t.Fatalf("complete repeated observation: %v", err)
	}
	if secondObservation.ID != firstObservation.ID {
		t.Fatalf("repeated observation ID = %q, want %q", secondObservation.ID, firstObservation.ID)
	}

	signalInput := SignalInput{
		ID:                uuid.NewString(),
		ObservationID:     firstObservation.ID,
		Category:          "delegation_autonomy",
		Direction:         "strengthening",
		ExtractionModelID: "synthetic-model",
		PromptVersion:     "synthetic-v1",
		Confidence:        0.8,
	}
	firstSignal, err := graph.CompleteSignal(ctx, signalInput, []SignalEvidenceInput{{EvidenceSpanID: spanID, Role: "supporting"}})
	if err != nil {
		t.Fatalf("complete signal: %v", err)
	}
	secondSignal, err := graph.CompleteSignal(ctx, signalInput, []SignalEvidenceInput{{EvidenceSpanID: spanID, Role: "supporting"}})
	if err != nil {
		t.Fatalf("complete repeated signal: %v", err)
	}
	if secondSignal.ID != firstSignal.ID {
		t.Fatalf("repeated signal ID = %q, want %q", secondSignal.ID, firstSignal.ID)
	}
	changedObservation := observationInput
	changedObservation.Predicate = "different_interaction"
	if _, err := graph.CompleteObservation(ctx, changedObservation, []string{spanID}); err == nil {
		t.Fatal("changed observation payload with existing ID succeeded")
	}
	changedSignal := signalInput
	changedSignal.Direction = "weakening"
	if _, err := graph.CompleteSignal(ctx, changedSignal, []SignalEvidenceInput{{EvidenceSpanID: spanID, Role: "supporting"}}); err == nil {
		t.Fatal("changed signal payload with existing ID succeeded")
	}
}

func TestPutEvidenceSpanRejectsImmutableQuoteConflict(t *testing.T) {
	pool := openIntegrationDatabase(t)
	version := testDocumentVersion(t, testIdentifier("document-evidence-conflict"))
	documents := NewDocumentRepository(pool)
	if _, _, err := documents.PutDocumentVersion(context.Background(), version); err != nil {
		t.Fatalf("put evidence document version: %v", err)
	}
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, TabID: "tab-synthetic", StartOffset: 0, EndOffset: len("Synthetic"), Quote: "Synthetic",
	})
	if err != nil {
		t.Fatalf("new evidence span: %v", err)
	}
	stored, err := documents.PutEvidenceSpan(context.Background(), span)
	if err != nil {
		t.Fatalf("put evidence span: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE stacks.evidence_spans SET quote = 'corrupted' WHERE id = $1`, stored.ID); err != nil {
		t.Fatalf("create synthetic immutable conflict: %v", err)
	}
	if _, err := documents.PutEvidenceSpan(context.Background(), span); err == nil {
		t.Fatal("repeated evidence span with conflicting stored quote succeeded")
	}
}

func TestCompleteSignalRejectsNotesOnlyEvidence(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	version := testDocumentVersionWithRole(t, testIdentifier("document-notes-signal"), source.TabRoleGeminiNotes)
	documents := NewDocumentRepository(pool)
	if _, _, err := documents.PutDocumentVersion(ctx, version); err != nil {
		t.Fatalf("put notes document version: %v", err)
	}
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, TabID: "tab-synthetic", StartOffset: 0, EndOffset: len("Synthetic"), Quote: "Synthetic",
	})
	if err != nil {
		t.Fatalf("new notes evidence span: %v", err)
	}
	storedSpan, err := documents.PutEvidenceSpan(ctx, span)
	if err != nil {
		t.Fatalf("put notes evidence span: %v", err)
	}
	graph := NewGraphRepository(pool)
	observation, err := graph.CompleteObservation(ctx, ObservationInput{
		ID: uuid.NewString(), Predicate: "interacted_with", Derivation: "synthetic", EpistemicStatus: "inferred",
	}, []string{storedSpan.ID})
	if err != nil {
		t.Fatalf("complete notes observation: %v", err)
	}
	_, err = graph.CompleteSignal(ctx, SignalInput{
		ID: uuid.NewString(), ObservationID: observation.ID, Category: "delegation_autonomy", Direction: "strengthening",
		ExtractionModelID: "synthetic-model", PromptVersion: "synthetic-v1", Confidence: 0.8,
	}, []SignalEvidenceInput{{EvidenceSpanID: storedSpan.ID, Role: "supporting"}})
	if err == nil {
		t.Fatal("notes-only signal completion succeeded")
	}
}

func openIntegrationDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv(testDatabaseURLEnvironmentVariable)
	if databaseURL == "" {
		t.Skipf("%s is not set", testDatabaseURLEnvironmentVariable)
	}

	pool, err := Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func createSyntheticMention(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	mentionID, _ := createSyntheticMentionAndSpan(t, pool)
	return mentionID
}

func createSyntheticMentionAndSpan(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	documents := NewDocumentRepository(pool)
	version := testDocumentVersion(t, testIdentifier("document-mention"))
	if _, _, err := documents.PutDocumentVersion(context.Background(), version); err != nil {
		t.Fatalf("put mention document version: %v", err)
	}
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document:    version,
		TabID:       "tab-synthetic",
		StartOffset: 0,
		EndOffset:   len("Synthetic"),
		Quote:       "Synthetic",
	})
	if err != nil {
		t.Fatalf("new synthetic evidence span: %v", err)
	}
	storedSpan, err := documents.PutEvidenceSpan(context.Background(), span)
	if err != nil {
		t.Fatalf("put synthetic evidence span: %v", err)
	}
	entities := NewEntityRepository(pool)
	mention, err := entities.CreateMention(context.Background(), MentionInput{
		EvidenceSpanID: storedSpan.ID,
		Surface:        "Synthetic",
		Role:           "speaker",
	})
	if err != nil {
		t.Fatalf("create synthetic mention: %v", err)
	}
	return mention.ID, storedSpan.ID
}

func testIdentifier(prefix string) string {
	return prefix + "-" + uuid.NewString()
}

func testDocumentVersion(t *testing.T, providerDocumentID string) knowledge.DocumentVersion {
	return testDocumentVersionWithRole(t, providerDocumentID, source.TabRoleTranscript)
}

func testExtractionDerivation(t *testing.T, version knowledge.DocumentVersion) ingest.DerivationIdentity {
	t.Helper()
	identity := ingest.DerivationIdentity{
		Region: "us-east-1", ModelID: "synthetic-model", MaxTokens: 256,
		PromptVersion: extract.ExtractionPromptVersion,
		SchemaDigest:  sha256.Sum256(extract.ExtractionJSONSchema()),
	}
	var err error
	identity.Digest, err = ingest.ComputeDerivationDigest(version, identity)
	if err != nil {
		t.Fatalf("compute extraction derivation: %v", err)
	}
	return identity
}

func testDocumentVersionWithRole(t *testing.T, providerDocumentID string, role source.TabRole) knowledge.DocumentVersion {
	t.Helper()
	version, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
		Provider:           "synthetic-drive",
		ProviderDocumentID: providerDocumentID,
		Title:              "Synthetic meeting",
		Locator:            "https://docs.example.invalid/document/" + providerDocumentID,
		ProviderVersion:    "synthetic-version-1",
		ProviderRevision:   "synthetic-revision-1",
		ModifiedAt:         time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC),
		RecordedAt:         time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
		Tabs: []source.Tab{{
			ID:    "tab-synthetic",
			Title: "Synthetic Transcript",
			Order: 0,
			Role:  role,
			Text:  "Synthetic meeting text.",
		}},
	})
	if err != nil {
		t.Fatalf("new synthetic document version: %v", err)
	}
	return version
}
