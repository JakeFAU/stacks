package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	knowledge "github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	analysisdomain "stacks/internal/analysis"
	"stacks/internal/directory"
	"stacks/internal/entity"
	"stacks/internal/extract"
	"stacks/internal/ingest"
	"stacks/internal/modelpolicy"
	"stacks/internal/source"
)

const (
	testDatabaseURLEnvironmentVariable          = "STACKS_TEST_DATABASE_URL"
	testMigrationDatabaseURLEnvironmentVariable = "STACKS_TEST_MIGRATION_DATABASE_URL"
)

func TestDirectoryLoadWorkReturnsCurrentAdmissiblePendingMentionEvidence(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	derivationID, mention := createDirectoryPendingMentionFixture(t, pool, "load-work")
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)

	work, err := NewDirectoryRepository(pool).LoadWork(ctx, derivationID, now, 24*time.Hour, 5*time.Minute)
	if err != nil {
		t.Fatalf("load directory work: %v", err)
	}
	if work.Reused != 0 || len(work.Mentions) != 1 {
		t.Fatalf("directory work counts = reused %d, mentions %d; want 0/1", work.Reused, len(work.Mentions))
	}
	if work.Mentions[0] != mention {
		t.Fatalf("directory pending mention = %#v, want exact synthetic evidence %#v", work.Mentions[0], mention)
	}

	seedDirectoryLookupAttempt(t, pool, mention.MentionID, entity.DirectoryNoMatch, now.Add(-time.Hour), nil)
	work, err = NewDirectoryRepository(pool).LoadWork(ctx, derivationID, now, 24*time.Hour, 5*time.Minute)
	if err != nil {
		t.Fatalf("reuse fresh directory work: %v", err)
	}
	if work.Reused != 1 || len(work.Mentions) != 0 {
		t.Fatalf("fresh directory work counts = reused %d, mentions %d; want 1/0", work.Reused, len(work.Mentions))
	}
}

func TestDirectoryLoadWorkRetriesOnlyExpiredTransientAttempt(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	derivationID, mention := createDirectoryPendingMentionFixture(t, pool, "retry-work")
	recordedAt := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	retryAfter := recordedAt.Add(10 * time.Minute)
	seedDirectoryLookupAttempt(t, pool, mention.MentionID, entity.DirectoryUnavailable, recordedAt, &retryAfter)

	blocked, err := NewDirectoryRepository(pool).LoadWork(ctx, derivationID, recordedAt.Add(time.Minute), 24*time.Hour, 5*time.Minute)
	if err != nil {
		t.Fatalf("load retry-blocked directory work: %v", err)
	}
	if blocked.Reused != 0 || len(blocked.Mentions) != 0 {
		t.Fatalf("retry-blocked work counts = reused %d, mentions %d; want 0/0", blocked.Reused, len(blocked.Mentions))
	}

	expired, err := NewDirectoryRepository(pool).LoadWork(ctx, derivationID, retryAfter.Add(time.Nanosecond), 24*time.Hour, 5*time.Minute)
	if err != nil {
		t.Fatalf("load expired directory work: %v", err)
	}
	if expired.Reused != 0 || len(expired.Mentions) != 1 || expired.Mentions[0] != mention {
		t.Fatalf("expired retry work = %#v, want one pending synthetic mention", expired)
	}
}

func TestDirectoryLoadIdentityStateMatchesAcceptedAliasAuthority(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	_, mention := createDirectoryPendingMentionFixture(t, pool, "identity-state")
	entityID := uuid.NewString()
	storedEntity, decision, err := NewEntityRepository(pool).CreateReviewPerson(ctx, CreateReviewPersonInput{
		ProposalID:  mention.ProposalID,
		EntityID:    entityID,
		Kind:        string(entity.KindPerson),
		DisplayName: "Synthetic Canonical Person",
		Aliases: []AliasInput{{
			NormalizedValue: mention.ProposedEmail,
			Type:            string(entity.AliasTypeEmail),
		}},
	})
	if err != nil {
		t.Fatalf("create synthetic accepted identity: %v", err)
	}
	profile := syntheticDirectoryProfile(
		"identity-state-"+mention.MentionID,
		mention.ProposedEmail,
		time.Time{},
	)
	snapshotID, attemptID := seedDirectoryProfileEvidence(t, pool, mention.MentionID, profile)
	if _, err := pool.Exec(ctx, `
		INSERT INTO stacks.entity_directory_identity_assertions
			(decision_id, entity_id, lookup_attempt_id, snapshot_id, provider, provider_subject_id, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		decision.ID, storedEntity.ID, attemptID, snapshotID, profile.Provider, profile.SubjectID,
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed synthetic directory identity assertion: %v", err)
	}

	wantSnapshots, err := NewIngestionRepository(pool).EntitySnapshots(ctx)
	if err != nil {
		t.Fatalf("load ingestion entity snapshots: %v", err)
	}
	state, err := NewDirectoryRepository(pool).LoadIdentityState(ctx)
	if err != nil {
		t.Fatalf("load directory identity state: %v", err)
	}
	wantLink := entity.DirectoryIdentityLink{
		Provider: profile.Provider, SubjectID: profile.SubjectID, EntityID: entityID,
	}
	if !containsDirectoryIdentityLink(state.Links, wantLink) {
		t.Fatalf("directory identity links = %#v, want current synthetic link %#v", state.Links, wantLink)
	}
	if !sameEntitySnapshots(state.Snapshots, wantSnapshots) {
		t.Fatalf("directory snapshots = %#v, want accepted-alias projection %#v", state.Snapshots, wantSnapshots)
	}
}

func TestDirectoryPersistIsIdempotentAndPreservesChangedSnapshots(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	_, mention := createDirectoryPendingMentionFixture(t, pool, "persist-idempotent")
	profile := syntheticDirectoryProfile(
		"persist-idempotent-"+mention.MentionID,
		mention.ProposedEmail,
		time.Time{},
	)
	recordedAt := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	input := matchedDirectoryPersistInput(mention, profile, recordedAt)
	repository := NewDirectoryRepository(pool)

	first, err := repository.Persist(ctx, input)
	if err != nil {
		t.Fatalf("persist initial directory match: %v", err)
	}
	second, err := repository.Persist(ctx, input)
	if err != nil {
		t.Fatalf("repeat identical directory match: %v", err)
	}
	if !first.AutoResolved || first.EntityID == "" || second != first {
		t.Fatalf("directory persist results = %#v then %#v, want one idempotent automatic entity", first, second)
	}

	assertDirectoryAuthorityCounts(t, pool, mention, profile, first.EntityID, 1, 1)
	var observedAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT observed_at
		FROM stacks.directory_profile_snapshots
		WHERE provider = $1 AND provider_subject_id = $2`,
		profile.Provider, profile.SubjectID).Scan(&observedAt); err != nil {
		t.Fatalf("load nullable synthetic directory observation time: %v", err)
	}
	if observedAt != nil {
		t.Fatalf("directory observed_at = %v, want unknown source time preserved as NULL", observedAt)
	}

	changed := profile
	changed.DisplayName = "Synthetic Directory Person Updated"
	changed.ObservedAt = recordedAt.Add(time.Hour)
	changedInput := matchedDirectoryPersistInput(mention, changed, recordedAt.Add(2*time.Hour))
	changedResult, err := repository.Persist(ctx, changedInput)
	if err != nil {
		t.Fatalf("persist changed directory profile: %v", err)
	}
	if !changedResult.AutoResolved || changedResult.EntityID != first.EntityID {
		t.Fatalf("changed directory result = %#v, want existing automatic entity %q", changedResult, first.EntityID)
	}
	assertDirectoryAuthorityCounts(t, pool, mention, profile, first.EntityID, 2, 2)
	var originalDisplayName string
	if err := pool.QueryRow(ctx, `
		SELECT display_name
		FROM stacks.directory_profile_snapshots
		WHERE provider = $1 AND provider_subject_id = $2 AND observed_at IS NULL`,
		profile.Provider, profile.SubjectID).Scan(&originalDisplayName); err != nil {
		t.Fatalf("load original immutable directory snapshot: %v", err)
	}
	if originalDisplayName != profile.DisplayName {
		t.Fatalf("original directory snapshot display name = %q, want %q", originalDisplayName, profile.DisplayName)
	}
}

func TestDirectoryPersistUsesExistingAcceptedEmailOwner(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	email := "existing.owner." + strings.ReplaceAll(uuid.NewString(), "-", "") + "@synthetic.example"
	_, ownerMention := createDirectoryPendingMentionFixtureWithEmail(t, pool, "existing-owner", email)
	ownerID := uuid.NewString()
	if _, _, err := NewEntityRepository(pool).CreateReviewPerson(ctx, CreateReviewPersonInput{
		ProposalID:  ownerMention.ProposalID,
		EntityID:    ownerID,
		Kind:        string(entity.KindPerson),
		DisplayName: "Synthetic Existing Owner",
		Aliases: []AliasInput{{
			NormalizedValue: email,
			Type:            string(entity.AliasTypeEmail),
		}},
	}); err != nil {
		t.Fatalf("create existing accepted email owner: %v", err)
	}
	_, mention := createDirectoryPendingMentionFixtureWithEmail(t, pool, "existing-match", email)
	profile := syntheticDirectoryProfile("existing-owner-"+mention.MentionID, email, time.Time{})
	input := matchedDirectoryPersistInput(mention, profile, time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC))
	input.Evaluation.CreatePerson = false
	input.Evaluation.EntityID = ownerID

	result, err := NewDirectoryRepository(pool).Persist(ctx, input)
	if err != nil {
		t.Fatalf("persist directory match for existing owner: %v", err)
	}
	if !result.AutoResolved || result.EntityID != ownerID {
		t.Fatalf("existing-owner directory result = %#v, want entity %q", result, ownerID)
	}
	var nameAliases, emailAliases int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE assertion.alias_type = 'name'),
		       count(*) FILTER (WHERE assertion.alias_type = 'email')
		FROM stacks.entity_alias_assertions AS assertion
		JOIN stacks.resolution_decisions AS decision ON decision.id = assertion.decision_id
		WHERE decision.proposal_id = $1`, mention.ProposalID).Scan(&nameAliases, &emailAliases); err != nil {
		t.Fatalf("count automatic existing-owner aliases: %v", err)
	}
	if nameAliases != 0 || emailAliases != 1 {
		t.Fatalf("automatic name/email aliases = %d/%d, want email only", nameAliases, emailAliases)
	}
}

func TestDirectoryPersistNameCandidateCreatesNoAuthority(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	_, mention := createDirectoryPendingMentionFixture(t, pool, "name-candidate")
	profile := syntheticDirectoryProfile(
		"name-candidate-"+mention.MentionID,
		mention.ProposedEmail,
		time.Time{},
	)
	profile.DisplayName = "Synthetic Name Candidate " + mention.MentionID
	existingEntityID := uuid.NewString()
	if _, err := NewEntityRepository(pool).CreateEntity(ctx, EntityInput{
		ID: existingEntityID, Kind: string(entity.KindPerson), DisplayName: "Synthetic Existing Candidate",
	}); err != nil {
		t.Fatalf("create existing entity candidate: %v", err)
	}
	confidence := 0.5
	if _, err := NewEntityRepository(pool).PutCandidate(ctx, ResolutionCandidateInput{
		ProposalID: mention.ProposalID, EntityID: existingEntityID, Rank: 0,
		Confidence: &confidence, Reason: "synthetic existing candidate",
	}); err != nil {
		t.Fatalf("put existing entity candidate: %v", err)
	}
	input := directory.PersistInput{
		Mention: mention,
		Query: entity.DirectoryQuery{
			Kind: entity.DirectoryQueryName, Name: mention.NormalizedName,
			EmailEvidence: entity.EmailEvidenceNone,
		},
		Lookup: directory.LookupResult{
			Outcome: entity.DirectoryMatched, Profiles: []entity.DirectoryProfile{profile},
		},
		Evaluation: entity.DirectoryEvaluation{
			Outcome: entity.DirectoryReview, Candidates: []entity.DirectoryProfile{profile},
		},
		AttemptCount: 1,
		RecordedAt:   time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
	}

	result, err := NewDirectoryRepository(pool).Persist(ctx, input)
	if err != nil {
		t.Fatalf("persist directory name candidate: %v", err)
	}
	repeated, err := NewDirectoryRepository(pool).Persist(ctx, input)
	if err != nil {
		t.Fatalf("repeat identical directory name candidate: %v", err)
	}
	if result != (directory.PersistResult{}) {
		t.Fatalf("name-candidate directory result = %#v, want no authority", result)
	}
	if repeated != (directory.PersistResult{}) {
		t.Fatalf("repeated name-candidate directory result = %#v, want no authority", repeated)
	}
	var decisions, directoryCandidates, createdEntities int
	if err := pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*) FROM stacks.resolution_decisions WHERE proposal_id = $1),
		    (SELECT count(*) FROM stacks.resolution_candidates
		     WHERE proposal_id = $1 AND directory_profile_snapshot_id IS NOT NULL),
		    (SELECT count(*) FROM stacks.entities
		     WHERE display_name = $2)`,
		mention.ProposalID, profile.DisplayName).Scan(
		&decisions, &directoryCandidates, &createdEntities,
	); err != nil {
		t.Fatalf("count directory name-candidate authority: %v", err)
	}
	if decisions != 0 || directoryCandidates != 1 || createdEntities != 0 {
		t.Fatalf("name-candidate decisions/candidates/entities = %d/%d/%d, want 0/1/0", decisions, directoryCandidates, createdEntities)
	}
	var rank int
	var reason string
	if err := pool.QueryRow(ctx, `
		SELECT rank, reason
		FROM stacks.resolution_candidates
		WHERE proposal_id = $1 AND directory_profile_snapshot_id IS NOT NULL`,
		mention.ProposalID).Scan(&rank, &reason); err != nil {
		t.Fatalf("load directory name candidate: %v", err)
	}
	if rank != 1 || reason != "directory name candidate requires review" {
		t.Fatalf("directory name candidate rank/reason = %d/%q, want 1/bounded reason", rank, reason)
	}
}

func TestDirectoryReviewAcceptanceCreatesNameAndProviderAuthorityAtomically(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	_, mention := createDirectoryPendingMentionFixture(t, pool, "review-accept")
	profile := syntheticDirectoryProfile(
		"review-accept-"+mention.MentionID,
		mention.ProposedEmail,
		time.Time{},
	)
	snapshotID := persistDirectoryNameCandidateForReview(t, pool, mention, profile)
	repository := NewEntityRepository(pool)

	if _, _, err := repository.AcceptDirectoryCandidate(ctx, AcceptDirectoryInput{
		ProposalID:         mention.ProposalID,
		DirectoryProfileID: uuid.NewString(),
	}); err == nil || !strings.Contains(err.Error(), "candidate") {
		t.Fatalf("accept non-candidate snapshot error = %v, want exact candidate rejection", err)
	}
	if got := countRows(t, pool, `
		SELECT count(*) FROM stacks.resolution_decisions WHERE proposal_id = $1`,
		mention.ProposalID,
	); got != 0 {
		t.Fatalf("decisions after non-candidate acceptance = %d, want 0", got)
	}

	storedEntity, decision, err := repository.AcceptDirectoryCandidate(ctx, AcceptDirectoryInput{
		ProposalID:         mention.ProposalID,
		DirectoryProfileID: snapshotID,
	})
	if err != nil {
		t.Fatalf("accept exact directory candidate: %v", err)
	}
	if storedEntity.ID == "" ||
		decision.Outcome != ResolutionOutcomeCreated ||
		decision.EntityID != storedEntity.ID {
		t.Fatalf("directory review result = entity %#v decision %#v, want created authority", storedEntity, decision)
	}

	var displayName, status string
	var nameAliases, providerAssertions int
	if err := pool.QueryRow(ctx, `
		SELECT
		    entity.display_name,
		    proposal.status,
		    (SELECT count(*)
		     FROM stacks.entity_alias_assertions AS assertion
		     WHERE assertion.decision_id = $1
		       AND assertion.entity_id = $2
		       AND assertion.alias_type = 'name'
		       AND assertion.normalized_value = $3),
		    (SELECT count(*)
		     FROM stacks.entity_directory_identity_assertions AS assertion
		     WHERE assertion.decision_id = $1
		       AND assertion.entity_id = $2
		       AND assertion.snapshot_id = $4)
		FROM stacks.entities AS entity
		JOIN stacks.resolution_proposals AS proposal ON proposal.id = $5
		WHERE entity.id = $2`,
		decision.ID,
		storedEntity.ID,
		mention.NormalizedName,
		snapshotID,
		mention.ProposalID,
	).Scan(&displayName, &status, &nameAliases, &providerAssertions); err != nil {
		t.Fatalf("load accepted directory review authority: %v", err)
	}
	if displayName != profile.DisplayName ||
		status != "resolved" ||
		nameAliases != 1 ||
		providerAssertions != 1 {
		t.Fatalf(
			"accepted directory display/status/name aliases/provider assertions = %q/%q/%d/%d, want %q/resolved/1/1",
			displayName,
			status,
			nameAliases,
			providerAssertions,
			profile.DisplayName,
		)
	}
}

func TestDirectoryReviewAcceptanceProviderConflictRollsBackTransition(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	_, ownerMention := createDirectoryPendingMentionFixture(t, pool, "review-conflict-owner")
	ownerID := uuid.NewString()
	_, ownerDecision, err := NewEntityRepository(pool).CreateReviewPerson(ctx, CreateReviewPersonInput{
		ProposalID:  ownerMention.ProposalID,
		EntityID:    ownerID,
		Kind:        string(entity.KindPerson),
		DisplayName: "Synthetic Provider Owner",
		Aliases: []AliasInput{{
			NormalizedValue: ownerMention.ProposedEmail,
			Type:            string(entity.AliasTypeEmail),
		}},
	})
	if err != nil {
		t.Fatalf("create provider owner: %v", err)
	}
	sharedSubject := "synthetic-review-conflict-" + uuid.NewString()
	ownerProfile := syntheticDirectoryProfile(
		sharedSubject,
		ownerMention.ProposedEmail,
		time.Time{},
	)
	ownerProfile.SubjectID = sharedSubject
	ownerSnapshotID, ownerAttemptID := seedDirectoryProfileEvidence(
		t,
		pool,
		ownerMention.MentionID,
		ownerProfile,
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO stacks.entity_directory_identity_assertions
			(decision_id, entity_id, lookup_attempt_id, snapshot_id,
			 provider, provider_subject_id, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		ownerDecision.ID,
		ownerID,
		ownerAttemptID,
		ownerSnapshotID,
		ownerProfile.Provider,
		ownerProfile.SubjectID,
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("seed provider owner assertion: %v", err)
	}

	_, mention := createDirectoryPendingMentionFixture(t, pool, "review-conflict-target")
	targetProfile := syntheticDirectoryProfile(
		"review-conflict-target-"+mention.MentionID,
		mention.ProposedEmail,
		time.Time{},
	)
	targetProfile.SubjectID = sharedSubject
	snapshotID := persistDirectoryNameCandidateForReview(t, pool, mention, targetProfile)
	entitiesBefore := countRows(t, pool, `SELECT count(*) FROM stacks.entities`)

	_, _, err = NewEntityRepository(pool).AcceptDirectoryCandidate(ctx, AcceptDirectoryInput{
		ProposalID:         mention.ProposalID,
		DirectoryProfileID: snapshotID,
	})
	if err == nil ||
		!strings.Contains(err.Error(), "directory identity conflict") ||
		strings.Contains(err.Error(), sharedSubject) {
		t.Fatalf("conflicting directory acceptance error = %v, want bounded reviewable conflict", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `
		SELECT status FROM stacks.resolution_proposals WHERE id = $1`,
		mention.ProposalID,
	).Scan(&status); err != nil {
		t.Fatalf("load conflicted proposal status: %v", err)
	}
	decisions := countRows(t, pool, `
		SELECT count(*) FROM stacks.resolution_decisions WHERE proposal_id = $1`,
		mention.ProposalID,
	)
	entitiesAfter := countRows(t, pool, `SELECT count(*) FROM stacks.entities`)
	if status != "pending" || decisions != 0 || entitiesAfter != entitiesBefore {
		t.Fatalf(
			"conflict status/decisions/entities before-after = %q/%d/%d-%d, want pending/0/unchanged",
			status,
			decisions,
			entitiesBefore,
			entitiesAfter,
		)
	}
}

func TestDirectoryReviewCorrectionCopiesEvidenceAndSupersedesAuthority(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	_, mention := createDirectoryPendingMentionFixture(t, pool, "review-correction")
	profile := syntheticDirectoryProfile(
		"review-correction-"+mention.MentionID,
		mention.ProposedEmail,
		time.Time{},
	)
	snapshotID := persistDirectoryNameCandidateForReview(t, pool, mention, profile)
	repository := NewEntityRepository(pool)
	first, err := repository.CreateEntity(ctx, EntityInput{
		ID:          uuid.NewString(),
		Kind:        string(entity.KindPerson),
		DisplayName: "Synthetic Review Owner A",
	})
	if err != nil {
		t.Fatalf("create first review owner: %v", err)
	}
	second, err := repository.CreateEntity(ctx, EntityInput{
		ID:          uuid.NewString(),
		Kind:        string(entity.KindPerson),
		DisplayName: "Synthetic Review Owner B",
	})
	if err != nil {
		t.Fatalf("create second review owner: %v", err)
	}
	_, accepted, err := repository.AcceptDirectoryCandidate(ctx, AcceptDirectoryInput{
		ProposalID:         mention.ProposalID,
		DirectoryProfileID: snapshotID,
		EntityID:           first.ID,
	})
	if err != nil {
		t.Fatalf("accept directory candidate for first owner: %v", err)
	}

	replacement, err := repository.CorrectReviewDecision(ctx, accepted.ID, ResolutionDecisionInput{
		Outcome:  ResolutionOutcomeAccepted,
		EntityID: second.ID,
	})
	if err != nil {
		t.Fatalf("correct directory-backed decision: %v", err)
	}
	if replacement.SupersedesID != accepted.ID {
		t.Fatalf("replacement supersedes %q, want %q", replacement.SupersedesID, accepted.ID)
	}

	var assertionHistory, effectiveProviderOwners, effectiveSecondAliases int
	if err := pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*)
		     FROM stacks.entity_directory_identity_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision
		       ON decision.id = assertion.decision_id
		     WHERE decision.proposal_id = $1
		       AND assertion.snapshot_id = $2),
		    (SELECT count(DISTINCT assertion.entity_id)
		     FROM stacks.entity_directory_identity_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision
		       ON decision.id = assertion.decision_id
		     WHERE assertion.provider = $3
		       AND assertion.provider_subject_id = $4
		       AND decision.superseded_by_id IS NULL
		       AND decision.currently_admissible
		       AND assertion.entity_id = $5),
		    (SELECT count(*)
		     FROM stacks.entity_alias_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision
		       ON decision.id = assertion.decision_id
		     WHERE decision.proposal_id = $1
		       AND decision.superseded_by_id IS NULL
		       AND assertion.entity_id = $5
		       AND assertion.alias_type = 'name'
		       AND assertion.normalized_value = $6)`,
		mention.ProposalID,
		snapshotID,
		profile.Provider,
		profile.SubjectID,
		second.ID,
		mention.NormalizedName,
	).Scan(
		&assertionHistory,
		&effectiveProviderOwners,
		&effectiveSecondAliases,
	); err != nil {
		t.Fatalf("load corrected directory authority: %v", err)
	}
	if assertionHistory != 2 ||
		effectiveProviderOwners != 1 ||
		effectiveSecondAliases != 1 {
		t.Fatalf(
			"correction assertion history/effective provider owner/effective name alias = %d/%d/%d, want 2/1/1",
			assertionHistory,
			effectiveProviderOwners,
			effectiveSecondAliases,
		)
	}
	snapshots := snapshotsForTest(t, NewIngestionRepository(pool))
	assertSnapshotAlias(t, snapshots, first.ID, mention.NormalizedName, false)
	assertSnapshotAlias(t, snapshots, second.ID, mention.NormalizedName, true)
}

func TestDirectoryReviewCorrectionAndPersistUseOneAuthorityLockOrder(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, mention := createDirectoryPendingMentionFixture(t, pool, "review-correction-lock-order")
	profile := syntheticDirectoryProfile(
		"review-correction-lock-order-"+mention.MentionID,
		mention.ProposedEmail,
		time.Time{},
	)
	snapshotID := persistDirectoryNameCandidateForReview(t, pool, mention, profile)
	entityRepository := NewEntityRepository(pool)
	first, err := entityRepository.CreateEntity(ctx, EntityInput{
		ID:          uuid.NewString(),
		Kind:        string(entity.KindPerson),
		DisplayName: "Synthetic Lock Order Owner A",
	})
	if err != nil {
		t.Fatalf("create first lock-order owner: %v", err)
	}
	second, err := entityRepository.CreateEntity(ctx, EntityInput{
		ID:          uuid.NewString(),
		Kind:        string(entity.KindPerson),
		DisplayName: "Synthetic Lock Order Owner B",
	})
	if err != nil {
		t.Fatalf("create second lock-order owner: %v", err)
	}
	_, accepted, err := entityRepository.AcceptDirectoryCandidate(ctx, AcceptDirectoryInput{
		ProposalID:         mention.ProposalID,
		DirectoryProfileID: snapshotID,
		EntityID:           first.ID,
	})
	if err != nil {
		t.Fatalf("accept lock-order directory candidate: %v", err)
	}

	persistRepository := NewDirectoryRepository(pool)
	persistLocked := make(chan struct{})
	releasePersist := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releasePersist) })
	}
	defer release()
	persistRepository.testHooks.afterAuthorityLocks = func() error {
		close(persistLocked)
		<-releasePersist
		return nil
	}
	type persistCallResult struct {
		result directory.PersistResult
		err    error
	}
	persistResult := make(chan persistCallResult, 1)
	go func() {
		result, callErr := persistRepository.Persist(
			ctx,
			matchedDirectoryPersistInput(
				mention,
				profile,
				time.Date(2026, time.July, 24, 13, 0, 0, 0, time.UTC),
			),
		)
		persistResult <- persistCallResult{result: result, err: callErr}
	}()
	<-persistLocked

	applicationName := "stacks-correction-lock-order-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	correctionPool := openNamedIntegrationDatabase(t, pool, applicationName)
	type correctionCallResult struct {
		decision ResolutionDecision
		err      error
	}
	correctionResult := make(chan correctionCallResult, 1)
	correctionDone := make(chan struct{})
	go func() {
		defer close(correctionDone)
		decision, callErr := NewEntityRepository(correctionPool).CorrectReviewDecision(
			ctx,
			accepted.ID,
			ResolutionDecisionInput{
				Outcome:  ResolutionOutcomeAccepted,
				EntityID: second.ID,
			},
		)
		correctionResult <- correctionCallResult{decision: decision, err: callErr}
	}()
	if !waitForNamedBackendLockOrCompletion(
		t,
		pool,
		applicationName,
		correctionDone,
	) {
		release()
		<-persistResult
		correction := <-correctionResult
		t.Fatalf("correction completed before lock-order boundary: %v", correction.err)
	}
	release()

	persisted := <-persistResult
	correction := <-correctionResult
	if persisted.err != nil || correction.err != nil {
		t.Fatalf(
			"ordered persistence/correction errors = %v / %v, want both complete",
			persisted.err,
			correction.err,
		)
	}
	if correction.decision.SupersedesID != accepted.ID ||
		correction.decision.EntityID != second.ID {
		t.Fatalf(
			"ordered correction = %#v, want replacement of %q for %q",
			correction.decision,
			accepted.ID,
			second.ID,
		)
	}
	var history, effectiveDecisions, effectiveProviderOwners int
	if err := pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*)
		     FROM stacks.resolution_decisions
		     WHERE proposal_id = $1),
		    (SELECT count(*)
		     FROM stacks.resolution_decisions
		     WHERE proposal_id = $1
		       AND superseded_by_id IS NULL),
		    (SELECT count(DISTINCT assertion.entity_id)
		     FROM stacks.entity_directory_identity_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision
		       ON decision.id = assertion.decision_id
		     WHERE assertion.provider = $2
		       AND assertion.provider_subject_id = $3
		       AND assertion.entity_id = $4
		       AND decision.superseded_by_id IS NULL
		       AND decision.currently_admissible)`,
		mention.ProposalID,
		profile.Provider,
		profile.SubjectID,
		second.ID,
	).Scan(&history, &effectiveDecisions, &effectiveProviderOwners); err != nil {
		t.Fatalf("load lock-order integrity: %v", err)
	}
	if history != 2 || effectiveDecisions != 1 || effectiveProviderOwners != 1 {
		t.Fatalf(
			"lock-order history/effective decisions/provider owners = %d/%d/%d, want 2/1/1",
			history,
			effectiveDecisions,
			effectiveProviderOwners,
		)
	}
}

