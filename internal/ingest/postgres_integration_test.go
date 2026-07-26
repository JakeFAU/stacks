package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/jackc/pgx/v5"

	"stacks/internal/entity"
	"stacks/internal/extract"
)

const canonicalRepositoryTimeout = 10 * time.Second

type canonicalRepositoryFixture struct {
	ctx        context.Context
	repository *PostgresRepository
	admin      *pgx.Conn
	now        time.Time
}

func TestCanonicalCompletionRecordsBoundedLeaseAndTransactionOutcome(t *testing.T) {
	fixture := newCanonicalRepositoryFixture(t)
	version, metadata, derivation := canonicalPreparationInput(t, fixture.now)

	state, err := fixture.repository.PrepareVersion(
		fixture.ctx,
		version,
		metadata,
		derivation,
		"personal",
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("PrepareVersion() error = %v", err)
	}
	if state.Status != VersionStatusPending ||
		state.LeaseExpiresAt != fixture.now.Add(5*time.Minute) ||
		state.DocumentRecordedAt != version.RecordedAt() {
		t.Fatalf("PrepareVersion() = %#v, want bounded claimed attempt", state)
	}
	completion := canonicalLiveCompletion(t, version, state, derivation, fixture.now.Add(time.Minute))
	if err := fixture.repository.CompleteVersion(fixture.ctx, completion); err != nil {
		t.Fatalf("CompleteVersion() error = %v", err)
	}

	var runState, attemptState string
	var currentVersion *string
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT run.state, attempt.state, source.current_version_id
		 FROM stacks_core.extraction_runs AS run
		 JOIN stacks_core.extraction_attempts AS attempt ON attempt.run_id = run.id
		 JOIN stacks_core.document_versions AS version ON version.id = run.document_version_id
		 JOIN stacks_core.source_documents AS source ON source.id = version.source_document_id
		 WHERE run.id = $1 AND attempt.id = $2`,
		state.RunID,
		state.AttemptID,
	).Scan(&runState, &attemptState, &currentVersion); err != nil {
		t.Fatalf("inspect canonical completion: %v", err)
	}
	if runState != "completed" || attemptState != "completed" ||
		currentVersion == nil || *currentVersion != state.VersionID {
		t.Fatalf(
			"completion state = run:%q attempt:%q current:%v, want completed/completed/%q",
			runState,
			attemptState,
			currentVersion,
			state.VersionID,
		)
	}
	assertCanonicalPayloadCounts(t, fixture, 1, 2, 2, 1, 2, 1, 1, 6)
}

func TestCanonicalRollbackMakesEveryCompletionStageInvisible(t *testing.T) {
	stages := []completionStage{
		completionStageEvidence,
		completionStageIdentityInputs,
		completionStageIdentityAuthority,
		completionStageObservations,
		completionStageAdmission,
		completionStageCurrentVersion,
		completionStageExtractionCompletion,
	}
	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			fixture := newCanonicalRepositoryFixture(t)
			version, metadata, derivation := canonicalPreparationInput(t, fixture.now)
			state, err := fixture.repository.PrepareVersion(
				fixture.ctx,
				version,
				metadata,
				derivation,
				"personal",
				5*time.Minute,
			)
			if err != nil {
				t.Fatalf("PrepareVersion() error = %v", err)
			}
			injected := errors.New("synthetic completion stage failure")
			fixture.repository.afterStage = func(got completionStage) error {
				if got == stage {
					return injected
				}
				return nil
			}
			err = fixture.repository.CompleteVersion(
				fixture.ctx,
				canonicalLiveCompletion(
					t,
					version,
					state,
					derivation,
					fixture.now.Add(time.Minute),
				),
			)
			if !errors.Is(err, injected) {
				t.Fatalf("CompleteVersion() error = %v, want injected failure", err)
			}

			var runState, attemptState string
			var currentVersion *string
			if err := fixture.admin.QueryRow(
				fixture.ctx,
				`SELECT run.state, attempt.state, source.current_version_id
				 FROM stacks_core.extraction_runs AS run
				 JOIN stacks_core.extraction_attempts AS attempt ON attempt.run_id = run.id
				 JOIN stacks_core.document_versions AS version ON version.id = run.document_version_id
				 JOIN stacks_core.source_documents AS source ON source.id = version.source_document_id
				 WHERE run.id = $1 AND attempt.id = $2`,
				state.RunID,
				state.AttemptID,
			).Scan(&runState, &attemptState, &currentVersion); err != nil {
				t.Fatalf("inspect rollback: %v", err)
			}
			if runState != "active" || attemptState != "active" || currentVersion != nil {
				t.Fatalf(
					"rollback state = run:%q attempt:%q current:%v, want active/active/nil",
					runState,
					attemptState,
					currentVersion,
				)
			}
			assertCanonicalPayloadCounts(t, fixture, 0, 0, 0, 0, 0, 0, 0, 0)
		})
	}
}

func TestCanonicalCompletedRetryIsExactAndCreatesNoAttempt(t *testing.T) {
	fixture := newCanonicalRepositoryFixture(t)
	version, metadata, derivation := canonicalPreparationInput(t, fixture.now)
	state, err := fixture.repository.PrepareVersion(
		fixture.ctx,
		version,
		metadata,
		derivation,
		"personal",
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("PrepareVersion() error = %v", err)
	}
	completion := canonicalLiveCompletion(t, version, state, derivation, fixture.now.Add(time.Minute))
	if err := fixture.repository.CompleteVersion(fixture.ctx, completion); err != nil {
		t.Fatalf("first CompleteVersion() error = %v", err)
	}
	if err := fixture.repository.CompleteVersion(fixture.ctx, completion); err != nil {
		t.Fatalf("exact retry CompleteVersion() error = %v", err)
	}

	resumed, err := fixture.repository.PrepareVersion(
		fixture.ctx,
		version,
		metadata,
		derivation,
		"personal",
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("completed PrepareVersion() error = %v", err)
	}
	if resumed.Status != VersionStatusComplete ||
		resumed.RunID != state.RunID ||
		resumed.AttemptID != state.AttemptID {
		t.Fatalf("completed resume = %#v, want original completed attempt", resumed)
	}
	var attempts int
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT count(*) FROM stacks_core.extraction_attempts WHERE run_id = $1`,
		state.RunID,
	).Scan(&attempts); err != nil {
		t.Fatalf("count extraction attempts: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("completed retry attempt count = %d, want 1", attempts)
	}
	assertCanonicalPayloadCounts(t, fixture, 1, 2, 2, 1, 2, 1, 1, 6)

	mismatch := completion
	mismatch.CompletedAt = mismatch.CompletedAt.Add(time.Second)
	if err := fixture.repository.CompleteVersion(fixture.ctx, mismatch); err == nil {
		t.Fatal("semantic mismatch CompleteVersion() error = nil")
	}
	wrongOwner := completion
	wrongOwner.LeaseOwner = "other-owner"
	if err := fixture.repository.CompleteVersion(fixture.ctx, wrongOwner); err == nil {
		t.Fatal("different owner CompleteVersion() error = nil")
	}
}

