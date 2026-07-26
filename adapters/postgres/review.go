package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/jackc/pgx/v5"
)

const reviewerEligibleProposalsQuery = `
	SELECT proposal.id, proposal.recorded_at
	FROM stacks_core.resolution_proposals AS proposal
	JOIN stacks_core.mentions AS mention
	  ON mention.id = proposal.mention_id
	JOIN stacks_core.extraction_runs AS run
	  ON run.id = mention.derivation_run_id
	JOIN stacks_core.document_versions AS version
	  ON version.id = run.document_version_id
	JOIN stacks_core.source_documents AS source
	  ON source.id = version.source_document_id
	WHERE run.state = 'completed'
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
	  )`

// ReviewerEvidence is one exact cited span in a private reviewer projection.
type ReviewerEvidence struct {
	ID    string
	Quote string
}

// ReviewerEntityRecord is the bounded private projection used by an
// interactive identity reviewer.
type ReviewerEntityRecord struct {
	Entity       identity.Entity
	Aliases      []identity.Alias
	MentionCount int
	Evidence     []ReviewerEvidence
}

// ReviewerDirectoryProfile is the exact optional directory snapshot presented
// for one candidate.
type ReviewerDirectoryProfile struct {
	SnapshotID  string
	DisplayName string
	MaskedEmail string
	Source      string
}

// ReviewerCandidateRecord is one ranked candidate with optional directory
// provenance.
type ReviewerCandidateRecord struct {
	Candidate identity.ResolutionCandidate
	Entity    identity.Entity
	Directory *ReviewerDirectoryProfile
}

// ReviewerProposalRecord is one cited proposal and its deterministic
// candidates.
type ReviewerProposalRecord struct {
	Proposal          identity.ResolutionProposal
	Mention           identity.MentionRecord
	Evidence          []ReviewerEvidence
	Candidates        []ReviewerCandidateRecord
	EffectiveDecision *identity.ResolutionDecision
}

// ReviewerDecisionCommand appends one canonical reviewer decision.
type ReviewerDecisionCommand struct {
	Decision identity.ResolutionDecision
	Aliases  []identity.AliasAssertion
}

// ReviewerCreatePersonCommand atomically creates a person and accepts it for a
// proposal. Optional directory evidence remains additive.
type ReviewerCreatePersonCommand struct {
	Entity            identity.Entity
	Decision          identity.ResolutionDecision
	Aliases           []identity.AliasAssertion
	DirectoryEvidence *ReviewerDirectoryEvidenceCommand
}

// ReviewerDirectoryEvidenceCommand is optional reviewer-supplied directory
// audit evidence. It is deliberately distinct from enrichment persistence.
type ReviewerDirectoryEvidenceCommand struct {
	Mention      DirectoryPendingMention
	Query        DirectoryQuery
	Lookup       DirectoryLookupResult
	Evaluation   DirectoryEvaluation
	AttemptCount int
	RecordedAt   time.Time
	RetryAfter   *time.Time
}

// ReviewerDirectoryDecisionCommand accepts exactly one previously persisted
// directory-backed candidate.
type ReviewerDirectoryDecisionCommand struct {
	Decision   identity.ResolutionDecision
	Aliases    []identity.AliasAssertion
	SnapshotID string
}

// ReviewerStore owns canonical interactive-review reads and transactions.
type ReviewerStore struct {
	Database         *Database
	IncludeDirectory bool
}

// ListEntities returns deterministic current effective reviewer projections.
func (store ReviewerStore) ListEntities(
	ctx context.Context,
) ([]ReviewerEntityRecord, error) {
	if err := store.validate("list reviewer entities"); err != nil {
		return nil, err
	}
	records, err := store.Database.ListEntities(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]ReviewerEntityRecord, len(records))
	for index, record := range records {
		projected, err := store.projectEntity(ctx, record)
		if err != nil {
			return nil, err
		}
		result[index] = projected
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Entity.DisplayName() == result[right].Entity.DisplayName() {
			return result[left].Entity.ID() < result[right].Entity.ID()
		}
		return result[left].Entity.DisplayName() < result[right].Entity.DisplayName()
	})
	return result, nil
}

