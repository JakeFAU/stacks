package storage

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
)

var (
	ErrObservationNotRepresentable = errors.New("observation is not representable by legacy PostgreSQL")
	ErrObservationCompatibility    = errors.New("legacy observation is incompatible")
	ErrObservationConflict         = errors.New("observation conflicts with stored state")
)

const legacyPostgresTimestampPrecision = time.Microsecond

const (
	reasonObservationOriginMismatch        = "observation_origin_mismatch"
	reasonObservationDigestMismatch        = "observation_digest_mismatch"
	reasonConfidenceScaleNotRepresentable  = "confidence_scale_not_representable"
	reasonRecordedAtNotRepresentable       = "recorded_at_not_representable"
	reasonCompletionOwnerMismatch          = "completion_owner_mismatch"
	reasonCompletionWriteSetMismatch       = "completion_write_set_mismatch"
	reasonTermNotRepresentable             = "term_not_representable"
	reasonValidTimeNotRepresentable        = "valid_time_not_representable"
	reasonLegacyUncitedNotRepresentable    = "legacy_uncited_not_representable"
	reasonLegacyDerivationNotRepresentable = "legacy_derivation_not_representable"
	reasonEvidenceOwnershipMismatch        = "evidence_role_ownership_mismatch"
	reasonOwningRunRequired                = "owning_run_required"
	reasonOwningRunProvenanceMismatch      = "owning_run_provenance_mismatch"
	reasonRecordedAtOwnerMismatch          = "recorded_at_owner_mismatch"
	reasonLegacyUUIDNotRepresentable       = "legacy_uuid_not_representable"
	reasonInvalidLegacyShape               = "invalid_legacy_shape"
)

type observationBoundaryError struct {
	kind          error
	reason        string
	observationID string
	runID         string
}

func (err *observationBoundaryError) Error() string {
	switch {
	case err.observationID != "":
		return fmt.Sprintf("observation boundary %q: %s", err.observationID, err.reason)
	case err.runID != "":
		return fmt.Sprintf("observation boundary run %q: %s", err.runID, err.reason)
	default:
		return fmt.Sprintf("observation boundary: %s", err.reason)
	}
}

func (err *observationBoundaryError) Unwrap() error { return err.kind }

func newObservationBoundaryError(kind error, reason, observationID string) error {
	return &observationBoundaryError{kind: kind, reason: reason, observationID: observationID}
}

func newCompletionBoundaryError(kind error, reason, runID string) error {
	return &observationBoundaryError{kind: kind, reason: reason, runID: runID}
}

func legacyTimestampRepresentable(value time.Time) bool {
	return !value.IsZero() && value.Equal(value.Truncate(legacyPostgresTimestampPrecision))
}

type legacyObservationCompatibility struct {
	observationEvidenceOrigin []evidence.EvidenceID
	storedDigest              [sha256.Size]byte
}

type owningExtractionRun struct {
	ID            string
	ModelID       string
	PromptVersion string
	RecordedAt    time.Time
}

type legacyObservationRow struct {
	ID, ExtractionRunID                    string
	SubjectEntityID, ObjectEntityID        string
	SubjectMentionID, ObjectMentionID      string
	Predicate, Derivation, EpistemicStatus string
	ValidStart, ValidEnd                   *time.Time
	RecordedAt                             time.Time
	Confidence                             *float64
	Digest                                 [sha256.Size]byte
}

type legacySignalState struct {
	Input    SignalInput
	Evidence []SignalEvidenceInput
	Digest   [sha256.Size]byte
}

type decodedLegacyObservation struct {
	Observation   observation.Observation
	Signal        *legacySignalState
	Compatibility legacyObservationCompatibility
}

type legacyObservationWrite struct {
	Row    legacyObservationRow
	Origin []evidence.EvidenceID
	Signal *legacySignalState
}

