package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/directorymigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/jackc/pgx/v5"
)

const directoryRepositoryTimeout = 15 * time.Second

type canonicalDirectoryFixture struct {
	ctx      context.Context
	database *postgres.Database
	admin    *pgx.Conn
}

func TestCanonicalDirectoryExactEmailCreatesAutomaticAuthority(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	mention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"exact-email",
		"synthetic.person@example.test",
	)
	input := canonicalDirectoryPersistInput()
	input.Mention = mention
	input.Query.Email = mention.ProposedEmail
	input.Lookup.Profiles[0].Emails[0].Value = mention.ProposedEmail
	input.Evaluation.AcceptedEmail = mention.ProposedEmail
	input.Evaluation.Profile = &input.Lookup.Profiles[0]

	result, err := (postgres.DirectoryStore{Database: fixture.database}).Persist(
		fixture.ctx,
		input,
	)
	if err != nil {
		t.Fatalf("Persist() error = %v", err)
	}
	if !result.AutoResolved || result.EntityID == "" {
		t.Fatalf("Persist() result = %#v, want automatic entity authority", result)
	}

	decision, err := fixture.database.EffectiveResolutionDecision(
		fixture.ctx,
		identity.ProposalID(mention.ProposalID),
	)
	if err != nil {
		t.Fatalf("EffectiveResolutionDecision() error = %v", err)
	}
	if decision.Authority() != identity.AuthorityAutomatic ||
		string(decision.EntityID()) != result.EntityID {
		t.Fatalf("effective directory decision = %#v, want automatic entity %q", decision, result.EntityID)
	}
	proposal, err := fixture.database.LoadResolutionProposal(
		fixture.ctx,
		identity.ProposalID(mention.ProposalID),
	)
	if err != nil {
		t.Fatalf("LoadResolutionProposal() error = %v", err)
	}
	if len(proposal.Candidates) != 1 ||
		proposal.Candidates[0].ReasonCode() != "unique_exact_work_email" ||
		proposal.Candidates[0].Source().Kind != "directory" ||
		proposal.Candidates[0].Source().Reference == "" {
		t.Fatalf("directory candidate provenance = %#v, want one exact-email opaque source", proposal.Candidates)
	}
}

func TestCanonicalDirectoryNameOnlyCreatesReviewCandidate(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	mention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"name-only",
		"synthetic.name@example.test",
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

	result, err := (postgres.DirectoryStore{Database: fixture.database}).Persist(
		fixture.ctx,
		input,
	)
	if err != nil {
		t.Fatalf("Persist() error = %v", err)
	}
	if result != (postgres.DirectoryPersistResult{}) {
		t.Fatalf("Persist() result = %#v, want review-only result", result)
	}
	if _, err := fixture.database.EffectiveResolutionDecision(
		fixture.ctx,
		identity.ProposalID(mention.ProposalID),
	); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("EffectiveResolutionDecision() error = %v, want ErrNotFound", err)
	}
	proposal, err := fixture.database.LoadResolutionProposal(
		fixture.ctx,
		identity.ProposalID(mention.ProposalID),
	)
	if err != nil {
		t.Fatalf("LoadResolutionProposal() error = %v", err)
	}
	if len(proposal.Candidates) != 1 ||
		proposal.Candidates[0].ReasonCode() != "directory_name_review" ||
		proposal.Candidates[0].Source().Kind != "directory" {
		t.Fatalf("name-only candidates = %#v, want one directory review candidate", proposal.Candidates)
	}
}

