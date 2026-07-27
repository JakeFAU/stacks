package postgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
	"github.com/JakeFAU/stacks/core/timepoint"
	"github.com/jackc/pgx/v5"
)

const (
	// These hard ceilings match the approved maximum configurable query limits.
	// The root query service may impose smaller operator-configured limits.
	maxTemporalQueryEntities   = 64
	maxTemporalQueryPredicates = 256
	maxTemporalQuerySelections = 2
	temporalSmallCountLimit    = 5

	temporalCoverageRetained = "retained"
)

// TemporalEntityMatch controls whether every requested entity or at least one
// requested entity must occur among an observation's resolved endpoints.
type TemporalEntityMatch uint8

const (
	TemporalEntityMatchAll TemporalEntityMatch = iota + 1
	TemporalEntityMatchAny
)

// TemporalQuerySelection is the normalized, bounded selection accepted by the
// PostgreSQL historical projection. Valid-time selections are carried only so
// the adapter can preserve the read contract; SQL does not apply them.
type TemporalQuerySelection struct {
	EntityIDs     []identity.EntityID
	EntityMatch   TemporalEntityMatch
	Predicates    []observation.Predicate
	Selections    []temporal.TemporalSelection
	KnowledgeAsOf *time.Time
}

// TemporalEntityRecord states whether one requested entity exists at the
// selected recorded-time scope.
type TemporalEntityRecord struct {
	EntityID identity.EntityID
	Known    bool
}

// TemporalEvidenceRecord is immutable exact citation metadata owned by the
// PostgreSQL adapter projection.
type TemporalEvidenceRecord struct {
	EvidenceID        evidence.EvidenceID
	Role              observation.EvidenceRole
	SourceDocumentID  string
	DocumentVersionID string
	SectionID         string
	SectionTitle      string
	SectionPath       []string
	SectionOrder      int
	SectionRole       string
	StartOffset       int
	EndOffset         int
	Locator           string
	Text              string
}

// TemporalObservationRecord carries one canonical observation together with
// its cutoff-resolved semantic terms and exact evidence records.
type TemporalObservationRecord struct {
	Observation               observation.Observation
	Subject                   observation.Term
	Object                    observation.Term
	SubjectGroundingMentionID string
	ObjectGroundingMentionID  string
	Evidence                  []TemporalEvidenceRecord
}

// TemporalCoverageReason is the closed adapter-owned exclusion vocabulary.
type TemporalCoverageReason string

const (
	TemporalCoverageUnresolvedMention TemporalCoverageReason = "unresolved-mention"
	TemporalCoverageAuthorityExcluded TemporalCoverageReason = "authority-excluded"
	TemporalCoverageEntityFiltered    TemporalCoverageReason = "entity-filtered"
	TemporalCoveragePredicateFiltered TemporalCoverageReason = "predicate-filtered"
)

// TemporalCoverageRecord explains why an otherwise relevant observation did
// not become a pre-valid-time candidate.
type TemporalCoverageRecord struct {
	Reason        TemporalCoverageReason
	EntityID      identity.EntityID
	Predicate     observation.Predicate
	ObservationID observation.ObservationID
}

// TemporalQuerySnapshot is one coherent historical authority, observation,
// and evidence projection.
type TemporalQuerySnapshot struct {
	Entities     []TemporalEntityRecord
	Observations []TemporalObservationRecord
	Coverage     []TemporalCoverageRecord
}

// TemporalSnapshotCountBucket is a bounded telemetry-safe input count bucket.
type TemporalSnapshotCountBucket string

const (
	TemporalSnapshotCountZero      TemporalSnapshotCountBucket = "0"
	TemporalSnapshotCountOne       TemporalSnapshotCountBucket = "1"
	TemporalSnapshotCountTwoToFive TemporalSnapshotCountBucket = "2-5"
	TemporalSnapshotCountSixPlus   TemporalSnapshotCountBucket = "6-plus"
)

// TemporalSnapshotAttributes contains only bounded, low-cardinality inputs.
type TemporalSnapshotAttributes struct {
	HasKnowledgeCutoff   bool
	EntityCountBucket    TemporalSnapshotCountBucket
	PredicateCountBucket TemporalSnapshotCountBucket
	SelectionCountBucket TemporalSnapshotCountBucket
}

// TemporalSnapshotObserver owns snapshot-boundary instrumentation without
// coupling the adapter to a telemetry implementation.
type TemporalSnapshotObserver interface {
	StartTemporalSnapshot(
		context.Context,
		TemporalSnapshotAttributes,
	) (context.Context, func(error))
}

type temporalQueryBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// LoadTemporalQuerySnapshot reads current or historical authority and
// canonical evidence in one repeatable-read, read-only transaction.
func (database *Database) LoadTemporalQuerySnapshot(
	ctx context.Context,
	selection TemporalQuerySelection,
	observer TemporalSnapshotObserver,
) (TemporalQuerySnapshot, error) {
	if database == nil || database.pool == nil {
		return TemporalQuerySnapshot{}, errors.New(
			"load temporal query snapshot: database is closed",
		)
	}
	return loadTemporalQuerySnapshot(ctx, database.pool, selection, observer)
}

