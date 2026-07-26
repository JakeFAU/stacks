package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/timepoint"
	"github.com/jackc/pgx/v5"
)

const (
	DirectoryQueryEmail = "email"
	DirectoryQueryName  = "name"

	DirectoryEmailEvidenceSourceBound      = "source_bound"
	DirectoryEmailEvidenceCitationVerified = "citation_verified"
	DirectoryEmailEvidenceReviewerSupplied = "reviewer_supplied"
	DirectoryEmailEvidenceNone             = "none"

	DirectorySourceDomainProfile = "domain_profile"
	DirectorySourceDomainContact = "domain_contact"

	DirectoryOutcomeMatched             = "matched"
	DirectoryOutcomeNoMatch             = "no_match"
	DirectoryOutcomeAmbiguous           = "ambiguous"
	DirectoryOutcomeReview              = "review"
	DirectoryOutcomeDisabled            = "disabled"
	DirectoryOutcomeNotConfigured       = "not_configured"
	DirectoryOutcomeUnauthorized        = "unauthorized"
	DirectoryOutcomeForbidden           = "forbidden"
	DirectoryOutcomeRateLimited         = "rate_limited"
	DirectoryOutcomeUnavailable         = "unavailable"
	DirectoryOutcomeInvalidResponse     = "invalid_response"
	DirectoryOutcomeResultLimitExceeded = "result_limit_exceeded"

	directoryPolicyVersion       = "directory-identity-v1"
	directoryProfileDigest       = "stacks.directory-profile.v2.canonical"
	directoryQueryDigest         = "stacks.directory-query.v2.canonical"
	directoryAttemptDigest       = "stacks.directory-attempt.v3.lookup-provider"
	directoryEntityIDVersion     = "stacks.directory-entity.v1"
	directoryCandidateIDVersion  = "stacks.directory-candidate.v1"
	directoryDecisionIDVersion   = "stacks.directory-decision.v1"
	directoryAliasIDVersion      = "stacks.directory-alias-assertion.v1"
	directoryLinkIDVersion       = "stacks.directory-link.v1"
	directoryAuthorityNamespace  = "github.com/JakeFAU/stacks/postgres-directory-authority/v1"
	directoryCandidateSourceKind = "directory"
	directoryExactEmailReason    = "unique_exact_work_email"
	directoryEmailReviewReason   = "directory_exact_email_review"
	directoryNameReviewReason    = "directory_name_review"

	directoryLookupProviderMaximumRunes = 128
)

// DirectoryPendingMention is private source-grounded work without provider
// SDK or application package types.
type DirectoryPendingMention struct {
	MentionID      string
	ProposalID     string
	Surface        string
	NormalizedName string
	ProposedEmail  string
	NameQuote      string
	EmailQuote     string
}

// DirectoryWorkRequest identifies one completed canonical derivation and its
// bounded reuse windows.
type DirectoryWorkRequest struct {
	DerivationID string
	Now          time.Time
	Freshness    time.Duration
	RetryAfter   time.Duration
}

// DirectoryWorkset contains pending mentions and fresh conclusive attempts.
type DirectoryWorkset struct {
	Mentions []DirectoryPendingMention
	Reused   int
}

// DirectoryIdentityLink is current provider-subject authority after reviewer
// supersession is applied.
type DirectoryIdentityLink struct {
	Provider  string
	SubjectID string
	EntityID  string
}

// DirectoryIdentityState contains canonical effective aliases and directory
// links used by deterministic root policy.
type DirectoryIdentityState struct {
	Snapshots []identity.EntitySnapshot
	Links     []DirectoryIdentityLink
}

// DirectoryQuery is a normalized disclosure-safe query description.
type DirectoryQuery struct {
	Kind          string
	Name          string
	Email         string
	EmailEvidence string
}

// DirectoryEmail is one normalized work email in a bounded snapshot.
type DirectoryEmail struct {
	Value   string
	Primary bool
}

// DirectoryProfile is an adapter-neutral provider snapshot.
type DirectoryProfile struct {
	Provider    string
	SubjectID   string
	Source      string
	DisplayName string
	Emails      []DirectoryEmail
	ObservedAt  time.Time
}

// DirectoryLookupResult is one bounded provider outcome.
type DirectoryLookupResult struct {
	Provider   string
	Outcome    string
	Profiles   []DirectoryProfile
	RetryAfter time.Duration
}

// DirectoryEvaluation is the deterministic root policy result.
type DirectoryEvaluation struct {
	Outcome       string
	EntityID      string
	CreatePerson  bool
	AcceptedEmail string
	Profile       *DirectoryProfile
	Candidates    []DirectoryProfile
}

// DirectoryPersistInput is the complete raw adapter write boundary.
type DirectoryPersistInput struct {
	Mention      DirectoryPendingMention
	Query        DirectoryQuery
	Lookup       DirectoryLookupResult
	Evaluation   DirectoryEvaluation
	AttemptCount int
	RecordedAt   time.Time
	RetryAfter   *time.Time
}

// DirectoryPersistResult reports only admitted automatic identity authority.
type DirectoryPersistResult struct {
	AutoResolved bool
	EntityID     string
}

// DirectoryStore owns optional directory persistence over a caller-owned
// canonical database. It has no provider or root application dependency.
type DirectoryStore struct {
	Database *Database
}

type storedDirectoryProfile struct {
	profileID  string
	snapshotID string
	profile    DirectoryProfile
	digest     [sha256.Size]byte
}