func decodeLegacyObservation(
	row legacyObservationRow,
	origin []evidence.EvidenceID,
	signal *legacySignalState,
	run *owningExtractionRun,
) (decodedLegacyObservation, error) {
	origin, err := normalizeLegacyOrigin(origin)
	if err != nil {
		return decodedLegacyObservation{}, newObservationBoundaryError(ErrObservationCompatibility, reasonInvalidLegacyShape, row.ID)
	}
	subject, err := decodeLegacyTerm(row.SubjectEntityID, row.SubjectMentionID)
	if err != nil {
		return decodedLegacyObservation{}, err
	}
	object, err := decodeLegacyTerm(row.ObjectEntityID, row.ObjectMentionID)
	if err != nil {
		return decodedLegacyObservation{}, err
	}
	validTime, err := decodeLegacyValidTime(row.ValidStart, row.ValidEnd)
	if err != nil {
		return decodedLegacyObservation{}, newObservationBoundaryError(ErrObservationCompatibility, reasonInvalidLegacyShape, row.ID)
	}
	confidence, err := decodeLegacyConfidence(row.Confidence)
	if err != nil {
		return decodedLegacyObservation{}, newObservationBoundaryError(ErrObservationCompatibility, reasonInvalidLegacyShape, row.ID)
	}
	evidenceLinks, err := canonicalEvidenceLinks(origin, signal)
	if err != nil {
		return decodedLegacyObservation{}, newObservationBoundaryError(ErrObservationCompatibility, reasonInvalidLegacyShape, row.ID)
	}

	derivation := observation.Derivation{Method: row.Derivation}
	legacyUncited := len(evidenceLinks) == 0
	if row.ExtractionRunID == "" {
		derivation.LegacyUnversioned = true
	} else {
		if run == nil || run.ID != row.ExtractionRunID {
			return decodedLegacyObservation{}, newObservationBoundaryError(ErrObservationCompatibility, reasonCompletionOwnerMismatch, row.ID)
		}
		if (run.ModelID == "") != (run.PromptVersion == "") {
			return decodedLegacyObservation{}, newCompletionBoundaryError(ErrObservationCompatibility, reasonOwningRunProvenanceMismatch, run.ID)
		}
		derivation.Version = run.PromptVersion
		derivation.RunID = run.ID
		derivation.Model = run.ModelID
		derivation.PromptVersion = run.PromptVersion
	}

	value, err := observation.NewObservation(observation.ObservationInput{
		ID: observation.ObservationID(row.ID),
		Statement: observation.Statement{
			Subject: subject, Predicate: observation.Predicate(row.Predicate), Object: object,
		},
		ValidTime: validTime, RecordedAt: row.RecordedAt, Evidence: evidenceLinks,
		Derivation: derivation, Status: observation.EpistemicStatus(row.EpistemicStatus),
		Confidence: confidence, LegacyUncited: legacyUncited,
	})
	if err != nil {
		return decodedLegacyObservation{}, newObservationBoundaryError(ErrObservationCompatibility, reasonInvalidLegacyShape, row.ID)
	}
	return decodedLegacyObservation{
		Observation: value,
		Signal:      cloneLegacySignalState(signal),
		Compatibility: legacyObservationCompatibility{
			observationEvidenceOrigin: cloneEvidenceIDs(origin),
			storedDigest:              row.Digest,
		},
	}, nil
}

