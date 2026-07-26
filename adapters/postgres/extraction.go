package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/timepoint"
	"github.com/jackc/pgx/v5"
)

// ExtractionStatus is the bounded result of preparing one derivation.
type ExtractionStatus string

const (
	// ExtractionClaimed means the caller owns the returned active attempt.
	ExtractionClaimed ExtractionStatus = "claimed"
	// ExtractionBusy means a different caller owns an unexpired attempt.
	ExtractionBusy ExtractionStatus = "busy"
	// ExtractionCompleted means the exact derivation already has a durable result.
	ExtractionCompleted ExtractionStatus = "completed"
)

// ExtractionRunInput is immutable derivation provenance supplied by the
// extraction boundary.
type ExtractionRunInput struct {
	ID                      string
	DocumentVersionID       string
	DerivationDigestVersion string
	DerivationDigest        evidence.ContentDigest
	Method                  string
	Version                 string
	Provider                string
	DataMode                string
	Model                   string
	PromptVersion           string
	SchemaDigest            evidence.ContentDigest
	MaxOutputTokens         int
	RecordedAt              time.Time
}

// LeaseRequest identifies one caller-owned bounded extraction attempt.
type LeaseRequest struct {
	AttemptID     string
	Owner         string
	ClaimedAt     time.Time
	LeaseDuration time.Duration
}

// ExtractionFailureInput terminally records one bounded failed attempt.
type ExtractionFailureInput struct {
	RunID       string
	AttemptID   string
	Owner       string
	FailedAt    time.Time
	FailureCode string
}

// ExtractionCompletionInput terminally records one complete durable write set.
type ExtractionCompletionInput struct {
	RunID                 string
	AttemptID             string
	Owner                 string
	CompletedAt           time.Time
	WriteSetDigestVersion string
	WriteSetDigest        evidence.ContentDigest
}

// ExtractionCompletionCheckInput binds a completion to its owning immutable
// document version before any caller-owned payload writes begin.
type ExtractionCompletionCheckInput struct {
	DocumentVersionID string
	Completion        ExtractionCompletionInput
}

// ExtractionState describes the attempt a caller owns, the live attempt that
// made the derivation busy, or the completed durable result.
type ExtractionState struct {
	RunID                 string
	AttemptID             string
	AttemptNumber         int
	Status                ExtractionStatus
	LeaseExpiresAt        time.Time
	HasWriteSetDigest     bool
	WriteSetDigestVersion string
	WriteSetDigest        evidence.ContentDigest
}

type storedExtractionRun struct {
	input                 ExtractionRunInput
	state                 string
	completedAt           *time.Time
	writeSetDigestVersion *string
	writeSetDigest        []byte
}

type storedExtractionAttempt struct {
	id             string
	runID          string
	number         int
	owner          string
	claimedAt      time.Time
	leaseExpiresAt time.Time
	state          string
	terminalAt     *time.Time
	failureCode    *string
}