// LoadWork returns current admitted pending mentions not covered by a fresh
// conclusive result or active retry window.
func (store DirectoryStore) LoadWork(
	ctx context.Context,
	request DirectoryWorkRequest,
) (DirectoryWorkset, error) {
	if err := contextRequired(ctx, "load directory work"); err != nil {
		return DirectoryWorkset{}, err
	}
	if strings.TrimSpace(request.DerivationID) == "" ||
		!timepoint.IsCanonical(request.Now) ||
		request.Now.IsZero() ||
		request.Freshness <= 0 ||
		request.RetryAfter <= 0 {
		return DirectoryWorkset{}, fmt.Errorf("load directory work: input is invalid or time is not canonical")
	}
	if store.Database == nil || store.Database.pool == nil {
		return DirectoryWorkset{}, fmt.Errorf("load directory work: database is not configured")
	}

	rows, err := store.Database.pool.Query(ctx, `
		WITH eligible AS (
			SELECT
				mention.id AS mention_id,
				proposal.id AS proposal_id,
				mention.surface,
				mention.normalized_name,
				mention.proposed_email,
				name_evidence.quote AS name_quote,
				COALESCE(email_evidence.quote, '') AS email_quote
			FROM stacks_core.mentions AS mention
			JOIN stacks_core.resolution_proposals AS proposal
			  ON proposal.mention_id = mention.id
			JOIN stacks_core.evidence_spans AS name_evidence
			  ON name_evidence.id = mention.evidence_id
			LEFT JOIN stacks_core.evidence_spans AS email_evidence
			  ON email_evidence.id = mention.proposed_email_evidence_id
			JOIN stacks_core.extraction_runs AS run
			  ON run.id = mention.derivation_run_id
			JOIN stacks_core.document_versions AS version
			  ON version.id = run.document_version_id
			JOIN stacks_core.source_documents AS source
			  ON source.id = version.source_document_id
			WHERE run.id = $1
			  AND run.state = 'completed'
			  AND source.current_version_id = version.id
			  AND EXISTS (
				SELECT 1
				FROM stacks_core.admission_decisions AS decision
				WHERE decision.target_kind = 'extraction_run'
				  AND decision.target_id = run.id
				  AND decision.outcome = 'admitted'
				  AND NOT EXISTS (
					SELECT 1
					FROM stacks_core.admission_decisions AS successor
					WHERE successor.supersedes_id = decision.id
				  )
			  )
			  AND EXISTS (
				SELECT 1
				FROM stacks_core.admission_decisions AS decision
				WHERE decision.target_kind = 'mention'
				  AND decision.target_id = mention.id
				  AND decision.outcome = 'admitted'
				  AND NOT EXISTS (
					SELECT 1
					FROM stacks_core.admission_decisions AS successor
					WHERE successor.supersedes_id = decision.id
				  )
			  )
			  AND NOT EXISTS (
				SELECT 1
				FROM stacks_core.resolution_decisions AS decision
				WHERE decision.proposal_id = proposal.id
				  AND NOT EXISTS (
					SELECT 1
					FROM stacks_core.resolution_decisions AS successor
					WHERE successor.supersedes_id = decision.id
				  )
			  )
		)
		SELECT
			eligible.mention_id,
			eligible.proposal_id,
			eligible.surface,
			eligible.normalized_name,
			eligible.proposed_email,
			eligible.name_quote,
			eligible.email_quote,
			EXISTS (
				SELECT 1
				FROM stacks_directory.lookup_attempts AS attempt
				WHERE attempt.mention_id = eligible.mention_id
				  AND attempt.outcome IN ('matched', 'no_match', 'ambiguous', 'review')
				  AND attempt.recorded_at >=
					$2::timestamptz - ($3::bigint * INTERVAL '1 microsecond')
			) AS reused,
			EXISTS (
				SELECT 1
				FROM stacks_directory.lookup_attempts AS attempt
				WHERE attempt.mention_id = eligible.mention_id
				  AND attempt.outcome IN ('rate_limited', 'unavailable')
				  AND COALESCE(
					attempt.retry_after,
					attempt.recorded_at + ($4::bigint * INTERVAL '1 microsecond')
				  ) > $2::timestamptz
			) AS retry_blocked
		FROM eligible
		ORDER BY eligible.mention_id`,
		request.DerivationID,
		request.Now,
		request.Freshness.Microseconds(),
		request.RetryAfter.Microseconds(),
	)
	if err != nil {
		return DirectoryWorkset{}, wrapDirectoryError(ctx, "load directory work", err)
	}
	defer rows.Close()
	var work DirectoryWorkset
	for rows.Next() {
		var mention DirectoryPendingMention
		var reused, retryBlocked bool
		if err := rows.Scan(
			&mention.MentionID,
			&mention.ProposalID,
			&mention.Surface,
			&mention.NormalizedName,
			&mention.ProposedEmail,
			&mention.NameQuote,
			&mention.EmailQuote,
			&reused,
			&retryBlocked,
		); err != nil {
			return DirectoryWorkset{}, wrapDirectoryError(ctx, "scan directory work", err)
		}
		if reused {
			work.Reused++
			continue
		}
		if !retryBlocked {
			work.Mentions = append(work.Mentions, mention)
		}
	}
	if err := rows.Err(); err != nil {
		return DirectoryWorkset{}, wrapDirectoryError(ctx, "iterate directory work", err)
	}
	return work, nil
}

// LoadIdentityState returns only effective accepted alias and reviewer-aware
// directory authority.
func (store DirectoryStore) LoadIdentityState(
	ctx context.Context,
) (DirectoryIdentityState, error) {
	if err := contextRequired(ctx, "load directory identity state"); err != nil {
		return DirectoryIdentityState{}, err
	}
	if store.Database == nil || store.Database.pool == nil {
		return DirectoryIdentityState{}, fmt.Errorf("load directory identity state: database is not configured")
	}
	snapshots, err := store.Database.EntitySnapshots(ctx)
	if err != nil {
		return DirectoryIdentityState{}, fmt.Errorf("load directory identity state: %w", err)
	}
	rows, err := store.Database.pool.Query(ctx, `
		SELECT DISTINCT
			profile.provider,
			profile.provider_subject_id,
			current.entity_id
		FROM stacks_directory.entity_links AS link
		JOIN stacks_directory.profiles AS profile
		  ON profile.id = link.profile_id
		JOIN stacks_core.resolution_decisions AS current
		  ON current.proposal_id = link.proposal_id
		 AND current.entity_id = link.entity_id
		WHERE current.outcome = 'accepted'
		  AND NOT EXISTS (
			SELECT 1
			FROM stacks_core.resolution_decisions AS successor
			WHERE successor.supersedes_id = current.id
		  )
		ORDER BY profile.provider, profile.provider_subject_id, current.entity_id`)
	if err != nil {
		return DirectoryIdentityState{}, wrapDirectoryError(ctx, "load directory identity links", err)
	}
	defer rows.Close()
	state := DirectoryIdentityState{Snapshots: snapshots}
	for rows.Next() {
		var link DirectoryIdentityLink
		if err := rows.Scan(&link.Provider, &link.SubjectID, &link.EntityID); err != nil {
			return DirectoryIdentityState{}, wrapDirectoryError(ctx, "scan directory identity link", err)
		}
		state.Links = append(state.Links, link)
	}
	if err := rows.Err(); err != nil {
		return DirectoryIdentityState{}, wrapDirectoryError(ctx, "iterate directory identity links", err)
	}
	return state, nil
}