func encodeLegacyObservation(
	value observation.Observation,
	compatibility legacyObservationCompatibility,
	run *owningExtractionRun,
	signal *legacySignalState,
) (legacyObservationWrite, error) {
	if !legacyTimestampRepresentable(value.RecordedAt()) {
		return legacyObservationWrite{}, newObservationBoundaryError(
			ErrObservationNotRepresentable, reasonRecordedAtNotRepresentable, string(value.ID()),
		)
	}
	if err := ensureLegacyValidTimePrecision(value.ValidTime(), value.ID()); err != nil {
		return legacyObservationWrite{}, err
	}
	if value.LegacyUncited() {
		return legacyObservationWrite{}, newObservationBoundaryError(ErrObservationNotRepresentable, reasonLegacyUncitedNotRepresentable, string(value.ID()))
	}
	subjectEntityID, subjectMentionID, err := encodeLegacyTerm(value.Statement().Subject)
	if err != nil {
		return legacyObservationWrite{}, newObservationBoundaryError(ErrObservationNotRepresentable, reasonTermNotRepresentable, string(value.ID()))
	}
	objectEntityID, objectMentionID, err := encodeLegacyTerm(value.Statement().Object)
	if err != nil {
		return legacyObservationWrite{}, newObservationBoundaryError(ErrObservationNotRepresentable, reasonTermNotRepresentable, string(value.ID()))
	}
	validStart, validEnd, err := encodeLegacyValidTime(value.ValidTime())
	if err != nil {
		return legacyObservationWrite{}, newObservationBoundaryError(ErrObservationNotRepresentable, reasonValidTimeNotRepresentable, string(value.ID()))
	}
	var confidence *float64
	if canonicalConfidence, ok := value.Confidence(); ok {
		if canonicalConfidence.Scale() != observation.ConfidenceUnspecifiedLegacy {
			return legacyObservationWrite{}, newObservationBoundaryError(ErrObservationNotRepresentable, reasonConfidenceScaleNotRepresentable, string(value.ID()))
		}
		confidenceValue := canonicalConfidence.Value()
		confidence = &confidenceValue
	}
	if err := validateOwningRun(value, run); err != nil {
		return legacyObservationWrite{}, err
	}
	if err := validateActiveSignal(signal, run, value.ID()); err != nil {
		return legacyObservationWrite{}, err
	}
	origin, err := legacyObservationOrigin(value, compatibility, signal)
	if err != nil {
		return legacyObservationWrite{}, err
	}

	write := legacyObservationWrite{
		Row: legacyObservationRow{
			ID: string(value.ID()), SubjectEntityID: subjectEntityID, ObjectEntityID: objectEntityID,
			SubjectMentionID: subjectMentionID, ObjectMentionID: objectMentionID,
			Predicate: string(value.Statement().Predicate), Derivation: value.Derivation().Method,
			EpistemicStatus: string(value.Status()), ValidStart: validStart, ValidEnd: validEnd,
			RecordedAt: value.RecordedAt(), Confidence: confidence,
		},
		Origin: origin, Signal: cloneLegacySignalState(signal),
	}
	if run != nil && value.Derivation().RunID != "" {
		write.Row.ExtractionRunID = run.ID
	}
	digest, err := computeObservationDigestV1(write)
	if err != nil {
		return legacyObservationWrite{}, newObservationBoundaryError(ErrObservationNotRepresentable, reasonLegacyUUIDNotRepresentable, string(value.ID()))
	}
	if compatibility.storedDigest != ([sha256.Size]byte{}) && compatibility.storedDigest != digest {
		return legacyObservationWrite{}, newObservationBoundaryError(ErrObservationConflict, reasonObservationDigestMismatch, string(value.ID()))
	}
	write.Row.Digest = digest
	return write, nil
}

func ensureLegacyValidTimePrecision(value observation.TemporalExtent, observationID observation.ObservationID) error {
	if instant, ok := value.Instant(); ok {
		if !legacyTimestampRepresentable(instant) {
			return newObservationBoundaryError(ErrObservationNotRepresentable, reasonValidTimeNotRepresentable, string(observationID))
		}
		return nil
	}
	start, hasStart, end, hasEnd := value.Bounds()
	if hasStart && !legacyTimestampRepresentable(start) || hasEnd && !legacyTimestampRepresentable(end) {
		return newObservationBoundaryError(ErrObservationNotRepresentable, reasonValidTimeNotRepresentable, string(observationID))
	}
	return nil
}

func decodeLegacyTerm(entityID, mentionID string) (observation.Term, error) {
	switch {
	case entityID == "" && mentionID == "":
		return observation.AbsentTerm(), nil
	case entityID == "":
		return observation.NewMentionTerm(mentionID)
	default:
		return observation.NewEntityTerm(entityID, mentionID)
	}
}

func encodeLegacyTerm(term observation.Term) (entityID, mentionID string, err error) {
	switch term.Kind() {
	case observation.TermAbsent:
		return "", "", nil
	case observation.TermMention:
		mentionID, _ = term.MentionID()
		return "", mentionID, nil
	case observation.TermEntity:
		entityID, mentionID, _ = term.Entity()
		return entityID, mentionID, nil
	default:
		return "", "", fmt.Errorf("term is not representable")
	}
}

func decodeLegacyValidTime(start, end *time.Time) (observation.TemporalExtent, error) {
	switch {
	case start == nil && end == nil:
		return observation.UnknownTime(), nil
	case start == nil:
		return observation.TemporalExtent{}, fmt.Errorf("legacy end requires start")
	case end == nil:
		return observation.Since(*start)
	case start.Equal(*end):
		return observation.AtTime(*start)
	default:
		return observation.During(*start, *end)
	}
}