// LoadEntity returns one current effective reviewer projection.
func (store ReviewerStore) LoadEntity(
	ctx context.Context,
	entityID identity.EntityID,
) (ReviewerEntityRecord, error) {
	if err := store.validate("load reviewer entity"); err != nil {
		return ReviewerEntityRecord{}, err
	}
	record, err := store.Database.LoadEntity(ctx, entityID)
	if err != nil {
		return ReviewerEntityRecord{}, err
	}
	return store.projectEntity(ctx, record)
}

func (store ReviewerStore) projectEntity(
	ctx context.Context,
	record EntityRecord,
) (ReviewerEntityRecord, error) {
	result := ReviewerEntityRecord{
		Entity:       record.Entity,
		Aliases:      make([]identity.Alias, len(record.Aliases)),
		MentionCount: len(record.GroundingMentionIDs),
		Evidence:     make([]ReviewerEvidence, len(record.EvidenceIDs)),
	}
	for index, assertion := range record.Aliases {
		result.Aliases[index] = assertion.Alias()
	}
	for index, evidenceID := range record.EvidenceIDs {
		citation, err := loadReviewerEvidence(ctx, store.Database, string(evidenceID))
		if err != nil {
			return ReviewerEntityRecord{}, fmt.Errorf("load reviewer entity evidence: %w", err)
		}
		result.Evidence[index] = citation
	}
	sort.Slice(result.Aliases, func(left, right int) bool {
		if result.Aliases[left].Type == result.Aliases[right].Type {
			return result.Aliases[left].Value < result.Aliases[right].Value
		}
		return result.Aliases[left].Type < result.Aliases[right].Type
	})
	sort.Slice(result.Evidence, func(left, right int) bool {
		return result.Evidence[left].ID < result.Evidence[right].ID
	})
	return result, nil
}

// ListProposals returns pending proposals only.
func (store ReviewerStore) ListProposals(
	ctx context.Context,
) ([]ReviewerProposalRecord, error) {
	if err := store.validate("list reviewer proposals"); err != nil {
		return nil, err
	}
	rows, err := store.Database.pool.Query(ctx, `
		WITH eligible AS (`+reviewerEligibleProposalsQuery+`)
		SELECT eligible.id
		FROM eligible
		WHERE NOT EXISTS (
			SELECT 1
			FROM stacks_core.resolution_decisions AS decision
			WHERE decision.proposal_id = eligible.id
		)
		ORDER BY eligible.recorded_at, eligible.id`)
	if err != nil {
		return nil, wrapIdentityError(ctx, "list eligible reviewer proposals", err)
	}
	defer rows.Close()
	var proposalIDs []identity.ProposalID
	for rows.Next() {
		var proposalID identity.ProposalID
		if err := rows.Scan(&proposalID); err != nil {
			return nil, wrapIdentityError(ctx, "scan eligible reviewer proposal", err)
		}
		proposalIDs = append(proposalIDs, proposalID)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapIdentityError(ctx, "iterate eligible reviewer proposals", err)
	}
	result := make([]ReviewerProposalRecord, len(proposalIDs))
	for index, proposalID := range proposalIDs {
		record, err := loadResolutionProposalRecord(ctx, store.Database.pool, proposalID)
		if err != nil {
			return nil, wrapIdentityReadError(ctx, "load eligible reviewer proposal", err)
		}
		projected, err := store.projectProposal(ctx, record)
		if err != nil {
			return nil, err
		}
		result[index] = projected
	}
	return result, nil
}

// LoadProposal returns one proposal including its current effective authority.
func (store ReviewerStore) LoadProposal(
	ctx context.Context,
	proposalID identity.ProposalID,
) (ReviewerProposalRecord, error) {
	if err := store.validate("load reviewer proposal"); err != nil {
		return ReviewerProposalRecord{}, err
	}
	if err := requireReviewerEligibleProposal(ctx, store.Database.pool, proposalID); err != nil {
		return ReviewerProposalRecord{}, err
	}
	record, err := store.Database.LoadResolutionProposal(ctx, proposalID)
	if err != nil {
		return ReviewerProposalRecord{}, err
	}
	return store.projectProposal(ctx, record)
}

func requireReviewerEligibleProposal(
	ctx context.Context,
	reader documentReader,
	proposalID identity.ProposalID,
) error {
	var eligibleID identity.ProposalID
	if err := reader.QueryRow(ctx, `
		WITH eligible AS (`+reviewerEligibleProposalsQuery+`)
		SELECT eligible.id
		FROM eligible
		WHERE eligible.id = $1`,
		proposalID,
	).Scan(&eligibleID); err != nil {
		return wrapIdentityReadError(ctx, "load eligible reviewer proposal", err)
	}
	return nil
}