func TestDirectoryReviewerEmailVerificationAddsProviderEvidence(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	_, mention := createDirectoryPendingMentionFixture(t, pool, "reviewer-email-match")
	profile := syntheticDirectoryProfile(
		"reviewer-email-match-"+mention.MentionID,
		mention.ProposedEmail,
		time.Time{},
	)
	verification := matchedReviewerVerification(mention.ProposedEmail, profile)
	storedEntity, decision, err := NewEntityRepository(pool).CreateReviewPerson(
		ctx,
		CreateReviewPersonInput{
			ProposalID:  mention.ProposalID,
			EntityID:    uuid.NewString(),
			Kind:        string(entity.KindPerson),
			DisplayName: "Synthetic Person",
			Aliases: []AliasInput{
				{
					NormalizedValue: mention.NormalizedName,
					Type:            string(entity.AliasTypeName),
				},
				{
					NormalizedValue: mention.ProposedEmail,
					Type:            string(entity.AliasTypeEmail),
				},
			},
			DirectoryVerification: &verification,
		},
	)
	if err != nil {
		t.Fatalf("create review person with directory verification: %v", err)
	}
	if decision.Outcome != ResolutionOutcomeCreated ||
		decision.EntityID != storedEntity.ID {
		t.Fatalf("verified review creation = entity %#v decision %#v, want created decision", storedEntity, decision)
	}
	var attempts, matches, providerAssertions, nameAliases, emailAliases int
	var emailEvidence string
	if err := pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*)
		     FROM stacks.directory_lookup_attempts
		     WHERE mention_id = $1),
		    (SELECT count(*)
		     FROM stacks.directory_lookup_matches AS match
		     JOIN stacks.directory_lookup_attempts AS attempt
		       ON attempt.id = match.lookup_attempt_id
		     WHERE attempt.mention_id = $1),
		    (SELECT count(*)
		     FROM stacks.entity_directory_identity_assertions
		     WHERE decision_id = $2
		       AND entity_id = $3),
		    (SELECT count(*)
		     FROM stacks.entity_alias_assertions
		     WHERE decision_id = $2
		       AND alias_type = 'name'
		       AND normalized_value = $4),
		    (SELECT count(*)
		     FROM stacks.entity_alias_assertions
		     WHERE decision_id = $2
		       AND alias_type = 'email'
		       AND normalized_value = $5),
		    (SELECT email_evidence
		     FROM stacks.directory_lookup_attempts
		     WHERE mention_id = $1
		     ORDER BY recorded_at DESC
		     LIMIT 1)`,
		mention.MentionID,
		decision.ID,
		storedEntity.ID,
		mention.NormalizedName,
		mention.ProposedEmail,
	).Scan(
		&attempts,
		&matches,
		&providerAssertions,
		&nameAliases,
		&emailAliases,
		&emailEvidence,
	); err != nil {
		t.Fatalf("load reviewer-email directory evidence: %v", err)
	}
	if attempts != 1 ||
		matches != 1 ||
		providerAssertions != 1 ||
		nameAliases != 1 ||
		emailAliases != 1 ||
		emailEvidence != string(entity.EmailEvidenceReviewerSupplied) {
		t.Fatalf(
			"reviewer email attempts/matches/provider/name/email/evidence = %d/%d/%d/%d/%d/%q, want 1/1/1/1/1/reviewer_supplied",
			attempts,
			matches,
			providerAssertions,
			nameAliases,
			emailAliases,
			emailEvidence,
		)
	}
}

func TestDirectoryReviewerEmailConcurrentAcceptedOwnerDowngradesStaleMatch(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	email := "stale.reviewer." + strings.ReplaceAll(uuid.NewString(), "-", "") + "@synthetic.example"
	_, ownerMention := createDirectoryPendingMentionFixtureWithEmail(
		t,
		pool,
		"reviewer-email-stale-owner",
		email,
	)
	ownerProfile := syntheticDirectoryProfile(
		"reviewer-email-stale-owner-"+ownerMention.MentionID,
		email,
		time.Time{},
	)
	_, reviewerMention := createDirectoryPendingMentionFixtureWithEmail(
		t,
		pool,
		"reviewer-email-stale-explicit",
		email,
	)
	reviewerProfile := syntheticDirectoryProfile(
		"reviewer-email-stale-explicit-"+reviewerMention.MentionID,
		email,
		time.Time{},
	)
	staleVerification := matchedReviewerVerification(email, reviewerProfile)

	ownerRepository := NewDirectoryRepository(pool)
	ownerLocked := make(chan struct{})
	releaseOwner := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseOwner) })
	}
	defer release()
	ownerRepository.testHooks.afterAuthorityLocks = func() error {
		close(ownerLocked)
		<-releaseOwner
		return nil
	}
	type ownerCallResult struct {
		result directory.PersistResult
		err    error
	}
	ownerResult := make(chan ownerCallResult, 1)
	go func() {
		result, callErr := ownerRepository.Persist(
			ctx,
			matchedDirectoryPersistInput(
				ownerMention,
				ownerProfile,
				time.Date(2026, time.July, 24, 13, 0, 0, 0, time.UTC),
			),
		)
		ownerResult <- ownerCallResult{result: result, err: callErr}
	}()
	<-ownerLocked

	applicationName := "stacks-reviewer-stale-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	reviewerPool := openNamedIntegrationDatabase(t, pool, applicationName)
	type reviewerCallResult struct {
		storedEntity Entity
		decision     ResolutionDecision
		err          error
	}
	reviewerResult := make(chan reviewerCallResult, 1)
	reviewerDone := make(chan struct{})
	go func() {
		defer close(reviewerDone)
		storedEntity, decision, callErr := NewEntityRepository(reviewerPool).CreateReviewPerson(
			ctx,
			CreateReviewPersonInput{
				ProposalID:  reviewerMention.ProposalID,
				EntityID:    uuid.NewString(),
				Kind:        string(entity.KindPerson),
				DisplayName: "Synthetic Explicit Reviewer Person",
				Aliases: []AliasInput{
					{
						NormalizedValue: reviewerMention.NormalizedName,
						Type:            string(entity.AliasTypeName),
					},
					{
						NormalizedValue: email,
						Type:            string(entity.AliasTypeEmail),
					},
				},
				DirectoryVerification: &staleVerification,
			},
		)
		reviewerResult <- reviewerCallResult{
			storedEntity: storedEntity,
			decision:     decision,
			err:          callErr,
		}
	}()
	if !waitForNamedBackendLockOrCompletion(
		t,
		pool,
		applicationName,
		reviewerDone,
	) {
		release()
		owner := <-ownerResult
		reviewer := <-reviewerResult
		t.Fatalf(
			"stale reviewer write completed before accepted-email authority committed: owner=%v reviewer=%v",
			owner.err,
			reviewer.err,
		)
	}
	release()

	owner := <-ownerResult
	reviewer := <-reviewerResult
	if owner.err != nil || reviewer.err != nil {
		t.Fatalf(
			"concurrent owner/reviewer errors = %v / %v, want additive completion",
			owner.err,
			reviewer.err,
		)
	}
	if !owner.result.AutoResolved ||
		owner.result.EntityID == "" ||
		reviewer.storedEntity.ID == "" ||
		reviewer.decision.Outcome != ResolutionOutcomeCreated ||
		reviewer.decision.EntityID != reviewer.storedEntity.ID ||
		reviewer.storedEntity.ID == owner.result.EntityID {
		t.Fatalf(
			"concurrent owner/reviewer results = %#v / entity %#v decision %#v, want distinct accepted and explicit entities",
			owner.result,
			reviewer.storedEntity,
			reviewer.decision,
		)
	}

	var reviewerAttempts, reviewerMatches, reviewerProviderAssertions int
	var ownerProviderOwners, staleProviderOwners, emailOwners int
	if err := pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*)
		     FROM stacks.directory_lookup_attempts
		     WHERE mention_id = $1),
		    (SELECT count(*)
		     FROM stacks.directory_lookup_matches AS match
		     JOIN stacks.directory_lookup_attempts AS attempt
		       ON attempt.id = match.lookup_attempt_id
		     WHERE attempt.mention_id = $1),
		    (SELECT count(*)
		     FROM stacks.entity_directory_identity_assertions
		     WHERE decision_id = $2),
		    (SELECT count(DISTINCT assertion.entity_id)
		     FROM stacks.entity_directory_identity_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision
		       ON decision.id = assertion.decision_id
		     WHERE assertion.provider = $3
		       AND assertion.provider_subject_id = $4
		       AND decision.superseded_by_id IS NULL
		       AND decision.currently_admissible),
		    (SELECT count(DISTINCT assertion.entity_id)
		     FROM stacks.entity_directory_identity_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision
		       ON decision.id = assertion.decision_id
		     WHERE assertion.provider = $3
		       AND assertion.provider_subject_id = $5
		       AND decision.superseded_by_id IS NULL
		       AND decision.currently_admissible),
		    (SELECT count(DISTINCT assertion.entity_id)
		     FROM stacks.entity_alias_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision
		       ON decision.id = assertion.decision_id
		     WHERE assertion.alias_type = 'email'
		       AND assertion.normalized_value = $6
		       AND decision.superseded_by_id IS NULL
		       AND decision.currently_admissible)`,
		reviewerMention.MentionID,
		reviewer.decision.ID,
		ownerProfile.Provider,
		ownerProfile.SubjectID,
		reviewerProfile.SubjectID,
		email,
	).Scan(
		&reviewerAttempts,
		&reviewerMatches,
		&reviewerProviderAssertions,
		&ownerProviderOwners,
		&staleProviderOwners,
		&emailOwners,
	); err != nil {
		t.Fatalf("load stale reviewer authority result: %v", err)
	}
	if reviewerAttempts != 1 ||
		reviewerMatches != 1 ||
		reviewerProviderAssertions != 0 ||
		ownerProviderOwners != 1 ||
		staleProviderOwners != 0 ||
		emailOwners != 2 {
		t.Fatalf(
			"stale reviewer attempts/matches/assertions/owner provider/stale provider/email owners = %d/%d/%d/%d/%d/%d, want 1/1/0/1/0/2",
			reviewerAttempts,
			reviewerMatches,
			reviewerProviderAssertions,
			ownerProviderOwners,
			staleProviderOwners,
			emailOwners,
		)
	}
}

func TestCreateReviewPersonParticipatesInAcceptedEmailAuthorityLock(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	email := "reviewer.email.lock." + strings.ReplaceAll(uuid.NewString(), "-", "") + "@synthetic.example"
	_, mention := createDirectoryPendingMentionFixtureWithEmail(
		t,
		pool,
		"reviewer-email-authority-lock",
		email,
	)
	authorityTransaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin accepted-email authority transaction: %v", err)
	}
	defer authorityTransaction.Rollback(context.Background()) //nolint:errcheck // committed transactions are already closed.
	if _, err := authorityTransaction.Exec(ctx, `
		SELECT pg_advisory_xact_lock(
			hashtext('directory_email'),
			hashtext($1)
		)`,
		email,
	); err != nil {
		t.Fatalf("hold accepted-email authority lock: %v", err)
	}

	applicationName := "stacks-reviewer-email-lock-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	reviewerPool := openNamedIntegrationDatabase(t, pool, applicationName)
	type createCallResult struct {
		storedEntity Entity
		decision     ResolutionDecision
		err          error
	}
	result := make(chan createCallResult, 1)
	completed := make(chan struct{})
	go func() {
		defer close(completed)
		storedEntity, decision, callErr := NewEntityRepository(reviewerPool).CreateReviewPerson(
			ctx,
			CreateReviewPersonInput{
				ProposalID:  mention.ProposalID,
				EntityID:    uuid.NewString(),
				Kind:        string(entity.KindPerson),
				DisplayName: "Synthetic Email Lock Reviewer",
				Aliases: []AliasInput{{
					NormalizedValue: email,
					Type:            string(entity.AliasTypeEmail),
				}},
			},
		)
		result <- createCallResult{
			storedEntity: storedEntity,
			decision:     decision,
			err:          callErr,
		}
	}()
	if !waitForNamedBackendLockOrCompletion(
		t,
		pool,
		applicationName,
		completed,
	) {
		_ = authorityTransaction.Rollback(context.Background())
		created := <-result
		t.Fatalf(
			"explicit email creation bypassed accepted-email authority lock: %v",
			created.err,
		)
	}
	if err := authorityTransaction.Commit(ctx); err != nil {
		t.Fatalf("release accepted-email authority lock: %v", err)
	}
	created := <-result
	if created.err != nil ||
		created.storedEntity.ID == "" ||
		created.decision.EntityID != created.storedEntity.ID ||
		created.decision.Outcome != ResolutionOutcomeCreated {
		t.Fatalf(
			"explicit email creation after authority lock = entity %#v decision %#v error %v, want created",
			created.storedEntity,
			created.decision,
			created.err,
		)
	}
}

func TestDirectoryReviewerEmailUnavailableOrNoMatchPreservesExplicitDecision(t *testing.T) {
	for _, outcome := range []entity.DirectoryOutcome{
		entity.DirectoryNoMatch,
		entity.DirectoryUnavailable,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			pool := openIntegrationDatabase(t)
			ctx := context.Background()
			_, mention := createDirectoryPendingMentionFixture(
				t,
				pool,
				"reviewer-email-"+string(outcome),
			)
			recordedAt := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
			verification := directory.ReviewerVerification{
				Query: entity.DirectoryQuery{
					Kind:          entity.DirectoryQueryEmail,
					Email:         mention.ProposedEmail,
					EmailEvidence: entity.EmailEvidenceReviewerSupplied,
				},
				Lookup:       directory.LookupResult{Outcome: outcome},
				Evaluation:   entity.DirectoryEvaluation{Outcome: outcome},
				AttemptCount: 1,
				RecordedAt:   recordedAt,
			}
			if outcome == entity.DirectoryUnavailable {
				retryAfter := recordedAt.Add(time.Minute)
				verification.RetryAfter = &retryAfter
			}
			storedEntity, decision, err := NewEntityRepository(pool).CreateReviewPerson(
				ctx,
				CreateReviewPersonInput{
					ProposalID:  mention.ProposalID,
					EntityID:    uuid.NewString(),
					Kind:        string(entity.KindPerson),
					DisplayName: "Synthetic Explicit Reviewer Person",
					Aliases: []AliasInput{{
						NormalizedValue: mention.ProposedEmail,
						Type:            string(entity.AliasTypeEmail),
					}},
					DirectoryVerification: &verification,
				},
			)
			if err != nil {
				t.Fatalf("create explicit reviewer person after %s: %v", outcome, err)
			}
			if storedEntity.ID == "" ||
				decision.Outcome != ResolutionOutcomeCreated ||
				decision.EntityID != storedEntity.ID {
				t.Fatalf("explicit reviewer result = entity %#v decision %#v, want created", storedEntity, decision)
			}
			var attempts, providerAssertions, emailAliases int
			if err := pool.QueryRow(ctx, `
				SELECT
				    (SELECT count(*)
				     FROM stacks.directory_lookup_attempts
				     WHERE mention_id = $1
				       AND outcome = $2),
				    (SELECT count(*)
				     FROM stacks.entity_directory_identity_assertions
				     WHERE decision_id = $3),
				    (SELECT count(*)
				     FROM stacks.entity_alias_assertions
				     WHERE decision_id = $3
				       AND alias_type = 'email'
				       AND normalized_value = $4)`,
				mention.MentionID,
				string(outcome),
				decision.ID,
				mention.ProposedEmail,
			).Scan(&attempts, &providerAssertions, &emailAliases); err != nil {
				t.Fatalf("load additive reviewer result after %s: %v", outcome, err)
			}
			if attempts != 1 || providerAssertions != 0 || emailAliases != 1 {
				t.Fatalf(
					"%s attempts/provider assertions/email aliases = %d/%d/%d, want 1/0/1",
					outcome,
					attempts,
					providerAssertions,
					emailAliases,
				)
			}
		})
	}
}

func TestDirectoryReviewerEmailProviderConflictMakesNoWrites(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	_, ownerMention := createDirectoryPendingMentionFixture(t, pool, "reviewer-email-owner")
	ownerID := uuid.NewString()
	_, ownerDecision, err := NewEntityRepository(pool).CreateReviewPerson(ctx, CreateReviewPersonInput{
		ProposalID:  ownerMention.ProposalID,
		EntityID:    ownerID,
		Kind:        string(entity.KindPerson),
		DisplayName: "Synthetic Reviewer Email Owner",
		Aliases: []AliasInput{{
			NormalizedValue: ownerMention.ProposedEmail,
			Type:            string(entity.AliasTypeEmail),
		}},
	})
	if err != nil {
		t.Fatalf("create reviewer-email provider owner: %v", err)
	}
	sharedSubject := "synthetic-reviewer-email-conflict-" + uuid.NewString()
	ownerProfile := syntheticDirectoryProfile(
		"reviewer-email-owner-"+ownerMention.MentionID,
		ownerMention.ProposedEmail,
		time.Time{},
	)
	ownerProfile.SubjectID = sharedSubject
	ownerSnapshotID, ownerAttemptID := seedDirectoryProfileEvidence(
		t,
		pool,
		ownerMention.MentionID,
		ownerProfile,
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO stacks.entity_directory_identity_assertions
			(decision_id, entity_id, lookup_attempt_id, snapshot_id,
			 provider, provider_subject_id, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		ownerDecision.ID,
		ownerID,
		ownerAttemptID,
		ownerSnapshotID,
		ownerProfile.Provider,
		ownerProfile.SubjectID,
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("seed reviewer-email provider owner: %v", err)
	}

	_, mention := createDirectoryPendingMentionFixture(t, pool, "reviewer-email-conflict")
	profile := syntheticDirectoryProfile(
		"reviewer-email-conflict-"+mention.MentionID,
		mention.ProposedEmail,
		time.Time{},
	)
	profile.SubjectID = sharedSubject
	verification := matchedReviewerVerification(mention.ProposedEmail, profile)
	verification.Lookup.Outcome = entity.DirectoryReview
	verification.Evaluation = entity.DirectoryEvaluation{
		Outcome:    entity.DirectoryReview,
		Candidates: []entity.DirectoryProfile{profile},
	}
	entitiesBefore := countRows(t, pool, `SELECT count(*) FROM stacks.entities`)

	_, _, err = NewEntityRepository(pool).CreateReviewPerson(ctx, CreateReviewPersonInput{
		ProposalID:  mention.ProposalID,
		EntityID:    uuid.NewString(),
		Kind:        string(entity.KindPerson),
		DisplayName: "Synthetic Conflicting Reviewer Person",
		Aliases: []AliasInput{{
			NormalizedValue: mention.ProposedEmail,
			Type:            string(entity.AliasTypeEmail),
		}},
		DirectoryVerification: &verification,
	})
	if err == nil ||
		!strings.Contains(err.Error(), "directory identity conflict") ||
		strings.Contains(err.Error(), sharedSubject) {
		t.Fatalf("reviewer-email conflict error = %v, want bounded conflict", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `
		SELECT status FROM stacks.resolution_proposals WHERE id = $1`,
		mention.ProposalID,
	).Scan(&status); err != nil {
		t.Fatalf("load reviewer-email conflict proposal: %v", err)
	}
	decisions := countRows(t, pool, `
		SELECT count(*) FROM stacks.resolution_decisions WHERE proposal_id = $1`,
		mention.ProposalID,
	)
	attempts := countRows(t, pool, `
		SELECT count(*) FROM stacks.directory_lookup_attempts WHERE mention_id = $1`,
		mention.MentionID,
	)
	entitiesAfter := countRows(t, pool, `SELECT count(*) FROM stacks.entities`)
	if status != "pending" ||
		decisions != 0 ||
		attempts != 0 ||
		entitiesAfter != entitiesBefore {
		t.Fatalf(
			"reviewer-email conflict status/decisions/attempts/entities = %q/%d/%d/%d-%d, want pending/0/0/unchanged",
			status,
			decisions,
			attempts,
			entitiesBefore,
			entitiesAfter,
		)
	}
}

func TestDirectoryReviewLifecycleUniqueNameResolvesThenDuplicateIsAmbiguous(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	repository := NewEntityRepository(pool)
	ingestion := NewIngestionRepository(pool)
	surface := "Synthetic Lifecycle " + strings.ReplaceAll(uuid.NewString(), "-", "")

	_, firstMention := createDirectoryPendingMentionFixtureWithSurfaceEmail(
		t,
		pool,
		"lifecycle-first",
		surface,
		"lifecycle.first."+strings.ReplaceAll(uuid.NewString(), "-", "")+"@synthetic.example",
	)
	firstProfile := syntheticDirectoryProfile(
		"lifecycle-first-"+firstMention.MentionID,
		firstMention.ProposedEmail,
		time.Time{},
	)
	firstSnapshotID := persistDirectoryNameCandidateForReview(
		t,
		pool,
		firstMention,
		firstProfile,
	)
	firstEntity, _, err := repository.AcceptDirectoryCandidate(ctx, AcceptDirectoryInput{
		ProposalID:         firstMention.ProposalID,
		DirectoryProfileID: firstSnapshotID,
	})
	if err != nil {
		t.Fatalf("accept first lifecycle directory candidate: %v", err)
	}
	snapshots, err := ingestion.EntitySnapshots(ctx)
	if err != nil {
		t.Fatalf("load first lifecycle snapshots: %v", err)
	}
	resolved := (entity.Resolver{}).Resolve(
		entity.Mention{Name: firstMention.Surface},
		snapshots,
	)
	if !resolved.AutoResolved || resolved.EntityID != firstEntity.ID {
		t.Fatalf("unique accepted name resolution = %#v, want entity %q", resolved, firstEntity.ID)
	}

	_, secondMention := createDirectoryPendingMentionFixtureWithSurfaceEmail(
		t,
		pool,
		"lifecycle-second",
		surface,
		"lifecycle.second."+strings.ReplaceAll(uuid.NewString(), "-", "")+"@synthetic.example",
	)
	secondProfile := syntheticDirectoryProfile(
		"lifecycle-second-"+secondMention.MentionID,
		secondMention.ProposedEmail,
		time.Time{},
	)
	secondSnapshotID := persistDirectoryNameCandidateForReview(
		t,
		pool,
		secondMention,
		secondProfile,
	)
	secondEntity, _, err := repository.AcceptDirectoryCandidate(ctx, AcceptDirectoryInput{
		ProposalID:         secondMention.ProposalID,
		DirectoryProfileID: secondSnapshotID,
	})
	if err != nil {
		t.Fatalf("accept second lifecycle directory candidate: %v", err)
	}
	if secondEntity.ID == firstEntity.ID {
		t.Fatalf("second lifecycle entity ID = %q, want distinct person", secondEntity.ID)
	}
	snapshots, err = ingestion.EntitySnapshots(ctx)
	if err != nil {
		t.Fatalf("load duplicate-name lifecycle snapshots: %v", err)
	}
	ambiguous := (entity.Resolver{}).Resolve(
		entity.Mention{Name: firstMention.Surface},
		snapshots,
	)
	if ambiguous.AutoResolved || ambiguous.EntityID != "" {
		t.Fatalf("duplicate accepted name resolution = %#v, want ambiguity", ambiguous)
	}
	assertSnapshotAlias(
		t,
		snapshots,
		firstEntity.ID,
		firstMention.NormalizedName,
		true,
	)
	assertSnapshotAlias(
		t,
		snapshots,
		secondEntity.ID,
		secondMention.NormalizedName,
		true,
	)
}

func TestDirectoryReviewRejectionPreservesMaskedCandidateAndLookupEvidence(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	_, mention := createDirectoryPendingMentionFixture(t, pool, "review-rejection")
	profile := syntheticDirectoryProfile(
		"review-rejection-"+mention.MentionID,
		mention.ProposedEmail,
		time.Time{},
	)
	snapshotID := persistDirectoryNameCandidateForReview(t, pool, mention, profile)
	repository := NewEntityRepository(pool)

	pending, err := repository.ShowResolutionProposalDetail(ctx, mention.ProposalID)
	if err != nil {
		t.Fatalf("show pending directory proposal: %v", err)
	}
	if len(pending.Candidates) != 1 {
		t.Fatalf("pending directory candidate count = %d, want 1", len(pending.Candidates))
	}
	candidate := pending.Candidates[0]
	if candidate.DirectoryProfileID != snapshotID ||
		candidate.DisplayName != profile.DisplayName ||
		candidate.MaskedEmail != "s***@synthetic.example" ||
		candidate.Source != string(entity.DirectorySourceDomainProfile) {
		t.Fatalf("pending directory candidate = %#v, want bounded masked projection", candidate)
	}
	projectedCandidate := fmt.Sprint(candidate)
	if strings.Contains(projectedCandidate, mention.ProposedEmail) ||
		strings.Contains(projectedCandidate, profile.SubjectID) {
		t.Fatalf("pending directory candidate leaked private provider identity: %q", projectedCandidate)
	}

	if _, err := repository.RecordReviewDecision(ctx, ResolutionDecisionInput{
		ProposalID: mention.ProposalID,
		Outcome:    ResolutionOutcomeRejected,
	}); err != nil {
		t.Fatalf("reject directory review proposal: %v", err)
	}
	rejected, err := repository.ShowResolutionProposalDetail(ctx, mention.ProposalID)
	if err != nil {
		t.Fatalf("show rejected directory proposal: %v", err)
	}
	if !reflect.DeepEqual(rejected.Candidates, pending.Candidates) {
		t.Fatalf("rejected directory candidates = %#v, want preserved %#v", rejected.Candidates, pending.Candidates)
	}
	var attempts, matches, snapshots, candidates int
	if err := pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*)
		     FROM stacks.directory_lookup_attempts
		     WHERE mention_id = $1),
		    (SELECT count(*)
		     FROM stacks.directory_lookup_matches AS match
		     JOIN stacks.directory_lookup_attempts AS attempt
		       ON attempt.id = match.lookup_attempt_id
		     WHERE attempt.mention_id = $1
		       AND match.snapshot_id = $2),
		    (SELECT count(*)
		     FROM stacks.directory_profile_snapshots
		     WHERE id = $2),
		    (SELECT count(*)
		     FROM stacks.resolution_candidates
		     WHERE proposal_id = $3
		       AND directory_profile_snapshot_id = $2)`,
		mention.MentionID,
		snapshotID,
		mention.ProposalID,
	).Scan(&attempts, &matches, &snapshots, &candidates); err != nil {
		t.Fatalf("load rejected directory evidence: %v", err)
	}
	if attempts != 1 || matches != 1 || snapshots != 1 || candidates != 1 {
		t.Fatalf(
			"rejected attempt/match/snapshot/candidate counts = %d/%d/%d/%d, want 1/1/1/1",
			attempts,
			matches,
			snapshots,
			candidates,
		)
	}
}

func TestDirectoryPersistBoundedProviderFailureStoresNoProfiles(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	_, mention := createDirectoryPendingMentionFixture(t, pool, "bounded-failure")
	recordedAt := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	retryAfter := recordedAt.Add(5 * time.Minute)
	input := directory.PersistInput{
		Mention: mention,
		Query: entity.DirectoryQuery{
			Kind: entity.DirectoryQueryName, Name: mention.NormalizedName,
			EmailEvidence: entity.EmailEvidenceNone,
		},
		Lookup:       directory.LookupResult{Outcome: entity.DirectoryUnavailable},
		AttemptCount: 2,
		RecordedAt:   recordedAt,
		RetryAfter:   &retryAfter,
	}
	if _, err := NewDirectoryRepository(pool).Persist(ctx, input); err != nil {
		t.Fatalf("persist bounded directory provider failure: %v", err)
	}
	var attempts, matches int
	if err := pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*) FROM stacks.directory_lookup_attempts WHERE mention_id = $1),
		    (SELECT count(*)
		     FROM stacks.directory_lookup_matches AS match
		     JOIN stacks.directory_lookup_attempts AS attempt ON attempt.id = match.lookup_attempt_id
		     WHERE attempt.mention_id = $1)`,
		mention.MentionID).Scan(&attempts, &matches); err != nil {
		t.Fatalf("count bounded directory failure evidence: %v", err)
	}
	if attempts != 1 || matches != 0 {
		t.Fatalf("bounded failure attempts/matches = %d/%d, want 1/0", attempts, matches)
	}
}

