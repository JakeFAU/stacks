package knowledge

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// ObservationID is assigned by the extraction boundary. Retries for the same
// logical observation must use the same identifier.
type ObservationID string

// EpistemicStatus records how strongly an observation is supported.
type EpistemicStatus string

const (
	StatusObserved     EpistemicStatus = "observed"
	StatusInferred     EpistemicStatus = "inferred"
	StatusHypothesized EpistemicStatus = "hypothesized"
	StatusRejected     EpistemicStatus = "rejected"
)

// TemporalKind distinguishes the temporal meanings an observation may carry.
type TemporalKind uint8

const (
	TemporalUnknown TemporalKind = iota
	TemporalInstant
	TemporalInterval
	TemporalWindow
)

// TemporalExtent describes when an observation was valid in the source world.
// An interval describes a duration and may have an open start or end. A window
// describes uncertainty: an instant occurred somewhere within its bounds.
// Bounded intervals and windows are half-open: start is included, end excluded.
type TemporalExtent struct {
	kind     TemporalKind
	start    time.Time
	end      time.Time
	hasStart bool
	hasEnd   bool
}

// UnknownTime represents source time that is genuinely unknown.
func UnknownTime() TemporalExtent {
	return TemporalExtent{kind: TemporalUnknown}
}

// AtTime represents an observation valid at a specific instant.
func AtTime(instant time.Time) (TemporalExtent, error) {
	if instant.IsZero() {
		return TemporalExtent{}, fmt.Errorf("valid instant is required")
	}
	return TemporalExtent{
		kind:     TemporalInstant,
		start:    instant.UTC(),
		hasStart: true,
	}, nil
}

// During represents an observation valid during the half-open interval
// [start, end).
func During(start, end time.Time) (TemporalExtent, error) {
	if start.IsZero() || end.IsZero() {
		return TemporalExtent{}, fmt.Errorf("valid interval bounds are required")
	}
	if !end.After(start) {
		return TemporalExtent{}, fmt.Errorf("valid interval end must be after start")
	}
	return TemporalExtent{
		kind:     TemporalInterval,
		start:    start.UTC(),
		end:      end.UTC(),
		hasStart: true,
		hasEnd:   true,
	}, nil
}

// Since represents an observation valid from start with no known end.
func Since(start time.Time) (TemporalExtent, error) {
	if start.IsZero() {
		return TemporalExtent{}, fmt.Errorf("valid interval start is required")
	}
	return TemporalExtent{
		kind:     TemporalInterval,
		start:    start.UTC(),
		hasStart: true,
	}, nil
}

// Until represents an observation valid before end with no known start.
func Until(end time.Time) (TemporalExtent, error) {
	if end.IsZero() {
		return TemporalExtent{}, fmt.Errorf("valid interval end is required")
	}
	return TemporalExtent{
		kind:   TemporalInterval,
		end:    end.UTC(),
		hasEnd: true,
	}, nil
}

// Within represents an instant known only to have occurred somewhere in the
// half-open window [start, end).
func Within(start, end time.Time) (TemporalExtent, error) {
	if start.IsZero() || end.IsZero() {
		return TemporalExtent{}, fmt.Errorf("valid time window bounds are required")
	}
	if !end.After(start) {
		return TemporalExtent{}, fmt.Errorf("valid time window end must be after start")
	}
	return TemporalExtent{
		kind:     TemporalWindow,
		start:    start.UTC(),
		end:      end.UTC(),
		hasStart: true,
		hasEnd:   true,
	}, nil
}

// Kind returns the temporal extent kind.
func (extent TemporalExtent) Kind() TemporalKind {
	return extent.kind
}

// Instant returns the valid instant when Kind is TemporalInstant.
func (extent TemporalExtent) Instant() (time.Time, bool) {
	return extent.start, extent.kind == TemporalInstant
}

// Bounds returns interval or uncertainty-window bounds. hasStart and hasEnd
// distinguish open bounds from actual timestamps.
func (extent TemporalExtent) Bounds() (start time.Time, hasStart bool, end time.Time, hasEnd bool) {
	if extent.kind != TemporalInterval && extent.kind != TemporalWindow {
		return time.Time{}, false, time.Time{}, false
	}
	return extent.start, extent.hasStart, extent.end, extent.hasEnd
}

// Statement is a source-grounded proposition before entity resolution.
type Statement struct {
	Subject   string
	Predicate string
	Object    string
}

// Derivation identifies the transformation that produced an observation.
// Model and PromptVersion are either both present or both absent.
type Derivation struct {
	Method        string
	Version       string
	Model         string
	PromptVersion string
}

// ObservationInput contains the values needed to construct an observation.
type ObservationInput struct {
	ID          ObservationID
	Statement   Statement
	ValidTime   TemporalExtent
	RecordedAt  time.Time
	EvidenceIDs []EvidenceID
	Derivation  Derivation
	Status      EpistemicStatus
	Confidence  *float64
}