func TestCanonicalCompletedRetryPreservesAdditiveIdentityAndDirectoryState(t *testing.T) {
	fixture := newCanonicalRepositoryFixture(t)
	version, metadata, derivation := canonicalPreparationInput(t, fixture.now)
	state, err := fixture.repository.PrepareVersion(
		fixture.ctx,
		version,
		metadata,
		derivation,
		"personal",
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("PrepareVersion() error = %v", err)
	}
	completion := canonicalLiveCompletion(t, version, state, derivation, fixture.now.Add(time.Minute))
	if err := fixture.repository.CompleteVersion(fixture.ctx, completion); err != nil {
		t.Fatalf("first CompleteVersion() error = %v", err)
	}

	correctedEntity, err := identity.NewEntity(identity.EntityInput{
		ID: "entity-reviewer-corrected", Kind: identity.KindPerson,
		DisplayName: "Synthetic Reviewer Correction", RecordedAt: fixture.now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("construct corrected identity: %v", err)
	}
	correctedDecision, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
		ID: "decision-reviewer-corrected", ProposalID: completion.Proposals[0].ID(),
		Outcome: identity.DecisionAccepted, EntityID: correctedEntity.ID(),
		Authority: identity.AuthorityReviewer, ReasonCode: "reviewer_corrected_identity",
		SupersedesID: completion.Decisions[0].ID(),
		RecordedAt:   fixture.now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("construct corrected decision: %v", err)
	}
	correctedAlias, err := identity.NewAliasAssertion(identity.AliasAssertionInput{
		ID: "alias-reviewer-corrected", DecisionID: correctedDecision.ID(),
		EntityID:   correctedEntity.ID(),
		Alias:      identity.Alias{Type: identity.AliasTypeName, Value: "Synthetic Corrected"},
		RecordedAt: fixture.now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("construct corrected alias: %v", err)
	}
	if err := fixture.repository.database.InTransaction(
		fixture.ctx,
		func(transaction *postgres.Transaction) error {
			if _, err := transaction.PutEntity(fixture.ctx, correctedEntity); err != nil {
				return err
			}
			return transaction.AppendResolutionDecision(
				fixture.ctx,
				correctedDecision,
				[]identity.AliasAssertion{correctedAlias},
			)
		},
	); err != nil {
		t.Fatalf("append later identity authority: %v", err)
	}
	if _, err := fixture.admin.Exec(fixture.ctx, `
		CREATE SCHEMA synthetic_optional_directory;
		CREATE TABLE synthetic_optional_directory.state (
			id text PRIMARY KEY,
			value text NOT NULL
		);
		INSERT INTO synthetic_optional_directory.state (id, value)
		VALUES ('directory-state', 'present')`); err != nil {
		t.Fatalf("seed additive directory state: %v", err)
	}

	if err := fixture.repository.CompleteVersion(fixture.ctx, completion); err != nil {
		t.Fatalf("exact retry after additive state error = %v", err)
	}
	var correctedDecisions, correctedAliases, directoryRows int
	if err := fixture.admin.QueryRow(fixture.ctx, `
		SELECT
			(SELECT count(*) FROM stacks_core.resolution_decisions WHERE id = $1),
			(SELECT count(*) FROM stacks_core.entity_alias_assertions WHERE id = $2),
			(SELECT count(*) FROM synthetic_optional_directory.state WHERE id = 'directory-state')`,
		correctedDecision.ID(),
		correctedAlias.ID(),
	).Scan(&correctedDecisions, &correctedAliases, &directoryRows); err != nil {
		t.Fatalf("inspect additive state after exact retry: %v", err)
	}
	if correctedDecisions != 1 || correctedAliases != 1 || directoryRows != 1 {
		t.Fatalf(
			"additive state counts = decisions:%d aliases:%d directory:%d, want 1/1/1",
			correctedDecisions,
			correctedAliases,
			directoryRows,
		)
	}
}

func TestCanonicalPreparationRejectsSourceMetadataMismatch(t *testing.T) {
	fixture := newCanonicalRepositoryFixture(t)
	version, metadata, derivation := canonicalPreparationInput(t, fixture.now)
	metadata.ProviderVersion = "different-provider-version"

	if _, err := fixture.repository.PrepareVersion(
		fixture.ctx,
		version,
		metadata,
		derivation,
		"personal",
		5*time.Minute,
	); err == nil {
		t.Fatal("PrepareVersion() metadata mismatch error = nil")
	}
	var versions int
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT count(*) FROM stacks_core.document_versions`,
	).Scan(&versions); err != nil {
		t.Fatalf("count document versions after rejected metadata: %v", err)
	}
	if versions != 0 {
		t.Fatalf("document version count after rejected metadata = %d, want 0", versions)
	}
}

func newCanonicalRepositoryFixture(t testing.TB) canonicalRepositoryFixture {
	t.Helper()
	isolated := postgrestest.NewDatabase(t)
	manifest, err := coremigrations.Manifest()
	if err != nil {
		t.Fatalf("coremigrations.Manifest() error = %v", err)
	}
	applicationConfig, err := pgx.ParseConfig(isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("parse application database URL: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), canonicalRepositoryTimeout)
	t.Cleanup(cancel)
	if _, err := (migration.Migrator{
		DatabaseURL:     isolated.AdminURL(),
		ApplicationRole: applicationConfig.User,
		Manifests:       []migration.Manifest{manifest},
	}).Apply(ctx); err != nil {
		t.Fatalf("install canonical core schema: %v", err)
	}
	database, err := postgres.Open(ctx, isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	t.Cleanup(database.Close)
	admin, err := pgx.Connect(ctx, isolated.AdminURL())
	if err != nil {
		t.Fatalf("connect fixture administrator: %v", err)
	}
	t.Cleanup(func() {
		_ = admin.Close(context.Background())
	})
	now := time.Date(2026, time.July, 26, 15, 0, 0, 123456000, time.UTC)
	ids := 0
	repository := NewPostgresRepository(database)
	repository.now = func() time.Time { return now }
	repository.newID = func() string {
		ids++
		return fmt.Sprintf("canonical-id-%d", ids)
	}
	autoEntity, err := identity.NewEntity(identity.EntityInput{
		ID: "entity-automatic", Kind: identity.KindPerson,
		DisplayName: "Synthetic Automatic", RecordedAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("construct automatic identity fixture: %v", err)
	}
	if err := database.InTransaction(ctx, func(transaction *postgres.Transaction) error {
		_, err := transaction.PutEntity(ctx, autoEntity)
		return err
	}); err != nil {
		t.Fatalf("persist automatic identity fixture: %v", err)
	}
	return canonicalRepositoryFixture{ctx: ctx, repository: repository, admin: admin, now: now}
}

func canonicalPreparationInput(
	t *testing.T,
	recordedAt time.Time,
) (evidence.DocumentVersion, SourceRevisionMetadata, DerivationIdentity) {
	t.Helper()
	document := syntheticDocument("canonical-postgres-document", "Leader assigns follow-up.")
	document.Version = "provider-version-1"
	document.Revision = ""
	document.ModifiedAt = recordedAt.Add(-time.Hour)
	version := documentVersion(t, document)
	derivation := testDerivationIdentity(t, version)
	return version, SourceRevisionMetadata{
		ProviderVersion: document.Version, ProviderRevision: "provider-revision-1",
	}, derivation
}

func canonicalLiveCompletion(
	t *testing.T,
	version evidence.DocumentVersion,
	state VersionState,
	derivation DerivationIdentity,
	completedAt time.Time,
) Completion {
	t.Helper()
	response := validSignalResponse(t, "2026-07-25")
	var output extract.ExtractionOutput
	if err := json.Unmarshal(response.Output, &output); err != nil {
		t.Fatalf("decode synthetic extraction output: %v", err)
	}
	service := &Service{
		Resolver: entity.Resolver{},
		DataMode: "personal",
		Now:      func() time.Time { return completedAt },
	}
	completion, err := service.completion(
		version,
		state,
		derivation,
		response,
		output,
		nil,
	)
	if err != nil {
		t.Fatalf("construct canonical live completion: %v", err)
	}
	proposalID := completion.Proposals[0].ID()
	candidate, err := identity.NewResolutionCandidate(identity.ResolutionCandidateInput{
		ID: "candidate-automatic", ProposalID: proposalID, EntityID: "entity-automatic",
		Rank: 1, Confidence: 1, ReasonCode: "unique_exact_work_email",
		Source: identity.CandidateSource{
			Kind: "synthetic_exact_email", Reference: "synthetic-candidate",
		},
		RecordedAt: completedAt,
	})
	if err != nil {
		t.Fatalf("construct automatic candidate: %v", err)
	}
	decision, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
		ID: "decision-automatic", ProposalID: proposalID,
		Outcome: identity.DecisionAccepted, EntityID: "entity-automatic",
		Authority: identity.AuthorityAutomatic, ReasonCode: "unique_exact_work_email",
		RecordedAt: completedAt,
	})
	if err != nil {
		t.Fatalf("construct automatic decision: %v", err)
	}
	reviewerDecision, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
		ID: "decision-reviewer", ProposalID: completion.Proposals[1].ID(),
		Outcome: identity.DecisionAccepted, EntityID: "entity-automatic",
		Authority: identity.AuthorityReviewer, ReasonCode: "reviewer_confirmed_identity",
		RecordedAt: completedAt,
	})
	if err != nil {
		t.Fatalf("construct reviewer decision: %v", err)
	}
	alias, err := identity.NewAliasAssertion(identity.AliasAssertionInput{
		ID: "alias-reviewer", DecisionID: reviewerDecision.ID(), EntityID: reviewerDecision.EntityID(),
		Alias:      identity.Alias{Type: identity.AliasTypeName, Value: "Synthetic Automatic"},
		RecordedAt: completedAt,
	})
	if err != nil {
		t.Fatalf("construct reviewer alias: %v", err)
	}
	identityAdmission, err := newInitialAdmission(
		completion.RunID,
		admission.TargetIdentityDecision,
		string(decision.ID()),
		completedAt,
	)
	if err != nil {
		t.Fatalf("construct identity-decision admission: %v", err)
	}
	reviewerAdmission, err := newInitialAdmission(
		completion.RunID,
		admission.TargetIdentityDecision,
		string(reviewerDecision.ID()),
		completedAt,
	)
	if err != nil {
		t.Fatalf("construct reviewer-decision admission: %v", err)
	}
	completion.Candidates = append(completion.Candidates, candidate)
	completion.Decisions = append(completion.Decisions, decision, reviewerDecision)
	completion.AliasAssertions = append(completion.AliasAssertions, alias)
	completion.AdmissionDecisions = append(
		completion.AdmissionDecisions,
		identityAdmission,
		reviewerAdmission,
	)
	return completion
}

func assertCanonicalPayloadCounts(
	t *testing.T,
	fixture canonicalRepositoryFixture,
	evidenceCount int,
	mentionCount int,
	proposalCount int,
	candidateCount int,
	decisionCount int,
	aliasCount int,
	observationCount int,
	admissionCount int,
) {
	t.Helper()
	var evidenceGot, mentionGot, proposalGot, candidateGot, decisionGot, aliasGot int
	var observationGot, admissionGot int
	if err := fixture.admin.QueryRow(fixture.ctx, `
		SELECT
			(SELECT count(*) FROM stacks_core.evidence_spans),
			(SELECT count(*) FROM stacks_core.mentions),
			(SELECT count(*) FROM stacks_core.resolution_proposals),
			(SELECT count(*) FROM stacks_core.resolution_candidates),
			(SELECT count(*) FROM stacks_core.resolution_decisions),
			(SELECT count(*) FROM stacks_core.entity_alias_assertions),
			(SELECT count(*) FROM stacks_core.observations),
			(SELECT count(*) FROM stacks_core.admission_decisions)`).Scan(
		&evidenceGot,
		&mentionGot,
		&proposalGot,
		&candidateGot,
		&decisionGot,
		&aliasGot,
		&observationGot,
		&admissionGot,
	); err != nil {
		t.Fatalf("inspect canonical payload counts: %v", err)
	}
	if evidenceGot != evidenceCount ||
		mentionGot != mentionCount ||
		proposalGot != proposalCount ||
		candidateGot != candidateCount ||
		decisionGot != decisionCount ||
		aliasGot != aliasCount ||
		observationGot != observationCount ||
		admissionGot != admissionCount {
		t.Fatalf(
			"canonical payload counts = %d/%d/%d/%d/%d/%d/%d/%d, want %d/%d/%d/%d/%d/%d/%d/%d",
			evidenceGot,
			mentionGot,
			proposalGot,
			candidateGot,
			decisionGot,
			aliasGot,
			observationGot,
			admissionGot,
			evidenceCount,
			mentionCount,
			proposalCount,
			candidateCount,
			decisionCount,
			aliasCount,
			observationCount,
			admissionCount,
		)
	}
}
