package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/jackc/pgx/v5"
)

const (
	testIdentityAuthorityLockNamespace  = "github.com/JakeFAU/stacks/postgres-identity-authority/v1"
	testAdmissionAuthorityLockNamespace = "github.com/JakeFAU/stacks/postgres-admission-authority/v1"
)

func TestCanonicalReviewerCreatePersonIsAtomicIdempotentAndCited(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	mention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"review-create",
		"review.create@example.test",
	)
	if _, err := fixture.admin.Exec(fixture.ctx, `
		INSERT INTO stacks_core.evidence_spans (
			id, document_version_id, section_id, digest_version, digest,
			start_offset, end_offset, quote, recorded_at
		)
		VALUES (
			'evidence:review-create:second', 'version:review-create',
			'section:review-create', 'synthetic.evidence.v1',
			decode(repeat('23', 32), 'hex'), 0, 16,
			'Synthetic corroboration', $1
		)`,
		canonicalDirectoryRecordedAt,
	); err != nil {
		t.Fatalf("seed second reviewer citation: %v", err)
	}
	if _, err := fixture.admin.Exec(fixture.ctx, `
		INSERT INTO stacks_core.resolution_proposal_evidence (
			proposal_id, evidence_id, evidence_order
		)
		VALUES (
			'proposal:review-create', 'evidence:review-create:second', 1
		)`,
	); err != nil {
		t.Fatalf("link second reviewer citation: %v", err)
	}
	store := postgres.ReviewerStore{Database: fixture.database}
	proposals, err := store.ListProposals(fixture.ctx)
	if err != nil {
		t.Fatalf("ListProposals() error = %v", err)
	}
	if len(proposals) != 1 ||
		len(proposals[0].Evidence) != 2 ||
		proposals[0].Evidence[0].ID != "evidence:review-create" ||
		proposals[0].Evidence[0].Quote != "Synthetic Person" ||
		proposals[0].Evidence[1].ID != "evidence:review-create:second" ||
		proposals[0].Evidence[1].Quote != "Synthetic corroboration" ||
		proposals[0].Mention.ID() != identity.MentionID(mention.MentionID) {
		t.Fatalf("review proposal = %#v, want all ordered exact citations", proposals)
	}

	recordedAt := canonicalDirectoryRecordedAt.Add(2 * time.Hour)
	person := canonicalEntity(t, "reviewer-person:opaque/1", "Reviewer Person")
	decision := canonicalResolutionDecision(
		t,
		"reviewer-decision:create",
		identity.ProposalID(mention.ProposalID),
		identity.DecisionAccepted,
		person.ID(),
		identity.AuthorityReviewer,
		"",
		recordedAt,
	)
	alias := canonicalAliasAssertion(
		t,
		"reviewer-alias:create",
		decision.ID(),
		person.ID(),
		identity.Alias{Type: identity.AliasTypeName, Value: mention.NormalizedName},
		recordedAt,
	)
	command := postgres.ReviewerCreatePersonCommand{
		Entity: person, Decision: decision, Aliases: []identity.AliasAssertion{alias},
	}
	first, err := store.CreatePerson(fixture.ctx, command)
	if err != nil {
		t.Fatalf("CreatePerson() error = %v", err)
	}
	second, err := store.CreatePerson(fixture.ctx, command)
	if err != nil {
		t.Fatalf("CreatePerson() exact retry error = %v", err)
	}
	if first.ID() != decision.ID() || second.ID() != decision.ID() {
		t.Fatalf("create decision IDs = %q/%q, want exact retry %q", first.ID(), second.ID(), decision.ID())
	}
	changedPerson, err := identity.NewEntity(identity.EntityInput{
		ID: person.ID(), Kind: person.Kind(), DisplayName: "Changed Reviewer Person",
		RecordedAt: person.RecordedAt(),
	})
	if err != nil {
		t.Fatalf("construct changed retry entity: %v", err)
	}
	changedEntityRetry := command
	changedEntityRetry.Entity = changedPerson
	if _, err := store.CreatePerson(
		fixture.ctx,
		changedEntityRetry,
	); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("CreatePerson(changed entity retry) error = %v, want ErrConflict", err)
	}
	addedEvidenceInput := canonicalDirectoryPersistInput()
	addedEvidenceInput.Mention = mention
	addedEvidenceInput.Query.Email = mention.ProposedEmail
	addedEvidenceInput.Query.EmailEvidence = postgres.DirectoryEmailEvidenceReviewerSupplied
	addedEvidenceInput.Lookup.Profiles[0].Emails[0].Value = mention.ProposedEmail
	addedEvidenceInput.Evaluation.AcceptedEmail = mention.ProposedEmail
	addedEvidenceInput.Evaluation.Profile = &addedEvidenceInput.Lookup.Profiles[0]
	addedEvidenceInput.RecordedAt = decision.RecordedAt()
	addedEvidenceRetry := command
	addedEvidenceRetry.DirectoryEvidence = &postgres.ReviewerDirectoryEvidenceCommand{
		Mention:      addedEvidenceInput.Mention,
		Query:        addedEvidenceInput.Query,
		Lookup:       addedEvidenceInput.Lookup,
		Evaluation:   addedEvidenceInput.Evaluation,
		AttemptCount: addedEvidenceInput.AttemptCount,
		RecordedAt:   addedEvidenceInput.RecordedAt,
		RetryAfter:   addedEvidenceInput.RetryAfter,
	}
	directoryStore := postgres.ReviewerStore{
		Database: fixture.database, IncludeDirectory: true,
	}
	if _, err := directoryStore.CreatePerson(
		fixture.ctx,
		addedEvidenceRetry,
	); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("CreatePerson(added directory evidence retry) error = %v, want ErrConflict", err)
	}
	entities, err := store.ListEntities(fixture.ctx)
	if err != nil {
		t.Fatalf("ListEntities() error = %v", err)
	}
	var created *postgres.ReviewerEntityRecord
	for index := range entities {
		if entities[index].Entity.ID() == person.ID() {
			created = &entities[index]
			break
		}
	}
	if created == nil ||
		len(created.Aliases) != 1 ||
		len(created.Evidence) != 2 ||
		created.Evidence[0].ID != "evidence:review-create" ||
		created.Evidence[0].Quote != "Synthetic Person" ||
		created.Evidence[1].ID != "evidence:review-create:second" ||
		created.Evidence[1].Quote != "Synthetic corroboration" {
		t.Fatalf("created reviewer projection = %#v, want current alias and all exact evidence", created)
	}

	conflictingPerson := canonicalEntity(t, "reviewer-person:rolled-back", "Rolled Back")
	conflictingDecision := canonicalResolutionDecision(
		t,
		"reviewer-decision:second-initial",
		identity.ProposalID(mention.ProposalID),
		identity.DecisionAccepted,
		conflictingPerson.ID(),
		identity.AuthorityReviewer,
		"",
		recordedAt.Add(time.Microsecond),
	)
	if _, err := store.CreatePerson(fixture.ctx, postgres.ReviewerCreatePersonCommand{
		Entity: conflictingPerson, Decision: conflictingDecision,
	}); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("second initial CreatePerson() error = %v, want ErrConflict", err)
	}
	if _, err := fixture.database.LoadEntity(fixture.ctx, conflictingPerson.ID()); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("rolled-back entity LoadEntity() error = %v, want ErrNotFound", err)
	}

	canceled, cancel := context.WithCancel(fixture.ctx)
	cancel()
	canceledPerson := canonicalEntity(t, "reviewer-person:canceled", "Canceled")
	canceledDecision := canonicalResolutionDecision(
		t,
		"reviewer-decision:canceled",
		"proposal:canceled",
		identity.DecisionAccepted,
		canceledPerson.ID(),
		identity.AuthorityReviewer,
		"",
		recordedAt,
	)
	if _, err := store.CreatePerson(canceled, postgres.ReviewerCreatePersonCommand{
		Entity: canceledPerson, Decision: canceledDecision,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled CreatePerson() error = %v, want context.Canceled", err)
	}
}

