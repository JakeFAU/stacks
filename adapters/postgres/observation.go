package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/timepoint"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	termKindAbsent         = "absent"
	termKindText           = "text"
	termKindMention        = "mention"
	termKindEntity         = "entity"
	termKindGroundedEntity = "grounded_entity"

	temporalKindUnknown  = "unknown"
	temporalKindInstant  = "instant"
	temporalKindInterval = "interval"
	temporalKindWindow   = "window"
)

type storedTerm struct {
	kind               string
	text               *string
	mentionID          *string
	entityID           *string
	groundingMentionID *string
}

type storedTemporalExtent struct {
	kind     string
	hasStart bool
	start    *time.Time
	hasEnd   bool
	end      *time.Time
}

// PutObservation stores one complete immutable canonical observation. It
// returns false for an exact read-only retry.
func (transaction *Transaction) PutObservation(
	ctx context.Context,
	value observation.Observation,
) (bool, error) {
	if err := contextRequired(ctx, "put observation"); err != nil {
		return false, err
	}
	if err := validateCanonicalObservation(value); err != nil {
		return false, fmt.Errorf("put observation: %w", err)
	}
	if transaction == nil || transaction.transaction == nil {
		return false, fmt.Errorf("put observation: transaction is closed")
	}

	subject, err := encodeObservationTerm(value.Statement().Subject)
	if err != nil {
		return false, fmt.Errorf("put observation subject: %w", err)
	}
	object, err := encodeObservationTerm(value.Statement().Object)
	if err != nil {
		return false, fmt.Errorf("put observation object: %w", err)
	}
	validTime, err := encodeTemporalExtent(value.ValidTime())
	if err != nil {
		return false, fmt.Errorf("put observation valid time: %w", err)
	}
	derivation := value.Derivation()
	var derivationRunID, derivationModel, derivationPromptVersion any
	if derivation.RunID != "" {
		derivationRunID = derivation.RunID
	}
	if derivation.Model != "" {
		derivationModel = derivation.Model
		derivationPromptVersion = derivation.PromptVersion
	}
	var confidenceValue, confidenceScale any
	if confidence, ok := value.Confidence(); ok {
		confidenceValue = confidence.Value()
		confidenceScale = confidence.Scale()
	}
	digest := value.Digest()
	var insertedID string
	insertErr := transaction.transaction.QueryRow(ctx, `
		INSERT INTO stacks_core.observations (
			id,
			subject_kind, subject_text, subject_mention_id,
			subject_entity_id, subject_grounding_mention_id,
			predicate,
			object_kind, object_text, object_mention_id,
			object_entity_id, object_grounding_mention_id,
			temporal_kind, has_start, valid_start, has_end, valid_end,
			recorded_at,
			derivation_method, derivation_version, derivation_run_id,
			derivation_model, derivation_prompt_version,
			epistemic_status, confidence_value, confidence_scale,
			digest_version, digest
		)
		VALUES (
			$1,
			$2, $3, $4, $5, $6,
			$7,
			$8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17,
			$18,
			$19, $20, $21, $22, $23,
			$24, $25, $26,
			$27, $28
		)
		ON CONFLICT (id) DO NOTHING
		RETURNING id`,
		value.ID(),
		subject.kind,
		subject.text,
		subject.mentionID,
		subject.entityID,
		subject.groundingMentionID,
		value.Statement().Predicate,
		object.kind,
		object.text,
		object.mentionID,
		object.entityID,
		object.groundingMentionID,
		validTime.kind,
		validTime.hasStart,
		validTime.start,
		validTime.hasEnd,
		validTime.end,
		value.RecordedAt(),
		derivation.Method,
		derivation.Version,
		derivationRunID,
		derivationModel,
		derivationPromptVersion,
		value.Status(),
		confidenceValue,
		confidenceScale,
		value.DigestVersion(),
		digest[:],
	).Scan(&insertedID)
	switch {
	case insertErr == nil:
		if err := insertObservationEvidence(
			ctx,
			transaction.transaction,
			value.ID(),
			value.EvidenceLinks(),
		); err != nil {
			return false, wrapObservationError(
				ctx,
				"insert observation evidence",
				conflictError(err),
			)
		}
		return true, nil
	case errors.Is(insertErr, pgx.ErrNoRows):
		stored, loadErr := loadObservationValue(
			ctx,
			transaction.transaction,
			value.ID(),
		)
		if loadErr != nil {
			return false, wrapObservationError(
				ctx,
				"load existing observation",
				loadErr,
			)
		}
		if !sameCanonicalObservation(stored, value) {
			return false, fmt.Errorf(
				"put observation: immutable identity: %w",
				ErrConflict,
			)
		}
		return false, nil
	default:
		return false, wrapObservationError(
			ctx,
			"insert observation",
			conflictError(insertErr),
		)
	}
}

type observationEvidenceExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func insertObservationEvidence(
	ctx context.Context,
	executor observationEvidenceExecutor,
	observationID observation.ObservationID,
	links []observation.EvidenceLink,
) error {
	evidenceIDs := make([]string, len(links))
	roles := make([]string, len(links))
	for index, link := range links {
		evidenceIDs[index] = string(link.EvidenceID)
		roles[index] = string(link.Role)
	}
	_, err := executor.Exec(ctx, `
		INSERT INTO stacks_core.observation_evidence (
			observation_id, evidence_id, role
		)
		SELECT $1, evidence_id, role
		FROM unnest($2::text[], $3::text[]) AS link(evidence_id, role)`,
		observationID,
		evidenceIDs,
		roles,
	)
	return err
}

// LoadObservation reconstructs and digest-verifies one complete canonical
// observation.
func (database *Database) LoadObservation(
	ctx context.Context,
	id observation.ObservationID,
) (observation.Observation, error) {
	if err := contextRequired(ctx, "load observation"); err != nil {
		return observation.Observation{}, err
	}
	if database == nil || database.pool == nil {
		return observation.Observation{}, fmt.Errorf("load observation: database is closed")
	}
	if strings.TrimSpace(string(id)) == "" {
		return observation.Observation{}, fmt.Errorf("load observation: observation ID is required")
	}
	value, err := loadObservationValue(ctx, database.pool, id)
	if err != nil {
		return observation.Observation{}, wrapObservationReadError(
			ctx,
			"load observation",
			err,
		)
	}
	return value, nil
}