func lockReviewerEligibleProposal(
	ctx context.Context,
	transaction *Transaction,
	proposalID identity.ProposalID,
) error {
	if err := lockResolutionProposalAuthority(ctx, transaction, proposalID); err != nil {
		return err
	}
	var runID, mentionID, sourceDocumentID string
	if err := transaction.QueryRow(ctx, `
		SELECT run.id, mention.id, version.source_document_id
		FROM stacks_core.resolution_proposals AS proposal
		JOIN stacks_core.mentions AS mention
		  ON mention.id = proposal.mention_id
		JOIN stacks_core.extraction_runs AS run
		  ON run.id = mention.derivation_run_id
		JOIN stacks_core.document_versions AS version
		  ON version.id = run.document_version_id
		WHERE proposal.id = $1`,
		proposalID,
	).Scan(&runID, &mentionID, &sourceDocumentID); err != nil {
		return wrapIdentityReadError(ctx, "load reviewer eligibility lock targets", err)
	}
	if err := lockAdmissionTargetAuthority(
		ctx,
		transaction,
		string(admission.TargetExtractionRun),
		runID,
	); err != nil {
		return err
	}
	if err := lockAdmissionTargetAuthority(
		ctx,
		transaction,
		string(admission.TargetMention),
		mentionID,
	); err != nil {
		return err
	}
	var lockedSourceDocumentID string
	if err := transaction.QueryRow(ctx, `
		/* stacks_reviewer_eligibility_source */
		SELECT id
		FROM stacks_core.source_documents
		WHERE id = $1
		FOR SHARE`,
		sourceDocumentID,
	).Scan(&lockedSourceDocumentID); err != nil {
		return wrapIdentityReadError(ctx, "lock reviewer source document eligibility", err)
	}
	return requireReviewerEligibleProposal(ctx, transaction, proposalID)
}

// LoadDecision returns one immutable historical reviewer decision.
func (store ReviewerStore) LoadDecision(
	ctx context.Context,
	decisionID identity.DecisionID,
) (identity.ResolutionDecision, error) {
	if err := store.validate("load reviewer decision"); err != nil {
		return identity.ResolutionDecision{}, err
	}
	return store.Database.LoadResolutionDecision(ctx, decisionID)
}

func (store ReviewerStore) projectProposal(
	ctx context.Context,
	record ResolutionProposalRecord,
) (ReviewerProposalRecord, error) {
	result := ReviewerProposalRecord{
		Proposal:          record.Proposal,
		EffectiveDecision: record.EffectiveDecision,
		Evidence:          make([]ReviewerEvidence, len(record.Proposal.EvidenceIDs())),
		Candidates:        make([]ReviewerCandidateRecord, len(record.Candidates)),
	}
	mention, err := loadMentionValue(ctx, store.Database.pool, record.Proposal.MentionID())
	if err != nil {
		return ReviewerProposalRecord{}, fmt.Errorf("load reviewer proposal mention: %w", err)
	}
	result.Mention = mention
	evidenceIDs := record.Proposal.EvidenceIDs()
	for index, evidenceID := range evidenceIDs {
		citation, err := loadReviewerEvidence(ctx, store.Database, string(evidenceID))
		if err != nil {
			return ReviewerProposalRecord{}, fmt.Errorf("load reviewer proposal evidence: %w", err)
		}
		result.Evidence[index] = citation
	}
	for index, candidate := range record.Candidates {
		entityRecord, err := store.Database.LoadEntity(ctx, candidate.EntityID())
		if err != nil {
			return ReviewerProposalRecord{}, fmt.Errorf("load reviewer candidate entity: %w", err)
		}
		projected := ReviewerCandidateRecord{
			Candidate: candidate,
			Entity:    entityRecord.Entity,
		}
		if store.IncludeDirectory && candidate.Source().Kind == directoryCandidateSourceKind {
			directory, err := store.loadDirectoryProfile(ctx, record.Proposal.ID(), candidate)
			if err != nil {
				return ReviewerProposalRecord{}, err
			}
			projected.Directory = &directory
		}
		result.Candidates[index] = projected
	}
	return result, nil
}

