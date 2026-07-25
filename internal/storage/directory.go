package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"stacks/internal/directory"
	"stacks/internal/entity"
)

const (
	googlePeopleDirectoryProvider = "google_people"

	directoryProfileDigestVersion = "stacks.directory-profile.v1"
	directoryQueryDigestVersion   = "stacks.directory-query.v1"
	directoryLookupDigestVersion  = "stacks.directory-lookup.v1"

	directoryPersonIdentityVersion = "stacks.directory-person.v1"

	directoryExactEmailReviewReason = "directory exact email requires review"
	directoryNameReviewReason       = "directory name candidate requires review"
)

// DirectoryRepository owns private directory evidence persistence and the
// transaction that may create identity authority.
type DirectoryRepository struct {
	pool      *pgxpool.Pool
	testHooks directoryPersistenceTestHooks
}

type directoryPersistenceTestHooks struct {
	beforeAuthorityLocks func()
	afterAuthorityLocks  func() error
	afterEntityCreated   func() error
}

type storedDirectoryProfile struct {
	ID      string
	Profile entity.DirectoryProfile
	Digest  [sha256.Size]byte
}

type storedDirectoryAttempt struct {
	ID string
}

// NewDirectoryRepository creates a directory repository backed by pool.
func NewDirectoryRepository(pool *pgxpool.Pool) *DirectoryRepository {
	return &DirectoryRepository{pool: pool}
}