func loadObservationValue(
	ctx context.Context,
	reader documentReader,
	id observation.ObservationID,
) (observation.Observation, error) {
	var (
		storedID                                                  string
		subject, object                                           storedTerm
		predicate                                                 string
		validTime                                                 storedTemporalExtent
		recordedAt                                                time.Time
		derivationMethod, derivationVersion                       string
		derivationRunID, derivationModel, derivationPromptVersion *string
		epistemicStatus                                           string
		confidenceValue                                           *float64
		confidenceScale                                           *string
		digestVersion                                             string
		storedDigest                                              []byte
	)
	if err := reader.QueryRow(ctx, `
		SELECT
			id,
			subject_kind, subject_text, subject_mention_id,
			subject_entity_id, subject_grounding_mention_id,
			predicate,
			object_kind, object_text, object_mention_id,
			object_entity_id, object_grounding_mention_id,
			temporal_kind, has_start, valid_start, has_end, valid_end,
			recorded_at,
			derivation_method, derivation_version, derivation_run_id,
			derivation_model, derivation_prompt_version,
			epistemic_status, confidence_value, confidence_scale,
			digest_version, digest
		FROM stacks_core.observations
		WHERE id = $1`,
		id,
	).Scan(
		&storedID,
		&subject.kind,
		&subject.text,
		&subject.mentionID,
		&subject.entityID,
		&subject.groundingMentionID,
		&predicate,
		&object.kind,
		&object.text,
		&object.mentionID,
		&object.entityID,
		&object.groundingMentionID,
		&validTime.kind,
		&validTime.hasStart,
		&validTime.start,
		&validTime.hasEnd,
		&validTime.end,
		&recordedAt,
		&derivationMethod,
		&derivationVersion,
		&derivationRunID,
		&derivationModel,
		&derivationPromptVersion,
		&epistemicStatus,
		&confidenceValue,
		&confidenceScale,
		&digestVersion,
		&storedDigest,
	); err != nil {
		return observation.Observation{}, err
	}
	recordedAt, err := canonicalStoredTime(recordedAt)
	if err != nil {
		return observation.Observation{}, fmt.Errorf(
			"stored observation recorded time: %w",
			err,
		)
	}
	subjectTerm, err := decodeObservationTerm(subject)
	if err != nil {
		return observation.Observation{}, fmt.Errorf(
			"stored observation subject: %w",
			err,
		)
	}
	objectTerm, err := decodeObservationTerm(object)
	if err != nil {
		return observation.Observation{}, fmt.Errorf(
			"stored observation object: %w",
			err,
		)
	}
	extent, err := decodeTemporalExtent(validTime)
	if err != nil {
		return observation.Observation{}, fmt.Errorf(
			"stored observation valid time: %w",
			err,
		)
	}
	predicateValue, err := observation.NewPredicate(predicate)
	if err != nil {
		return observation.Observation{}, fmt.Errorf(
			"stored observation predicate: %w",
			err,
		)
	}
	links, err := loadObservationEvidenceLinks(ctx, reader, id)
	if err != nil {
		return observation.Observation{}, err
	}
	derivation := observation.Derivation{
		Method:  derivationMethod,
		Version: derivationVersion,
	}
	if derivationRunID != nil {
		derivation.RunID = *derivationRunID
	}
	if derivationModel != nil {
		derivation.Model = *derivationModel
	}
	if derivationPromptVersion != nil {
		derivation.PromptVersion = *derivationPromptVersion
	}
	var confidence *observation.Confidence
	switch {
	case confidenceValue == nil && confidenceScale == nil:
	case confidenceValue == nil || confidenceScale == nil:
		return observation.Observation{}, fmt.Errorf(
			"stored observation confidence pair is incomplete: %w",
			ErrConflict,
		)
	case *confidenceScale != string(observation.ConfidenceUnitInterval):
		return observation.Observation{}, fmt.Errorf(
			"stored observation confidence scale is unsupported: %w",
			ErrConflict,
		)
	default:
		parsedConfidence, confidenceErr := observation.NewUnitIntervalConfidence(
			*confidenceValue,
		)
		if confidenceErr != nil {
			return observation.Observation{}, fmt.Errorf(
				"stored observation confidence: %w",
				confidenceErr,
			)
		}
		confidence = &parsedConfidence
	}
	value, err := observation.NewObservation(observation.ObservationInput{
		ID: observation.ObservationID(storedID),
		Statement: observation.Statement{
			Subject:   subjectTerm,
			Predicate: predicateValue,
			Object:    objectTerm,
		},
		ValidTime:  extent,
		RecordedAt: recordedAt,
		Evidence:   links,
		Derivation: derivation,
		Status:     observation.EpistemicStatus(epistemicStatus),
		Confidence: confidence,
	})
	if err != nil {
		return observation.Observation{}, fmt.Errorf(
			"validate stored observation: %w",
			err,
		)
	}
	if value.ID() != id ||
		value.DigestVersion() != digestVersion ||
		!sameDigestBytes(value.Digest(), storedDigest) {
		return observation.Observation{}, fmt.Errorf(
			"stored observation digest: %w",
			ErrConflict,
		)
	}
	return value, nil
}