func loadReviewerEvidence(
	ctx context.Context,
	database *Database,
	evidenceID string,
) (ReviewerEvidence, error) {
	var result ReviewerEvidence
	if err := database.pool.QueryRow(ctx, `
		SELECT id, quote
		FROM stacks_core.evidence_spans
		WHERE id = $1`,
		evidenceID,
	).Scan(&result.ID, &result.Quote); err != nil {
		return ReviewerEvidence{}, wrapIdentityReadError(ctx, "load reviewer evidence", err)
	}
	return result, nil
}

func (store ReviewerStore) loadDirectoryProfile(
	ctx context.Context,
	proposalID identity.ProposalID,
	candidate identity.ResolutionCandidate,
) (ReviewerDirectoryProfile, error) {
	var profile ReviewerDirectoryProfile
	err := store.Database.pool.QueryRow(ctx, `
		SELECT
			snapshot.id,
			snapshot.display_name,
			snapshot.source_type,
			COALESCE((
				SELECT email.normalized_email
				FROM stacks_directory.profile_emails AS email
				WHERE email.snapshot_id = snapshot.id
				ORDER BY email.is_primary DESC, email.email_order
				LIMIT 1
			), '')
		FROM stacks_directory.entity_links AS link
		JOIN stacks_directory.snapshots AS snapshot
		  ON snapshot.id = link.snapshot_id
		WHERE link.proposal_id = $1
		  AND link.candidate_id = $2
		  AND link.snapshot_id = $3`,
		proposalID,
		candidate.ID(),
		candidate.Source().Reference,
	).Scan(
		&profile.SnapshotID,
		&profile.DisplayName,
		&profile.Source,
		&profile.MaskedEmail,
	)
	if err != nil {
		return ReviewerDirectoryProfile{}, wrapDirectoryError(ctx, "load reviewer directory profile", err)
	}
	profile.MaskedEmail = maskReviewerEmail(profile.MaskedEmail)
	return profile, nil
}

// AppendDecision appends an accept, reject, or correction.
func (store ReviewerStore) AppendDecision(
	ctx context.Context,
	command ReviewerDecisionCommand,
) (identity.ResolutionDecision, error) {
	if err := store.validate("append reviewer decision"); err != nil {
		return identity.ResolutionDecision{}, err
	}
	err := store.Database.InTransaction(ctx, func(transaction *Transaction) error {
		if err := lockResolutionProposalAuthority(
			ctx,
			transaction,
			command.Decision.ProposalID(),
		); err != nil {
			return err
		}
		exact, err := transaction.exactResolutionDecisionRetry(
			ctx,
			command.Decision,
			command.Aliases,
		)
		if err != nil {
			return err
		}
		if exact {
			return nil
		}
		if command.Decision.SupersedesID() == "" {
			if err := lockReviewerEligibleProposal(
				ctx,
				transaction,
				command.Decision.ProposalID(),
			); err != nil {
				return err
			}
		}
		var proof *reviewerDirectoryProof
		if store.IncludeDirectory &&
			command.Decision.SupersedesID() != "" &&
			command.Decision.Outcome() == identity.DecisionAccepted {
			current, err := loadReviewerDirectoryProof(
				ctx,
				transaction,
				command.Decision.SupersedesID(),
			)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil {
				proof = &current
			}
		}
		if err := transaction.AppendResolutionDecision(ctx, command.Decision, command.Aliases); err != nil {
			return err
		}
		if proof == nil {
			return nil
		}
		return persistDirectoryEntityLink(
			ctx,
			transaction,
			storedDirectoryProfile{
				profileID:  proof.profileID,
				snapshotID: proof.snapshotID,
			},
			proof.attemptID,
			string(command.Decision.ProposalID()),
			proof.candidateID,
			command.Decision.ID(),
			string(command.Decision.EntityID()),
			command.Decision.RecordedAt(),
		)
	})
	if err != nil {
		return identity.ResolutionDecision{}, fmt.Errorf("append reviewer decision: %w", err)
	}
	return command.Decision, nil
}

type reviewerDirectoryProof struct {
	profileID   string
	snapshotID  string
	attemptID   string
	candidateID identity.CandidateID
	entityID    identity.EntityID
}