func TestCanonicalDirectoryProviderConflictDowngradesToReview(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	store := postgres.DirectoryStore{Database: fixture.database}
	firstMention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"provider-conflict-first",
		"provider.first@example.test",
	)
	first := canonicalDirectoryPersistInput()
	first.Mention = firstMention
	first.Query.Email = firstMention.ProposedEmail
	first.Lookup.Profiles[0].SubjectID = "people/shared-provider-subject"
	first.Lookup.Profiles[0].Emails[0].Value = firstMention.ProposedEmail
	first.Evaluation.AcceptedEmail = firstMention.ProposedEmail
	first.Evaluation.Profile = &first.Lookup.Profiles[0]
	if result, err := store.Persist(fixture.ctx, first); err != nil {
		t.Fatalf("persist first provider authority: %v", err)
	} else if !result.AutoResolved {
		t.Fatalf("first provider authority result = %#v, want automatic", result)
	}

	secondMention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"provider-conflict-second",
		"provider.second@example.test",
	)
	second := canonicalDirectoryPersistInput()
	second.Mention = secondMention
	second.Query.Email = secondMention.ProposedEmail
	second.Lookup.Profiles[0].SubjectID = first.Lookup.Profiles[0].SubjectID
	second.Lookup.Profiles[0].Emails[0].Value = secondMention.ProposedEmail
	second.Lookup.Profiles[0].ObservedAt = canonicalDirectoryRecordedAt.Add(time.Hour)
	second.Evaluation.AcceptedEmail = secondMention.ProposedEmail
	second.Evaluation.Profile = &second.Lookup.Profiles[0]
	second.RecordedAt = canonicalDirectoryRecordedAt.Add(time.Hour)
	result, err := store.Persist(fixture.ctx, second)
	if err != nil {
		t.Fatalf("persist conflicting provider authority: %v", err)
	}
	if result != (postgres.DirectoryPersistResult{}) {
		t.Fatalf("provider conflict result = %#v, want review-only", result)
	}
	if _, err := fixture.database.EffectiveResolutionDecision(
		fixture.ctx,
		identity.ProposalID(secondMention.ProposalID),
	); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("conflicting provider effective decision error = %v, want ErrNotFound", err)
	}
	proposal, err := fixture.database.LoadResolutionProposal(
		fixture.ctx,
		identity.ProposalID(secondMention.ProposalID),
	)
	if err != nil {
		t.Fatalf("load conflicting provider proposal: %v", err)
	}
	if len(proposal.Candidates) != 1 ||
		proposal.Candidates[0].ReasonCode() != "directory_exact_email_review" {
		t.Fatalf("provider conflict candidates = %#v, want exact-email review", proposal.Candidates)
	}
}

func TestCanonicalDirectoryRetryAndChangedSnapshotsAreIdempotent(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	mention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"snapshot-retry",
		"snapshot.retry@example.test",
	)
	input := canonicalDirectoryPersistInput()
	input.Mention = mention
	input.Query.Email = mention.ProposedEmail
	input.Lookup.Profiles[0].SubjectID = "people/snapshot-retry"
	input.Lookup.Profiles[0].Emails[0].Value = mention.ProposedEmail
	input.Evaluation.AcceptedEmail = mention.ProposedEmail
	input.Evaluation.Profile = &input.Lookup.Profiles[0]
	store := postgres.DirectoryStore{Database: fixture.database}

	first, err := store.Persist(fixture.ctx, input)
	if err != nil {
		t.Fatalf("first Persist() error = %v", err)
	}
	repeated, err := store.Persist(fixture.ctx, input)
	if err != nil {
		t.Fatalf("repeated Persist() error = %v", err)
	}
	if first != repeated || !first.AutoResolved {
		t.Fatalf("exact retry results = %#v/%#v, want identical automatic authority", first, repeated)
	}
	assertCanonicalDirectoryCounts(t, fixture, mention, 1, 1, 1, 1)

	changed := input
	changed.RecordedAt = canonicalDirectoryRecordedAt.Add(2 * time.Hour)
	changed.Lookup.Profiles = append(
		[]postgres.DirectoryProfile(nil),
		input.Lookup.Profiles...,
	)
	changed.Lookup.Profiles[0].DisplayName = "Synthetic Person Updated"
	changed.Lookup.Profiles[0].ObservedAt = canonicalDirectoryRecordedAt.Add(time.Hour)
	changed.Evaluation.Profile = &changed.Lookup.Profiles[0]
	result, err := store.Persist(fixture.ctx, changed)
	if err != nil {
		t.Fatalf("changed Persist() error = %v", err)
	}
	if result != (postgres.DirectoryPersistResult{}) {
		t.Fatalf("changed snapshot result = %#v, want review-only changed evidence", result)
	}
	assertCanonicalDirectoryCounts(t, fixture, mention, 2, 2, 2, 1)
}

