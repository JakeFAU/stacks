package observation

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
)

// ObservationID is assigned by the extraction boundary. Retries for the same
// logical observation must use the same identifier.
type ObservationID string

// EpistemicStatus records how an observation is supported and validated.
type EpistemicStatus string

const (
	StatusObserved              EpistemicStatus = "observed"
	StatusInferred              EpistemicStatus = "inferred"
	StatusHypothesized          EpistemicStatus = "hypothesized"
	StatusValidatedStructurally EpistemicStatus = "validated_structurally"
	StatusValidatedEmpirically  EpistemicStatus = "validated_empirically"
	StatusRejected              EpistemicStatus = "rejected"
)

// Predicate names the relationship between a statement's subject and object.
type Predicate string

// NewPredicate validates a predicate while preserving its exact bytes.
func NewPredicate(value string) (Predicate, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("observation predicate is required")
	}
	return Predicate(value), nil
}

// Statement is a closed, source-grounded proposition.
type Statement struct {
	Subject   Term
	Predicate Predicate
	Object    Term
}

// EvidenceRole records whether evidence supports or contradicts an
// observation.
type EvidenceRole string

const (
	EvidenceSupporting    EvidenceRole = "supporting"
	EvidenceContradicting EvidenceRole = "contradicting"
)

// EvidenceLink associates immutable source evidence with its role.
type EvidenceLink struct {
	EvidenceID evidence.EvidenceID
	Role       EvidenceRole
}

// Derivation identifies the transformation that produced an observation.
// LegacyUnversioned explicitly marks historical derivations that predate
// versioned extraction.
type Derivation struct {
	Method            string
	Version           string
	RunID             string
	Model             string
	PromptVersion     string
	LegacyUnversioned bool
}

// ObservationInput contains the values required to construct an observation.
type ObservationInput struct {
	ID         ObservationID
	Statement  Statement
	ValidTime  TemporalExtent
	RecordedAt time.Time
	Evidence   []EvidenceLink
	Derivation Derivation
	Status     EpistemicStatus
	Confidence *Confidence
	// LegacyUncited marks a historical observation that has no durable
	// evidence links. New observations must remain cited.
	LegacyUncited bool
}

// Observation is an immutable temporal proposition. Ordinary observations are
// evidence-backed. Explicit legacy compatibility values may be uncited; they
// retain observation and provenance metadata and remain unresolved during
// temporal aggregation. ValidTime says when a proposition applied in the
// source world; RecordedAt says when it was recorded.
type Observation struct {
	id            ObservationID
	statement     Statement
	validTime     TemporalExtent
	recordedAt    time.Time
	evidence      []EvidenceLink
	derivation    Derivation
	status        EpistemicStatus
	confidence    Confidence
	hasConfidence bool
	legacyUncited bool
}

// NewObservation validates and constructs a canonical observation.
func NewObservation(input ObservationInput) (Observation, error) {
	id := ObservationID(strings.TrimSpace(string(input.ID)))
	if id == "" {
		return Observation{}, fmt.Errorf("observation ID is required")
	}

	predicate, err := NewPredicate(string(input.Statement.Predicate))
	if err != nil {
		return Observation{}, err
	}
	statement := input.Statement
	statement.Predicate = predicate

	if input.RecordedAt.IsZero() {
		return Observation{}, fmt.Errorf("observation recorded time is required")
	}
	evidenceLinks, err := normalizeEvidenceLinks(input.Evidence, input.LegacyUncited)
	if err != nil {
		return Observation{}, err
	}
	derivation, err := normalizeDerivation(input.Derivation)
	if err != nil {
		return Observation{}, err
	}
	if !input.Status.valid() {
		return Observation{}, fmt.Errorf("observation epistemic status is invalid")
	}

	result := Observation{
		id:            id,
		statement:     statement,
		validTime:     input.ValidTime,
		recordedAt:    input.RecordedAt.UTC(),
		evidence:      evidenceLinks,
		derivation:    derivation,
		status:        input.Status,
		legacyUncited: input.LegacyUncited,
	}
	if input.Confidence != nil {
		if err := validateConfidence(*input.Confidence); err != nil {
			return Observation{}, err
		}
		result.confidence = *input.Confidence
		result.hasConfidence = true
	}
	return result, nil
}

