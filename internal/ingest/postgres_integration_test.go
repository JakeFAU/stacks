package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/directorymigrations"
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
	isolated   postgrestest.Database
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
	resolved, err := resolveCanonicalWriteSet(completion)
	if err != nil {
		t.Fatalf("resolve completed write set: %v", err)
	}
	wantDigest := digestCanonicalWriteSet(completion, resolved)
	var digestVersion string
	var storedDigest []byte
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT write_set_digest_version, write_set_digest
		 FROM stacks_core.extraction_runs
		 WHERE id = $1`,
		state.RunID,
	).Scan(&digestVersion, &storedDigest); err != nil {
		t.Fatalf("load completed write-set digest: %v", err)
	}
	if digestVersion != canonicalWriteSetDigestVersion ||
		!bytes.Equal(storedDigest, wantDigest[:]) {
		t.Fatalf(
			"stored write-set digest = %q/%x, want %q/%x",
			digestVersion,
			storedDigest,
			canonicalWriteSetDigestVersion,
			wantDigest,
		)
	}
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

	injected := errors.New("completed mismatch reached payload replay")
	fixture.repository.afterStage = func(completionStage) error {
		return injected
	}
	mismatch := completion
	mismatch.CompletedAt = mismatch.CompletedAt.Add(time.Second)
	if err := fixture.repository.CompleteVersion(
		fixture.ctx,
		mismatch,
	); !errors.Is(err, postgres.ErrConflict) || errors.Is(err, injected) {
		t.Fatalf("semantic mismatch CompleteVersion() error = %v, want early conflict", err)
	}
	writeSetMismatch := completion
	writeSetMismatch.Observations = append(
		[]CanonicalObservationDraft(nil),
		completion.Observations...,
	)
	writeSetMismatch.Observations[0].Predicate =
		"stacks.interaction.v1/future_responsibility/weakening"
	if err := fixture.repository.CompleteVersion(
		fixture.ctx,
		writeSetMismatch,
	); !errors.Is(err, postgres.ErrConflict) || errors.Is(err, injected) {
		t.Fatalf("write-set mismatch CompleteVersion() error = %v, want early conflict", err)
	}
	wrongOwner := completion
	wrongOwner.LeaseOwner = "other-owner"
	if err := fixture.repository.CompleteVersion(
		fixture.ctx,
		wrongOwner,
	); !errors.Is(err, postgres.ErrConflict) || errors.Is(err, injected) {
		t.Fatalf("different owner CompleteVersion() error = %v, want early conflict", err)
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
	coreManifest, err := coremigrations.Manifest()
	if err != nil {
		t.Fatalf("coremigrations.Manifest() error = %v", err)
	}
	directoryManifest, err := directorymigrations.Manifest()
	if err != nil {
		t.Fatalf("directorymigrations.Manifest() error = %v", err)
	}
	applicationConfig, err := pgx.ParseConfig(fixture.isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("parse application database URL: %v", err)
	}
	if _, err := (migration.Migrator{
		DatabaseURL:     fixture.isolated.AdminURL(),
		ApplicationRole: applicationConfig.User,
		Manifests:       []migration.Manifest{coreManifest, directoryManifest},
	}).Apply(fixture.ctx); err != nil {
		t.Fatalf("install optional directory schema: %v", err)
	}
	mention := completion.Mentions[0]
	profile := postgres.DirectoryProfile{
		Provider:    "synthetic_directory",
		SubjectID:   "profiles/additive-state",
		Source:      postgres.DirectorySourceDomainProfile,
		DisplayName: "Synthetic Directory State",
		Emails: []postgres.DirectoryEmail{{
			Value:   "additive.state@example.test",
			Primary: true,
		}},
		ObservedAt: fixture.now.Add(2 * time.Minute),
	}
	if _, err := (postgres.DirectoryStore{
		Database: fixture.repository.database,
	}).Persist(fixture.ctx, postgres.DirectoryPersistInput{
		Mention: postgres.DirectoryPendingMention{
			MentionID:      string(completion.Proposals[0].MentionID()),
			ProposalID:     string(completion.Proposals[0].ID()),
			Surface:        mention.Surface,
			NormalizedName: mention.NormalizedName,
		},
		Query: postgres.DirectoryQuery{
			Kind:          postgres.DirectoryQueryName,
			Name:          mention.NormalizedName,
			EmailEvidence: postgres.DirectoryEmailEvidenceNone,
		},
		Lookup: postgres.DirectoryLookupResult{
			Outcome:  postgres.DirectoryOutcomeReview,
			Profiles: []postgres.DirectoryProfile{profile},
		},
		Evaluation: postgres.DirectoryEvaluation{
			Outcome:    postgres.DirectoryOutcomeReview,
			Candidates: []postgres.DirectoryProfile{profile},
		},
		AttemptCount: 1,
		RecordedAt:   fixture.now.Add(3 * time.Minute),
	}); err != nil {
		t.Fatalf("seed additive directory state: %v", err)
	}

	if err := fixture.repository.CompleteVersion(fixture.ctx, completion); err != nil {
		t.Fatalf("exact retry after additive state error = %v", err)
	}
	var correctedDecisions, correctedAliases int
	var directoryProfiles, directorySnapshots, directoryAttempts, directoryLinks int
	if err := fixture.admin.QueryRow(fixture.ctx, `
		SELECT
			(SELECT count(*) FROM stacks_core.resolution_decisions WHERE id = $1),
			(SELECT count(*) FROM stacks_core.entity_alias_assertions WHERE id = $2),
			(SELECT count(*) FROM stacks_directory.profiles),
			(SELECT count(*) FROM stacks_directory.snapshots),
			(SELECT count(*) FROM stacks_directory.lookup_attempts),
			(SELECT count(*) FROM stacks_directory.entity_links)`,
		correctedDecision.ID(),
		correctedAlias.ID(),
	).Scan(
		&correctedDecisions,
		&correctedAliases,
		&directoryProfiles,
		&directorySnapshots,
		&directoryAttempts,
		&directoryLinks,
	); err != nil {
		t.Fatalf("inspect additive state after exact retry: %v", err)
	}
	if correctedDecisions != 1 ||
		correctedAliases != 1 ||
		directoryProfiles != 1 ||
		directorySnapshots != 1 ||
		directoryAttempts != 1 ||
		directoryLinks != 1 {
		t.Fatalf(
			"additive state counts = decisions:%d aliases:%d directory:%d/%d/%d/%d, want 1/1/1/1/1/1",
			correctedDecisions,
			correctedAliases,
			directoryProfiles,
			directorySnapshots,
			directoryAttempts,
			directoryLinks,
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

func TestCanonicalCompletionRejectsRunVersionMismatchBeforeWrites(t *testing.T) {
	testCases := []struct {
		name      string
		completed bool
	}{
		{name: "active"},
		{name: "completed", completed: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
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
			completion := canonicalLiveCompletion(
				t,
				version,
				state,
				derivation,
				fixture.now.Add(time.Minute),
			)
			if testCase.completed {
				if err := fixture.repository.CompleteVersion(fixture.ctx, completion); err != nil {
					t.Fatalf("initial CompleteVersion() error = %v", err)
				}
			}
			successor := putCanonicalSuccessorVersion(t, fixture)
			completion.VersionID = successor.VersionID
			before := canonicalDurableSnapshot(t, fixture, state.RunID)
			injected := errors.New("completion reached a payload stage")
			fixture.repository.afterStage = func(completionStage) error {
				return injected
			}

			err = fixture.repository.CompleteVersion(fixture.ctx, completion)
			if !errors.Is(err, postgres.ErrConflict) {
				t.Fatalf("CompleteVersion() error = %v, want PostgreSQL conflict", err)
			}
			if errors.Is(err, injected) {
				t.Fatalf("CompleteVersion() reached a payload stage: %v", err)
			}
			after := canonicalDurableSnapshot(t, fixture, state.RunID)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("version mismatch changed durable state:\nbefore=%#v\nafter=%#v", before, after)
			}
		})
	}
}

func TestCanonicalCompletedRetryAndResumeAreReadOnlyWithNewerCurrentVersion(t *testing.T) {
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
		t.Fatalf("initial CompleteVersion() error = %v", err)
	}
	successor := putCanonicalSuccessorVersion(t, fixture)
	setCanonicalCurrentVersion(t, fixture, successor)
	before := canonicalDurableSnapshot(t, fixture, state.RunID)
	injected := errors.New("completed retry replayed payload")
	fixture.repository.afterStage = func(completionStage) error {
		return injected
	}

	if err := fixture.repository.CompleteVersion(fixture.ctx, completion); err != nil {
		t.Fatalf("exact completed retry error = %v", err)
	}
	afterRetry := canonicalDurableSnapshot(t, fixture, state.RunID)
	if !reflect.DeepEqual(afterRetry, before) {
		t.Fatalf("exact completed retry changed durable state:\nbefore=%#v\nafter=%#v", before, afterRetry)
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
	afterResume := canonicalDurableSnapshot(t, fixture, state.RunID)
	if !reflect.DeepEqual(afterResume, before) {
		t.Fatalf("completed resume changed durable state:\nbefore=%#v\nafter=%#v", before, afterResume)
	}
}

func TestCanonicalCompletedDerivationReusesAcrossDisclosureModes(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
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
			t.Fatalf("personal PrepareVersion() error = %v", err)
		}
		completion := canonicalLiveCompletion(t, version, state, derivation, fixture.now.Add(time.Minute))
		if err := fixture.repository.CompleteVersion(fixture.ctx, completion); err != nil {
			t.Fatalf("CompleteVersion() error = %v", err)
		}
		before := canonicalDurableSnapshot(t, fixture, state.RunID)

		reused, err := fixture.repository.PrepareVersion(
			fixture.ctx,
			version,
			metadata,
			derivation,
			"restricted",
			5*time.Minute,
		)
		if err != nil {
			t.Fatalf("restricted completed PrepareVersion() error = %v", err)
		}
		if reused.Status != VersionStatusComplete ||
			reused.RunID != state.RunID ||
			reused.AttemptID != state.AttemptID {
			t.Fatalf("cross-mode completed reuse = %#v, want original completed attempt", reused)
		}
		after := canonicalDurableSnapshot(t, fixture, state.RunID)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("cross-mode completed reuse changed durable state:\nbefore=%#v\nafter=%#v", before, after)
		}
		var storedMode string
		if err := fixture.admin.QueryRow(
			fixture.ctx,
			`SELECT data_mode FROM stacks_core.extraction_runs WHERE id = $1`,
			state.RunID,
		).Scan(&storedMode); err != nil {
			t.Fatalf("load completed run data mode: %v", err)
		}
		if storedMode != "personal" {
			t.Fatalf("completed run data mode = %q, want original personal", storedMode)
		}
	})

	t.Run("active remains immutable", func(t *testing.T) {
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
			t.Fatalf("personal PrepareVersion() error = %v", err)
		}
		before := canonicalDurableSnapshot(t, fixture, state.RunID)

		if _, err := fixture.repository.PrepareVersion(
			fixture.ctx,
			version,
			metadata,
			derivation,
			"restricted",
			5*time.Minute,
		); !errors.Is(err, postgres.ErrConflict) {
			t.Fatalf("restricted active PrepareVersion() error = %v, want conflict", err)
		}
		after := canonicalDurableSnapshot(t, fixture, state.RunID)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("active cross-mode rejection changed durable state:\nbefore=%#v\nafter=%#v", before, after)
		}
	})

	t.Run("new invocation records requested mode", func(t *testing.T) {
		fixture := newCanonicalRepositoryFixture(t)
		version, metadata, derivation := canonicalPreparationInput(t, fixture.now)
		state, err := fixture.repository.PrepareVersion(
			fixture.ctx,
			version,
			metadata,
			derivation,
			"restricted",
			5*time.Minute,
		)
		if err != nil {
			t.Fatalf("restricted PrepareVersion() error = %v", err)
		}
		if state.Status != VersionStatusPending {
			t.Fatalf("restricted new invocation status = %q, want pending", state.Status)
		}
		var storedMode string
		if err := fixture.admin.QueryRow(
			fixture.ctx,
			`SELECT data_mode FROM stacks_core.extraction_runs WHERE id = $1`,
			state.RunID,
		).Scan(&storedMode); err != nil {
			t.Fatalf("load new run data mode: %v", err)
		}
		if storedMode != "restricted" {
			t.Fatalf("new run data mode = %q, want restricted", storedMode)
		}
	})
}

func TestCanonicalInvalidGraphWriteSetsLeaveNoRows(t *testing.T) {
	testCases := canonicalDuplicateGraphCases()
	testCases = append(testCases, canonicalGraphMutation{
		name: "orphan alias",
		mutate: func(completion *Completion) {
			appendOrphanAlias(t, completion)
		},
	})
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
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
			completion := canonicalLiveCompletion(
				t,
				version,
				state,
				derivation,
				fixture.now.Add(time.Minute),
			)
			testCase.mutate(&completion)

			err = fixture.repository.CompleteVersion(fixture.ctx, completion)
			if err == nil {
				t.Fatal("CompleteVersion() error = nil, want invalid graph rejection")
			}
			assertCanonicalPayloadCounts(t, fixture, 0, 0, 0, 0, 0, 0, 0, 0)
			snapshot := canonicalDurableSnapshot(t, fixture, state.RunID)
			if snapshot.runState != "active" ||
				snapshot.attemptState != "active" ||
				snapshot.currentVersionID != "" {
				t.Fatalf("invalid graph lifecycle snapshot = %#v, want active/active/no current", snapshot)
			}
		})
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
	return canonicalRepositoryFixture{
		ctx:        ctx,
		repository: repository,
		admin:      admin,
		isolated:   isolated,
		now:        now,
	}
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

func putCanonicalSuccessorVersion(
	t *testing.T,
	fixture canonicalRepositoryFixture,
) postgres.DocumentVersionRef {
	t.Helper()
	document := syntheticDocument(
		"canonical-postgres-document",
		"Leader assigns a newer follow-up.",
	)
	document.Version = "provider-version-2"
	document.Revision = "provider-revision-2"
	document.ModifiedAt = fixture.now.Add(-30 * time.Minute)
	result, err := fixture.repository.database.PutDocumentVersion(
		fixture.ctx,
		documentVersion(t, document),
	)
	if err != nil {
		t.Fatalf("persist canonical successor version: %v", err)
	}
	return result.Ref
}

func setCanonicalCurrentVersion(
	t *testing.T,
	fixture canonicalRepositoryFixture,
	ref postgres.DocumentVersionRef,
) {
	t.Helper()
	if err := fixture.repository.database.InTransaction(
		fixture.ctx,
		func(transaction *postgres.Transaction) error {
			return transaction.SetCurrentDocumentVersion(
				fixture.ctx,
				ref.SourceDocumentID,
				ref.VersionID,
			)
		},
	); err != nil {
		t.Fatalf("set canonical current version: %v", err)
	}
}

type canonicalDurableState struct {
	runState, attemptState, currentVersionID string
	runXmin, attemptXmin, sourceXmin         string
	attemptCount                             int
	payloadXmins                             map[string]string
}

func canonicalDurableSnapshot(
	t *testing.T,
	fixture canonicalRepositoryFixture,
	runID string,
) canonicalDurableState {
	t.Helper()
	var snapshot canonicalDurableState
	var currentVersionID *string
	if err := fixture.admin.QueryRow(fixture.ctx, `
		SELECT
			run.state,
			run.xmin::text,
			attempt.state,
			attempt.xmin::text,
			source.current_version_id,
			source.xmin::text,
			(
				SELECT count(*)
				FROM stacks_core.extraction_attempts AS counted
				WHERE counted.run_id = run.id
			)
		FROM stacks_core.extraction_runs AS run
		JOIN stacks_core.document_versions AS version
		  ON version.id = run.document_version_id
		JOIN stacks_core.source_documents AS source
		  ON source.id = version.source_document_id
		JOIN LATERAL (
			SELECT state, xmin
			FROM stacks_core.extraction_attempts
			WHERE run_id = run.id
			ORDER BY attempt_number DESC
			LIMIT 1
		) AS attempt ON true
		WHERE run.id = $1`,
		runID,
	).Scan(
		&snapshot.runState,
		&snapshot.runXmin,
		&snapshot.attemptState,
		&snapshot.attemptXmin,
		&currentVersionID,
		&snapshot.sourceXmin,
		&snapshot.attemptCount,
	); err != nil {
		t.Fatalf("snapshot canonical lifecycle: %v", err)
	}
	if currentVersionID != nil {
		snapshot.currentVersionID = *currentVersionID
	}
	queries := map[string]string{
		"evidence": `
			SELECT coalesce(string_agg(xmin::text, ',' ORDER BY id), '')
			FROM stacks_core.evidence_spans`,
		"mentions": `
			SELECT coalesce(string_agg(xmin::text, ',' ORDER BY id), '')
			FROM stacks_core.mentions`,
		"proposals": `
			SELECT coalesce(string_agg(xmin::text, ',' ORDER BY id), '')
			FROM stacks_core.resolution_proposals`,
		"proposal_evidence": `
			SELECT coalesce(
				string_agg(
					xmin::text,
					','
					ORDER BY proposal_id, evidence_id
				),
				''
			)
			FROM stacks_core.resolution_proposal_evidence`,
		"candidates": `
			SELECT coalesce(string_agg(xmin::text, ',' ORDER BY id), '')
			FROM stacks_core.resolution_candidates`,
		"decisions": `
			SELECT coalesce(string_agg(xmin::text, ',' ORDER BY id), '')
			FROM stacks_core.resolution_decisions`,
		"aliases": `
			SELECT coalesce(string_agg(xmin::text, ',' ORDER BY id), '')
			FROM stacks_core.entity_alias_assertions`,
		"observations": `
			SELECT coalesce(string_agg(xmin::text, ',' ORDER BY id), '')
			FROM stacks_core.observations`,
		"observation_evidence": `
			SELECT coalesce(
				string_agg(
					xmin::text,
					','
					ORDER BY observation_id, evidence_id, role
				),
				''
			)
			FROM stacks_core.observation_evidence`,
		"admission_targets": `
			SELECT coalesce(
				string_agg(
					xmin::text,
					','
					ORDER BY target_kind, target_id
				),
				''
			)
			FROM stacks_core.admission_targets`,
		"admission_decisions": `
			SELECT coalesce(string_agg(xmin::text, ',' ORDER BY id), '')
			FROM stacks_core.admission_decisions`,
	}
	snapshot.payloadXmins = make(map[string]string, len(queries))
	for name, query := range queries {
		var value string
		if err := fixture.admin.QueryRow(fixture.ctx, query).Scan(&value); err != nil {
			t.Fatalf("snapshot canonical %s xmin: %v", name, err)
		}
		snapshot.payloadXmins[name] = value
	}
	return snapshot
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
