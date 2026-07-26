package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type legacyObservationQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

const (
	reasonLegacyObservationQueryFailed             = "legacy_observation_query_failed"
	reasonLegacySignalEvidenceQueryFailed          = "legacy_signal_evidence_query_failed"
	reasonLegacySignalEvidenceScanFailed           = "legacy_signal_evidence_scan_failed"
	reasonLegacySignalEvidenceIterationFailed      = "legacy_signal_evidence_iteration_failed"
	reasonLegacyObservationEvidenceQueryFailed     = "legacy_observation_evidence_query_failed"
	reasonLegacyObservationEvidenceScanFailed      = "legacy_observation_evidence_scan_failed"
	reasonLegacyObservationEvidenceIterationFailed = "legacy_observation_evidence_iteration_failed"
	unknownLegacyObservationOperationIdentifier    = "unknown"
)

type legacyObservationStorageOperationError struct {
	reason        string
	observationID string
	cause         error
}

type observationConflictStorageError struct {
	reason        string
	observationID string
	cause         error
}

func (err *observationConflictStorageError) Error() string {
	return fmt.Sprintf("observation boundary %q: %s", err.observationID, err.reason)
}

func (err *observationConflictStorageError) Unwrap() []error {
	return []error{ErrObservationConflict, err.cause}
}

func newObservationConflictStorageError(reason, observationID string, cause error) error {
	canonicalID, err := canonicalUUID(observationID)
	if err != nil {
		canonicalID = unknownLegacyObservationOperationIdentifier
	}
	return &observationConflictStorageError{reason: reason, observationID: canonicalID, cause: cause}
}

func (err *legacyObservationStorageOperationError) Error() string {
	return fmt.Sprintf("legacy observation storage %q: %s", err.observationID, err.reason)
}

func (err *legacyObservationStorageOperationError) Unwrap() error { return err.cause }

func newLegacyObservationStorageOperationError(reason, observationID string, cause error) error {
	canonicalID, err := canonicalUUID(observationID)
	if err != nil {
		canonicalID = unknownLegacyObservationOperationIdentifier
	}
	return &legacyObservationStorageOperationError{reason: reason, observationID: canonicalID, cause: cause}
}

func loadLegacyObservation(
	ctx context.Context,
	query legacyObservationQuerier,
	observationID string,
) (decodedLegacyObservation, error) {
	row, run, signal, err := scanLegacyObservationState(ctx, query, observationID)
	if err != nil {
		return decodedLegacyObservation{}, err
	}
	origin, err := loadObservationEvidenceOrigin(ctx, query, observationID)
	if err != nil {
		return decodedLegacyObservation{}, err
	}
	decoded, err := decodeLegacyObservation(row, origin, signal, run)
	if err != nil {
		return decodedLegacyObservation{}, err
	}
	expected, err := computeObservationDigestV1(legacyObservationWrite{
		Row: row, Origin: origin, Signal: signal,
	})
	if err != nil || expected != row.Digest {
		return decodedLegacyObservation{}, newObservationBoundaryError(
			ErrObservationCompatibility, reasonObservationDigestMismatch, observationID,
		)
	}
	if signal != nil {
		expectedSignalDigest, err := ComputeSignalDigest(signal.Input, signal.Evidence)
		if err != nil || expectedSignalDigest != signal.Digest {
			return decodedLegacyObservation{}, newObservationBoundaryError(
				ErrObservationCompatibility, reasonSignalDigestMismatch, observationID,
			)
		}
	}
	return decoded, nil
}

