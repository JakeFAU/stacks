package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EntityInput is the minimum durable identity record. Name normalization and
// matching policy remain outside storage.
type EntityInput struct {
	ID          string
	Kind        string
	DisplayName string
}

// Entity is a stored canonical entity identifier.
type Entity struct {
	ID string
}

// AliasInput identifies an accepted normalized alias for an entity.
type AliasInput struct {
	EntityID        string
	NormalizedValue string
	Type            string
}

// EntityAlias is a stored alias identifier.
type EntityAlias struct {
	ID string
}

// MentionInput identifies a source-grounded entity reference.
type MentionInput struct {
	EvidenceSpanID string
	Surface        string
	Role           string
}

// Mention is a stored source-grounded entity reference.
type Mention struct {
	ID string
}

// ResolutionProposalInput identifies the mention awaiting review.
type ResolutionProposalInput struct {
	MentionID string
}

// ResolutionProposal is a stored resolution review item.
type ResolutionProposal struct {
	ID string
}

// ResolutionCandidateInput stores one deterministic candidate rank.
type ResolutionCandidateInput struct {
	ProposalID string
	EntityID   string
	Rank       int
	Confidence *float64
	Reason     string
}

// ResolutionCandidate is a stored proposal candidate.
type ResolutionCandidate struct {
	ID string
}

// ResolutionOutcome is the finite set of review outcomes persisted by storage.
type ResolutionOutcome string

const (
	ResolutionOutcomeAccepted ResolutionOutcome = "accepted"
	ResolutionOutcomeRejected ResolutionOutcome = "rejected"
	ResolutionOutcomeCreated  ResolutionOutcome = "created"
)

// ResolutionDecisionInput contains a review action. EntityID is required for
// accepted and created outcomes and absent for rejected outcomes.
type ResolutionDecisionInput struct {
	ProposalID string
	Outcome    ResolutionOutcome
	EntityID   string
}

// ResolutionDecision is an immutable review record.
type ResolutionDecision struct {
	ID           string
	ProposalID   string
	SupersedesID string
	Outcome      ResolutionOutcome
	EntityID     string
}

// EntityRepository owns storage transactions for resolution decisions so a
// correction never exposes two effective decisions.
type EntityRepository struct {
	pool *pgxpool.Pool
}

// NewEntityRepository creates an identity repository backed by pool.
func NewEntityRepository(pool *pgxpool.Pool) *EntityRepository {
	return &EntityRepository{pool: pool}
}

// CreateEntity persists a canonical entity.
func (repository *EntityRepository) CreateEntity(ctx context.Context, input EntityInput) (Entity, error) {
	if strings.TrimSpace(input.ID) == "" {
		return Entity{}, fmt.Errorf("create entity: ID is required")
	}
	if strings.TrimSpace(input.Kind) == "" {
		return Entity{}, fmt.Errorf("create entity: kind is required")
	}
	if strings.TrimSpace(input.DisplayName) == "" {
		return Entity{}, fmt.Errorf("create entity: display name is required")
	}
	var entity Entity
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO stacks.entities (id, kind, display_name, recorded_at)
		VALUES ($1::uuid, $2, $3, $4)
		ON CONFLICT (id) DO NOTHING
		RETURNING id`, input.ID, input.Kind, input.DisplayName, time.Now().UTC()).Scan(&entity.ID)
	if err == pgx.ErrNoRows {
		var storedKind, storedDisplayName string
		err = repository.pool.QueryRow(ctx, `
			SELECT id, kind, display_name FROM stacks.entities WHERE id = $1`, input.ID).Scan(&entity.ID, &storedKind, &storedDisplayName)
		if err != nil {
			return Entity{}, fmt.Errorf("load entity %q: %w", input.ID, err)
		}
		if storedKind != input.Kind || storedDisplayName != input.DisplayName {
			return Entity{}, fmt.Errorf("load entity %q: stored entity conflicts", input.ID)
		}
		return entity, nil
	}
	if err != nil {
		return Entity{}, fmt.Errorf("create entity %q: %w", input.ID, err)
	}
	return entity, nil
}

// PutAlias persists an alias once for its entity, type, and normalized value.
func (repository *EntityRepository) PutAlias(ctx context.Context, input AliasInput) (EntityAlias, error) {
	if strings.TrimSpace(input.EntityID) == "" || strings.TrimSpace(input.NormalizedValue) == "" {
		return EntityAlias{}, fmt.Errorf("put entity alias: entity ID and normalized value are required")
	}
	if input.Type != "name" && input.Type != "email" {
		return EntityAlias{}, fmt.Errorf("put entity alias for entity %q: type is invalid", input.EntityID)
	}
	var alias EntityAlias
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO stacks.entity_aliases (entity_id, normalized_value, alias_type, recorded_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (entity_id, normalized_value, alias_type) DO NOTHING
		RETURNING id`, input.EntityID, input.NormalizedValue, input.Type, time.Now().UTC()).Scan(&alias.ID)
	if err == pgx.ErrNoRows {
		err = repository.pool.QueryRow(ctx, `
			SELECT id FROM stacks.entity_aliases
			WHERE entity_id = $1 AND normalized_value = $2 AND alias_type = $3`, input.EntityID, input.NormalizedValue, input.Type).Scan(&alias.ID)
		if err != nil {
			return EntityAlias{}, fmt.Errorf("load alias for entity %q: %w", input.EntityID, err)
		}
		return alias, nil
	}
	if err != nil {
		return EntityAlias{}, fmt.Errorf("put alias for entity %q: %w", input.EntityID, err)
	}
	return alias, nil
}

