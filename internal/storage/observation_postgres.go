package storage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/jackc/pgx/v5"
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
	var origin []evidence.EvidenceID
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