func loadTemporalQuerySnapshot(
	ctx context.Context,
	pool temporalQueryBeginner,
	selection TemporalQuerySelection,
	observer TemporalSnapshotObserver,
) (snapshot TemporalQuerySnapshot, resultErr error) {
	normalized, err := normalizeTemporalQuerySelection(ctx, selection)
	if err != nil {
		return TemporalQuerySnapshot{}, err
	}
	if pool == nil {
		return TemporalQuerySnapshot{}, errors.New(
			"load temporal query snapshot: database is closed",
		)
	}

	attributes := TemporalSnapshotAttributes{
		HasKnowledgeCutoff:   normalized.KnowledgeAsOf != nil,
		EntityCountBucket:    temporalSnapshotCount(len(normalized.EntityIDs)),
		PredicateCountBucket: temporalSnapshotCount(len(normalized.Predicates)),
		SelectionCountBucket: temporalSnapshotCount(len(normalized.Selections)),
	}
	ctx, finish := startTemporalSnapshotObservation(ctx, observer, attributes)
	defer func() {
		finish(resultErr)
	}()

	transaction, err := pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return TemporalQuerySnapshot{}, temporalSnapshotError(
			ctx,
			"start transaction",
			err,
		)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.

	cutoff := temporalQueryCutoff(normalized.KnowledgeAsOf)
	entityRecords, err := readTemporalEntityRecords(
		ctx,
		transaction,
		cutoff,
		normalized.EntityIDs,
	)
	if err != nil {
		return TemporalQuerySnapshot{}, err
	}
	qualified, coverage, err := readTemporalQualification(
		ctx,
		transaction,
		cutoff,
		normalized,
	)
	if err != nil {
		return TemporalQuerySnapshot{}, err
	}
	rawObservations, err := readTemporalObservationValues(
		ctx,
		transaction,
		cutoff,
		qualified,
	)
	if err != nil {
		return TemporalQuerySnapshot{}, err
	}
	evidenceRecords, err := readTemporalEvidenceRecords(
		ctx,
		transaction,
		cutoff,
		qualified,
	)
	if err != nil {
		return TemporalQuerySnapshot{}, err
	}
	observationRecords, err := buildTemporalObservationRecords(
		qualified,
		rawObservations,
		evidenceRecords,
	)
	if err != nil {
		return TemporalQuerySnapshot{}, temporalSnapshotError(
			ctx,
			"validate projection",
			err,
		)
	}

	snapshot = TemporalQuerySnapshot{
		Entities:     entityRecords,
		Observations: observationRecords,
		Coverage:     coverage,
	}
	if err := transaction.Commit(ctx); err != nil {
		return TemporalQuerySnapshot{}, temporalSnapshotError(
			ctx,
			"commit transaction",
			err,
		)
	}
	return snapshot, nil
}

func normalizeTemporalQuerySelection(
	ctx context.Context,
	selection TemporalQuerySelection,
) (TemporalQuerySelection, error) {
	if ctx == nil {
		return TemporalQuerySelection{}, errors.New(
			"load temporal query snapshot: context is required",
		)
	}
	if err := ctx.Err(); err != nil {
		return TemporalQuerySelection{}, temporalSnapshotError(
			ctx,
			"validate context",
			err,
		)
	}
	if len(selection.EntityIDs) == 0 ||
		len(selection.EntityIDs) > maxTemporalQueryEntities {
		return TemporalQuerySelection{}, errors.New(
			"load temporal query snapshot: bounded entity IDs are required",
		)
	}
	entityIDs := append([]identity.EntityID(nil), selection.EntityIDs...)
	for index, entityID := range entityIDs {
		value := string(entityID)
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return TemporalQuerySelection{}, errors.New(
				"load temporal query snapshot: entity IDs are not normalized",
			)
		}
		if index > 0 && entityIDs[index-1] >= entityID {
			return TemporalQuerySelection{}, errors.New(
				"load temporal query snapshot: entity IDs are not canonical",
			)
		}
	}
	if selection.EntityMatch != TemporalEntityMatchAll &&
		selection.EntityMatch != TemporalEntityMatchAny {
		return TemporalQuerySelection{}, errors.New(
			"load temporal query snapshot: entity match is invalid",
		)
	}
	if len(selection.Predicates) > maxTemporalQueryPredicates {
		return TemporalQuerySelection{}, errors.New(
			"load temporal query snapshot: predicates exceed the bounded maximum",
		)
	}
	predicates := append([]observation.Predicate(nil), selection.Predicates...)
	for index, predicate := range predicates {
		value := string(predicate)
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return TemporalQuerySelection{}, errors.New(
				"load temporal query snapshot: predicates are not normalized",
			)
		}
		if _, err := observation.NewPredicate(value); err != nil {
			return TemporalQuerySelection{}, errors.New(
				"load temporal query snapshot: predicate is invalid",
			)
		}
		if index > 0 && predicates[index-1] >= predicate {
			return TemporalQuerySelection{}, errors.New(
				"load temporal query snapshot: predicates are not canonical",
			)
		}
	}
	if len(selection.Selections) == 0 ||
		len(selection.Selections) > maxTemporalQuerySelections {
		return TemporalQuerySelection{}, errors.New(
			"load temporal query snapshot: bounded temporal selections are required",
		)
	}
	selections := append(
		[]temporal.TemporalSelection(nil),
		selection.Selections...,
	)
	labels := make(map[string]struct{}, len(selections))
	for _, selected := range selections {
		label := selected.Label()
		if strings.TrimSpace(label) == "" || strings.TrimSpace(label) != label {
			return TemporalQuerySelection{}, errors.New(
				"load temporal query snapshot: temporal selection is invalid",
			)
		}
		if _, exists := labels[label]; exists {
			return TemporalQuerySelection{}, errors.New(
				"load temporal query snapshot: temporal selections are duplicated",
			)
		}
		labels[label] = struct{}{}
		switch selected.Kind() {
		case temporal.SelectionPoint:
			point, ok := selected.Point()
			if !ok || point.IsZero() || !timepoint.IsCanonical(point) {
				return TemporalQuerySelection{}, errors.New(
					"load temporal query snapshot: temporal selection is invalid",
				)
			}
		case temporal.SelectionWindow:
			start, end, ok := selected.Window()
			if !ok ||
				start.IsZero() ||
				end.IsZero() ||
				!end.After(start) ||
				!timepoint.IsCanonical(start) ||
				!timepoint.IsCanonical(end) {
				return TemporalQuerySelection{}, errors.New(
					"load temporal query snapshot: temporal selection is invalid",
				)
			}
		default:
			return TemporalQuerySelection{}, errors.New(
				"load temporal query snapshot: temporal selection is invalid",
			)
		}
	}

	var knowledgeAsOf *time.Time
	if selection.KnowledgeAsOf != nil {
		if selection.KnowledgeAsOf.IsZero() {
			return TemporalQuerySelection{}, errors.New(
				"load temporal query snapshot: knowledge cutoff is required",
			)
		}
		value := timepoint.Normalize(*selection.KnowledgeAsOf)
		knowledgeAsOf = &value
	}
	return TemporalQuerySelection{
		EntityIDs:     entityIDs,
		EntityMatch:   selection.EntityMatch,
		Predicates:    predicates,
		Selections:    selections,
		KnowledgeAsOf: knowledgeAsOf,
	}, nil
}