// Observation is an immutable, source-grounded proposition. ValidTime says
// when it applied in the source world; RecordedAt says when Stacks learned it.
type Observation struct {
	id            ObservationID
	statement     Statement
	validTime     TemporalExtent
	recordedAt    time.Time
	evidenceIDs   []EvidenceID
	derivation    Derivation
	status        EpistemicStatus
	confidence    float64
	hasConfidence bool
}

// NewObservation validates and constructs a temporal observation.
func NewObservation(input ObservationInput) (Observation, error) {
	id := ObservationID(strings.TrimSpace(string(input.ID)))
	if id == "" {
		return Observation{}, fmt.Errorf("observation ID is required")
	}

	statement := Statement{
		Subject:   strings.TrimSpace(input.Statement.Subject),
		Predicate: strings.TrimSpace(input.Statement.Predicate),
		Object:    strings.TrimSpace(input.Statement.Object),
	}
	if statement.Subject == "" || statement.Predicate == "" || statement.Object == "" {
		return Observation{}, fmt.Errorf("observation statement fields are required")
	}
	if input.RecordedAt.IsZero() {
		return Observation{}, fmt.Errorf("observation recorded time is required")
	}
	if len(input.EvidenceIDs) == 0 {
		return Observation{}, fmt.Errorf("observation evidence is required")
	}

	evidenceIDs := make([]EvidenceID, len(input.EvidenceIDs))
	seenEvidence := make(map[EvidenceID]struct{}, len(input.EvidenceIDs))
	for index, evidenceID := range input.EvidenceIDs {
		evidenceID = EvidenceID(strings.TrimSpace(string(evidenceID)))
		if evidenceID == "" {
			return Observation{}, fmt.Errorf("observation evidence ID is required")
		}
		if _, exists := seenEvidence[evidenceID]; exists {
			return Observation{}, fmt.Errorf("observation evidence IDs must be unique")
		}
		seenEvidence[evidenceID] = struct{}{}
		evidenceIDs[index] = evidenceID
	}

	derivation := Derivation{
		Method:        strings.TrimSpace(input.Derivation.Method),
		Version:       strings.TrimSpace(input.Derivation.Version),
		Model:         strings.TrimSpace(input.Derivation.Model),
		PromptVersion: strings.TrimSpace(input.Derivation.PromptVersion),
	}
	if derivation.Method == "" || derivation.Version == "" {
		return Observation{}, fmt.Errorf("observation derivation method and version are required")
	}
	if (derivation.Model == "") != (derivation.PromptVersion == "") {
		return Observation{}, fmt.Errorf("observation model and prompt version must be provided together")
	}
	if !input.Status.valid() {
		return Observation{}, fmt.Errorf("observation epistemic status is invalid")
	}

	observation := Observation{
		id:          id,
		statement:   statement,
		validTime:   input.ValidTime,
		recordedAt:  input.RecordedAt.UTC(),
		evidenceIDs: evidenceIDs,
		derivation:  derivation,
		status:      input.Status,
	}
	if input.Confidence != nil {
		if math.IsNaN(*input.Confidence) || *input.Confidence < 0 || *input.Confidence > 1 {
			return Observation{}, fmt.Errorf("observation confidence must be between 0 and 1")
		}
		observation.confidence = *input.Confidence
		observation.hasConfidence = true
	}

	return observation, nil
}

func (status EpistemicStatus) valid() bool {
	switch status {
	case StatusObserved, StatusInferred, StatusHypothesized, StatusRejected:
		return true
	default:
		return false
	}
}

// ID returns the stable observation identifier.
func (observation Observation) ID() ObservationID {
	return observation.id
}

// Statement returns the source-grounded proposition.
func (observation Observation) Statement() Statement {
	return observation.statement
}

// ValidTime returns when the proposition applied in the source world.
func (observation Observation) ValidTime() TemporalExtent {
	return observation.validTime
}

// RecordedAt returns when Stacks learned the proposition.
func (observation Observation) RecordedAt() time.Time {
	return observation.recordedAt
}

// EvidenceIDs returns a defensive copy of the supporting evidence identifiers.
func (observation Observation) EvidenceIDs() []EvidenceID {
	return append([]EvidenceID(nil), observation.evidenceIDs...)
}

// Derivation returns how the observation was produced.
func (observation Observation) Derivation() Derivation {
	return observation.derivation
}

// Status returns the observation's epistemic status.
func (observation Observation) Status() EpistemicStatus {
	return observation.status
}

// Confidence returns the confidence and whether one was supplied.
func (observation Observation) Confidence() (float64, bool) {
	return observation.confidence, observation.hasConfidence
}
