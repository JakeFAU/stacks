package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/timepoint"
	"github.com/jackc/pgx/v5"
)

const admissionAuthorityLockNamespace = "github.com/JakeFAU/stacks/postgres-admission-authority/v1"

// AppendAdmissionDecision appends one generic authority decision without
// rewriting its target or predecessor.
func (transaction *Transaction) AppendAdmissionDecision(
	ctx context.Context,
	decision admission.Decision,
) error {
	if err := contextRequired(ctx, "append admission decision"); err != nil {
		return err
	}
	if transaction == nil || transaction.transaction == nil {
		return fmt.Errorf("append admission decision: transaction is closed")
	}
	if err := validateAdmissionDecision(decision); err != nil {
		return fmt.Errorf("append admission decision: %w", err)
	}
	if exact, err := transaction.exactAdmissionDecisionRetry(ctx, decision); err != nil {
		return err
	} else if exact {
		return nil
	}

	if _, err := transaction.transaction.Exec(ctx, `
		INSERT INTO stacks_core.admission_targets (
			target_kind, target_id, recorded_at
		)
		VALUES ($1, $2, $3)
		ON CONFLICT (target_kind, target_id) DO NOTHING`,
		decision.TargetKind(),
		decision.TargetID(),
		decision.RecordedAt(),
	); err != nil {
		return wrapAdmissionError(ctx, "insert admission target", conflictError(err))
	}
	if err := lockAdmissionTargetAuthority(
		ctx,
		transaction,
		string(decision.TargetKind()),
		decision.TargetID(),
	); err != nil {
		return err
	}
	var lockedTargetID string
	if err := transaction.transaction.QueryRow(ctx, `
		SELECT target_id
		FROM stacks_core.admission_targets
		WHERE target_kind = $1
		  AND target_id = $2`,
		decision.TargetKind(),
		decision.TargetID(),
	).Scan(&lockedTargetID); err != nil {
		return wrapAdmissionError(ctx, "lock admission target", err)
	}
	if exact, err := transaction.exactAdmissionDecisionRetry(ctx, decision); err != nil {
		return err
	} else if exact {
		return nil
	}

	effective, effectiveErr := loadEffectiveAdmissionDecisionValue(
		ctx,
		transaction.transaction,
		decision.TargetKind(),
		decision.TargetID(),
	)
	switch {
	case decision.SupersedesID() == "" && effectiveErr == nil:
		return fmt.Errorf("append admission decision: target already decided: %w", ErrConflict)
	case decision.SupersedesID() == "" && !errors.Is(effectiveErr, pgx.ErrNoRows):
		return wrapAdmissionError(ctx, "load effective admission decision", effectiveErr)
	case decision.SupersedesID() != "" && errors.Is(effectiveErr, pgx.ErrNoRows):
		return fmt.Errorf("append admission decision: predecessor is not effective: %w", ErrConflict)
	case decision.SupersedesID() != "" && effectiveErr != nil:
		return wrapAdmissionError(ctx, "load effective admission decision", effectiveErr)
	case decision.SupersedesID() != "" && effective.ID() != decision.SupersedesID():
		return fmt.Errorf("append admission decision: predecessor is not effective: %w", ErrConflict)
	case decision.SupersedesID() != "" && decision.Authority() != admission.AuthorityReviewer:
		return fmt.Errorf("append admission decision: corrections require reviewer authority: %w", ErrConflict)
	}

	var supersedesID any
	if decision.SupersedesID() != "" {
		supersedesID = decision.SupersedesID()
	}
	digest := decision.Digest()
	if _, err := transaction.transaction.Exec(ctx, `
		INSERT INTO stacks_core.admission_decisions (
			id, target_kind, target_id, outcome, reason_code, authority,
			recorded_at, supersedes_id, digest_version, digest
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		decision.ID(),
		decision.TargetKind(),
		decision.TargetID(),
		decision.Outcome(),
		decision.ReasonCode(),
		decision.Authority(),
		decision.RecordedAt(),
		supersedesID,
		decision.DigestVersion(),
		digest[:],
	); err != nil {
		return wrapAdmissionError(ctx, "insert admission decision", conflictError(err))
	}
	return nil
}

func lockAdmissionTargetAuthority(
	ctx context.Context,
	transaction *Transaction,
	targetKind string,
	targetID string,
) error {
	if _, err := transaction.Exec(ctx, `
		/* stacks_admission_target_authority */
		SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		admissionAuthorityLockNamespace+"/"+targetKind,
		targetID,
	); err != nil {
		return wrapAdmissionError(ctx, "lock admission target authority", err)
	}
	return nil
}

func (transaction *Transaction) exactAdmissionDecisionRetry(
	ctx context.Context,
	decision admission.Decision,
) (bool, error) {
	stored, err := loadAdmissionDecisionValue(ctx, transaction.transaction, decision.ID())
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, wrapAdmissionError(ctx, "load admission decision retry", err)
	}
	if !sameAdmissionDecision(stored, decision) {
		return false, fmt.Errorf("append admission decision: immutable identity: %w", ErrConflict)
	}
	return true, nil
}