func TestCanonicalDirectoryBoundedFailurePreservesReviewerAuthority(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	mention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"reviewer-authority",
		"reviewer.authority@example.test",
	)
	matched := canonicalDirectoryPersistInput()
	matched.Mention = mention
	matched.Query.Email = mention.ProposedEmail
	matched.Lookup.Profiles[0].SubjectID = "people/reviewer-authority"
	matched.Lookup.Profiles[0].Emails[0].Value = mention.ProposedEmail
	matched.Evaluation.AcceptedEmail = mention.ProposedEmail
	matched.Evaluation.Profile = &matched.Lookup.Profiles[0]
	store := postgres.DirectoryStore{Database: fixture.database}
	automatic, err := store.Persist(fixture.ctx, matched)
	if err != nil {
		t.Fatalf("persist initial automatic authority: %v", err)
	}

	reviewerEntity, err := identity.NewEntity(identity.EntityInput{
		ID:          "entity:reviewer-correction",
		Kind:        identity.KindPerson,
		DisplayName: "Synthetic Reviewer Choice",
		RecordedAt:  canonicalDirectoryRecordedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("NewEntity() error = %v", err)
	}
	current, err := fixture.database.EffectiveResolutionDecision(
		fixture.ctx,
		identity.ProposalID(mention.ProposalID),
	)
	if err != nil {
		t.Fatalf("load automatic decision: %v", err)
	}
	correction, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
		ID:           "decision:reviewer-correction",
		ProposalID:   identity.ProposalID(mention.ProposalID),
		Outcome:      identity.DecisionAccepted,
		EntityID:     reviewerEntity.ID(),
		Authority:    identity.AuthorityReviewer,
		ReasonCode:   "reviewer_corrected",
		RecordedAt:   canonicalDirectoryRecordedAt.Add(time.Hour),
		SupersedesID: current.ID(),
	})
	if err != nil {
		t.Fatalf("NewResolutionDecision() error = %v", err)
	}
	if err := fixture.database.InTransaction(
		fixture.ctx,
		func(transaction *postgres.Transaction) error {
			if _, err := transaction.PutEntity(fixture.ctx, reviewerEntity); err != nil {
				return err
			}
			return transaction.AppendResolutionDecision(fixture.ctx, correction, nil)
		},
	); err != nil {
		t.Fatalf("append reviewer correction: %v", err)
	}

	noMatch := postgres.DirectoryPersistInput{
		Mention: mention,
		Query: postgres.DirectoryQuery{
			Kind:          postgres.DirectoryQueryEmail,
			Email:         mention.ProposedEmail,
			EmailEvidence: postgres.DirectoryEmailEvidenceSourceBound,
		},
		Lookup: postgres.DirectoryLookupResult{
			Outcome: postgres.DirectoryOutcomeNoMatch,
		},
		AttemptCount: 2,
		RecordedAt:   canonicalDirectoryRecordedAt.Add(2 * time.Hour),
	}
	if result, err := store.Persist(fixture.ctx, noMatch); err != nil {
		t.Fatalf("persist bounded no-match attempt: %v", err)
	} else if result != (postgres.DirectoryPersistResult{}) {
		t.Fatalf("bounded no-match result = %#v, want no authority mutation", result)
	}
	effectiveAfterNoMatch, err := fixture.database.EffectiveResolutionDecision(
		fixture.ctx,
		identity.ProposalID(mention.ProposalID),
	)
	if err != nil {
		t.Fatalf("load reviewer correction after no-match: %v", err)
	}
	if effectiveAfterNoMatch.ID() != correction.ID() {
		t.Fatalf(
			"effective decision after no-match = %q, want reviewer correction %q",
			effectiveAfterNoMatch.ID(),
			correction.ID(),
		)
	}

	retryAfter := canonicalDirectoryRecordedAt.Add(4 * time.Hour)
	unavailable := postgres.DirectoryPersistInput{
		Mention: mention,
		Query: postgres.DirectoryQuery{
			Kind:          postgres.DirectoryQueryEmail,
			Email:         mention.ProposedEmail,
			EmailEvidence: postgres.DirectoryEmailEvidenceSourceBound,
		},
		Lookup: postgres.DirectoryLookupResult{
			Outcome: postgres.DirectoryOutcomeUnavailable,
		},
		AttemptCount: 3,
		RecordedAt:   canonicalDirectoryRecordedAt.Add(3 * time.Hour),
		RetryAfter:   &retryAfter,
	}
	if result, err := store.Persist(fixture.ctx, unavailable); err != nil {
		t.Fatalf("persist bounded unavailable attempt: %v", err)
	} else if result != (postgres.DirectoryPersistResult{}) {
		t.Fatalf("bounded unavailable result = %#v, want no authority mutation", result)
	}
	effective, err := fixture.database.EffectiveResolutionDecision(
		fixture.ctx,
		identity.ProposalID(mention.ProposalID),
	)
	if err != nil {
		t.Fatalf("load reviewer correction after failure: %v", err)
	}
	if effective.ID() != correction.ID() ||
		effective.Authority() != identity.AuthorityReviewer ||
		string(effective.EntityID()) != string(reviewerEntity.ID()) ||
		automatic.EntityID == string(reviewerEntity.ID()) {
		t.Fatalf("effective decision after directory failure = %#v, want reviewer correction", effective)
	}
	state, err := store.LoadIdentityState(fixture.ctx)
	if err != nil {
		t.Fatalf("LoadIdentityState() error = %v", err)
	}
	if len(state.Links) != 1 || state.Links[0].EntityID != string(reviewerEntity.ID()) {
		t.Fatalf("effective directory links = %#v, want reviewer entity", state.Links)
	}
}