func TestDirectoryPersistRollsBackAfterInjectedEntityFailure(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	_, mention := createDirectoryPendingMentionFixture(t, pool, "rollback")
	profile := syntheticDirectoryProfile("rollback-"+mention.MentionID, mention.ProposedEmail, time.Time{})
	profile.DisplayName = "Synthetic Rollback " + mention.MentionID
	input := matchedDirectoryPersistInput(
		mention,
		profile,
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
	)
	repository := NewDirectoryRepository(pool)
	repository.testHooks.afterEntityCreated = func() error {
		return errors.New("synthetic injected transaction failure")
	}

	if _, err := repository.Persist(ctx, input); err == nil ||
		!strings.Contains(err.Error(), "injected transaction failure") {
		t.Fatalf("persist with injected failure error = %v, want bounded synthetic failure", err)
	}
	var snapshots, attempts, entities, decisions, aliases, links int
	if err := pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*) FROM stacks.directory_profile_snapshots
		     WHERE provider = $1 AND provider_subject_id = $2),
		    (SELECT count(*) FROM stacks.directory_lookup_attempts WHERE mention_id = $3),
		    (SELECT count(*) FROM stacks.entities WHERE display_name = $4),
		    (SELECT count(*) FROM stacks.resolution_decisions WHERE proposal_id = $5),
		    (SELECT count(*)
		     FROM stacks.entity_alias_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision ON decision.id = assertion.decision_id
		     WHERE decision.proposal_id = $5),
		    (SELECT count(*)
		     FROM stacks.entity_directory_identity_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision ON decision.id = assertion.decision_id
		     WHERE decision.proposal_id = $5)`,
		profile.Provider, profile.SubjectID, mention.MentionID, profile.DisplayName,
		mention.ProposalID).Scan(
		&snapshots, &attempts, &entities, &decisions, &aliases, &links,
	); err != nil {
		t.Fatalf("count rolled-back directory rows: %v", err)
	}
	if snapshots+attempts+entities+decisions+aliases+links != 0 {
		t.Fatalf("rolled-back snapshot/attempt/entity/decision/alias/link counts = %d/%d/%d/%d/%d/%d, want all zero",
			snapshots, attempts, entities, decisions, aliases, links)
	}
}

func TestDirectoryPersistConcurrentExactEmailCreatesOneAuthority(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	email := "concurrent.owner." + strings.ReplaceAll(uuid.NewString(), "-", "") + "@synthetic.example"
	_, firstMention := createDirectoryPendingMentionFixtureWithEmail(t, pool, "concurrent-first", email)
	_, secondMention := createDirectoryPendingMentionFixtureWithEmail(t, pool, "concurrent-second", email)
	profile := syntheticDirectoryProfile("concurrent-"+uuid.NewString(), email, time.Time{})
	secondProfile := profile
	secondProfile.DisplayName = profile.DisplayName + " Updated"
	recordedAt := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	firstRepository := NewDirectoryRepository(pool)
	secondRepository := NewDirectoryRepository(pool)
	firstLocked := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstRepository.testHooks.afterAuthorityLocks = func() error {
		close(firstLocked)
		<-releaseFirst
		return nil
	}
	secondRepository.testHooks.beforeAuthorityLocks = func() {
		close(secondStarted)
	}
	type persistResult struct {
		result directory.PersistResult
		err    error
	}
	firstResult := make(chan persistResult, 1)
	secondResult := make(chan persistResult, 1)
	go func() {
		result, err := firstRepository.Persist(ctx, matchedDirectoryPersistInput(firstMention, profile, recordedAt))
		firstResult <- persistResult{result: result, err: err}
	}()
	<-firstLocked
	go func() {
		result, err := secondRepository.Persist(ctx, matchedDirectoryPersistInput(secondMention, secondProfile, recordedAt.Add(time.Second)))
		secondResult <- persistResult{result: result, err: err}
	}()
	<-secondStarted
	close(releaseFirst)

	first := <-firstResult
	second := <-secondResult
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent directory persistence errors = %v / %v", first.err, second.err)
	}
	if !first.result.AutoResolved || !second.result.AutoResolved ||
		first.result.EntityID == "" || second.result.EntityID != first.result.EntityID {
		t.Fatalf("concurrent directory results = %#v / %#v, want one shared authority", first.result, second.result)
	}
	var entities, emailOwners, providerOwners int
	if err := pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(DISTINCT assertion.entity_id)
		     FROM stacks.entity_directory_identity_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision ON decision.id = assertion.decision_id
		     WHERE assertion.provider = $1
		       AND assertion.provider_subject_id = $2
		       AND decision.superseded_by_id IS NULL
		       AND decision.currently_admissible),
		    (SELECT count(DISTINCT assertion.entity_id)
		     FROM stacks.entity_alias_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision ON decision.id = assertion.decision_id
		     WHERE assertion.alias_type = 'email'
		       AND assertion.normalized_value = $3
		       AND decision.superseded_by_id IS NULL
		       AND decision.currently_admissible),
		    (SELECT count(*) FROM stacks.entities
		     WHERE id IN (
		         SELECT assertion.entity_id
		         FROM stacks.entity_directory_identity_assertions AS assertion
		         WHERE assertion.provider = $1
		           AND assertion.provider_subject_id = $2
		     ))`,
		profile.Provider, profile.SubjectID, email).Scan(
		&providerOwners, &emailOwners, &entities,
	); err != nil {
		t.Fatalf("count concurrent directory authority: %v", err)
	}
	if entities != 1 || emailOwners != 1 || providerOwners != 1 {
		t.Fatalf("concurrent entities/email owners/provider owners = %d/%d/%d, want 1/1/1",
			entities, emailOwners, providerOwners)
	}
}

func TestDirectoryPersistDowngradesPostLockAuthorityConflictToReview(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	targetEmail := "conflict.target." + strings.ReplaceAll(uuid.NewString(), "-", "") + "@synthetic.example"
	otherEmail := "conflict.other." + strings.ReplaceAll(uuid.NewString(), "-", "") + "@synthetic.example"

	_, emailOwnerMention := createDirectoryPendingMentionFixtureWithEmail(t, pool, "conflict-email-owner", targetEmail)
	emailOwnerID := uuid.NewString()
	if _, _, err := NewEntityRepository(pool).CreateReviewPerson(ctx, CreateReviewPersonInput{
		ProposalID:  emailOwnerMention.ProposalID,
		EntityID:    emailOwnerID,
		Kind:        string(entity.KindPerson),
		DisplayName: "Synthetic Conflict Email Owner",
		Aliases: []AliasInput{{
			NormalizedValue: targetEmail,
			Type:            string(entity.AliasTypeEmail),
		}},
	}); err != nil {
		t.Fatalf("create synthetic conflict email owner: %v", err)
	}

	_, providerOwnerMention := createDirectoryPendingMentionFixtureWithEmail(t, pool, "conflict-provider-owner", otherEmail)
	providerOwnerID := uuid.NewString()
	_, providerDecision, err := NewEntityRepository(pool).CreateReviewPerson(ctx, CreateReviewPersonInput{
		ProposalID:  providerOwnerMention.ProposalID,
		EntityID:    providerOwnerID,
		Kind:        string(entity.KindPerson),
		DisplayName: "Synthetic Conflict Provider Owner",
		Aliases: []AliasInput{{
			NormalizedValue: otherEmail,
			Type:            string(entity.AliasTypeEmail),
		}},
	})
	if err != nil {
		t.Fatalf("create synthetic conflict provider owner: %v", err)
	}
	sharedSubject := "conflict-shared-" + uuid.NewString()
	providerProfile := syntheticDirectoryProfile(sharedSubject, otherEmail, time.Time{})
	snapshotID, attemptID := seedDirectoryProfileEvidence(t, pool, providerOwnerMention.MentionID, providerProfile)
	if _, err := pool.Exec(ctx, `
		INSERT INTO stacks.entity_directory_identity_assertions
			(decision_id, entity_id, lookup_attempt_id, snapshot_id,
			 provider, provider_subject_id, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		providerDecision.ID,
		providerOwnerID,
		attemptID,
		snapshotID,
		providerProfile.Provider,
		providerProfile.SubjectID,
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("seed synthetic conflicting provider authority: %v", err)
	}

	_, targetMention := createDirectoryPendingMentionFixtureWithEmail(t, pool, "conflict-target", targetEmail)
	targetProfile := syntheticDirectoryProfile(sharedSubject, targetEmail, time.Time{})
	input := matchedDirectoryPersistInput(
		targetMention,
		targetProfile,
		time.Date(2026, time.July, 24, 13, 0, 0, 0, time.UTC),
	)
	result, err := NewDirectoryRepository(pool).Persist(ctx, input)
	if err != nil {
		t.Fatalf("persist conflicting directory authority: %v", err)
	}
	if result != (directory.PersistResult{}) {
		t.Fatalf("conflicting directory result = %#v, want review fallback", result)
	}
	var decisions, candidates int
	if err := pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*) FROM stacks.resolution_decisions WHERE proposal_id = $1),
		    (SELECT count(*) FROM stacks.resolution_candidates
		     WHERE proposal_id = $1
		       AND directory_profile_snapshot_id IS NOT NULL
		       AND reason = 'directory exact email requires review')`,
		targetMention.ProposalID,
	).Scan(&decisions, &candidates); err != nil {
		t.Fatalf("count conflict review fallback: %v", err)
	}
	if decisions != 0 || candidates != 1 {
		t.Fatalf("conflict decisions/review candidates = %d/%d, want 0/1", decisions, candidates)
	}
}

func TestDirectoryPersistDowngradesTriggerEffectiveStaleSourceAuthorityToReview(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	priorEmail := "stale.prior." + strings.ReplaceAll(uuid.NewString(), "-", "") + "@synthetic.example"
	targetEmail := "stale.target." + strings.ReplaceAll(uuid.NewString(), "-", "") + "@synthetic.example"

	_, priorMention := createDirectoryPendingMentionFixtureWithEmail(t, pool, "stale-source-prior", priorEmail)
	priorOwnerID := uuid.NewString()
	_, priorDecision, err := NewEntityRepository(pool).CreateReviewPerson(ctx, CreateReviewPersonInput{
		ProposalID:  priorMention.ProposalID,
		EntityID:    priorOwnerID,
		Kind:        string(entity.KindPerson),
		DisplayName: "Synthetic Stale Source Owner",
		Aliases: []AliasInput{{
			NormalizedValue: priorEmail,
			Type:            string(entity.AliasTypeEmail),
		}},
	})
	if err != nil {
		t.Fatalf("create synthetic stale-source owner: %v", err)
	}
	sharedSubject := "stale-source-shared-" + uuid.NewString()
	priorProfile := syntheticDirectoryProfile(sharedSubject, priorEmail, time.Time{})
	priorSnapshotID, priorAttemptID := seedDirectoryProfileEvidence(
		t,
		pool,
		priorMention.MentionID,
		priorProfile,
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO stacks.entity_directory_identity_assertions
			(decision_id, entity_id, lookup_attempt_id, snapshot_id,
			 provider, provider_subject_id, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		priorDecision.ID,
		priorOwnerID,
		priorAttemptID,
		priorSnapshotID,
		priorProfile.Provider,
		priorProfile.SubjectID,
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("seed synthetic stale-source provider authority: %v", err)
	}
	advanceDirectoryMentionSourceVersion(t, pool, priorMention.MentionID)

	_, targetMention := createDirectoryPendingMentionFixtureWithEmail(t, pool, "stale-source-target", targetEmail)
	targetProfile := syntheticDirectoryProfile(sharedSubject, targetEmail, time.Time{})
	targetProfile.DisplayName = "Synthetic Stale Source Target " + targetMention.MentionID
	result, err := NewDirectoryRepository(pool).Persist(
		ctx,
		matchedDirectoryPersistInput(
			targetMention,
			targetProfile,
			time.Date(2026, time.July, 24, 14, 0, 0, 0, time.UTC),
		),
	)
	if err != nil {
		t.Fatalf("persist against trigger-effective stale-source authority: %v", err)
	}
	if result != (directory.PersistResult{}) {
		t.Fatalf("stale-source authority result = %#v, want review fallback", result)
	}
	var attempts, candidates, decisions, newEntities, providerOwners int
	if err := pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*) FROM stacks.directory_lookup_attempts WHERE mention_id = $1),
		    (SELECT count(*) FROM stacks.resolution_candidates
		     WHERE proposal_id = $2
		       AND directory_profile_snapshot_id IS NOT NULL
		       AND reason = 'directory exact email requires review'),
		    (SELECT count(*) FROM stacks.resolution_decisions WHERE proposal_id = $2),
		    (SELECT count(*) FROM stacks.entities WHERE display_name = $3),
		    (SELECT count(DISTINCT assertion.entity_id)
		     FROM stacks.entity_directory_identity_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision ON decision.id = assertion.decision_id
		     WHERE assertion.provider = $4
		       AND assertion.provider_subject_id = $5
		       AND decision.superseded_by_id IS NULL
		       AND decision.outcome IN ('accepted', 'created')
		       AND decision.currently_admissible)`,
		targetMention.MentionID,
		targetMention.ProposalID,
		targetProfile.DisplayName,
		targetProfile.Provider,
		targetProfile.SubjectID,
	).Scan(
		&attempts,
		&candidates,
		&decisions,
		&newEntities,
		&providerOwners,
	); err != nil {
		t.Fatalf("count stale-source review fallback: %v", err)
	}
	if attempts != 1 || candidates != 1 || decisions != 0 ||
		newEntities != 0 || providerOwners != 1 {
		t.Fatalf(
			"stale-source attempts/candidates/decisions/entities/provider owners = %d/%d/%d/%d/%d, want 1/1/0/0/1",
			attempts,
			candidates,
			decisions,
			newEntities,
			providerOwners,
		)
	}
}

func TestDirectoryPersistRejectsImmutableSnapshotConflict(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	_, mention := createDirectoryPendingMentionFixture(t, pool, "immutable-conflict")
	profile := syntheticDirectoryProfile("immutable-conflict-"+mention.MentionID, mention.ProposedEmail, time.Time{})
	digest, err := directoryProfileDigest(profile)
	if err != nil {
		t.Fatalf("digest synthetic conflicting directory profile: %v", err)
	}
	snapshotID := uuid.NewSHA1(uuid.NameSpaceOID, digest[:]).String()
	if _, err := pool.Exec(ctx, `
		INSERT INTO stacks.directory_profile_snapshots
			(id, provider, provider_subject_id, source_type, display_name,
			 observed_at, recorded_at, digest)
		VALUES ($1, 'google_people', $2, 'domain_profile', $3, NULL, $4, $5)`,
		snapshotID,
		"different-"+profile.SubjectID,
		profile.DisplayName,
		time.Date(2026, time.July, 24, 11, 0, 0, 0, time.UTC),
		digest[:],
	); err != nil {
		t.Fatalf("seed immutable directory snapshot conflict: %v", err)
	}

	_, err = NewDirectoryRepository(pool).Persist(
		ctx,
		matchedDirectoryPersistInput(
			mention,
			profile,
			time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		),
	)
	if err == nil || !strings.Contains(err.Error(), "stored profile snapshot conflicts") {
		t.Fatalf("immutable directory snapshot conflict error = %v, want loud bounded conflict", err)
	}
	if got := countRows(t, pool, `
		SELECT count(*) FROM stacks.directory_lookup_attempts WHERE mention_id = $1`,
		mention.MentionID,
	); got != 0 {
		t.Fatalf("directory attempts after immutable conflict = %d, want rollback", got)
	}
}

func TestModelProviderProvenancePersistsExtractionLeaseWithoutMutatingCompletedCache(t *testing.T) {
	pool := openIntegrationDatabase(t)
	repository := NewIngestionRepository(pool)
	ctx := context.Background()
	version := testDocumentVersion(t, testIdentifier("document-provider-provenance"))
	derivation := testExtractionDerivation(t, version)

	personal, err := repository.PrepareVersion(ctx, version, derivation, modelpolicy.DataModePersonal, 5*time.Minute)
	if err != nil {
		t.Fatalf("prepare personal Bedrock extraction: %v", err)
	}
	var provider, dataMode, region string
	if err := pool.QueryRow(ctx, `
		SELECT model_provider, data_mode, bedrock_region
		FROM stacks.extraction_runs WHERE id = $1`, personal.DerivationID).Scan(&provider, &dataMode, &region); err != nil {
		t.Fatalf("load personal Bedrock extraction provenance: %v", err)
	}
	if provider != string(modelpolicy.ProviderBedrock) || dataMode != string(modelpolicy.DataModePersonal) || region != derivation.Region {
		t.Fatalf("personal Bedrock extraction provenance = %q/%q/%q", provider, dataMode, region)
	}
	if err := repository.CompleteVersion(ctx, ingest.Completion{
		VersionID: personal.ID, DerivationID: personal.DerivationID, LeaseOwner: personal.LeaseOwner,
		DataMode: modelpolicy.DataModePersonal,
	}); err != nil {
		t.Fatalf("complete personal Bedrock extraction: %v", err)
	}

	restricted, err := repository.PrepareVersion(ctx, version, derivation, modelpolicy.DataModeRestricted, 5*time.Minute)
	if err != nil {
		t.Fatalf("reuse completed Bedrock extraction in restricted mode: %v", err)
	}
	if restricted.Status != ingest.VersionStatusComplete || restricted.DerivationID != personal.DerivationID {
		t.Fatalf("restricted cache state = %#v, want completed personal derivation", restricted)
	}
	if err := pool.QueryRow(ctx, `SELECT data_mode FROM stacks.extraction_runs WHERE id = $1`, personal.DerivationID).Scan(&dataMode); err != nil {
		t.Fatalf("reload completed Bedrock extraction mode: %v", err)
	}
	if dataMode != string(modelpolicy.DataModePersonal) {
		t.Fatalf("completed Bedrock extraction mode = %q, want original personal provenance", dataMode)
	}
}

func TestModelProviderProvenanceSeparatesDirectExtractionCaches(t *testing.T) {
	pool := openIntegrationDatabase(t)
	repository := NewIngestionRepository(pool)
	ctx := context.Background()
	version := testDocumentVersion(t, testIdentifier("document-direct-provider-caches"))
	states := make(map[modelpolicy.Provider]ingest.VersionState)
	for _, provider := range []modelpolicy.Provider{modelpolicy.ProviderOpenAI, modelpolicy.ProviderAnthropic} {
		derivation := testExtractionDerivationForProvider(t, version, provider)
		state, err := repository.PrepareVersion(ctx, version, derivation, modelpolicy.DataModePersonal, 5*time.Minute)
		if err != nil {
			t.Fatalf("prepare %s extraction: %v", provider, err)
		}
		var storedProvider, storedMode string
		var storedRegion *string
		if err := pool.QueryRow(ctx, `
			SELECT model_provider, data_mode, bedrock_region
			FROM stacks.extraction_runs WHERE id = $1`, state.DerivationID).Scan(&storedProvider, &storedMode, &storedRegion); err != nil {
			t.Fatalf("load %s extraction provenance: %v", provider, err)
		}
		if storedProvider != string(provider) || storedMode != string(modelpolicy.DataModePersonal) || storedRegion != nil {
			t.Fatalf("%s extraction provenance = %q/%q/%v, want provider/personal/NULL", provider, storedProvider, storedMode, storedRegion)
		}
		states[provider] = state
	}
	if states[modelpolicy.ProviderOpenAI].DerivationID == states[modelpolicy.ProviderAnthropic].DerivationID {
		t.Fatal("OpenAI and Anthropic extraction identities shared one durable cache row")
	}
}

func TestIngestionRepositoryResumesVersionAndCompletesAtomically(t *testing.T) {
	pool := openIntegrationDatabase(t)
	repository := NewIngestionRepository(pool)
	ctx := context.Background()
	version := testDocumentVersion(t, testIdentifier("document-ingestion-state"))
	derivation := testExtractionDerivation(t, version)

	first, err := repository.PrepareVersion(ctx, version, derivation, modelpolicy.DataModePersonal, 5*time.Minute)
	if err != nil {
		t.Fatalf("prepare first ingestion attempt: %v", err)
	}
	if first.Status != ingest.VersionStatusPending || first.RetryCount != 0 {
		t.Fatalf("first state = %#v, want pending retry_count=0", first)
	}
	if err := repository.RecordFailure(ctx, first.DerivationID, first.LeaseOwner, ingest.VersionStatusIncomplete, ingest.FailureStorage); err != nil {
		t.Fatalf("record incomplete attempt: %v", err)
	}
	second, err := repository.PrepareVersion(ctx, version, derivation, modelpolicy.DataModeRestricted, 5*time.Minute)
	if err != nil {
		t.Fatalf("prepare retry: %v", err)
	}
	if second.ID != first.ID || second.Status != ingest.VersionStatusPending || second.RetryCount != 1 || second.FailureCode != "" {
		t.Fatalf("retry state = %#v, want same pending version with retry_count=1", second)
	}
	var retriedDataMode string
	if err := pool.QueryRow(ctx, `SELECT data_mode FROM stacks.extraction_runs WHERE id = $1`, second.DerivationID).Scan(&retriedDataMode); err != nil {
		t.Fatalf("load retried extraction data mode: %v", err)
	}
	if retriedDataMode != string(modelpolicy.DataModeRestricted) {
		t.Fatalf("retried extraction data mode = %q, want restricted", retriedDataMode)
	}
	if err := repository.CompleteVersion(ctx, ingest.Completion{
		VersionID: second.ID, DerivationID: second.DerivationID, LeaseOwner: second.LeaseOwner,
		DataMode: modelpolicy.DataModeRestricted,
	}); err != nil {
		t.Fatalf("complete retry: %v", err)
	}
	complete, err := repository.PrepareVersion(ctx, version, derivation, modelpolicy.DataModePersonal, 5*time.Minute)
	if err != nil {
		t.Fatalf("prepare completed version: %v", err)
	}
	if complete.Status != ingest.VersionStatusComplete || complete.RetryCount != 1 {
		t.Fatalf("completed state = %#v, want complete retry_count=1", complete)
	}
}

func TestPendingUnassociatedIdentityPersistsExactEvidenceWithoutTeachingAliases(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	const (
		alexEvidence = "Alex Reviewer led the review."
		bobEvidence  = "Bob Builder uses bob.builder@synthetic.example."
		transcript   = alexEvidence + " " + bobEvidence
	)
	version, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
		Provider:           "synthetic-drive",
		ProviderDocumentID: testIdentifier("document-unassociated-identity"),
		Title:              "Synthetic identity review",
		Locator:            "https://docs.example.invalid/unassociated-identity",
		ProviderVersion:    "synthetic-version-1",
		ProviderRevision:   "",
		ModifiedAt:         time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC),
		RecordedAt:         time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
		Sections: testEvidenceSections(t, []source.Tab{{
			ID: "tab-synthetic", Title: "Synthetic Transcript", Order: 0,
			Role: source.TabRoleTranscript, Text: transcript,
		}}),
	})
	if err != nil {
		t.Fatalf("new unassociated identity version: %v", err)
	}
	alexSpan, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, SectionID: "tab-synthetic", StartOffset: 0,
		EndOffset: len(alexEvidence), Quote: alexEvidence,
	})
	if err != nil {
		t.Fatalf("new Alex evidence span: %v", err)
	}
	bobSpan, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, SectionID: "tab-synthetic", StartOffset: len(alexEvidence) + 1,
		EndOffset: len(transcript), Quote: bobEvidence,
	})
	if err != nil {
		t.Fatalf("new Bob evidence span: %v", err)
	}
	repository := NewIngestionRepository(pool)
	state, err := repository.PrepareVersion(ctx, version, testExtractionDerivation(t, version), modelpolicy.DataModePersonal, 5*time.Minute)
	if err != nil {
		t.Fatalf("prepare unassociated identity derivation: %v", err)
	}
	if err := repository.CompleteVersion(ctx, ingest.Completion{
		VersionID: state.ID, DerivationID: state.DerivationID, LeaseOwner: state.LeaseOwner,
		DataMode: modelpolicy.DataModePersonal,
		Evidence: []ingest.EvidenceRecord{
			{Key: "citation-alex", Span: alexSpan},
			{Key: "citation-bob", Span: bobSpan},
		},
		Mentions: []ingest.MentionRecord{{
			Key: "mention-alex", EvidenceKey: "citation-alex", Surface: "Alex Reviewer",
			NormalizedName: "alex reviewer", ProposedEmail: "bob.builder@synthetic.example",
			ProposedEmailEvidenceKey: "citation-bob", Role: "speaker",
		}},
	}); err != nil {
		t.Fatalf("complete pending unassociated identity: %v", err)
	}

	var proposalID, quote, normalizedName, normalizedEmail, proposedEmail, proposedEmailQuote string
	if err := pool.QueryRow(ctx, `
		SELECT proposal.id::text, span.quote, mention.normalized_name, mention.normalized_email,
		       mention.proposed_email, proposed_email_span.quote
		FROM stacks.resolution_proposals AS proposal
		JOIN stacks.mentions AS mention ON mention.id = proposal.mention_id
		JOIN stacks.evidence_spans AS span ON span.id = mention.evidence_span_id
		JOIN stacks.evidence_spans AS proposed_email_span ON proposed_email_span.id = mention.proposed_email_evidence_span_id
		WHERE mention.extraction_run_id = $1`, state.DerivationID).Scan(
		&proposalID, &quote, &normalizedName, &normalizedEmail, &proposedEmail, &proposedEmailQuote,
	); err != nil {
		t.Fatalf("load pending identity evidence: %v", err)
	}
	if quote != alexEvidence || normalizedName != "alex reviewer" || normalizedEmail != "" ||
		proposedEmail != "bob.builder@synthetic.example" || proposedEmailQuote != bobEvidence {
		t.Fatalf("stored identity provenance = %q/%q/%q/%q/%q, want exact name/email evidence with no teachable email",
			quote, normalizedName, normalizedEmail, proposedEmail, proposedEmailQuote)
	}

	acceptedEntityID := uuid.NewString()
	entities := NewEntityRepository(pool)
	if _, err := entities.CreateEntity(ctx, EntityInput{
		ID: acceptedEntityID, Kind: "person", DisplayName: "Synthetic Reviewed Person",
	}); err != nil {
		t.Fatalf("create reviewed entity: %v", err)
	}
	if _, err := entities.RecordReviewDecision(ctx, ResolutionDecisionInput{
		ProposalID: proposalID, Outcome: ResolutionOutcomeAccepted, EntityID: acceptedEntityID,
	}); err != nil {
		t.Fatalf("accept pending unassociated identity: %v", err)
	}
	var nameAliasAssertions, emailAliasAssertions int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE alias_type = 'name'),
		       count(*) FILTER (WHERE alias_type = 'email')
		FROM stacks.entity_alias_assertions WHERE entity_id = $1`, acceptedEntityID).Scan(&nameAliasAssertions, &emailAliasAssertions); err != nil {
		t.Fatalf("count unassociated identity aliases: %v", err)
	}
	if nameAliasAssertions != 1 || emailAliasAssertions != 0 {
		t.Fatalf("name/email alias assertion counts = %d/%d, want name taught independently and model email kept pending", nameAliasAssertions, emailAliasAssertions)
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
			state, err := NewIngestionRepository(pool).PrepareVersion(context.Background(), version, derivation, modelpolicy.DataModePersonal, 5*time.Minute)
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
	state, err := repository.PrepareVersion(context.Background(), version, testExtractionDerivation(t, version), modelpolicy.DataModePersonal, 5*time.Minute)
	if err != nil {
		t.Fatalf("claim extraction derivation: %v", err)
	}
	if err := repository.RecordFailure(context.Background(), state.DerivationID, uuid.NewString(), ingest.VersionStatusIncomplete, ingest.FailureStorage); err == nil {
		t.Fatal("non-owner failure update error = nil")
	}
	if err := repository.CompleteVersion(context.Background(), ingest.Completion{
		VersionID: state.ID, DerivationID: state.DerivationID, LeaseOwner: uuid.NewString(),
		DataMode: modelpolicy.DataModePersonal,
	}); err == nil {
		t.Fatal("non-owner completion error = nil")
	}
	if err := repository.CompleteVersion(context.Background(), ingest.Completion{
		VersionID: state.ID, DerivationID: state.DerivationID, LeaseOwner: state.LeaseOwner,
		DataMode: modelpolicy.DataModePersonal,
	}); err != nil {
		t.Fatalf("owner completion: %v", err)
	}
}