// CreateMention persists a mention tied to exact source evidence.
func (repository *EntityRepository) CreateMention(ctx context.Context, input MentionInput) (Mention, error) {
	if strings.TrimSpace(input.EvidenceSpanID) == "" {
		return Mention{}, fmt.Errorf("create mention: evidence span ID is required")
	}
	if strings.TrimSpace(input.Surface) == "" {
		return Mention{}, fmt.Errorf("create mention: surface is required")
	}
	if input.Role != "speaker" && input.Role != "reference" {
		return Mention{}, fmt.Errorf("create mention: role is invalid")
	}
	var mention Mention
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO stacks.mentions (evidence_span_id, surface, role, recorded_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (evidence_span_id, surface, role) DO NOTHING
		RETURNING id`, input.EvidenceSpanID, input.Surface, input.Role, time.Now().UTC()).Scan(&mention.ID)
	if err == pgx.ErrNoRows {
		err = repository.pool.QueryRow(ctx, `
			SELECT id FROM stacks.mentions
			WHERE evidence_span_id = $1 AND surface = $2 AND role = $3`, input.EvidenceSpanID, input.Surface, input.Role).Scan(&mention.ID)
		if err != nil {
			return Mention{}, fmt.Errorf("load mention for evidence span %q: %w", input.EvidenceSpanID, err)
		}
		return mention, nil
	}
	if err != nil {
		return Mention{}, fmt.Errorf("create mention for evidence span %q: %w", input.EvidenceSpanID, err)
	}
	return mention, nil
}

// CreateResolutionProposal persists a pending proposal for one mention.
func (repository *EntityRepository) CreateResolutionProposal(ctx context.Context, input ResolutionProposalInput) (ResolutionProposal, error) {
	if strings.TrimSpace(input.MentionID) == "" {
		return ResolutionProposal{}, fmt.Errorf("create resolution proposal: mention ID is required")
	}
	var proposal ResolutionProposal
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO stacks.resolution_proposals (mention_id, derivation, recorded_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (mention_id) DO NOTHING
		RETURNING id`, input.MentionID, "review", time.Now().UTC()).Scan(&proposal.ID)
	if err == pgx.ErrNoRows {
		err = repository.pool.QueryRow(ctx, `
			SELECT id FROM stacks.resolution_proposals WHERE mention_id = $1`, input.MentionID).Scan(&proposal.ID)
		if err != nil {
			return ResolutionProposal{}, fmt.Errorf("load resolution proposal for mention %q: %w", input.MentionID, err)
		}
		return proposal, nil
	}
	if err != nil {
		return ResolutionProposal{}, fmt.Errorf("create resolution proposal for mention %q: %w", input.MentionID, err)
	}
	return proposal, nil
}

