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
	Kind        string
	DisplayName string
}

// Entity is a stored canonical entity identifier.
type Entity struct {
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
	if strings.TrimSpace(input.Kind) == "" {
		return Entity{}, fmt.Errorf("create entity: kind is required")
	}
	if strings.TrimSpace(input.DisplayName) == "" {
		return Entity{}, fmt.Errorf("create entity: display name is required")
	}
	var entity Entity
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO stacks.entities (kind, display_name, recorded_at)
		VALUES ($1, $2, $3)
		RETURNING id`, input.Kind, input.DisplayName, time.Now().UTC()).Scan(&entity.ID)
	if err != nil {
		return Entity{}, fmt.Errorf("create entity: %w", err)
	}
	return entity, nil
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
		RETURNING id`, input.EvidenceSpanID, input.Surface, input.Role, time.Now().UTC()).Scan(&mention.ID)
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
		RETURNING id`, input.MentionID, "review", time.Now().UTC()).Scan(&proposal.ID)
	if err != nil {
		return ResolutionProposal{}, fmt.Errorf("create resolution proposal for mention %q: %w", input.MentionID, err)
	}
	return proposal, nil
}

// RecordDecision adds the initial effective decision for a proposal.
func (repository *EntityRepository) RecordDecision(ctx context.Context, input ResolutionDecisionInput) (ResolutionDecision, error) {
	if err := validateDecisionInput(input); err != nil {
		return ResolutionDecision{}, err
	}
	var decision ResolutionDecision
	err := repository.withTransaction(ctx, func(transaction pgx.Tx) error {
		var err error
		decision, err = insertDecision(ctx, transaction, uuid.NewString(), input, "")
		if err != nil {
			return err
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
		var proposalID string
		err := transaction.QueryRow(ctx, `
			SELECT proposal_id FROM stacks.resolution_decisions
			WHERE id = $1 AND superseded_by_id IS NULL
			FOR UPDATE`, effectiveDecisionID).Scan(&proposalID)
		if err == pgx.ErrNoRows {
			return fmt.Errorf("correct resolution decision %q: decision is not effective", effectiveDecisionID)
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
		SELECT id, proposal_id, COALESCE(supersedes_id::text, '')
		FROM stacks.resolution_decisions
		WHERE proposal_id = $1 AND superseded_by_id IS NULL`, proposalID).Scan(&decision.ID, &decision.ProposalID, &decision.SupersedesID)
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
		RETURNING id, proposal_id, COALESCE(supersedes_id::text, '')`,
		id, input.ProposalID, string(input.Outcome), input.EntityID, supersedesID, time.Now().UTC()).Scan(&decision.ID, &decision.ProposalID, &decision.SupersedesID)
	if err != nil {
		return ResolutionDecision{}, fmt.Errorf("record resolution decision for proposal %q: %w", input.ProposalID, err)
	}
	return decision, nil
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