func encodeLegacyValidTime(value observation.TemporalExtent) (start, end *time.Time, err error) {
	switch value.Kind() {
	case observation.TemporalUnknown:
		return nil, nil, nil
	case observation.TemporalInstant:
		instant, _ := value.Instant()
		return timePointerCopy(instant), timePointerCopy(instant), nil
	case observation.TemporalInterval:
		intervalStart, hasStart, intervalEnd, hasEnd := value.Bounds()
		if !hasStart {
			return nil, nil, fmt.Errorf("legacy interval requires start")
		}
		start = timePointerCopy(intervalStart)
		if hasEnd {
			end = timePointerCopy(intervalEnd)
		}
		return start, end, nil
	default:
		return nil, nil, fmt.Errorf("valid time is not representable")
	}
}

func decodeLegacyConfidence(value *float64) (*observation.Confidence, error) {
	if value == nil {
		return nil, nil
	}
	confidence, err := observation.NewLegacyConfidence(*value)
	if err != nil {
		return nil, err
	}
	return &confidence, nil
}

func canonicalEvidenceLinks(origin []evidence.EvidenceID, signal *legacySignalState) ([]observation.EvidenceLink, error) {
	links := make([]observation.EvidenceLink, 0, len(origin))
	seen := make(map[observation.EvidenceLink]struct{}, len(origin))
	appendLink := func(link observation.EvidenceLink) {
		if _, exists := seen[link]; exists {
			return
		}
		seen[link] = struct{}{}
		links = append(links, link)
	}
	for _, evidenceID := range origin {
		appendLink(observation.EvidenceLink{EvidenceID: evidenceID, Role: observation.EvidenceSupporting})
	}
	if signal != nil {
		for _, signalEvidence := range signal.Evidence {
			appendLink(observation.EvidenceLink{
				EvidenceID: evidence.EvidenceID(signalEvidence.EvidenceSpanID),
				Role:       observation.EvidenceRole(signalEvidence.Role),
			})
		}
	}
	return links, nil
}

func validateOwningRun(value observation.Observation, run *owningExtractionRun) error {
	derivation := value.Derivation()
	if derivation.LegacyUnversioned {
		return newObservationBoundaryError(ErrObservationNotRepresentable, reasonLegacyDerivationNotRepresentable, string(value.ID()))
	}
	if run == nil {
		return newObservationBoundaryError(ErrObservationCompatibility, reasonOwningRunRequired, string(value.ID()))
	}
	if derivation.RunID == "" || run.ID != derivation.RunID {
		return newObservationBoundaryError(ErrObservationCompatibility, reasonCompletionOwnerMismatch, string(value.ID()))
	}
	if (run.ModelID == "") != (run.PromptVersion == "") ||
		(derivation.Model == "") != (derivation.PromptVersion == "") ||
		derivation.Version != run.PromptVersion || run.ModelID != derivation.Model || run.PromptVersion != derivation.PromptVersion {
		return newObservationBoundaryError(ErrObservationCompatibility, reasonOwningRunProvenanceMismatch, string(value.ID()))
	}
	if !value.RecordedAt().Equal(run.RecordedAt) {
		return newObservationBoundaryError(ErrObservationCompatibility, reasonRecordedAtOwnerMismatch, string(value.ID()))
	}
	return nil
}

func validateActiveSignal(signal *legacySignalState, run *owningExtractionRun, observationID observation.ObservationID) error {
	if signal == nil {
		return nil
	}
	if signal.Input.ObservationID != string(observationID) {
		return newObservationBoundaryError(ErrObservationCompatibility, reasonCompletionOwnerMismatch, string(observationID))
	}
	if signal.Input.ExtractionModelID != run.ModelID || signal.Input.PromptVersion != run.PromptVersion {
		return newObservationBoundaryError(ErrObservationCompatibility, reasonOwningRunProvenanceMismatch, string(observationID))
	}
	return nil
}