func temporalQueryCutoff(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func temporalSnapshotCount(count int) TemporalSnapshotCountBucket {
	switch {
	case count == 0:
		return TemporalSnapshotCountZero
	case count == 1:
		return TemporalSnapshotCountOne
	case count <= temporalSmallCountLimit:
		return TemporalSnapshotCountTwoToFive
	default:
		return TemporalSnapshotCountSixPlus
	}
}

func startTemporalSnapshotObservation(
	ctx context.Context,
	observer TemporalSnapshotObserver,
	attributes TemporalSnapshotAttributes,
) (context.Context, func(error)) {
	if observer == nil {
		return ctx, func(error) {}
	}
	observedContext, finish := observer.StartTemporalSnapshot(ctx, attributes)
	if observedContext == nil {
		observedContext = ctx
	}
	if finish == nil {
		finish = func(error) {}
	}
	return observedContext, finish
}

type boundedTemporalSnapshotError struct {
	operation string
	cause     error
}

var errTemporalSnapshotFailure = errors.New("PostgreSQL temporal snapshot failed")

func (err boundedTemporalSnapshotError) Error() string {
	return "load temporal query snapshot: " + err.operation + " failed"
}

func (err boundedTemporalSnapshotError) Unwrap() error {
	return err.cause
}

func temporalSnapshotError(
	ctx context.Context,
	operation string,
	err error,
) error {
	cause := errTemporalSnapshotFailure
	switch {
	case ctx != nil && errors.Is(ctx.Err(), context.Canceled),
		errors.Is(err, context.Canceled):
		cause = context.Canceled
	case ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded),
		errors.Is(err, context.DeadlineExceeded):
		cause = context.DeadlineExceeded
	case errors.Is(err, ErrConflict):
		cause = ErrConflict
	case errors.Is(err, ErrNotFound):
		cause = ErrNotFound
	}
	return boundedTemporalSnapshotError{operation: operation, cause: cause}
}