func loadReviewerDirectoryProof(
	ctx context.Context,
	transaction *Transaction,
	decisionID identity.DecisionID,
) (reviewerDirectoryProof, error) {
	var proof reviewerDirectoryProof
	var candidateID *string
	err := transaction.QueryRow(ctx, `
		SELECT profile_id, snapshot_id, lookup_attempt_id, candidate_id, entity_id
		FROM stacks_directory.entity_links
		WHERE decision_id = $1`,
		decisionID,
	).Scan(
		&proof.profileID,
		&proof.snapshotID,
		&proof.attemptID,
		&candidateID,
		&proof.entityID,
	)
	if err != nil {
		return reviewerDirectoryProof{}, err
	}
	if candidateID != nil {
		proof.candidateID = identity.CandidateID(*candidateID)
	}
	return proof, nil
}

// CreatePerson atomically creates a person, appends reviewer authority, and
// records optional reviewer-supplied directory evidence.
func (store ReviewerStore) CreatePerson(
	ctx context.Context,
	command ReviewerCreatePersonCommand,
) (identity.ResolutionDecision, error) {
	if err := store.validate("create reviewer person"); err != nil {
		return identity.ResolutionDecision{}, err
	}
	if command.Decision.Outcome() != identity.DecisionAccepted ||
		command.Decision.Authority() != identity.AuthorityReviewer ||
		command.Decision.EntityID() != command.Entity.ID() {
		return identity.ResolutionDecision{}, fmt.Errorf("create reviewer person: accepted reviewer authority is required")
	}
	err := store.Database.InTransaction(ctx, func(transaction *Transaction) error {
		exact, err := transaction.exactResolutionDecisionRetry(
			ctx,
			command.Decision,
			command.Aliases,
		)
		if err != nil {
			return err
		}
		if exact {
			storedEntity, err := loadEntityValue(
				ctx,
				transaction.transaction,
				command.Entity.ID(),
			)
			if err != nil {
				return wrapIdentityReadError(ctx, "load reviewer person retry", err)
			}
			if !sameEntity(storedEntity, command.Entity) {
				return fmt.Errorf("reviewer person retry conflicts: %w", ErrConflict)
			}
			if !store.IncludeDirectory {
				if command.DirectoryEvidence != nil {
					return fmt.Errorf("reviewer directory evidence retry conflicts: %w", ErrConflict)
				}
				return nil
			}
			return requireExactReviewerDirectoryEvidence(
				ctx,
				transaction,
				command,
			)
		}
		if err := lockReviewerEligibleProposal(
			ctx,
			transaction,
			command.Decision.ProposalID(),
		); err != nil {
			return err
		}
		if _, err := transaction.PutEntity(ctx, command.Entity); err != nil {
			return err
		}
		var directoryLink *reviewerDirectoryLink
		if command.DirectoryEvidence != nil {
			if !store.IncludeDirectory {
				return fmt.Errorf("reviewer directory evidence requires the optional directory scope")
			}
			link, _, err := persistReviewerDirectoryEvidence(
				ctx,
				transaction,
				*command.DirectoryEvidence,
				command.Decision,
			)
			if err != nil {
				return err
			}
			directoryLink = link
		}
		if err := transaction.AppendResolutionDecision(ctx, command.Decision, command.Aliases); err != nil {
			return err
		}
		if directoryLink != nil {
			return persistDirectoryEntityLink(
				ctx,
				transaction,
				directoryLink.profile,
				directoryLink.attemptID,
				string(command.Decision.ProposalID()),
				directoryLink.candidate,
				command.Decision.ID(),
				string(command.Entity.ID()),
				command.Decision.RecordedAt(),
			)
		}
		return nil
	})
	if err != nil {
		return identity.ResolutionDecision{}, fmt.Errorf("create reviewer person: %w", err)
	}
	return command.Decision, nil
}

type reviewerDirectoryLink struct {
	attemptID string
	profile   storedDirectoryProfile
	candidate identity.CandidateID
}