func TestExpiredExtractionClaimCanBeRecoveredByNewOwner(t *testing.T) {
	pool := openIntegrationDatabase(t)
	repository := NewIngestionRepository(pool)
	version := testDocumentVersion(t, testIdentifier("document-expired-claim"))
	derivation := testExtractionDerivation(t, version)
	first, err := repository.PrepareVersion(context.Background(), version, derivation, modelpolicy.DataModePersonal, 5*time.Minute)
	if err != nil {
		t.Fatalf("claim extraction derivation: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE stacks.extraction_runs
		SET lease_expires_at = clock_timestamp() - interval '1 second'
		WHERE id = $1`, first.DerivationID); err != nil {
		t.Fatalf("expire synthetic extraction claim: %v", err)
	}

	recovered, err := repository.PrepareVersion(context.Background(), version, derivation, modelpolicy.DataModePersonal, 5*time.Minute)
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
		Document: version, SectionID: "tab-synthetic", StartOffset: 0,
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
	var renderableLegacyAnalyses int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM stacks.analysis_runs
		WHERE input_digest = $1 AND state = 'complete' AND report_json IS NOT NULL
		  AND currently_admissible`, legacyAnalysisDigest[:]).Scan(&renderableLegacyAnalyses); err != nil {
		t.Fatalf("count renderable legacy analyses: %v", err)
	}
	if renderableLegacyAnalyses != 0 {
		t.Fatalf("renderable legacy analyses = %d, want zero", renderableLegacyAnalyses)
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
	if pair.Accepted || len(pair.Signals) != 0 {
		t.Fatalf("explicitly reviewed legacy pair = %#v, want retired mentions and unsafe derived signals to remain quarantined", pair)
	}
}

func TestLegacyAdmissionMigrationUpgradesPre00005RowsWithoutRewritingPayload(t *testing.T) {
	pool := openMigrationIntegrationDatabase(t)
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

func TestCompatibilityAdmissionMigrationUpgradesFullyMigrated00006Database(t *testing.T) {
	pool := openMigrationIntegrationDatabase(t)
	ctx := context.Background()
	schemaName := "stacks_compat_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := `"` + schemaName + `"`
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create isolated compatibility schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	})

	for _, migration := range []string{
		"00002_manager_confidence_poc.sql",
		"00003_ingestion_processing_state.sql",
		"00004_temporal_pair_analysis.sql",
		"00005_manager_confidence_final_fixes.sql",
		"00006_legacy_admission_boundary.sql",
	} {
		applyMigrationToSchema(t, pool, quotedSchema, migration)
	}
	unsafe := seedPost00006UnsafeRows(t, pool, quotedSchema)
	applyMigrationToSchema(t, pool, quotedSchema, "00007_compatibility_admission_boundary.sql")

	for _, row := range []struct {
		table string
		id    string
	}{
		{table: "extraction_runs", id: unsafe.extractionRunID},
		{table: "mentions", id: unsafe.mentionID},
		{table: "resolution_decisions", id: unsafe.decisionID},
		{table: "observations", id: unsafe.observationID},
		{table: "interaction_signals", id: unsafe.signalID},
		{table: "analysis_runs", id: unsafe.analysisID},
	} {
		var admissible bool
		if err := pool.QueryRow(ctx, "SELECT currently_admissible FROM "+quotedSchema+`."`+row.table+`" WHERE id = $1`, row.id).Scan(&admissible); err != nil {
			t.Fatalf("load upgraded %s admission state: %v", row.table, err)
		}
		if admissible {
			t.Fatalf("superseded-semantics %s row remained currently admissible", row.table)
		}
	}

	var rationalePreserved, hypothesisPreserved, reportPreserved, normalizedEmailPreserved bool
	if err := pool.QueryRow(ctx, `
		SELECT signal.rationale = $4, run.hypothesis = $5,
		       run.report_json = $6::jsonb, mention.normalized_email = $7
		FROM `+quotedSchema+`.interaction_signals AS signal
		CROSS JOIN `+quotedSchema+`.analysis_runs AS run
		CROSS JOIN `+quotedSchema+`.mentions AS mention
		WHERE signal.id = $1 AND run.id = $2 AND mention.id = $3`,
		unsafe.signalID, unsafe.analysisID, unsafe.mentionID,
		unsafe.rationale, unsafe.hypothesis, unsafe.reportJSON, unsafe.email,
	).Scan(&rationalePreserved, &hypothesisPreserved, &reportPreserved, &normalizedEmailPreserved); err != nil {
		t.Fatalf("load preserved superseded-semantics audit payload: %v", err)
	}
	if !rationalePreserved || !hypothesisPreserved || !reportPreserved || !normalizedEmailPreserved {
		t.Fatalf("compatibility migration rewrote audit payload: rationale=%t hypothesis=%t report=%t email=%t",
			rationalePreserved, hypothesisPreserved, reportPreserved, normalizedEmailPreserved)
	}

	var admittedAliases, admittedSignals, admittedAnalyses int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE decision.currently_admissible)
		FROM `+quotedSchema+`.entity_alias_assertions AS assertion
		JOIN `+quotedSchema+`.resolution_decisions AS decision ON decision.id = assertion.decision_id
		WHERE assertion.decision_id = $1`, unsafe.decisionID).Scan(&admittedAliases); err != nil {
		t.Fatalf("count admitted superseded aliases: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM `+quotedSchema+`.interaction_signals AS signal
		JOIN `+quotedSchema+`.observations AS observation ON observation.id = signal.observation_id
		JOIN `+quotedSchema+`.extraction_runs AS run ON run.id = observation.extraction_run_id
		WHERE signal.id = $1 AND signal.currently_admissible
		  AND observation.currently_admissible AND run.currently_admissible`, unsafe.signalID).Scan(&admittedSignals); err != nil {
		t.Fatalf("count admitted superseded signals: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM `+quotedSchema+`.analysis_runs
		WHERE id = $1 AND state = 'complete' AND currently_admissible`, unsafe.analysisID).Scan(&admittedAnalyses); err != nil {
		t.Fatalf("count admitted superseded analyses: %v", err)
	}
	if admittedAliases != 0 || admittedSignals != 0 || admittedAnalyses != 0 {
		t.Fatalf("admitted alias/signal/analysis counts = %d/%d/%d, want zero", admittedAliases, admittedSignals, admittedAnalyses)
	}

	for _, table := range []string{
		"extraction_runs", "mentions", "resolution_decisions", "observations", "interaction_signals", "analysis_runs",
	} {
		var columnDefault string
		if err := pool.QueryRow(ctx, `
			SELECT column_default FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = $2 AND column_name = 'currently_admissible'`,
			schemaName, table).Scan(&columnDefault); err != nil {
			t.Fatalf("load compatibility %s admission default: %v", table, err)
		}
		if columnDefault != "true" {
			t.Fatalf("post-compatibility %s admission default = %q, want true", table, columnDefault)
		}
	}
}

func TestSnapshotCoherenceAdmissionMigrationQuarantinesHybridRowsAndSafeResyncUsesFetchedTime(t *testing.T) {
	pool := openMigrationIntegrationDatabase(t)
	ctx := context.Background()
	schemaName := "stacks_snapshot_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := `"` + schemaName + `"`
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create isolated snapshot-coherence schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	})

	for _, migration := range []string{
		"00002_manager_confidence_poc.sql",
		"00003_ingestion_processing_state.sql",
		"00004_temporal_pair_analysis.sql",
		"00005_manager_confidence_final_fixes.sql",
		"00006_legacy_admission_boundary.sql",
		"00007_compatibility_admission_boundary.sql",
	} {
		applyMigrationToSchema(t, pool, quotedSchema, migration)
	}

	meetingTime := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	listed := snapshotCoherenceDocument("snapshot-coherence-document", "[2026-07-20] Weekly")
	listed.MeetingTime = &meetingTime
	listed.Tabs = nil
	listed.Revision = ""
	fetched := snapshotCoherenceDocument(listed.ID, "Weekly")
	fetched.MeetingTime = nil
	hybrid := fetched
	hybrid.MeetingTime = &meetingTime
	hybridVersion := snapshotCoherenceVersion(t, hybrid)
	unsafe := seedPost00007HybridRows(t, pool, quotedSchema, hybridVersion)

	applyMigrationToSchema(t, pool, quotedSchema, "00008_snapshot_coherence_admission_boundary.sql")

	for _, row := range []struct {
		table string
		id    string
	}{
		{table: "extraction_runs", id: unsafe.extractionRunID},
		{table: "mentions", id: unsafe.mentionID},
		{table: "resolution_decisions", id: unsafe.decisionID},
		{table: "observations", id: unsafe.observationID},
		{table: "interaction_signals", id: unsafe.signalID},
		{table: "analysis_runs", id: unsafe.analysisID},
	} {
		var admissible bool
		if err := pool.QueryRow(ctx, "SELECT currently_admissible FROM "+quotedSchema+`."`+row.table+`" WHERE id = $1`, row.id).Scan(&admissible); err != nil {
			t.Fatalf("load snapshot-upgraded %s admission state: %v", row.table, err)
		}
		if admissible {
			t.Fatalf("snapshot-incoherent %s row remained currently admissible", row.table)
		}
	}
	var admittedAliases int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM `+quotedSchema+`.entity_alias_assertions AS assertion
		JOIN `+quotedSchema+`.resolution_decisions AS decision ON decision.id = assertion.decision_id
		WHERE assertion.decision_id = $1 AND decision.currently_admissible`, unsafe.decisionID,
	).Scan(&admittedAliases); err != nil {
		t.Fatalf("count snapshot-incoherent current aliases: %v", err)
	}
	if admittedAliases != 0 {
		t.Fatalf("snapshot-incoherent current aliases = %d, want zero", admittedAliases)
	}

	var preservedTitle, preservedRationale, preservedHypothesis string
	var preservedMeetingTime time.Time
	var preservedReport bool
	if err := pool.QueryRow(ctx, `
		SELECT version.title, version.source_meeting_time, signal.rationale,
		       analysis.hypothesis, analysis.report_json = $4::jsonb
		FROM `+quotedSchema+`.document_versions AS version
		CROSS JOIN `+quotedSchema+`.interaction_signals AS signal
		CROSS JOIN `+quotedSchema+`.analysis_runs AS analysis
		WHERE version.id = $1 AND signal.id = $2 AND analysis.id = $3`,
		unsafe.versionID, unsafe.signalID, unsafe.analysisID, unsafe.reportJSON,
	).Scan(&preservedTitle, &preservedMeetingTime, &preservedRationale, &preservedHypothesis, &preservedReport); err != nil {
		t.Fatalf("load preserved snapshot-incoherent audit payload: %v", err)
	}
	if preservedTitle != "Weekly" || !preservedMeetingTime.Equal(meetingTime) ||
		preservedRationale != unsafe.rationale || preservedHypothesis != unsafe.hypothesis ||
		!preservedReport {
		t.Fatalf("migration rewrote hybrid audit payload: title=%q time=%v rationale=%q hypothesis=%q report_preserved=%t",
			preservedTitle, preservedMeetingTime, preservedRationale, preservedHypothesis, preservedReport)
	}

	repository := &snapshotCoherenceRepository{pool: pool, quotedSchema: quotedSchema}
	model := &snapshotCoherenceModel{}
	service := ingest.Service{
		Source: &snapshotCoherenceSource{listed: listed, fetched: fetched},
		Model:  model, Resolver: entity.Resolver{}, Repository: repository,
		CollectionID: "synthetic-folder", PromptVersion: extract.ExtractionPromptVersion,
		Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModePersonal,
		Region: "us-east-1", ModelID: "synthetic-model", MaxTokens: 256,
		LeaseDuration: 5 * time.Minute, AttemptTimeout: 4 * time.Minute,
		Now: func() time.Time { return time.Date(2026, time.July, 22, 2, 0, 0, 0, time.UTC) },
	}
	summary, err := service.Sync(ctx)
	if err != nil || summary.Completed != 1 || model.calls != 1 {
		t.Fatalf("safe post-upgrade Sync() = (%#v, %v), model calls=%d, want one new completed derivation", summary, err, model.calls)
	}
	if repository.preparedVersion.Title() != "Weekly" || repository.preparedVersion.SourceTime() != nil {
		t.Fatalf("safe resync version title/time = %q/%v, want fetched undated snapshot",
			repository.preparedVersion.Title(), repository.preparedVersion.SourceTime())
	}
	if repository.versionID == unsafe.versionID {
		t.Fatal("safe resync reused the hybrid source version")
	}
	var safeMeetingTime *time.Time
	var safeRunAdmissible bool
	if err := pool.QueryRow(ctx, `
		SELECT version.source_meeting_time, run.currently_admissible
		FROM `+quotedSchema+`.document_versions AS version
		JOIN `+quotedSchema+`.extraction_runs AS run ON run.document_version_id = version.id
		WHERE version.id = $1 AND run.id = $2`, repository.versionID, repository.derivationID,
	).Scan(&safeMeetingTime, &safeRunAdmissible); err != nil {
		t.Fatalf("load safe resync state: %v", err)
	}
	if safeMeetingTime != nil || !safeRunAdmissible {
		t.Fatalf("safe resync meeting time/admission = %v/%t, want unknown/current", safeMeetingTime, safeRunAdmissible)
	}
}

func TestModelProviderProvenanceMigrationUpgrades00009RowsWithoutChangingDigests(t *testing.T) {
	pool := openMigrationIntegrationDatabase(t)
	ctx := context.Background()
	schemaName := "stacks_provider_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := `"` + schemaName + `"`
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create isolated provider-provenance schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	})
	applyMigrationsThrough00009ToSchema(t, pool, quotedSchema)

	recordedAt := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	sourceID := uuid.NewString()
	versionID := uuid.NewString()
	extractionID := uuid.NewString()
	analysisID := uuid.NewString()
	employeeID := uuid.NewString()
	managerID := uuid.NewString()
	ownerID := uuid.NewString()
	versionDigest := sha256.Sum256([]byte("provider-upgrade-version"))
	derivationDigest := sha256.Sum256([]byte("provider-upgrade-derivation"))
	inputDigest := sha256.Sum256([]byte("provider-upgrade-analysis"))
	schemaDigest := sha256.Sum256([]byte("provider-upgrade-schema"))

	statements := []struct {
		operation string
		query     string
		arguments []any
	}{
		{"source", "INSERT INTO " + quotedSchema + `.source_documents (id, provider, provider_document_id, title, locator, recorded_at) VALUES ($1, 'drive', 'provider-upgrade-document', 'Synthetic', 'https://example.invalid/provider-upgrade', $2)`, []any{sourceID, recordedAt}},
		{"version", "INSERT INTO " + quotedSchema + `.document_versions (id, source_document_id, digest, content_digest_v2, title, locator, recorded_at) VALUES ($1, $2, $3, $3, 'Synthetic', 'https://example.invalid/provider-upgrade', $4)`, []any{versionID, sourceID, versionDigest[:], recordedAt}},
		{"employee", "INSERT INTO " + quotedSchema + `.entities (id, kind, display_name, recorded_at) VALUES ($1, 'person', 'Synthetic Employee', $2)`, []any{employeeID, recordedAt}},
		{"manager", "INSERT INTO " + quotedSchema + `.entities (id, kind, display_name, recorded_at) VALUES ($1, 'person', 'Synthetic Manager', $2)`, []any{managerID, recordedAt}},
		{"extraction", "INSERT INTO " + quotedSchema + `.extraction_runs (id, document_version_id, derivation_digest, model_id, bedrock_region, max_output_tokens, prompt_version, schema_digest, processing_status, completed_by_owner, recorded_at, completed_at, currently_admissible) VALUES ($1, $2, $3, 'synthetic-model', 'us-east-1', 256, 'extract-v2', $4, 'complete', $5, $6, $6, true)`, []any{extractionID, versionID, derivationDigest[:], schemaDigest[:], ownerID, recordedAt}},
		{"analysis", "INSERT INTO " + quotedSchema + `.analysis_runs (id, employee_entity_id, manager_entity_id, input_digest, analysis_prompt_version, policy_version, state, recorded_at, completed_at, hypothesis, report_state, bedrock_region, model_id, max_output_tokens, report_json, currently_admissible) VALUES ($1, $2, $3, $4, 'analyze-v1', 'policy-v1', 'complete', $5, $5, 'Synthetic hypothesis', 'mixed_or_conflicting', 'us-east-1', 'synthetic-model', 256, $6::jsonb, true)`, []any{analysisID, employeeID, managerID, inputDigest[:], recordedAt, `{"status":"mixed_or_conflicting"}`}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.arguments...); err != nil {
			t.Fatalf("seed provider-provenance %s: %v", statement.operation, err)
		}
	}

	applyMigrationToSchema(t, pool, quotedSchema, "00010_model_provider_provenance.sql")

	var extractionProvider, extractionMode, extractionRegion string
	var storedDerivationDigest []byte
	if err := pool.QueryRow(ctx, "SELECT model_provider, data_mode, bedrock_region, derivation_digest FROM "+quotedSchema+`.extraction_runs WHERE id = $1`, extractionID).Scan(
		&extractionProvider, &extractionMode, &extractionRegion, &storedDerivationDigest,
	); err != nil {
		t.Fatalf("load upgraded extraction provenance: %v", err)
	}
	if extractionProvider != "bedrock" || extractionMode != "legacy" || extractionRegion != "us-east-1" ||
		!bytes.Equal(storedDerivationDigest, derivationDigest[:]) {
		t.Fatalf("upgraded extraction provenance = %q/%q/%q digest-preserved=%t, want bedrock/legacy/us-east-1/true",
			extractionProvider, extractionMode, extractionRegion, bytes.Equal(storedDerivationDigest, derivationDigest[:]))
	}

	var analysisProvider, analysisMode, analysisRegion string
	var storedInputDigest []byte
	if err := pool.QueryRow(ctx, "SELECT model_provider, data_mode, bedrock_region, input_digest FROM "+quotedSchema+`.analysis_runs WHERE id = $1`, analysisID).Scan(
		&analysisProvider, &analysisMode, &analysisRegion, &storedInputDigest,
	); err != nil {
		t.Fatalf("load upgraded analysis provenance: %v", err)
	}
	if analysisProvider != "bedrock" || analysisMode != "legacy" || analysisRegion != "us-east-1" ||
		!bytes.Equal(storedInputDigest, inputDigest[:]) {
		t.Fatalf("upgraded analysis provenance = %q/%q/%q digest-preserved=%t, want bedrock/legacy/us-east-1/true",
			analysisProvider, analysisMode, analysisRegion, bytes.Equal(storedInputDigest, inputDigest[:]))
	}
}

func TestModelProviderProvenanceMigrationEnforcesProviderAndModeConstraints(t *testing.T) {
	pool := openMigrationIntegrationDatabase(t)
	ctx := context.Background()
	schemaName := "stacks_provider_constraints_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := `"` + schemaName + `"`
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create isolated provider-constraint schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	})
	applyMigrationsThrough00009ToSchema(t, pool, quotedSchema)
	applyMigrationToSchema(t, pool, quotedSchema, "00010_model_provider_provenance.sql")

	recordedAt := time.Date(2026, time.July, 22, 11, 0, 0, 0, time.UTC)
	sourceID := uuid.NewString()
	versionID := uuid.NewString()
	employeeID := uuid.NewString()
	managerID := uuid.NewString()
	versionDigest := sha256.Sum256([]byte("provider-constraint-version"))
	for _, statement := range []struct {
		operation string
		query     string
		arguments []any
	}{
		{"source", "INSERT INTO " + quotedSchema + `.source_documents (id, provider, provider_document_id, title, locator, recorded_at) VALUES ($1, 'drive', 'provider-constraint-document', 'Synthetic', 'https://example.invalid/provider-constraint', $2)`, []any{sourceID, recordedAt}},
		{"version", "INSERT INTO " + quotedSchema + `.document_versions (id, source_document_id, digest, content_digest_v2, title, locator, recorded_at) VALUES ($1, $2, $3, $3, 'Synthetic', 'https://example.invalid/provider-constraint', $4)`, []any{versionID, sourceID, versionDigest[:], recordedAt}},
		{"employee", "INSERT INTO " + quotedSchema + `.entities (id, kind, display_name, recorded_at) VALUES ($1, 'person', 'Synthetic Employee', $2)`, []any{employeeID, recordedAt}},
		{"manager", "INSERT INTO " + quotedSchema + `.entities (id, kind, display_name, recorded_at) VALUES ($1, 'person', 'Synthetic Manager', $2)`, []any{managerID, recordedAt}},
	} {
		if _, err := pool.Exec(ctx, statement.query, statement.arguments...); err != nil {
			t.Fatalf("seed provider constraint %s: %v", statement.operation, err)
		}
	}

	insertExtraction := func(provider, mode string, region *string) error {
		derivationDigest := sha256.Sum256([]byte(uuid.NewString()))
		schemaDigest := sha256.Sum256([]byte("provider-constraint-schema"))
		_, err := pool.Exec(ctx, "INSERT INTO "+quotedSchema+`.extraction_runs (document_version_id, derivation_digest, model_provider, data_mode, model_id, bedrock_region, max_output_tokens, prompt_version, schema_digest, lease_owner, lease_expires_at, recorded_at, currently_admissible) VALUES ($1, $2, $3, $4, 'synthetic-model', $5, 256, 'extract-v2', $6, $7, $8, $9, true)`,
			versionID, derivationDigest[:], provider, mode, region, schemaDigest[:], uuid.NewString(), recordedAt.Add(time.Hour), recordedAt)
		return err
	}
	region := "us-east-1"
	for _, testCase := range []struct {
		name     string
		provider string
		mode     string
		region   *string
		wantErr  bool
	}{
		{name: "bedrock without region", provider: "bedrock", mode: "personal", wantErr: true},
		{name: "openai with region", provider: "openai", mode: "personal", region: &region, wantErr: true},
		{name: "invalid provider", provider: "synthetic", mode: "personal", wantErr: true},
		{name: "invalid mode", provider: "openai", mode: "synthetic", wantErr: true},
		{name: "personal openai", provider: "openai", mode: "personal"},
		{name: "personal anthropic", provider: "anthropic", mode: "personal"},
	} {
		t.Run("extraction "+testCase.name, func(t *testing.T) {
			err := insertExtraction(testCase.provider, testCase.mode, testCase.region)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("insert extraction error = %v, wantErr %t", err, testCase.wantErr)
			}
		})
	}

	insertAnalysis := func(provider, mode string, region *string, withModel bool) error {
		inputDigest := sha256.Sum256([]byte(uuid.NewString()))
		var modelID *string
		var maxTokens *int
		if withModel {
			model := "synthetic-model"
			tokens := 256
			modelID = &model
			maxTokens = &tokens
		}
		var providerValue, modeValue any
		if provider != "" {
			providerValue = provider
		}
		if mode != "" {
			modeValue = mode
		}
		_, err := pool.Exec(ctx, "INSERT INTO "+quotedSchema+`.analysis_runs (employee_entity_id, manager_entity_id, input_digest, analysis_prompt_version, policy_version, state, recorded_at, completed_at, report_json, model_provider, data_mode, bedrock_region, model_id, max_output_tokens, currently_admissible) VALUES ($1, $2, $3, 'analyze-v1', 'policy-v1', 'complete', $4, $4, '{}'::jsonb, $5, $6, $7, $8, $9, true)`,
			employeeID, managerID, inputDigest[:], recordedAt, providerValue, modeValue, region, modelID, maxTokens)
		return err
	}
	for _, testCase := range []struct {
		name      string
		provider  string
		mode      string
		region    *string
		withModel bool
		wantErr   bool
	}{
		{name: "bedrock without region", provider: "bedrock", mode: "personal", withModel: true, wantErr: true},
		{name: "anthropic with region", provider: "anthropic", mode: "personal", region: &region, withModel: true, wantErr: true},
		{name: "personal openai", provider: "openai", mode: "personal", withModel: true},
		{name: "personal anthropic", provider: "anthropic", mode: "personal", withModel: true},
		{name: "deterministic non-model", withModel: false},
	} {
		t.Run("analysis "+testCase.name, func(t *testing.T) {
			err := insertAnalysis(testCase.provider, testCase.mode, testCase.region, testCase.withModel)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("insert analysis error = %v, wantErr %t", err, testCase.wantErr)
			}
		})
	}
}

func TestMigrationUpgradeToGoogleDirectoryPreservesIdentityHistory(t *testing.T) {
	pool := openMigrationIntegrationDatabase(t)
	ctx := context.Background()
	schemaName := "stacks_directory_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := `"` + schemaName + `"`
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create isolated directory-upgrade schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	})

	applyMigrationsThrough00011ToSchema(t, pool, quotedSchema)
	seedGoogleDirectoryMigrationFixture(t, pool, quotedSchema)
	before := captureIdentityHistory(t, pool, quotedSchema)

	applyMigrationToSchema(t, pool, quotedSchema, "00012_google_directory_identity.sql")

	after := captureIdentityHistory(t, pool, quotedSchema)
	for index, want := range before {
		got := after[index]
		if got != want {
			t.Fatalf("%s history after directory migration = count:%d digest:%s, want count:%d digest:%s",
				want.table, got.count, got.digest, want.count, want.digest)
		}
	}
}