// LoadWork returns pending mentions whose completed derivation remains current
// and admissible, excluding work covered by fresh durable attempts.
func (repository *DirectoryRepository) LoadWork(
	ctx context.Context,
	derivationID string,
	now time.Time,
	freshness time.Duration,
	retryAfter time.Duration,
) (directory.Workset, error) {
	if repository == nil || repository.pool == nil {
		return directory.Workset{}, fmt.Errorf("load directory work: repository is not configured")
	}
	if strings.TrimSpace(derivationID) == "" || now.IsZero() ||
		freshness <= 0 || retryAfter <= 0 {
		return directory.Workset{}, fmt.Errorf("load directory work: input is invalid")
	}
	const eligibleMentions = `
		SELECT mention.id AS mention_id,
		       proposal.id AS proposal_id,
		       mention.surface,
		       mention.normalized_name,
		       mention.proposed_email,
		       name_span.quote AS name_quote,
		       COALESCE(email_span.quote, '') AS email_quote
		FROM stacks.mentions AS mention
		JOIN stacks.resolution_proposals AS proposal
		  ON proposal.mention_id = mention.id
		JOIN stacks.evidence_spans AS name_span
		  ON name_span.id = mention.evidence_span_id
		LEFT JOIN stacks.evidence_spans AS email_span
		  ON email_span.id = mention.proposed_email_evidence_span_id
		JOIN stacks.extraction_runs AS extraction_run
		  ON extraction_run.id = mention.extraction_run_id
		JOIN stacks.document_versions AS extraction_version
		  ON extraction_version.id = extraction_run.document_version_id
		JOIN stacks.source_documents AS source_document
		  ON source_document.id = extraction_version.source_document_id
		WHERE extraction_run.id = $1
		  AND extraction_run.processing_status = 'complete'
		  AND extraction_run.currently_admissible
		  AND source_document.current_document_version_id = extraction_run.document_version_id
		  AND mention.currently_admissible
		  AND proposal.status = 'pending'
		  AND NOT EXISTS (
		      SELECT 1
		      FROM stacks.resolution_decisions AS decision
		      WHERE decision.proposal_id = proposal.id
		        AND decision.superseded_by_id IS NULL
		  )`

	var work directory.Workset
	if err := repository.pool.QueryRow(ctx, `
		WITH eligible AS (`+eligibleMentions+`)
		SELECT count(*)
		FROM eligible
		WHERE EXISTS (
		    SELECT 1
		    FROM stacks.directory_lookup_attempts AS attempt
		    WHERE attempt.mention_id = eligible.mention_id
		      AND attempt.outcome IN ('matched', 'no_match', 'ambiguous', 'review')
		      AND attempt.recorded_at >= $2::timestamptz - ($3::bigint * INTERVAL '1 microsecond')
		)`,
		derivationID, now.UTC(), freshness.Microseconds(),
	).Scan(&work.Reused); err != nil {
		return directory.Workset{}, fmt.Errorf("load directory work reuse count: %w", err)
	}

	rows, err := repository.pool.Query(ctx, `
		WITH eligible AS (`+eligibleMentions+`)
		SELECT mention_id::text,
		       proposal_id::text,
		       surface,
		       normalized_name,
		       proposed_email,
		       name_quote,
		       email_quote
		FROM eligible
		WHERE NOT EXISTS (
		    SELECT 1
		    FROM stacks.directory_lookup_attempts AS attempt
		    WHERE attempt.mention_id = eligible.mention_id
		      AND attempt.outcome IN ('matched', 'no_match', 'ambiguous', 'review')
		      AND attempt.recorded_at >= $2::timestamptz - ($3::bigint * INTERVAL '1 microsecond')
		)
		  AND NOT EXISTS (
		    SELECT 1
		    FROM stacks.directory_lookup_attempts AS attempt
		    WHERE attempt.mention_id = eligible.mention_id
		      AND attempt.outcome IN ('rate_limited', 'unavailable')
		      AND COALESCE(
		          attempt.retry_after,
		          attempt.recorded_at + ($4::bigint * INTERVAL '1 microsecond')
		      ) > $2::timestamptz
		)
		ORDER BY mention_id`,
		derivationID, now.UTC(), freshness.Microseconds(), retryAfter.Microseconds(),
	)
	if err != nil {
		return directory.Workset{}, fmt.Errorf("load directory work mentions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var mention directory.PendingMention
		if err := rows.Scan(
			&mention.MentionID,
			&mention.ProposalID,
			&mention.Surface,
			&mention.NormalizedName,
			&mention.ProposedEmail,
			&mention.NameQuote,
			&mention.EmailQuote,
		); err != nil {
			return directory.Workset{}, fmt.Errorf("scan directory work mention: %w", err)
		}
		work.Mentions = append(work.Mentions, mention)
	}
	if err := rows.Err(); err != nil {
		return directory.Workset{}, fmt.Errorf("iterate directory work mentions: %w", err)
	}
	return work, nil
}

// LoadIdentityState reuses the ingestion resolver's accepted-alias projection
// and adds only currently effective, admissible directory identity links.
func (repository *DirectoryRepository) LoadIdentityState(ctx context.Context) (directory.IdentityState, error) {
	if repository == nil || repository.pool == nil {
		return directory.IdentityState{}, fmt.Errorf("load directory identity state: repository is not configured")
	}
	snapshots, err := NewIngestionRepository(repository.pool).EntitySnapshots(ctx)
	if err != nil {
		return directory.IdentityState{}, fmt.Errorf("load directory identity state snapshots: %w", err)
	}
	for index := range snapshots {
		sort.Slice(snapshots[index].Aliases, func(left, right int) bool {
			if snapshots[index].Aliases[left].Type == snapshots[index].Aliases[right].Type {
				return snapshots[index].Aliases[left].Value < snapshots[index].Aliases[right].Value
			}
			return snapshots[index].Aliases[left].Type < snapshots[index].Aliases[right].Type
		})
	}
	sort.Slice(snapshots, func(left, right int) bool {
		return snapshots[left].ID < snapshots[right].ID
	})

	rows, err := repository.pool.Query(ctx, `
		SELECT DISTINCT
		       assertion.provider,
		       assertion.provider_subject_id,
		       assertion.entity_id::text
		FROM stacks.entity_directory_identity_assertions AS assertion
		JOIN stacks.resolution_decisions AS decision
		  ON decision.id = assertion.decision_id
		JOIN stacks.resolution_proposals AS proposal
		  ON proposal.id = decision.proposal_id
		JOIN stacks.mentions AS mention
		  ON mention.id = proposal.mention_id
		LEFT JOIN stacks.extraction_runs AS extraction_run
		  ON extraction_run.id = mention.extraction_run_id
		LEFT JOIN stacks.document_versions AS extraction_version
		  ON extraction_version.id = extraction_run.document_version_id
		LEFT JOIN stacks.source_documents AS source_document
		  ON source_document.id = extraction_version.source_document_id
		WHERE decision.entity_id = assertion.entity_id
		  AND decision.outcome IN ('accepted', 'created')
		  AND decision.superseded_by_id IS NULL
		  AND decision.currently_admissible
		  AND mention.currently_admissible
		  AND (
		      mention.extraction_run_id IS NULL
		      OR (
		          extraction_run.currently_admissible
		          AND source_document.current_document_version_id = extraction_run.document_version_id
		      )
		  )
		ORDER BY assertion.provider,
		         assertion.provider_subject_id,
		         assertion.entity_id::text`)
	if err != nil {
		return directory.IdentityState{}, fmt.Errorf("load directory identity state links: %w", err)
	}
	defer rows.Close()
	state := directory.IdentityState{Snapshots: snapshots}
	for rows.Next() {
		var link entity.DirectoryIdentityLink
		if err := rows.Scan(&link.Provider, &link.SubjectID, &link.EntityID); err != nil {
			return directory.IdentityState{}, fmt.Errorf("scan directory identity state link: %w", err)
		}
		state.Links = append(state.Links, link)
	}
	if err := rows.Err(); err != nil {
		return directory.IdentityState{}, fmt.Errorf("iterate directory identity state links: %w", err)
	}
	return state, nil
}

// Persist atomically stores immutable directory evidence and, only for a
// post-lock unique exact-email match, current identity authority.
func (repository *DirectoryRepository) Persist(ctx context.Context, input directory.PersistInput) (directory.PersistResult, error) {
	if repository == nil || repository.pool == nil {
		return directory.PersistResult{}, fmt.Errorf("persist directory lookup: repository is not configured")
	}
	input.RecordedAt = directoryDatabaseTime(input.RecordedAt)
	if input.RetryAfter != nil {
		retryAfter := directoryDatabaseTime(*input.RetryAfter)
		input.RetryAfter = &retryAfter
	}
	if err := validateDirectoryPersistInput(input); err != nil {
		return directory.PersistResult{}, err
	}

	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return directory.PersistResult{}, fmt.Errorf("persist directory lookup: start transaction: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.

	if err := lockDirectoryProposal(ctx, transaction, input.Mention.ProposalID); err != nil {
		return directory.PersistResult{}, err
	}
	profiles, err := persistDirectoryProfiles(ctx, transaction, input.Lookup.Profiles, input.RecordedAt)
	if err != nil {
		return directory.PersistResult{}, err
	}
	attempt, err := persistDirectoryAttempt(ctx, transaction, input, profiles)
	if err != nil {
		return directory.PersistResult{}, err
	}

	var result directory.PersistResult
	if input.Evaluation.Outcome == entity.DirectoryMatched {
		result, err = repository.persistAutomaticDirectoryAuthority(
			ctx,
			transaction,
			input,
			attempt,
			profiles,
		)
	} else if len(input.Evaluation.Candidates) > 0 {
		err = persistDirectoryReviewCandidates(
			ctx,
			transaction,
			input.Mention.ProposalID,
			input.Query.Kind,
			input.Evaluation.Candidates,
			profiles,
		)
	}
	if err != nil {
		return directory.PersistResult{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return directory.PersistResult{}, fmt.Errorf("persist directory lookup: commit transaction: %w", err)
	}
	return result, nil
}

func lockDirectoryProposal(ctx context.Context, transaction pgx.Tx, proposalID string) error {
	if err := lockResolutionProposal(ctx, transaction, proposalID); err != nil {
		return fmt.Errorf("persist directory lookup: %w", err)
	}
	return nil
}

// Identity mutations use one global lock order: proposal row, then sorted
// email/provider advisory authority, then effective decision row. Callers may
// inspect immutable decision evidence before the advisory locks, but must not
// lock a decision row before them.
func lockResolutionProposal(
	ctx context.Context,
	transaction pgx.Tx,
	proposalID string,
) error {
	var locked bool
	if err := transaction.QueryRow(ctx, `
		SELECT true
		FROM stacks.resolution_proposals
		WHERE id = $1
		FOR UPDATE`, proposalID).Scan(&locked); err == pgx.ErrNoRows {
		return fmt.Errorf("resolution proposal does not exist")
	} else if err != nil {
		return fmt.Errorf("lock resolution proposal: %w", err)
	}
	return nil
}

func persistDirectoryProfiles(
	ctx context.Context,
	transaction pgx.Tx,
	profiles []entity.DirectoryProfile,
	recordedAt time.Time,
) ([]storedDirectoryProfile, error) {
	canonical := make([]storedDirectoryProfile, 0, len(profiles))
	seen := make(map[[sha256.Size]byte]struct{}, len(profiles))
	for _, profile := range profiles {
		normalized, err := canonicalDirectoryProfile(profile)
		if err != nil {
			return nil, err
		}
		digest, err := directoryProfileDigest(normalized)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[digest]; duplicate {
			continue
		}
		seen[digest] = struct{}{}
		canonical = append(canonical, storedDirectoryProfile{
			ID:      uuid.NewSHA1(uuid.NameSpaceOID, digest[:]).String(),
			Profile: normalized,
			Digest:  digest,
		})
	}
	sort.Slice(canonical, func(left, right int) bool {
		return bytes.Compare(canonical[left].Digest[:], canonical[right].Digest[:]) < 0
	})
	for _, profile := range canonical {
		if err := persistDirectoryProfile(ctx, transaction, profile, recordedAt); err != nil {
			return nil, err
		}
	}
	return canonical, nil
}

func persistDirectoryProfile(
	ctx context.Context,
	transaction pgx.Tx,
	profile storedDirectoryProfile,
	recordedAt time.Time,
) error {
	var observedAt *time.Time
	if !profile.Profile.ObservedAt.IsZero() {
		value := directoryDatabaseTime(profile.Profile.ObservedAt)
		observedAt = &value
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO stacks.directory_profile_snapshots
			(id, provider, provider_subject_id, source_type, display_name,
			 observed_at, recorded_at, digest)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT DO NOTHING`,
		profile.ID,
		profile.Profile.Provider,
		profile.Profile.SubjectID,
		string(profile.Profile.Source),
		profile.Profile.DisplayName,
		observedAt,
		recordedAt,
		profile.Digest[:],
	); err != nil {
		return fmt.Errorf("persist directory lookup: insert profile snapshot: %w", err)
	}

	var (
		storedProvider    string
		storedSubject     string
		storedSource      string
		storedDisplayName string
		storedObservedAt  *time.Time
		storedDigest      []byte
	)
	if err := transaction.QueryRow(ctx, `
		SELECT provider, provider_subject_id, source_type, display_name,
		       observed_at, digest
		FROM stacks.directory_profile_snapshots
		WHERE id = $1`,
		profile.ID,
	).Scan(
		&storedProvider,
		&storedSubject,
		&storedSource,
		&storedDisplayName,
		&storedObservedAt,
		&storedDigest,
	); err != nil {
		return fmt.Errorf("persist directory lookup: load profile snapshot: %w", err)
	}
	if storedProvider != profile.Profile.Provider ||
		storedSubject != profile.Profile.SubjectID ||
		storedSource != string(profile.Profile.Source) ||
		storedDisplayName != profile.Profile.DisplayName ||
		!sameDirectoryTimePointer(storedObservedAt, observedAt) ||
		!bytes.Equal(storedDigest, profile.Digest[:]) {
		return fmt.Errorf("persist directory lookup: stored profile snapshot conflicts")
	}

	for position, email := range profile.Profile.Emails {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO stacks.directory_profile_emails
				(snapshot_id, normalized_email, is_primary, position)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT DO NOTHING`,
			profile.ID,
			email.Value,
			email.Primary,
			position,
		); err != nil {
			return fmt.Errorf("persist directory lookup: insert profile email: %w", err)
		}
	}
	rows, err := transaction.Query(ctx, `
		SELECT normalized_email, is_primary, position
		FROM stacks.directory_profile_emails
		WHERE snapshot_id = $1
		ORDER BY position`,
		profile.ID,
	)
	if err != nil {
		return fmt.Errorf("persist directory lookup: load profile emails: %w", err)
	}
	defer rows.Close()
	position := 0
	for rows.Next() {
		if position >= len(profile.Profile.Emails) {
			return fmt.Errorf("persist directory lookup: stored profile emails conflict")
		}
		var storedEmail string
		var storedPrimary bool
		var storedPosition int
		if err := rows.Scan(&storedEmail, &storedPrimary, &storedPosition); err != nil {
			return fmt.Errorf("persist directory lookup: scan profile email: %w", err)
		}
		want := profile.Profile.Emails[position]
		if storedEmail != want.Value ||
			storedPrimary != want.Primary ||
			storedPosition != position {
			return fmt.Errorf("persist directory lookup: stored profile emails conflict")
		}
		position++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("persist directory lookup: iterate profile emails: %w", err)
	}
	if position != len(profile.Profile.Emails) {
		return fmt.Errorf("persist directory lookup: stored profile emails conflict")
	}
	return nil
}

func persistDirectoryAttempt(
	ctx context.Context,
	transaction pgx.Tx,
	input directory.PersistInput,
	profiles []storedDirectoryProfile,
) (storedDirectoryAttempt, error) {
	queryDigest, err := directoryQueryDigest(input.Query)
	if err != nil {
		return storedDirectoryAttempt{}, err
	}
	attemptDigest, err := directoryLookupDigest(input, entity.DirectoryPolicyVersion)
	if err != nil {
		return storedDirectoryAttempt{}, err
	}
	attempt := storedDirectoryAttempt{
		ID: uuid.NewSHA1(uuid.NameSpaceOID, attemptDigest[:]).String(),
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO stacks.directory_lookup_attempts
			(id, mention_id, provider, query_kind, email_evidence, query_digest,
			 policy_version, outcome, attempt_count, retry_after, recorded_at, digest)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT DO NOTHING`,
		attempt.ID,
		input.Mention.MentionID,
		googlePeopleDirectoryProvider,
		string(input.Query.Kind),
		string(input.Query.EmailEvidence),
		queryDigest[:],
		entity.DirectoryPolicyVersion,
		string(input.Lookup.Outcome),
		input.AttemptCount,
		input.RetryAfter,
		input.RecordedAt,
		attemptDigest[:],
	); err != nil {
		return storedDirectoryAttempt{}, fmt.Errorf("persist directory lookup: insert attempt: %w", err)
	}

	var (
		storedMentionID     string
		storedProvider      string
		storedQueryKind     string
		storedEmailEvidence string
		storedQueryDigest   []byte
		storedPolicy        string
		storedOutcome       string
		storedAttemptCount  int
		storedRetryAfter    *time.Time
		storedRecordedAt    time.Time
		storedDigest        []byte
	)
	if err := transaction.QueryRow(ctx, `
		SELECT mention_id::text, provider, query_kind, email_evidence,
		       query_digest, policy_version, outcome, attempt_count,
		       retry_after, recorded_at, digest
		FROM stacks.directory_lookup_attempts
		WHERE id = $1`,
		attempt.ID,
	).Scan(
		&storedMentionID,
		&storedProvider,
		&storedQueryKind,
		&storedEmailEvidence,
		&storedQueryDigest,
		&storedPolicy,
		&storedOutcome,
		&storedAttemptCount,
		&storedRetryAfter,
		&storedRecordedAt,
		&storedDigest,
	); err != nil {
		return storedDirectoryAttempt{}, fmt.Errorf("persist directory lookup: load attempt: %w", err)
	}
	if storedMentionID != input.Mention.MentionID ||
		storedProvider != googlePeopleDirectoryProvider ||
		storedQueryKind != string(input.Query.Kind) ||
		storedEmailEvidence != string(input.Query.EmailEvidence) ||
		!bytes.Equal(storedQueryDigest, queryDigest[:]) ||
		storedPolicy != entity.DirectoryPolicyVersion ||
		storedOutcome != string(input.Lookup.Outcome) ||
		storedAttemptCount != input.AttemptCount ||
		!sameDirectoryTimePointer(storedRetryAfter, input.RetryAfter) ||
		!storedRecordedAt.Equal(input.RecordedAt) ||
		!bytes.Equal(storedDigest, attemptDigest[:]) {
		return storedDirectoryAttempt{}, fmt.Errorf("persist directory lookup: stored attempt conflicts")
	}

	reason := "name_candidate"
	if input.Query.Kind == entity.DirectoryQueryEmail {
		reason = "exact_email"
	}
	for rank, profile := range profiles {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO stacks.directory_lookup_matches
				(lookup_attempt_id, snapshot_id, rank, reason)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT DO NOTHING`,
			attempt.ID,
			profile.ID,
			rank,
			reason,
		); err != nil {
			return storedDirectoryAttempt{}, fmt.Errorf("persist directory lookup: insert match: %w", err)
		}
	}
	rows, err := transaction.Query(ctx, `
		SELECT snapshot_id::text, rank, reason
		FROM stacks.directory_lookup_matches
		WHERE lookup_attempt_id = $1
		ORDER BY rank`,
		attempt.ID,
	)
	if err != nil {
		return storedDirectoryAttempt{}, fmt.Errorf("persist directory lookup: load matches: %w", err)
	}
	defer rows.Close()
	rank := 0
	for rows.Next() {
		if rank >= len(profiles) {
			return storedDirectoryAttempt{}, fmt.Errorf("persist directory lookup: stored matches conflict")
		}
		var storedSnapshotID, storedReason string
		var storedRank int
		if err := rows.Scan(&storedSnapshotID, &storedRank, &storedReason); err != nil {
			return storedDirectoryAttempt{}, fmt.Errorf("persist directory lookup: scan match: %w", err)
		}
		if storedSnapshotID != profiles[rank].ID ||
			storedRank != rank ||
			storedReason != reason {
			return storedDirectoryAttempt{}, fmt.Errorf("persist directory lookup: stored matches conflict")
		}
		rank++
	}
	if err := rows.Err(); err != nil {
		return storedDirectoryAttempt{}, fmt.Errorf("persist directory lookup: iterate matches: %w", err)
	}
	if rank != len(profiles) {
		return storedDirectoryAttempt{}, fmt.Errorf("persist directory lookup: stored matches conflict")
	}
	return attempt, nil
}

func (repository *DirectoryRepository) persistAutomaticDirectoryAuthority(
	ctx context.Context,
	transaction pgx.Tx,
	input directory.PersistInput,
	attempt storedDirectoryAttempt,
	profiles []storedDirectoryProfile,
) (directory.PersistResult, error) {
	profile, err := findStoredDirectoryProfile(*input.Evaluation.Profile, profiles)
	if err != nil {
		return directory.PersistResult{}, err
	}
	acceptedEmail := entity.NormalizeEmail(input.Evaluation.AcceptedEmail)
	if repository.testHooks.beforeAuthorityLocks != nil {
		repository.testHooks.beforeAuthorityLocks()
	}
	if err := lockDirectoryAuthority(
		ctx,
		transaction,
		acceptedEmail,
		profile.Profile.Provider,
		profile.Profile.SubjectID,
	); err != nil {
		return directory.PersistResult{}, err
	}
	if repository.testHooks.afterAuthorityLocks != nil {
		if err := repository.testHooks.afterAuthorityLocks(); err != nil {
			return directory.PersistResult{}, fmt.Errorf("persist directory lookup: authority lock hook: %w", err)
		}
	}

	emailOwners, err := loadCurrentAcceptedEmailOwners(ctx, transaction, acceptedEmail)
	if err != nil {
		return directory.PersistResult{}, err
	}
	providerOwners, err := loadTriggerEffectiveDirectoryIdentityOwners(
		ctx,
		transaction,
		profile.Profile.Provider,
		profile.Profile.SubjectID,
	)
	if err != nil {
		return directory.PersistResult{}, err
	}
	effectiveDecision, effectiveErr := loadEffectiveDecision(
		ctx,
		transaction,
		input.Mention.ProposalID,
	)
	if effectiveErr == nil {
		if (effectiveDecision.Outcome == ResolutionOutcomeAccepted ||
			effectiveDecision.Outcome == ResolutionOutcomeCreated) &&
			containsString(emailOwners, effectiveDecision.EntityID) &&
			containsString(providerOwners, effectiveDecision.EntityID) {
			return directory.PersistResult{
				AutoResolved: true,
				EntityID:     effectiveDecision.EntityID,
			}, nil
		}
		return directory.PersistResult{}, nil
	}
	if effectiveErr != pgx.ErrNoRows {
		return directory.PersistResult{}, fmt.Errorf("persist directory lookup: load effective decision: %w", effectiveErr)
	}

	entityID, createPerson, conflict := automaticDirectoryEntity(
		input.Evaluation,
		emailOwners,
		providerOwners,
	)
	if conflict {
		if err := persistDirectoryReviewCandidates(
			ctx,
			transaction,
			input.Mention.ProposalID,
			input.Query.Kind,
			[]entity.DirectoryProfile{profile.Profile},
			profiles,
		); err != nil {
			return directory.PersistResult{}, err
		}
		return directory.PersistResult{}, nil
	}

	outcome := ResolutionOutcomeAccepted
	if createPerson {
		outcome = ResolutionOutcomeCreated
		entityID = uuid.NewSHA1(
			uuid.NameSpaceOID,
			[]byte(directoryPersonIdentityVersion+"\x00"+profile.ID),
		).String()
		if err := persistDirectoryEntity(
			ctx,
			transaction,
			entityID,
			profile.Profile.DisplayName,
			input.RecordedAt,
		); err != nil {
			return directory.PersistResult{}, err
		}
		if repository.testHooks.afterEntityCreated != nil {
			if err := repository.testHooks.afterEntityCreated(); err != nil {
				return directory.PersistResult{}, fmt.Errorf("persist directory lookup: entity hook: %w", err)
			}
		}
	}

	decision, err := persistDirectoryDecision(
		ctx,
		transaction,
		ResolutionDecisionInput{
			ProposalID: input.Mention.ProposalID,
			Outcome:    outcome,
			EntityID:   entityID,
		},
		attempt.ID,
		profile.ID,
		input.RecordedAt,
	)
	if err != nil {
		return directory.PersistResult{}, err
	}
	if err := insertDirectoryEmailAliasAssertion(
		ctx,
		transaction,
		decision,
		acceptedEmail,
		input.RecordedAt,
	); err != nil {
		return directory.PersistResult{}, err
	}
	if err := persistDirectoryIdentityAssertion(
		ctx,
		transaction,
		decision,
		attempt.ID,
		profile,
		input.RecordedAt,
	); err != nil {
		return directory.PersistResult{}, err
	}
	if err := updateProposalStatus(
		ctx,
		transaction,
		input.Mention.ProposalID,
		outcome,
	); err != nil {
		return directory.PersistResult{}, err
	}
	return directory.PersistResult{AutoResolved: true, EntityID: entityID}, nil
}

type directoryAuthorityLock struct {
	namespace string
	key       string
}

func lockDirectoryAuthority(
	ctx context.Context,
	transaction pgx.Tx,
	email string,
	provider string,
	subject string,
) error {
	var locks []directoryAuthorityLock
	if email = entity.NormalizeEmail(email); email != "" {
		locks = append(locks, directoryAuthorityLock{
			namespace: "directory_email",
			key:       email,
		})
	}
	provider = strings.TrimSpace(provider)
	subject = strings.TrimSpace(subject)
	if (provider == "") != (subject == "") {
		return fmt.Errorf("persist directory lookup: provider authority is incomplete")
	}
	if provider != "" {
		locks = append(locks, directoryAuthorityLock{
			namespace: provider,
			key:       subject,
		})
	}
	return acquireDirectoryAuthorityLocks(ctx, transaction, locks)
}

func lockDirectoryEmailAuthorities(
	ctx context.Context,
	transaction pgx.Tx,
	emails []string,
) error {
	locks := make([]directoryAuthorityLock, 0, len(emails))
	for _, email := range emails {
		normalized := entity.NormalizeEmail(email)
		if normalized == "" {
			continue
		}
		locks = append(locks, directoryAuthorityLock{
			namespace: "directory_email",
			key:       normalized,
		})
	}
	return acquireDirectoryAuthorityLocks(ctx, transaction, locks)
}

func acquireDirectoryAuthorityLocks(
	ctx context.Context,
	transaction pgx.Tx,
	locks []directoryAuthorityLock,
) error {
	sort.Slice(locks, func(left, right int) bool {
		if locks[left].namespace == locks[right].namespace {
			return locks[left].key < locks[right].key
		}
		return locks[left].namespace < locks[right].namespace
	})
	for index, lock := range locks {
		if index > 0 && lock == locks[index-1] {
			continue
		}
		if _, err := transaction.Exec(ctx, `
			SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
			lock.namespace,
			lock.key,
		); err != nil {
			return fmt.Errorf("persist directory lookup: acquire authority lock: %w", err)
		}
	}
	return nil
}

func loadCurrentAcceptedEmailOwners(
	ctx context.Context,
	transaction pgx.Tx,
	email string,
) ([]string, error) {
	rows, err := transaction.Query(ctx, `
		SELECT DISTINCT assertion.entity_id::text`+
		currentAcceptedAliasAuthoritySQL+`
		  AND assertion.alias_type = 'email'
		  AND assertion.normalized_value = $1
		ORDER BY assertion.entity_id::text`,
		email,
	)
	if err != nil {
		return nil, fmt.Errorf("persist directory lookup: load accepted email owners: %w", err)
	}
	defer rows.Close()
	var owners []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			return nil, fmt.Errorf("persist directory lookup: scan accepted email owner: %w", err)
		}
		owners = append(owners, owner)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("persist directory lookup: iterate accepted email owners: %w", err)
	}
	return owners, nil
}

// loadTriggerEffectiveDirectoryIdentityOwners intentionally mirrors migration
// 00012's deferred authority constraint. Source-current projection rules do
// not narrow the owners that can make the constraint reject a transaction.
func loadTriggerEffectiveDirectoryIdentityOwners(
	ctx context.Context,
	transaction pgx.Tx,
	provider string,
	subject string,
) ([]string, error) {
	rows, err := transaction.Query(ctx, `
		SELECT DISTINCT assertion.entity_id::text
		FROM stacks.entity_directory_identity_assertions AS assertion
		JOIN stacks.resolution_decisions AS decision
		  ON decision.id = assertion.decision_id
		WHERE assertion.provider = $1
		  AND assertion.provider_subject_id = $2
		  AND decision.superseded_by_id IS NULL
		  AND decision.outcome IN ('accepted', 'created')
		  AND decision.currently_admissible
		ORDER BY assertion.entity_id::text`,
		provider,
		subject,
	)
	if err != nil {
		return nil, fmt.Errorf("persist directory lookup: load provider identity owners: %w", err)
	}
	defer rows.Close()
	var owners []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			return nil, fmt.Errorf("persist directory lookup: scan provider identity owner: %w", err)
		}
		owners = append(owners, owner)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("persist directory lookup: iterate provider identity owners: %w", err)
	}
	return owners, nil
}

func automaticDirectoryEntity(
	evaluation entity.DirectoryEvaluation,
	emailOwners []string,
	providerOwners []string,
) (entityID string, createPerson bool, conflict bool) {
	if len(emailOwners) > 1 || len(providerOwners) > 1 {
		return "", false, true
	}
	if len(providerOwners) == 1 {
		if len(emailOwners) == 0 || emailOwners[0] != providerOwners[0] {
			return "", false, true
		}
		return emailOwners[0], false, false
	}
	if len(emailOwners) == 1 {
		return emailOwners[0], false, false
	}
	if !evaluation.CreatePerson || evaluation.EntityID != "" {
		return "", false, true
	}
	return "", true, false
}

func persistDirectoryEntity(
	ctx context.Context,
	transaction pgx.Tx,
	entityID string,
	displayName string,
	recordedAt time.Time,
) error {
	if _, err := transaction.Exec(ctx, `
		INSERT INTO stacks.entities (id, kind, display_name, recorded_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO NOTHING`,
		entityID,
		string(entity.KindPerson),
		displayName,
		recordedAt,
	); err != nil {
		return fmt.Errorf("persist directory lookup: create person: %w", err)
	}
	var storedKind, storedDisplayName string
	if err := transaction.QueryRow(ctx, `
		SELECT kind, display_name
		FROM stacks.entities
		WHERE id = $1`,
		entityID,
	).Scan(&storedKind, &storedDisplayName); err != nil {
		return fmt.Errorf("persist directory lookup: load person: %w", err)
	}
	if storedKind != string(entity.KindPerson) || storedDisplayName != displayName {
		return fmt.Errorf("persist directory lookup: stored person conflicts")
	}
	return nil
}

func persistDirectoryDecision(
	ctx context.Context,
	transaction pgx.Tx,
	input ResolutionDecisionInput,
	attemptID string,
	snapshotID string,
	recordedAt time.Time,
) (ResolutionDecision, error) {
	digest := resolutionDecisionDigest(input, "", attemptID, snapshotID)
	decisionID := uuid.NewSHA1(uuid.NameSpaceOID, digest[:]).String()
	if _, err := transaction.Exec(ctx, `
		INSERT INTO stacks.resolution_decisions
			(id, proposal_id, outcome, entity_id, supersedes_id, digest,
			 recorded_at, currently_admissible)
		VALUES ($1, $2, $3, $4, NULL, $5, $6, true)
		ON CONFLICT (id) DO NOTHING`,
		decisionID,
		input.ProposalID,
		string(input.Outcome),
		input.EntityID,
		digest[:],
		recordedAt,
	); err != nil {
		return ResolutionDecision{}, fmt.Errorf("persist directory lookup: insert decision: %w", err)
	}
	var decision ResolutionDecision
	var storedDigest []byte
	var storedRecordedAt time.Time
	if err := transaction.QueryRow(ctx, `
		SELECT id::text, proposal_id::text, outcome,
		       COALESCE(entity_id::text, ''), digest, recorded_at
		FROM stacks.resolution_decisions
		WHERE id = $1`,
		decisionID,
	).Scan(
		&decision.ID,
		&decision.ProposalID,
		&decision.Outcome,
		&decision.EntityID,
		&storedDigest,
		&storedRecordedAt,
	); err != nil {
		return ResolutionDecision{}, fmt.Errorf("persist directory lookup: load decision: %w", err)
	}
	if decision.ProposalID != input.ProposalID ||
		decision.Outcome != input.Outcome ||
		decision.EntityID != input.EntityID ||
		!bytes.Equal(storedDigest, digest[:]) ||
		!storedRecordedAt.Equal(recordedAt) {
		return ResolutionDecision{}, fmt.Errorf("persist directory lookup: stored decision conflicts")
	}
	return decision, nil
}

func insertDirectoryEmailAliasAssertion(
	ctx context.Context,
	transaction pgx.Tx,
	decision ResolutionDecision,
	email string,
	recordedAt time.Time,
) error {
	alias := AliasInput{
		EntityID:        decision.EntityID,
		NormalizedValue: email,
		Type:            string(entity.AliasTypeEmail),
	}
	if err := validateAliasInput(alias); err != nil {
		return fmt.Errorf("persist directory lookup: accepted email is invalid")
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO stacks.entity_alias_assertions
			(decision_id, entity_id, normalized_value, alias_type, recorded_at)
		VALUES ($1, $2, $3, 'email', $4)
		ON CONFLICT (decision_id, normalized_value, alias_type) DO NOTHING`,
		decision.ID,
		decision.EntityID,
		email,
		recordedAt,
	); err != nil {
		return fmt.Errorf("persist directory lookup: insert accepted email: %w", err)
	}
	var storedEntityID, storedType string
	var storedRecordedAt time.Time
	if err := transaction.QueryRow(ctx, `
		SELECT entity_id::text, alias_type, recorded_at
		FROM stacks.entity_alias_assertions
		WHERE decision_id = $1
		  AND normalized_value = $2
		  AND alias_type = 'email'`,
		decision.ID,
		email,
	).Scan(&storedEntityID, &storedType, &storedRecordedAt); err != nil {
		return fmt.Errorf("persist directory lookup: load accepted email: %w", err)
	}
	if storedEntityID != decision.EntityID ||
		storedType != string(entity.AliasTypeEmail) ||
		!storedRecordedAt.Equal(recordedAt) {
		return fmt.Errorf("persist directory lookup: stored accepted email conflicts")
	}
	return nil
}

func persistDirectoryIdentityAssertion(
	ctx context.Context,
	transaction pgx.Tx,
	decision ResolutionDecision,
	attemptID string,
	profile storedDirectoryProfile,
	recordedAt time.Time,
) error {
	assertionID := uuid.NewSHA1(
		uuid.NameSpaceOID,
		[]byte(strings.Join([]string{
			"stacks.directory-identity-assertion.v1",
			decision.ID,
			profile.Profile.Provider,
			profile.Profile.SubjectID,
		}, "\x00")),
	).String()
	if _, err := transaction.Exec(ctx, `
		INSERT INTO stacks.entity_directory_identity_assertions
			(id, decision_id, entity_id, lookup_attempt_id, snapshot_id,
			 provider, provider_subject_id, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT DO NOTHING`,
		assertionID,
		decision.ID,
		decision.EntityID,
		attemptID,
		profile.ID,
		profile.Profile.Provider,
		profile.Profile.SubjectID,
		recordedAt,
	); err != nil {
		return fmt.Errorf("persist directory lookup: insert identity assertion: %w", err)
	}
	var (
		storedDecisionID string
		storedEntityID   string
		storedAttemptID  string
		storedSnapshotID string
		storedProvider   string
		storedSubject    string
		storedRecordedAt time.Time
	)
	if err := transaction.QueryRow(ctx, `
		SELECT decision_id::text, entity_id::text, lookup_attempt_id::text,
		       snapshot_id::text, provider, provider_subject_id, recorded_at
		FROM stacks.entity_directory_identity_assertions
		WHERE id = $1`,
		assertionID,
	).Scan(
		&storedDecisionID,
		&storedEntityID,
		&storedAttemptID,
		&storedSnapshotID,
		&storedProvider,
		&storedSubject,
		&storedRecordedAt,
	); err != nil {
		return fmt.Errorf("persist directory lookup: load identity assertion: %w", err)
	}
	if storedDecisionID != decision.ID ||
		storedEntityID != decision.EntityID ||
		storedAttemptID != attemptID ||
		storedSnapshotID != profile.ID ||
		storedProvider != profile.Profile.Provider ||
		storedSubject != profile.Profile.SubjectID ||
		!storedRecordedAt.Equal(recordedAt) {
		return fmt.Errorf("persist directory lookup: stored identity assertion conflicts")
	}
	return nil
}

func persistDirectoryReviewCandidates(
	ctx context.Context,
	transaction pgx.Tx,
	proposalID string,
	queryKind entity.DirectoryQueryKind,
	candidates []entity.DirectoryProfile,
	profiles []storedDirectoryProfile,
) error {
	storedCandidates := make([]storedDirectoryProfile, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		stored, err := findStoredDirectoryProfile(candidate, profiles)
		if err != nil {
			return err
		}
		if _, duplicate := seen[stored.ID]; duplicate {
			continue
		}
		seen[stored.ID] = struct{}{}
		storedCandidates = append(storedCandidates, stored)
	}
	sort.Slice(storedCandidates, func(left, right int) bool {
		return storedCandidates[left].ID < storedCandidates[right].ID
	})
	if len(storedCandidates) == 0 {
		return nil
	}
	reason := directoryNameReviewReason
	if queryKind == entity.DirectoryQueryEmail {
		reason = directoryExactEmailReviewReason
	}
	confidence := 0.0
	newCandidates := storedCandidates[:0]
	for _, candidate := range storedCandidates {
		var storedRank int
		var storedConfidence *float64
		var storedReason string
		err := transaction.QueryRow(ctx, `
			SELECT rank, confidence, reason
			FROM stacks.resolution_candidates
			WHERE proposal_id = $1
			  AND directory_profile_snapshot_id = $2`,
			proposalID,
			candidate.ID,
		).Scan(&storedRank, &storedConfidence, &storedReason)
		if err == nil {
			if storedRank < 0 ||
				storedConfidence == nil ||
				*storedConfidence != confidence ||
				storedReason != reason {
				return fmt.Errorf("persist directory lookup: stored review candidate conflicts")
			}
			continue
		}
		if err != pgx.ErrNoRows {
			return fmt.Errorf("persist directory lookup: load review candidate: %w", err)
		}
		newCandidates = append(newCandidates, candidate)
	}
	if len(newCandidates) == 0 {
		return nil
	}
	var nextRank int
	if err := transaction.QueryRow(ctx, `
		SELECT COALESCE(max(rank), -1) + 1
		FROM stacks.resolution_candidates
		WHERE proposal_id = $1`,
		proposalID,
	).Scan(&nextRank); err != nil {
		return fmt.Errorf("persist directory lookup: load candidate rank: %w", err)
	}
	for offset, candidate := range newCandidates {
		rank := nextRank + offset
		if _, err := transaction.Exec(ctx, `
			INSERT INTO stacks.resolution_candidates
				(proposal_id, entity_id, directory_profile_snapshot_id,
				 rank, confidence, reason)
			VALUES ($1, NULL, $2, $3, $4, $5)
			ON CONFLICT DO NOTHING`,
			proposalID,
			candidate.ID,
			rank,
			confidence,
			reason,
		); err != nil {
			return fmt.Errorf("persist directory lookup: insert review candidate: %w", err)
		}
		var storedRank int
		var storedConfidence *float64
		var storedReason string
		if err := transaction.QueryRow(ctx, `
			SELECT rank, confidence, reason
			FROM stacks.resolution_candidates
			WHERE proposal_id = $1
			  AND directory_profile_snapshot_id = $2`,
			proposalID,
			candidate.ID,
		).Scan(&storedRank, &storedConfidence, &storedReason); err != nil {
			return fmt.Errorf("persist directory lookup: load review candidate: %w", err)
		}
		if storedRank != rank ||
			storedConfidence == nil ||
			*storedConfidence != confidence ||
			storedReason != reason {
			return fmt.Errorf("persist directory lookup: stored review candidate conflicts")
		}
	}
	return nil
}

func findStoredDirectoryProfile(
	profile entity.DirectoryProfile,
	profiles []storedDirectoryProfile,
) (storedDirectoryProfile, error) {
	digest, err := directoryProfileDigest(profile)
	if err != nil {
		return storedDirectoryProfile{}, err
	}
	for _, stored := range profiles {
		if stored.Digest == digest {
			return stored, nil
		}
	}
	return storedDirectoryProfile{}, fmt.Errorf("persist directory lookup: evaluation profile is not in lookup results")
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func directoryDatabaseTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC().Truncate(time.Microsecond)
}

func sameDirectoryTimePointer(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func directoryProfileDigest(profile entity.DirectoryProfile) ([sha256.Size]byte, error) {
	canonical, err := canonicalDirectoryProfile(profile)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	fields := []string{
		directoryProfileDigestVersion,
		canonical.Provider,
		canonical.SubjectID,
		string(canonical.Source),
		canonical.DisplayName,
		nullableDirectoryTime(canonical.ObservedAt),
	}
	for _, email := range canonical.Emails {
		fields = append(fields, email.Value)
		if email.Primary {
			fields = append(fields, "primary")
		} else {
			fields = append(fields, "alternate")
		}
	}
	return stableDirectoryDigest(fields...), nil
}

func directoryQueryDigest(query entity.DirectoryQuery) ([sha256.Size]byte, error) {
	if err := validateDirectoryQuery(query); err != nil {
		return [sha256.Size]byte{}, err
	}
	return stableDirectoryDigest(
		directoryQueryDigestVersion,
		string(query.Kind),
		entity.NormalizeName(query.Name),
		entity.NormalizeEmail(query.Email),
		string(query.EmailEvidence),
	), nil
}

func directoryLookupDigest(input directory.PersistInput, policyVersion string) ([sha256.Size]byte, error) {
	if err := validateDirectoryPersistInput(input); err != nil {
		return [sha256.Size]byte{}, err
	}
	if strings.TrimSpace(policyVersion) == "" {
		return [sha256.Size]byte{}, fmt.Errorf("compute directory lookup digest: policy version is required")
	}
	queryDigest, err := directoryQueryDigest(input.Query)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	profileDigests := make([]string, 0, len(input.Lookup.Profiles))
	for _, profile := range input.Lookup.Profiles {
		digest, err := directoryProfileDigest(profile)
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		profileDigests = append(profileDigests, string(digest[:]))
	}
	sort.Strings(profileDigests)
	fields := []string{
		directoryLookupDigestVersion,
		input.Mention.MentionID,
		googlePeopleDirectoryProvider,
		string(queryDigest[:]),
		policyVersion,
		string(input.Lookup.Outcome),
		fmt.Sprintf("%d", input.AttemptCount),
		input.RecordedAt.UTC().Format(time.RFC3339Nano),
		nullableDirectoryTimePointer(input.RetryAfter),
	}
	fields = append(fields, profileDigests...)
	return stableDirectoryDigest(fields...), nil
}

func validateDirectoryPersistInput(input directory.PersistInput) error {
	if strings.TrimSpace(input.Mention.MentionID) == "" ||
		strings.TrimSpace(input.Mention.ProposalID) == "" {
		return fmt.Errorf("persist directory lookup: mention and proposal IDs are required")
	}
	if err := validateDirectoryQuery(input.Query); err != nil {
		return err
	}
	if !boundedDirectoryOutcome(input.Lookup.Outcome) ||
		(input.Evaluation.Outcome != "" && !boundedDirectoryOutcome(input.Evaluation.Outcome)) ||
		(input.Evaluation.Outcome == "" && !expectedDirectoryFailure(input.Lookup.Outcome)) {
		return fmt.Errorf("persist directory lookup: outcome is invalid")
	}
	if input.AttemptCount < 0 {
		return fmt.Errorf("persist directory lookup: attempt count is invalid")
	}
	if input.RecordedAt.IsZero() {
		return fmt.Errorf("persist directory lookup: recorded time is required")
	}
	if input.RetryAfter != nil && input.RetryAfter.IsZero() {
		return fmt.Errorf("persist directory lookup: retry time is invalid")
	}
	if expectedDirectoryFailure(input.Lookup.Outcome) && len(input.Lookup.Profiles) != 0 {
		return fmt.Errorf("persist directory lookup: bounded provider failure includes profiles")
	}
	for _, profile := range input.Lookup.Profiles {
		if _, err := canonicalDirectoryProfile(profile); err != nil {
			return err
		}
	}
	if input.Evaluation.Outcome == entity.DirectoryMatched {
		if input.Evaluation.Profile == nil ||
			!entity.ValidEmail(entity.NormalizeEmail(input.Evaluation.AcceptedEmail)) {
			return fmt.Errorf("persist directory lookup: matched evaluation is incomplete")
		}
		if input.Lookup.Outcome != entity.DirectoryMatched ||
			input.Query.Kind != entity.DirectoryQueryEmail ||
			entity.NormalizeEmail(input.Query.Email) != entity.NormalizeEmail(input.Evaluation.AcceptedEmail) ||
			(input.Evaluation.CreatePerson && input.Evaluation.EntityID != "") ||
			(!input.Evaluation.CreatePerson && input.Evaluation.EntityID == "") {
			return fmt.Errorf("persist directory lookup: matched evaluation is inconsistent")
		}
		canonicalEvaluationProfile, err := canonicalDirectoryProfile(*input.Evaluation.Profile)
		if err != nil {
			return err
		}
		if canonicalEvaluationProfile.Source != entity.DirectorySourceDomainProfile ||
			!directoryProfileHasEmail(
				canonicalEvaluationProfile,
				entity.NormalizeEmail(input.Evaluation.AcceptedEmail),
			) {
			return fmt.Errorf("persist directory lookup: matched evaluation is not an exact domain profile")
		}
		evaluationDigest, err := directoryProfileDigest(canonicalEvaluationProfile)
		if err != nil {
			return err
		}
		found := false
		for _, profile := range input.Lookup.Profiles {
			profileDigest, err := directoryProfileDigest(profile)
			if err != nil {
				return err
			}
			if profileDigest == evaluationDigest {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("persist directory lookup: matched evaluation profile is not in lookup results")
		}
	}
	return nil
}

func validateDirectoryQuery(query entity.DirectoryQuery) error {
	switch query.Kind {
	case entity.DirectoryQueryEmail:
		if !entity.ValidEmail(entity.NormalizeEmail(query.Email)) ||
			entity.NormalizeName(query.Name) != "" {
			return fmt.Errorf("persist directory lookup: email query is invalid")
		}
	case entity.DirectoryQueryName:
		if entity.NormalizeName(query.Name) == "" ||
			entity.NormalizeEmail(query.Email) != "" {
			return fmt.Errorf("persist directory lookup: name query is invalid")
		}
	default:
		return fmt.Errorf("persist directory lookup: query kind is invalid")
	}
	switch query.EmailEvidence {
	case entity.EmailEvidenceSourceBound,
		entity.EmailEvidenceCitationVerified,
		entity.EmailEvidenceReviewerSupplied,
		entity.EmailEvidenceNone:
	default:
		return fmt.Errorf("persist directory lookup: email evidence is invalid")
	}
	return nil
}

func canonicalDirectoryProfile(profile entity.DirectoryProfile) (entity.DirectoryProfile, error) {
	profile.Provider = strings.TrimSpace(profile.Provider)
	profile.SubjectID = strings.TrimSpace(profile.SubjectID)
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	if profile.Provider != googlePeopleDirectoryProvider ||
		profile.SubjectID == "" ||
		profile.DisplayName == "" {
		return entity.DirectoryProfile{}, fmt.Errorf("persist directory lookup: profile identity is invalid")
	}
	switch profile.Source {
	case entity.DirectorySourceDomainProfile, entity.DirectorySourceDomainContact:
	default:
		return entity.DirectoryProfile{}, fmt.Errorf("persist directory lookup: profile source is invalid")
	}
	emailsByValue := make(map[string]entity.DirectoryEmail, len(profile.Emails))
	for _, email := range profile.Emails {
		value := entity.NormalizeEmail(email.Value)
		if !entity.ValidEmail(value) {
			return entity.DirectoryProfile{}, fmt.Errorf("persist directory lookup: profile email is invalid")
		}
		canonical := emailsByValue[value]
		canonical.Value = value
		canonical.Primary = canonical.Primary || email.Primary
		emailsByValue[value] = canonical
	}
	if len(emailsByValue) == 0 {
		return entity.DirectoryProfile{}, fmt.Errorf("persist directory lookup: profile emails are required")
	}
	profile.Emails = make([]entity.DirectoryEmail, 0, len(emailsByValue))
	for _, email := range emailsByValue {
		profile.Emails = append(profile.Emails, email)
	}
	sort.Slice(profile.Emails, func(left, right int) bool {
		return profile.Emails[left].Value < profile.Emails[right].Value
	})
	if !profile.ObservedAt.IsZero() {
		profile.ObservedAt = directoryDatabaseTime(profile.ObservedAt)
	}
	return profile, nil
}

func directoryProfileHasEmail(profile entity.DirectoryProfile, email string) bool {
	for _, profileEmail := range profile.Emails {
		if profileEmail.Value == email {
			return true
		}
	}
	return false
}

func boundedDirectoryOutcome(outcome entity.DirectoryOutcome) bool {
	switch outcome {
	case entity.DirectoryMatched,
		entity.DirectoryNoMatch,
		entity.DirectoryAmbiguous,
		entity.DirectoryReview,
		entity.DirectoryDisabled,
		entity.DirectoryNotConfigured,
		entity.DirectoryUnauthorized,
		entity.DirectoryForbidden,
		entity.DirectoryRateLimited,
		entity.DirectoryUnavailable,
		entity.DirectoryInvalidResponse,
		entity.DirectoryResultLimitExceeded:
		return true
	default:
		return false
	}
}

func expectedDirectoryFailure(outcome entity.DirectoryOutcome) bool {
	switch outcome {
	case entity.DirectoryDisabled,
		entity.DirectoryNotConfigured,
		entity.DirectoryUnauthorized,
		entity.DirectoryForbidden,
		entity.DirectoryRateLimited,
		entity.DirectoryUnavailable,
		entity.DirectoryInvalidResponse,
		entity.DirectoryResultLimitExceeded:
		return true
	default:
		return false
	}
}

func stableDirectoryDigest(fields ...string) [sha256.Size]byte {
	digest := sha256.New()
	var length [8]byte
	for _, field := range fields {
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(field))
	}
	var sum [sha256.Size]byte
	copy(sum[:], digest.Sum(nil))
	return sum
}

func nullableDirectoryTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableDirectoryTimePointer(value *time.Time) string {
	if value == nil {
		return ""
	}
	return nullableDirectoryTime(*value)
}