// PrepareExtraction atomically claims, reclaims, resumes, or reports an exact
// immutable derivation. The caller owns the surrounding transaction.
func (transaction *Transaction) PrepareExtraction(
	ctx context.Context,
	runInput ExtractionRunInput,
	lease LeaseRequest,
) (ExtractionState, error) {
	if err := contextRequired(ctx, "prepare extraction"); err != nil {
		return ExtractionState{}, err
	}
	leaseExpiresAt, err := validateExtractionPreparation(runInput, lease)
	if err != nil {
		return ExtractionState{}, fmt.Errorf("prepare extraction: %w", err)
	}
	if transaction == nil || transaction.transaction == nil {
		return ExtractionState{}, fmt.Errorf("prepare extraction: transaction is closed")
	}

	var insertedID string
	digest := runInput.DerivationDigest
	schemaDigest := runInput.SchemaDigest
	insertErr := transaction.transaction.QueryRow(ctx, `
		INSERT INTO stacks_core.extraction_runs (
			id, document_version_id, derivation_digest_version,
			derivation_digest, method, version, provider, data_mode, model,
			prompt_version, schema_digest, max_output_tokens, recorded_at, state
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'active'
		)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		runInput.ID,
		runInput.DocumentVersionID,
		runInput.DerivationDigestVersion,
		digest[:],
		runInput.Method,
		runInput.Version,
		runInput.Provider,
		runInput.DataMode,
		runInput.Model,
		runInput.PromptVersion,
		schemaDigest[:],
		runInput.MaxOutputTokens,
		runInput.RecordedAt,
	).Scan(&insertedID)
	inserted := insertErr == nil
	if insertErr != nil && !errors.Is(insertErr, pgx.ErrNoRows) {
		return ExtractionState{}, wrapExtractionError(
			ctx,
			"insert extraction run",
			conflictError(insertErr),
		)
	}

	storedRun, err := loadExtractionRunForUpdate(ctx, transaction.transaction, runInput.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		_, identityErr := loadExtractionRunByDerivationForUpdate(
			ctx,
			transaction.transaction,
			runInput.DocumentVersionID,
			runInput.DerivationDigestVersion,
			runInput.DerivationDigest,
		)
		if identityErr == nil {
			return ExtractionState{}, fmt.Errorf(
				"prepare extraction: derivation identity uses a different run ID: %w",
				ErrConflict,
			)
		}
		return ExtractionState{}, wrapExtractionError(ctx, "load extraction run", err)
	}
	if err != nil {
		return ExtractionState{}, wrapExtractionError(ctx, "load extraction run", err)
	}
	latest, latestErr := loadLatestExtractionAttemptForUpdate(
		ctx,
		transaction.transaction,
		runInput.ID,
	)
	if storedRun.state == "completed" {
		if !sameCompletedExtractionRunInput(storedRun.input, runInput) {
			return ExtractionState{}, fmt.Errorf(
				"prepare extraction: immutable completed run provenance: %w",
				ErrConflict,
			)
		}
		if latestErr != nil {
			return ExtractionState{}, wrapExtractionError(
				ctx,
				"load completed extraction attempt",
				latestErr,
			)
		}
		return completedExtractionState(storedRun, latest)
	}
	if !sameExtractionRunInput(storedRun.input, runInput) {
		return ExtractionState{}, fmt.Errorf(
			"prepare extraction: immutable run provenance: %w",
			ErrConflict,
		)
	}
	if inserted && latestErr == nil {
		return ExtractionState{}, fmt.Errorf(
			"prepare extraction: new run already has an attempt: %w",
			ErrConflict,
		)
	}
	if latestErr != nil && !errors.Is(latestErr, pgx.ErrNoRows) {
		return ExtractionState{}, wrapExtractionError(
			ctx,
			"load latest extraction attempt",
			latestErr,
		)
	}
	if !inserted && errors.Is(latestErr, pgx.ErrNoRows) {
		return ExtractionState{}, fmt.Errorf(
			"prepare extraction: run has no attempt history: %w",
			ErrConflict,
		)
	}

	nextAttemptNumber := 1
	if latestErr == nil {
		nextAttemptNumber = latest.number + 1
		switch latest.state {
		case "active":
			if sameLeaseAttempt(latest, lease, leaseExpiresAt) {
				return activeExtractionState(storedRun.input.ID, latest, ExtractionClaimed), nil
			}
			if lease.ClaimedAt.Before(latest.leaseExpiresAt) {
				return activeExtractionState(storedRun.input.ID, latest, ExtractionBusy), nil
			}
			if _, err := transaction.transaction.Exec(ctx, `
				UPDATE stacks_core.extraction_attempts
				SET state = 'expired', terminal_at = lease_expires_at
				WHERE id = $1
				  AND run_id = $2
				  AND state = 'active'`,
				latest.id,
				runInput.ID,
			); err != nil {
				return ExtractionState{}, wrapExtractionError(
					ctx,
					"expire extraction attempt",
					err,
				)
			}
		case "failed", "canceled", "expired":
			if lease.ClaimedAt.Before(*latest.terminalAt) {
				return ExtractionState{}, fmt.Errorf(
					"prepare extraction: retry precedes prior terminal time: %w",
					ErrConflict,
				)
			}
		default:
			return ExtractionState{}, fmt.Errorf(
				"prepare extraction: non-completed run has terminal completed attempt: %w",
				ErrConflict,
			)
		}
	}
	if lease.ClaimedAt.Before(storedRun.input.RecordedAt) {
		return ExtractionState{}, fmt.Errorf(
			"prepare extraction: claim precedes run recorded time: %w",
			ErrConflict,
		)
	}

	if _, err := transaction.transaction.Exec(ctx, `
		INSERT INTO stacks_core.extraction_attempts (
			id, run_id, attempt_number, owner, claimed_at,
			lease_expires_at, state
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'active')`,
		lease.AttemptID,
		runInput.ID,
		nextAttemptNumber,
		lease.Owner,
		lease.ClaimedAt,
		leaseExpiresAt,
	); err != nil {
		return ExtractionState{}, wrapExtractionError(
			ctx,
			"insert extraction attempt",
			conflictError(err),
		)
	}
	if storedRun.state != "active" {
		tag, err := transaction.transaction.Exec(ctx, `
			UPDATE stacks_core.extraction_runs
			SET state = 'active'
			WHERE id = $1
			  AND state IN ('failed')`,
			runInput.ID,
		)
		if err != nil {
			return ExtractionState{}, wrapExtractionError(ctx, "reactivate extraction run", err)
		}
		if tag.RowsAffected() != 1 {
			return ExtractionState{}, fmt.Errorf(
				"prepare extraction: run state changed while claiming: %w",
				ErrConflict,
			)
		}
	}
	return ExtractionState{
		RunID:          runInput.ID,
		AttemptID:      lease.AttemptID,
		AttemptNumber:  nextAttemptNumber,
		Status:         ExtractionClaimed,
		LeaseExpiresAt: leaseExpiresAt,
	}, nil
}

// RecordExtractionFailure appends a terminal outcome to the active attempt
// without removing any prior attempt.
func (transaction *Transaction) RecordExtractionFailure(
	ctx context.Context,
	input ExtractionFailureInput,
) error {
	if err := contextRequired(ctx, "record extraction failure"); err != nil {
		return err
	}
	if err := validateExtractionFailure(input); err != nil {
		return fmt.Errorf("record extraction failure: %w", err)
	}
	if transaction == nil || transaction.transaction == nil {
		return fmt.Errorf("record extraction failure: transaction is closed")
	}

	run, err := loadExtractionRunForUpdate(ctx, transaction.transaction, input.RunID)
	if err != nil {
		return wrapExtractionError(ctx, "load extraction run for failure", err)
	}
	attempt, err := loadLatestExtractionAttemptForUpdate(
		ctx,
		transaction.transaction,
		input.RunID,
	)
	if err != nil {
		return wrapExtractionError(ctx, "load extraction attempt for failure", err)
	}
	if run.state == "failed" &&
		attempt.state == "failed" &&
		attempt.id == input.AttemptID &&
		attempt.owner == input.Owner &&
		attempt.terminalAt != nil &&
		*attempt.terminalAt == input.FailedAt &&
		attempt.failureCode != nil &&
		*attempt.failureCode == input.FailureCode {
		return nil
	}
	if run.state != "active" ||
		attempt.state != "active" ||
		attempt.id != input.AttemptID ||
		attempt.owner != input.Owner ||
		input.FailedAt.Before(attempt.claimedAt) ||
		!input.FailedAt.Before(attempt.leaseExpiresAt) {
		return fmt.Errorf(
			"record extraction failure: attempt is not actively owned: %w",
			ErrConflict,
		)
	}

	tag, err := transaction.transaction.Exec(ctx, `
		UPDATE stacks_core.extraction_attempts
		SET state = 'failed', terminal_at = $4, failure_code = $5
		WHERE id = $1
		  AND run_id = $2
		  AND owner = $3
		  AND state = 'active'`,
		input.AttemptID,
		input.RunID,
		input.Owner,
		input.FailedAt,
		input.FailureCode,
	)
	if err != nil {
		return wrapExtractionError(ctx, "fail extraction attempt", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("record extraction failure: attempt changed: %w", ErrConflict)
	}
	tag, err = transaction.transaction.Exec(ctx, `
		UPDATE stacks_core.extraction_runs
		SET state = 'failed'
		WHERE id = $1
		  AND state = 'active'`,
		input.RunID,
	)
	if err != nil {
		return wrapExtractionError(ctx, "fail extraction run", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("record extraction failure: run changed: %w", ErrConflict)
	}
	return nil
}

// CheckExtractionCompletion locks the owning run and latest attempt before
// caller-owned payload writes. It reports completed only for an exact
// read-only retry of the stored completion.
func (transaction *Transaction) CheckExtractionCompletion(
	ctx context.Context,
	input ExtractionCompletionCheckInput,
) (ExtractionStatus, error) {
	if err := contextRequired(ctx, "check extraction completion"); err != nil {
		return "", err
	}
	if strings.TrimSpace(input.DocumentVersionID) == "" {
		return "", fmt.Errorf("check extraction completion: document version is required")
	}
	if err := validateExtractionCompletion(input.Completion); err != nil {
		return "", fmt.Errorf("check extraction completion: %w", err)
	}
	if transaction == nil || transaction.transaction == nil {
		return "", fmt.Errorf("check extraction completion: transaction is closed")
	}

	run, err := loadExtractionRunForUpdate(
		ctx,
		transaction.transaction,
		input.Completion.RunID,
	)
	if err != nil {
		return "", wrapExtractionError(
			ctx,
			"load extraction run for completion check",
			err,
		)
	}
	attempt, err := loadLatestExtractionAttemptForUpdate(
		ctx,
		transaction.transaction,
		input.Completion.RunID,
	)
	if err != nil {
		return "", wrapExtractionError(
			ctx,
			"load extraction attempt for completion check",
			err,
		)
	}
	if run.input.DocumentVersionID != input.DocumentVersionID {
		return "", fmt.Errorf(
			"check extraction completion: run owns a different document version: %w",
			ErrConflict,
		)
	}
	if run.state == "completed" {
		if exactExtractionCompletion(run, attempt, input.Completion) {
			return ExtractionCompleted, nil
		}
		return "", fmt.Errorf(
			"check extraction completion: completed write set differs: %w",
			ErrConflict,
		)
	}
	completion := input.Completion
	if run.state != "active" ||
		attempt.state != "active" ||
		attempt.id != completion.AttemptID ||
		attempt.owner != completion.Owner ||
		completion.CompletedAt.Before(attempt.claimedAt) ||
		!completion.CompletedAt.Before(attempt.leaseExpiresAt) {
		return "", fmt.Errorf(
			"check extraction completion: attempt is not actively owned: %w",
			ErrConflict,
		)
	}
	return ExtractionClaimed, nil
}

// CompleteExtraction stores the caller-supplied versioned canonical write-set
// digest and completes both the current attempt and run in the same transaction.
func (transaction *Transaction) CompleteExtraction(
	ctx context.Context,
	input ExtractionCompletionInput,
) error {
	if err := contextRequired(ctx, "complete extraction"); err != nil {
		return err
	}
	if err := validateExtractionCompletion(input); err != nil {
		return fmt.Errorf("complete extraction: %w", err)
	}
	if transaction == nil || transaction.transaction == nil {
		return fmt.Errorf("complete extraction: transaction is closed")
	}

	run, err := loadExtractionRunForUpdate(ctx, transaction.transaction, input.RunID)
	if err != nil {
		return wrapExtractionError(ctx, "load extraction run for completion", err)
	}
	attempt, err := loadLatestExtractionAttemptForUpdate(
		ctx,
		transaction.transaction,
		input.RunID,
	)
	if err != nil {
		return wrapExtractionError(ctx, "load extraction attempt for completion", err)
	}
	if run.state == "completed" {
		if exactExtractionCompletion(run, attempt, input) {
			return nil
		}
		return fmt.Errorf(
			"complete extraction: completed write set differs: %w",
			ErrConflict,
		)
	}
	if run.state != "active" ||
		attempt.state != "active" ||
		attempt.id != input.AttemptID ||
		attempt.owner != input.Owner ||
		input.CompletedAt.Before(attempt.claimedAt) ||
		!input.CompletedAt.Before(attempt.leaseExpiresAt) {
		return fmt.Errorf(
			"complete extraction: attempt is not actively owned: %w",
			ErrConflict,
		)
	}

	tag, err := transaction.transaction.Exec(ctx, `
		UPDATE stacks_core.extraction_attempts
		SET state = 'completed', terminal_at = $4
		WHERE id = $1
		  AND run_id = $2
		  AND owner = $3
		  AND state = 'active'`,
		input.AttemptID,
		input.RunID,
		input.Owner,
		input.CompletedAt,
	)
	if err != nil {
		return wrapExtractionError(ctx, "complete extraction attempt", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("complete extraction: attempt changed: %w", ErrConflict)
	}
	writeSetDigest := input.WriteSetDigest
	tag, err = transaction.transaction.Exec(ctx, `
		UPDATE stacks_core.extraction_runs
		SET
			state = 'completed',
			completed_at = $2,
			write_set_digest_version = $3,
			write_set_digest = $4
		WHERE id = $1
		  AND state = 'active'`,
		input.RunID,
		input.CompletedAt,
		input.WriteSetDigestVersion,
		writeSetDigest[:],
	)
	if err != nil {
		return wrapExtractionError(ctx, "complete extraction run", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("complete extraction: run changed: %w", ErrConflict)
	}
	return nil
}

func validateExtractionPreparation(
	run ExtractionRunInput,
	lease LeaseRequest,
) (time.Time, error) {
	if strings.TrimSpace(run.ID) == "" ||
		strings.TrimSpace(run.DocumentVersionID) == "" ||
		strings.TrimSpace(run.DerivationDigestVersion) == "" ||
		run.DerivationDigest == (evidence.ContentDigest{}) ||
		strings.TrimSpace(run.Method) == "" ||
		strings.TrimSpace(run.Version) == "" ||
		strings.TrimSpace(run.Provider) == "" ||
		strings.TrimSpace(run.DataMode) == "" ||
		strings.TrimSpace(run.Model) == "" ||
		strings.TrimSpace(run.PromptVersion) == "" ||
		run.SchemaDigest == (evidence.ContentDigest{}) ||
		run.MaxOutputTokens <= 0 {
		return time.Time{}, fmt.Errorf("canonical extraction run provenance is required")
	}
	if !timepoint.IsCanonical(run.RecordedAt) {
		return time.Time{}, fmt.Errorf(
			"extraction run recorded time must use canonical UTC microsecond precision",
		)
	}
	if strings.TrimSpace(lease.AttemptID) == "" ||
		strings.TrimSpace(lease.Owner) == "" ||
		lease.LeaseDuration <= 0 ||
		lease.LeaseDuration%timepoint.Precision != 0 {
		return time.Time{}, fmt.Errorf("canonical extraction lease is required")
	}
	if !timepoint.IsCanonical(lease.ClaimedAt) {
		return time.Time{}, fmt.Errorf(
			"extraction claim time must use canonical UTC microsecond precision",
		)
	}
	expiresAt := lease.ClaimedAt.Add(lease.LeaseDuration)
	if !expiresAt.After(lease.ClaimedAt) || !timepoint.IsCanonical(expiresAt) {
		return time.Time{}, fmt.Errorf(
			"extraction lease expiry must use canonical UTC microsecond precision",
		)
	}
	return expiresAt, nil
}

func validateExtractionFailure(input ExtractionFailureInput) error {
	if strings.TrimSpace(input.RunID) == "" ||
		strings.TrimSpace(input.AttemptID) == "" ||
		strings.TrimSpace(input.Owner) == "" ||
		strings.TrimSpace(input.FailureCode) == "" {
		return fmt.Errorf("canonical extraction failure is required")
	}
	if !timepoint.IsCanonical(input.FailedAt) {
		return fmt.Errorf(
			"extraction failure time must use canonical UTC microsecond precision",
		)
	}
	return nil
}

func validateExtractionCompletion(input ExtractionCompletionInput) error {
	if strings.TrimSpace(input.RunID) == "" ||
		strings.TrimSpace(input.AttemptID) == "" ||
		strings.TrimSpace(input.Owner) == "" ||
		strings.TrimSpace(input.WriteSetDigestVersion) == "" ||
		input.WriteSetDigest == (evidence.ContentDigest{}) {
		return fmt.Errorf("canonical extraction completion is required")
	}
	if !timepoint.IsCanonical(input.CompletedAt) {
		return fmt.Errorf(
			"extraction completion time must use canonical UTC microsecond precision",
		)
	}
	return nil
}

func loadExtractionRunForUpdate(
	ctx context.Context,
	reader documentReader,
	runID string,
) (storedExtractionRun, error) {
	return loadExtractionRunQuery(ctx, reader, `
		SELECT
			id, document_version_id, derivation_digest_version,
			derivation_digest, method, version, provider, data_mode, model,
			prompt_version, schema_digest, max_output_tokens, recorded_at,
			state, completed_at, write_set_digest_version, write_set_digest
		FROM stacks_core.extraction_runs
		WHERE id = $1
		FOR UPDATE`,
		runID,
	)
}

func loadExtractionRunByDerivationForUpdate(
	ctx context.Context,
	reader documentReader,
	documentVersionID string,
	digestVersion string,
	digest evidence.ContentDigest,
) (storedExtractionRun, error) {
	return loadExtractionRunQuery(ctx, reader, `
		SELECT
			id, document_version_id, derivation_digest_version,
			derivation_digest, method, version, provider, data_mode, model,
			prompt_version, schema_digest, max_output_tokens, recorded_at,
			state, completed_at, write_set_digest_version, write_set_digest
		FROM stacks_core.extraction_runs
		WHERE document_version_id = $1
		  AND derivation_digest_version = $2
		  AND derivation_digest = $3
		FOR UPDATE`,
		documentVersionID,
		digestVersion,
		digest[:],
	)
}

func loadExtractionRunQuery(
	ctx context.Context,
	reader documentReader,
	query string,
	arguments ...any,
) (storedExtractionRun, error) {
	var (
		run                   storedExtractionRun
		derivationDigest      []byte
		schemaDigest          []byte
		completedAt           *time.Time
		writeSetDigestVersion *string
		writeSetDigest        []byte
	)
	if err := reader.QueryRow(ctx, query, arguments...).Scan(
		&run.input.ID,
		&run.input.DocumentVersionID,
		&run.input.DerivationDigestVersion,
		&derivationDigest,
		&run.input.Method,
		&run.input.Version,
		&run.input.Provider,
		&run.input.DataMode,
		&run.input.Model,
		&run.input.PromptVersion,
		&schemaDigest,
		&run.input.MaxOutputTokens,
		&run.input.RecordedAt,
		&run.state,
		&completedAt,
		&writeSetDigestVersion,
		&writeSetDigest,
	); err != nil {
		return storedExtractionRun{}, err
	}
	recordedAt, err := canonicalStoredTime(run.input.RecordedAt)
	if err != nil {
		return storedExtractionRun{}, fmt.Errorf("stored extraction recorded time: %w", err)
	}
	run.input.RecordedAt = recordedAt
	parsedDerivationDigest, err := evidenceDigest(derivationDigest)
	if err != nil {
		return storedExtractionRun{}, fmt.Errorf("stored extraction derivation digest: %w", err)
	}
	parsedSchemaDigest, err := evidenceDigest(schemaDigest)
	if err != nil {
		return storedExtractionRun{}, fmt.Errorf("stored extraction schema digest: %w", err)
	}
	run.input.DerivationDigest = parsedDerivationDigest
	run.input.SchemaDigest = parsedSchemaDigest
	if completedAt != nil {
		canonicalCompletedAt, err := canonicalStoredTime(*completedAt)
		if err != nil {
			return storedExtractionRun{}, fmt.Errorf("stored extraction completed time: %w", err)
		}
		completedAt = &canonicalCompletedAt
	}
	run.completedAt = completedAt
	run.writeSetDigestVersion = writeSetDigestVersion
	run.writeSetDigest = writeSetDigest
	if run.state == "completed" {
		if completedAt == nil ||
			writeSetDigestVersion == nil ||
			strings.TrimSpace(*writeSetDigestVersion) == "" ||
			len(writeSetDigest) != len(evidence.ContentDigest{}) {
			return storedExtractionRun{}, fmt.Errorf(
				"stored extraction completion is invalid: %w",
				ErrConflict,
			)
		}
	} else if completedAt != nil || writeSetDigestVersion != nil || writeSetDigest != nil {
		return storedExtractionRun{}, fmt.Errorf(
			"stored incomplete extraction has completion payload: %w",
			ErrConflict,
		)
	}
	return run, nil
}

func loadLatestExtractionAttemptForUpdate(
	ctx context.Context,
	reader documentReader,
	runID string,
) (storedExtractionAttempt, error) {
	var attempt storedExtractionAttempt
	if err := reader.QueryRow(ctx, `
		SELECT
			id, run_id, attempt_number, owner, claimed_at,
			lease_expires_at, state, terminal_at, failure_code
		FROM stacks_core.extraction_attempts
		WHERE run_id = $1
		ORDER BY attempt_number DESC
		LIMIT 1
		FOR UPDATE`,
		runID,
	).Scan(
		&attempt.id,
		&attempt.runID,
		&attempt.number,
		&attempt.owner,
		&attempt.claimedAt,
		&attempt.leaseExpiresAt,
		&attempt.state,
		&attempt.terminalAt,
		&attempt.failureCode,
	); err != nil {
		return storedExtractionAttempt{}, err
	}
	claimedAt, err := canonicalStoredTime(attempt.claimedAt)
	if err != nil {
		return storedExtractionAttempt{}, fmt.Errorf("stored extraction claim time: %w", err)
	}
	leaseExpiresAt, err := canonicalStoredTime(attempt.leaseExpiresAt)
	if err != nil {
		return storedExtractionAttempt{}, fmt.Errorf("stored extraction lease expiry: %w", err)
	}
	attempt.claimedAt = claimedAt
	attempt.leaseExpiresAt = leaseExpiresAt
	if attempt.terminalAt != nil {
		terminalAt, err := canonicalStoredTime(*attempt.terminalAt)
		if err != nil {
			return storedExtractionAttempt{}, fmt.Errorf("stored extraction terminal time: %w", err)
		}
		attempt.terminalAt = &terminalAt
	}
	if attempt.number <= 0 ||
		strings.TrimSpace(attempt.id) == "" ||
		strings.TrimSpace(attempt.runID) == "" ||
		strings.TrimSpace(attempt.owner) == "" ||
		!attempt.leaseExpiresAt.After(attempt.claimedAt) {
		return storedExtractionAttempt{}, fmt.Errorf(
			"stored extraction attempt is invalid: %w",
			ErrConflict,
		)
	}
	switch attempt.state {
	case "active":
		if attempt.terminalAt != nil || attempt.failureCode != nil {
			return storedExtractionAttempt{}, fmt.Errorf(
				"stored active extraction attempt is terminal: %w",
				ErrConflict,
			)
		}
	case "failed":
		if attempt.terminalAt == nil ||
			attempt.failureCode == nil ||
			strings.TrimSpace(*attempt.failureCode) == "" {
			return storedExtractionAttempt{}, fmt.Errorf(
				"stored failed extraction attempt is incomplete: %w",
				ErrConflict,
			)
		}
	case "completed", "canceled", "expired":
		if attempt.terminalAt == nil || attempt.failureCode != nil {
			return storedExtractionAttempt{}, fmt.Errorf(
				"stored terminal extraction attempt is invalid: %w",
				ErrConflict,
			)
		}
	default:
		return storedExtractionAttempt{}, fmt.Errorf(
			"stored extraction attempt state is invalid: %w",
			ErrConflict,
		)
	}
	return attempt, nil
}

func sameExtractionRunInput(left, right ExtractionRunInput) bool {
	return left == right
}

func sameCompletedExtractionRunInput(left, right ExtractionRunInput) bool {
	right.DataMode = left.DataMode
	return left == right
}

func sameLeaseAttempt(
	stored storedExtractionAttempt,
	supplied LeaseRequest,
	expiresAt time.Time,
) bool {
	return stored.id == supplied.AttemptID &&
		stored.owner == supplied.Owner &&
		stored.claimedAt == supplied.ClaimedAt &&
		stored.leaseExpiresAt == expiresAt
}

func activeExtractionState(
	runID string,
	attempt storedExtractionAttempt,
	status ExtractionStatus,
) ExtractionState {
	return ExtractionState{
		RunID:          runID,
		AttemptID:      attempt.id,
		AttemptNumber:  attempt.number,
		Status:         status,
		LeaseExpiresAt: attempt.leaseExpiresAt,
	}
}

func completedExtractionState(
	run storedExtractionRun,
	attempt storedExtractionAttempt,
) (ExtractionState, error) {
	if attempt.state != "completed" ||
		run.completedAt == nil ||
		attempt.terminalAt == nil ||
		*run.completedAt != *attempt.terminalAt ||
		run.writeSetDigestVersion == nil {
		return ExtractionState{}, fmt.Errorf(
			"stored completed extraction lifecycle is inconsistent: %w",
			ErrConflict,
		)
	}
	writeSetDigest, err := evidenceDigest(run.writeSetDigest)
	if err != nil {
		return ExtractionState{}, fmt.Errorf("stored extraction write-set digest: %w", err)
	}
	return ExtractionState{
		RunID:                 run.input.ID,
		AttemptID:             attempt.id,
		AttemptNumber:         attempt.number,
		Status:                ExtractionCompleted,
		LeaseExpiresAt:        attempt.leaseExpiresAt,
		HasWriteSetDigest:     true,
		WriteSetDigestVersion: *run.writeSetDigestVersion,
		WriteSetDigest:        writeSetDigest,
	}, nil
}

func exactExtractionCompletion(
	run storedExtractionRun,
	attempt storedExtractionAttempt,
	input ExtractionCompletionInput,
) bool {
	if run.completedAt == nil ||
		run.writeSetDigestVersion == nil ||
		attempt.terminalAt == nil {
		return false
	}
	writeSetDigest, err := evidenceDigest(run.writeSetDigest)
	return err == nil &&
		attempt.state == "completed" &&
		attempt.id == input.AttemptID &&
		attempt.owner == input.Owner &&
		*attempt.terminalAt == input.CompletedAt &&
		*run.completedAt == input.CompletedAt &&
		*run.writeSetDigestVersion == input.WriteSetDigestVersion &&
		writeSetDigest == input.WriteSetDigest
}

func wrapExtractionError(ctx context.Context, operation string, err error) error {
	if ctx != nil {
		if contextError := ctx.Err(); contextError != nil {
			return fmt.Errorf("%s: %w", operation, contextError)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