// EffectiveAdmissionDecision returns the unsuperseded generic authority for
// one target.
func (database *Database) EffectiveAdmissionDecision(
	ctx context.Context,
	targetKind admission.TargetKind,
	targetID string,
) (admission.Decision, error) {
	if err := contextRequired(ctx, "load effective admission decision"); err != nil {
		return admission.Decision{}, err
	}
	if database == nil || database.pool == nil {
		return admission.Decision{}, fmt.Errorf("load effective admission decision: database is closed")
	}
	if strings.TrimSpace(string(targetKind)) == "" || strings.TrimSpace(targetID) == "" {
		return admission.Decision{}, fmt.Errorf(
			"load effective admission decision: target kind and ID are required",
		)
	}
	decision, err := loadEffectiveAdmissionDecisionValue(
		ctx,
		database.pool,
		targetKind,
		targetID,
	)
	if err != nil {
		return admission.Decision{}, wrapAdmissionReadError(
			ctx,
			"load effective admission decision",
			err,
		)
	}
	return decision, nil
}

func loadAdmissionDecisionValue(
	ctx context.Context,
	reader documentReader,
	decisionID string,
) (admission.Decision, error) {
	return loadAdmissionDecisionQuery(ctx, reader, `
		SELECT
			id, target_kind, target_id, outcome, reason_code, authority,
			recorded_at, supersedes_id, digest_version, digest
		FROM stacks_core.admission_decisions
		WHERE id = $1`,
		decisionID,
	)
}

func loadEffectiveAdmissionDecisionValue(
	ctx context.Context,
	reader documentReader,
	targetKind admission.TargetKind,
	targetID string,
) (admission.Decision, error) {
	return loadAdmissionDecisionQuery(ctx, reader, `
		SELECT
			decision.id, decision.target_kind, decision.target_id,
			decision.outcome, decision.reason_code, decision.authority,
			decision.recorded_at, decision.supersedes_id,
			decision.digest_version, decision.digest
		FROM stacks_core.admission_decisions AS decision
		WHERE decision.target_kind = $1
		  AND decision.target_id = $2
		  AND NOT EXISTS (
			SELECT 1
			FROM stacks_core.admission_decisions AS successor
			WHERE successor.supersedes_id = decision.id
		  )`,
		targetKind,
		targetID,
	)
}

func loadAdmissionDecisionQuery(
	ctx context.Context,
	reader documentReader,
	query string,
	arguments ...any,
) (admission.Decision, error) {
	var (
		id, targetKind, targetID, outcome, reasonCode, authority string
		recordedAt                                               time.Time
		supersedesID                                             *string
		digestVersion                                            string
		storedDigest                                             []byte
	)
	if err := reader.QueryRow(ctx, query, arguments...).Scan(
		&id,
		&targetKind,
		&targetID,
		&outcome,
		&reasonCode,
		&authority,
		&recordedAt,
		&supersedesID,
		&digestVersion,
		&storedDigest,
	); err != nil {
		return admission.Decision{}, err
	}
	recordedAt, err := canonicalStoredTime(recordedAt)
	if err != nil {
		return admission.Decision{}, fmt.Errorf(
			"stored admission decision recorded time: %w",
			err,
		)
	}
	var canonicalSupersedesID string
	if supersedesID != nil {
		canonicalSupersedesID = *supersedesID
	}
	decision, err := admission.NewDecision(admission.DecisionInput{
		ID:           id,
		TargetKind:   admission.TargetKind(targetKind),
		TargetID:     targetID,
		Outcome:      admission.Outcome(outcome),
		ReasonCode:   reasonCode,
		Authority:    admission.Authority(authority),
		RecordedAt:   recordedAt,
		SupersedesID: canonicalSupersedesID,
	})
	if err != nil {
		return admission.Decision{}, fmt.Errorf("validate stored admission decision: %w", err)
	}
	if decision.DigestVersion() != digestVersion ||
		!sameDigestBytes(decision.Digest(), storedDigest) {
		return admission.Decision{}, fmt.Errorf(
			"stored admission decision digest: %w",
			ErrConflict,
		)
	}
	return decision, nil
}

func validateAdmissionDecision(decision admission.Decision) error {
	if decision.ID() == "" ||
		decision.TargetKind() == "" ||
		decision.TargetID() == "" ||
		decision.Outcome() == "" ||
		decision.ReasonCode() == "" ||
		decision.Authority() == "" ||
		decision.DigestVersion() == "" ||
		decision.Digest() == (evidence.ContentDigest{}) ||
		!timepoint.IsCanonical(decision.RecordedAt()) {
		return fmt.Errorf("canonical admission decision is required")
	}
	return nil
}

func sameAdmissionDecision(left, right admission.Decision) bool {
	return left.ID() == right.ID() &&
		left.TargetKind() == right.TargetKind() &&
		left.TargetID() == right.TargetID() &&
		left.Outcome() == right.Outcome() &&
		left.ReasonCode() == right.ReasonCode() &&
		left.Authority() == right.Authority() &&
		left.RecordedAt() == right.RecordedAt() &&
		left.SupersedesID() == right.SupersedesID() &&
		left.DigestVersion() == right.DigestVersion() &&
		left.Digest() == right.Digest()
}

func wrapAdmissionError(ctx context.Context, operation string, err error) error {
	if ctx != nil {
		if contextError := ctx.Err(); contextError != nil {
			return fmt.Errorf("%s: %w", operation, contextError)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func wrapAdmissionReadError(ctx context.Context, operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, ErrNotFound)
	}
	return wrapAdmissionError(ctx, operation, err)
}
