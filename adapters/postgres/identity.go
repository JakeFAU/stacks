package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/timepoint"
	"github.com/jackc/pgx/v5"
)

const (
	identityAuthorityLockNamespace = "github.com/JakeFAU/stacks/postgres-identity-authority/v1"
	uniqueExactWorkEmailReasonCode = "unique_exact_work_email"
)

// EntityRecord is a canonical entity plus only its current effective identity
// authority and grounding provenance.
type EntityRecord struct {
	Entity              identity.Entity
	Aliases             []identity.AliasAssertion
	GroundingMentionIDs []identity.MentionID
	EvidenceIDs         []evidence.EvidenceID
}

// ResolutionProposalRecord is canonical review state without provider or
// presentation DTOs.
type ResolutionProposalRecord struct {
	Proposal          identity.ResolutionProposal
	Candidates        []identity.ResolutionCandidate
	EffectiveDecision *identity.ResolutionDecision
}

// PutEntity stores one immutable canonical entity. It returns false for an
// exact retry.
func (transaction *Transaction) PutEntity(
	ctx context.Context,
	entity identity.Entity,
) (bool, error) {
	if err := contextRequired(ctx, "put entity"); err != nil {
		return false, err
	}
	if transaction == nil || transaction.transaction == nil {
		return false, fmt.Errorf("put entity: transaction is closed")
	}
	if err := validateEntity(entity); err != nil {
		return false, fmt.Errorf("put entity: %w", err)
	}
	var insertedID string
	err := transaction.transaction.QueryRow(ctx, `
		INSERT INTO stacks_core.entities (id, kind, display_name, recorded_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO NOTHING
		RETURNING id`,
		entity.ID(),
		entity.Kind(),
		entity.DisplayName(),
		entity.RecordedAt(),
	).Scan(&insertedID)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, pgx.ErrNoRows):
		stored, loadErr := loadEntityValue(ctx, transaction.transaction, entity.ID())
		if loadErr != nil {
			return false, wrapIdentityError(ctx, "load existing entity", loadErr)
		}
		if !sameEntity(stored, entity) {
			return false, fmt.Errorf("put entity: immutable identity: %w", ErrConflict)
		}
		return false, nil
	default:
		return false, wrapIdentityError(ctx, "insert entity", conflictError(err))
	}
}

// PutMention stores one immutable source-grounded mention. It returns false
// for an exact retry.
func (transaction *Transaction) PutMention(
	ctx context.Context,
	mention identity.MentionRecord,
) (bool, error) {
	if err := contextRequired(ctx, "put mention"); err != nil {
		return false, err
	}
	if transaction == nil || transaction.transaction == nil {
		return false, fmt.Errorf("put mention: transaction is closed")
	}
	if err := validateMention(mention); err != nil {
		return false, fmt.Errorf("put mention: %w", err)
	}
	var proposedEmailEvidenceID any
	if mention.ProposedEmailEvidenceID() != "" {
		proposedEmailEvidenceID = mention.ProposedEmailEvidenceID()
	}
	var insertedID string
	err := transaction.transaction.QueryRow(ctx, `
		INSERT INTO stacks_core.mentions (
			id, evidence_id, derivation_run_id, surface, normalized_name,
			proposed_email, proposed_email_evidence_id, role, recorded_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO NOTHING
		RETURNING id`,
		mention.ID(),
		mention.EvidenceID(),
		mention.DerivationRunID(),
		mention.Surface(),
		mention.NormalizedName(),
		mention.ProposedEmail(),
		proposedEmailEvidenceID,
		mention.Role(),
		mention.RecordedAt(),
	).Scan(&insertedID)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, pgx.ErrNoRows):
		stored, loadErr := loadMentionValue(ctx, transaction.transaction, mention.ID())
		if loadErr != nil {
			return false, wrapIdentityError(ctx, "load existing mention", loadErr)
		}
		if !sameMention(stored, mention) {
			return false, fmt.Errorf("put mention: immutable identity: %w", ErrConflict)
		}
		return false, nil
	default:
		return false, wrapIdentityError(ctx, "insert mention", conflictError(err))
	}
}

// PutResolutionProposal stores one immutable proposal and its ordered evidence
// links. It returns false for an exact retry.
func (transaction *Transaction) PutResolutionProposal(
	ctx context.Context,
	proposal identity.ResolutionProposal,
) (bool, error) {
	if err := contextRequired(ctx, "put resolution proposal"); err != nil {
		return false, err
	}
	if transaction == nil || transaction.transaction == nil {
		return false, fmt.Errorf("put resolution proposal: transaction is closed")
	}
	if err := validateResolutionProposal(proposal); err != nil {
		return false, fmt.Errorf("put resolution proposal: %w", err)
	}
	var insertedID string
	err := transaction.transaction.QueryRow(ctx, `
		INSERT INTO stacks_core.resolution_proposals (
			id, mention_id, reason_code, recorded_at
		)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO NOTHING
		RETURNING id`,
		proposal.ID(),
		proposal.MentionID(),
		proposal.ReasonCode(),
		proposal.RecordedAt(),
	).Scan(&insertedID)
	switch {
	case err == nil:
		for index, evidenceID := range proposal.EvidenceIDs() {
			if _, insertErr := transaction.transaction.Exec(ctx, `
				INSERT INTO stacks_core.resolution_proposal_evidence (
					proposal_id, evidence_id, evidence_order
				)
				VALUES ($1, $2, $3)`,
				proposal.ID(),
				evidenceID,
				index,
			); insertErr != nil {
				return false, wrapIdentityError(
					ctx,
					"insert resolution proposal evidence",
					conflictError(insertErr),
				)
			}
		}
		return true, nil
	case errors.Is(err, pgx.ErrNoRows):
		stored, loadErr := loadResolutionProposalValue(
			ctx,
			transaction.transaction,
			proposal.ID(),
		)
		if loadErr != nil {
			return false, wrapIdentityError(ctx, "load existing resolution proposal", loadErr)
		}
		if !sameResolutionProposal(stored, proposal) {
			return false, fmt.Errorf(
				"put resolution proposal: immutable identity: %w",
				ErrConflict,
			)
		}
		return false, nil
	default:
		return false, wrapIdentityError(
			ctx,
			"insert resolution proposal",
			conflictError(err),
		)
	}
}