// Persist atomically stores bounded directory evidence and any eligible core
// candidate, automatic decision, and directory link.
func (store DirectoryStore) Persist(
	ctx context.Context,
	input DirectoryPersistInput,
) (DirectoryPersistResult, error) {
	if err := contextRequired(ctx, "persist directory lookup"); err != nil {
		return DirectoryPersistResult{}, err
	}
	if err := validateDirectoryPersistInput(input); err != nil {
		return DirectoryPersistResult{}, err
	}
	if store.Database == nil || store.Database.pool == nil {
		return DirectoryPersistResult{}, fmt.Errorf("persist directory lookup: database is not configured")
	}

	var result DirectoryPersistResult
	err := store.Database.InTransaction(ctx, func(transaction *Transaction) error {
		if err := lockDirectoryProposal(ctx, transaction, input.Mention); err != nil {
			return err
		}
		if err := lockDirectoryAuthorities(ctx, transaction, input); err != nil {
			return err
		}
		profiles, err := persistDirectoryProfiles(ctx, transaction, input)
		if err != nil {
			return err
		}
		attemptID, err := persistDirectoryAttempt(ctx, transaction, input, profiles)
		if err != nil {
			return err
		}

		switch {
		case input.Evaluation.Outcome == DirectoryOutcomeMatched:
			result, err = persistAutomaticDirectoryAuthority(
				ctx,
				transaction,
				input,
				attemptID,
				profiles,
			)
		case len(input.Evaluation.Candidates) > 0:
			err = persistDirectoryReviewCandidates(
				ctx,
				transaction,
				input,
				attemptID,
				profiles,
				input.Evaluation.Candidates,
			)
		}
		return err
	})
	if err != nil {
		return DirectoryPersistResult{}, wrapDirectoryError(ctx, "persist directory lookup", err)
	}
	return result, nil
}

func lockDirectoryProposal(
	ctx context.Context,
	transaction *Transaction,
	mention DirectoryPendingMention,
) error {
	if _, err := transaction.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		identityAuthorityLockNamespace,
		mention.ProposalID,
	); err != nil {
		return fmt.Errorf("lock directory proposal authority: %w", err)
	}
	var storedMentionID string
	if err := transaction.QueryRow(ctx, `
		SELECT mention_id
		FROM stacks_core.resolution_proposals
		WHERE id = $1`,
		mention.ProposalID,
	).Scan(&storedMentionID); err != nil {
		return fmt.Errorf("lock directory proposal: %w", err)
	}
	if storedMentionID != mention.MentionID {
		return fmt.Errorf("directory proposal does not belong to mention: %w", ErrConflict)
	}
	return nil
}