// ValidateReviewerDirectoryEvidence applies the complete pure persistence
// contract for optional reviewer-supplied directory evidence.
func ValidateReviewerDirectoryEvidence(
	command ReviewerDirectoryEvidenceCommand,
	decision identity.ResolutionDecision,
) error {
	input := DirectoryPersistInput(command)
	if err := validateDirectoryPersistInput(input); err != nil {
		return err
	}
	if input.Mention.ProposalID != string(decision.ProposalID()) ||
		input.Query.Kind != DirectoryQueryEmail ||
		input.Query.EmailEvidence != DirectoryEmailEvidenceReviewerSupplied ||
		input.Evaluation.Outcome != input.Lookup.Outcome {
		return fmt.Errorf("reviewer directory evidence is invalid")
	}
	for _, profile := range appendDirectoryProfiles(
		input.Lookup.Profiles,
		input.Evaluation.Profile,
		input.Evaluation.Candidates,
	) {
		if profile.ObservedAt.IsZero() {
			return fmt.Errorf("reviewer directory evidence observation time is required")
		}
	}
	switch input.Lookup.Outcome {
	case DirectoryOutcomeMatched:
		hasEntity := strings.TrimSpace(input.Evaluation.EntityID) != ""
		if input.Evaluation.CreatePerson == hasEntity ||
			len(input.Evaluation.Candidates) != 0 {
			return fmt.Errorf("reviewer directory matched evaluation is invalid")
		}
	case DirectoryOutcomeAmbiguous, DirectoryOutcomeReview:
		if len(input.Lookup.Profiles) == 0 ||
			len(input.Evaluation.Candidates) == 0 ||
			strings.TrimSpace(input.Evaluation.EntityID) != "" ||
			input.Evaluation.CreatePerson ||
			strings.TrimSpace(input.Evaluation.AcceptedEmail) != "" ||
			input.Evaluation.Profile != nil {
			return fmt.Errorf("reviewer directory candidate evaluation is invalid")
		}
		queryEmail := normalizeDirectoryEmail(input.Query.Email)
		for _, candidate := range input.Evaluation.Candidates {
			if !directoryProfileInSet(candidate, input.Lookup.Profiles) ||
				!directoryProfileHasEmail(
					canonicalDirectoryProfile(candidate),
					queryEmail,
				) {
				return fmt.Errorf("reviewer directory candidate evaluation is invalid")
			}
		}
	default:
		if len(input.Lookup.Profiles) != 0 ||
			strings.TrimSpace(input.Evaluation.EntityID) != "" ||
			input.Evaluation.CreatePerson ||
			strings.TrimSpace(input.Evaluation.AcceptedEmail) != "" ||
			input.Evaluation.Profile != nil ||
			len(input.Evaluation.Candidates) != 0 {
			return fmt.Errorf("reviewer directory terminal evaluation is invalid")
		}
	}
	return nil
}