func TestDirectoryStoreConcurrentExactEmailCreatesOneAuthority(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	firstMention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"concurrent-first",
		"concurrent.owner@example.test",
	)
	secondMention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"concurrent-second",
		"concurrent.owner@example.test",
	)
	profile := canonicalDirectoryPersistInput().Lookup.Profiles[0]
	profile.SubjectID = "people/concurrent-owner"
	profile.Emails[0].Value = firstMention.ProposedEmail
	inputs := []postgres.DirectoryPersistInput{
		canonicalDirectoryPersistInput(),
		canonicalDirectoryPersistInput(),
	}
	for index, mention := range []postgres.DirectoryPendingMention{firstMention, secondMention} {
		inputs[index].Mention = mention
		inputs[index].Query.Email = mention.ProposedEmail
		inputs[index].Lookup.Profiles = []postgres.DirectoryProfile{profile}
		inputs[index].Evaluation.AcceptedEmail = mention.ProposedEmail
		inputs[index].Evaluation.Profile = &inputs[index].Lookup.Profiles[0]
		inputs[index].RecordedAt = canonicalDirectoryRecordedAt.Add(time.Duration(index) * time.Second)
	}

	store := postgres.DirectoryStore{Database: fixture.database}
	results := make([]postgres.DirectoryPersistResult, len(inputs))
	errs := make([]error, len(inputs))
	var wait sync.WaitGroup
	wait.Add(len(inputs))
	for index := range inputs {
		index := index
		go func() {
			defer wait.Done()
			results[index], errs[index] = store.Persist(fixture.ctx, inputs[index])
		}()
	}
	wait.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("concurrent Persist() errors = %v/%v", errs[0], errs[1])
	}
	if results[0].AutoResolved == results[1].AutoResolved {
		t.Fatalf("concurrent results = %#v/%#v, want exactly one automatic authority", results[0], results[1])
	}
	authorityID := results[0].EntityID
	if authorityID == "" {
		authorityID = results[1].EntityID
	}
	if authorityID == "" {
		t.Fatalf("concurrent results = %#v/%#v, want one nonblank authority", results[0], results[1])
	}

	var entities, decisions, providerOwners int
	if err := fixture.admin.QueryRow(fixture.ctx, `
		SELECT
			(
				SELECT count(*)
				FROM stacks_core.entities
				WHERE id = $1
			),
			(
				SELECT count(*)
				FROM stacks_core.resolution_decisions AS decision
				WHERE decision.proposal_id IN ($2, $3)
				  AND decision.authority = 'automatic'
			),
			(
				SELECT count(DISTINCT current.entity_id)
				FROM stacks_directory.entity_links AS link
				JOIN stacks_directory.profiles AS profile
				  ON profile.id = link.profile_id
				JOIN stacks_core.resolution_decisions AS current
				  ON current.proposal_id = link.proposal_id
				WHERE profile.provider = $4
				  AND profile.provider_subject_id = $5
				  AND current.outcome = 'accepted'
				  AND NOT EXISTS (
					SELECT 1
					FROM stacks_core.resolution_decisions AS successor
					WHERE successor.supersedes_id = current.id
				  )
			)`,
		authorityID,
		firstMention.ProposalID,
		secondMention.ProposalID,
		profile.Provider,
		profile.SubjectID,
	).Scan(&entities, &decisions, &providerOwners); err != nil {
		t.Fatalf("count concurrent authority: %v", err)
	}
	if entities != 1 || decisions != 1 || providerOwners != 1 {
		t.Fatalf(
			"concurrent entities/automatic decisions/provider owners = %d/%d/%d, want 1/1/1",
			entities,
			decisions,
			providerOwners,
		)
	}
}