func scanLegacyObservationState(
	ctx context.Context,
	query legacyObservationQuerier,
	observationID string,
) (legacyObservationRow, *owningExtractionRun, *legacySignalState, error) {
	var row legacyObservationRow
	var observationDigest []byte
	var runID, runModelID, runPromptVersion *string
	var runRecordedAt *time.Time
	var signalID, signalObservationID, signalCategory, signalDirection *string
	var signalModelID, signalPromptVersion, signalRationale *string
	var signalConfidence *float64
	var signalDigest []byte
	err := query.QueryRow(ctx, `
		SELECT observation.id::text, COALESCE(observation.extraction_run_id::text, ''),
		       COALESCE(observation.subject_entity_id::text, ''), COALESCE(observation.object_entity_id::text, ''),
		       COALESCE(observation.subject_mention_id::text, ''), COALESCE(observation.object_mention_id::text, ''),
		       observation.predicate, observation.valid_start, observation.valid_end, observation.recorded_at,
		       observation.derivation, observation.epistemic_status, observation.confidence, observation.digest,
		       extraction_run.id::text, extraction_run.model_id, extraction_run.prompt_version, extraction_run.recorded_at,
		       signal.id::text, signal.observation_id::text, signal.category, signal.direction,
		       signal.extraction_model_id, signal.prompt_version, signal.rationale, signal.confidence, signal.digest
		FROM stacks.observations AS observation
		LEFT JOIN stacks.extraction_runs AS extraction_run ON extraction_run.id = observation.extraction_run_id
		LEFT JOIN stacks.interaction_signals AS signal ON signal.observation_id = observation.id
		WHERE observation.id = $1`, observationID).Scan(
		&row.ID, &row.ExtractionRunID, &row.SubjectEntityID, &row.ObjectEntityID,
		&row.SubjectMentionID, &row.ObjectMentionID, &row.Predicate, &row.ValidStart, &row.ValidEnd,
		&row.RecordedAt, &row.Derivation, &row.EpistemicStatus, &row.Confidence, &observationDigest,
		&runID, &runModelID, &runPromptVersion, &runRecordedAt,
		&signalID, &signalObservationID, &signalCategory, &signalDirection,
		&signalModelID, &signalPromptVersion, &signalRationale, &signalConfidence, &signalDigest,
	)
	if err != nil {
		return legacyObservationRow{}, nil, nil, newLegacyObservationStorageOperationError(
			reasonLegacyObservationQueryFailed, observationID, err,
		)
	}
	if len(observationDigest) != sha256.Size {
		return legacyObservationRow{}, nil, nil, newObservationBoundaryError(
			ErrObservationCompatibility, reasonInvalidLegacyShape, observationID,
		)
	}
	copy(row.Digest[:], observationDigest)

	var run *owningExtractionRun
	if runID != nil {
		if runModelID == nil || runPromptVersion == nil || runRecordedAt == nil {
			return legacyObservationRow{}, nil, nil, newObservationBoundaryError(
				ErrObservationCompatibility, reasonInvalidLegacyShape, observationID,
			)
		}
		run = &owningExtractionRun{
			ID: *runID, ModelID: *runModelID, PromptVersion: *runPromptVersion, RecordedAt: *runRecordedAt,
		}
	}
	if signalID == nil {
		return row, run, nil, nil
	}
	if signalObservationID == nil || signalCategory == nil || signalDirection == nil || signalModelID == nil ||
		signalPromptVersion == nil || signalRationale == nil || signalConfidence == nil || len(signalDigest) != sha256.Size {
		return legacyObservationRow{}, nil, nil, newObservationBoundaryError(
			ErrObservationCompatibility, reasonInvalidLegacyShape, observationID,
		)
	}
	signal := &legacySignalState{Input: SignalInput{
		ID: *signalID, ObservationID: *signalObservationID, Category: *signalCategory, Direction: *signalDirection,
		ExtractionModelID: *signalModelID, PromptVersion: *signalPromptVersion, Rationale: *signalRationale,
		Confidence: *signalConfidence,
	}}
	copy(signal.Digest[:], signalDigest)

	rows, err := query.Query(ctx, `
		SELECT evidence_span_id::text, role
		FROM stacks.signal_evidence
		WHERE signal_id = $1
		ORDER BY evidence_span_id, role`, signal.Input.ID)
	if err != nil {
		return legacyObservationRow{}, nil, nil, newLegacyObservationStorageOperationError(
			reasonLegacySignalEvidenceQueryFailed, observationID, err,
		)
	}
	defer rows.Close()
	for rows.Next() {
		var signalEvidence SignalEvidenceInput
		if err := rows.Scan(&signalEvidence.EvidenceSpanID, &signalEvidence.Role); err != nil {
			return legacyObservationRow{}, nil, nil, newLegacyObservationStorageOperationError(
				reasonLegacySignalEvidenceScanFailed, observationID, err,
			)
		}
		signal.Evidence = append(signal.Evidence, signalEvidence)
	}
	if err := rows.Err(); err != nil {
		return legacyObservationRow{}, nil, nil, newLegacyObservationStorageOperationError(
			reasonLegacySignalEvidenceIterationFailed, observationID, err,
		)
	}
	return row, run, signal, nil
}