// PutResolutionCandidate stores one immutable ranked candidate. It returns
// false for an exact retry.
func (transaction *Transaction) PutResolutionCandidate(
	ctx context.Context,
	candidate identity.ResolutionCandidate,
) (bool, error) {
	if err := contextRequired(ctx, "put resolution candidate"); err != nil {
		return false, err
	}
	if transaction == nil || transaction.transaction == nil {
		return false, fmt.Errorf("put resolution candidate: transaction is closed")
	}
	if err := validateResolutionCandidate(candidate); err != nil {
		return false, fmt.Errorf("put resolution candidate: %w", err)
	}
	var insertedID string
	source := candidate.Source()
	err := transaction.transaction.QueryRow(ctx, `
		INSERT INTO stacks_core.resolution_candidates (
			id, proposal_id, entity_id, candidate_rank, confidence,
			reason_code, source_kind, source_reference, recorded_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO NOTHING
		RETURNING id`,
		candidate.ID(),
		candidate.ProposalID(),
		candidate.EntityID(),
		candidate.Rank(),
		candidate.Confidence(),
		candidate.ReasonCode(),
		source.Kind,
		source.Reference,
		candidate.RecordedAt(),
	).Scan(&insertedID)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, pgx.ErrNoRows):
		stored, loadErr := loadResolutionCandidateValue(
			ctx,
			transaction.transaction,
			candidate.ID(),
		)
		if loadErr != nil {
			return false, wrapIdentityError(ctx, "load existing resolution candidate", loadErr)
		}
		if !sameResolutionCandidate(stored, candidate) {
			return false, fmt.Errorf(
				"put resolution candidate: immutable identity: %w",
				ErrConflict,
			)
		}
		return false, nil
	default:
		return false, wrapIdentityError(
			ctx,
			"insert resolution candidate",
			conflictError(err),
		)
	}
}

// AppendResolutionDecision appends one authority decision and its accepted
// aliases without rewriting any predecessor.
func (transaction *Transaction) AppendResolutionDecision(
	ctx context.Context,
	decision identity.ResolutionDecision,
	aliases []identity.AliasAssertion,
) error {
	if err := contextRequired(ctx, "append resolution decision"); err != nil {
		return err
	}
	if transaction == nil || transaction.transaction == nil {
		return fmt.Errorf("append resolution decision: transaction is closed")
	}
	if err := validateResolutionDecision(decision); err != nil {
		return fmt.Errorf("append resolution decision: %w", err)
	}
	if err := validateDecisionAliases(decision, aliases); err != nil {
		return fmt.Errorf("append resolution decision: %w", err)
	}

	if exact, err := transaction.exactResolutionDecisionRetry(ctx, decision, aliases); err != nil {
		return err
	} else if exact {
		return nil
	}
	var proposalID string
	if _, err := transaction.transaction.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		identityAuthorityLockNamespace,
		decision.ProposalID(),
	); err != nil {
		return wrapIdentityError(ctx, "lock resolution proposal authority", err)
	}
	if err := transaction.transaction.QueryRow(ctx, `
		SELECT id
		FROM stacks_core.resolution_proposals
		WHERE id = $1`,
		decision.ProposalID(),
	).Scan(&proposalID); err != nil {
		return wrapIdentityError(ctx, "lock resolution proposal", err)
	}
	if exact, err := transaction.exactResolutionDecisionRetry(ctx, decision, aliases); err != nil {
		return err
	} else if exact {
		return nil
	}
	if decision.Authority() == identity.AuthorityAutomatic {
		if err := transaction.validateAutomaticResolutionAuthority(ctx, decision); err != nil {
			return err
		}
	}

	effective, effectiveErr := loadEffectiveResolutionDecisionValue(
		ctx,
		transaction.transaction,
		decision.ProposalID(),
	)
	switch {
	case decision.SupersedesID() == "" && effectiveErr == nil:
		return fmt.Errorf("append resolution decision: proposal already decided: %w", ErrConflict)
	case decision.SupersedesID() == "" && !errors.Is(effectiveErr, pgx.ErrNoRows):
		return wrapIdentityError(ctx, "load effective resolution decision", effectiveErr)
	case decision.SupersedesID() != "" && errors.Is(effectiveErr, pgx.ErrNoRows):
		return fmt.Errorf("append resolution decision: predecessor is not effective: %w", ErrConflict)
	case decision.SupersedesID() != "" && effectiveErr != nil:
		return wrapIdentityError(ctx, "load effective resolution decision", effectiveErr)
	case decision.SupersedesID() != "" && effective.ID() != decision.SupersedesID():
		return fmt.Errorf("append resolution decision: predecessor is not effective: %w", ErrConflict)
	case decision.SupersedesID() != "" && decision.Authority() != identity.AuthorityReviewer:
		return fmt.Errorf("append resolution decision: corrections require reviewer authority: %w", ErrConflict)
	}

	var entityID any
	if decision.EntityID() != "" {
		entityID = decision.EntityID()
	}
	var supersedesID any
	if decision.SupersedesID() != "" {
		supersedesID = decision.SupersedesID()
	}
	digest := decision.Digest()
	if _, err := transaction.transaction.Exec(ctx, `
		INSERT INTO stacks_core.resolution_decisions (
			id, proposal_id, outcome, entity_id, authority, reason_code,
			recorded_at, supersedes_id, digest_version, digest
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		decision.ID(),
		decision.ProposalID(),
		decision.Outcome(),
		entityID,
		decision.Authority(),
		decision.ReasonCode(),
		decision.RecordedAt(),
		supersedesID,
		decision.DigestVersion(),
		digest[:],
	); err != nil {
		return wrapIdentityError(ctx, "insert resolution decision", conflictError(err))
	}
	for _, assertion := range aliases {
		alias := assertion.Alias()
		if _, err := transaction.transaction.Exec(ctx, `
			INSERT INTO stacks_core.entity_alias_assertions (
				id, decision_id, entity_id, alias_type, alias_value, recorded_at
			)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			assertion.ID(),
			assertion.DecisionID(),
			assertion.EntityID(),
			alias.Type,
			alias.Value,
			assertion.RecordedAt(),
		); err != nil {
			return wrapIdentityError(ctx, "insert entity alias assertion", conflictError(err))
		}
	}
	return nil
}