func TestCanonicalReviewerCreatePersonExactRetryNeedsNoDirectorySchema(t *testing.T) {
	coreFixture := newDocumentRepositoryFixture(t)
	fixture := canonicalDirectoryFixture(coreFixture)
	mention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"review-create-core-only",
		"review.core@example.test",
	)
	recordedAt := canonicalDirectoryRecordedAt.Add(2 * time.Hour)
	person := canonicalEntity(t, "reviewer-person:core-only", "Core Person")
	command := postgres.ReviewerCreatePersonCommand{
		Entity: person,
		Decision: canonicalResolutionDecision(
			t,
			"reviewer-decision:core-only",
			identity.ProposalID(mention.ProposalID),
			identity.DecisionAccepted,
			person.ID(),
			identity.AuthorityReviewer,
			"",
			recordedAt,
		),
	}
	store := postgres.ReviewerStore{Database: fixture.database}
	if _, err := store.CreatePerson(fixture.ctx, command); err != nil {
		t.Fatalf("CreatePerson(core-only) error = %v", err)
	}
	if _, err := store.CreatePerson(fixture.ctx, command); err != nil {
		t.Fatalf("CreatePerson(core-only exact retry) error = %v", err)
	}
}

func TestCanonicalReviewerCoreExactRetryIsReadOnlyAfterEligibilityChanges(t *testing.T) {
	tests := []struct {
		name       string
		suffix     string
		invalidate func(
			testing.TB,
			canonicalDirectoryFixture,
			postgres.DirectoryPendingMention,
		)
	}{
		{
			name:   "run quarantine",
			suffix: "review-retry-run-quarantine",
			invalidate: func(
				t testing.TB,
				fixture canonicalDirectoryFixture,
				_ postgres.DirectoryPendingMention,
			) {
				t.Helper()
				targetID := "run:review-retry-run-quarantine"
				decision := canonicalAdmissionDecision(
					t,
					"admission-quarantine:"+targetID,
					admission.TargetExtractionRun,
					targetID,
					admission.Quarantined,
					admission.AuthorityReviewer,
					"admission-run:"+targetID,
					canonicalDirectoryRecordedAt.Add(3*time.Hour),
				)
				if err := fixture.database.InTransaction(
					fixture.ctx,
					func(transaction *postgres.Transaction) error {
						return transaction.AppendAdmissionDecision(fixture.ctx, decision)
					},
				); err != nil {
					t.Fatalf("quarantine extraction run: %v", err)
				}
			},
		},
		{
			name:   "mention quarantine",
			suffix: "review-retry-mention-quarantine",
			invalidate: func(
				t testing.TB,
				fixture canonicalDirectoryFixture,
				mention postgres.DirectoryPendingMention,
			) {
				t.Helper()
				decision := canonicalAdmissionDecision(
					t,
					"admission-quarantine:"+mention.MentionID,
					admission.TargetMention,
					mention.MentionID,
					admission.Quarantined,
					admission.AuthorityReviewer,
					"admission-mention:"+mention.MentionID,
					canonicalDirectoryRecordedAt.Add(3*time.Hour),
				)
				if err := fixture.database.InTransaction(
					fixture.ctx,
					func(transaction *postgres.Transaction) error {
						return transaction.AppendAdmissionDecision(fixture.ctx, decision)
					},
				); err != nil {
					t.Fatalf("quarantine mention: %v", err)
				}
			},
		},
		{
			name:   "current version replacement",
			suffix: "review-retry-current-version",
			invalidate: func(
				t testing.TB,
				fixture canonicalDirectoryFixture,
				_ postgres.DirectoryPendingMention,
			) {
				t.Helper()
				insertCanonicalReplacementVersion(
					t,
					fixture,
					"review-retry-current-version",
				)
				if _, err := fixture.admin.Exec(
					fixture.ctx,
					`UPDATE stacks_core.source_documents
					 SET current_version_id = $1
					 WHERE id = $2`,
					"version:review-retry-current-version:replacement",
					"source:review-retry-current-version",
				); err != nil {
					t.Fatalf("replace current document version: %v", err)
				}
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			coreFixture := newDocumentRepositoryFixture(t)
			fixture := canonicalDirectoryFixture(coreFixture)
			mention := seedCanonicalDirectoryMention(
				t,
				fixture,
				testCase.suffix,
				testCase.suffix+"@example.test",
			)
			recordedAt := canonicalDirectoryRecordedAt.Add(2 * time.Hour)
			person := canonicalEntity(
				t,
				identity.EntityID("reviewer-person:"+testCase.suffix),
				"Core Retry Person",
			)
			decision := canonicalResolutionDecision(
				t,
				identity.DecisionID("reviewer-decision:"+testCase.suffix),
				identity.ProposalID(mention.ProposalID),
				identity.DecisionAccepted,
				person.ID(),
				identity.AuthorityReviewer,
				"",
				recordedAt,
			)
			alias := canonicalAliasAssertion(
				t,
				identity.AliasAssertionID("reviewer-alias:"+testCase.suffix),
				decision.ID(),
				person.ID(),
				identity.Alias{
					Type: identity.AliasTypeName, Value: mention.NormalizedName,
				},
				recordedAt,
			)
			command := postgres.ReviewerCreatePersonCommand{
				Entity: person, Decision: decision,
				Aliases: []identity.AliasAssertion{alias},
			}
			store := postgres.ReviewerStore{Database: fixture.database}
			if _, err := store.CreatePerson(fixture.ctx, command); err != nil {
				t.Fatalf("CreatePerson(initial) error = %v", err)
			}
			before := loadCanonicalReviewerWriteState(
				t,
				fixture,
				person.ID(),
				decision.ID(),
				alias.ID(),
			)

			testCase.invalidate(t, fixture, mention)
			applicationConfig, err := pgx.ParseConfig(coreFixture.applicationURL)
			if err != nil {
				t.Fatalf("parse application URL: %v", err)
			}
			if _, err := fixture.admin.Exec(
				fixture.ctx,
				`REVOKE INSERT ON
				 stacks_core.entities,
				 stacks_core.resolution_decisions,
				 stacks_core.entity_alias_assertions
				 FROM `+pgx.Identifier{applicationConfig.User}.Sanitize(),
			); err != nil {
				t.Fatalf("revoke reviewer insert privileges: %v", err)
			}

			if _, err := store.CreatePerson(fixture.ctx, command); err != nil {
				t.Fatalf("CreatePerson(exact retry after invalidation) error = %v", err)
			}
			after := loadCanonicalReviewerWriteState(
				t,
				fixture,
				person.ID(),
				decision.ID(),
				alias.ID(),
			)
			if after != before {
				t.Fatalf("reviewer write state changed: before=%#v after=%#v", before, after)
			}

			changedPerson, err := identity.NewEntity(identity.EntityInput{
				ID: person.ID(), Kind: person.Kind(), DisplayName: "Changed Core Retry Person",
				RecordedAt: person.RecordedAt(),
			})
			if err != nil {
				t.Fatalf("construct changed entity retry: %v", err)
			}
			changedEntity := command
			changedEntity.Entity = changedPerson
			if _, err := store.CreatePerson(
				fixture.ctx,
				changedEntity,
			); !errors.Is(err, postgres.ErrConflict) {
				t.Fatalf("CreatePerson(changed entity) error = %v, want ErrConflict", err)
			}
			changedAlias := canonicalAliasAssertion(
				t,
				alias.ID(),
				decision.ID(),
				person.ID(),
				identity.Alias{
					Type: identity.AliasTypeName, Value: "changed alias",
				},
				recordedAt,
			)
			changedAliases := command
			changedAliases.Aliases = []identity.AliasAssertion{changedAlias}
			if _, err := store.CreatePerson(
				fixture.ctx,
				changedAliases,
			); !errors.Is(err, postgres.ErrConflict) {
				t.Fatalf("CreatePerson(changed aliases) error = %v, want ErrConflict", err)
			}
		})
	}
}