func TestCanonicalDirectoryCoreFailureRollsBackDirectoryAndAuthority(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	mention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"rollback",
		"rollback.person@example.test",
	)
	input := canonicalDirectoryPersistInput()
	input.Mention = mention
	input.Query.Email = mention.ProposedEmail
	input.Lookup.Profiles[0].SubjectID = "people/rollback"
	input.Lookup.Profiles[0].Emails[0].Value = mention.ProposedEmail
	input.Evaluation.AcceptedEmail = mention.ProposedEmail
	input.Evaluation.Profile = &input.Lookup.Profiles[0]

	if _, err := fixture.admin.Exec(fixture.ctx, `
		CREATE FUNCTION stacks_core.synthetic_directory_decision_failure()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			RAISE EXCEPTION 'synthetic core authority failure';
		END;
		$$;
		CREATE TRIGGER synthetic_directory_decision_failure
		BEFORE INSERT ON stacks_core.resolution_decisions
		FOR EACH ROW
		EXECUTE FUNCTION stacks_core.synthetic_directory_decision_failure()`,
	); err != nil {
		t.Fatalf("install synthetic core failure: %v", err)
	}
	if _, err := (postgres.DirectoryStore{Database: fixture.database}).Persist(
		fixture.ctx,
		input,
	); err == nil {
		t.Fatal("Persist() error = nil, want injected core failure")
	}

	var directoryRows, coreRows int
	if err := fixture.admin.QueryRow(fixture.ctx, `
		SELECT
			(
				SELECT count(*)
				FROM (
					SELECT id FROM stacks_directory.profiles
					UNION ALL
					SELECT id FROM stacks_directory.snapshots
					UNION ALL
					SELECT id FROM stacks_directory.lookup_attempts
					UNION ALL
					SELECT id FROM stacks_directory.entity_links
				) AS directory_rows
			),
			(
				SELECT count(*)
				FROM stacks_core.resolution_candidates
				WHERE proposal_id = $1
			) + (
				SELECT count(*)
				FROM stacks_core.resolution_decisions
				WHERE proposal_id = $1
			)`,
		mention.ProposalID,
	).Scan(&directoryRows, &coreRows); err != nil {
		t.Fatalf("count rolled-back directory/core rows: %v", err)
	}
	if directoryRows != 0 || coreRows != 0 {
		t.Fatalf("rolled-back directory/core row counts = %d/%d, want 0/0", directoryRows, coreRows)
	}
}

func TestCanonicalDirectoryWorkHonorsFreshnessAndRetryWindows(t *testing.T) {
	fixture := newCanonicalDirectoryFixture(t)
	mention := seedCanonicalDirectoryMention(
		t,
		fixture,
		"work-windows",
		"work.windows@example.test",
	)
	store := postgres.DirectoryStore{Database: fixture.database}
	request := postgres.DirectoryWorkRequest{
		DerivationID: "run:work-windows",
		Now:          canonicalDirectoryRecordedAt,
		Freshness:    time.Hour,
		RetryAfter:   5 * time.Minute,
	}
	work, err := store.LoadWork(fixture.ctx, request)
	if err != nil {
		t.Fatalf("initial LoadWork() error = %v", err)
	}
	if len(work.Mentions) != 1 || work.Mentions[0].MentionID != mention.MentionID {
		t.Fatalf("initial work = %#v, want pending mention", work)
	}

	retryAt := canonicalDirectoryRecordedAt.Add(5 * time.Minute)
	if _, err := store.Persist(fixture.ctx, postgres.DirectoryPersistInput{
		Mention: mention,
		Query: postgres.DirectoryQuery{
			Kind:          postgres.DirectoryQueryName,
			Name:          mention.NormalizedName,
			EmailEvidence: postgres.DirectoryEmailEvidenceNone,
		},
		Lookup:       postgres.DirectoryLookupResult{Outcome: postgres.DirectoryOutcomeUnavailable},
		AttemptCount: 1,
		RecordedAt:   canonicalDirectoryRecordedAt,
		RetryAfter:   &retryAt,
	}); err != nil {
		t.Fatalf("persist retryable attempt: %v", err)
	}
	request.Now = canonicalDirectoryRecordedAt.Add(time.Minute)
	blocked, err := store.LoadWork(fixture.ctx, request)
	if err != nil {
		t.Fatalf("blocked LoadWork() error = %v", err)
	}
	if len(blocked.Mentions) != 0 || blocked.Reused != 0 {
		t.Fatalf("blocked work = %#v, want retry suppression", blocked)
	}
	request.Now = retryAt
	retry, err := store.LoadWork(fixture.ctx, request)
	if err != nil {
		t.Fatalf("retry LoadWork() error = %v", err)
	}
	if len(retry.Mentions) != 1 {
		t.Fatalf("retry work = %#v, want one mention at retry boundary", retry)
	}
}