func TestMigrationUpgradeToGoogleDirectoryEnforcesCandidateAndEffectiveIdentityConstraints(t *testing.T) {
	pool := openMigrationIntegrationDatabase(t)
	ctx := context.Background()
	schemaName := "stacks_directory_constraints_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := `"` + schemaName + `"`
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create isolated directory-constraint schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	})

	applyMigrationsThrough00011ToSchema(t, pool, quotedSchema)
	first := seedGoogleDirectoryMigrationFixture(t, pool, quotedSchema)
	applyMigrationToSchema(t, pool, quotedSchema, "00012_google_directory_identity.sql")

	second := seedAdditionalDirectoryDecision(t, pool, quotedSchema, first)
	snapshotID := uuid.NewString()
	lookupAttemptID := uuid.NewString()
	snapshotDigest := sha256.Sum256([]byte("directory-constraint-snapshot"))
	queryDigest := sha256.Sum256([]byte("directory-constraint-query"))
	attemptDigest := sha256.Sum256([]byte("directory-constraint-attempt"))
	for _, statement := range []struct {
		operation string
		query     string
		arguments []any
	}{
		{
			operation: "directory profile snapshot",
			query: "INSERT INTO " + quotedSchema + `.directory_profile_snapshots
				(id, provider, provider_subject_id, source_type, display_name, observed_at, recorded_at, digest)
				VALUES ($1, 'google_people', 'synthetic-subject', 'domain_profile', 'Synthetic Directory Person', NULL, $2, $3)`,
			arguments: []any{snapshotID, first.recordedAt, snapshotDigest[:]},
		},
		{
			operation: "directory profile email",
			query: "INSERT INTO " + quotedSchema + `.directory_profile_emails
				(snapshot_id, normalized_email, is_primary, position)
				VALUES ($1, 'synthetic.person@example.invalid', true, 0)`,
			arguments: []any{snapshotID},
		},
		{
			operation: "directory lookup attempt",
			query: "INSERT INTO " + quotedSchema + `.directory_lookup_attempts
				(id, mention_id, provider, query_kind, email_evidence, query_digest, policy_version,
				 outcome, attempt_count, recorded_at, digest)
				VALUES ($1, $2, 'google_people', 'email', 'source_bound', $3, 'directory-policy-v1',
				 'matched', 1, $4, $5)`,
			arguments: []any{lookupAttemptID, first.mentionID, queryDigest[:], first.recordedAt, attemptDigest[:]},
		},
		{
			operation: "directory lookup match",
			query: "INSERT INTO " + quotedSchema + `.directory_lookup_matches
				(lookup_attempt_id, snapshot_id, rank, reason)
				VALUES ($1, $2, 0, 'exact_email')`,
			arguments: []any{lookupAttemptID, snapshotID},
		},
		{
			operation: "entity-backed candidate",
			query: "INSERT INTO " + quotedSchema + `.resolution_candidates
				(proposal_id, entity_id, rank, confidence, reason)
				VALUES ($1, $2, 0, 1, 'synthetic entity candidate')`,
			arguments: []any{first.proposalID, first.entityID},
		},
		{
			operation: "directory-backed candidate",
			query: "INSERT INTO " + quotedSchema + `.resolution_candidates
				(proposal_id, directory_profile_snapshot_id, rank, confidence, reason)
				VALUES ($1, $2, 1, 1, 'synthetic directory candidate')`,
			arguments: []any{first.proposalID, snapshotID},
		},
	} {
		if _, err := pool.Exec(ctx, statement.query, statement.arguments...); err != nil {
			t.Fatalf("seed %s: %v", statement.operation, err)
		}
	}

	if _, err := pool.Exec(ctx, "INSERT INTO "+quotedSchema+`.resolution_candidates
		(proposal_id, rank, reason) VALUES ($1, 0, 'synthetic zero-source candidate')`, second.proposalID); err == nil {
		t.Fatal("zero-source resolution candidate insert succeeded, want source XOR constraint failure")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO "+quotedSchema+`.resolution_candidates
		(proposal_id, entity_id, directory_profile_snapshot_id, rank, reason)
		VALUES ($1, $2, $3, 0, 'synthetic two-source candidate')`,
		second.proposalID, second.entityID, snapshotID); err == nil {
		t.Fatal("two-source resolution candidate insert succeeded, want source XOR constraint failure")
	}

	if _, err := pool.Exec(ctx, "INSERT INTO "+quotedSchema+`.entity_directory_identity_assertions
		(decision_id, entity_id, lookup_attempt_id, snapshot_id, provider, provider_subject_id, recorded_at)
		VALUES ($1, $2, $3, $4, 'google_people', 'synthetic-subject', $5)`,
		first.decisionID, first.entityID, lookupAttemptID, snapshotID, first.recordedAt); err != nil {
		t.Fatalf("insert first effective directory identity: %v", err)
	}

	conflict, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin conflicting directory identity transaction: %v", err)
	}
	if _, err := conflict.Exec(ctx, "INSERT INTO "+quotedSchema+`.entity_directory_identity_assertions
		(decision_id, entity_id, lookup_attempt_id, snapshot_id, provider, provider_subject_id, recorded_at)
		VALUES ($1, $2, $3, $4, 'google_people', 'synthetic-subject', $5)`,
		second.decisionID, second.entityID, lookupAttemptID, snapshotID, first.recordedAt); err != nil {
		_ = conflict.Rollback(ctx)
		t.Fatalf("insert conflicting directory identity before deferred validation: %v", err)
	}
	if err := conflict.Commit(ctx); err == nil {
		t.Fatal("commit conflicting effective directory identity succeeded, want deferred constraint failure")
	}

	if _, err := pool.Exec(ctx, "UPDATE "+quotedSchema+`.resolution_decisions
		SET currently_admissible = false WHERE id = $1`, second.decisionID); err != nil {
		t.Fatalf("make second directory decision inadmissible: %v", err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO "+quotedSchema+`.entity_directory_identity_assertions
		(decision_id, entity_id, lookup_attempt_id, snapshot_id, provider, provider_subject_id, recorded_at)
		VALUES ($1, $2, $3, $4, 'google_people', 'synthetic-subject', $5)`,
		second.decisionID, second.entityID, lookupAttemptID, snapshotID, first.recordedAt); err != nil {
		t.Fatalf("insert identity for inadmissible decision: %v", err)
	}
	activation, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin conflicting decision activation transaction: %v", err)
	}
	if _, err := activation.Exec(ctx, "UPDATE "+quotedSchema+`.resolution_decisions
		SET currently_admissible = true WHERE id = $1`, second.decisionID); err != nil {
		_ = activation.Rollback(ctx)
		t.Fatalf("activate conflicting directory decision before deferred validation: %v", err)
	}
	if err := activation.Commit(ctx); err == nil {
		t.Fatal("commit conflicting directory decision activation succeeded, want deferred constraint failure")
	}

	correction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin corrected directory identity transaction: %v", err)
	}
	defer correction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.
	if _, err := correction.Exec(ctx, "UPDATE "+quotedSchema+`.resolution_decisions
		SET superseded_by_id = $1 WHERE id = $2`, second.decisionID, first.decisionID); err != nil {
		t.Fatalf("supersede first directory decision: %v", err)
	}
	if _, err := correction.Exec(ctx, "UPDATE "+quotedSchema+`.resolution_decisions
		SET supersedes_id = $1, currently_admissible = true WHERE id = $2`,
		first.decisionID, second.decisionID); err != nil {
		t.Fatalf("link corrected directory decision: %v", err)
	}
	if err := correction.Commit(ctx); err != nil {
		t.Fatalf("commit corrected directory identity: %v", err)
	}

	var effectiveEntities int
	if err := pool.QueryRow(ctx, `
		SELECT count(DISTINCT assertion.entity_id)
		FROM `+quotedSchema+`.entity_directory_identity_assertions AS assertion
		JOIN `+quotedSchema+`.resolution_decisions AS decision ON decision.id = assertion.decision_id
		WHERE assertion.provider = 'google_people'
		  AND assertion.provider_subject_id = 'synthetic-subject'
		  AND decision.superseded_by_id IS NULL
		  AND decision.outcome IN ('accepted', 'created')
		  AND decision.currently_admissible`).Scan(&effectiveEntities); err != nil {
		t.Fatalf("count corrected effective directory identities: %v", err)
	}
	if effectiveEntities != 1 {
		t.Fatalf("corrected effective directory entity count = %d, want 1", effectiveEntities)
	}
}

func TestMigrationUpgradeToGoogleDirectorySerializesConcurrentEffectiveIdentityAssignments(t *testing.T) {
	pool := openMigrationIntegrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	schemaName := "stacks_directory_concurrency_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := `"` + schemaName + `"`
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create isolated directory-concurrency schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	})

	applyMigrationsThrough00011ToSchema(t, pool, quotedSchema)
	first := seedGoogleDirectoryMigrationFixture(t, pool, quotedSchema)
	applyMigrationToSchema(t, pool, quotedSchema, "00012_google_directory_identity.sql")
	second := seedAdditionalDirectoryDecision(t, pool, quotedSchema, first)

	snapshotID := uuid.NewString()
	lookupAttemptID := uuid.NewString()
	snapshotDigest := sha256.Sum256([]byte("directory-concurrency-snapshot"))
	queryDigest := sha256.Sum256([]byte("directory-concurrency-query"))
	attemptDigest := sha256.Sum256([]byte("directory-concurrency-attempt"))
	for _, statement := range []struct {
		operation string
		query     string
		arguments []any
	}{
		{
			operation: "concurrent directory profile snapshot",
			query: "INSERT INTO " + quotedSchema + `.directory_profile_snapshots
				(id, provider, provider_subject_id, source_type, display_name, recorded_at, digest)
				VALUES ($1, 'google_people', 'synthetic-concurrent-subject', 'domain_profile',
					'Synthetic Concurrent Person', $2, $3)`,
			arguments: []any{snapshotID, first.recordedAt, snapshotDigest[:]},
		},
		{
			operation: "concurrent directory lookup attempt",
			query: "INSERT INTO " + quotedSchema + `.directory_lookup_attempts
				(id, mention_id, provider, query_kind, email_evidence, query_digest, policy_version,
				 outcome, attempt_count, recorded_at, digest)
				VALUES ($1, $2, 'google_people', 'email', 'source_bound', $3,
					'directory-policy-v1', 'matched', 1, $4, $5)`,
			arguments: []any{lookupAttemptID, first.mentionID, queryDigest[:], first.recordedAt, attemptDigest[:]},
		},
	} {
		if _, err := pool.Exec(ctx, statement.query, statement.arguments...); err != nil {
			t.Fatalf("seed %s: %v", statement.operation, err)
		}
	}

	firstTransaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin first concurrent directory identity transaction: %v", err)
	}
	defer firstTransaction.Rollback(context.Background()) //nolint:errcheck // committed transactions are already closed.
	secondTransaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin second concurrent directory identity transaction: %v", err)
	}
	defer secondTransaction.Rollback(context.Background()) //nolint:errcheck // committed transactions are already closed.

	var firstBackendPID, secondBackendPID int32
	if err := firstTransaction.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&firstBackendPID); err != nil {
		t.Fatalf("load first directory identity backend PID: %v", err)
	}
	if err := secondTransaction.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&secondBackendPID); err != nil {
		t.Fatalf("load second directory identity backend PID: %v", err)
	}
	if firstBackendPID == secondBackendPID {
		t.Fatalf("concurrent directory identity transactions share backend PID %d", firstBackendPID)
	}

	insertAssertion := func(transaction pgx.Tx, decisionID, entityID string) error {
		_, err := transaction.Exec(ctx, "INSERT INTO "+quotedSchema+`.entity_directory_identity_assertions
			(decision_id, entity_id, lookup_attempt_id, snapshot_id, provider, provider_subject_id, recorded_at)
			VALUES ($1, $2, $3, $4, 'google_people', 'synthetic-concurrent-subject', $5)`,
			decisionID, entityID, lookupAttemptID, snapshotID, first.recordedAt)
		return err
	}
	if err := insertAssertion(firstTransaction, first.decisionID, first.entityID); err != nil {
		t.Fatalf("insert first concurrent directory identity: %v", err)
	}
	if err := insertAssertion(secondTransaction, second.decisionID, second.entityID); err != nil {
		t.Fatalf("insert second concurrent directory identity: %v", err)
	}

	if _, err := firstTransaction.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
		t.Fatalf("validate first concurrent directory identity: %v", err)
	}
	secondValidation := make(chan error, 1)
	go func() {
		_, validationErr := secondTransaction.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE")
		secondValidation <- validationErr
	}()

	var secondValidationErr error
	secondValidationCompleted := false
	secondWaitingForLock := false
	for !secondWaitingForLock && !secondValidationCompleted {
		select {
		case secondValidationErr = <-secondValidation:
			secondValidationCompleted = true
		default:
			if err := pool.QueryRow(ctx, `
				SELECT COALESCE(wait_event_type = 'Lock', false)
				FROM pg_stat_activity
				WHERE pid = $1`, secondBackendPID).Scan(&secondWaitingForLock); err != nil {
				t.Fatalf("inspect second directory identity transaction wait state: %v", err)
			}
		}
	}

	firstCommitErr := firstTransaction.Commit(ctx)
	if secondWaitingForLock {
		secondValidationErr = <-secondValidation
	}
	var secondCommitErr error
	if secondValidationErr == nil {
		secondCommitErr = secondTransaction.Commit(ctx)
	} else {
		secondCommitErr = secondValidationErr
		_ = secondTransaction.Rollback(context.Background())
	}
	if firstCommitErr != nil {
		t.Fatalf("first concurrent directory identity commit error = %v, want winner", firstCommitErr)
	}
	if secondCommitErr == nil {
		t.Fatal("both concurrent conflicting directory identities committed, want exactly one failure")
	}
	if errors.Is(secondCommitErr, context.DeadlineExceeded) || errors.Is(secondCommitErr, context.Canceled) {
		t.Fatalf("second concurrent directory identity ended by context instead of conflict: %v", secondCommitErr)
	}
	if !strings.Contains(secondCommitErr.Error(), "directory identity has conflicting effective entities") {
		t.Fatalf("second concurrent directory identity error = %v, want effective-identity conflict", secondCommitErr)
	}

	var assertionCount, effectiveEntityCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(DISTINCT assertion.entity_id)
		FROM `+quotedSchema+`.entity_directory_identity_assertions AS assertion
		JOIN `+quotedSchema+`.resolution_decisions AS decision ON decision.id = assertion.decision_id
		WHERE assertion.provider = 'google_people'
		  AND assertion.provider_subject_id = 'synthetic-concurrent-subject'
		  AND decision.superseded_by_id IS NULL
		  AND decision.outcome IN ('accepted', 'created')
		  AND decision.currently_admissible`).Scan(&assertionCount, &effectiveEntityCount); err != nil {
		t.Fatalf("load concurrent effective directory identity result: %v", err)
	}
	if assertionCount != 1 || effectiveEntityCount != 1 {
		t.Fatalf("concurrent effective directory identities = assertions:%d entities:%d, want 1/1",
			assertionCount, effectiveEntityCount)
	}
}

type googleDirectoryMigrationFixture struct {
	entityID     string
	mentionID    string
	proposalID   string
	decisionID   string
	recordedAt   time.Time
	evidenceID   string
	extractionID string
}

type identityHistorySnapshot struct {
	table  string
	count  int64
	digest string
}