func normalizeEvidenceLinks(input []EvidenceLink, legacyUncited bool) ([]EvidenceLink, error) {
	if len(input) == 0 {
		if legacyUncited {
			return nil, nil
		}
		return nil, fmt.Errorf("observation evidence is required")
	}
	if legacyUncited {
		return nil, fmt.Errorf("legacy uncited observation cannot declare evidence")
	}
	result := make([]EvidenceLink, len(input))
	seen := make(map[EvidenceLink]struct{}, len(input))
	for index, link := range input {
		link.EvidenceID = evidence.EvidenceID(strings.TrimSpace(string(link.EvidenceID)))
		if link.EvidenceID == "" {
			return nil, fmt.Errorf("observation evidence ID is required")
		}
		if !link.Role.valid() {
			return nil, fmt.Errorf("observation evidence role is invalid")
		}
		if _, exists := seen[link]; exists {
			return nil, fmt.Errorf("observation evidence-role pairs must be unique")
		}
		seen[link] = struct{}{}
		result[index] = link
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].EvidenceID != result[right].EvidenceID {
			return result[left].EvidenceID < result[right].EvidenceID
		}
		return result[left].Role < result[right].Role
	})
	return result, nil
}

func normalizeDerivation(input Derivation) (Derivation, error) {
	if strings.TrimSpace(input.Method) == "" {
		return Derivation{}, fmt.Errorf("observation derivation method is required")
	}
	switch {
	case input.LegacyUnversioned && input.Version != "":
		return Derivation{}, fmt.Errorf("legacy unversioned derivation cannot declare a version")
	case !input.LegacyUnversioned && strings.TrimSpace(input.Version) == "":
		return Derivation{}, fmt.Errorf("observation derivation version is required")
	}
	if input.RunID != "" && strings.TrimSpace(input.RunID) == "" {
		return Derivation{}, fmt.Errorf("observation derivation run ID cannot be whitespace")
	}
	if input.Model != "" && strings.TrimSpace(input.Model) == "" {
		return Derivation{}, fmt.Errorf("observation model cannot be whitespace")
	}
	if input.PromptVersion != "" && strings.TrimSpace(input.PromptVersion) == "" {
		return Derivation{}, fmt.Errorf("observation prompt version cannot be whitespace")
	}
	if (input.Model == "") != (input.PromptVersion == "") {
		return Derivation{}, fmt.Errorf("observation model and prompt version must be provided together")
	}
	return input, nil
}

func validateConfidence(confidence Confidence) error {
	switch confidence.scale {
	case ConfidenceUnitInterval:
		if !finite(confidence.value) || confidence.value < 0 || confidence.value > 1 {
			return fmt.Errorf("observation unit-interval confidence is invalid")
		}
	case ConfidenceUnspecifiedLegacy:
		if !finite(confidence.value) {
			return fmt.Errorf("observation legacy confidence is invalid")
		}
	default:
		return fmt.Errorf("observation confidence scale is invalid")
	}
	return nil
}

func (status EpistemicStatus) valid() bool {
	switch status {
	case StatusObserved,
		StatusInferred,
		StatusHypothesized,
		StatusValidatedStructurally,
		StatusValidatedEmpirically,
		StatusRejected:
		return true
	default:
		return false
	}
}

func (role EvidenceRole) valid() bool {
	return role == EvidenceSupporting || role == EvidenceContradicting
}

// ID returns the stable observation identifier.
func (value Observation) ID() ObservationID {
	return value.id
}

// Statement returns the proposition.
func (value Observation) Statement() Statement {
	return value.statement
}

// ValidTime returns when the proposition applied in the source world.
func (value Observation) ValidTime() TemporalExtent {
	return value.validTime
}

// RecordedAt returns when the proposition was recorded.
func (value Observation) RecordedAt() time.Time {
	return value.recordedAt
}

// EvidenceLinks returns a defensive copy in stable identifier-and-role order.
func (value Observation) EvidenceLinks() []EvidenceLink {
	return append([]EvidenceLink(nil), value.evidence...)
}

// Derivation returns how the observation was produced.
func (value Observation) Derivation() Derivation {
	return value.derivation
}

// Status returns the observation's epistemic status.
func (value Observation) Status() EpistemicStatus {
	return value.status
}

// Confidence returns model confidence metadata when one was supplied.
func (value Observation) Confidence() (Confidence, bool) {
	return value.confidence, value.hasConfidence
}

// LegacyUncited reports whether this observation is a lossless historical
// compatibility value with no durable evidence links.
func (value Observation) LegacyUncited() bool {
	return value.legacyUncited
}