func temporalEntityIDStrings(values []identity.EntityID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func temporalPredicateStrings(values []observation.Predicate) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

const temporalEntityAuthoritySQL = `
	WITH
	parameters AS (
		SELECT $1::timestamptz AS cutoff
	),
	requested_entities AS (
		SELECT unnest($2::text[]) AS id
	),
	visible_entities AS (
		SELECT entity.id
		FROM stacks_core.entities AS entity
		CROSS JOIN parameters
		WHERE parameters.cutoff IS NULL
		   OR entity.recorded_at <= parameters.cutoff
	)
	SELECT requested.id, visible.id IS NOT NULL
	FROM requested_entities AS requested
	LEFT JOIN visible_entities AS visible
	  ON visible.id = requested.id
	ORDER BY requested.id`

func readTemporalEntityRecords(
	ctx context.Context,
	transaction pgx.Tx,
	cutoff any,
	entityIDs []identity.EntityID,
) ([]TemporalEntityRecord, error) {
	rows, err := transaction.Query(
		ctx,
		temporalEntityAuthoritySQL,
		cutoff,
		temporalEntityIDStrings(entityIDs),
	)
	if err != nil {
		return nil, temporalSnapshotError(ctx, "read entity authority", err)
	}
	defer rows.Close()

	records := make([]TemporalEntityRecord, 0, len(entityIDs))
	for rows.Next() {
		var id string
		var known bool
		if err := rows.Scan(&id, &known); err != nil {
			return nil, temporalSnapshotError(ctx, "scan entity authority", err)
		}
		records = append(records, TemporalEntityRecord{
			EntityID: identity.EntityID(id),
			Known:    known,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, temporalSnapshotError(ctx, "iterate entity authority", err)
	}
	if len(records) != len(entityIDs) {
		return nil, temporalSnapshotError(
			ctx,
			"validate entity authority",
			ErrConflict,
		)
	}
	for index, record := range records {
		if record.EntityID != entityIDs[index] {
			return nil, temporalSnapshotError(
				ctx,
				"validate entity authority",
				ErrConflict,
			)
		}
	}
	return records, nil
}

const temporalQualificationSQL = `
	WITH RECURSIVE
	parameters AS (
		SELECT
			$1::timestamptz AS cutoff,
			$3::smallint AS entity_match,
			$4::text[] AS predicates
	),
	requested_entities AS (
		SELECT unnest($2::text[]) AS id
	),
	visible_entities AS (
		SELECT entity.*
		FROM stacks_core.entities AS entity
		CROSS JOIN parameters
		WHERE parameters.cutoff IS NULL
		   OR entity.recorded_at <= parameters.cutoff
	),
	visible_mentions AS (
		SELECT mention.*
		FROM stacks_core.mentions AS mention
		CROSS JOIN parameters
		WHERE parameters.cutoff IS NULL
		   OR mention.recorded_at <= parameters.cutoff
	),
	visible_resolution_proposals AS (
		SELECT proposal.*
		FROM stacks_core.resolution_proposals AS proposal
		CROSS JOIN parameters
		WHERE parameters.cutoff IS NULL
		   OR proposal.recorded_at <= parameters.cutoff
	),
	visible_resolution_decisions AS (
		SELECT decision.*
		FROM stacks_core.resolution_decisions AS decision
		CROSS JOIN parameters
		WHERE parameters.cutoff IS NULL
		   OR decision.recorded_at <= parameters.cutoff
	),
	reachable_resolution_decisions AS (
		SELECT root_resolution.*
		FROM visible_resolution_decisions AS root_resolution
		WHERE root_resolution.supersedes_id IS NULL
		UNION
		SELECT resolution_successor.*
		FROM visible_resolution_decisions AS resolution_successor
		JOIN reachable_resolution_decisions AS resolution_predecessor
		  ON resolution_successor.supersedes_id = resolution_predecessor.id
		 AND resolution_successor.proposal_id = resolution_predecessor.proposal_id
		 AND resolution_successor.recorded_at >= resolution_predecessor.recorded_at
	),
	effective_resolution_decisions AS (
		SELECT
			decision.id,
			proposal.mention_id,
			decision.entity_id
		FROM reachable_resolution_decisions AS decision
		JOIN visible_resolution_proposals AS proposal
		  ON proposal.id = decision.proposal_id
		JOIN visible_mentions AS mention
		  ON mention.id = proposal.mention_id
		JOIN visible_entities AS entity
		  ON entity.id = decision.entity_id
		WHERE decision.outcome = 'accepted'
		  AND NOT EXISTS (
			SELECT 1
			FROM reachable_resolution_decisions AS resolution_successor
			WHERE resolution_successor.supersedes_id = decision.id
			  AND resolution_successor.proposal_id = decision.proposal_id
		  )
	),
	visible_admission_targets AS (
		SELECT target.*
		FROM stacks_core.admission_targets AS target
		CROSS JOIN parameters
		WHERE parameters.cutoff IS NULL
		   OR target.recorded_at <= parameters.cutoff
	),
	visible_admission_decisions AS (
		SELECT decision.*
		FROM stacks_core.admission_decisions AS decision
		JOIN visible_admission_targets AS target
		  ON target.target_kind = decision.target_kind
		 AND target.target_id = decision.target_id
		CROSS JOIN parameters
		WHERE parameters.cutoff IS NULL
		   OR decision.recorded_at <= parameters.cutoff
	),
	reachable_admission_decisions AS (
		SELECT root_admission.*
		FROM visible_admission_decisions AS root_admission
		WHERE root_admission.supersedes_id IS NULL
		UNION
		SELECT admission_successor.*
		FROM visible_admission_decisions AS admission_successor
		JOIN reachable_admission_decisions AS admission_predecessor
		  ON admission_successor.supersedes_id = admission_predecessor.id
		 AND admission_successor.target_kind = admission_predecessor.target_kind
		 AND admission_successor.target_id = admission_predecessor.target_id
		 AND admission_successor.recorded_at >= admission_predecessor.recorded_at
	),
	effective_admissions AS (
		SELECT decision.*
		FROM reachable_admission_decisions AS decision
		WHERE NOT EXISTS (
			SELECT 1
			FROM reachable_admission_decisions AS admission_successor
			WHERE admission_successor.supersedes_id = decision.id
			  AND admission_successor.target_kind = decision.target_kind
			  AND admission_successor.target_id = decision.target_id
		)
	),
	admitted_resolution_edges AS (
		SELECT
			resolution.mention_id,
			resolution.entity_id
		FROM effective_resolution_decisions AS resolution
		JOIN effective_admissions AS mention_admission
		  ON mention_admission.target_kind = 'mention'
		 AND mention_admission.target_id = resolution.mention_id
		 AND mention_admission.outcome = 'admitted'
		JOIN effective_admissions AS decision_admission
		  ON decision_admission.target_kind = 'identity_decision'
		 AND decision_admission.target_id = resolution.id
		 AND decision_admission.outcome = 'admitted'
	),
	resolved_mentions AS (
		SELECT
			mention_id,
			min(entity_id) AS entity_id
		FROM admitted_resolution_edges
		GROUP BY mention_id
		HAVING count(DISTINCT entity_id) = 1
	),
	visible_observations AS (
		SELECT value.*
		FROM stacks_core.observations AS value
		CROSS JOIN parameters
		WHERE parameters.cutoff IS NULL
		   OR value.recorded_at <= parameters.cutoff
	),
	qualified_observations AS (
		SELECT value.id
		FROM visible_observations AS value
		JOIN effective_admissions AS observation_admission
		  ON observation_admission.target_kind = 'observation'
		 AND observation_admission.target_id = value.id
		 AND observation_admission.outcome = 'admitted'
	),
	observation_candidates AS (
		SELECT
			value.*,
			CASE
				WHEN value.subject_kind IN ('entity', 'grounded_entity')
					THEN subject_entity.id
				WHEN value.subject_kind = 'mention'
					THEN subject_resolution.entity_id
			END AS resolved_subject_entity_id,
			CASE
				WHEN value.object_kind IN ('entity', 'grounded_entity')
					THEN object_entity.id
				WHEN value.object_kind = 'mention'
					THEN object_resolution.entity_id
			END AS resolved_object_entity_id,
			(
				value.subject_kind IN ('entity', 'grounded_entity')
				AND subject_entity.id IS NULL
			) OR (
				value.object_kind IN ('entity', 'grounded_entity')
				AND object_entity.id IS NULL
			) AS direct_entity_missing,
			(
				value.subject_kind = 'mention'
				AND subject_resolution.entity_id IS NULL
			) OR (
				value.object_kind = 'mention'
				AND object_resolution.entity_id IS NULL
			) AS mention_unresolved,
			qualified.id IS NOT NULL AS observation_admitted,
			EXISTS (
				SELECT 1
				FROM stacks_core.observation_evidence AS link
				WHERE link.observation_id = value.id
			) AND NOT EXISTS (
				SELECT 1
				FROM stacks_core.observation_evidence AS link
				JOIN stacks_core.evidence_spans AS span
				  ON span.id = link.evidence_id
				JOIN stacks_core.document_versions AS version
				  ON version.id = span.document_version_id
				JOIN stacks_core.source_documents AS source
				  ON source.id = version.source_document_id
				CROSS JOIN parameters
				WHERE link.observation_id = value.id
				  AND parameters.cutoff IS NOT NULL
				  AND (
					span.recorded_at > parameters.cutoff
					OR version.recorded_at > parameters.cutoff
					OR source.created_at > parameters.cutoff
				  )
			) AS evidence_visible
		FROM visible_observations AS value
		LEFT JOIN visible_entities AS subject_entity
		  ON value.subject_kind IN ('entity', 'grounded_entity')
		 AND subject_entity.id = value.subject_entity_id
		LEFT JOIN visible_entities AS object_entity
		  ON value.object_kind IN ('entity', 'grounded_entity')
		 AND object_entity.id = value.object_entity_id
		LEFT JOIN resolved_mentions AS subject_resolution
		  ON value.subject_kind = 'mention'
		 AND subject_resolution.mention_id = value.subject_mention_id
		LEFT JOIN resolved_mentions AS object_resolution
		  ON value.object_kind = 'mention'
		 AND object_resolution.mention_id = value.object_mention_id
		LEFT JOIN qualified_observations AS qualified
		  ON qualified.id = value.id
	),
	classified AS (
		SELECT
			candidate.*,
			EXISTS (
				SELECT 1
				FROM requested_entities AS requested
				WHERE requested.id IN (
					COALESCE(
						candidate.resolved_subject_entity_id,
						CASE
							WHEN candidate.subject_kind IN ('entity', 'grounded_entity')
								THEN candidate.subject_entity_id
						END
					),
					COALESCE(
						candidate.resolved_object_entity_id,
						CASE
							WHEN candidate.object_kind IN ('entity', 'grounded_entity')
								THEN candidate.object_entity_id
						END
					)
				)
			) AS related_to_request,
			CASE
				WHEN cardinality(parameters.predicates) > 0
				 AND NOT candidate.predicate = ANY(parameters.predicates)
					THEN 'predicate-filtered'
				WHEN candidate.direct_entity_missing
					THEN 'authority-excluded'
				WHEN candidate.mention_unresolved
					THEN 'unresolved-mention'
				WHEN parameters.entity_match = 1
				 AND EXISTS (
					SELECT 1
					FROM requested_entities AS requested
					WHERE requested.id NOT IN (
						COALESCE(candidate.resolved_subject_entity_id, ''),
						COALESCE(candidate.resolved_object_entity_id, '')
					)
				 )
					THEN 'entity-filtered'
				WHEN parameters.entity_match = 2
				 AND NOT EXISTS (
					SELECT 1
					FROM requested_entities AS requested
					WHERE requested.id IN (
						COALESCE(candidate.resolved_subject_entity_id, ''),
						COALESCE(candidate.resolved_object_entity_id, '')
					)
				 )
					THEN 'entity-filtered'
				WHEN NOT candidate.observation_admitted
				  OR NOT candidate.evidence_visible
					THEN 'authority-excluded'
				ELSE 'retained'
			END AS classification
		FROM observation_candidates AS candidate
		CROSS JOIN parameters
	),
	projected AS (
		SELECT
			classified.*,
			CASE classified.subject_kind
				WHEN 'entity' THEN 'entity'
				WHEN 'grounded_entity' THEN 'entity'
				WHEN 'mention' THEN
					CASE
						WHEN classified.resolved_subject_entity_id IS NULL
							THEN 'mention'
						ELSE 'entity'
					END
				ELSE classified.subject_kind
			END AS resolved_subject_kind,
			CASE classified.object_kind
				WHEN 'entity' THEN 'entity'
				WHEN 'grounded_entity' THEN 'entity'
				WHEN 'mention' THEN
					CASE
						WHEN classified.resolved_object_entity_id IS NULL
							THEN 'mention'
						ELSE 'entity'
					END
				ELSE classified.object_kind
			END AS resolved_object_kind,
			CASE classified.subject_kind
				WHEN 'mention' THEN classified.subject_mention_id
				WHEN 'grounded_entity'
					THEN classified.subject_grounding_mention_id
			END AS resolved_subject_grounding_id,
			CASE classified.object_kind
				WHEN 'mention' THEN classified.object_mention_id
				WHEN 'grounded_entity'
					THEN classified.object_grounding_mention_id
			END AS resolved_object_grounding_id
		FROM classified
		WHERE classified.related_to_request
	)
	SELECT
		projected.id,
		projected.predicate,
		projected.resolved_subject_kind,
		COALESCE(projected.subject_text, ''),
		COALESCE(projected.resolved_subject_entity_id, ''),
		COALESCE(projected.resolved_subject_grounding_id, ''),
		projected.resolved_object_kind,
		COALESCE(projected.object_text, ''),
		COALESCE(projected.resolved_object_entity_id, ''),
		COALESCE(projected.resolved_object_grounding_id, ''),
		projected.classification,
		COALESCE(coverage_entity.id, '')
	FROM projected
	LEFT JOIN LATERAL (
		SELECT requested.id
		FROM requested_entities AS requested
		WHERE projected.classification <> 'retained'
		  AND requested.id IN (
			COALESCE(
				projected.resolved_subject_entity_id,
				CASE
					WHEN projected.subject_kind IN ('entity', 'grounded_entity')
						THEN projected.subject_entity_id
				END
			),
			COALESCE(
				projected.resolved_object_entity_id,
				CASE
					WHEN projected.object_kind IN ('entity', 'grounded_entity')
						THEN projected.object_entity_id
				END
			)
		  )
		ORDER BY requested.id
	) AS coverage_entity ON true
	ORDER BY projected.recorded_at, projected.id, coverage_entity.id`

type temporalQualifiedObservation struct {
	id                        observation.ObservationID
	predicate                 observation.Predicate
	subject                   observation.Term
	object                    observation.Term
	subjectGroundingMentionID string
	objectGroundingMentionID  string
}

func readTemporalQualification(
	ctx context.Context,
	transaction pgx.Tx,
	cutoff any,
	selection TemporalQuerySelection,
) ([]temporalQualifiedObservation, []TemporalCoverageRecord, error) {
	rows, err := transaction.Query(
		ctx,
		temporalQualificationSQL,
		cutoff,
		temporalEntityIDStrings(selection.EntityIDs),
		int16(selection.EntityMatch),
		temporalPredicateStrings(selection.Predicates),
	)
	if err != nil {
		return nil, nil, temporalSnapshotError(
			ctx,
			"read observation authority",
			err,
		)
	}
	defer rows.Close()

	qualified := make([]temporalQualifiedObservation, 0)
	coverage := make([]TemporalCoverageRecord, 0)
	classifications := make(map[observation.ObservationID]string)
	retainedObservationIDs := make(map[observation.ObservationID]struct{})
	seenCoverage := make(map[TemporalCoverageRecord]struct{})
	for rows.Next() {
		var (
			id, predicate                                        string
			subjectKind, subjectText, subjectEntityID            string
			subjectGroundingMentionID                            string
			objectKind, objectText, objectEntityID               string
			objectGroundingMentionID, classification, coverageID string
		)
		if err := rows.Scan(
			&id,
			&predicate,
			&subjectKind,
			&subjectText,
			&subjectEntityID,
			&subjectGroundingMentionID,
			&objectKind,
			&objectText,
			&objectEntityID,
			&objectGroundingMentionID,
			&classification,
			&coverageID,
		); err != nil {
			return nil, nil, temporalSnapshotError(
				ctx,
				"scan observation authority",
				err,
			)
		}
		observationID := observation.ObservationID(strings.TrimSpace(id))
		predicateValue, predicateErr := observation.NewPredicate(predicate)
		if observationID == "" || predicateErr != nil {
			return nil, nil, temporalSnapshotError(
				ctx,
				"validate observation authority",
				ErrConflict,
			)
		}
		if previous, exists := classifications[observationID]; exists &&
			previous != classification {
			return nil, nil, temporalSnapshotError(
				ctx,
				"validate observation authority",
				ErrConflict,
			)
		}
		classifications[observationID] = classification

		switch classification {
		case temporalCoverageRetained:
			if _, exists := retainedObservationIDs[observationID]; exists {
				return nil, nil, temporalSnapshotError(
					ctx,
					"validate observation authority",
					ErrConflict,
				)
			}
			retainedObservationIDs[observationID] = struct{}{}
			subject, decodeErr := decodeTemporalResolvedTerm(
				subjectKind,
				subjectText,
				subjectEntityID,
			)
			if decodeErr != nil {
				return nil, nil, temporalSnapshotError(
					ctx,
					"validate resolved observation",
					decodeErr,
				)
			}
			object, decodeErr := decodeTemporalResolvedTerm(
				objectKind,
				objectText,
				objectEntityID,
			)
			if decodeErr != nil {
				return nil, nil, temporalSnapshotError(
					ctx,
					"validate resolved observation",
					decodeErr,
				)
			}
			qualified = append(qualified, temporalQualifiedObservation{
				id:                        observationID,
				predicate:                 predicateValue,
				subject:                   subject,
				object:                    object,
				subjectGroundingMentionID: subjectGroundingMentionID,
				objectGroundingMentionID:  objectGroundingMentionID,
			})
		case string(TemporalCoverageUnresolvedMention),
			string(TemporalCoverageAuthorityExcluded),
			string(TemporalCoverageEntityFiltered),
			string(TemporalCoveragePredicateFiltered):
			record := TemporalCoverageRecord{
				Reason:        TemporalCoverageReason(classification),
				EntityID:      identity.EntityID(coverageID),
				Predicate:     predicateValue,
				ObservationID: observationID,
			}
			if !slices.Contains(selection.EntityIDs, record.EntityID) {
				return nil, nil, temporalSnapshotError(
					ctx,
					"validate observation authority",
					ErrConflict,
				)
			}
			if _, exists := seenCoverage[record]; exists {
				return nil, nil, temporalSnapshotError(
					ctx,
					"validate observation authority",
					ErrConflict,
				)
			}
			seenCoverage[record] = struct{}{}
			coverage = append(coverage, record)
		default:
			return nil, nil, temporalSnapshotError(
				ctx,
				"validate observation authority",
				ErrConflict,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, temporalSnapshotError(
			ctx,
			"iterate observation authority",
			err,
		)
	}
	return qualified, coverage, nil
}

func decodeTemporalResolvedTerm(
	kind string,
	text string,
	entityID string,
) (observation.Term, error) {
	switch kind {
	case termKindAbsent:
		if text != "" || entityID != "" {
			return observation.Term{}, ErrConflict
		}
		return observation.AbsentTerm(), nil
	case termKindText:
		if entityID != "" {
			return observation.Term{}, ErrConflict
		}
		return observation.NewTextTerm(text)
	case termKindEntity:
		if text != "" {
			return observation.Term{}, ErrConflict
		}
		return observation.NewEntityTerm(entityID, "")
	default:
		return observation.Term{}, ErrConflict
	}
}

const temporalObservationValuesSQL = `
	WITH parameters AS (
		SELECT $1::timestamptz AS cutoff
	)
	SELECT
		value.id,
		value.subject_kind,
		value.subject_text,
		value.subject_mention_id,
		value.subject_entity_id,
		value.subject_grounding_mention_id,
		value.predicate,
		value.object_kind,
		value.object_text,
		value.object_mention_id,
		value.object_entity_id,
		value.object_grounding_mention_id,
		value.temporal_kind,
		value.has_start,
		value.valid_start,
		value.has_end,
		value.valid_end,
		value.recorded_at,
		value.derivation_method,
		value.derivation_version,
		value.derivation_run_id,
		value.derivation_model,
		value.derivation_prompt_version,
		value.epistemic_status,
		value.confidence_value,
		value.confidence_scale,
		value.digest_version,
		value.digest
	FROM stacks_core.observations AS value
	CROSS JOIN parameters
	WHERE value.id = ANY($2::text[])
	  AND (
		parameters.cutoff IS NULL
		OR value.recorded_at <= parameters.cutoff
	  )
	ORDER BY value.recorded_at, value.id`

type temporalRawObservation struct {
	id                      string
	subject, object         storedTerm
	predicate               string
	validTime               storedTemporalExtent
	recordedAt              time.Time
	derivationMethod        string
	derivationVersion       string
	derivationRunID         *string
	derivationModel         *string
	derivationPromptVersion *string
	epistemicStatus         string
	confidenceValue         *float64
	confidenceScale         *string
	digestVersion           string
	digest                  []byte
}

func readTemporalObservationValues(
	ctx context.Context,
	transaction pgx.Tx,
	cutoff any,
	qualified []temporalQualifiedObservation,
) ([]temporalRawObservation, error) {
	ids := temporalQualifiedObservationIDs(qualified)
	rows, err := transaction.Query(
		ctx,
		temporalObservationValuesSQL,
		cutoff,
		ids,
	)
	if err != nil {
		return nil, temporalSnapshotError(
			ctx,
			"read canonical observations",
			err,
		)
	}
	defer rows.Close()

	expected := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		expected[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(ids))
	values := make([]temporalRawObservation, 0, len(ids))
	for rows.Next() {
		var value temporalRawObservation
		if err := rows.Scan(
			&value.id,
			&value.subject.kind,
			&value.subject.text,
			&value.subject.mentionID,
			&value.subject.entityID,
			&value.subject.groundingMentionID,
			&value.predicate,
			&value.object.kind,
			&value.object.text,
			&value.object.mentionID,
			&value.object.entityID,
			&value.object.groundingMentionID,
			&value.validTime.kind,
			&value.validTime.hasStart,
			&value.validTime.start,
			&value.validTime.hasEnd,
			&value.validTime.end,
			&value.recordedAt,
			&value.derivationMethod,
			&value.derivationVersion,
			&value.derivationRunID,
			&value.derivationModel,
			&value.derivationPromptVersion,
			&value.epistemicStatus,
			&value.confidenceValue,
			&value.confidenceScale,
			&value.digestVersion,
			&value.digest,
		); err != nil {
			return nil, temporalSnapshotError(
				ctx,
				"scan canonical observations",
				err,
			)
		}
		if _, exists := expected[value.id]; !exists {
			return nil, temporalSnapshotError(
				ctx,
				"validate canonical observations",
				ErrConflict,
			)
		}
		if _, exists := seen[value.id]; exists {
			return nil, temporalSnapshotError(
				ctx,
				"validate canonical observations",
				ErrConflict,
			)
		}
		seen[value.id] = struct{}{}
		value.digest = append([]byte(nil), value.digest...)
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, temporalSnapshotError(
			ctx,
			"iterate canonical observations",
			err,
		)
	}
	if len(values) != len(ids) {
		return nil, temporalSnapshotError(
			ctx,
			"validate canonical observations",
			ErrConflict,
		)
	}
	return values, nil
}

func temporalQualifiedObservationIDs(
	values []temporalQualifiedObservation,
) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value.id)
	}
	return result
}

const temporalEvidenceRecordsSQL = `
	WITH parameters AS (
		SELECT $1::timestamptz AS cutoff
	)
	SELECT
		link.observation_id,
		span.id,
		link.role,
		source.id,
		version.id,
		section.section_id,
		section.title,
		section.path,
		section.section_order,
		section.role,
		span.start_offset,
		span.end_offset,
		version.locator,
		span.quote
	FROM stacks_core.observation_evidence AS link
	JOIN stacks_core.observations AS value
	  ON value.id = link.observation_id
	JOIN stacks_core.evidence_spans AS span
	  ON span.id = link.evidence_id
	JOIN stacks_core.document_versions AS version
	  ON version.id = span.document_version_id
	JOIN stacks_core.source_documents AS source
	  ON source.id = version.source_document_id
	JOIN stacks_core.document_sections AS section
	  ON section.document_version_id = span.document_version_id
	 AND section.section_id = span.section_id
	CROSS JOIN parameters
	WHERE link.observation_id = ANY($2::text[])
	  AND (
		parameters.cutoff IS NULL
		OR (
			value.recorded_at <= parameters.cutoff
			AND span.recorded_at <= parameters.cutoff
			AND version.recorded_at <= parameters.cutoff
			AND source.created_at <= parameters.cutoff
		)
	  )
	ORDER BY value.recorded_at, value.id, span.id, link.role`

type temporalEvidenceProjection struct {
	observationID string
	record        TemporalEvidenceRecord
}

func readTemporalEvidenceRecords(
	ctx context.Context,
	transaction pgx.Tx,
	cutoff any,
	qualified []temporalQualifiedObservation,
) ([]temporalEvidenceProjection, error) {
	ids := temporalQualifiedObservationIDs(qualified)
	rows, err := transaction.Query(
		ctx,
		temporalEvidenceRecordsSQL,
		cutoff,
		ids,
	)
	if err != nil {
		return nil, temporalSnapshotError(ctx, "read observation evidence", err)
	}
	defer rows.Close()

	expected := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		expected[id] = struct{}{}
	}
	records := make([]temporalEvidenceProjection, 0)
	for rows.Next() {
		var projection temporalEvidenceProjection
		var evidenceID, role string
		if err := rows.Scan(
			&projection.observationID,
			&evidenceID,
			&role,
			&projection.record.SourceDocumentID,
			&projection.record.DocumentVersionID,
			&projection.record.SectionID,
			&projection.record.SectionTitle,
			&projection.record.SectionPath,
			&projection.record.SectionOrder,
			&projection.record.SectionRole,
			&projection.record.StartOffset,
			&projection.record.EndOffset,
			&projection.record.Locator,
			&projection.record.Text,
		); err != nil {
			return nil, temporalSnapshotError(
				ctx,
				"scan observation evidence",
				err,
			)
		}
		if _, exists := expected[projection.observationID]; !exists {
			return nil, temporalSnapshotError(
				ctx,
				"validate observation evidence",
				ErrConflict,
			)
		}
		projection.record.EvidenceID = evidence.EvidenceID(evidenceID)
		projection.record.Role = observation.EvidenceRole(role)
		projection.record.SectionPath = append(
			[]string(nil),
			projection.record.SectionPath...,
		)
		if err := validateTemporalEvidenceRecord(projection.record); err != nil {
			return nil, temporalSnapshotError(
				ctx,
				"validate observation evidence",
				err,
			)
		}
		records = append(records, projection)
	}
	if err := rows.Err(); err != nil {
		return nil, temporalSnapshotError(
			ctx,
			"iterate observation evidence",
			err,
		)
	}
	return records, nil
}

func validateTemporalEvidenceRecord(value TemporalEvidenceRecord) error {
	if strings.TrimSpace(string(value.EvidenceID)) == "" ||
		(value.Role != observation.EvidenceSupporting &&
			value.Role != observation.EvidenceContradicting) ||
		strings.TrimSpace(value.SourceDocumentID) == "" ||
		strings.TrimSpace(value.DocumentVersionID) == "" ||
		strings.TrimSpace(value.SectionID) == "" ||
		strings.TrimSpace(value.SectionTitle) == "" ||
		value.SectionOrder < 0 ||
		strings.TrimSpace(value.SectionRole) == "" ||
		value.StartOffset < 0 ||
		value.EndOffset <= value.StartOffset {
		return ErrConflict
	}
	for _, title := range value.SectionPath {
		if strings.TrimSpace(title) == "" {
			return ErrConflict
		}
	}
	return nil
}

func buildTemporalObservationRecords(
	qualified []temporalQualifiedObservation,
	rawValues []temporalRawObservation,
	evidenceValues []temporalEvidenceProjection,
) ([]TemporalObservationRecord, error) {
	qualifiedByID := make(
		map[observation.ObservationID]temporalQualifiedObservation,
		len(qualified),
	)
	for _, value := range qualified {
		qualifiedByID[value.id] = value
	}
	evidenceByObservation := make(
		map[string][]TemporalEvidenceRecord,
		len(rawValues),
	)
	for _, value := range evidenceValues {
		evidenceByObservation[value.observationID] = append(
			evidenceByObservation[value.observationID],
			value.record,
		)
	}

	records := make([]TemporalObservationRecord, 0, len(rawValues))
	for _, raw := range rawValues {
		qualifiedValue, exists := qualifiedByID[observation.ObservationID(raw.id)]
		if !exists || qualifiedValue.predicate != observation.Predicate(raw.predicate) {
			return nil, ErrConflict
		}
		evidenceRecords := evidenceByObservation[raw.id]
		if len(evidenceRecords) == 0 {
			return nil, ErrConflict
		}
		links := make([]observation.EvidenceLink, len(evidenceRecords))
		for index, record := range evidenceRecords {
			links[index] = observation.EvidenceLink{
				EvidenceID: record.EvidenceID,
				Role:       record.Role,
			}
		}
		value, err := decodeTemporalObservation(raw, links)
		if err != nil {
			return nil, err
		}
		records = append(records, TemporalObservationRecord{
			Observation:               value,
			Subject:                   qualifiedValue.subject,
			Object:                    qualifiedValue.object,
			SubjectGroundingMentionID: qualifiedValue.subjectGroundingMentionID,
			ObjectGroundingMentionID:  qualifiedValue.objectGroundingMentionID,
			Evidence:                  cloneTemporalEvidenceRecords(evidenceRecords),
		})
	}
	return records, nil
}

func decodeTemporalObservation(
	raw temporalRawObservation,
	links []observation.EvidenceLink,
) (observation.Observation, error) {
	recordedAt, err := canonicalStoredTime(raw.recordedAt)
	if err != nil {
		return observation.Observation{}, err
	}
	subject, err := decodeObservationTerm(raw.subject)
	if err != nil {
		return observation.Observation{}, err
	}
	object, err := decodeObservationTerm(raw.object)
	if err != nil {
		return observation.Observation{}, err
	}
	validTime, err := decodeTemporalExtent(raw.validTime)
	if err != nil {
		return observation.Observation{}, err
	}
	predicate, err := observation.NewPredicate(raw.predicate)
	if err != nil {
		return observation.Observation{}, err
	}
	derivation := observation.Derivation{
		Method:  raw.derivationMethod,
		Version: raw.derivationVersion,
	}
	if raw.derivationRunID != nil {
		derivation.RunID = *raw.derivationRunID
	}
	if raw.derivationModel != nil {
		derivation.Model = *raw.derivationModel
	}
	if raw.derivationPromptVersion != nil {
		derivation.PromptVersion = *raw.derivationPromptVersion
	}

	var confidence *observation.Confidence
	switch {
	case raw.confidenceValue == nil && raw.confidenceScale == nil:
	case raw.confidenceValue == nil ||
		raw.confidenceScale == nil ||
		*raw.confidenceScale != string(observation.ConfidenceUnitInterval):
		return observation.Observation{}, ErrConflict
	default:
		parsed, parseErr := observation.NewUnitIntervalConfidence(
			*raw.confidenceValue,
		)
		if parseErr != nil {
			return observation.Observation{}, parseErr
		}
		confidence = &parsed
	}
	value, err := observation.NewObservation(observation.ObservationInput{
		ID: observation.ObservationID(raw.id),
		Statement: observation.Statement{
			Subject:   subject,
			Predicate: predicate,
			Object:    object,
		},
		ValidTime:  validTime,
		RecordedAt: recordedAt,
		Evidence:   links,
		Derivation: derivation,
		Status:     observation.EpistemicStatus(raw.epistemicStatus),
		Confidence: confidence,
	})
	if err != nil {
		return observation.Observation{}, err
	}
	if value.DigestVersion() != raw.digestVersion ||
		!sameDigestBytes(value.Digest(), raw.digest) {
		return observation.Observation{}, ErrConflict
	}
	return value, nil
}

func cloneTemporalEvidenceRecords(
	values []TemporalEvidenceRecord,
) []TemporalEvidenceRecord {
	result := make([]TemporalEvidenceRecord, len(values))
	for index, value := range values {
		value.SectionPath = append([]string(nil), value.SectionPath...)
		result[index] = value
	}
	return result
}