func legacyObservationOrigin(value observation.Observation, compatibility legacyObservationCompatibility, signal *legacySignalState) ([]evidence.EvidenceID, error) {
	origin := cloneEvidenceIDs(compatibility.observationEvidenceOrigin)
	if compatibility.observationEvidenceOrigin == nil {
		for _, link := range value.EvidenceLinks() {
			if link.Role == observation.EvidenceSupporting {
				origin = append(origin, link.EvidenceID)
			}
		}
	}
	origin, err := normalizeLegacyOrigin(origin)
	if err != nil {
		return nil, newObservationBoundaryError(ErrObservationNotRepresentable, reasonLegacyUUIDNotRepresentable, string(value.ID()))
	}
	wantLinks, err := canonicalEvidenceLinks(origin, signal)
	if err != nil {
		return nil, err
	}
	if !sameEvidenceLinks(value.EvidenceLinks(), wantLinks) {
		return nil, newObservationBoundaryError(ErrObservationCompatibility, reasonEvidenceOwnershipMismatch, string(value.ID()))
	}
	return origin, nil
}

func sameEvidenceLinks(left, right []observation.EvidenceLink) bool {
	if len(left) != len(right) {
		return false
	}
	leftSet := make(map[observation.EvidenceLink]struct{}, len(left))
	for _, link := range left {
		leftSet[link] = struct{}{}
	}
	for _, link := range right {
		if _, exists := leftSet[link]; !exists {
			return false
		}
	}
	return true
}

func cloneLegacySignalState(value *legacySignalState) *legacySignalState {
	if value == nil {
		return nil
	}
	clone := *value
	clone.Evidence = append([]SignalEvidenceInput(nil), value.Evidence...)
	return &clone
}

func cloneEvidenceIDs(value []evidence.EvidenceID) []evidence.EvidenceID {
	if value == nil {
		return nil
	}
	return append(make([]evidence.EvidenceID, 0, len(value)), value...)
}

func normalizeLegacyOrigin(value []evidence.EvidenceID) ([]evidence.EvidenceID, error) {
	if value == nil {
		return nil, nil
	}
	identifiers := make([]string, len(value))
	for index, evidenceID := range value {
		identifiers[index] = string(evidenceID)
	}
	identifiers, err := canonicalEvidenceIDs(identifiers)
	if err != nil {
		return nil, err
	}
	result := make([]evidence.EvidenceID, len(identifiers))
	for index, identifier := range identifiers {
		result[index] = evidence.EvidenceID(identifier)
	}
	return result, nil
}

func timePointerCopy(value time.Time) *time.Time { return &value }

func computeObservationDigestV1(write legacyObservationWrite) ([sha256.Size]byte, error) {
	row := write.Row
	var err error
	for _, value := range []*string{
		&row.ExtractionRunID,
		&row.SubjectEntityID,
		&row.ObjectEntityID,
		&row.SubjectMentionID,
		&row.ObjectMentionID,
	} {
		if *value == "" {
			continue
		}
		*value, err = canonicalUUID(*value)
		if err != nil {
			return [sha256.Size]byte{}, fmt.Errorf("legacy observation identifier is invalid: %w", err)
		}
	}
	origin := make([]string, len(write.Origin))
	for index, evidenceID := range write.Origin {
		origin[index] = string(evidenceID)
	}
	origin, err = canonicalEvidenceIDs(origin)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("legacy observation origin is invalid: %w", err)
	}
	fields := []string{
		row.ExtractionRunID,
		row.SubjectEntityID,
		row.ObjectEntityID,
		row.SubjectMentionID,
		row.ObjectMentionID,
		row.Predicate,
		row.Derivation,
		row.EpistemicStatus,
	}
	fields = appendLegacyTime(fields, row.ValidStart)
	fields = appendLegacyTime(fields, row.ValidEnd)
	fields = appendLegacyConfidence(fields, row.Confidence)
	fields = append(fields, origin...)
	return sha256.Sum256([]byte(strings.Join(fields, "\x00"))), nil
}

func appendLegacyTime(fields []string, value *time.Time) []string {
	if value == nil {
		return append(fields, "")
	}
	return append(fields, value.UTC().Format(time.RFC3339Nano))
}

func appendLegacyConfidence(fields []string, value *float64) []string {
	if value == nil {
		return append(fields, "")
	}
	return append(fields, fmt.Sprintf("%.17g", *value))
}