func newCanonicalDirectoryFixture(t testing.TB) canonicalDirectoryFixture {
	t.Helper()

	isolated := postgrestest.NewDatabase(t)
	core, err := coremigrations.Manifest()
	if err != nil {
		t.Fatalf("coremigrations.Manifest() error = %v", err)
	}
	directory, err := directorymigrations.Manifest()
	if err != nil {
		t.Fatalf("directorymigrations.Manifest() error = %v", err)
	}
	applicationConfig, err := pgx.ParseConfig(isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("parse application test database URL: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), directoryRepositoryTimeout)
	t.Cleanup(cancel)
	if _, err := (migration.Migrator{
		DatabaseURL:     isolated.AdminURL(),
		ApplicationRole: applicationConfig.User,
		Manifests:       []migration.Manifest{core, directory},
	}).Apply(ctx); err != nil {
		t.Fatalf("install canonical directory scopes: %v", err)
	}
	database, err := postgres.Open(ctx, isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	t.Cleanup(database.Close)
	admin, err := pgx.Connect(ctx, isolated.AdminURL())
	if err != nil {
		t.Fatalf("connect canonical directory admin: %v", err)
	}
	t.Cleanup(func() {
		_ = admin.Close(context.Background())
	})
	return canonicalDirectoryFixture{ctx: ctx, database: database, admin: admin}
}

func seedCanonicalDirectoryMention(
	t testing.TB,
	fixture canonicalDirectoryFixture,
	suffix string,
	email string,
) postgres.DirectoryPendingMention {
	t.Helper()

	sourceID := "source:" + suffix
	versionID := "version:" + suffix
	sectionID := "section:" + suffix
	evidenceID := "evidence:" + suffix
	runID := "run:" + suffix
	mentionID := "mention:" + suffix
	proposalID := "proposal:" + suffix
	if _, err := fixture.admin.Exec(fixture.ctx, `
		INSERT INTO stacks_core.source_documents (
			id, provider, provider_document_id, current_version_id, created_at
		)
		VALUES ($1, 'synthetic', $1, NULL, $7);
		INSERT INTO stacks_core.document_versions (
			id, source_document_id, digest_version, content_digest, title,
			locator, provider_version, modified_at, source_time, recorded_at
		)
		VALUES (
			$2, $1, 'synthetic.document.v1', decode(repeat('11', 32), 'hex'),
			'Synthetic directory record', 'synthetic://directory', 'version-1',
			$7, NULL, $7
		);
		UPDATE stacks_core.source_documents
		SET current_version_id = $2
		WHERE id = $1;
		INSERT INTO stacks_core.document_sections (
			document_version_id, section_id, title, parent_id, path,
			section_order, role, content
		)
		VALUES (
			$2, $3, 'Synthetic section', '', ARRAY['Synthetic section'],
			0, 'transcript', 'Synthetic Person'
		);
		INSERT INTO stacks_core.evidence_spans (
			id, document_version_id, section_id, digest_version, digest,
			start_offset, end_offset, quote, recorded_at
		)
		VALUES (
			$4, $2, $3, 'synthetic.evidence.v1',
			decode(repeat('22', 32), 'hex'), 0, 16, 'Synthetic Person', $7
		);
		INSERT INTO stacks_core.extraction_runs (
			id, document_version_id, derivation_digest_version,
			derivation_digest, method, version, provider, data_mode, model,
			prompt_version, schema_digest, max_output_tokens, recorded_at,
			state, completed_at, write_set_digest_version, write_set_digest
		)
		VALUES (
			$5, $2, 'synthetic.derivation.v1',
			decode(repeat('33', 32), 'hex'), 'synthetic', 'v1',
			'synthetic', 'personal', 'synthetic-model', 'synthetic-prompt',
			decode(repeat('44', 32), 'hex'), 1, $7, 'completed', $7,
			'synthetic.write-set.v1', decode(repeat('55', 32), 'hex')
		);
		INSERT INTO stacks_core.mentions (
			id, evidence_id, derivation_run_id, surface, normalized_name,
			proposed_email, proposed_email_evidence_id, role, recorded_at
		)
		VALUES (
			$6, $4, $5, 'Synthetic Person', 'synthetic person',
			$8, $4, 'speaker', $7
		);
		INSERT INTO stacks_core.resolution_proposals (
			id, mention_id, reason_code, recorded_at
		)
		VALUES ($9, $6, 'identity_review', $7);
		INSERT INTO stacks_core.resolution_proposal_evidence (
			proposal_id, evidence_id, evidence_order
		)
		VALUES ($9, $4, 0);
		INSERT INTO stacks_core.admission_targets (
			target_kind, target_id, recorded_at
		)
		VALUES
			('extraction_run', $5, $7),
			('mention', $6, $7);
		INSERT INTO stacks_core.admission_decisions (
			id, target_kind, target_id, outcome, reason_code, authority,
			recorded_at, supersedes_id, digest_version, digest
		)
		VALUES
			('admission-run:' || $5, 'extraction_run', $5, 'admitted',
			 'validated', 'policy', $7, NULL, 'synthetic.admission.v1',
			 decode(repeat('66', 32), 'hex')),
			('admission-mention:' || $6, 'mention', $6, 'admitted',
			 'validated', 'policy', $7, NULL, 'synthetic.admission.v1',
			 decode(repeat('77', 32), 'hex'))`,
		pgx.QueryExecModeSimpleProtocol,
		sourceID,
		versionID,
		sectionID,
		evidenceID,
		runID,
		mentionID,
		canonicalDirectoryRecordedAt.Add(-2*time.Hour),
		email,
		proposalID,
	); err != nil {
		t.Fatalf("seed canonical directory mention: %v", err)
	}
	return postgres.DirectoryPendingMention{
		MentionID:      mentionID,
		ProposalID:     proposalID,
		Surface:        "Synthetic Person",
		NormalizedName: "synthetic person",
		ProposedEmail:  email,
		NameQuote:      "Synthetic Person",
		EmailQuote:     fmt.Sprintf("Synthetic Person <%s>", email),
	}
}

func assertCanonicalDirectoryCounts(
	t testing.TB,
	fixture canonicalDirectoryFixture,
	mention postgres.DirectoryPendingMention,
	wantSnapshots int,
	wantAttempts int,
	wantLinks int,
	wantEntities int,
) {
	t.Helper()

	var snapshots, attempts, links, entities int
	if err := fixture.admin.QueryRow(fixture.ctx, `
		SELECT
			(
				SELECT count(*)
				FROM stacks_directory.snapshots AS snapshot
				JOIN stacks_directory.profiles AS profile
				  ON profile.id = snapshot.profile_id
				WHERE profile.provider_subject_id = $1
			),
			(
				SELECT count(*)
				FROM stacks_directory.lookup_attempts
				WHERE mention_id = $2
			),
			(
				SELECT count(*)
				FROM stacks_directory.entity_links
				WHERE proposal_id = $3
			),
			(
				SELECT count(DISTINCT entity_id)
				FROM stacks_directory.entity_links
				WHERE proposal_id = $3
			)`,
		"people/snapshot-retry",
		mention.MentionID,
		mention.ProposalID,
	).Scan(&snapshots, &attempts, &links, &entities); err != nil {
		t.Fatalf("count canonical directory state: %v", err)
	}
	if snapshots != wantSnapshots ||
		attempts != wantAttempts ||
		links != wantLinks ||
		entities != wantEntities {
		t.Fatalf(
			"directory snapshots/attempts/links/entities = %d/%d/%d/%d, want %d/%d/%d/%d",
			snapshots,
			attempts,
			links,
			entities,
			wantSnapshots,
			wantAttempts,
			wantLinks,
			wantEntities,
		)
	}
}