func seedGoogleDirectoryMigrationFixture(
	t *testing.T,
	pool *pgxpool.Pool,
	quotedSchema string,
) googleDirectoryMigrationFixture {
	t.Helper()
	ctx := context.Background()
	fixture := googleDirectoryMigrationFixture{
		entityID:     uuid.NewString(),
		mentionID:    uuid.NewString(),
		proposalID:   uuid.NewString(),
		decisionID:   uuid.NewString(),
		recordedAt:   time.Date(2026, time.July, 23, 14, 0, 0, 0, time.UTC),
		evidenceID:   uuid.NewString(),
		extractionID: uuid.NewString(),
	}
	sourceID := uuid.NewString()
	versionID := uuid.NewString()
	tabID := uuid.NewString()
	observationID := uuid.NewString()
	signalID := uuid.NewString()
	analysisID := uuid.NewString()
	ownerID := uuid.NewString()
	digest := func(value string) []byte {
		sum := sha256.Sum256([]byte(value))
		return sum[:]
	}

	statements := []struct {
		operation string
		query     string
		arguments []any
	}{
		{"source document", "INSERT INTO " + quotedSchema + `.source_documents
			(id, provider, provider_document_id, title, locator, recorded_at)
			VALUES ($1, 'drive', 'synthetic-directory-upgrade-document', 'Synthetic Directory Upgrade',
				'https://example.invalid/directory-upgrade', $2)`, []any{sourceID, fixture.recordedAt}},
		{"document version", "INSERT INTO " + quotedSchema + `.document_versions
			(id, source_document_id, digest, content_digest_v2, title, locator, provider_version,
			 provider_revision, recorded_at)
			VALUES ($1, $2, $3, $3, 'Synthetic Directory Upgrade',
				'https://example.invalid/directory-upgrade', 'version-1', 'revision-1', $4)`,
			[]any{versionID, sourceID, digest("directory-upgrade-version"), fixture.recordedAt}},
		{"document tab", "INSERT INTO " + quotedSchema + `.document_tabs
			(id, document_version_id, provider_tab_id, title, title_path, display_order, role, content, content_digest)
			VALUES ($1, $2, 'synthetic-tab', 'Transcript', ARRAY['Transcript'], 0, 'transcript',
				'Synthetic person discussed synthetic work.', $3)`,
			[]any{tabID, versionID, digest("directory-upgrade-tab")}},
		{"evidence span", "INSERT INTO " + quotedSchema + `.evidence_spans
			(id, document_tab_id, start_offset, end_offset, quote)
			VALUES ($1, $2, 0, 16, 'Synthetic person')`, []any{fixture.evidenceID, tabID}},
		{"entity", "INSERT INTO " + quotedSchema + `.entities
			(id, kind, display_name, recorded_at)
			VALUES ($1, 'person', 'Synthetic Person One', $2)`, []any{fixture.entityID, fixture.recordedAt}},
		{"legacy alias", "INSERT INTO " + quotedSchema + `.entity_aliases
			(entity_id, normalized_value, alias_type, recorded_at)
			VALUES ($1, 'synthetic person one', 'name', $2)`, []any{fixture.entityID, fixture.recordedAt}},
		{"extraction run", "INSERT INTO " + quotedSchema + `.extraction_runs
			(id, document_version_id, derivation_digest, model_provider, data_mode, model_id, bedrock_region,
			 max_output_tokens, prompt_version, schema_digest, processing_status, completed_by_owner,
			 recorded_at, completed_at, currently_admissible)
			VALUES ($1, $2, $3, 'bedrock', 'personal', 'synthetic-model', 'us-east-1', 256,
				'extract-directory-v1', $4, 'complete', $5, $6, $6, true)`,
			[]any{fixture.extractionID, versionID, digest("directory-upgrade-derivation"),
				digest("directory-upgrade-schema"), ownerID, fixture.recordedAt}},
		{"mention", "INSERT INTO " + quotedSchema + `.mentions
			(id, evidence_span_id, extraction_run_id, surface, normalized_name, normalized_email,
			 role, recorded_at, currently_admissible)
			VALUES ($1, $2, $3, 'Synthetic Person One', 'synthetic person one',
				'synthetic.one@example.invalid', 'speaker', $4, true)`,
			[]any{fixture.mentionID, fixture.evidenceID, fixture.extractionID, fixture.recordedAt}},
		{"proposal", "INSERT INTO " + quotedSchema + `.resolution_proposals
			(id, mention_id, status, derivation, recorded_at)
			VALUES ($1, $2, 'resolved', 'synthetic_directory_upgrade', $3)`,
			[]any{fixture.proposalID, fixture.mentionID, fixture.recordedAt}},
		{"decision", "INSERT INTO " + quotedSchema + `.resolution_decisions
			(id, proposal_id, outcome, entity_id, digest, recorded_at, currently_admissible)
			VALUES ($1, $2, 'accepted', $3, $4, $5, true)`,
			[]any{fixture.decisionID, fixture.proposalID, fixture.entityID,
				digest("directory-upgrade-decision"), fixture.recordedAt}},
		{"alias assertion", "INSERT INTO " + quotedSchema + `.entity_alias_assertions
			(decision_id, entity_id, normalized_value, alias_type, recorded_at)
			VALUES ($1, $2, 'synthetic person one', 'name', $3)`,
			[]any{fixture.decisionID, fixture.entityID, fixture.recordedAt}},
		{"observation", "INSERT INTO " + quotedSchema + `.observations
			(id, extraction_run_id, subject_entity_id, object_entity_id, subject_mention_id,
			 object_mention_id, predicate, recorded_at, derivation, epistemic_status, digest,
			 currently_admissible)
			VALUES ($1, $2, $3, $3, $4, $4, 'interaction_signal', $5,
				'synthetic_extraction', 'inferred', $6, true)`,
			[]any{observationID, fixture.extractionID, fixture.entityID, fixture.mentionID,
				fixture.recordedAt, digest("directory-upgrade-observation")}},
		{"observation evidence", "INSERT INTO " + quotedSchema + `.observation_evidence
			(observation_id, evidence_span_id) VALUES ($1, $2)`, []any{observationID, fixture.evidenceID}},
		{"analysis", "INSERT INTO " + quotedSchema + `.analysis_runs
			(id, employee_entity_id, manager_entity_id, input_digest, analysis_prompt_version,
			 policy_version, state, recorded_at, completed_at, hypothesis, report_state,
			 report_json, currently_admissible)
			VALUES ($1, $2, $2, $3, 'synthetic-analysis-v1', 'synthetic-policy-v1', 'complete',
				$4, $4, 'Synthetic hypothesis', 'mixed_or_conflicting',
				'{"status":"mixed_or_conflicting"}'::jsonb, true)`,
			[]any{analysisID, fixture.entityID, digest("directory-upgrade-analysis"), fixture.recordedAt}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.arguments...); err != nil {
			t.Fatalf("seed directory-upgrade %s: %v", statement.operation, err)
		}
	}

	transaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin directory-upgrade signal seed: %v", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.
	if _, err := transaction.Exec(ctx, "INSERT INTO "+quotedSchema+`.interaction_signals
		(id, observation_id, category, direction, extraction_model_id, prompt_version,
		 rationale, confidence, digest, currently_admissible)
		VALUES ($1, $2, 'delegation_autonomy', 'strengthening', 'synthetic-model',
			'extract-directory-v1', 'Synthetic rationale.', 0.75, $3, true)`,
		signalID, observationID, digest("directory-upgrade-signal")); err != nil {
		t.Fatalf("seed directory-upgrade signal: %v", err)
	}
	if _, err := transaction.Exec(ctx, "INSERT INTO "+quotedSchema+`.signal_evidence
		(signal_id, evidence_span_id, role) VALUES ($1, $2, 'supporting')`,
		signalID, fixture.evidenceID); err != nil {
		t.Fatalf("seed directory-upgrade signal evidence: %v", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatalf("commit directory-upgrade signal seed: %v", err)
	}
	return fixture
}

func seedAdditionalDirectoryDecision(
	t *testing.T,
	pool *pgxpool.Pool,
	quotedSchema string,
	first googleDirectoryMigrationFixture,
) googleDirectoryMigrationFixture {
	t.Helper()
	ctx := context.Background()
	second := googleDirectoryMigrationFixture{
		entityID:   uuid.NewString(),
		mentionID:  uuid.NewString(),
		proposalID: uuid.NewString(),
		decisionID: uuid.NewString(),
		recordedAt: first.recordedAt.Add(time.Minute),
	}
	decisionDigest := sha256.Sum256([]byte("directory-constraint-second-decision"))
	for _, statement := range []struct {
		operation string
		query     string
		arguments []any
	}{
		{"second entity", "INSERT INTO " + quotedSchema + `.entities
			(id, kind, display_name, recorded_at)
			VALUES ($1, 'person', 'Synthetic Person Two', $2)`, []any{second.entityID, second.recordedAt}},
		{"second mention", "INSERT INTO " + quotedSchema + `.mentions
			(id, evidence_span_id, extraction_run_id, surface, normalized_name, normalized_email,
			 role, recorded_at, currently_admissible)
			VALUES ($1, $2, $3, 'Synthetic Person Two', 'synthetic person two', '',
				'reference', $4, true)`,
			[]any{second.mentionID, first.evidenceID, first.extractionID, second.recordedAt}},
		{"second proposal", "INSERT INTO " + quotedSchema + `.resolution_proposals
			(id, mention_id, status, derivation, recorded_at)
			VALUES ($1, $2, 'resolved', 'synthetic_directory_constraint', $3)`,
			[]any{second.proposalID, second.mentionID, second.recordedAt}},
		{"second decision", "INSERT INTO " + quotedSchema + `.resolution_decisions
			(id, proposal_id, outcome, entity_id, digest, recorded_at, currently_admissible)
			VALUES ($1, $2, 'accepted', $3, $4, $5, true)`,
			[]any{second.decisionID, second.proposalID, second.entityID,
				decisionDigest[:], second.recordedAt}},
	} {
		if _, err := pool.Exec(ctx, statement.query, statement.arguments...); err != nil {
			t.Fatalf("seed %s: %v", statement.operation, err)
		}
	}
	return second
}

func captureIdentityHistory(
	t *testing.T,
	pool *pgxpool.Pool,
	quotedSchema string,
) []identityHistorySnapshot {
	t.Helper()
	ctx := context.Background()
	tables := []string{
		"entities",
		"mentions",
		"resolution_proposals",
		"resolution_decisions",
		"entity_aliases",
		"entity_alias_assertions",
		"observations",
		"interaction_signals",
		"analysis_runs",
	}
	snapshots := make([]identityHistorySnapshot, 0, len(tables))
	for _, table := range tables {
		snapshot := identityHistorySnapshot{table: table}
		if err := pool.QueryRow(ctx, `
			SELECT count(*),
			       md5(COALESCE(
			           string_agg(to_jsonb(row_value)::text, '|' ORDER BY to_jsonb(row_value)::text),
			           ''
			       ))
			FROM `+quotedSchema+`.`+table+` AS row_value`).Scan(&snapshot.count, &snapshot.digest); err != nil {
			t.Fatalf("capture %s history before directory migration: %v", table, err)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

type post00007HybridRows struct {
	versionID       string
	extractionRunID string
	mentionID       string
	decisionID      string
	observationID   string
	signalID        string
	analysisID      string
	rationale       string
	hypothesis      string
	reportJSON      string
}

func seedPost00007HybridRows(t *testing.T, pool *pgxpool.Pool, quotedSchema string, version knowledge.DocumentVersion) post00007HybridRows {
	t.Helper()
	ctx := context.Background()
	rows := post00007HybridRows{
		versionID: uuid.NewString(), extractionRunID: uuid.NewString(), mentionID: uuid.NewString(),
		decisionID: uuid.NewString(), observationID: uuid.NewString(), signalID: uuid.NewString(), analysisID: uuid.NewString(),
		rationale: "Preserved synthetic rationale.", hypothesis: "Preserved synthetic hypothesis.",
		reportJSON: `{"rationale":"preserved synthetic report"}`,
	}
	sourceID := uuid.NewString()
	tabID := uuid.NewString()
	spanID := uuid.NewString()
	entityID := uuid.NewString()
	proposalID := uuid.NewString()
	ownerID := uuid.NewString()
	recordedAt := time.Date(2026, time.July, 22, 1, 0, 0, 0, time.UTC)
	digest := func(value string) []byte {
		sum := sha256.Sum256([]byte(value))
		return sum[:]
	}
	versionDigest := version.Digest()
	section := version.Sections()[0]
	sectionDigest := sha256.Sum256([]byte(section.Text()))
	schemaDigest := sha256.Sum256(extract.ExtractionJSONSchema())
	meetingTime := version.SourceTime()

	statements := []struct {
		operation string
		query     string
		arguments []any
	}{
		{"source", "INSERT INTO " + quotedSchema + `.source_documents (id, provider, provider_document_id, title, locator, recorded_at) VALUES ($1, $2, $3, $4, $5, $6)`, []any{sourceID, version.Provider(), version.ProviderDocumentID(), version.Title(), version.Locator(), recordedAt}},
		{"hybrid version", "INSERT INTO " + quotedSchema + `.document_versions (id, source_document_id, digest, content_digest_v2, title, locator, provider_version, provider_revision, provider_modified_at, source_meeting_time, recorded_at) VALUES ($1, $2, $3, $3, $4, $5, $6, $7, $8, $9, $10)`, []any{rows.versionID, sourceID, versionDigest[:], version.Title(), version.Locator(), version.ProviderVersion(), version.ProviderRevision(), version.ModifiedAt(), meetingTime, recordedAt}},
		{"tab", "INSERT INTO " + quotedSchema + `.document_tabs (id, document_version_id, provider_tab_id, title, title_path, display_order, role, content, content_digest) VALUES ($1, $2, $3, $4, $5, 0, 'transcript', $6, $7)`, []any{tabID, rows.versionID, section.ID(), section.Title(), section.Path(), section.Text(), sectionDigest[:]}},
		{"span", "INSERT INTO " + quotedSchema + `.evidence_spans (id, document_tab_id, start_offset, end_offset, quote) VALUES ($1, $2, 0, $3, $4)`, []any{spanID, tabID, len(section.Text()), section.Text()}},
		{"entity", "INSERT INTO " + quotedSchema + `.entities (id, kind, display_name, recorded_at) VALUES ($1, 'person', 'Synthetic Person', $2)`, []any{entityID, recordedAt}},
		{"v4 run", "INSERT INTO " + quotedSchema + `.extraction_runs (id, document_version_id, derivation_digest, model_id, bedrock_region, max_output_tokens, prompt_version, schema_digest, processing_status, completed_by_owner, recorded_at, completed_at, currently_admissible) VALUES ($1, $2, $3, 'synthetic-model', 'us-east-1', 256, 'extract-v2', $4, 'complete', $5, $6, $6, true)`, []any{rows.extractionRunID, rows.versionID, digest("snapshot-merge-v4"), schemaDigest[:], ownerID, recordedAt}},
		{"mention", "INSERT INTO " + quotedSchema + `.mentions (id, evidence_span_id, extraction_run_id, surface, normalized_name, role, recorded_at, currently_admissible) VALUES ($1, $2, $3, 'Synthetic', 'synthetic', 'speaker', $4, true)`, []any{rows.mentionID, spanID, rows.extractionRunID, recordedAt}},
		{"proposal", "INSERT INTO " + quotedSchema + `.resolution_proposals (id, mention_id, status, derivation, recorded_at) VALUES ($1, $2, 'resolved', 'model_extraction', $3)`, []any{proposalID, rows.mentionID, recordedAt}},
		{"decision", "INSERT INTO " + quotedSchema + `.resolution_decisions (id, proposal_id, outcome, entity_id, digest, recorded_at, currently_admissible) VALUES ($1, $2, 'accepted', $3, $4, $5, true)`, []any{rows.decisionID, proposalID, entityID, digest("snapshot-decision"), recordedAt}},
		{"alias", "INSERT INTO " + quotedSchema + `.entity_alias_assertions (decision_id, entity_id, normalized_value, alias_type, recorded_at) VALUES ($1, $2, 'synthetic', 'name', $3)`, []any{rows.decisionID, entityID, recordedAt}},
		{"observation", "INSERT INTO " + quotedSchema + `.observations (id, extraction_run_id, subject_entity_id, object_entity_id, subject_mention_id, object_mention_id, predicate, valid_start, recorded_at, derivation, epistemic_status, digest, currently_admissible) VALUES ($1, $2, $3, $3, $4, $4, 'interaction_signal', $5, $6, 'model_extraction', 'inferred', $7, true)`, []any{rows.observationID, rows.extractionRunID, entityID, rows.mentionID, meetingTime, recordedAt, digest("snapshot-observation")}},
		{"observation evidence", "INSERT INTO " + quotedSchema + `.observation_evidence (observation_id, evidence_span_id) VALUES ($1, $2)`, []any{rows.observationID, spanID}},
		{"analysis", "INSERT INTO " + quotedSchema + `.analysis_runs (id, employee_entity_id, manager_entity_id, input_digest, analysis_prompt_version, policy_version, state, recorded_at, completed_at, hypothesis, report_state, report_json, currently_admissible) VALUES ($1, $2, $2, $3, 'analyze-v1', 'manager-confidence-policy-v5', 'complete', $4, $4, $5, 'possible declining-confidence signal', $6::jsonb, true)`, []any{rows.analysisID, entityID, digest("snapshot-analysis-v5"), recordedAt, rows.hypothesis, rows.reportJSON}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.arguments...); err != nil {
			t.Fatalf("seed post-00007 hybrid %s: %v", statement.operation, err)
		}
	}

	transaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("start post-00007 hybrid signal seed: %v", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.
	if _, err := transaction.Exec(ctx, "INSERT INTO "+quotedSchema+`.interaction_signals (id, observation_id, category, direction, extraction_model_id, prompt_version, rationale, confidence, digest, currently_admissible) VALUES ($1, $2, 'delegation_autonomy', 'weakening', 'synthetic-model', 'extract-v2', $3, 0.8, $4, true)`, rows.signalID, rows.observationID, rows.rationale, digest("snapshot-signal")); err != nil {
		t.Fatalf("seed post-00007 hybrid signal: %v", err)
	}
	if _, err := transaction.Exec(ctx, "INSERT INTO "+quotedSchema+`.signal_evidence (signal_id, evidence_span_id, role) VALUES ($1, $2, 'supporting')`, rows.signalID, spanID); err != nil {
		t.Fatalf("seed post-00007 hybrid signal evidence: %v", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatalf("commit post-00007 hybrid signal: %v", err)
	}
	return rows
}

type snapshotCoherenceSource struct {
	listed  source.Document
	fetched source.Document
}

func (boundary *snapshotCoherenceSource) List(context.Context, string) ([]source.Document, error) {
	return []source.Document{boundary.listed}, nil
}

func (boundary *snapshotCoherenceSource) Get(context.Context, string) (source.Document, error) {
	return boundary.fetched, nil
}

type snapshotCoherenceModel struct{ calls int }

func (model *snapshotCoherenceModel) Generate(context.Context, extract.Request) (extract.Response, error) {
	model.calls++
	return extract.Response{
		Output:  []byte(`{"meeting_date":"","citations":[],"people":[],"statements":[],"signals":[]}`),
		ModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion, Outcome: "success",
	}, nil
}

type snapshotCoherenceRepository struct {
	pool            *pgxpool.Pool
	quotedSchema    string
	preparedVersion knowledge.DocumentVersion
	versionID       string
	derivationID    string
	leaseOwner      string
}

func (repository *snapshotCoherenceRepository) PrepareVersion(ctx context.Context, version knowledge.DocumentVersion, derivation ingest.DerivationIdentity, _ modelpolicy.DataMode, leaseDuration time.Duration) (ingest.VersionState, error) {
	wantDigest, err := ingest.ComputeDerivationDigest(version, derivation)
	if err != nil || wantDigest != derivation.Digest || leaseDuration <= 0 {
		return ingest.VersionState{}, errors.New("snapshot-coherence derivation is invalid")
	}
	repository.preparedVersion = version
	repository.versionID = uuid.NewString()
	repository.derivationID = uuid.NewString()
	repository.leaseOwner = uuid.NewString()
	var sourceID string
	err = repository.pool.QueryRow(ctx, "SELECT id FROM "+repository.quotedSchema+`.source_documents WHERE provider = $1 AND provider_document_id = $2`, version.Provider(), version.ProviderDocumentID()).Scan(&sourceID)
	if err != nil {
		return ingest.VersionState{}, err
	}
	versionDigest := version.Digest()
	if _, err := repository.pool.Exec(ctx, "INSERT INTO "+repository.quotedSchema+`.document_versions (id, source_document_id, digest, content_digest_v2, title, locator, provider_version, provider_revision, provider_modified_at, source_meeting_time, recorded_at) VALUES ($1, $2, $3, $3, $4, $5, $6, $7, $8, $9, $10)`, repository.versionID, sourceID, versionDigest[:], version.Title(), version.Locator(), version.ProviderVersion(), version.ProviderRevision(), version.ModifiedAt(), version.SourceTime(), version.RecordedAt()); err != nil {
		return ingest.VersionState{}, err
	}
	for _, section := range version.Sections() {
		sectionDigest := sha256.Sum256([]byte(section.Text()))
		if _, err := repository.pool.Exec(ctx, "INSERT INTO "+repository.quotedSchema+`.document_tabs (document_version_id, provider_tab_id, title, parent_provider_tab_id, title_path, display_order, role, content, content_digest) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, repository.versionID, section.ID(), section.Title(), section.ParentID(), section.Path(), section.Order(), section.Role(), section.Text(), sectionDigest[:]); err != nil {
			return ingest.VersionState{}, err
		}
	}
	leaseExpiresAt := time.Now().UTC().Add(leaseDuration)
	if _, err := repository.pool.Exec(ctx, "INSERT INTO "+repository.quotedSchema+`.extraction_runs (id, document_version_id, derivation_digest, model_id, bedrock_region, max_output_tokens, prompt_version, schema_digest, lease_owner, lease_expires_at, recorded_at, currently_admissible) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, true)`, repository.derivationID, repository.versionID, derivation.Digest[:], derivation.ModelID, derivation.Region, derivation.MaxTokens, derivation.PromptVersion, derivation.SchemaDigest[:], repository.leaseOwner, leaseExpiresAt, time.Now().UTC()); err != nil {
		return ingest.VersionState{}, err
	}
	return ingest.VersionState{
		ID: repository.versionID, DerivationID: repository.derivationID, DerivationDigest: derivation.Digest,
		LeaseOwner: repository.leaseOwner, LeaseExpiresAt: leaseExpiresAt, Status: ingest.VersionStatusPending,
	}, nil
}

func (repository *snapshotCoherenceRepository) CompleteVersion(ctx context.Context, completion ingest.Completion) error {
	if completion.VersionID != repository.versionID || completion.DerivationID != repository.derivationID || completion.LeaseOwner != repository.leaseOwner ||
		len(completion.Evidence) != 0 || len(completion.Mentions) != 0 || len(completion.Observations) != 0 || len(completion.Signals) != 0 {
		return errors.New("snapshot-coherence completion is invalid")
	}
	result, err := repository.pool.Exec(ctx, "UPDATE "+repository.quotedSchema+`.extraction_runs SET processing_status = 'complete', completed_by_owner = $2, completed_at = $3, lease_owner = NULL, lease_expires_at = NULL WHERE id = $1 AND lease_owner = $2`, repository.derivationID, repository.leaseOwner, time.Now().UTC())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("snapshot-coherence lease is not owned")
	}
	return nil
}

func (*snapshotCoherenceRepository) RecordFailure(context.Context, string, string, ingest.VersionStatus, ingest.FailureCode) error {
	return errors.New("snapshot-coherence sync unexpectedly failed")
}

func (*snapshotCoherenceRepository) EntitySnapshots(context.Context) ([]entity.EntitySnapshot, error) {
	return nil, nil
}

func snapshotCoherenceDocument(id, title string) source.Document {
	return source.Document{
		Provider: "drive", ID: id, Title: title,
		Locator: "https://example.invalid/snapshot-coherence-document", Version: "version-1", Revision: "revision-1",
		ModifiedAt: time.Date(2026, time.July, 22, 0, 0, 0, 0, time.UTC),
		Tabs: []source.Tab{{
			ID: "transcript-tab", Title: "Transcript", Path: []string{"Transcript"}, Order: 0,
			Role: source.TabRoleTranscript, Text: "Synthetic meeting content.",
		}},
	}
}

func snapshotCoherenceVersion(t *testing.T, document source.Document) knowledge.DocumentVersion {
	t.Helper()
	version, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
		Provider: document.Provider, ProviderDocumentID: document.ID, Title: document.Title,
		Locator: document.Locator, ProviderVersion: document.Version, ProviderRevision: document.Revision,
		ModifiedAt: document.ModifiedAt, SourceTime: document.MeetingTime,
		RecordedAt: time.Date(2026, time.July, 22, 1, 0, 0, 0, time.UTC), Sections: testEvidenceSections(t, document.Tabs),
	})
	if err != nil {
		t.Fatalf("build snapshot-coherence version: %v", err)
	}
	return version
}

type post00006UnsafeRows struct {
	extractionRunID string
	mentionID       string
	decisionID      string
	observationID   string
	signalID        string
	analysisID      string
	email           string
	rationale       string
	hypothesis      string
	reportJSON      string
}

func seedPost00006UnsafeRows(t *testing.T, pool *pgxpool.Pool, quotedSchema string) post00006UnsafeRows {
	t.Helper()
	ctx := context.Background()
	unsafe := post00006UnsafeRows{
		extractionRunID: uuid.NewString(), mentionID: uuid.NewString(), decisionID: uuid.NewString(),
		observationID: uuid.NewString(), signalID: uuid.NewString(), analysisID: uuid.NewString(),
		email:     "bob.builder@synthetic.example",
		rationale: "Superseded model rationale.", hypothesis: "Superseded hidden-state hypothesis.",
		reportJSON: `{"rationale":"superseded hidden-state report"}`,
	}
	sourceID := uuid.NewString()
	versionID := uuid.NewString()
	tabID := uuid.NewString()
	spanID := uuid.NewString()
	entityID := uuid.NewString()
	proposalID := uuid.NewString()
	ownerID := uuid.NewString()
	recordedAt := time.Date(2026, time.July, 22, 1, 0, 0, 0, time.UTC)
	digest := func(value string) []byte {
		sum := sha256.Sum256([]byte(value))
		return sum[:]
	}

	statements := []struct {
		operation string
		query     string
		arguments []any
	}{
		{"source", "INSERT INTO " + quotedSchema + `.source_documents (id, provider, provider_document_id, title, locator, recorded_at) VALUES ($1, 'drive', 'post-00006-document', 'Synthetic', 'https://example.invalid/synthetic', $2)`, []any{sourceID, recordedAt}},
		{"version", "INSERT INTO " + quotedSchema + `.document_versions (id, source_document_id, digest, title, locator, provider_version, provider_revision, provider_modified_at, recorded_at) VALUES ($1, $2, $3, 'Synthetic', 'https://example.invalid/synthetic', 'version-1', 'revision-1', $4, $4)`, []any{versionID, sourceID, digest("legacy-revision-inclusive-version"), recordedAt}},
		{"tab", "INSERT INTO " + quotedSchema + `.document_tabs (id, document_version_id, provider_tab_id, title, title_path, display_order, role, content, content_digest) VALUES ($1, $2, 'tab-1', 'Transcript', ARRAY['Transcript'], 0, 'transcript', 'Alex Reviewer asked Bob Builder.', $3)`, []any{tabID, versionID, digest("tab")}},
		{"span", "INSERT INTO " + quotedSchema + `.evidence_spans (id, document_tab_id, start_offset, end_offset, quote) VALUES ($1, $2, 0, 32, 'Alex Reviewer asked Bob Builder.')`, []any{spanID, tabID}},
		{"entity", "INSERT INTO " + quotedSchema + `.entities (id, kind, display_name, recorded_at) VALUES ($1, 'person', 'Synthetic Person', $2)`, []any{entityID, recordedAt}},
		{"run", "INSERT INTO " + quotedSchema + `.extraction_runs (id, document_version_id, derivation_digest, model_id, bedrock_region, max_output_tokens, prompt_version, schema_digest, processing_status, completed_by_owner, recorded_at, completed_at, currently_admissible) VALUES ($1, $2, $3, 'synthetic-model', 'us-east-1', 256, 'extract-v1', $4, 'complete', $5, $6, $6, true)`, []any{unsafe.extractionRunID, versionID, digest("derivation-v3"), digest("schema-v1"), ownerID, recordedAt}},
		{"mention", "INSERT INTO " + quotedSchema + `.mentions (id, evidence_span_id, extraction_run_id, surface, normalized_name, normalized_email, role, recorded_at, currently_admissible) VALUES ($1, $2, $3, 'Alex Reviewer', 'alex reviewer', $4, 'speaker', $5, true)`, []any{unsafe.mentionID, spanID, unsafe.extractionRunID, unsafe.email, recordedAt}},
		{"proposal", "INSERT INTO " + quotedSchema + `.resolution_proposals (id, mention_id, status, derivation, recorded_at) VALUES ($1, $2, 'resolved', 'model_extraction', $3)`, []any{proposalID, unsafe.mentionID, recordedAt}},
		{"decision", "INSERT INTO " + quotedSchema + `.resolution_decisions (id, proposal_id, outcome, entity_id, digest, recorded_at, currently_admissible) VALUES ($1, $2, 'accepted', $3, $4, $5, true)`, []any{unsafe.decisionID, proposalID, entityID, digest("decision"), recordedAt}},
		{"alias", "INSERT INTO " + quotedSchema + `.entity_alias_assertions (decision_id, entity_id, normalized_value, alias_type, recorded_at) VALUES ($1, $2, $3, 'email', $4)`, []any{unsafe.decisionID, entityID, unsafe.email, recordedAt}},
		{"observation", "INSERT INTO " + quotedSchema + `.observations (id, extraction_run_id, subject_entity_id, object_entity_id, subject_mention_id, object_mention_id, predicate, recorded_at, derivation, epistemic_status, digest, currently_admissible) VALUES ($1, $2, $3, $3, $4, $4, 'interaction_signal', $5, 'model_extraction', 'inferred', $6, true)`, []any{unsafe.observationID, unsafe.extractionRunID, entityID, unsafe.mentionID, recordedAt, digest("observation")}},
		{"observation evidence", "INSERT INTO " + quotedSchema + `.observation_evidence (observation_id, evidence_span_id) VALUES ($1, $2)`, []any{unsafe.observationID, spanID}},
		{"analysis", "INSERT INTO " + quotedSchema + `.analysis_runs (id, employee_entity_id, manager_entity_id, input_digest, analysis_prompt_version, policy_version, state, recorded_at, completed_at, hypothesis, report_state, report_json, currently_admissible) VALUES ($1, $2, $2, $3, 'analyze-v1', 'manager-confidence-policy-v4', 'complete', $4, $4, $5, 'possible declining-confidence signal', $6::jsonb, true)`, []any{unsafe.analysisID, entityID, digest("analysis-v4"), recordedAt, unsafe.hypothesis, unsafe.reportJSON}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.arguments...); err != nil {
			t.Fatalf("seed post-00006 unsafe %s: %v", statement.operation, err)
		}
	}

	transaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("start post-00006 signal seed: %v", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.
	if _, err := transaction.Exec(ctx, "INSERT INTO "+quotedSchema+`.interaction_signals (id, observation_id, category, direction, extraction_model_id, prompt_version, rationale, confidence, digest, currently_admissible) VALUES ($1, $2, 'delegation_autonomy', 'weakening', 'synthetic-model', 'extract-v1', $3, 0.9, $4, true)`, unsafe.signalID, unsafe.observationID, unsafe.rationale, digest("signal")); err != nil {
		t.Fatalf("seed post-00006 unsafe signal: %v", err)
	}
	if _, err := transaction.Exec(ctx, "INSERT INTO "+quotedSchema+`.signal_evidence (signal_id, evidence_span_id, role) VALUES ($1, $2, 'supporting')`, unsafe.signalID, spanID); err != nil {
		t.Fatalf("seed post-00006 unsafe signal evidence: %v", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatalf("commit post-00006 unsafe signal: %v", err)
	}
	return unsafe
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

func applyMigrationsThrough00009ToSchema(t *testing.T, pool *pgxpool.Pool, quotedSchema string) {
	t.Helper()
	for _, migration := range []string{
		"00002_manager_confidence_poc.sql",
		"00003_ingestion_processing_state.sql",
		"00004_temporal_pair_analysis.sql",
		"00005_manager_confidence_final_fixes.sql",
		"00006_legacy_admission_boundary.sql",
		"00007_compatibility_admission_boundary.sql",
		"00008_snapshot_coherence_admission_boundary.sql",
		"00009_doctor_migration_inspection.sql",
	} {
		applyMigrationToSchema(t, pool, quotedSchema, migration)
	}
}

func applyMigrationsThrough00011ToSchema(t *testing.T, pool *pgxpool.Pool, quotedSchema string) {
	t.Helper()
	applyMigrationsThrough00009ToSchema(t, pool, quotedSchema)
	for _, migration := range []string{
		"00010_model_provider_provenance.sql",
		"00011_current_document_version.sql",
	} {
		applyMigrationToSchema(t, pool, quotedSchema, migration)
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

func TestPutDocumentVersionReusesRevisionInclusiveLegacyDigestAcrossUpgrade(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	providerDocumentID := testIdentifier("document-legacy-digest-compatibility")
	newVersion := func(revision string) knowledge.DocumentVersion {
		version, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
			Provider: "synthetic-drive", ProviderDocumentID: providerDocumentID,
			Title:           "Synthetic compatibility meeting",
			Locator:         "https://docs.example.invalid/document/" + providerDocumentID,
			ProviderVersion: "synthetic-version-1", ProviderRevision: revision,
			ModifiedAt: time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC),
			RecordedAt: time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
			Sections: testEvidenceSections(t, []source.Tab{{
				ID: "tab-synthetic", Title: "Synthetic Transcript", Path: []string{"Synthetic Transcript"},
				Order: 0, Role: source.TabRoleTranscript, Text: "Synthetic compatibility text.",
			}}),
		})
		if err != nil {
			t.Fatalf("new compatibility document version: %v", err)
		}
		return version
	}
	legacyVersion := newVersion("synthetic-revision-1")
	legacyDigest := legacyVersion.LegacyRevisionInclusiveDigest()
	sourceID := uuid.NewString()
	legacyVersionID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO stacks.source_documents (id, provider, provider_document_id, title, locator, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, sourceID, legacyVersion.Provider(), legacyVersion.ProviderDocumentID(),
		legacyVersion.Title(), legacyVersion.Locator(), legacyVersion.RecordedAt()); err != nil {
		t.Fatalf("seed legacy source document: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO stacks.document_versions
			(id, source_document_id, digest, title, locator, provider_version, provider_revision,
			 provider_modified_at, source_meeting_time, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		legacyVersionID, sourceID, legacyDigest[:], legacyVersion.Title(), legacyVersion.Locator(),
		legacyVersion.ProviderVersion(), legacyVersion.ProviderRevision(), legacyVersion.ModifiedAt(),
		legacyVersion.SourceTime(), legacyVersion.RecordedAt()); err != nil {
		t.Fatalf("seed legacy document version: %v", err)
	}
	for _, section := range legacyVersion.Sections() {
		contentDigest := sha256.Sum256([]byte(section.Text()))
		if _, err := pool.Exec(ctx, `
			INSERT INTO stacks.document_tabs
				(document_version_id, provider_tab_id, title, parent_provider_tab_id, title_path,
				 display_order, role, content, content_digest)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, legacyVersionID, section.ID(), section.Title(),
			section.ParentID(), section.Path(), section.Order(), section.Role(), section.Text(), contentDigest[:]); err != nil {
			t.Fatalf("seed legacy document tab: %v", err)
		}
	}

	repository := NewDocumentRepository(pool)
	revisionChurned := newVersion("synthetic-revision-2")
	first, created, err := repository.PutDocumentVersion(ctx, revisionChurned)
	if err != nil {
		t.Fatalf("reuse legacy digest on first upgraded sync: %v", err)
	}
	if created || first.ID != legacyVersionID {
		t.Fatalf("first upgraded write = (%q, created=%t), want legacy version %q reused", first.ID, created, legacyVersionID)
	}

	second, created, err := repository.PutDocumentVersion(ctx, revisionChurned)
	if err != nil {
		t.Fatalf("reuse stable compatibility identity after revision churn: %v", err)
	}
	if created || second.ID != legacyVersionID {
		t.Fatalf("revision-churned write = (%q, created=%t), want legacy version %q reused", second.ID, created, legacyVersionID)
	}

	var versionCount int
	var stableDigest []byte
	var storedRevision string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stacks.document_versions WHERE source_document_id = $1`, sourceID).Scan(&versionCount); err != nil {
		t.Fatalf("count upgraded content versions: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT content_digest_v2, provider_revision
		FROM stacks.document_versions WHERE id = $1`, legacyVersionID).Scan(&stableDigest, &storedRevision); err != nil {
		t.Fatalf("load upgraded content identity: %v", err)
	}
	currentDigest := legacyVersion.Digest()
	if versionCount != 1 || string(stableDigest) != string(currentDigest[:]) || storedRevision != "synthetic-revision-1" {
		t.Fatalf("upgraded version count/digest/revision = %d/%x/%q, want one stable alias and immutable original provenance",
			versionCount, stableDigest, storedRevision)
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

func TestModelProviderProvenancePersistsAnalysisCompletionAndDeduplicatesAcrossModes(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	entities := NewEntityRepository(pool)
	repository := NewAnalysisRepository(pool)
	employee, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Provider Employee"})
	if err != nil {
		t.Fatalf("create provider employee: %v", err)
	}
	manager, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Provider Manager"})
	if err != nil {
		t.Fatalf("create provider manager: %v", err)
	}
	acceptSyntheticIdentity(t, pool, employee.ID)
	acceptSyntheticIdentity(t, pool, manager.ID)
	pair, err := repository.LoadPairInputs(ctx, employee.ID, manager.ID)
	if err != nil || !pair.Accepted {
		t.Fatalf("load accepted provider pair = (%#v, %v)", pair, err)
	}

	identity := analysisdomain.AnalysisIdentity{
		EmployeeEntityID: employee.ID, ManagerEntityID: manager.ID,
		PromptVersion: "analyze-provider-v1", PolicyVersion: "policy-provider-v1",
		Provider: modelpolicy.ProviderBedrock, Region: "us-east-1",
		ModelID: "synthetic-model", MaxTokens: 256, Inputs: pair.Inputs,
	}
	identity.InputDigest, err = analysisdomain.ComputeInputDigest(identity)
	if err != nil {
		t.Fatalf("compute provider analysis identity: %v", err)
	}
	report := analysisdomain.Report{
		Status: analysisdomain.StatusInsufficientEvidence, Rationale: "Synthetic provider report.",
		RecordedAt: time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC),
		ModelID:    identity.ModelID, Region: identity.Region, MaxTokens: identity.MaxTokens,
		PromptVersion: identity.PromptVersion, PolicyVersion: identity.PolicyVersion,
	}
	personal, err := repository.CompleteAnalysis(ctx, analysisdomain.Completion{
		Identity: identity, Report: report, DataMode: modelpolicy.DataModePersonal,
	})
	if err != nil {
		t.Fatalf("complete personal Bedrock analysis: %v", err)
	}
	restricted, err := repository.CompleteAnalysis(ctx, analysisdomain.Completion{
		Identity: identity, Report: report, DataMode: modelpolicy.DataModeRestricted,
	})
	if err != nil {
		t.Fatalf("deduplicate restricted Bedrock analysis: %v", err)
	}
	if restricted.ID != personal.ID {
		t.Fatalf("restricted analysis ID = %q, want completed personal row %q", restricted.ID, personal.ID)
	}
	var provider, dataMode, region string
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT min(model_provider), min(data_mode), min(bedrock_region), count(*)
		FROM stacks.analysis_runs WHERE input_digest = $1`, identity.InputDigest[:]).Scan(&provider, &dataMode, &region, &count); err != nil {
		t.Fatalf("load Bedrock analysis provenance: %v", err)
	}
	if provider != string(modelpolicy.ProviderBedrock) || dataMode != string(modelpolicy.DataModePersonal) || region != identity.Region || count != 1 {
		t.Fatalf("Bedrock analysis provenance/count = %q/%q/%q/%d", provider, dataMode, region, count)
	}

	directIdentity := identity
	directIdentity.Provider = modelpolicy.ProviderOpenAI
	directIdentity.Region = ""
	directIdentity.InputDigest, err = analysisdomain.ComputeInputDigest(directIdentity)
	if err != nil {
		t.Fatalf("compute OpenAI analysis identity: %v", err)
	}
	directReport := report
	directReport.Region = ""
	direct, err := repository.CompleteAnalysis(ctx, analysisdomain.Completion{
		Identity: directIdentity, Report: directReport, DataMode: modelpolicy.DataModePersonal,
	})
	if err != nil {
		t.Fatalf("complete OpenAI analysis: %v", err)
	}
	var directProvider, directMode string
	var directRegion *string
	if err := pool.QueryRow(ctx, `
		SELECT model_provider, data_mode, bedrock_region
		FROM stacks.analysis_runs WHERE id = $1`, direct.ID).Scan(&directProvider, &directMode, &directRegion); err != nil {
		t.Fatalf("load OpenAI analysis provenance: %v", err)
	}
	if directProvider != string(modelpolicy.ProviderOpenAI) || directMode != string(modelpolicy.DataModePersonal) || directRegion != nil {
		t.Fatalf("OpenAI analysis provenance = %q/%q/%v, want openai/personal/NULL", directProvider, directMode, directRegion)
	}

	deterministicIdentity := directIdentity
	deterministicIdentity.PromptVersion = "analyze-deterministic-v1"
	deterministicIdentity.InputDigest, err = analysisdomain.ComputeInputDigest(deterministicIdentity)
	if err != nil {
		t.Fatalf("compute deterministic analysis identity: %v", err)
	}
	deterministicReport := directReport
	deterministicReport.PromptVersion = deterministicIdentity.PromptVersion
	deterministic, err := repository.CompleteAnalysis(ctx, analysisdomain.Completion{
		Identity: deterministicIdentity, Report: deterministicReport,
	})
	if err != nil {
		t.Fatalf("complete deterministic non-model analysis: %v", err)
	}
	var modelProvider, storedDataMode, modelID, storedRegion *string
	var maxTokens *int
	if err := pool.QueryRow(ctx, `
		SELECT model_provider, data_mode, model_id, bedrock_region, max_output_tokens
		FROM stacks.analysis_runs WHERE id = $1`, deterministic.ID).Scan(
		&modelProvider, &storedDataMode, &modelID, &storedRegion, &maxTokens,
	); err != nil {
		t.Fatalf("load deterministic analysis provenance: %v", err)
	}
	if modelProvider != nil || storedDataMode != nil || modelID != nil || storedRegion != nil || maxTokens != nil {
		t.Fatalf("deterministic analysis stored model provenance = %v/%v/%v/%v/%v, want all NULL",
			modelProvider, storedDataMode, modelID, storedRegion, maxTokens)
	}
}

func TestPairAnalysisUsesOnlyCurrentCompletedVersionOfEachSourceDocument(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	entities := NewEntityRepository(pool)
	repository := NewAnalysisRepository(pool)

	employee, err := entities.CreateEntity(ctx, EntityInput{
		ID: uuid.NewString(), Kind: "person", DisplayName: "Jordan Employee",
	})
	if err != nil {
		t.Fatalf("create employee: %v", err)
	}
	manager, err := entities.CreateEntity(ctx, EntityInput{
		ID: uuid.NewString(), Kind: "person", DisplayName: "Alex Manager",
	})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}

	providerDocumentID := testIdentifier("document-current-version-analysis")
	completeVersionedPairSignal(t, pool, providerDocumentID, "synthetic-revision-1",
		"Alex Manager assigned planning work to Jordan Employee.", employee.ID, manager.ID)
	completeVersionedPairSignal(t, pool, providerDocumentID, "synthetic-revision-2",
		"Alex Manager assigned final review work to Jordan Employee.", employee.ID, manager.ID)

	pair, err := repository.LoadPairInputs(ctx, employee.ID, manager.ID)
	if err != nil {
		t.Fatalf("load current-version pair inputs: %v", err)
	}
	if !pair.Accepted || len(pair.Signals) != 1 {
		t.Fatalf("current-version pair accepted/signals = %t/%d, want true/1", pair.Accepted, len(pair.Signals))
	}
	if len(pair.Signals[0].Citations) != 1 ||
		pair.Signals[0].Citations[0].Quote != "Alex Manager assigned final review work to Jordan Employee." {
		t.Fatalf("current-version evidence = %#v, want only the latest completed source version", pair.Signals[0].Citations)
	}
}

func completeVersionedPairSignal(
	t *testing.T,
	pool *pgxpool.Pool,
	providerDocumentID string,
	providerRevision string,
	transcript string,
	employeeID string,
	managerID string,
) {
	t.Helper()
	ctx := context.Background()
	recordedAt := time.Now().UTC()
	version, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
		Provider:           "synthetic-drive",
		ProviderDocumentID: providerDocumentID,
		Title:              "[2026-07-24] Synthetic current-version meeting",
		Locator:            "https://docs.example.invalid/document/" + providerDocumentID,
		ProviderVersion:    "synthetic-version",
		ProviderRevision:   providerRevision,
		ModifiedAt:         recordedAt,
		RecordedAt:         recordedAt,
		Sections: testEvidenceSections(t, []source.Tab{{
			ID: "tab-synthetic", Title: "Synthetic Transcript", Order: 0,
			Role: source.TabRoleTranscript, Text: transcript,
		}}),
	})
	if err != nil {
		t.Fatalf("new current-version document: %v", err)
	}
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, SectionID: "tab-synthetic",
		StartOffset: 0, EndOffset: len(transcript), Quote: transcript,
	})
	if err != nil {
		t.Fatalf("new current-version evidence: %v", err)
	}
	ingestion := NewIngestionRepository(pool)
	state, err := ingestion.PrepareVersion(
		ctx,
		version,
		testExtractionDerivation(t, version),
		modelpolicy.DataModePersonal,
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("prepare current-version extraction: %v", err)
	}
	validTime := time.Date(2026, time.July, 24, 0, 0, 0, 0, time.UTC)
	observationID := uuid.NewString()
	if err := ingestion.CompleteVersion(ctx, ingest.Completion{
		VersionID: state.ID, DerivationID: state.DerivationID, LeaseOwner: state.LeaseOwner,
		DataMode: modelpolicy.DataModePersonal,
		Evidence: []ingest.EvidenceRecord{{Key: "evidence", Span: span}},
		Mentions: []ingest.MentionRecord{
			{
				Key: "manager", EvidenceKey: "evidence", Surface: "Alex Manager",
				NormalizedName: entity.NormalizeName("Alex Manager"), Role: "speaker",
				Resolution: entity.Resolution{EntityID: managerID, AutoResolved: true},
			},
			{
				Key: "employee", EvidenceKey: "evidence", Surface: "Jordan Employee",
				NormalizedName: entity.NormalizeName("Jordan Employee"), Role: "reference",
				Resolution: entity.Resolution{EntityID: employeeID, AutoResolved: true},
			},
		},
		Observations: []ingest.ObservationRecord{{
			ID: observationID, SubjectEntityID: managerID, ObjectEntityID: employeeID,
			SubjectMentionKey: "manager", ObjectMentionKey: "employee",
			Predicate: "interaction_signal", ValidStart: &validTime, EvidenceKeys: []string{"evidence"},
		}},
		Signals: []ingest.SignalRecord{{
			ID: uuid.NewString(), ObservationID: observationID,
			Category: "delegation_autonomy", Direction: "strengthening",
			ExtractionModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion,
			Rationale: "Synthetic source-grounded signal.", Confidence: 0.9,
			Evidence: []ingest.SignalEvidenceRecord{{EvidenceKey: "evidence", Role: "supporting"}},
		}},
	}); err != nil {
		t.Fatalf("complete current-version extraction: %v", err)
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
		Provider: modelpolicy.ProviderBedrock,
		Region:   "us-east-1", ModelID: "synthetic-model", MaxTokens: 256, Inputs: acceptedEmpty.Inputs,
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
		Provider: modelpolicy.ProviderBedrock,
		Region:   "us-east-1", ModelID: "synthetic-model", MaxTokens: 256, Inputs: accepted.Inputs,
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
		DataMode: modelpolicy.DataModePersonal,
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
		Provider: modelpolicy.ProviderBedrock,
		Region:   "us-east-1", ModelID: "synthetic-model", MaxTokens: 256, Inputs: corrected.Inputs,
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
		Document: version, SectionID: "tab-synthetic", StartOffset: 0, EndOffset: len("Synthetic"), Quote: "Synthetic",
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
	run := testOwningExtractionRun(t, pool, version)
	canonicalObservation := testCanonicalObservation(t, run, uuid.NewString(), "", managerMention.ID, "", employeeMention.ID,
		"interaction_signal", testCanonicalInstant(t, validTime), storedSpan.ID)
	observation, _, err := graph.CompleteObservation(ctx, canonicalObservation, []knowledge.EvidenceID{knowledge.EvidenceID(storedSpan.ID)}, &SignalInput{
		ID: uuid.NewString(), ObservationID: string(canonicalObservation.ID()), Category: "delegation_autonomy", Direction: direction,
		ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion, Rationale: "Synthetic observable pair rationale.", Confidence: 0.5,
	}, []SignalEvidenceInput{{EvidenceSpanID: storedSpan.ID, Role: "supporting"}})
	if err != nil {
		t.Fatalf("complete pair observation: %v", err)
	}
	if observation.ID() != canonicalObservation.ID() {
		t.Fatalf("complete pair observation ID = %q, want %q", observation.ID(), canonicalObservation.ID())
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

	run := testOwningExtractionRun(t, pool, testDocumentVersion(t, testIdentifier("document-graph-retry")))
	observationInput := testCanonicalObservation(t, run, uuid.NewString(), entity.ID, "", "", "", "interacted_with", observation.UnknownTime(), spanID)
	signalInput := SignalInput{
		ID:                uuid.NewString(),
		ObservationID:     string(observationInput.ID()),
		Category:          "delegation_autonomy",
		Direction:         "strengthening",
		ExtractionModelID: run.ModelID,
		PromptVersion:     run.PromptVersion,
		Confidence:        0.8,
	}
	firstObservation, firstSignal, err := graph.CompleteObservation(ctx, observationInput, []knowledge.EvidenceID{knowledge.EvidenceID(spanID)}, &signalInput, []SignalEvidenceInput{{EvidenceSpanID: spanID, Role: "supporting"}})
	if err != nil {
		t.Fatalf("complete observation: %v", err)
	}
	secondObservation, secondSignal, err := graph.CompleteObservation(ctx, observationInput, []knowledge.EvidenceID{knowledge.EvidenceID(spanID)}, &signalInput, []SignalEvidenceInput{{EvidenceSpanID: spanID, Role: "supporting"}})
	if err != nil {
		t.Fatalf("complete repeated observation: %v", err)
	}
	if secondObservation.ID() != firstObservation.ID() {
		t.Fatalf("repeated observation ID = %q, want %q", secondObservation.ID(), firstObservation.ID())
	}
	if firstSignal == nil || secondSignal == nil || secondSignal.ID != firstSignal.ID {
		t.Fatalf("repeated signal ID = %q, want %q", secondSignal.ID, firstSignal.ID)
	}
	changedObservation := observationInput
	changedObservation = testCanonicalObservation(t, run, string(observationInput.ID()), entity.ID, "", "", "", "different_interaction", observation.UnknownTime(), spanID)
	if _, _, err := graph.CompleteObservation(ctx, changedObservation, []knowledge.EvidenceID{knowledge.EvidenceID(spanID)}, &signalInput, []SignalEvidenceInput{{EvidenceSpanID: spanID, Role: "supporting"}}); err == nil {
		t.Fatal("changed observation payload with existing ID succeeded")
	}
	changedSignal := signalInput
	changedSignal.Direction = "weakening"
	if _, _, err := graph.CompleteObservation(ctx, observationInput, []knowledge.EvidenceID{knowledge.EvidenceID(spanID)}, &changedSignal, []SignalEvidenceInput{{EvidenceSpanID: spanID, Role: "supporting"}}); err == nil {
		t.Fatal("changed signal payload with existing ID succeeded")
	}
}

func TestCompleteObservationAcceptsExactCanonicalRetry(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	graph := NewGraphRepository(pool)
	_, spanID := createSyntheticMentionAndSpan(t, pool)
	run := testOwningExtractionRun(t, pool, testDocumentVersion(t, testIdentifier("document-canonical-retry")))
	value := testCanonicalObservation(t, run, uuid.NewString(), "", "", "", "", "interacted_with", observation.UnknownTime(), spanID)
	signal := &SignalInput{
		ID: uuid.NewString(), ObservationID: string(value.ID()), Category: "delegation_autonomy", Direction: "strengthening",
		ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion, Rationale: "Synthetic retry rationale.", Confidence: 0.8,
	}
	first, firstSignal, err := graph.CompleteObservation(ctx, value, []knowledge.EvidenceID{knowledge.EvidenceID(spanID)}, signal, []SignalEvidenceInput{{EvidenceSpanID: spanID, Role: "supporting"}})
	if err != nil {
		t.Fatalf("complete canonical observation: %v", err)
	}
	second, secondSignal, err := graph.CompleteObservation(ctx, value, []knowledge.EvidenceID{knowledge.EvidenceID(spanID)}, signal, []SignalEvidenceInput{{EvidenceSpanID: spanID, Role: "supporting"}})
	if err != nil {
		t.Fatalf("retry canonical observation: %v", err)
	}
	if second.ID() != first.ID() || firstSignal == nil || secondSignal == nil || secondSignal.ID != firstSignal.ID {
		t.Fatalf("canonical retry = %#v/%#v, want identical completion", second, secondSignal)
	}
}

func TestCompleteObservationCanonicalizesUUIDBoundFieldsForRetry(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	graph := NewGraphRepository(pool)
	entities := NewEntityRepository(pool)

	subjectEntity, err := entities.CreateEntity(ctx, EntityInput{
		ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Uppercase Subject",
	})
	if err != nil {
		t.Fatalf("create subject entity: %v", err)
	}
	objectEntity, err := entities.CreateEntity(ctx, EntityInput{
		ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Uppercase Object",
	})
	if err != nil {
		t.Fatalf("create object entity: %v", err)
	}
	subjectMentionID, originID := createSyntheticMentionAndSpan(t, pool)
	objectMentionID, signalOnlyID := createSyntheticMentionAndSpan(t, pool)
	run := testOwningExtractionRun(t, pool, testDocumentVersion(t, testIdentifier("document-uppercase-uuid-retry")))
	uppercaseRun := run
	uppercaseRun.ID = strings.ToUpper(run.ID)
	observationID := strings.ToUpper(uuid.NewString())
	value := testCanonicalObservation(
		t,
		uppercaseRun,
		observationID,
		strings.ToUpper(subjectEntity.ID),
		strings.ToUpper(subjectMentionID),
		strings.ToUpper(objectEntity.ID),
		strings.ToUpper(objectMentionID),
		"interacted_with",
		observation.UnknownTime(),
		strings.ToUpper(originID),
		strings.ToUpper(signalOnlyID),
	)
	signalID := strings.ToUpper(uuid.NewString())
	signal := &SignalInput{
		ID: signalID, ObservationID: observationID, Category: "delegation_autonomy", Direction: "strengthening",
		ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion, Rationale: "Synthetic uppercase UUID retry.", Confidence: 0.8,
	}
	origin := []knowledge.EvidenceID{knowledge.EvidenceID(strings.ToUpper(originID))}
	signalEvidence := []SignalEvidenceInput{{EvidenceSpanID: strings.ToUpper(signalOnlyID), Role: "supporting"}}

	first, firstSignal, err := graph.CompleteObservation(ctx, value, origin, signal, signalEvidence)
	if err != nil {
		t.Fatalf("complete uppercase UUID observation: %v", err)
	}
	second, secondSignal, err := graph.CompleteObservation(ctx, value, origin, signal, signalEvidence)
	if err != nil {
		t.Fatalf("retry uppercase UUID observation: %v", err)
	}
	if first.ID() != second.ID() || firstSignal == nil || secondSignal == nil || firstSignal.ID != secondSignal.ID {
		t.Fatalf("uppercase UUID retry = %#v/%#v, want identical completion", second, secondSignal)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM stacks.observations WHERE id = $1`, observationID); got != 1 {
		t.Fatalf("observation rows = %d, want 1", got)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM stacks.observation_evidence WHERE observation_id = $1`, observationID); got != 1 {
		t.Fatalf("observation evidence rows = %d, want 1", got)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM stacks.interaction_signals WHERE id = $1`, signalID); got != 1 {
		t.Fatalf("signal rows = %d, want 1", got)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM stacks.signal_evidence WHERE signal_id = $1`, signalID); got != 1 {
		t.Fatalf("signal evidence rows = %d, want 1", got)
	}
}

func TestCompleteObservationRejectsInadmissibleOwningRunWithoutGraphWrites(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	graph := NewGraphRepository(pool)
	_, spanID := createSyntheticMentionAndSpan(t, pool)
	run := testOwningExtractionRun(t, pool, testDocumentVersion(t, testIdentifier("document-inadmissible-owning-run")))
	if _, err := pool.Exec(ctx, `UPDATE stacks.extraction_runs SET currently_admissible = false WHERE id = $1`, run.ID); err != nil {
		t.Fatalf("mark synthetic run inadmissible: %v", err)
	}
	value := testCanonicalObservation(t, run, uuid.NewString(), "", "", "", "", "interacted_with", observation.UnknownTime(), spanID)
	signal := &SignalInput{
		ID: uuid.NewString(), ObservationID: string(value.ID()), Category: "delegation_autonomy", Direction: "strengthening",
		ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion, Confidence: 0.8,
	}
	_, _, err := graph.CompleteObservation(ctx, value, []knowledge.EvidenceID{knowledge.EvidenceID(spanID)}, signal, []SignalEvidenceInput{{EvidenceSpanID: spanID, Role: "supporting"}})
	if !errors.Is(err, ErrObservationCompatibility) || strings.Contains(err.Error(), "synthetic") {
		t.Fatalf("inadmissible run error = %v", err)
	}
	for _, testCase := range []struct {
		name  string
		query string
		id    string
	}{
		{name: "observation", query: `SELECT count(*) FROM stacks.observations WHERE id = $1`, id: string(value.ID())},
		{name: "observation_evidence", query: `SELECT count(*) FROM stacks.observation_evidence WHERE observation_id = $1`, id: string(value.ID())},
		{name: "signal", query: `SELECT count(*) FROM stacks.interaction_signals WHERE id = $1`, id: signal.ID},
		{name: "signal_evidence", query: `SELECT count(*) FROM stacks.signal_evidence WHERE signal_id = $1`, id: signal.ID},
	} {
		if got := countRows(t, pool, testCase.query, testCase.id); got != 0 {
			t.Fatalf("%s writes = %d, want 0", testCase.name, got)
		}
	}
}

func TestCompleteObservationRejectsEveryDigestExcludedDifference(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	graph := NewGraphRepository(pool)
	run := testOwningExtractionRun(t, pool, testDocumentVersion(t, testIdentifier("document-canonical-differences")))
	_, originID := createSyntheticMentionAndSpan(t, pool)
	_, signalOnlyID := createSyntheticMentionAndSpan(t, pool)
	_, extraSignalID := createSyntheticMentionAndSpan(t, pool)

	for _, testCase := range []struct {
		name     string
		complete func(t *testing.T, id, signalID string) error
		want     error
	}{
		{
			name: "private_origin",
			complete: func(t *testing.T, id, signalID string) error {
				value := testCanonicalObservation(t, run, id, "", "", "", "", testRetryPredicate(id), observation.UnknownTime(), originID, signalOnlyID)
				_, _, err := graph.CompleteObservation(ctx, value, []knowledge.EvidenceID{knowledge.EvidenceID(signalOnlyID)}, &SignalInput{
					ID: signalID, ObservationID: id, Category: "delegation_autonomy", Direction: "strengthening", ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion, Confidence: 0.8,
				}, []SignalEvidenceInput{{EvidenceSpanID: originID, Role: "supporting"}})
				return err
			},
			want: ErrObservationConflict,
		},
		{
			name: "signal_only_evidence",
			complete: func(t *testing.T, id, signalID string) error {
				value := testCanonicalObservation(t, run, id, "", "", "", "", testRetryPredicate(id), observation.UnknownTime(), originID, signalOnlyID, extraSignalID)
				_, _, err := graph.CompleteObservation(ctx, value, []knowledge.EvidenceID{knowledge.EvidenceID(originID)}, &SignalInput{
					ID: signalID, ObservationID: id, Category: "delegation_autonomy", Direction: "strengthening", ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion, Confidence: 0.8,
				}, []SignalEvidenceInput{{EvidenceSpanID: signalOnlyID, Role: "supporting"}, {EvidenceSpanID: extraSignalID, Role: "supporting"}})
				return err
			},
			want: ErrObservationConflict,
		},
		{
			name: "signal_role",
			complete: func(t *testing.T, id, signalID string) error {
				value := testCanonicalObservation(t, run, id, "", "", "", "", testRetryPredicate(id), observation.UnknownTime(), originID, signalOnlyID)
				_, _, err := graph.CompleteObservation(ctx, value, []knowledge.EvidenceID{knowledge.EvidenceID(originID)}, &SignalInput{
					ID: signalID, ObservationID: id, Category: "delegation_autonomy", Direction: "strengthening", ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion, Confidence: 0.8,
				}, []SignalEvidenceInput{{EvidenceSpanID: signalOnlyID, Role: "supporting"}})
				return err
			},
			want: ErrObservationConflict,
		},
		{
			name: "generic_confidence",
			complete: func(t *testing.T, id, signalID string) error {
				value := testCanonicalObservationWithConfidence(t, run, id, "", "", "", "", testRetryPredicate(id), observation.UnknownTime(), 0.7, originID, signalOnlyID)
				_, _, err := graph.CompleteObservation(ctx, value, []knowledge.EvidenceID{knowledge.EvidenceID(originID)}, &SignalInput{
					ID: signalID, ObservationID: id, Category: "delegation_autonomy", Direction: "strengthening", ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion, Confidence: 0.8,
				}, []SignalEvidenceInput{{EvidenceSpanID: signalOnlyID, Role: "supporting"}})
				return err
			},
			want: ErrObservationConflict,
		},
		{
			name: "vertical_signal_confidence",
			complete: func(t *testing.T, id, signalID string) error {
				value := testCanonicalObservation(t, run, id, "", "", "", "", testRetryPredicate(id), observation.UnknownTime(), originID, signalOnlyID)
				_, _, err := graph.CompleteObservation(ctx, value, []knowledge.EvidenceID{knowledge.EvidenceID(originID)}, &SignalInput{
					ID: signalID, ObservationID: id, Category: "delegation_autonomy", Direction: "strengthening", ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion, Confidence: 0.6,
				}, []SignalEvidenceInput{{EvidenceSpanID: signalOnlyID, Role: "supporting"}})
				return err
			},
			want: ErrObservationConflict,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			id := uuid.NewString()
			value := testCanonicalObservation(t, run, id, "", "", "", "", testRetryPredicate(id), observation.UnknownTime(), originID, signalOnlyID)
			roles := []SignalEvidenceInput{{EvidenceSpanID: signalOnlyID, Role: "supporting"}}
			if testCase.name == "signal_role" {
				value = testCanonicalObservationWithLinks(t, run, id, "", "", "", "", testRetryPredicate(id), observation.UnknownTime(), []observation.EvidenceLink{
					{EvidenceID: knowledge.EvidenceID(originID), Role: observation.EvidenceSupporting},
					{EvidenceID: knowledge.EvidenceID(signalOnlyID), Role: observation.EvidenceSupporting},
					{EvidenceID: knowledge.EvidenceID(signalOnlyID), Role: observation.EvidenceContradicting},
				}, nil)
				roles = append(roles, SignalEvidenceInput{EvidenceSpanID: signalOnlyID, Role: "contradicting"})
			}
			signal := &SignalInput{ID: uuid.NewString(), ObservationID: id, Category: "delegation_autonomy", Direction: "strengthening", ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion, Confidence: 0.8}
			if _, _, err := graph.CompleteObservation(ctx, value, []knowledge.EvidenceID{knowledge.EvidenceID(originID)}, signal, roles); err != nil {
				t.Fatalf("seed canonical completion: %v", err)
			}
			err := testCase.complete(t, id, signal.ID)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("difference error = %v, want %v", err, testCase.want)
			}
		})
	}

	for _, testCase := range []struct {
		name   string
		mutate func(t *testing.T, id string)
		want   error
	}{
		{name: "recorded_at", mutate: func(t *testing.T, id string) {
			if _, err := pool.Exec(ctx, `UPDATE stacks.observations SET recorded_at = recorded_at + interval '1 microsecond' WHERE id = $1`, id); err != nil {
				t.Fatalf("mutate stored recorded_at: %v", err)
			}
		}, want: ErrObservationConflict},
		{name: "stored_digest", mutate: func(t *testing.T, id string) {
			digest := sha256.Sum256([]byte("stored-digest-" + id))
			if _, err := pool.Exec(ctx, `UPDATE stacks.observations SET digest = $2 WHERE id = $1`, id, digest[:]); err != nil {
				t.Fatalf("mutate stored digest: %v", err)
			}
		}, want: ErrObservationCompatibility},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			id := uuid.NewString()
			value := testCanonicalObservation(t, run, id, "", "", "", "", testRetryPredicate(id), observation.UnknownTime(), originID, signalOnlyID)
			signal := &SignalInput{ID: uuid.NewString(), ObservationID: id, Category: "delegation_autonomy", Direction: "strengthening", ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion, Confidence: 0.8}
			roles := []SignalEvidenceInput{{EvidenceSpanID: signalOnlyID, Role: "supporting"}}
			if _, _, err := graph.CompleteObservation(ctx, value, []knowledge.EvidenceID{knowledge.EvidenceID(originID)}, signal, roles); err != nil {
				t.Fatalf("seed canonical completion: %v", err)
			}
			testCase.mutate(t, id)
			if _, _, err := graph.CompleteObservation(ctx, value, []knowledge.EvidenceID{knowledge.EvidenceID(originID)}, signal, roles); !errors.Is(err, testCase.want) {
				t.Fatalf("stored difference error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestCompleteObservationRejectsDifferentIDWithSameDigest(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	graph := NewGraphRepository(pool)
	_, spanID := createSyntheticMentionAndSpan(t, pool)
	run := testOwningExtractionRun(t, pool, testDocumentVersion(t, testIdentifier("document-canonical-digest-conflict")))
	first := testCanonicalObservation(t, run, uuid.NewString(), "", "", "", "", "interacted_with", observation.UnknownTime(), spanID)
	if _, _, err := graph.CompleteObservation(ctx, first, []knowledge.EvidenceID{knowledge.EvidenceID(spanID)}, nil, nil); err != nil {
		t.Fatalf("complete first observation: %v", err)
	}
	second := testCanonicalObservation(t, run, uuid.NewString(), "", "", "", "", "interacted_with", observation.UnknownTime(), spanID)
	_, _, err := graph.CompleteObservation(ctx, second, []knowledge.EvidenceID{knowledge.EvidenceID(spanID)}, nil, nil)
	var databaseError *pgconn.PgError
	if !errors.Is(err, ErrObservationConflict) || !errors.As(err, &databaseError) || strings.Contains(err.Error(), databaseError.Message) {
		t.Fatalf("same-digest error = %v, want ErrObservationConflict", err)
	}
}

func TestCompleteObservationRejectsUnrepresentableCanonicalValueBeforeSQL(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	graph := NewGraphRepository(pool)
	_, spanID := createSyntheticMentionAndSpan(t, pool)
	run := testOwningExtractionRun(t, pool, testDocumentVersion(t, testIdentifier("document-unrepresentable-canonical")))
	run.RecordedAt = run.RecordedAt.Add(time.Nanosecond)
	value := testCanonicalObservation(t, run, uuid.NewString(), "", "", "", "", "interacted_with", observation.UnknownTime(), spanID)
	if _, _, err := graph.CompleteObservation(ctx, value, []knowledge.EvidenceID{knowledge.EvidenceID(spanID)}, nil, nil); !errors.Is(err, ErrObservationNotRepresentable) {
		t.Fatalf("unrepresentable completion error = %v, want ErrObservationNotRepresentable", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM stacks.observations WHERE id = $1`, string(value.ID())); got != 0 {
		t.Fatalf("unrepresentable observation rows = %d, want 0", got)
	}
}

func TestCompleteObservationPreservesOriginRelativeSignalEvidence(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	graph := NewGraphRepository(pool)
	_, observationOnlyID := createSyntheticMentionAndSpan(t, pool)
	_, signalOnlyID := createSyntheticMentionAndSpan(t, pool)
	_, bothID := createSyntheticMentionAndSpan(t, pool)
	_, contradictingOriginID := createSyntheticMentionAndSpan(t, pool)
	run := testOwningExtractionRun(t, pool, testDocumentVersion(t, testIdentifier("document-origin-relative-signal")))
	value := testCanonicalObservationWithLinks(t, run, uuid.NewString(), "", "", "", "", "interacted_with", observation.UnknownTime(), []observation.EvidenceLink{
		{EvidenceID: knowledge.EvidenceID(observationOnlyID), Role: observation.EvidenceSupporting},
		{EvidenceID: knowledge.EvidenceID(signalOnlyID), Role: observation.EvidenceSupporting},
		{EvidenceID: knowledge.EvidenceID(bothID), Role: observation.EvidenceSupporting},
		{EvidenceID: knowledge.EvidenceID(contradictingOriginID), Role: observation.EvidenceSupporting},
		{EvidenceID: knowledge.EvidenceID(contradictingOriginID), Role: observation.EvidenceContradicting},
	}, nil)
	signal := &SignalInput{ID: uuid.NewString(), ObservationID: string(value.ID()), Category: "delegation_autonomy", Direction: "mixed", ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion, Confidence: 0.8}
	if _, _, err := graph.CompleteObservation(ctx, value,
		[]knowledge.EvidenceID{knowledge.EvidenceID(observationOnlyID), knowledge.EvidenceID(bothID), knowledge.EvidenceID(contradictingOriginID)}, signal,
		[]SignalEvidenceInput{{EvidenceSpanID: signalOnlyID, Role: "supporting"}, {EvidenceSpanID: bothID, Role: "supporting"}, {EvidenceSpanID: contradictingOriginID, Role: "contradicting"}},
	); err != nil {
		t.Fatalf("complete origin-relative signal evidence: %v", err)
	}
	loaded, err := loadLegacyObservation(ctx, pool, string(value.ID()))
	if err != nil {
		t.Fatalf("load completed observation: %v", err)
	}
	wantPairs := []observation.EvidenceLink{
		{EvidenceID: knowledge.EvidenceID(observationOnlyID), Role: observation.EvidenceSupporting},
		{EvidenceID: knowledge.EvidenceID(signalOnlyID), Role: observation.EvidenceSupporting},
		{EvidenceID: knowledge.EvidenceID(bothID), Role: observation.EvidenceSupporting},
		{EvidenceID: knowledge.EvidenceID(contradictingOriginID), Role: observation.EvidenceSupporting},
		{EvidenceID: knowledge.EvidenceID(contradictingOriginID), Role: observation.EvidenceContradicting},
	}
	if !sameEvidenceLinks(loaded.Observation.EvidenceLinks(), wantPairs) {
		t.Fatalf("canonical evidence = %#v, want %#v", loaded.Observation.EvidenceLinks(), wantPairs)
	}
	wantOrigin := []knowledge.EvidenceID{knowledge.EvidenceID(observationOnlyID), knowledge.EvidenceID(bothID), knowledge.EvidenceID(contradictingOriginID)}
	sort.Slice(wantOrigin, func(left, right int) bool { return wantOrigin[left] < wantOrigin[right] })
	if !sameEvidenceIDs(loaded.Compatibility.observationEvidenceOrigin, wantOrigin) {
		t.Fatalf("private origin = %#v, want %#v", loaded.Compatibility.observationEvidenceOrigin, wantOrigin)
	}
	wantRoles := []SignalEvidenceInput{{EvidenceSpanID: bothID, Role: "supporting"}, {EvidenceSpanID: contradictingOriginID, Role: "contradicting"}, {EvidenceSpanID: signalOnlyID, Role: "supporting"}}
	if loaded.Signal == nil || !sameSignalEvidence(loaded.Signal.Evidence, wantRoles) {
		t.Fatalf("signal evidence = %#v, want %#v", loaded.Signal, wantRoles)
	}
}

func sameSignalEvidence(left, right []SignalEvidenceInput) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[SignalEvidenceInput]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		if counts[value] == 0 {
			return false
		}
		counts[value]--
	}
	return true
}

func testRetryPredicate(id string) string {
	return "interacted_with_" + id
}

func TestPutEvidenceSpanRejectsImmutableQuoteConflict(t *testing.T) {
	pool := openIntegrationDatabase(t)
	version := testDocumentVersion(t, testIdentifier("document-evidence-conflict"))
	documents := NewDocumentRepository(pool)
	if _, _, err := documents.PutDocumentVersion(context.Background(), version); err != nil {
		t.Fatalf("put evidence document version: %v", err)
	}
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, SectionID: "tab-synthetic", StartOffset: 0, EndOffset: len("Synthetic"), Quote: "Synthetic",
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

func TestCompleteObservationRejectsNotesOnlySignalEvidence(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	version := testDocumentVersionWithRole(t, testIdentifier("document-notes-signal"), source.TabRoleGeminiNotes)
	documents := NewDocumentRepository(pool)
	if _, _, err := documents.PutDocumentVersion(ctx, version); err != nil {
		t.Fatalf("put notes document version: %v", err)
	}
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, SectionID: "tab-synthetic", StartOffset: 0, EndOffset: len("Synthetic"), Quote: "Synthetic",
	})
	if err != nil {
		t.Fatalf("new notes evidence span: %v", err)
	}
	storedSpan, err := documents.PutEvidenceSpan(ctx, span)
	if err != nil {
		t.Fatalf("put notes evidence span: %v", err)
	}
	graph := NewGraphRepository(pool)
	run := testOwningExtractionRun(t, pool, version)
	canonicalObservation := testCanonicalObservation(t, run, uuid.NewString(), "", "", "", "", "interacted_with", observation.UnknownTime(), storedSpan.ID)
	signal := &SignalInput{
		ID: uuid.NewString(), ObservationID: string(canonicalObservation.ID()), Category: "delegation_autonomy", Direction: "strengthening",
		ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion, Confidence: 0.8,
	}
	_, _, err = graph.CompleteObservation(ctx, canonicalObservation, []knowledge.EvidenceID{knowledge.EvidenceID(storedSpan.ID)}, signal, []SignalEvidenceInput{{EvidenceSpanID: storedSpan.ID, Role: "supporting"}})
	if err == nil {
		t.Fatal("notes-only signal completion succeeded")
	}
	for _, testCase := range []struct {
		name  string
		query string
		id    string
	}{
		{name: "observation", query: `SELECT count(*) FROM stacks.observations WHERE id = $1`, id: string(canonicalObservation.ID())},
		{name: "observation_evidence", query: `SELECT count(*) FROM stacks.observation_evidence WHERE observation_id = $1`, id: string(canonicalObservation.ID())},
		{name: "signal", query: `SELECT count(*) FROM stacks.interaction_signals WHERE id = $1`, id: signal.ID},
		{name: "signal_evidence", query: `SELECT count(*) FROM stacks.signal_evidence WHERE signal_id = $1`, id: signal.ID},
	} {
		if got := countRows(t, pool, testCase.query, testCase.id); got != 0 {
			t.Fatalf("failed notes-only completion left %s rows = %d, want 0", testCase.name, got)
		}
	}
}

func openIntegrationDatabase(t *testing.T) *pgxpool.Pool {
	return openIntegrationDatabaseFromEnvironment(t, testDatabaseURLEnvironmentVariable)
}

func openMigrationIntegrationDatabase(t *testing.T) *pgxpool.Pool {
	return openIntegrationDatabaseFromEnvironment(t, testMigrationDatabaseURLEnvironmentVariable)
}

func openIntegrationDatabaseFromEnvironment(t *testing.T, environmentVariable string) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv(environmentVariable)
	if databaseURL == "" {
		t.Skipf("%s is not set", environmentVariable)
	}

	pool, err := Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func openNamedIntegrationDatabase(
	t *testing.T,
	base *pgxpool.Pool,
	applicationName string,
) *pgxpool.Pool {
	t.Helper()
	config := base.Config().Copy()
	config.MaxConns = 1
	config.MinConns = 0
	config.ConnConfig.RuntimeParams["application_name"] = applicationName
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open named integration database: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping named integration database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func waitForNamedBackendLockOrCompletion(
	t *testing.T,
	observer *pgxpool.Pool,
	applicationName string,
	completed <-chan struct{},
) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case <-completed:
			return false
		default:
		}
		var waiting bool
		err := observer.QueryRow(ctx, `
			SELECT COALESCE(bool_or(wait_event_type = 'Lock'), false)
			FROM pg_stat_activity
			WHERE application_name = $1`,
			applicationName,
		).Scan(&waiting)
		if err != nil {
			t.Fatalf("inspect named backend lock state: %v", err)
		}
		if waiting {
			return true
		}
	}
}