// PutCandidate persists one idempotent ranked resolution candidate.
func (repository *EntityRepository) PutCandidate(ctx context.Context, input ResolutionCandidateInput) (ResolutionCandidate, error) {
	if strings.TrimSpace(input.ProposalID) == "" || strings.TrimSpace(input.EntityID) == "" || input.Rank < 0 {
		return ResolutionCandidate{}, fmt.Errorf("put resolution candidate: proposal ID, entity ID, and non-negative rank are required")
	}
	var candidate ResolutionCandidate
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO stacks.resolution_candidates (proposal_id, entity_id, rank, confidence, reason)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (proposal_id, entity_id) DO NOTHING
		RETURNING id`, input.ProposalID, input.EntityID, input.Rank, input.Confidence, input.Reason).Scan(&candidate.ID)
	if err == pgx.ErrNoRows {
		var storedRank int
		var storedConfidence *float64
		var storedReason string
		err = repository.pool.QueryRow(ctx, `
			SELECT id, rank, confidence, reason FROM stacks.resolution_candidates
			WHERE proposal_id = $1 AND entity_id = $2`, input.ProposalID, input.EntityID).Scan(&candidate.ID, &storedRank, &storedConfidence, &storedReason)
		if err != nil {
			return ResolutionCandidate{}, fmt.Errorf("load resolution candidate for proposal %q: %w", input.ProposalID, err)
		}
		if storedRank != input.Rank || storedReason != input.Reason || !sameOptionalFloat(storedConfidence, input.Confidence) {
			return ResolutionCandidate{}, fmt.Errorf("load resolution candidate for proposal %q: stored candidate conflicts", input.ProposalID)
		}
		return candidate, nil
	}
	if err != nil {
		return ResolutionCandidate{}, fmt.Errorf("put resolution candidate for proposal %q: %w", input.ProposalID, err)
	}
	return candidate, nil
}

// RecordDecision adds the initial effective decision for a proposal.
func (repository *EntityRepository) RecordDecision(ctx context.Context, input ResolutionDecisionInput) (ResolutionDecision, error) {
	if err := validateDecisionInput(input); err != nil {
		return ResolutionDecision{}, err
	}
	var decision ResolutionDecision
	err := repository.withTransaction(ctx, func(transaction pgx.Tx) error {
		existing, err := loadEffectiveDecision(ctx, transaction, input.ProposalID)
		if err == nil {
			if existing.Outcome == input.Outcome && existing.EntityID == input.EntityID {
				decision = existing
				return nil
			}
			return fmt.Errorf("record resolution decision for proposal %q: effective decision conflicts", input.ProposalID)
		}
		if err != pgx.ErrNoRows {
			return fmt.Errorf("load effective resolution decision for proposal %q: %w", input.ProposalID, err)
		}
		var insertErr error
		decision, insertErr = insertDecision(ctx, transaction, uuid.NewString(), input, "")
		if insertErr != nil {
			return insertErr
		}
		return updateProposalStatus(ctx, transaction, input.ProposalID, input.Outcome)
	})
	if err != nil {
		return ResolutionDecision{}, err
	}
	return decision, nil
}

// CorrectDecision appends a replacement for one currently effective decision.
func (repository *EntityRepository) CorrectDecision(ctx context.Context, effectiveDecisionID string, input ResolutionDecisionInput) (ResolutionDecision, error) {
	if strings.TrimSpace(effectiveDecisionID) == "" {
		return ResolutionDecision{}, fmt.Errorf("correct resolution decision: effective decision ID is required")
	}
	var replacement ResolutionDecision
	err := repository.withTransaction(ctx, func(transaction pgx.Tx) error {
		var proposalID, supersededByID string
		err := transaction.QueryRow(ctx, `
			SELECT proposal_id, COALESCE(superseded_by_id::text, '')
			FROM stacks.resolution_decisions WHERE id = $1 FOR UPDATE`, effectiveDecisionID).Scan(&proposalID, &supersededByID)
		if err == pgx.ErrNoRows {
			return fmt.Errorf("correct resolution decision %q: decision does not exist", effectiveDecisionID)
		}
		if err != nil {
			return fmt.Errorf("lock resolution decision %q: %w", effectiveDecisionID, err)
		}
		if input.ProposalID != "" && input.ProposalID != proposalID {
			return fmt.Errorf("correct resolution decision %q: proposal ID does not match", effectiveDecisionID)
		}
		input.ProposalID = proposalID
		if err := validateDecisionInput(input); err != nil {
			return err
		}
		if supersededByID != "" {
			replacement, err = loadDecision(ctx, transaction, supersededByID)
			if err != nil {
				return fmt.Errorf("load correction for resolution decision %q: %w", effectiveDecisionID, err)
			}
			if replacement.Outcome == input.Outcome && replacement.EntityID == input.EntityID {
				return nil
			}
			return fmt.Errorf("correct resolution decision %q: existing correction conflicts", effectiveDecisionID)
		}
		replacementID := uuid.NewString()
		result, err := transaction.Exec(ctx, `
			UPDATE stacks.resolution_decisions
			SET superseded_by_id = $1
			WHERE id = $2 AND superseded_by_id IS NULL`, replacementID, effectiveDecisionID)
		if err != nil {
			return fmt.Errorf("supersede resolution decision %q: %w", effectiveDecisionID, err)
		}
		if result.RowsAffected() != 1 {
			return fmt.Errorf("supersede resolution decision %q: decision is not effective", effectiveDecisionID)
		}
		replacement, err = insertDecision(ctx, transaction, replacementID, input, effectiveDecisionID)
		if err != nil {
			return err
		}
		return updateProposalStatus(ctx, transaction, input.ProposalID, input.Outcome)
	})
	if err != nil {
		return ResolutionDecision{}, err
	}
	return replacement, nil
}

// EffectiveDecision returns the one unsuperseded decision for a proposal.
func (repository *EntityRepository) EffectiveDecision(ctx context.Context, proposalID string) (ResolutionDecision, error) {
	var decision ResolutionDecision
	err := repository.pool.QueryRow(ctx, `
		SELECT id, proposal_id, COALESCE(supersedes_id::text, ''), outcome, COALESCE(entity_id::text, '')
		FROM stacks.resolution_decisions
		WHERE proposal_id = $1 AND superseded_by_id IS NULL`, proposalID).Scan(&decision.ID, &decision.ProposalID, &decision.SupersedesID, &decision.Outcome, &decision.EntityID)
	if err != nil {
		return ResolutionDecision{}, fmt.Errorf("load effective resolution decision for proposal %q: %w", proposalID, err)
	}
	return decision, nil
}

func (repository *EntityRepository) withTransaction(ctx context.Context, work func(pgx.Tx) error) error {
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start resolution transaction: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.
	if err := work(transaction); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit resolution transaction: %w", err)
	}
	return nil
}

func validateDecisionInput(input ResolutionDecisionInput) error {
	if strings.TrimSpace(input.ProposalID) == "" {
		return fmt.Errorf("record resolution decision: proposal ID is required")
	}
	switch input.Outcome {
	case ResolutionOutcomeAccepted, ResolutionOutcomeCreated:
		if strings.TrimSpace(input.EntityID) == "" {
			return fmt.Errorf("record resolution decision for proposal %q: entity ID is required", input.ProposalID)
		}
	case ResolutionOutcomeRejected:
		if strings.TrimSpace(input.EntityID) != "" {
			return fmt.Errorf("record resolution decision for proposal %q: rejected decision must not include entity ID", input.ProposalID)
		}
	default:
		return fmt.Errorf("record resolution decision for proposal %q: outcome is invalid", input.ProposalID)
	}
	return nil
}

func insertDecision(ctx context.Context, transaction pgx.Tx, id string, input ResolutionDecisionInput, supersedesID string) (ResolutionDecision, error) {
	var decision ResolutionDecision
	err := transaction.QueryRow(ctx, `
		INSERT INTO stacks.resolution_decisions
			(id, proposal_id, outcome, entity_id, supersedes_id, recorded_at)
		VALUES ($1, $2, $3, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, $6)
		RETURNING id, proposal_id, COALESCE(supersedes_id::text, ''), outcome, COALESCE(entity_id::text, '')`,
		id, input.ProposalID, string(input.Outcome), input.EntityID, supersedesID, time.Now().UTC()).Scan(&decision.ID, &decision.ProposalID, &decision.SupersedesID, &decision.Outcome, &decision.EntityID)
	if err != nil {
		return ResolutionDecision{}, fmt.Errorf("record resolution decision for proposal %q: %w", input.ProposalID, err)
	}
	return decision, nil
}

func loadEffectiveDecision(ctx context.Context, transaction pgx.Tx, proposalID string) (ResolutionDecision, error) {
	var decision ResolutionDecision
	err := transaction.QueryRow(ctx, `
		SELECT id, proposal_id, COALESCE(supersedes_id::text, ''), outcome, COALESCE(entity_id::text, '')
		FROM stacks.resolution_decisions
		WHERE proposal_id = $1 AND superseded_by_id IS NULL
		FOR UPDATE`, proposalID).Scan(&decision.ID, &decision.ProposalID, &decision.SupersedesID, &decision.Outcome, &decision.EntityID)
	return decision, err
}

func loadDecision(ctx context.Context, transaction pgx.Tx, decisionID string) (ResolutionDecision, error) {
	var decision ResolutionDecision
	err := transaction.QueryRow(ctx, `
		SELECT id, proposal_id, COALESCE(supersedes_id::text, ''), outcome, COALESCE(entity_id::text, '')
		FROM stacks.resolution_decisions WHERE id = $1`, decisionID).Scan(
		&decision.ID, &decision.ProposalID, &decision.SupersedesID, &decision.Outcome, &decision.EntityID)
	return decision, err
}

func sameOptionalFloat(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func updateProposalStatus(ctx context.Context, transaction pgx.Tx, proposalID string, outcome ResolutionOutcome) error {
	status := "resolved"
	if outcome == ResolutionOutcomeRejected {
		status = "rejected"
	}
	result, err := transaction.Exec(ctx, `
		UPDATE stacks.resolution_proposals SET status = $1 WHERE id = $2`, status, proposalID)
	if err != nil {
		return fmt.Errorf("update resolution proposal %q status: %w", proposalID, err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("update resolution proposal %q status: proposal does not exist", proposalID)
	}
	return nil
}
