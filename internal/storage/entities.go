package storage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"stacks/internal/entity"
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
	ID         string
	RecordedAt time.Time
}

// EntityDetail is the private review projection of one canonical entity.
type EntityDetail struct {
	ID           string
	Kind         string
	DisplayName  string
	RecordedAt   time.Time
	Aliases      []string
	MentionCount int
	Evidence     []string
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
	Email          string
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

// ResolutionProposalDetail is the private review projection of a proposal.
type ResolutionProposalDetail struct {
	ID         string
	Context    string
	Candidates []ResolutionCandidateDetail
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

// ResolutionCandidateDetail is one ranked non-authoritative candidate.
type ResolutionCandidateDetail struct {
	EntityID   string
	Confidence *float64
	Reason     string
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

// CreateReviewPersonInput atomically creates a canonical person and accepts it
// for one pending proposal. Alias values must be normalized by domain policy.
type CreateReviewPersonInput struct {
	ProposalID  string
	EntityID    string
	Kind        string
	DisplayName string
	Aliases     []AliasInput
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

// EntitySnapshots returns canonical entities with accepted aliases only for
// deterministic ingestion-time resolution. Private display names and aliases
// never leave this in-process boundary through logs or telemetry.
func (repository *IngestionRepository) EntitySnapshots(ctx context.Context) ([]entity.EntitySnapshot, error) {
	if repository == nil || repository.pool == nil {
		return nil, fmt.Errorf("list ingestion entity snapshots: repository is not configured")
	}
	rows, err := repository.pool.Query(ctx, `
		SELECT id::text, kind, display_name, recorded_at
		FROM stacks.entities
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list ingestion entity snapshots: %w", err)
	}
	defer rows.Close()
	var snapshots []entity.EntitySnapshot
	for rows.Next() {
		var snapshot entity.EntitySnapshot
		var kind string
		if err := rows.Scan(&snapshot.ID, &kind, &snapshot.DisplayName, &snapshot.RecordedAt); err != nil {
			return nil, fmt.Errorf("scan ingestion entity snapshot: %w", err)
		}
		snapshot.Kind = entity.Kind(kind)
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ingestion entity snapshots: %w", err)
	}
	for index := range snapshots {
		aliases, err := repository.pool.Query(ctx, `
			SELECT DISTINCT assertion.alias_type, assertion.normalized_value
			FROM stacks.entity_alias_assertions AS assertion
			JOIN stacks.resolution_decisions AS decision ON decision.id = assertion.decision_id
			WHERE assertion.entity_id = $1
			  AND decision.entity_id = assertion.entity_id
			  AND decision.outcome IN ('accepted', 'created')
			  AND decision.superseded_by_id IS NULL
			  AND decision.currently_admissible
			ORDER BY alias_type, normalized_value`, snapshots[index].ID)
		if err != nil {
			return nil, fmt.Errorf("list ingestion entity aliases: %w", err)
		}
		for aliases.Next() {
			var aliasType, value string
			if err := aliases.Scan(&aliasType, &value); err != nil {
				aliases.Close()
				return nil, fmt.Errorf("scan ingestion entity alias: %w", err)
			}
			snapshots[index].Aliases = append(snapshots[index].Aliases, entity.Alias{Type: entity.AliasType(aliasType), Value: value})
		}
		if err := aliases.Err(); err != nil {
			aliases.Close()
			return nil, fmt.Errorf("iterate ingestion entity aliases: %w", err)
		}
		aliases.Close()
	}
	return snapshots, nil
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
		RETURNING id, recorded_at`, input.ID, input.Kind, input.DisplayName, time.Now().UTC()).Scan(&entity.ID, &entity.RecordedAt)
	if err == pgx.ErrNoRows {
		var storedKind, storedDisplayName string
		err = repository.pool.QueryRow(ctx, `
			SELECT id, kind, display_name, recorded_at FROM stacks.entities WHERE id = $1`, input.ID).Scan(&entity.ID, &storedKind, &storedDisplayName, &entity.RecordedAt)
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
	if err := validateAliasInput(input); err != nil {
		return EntityAlias{}, fmt.Errorf("put entity alias for entity %q: alias is invalid: %w", input.EntityID, err)
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
	if strings.TrimSpace(input.Email) != "" && !entity.ValidEmail(input.Email) {
		return Mention{}, fmt.Errorf("create mention: email is invalid")
	}
	normalizedName := entity.NormalizeName(input.Surface)
	normalizedEmail := entity.NormalizeEmail(input.Email)
	var mention Mention
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO stacks.mentions
			(evidence_span_id, surface, normalized_name, normalized_email, role, recorded_at, currently_admissible)
		VALUES ($1, $2, $3, $4, $5, $6, true)
		ON CONFLICT (evidence_span_id, surface, role) WHERE extraction_run_id IS NULL DO UPDATE
		SET normalized_name = EXCLUDED.normalized_name,
			normalized_email = EXCLUDED.normalized_email
		RETURNING id`, input.EvidenceSpanID, input.Surface, normalizedName, normalizedEmail, input.Role, time.Now().UTC()).Scan(&mention.ID)
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
	return repository.recordDecision(ctx, input, false)
}

// RecordReviewDecision appends an initial review decision and rejects proposals
// that already have effective state. Retryable processing should use
// RecordDecision instead, which retains idempotency semantics.
func (repository *EntityRepository) RecordReviewDecision(ctx context.Context, input ResolutionDecisionInput) (ResolutionDecision, error) {
	return repository.recordDecision(ctx, input, true)
}

func (repository *EntityRepository) recordDecision(ctx context.Context, input ResolutionDecisionInput, strict bool) (ResolutionDecision, error) {
	if err := validateDecisionInput(input); err != nil {
		return ResolutionDecision{}, err
	}
	var decision ResolutionDecision
	err := repository.withTransaction(ctx, func(transaction pgx.Tx) error {
		existing, err := loadEffectiveDecision(ctx, transaction, input.ProposalID)
		if err == nil {
			if !strict && existing.Outcome == input.Outcome && existing.EntityID == input.EntityID {
				decision = existing
				return nil
			}
			return fmt.Errorf("record resolution decision for proposal %q: proposal already has an effective decision", input.ProposalID)
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
	return repository.correctDecision(ctx, effectiveDecisionID, input, false)
}

// CorrectReviewDecision appends a replacement only when the named decision is
// still effective. It never treats a stale command as a successful retry.
func (repository *EntityRepository) CorrectReviewDecision(ctx context.Context, effectiveDecisionID string, input ResolutionDecisionInput) (ResolutionDecision, error) {
	return repository.correctDecision(ctx, effectiveDecisionID, input, true)
}

func (repository *EntityRepository) correctDecision(ctx context.Context, effectiveDecisionID string, input ResolutionDecisionInput, strict bool) (ResolutionDecision, error) {
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
			if strict {
				return fmt.Errorf("correct resolution decision %q: decision is not effective", effectiveDecisionID)
			}
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

// ListEntityDetails returns canonical people for private local review.
func (repository *EntityRepository) ListEntityDetails(ctx context.Context) ([]EntityDetail, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id::text, kind, display_name, recorded_at
		FROM stacks.entities
		ORDER BY display_name, id`)
	if err != nil {
		return nil, fmt.Errorf("list entities: %w", err)
	}
	defer rows.Close()
	entities := make([]EntityDetail, 0)
	for rows.Next() {
		var detail EntityDetail
		if err := rows.Scan(&detail.ID, &detail.Kind, &detail.DisplayName, &detail.RecordedAt); err != nil {
			return nil, fmt.Errorf("scan entity: %w", err)
		}
		entities = append(entities, detail)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entities: %w", err)
	}
	for index := range entities {
		detail, err := repository.ShowEntityDetail(ctx, entities[index].ID)
		if err != nil {
			return nil, err
		}
		entities[index] = detail
	}
	return entities, nil
}

// ShowEntityDetail returns accepted aliases, mention count, and cited private
// source context for one canonical entity.
func (repository *EntityRepository) ShowEntityDetail(ctx context.Context, entityID string) (EntityDetail, error) {
	var detail EntityDetail
	err := repository.pool.QueryRow(ctx, `
		SELECT id::text, kind, display_name, recorded_at
		FROM stacks.entities WHERE id = $1`, entityID).Scan(&detail.ID, &detail.Kind, &detail.DisplayName, &detail.RecordedAt)
	if err != nil {
		return EntityDetail{}, fmt.Errorf("show entity %q: %w", entityID, err)
	}
	aliases, err := repository.pool.Query(ctx, `
		SELECT DISTINCT assertion.normalized_value
		FROM stacks.entity_alias_assertions AS assertion
		JOIN stacks.resolution_decisions AS decision ON decision.id = assertion.decision_id
		WHERE assertion.entity_id = $1
		  AND decision.entity_id = assertion.entity_id
		  AND decision.outcome IN ('accepted', 'created')
		  AND decision.superseded_by_id IS NULL
		  AND decision.currently_admissible
		ORDER BY assertion.normalized_value`, entityID)
	if err != nil {
		return EntityDetail{}, fmt.Errorf("list aliases for entity %q: %w", entityID, err)
	}
	defer aliases.Close()
	for aliases.Next() {
		var alias string
		if err := aliases.Scan(&alias); err != nil {
			return EntityDetail{}, fmt.Errorf("scan alias for entity %q: %w", entityID, err)
		}
		detail.Aliases = append(detail.Aliases, alias)
	}
	if err := aliases.Err(); err != nil {
		return EntityDetail{}, fmt.Errorf("iterate aliases for entity %q: %w", entityID, err)
	}
	if err := repository.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM stacks.resolution_decisions AS decision
		JOIN stacks.resolution_proposals AS proposal ON proposal.id = decision.proposal_id
		JOIN stacks.mentions AS mention ON mention.id = proposal.mention_id
		LEFT JOIN stacks.extraction_runs AS extraction_run ON extraction_run.id = mention.extraction_run_id
		WHERE decision.entity_id = $1 AND decision.outcome IN ('accepted', 'created')
		  AND decision.superseded_by_id IS NULL AND decision.currently_admissible
		  AND mention.currently_admissible
		  AND (mention.extraction_run_id IS NULL OR extraction_run.currently_admissible)`, entityID).Scan(&detail.MentionCount); err != nil {
		return EntityDetail{}, fmt.Errorf("count mentions for entity %q: %w", entityID, err)
	}
	evidence, err := repository.pool.Query(ctx, `
		SELECT DISTINCT span.quote
		FROM stacks.resolution_decisions AS decision
		JOIN stacks.resolution_proposals AS proposal ON proposal.id = decision.proposal_id
		JOIN stacks.mentions AS mention ON mention.id = proposal.mention_id
		LEFT JOIN stacks.extraction_runs AS extraction_run ON extraction_run.id = mention.extraction_run_id
		JOIN stacks.evidence_spans AS span ON span.id = mention.evidence_span_id
		WHERE decision.entity_id = $1 AND decision.outcome IN ('accepted', 'created')
		  AND decision.superseded_by_id IS NULL AND decision.currently_admissible
		  AND mention.currently_admissible
		  AND (mention.extraction_run_id IS NULL OR extraction_run.currently_admissible)
		ORDER BY span.quote`, entityID)
	if err != nil {
		return EntityDetail{}, fmt.Errorf("list evidence for entity %q: %w", entityID, err)
	}
	defer evidence.Close()
	for evidence.Next() {
		var quote string
		if err := evidence.Scan(&quote); err != nil {
			return EntityDetail{}, fmt.Errorf("scan evidence for entity %q: %w", entityID, err)
		}
		detail.Evidence = append(detail.Evidence, quote)
	}
	if err := evidence.Err(); err != nil {
		return EntityDetail{}, fmt.Errorf("iterate evidence for entity %q: %w", entityID, err)
	}
	return detail, nil
}

// ListResolutionProposalDetails returns pending proposals for local review.
func (repository *EntityRepository) ListResolutionProposalDetails(ctx context.Context) ([]ResolutionProposalDetail, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT proposal.id::text, span.quote
		FROM stacks.resolution_proposals AS proposal
		JOIN stacks.mentions AS mention ON mention.id = proposal.mention_id
		LEFT JOIN stacks.extraction_runs AS extraction_run ON extraction_run.id = mention.extraction_run_id
		JOIN stacks.evidence_spans AS span ON span.id = mention.evidence_span_id
		WHERE proposal.status = 'pending'
		  AND mention.currently_admissible
		  AND (mention.extraction_run_id IS NULL OR extraction_run.currently_admissible)
		ORDER BY proposal.recorded_at, proposal.id`)
	if err != nil {
		return nil, fmt.Errorf("list resolution proposals: %w", err)
	}
	defer rows.Close()
	var proposals []ResolutionProposalDetail
	for rows.Next() {
		var proposal ResolutionProposalDetail
		if err := rows.Scan(&proposal.ID, &proposal.Context); err != nil {
			return nil, fmt.Errorf("scan resolution proposal: %w", err)
		}
		candidates, err := repository.listResolutionCandidateDetails(ctx, proposal.ID)
		if err != nil {
			return nil, err
		}
		proposal.Candidates = candidates
		proposals = append(proposals, proposal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resolution proposals: %w", err)
	}
	return proposals, nil
}

// ShowResolutionProposalDetail returns one proposal, including private cited
// context and the ranked candidate list.
func (repository *EntityRepository) ShowResolutionProposalDetail(ctx context.Context, proposalID string) (ResolutionProposalDetail, error) {
	var proposal ResolutionProposalDetail
	err := repository.pool.QueryRow(ctx, `
		SELECT proposal.id::text, span.quote
		FROM stacks.resolution_proposals AS proposal
		JOIN stacks.mentions AS mention ON mention.id = proposal.mention_id
		LEFT JOIN stacks.extraction_runs AS extraction_run ON extraction_run.id = mention.extraction_run_id
		JOIN stacks.evidence_spans AS span ON span.id = mention.evidence_span_id
		WHERE proposal.id = $1
		  AND mention.currently_admissible
		  AND (mention.extraction_run_id IS NULL OR extraction_run.currently_admissible)`, proposalID).Scan(&proposal.ID, &proposal.Context)
	if err != nil {
		return ResolutionProposalDetail{}, fmt.Errorf("show resolution proposal %q: %w", proposalID, err)
	}
	candidates, err := repository.listResolutionCandidateDetails(ctx, proposalID)
	if err != nil {
		return ResolutionProposalDetail{}, err
	}
	proposal.Candidates = candidates
	return proposal, nil
}

func (repository *EntityRepository) listResolutionCandidateDetails(ctx context.Context, proposalID string) ([]ResolutionCandidateDetail, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT entity_id::text, confidence, reason FROM stacks.resolution_candidates
		WHERE proposal_id = $1 ORDER BY rank, entity_id`, proposalID)
	if err != nil {
		return nil, fmt.Errorf("list candidates for resolution proposal %q: %w", proposalID, err)
	}
	defer rows.Close()
	var candidates []ResolutionCandidateDetail
	for rows.Next() {
		var candidate ResolutionCandidateDetail
		if err := rows.Scan(&candidate.EntityID, &candidate.Confidence, &candidate.Reason); err != nil {
			return nil, fmt.Errorf("scan candidate for resolution proposal %q: %w", proposalID, err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidates for resolution proposal %q: %w", proposalID, err)
	}
	return candidates, nil
}

// CreateReviewPerson atomically creates a person, records accepted aliases,
// and appends the initial created decision for its proposal.
func (repository *EntityRepository) CreateReviewPerson(ctx context.Context, input CreateReviewPersonInput) (Entity, ResolutionDecision, error) {
	if strings.TrimSpace(input.ProposalID) == "" || strings.TrimSpace(input.EntityID) == "" || strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.DisplayName) == "" {
		return Entity{}, ResolutionDecision{}, fmt.Errorf("create review person: proposal ID, entity ID, kind, and display name are required")
	}
	var entity Entity
	var decision ResolutionDecision
	err := repository.withTransaction(ctx, func(transaction pgx.Tx) error {
		if err := transaction.QueryRow(ctx, `
			INSERT INTO stacks.entities (id, kind, display_name, recorded_at)
			VALUES ($1::uuid, $2, $3, $4)
			RETURNING id, recorded_at`, input.EntityID, input.Kind, input.DisplayName, time.Now().UTC()).Scan(&entity.ID, &entity.RecordedAt); err != nil {
			return fmt.Errorf("create review entity %q: %w", input.EntityID, err)
		}
		for _, alias := range input.Aliases {
			if alias.EntityID != "" && alias.EntityID != entity.ID {
				return fmt.Errorf("create review person %q: alias entity ID does not match", entity.ID)
			}
			if err := validateAliasInput(alias); err != nil {
				return fmt.Errorf("create review person %q: alias is invalid", entity.ID)
			}
		}
		if _, err := loadEffectiveDecision(ctx, transaction, input.ProposalID); err == nil {
			return fmt.Errorf("create review person for proposal %q: proposal already has an effective decision", input.ProposalID)
		} else if err != pgx.ErrNoRows {
			return fmt.Errorf("load effective resolution decision for proposal %q: %w", input.ProposalID, err)
		}
		var err error
		decision, err = insertDecision(ctx, transaction, uuid.NewString(), ResolutionDecisionInput{
			ProposalID: input.ProposalID,
			Outcome:    ResolutionOutcomeCreated,
			EntityID:   entity.ID,
		}, "")
		if err != nil {
			return err
		}
		for _, alias := range input.Aliases {
			if err := insertAliasAssertion(ctx, transaction, decision.ID, entity.ID, alias); err != nil {
				return fmt.Errorf("create alias for review entity %q: %w", entity.ID, err)
			}
		}
		return updateProposalStatus(ctx, transaction, input.ProposalID, ResolutionOutcomeCreated)
	})
	if err != nil {
		return Entity{}, ResolutionDecision{}, err
	}
	return entity, decision, nil
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
	digest := resolutionDecisionDigest(input, supersedesID)
	err := transaction.QueryRow(ctx, `
		INSERT INTO stacks.resolution_decisions
			(id, proposal_id, outcome, entity_id, supersedes_id, digest, recorded_at, currently_admissible)
		VALUES ($1, $2, $3, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, $6, $7, true)
		RETURNING id, proposal_id, COALESCE(supersedes_id::text, ''), outcome, COALESCE(entity_id::text, '')`,
		id, input.ProposalID, string(input.Outcome), input.EntityID, supersedesID, digest[:], time.Now().UTC()).Scan(&decision.ID, &decision.ProposalID, &decision.SupersedesID, &decision.Outcome, &decision.EntityID)
	if err != nil {
		return ResolutionDecision{}, fmt.Errorf("record resolution decision for proposal %q: %w", input.ProposalID, err)
	}
	if err := insertMentionAliasAssertions(ctx, transaction, decision); err != nil {
		return ResolutionDecision{}, err
	}
	return decision, nil
}

func insertMentionAliasAssertions(ctx context.Context, transaction pgx.Tx, decision ResolutionDecision) error {
	if decision.Outcome != ResolutionOutcomeAccepted && decision.Outcome != ResolutionOutcomeCreated {
		return nil
	}
	var normalizedName string
	var mentionAdmissible bool
	if err := transaction.QueryRow(ctx, `
		SELECT mention.normalized_name, mention.currently_admissible
		FROM stacks.resolution_proposals AS proposal
		JOIN stacks.mentions AS mention ON mention.id = proposal.mention_id
		WHERE proposal.id = $1`, decision.ProposalID).Scan(&normalizedName, &mentionAdmissible); err != nil {
		return fmt.Errorf("load accepted aliases for resolution proposal %q: %w", decision.ProposalID, err)
	}
	if !mentionAdmissible {
		return nil
	}
	if normalizedName == "" {
		return nil
	}
	alias := AliasInput{EntityID: decision.EntityID, NormalizedValue: normalizedName, Type: "name"}
	if err := insertAliasAssertion(ctx, transaction, decision.ID, decision.EntityID, alias); err != nil {
		return fmt.Errorf("record accepted alias for resolution proposal %q: %w", decision.ProposalID, err)
	}
	return nil
}

func insertAliasAssertion(ctx context.Context, transaction pgx.Tx, decisionID, entityID string, alias AliasInput) error {
	if alias.EntityID != "" && alias.EntityID != entityID {
		return fmt.Errorf("alias entity ID does not match decision")
	}
	if err := validateAliasInput(alias); err != nil {
		return err
	}
	_, err := transaction.Exec(ctx, `
		INSERT INTO stacks.entity_alias_assertions
			(decision_id, entity_id, normalized_value, alias_type, recorded_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		ON CONFLICT (decision_id, normalized_value, alias_type) DO NOTHING`,
		decisionID, entityID, alias.NormalizedValue, alias.Type, time.Now().UTC())
	return err
}

func validateAliasInput(alias AliasInput) error {
	if strings.TrimSpace(alias.NormalizedValue) == "" {
		return fmt.Errorf("normalized alias value is required")
	}
	switch alias.Type {
	case "name":
		if entity.NormalizeName(alias.NormalizedValue) != alias.NormalizedValue {
			return fmt.Errorf("name alias is not normalized")
		}
	case "email":
		if entity.NormalizeEmail(alias.NormalizedValue) != alias.NormalizedValue || !entity.ValidEmail(alias.NormalizedValue) {
			return fmt.Errorf("email alias is not normalized")
		}
	default:
		return fmt.Errorf("alias type is invalid")
	}
	return nil
}

func resolutionDecisionDigest(input ResolutionDecisionInput, supersedesID string) [sha256.Size]byte {
	return sha256.Sum256([]byte(strings.Join([]string{input.ProposalID, string(input.Outcome), input.EntityID, supersedesID}, "\x00")))
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