func createSyntheticMention(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	mentionID, _ := createSyntheticMentionAndSpan(t, pool)
	return mentionID
}

func createDirectoryPendingMentionFixture(t *testing.T, pool *pgxpool.Pool, label string) (string, directory.PendingMention) {
	t.Helper()
	email := "synthetic.person." + strings.ReplaceAll(uuid.NewString(), "-", "") + "@synthetic.example"
	return createDirectoryPendingMentionFixtureWithEmail(t, pool, label, email)
}

func createDirectoryPendingMentionFixtureWithEmail(
	t *testing.T,
	pool *pgxpool.Pool,
	label string,
	email string,
) (string, directory.PendingMention) {
	t.Helper()
	return createDirectoryPendingMentionFixtureWithSurfaceEmail(
		t,
		pool,
		label,
		"Synthetic Person",
		email,
	)
}

func createDirectoryPendingMentionFixtureWithSurfaceEmail(
	t *testing.T,
	pool *pgxpool.Pool,
	label string,
	surface string,
	email string,
) (string, directory.PendingMention) {
	t.Helper()
	quote := surface + " <" + email + ">"
	version, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
		Provider:           "synthetic-drive",
		ProviderDocumentID: testIdentifier("directory-" + label),
		Title:              "Synthetic directory identity",
		Locator:            "https://docs.example.invalid/directory-identity",
		ProviderVersion:    "synthetic-version-1",
		ModifiedAt:         time.Date(2026, time.July, 24, 10, 0, 0, 0, time.UTC),
		RecordedAt:         time.Date(2026, time.July, 24, 11, 0, 0, 0, time.UTC),
		Sections: testEvidenceSections(t, []source.Tab{{
			ID: "tab-synthetic", Title: "Synthetic Transcript", Order: 0,
			Role: source.TabRoleTranscript, Text: quote,
		}}),
	})
	if err != nil {
		t.Fatalf("new synthetic directory document: %v", err)
	}
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, SectionID: "tab-synthetic", StartOffset: 0,
		EndOffset: len(quote), Quote: quote,
	})
	if err != nil {
		t.Fatalf("new synthetic directory evidence: %v", err)
	}
	repository := NewIngestionRepository(pool)
	state, err := repository.PrepareVersion(
		context.Background(),
		version,
		testExtractionDerivation(t, version),
		modelpolicy.DataModePersonal,
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("prepare synthetic directory derivation: %v", err)
	}
	if err := repository.CompleteVersion(context.Background(), ingest.Completion{
		VersionID: state.ID, DerivationID: state.DerivationID, LeaseOwner: state.LeaseOwner,
		DataMode: modelpolicy.DataModePersonal,
		Evidence: []ingest.EvidenceRecord{{Key: "identity", Span: span}},
		Mentions: []ingest.MentionRecord{{
			Key: "person", EvidenceKey: "identity", Surface: surface,
			NormalizedName: entity.NormalizeName(surface), ProposedEmail: email,
			ProposedEmailEvidenceKey: "identity", Role: "speaker",
		}},
	}); err != nil {
		t.Fatalf("complete synthetic directory derivation: %v", err)
	}
	var mention directory.PendingMention
	if err := pool.QueryRow(context.Background(), `
		SELECT mention.id::text, proposal.id::text
		FROM stacks.mentions AS mention
		JOIN stacks.resolution_proposals AS proposal ON proposal.mention_id = mention.id
		WHERE mention.extraction_run_id = $1`, state.DerivationID).Scan(
		&mention.MentionID, &mention.ProposalID,
	); err != nil {
		t.Fatalf("load synthetic directory mention IDs: %v", err)
	}
	mention.Surface = surface
	mention.NormalizedName = entity.NormalizeName(surface)
	mention.ProposedEmail = email
	mention.NameQuote = quote
	mention.EmailQuote = quote
	return state.DerivationID, mention
}