func (transaction *Transaction) validateAutomaticResolutionAuthority(
	ctx context.Context,
	decision identity.ResolutionDecision,
) error {
	if decision.Outcome() != identity.DecisionAccepted || decision.SupersedesID() != "" {
		return fmt.Errorf(
			"append resolution decision: automatic authority requires an initial accepted exact-email match: %w",
			ErrConflict,
		)
	}
	var exactCandidates, matchingEntityCandidates int
	if err := transaction.transaction.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE entity_id = $2)
		FROM stacks_core.resolution_candidates
		WHERE proposal_id = $1
		  AND reason_code = $3`,
		decision.ProposalID(),
		decision.EntityID(),
		uniqueExactWorkEmailReasonCode,
	).Scan(&exactCandidates, &matchingEntityCandidates); err != nil {
		return wrapIdentityError(ctx, "validate automatic resolution authority", err)
	}
	if exactCandidates != 1 || matchingEntityCandidates != 1 {
		return fmt.Errorf(
			"append resolution decision: automatic authority requires one unique exact work-email candidate: %w",
			ErrConflict,
		)
	}
	return nil
}

func (transaction *Transaction) exactResolutionDecisionRetry(
	ctx context.Context,
	decision identity.ResolutionDecision,
	aliases []identity.AliasAssertion,
) (bool, error) {
	stored, err := loadResolutionDecisionValue(ctx, transaction.transaction, decision.ID())
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, wrapIdentityError(ctx, "load resolution decision retry", err)
	}
	if !sameResolutionDecision(stored, decision) {
		return false, fmt.Errorf("append resolution decision: immutable identity: %w", ErrConflict)
	}
	storedAliases, err := loadDecisionAliases(ctx, transaction.transaction, decision.ID())
	if err != nil {
		return false, wrapIdentityError(ctx, "load resolution decision aliases", err)
	}
	if !sameAliasAssertions(storedAliases, aliases) {
		return false, fmt.Errorf("append resolution decision: immutable aliases: %w", ErrConflict)
	}
	return true, nil
}

// EffectiveResolutionDecision returns the unsuperseded authority decision for
// a proposal.
func (database *Database) EffectiveResolutionDecision(
	ctx context.Context,
	proposalID identity.ProposalID,
) (identity.ResolutionDecision, error) {
	if err := contextRequired(ctx, "load effective resolution decision"); err != nil {
		return identity.ResolutionDecision{}, err
	}
	if database == nil || database.pool == nil {
		return identity.ResolutionDecision{}, fmt.Errorf(
			"load effective resolution decision: database is closed",
		)
	}
	if strings.TrimSpace(string(proposalID)) == "" {
		return identity.ResolutionDecision{}, fmt.Errorf(
			"load effective resolution decision: proposal ID is required",
		)
	}
	decision, err := loadEffectiveResolutionDecisionValue(ctx, database.pool, proposalID)
	if err != nil {
		return identity.ResolutionDecision{}, wrapIdentityReadError(
			ctx,
			"load effective resolution decision",
			err,
		)
	}
	return decision, nil
}

// LoadResolutionDecision returns one immutable historical authority decision.
func (database *Database) LoadResolutionDecision(
	ctx context.Context,
	decisionID identity.DecisionID,
) (identity.ResolutionDecision, error) {
	if err := contextRequired(ctx, "load resolution decision"); err != nil {
		return identity.ResolutionDecision{}, err
	}
	if database == nil || database.pool == nil {
		return identity.ResolutionDecision{}, fmt.Errorf("load resolution decision: database is closed")
	}
	if strings.TrimSpace(string(decisionID)) == "" {
		return identity.ResolutionDecision{}, fmt.Errorf("load resolution decision: decision ID is required")
	}
	decision, err := loadResolutionDecisionValue(ctx, database.pool, decisionID)
	if err != nil {
		return identity.ResolutionDecision{}, wrapIdentityReadError(
			ctx,
			"load resolution decision",
			err,
		)
	}
	return decision, nil
}

// EntitySnapshots returns deterministic resolver inputs using only effective
// accepted alias authority.
func (database *Database) EntitySnapshots(
	ctx context.Context,
) ([]identity.EntitySnapshot, error) {
	records, err := database.ListEntities(ctx)
	if err != nil {
		return nil, err
	}
	snapshots := make([]identity.EntitySnapshot, len(records))
	for index, record := range records {
		aliases := make([]identity.Alias, len(record.Aliases))
		for aliasIndex, assertion := range record.Aliases {
			aliases[aliasIndex] = assertion.Alias()
		}
		snapshots[index] = identity.EntitySnapshot{
			ID:          string(record.Entity.ID()),
			Kind:        record.Entity.Kind(),
			DisplayName: record.Entity.DisplayName(),
			RecordedAt:  record.Entity.RecordedAt(),
			Aliases:     aliases,
		}
	}
	return snapshots, nil
}

// ListEntities returns deterministic generic canonical entity read models.
func (database *Database) ListEntities(ctx context.Context) ([]EntityRecord, error) {
	if err := contextRequired(ctx, "list entities"); err != nil {
		return nil, err
	}
	if database == nil || database.pool == nil {
		return nil, fmt.Errorf("list entities: database is closed")
	}
	rows, err := database.pool.Query(ctx, `
		SELECT id
		FROM stacks_core.entities
		ORDER BY recorded_at, id`)
	if err != nil {
		return nil, wrapIdentityError(ctx, "list entity identities", err)
	}
	var ids []identity.EntityID
	for rows.Next() {
		var id identity.EntityID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, wrapIdentityError(ctx, "scan entity identity", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, wrapIdentityError(ctx, "iterate entity identities", err)
	}
	rows.Close()
	records := make([]EntityRecord, len(ids))
	for index, id := range ids {
		record, err := loadEntityRecord(ctx, database.pool, id)
		if err != nil {
			return nil, wrapIdentityError(ctx, "load entity read model", err)
		}
		records[index] = record
	}
	return records, nil
}

// LoadEntity returns one generic canonical entity read model.
func (database *Database) LoadEntity(
	ctx context.Context,
	entityID identity.EntityID,
) (EntityRecord, error) {
	if err := contextRequired(ctx, "load entity"); err != nil {
		return EntityRecord{}, err
	}
	if database == nil || database.pool == nil {
		return EntityRecord{}, fmt.Errorf("load entity: database is closed")
	}
	if strings.TrimSpace(string(entityID)) == "" {
		return EntityRecord{}, fmt.Errorf("load entity: entity ID is required")
	}
	record, err := loadEntityRecord(ctx, database.pool, entityID)
	if err != nil {
		return EntityRecord{}, wrapIdentityReadError(ctx, "load entity", err)
	}
	return record, nil
}

// ListPendingResolutionProposals returns proposals with no authority decision.
func (database *Database) ListPendingResolutionProposals(
	ctx context.Context,
) ([]ResolutionProposalRecord, error) {
	if err := contextRequired(ctx, "list pending resolution proposals"); err != nil {
		return nil, err
	}
	if database == nil || database.pool == nil {
		return nil, fmt.Errorf("list pending resolution proposals: database is closed")
	}
	rows, err := database.pool.Query(ctx, `
		SELECT proposal.id
		FROM stacks_core.resolution_proposals AS proposal
		WHERE NOT EXISTS (
			SELECT 1
			FROM stacks_core.resolution_decisions AS decision
			WHERE decision.proposal_id = proposal.id
		)
		ORDER BY proposal.recorded_at, proposal.id`)
	if err != nil {
		return nil, wrapIdentityError(ctx, "list pending resolution proposal identities", err)
	}
	var ids []identity.ProposalID
	for rows.Next() {
		var id identity.ProposalID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, wrapIdentityError(ctx, "scan pending resolution proposal identity", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, wrapIdentityError(ctx, "iterate pending resolution proposal identities", err)
	}
	rows.Close()
	records := make([]ResolutionProposalRecord, len(ids))
	for index, id := range ids {
		record, err := loadResolutionProposalRecord(ctx, database.pool, id)
		if err != nil {
			return nil, wrapIdentityError(ctx, "load pending resolution proposal", err)
		}
		records[index] = record
	}
	return records, nil
}

// LoadResolutionProposal returns one canonical proposal review model.
func (database *Database) LoadResolutionProposal(
	ctx context.Context,
	proposalID identity.ProposalID,
) (ResolutionProposalRecord, error) {
	if err := contextRequired(ctx, "load resolution proposal"); err != nil {
		return ResolutionProposalRecord{}, err
	}
	if database == nil || database.pool == nil {
		return ResolutionProposalRecord{}, fmt.Errorf("load resolution proposal: database is closed")
	}
	if strings.TrimSpace(string(proposalID)) == "" {
		return ResolutionProposalRecord{}, fmt.Errorf("load resolution proposal: proposal ID is required")
	}
	record, err := loadResolutionProposalRecord(ctx, database.pool, proposalID)
	if err != nil {
		return ResolutionProposalRecord{}, wrapIdentityReadError(ctx, "load resolution proposal", err)
	}
	return record, nil
}

func loadEntityValue(
	ctx context.Context,
	reader documentReader,
	entityID identity.EntityID,
) (identity.Entity, error) {
	var (
		id, kind, displayName string
		recordedAt            time.Time
	)
	if err := reader.QueryRow(ctx, `
		SELECT id, kind, display_name, recorded_at
		FROM stacks_core.entities
		WHERE id = $1`,
		entityID,
	).Scan(&id, &kind, &displayName, &recordedAt); err != nil {
		return identity.Entity{}, err
	}
	canonicalTime, err := canonicalStoredTime(recordedAt)
	if err != nil {
		return identity.Entity{}, fmt.Errorf("stored entity recorded time: %w", err)
	}
	entity, err := identity.NewEntity(identity.EntityInput{
		ID:          identity.EntityID(id),
		Kind:        identity.Kind(kind),
		DisplayName: displayName,
		RecordedAt:  canonicalTime,
	})
	if err != nil {
		return identity.Entity{}, fmt.Errorf("validate stored entity: %w", err)
	}
	return entity, nil
}

func loadMentionValue(
	ctx context.Context,
	reader documentReader,
	mentionID identity.MentionID,
) (identity.MentionRecord, error) {
	var (
		id, evidenceID, derivationRunID, surface, normalizedName string
		proposedEmail, role                                      string
		proposedEmailEvidenceID                                  *string
		recordedAt                                               time.Time
	)
	if err := reader.QueryRow(ctx, `
		SELECT
			id, evidence_id, derivation_run_id, surface, normalized_name,
			proposed_email, proposed_email_evidence_id, role, recorded_at
		FROM stacks_core.mentions
		WHERE id = $1`,
		mentionID,
	).Scan(
		&id,
		&evidenceID,
		&derivationRunID,
		&surface,
		&normalizedName,
		&proposedEmail,
		&proposedEmailEvidenceID,
		&role,
		&recordedAt,
	); err != nil {
		return identity.MentionRecord{}, err
	}
	recordedAt, err := canonicalStoredTime(recordedAt)
	if err != nil {
		return identity.MentionRecord{}, fmt.Errorf("stored mention recorded time: %w", err)
	}
	var proposedEvidence evidence.EvidenceID
	if proposedEmailEvidenceID != nil {
		proposedEvidence = evidence.EvidenceID(*proposedEmailEvidenceID)
	}
	mention, err := identity.NewMention(identity.MentionInput{
		ID:                      identity.MentionID(id),
		EvidenceID:              evidence.EvidenceID(evidenceID),
		DerivationRunID:         derivationRunID,
		Surface:                 surface,
		NormalizedName:          normalizedName,
		ProposedEmail:           proposedEmail,
		ProposedEmailEvidenceID: proposedEvidence,
		Role:                    role,
		RecordedAt:              recordedAt,
	})
	if err != nil {
		return identity.MentionRecord{}, fmt.Errorf("validate stored mention: %w", err)
	}
	return mention, nil
}

func loadResolutionProposalValue(
	ctx context.Context,
	reader documentReader,
	proposalID identity.ProposalID,
) (identity.ResolutionProposal, error) {
	var (
		id, mentionID, reasonCode string
		recordedAt                time.Time
	)
	if err := reader.QueryRow(ctx, `
		SELECT id, mention_id, reason_code, recorded_at
		FROM stacks_core.resolution_proposals
		WHERE id = $1`,
		proposalID,
	).Scan(&id, &mentionID, &reasonCode, &recordedAt); err != nil {
		return identity.ResolutionProposal{}, err
	}
	recordedAt, err := canonicalStoredTime(recordedAt)
	if err != nil {
		return identity.ResolutionProposal{}, fmt.Errorf(
			"stored resolution proposal recorded time: %w",
			err,
		)
	}
	rows, err := reader.Query(ctx, `
		SELECT evidence_id
		FROM stacks_core.resolution_proposal_evidence
		WHERE proposal_id = $1
		ORDER BY evidence_order`,
		proposalID,
	)
	if err != nil {
		return identity.ResolutionProposal{}, err
	}
	var evidenceIDs []evidence.EvidenceID
	for rows.Next() {
		var evidenceID evidence.EvidenceID
		if err := rows.Scan(&evidenceID); err != nil {
			rows.Close()
			return identity.ResolutionProposal{}, err
		}
		evidenceIDs = append(evidenceIDs, evidenceID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return identity.ResolutionProposal{}, err
	}
	rows.Close()
	proposal, err := identity.NewResolutionProposal(identity.ResolutionProposalInput{
		ID:          identity.ProposalID(id),
		MentionID:   identity.MentionID(mentionID),
		ReasonCode:  reasonCode,
		EvidenceIDs: evidenceIDs,
		RecordedAt:  recordedAt,
	})
	if err != nil {
		return identity.ResolutionProposal{}, fmt.Errorf(
			"validate stored resolution proposal: %w",
			err,
		)
	}
	return proposal, nil
}

func loadResolutionCandidateValue(
	ctx context.Context,
	reader documentReader,
	candidateID identity.CandidateID,
) (identity.ResolutionCandidate, error) {
	var (
		id, proposalID, entityID, reasonCode string
		sourceKind, sourceReference          string
		rank                                 int
		confidence                           float64
		recordedAt                           time.Time
	)
	if err := reader.QueryRow(ctx, `
		SELECT
			id, proposal_id, entity_id, candidate_rank, confidence,
			reason_code, source_kind, source_reference, recorded_at
		FROM stacks_core.resolution_candidates
		WHERE id = $1`,
		candidateID,
	).Scan(
		&id,
		&proposalID,
		&entityID,
		&rank,
		&confidence,
		&reasonCode,
		&sourceKind,
		&sourceReference,
		&recordedAt,
	); err != nil {
		return identity.ResolutionCandidate{}, err
	}
	recordedAt, err := canonicalStoredTime(recordedAt)
	if err != nil {
		return identity.ResolutionCandidate{}, fmt.Errorf(
			"stored resolution candidate recorded time: %w",
			err,
		)
	}
	candidate, err := identity.NewResolutionCandidate(identity.ResolutionCandidateInput{
		ID:         identity.CandidateID(id),
		ProposalID: identity.ProposalID(proposalID),
		EntityID:   identity.EntityID(entityID),
		Rank:       rank,
		Confidence: confidence,
		ReasonCode: reasonCode,
		Source: identity.CandidateSource{
			Kind:      sourceKind,
			Reference: sourceReference,
		},
		RecordedAt: recordedAt,
	})
	if err != nil {
		return identity.ResolutionCandidate{}, fmt.Errorf(
			"validate stored resolution candidate: %w",
			err,
		)
	}
	return candidate, nil
}

func loadResolutionDecisionValue(
	ctx context.Context,
	reader documentReader,
	decisionID identity.DecisionID,
) (identity.ResolutionDecision, error) {
	return loadResolutionDecisionQuery(ctx, reader, `
		SELECT
			id, proposal_id, outcome, entity_id, authority, reason_code,
			recorded_at, supersedes_id, digest_version, digest
		FROM stacks_core.resolution_decisions
		WHERE id = $1`,
		decisionID,
	)
}

func loadEffectiveResolutionDecisionValue(
	ctx context.Context,
	reader documentReader,
	proposalID identity.ProposalID,
) (identity.ResolutionDecision, error) {
	return loadResolutionDecisionQuery(ctx, reader, `
		SELECT
			decision.id, decision.proposal_id, decision.outcome,
			decision.entity_id, decision.authority, decision.reason_code,
			decision.recorded_at, decision.supersedes_id,
			decision.digest_version, decision.digest
		FROM stacks_core.resolution_decisions AS decision
		WHERE decision.proposal_id = $1
		  AND NOT EXISTS (
			SELECT 1
			FROM stacks_core.resolution_decisions AS successor
			WHERE successor.supersedes_id = decision.id
		  )`,
		proposalID,
	)
}

func loadResolutionDecisionQuery(
	ctx context.Context,
	reader documentReader,
	query string,
	argument any,
) (identity.ResolutionDecision, error) {
	var (
		id, proposalID, outcome, authority, reasonCode string
		entityID, supersedesID                         *string
		recordedAt                                     time.Time
		digestVersion                                  string
		storedDigest                                   []byte
	)
	if err := reader.QueryRow(ctx, query, argument).Scan(
		&id,
		&proposalID,
		&outcome,
		&entityID,
		&authority,
		&reasonCode,
		&recordedAt,
		&supersedesID,
		&digestVersion,
		&storedDigest,
	); err != nil {
		return identity.ResolutionDecision{}, err
	}
	recordedAt, err := canonicalStoredTime(recordedAt)
	if err != nil {
		return identity.ResolutionDecision{}, fmt.Errorf(
			"stored resolution decision recorded time: %w",
			err,
		)
	}
	var canonicalEntityID identity.EntityID
	if entityID != nil {
		canonicalEntityID = identity.EntityID(*entityID)
	}
	var canonicalSupersedesID identity.DecisionID
	if supersedesID != nil {
		canonicalSupersedesID = identity.DecisionID(*supersedesID)
	}
	decision, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
		ID:           identity.DecisionID(id),
		ProposalID:   identity.ProposalID(proposalID),
		Outcome:      identity.DecisionOutcome(outcome),
		EntityID:     canonicalEntityID,
		Authority:    identity.DecisionAuthority(authority),
		ReasonCode:   reasonCode,
		RecordedAt:   recordedAt,
		SupersedesID: canonicalSupersedesID,
	})
	if err != nil {
		return identity.ResolutionDecision{}, fmt.Errorf(
			"validate stored resolution decision: %w",
			err,
		)
	}
	if decision.DigestVersion() != digestVersion ||
		!sameDigestBytes(decision.Digest(), storedDigest) {
		return identity.ResolutionDecision{}, fmt.Errorf(
			"stored resolution decision digest: %w",
			ErrConflict,
		)
	}
	return decision, nil
}

func loadDecisionAliases(
	ctx context.Context,
	reader documentReader,
	decisionID identity.DecisionID,
) ([]identity.AliasAssertion, error) {
	rows, err := reader.Query(ctx, `
		SELECT id, entity_id, alias_type, alias_value, recorded_at
		FROM stacks_core.entity_alias_assertions
		WHERE decision_id = $1
		ORDER BY id`,
		decisionID,
	)
	if err != nil {
		return nil, err
	}
	var assertions []identity.AliasAssertion
	for rows.Next() {
		var (
			id, entityID, aliasType, aliasValue string
			recordedAt                          time.Time
		)
		if err := rows.Scan(
			&id,
			&entityID,
			&aliasType,
			&aliasValue,
			&recordedAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		recordedAt, err = canonicalStoredTime(recordedAt)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("stored alias assertion recorded time: %w", err)
		}
		assertion, err := identity.NewAliasAssertion(identity.AliasAssertionInput{
			ID:         identity.AliasAssertionID(id),
			DecisionID: decisionID,
			EntityID:   identity.EntityID(entityID),
			Alias: identity.Alias{
				Type:  identity.AliasType(aliasType),
				Value: aliasValue,
			},
			RecordedAt: recordedAt,
		})
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("validate stored alias assertion: %w", err)
		}
		assertions = append(assertions, assertion)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return assertions, nil
}

func loadEntityRecord(
	ctx context.Context,
	reader documentReader,
	entityID identity.EntityID,
) (EntityRecord, error) {
	entity, err := loadEntityValue(ctx, reader, entityID)
	if err != nil {
		return EntityRecord{}, err
	}
	aliasRows, err := reader.Query(ctx, `
		SELECT
			assertion.id, assertion.decision_id, assertion.alias_type,
			assertion.alias_value, assertion.recorded_at
		FROM stacks_core.entity_alias_assertions AS assertion
		JOIN stacks_core.resolution_decisions AS decision
		  ON decision.id = assertion.decision_id
		WHERE assertion.entity_id = $1
		  AND decision.outcome = 'accepted'
		  AND decision.entity_id = assertion.entity_id
		  AND NOT EXISTS (
			SELECT 1
			FROM stacks_core.resolution_decisions AS successor
			WHERE successor.supersedes_id = decision.id
		  )
		ORDER BY assertion.recorded_at, assertion.id`,
		entityID,
	)
	if err != nil {
		return EntityRecord{}, err
	}
	var aliases []identity.AliasAssertion
	for aliasRows.Next() {
		var (
			id, decisionID, aliasType, aliasValue string
			recordedAt                            time.Time
		)
		if err := aliasRows.Scan(
			&id,
			&decisionID,
			&aliasType,
			&aliasValue,
			&recordedAt,
		); err != nil {
			aliasRows.Close()
			return EntityRecord{}, err
		}
		recordedAt, err = canonicalStoredTime(recordedAt)
		if err != nil {
			aliasRows.Close()
			return EntityRecord{}, err
		}
		assertion, err := identity.NewAliasAssertion(identity.AliasAssertionInput{
			ID:         identity.AliasAssertionID(id),
			DecisionID: identity.DecisionID(decisionID),
			EntityID:   entityID,
			Alias: identity.Alias{
				Type:  identity.AliasType(aliasType),
				Value: aliasValue,
			},
			RecordedAt: recordedAt,
		})
		if err != nil {
			aliasRows.Close()
			return EntityRecord{}, err
		}
		aliases = append(aliases, assertion)
	}
	if err := aliasRows.Err(); err != nil {
		aliasRows.Close()
		return EntityRecord{}, err
	}
	aliasRows.Close()

	groundingRows, err := reader.Query(ctx, `
		SELECT
			proposal.mention_id,
			proposal_evidence.evidence_id
		FROM stacks_core.resolution_decisions AS decision
		JOIN stacks_core.resolution_proposals AS proposal
		  ON proposal.id = decision.proposal_id
		JOIN stacks_core.resolution_proposal_evidence AS proposal_evidence
		  ON proposal_evidence.proposal_id = proposal.id
		WHERE decision.outcome = 'accepted'
		  AND decision.entity_id = $1
		  AND NOT EXISTS (
			SELECT 1
			FROM stacks_core.resolution_decisions AS successor
			WHERE successor.supersedes_id = decision.id
		  )
		ORDER BY
			proposal.recorded_at,
			proposal.id,
			proposal_evidence.evidence_order`,
		entityID,
	)
	if err != nil {
		return EntityRecord{}, err
	}
	var mentionIDs []identity.MentionID
	var evidenceIDs []evidence.EvidenceID
	seenMentions := make(map[identity.MentionID]struct{})
	seenEvidence := make(map[evidence.EvidenceID]struct{})
	for groundingRows.Next() {
		var mentionID identity.MentionID
		var evidenceID evidence.EvidenceID
		if err := groundingRows.Scan(&mentionID, &evidenceID); err != nil {
			groundingRows.Close()
			return EntityRecord{}, err
		}
		if _, exists := seenMentions[mentionID]; !exists {
			seenMentions[mentionID] = struct{}{}
			mentionIDs = append(mentionIDs, mentionID)
		}
		if _, exists := seenEvidence[evidenceID]; !exists {
			seenEvidence[evidenceID] = struct{}{}
			evidenceIDs = append(evidenceIDs, evidenceID)
		}
	}
	if err := groundingRows.Err(); err != nil {
		groundingRows.Close()
		return EntityRecord{}, err
	}
	groundingRows.Close()
	return EntityRecord{
		Entity:              entity,
		Aliases:             aliases,
		GroundingMentionIDs: mentionIDs,
		EvidenceIDs:         evidenceIDs,
	}, nil
}

func loadResolutionProposalRecord(
	ctx context.Context,
	reader documentReader,
	proposalID identity.ProposalID,
) (ResolutionProposalRecord, error) {
	proposal, err := loadResolutionProposalValue(ctx, reader, proposalID)
	if err != nil {
		return ResolutionProposalRecord{}, err
	}
	rows, err := reader.Query(ctx, `
		SELECT id
		FROM stacks_core.resolution_candidates
		WHERE proposal_id = $1
		ORDER BY candidate_rank, id`,
		proposalID,
	)
	if err != nil {
		return ResolutionProposalRecord{}, err
	}
	var candidates []identity.ResolutionCandidate
	for rows.Next() {
		var candidateID identity.CandidateID
		if err := rows.Scan(&candidateID); err != nil {
			rows.Close()
			return ResolutionProposalRecord{}, err
		}
		candidate, err := loadResolutionCandidateValue(ctx, reader, candidateID)
		if err != nil {
			rows.Close()
			return ResolutionProposalRecord{}, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ResolutionProposalRecord{}, err
	}
	rows.Close()
	var effective *identity.ResolutionDecision
	decision, err := loadEffectiveResolutionDecisionValue(ctx, reader, proposalID)
	switch {
	case err == nil:
		effective = &decision
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return ResolutionProposalRecord{}, err
	}
	return ResolutionProposalRecord{
		Proposal:          proposal,
		Candidates:        candidates,
		EffectiveDecision: effective,
	}, nil
}

func validateEntity(value identity.Entity) error {
	if value.ID() == "" ||
		value.Kind() == "" ||
		strings.TrimSpace(value.DisplayName()) == "" ||
		!timepoint.IsCanonical(value.RecordedAt()) {
		return fmt.Errorf("canonical entity is required")
	}
	return nil
}

func validateMention(value identity.MentionRecord) error {
	if value.ID() == "" ||
		value.EvidenceID() == "" ||
		value.DerivationRunID() == "" ||
		value.Surface() == "" ||
		value.NormalizedName() == "" ||
		value.Role() == "" ||
		!timepoint.IsCanonical(value.RecordedAt()) {
		return fmt.Errorf("canonical mention is required")
	}
	if (value.ProposedEmail() == "") != (value.ProposedEmailEvidenceID() == "") {
		return fmt.Errorf("canonical mention proposed email evidence is invalid")
	}
	return nil
}

func validateResolutionProposal(value identity.ResolutionProposal) error {
	if value.ID() == "" ||
		value.MentionID() == "" ||
		value.ReasonCode() == "" ||
		len(value.EvidenceIDs()) == 0 ||
		!timepoint.IsCanonical(value.RecordedAt()) {
		return fmt.Errorf("canonical resolution proposal is required")
	}
	return nil
}

func validateResolutionCandidate(value identity.ResolutionCandidate) error {
	source := value.Source()
	if value.ID() == "" ||
		value.ProposalID() == "" ||
		value.EntityID() == "" ||
		value.Rank() <= 0 ||
		value.ReasonCode() == "" ||
		source.Kind == "" ||
		source.Reference == "" ||
		!timepoint.IsCanonical(value.RecordedAt()) {
		return fmt.Errorf("canonical resolution candidate is required")
	}
	return nil
}

func validateResolutionDecision(value identity.ResolutionDecision) error {
	if value.ID() == "" ||
		value.ProposalID() == "" ||
		value.Outcome() == "" ||
		value.Authority() == "" ||
		value.ReasonCode() == "" ||
		value.DigestVersion() == "" ||
		value.Digest() == (evidence.ContentDigest{}) ||
		!timepoint.IsCanonical(value.RecordedAt()) {
		return fmt.Errorf("canonical resolution decision is required")
	}
	return nil
}

func validateDecisionAliases(
	decision identity.ResolutionDecision,
	aliases []identity.AliasAssertion,
) error {
	if decision.Outcome() == identity.DecisionRejected && len(aliases) != 0 {
		return fmt.Errorf("rejected decisions cannot assert aliases: %w", ErrConflict)
	}
	if decision.Authority() == identity.AuthorityAutomatic && len(aliases) != 0 {
		return fmt.Errorf("automatic decisions cannot teach aliases: %w", ErrConflict)
	}
	seen := make(map[identity.AliasAssertionID]struct{}, len(aliases))
	for _, assertion := range aliases {
		if assertion.ID() == "" ||
			assertion.DecisionID() != decision.ID() ||
			assertion.EntityID() != decision.EntityID() ||
			!timepoint.IsCanonical(assertion.RecordedAt()) {
			return fmt.Errorf("alias assertion does not belong to accepted decision: %w", ErrConflict)
		}
		if _, exists := seen[assertion.ID()]; exists {
			return fmt.Errorf("duplicate alias assertion identity: %w", ErrConflict)
		}
		seen[assertion.ID()] = struct{}{}
	}
	return nil
}

func sameEntity(left, right identity.Entity) bool {
	return left.ID() == right.ID() &&
		left.Kind() == right.Kind() &&
		left.DisplayName() == right.DisplayName() &&
		left.RecordedAt() == right.RecordedAt()
}

func sameMention(left, right identity.MentionRecord) bool {
	return left.ID() == right.ID() &&
		left.EvidenceID() == right.EvidenceID() &&
		left.DerivationRunID() == right.DerivationRunID() &&
		left.Surface() == right.Surface() &&
		left.NormalizedName() == right.NormalizedName() &&
		left.ProposedEmail() == right.ProposedEmail() &&
		left.ProposedEmailEvidenceID() == right.ProposedEmailEvidenceID() &&
		left.Role() == right.Role() &&
		left.RecordedAt() == right.RecordedAt()
}

func sameResolutionProposal(
	left,
	right identity.ResolutionProposal,
) bool {
	if left.ID() != right.ID() ||
		left.MentionID() != right.MentionID() ||
		left.ReasonCode() != right.ReasonCode() ||
		left.RecordedAt() != right.RecordedAt() {
		return false
	}
	leftEvidence, rightEvidence := left.EvidenceIDs(), right.EvidenceIDs()
	if len(leftEvidence) != len(rightEvidence) {
		return false
	}
	for index := range leftEvidence {
		if leftEvidence[index] != rightEvidence[index] {
			return false
		}
	}
	return true
}

func sameResolutionCandidate(
	left,
	right identity.ResolutionCandidate,
) bool {
	return left.ID() == right.ID() &&
		left.ProposalID() == right.ProposalID() &&
		left.EntityID() == right.EntityID() &&
		left.Rank() == right.Rank() &&
		left.Confidence() == right.Confidence() &&
		left.ReasonCode() == right.ReasonCode() &&
		left.Source() == right.Source() &&
		left.RecordedAt() == right.RecordedAt()
}

func sameResolutionDecision(
	left,
	right identity.ResolutionDecision,
) bool {
	return left.ID() == right.ID() &&
		left.ProposalID() == right.ProposalID() &&
		left.Outcome() == right.Outcome() &&
		left.EntityID() == right.EntityID() &&
		left.Authority() == right.Authority() &&
		left.ReasonCode() == right.ReasonCode() &&
		left.RecordedAt() == right.RecordedAt() &&
		left.SupersedesID() == right.SupersedesID() &&
		left.DigestVersion() == right.DigestVersion() &&
		left.Digest() == right.Digest()
}

func sameAliasAssertions(
	left,
	right []identity.AliasAssertion,
) bool {
	if len(left) != len(right) {
		return false
	}
	sortedRight := append([]identity.AliasAssertion(nil), right...)
	sort.Slice(sortedRight, func(i, j int) bool {
		return sortedRight[i].ID() < sortedRight[j].ID()
	})
	for index := range left {
		if left[index].ID() != sortedRight[index].ID() ||
			left[index].DecisionID() != sortedRight[index].DecisionID() ||
			left[index].EntityID() != sortedRight[index].EntityID() ||
			left[index].Alias() != sortedRight[index].Alias() ||
			left[index].RecordedAt() != sortedRight[index].RecordedAt() {
			return false
		}
	}
	return true
}

func wrapIdentityError(ctx context.Context, operation string, err error) error {
	if ctx != nil {
		if contextError := ctx.Err(); contextError != nil {
			return fmt.Errorf("%s: %w", operation, contextError)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func wrapIdentityReadError(ctx context.Context, operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, ErrNotFound)
	}
	return wrapIdentityError(ctx, operation, err)
}