func lockDirectoryAuthorities(
	ctx context.Context,
	transaction *Transaction,
	input DirectoryPersistInput,
) error {
	type authority struct{ namespace, key string }
	var authorities []authority
	if input.Query.Kind == DirectoryQueryEmail {
		authorities = append(authorities, authority{
			namespace: directoryAuthorityNamespace + "/email",
			key:       normalizeDirectoryEmail(input.Query.Email),
		})
	}
	for _, profile := range input.Lookup.Profiles {
		authorities = append(authorities, authority{
			namespace: directoryAuthorityNamespace + "/" + strings.TrimSpace(profile.Provider),
			key:       strings.TrimSpace(profile.SubjectID),
		})
	}
	sort.Slice(authorities, func(left, right int) bool {
		if authorities[left].namespace == authorities[right].namespace {
			return authorities[left].key < authorities[right].key
		}
		return authorities[left].namespace < authorities[right].namespace
	})
	for index, value := range authorities {
		if index > 0 && value == authorities[index-1] {
			continue
		}
		if _, err := transaction.Exec(ctx, `
			SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
			value.namespace,
			value.key,
		); err != nil {
			return fmt.Errorf("lock directory identity authority: %w", err)
		}
	}
	return nil
}

func persistDirectoryProfiles(
	ctx context.Context,
	transaction *Transaction,
	input DirectoryPersistInput,
) ([]storedDirectoryProfile, error) {
	byDigest := make(map[[sha256.Size]byte]storedDirectoryProfile)
	for _, raw := range input.Lookup.Profiles {
		profile := canonicalDirectoryProfile(raw)
		digest := digestDirectoryProfile(profile)
		profileID := directoryOpaqueID(
			directoryEntityIDVersion+"/profile",
			profile.Provider,
			profile.SubjectID,
		)
		snapshotID := directoryOpaqueID(
			directoryProfileDigest+"/snapshot",
			fmt.Sprintf("%x", digest),
		)
		byDigest[digest] = storedDirectoryProfile{
			profileID: profileID, snapshotID: snapshotID, profile: profile, digest: digest,
		}
	}
	profiles := make([]storedDirectoryProfile, 0, len(byDigest))
	for _, profile := range byDigest {
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(left, right int) bool {
		return bytes.Compare(profiles[left].digest[:], profiles[right].digest[:]) < 0
	})
	for _, profile := range profiles {
		if err := persistDirectoryProfile(ctx, transaction, profile, input.RecordedAt); err != nil {
			return nil, err
		}
	}
	return profiles, nil
}

func persistDirectoryProfile(
	ctx context.Context,
	transaction *Transaction,
	profile storedDirectoryProfile,
	recordedAt time.Time,
) error {
	if _, err := transaction.Exec(ctx, `
		INSERT INTO stacks_directory.profiles (
			id, provider, provider_subject_id, recorded_at
		)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO NOTHING`,
		profile.profileID,
		profile.profile.Provider,
		profile.profile.SubjectID,
		recordedAt,
	); err != nil {
		return fmt.Errorf("insert directory profile: %w", err)
	}
	var storedProvider, storedSubject string
	if err := transaction.QueryRow(ctx, `
		SELECT provider, provider_subject_id
		FROM stacks_directory.profiles
		WHERE id = $1`,
		profile.profileID,
	).Scan(&storedProvider, &storedSubject); err != nil {
		return fmt.Errorf("load directory profile: %w", err)
	}
	if storedProvider != profile.profile.Provider || storedSubject != profile.profile.SubjectID {
		return fmt.Errorf("stored directory profile conflicts: %w", ErrConflict)
	}

	var observedAt any
	if !profile.profile.ObservedAt.IsZero() {
		observedAt = profile.profile.ObservedAt
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO stacks_directory.snapshots (
			id, profile_id, source_type, display_name, observed_at,
			recorded_at, digest
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO NOTHING`,
		profile.snapshotID,
		profile.profileID,
		profile.profile.Source,
		profile.profile.DisplayName,
		observedAt,
		recordedAt,
		profile.digest[:],
	); err != nil {
		return fmt.Errorf("insert directory snapshot: %w", err)
	}
	var (
		storedProfileID, storedSource, storedDisplayName string
		storedObservedAt                                 *time.Time
		storedDigest                                     []byte
	)
	if err := transaction.QueryRow(ctx, `
		SELECT profile_id, source_type, display_name, observed_at, digest
		FROM stacks_directory.snapshots
		WHERE id = $1`,
		profile.snapshotID,
	).Scan(
		&storedProfileID,
		&storedSource,
		&storedDisplayName,
		&storedObservedAt,
		&storedDigest,
	); err != nil {
		return fmt.Errorf("load directory snapshot: %w", err)
	}
	if storedProfileID != profile.profileID ||
		storedSource != profile.profile.Source ||
		storedDisplayName != profile.profile.DisplayName ||
		!sameDirectoryTime(storedObservedAt, profile.profile.ObservedAt) ||
		!bytes.Equal(storedDigest, profile.digest[:]) {
		return fmt.Errorf("stored directory snapshot conflicts: %w", ErrConflict)
	}

	for index, email := range profile.profile.Emails {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO stacks_directory.profile_emails (
				snapshot_id, normalized_email, is_primary, email_order
			)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (snapshot_id, normalized_email) DO NOTHING`,
			profile.snapshotID,
			email.Value,
			email.Primary,
			index,
		); err != nil {
			return fmt.Errorf("insert directory profile email: %w", err)
		}
	}
	rows, err := transaction.Query(ctx, `
		SELECT normalized_email, is_primary, email_order
		FROM stacks_directory.profile_emails
		WHERE snapshot_id = $1
		ORDER BY email_order`,
		profile.snapshotID,
	)
	if err != nil {
		return fmt.Errorf("load directory profile emails: %w", err)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		if index >= len(profile.profile.Emails) {
			return fmt.Errorf("stored directory profile emails conflict: %w", ErrConflict)
		}
		var value string
		var primary bool
		var order int
		if err := rows.Scan(&value, &primary, &order); err != nil {
			return fmt.Errorf("scan directory profile email: %w", err)
		}
		want := profile.profile.Emails[index]
		if value != want.Value || primary != want.Primary || order != index {
			return fmt.Errorf("stored directory profile emails conflict: %w", ErrConflict)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate directory profile emails: %w", err)
	}
	if index != len(profile.profile.Emails) {
		return fmt.Errorf("stored directory profile emails conflict: %w", ErrConflict)
	}
	return nil
}

func persistDirectoryAttempt(
	ctx context.Context,
	transaction *Transaction,
	input DirectoryPersistInput,
	profiles []storedDirectoryProfile,
) (string, error) {
	queryDigest := digestDirectoryQuery(input.Query)
	snapshotIDs := make([]string, len(profiles))
	for index, profile := range profiles {
		snapshotIDs[index] = profile.snapshotID
	}
	sort.Strings(snapshotIDs)
	attemptDigest := digestDirectoryAttempt(input, queryDigest, snapshotIDs)
	attemptID := directoryOpaqueID(directoryAttemptDigest+"/id", fmt.Sprintf("%x", attemptDigest))
	if _, err := transaction.Exec(ctx, `
		INSERT INTO stacks_directory.lookup_attempts (
			id, mention_id, proposal_id, provider, query_kind, email_evidence,
			query_digest, policy_version, outcome, attempt_count, retry_after,
			recorded_at, snapshot_ids, digest
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
		)
		ON CONFLICT (id) DO NOTHING`,
		attemptID,
		input.Mention.MentionID,
		input.Mention.ProposalID,
		normalizeDirectoryProvider(input.Lookup.Provider),
		input.Query.Kind,
		input.Query.EmailEvidence,
		queryDigest[:],
		directoryPolicyVersion,
		input.Lookup.Outcome,
		input.AttemptCount,
		input.RetryAfter,
		input.RecordedAt,
		snapshotIDs,
		attemptDigest[:],
	); err != nil {
		return "", fmt.Errorf("insert directory lookup attempt: %w", err)
	}
	var (
		storedMention, storedProposal, storedProvider, storedOutcome string
		storedRecordedAt                                             time.Time
		storedRetryAfter                                             *time.Time
		storedSnapshots                                              []string
		storedDigest                                                 []byte
	)
	if err := transaction.QueryRow(ctx, `
		SELECT
			mention_id, proposal_id, provider, outcome, recorded_at, retry_after,
			snapshot_ids, digest
		FROM stacks_directory.lookup_attempts
		WHERE id = $1`,
		attemptID,
	).Scan(
		&storedMention,
		&storedProposal,
		&storedProvider,
		&storedOutcome,
		&storedRecordedAt,
		&storedRetryAfter,
		&storedSnapshots,
		&storedDigest,
	); err != nil {
		return "", fmt.Errorf("load directory lookup attempt: %w", err)
	}
	if storedMention != input.Mention.MentionID ||
		storedProposal != input.Mention.ProposalID ||
		storedProvider != normalizeDirectoryProvider(input.Lookup.Provider) ||
		storedOutcome != input.Lookup.Outcome ||
		!storedRecordedAt.Equal(input.RecordedAt) ||
		!sameDirectoryTimePointer(storedRetryAfter, input.RetryAfter) ||
		!equalDirectoryStrings(storedSnapshots, snapshotIDs) ||
		!bytes.Equal(storedDigest, attemptDigest[:]) {
		return "", fmt.Errorf("stored directory lookup attempt conflicts: %w", ErrConflict)
	}
	return attemptID, nil
}

func persistAutomaticDirectoryAuthority(
	ctx context.Context,
	transaction *Transaction,
	input DirectoryPersistInput,
	attemptID string,
	profiles []storedDirectoryProfile,
) (DirectoryPersistResult, error) {
	profile, err := findDirectoryProfile(*input.Evaluation.Profile, profiles)
	if err != nil {
		return DirectoryPersistResult{}, err
	}
	current, currentErr := loadEffectiveDirectoryDecision(
		ctx,
		transaction,
		input.Mention.ProposalID,
	)
	if currentErr != nil && !errors.Is(currentErr, pgx.ErrNoRows) {
		return DirectoryPersistResult{}, fmt.Errorf("load current directory proposal authority: %w", currentErr)
	}
	if currentErr == nil && current.Authority() == identity.AuthorityReviewer {
		if err := persistDirectoryReviewCandidates(
			ctx,
			transaction,
			input,
			attemptID,
			profiles,
			[]DirectoryProfile{profile.profile},
		); err != nil {
			return DirectoryPersistResult{}, err
		}
		return DirectoryPersistResult{}, nil
	}
	if currentErr == nil && current.Authority() == identity.AuthorityAutomatic {
		var exactLink bool
		if err := transaction.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM stacks_directory.entity_links
				WHERE proposal_id = $1
				  AND snapshot_id = $2
				  AND decision_id = $3
				  AND entity_id = $4
			)`,
			input.Mention.ProposalID,
			profile.snapshotID,
			current.ID(),
			current.EntityID(),
		).Scan(&exactLink); err != nil {
			return DirectoryPersistResult{}, fmt.Errorf("load exact directory authority link: %w", err)
		}
		if exactLink {
			return DirectoryPersistResult{
				AutoResolved: true,
				EntityID:     string(current.EntityID()),
			}, nil
		}
		if err := persistDirectoryReviewCandidates(
			ctx,
			transaction,
			input,
			attemptID,
			profiles,
			[]DirectoryProfile{profile.profile},
		); err != nil {
			return DirectoryPersistResult{}, err
		}
		return DirectoryPersistResult{}, nil
	}

	email := normalizeDirectoryEmail(input.Evaluation.AcceptedEmail)
	emailOwners, err := loadDirectoryEmailOwners(ctx, transaction, email)
	if err != nil {
		return DirectoryPersistResult{}, err
	}
	providerOwners, err := loadDirectoryProviderOwners(
		ctx,
		transaction,
		profile.profile.Provider,
		profile.profile.SubjectID,
	)
	if err != nil {
		return DirectoryPersistResult{}, err
	}
	entityID, conflict := chooseDirectoryAuthorityEntity(
		input.Evaluation,
		profile,
		emailOwners,
		providerOwners,
	)
	if conflict {
		if err := persistDirectoryReviewCandidates(
			ctx,
			transaction,
			input,
			attemptID,
			profiles,
			[]DirectoryProfile{profile.profile},
		); err != nil {
			return DirectoryPersistResult{}, err
		}
		return DirectoryPersistResult{}, nil
	}
	if err := ensureDirectoryEntity(
		ctx,
		transaction,
		entityID,
		profile.profile.DisplayName,
		input.RecordedAt,
	); err != nil {
		return DirectoryPersistResult{}, err
	}
	candidate, err := ensureDirectoryCandidate(
		ctx,
		transaction,
		input.Mention.ProposalID,
		entityID,
		profile.snapshotID,
		directoryExactEmailReason,
		input.RecordedAt,
	)
	if err != nil {
		return DirectoryPersistResult{}, err
	}
	decisionID := directoryOpaqueID(
		directoryDecisionIDVersion,
		input.Mention.ProposalID,
		entityID,
	)
	decision, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
		ID:         identity.DecisionID(decisionID),
		ProposalID: identity.ProposalID(input.Mention.ProposalID),
		Outcome:    identity.DecisionAccepted,
		EntityID:   identity.EntityID(entityID),
		Authority:  identity.AuthorityAutomatic,
		ReasonCode: directoryExactEmailReason,
		RecordedAt: input.RecordedAt,
	})
	if err != nil {
		return DirectoryPersistResult{}, fmt.Errorf("construct directory decision: %w", err)
	}
	alias, err := identity.NewAliasAssertion(identity.AliasAssertionInput{
		ID: identity.AliasAssertionID(directoryOpaqueID(
			directoryAliasIDVersion,
			string(decision.ID()),
			email,
		)),
		DecisionID: decision.ID(),
		EntityID:   identity.EntityID(entityID),
		Alias: identity.Alias{
			Type:  identity.AliasTypeEmail,
			Value: email,
		},
		RecordedAt: input.RecordedAt,
	})
	if err != nil {
		return DirectoryPersistResult{}, fmt.Errorf("construct directory email alias: %w", err)
	}
	if err := transaction.AppendResolutionDecision(
		ctx,
		decision,
		[]identity.AliasAssertion{alias},
	); err != nil {
		return DirectoryPersistResult{}, err
	}
	if err := persistDirectoryEntityLink(
		ctx,
		transaction,
		profile,
		attemptID,
		input.Mention.ProposalID,
		candidate.ID(),
		decision.ID(),
		entityID,
		input.RecordedAt,
	); err != nil {
		return DirectoryPersistResult{}, err
	}
	return DirectoryPersistResult{AutoResolved: true, EntityID: entityID}, nil
}