func loadObservationEvidenceLinks(
	ctx context.Context,
	reader documentReader,
	id observation.ObservationID,
) ([]observation.EvidenceLink, error) {
	rows, err := reader.Query(ctx, `
		SELECT evidence_id, role
		FROM stacks_core.observation_evidence
		WHERE observation_id = $1
		ORDER BY evidence_id, role`,
		id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var links []observation.EvidenceLink
	for rows.Next() {
		var evidenceID, role string
		if err := rows.Scan(&evidenceID, &role); err != nil {
			return nil, err
		}
		switch observation.EvidenceRole(role) {
		case observation.EvidenceSupporting, observation.EvidenceContradicting:
		default:
			return nil, fmt.Errorf(
				"stored observation evidence role is invalid: %w",
				ErrConflict,
			)
		}
		links = append(links, observation.EvidenceLink{
			EvidenceID: evidence.EvidenceID(evidenceID),
			Role:       observation.EvidenceRole(role),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, fmt.Errorf("stored observation is uncited: %w", ErrConflict)
	}
	return links, nil
}

func validateCanonicalObservation(value observation.Observation) error {
	if strings.TrimSpace(string(value.ID())) == "" ||
		value.DigestVersion() == "" ||
		value.Digest() == (evidence.ContentDigest{}) ||
		strings.TrimSpace(string(value.Statement().Predicate)) == "" ||
		len(value.EvidenceLinks()) == 0 ||
		!timepoint.IsCanonical(value.RecordedAt()) {
		return fmt.Errorf("complete canonical observation is required")
	}
	derivation := value.Derivation()
	if strings.TrimSpace(derivation.Method) == "" ||
		strings.TrimSpace(derivation.Version) == "" {
		return fmt.Errorf("versioned canonical observation derivation is required")
	}
	if _, err := encodeObservationTerm(value.Statement().Subject); err != nil {
		return fmt.Errorf("observation subject: %w", err)
	}
	if _, err := encodeObservationTerm(value.Statement().Object); err != nil {
		return fmt.Errorf("observation object: %w", err)
	}
	if _, err := encodeTemporalExtent(value.ValidTime()); err != nil {
		return fmt.Errorf("observation valid time: %w", err)
	}
	for _, link := range value.EvidenceLinks() {
		if strings.TrimSpace(string(link.EvidenceID)) == "" {
			return fmt.Errorf("observation evidence ID is required")
		}
		switch link.Role {
		case observation.EvidenceSupporting, observation.EvidenceContradicting:
		default:
			return fmt.Errorf("observation evidence role is invalid")
		}
	}
	if confidence, ok := value.Confidence(); ok &&
		confidence.Scale() != observation.ConfidenceUnitInterval {
		return fmt.Errorf("observation confidence scale is not canonical")
	}
	return nil
}

func encodeObservationTerm(term observation.Term) (storedTerm, error) {
	switch term.Kind() {
	case observation.TermAbsent:
		if text, ok := term.Text(); ok || text != "" {
			return storedTerm{}, fmt.Errorf("absent term carries text")
		}
		if mentionID, ok := term.MentionID(); ok || mentionID != "" {
			return storedTerm{}, fmt.Errorf("absent term carries a mention")
		}
		if entityID, groundingID, ok := term.Entity(); ok || entityID != "" || groundingID != "" {
			return storedTerm{}, fmt.Errorf("absent term carries an entity")
		}
		return storedTerm{kind: termKindAbsent}, nil
	case observation.TermText:
		text, ok := term.Text()
		if !ok || strings.TrimSpace(text) == "" {
			return storedTerm{}, fmt.Errorf("text term is invalid")
		}
		return storedTerm{kind: termKindText, text: &text}, nil
	case observation.TermMention:
		mentionID, ok := term.MentionID()
		if !ok || strings.TrimSpace(mentionID) == "" {
			return storedTerm{}, fmt.Errorf("mention term is invalid")
		}
		return storedTerm{kind: termKindMention, mentionID: &mentionID}, nil
	case observation.TermEntity:
		entityID, groundingID, ok := term.Entity()
		if !ok || strings.TrimSpace(entityID) == "" {
			return storedTerm{}, fmt.Errorf("entity term is invalid")
		}
		result := storedTerm{kind: termKindEntity, entityID: &entityID}
		if groundingID != "" {
			if strings.TrimSpace(groundingID) == "" {
				return storedTerm{}, fmt.Errorf("grounding mention ID is invalid")
			}
			result.kind = termKindGroundedEntity
			result.groundingMentionID = &groundingID
		}
		return result, nil
	default:
		return storedTerm{}, fmt.Errorf("term kind is invalid")
	}
}

func decodeObservationTerm(stored storedTerm) (observation.Term, error) {
	switch stored.kind {
	case termKindAbsent:
		if stored.text != nil ||
			stored.mentionID != nil ||
			stored.entityID != nil ||
			stored.groundingMentionID != nil {
			return observation.Term{}, fmt.Errorf("absent term has values")
		}
		return observation.AbsentTerm(), nil
	case termKindText:
		if stored.text == nil ||
			stored.mentionID != nil ||
			stored.entityID != nil ||
			stored.groundingMentionID != nil {
			return observation.Term{}, fmt.Errorf("text term shape is invalid")
		}
		return observation.NewTextTerm(*stored.text)
	case termKindMention:
		if stored.text != nil ||
			stored.mentionID == nil ||
			stored.entityID != nil ||
			stored.groundingMentionID != nil {
			return observation.Term{}, fmt.Errorf("mention term shape is invalid")
		}
		return observation.NewMentionTerm(*stored.mentionID)
	case termKindEntity:
		if stored.text != nil ||
			stored.mentionID != nil ||
			stored.entityID == nil ||
			stored.groundingMentionID != nil {
			return observation.Term{}, fmt.Errorf("entity term shape is invalid")
		}
		return observation.NewEntityTerm(*stored.entityID, "")
	case termKindGroundedEntity:
		if stored.text != nil ||
			stored.mentionID != nil ||
			stored.entityID == nil ||
			stored.groundingMentionID == nil {
			return observation.Term{}, fmt.Errorf("grounded entity term shape is invalid")
		}
		return observation.NewEntityTerm(*stored.entityID, *stored.groundingMentionID)
	default:
		return observation.Term{}, fmt.Errorf("term tag is invalid")
	}
}

func encodeTemporalExtent(
	extent observation.TemporalExtent,
) (storedTemporalExtent, error) {
	switch extent.Kind() {
	case observation.TemporalUnknown:
		if _, ok := extent.Instant(); ok {
			return storedTemporalExtent{}, fmt.Errorf("unknown time carries an instant")
		}
		start, hasStart, end, hasEnd := extent.Bounds()
		if hasStart || hasEnd || !start.IsZero() || !end.IsZero() {
			return storedTemporalExtent{}, fmt.Errorf("unknown time carries bounds")
		}
		return storedTemporalExtent{kind: temporalKindUnknown}, nil
	case observation.TemporalInstant:
		instant, ok := extent.Instant()
		if !ok || !timepoint.IsCanonical(instant) {
			return storedTemporalExtent{}, fmt.Errorf(
				"instant must use canonical UTC microsecond precision",
			)
		}
		return storedTemporalExtent{
			kind:     temporalKindInstant,
			hasStart: true,
			start:    &instant,
		}, nil
	case observation.TemporalInterval, observation.TemporalWindow:
		start, hasStart, end, hasEnd := extent.Bounds()
		if !hasStart && !hasEnd {
			return storedTemporalExtent{}, fmt.Errorf("temporal bounds are required")
		}
		if hasStart && !timepoint.IsCanonical(start) {
			return storedTemporalExtent{}, fmt.Errorf(
				"temporal start must use canonical UTC microsecond precision",
			)
		}
		if hasEnd && !timepoint.IsCanonical(end) {
			return storedTemporalExtent{}, fmt.Errorf(
				"temporal end must use canonical UTC microsecond precision",
			)
		}
		if hasStart && hasEnd && !end.After(start) {
			return storedTemporalExtent{}, fmt.Errorf("temporal end must be after start")
		}
		if extent.Kind() == observation.TemporalWindow && (!hasStart || !hasEnd) {
			return storedTemporalExtent{}, fmt.Errorf("uncertainty window requires both bounds")
		}
		result := storedTemporalExtent{
			kind:     temporalKindInterval,
			hasStart: hasStart,
			hasEnd:   hasEnd,
		}
		if extent.Kind() == observation.TemporalWindow {
			result.kind = temporalKindWindow
		}
		if hasStart {
			result.start = &start
		}
		if hasEnd {
			result.end = &end
		}
		return result, nil
	default:
		return storedTemporalExtent{}, fmt.Errorf("temporal kind is invalid")
	}
}

func decodeTemporalExtent(
	stored storedTemporalExtent,
) (observation.TemporalExtent, error) {
	if stored.hasStart != (stored.start != nil) ||
		stored.hasEnd != (stored.end != nil) {
		return observation.TemporalExtent{}, fmt.Errorf("bound presence is inconsistent")
	}
	var start, end time.Time
	if stored.start != nil {
		canonical, err := canonicalStoredTime(*stored.start)
		if err != nil {
			return observation.TemporalExtent{}, fmt.Errorf("stored temporal start: %w", err)
		}
		start = canonical
	}
	if stored.end != nil {
		canonical, err := canonicalStoredTime(*stored.end)
		if err != nil {
			return observation.TemporalExtent{}, fmt.Errorf("stored temporal end: %w", err)
		}
		end = canonical
	}
	switch stored.kind {
	case temporalKindUnknown:
		if stored.hasStart || stored.hasEnd {
			return observation.TemporalExtent{}, fmt.Errorf("unknown time has bounds")
		}
		return observation.UnknownTime(), nil
	case temporalKindInstant:
		if !stored.hasStart || stored.hasEnd {
			return observation.TemporalExtent{}, fmt.Errorf("instant shape is invalid")
		}
		return observation.AtTime(start)
	case temporalKindInterval:
		switch {
		case stored.hasStart && stored.hasEnd:
			return observation.During(start, end)
		case stored.hasStart:
			return observation.Since(start)
		case stored.hasEnd:
			return observation.Until(end)
		default:
			return observation.TemporalExtent{}, fmt.Errorf("interval has no bounds")
		}
	case temporalKindWindow:
		if !stored.hasStart || !stored.hasEnd {
			return observation.TemporalExtent{}, fmt.Errorf("uncertainty window shape is invalid")
		}
		return observation.Within(start, end)
	default:
		return observation.TemporalExtent{}, fmt.Errorf("temporal tag is invalid")
	}
}

func sameCanonicalObservation(
	left observation.Observation,
	right observation.Observation,
) bool {
	leftConfidence, leftHasConfidence := left.Confidence()
	rightConfidence, rightHasConfidence := right.Confidence()
	return left.ID() == right.ID() &&
		left.Statement() == right.Statement() &&
		reflect.DeepEqual(left.ValidTime(), right.ValidTime()) &&
		left.RecordedAt() == right.RecordedAt() &&
		reflect.DeepEqual(left.EvidenceLinks(), right.EvidenceLinks()) &&
		left.Derivation() == right.Derivation() &&
		left.Status() == right.Status() &&
		leftHasConfidence == rightHasConfidence &&
		(!leftHasConfidence || leftConfidence == rightConfidence) &&
		left.DigestVersion() == right.DigestVersion() &&
		left.Digest() == right.Digest()
}

func wrapObservationError(ctx context.Context, operation string, err error) error {
	if ctx != nil {
		if contextError := ctx.Err(); contextError != nil {
			return fmt.Errorf("%s: %w", operation, contextError)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func wrapObservationReadError(
	ctx context.Context,
	operation string,
	err error,
) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, ErrNotFound)
	}
	return wrapObservationError(ctx, operation, err)
}