func requireExactReviewerDirectoryEvidence(
	ctx context.Context,
	transaction *Transaction,
	command ReviewerCreatePersonCommand,
) error {
	storedAttemptID, err := loadReviewerDirectoryAttemptID(
		ctx,
		transaction,
		command.Decision,
	)
	if err != nil {
		return err
	}
	if command.DirectoryEvidence == nil {
		if storedAttemptID != "" {
			return fmt.Errorf("reviewer directory evidence retry conflicts: %w", ErrConflict)
		}
		if _, err := loadReviewerDirectoryProof(
			ctx,
			transaction,
			command.Decision.ID(),
		); err == nil {
			return fmt.Errorf("reviewer directory proof retry conflicts: %w", ErrConflict)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return nil
	}
	if storedAttemptID == "" {
		return fmt.Errorf("reviewer directory evidence retry conflicts: %w", ErrConflict)
	}
	if err := ValidateReviewerDirectoryEvidence(
		*command.DirectoryEvidence,
		command.Decision,
	); err != nil {
		return err
	}
	input := DirectoryPersistInput(*command.DirectoryEvidence)
	profiles := prepareDirectoryProfiles(input)
	for _, profile := range profiles {
		if err := requireExactDirectoryProfile(ctx, transaction, profile); err != nil {
			return err
		}
	}
	attemptID, attemptDigest, snapshotIDs := prepareDirectoryAttempt(input, profiles)
	if storedAttemptID != attemptID {
		return fmt.Errorf("reviewer directory evidence retry conflicts: %w", ErrConflict)
	}
	if err := requireExactDirectoryAttempt(
		ctx,
		transaction,
		input,
		attemptID,
		attemptDigest,
		snapshotIDs,
	); err != nil {
		return err
	}
	proof, proofErr := loadReviewerDirectoryProof(
		ctx,
		transaction,
		command.Decision.ID(),
	)
	if input.Evaluation.Profile == nil {
		if errors.Is(proofErr, pgx.ErrNoRows) {
			return nil
		}
		if proofErr != nil {
			return proofErr
		}
		return fmt.Errorf("reviewer directory proof retry conflicts: %w", ErrConflict)
	}
	if proofErr != nil {
		if errors.Is(proofErr, pgx.ErrNoRows) {
			return fmt.Errorf("reviewer directory proof retry conflicts: %w", ErrConflict)
		}
		return proofErr
	}
	profile, err := findDirectoryProfile(*input.Evaluation.Profile, profiles)
	if err != nil {
		return err
	}
	if proof.profileID != profile.profileID ||
		proof.snapshotID != profile.snapshotID ||
		proof.attemptID != attemptID ||
		proof.candidateID != "" ||
		proof.entityID != command.Entity.ID() {
		return fmt.Errorf("reviewer directory proof retry conflicts: %w", ErrConflict)
	}
	return nil
}

func persistReviewerDirectoryEvidence(
	ctx context.Context,
	transaction *Transaction,
	command ReviewerDirectoryEvidenceCommand,
	decision identity.ResolutionDecision,
) (*reviewerDirectoryLink, string, error) {
	input := DirectoryPersistInput(command)
	if err := ValidateReviewerDirectoryEvidence(command, decision); err != nil {
		return nil, "", err
	}
	if err := lockDirectoryProposal(ctx, transaction, input.Mention); err != nil {
		return nil, "", err
	}
	if err := lockDirectoryAuthorities(ctx, transaction, input); err != nil {
		return nil, "", err
	}
	profiles, err := persistDirectoryProfiles(ctx, transaction, input)
	if err != nil {
		return nil, "", err
	}
	attemptID, err := persistDirectoryAttempt(ctx, transaction, input, profiles)
	if err != nil {
		return nil, "", err
	}
	if input.Evaluation.Profile == nil {
		return nil, attemptID, nil
	}
	profile, err := findDirectoryProfile(*input.Evaluation.Profile, profiles)
	if err != nil {
		return nil, "", err
	}
	if err := requireDirectoryProviderOwner(
		ctx,
		transaction,
		profile.profile.Provider,
		profile.profile.SubjectID,
		string(decision.EntityID()),
	); err != nil {
		return nil, "", err
	}
	return &reviewerDirectoryLink{
		attemptID: attemptID,
		profile:   profile,
		candidate: "",
	}, attemptID, nil
}

func loadReviewerDirectoryAttemptID(
	ctx context.Context,
	transaction *Transaction,
	decision identity.ResolutionDecision,
) (string, error) {
	rows, err := transaction.Query(ctx, `
		SELECT id
		FROM stacks_directory.lookup_attempts
		WHERE proposal_id = $1
		  AND email_evidence = $2
		  AND recorded_at = $3
		ORDER BY id`,
		decision.ProposalID(),
		DirectoryEmailEvidenceReviewerSupplied,
		decision.RecordedAt(),
	)
	if err != nil {
		return "", fmt.Errorf("load reviewer directory attempts: %w", err)
	}
	defer rows.Close()
	var attemptIDs []string
	for rows.Next() {
		var attemptID string
		if err := rows.Scan(&attemptID); err != nil {
			return "", fmt.Errorf("scan reviewer directory attempt: %w", err)
		}
		attemptIDs = append(attemptIDs, attemptID)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate reviewer directory attempts: %w", err)
	}
	if len(attemptIDs) > 1 {
		return "", fmt.Errorf("reviewer directory attempts conflict: %w", ErrConflict)
	}
	if len(attemptIDs) == 0 {
		return "", nil
	}
	return attemptIDs[0], nil
}

// AcceptDirectoryCandidate appends reviewer authority only after proving the
// selected proposal, candidate, and directory snapshot.
func (store ReviewerStore) AcceptDirectoryCandidate(
	ctx context.Context,
	command ReviewerDirectoryDecisionCommand,
) (identity.ResolutionDecision, error) {
	if err := store.validate("accept reviewer directory candidate"); err != nil {
		return identity.ResolutionDecision{}, err
	}
	if !store.IncludeDirectory {
		return identity.ResolutionDecision{}, fmt.Errorf("accept reviewer directory candidate: optional directory scope is disabled")
	}
	if strings.TrimSpace(command.SnapshotID) == "" ||
		command.Decision.Outcome() != identity.DecisionAccepted ||
		command.Decision.Authority() != identity.AuthorityReviewer {
		return identity.ResolutionDecision{}, fmt.Errorf("accept reviewer directory candidate: canonical selection is invalid")
	}
	err := store.Database.InTransaction(ctx, func(transaction *Transaction) error {
		exact, err := transaction.exactResolutionDecisionRetry(
			ctx,
			command.Decision,
			command.Aliases,
		)
		if err != nil {
			return err
		}
		if exact {
			proof, err := loadReviewerDirectoryProof(
				ctx,
				transaction,
				command.Decision.ID(),
			)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("reviewer directory proof retry conflicts: %w", ErrConflict)
				}
				return fmt.Errorf("load exact reviewer directory proof: %w", err)
			}
			if proof.snapshotID != command.SnapshotID ||
				proof.candidateID == "" ||
				proof.entityID != command.Decision.EntityID() {
				return fmt.Errorf("reviewer directory proof retry conflicts: %w", ErrConflict)
			}
			return nil
		}
		if err := lockReviewerEligibleProposal(
			ctx,
			transaction,
			command.Decision.ProposalID(),
		); err != nil {
			return err
		}
		var mentionID string
		if err := transaction.QueryRow(ctx, `
			SELECT mention_id
			FROM stacks_core.resolution_proposals
			WHERE id = $1`,
			command.Decision.ProposalID(),
		).Scan(&mentionID); err != nil {
			return fmt.Errorf("load reviewer directory proposal: %w", err)
		}
		if err := lockDirectoryProposal(ctx, transaction, DirectoryPendingMention{
			MentionID: mentionID, ProposalID: string(command.Decision.ProposalID()),
		}); err != nil {
			return err
		}
		var (
			candidateID, sourceKind, sourceReference  string
			profileID, attemptID, provider, subjectID string
		)
		if err := transaction.QueryRow(ctx, `
			SELECT
				candidate.id,
				candidate.source_kind,
				candidate.source_reference,
				link.profile_id,
				link.lookup_attempt_id,
				profile.provider,
				profile.provider_subject_id
			FROM stacks_core.resolution_candidates AS candidate
			JOIN stacks_directory.entity_links AS link
			  ON link.candidate_id = candidate.id
			 AND link.proposal_id = candidate.proposal_id
			JOIN stacks_directory.profiles AS profile
			  ON profile.id = link.profile_id
			WHERE candidate.proposal_id = $1
			  AND link.snapshot_id = $2
			  AND link.decision_id IS NULL`,
			command.Decision.ProposalID(),
			command.SnapshotID,
		).Scan(
			&candidateID,
			&sourceKind,
			&sourceReference,
			&profileID,
			&attemptID,
			&provider,
			&subjectID,
		); err != nil {
			return fmt.Errorf("prove reviewer directory candidate: %w", err)
		}
		if sourceKind != directoryCandidateSourceKind || sourceReference != command.SnapshotID {
			return fmt.Errorf("prove reviewer directory candidate: source does not match")
		}
		if err := lockDirectoryProviderAuthority(
			ctx,
			transaction,
			provider,
			subjectID,
		); err != nil {
			return err
		}
		if err := requireDirectoryProviderOwner(
			ctx,
			transaction,
			provider,
			subjectID,
			string(command.Decision.EntityID()),
		); err != nil {
			return err
		}
		if _, err := loadEntityValue(ctx, transaction.transaction, command.Decision.EntityID()); err != nil {
			return fmt.Errorf("prove reviewer directory entity: %w", err)
		}
		if err := transaction.AppendResolutionDecision(ctx, command.Decision, command.Aliases); err != nil {
			return err
		}
		return persistDirectoryEntityLink(
			ctx,
			transaction,
			storedDirectoryProfile{
				profileID:  profileID,
				snapshotID: command.SnapshotID,
			},
			attemptID,
			string(command.Decision.ProposalID()),
			identity.CandidateID(candidateID),
			command.Decision.ID(),
			string(command.Decision.EntityID()),
			command.Decision.RecordedAt(),
		)
	})
	if err != nil {
		return identity.ResolutionDecision{}, fmt.Errorf("accept reviewer directory candidate: %w", err)
	}
	return command.Decision, nil
}

func (store ReviewerStore) validate(operation string) error {
	if store.Database == nil || store.Database.pool == nil {
		return fmt.Errorf("%s: database is not configured", operation)
	}
	return nil
}

func maskReviewerEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	local := []rune(email[:at])
	if len(local) == 0 {
		return ""
	}
	return string(local[0]) + "***" + email[at:]
}