func persistDirectoryReviewCandidates(
	ctx context.Context,
	transaction *Transaction,
	input DirectoryPersistInput,
	attemptID string,
	profiles []storedDirectoryProfile,
	candidates []DirectoryProfile,
) error {
	reason := directoryNameReviewReason
	if input.Query.Kind == DirectoryQueryEmail {
		reason = directoryEmailReviewReason
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidateProfile := range candidates {
		profile, err := findDirectoryProfile(candidateProfile, profiles)
		if err != nil {
			return err
		}
		if _, duplicate := seen[profile.snapshotID]; duplicate {
			continue
		}
		seen[profile.snapshotID] = struct{}{}
		entityID := directoryOpaqueID(
			directoryEntityIDVersion,
			profile.profile.Provider,
			profile.profile.SubjectID,
		)
		if err := ensureDirectoryEntity(
			ctx,
			transaction,
			entityID,
			profile.profile.DisplayName,
			input.RecordedAt,
		); err != nil {
			return err
		}
		candidate, err := ensureDirectoryCandidate(
			ctx,
			transaction,
			input.Mention.ProposalID,
			entityID,
			profile.snapshotID,
			reason,
			input.RecordedAt,
		)
		if err != nil {
			return err
		}
		if err := persistDirectoryEntityLink(
			ctx,
			transaction,
			profile,
			attemptID,
			input.Mention.ProposalID,
			candidate.ID(),
			"",
			entityID,
			input.RecordedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func ensureDirectoryEntity(
	ctx context.Context,
	transaction *Transaction,
	entityID string,
	displayName string,
	recordedAt time.Time,
) error {
	var storedID string
	err := transaction.QueryRow(ctx, `
		SELECT id
		FROM stacks_core.entities
		WHERE id = $1`,
		entityID,
	).Scan(&storedID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load directory candidate entity: %w", err)
	}
	value, err := identity.NewEntity(identity.EntityInput{
		ID:          identity.EntityID(entityID),
		Kind:        identity.KindPerson,
		DisplayName: displayName,
		RecordedAt:  recordedAt,
	})
	if err != nil {
		return fmt.Errorf("construct directory candidate entity: %w", err)
	}
	if _, err := transaction.PutEntity(ctx, value); err != nil {
		return err
	}
	return nil
}

func ensureDirectoryCandidate(
	ctx context.Context,
	transaction *Transaction,
	proposalID string,
	entityID string,
	snapshotID string,
	reason string,
	recordedAt time.Time,
) (identity.ResolutionCandidate, error) {
	candidateID := identity.CandidateID(directoryOpaqueID(
		directoryCandidateIDVersion,
		proposalID,
		snapshotID,
		reason,
	))
	source := identity.CandidateSource{
		Kind:      directoryCandidateSourceKind,
		Reference: snapshotID,
	}
	stored, err := loadResolutionCandidateValue(ctx, transaction.transaction, candidateID)
	if err == nil {
		if stored.ProposalID() != identity.ProposalID(proposalID) ||
			stored.EntityID() != identity.EntityID(entityID) ||
			stored.Confidence() != 0 ||
			stored.ReasonCode() != reason ||
			stored.Source() != source {
			return identity.ResolutionCandidate{}, fmt.Errorf(
				"stored directory candidate conflicts: %w",
				ErrConflict,
			)
		}
		return stored, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return identity.ResolutionCandidate{}, fmt.Errorf("load directory candidate: %w", err)
	}
	var rank int
	if err := transaction.QueryRow(ctx, `
		SELECT COALESCE(max(candidate_rank), 0) + 1
		FROM stacks_core.resolution_candidates
		WHERE proposal_id = $1`,
		proposalID,
	).Scan(&rank); err != nil {
		return identity.ResolutionCandidate{}, fmt.Errorf("load directory candidate rank: %w", err)
	}
	candidate, err := identity.NewResolutionCandidate(identity.ResolutionCandidateInput{
		ID:         candidateID,
		ProposalID: identity.ProposalID(proposalID),
		EntityID:   identity.EntityID(entityID),
		Rank:       rank,
		Confidence: 0,
		ReasonCode: reason,
		Source:     source,
		RecordedAt: recordedAt,
	})
	if err != nil {
		return identity.ResolutionCandidate{}, fmt.Errorf("construct directory candidate: %w", err)
	}
	if _, err := transaction.PutResolutionCandidate(ctx, candidate); err != nil {
		return identity.ResolutionCandidate{}, err
	}
	return candidate, nil
}

func persistDirectoryEntityLink(
	ctx context.Context,
	transaction *Transaction,
	profile storedDirectoryProfile,
	attemptID string,
	proposalID string,
	candidateID identity.CandidateID,
	decisionID identity.DecisionID,
	entityID string,
	recordedAt time.Time,
) error {
	linkID := directoryOpaqueID(
		directoryLinkIDVersion,
		proposalID,
		profile.snapshotID,
		string(candidateID),
	)
	var nullableDecision any
	if decisionID != "" {
		nullableDecision = decisionID
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO stacks_directory.entity_links (
			id, profile_id, snapshot_id, lookup_attempt_id, proposal_id,
			candidate_id, decision_id, entity_id, recorded_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO NOTHING`,
		linkID,
		profile.profileID,
		profile.snapshotID,
		attemptID,
		proposalID,
		candidateID,
		nullableDecision,
		entityID,
		recordedAt,
	); err != nil {
		return fmt.Errorf("insert directory entity link: %w", err)
	}
	var storedCandidate, storedEntity string
	var storedDecision *string
	if err := transaction.QueryRow(ctx, `
		SELECT candidate_id, decision_id, entity_id
		FROM stacks_directory.entity_links
		WHERE id = $1`,
		linkID,
	).Scan(&storedCandidate, &storedDecision, &storedEntity); err != nil {
		return fmt.Errorf("load directory entity link: %w", err)
	}
	if storedCandidate != string(candidateID) ||
		storedEntity != entityID ||
		!sameDirectoryStringPointer(storedDecision, string(decisionID)) {
		return fmt.Errorf("stored directory entity link conflicts: %w", ErrConflict)
	}
	return nil
}

func loadEffectiveDirectoryDecision(
	ctx context.Context,
	transaction *Transaction,
	proposalID string,
) (identity.ResolutionDecision, error) {
	var decisionID string
	if err := transaction.QueryRow(ctx, `
		SELECT decision.id
		FROM stacks_core.resolution_decisions AS decision
		WHERE decision.proposal_id = $1
		  AND NOT EXISTS (
			SELECT 1
			FROM stacks_core.resolution_decisions AS successor
			WHERE successor.supersedes_id = decision.id
		  )`,
		proposalID,
	).Scan(&decisionID); err != nil {
		return identity.ResolutionDecision{}, err
	}
	return loadResolutionDecisionValue(ctx, transaction.transaction, identity.DecisionID(decisionID))
}

func loadDirectoryEmailOwners(
	ctx context.Context,
	transaction *Transaction,
	email string,
) ([]string, error) {
	rows, err := transaction.Query(ctx, `
		SELECT DISTINCT alias.entity_id
		FROM stacks_core.entity_alias_assertions AS alias
		JOIN stacks_core.resolution_decisions AS decision
		  ON decision.id = alias.decision_id
		WHERE alias.alias_type = 'email'
		  AND alias.alias_value = $1
		  AND decision.outcome = 'accepted'
		  AND NOT EXISTS (
			SELECT 1
			FROM stacks_core.resolution_decisions AS successor
			WHERE successor.supersedes_id = decision.id
		  )
		ORDER BY alias.entity_id`,
		email,
	)
	if err != nil {
		return nil, fmt.Errorf("load directory email owners: %w", err)
	}
	defer rows.Close()
	var owners []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			return nil, fmt.Errorf("scan directory email owner: %w", err)
		}
		owners = append(owners, owner)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate directory email owners: %w", err)
	}
	return owners, nil
}

func loadDirectoryProviderOwners(
	ctx context.Context,
	transaction *Transaction,
	provider string,
	subjectID string,
) ([]string, error) {
	rows, err := transaction.Query(ctx, `
		SELECT DISTINCT current.entity_id
		FROM stacks_directory.entity_links AS link
		JOIN stacks_directory.profiles AS profile
		  ON profile.id = link.profile_id
		JOIN stacks_core.resolution_decisions AS current
		  ON current.proposal_id = link.proposal_id
		 AND current.entity_id = link.entity_id
		WHERE profile.provider = $1
		  AND profile.provider_subject_id = $2
		  AND current.outcome = 'accepted'
		  AND NOT EXISTS (
			SELECT 1
			FROM stacks_core.resolution_decisions AS successor
			WHERE successor.supersedes_id = current.id
		  )
		ORDER BY current.entity_id`,
		provider,
		subjectID,
	)
	if err != nil {
		return nil, fmt.Errorf("load directory provider owners: %w", err)
	}
	defer rows.Close()
	var owners []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			return nil, fmt.Errorf("scan directory provider owner: %w", err)
		}
		owners = append(owners, owner)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate directory provider owners: %w", err)
	}
	return owners, nil
}

func chooseDirectoryAuthorityEntity(
	evaluation DirectoryEvaluation,
	profile storedDirectoryProfile,
	emailOwners []string,
	providerOwners []string,
) (string, bool) {
	if len(emailOwners) > 1 || len(providerOwners) > 1 {
		return "", true
	}
	if len(providerOwners) == 1 {
		if len(emailOwners) != 1 || emailOwners[0] != providerOwners[0] {
			return "", true
		}
		return providerOwners[0], false
	}
	if len(emailOwners) == 1 {
		if evaluation.EntityID != "" && evaluation.EntityID != emailOwners[0] {
			return "", true
		}
		return emailOwners[0], false
	}
	if evaluation.EntityID != "" || !evaluation.CreatePerson {
		return "", true
	}
	return directoryOpaqueID(
		directoryEntityIDVersion,
		profile.profile.Provider,
		profile.profile.SubjectID,
	), false
}

func validateDirectoryPersistInput(input DirectoryPersistInput) error {
	if strings.TrimSpace(input.Mention.MentionID) == "" ||
		strings.TrimSpace(input.Mention.ProposalID) == "" {
		return fmt.Errorf("persist directory lookup: mention and proposal IDs are required")
	}
	if err := validateDirectoryQuery(input.Query); err != nil {
		return err
	}
	if !boundedDirectoryOutcome(input.Lookup.Outcome) ||
		(input.Evaluation.Outcome != "" && !boundedDirectoryOutcome(input.Evaluation.Outcome)) ||
		input.AttemptCount < 0 {
		return fmt.Errorf("persist directory lookup: bounded outcome or attempt count is invalid")
	}
	if !validDirectoryLookupProvider(input.Lookup.Provider) {
		return fmt.Errorf("persist directory lookup: lookup provider is invalid")
	}
	if input.RecordedAt.IsZero() || !timepoint.IsCanonical(input.RecordedAt) {
		return fmt.Errorf("persist directory lookup: recorded time is not canonical")
	}
	if input.RetryAfter != nil &&
		(input.RetryAfter.IsZero() || !timepoint.IsCanonical(*input.RetryAfter)) {
		return fmt.Errorf("persist directory lookup: retry time is not canonical")
	}
	retryable := input.Lookup.Outcome == DirectoryOutcomeRateLimited ||
		input.Lookup.Outcome == DirectoryOutcomeUnavailable
	if retryable != (input.RetryAfter != nil) ||
		(input.RetryAfter != nil && input.RetryAfter.Before(input.RecordedAt)) {
		return fmt.Errorf("persist directory lookup: retry time does not match outcome")
	}
	if isDirectoryProviderFailure(input.Lookup.Outcome) && len(input.Lookup.Profiles) > 0 {
		return fmt.Errorf("persist directory lookup: provider failure cannot include profiles")
	}
	for _, profile := range appendDirectoryProfiles(
		input.Lookup.Profiles,
		input.Evaluation.Profile,
		input.Evaluation.Candidates,
	) {
		if err := validateDirectoryProfile(profile); err != nil {
			return err
		}
	}
	if input.Evaluation.Outcome == DirectoryOutcomeMatched {
		if input.Lookup.Outcome != DirectoryOutcomeMatched ||
			input.Query.Kind != DirectoryQueryEmail ||
			input.Evaluation.Profile == nil ||
			normalizeDirectoryEmail(input.Evaluation.AcceptedEmail) !=
				normalizeDirectoryEmail(input.Query.Email) ||
			(input.Query.EmailEvidence != DirectoryEmailEvidenceSourceBound &&
				input.Query.EmailEvidence != DirectoryEmailEvidenceReviewerSupplied) ||
			input.Evaluation.Profile.Source != DirectorySourceDomainProfile ||
			!directoryProfileHasEmail(
				canonicalDirectoryProfile(*input.Evaluation.Profile),
				normalizeDirectoryEmail(input.Query.Email),
			) ||
			!directoryProfileInSet(*input.Evaluation.Profile, input.Lookup.Profiles) ||
			countEligibleDirectoryExactProfiles(
				input.Lookup.Profiles,
				normalizeDirectoryEmail(input.Evaluation.AcceptedEmail),
			) != 1 {
			return fmt.Errorf("persist directory lookup: automatic evaluation is invalid")
		}
	}
	return nil
}

func validateDirectoryQuery(query DirectoryQuery) error {
	switch query.Kind {
	case DirectoryQueryEmail:
		if !validDirectoryEmail(normalizeDirectoryEmail(query.Email)) ||
			strings.TrimSpace(query.Name) != "" {
			return fmt.Errorf("persist directory lookup: email query is invalid")
		}
		switch query.EmailEvidence {
		case DirectoryEmailEvidenceSourceBound,
			DirectoryEmailEvidenceCitationVerified,
			DirectoryEmailEvidenceReviewerSupplied:
		default:
			return fmt.Errorf("persist directory lookup: email evidence is invalid")
		}
	case DirectoryQueryName:
		if normalizeDirectoryName(query.Name) == "" ||
			strings.TrimSpace(query.Email) != "" ||
			query.EmailEvidence != DirectoryEmailEvidenceNone {
			return fmt.Errorf("persist directory lookup: name query is invalid")
		}
	default:
		return fmt.Errorf("persist directory lookup: query kind is invalid")
	}
	return nil
}

func validateDirectoryProfile(profile DirectoryProfile) error {
	if strings.TrimSpace(profile.Provider) == "" ||
		strings.TrimSpace(profile.SubjectID) == "" ||
		strings.TrimSpace(profile.DisplayName) == "" ||
		(profile.Source != DirectorySourceDomainProfile &&
			profile.Source != DirectorySourceDomainContact) ||
		len(profile.Emails) == 0 {
		return fmt.Errorf("persist directory lookup: directory profile is invalid")
	}
	if !profile.ObservedAt.IsZero() && !timepoint.IsCanonical(profile.ObservedAt) {
		return fmt.Errorf("persist directory lookup: profile observation time is not canonical")
	}
	for _, email := range profile.Emails {
		if !validDirectoryEmail(normalizeDirectoryEmail(email.Value)) {
			return fmt.Errorf("persist directory lookup: profile email is invalid")
		}
	}
	return nil
}

func canonicalDirectoryProfile(profile DirectoryProfile) DirectoryProfile {
	profile.Provider = strings.TrimSpace(profile.Provider)
	profile.SubjectID = strings.TrimSpace(profile.SubjectID)
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	emails := make(map[string]bool, len(profile.Emails))
	for _, email := range profile.Emails {
		value := normalizeDirectoryEmail(email.Value)
		emails[value] = emails[value] || email.Primary
	}
	profile.Emails = make([]DirectoryEmail, 0, len(emails))
	for value, primary := range emails {
		profile.Emails = append(profile.Emails, DirectoryEmail{Value: value, Primary: primary})
	}
	sort.Slice(profile.Emails, func(left, right int) bool {
		return profile.Emails[left].Value < profile.Emails[right].Value
	})
	return profile
}

func digestDirectoryProfile(profile DirectoryProfile) [sha256.Size]byte {
	fields := []string{
		profile.Provider,
		profile.SubjectID,
		profile.Source,
		profile.DisplayName,
		directoryTimeString(profile.ObservedAt),
	}
	for _, email := range profile.Emails {
		fields = append(fields, email.Value, strconv.FormatBool(email.Primary))
	}
	return directoryDigest(directoryProfileDigest, fields...)
}

func digestDirectoryQuery(query DirectoryQuery) [sha256.Size]byte {
	return directoryDigest(
		directoryQueryDigest,
		query.Kind,
		normalizeDirectoryName(query.Name),
		normalizeDirectoryEmail(query.Email),
		query.EmailEvidence,
	)
}

func digestDirectoryAttempt(
	input DirectoryPersistInput,
	queryDigest [sha256.Size]byte,
	snapshotIDs []string,
) [sha256.Size]byte {
	fields := []string{
		input.Mention.MentionID,
		input.Mention.ProposalID,
		normalizeDirectoryProvider(input.Lookup.Provider),
		fmt.Sprintf("%x", queryDigest),
		directoryPolicyVersion,
		input.Lookup.Outcome,
		strconv.Itoa(input.AttemptCount),
		directoryTimeString(input.RecordedAt),
	}
	if input.RetryAfter != nil {
		fields = append(fields, directoryTimeString(*input.RetryAfter))
	} else {
		fields = append(fields, "")
	}
	fields = append(fields, snapshotIDs...)
	return directoryDigest(directoryAttemptDigest, fields...)
}

func directoryDigest(version string, fields ...string) [sha256.Size]byte {
	hash := sha256.New()
	var length [8]byte
	write := func(value string) {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	write(version)
	for _, field := range fields {
		write(field)
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func directoryOpaqueID(version string, fields ...string) string {
	digest := directoryDigest(version, fields...)
	return "directory:" + fmt.Sprintf("%x", digest)
}

func findDirectoryProfile(
	raw DirectoryProfile,
	profiles []storedDirectoryProfile,
) (storedDirectoryProfile, error) {
	digest := digestDirectoryProfile(canonicalDirectoryProfile(raw))
	for _, profile := range profiles {
		if profile.digest == digest {
			return profile, nil
		}
	}
	return storedDirectoryProfile{}, fmt.Errorf(
		"directory evaluation profile is not in lookup results: %w",
		ErrConflict,
	)
}

func appendDirectoryProfiles(
	lookup []DirectoryProfile,
	evaluated *DirectoryProfile,
	candidates []DirectoryProfile,
) []DirectoryProfile {
	result := append([]DirectoryProfile(nil), lookup...)
	if evaluated != nil {
		result = append(result, *evaluated)
	}
	return append(result, candidates...)
}

func directoryProfileInSet(profile DirectoryProfile, values []DirectoryProfile) bool {
	digest := digestDirectoryProfile(canonicalDirectoryProfile(profile))
	for _, value := range values {
		if digestDirectoryProfile(canonicalDirectoryProfile(value)) == digest {
			return true
		}
	}
	return false
}

func directoryProfileHasEmail(profile DirectoryProfile, email string) bool {
	for _, candidate := range profile.Emails {
		if candidate.Value == email {
			return true
		}
	}
	return false
}

func boundedDirectoryOutcome(outcome string) bool {
	switch outcome {
	case DirectoryOutcomeMatched,
		DirectoryOutcomeNoMatch,
		DirectoryOutcomeAmbiguous,
		DirectoryOutcomeReview,
		DirectoryOutcomeDisabled,
		DirectoryOutcomeNotConfigured,
		DirectoryOutcomeUnauthorized,
		DirectoryOutcomeForbidden,
		DirectoryOutcomeRateLimited,
		DirectoryOutcomeUnavailable,
		DirectoryOutcomeInvalidResponse,
		DirectoryOutcomeResultLimitExceeded:
		return true
	default:
		return false
	}
}

func isDirectoryProviderFailure(outcome string) bool {
	switch outcome {
	case DirectoryOutcomeDisabled,
		DirectoryOutcomeNotConfigured,
		DirectoryOutcomeUnauthorized,
		DirectoryOutcomeForbidden,
		DirectoryOutcomeRateLimited,
		DirectoryOutcomeUnavailable,
		DirectoryOutcomeInvalidResponse,
		DirectoryOutcomeResultLimitExceeded:
		return true
	default:
		return false
	}
}

func normalizeDirectoryName(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func normalizeDirectoryEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validDirectoryEmail(value string) bool {
	at := strings.LastIndex(value, "@")
	return at > 0 && at < len(value)-1 && !strings.ContainsAny(value, " \t\r\n")
}

func normalizeDirectoryProvider(value string) string {
	return strings.TrimSpace(value)
}

func validDirectoryLookupProvider(value string) bool {
	normalized := normalizeDirectoryProvider(value)
	return normalized != "" &&
		utf8.ValidString(normalized) &&
		utf8.RuneCountInString(normalized) <= directoryLookupProviderMaximumRunes
}

func countEligibleDirectoryExactProfiles(
	profiles []DirectoryProfile,
	email string,
) int {
	matches := make(map[[sha256.Size]byte]struct{})
	for _, raw := range profiles {
		profile := canonicalDirectoryProfile(raw)
		if profile.Source != DirectorySourceDomainProfile ||
			!directoryProfileHasEmail(profile, email) {
			continue
		}
		matches[digestDirectoryProfile(profile)] = struct{}{}
	}
	return len(matches)
}

func directoryTimeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func sameDirectoryTime(stored *time.Time, want time.Time) bool {
	if want.IsZero() {
		return stored == nil
	}
	return stored != nil && stored.UTC() == want
}

func sameDirectoryTimePointer(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.UTC() == *right
}

func sameDirectoryStringPointer(stored *string, want string) bool {
	if want == "" {
		return stored == nil
	}
	return stored != nil && *stored == want
}

func equalDirectoryStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func wrapDirectoryError(ctx context.Context, operation string, err error) error {
	if ctx != nil {
		if contextError := ctx.Err(); contextError != nil {
			return fmt.Errorf("%s: %w", operation, contextError)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