func persistDirectoryNameCandidateForReview(
	t *testing.T,
	pool *pgxpool.Pool,
	mention directory.PendingMention,
	profile entity.DirectoryProfile,
) string {
	t.Helper()
	input := directory.PersistInput{
		Mention: mention,
		Query: entity.DirectoryQuery{
			Kind:          entity.DirectoryQueryName,
			Name:          mention.NormalizedName,
			EmailEvidence: entity.EmailEvidenceNone,
		},
		Lookup: directory.LookupResult{
			Outcome:  entity.DirectoryMatched,
			Profiles: []entity.DirectoryProfile{profile},
		},
		Evaluation: entity.DirectoryEvaluation{
			Outcome:    entity.DirectoryReview,
			Candidates: []entity.DirectoryProfile{profile},
		},
		AttemptCount: 1,
		RecordedAt:   time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
	}
	if _, err := NewDirectoryRepository(pool).Persist(context.Background(), input); err != nil {
		t.Fatalf("persist directory review candidate: %v", err)
	}
	var snapshotID string
	if err := pool.QueryRow(context.Background(), `
		SELECT directory_profile_snapshot_id::text
		FROM stacks.resolution_candidates
		WHERE proposal_id = $1
		  AND directory_profile_snapshot_id IS NOT NULL`,
		mention.ProposalID,
	).Scan(&snapshotID); err != nil {
		t.Fatalf("load directory review candidate snapshot ID: %v", err)
	}
	return snapshotID
}

func advanceDirectoryMentionSourceVersion(t *testing.T, pool *pgxpool.Pool, mentionID string) {
	t.Helper()
	var providerDocumentID string
	if err := pool.QueryRow(context.Background(), `
		SELECT source_document.provider_document_id
		FROM stacks.mentions AS mention
		JOIN stacks.extraction_runs AS extraction_run
		  ON extraction_run.id = mention.extraction_run_id
		JOIN stacks.document_versions AS document_version
		  ON document_version.id = extraction_run.document_version_id
		JOIN stacks.source_documents AS source_document
		  ON source_document.id = document_version.source_document_id
		WHERE mention.id = $1`,
		mentionID,
	).Scan(&providerDocumentID); err != nil {
		t.Fatalf("load synthetic stale-source document identity: %v", err)
	}
	laterVersion, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
		Provider:           "synthetic-drive",
		ProviderDocumentID: providerDocumentID,
		Title:              "Synthetic later directory version",
		Locator:            "https://docs.example.invalid/directory-identity",
		ProviderVersion:    "synthetic-version-2",
		ModifiedAt:         time.Date(2026, time.July, 24, 15, 0, 0, 0, time.UTC),
		RecordedAt:         time.Date(2026, time.July, 24, 16, 0, 0, 0, time.UTC),
		Sections: testEvidenceSections(t, []source.Tab{{
			ID: "tab-synthetic", Title: "Synthetic Transcript", Order: 0,
			Role: source.TabRoleTranscript, Text: "Synthetic later directory content.",
		}}),
	})
	if err != nil {
		t.Fatalf("new synthetic later directory version: %v", err)
	}
	repository := NewIngestionRepository(pool)
	state, err := repository.PrepareVersion(
		context.Background(),
		laterVersion,
		testExtractionDerivation(t, laterVersion),
		modelpolicy.DataModePersonal,
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("prepare synthetic later directory version: %v", err)
	}
	if err := repository.CompleteVersion(context.Background(), ingest.Completion{
		VersionID: state.ID, DerivationID: state.DerivationID, LeaseOwner: state.LeaseOwner,
		DataMode: modelpolicy.DataModePersonal,
	}); err != nil {
		t.Fatalf("complete synthetic later directory version: %v", err)
	}
}

func matchedDirectoryPersistInput(
	mention directory.PendingMention,
	profile entity.DirectoryProfile,
	recordedAt time.Time,
) directory.PersistInput {
	return directory.PersistInput{
		Mention: mention,
		Query: entity.DirectoryQuery{
			Kind:          entity.DirectoryQueryEmail,
			Email:         mention.ProposedEmail,
			EmailEvidence: entity.EmailEvidenceSourceBound,
		},
		Lookup: directory.LookupResult{
			Outcome:  entity.DirectoryMatched,
			Profiles: []entity.DirectoryProfile{profile},
		},
		Evaluation: entity.DirectoryEvaluation{
			Outcome:       entity.DirectoryMatched,
			CreatePerson:  true,
			AcceptedEmail: mention.ProposedEmail,
			Profile:       &profile,
		},
		AttemptCount: 1,
		RecordedAt:   recordedAt,
	}
}

func matchedReviewerVerification(
	email string,
	profile entity.DirectoryProfile,
) directory.ReviewerVerification {
	return directory.ReviewerVerification{
		Query: entity.DirectoryQuery{
			Kind:          entity.DirectoryQueryEmail,
			Email:         email,
			EmailEvidence: entity.EmailEvidenceReviewerSupplied,
		},
		Lookup: directory.LookupResult{
			Outcome:  entity.DirectoryMatched,
			Profiles: []entity.DirectoryProfile{profile},
		},
		Evaluation: entity.DirectoryEvaluation{
			Outcome:       entity.DirectoryMatched,
			CreatePerson:  true,
			AcceptedEmail: email,
			Profile:       &profile,
		},
		AttemptCount: 1,
		RecordedAt:   time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
	}
}

func assertDirectoryAuthorityCounts(
	t *testing.T,
	pool *pgxpool.Pool,
	mention directory.PendingMention,
	profile entity.DirectoryProfile,
	entityID string,
	wantSnapshots int,
	wantAttempts int,
) {
	t.Helper()
	var snapshots, emails, attempts, matches, decisions, nameAliases, emailAliases, links int
	if err := pool.QueryRow(context.Background(), `
		SELECT
		    (SELECT count(*) FROM stacks.directory_profile_snapshots
		     WHERE provider = $1 AND provider_subject_id = $2),
		    (SELECT count(*)
		     FROM stacks.directory_profile_emails AS email
		     JOIN stacks.directory_profile_snapshots AS snapshot ON snapshot.id = email.snapshot_id
		     WHERE snapshot.provider = $1 AND snapshot.provider_subject_id = $2),
		    (SELECT count(*) FROM stacks.directory_lookup_attempts WHERE mention_id = $3),
		    (SELECT count(*)
		     FROM stacks.directory_lookup_matches AS match
		     JOIN stacks.directory_lookup_attempts AS attempt ON attempt.id = match.lookup_attempt_id
		     WHERE attempt.mention_id = $3),
		    (SELECT count(*) FROM stacks.resolution_decisions WHERE proposal_id = $4),
		    (SELECT count(*)
		     FROM stacks.entity_alias_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision ON decision.id = assertion.decision_id
		     WHERE decision.proposal_id = $4 AND assertion.alias_type = 'name'),
		    (SELECT count(*)
		     FROM stacks.entity_alias_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision ON decision.id = assertion.decision_id
		     WHERE decision.proposal_id = $4 AND assertion.alias_type = 'email'),
		    (SELECT count(*)
		     FROM stacks.entity_directory_identity_assertions AS assertion
		     JOIN stacks.resolution_decisions AS decision ON decision.id = assertion.decision_id
		     WHERE decision.proposal_id = $4 AND assertion.entity_id = $5)`,
		profile.Provider, profile.SubjectID, mention.MentionID, mention.ProposalID, entityID,
	).Scan(
		&snapshots, &emails, &attempts, &matches, &decisions,
		&nameAliases, &emailAliases, &links,
	); err != nil {
		t.Fatalf("count synthetic directory authority: %v", err)
	}
	if snapshots != wantSnapshots || emails != wantSnapshots ||
		attempts != wantAttempts || matches != wantAttempts ||
		decisions != 1 || nameAliases != 0 || emailAliases != 1 || links != 1 {
		t.Fatalf(
			"directory snapshot/email/attempt/match/decision/name/email/link counts = %d/%d/%d/%d/%d/%d/%d/%d, want %d/%d/%d/%d/1/0/1/1",
			snapshots, emails, attempts, matches, decisions, nameAliases, emailAliases, links,
			wantSnapshots, wantSnapshots, wantAttempts, wantAttempts,
		)
	}
}

func seedDirectoryLookupAttempt(
	t *testing.T,
	pool *pgxpool.Pool,
	mentionID string,
	outcome entity.DirectoryOutcome,
	recordedAt time.Time,
	retryAfter *time.Time,
) string {
	t.Helper()
	attemptID := uuid.NewString()
	queryDigest := sha256.Sum256([]byte("synthetic-directory-query-" + attemptID))
	attemptDigest := sha256.Sum256([]byte("synthetic-directory-attempt-" + attemptID))
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO stacks.directory_lookup_attempts
			(id, mention_id, provider, query_kind, email_evidence, query_digest,
			 policy_version, outcome, attempt_count, retry_after, recorded_at, digest)
		VALUES ($1, $2, 'google_people', 'email', 'source_bound', $3,
		        $4, $5, 1, $6, $7, $8)`,
		attemptID, mentionID, queryDigest[:], entity.DirectoryPolicyVersion,
		string(outcome), retryAfter, recordedAt, attemptDigest[:]); err != nil {
		t.Fatalf("seed synthetic directory lookup attempt: %v", err)
	}
	return attemptID
}

func syntheticDirectoryProfile(label, email string, observedAt time.Time) entity.DirectoryProfile {
	return entity.DirectoryProfile{
		Provider:    "google_people",
		SubjectID:   "synthetic-subject-" + label,
		Source:      entity.DirectorySourceDomainProfile,
		DisplayName: "Synthetic Directory Person",
		Emails: []entity.DirectoryEmail{{
			Value: email, Primary: true,
		}},
		ObservedAt: observedAt,
	}
}

func seedDirectoryProfileEvidence(
	t *testing.T,
	pool *pgxpool.Pool,
	mentionID string,
	profile entity.DirectoryProfile,
) (string, string) {
	t.Helper()
	canonical, err := canonicalDirectoryProfile(profile)
	if err != nil {
		t.Fatalf("canonicalize synthetic directory profile: %v", err)
	}
	digest, err := directoryProfileDigest(canonical)
	if err != nil {
		t.Fatalf("digest synthetic directory profile: %v", err)
	}
	snapshotID := uuid.NewSHA1(uuid.NameSpaceOID, digest[:]).String()
	var observedAt *time.Time
	if !canonical.ObservedAt.IsZero() {
		value := canonical.ObservedAt
		observedAt = &value
	}
	recordedAt := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO stacks.directory_profile_snapshots
			(id, provider, provider_subject_id, source_type, display_name, observed_at, recorded_at, digest)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		snapshotID, canonical.Provider, canonical.SubjectID, string(canonical.Source),
		canonical.DisplayName, observedAt, recordedAt, digest[:]); err != nil {
		t.Fatalf("seed synthetic directory profile: %v", err)
	}
	for position, email := range canonical.Emails {
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO stacks.directory_profile_emails
				(snapshot_id, normalized_email, is_primary, position)
			VALUES ($1, $2, $3, $4)`,
			snapshotID, email.Value, email.Primary, position); err != nil {
			t.Fatalf("seed synthetic directory profile email: %v", err)
		}
	}
	attemptID := seedDirectoryLookupAttempt(
		t, pool, mentionID, entity.DirectoryMatched, recordedAt, nil,
	)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO stacks.directory_lookup_matches
			(lookup_attempt_id, snapshot_id, rank, reason)
		VALUES ($1, $2, 0, 'exact_email')`,
		attemptID, snapshotID); err != nil {
		t.Fatalf("seed synthetic directory lookup match: %v", err)
	}
	return snapshotID, attemptID
}

func sameEntitySnapshots(left, right []entity.EntitySnapshot) bool {
	return reflect.DeepEqual(left, right)
}

func containsDirectoryIdentityLink(links []entity.DirectoryIdentityLink, target entity.DirectoryIdentityLink) bool {
	for _, link := range links {
		if link == target {
			return true
		}
	}
	return false
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, arguments ...any) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), query, arguments...).Scan(&count); err != nil {
		t.Fatalf("count synthetic rows: %v", err)
	}
	return count
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
		SectionID:   "tab-synthetic",
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
	return testExtractionDerivationForProvider(t, version, modelpolicy.ProviderBedrock)
}

func testOwningExtractionRun(t *testing.T, pool *pgxpool.Pool, version knowledge.DocumentVersion) owningExtractionRun {
	t.Helper()
	state, err := NewIngestionRepository(pool).PrepareVersion(
		context.Background(), version, testExtractionDerivation(t, version), modelpolicy.DataModePersonal, time.Minute,
	)
	if err != nil {
		t.Fatalf("prepare synthetic extraction run: %v", err)
	}
	var run owningExtractionRun
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text, model_id, prompt_version, recorded_at
		FROM stacks.extraction_runs
		WHERE id = $1`, state.DerivationID).Scan(&run.ID, &run.ModelID, &run.PromptVersion, &run.RecordedAt); err != nil {
		t.Fatalf("load synthetic extraction run: %v", err)
	}
	return run
}

func testCanonicalObservation(
	t *testing.T,
	run owningExtractionRun,
	id, subjectEntityID, subjectMentionID, objectEntityID, objectMentionID string,
	predicate string,
	validTime observation.TemporalExtent,
	evidenceIDs ...string,
) observation.Observation {
	t.Helper()
	links := make([]observation.EvidenceLink, len(evidenceIDs))
	for index, evidenceID := range evidenceIDs {
		links[index] = observation.EvidenceLink{EvidenceID: knowledge.EvidenceID(evidenceID), Role: observation.EvidenceSupporting}
	}
	return testCanonicalObservationWithLinks(t, run, id, subjectEntityID, subjectMentionID, objectEntityID, objectMentionID, predicate, validTime, links, nil)
}

func testCanonicalObservationWithConfidence(
	t *testing.T,
	run owningExtractionRun,
	id, subjectEntityID, subjectMentionID, objectEntityID, objectMentionID string,
	predicate string,
	validTime observation.TemporalExtent,
	confidence float64,
	evidenceIDs ...string,
) observation.Observation {
	t.Helper()
	legacyConfidence, err := observation.NewLegacyConfidence(confidence)
	if err != nil {
		t.Fatalf("new legacy confidence: %v", err)
	}
	links := make([]observation.EvidenceLink, len(evidenceIDs))
	for index, evidenceID := range evidenceIDs {
		links[index] = observation.EvidenceLink{EvidenceID: knowledge.EvidenceID(evidenceID), Role: observation.EvidenceSupporting}
	}
	return testCanonicalObservationWithLinks(t, run, id, subjectEntityID, subjectMentionID, objectEntityID, objectMentionID, predicate, validTime, links, &legacyConfidence)
}

func testCanonicalObservationWithLinks(
	t *testing.T,
	run owningExtractionRun,
	id, subjectEntityID, subjectMentionID, objectEntityID, objectMentionID string,
	predicate string,
	validTime observation.TemporalExtent,
	links []observation.EvidenceLink,
	confidence *observation.Confidence,
) observation.Observation {
	t.Helper()
	subject := testCanonicalTerm(t, subjectEntityID, subjectMentionID)
	object := testCanonicalTerm(t, objectEntityID, objectMentionID)
	value, err := observation.NewObservation(observation.ObservationInput{
		ID:        observation.ObservationID(id),
		Statement: observation.Statement{Subject: subject, Predicate: observation.Predicate(predicate), Object: object},
		ValidTime: validTime, RecordedAt: run.RecordedAt, Evidence: links,
		Derivation: observation.Derivation{
			Method: "synthetic", Version: run.PromptVersion, RunID: run.ID, Model: run.ModelID, PromptVersion: run.PromptVersion,
		},
		Status: observation.StatusInferred, Confidence: confidence,
	})
	if err != nil {
		t.Fatalf("new canonical observation: %v", err)
	}
	return value
}

func testCanonicalTerm(t *testing.T, entityID, mentionID string) observation.Term {
	t.Helper()
	var (
		term observation.Term
		err  error
	)
	switch {
	case entityID != "":
		term, err = observation.NewEntityTerm(entityID, mentionID)
	case mentionID != "":
		term, err = observation.NewMentionTerm(mentionID)
	default:
		return observation.AbsentTerm()
	}
	if err != nil {
		t.Fatalf("new canonical term: %v", err)
	}
	return term
}

func testCanonicalInstant(t *testing.T, value time.Time) observation.TemporalExtent {
	t.Helper()
	instant, err := observation.AtTime(value)
	if err != nil {
		t.Fatalf("new canonical instant: %v", err)
	}
	return instant
}

func testExtractionDerivationForProvider(t *testing.T, version knowledge.DocumentVersion, provider modelpolicy.Provider) ingest.DerivationIdentity {
	t.Helper()
	identity := ingest.DerivationIdentity{
		Provider: provider,
		ModelID:  "synthetic-model", MaxTokens: 256,
		PromptVersion: extract.ExtractionPromptVersion,
		SchemaDigest:  sha256.Sum256(extract.ExtractionJSONSchema()),
	}
	if provider == modelpolicy.ProviderBedrock {
		identity.Region = "us-east-1"
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
		Sections: testEvidenceSections(t, []source.Tab{{
			ID:    "tab-synthetic",
			Title: "Synthetic Transcript",
			Order: 0,
			Role:  role,
			Text:  "Synthetic meeting text.",
		}}),
	})
	if err != nil {
		t.Fatalf("new synthetic document version: %v", err)
	}
	return version
}

func testEvidenceSections(t *testing.T, tabs []source.Tab) []knowledge.Section {
	t.Helper()
	sections := make([]knowledge.Section, len(tabs))
	for index, tab := range tabs {
		section, err := knowledge.NewSection(knowledge.SectionInput{
			ID: tab.ID, Title: tab.Title, ParentID: tab.ParentID, Path: tab.Path,
			Order: tab.Order, Role: string(tab.Role), Text: tab.Text,
		})
		if err != nil {
			t.Fatalf("new test evidence section: %v", err)
		}
		sections[index] = section
	}
	return sections
}