type canonicalReviewerWriteState struct {
	entityXID   string
	decisionXID string
	aliasXID    string
	entities    int
	decisions   int
	aliases     int
}

func loadCanonicalReviewerWriteState(
	t testing.TB,
	fixture canonicalDirectoryFixture,
	entityID identity.EntityID,
	decisionID identity.DecisionID,
	aliasID identity.AliasAssertionID,
) canonicalReviewerWriteState {
	t.Helper()
	var state canonicalReviewerWriteState
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT
			(SELECT xmin::text FROM stacks_core.entities WHERE id = $1),
			(SELECT xmin::text FROM stacks_core.resolution_decisions WHERE id = $2),
			(SELECT xmin::text FROM stacks_core.entity_alias_assertions WHERE id = $3),
			(SELECT count(*) FROM stacks_core.entities WHERE id = $1),
			(SELECT count(*) FROM stacks_core.resolution_decisions WHERE id = $2),
			(SELECT count(*) FROM stacks_core.entity_alias_assertions WHERE id = $3)`,
		entityID,
		decisionID,
		aliasID,
	).Scan(
		&state.entityXID,
		&state.decisionXID,
		&state.aliasXID,
		&state.entities,
		&state.decisions,
		&state.aliases,
	); err != nil {
		t.Fatalf("load reviewer write state: %v", err)
	}
	return state
}

func TestCanonicalReviewerDirectoryExactRetryIsReadOnlyAfterQuarantine(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	mention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"review-directory-retry-quarantine",
		"review.directory.retry@example.test",
	)
	recordedAt := canonicalDirectoryRecordedAt.Add(2 * time.Hour)
	person := canonicalEntity(
		t,
		"reviewer-person:directory-retry-quarantine",
		"Directory Retry Person",
	)
	decision := canonicalResolutionDecision(
		t,
		"reviewer-decision:directory-retry-quarantine",
		identity.ProposalID(mention.ProposalID),
		identity.DecisionAccepted,
		person.ID(),
		identity.AuthorityReviewer,
		"",
		recordedAt,
	)
	alias := canonicalAliasAssertion(
		t,
		"reviewer-alias:directory-retry-quarantine",
		decision.ID(),
		person.ID(),
		identity.Alias{Type: identity.AliasTypeName, Value: mention.NormalizedName},
		recordedAt,
	)
	input := canonicalDirectoryPersistInput()
	input.Mention = mention
	input.Query.Email = mention.ProposedEmail
	input.Query.EmailEvidence = postgres.DirectoryEmailEvidenceReviewerSupplied
	input.Lookup.Profiles[0].Emails[0].Value = mention.ProposedEmail
	input.Evaluation.AcceptedEmail = mention.ProposedEmail
	input.Evaluation.Profile = &input.Lookup.Profiles[0]
	input.RecordedAt = recordedAt
	command := postgres.ReviewerCreatePersonCommand{
		Entity: person, Decision: decision, Aliases: []identity.AliasAssertion{alias},
		DirectoryEvidence: &postgres.ReviewerDirectoryEvidenceCommand{
			Mention: input.Mention, Query: input.Query, Lookup: input.Lookup,
			Evaluation: input.Evaluation, AttemptCount: input.AttemptCount,
			RecordedAt: input.RecordedAt, RetryAfter: input.RetryAfter,
		},
	}
	store := postgres.ReviewerStore{
		Database:         fixture.database,
		IncludeDirectory: true,
	}
	if _, err := store.CreatePerson(fixture.ctx, command); err != nil {
		t.Fatalf("CreatePerson(initial directory evidence) error = %v", err)
	}
	quarantine := canonicalAdmissionDecision(
		t,
		"admission-quarantine:"+mention.MentionID,
		admission.TargetMention,
		mention.MentionID,
		admission.Quarantined,
		admission.AuthorityReviewer,
		"admission-mention:"+mention.MentionID,
		recordedAt.Add(time.Hour),
	)
	if err := fixture.database.InTransaction(
		fixture.ctx,
		func(transaction *postgres.Transaction) error {
			return transaction.AppendAdmissionDecision(fixture.ctx, quarantine)
		},
	); err != nil {
		t.Fatalf("quarantine mention: %v", err)
	}
	applicationConfig, err := pgx.ParseConfig(fixture.applicationURL)
	if err != nil {
		t.Fatalf("parse application URL: %v", err)
	}
	if _, err := fixture.admin.Exec(
		fixture.ctx,
		`REVOKE INSERT ON
		 stacks_core.entities,
		 stacks_core.resolution_decisions,
		 stacks_core.entity_alias_assertions,
		 stacks_directory.profiles,
		 stacks_directory.snapshots,
		 stacks_directory.profile_emails,
		 stacks_directory.lookup_attempts,
		 stacks_directory.entity_links
		 FROM `+pgx.Identifier{applicationConfig.User}.Sanitize(),
	); err != nil {
		t.Fatalf("revoke reviewer insert privileges: %v", err)
	}
	var attemptsBefore, proofsBefore int
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT
			(SELECT count(*) FROM stacks_directory.lookup_attempts WHERE proposal_id = $1),
			(SELECT count(*) FROM stacks_directory.entity_links
			 WHERE proposal_id = $1 AND decision_id = $2)`,
		mention.ProposalID,
		decision.ID(),
	).Scan(&attemptsBefore, &proofsBefore); err != nil {
		t.Fatalf("load directory retry state: %v", err)
	}

	if _, err := store.CreatePerson(fixture.ctx, command); err != nil {
		t.Fatalf("CreatePerson(directory exact retry after quarantine) error = %v", err)
	}
	var attemptsAfter, proofsAfter int
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT
			(SELECT count(*) FROM stacks_directory.lookup_attempts WHERE proposal_id = $1),
			(SELECT count(*) FROM stacks_directory.entity_links
			 WHERE proposal_id = $1 AND decision_id = $2)`,
		mention.ProposalID,
		decision.ID(),
	).Scan(&attemptsAfter, &proofsAfter); err != nil {
		t.Fatalf("load directory retry state after replay: %v", err)
	}
	if attemptsBefore != 1 || proofsBefore != 1 ||
		attemptsAfter != attemptsBefore || proofsAfter != proofsBefore {
		t.Fatalf(
			"directory retry attempts/proofs = %d/%d -> %d/%d, want stable 1/1",
			attemptsBefore,
			proofsBefore,
			attemptsAfter,
			proofsAfter,
		)
	}
}

func TestCanonicalReviewerRequiresCurrentCompletedAdmittedMention(t *testing.T) {
	tests := []struct {
		name           string
		suffix         string
		makeIneligible func(
			testing.TB,
			canonicalDirectoryFixture,
			postgres.DirectoryPendingMention,
		)
	}{
		{
			name:   "live mention quarantine",
			suffix: "review-quarantined",
			makeIneligible: func(
				t testing.TB,
				fixture canonicalDirectoryFixture,
				mention postgres.DirectoryPendingMention,
			) {
				t.Helper()
				if _, err := fixture.admin.Exec(fixture.ctx, `
					INSERT INTO stacks_core.admission_decisions (
						id, target_kind, target_id, outcome, reason_code, authority,
						recorded_at, supersedes_id, digest_version, digest
					)
					VALUES (
						'admission-quarantine:' || $1, 'mention', $1,
						'quarantined', 'reviewer_test', 'reviewer', $2,
						'admission-mention:' || $1, 'synthetic.admission.v1',
						decode(repeat('88', 32), 'hex')
					)`,
					mention.MentionID,
					canonicalDirectoryRecordedAt,
				); err != nil {
					t.Fatalf("quarantine mention: %v", err)
				}
			},
		},
		{
			name:   "superseded document version",
			suffix: "review-superseded",
			makeIneligible: func(
				t testing.TB,
				fixture canonicalDirectoryFixture,
				_ postgres.DirectoryPendingMention,
			) {
				t.Helper()
				sourceID := "source:review-superseded"
				versionID := "version:review-superseded:current"
				if _, err := fixture.admin.Exec(fixture.ctx, `
					INSERT INTO stacks_core.document_versions (
						id, source_document_id, digest_version, content_digest, title,
						locator, provider_version, modified_at, source_time, recorded_at
					)
					VALUES (
						$1, $2, 'synthetic.document.v1',
						decode(repeat('99', 32), 'hex'), 'Replacement version',
						'synthetic://replacement', 'version-2', $3, NULL, $3
					)`,
					versionID,
					sourceID,
					canonicalDirectoryRecordedAt,
				); err != nil {
					t.Fatalf("insert replacement document version: %v", err)
				}
				if _, err := fixture.admin.Exec(fixture.ctx, `
					UPDATE stacks_core.source_documents
					SET current_version_id = $1
					WHERE id = $2`,
					versionID,
					sourceID,
				); err != nil {
					t.Fatalf("supersede document version: %v", err)
				}
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newCanonicalDirectoryFixture(t)
			mention := seedCanonicalDirectoryMention(
				t,
				fixture,
				testCase.suffix,
				testCase.suffix+"@example.test",
			)
			testCase.makeIneligible(t, fixture, mention)
			store := postgres.ReviewerStore{Database: fixture.database}

			proposals, err := store.ListProposals(fixture.ctx)
			if err != nil {
				t.Fatalf("ListProposals() error = %v", err)
			}
			for _, proposal := range proposals {
				if proposal.Proposal.ID() == identity.ProposalID(mention.ProposalID) {
					t.Fatalf("ineligible proposal %q was listed", mention.ProposalID)
				}
			}
			if _, err := store.LoadProposal(
				fixture.ctx,
				identity.ProposalID(mention.ProposalID),
			); !errors.Is(err, postgres.ErrNotFound) {
				t.Fatalf("LoadProposal() error = %v, want ErrNotFound", err)
			}
			decision := canonicalResolutionDecision(
				t,
				identity.DecisionID("reviewer-decision:"+testCase.suffix),
				identity.ProposalID(mention.ProposalID),
				identity.DecisionRejected,
				"",
				identity.AuthorityReviewer,
				"",
				canonicalDirectoryRecordedAt.Add(time.Hour),
			)
			if _, err := store.AppendDecision(
				fixture.ctx,
				postgres.ReviewerDecisionCommand{Decision: decision},
			); !errors.Is(err, postgres.ErrNotFound) {
				t.Fatalf("AppendDecision() error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestCanonicalReviewerEligibilityMutationWinsConcurrentWrites(t *testing.T) {
	tests := []struct {
		name       string
		suffix     string
		waitMarker string
		targetKind admission.TargetKind
	}{
		{
			name:       "run quarantine",
			suffix:     "review-race-run-quarantine",
			waitMarker: "stacks_admission_target_authority",
			targetKind: admission.TargetExtractionRun,
		},
		{
			name:       "mention quarantine",
			suffix:     "review-race-mention-quarantine",
			waitMarker: "stacks_admission_target_authority",
			targetKind: admission.TargetMention,
		},
		{
			name:       "current version replacement",
			suffix:     "review-race-current-version",
			waitMarker: "stacks_reviewer_eligibility_source",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newCanonicalDirectoryFixture(t)
			mention := seedCanonicalDirectoryMention(
				t,
				fixture,
				testCase.suffix,
				testCase.suffix+"@example.test",
			)
			if testCase.targetKind == "" {
				insertCanonicalReplacementVersion(
					t,
					fixture,
					testCase.suffix,
				)
			}
			var quarantine admission.Decision
			if testCase.targetKind != "" {
				targetID := "mention:" + testCase.suffix
				predecessorID := "admission-mention:" + targetID
				if testCase.targetKind == admission.TargetExtractionRun {
					targetID = "run:" + testCase.suffix
					predecessorID = "admission-run:" + targetID
				}
				quarantine = canonicalAdmissionDecision(
					t,
					"admission-quarantine:"+targetID,
					testCase.targetKind,
					targetID,
					admission.Quarantined,
					admission.AuthorityReviewer,
					predecessorID,
					canonicalDirectoryRecordedAt.Add(time.Hour),
				)
			}
			reviewerDecision := canonicalResolutionDecision(
				t,
				identity.DecisionID("reviewer-decision:"+testCase.suffix),
				identity.ProposalID(mention.ProposalID),
				identity.DecisionRejected,
				"",
				identity.AuthorityReviewer,
				"",
				canonicalDirectoryRecordedAt.Add(2*time.Hour),
			)

			releaseMutation := make(chan struct{})
			var releaseOnce sync.Once
			release := func() {
				releaseOnce.Do(func() {
					close(releaseMutation)
				})
			}
			defer release()
			mutationReady := make(chan int, 1)
			mutationResult := make(chan error, 1)
			go func() {
				mutationResult <- fixture.database.InTransaction(
					fixture.ctx,
					func(transaction *postgres.Transaction) error {
						var backendPID int
						if err := transaction.QueryRow(
							fixture.ctx,
							`SELECT pg_backend_pid()`,
						).Scan(&backendPID); err != nil {
							return fmt.Errorf("load mutation backend PID: %w", err)
						}
						if testCase.targetKind == "" {
							if _, err := transaction.Exec(
								fixture.ctx,
								`UPDATE stacks_core.source_documents
								 SET current_version_id = $1
								 WHERE id = $2`,
								"version:"+testCase.suffix+":replacement",
								"source:"+testCase.suffix,
							); err != nil {
								return fmt.Errorf("replace current document version: %w", err)
							}
						} else {
							if err := transaction.AppendAdmissionDecision(
								fixture.ctx,
								quarantine,
							); err != nil {
								return err
							}
						}
						mutationReady <- backendPID
						<-releaseMutation
						return nil
					},
				)
			}()
			blockerPID := waitForCanonicalTransactionReady(
				t,
				fixture.ctx,
				mutationReady,
				mutationResult,
			)

			reviewerResult := make(chan error, 1)
			go func() {
				_, err := (postgres.ReviewerStore{
					Database: fixture.database,
				}).AppendDecision(
					fixture.ctx,
					postgres.ReviewerDecisionCommand{Decision: reviewerDecision},
				)
				reviewerResult <- err
			}()
			waitForCanonicalDatabaseBlocker(
				t,
				fixture,
				testCase.waitMarker,
				blockerPID,
				reviewerResult,
			)

			release()
			if err := <-mutationResult; err != nil {
				t.Fatalf("eligibility mutation error = %v", err)
			}
			if err := <-reviewerResult; !errors.Is(err, postgres.ErrNotFound) {
				t.Fatalf("concurrent reviewer decision error = %v, want ErrNotFound", err)
			}
			var decisionCount int
			if err := fixture.admin.QueryRow(
				fixture.ctx,
				`SELECT count(*)
				 FROM stacks_core.resolution_decisions
				 WHERE proposal_id = $1`,
				mention.ProposalID,
			).Scan(&decisionCount); err != nil {
				t.Fatalf("count concurrent reviewer decisions: %v", err)
			}
			if decisionCount != 0 {
				t.Fatalf("concurrent reviewer decision count = %d, want 0", decisionCount)
			}
		})
	}
}

func TestCanonicalReviewerEligibilityReviewerWinsConcurrentQuarantine(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	const suffix = "review-race-reviewer-wins"
	mention := seedCanonicalDirectoryMention(
		t,
		fixture,
		suffix,
		suffix+"@example.test",
	)
	decision := canonicalResolutionDecision(
		t,
		"reviewer-decision:"+suffix,
		identity.ProposalID(mention.ProposalID),
		identity.DecisionRejected,
		"",
		identity.AuthorityReviewer,
		"",
		canonicalDirectoryRecordedAt.Add(time.Hour),
	)

	releaseReviewer := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(releaseReviewer)
		})
	}
	defer release()
	reviewerReady := make(chan int, 1)
	reviewerResult := make(chan error, 1)
	go func() {
		reviewerResult <- fixture.database.InTransaction(
			fixture.ctx,
			func(transaction *postgres.Transaction) error {
				var backendPID int
				if err := transaction.QueryRow(
					fixture.ctx,
					`SELECT pg_backend_pid()`,
				).Scan(&backendPID); err != nil {
					return fmt.Errorf("load reviewer backend PID: %w", err)
				}
				if _, err := transaction.Exec(
					fixture.ctx,
					`SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
					testIdentityAuthorityLockNamespace,
					mention.ProposalID,
				); err != nil {
					return fmt.Errorf("lock reviewer proposal authority: %w", err)
				}
				for _, target := range []struct {
					kind admission.TargetKind
					id   string
				}{
					{kind: admission.TargetExtractionRun, id: "run:" + suffix},
					{kind: admission.TargetMention, id: mention.MentionID},
				} {
					if _, err := transaction.Exec(
						fixture.ctx,
						`SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
						testAdmissionAuthorityLockNamespace+"/"+string(target.kind),
						target.id,
					); err != nil {
						return fmt.Errorf("lock reviewer admission authority: %w", err)
					}
				}
				var sourceID string
				if err := transaction.QueryRow(
					fixture.ctx,
					`SELECT id
					 FROM stacks_core.source_documents
					 WHERE id = $1
					 FOR SHARE`,
					"source:"+suffix,
				).Scan(&sourceID); err != nil {
					return fmt.Errorf("lock reviewer source document: %w", err)
				}
				if err := transaction.AppendResolutionDecision(
					fixture.ctx,
					decision,
					nil,
				); err != nil {
					return err
				}
				reviewerReady <- backendPID
				<-releaseReviewer
				return nil
			},
		)
	}()
	blockerPID := waitForCanonicalTransactionReady(
		t,
		fixture.ctx,
		reviewerReady,
		reviewerResult,
	)

	quarantine := canonicalAdmissionDecision(
		t,
		"admission-quarantine:run:"+suffix,
		admission.TargetExtractionRun,
		"run:"+suffix,
		admission.Quarantined,
		admission.AuthorityReviewer,
		"admission-run:run:"+suffix,
		canonicalDirectoryRecordedAt.Add(2*time.Hour),
	)
	quarantineResult := make(chan error, 1)
	go func() {
		quarantineResult <- fixture.database.InTransaction(
			fixture.ctx,
			func(transaction *postgres.Transaction) error {
				return transaction.AppendAdmissionDecision(fixture.ctx, quarantine)
			},
		)
	}()
	waitForCanonicalDatabaseBlocker(
		t,
		fixture,
		"stacks_admission_target_authority",
		blockerPID,
		quarantineResult,
	)

	release()
	if err := <-reviewerResult; err != nil {
		t.Fatalf("reviewer transaction error = %v", err)
	}
	if err := <-quarantineResult; err != nil {
		t.Fatalf("quarantine transaction error = %v", err)
	}
	stored, err := fixture.database.LoadResolutionDecision(
		fixture.ctx,
		decision.ID(),
	)
	if err != nil {
		t.Fatalf("load reviewer-wins decision: %v", err)
	}
	if stored.ID() != decision.ID() {
		t.Fatalf("reviewer-wins decision = %q, want %q", stored.ID(), decision.ID())
	}
}

func insertCanonicalReplacementVersion(
	t testing.TB,
	fixture canonicalDirectoryFixture,
	suffix string,
) {
	t.Helper()
	if _, err := fixture.admin.Exec(
		fixture.ctx,
		`INSERT INTO stacks_core.document_versions (
			id, source_document_id, digest_version, content_digest, title,
			locator, provider_version, modified_at, source_time, recorded_at
		)
		VALUES (
			$1, $2, 'synthetic.document.v1',
			decode(repeat('99', 32), 'hex'), 'Replacement version',
			'synthetic://replacement', 'version-2', $3, NULL, $3
		)`,
		"version:"+suffix+":replacement",
		"source:"+suffix,
		canonicalDirectoryRecordedAt,
	); err != nil {
		t.Fatalf("insert replacement document version: %v", err)
	}
}

func waitForCanonicalTransactionReady(
	t testing.TB,
	ctx context.Context,
	ready <-chan int,
	result <-chan error,
) int {
	t.Helper()
	select {
	case backendPID := <-ready:
		return backendPID
	case err := <-result:
		t.Fatalf("transaction completed before ready: %v", err)
	case <-ctx.Done():
		t.Fatalf("wait for transaction ready: %v", ctx.Err())
	}
	return 0
}

func waitForCanonicalDatabaseBlocker(
	t testing.TB,
	fixture canonicalDirectoryFixture,
	queryMarker string,
	blockerPID int,
	earlyResult <-chan error,
) {
	t.Helper()
	for {
		select {
		case err := <-earlyResult:
			t.Fatalf("database operation completed before blocking: %v", err)
		default:
		}
		var blocked bool
		if err := fixture.admin.QueryRow(
			fixture.ctx,
			`SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity AS activity
				WHERE activity.datname = current_database()
				  AND activity.pid <> pg_backend_pid()
				  AND activity.state = 'active'
				  AND activity.wait_event_type = 'Lock'
				  AND activity.query LIKE '%' || $1 || '%'
				  AND $2 = ANY(pg_blocking_pids(activity.pid))
			)`,
			queryMarker,
			blockerPID,
		).Scan(&blocked); err != nil {
			t.Fatalf("inspect database blocker: %v", err)
		}
		if blocked {
			return
		}
		if err := fixture.ctx.Err(); err != nil {
			t.Fatalf("wait for database blocker: %v", err)
		}
		runtime.Gosched()
	}
}

func TestCanonicalReviewerDirectoryProofIsAppendOnlyAndCorrectionSafe(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	mention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"review-directory",
		"directory.review@example.test",
	)
	input := canonicalDirectoryPersistInput()
	input.Mention = mention
	input.Query = postgres.DirectoryQuery{
		Kind:          postgres.DirectoryQueryName,
		Name:          mention.NormalizedName,
		EmailEvidence: postgres.DirectoryEmailEvidenceNone,
	}
	input.Lookup.Outcome = postgres.DirectoryOutcomeReview
	input.Lookup.Profiles[0].Emails[0].Value = mention.ProposedEmail
	input.Evaluation = postgres.DirectoryEvaluation{
		Outcome:    postgres.DirectoryOutcomeReview,
		Candidates: append([]postgres.DirectoryProfile(nil), input.Lookup.Profiles...),
	}
	if _, err := (postgres.DirectoryStore{Database: fixture.database}).Persist(
		fixture.ctx,
		input,
	); err != nil {
		t.Fatalf("persist directory review candidate: %v", err)
	}

	store := postgres.ReviewerStore{Database: fixture.database, IncludeDirectory: true}
	proposal, err := store.LoadProposal(fixture.ctx, identity.ProposalID(mention.ProposalID))
	if err != nil {
		t.Fatalf("LoadProposal() error = %v", err)
	}
	if len(proposal.Candidates) != 1 ||
		proposal.Candidates[0].Directory == nil ||
		proposal.Candidates[0].Directory.MaskedEmail != "d***@example.test" {
		t.Fatalf("directory review projection = %#v, want one masked selected profile", proposal.Candidates)
	}
	candidate := proposal.Candidates[0]
	recordedAt := canonicalDirectoryRecordedAt.Add(3 * time.Hour)
	decision := canonicalResolutionDecision(
		t,
		"reviewer-decision:directory",
		proposal.Proposal.ID(),
		identity.DecisionAccepted,
		candidate.Candidate.EntityID(),
		identity.AuthorityReviewer,
		"",
		recordedAt,
	)
	alias := canonicalAliasAssertion(
		t,
		"reviewer-alias:directory",
		decision.ID(),
		decision.EntityID(),
		identity.Alias{Type: identity.AliasTypeName, Value: mention.NormalizedName},
		recordedAt,
	)
	command := postgres.ReviewerDirectoryDecisionCommand{
		Decision: decision, Aliases: []identity.AliasAssertion{alias},
		SnapshotID: candidate.Directory.SnapshotID,
	}
	if _, err := store.AcceptDirectoryCandidate(fixture.ctx, command); err != nil {
		t.Fatalf("AcceptDirectoryCandidate() error = %v", err)
	}
	if _, err := store.AcceptDirectoryCandidate(fixture.ctx, command); err != nil {
		t.Fatalf("AcceptDirectoryCandidate() exact retry error = %v", err)
	}
	changedSnapshot := command
	changedSnapshot.SnapshotID = command.SnapshotID + ":changed"
	if _, err := store.AcceptDirectoryCandidate(
		fixture.ctx,
		changedSnapshot,
	); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf(
			"AcceptDirectoryCandidate(changed snapshot retry) error = %v, want ErrConflict",
			err,
		)
	}

	correctedEntity := canonicalEntity(t, "reviewer-person:corrected", "Corrected Person")
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		_, err := transaction.PutEntity(fixture.ctx, correctedEntity)
		return err
	}); err != nil {
		t.Fatalf("persist correction entity: %v", err)
	}
	correction := canonicalResolutionDecision(
		t,
		"reviewer-decision:correction",
		proposal.Proposal.ID(),
		identity.DecisionAccepted,
		correctedEntity.ID(),
		identity.AuthorityReviewer,
		decision.ID(),
		recordedAt.Add(time.Microsecond),
	)
	correctionAlias := canonicalAliasAssertion(
		t,
		"reviewer-alias:correction",
		correction.ID(),
		correctedEntity.ID(),
		identity.Alias{Type: identity.AliasTypeName, Value: mention.NormalizedName},
		correction.RecordedAt(),
	)
	if _, err := store.AppendDecision(fixture.ctx, postgres.ReviewerDecisionCommand{
		Decision: correction, Aliases: []identity.AliasAssertion{correctionAlias},
	}); err != nil {
		t.Fatalf("AppendDecision(correction) error = %v", err)
	}
	if _, err := store.AppendDecision(fixture.ctx, postgres.ReviewerDecisionCommand{
		Decision: correction, Aliases: []identity.AliasAssertion{correctionAlias},
	}); err != nil {
		t.Fatalf("AppendDecision(correction exact retry) error = %v", err)
	}
	stale := canonicalResolutionDecision(
		t,
		"reviewer-decision:stale",
		proposal.Proposal.ID(),
		identity.DecisionAccepted,
		candidate.Candidate.EntityID(),
		identity.AuthorityReviewer,
		decision.ID(),
		recordedAt.Add(2*time.Microsecond),
	)
	if _, err := store.AppendDecision(fixture.ctx, postgres.ReviewerDecisionCommand{
		Decision: stale,
	}); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("stale correction error = %v, want ErrConflict", err)
	}

	var staged, proofs int
	if err := fixture.admin.QueryRow(fixture.ctx, `
		SELECT
			count(*) FILTER (WHERE decision_id IS NULL),
			count(*) FILTER (WHERE decision_id IS NOT NULL)
		FROM stacks_directory.entity_links
		WHERE proposal_id = $1`,
		proposal.Proposal.ID(),
	).Scan(&staged, &proofs); err != nil {
		t.Fatalf("inspect append-only directory proof history: %v", err)
	}
	if staged != 1 || proofs != 2 {
		t.Fatalf("directory staged/proof rows = %d/%d, want 1/2", staged, proofs)
	}
	state, err := (postgres.DirectoryStore{Database: fixture.database}).LoadIdentityState(fixture.ctx)
	if err != nil {
		t.Fatalf("LoadIdentityState() error = %v", err)
	}
	if len(state.Links) != 1 || state.Links[0].EntityID != string(correctedEntity.ID()) {
		t.Fatalf("effective directory links = %#v, want corrected entity", state.Links)
	}
}

func TestCanonicalReviewerDirectoryProviderSubjectHasOneOwnerSequentially(t *testing.T) {
	store, first, second := reviewerProviderOwnerCommands(t, "sequential")

	if _, err := store.AcceptDirectoryCandidate(context.Background(), first); err != nil {
		t.Fatalf("first AcceptDirectoryCandidate() error = %v", err)
	}
	if _, err := store.AcceptDirectoryCandidate(context.Background(), first); err != nil {
		t.Fatalf("first AcceptDirectoryCandidate() exact retry error = %v", err)
	}
	if _, err := store.AcceptDirectoryCandidate(
		context.Background(),
		second,
	); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("conflicting AcceptDirectoryCandidate() error = %v, want ErrConflict", err)
	}
}

func TestCanonicalReviewerDirectoryProviderSubjectHasOneOwnerConcurrently(t *testing.T) {
	store, first, second := reviewerProviderOwnerCommands(t, "concurrent")
	commands := []postgres.ReviewerDirectoryDecisionCommand{first, second}
	start := make(chan struct{})
	results := make(chan error, len(commands))
	var ready sync.WaitGroup
	ready.Add(len(commands))
	for _, command := range commands {
		command := command
		go func() {
			ready.Done()
			<-start
			_, err := store.AcceptDirectoryCandidate(context.Background(), command)
			results <- err
		}()
	}
	ready.Wait()
	close(start)

	var succeeded, conflicted int
	for range commands {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, postgres.ErrConflict):
			conflicted++
		default:
			t.Fatalf("concurrent AcceptDirectoryCandidate() error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent results = success:%d conflict:%d, want 1/1", succeeded, conflicted)
	}
}

func TestCanonicalReviewerSuppliedDirectoryEvidenceHasOneOwnerSequentially(t *testing.T) {
	store, first, second := reviewerSuppliedOwnerCommands(t, "sequential")

	if _, err := store.CreatePerson(context.Background(), first); err != nil {
		t.Fatalf("first CreatePerson() error = %v", err)
	}
	if _, err := store.CreatePerson(context.Background(), first); err != nil {
		t.Fatalf("first CreatePerson() exact retry error = %v", err)
	}
	if _, err := store.CreatePerson(
		context.Background(),
		second,
	); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("conflicting CreatePerson() error = %v, want ErrConflict", err)
	}
}

func TestCanonicalReviewerSuppliedDirectoryEvidenceHasOneOwnerConcurrently(t *testing.T) {
	store, first, second := reviewerSuppliedOwnerCommands(t, "concurrent")
	commands := []postgres.ReviewerCreatePersonCommand{first, second}
	start := make(chan struct{})
	results := make(chan error, len(commands))
	var ready sync.WaitGroup
	ready.Add(len(commands))
	for _, command := range commands {
		command := command
		go func() {
			ready.Done()
			<-start
			_, err := store.CreatePerson(context.Background(), command)
			results <- err
		}()
	}
	ready.Wait()
	close(start)

	var succeeded, conflicted int
	for range commands {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, postgres.ErrConflict):
			conflicted++
		default:
			t.Fatalf("concurrent CreatePerson() error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent results = success:%d conflict:%d, want 1/1", succeeded, conflicted)
	}
}

func reviewerProviderOwnerCommands(
	t testing.TB,
	suffix string,
) (
	postgres.ReviewerStore,
	postgres.ReviewerDirectoryDecisionCommand,
	postgres.ReviewerDirectoryDecisionCommand,
) {
	t.Helper()
	fixture := newCanonicalDirectoryFixture(t)
	firstMention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"review-owner-first-"+suffix,
		"first."+suffix+"@example.test",
	)
	secondMention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"review-owner-second-"+suffix,
		"second."+suffix+"@example.test",
	)
	profile := canonicalDirectoryPersistInput().Lookup.Profiles[0]
	for _, mention := range []postgres.DirectoryPendingMention{firstMention, secondMention} {
		input := canonicalDirectoryPersistInput()
		input.Mention = mention
		input.Query = postgres.DirectoryQuery{
			Kind:          postgres.DirectoryQueryName,
			Name:          mention.NormalizedName,
			EmailEvidence: postgres.DirectoryEmailEvidenceNone,
		}
		input.Lookup.Outcome = postgres.DirectoryOutcomeReview
		input.Lookup.Profiles = []postgres.DirectoryProfile{profile}
		input.Evaluation = postgres.DirectoryEvaluation{
			Outcome:    postgres.DirectoryOutcomeReview,
			Candidates: []postgres.DirectoryProfile{profile},
		}
		if _, err := (postgres.DirectoryStore{Database: fixture.database}).Persist(
			fixture.ctx,
			input,
		); err != nil {
			t.Fatalf("persist staged directory candidate: %v", err)
		}
	}
	firstEntity := canonicalEntity(
		t,
		identity.EntityID("review-owner-first-entity:"+suffix),
		"First Owner",
	)
	secondEntity := canonicalEntity(
		t,
		identity.EntityID("review-owner-second-entity:"+suffix),
		"Second Owner",
	)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		if _, err := transaction.PutEntity(fixture.ctx, firstEntity); err != nil {
			return err
		}
		_, err := transaction.PutEntity(fixture.ctx, secondEntity)
		return err
	}); err != nil {
		t.Fatalf("persist reviewer owner entities: %v", err)
	}
	store := postgres.ReviewerStore{Database: fixture.database, IncludeDirectory: true}
	firstProposal, err := store.LoadProposal(
		fixture.ctx,
		identity.ProposalID(firstMention.ProposalID),
	)
	if err != nil {
		t.Fatalf("load first staged proposal: %v", err)
	}
	secondProposal, err := store.LoadProposal(
		fixture.ctx,
		identity.ProposalID(secondMention.ProposalID),
	)
	if err != nil {
		t.Fatalf("load second staged proposal: %v", err)
	}
	if len(firstProposal.Candidates) != 1 ||
		firstProposal.Candidates[0].Directory == nil ||
		len(secondProposal.Candidates) != 1 ||
		secondProposal.Candidates[0].Directory == nil ||
		firstProposal.Candidates[0].Directory.SnapshotID !=
			secondProposal.Candidates[0].Directory.SnapshotID {
		t.Fatalf(
			"staged candidates = first:%#v second:%#v, want same provider snapshot",
			firstProposal.Candidates,
			secondProposal.Candidates,
		)
	}
	recordedAt := canonicalDirectoryRecordedAt.Add(5 * time.Hour)
	firstDecision := canonicalResolutionDecision(
		t,
		identity.DecisionID("review-owner-first-decision:"+suffix),
		firstProposal.Proposal.ID(),
		identity.DecisionAccepted,
		firstEntity.ID(),
		identity.AuthorityReviewer,
		"",
		recordedAt,
	)
	secondDecision := canonicalResolutionDecision(
		t,
		identity.DecisionID("review-owner-second-decision:"+suffix),
		secondProposal.Proposal.ID(),
		identity.DecisionAccepted,
		secondEntity.ID(),
		identity.AuthorityReviewer,
		"",
		recordedAt,
	)
	return store,
		postgres.ReviewerDirectoryDecisionCommand{
			Decision:   firstDecision,
			SnapshotID: firstProposal.Candidates[0].Directory.SnapshotID,
		},
		postgres.ReviewerDirectoryDecisionCommand{
			Decision:   secondDecision,
			SnapshotID: secondProposal.Candidates[0].Directory.SnapshotID,
		}
}

func reviewerSuppliedOwnerCommands(
	t testing.TB,
	suffix string,
) (
	postgres.ReviewerStore,
	postgres.ReviewerCreatePersonCommand,
	postgres.ReviewerCreatePersonCommand,
) {
	t.Helper()
	fixture := newCanonicalDirectoryFixture(t)
	mentions := []postgres.DirectoryPendingMention{
		seedCanonicalDirectoryMention(
			t,
			fixture,
			"review-supplied-first-"+suffix,
			"first.supplied."+suffix+"@example.test",
		),
		seedCanonicalDirectoryMention(
			t,
			fixture,
			"review-supplied-second-"+suffix,
			"second.supplied."+suffix+"@example.test",
		),
	}
	recordedAt := canonicalDirectoryRecordedAt.Add(6 * time.Hour)
	commands := make([]postgres.ReviewerCreatePersonCommand, len(mentions))
	for index, mention := range mentions {
		person := canonicalEntity(
			t,
			identity.EntityID(fmt.Sprintf("review-supplied-owner:%s:%d", suffix, index)),
			fmt.Sprintf("Supplied Owner %d", index+1),
		)
		decision := canonicalResolutionDecision(
			t,
			identity.DecisionID(fmt.Sprintf("review-supplied-decision:%s:%d", suffix, index)),
			identity.ProposalID(mention.ProposalID),
			identity.DecisionAccepted,
			person.ID(),
			identity.AuthorityReviewer,
			"",
			recordedAt,
		)
		input := canonicalDirectoryPersistInput()
		input.Mention = mention
		input.Query.Email = mention.ProposedEmail
		input.Query.EmailEvidence = postgres.DirectoryEmailEvidenceReviewerSupplied
		input.Lookup.Profiles[0].Emails[0].Value = mention.ProposedEmail
		input.Evaluation.AcceptedEmail = mention.ProposedEmail
		input.Evaluation.Profile = &input.Lookup.Profiles[0]
		input.RecordedAt = recordedAt
		commands[index] = postgres.ReviewerCreatePersonCommand{
			Entity: person, Decision: decision,
			DirectoryEvidence: &postgres.ReviewerDirectoryEvidenceCommand{
				Mention: input.Mention, Query: input.Query, Lookup: input.Lookup,
				Evaluation: input.Evaluation, AttemptCount: input.AttemptCount,
				RecordedAt: input.RecordedAt, RetryAfter: input.RetryAfter,
			},
		}
	}
	return postgres.ReviewerStore{
		Database: fixture.database, IncludeDirectory: true,
	}, commands[0], commands[1]
}

func TestCanonicalReviewerDirectoryEvidenceIsOptionalAndAtomic(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	mention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"reviewer-evidence",
		"reviewer.evidence@example.test",
	)
	recordedAt := canonicalDirectoryRecordedAt.Add(4 * time.Hour)
	person := canonicalEntity(t, "reviewer-person:evidence", "Evidence Person")
	decision := canonicalResolutionDecision(
		t,
		"reviewer-decision:evidence",
		identity.ProposalID(mention.ProposalID),
		identity.DecisionAccepted,
		person.ID(),
		identity.AuthorityReviewer,
		"",
		recordedAt,
	)
	alias := canonicalAliasAssertion(
		t,
		"reviewer-alias:evidence",
		decision.ID(),
		person.ID(),
		identity.Alias{Type: identity.AliasTypeEmail, Value: mention.ProposedEmail},
		recordedAt,
	)
	input := canonicalDirectoryPersistInput()
	input.Mention = mention
	input.Query.Email = mention.ProposedEmail
	input.Query.EmailEvidence = postgres.DirectoryEmailEvidenceReviewerSupplied
	input.Lookup.Profiles[0].Emails[0].Value = mention.ProposedEmail
	input.Evaluation.AcceptedEmail = mention.ProposedEmail
	input.Evaluation.Profile = &input.Lookup.Profiles[0]
	input.RecordedAt = recordedAt
	command := postgres.ReviewerCreatePersonCommand{
		Entity: person, Decision: decision, Aliases: []identity.AliasAssertion{alias},
		DirectoryEvidence: &postgres.ReviewerDirectoryEvidenceCommand{
			Mention: input.Mention, Query: input.Query, Lookup: input.Lookup,
			Evaluation: input.Evaluation, AttemptCount: input.AttemptCount,
			RecordedAt: input.RecordedAt, RetryAfter: input.RetryAfter,
		},
	}
	store := postgres.ReviewerStore{Database: fixture.database, IncludeDirectory: true}
	if _, err := store.CreatePerson(fixture.ctx, command); err != nil {
		t.Fatalf("CreatePerson(with reviewer evidence) error = %v", err)
	}
	if _, err := store.CreatePerson(fixture.ctx, command); err != nil {
		t.Fatalf("CreatePerson(with reviewer evidence exact retry) error = %v", err)
	}
	omittedEvidence := command
	omittedEvidence.DirectoryEvidence = nil
	if _, err := store.CreatePerson(
		fixture.ctx,
		omittedEvidence,
	); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("CreatePerson(omitted evidence retry) error = %v, want ErrConflict", err)
	}
	changedEvidence := command
	changedDirectoryEvidence := *command.DirectoryEvidence
	changedDirectoryEvidence.AttemptCount++
	changedEvidence.DirectoryEvidence = &changedDirectoryEvidence
	if _, err := store.CreatePerson(
		fixture.ctx,
		changedEvidence,
	); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("CreatePerson(changed evidence retry) error = %v, want ErrConflict", err)
	}
	var attempts, proofRows, candidateProofRows int
	if err := fixture.admin.QueryRow(fixture.ctx, `
		SELECT
			(SELECT count(*)
			 FROM stacks_directory.lookup_attempts
			 WHERE proposal_id = $1),
			(SELECT count(*)
			 FROM stacks_directory.entity_links
			 WHERE proposal_id = $1 AND decision_id = $2),
			(SELECT count(*)
			 FROM stacks_directory.entity_links
			 WHERE proposal_id = $1 AND decision_id = $2
			   AND candidate_id IS NOT NULL)`,
		mention.ProposalID,
		decision.ID(),
	).Scan(&attempts, &proofRows, &candidateProofRows); err != nil {
		t.Fatalf("inspect reviewer directory evidence: %v", err)
	}
	if attempts != 1 || proofRows != 1 || candidateProofRows != 0 {
		t.Fatalf(
			"reviewer attempts/proofs/candidate proofs = %d/%d/%d, want 1/1/0",
			attempts,
			proofRows,
			candidateProofRows,
		)
	}

	invalidMention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"reviewer-evidence-invalid",
		"invalid.evidence@example.test",
	)
	invalidPerson := canonicalEntity(t, "reviewer-person:evidence-rollback", "Rollback Person")
	invalidDecision := canonicalResolutionDecision(
		t,
		"reviewer-decision:evidence-rollback",
		identity.ProposalID(invalidMention.ProposalID),
		identity.DecisionAccepted,
		invalidPerson.ID(),
		identity.AuthorityReviewer,
		"",
		recordedAt.Add(time.Microsecond),
	)
	invalid := *command.DirectoryEvidence
	invalid.Mention = invalidMention
	invalid.Query.Email = "different@example.test"
	if _, err := store.CreatePerson(fixture.ctx, postgres.ReviewerCreatePersonCommand{
		Entity: invalidPerson, Decision: invalidDecision, DirectoryEvidence: &invalid,
	}); err == nil {
		t.Fatal("CreatePerson(invalid reviewer evidence) error = nil, want rollback")
	}
	if _, err := fixture.database.LoadEntity(fixture.ctx, invalidPerson.ID()); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("invalid-evidence rollback LoadEntity() error = %v, want ErrNotFound", err)
	}
}

func TestCanonicalReviewerPersistsRetryWindowFromActionTime(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	mention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"reviewer-retry-window",
		"reviewer.retry@example.test",
	)
	recordedAt := canonicalDirectoryRecordedAt.Add(8 * time.Hour)
	retryAfter := recordedAt.Add(30 * time.Second)
	person := canonicalEntity(t, "reviewer-person:retry-window", "Retry Person")
	decision := canonicalResolutionDecision(
		t,
		"reviewer-decision:retry-window",
		identity.ProposalID(mention.ProposalID),
		identity.DecisionAccepted,
		person.ID(),
		identity.AuthorityReviewer,
		"",
		recordedAt,
	)
	command := postgres.ReviewerCreatePersonCommand{
		Entity:   person,
		Decision: decision,
		DirectoryEvidence: &postgres.ReviewerDirectoryEvidenceCommand{
			Mention: mention,
			Query: postgres.DirectoryQuery{
				Kind:          postgres.DirectoryQueryEmail,
				Email:         mention.ProposedEmail,
				EmailEvidence: postgres.DirectoryEmailEvidenceReviewerSupplied,
			},
			Lookup: postgres.DirectoryLookupResult{
				Provider:   "google_people",
				Outcome:    postgres.DirectoryOutcomeUnavailable,
				RetryAfter: 30 * time.Second,
			},
			Evaluation: postgres.DirectoryEvaluation{
				Outcome: postgres.DirectoryOutcomeUnavailable,
			},
			AttemptCount: 1,
			RecordedAt:   recordedAt,
			RetryAfter:   &retryAfter,
		},
	}
	store := postgres.ReviewerStore{
		Database:         fixture.database,
		IncludeDirectory: true,
	}
	if _, err := store.CreatePerson(fixture.ctx, command); err != nil {
		t.Fatalf("CreatePerson(retry window) error = %v", err)
	}
	if _, err := store.CreatePerson(fixture.ctx, command); err != nil {
		t.Fatalf("CreatePerson(retry window exact retry) error = %v", err)
	}
	var storedRecordedAt time.Time
	var storedRetryAfter time.Time
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT recorded_at, retry_after
		 FROM stacks_directory.lookup_attempts
		 WHERE proposal_id = $1`,
		mention.ProposalID,
	).Scan(&storedRecordedAt, &storedRetryAfter); err != nil {
		t.Fatalf("load persisted reviewer retry window: %v", err)
	}
	if !storedRecordedAt.Equal(recordedAt) ||
		!storedRetryAfter.Equal(retryAfter) ||
		storedRetryAfter.Sub(storedRecordedAt) != 30*time.Second {
		t.Fatalf(
			"persisted reviewer retry window = %s -> %s, want %s -> %s",
			storedRecordedAt,
			storedRetryAfter,
			recordedAt,
			retryAfter,
		)
	}
}