func loadObservationEvidenceOrigin(
	ctx context.Context,
	query legacyObservationQuerier,
	observationID string,
) ([]evidence.EvidenceID, error) {
	rows, err := query.Query(ctx, `
		SELECT evidence_span_id::text
		FROM stacks.observation_evidence
		WHERE observation_id = $1
		ORDER BY evidence_span_id`, observationID)
	if err != nil {
		return nil, newLegacyObservationStorageOperationError(
			reasonLegacyObservationEvidenceQueryFailed, observationID, err,
		)
	}
	defer rows.Close()
	origin := make([]evidence.EvidenceID, 0)
	for rows.Next() {
		var evidenceID string
		if err := rows.Scan(&evidenceID); err != nil {
			return nil, newLegacyObservationStorageOperationError(
				reasonLegacyObservationEvidenceScanFailed, observationID, err,
			)
		}
		origin = append(origin, evidence.EvidenceID(evidenceID))
	}
	if err := rows.Err(); err != nil {
		return nil, newLegacyObservationStorageOperationError(
			reasonLegacyObservationEvidenceIterationFailed, observationID, err,
		)
	}
	return origin, nil
}

// putLegacyObservation writes the complete legacy PostgreSQL projection for a
// canonical observation. Stable-ID retries load the existing projection.
func putLegacyObservation(
	ctx context.Context,
	transaction pgx.Tx,
	write legacyObservationWrite,
) (observation.Observation, *InteractionSignal, error) {
	var storedID string
	err := transaction.QueryRow(ctx, `
		INSERT INTO stacks.observations
			(id, extraction_run_id, subject_entity_id, object_entity_id, subject_mention_id, object_mention_id,
			 predicate, valid_start, valid_end, recorded_at, derivation, epistemic_status, confidence, digest,
			 currently_admissible)
		VALUES ($1::uuid, $2::uuid, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, NULLIF($6, '')::uuid,
		        $7, $8, $9, $10, $11, $12, $13, $14, true)
		ON CONFLICT (id) DO NOTHING
		RETURNING id::text`,
		write.Row.ID, write.Row.ExtractionRunID, write.Row.SubjectEntityID, write.Row.ObjectEntityID,
		write.Row.SubjectMentionID, write.Row.ObjectMentionID, write.Row.Predicate, write.Row.ValidStart,
		write.Row.ValidEnd, write.Row.RecordedAt, write.Row.Derivation, write.Row.EpistemicStatus,
		write.Row.Confidence, write.Row.Digest[:],
	).Scan(&storedID)
	if err == pgx.ErrNoRows {
		stored, err := loadLegacyObservation(ctx, transaction, write.Row.ID)
		if err != nil {
			return observation.Observation{}, nil, err
		}
		if !equalLegacyObservationRetry(write, stored) {
			return observation.Observation{}, nil, newObservationBoundaryError(ErrObservationConflict, reasonCompletionWriteSetMismatch, write.Row.ID)
		}
		value, signal := legacyCompletionResult(stored)
		return value, signal, nil
	}
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return observation.Observation{}, nil, newObservationConflictStorageError(reasonCompletionWriteSetMismatch, write.Row.ID, databaseError)
		}
		return observation.Observation{}, nil, fmt.Errorf("persist legacy observation %q: %w", write.Row.ID, err)
	}

	for _, evidenceID := range write.Origin {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO stacks.observation_evidence (observation_id, evidence_span_id)
			VALUES ($1::uuid, $2::uuid)
			ON CONFLICT DO NOTHING`, write.Row.ID, string(evidenceID)); err != nil {
			return observation.Observation{}, nil, fmt.Errorf("persist legacy observation evidence %q: %w", write.Row.ID, err)
		}
	}
	if write.Signal != nil {
		signal, err := putSignal(ctx, transaction, write.Signal.Input, write.Signal.Digest[:])
		if err != nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) && databaseError.Code == "23505" {
				return observation.Observation{}, nil, newObservationConflictStorageError(reasonCompletionWriteSetMismatch, write.Row.ID, databaseError)
			}
			return observation.Observation{}, nil, err
		}
		for _, signalEvidence := range write.Signal.Evidence {
			if _, err := transaction.Exec(ctx, `
				INSERT INTO stacks.signal_evidence (signal_id, evidence_span_id, role)
				VALUES ($1::uuid, $2::uuid, $3)
				ON CONFLICT DO NOTHING`, signal.ID, signalEvidence.EvidenceSpanID, signalEvidence.Role); err != nil {
				return observation.Observation{}, nil, fmt.Errorf("persist legacy signal evidence %q: %w", signal.ID, err)
			}
		}
	}
	stored, err := loadLegacyObservation(ctx, transaction, storedID)
	if err != nil {
		return observation.Observation{}, nil, err
	}
	if !equalLegacyObservationRetry(write, stored) {
		return observation.Observation{}, nil, newObservationBoundaryError(ErrObservationConflict, reasonCompletionWriteSetMismatch, write.Row.ID)
	}
	value, signal := legacyCompletionResult(stored)
	return value, signal, nil
}

func legacyCompletionResult(stored decodedLegacyObservation) (observation.Observation, *InteractionSignal) {
	if stored.Signal == nil {
		return stored.Observation, nil
	}
	return stored.Observation, &InteractionSignal{ID: stored.Signal.Input.ID}
}

func equalLegacyObservationRetry(expected legacyObservationWrite, stored decodedLegacyObservation) bool {
	if stored.Observation.LegacyUncited() {
		return false
	}
	derivation := stored.Observation.Derivation()
	if derivation.LegacyUnversioned {
		return false
	}
	run := &owningExtractionRun{
		ID: derivation.RunID, ModelID: derivation.Model, PromptVersion: derivation.PromptVersion,
		RecordedAt: stored.Observation.RecordedAt(),
	}
	actual, err := encodeLegacyObservation(stored.Observation, stored.Compatibility, run, stored.Signal)
	if err != nil || !equalLegacyObservationRows(expected.Row, actual.Row) ||
		!equalLegacyEvidenceOrigin(expected.Origin, actual.Origin) || !equalLegacySignalState(expected.Signal, actual.Signal) {
		return false
	}
	return true
}

func equalLegacyEvidenceOrigin(left, right []evidence.EvidenceID) bool {
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

func equalLegacyObservationRows(left, right legacyObservationRow) bool {
	return left.ID == right.ID && left.ExtractionRunID == right.ExtractionRunID &&
		left.SubjectEntityID == right.SubjectEntityID && left.ObjectEntityID == right.ObjectEntityID &&
		left.SubjectMentionID == right.SubjectMentionID && left.ObjectMentionID == right.ObjectMentionID &&
		left.Predicate == right.Predicate && left.Derivation == right.Derivation && left.EpistemicStatus == right.EpistemicStatus &&
		equalLegacyTimes(left.ValidStart, right.ValidStart) && equalLegacyTimes(left.ValidEnd, right.ValidEnd) &&
		left.RecordedAt.Equal(right.RecordedAt) && equalLegacyConfidenceValues(left.Confidence, right.Confidence) && left.Digest == right.Digest
}

func equalLegacyTimes(left, right *time.Time) bool {
	return left == nil && right == nil || left != nil && right != nil && left.Equal(*right)
}

func equalLegacyConfidenceValues(left, right *float64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalLegacySignalState(left, right *legacySignalState) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Input == right.Input && left.Digest == right.Digest && sameSignalEvidenceInputs(left.Evidence, right.Evidence)
}

func sameSignalEvidenceInputs(left, right []SignalEvidenceInput) bool {
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
