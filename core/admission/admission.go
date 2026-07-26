// Package admission defines provider-neutral immutable admission authority.
package admission

import (
	"fmt"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/internal/canonicalhash"
	"github.com/JakeFAU/stacks/core/internal/reasoncode"
	"github.com/JakeFAU/stacks/core/timepoint"
)

// DecisionDigestVersion identifies the canonical admission decision encoding.
const DecisionDigestVersion = "stacks.admission-decision.v1.canonical"

// TargetKind identifies the bounded category controlled by a decision.
type TargetKind string

// Outcome records the target's admission state.
type Outcome string

// Authority identifies who or what made an admission decision.
type Authority string

const (
	// TargetExtractionRun applies admission to an extraction run.
	TargetExtractionRun TargetKind = "extraction_run"
	// TargetMention applies admission to an identity mention.
	TargetMention TargetKind = "mention"
	// TargetObservation applies admission to an observation.
	TargetObservation TargetKind = "observation"
	// TargetIdentityDecision applies admission to an identity decision.
	TargetIdentityDecision TargetKind = "identity_decision"

	// Admitted allows a target to participate in current projections.
	Admitted Outcome = "admitted"
	// Quarantined keeps a target out of current projections pending review.
	Quarantined Outcome = "quarantined"
	// Retired removes a formerly effective target from current projections.
	Retired Outcome = "retired"

	// AuthorityAutomatic identifies a deterministic automatic decision.
	AuthorityAutomatic Authority = "automatic"
	// AuthorityReviewer identifies an explicit reviewer decision.
	AuthorityReviewer Authority = "reviewer"
	// AuthorityPolicy identifies a deterministic policy decision.
	AuthorityPolicy Authority = "policy"
)

// DecisionInput contains immutable admission authority.
type DecisionInput struct {
	ID           string
	TargetKind   TargetKind
	TargetID     string
	Outcome      Outcome
	ReasonCode   string
	Authority    Authority
	RecordedAt   time.Time
	SupersedesID string
}

// Decision is an immutable admission authority decision.
type Decision struct {
	id           string
	targetKind   TargetKind
	targetID     string
	outcome      Outcome
	reasonCode   string
	authority    Authority
	recordedAt   time.Time
	supersedesID string
	digest       evidence.ContentDigest
}

// NewDecision validates and constructs an admission decision. Repository code
// owns effective-predecessor and reviewer-authorization invariants.
func NewDecision(input DecisionInput) (Decision, error) {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		return Decision{}, fmt.Errorf("admission decision ID is required")
	}
	if !input.TargetKind.valid() {
		return Decision{}, fmt.Errorf("admission target kind is invalid")
	}
	targetID := strings.TrimSpace(input.TargetID)
	if targetID == "" {
		return Decision{}, fmt.Errorf("admission target ID is required")
	}
	if !input.Outcome.valid() {
		return Decision{}, fmt.Errorf("admission outcome is invalid")
	}
	reasonCode, err := reasoncode.Validate(input.ReasonCode)
	if err != nil {
		return Decision{}, fmt.Errorf("admission decision: %w", err)
	}
	if !input.Authority.valid() {
		return Decision{}, fmt.Errorf("admission authority is invalid")
	}
	if input.RecordedAt.IsZero() {
		return Decision{}, fmt.Errorf("admission recorded time is required")
	}
	supersedesID := strings.TrimSpace(input.SupersedesID)
	if supersedesID == id {
		return Decision{}, fmt.Errorf("admission decision cannot supersede itself")
	}
	value := Decision{
		id:           id,
		targetKind:   input.TargetKind,
		targetID:     targetID,
		outcome:      input.Outcome,
		reasonCode:   reasonCode,
		authority:    input.Authority,
		recordedAt:   timepoint.Normalize(input.RecordedAt),
		supersedesID: supersedesID,
	}
	value.digest = digestDecision(value)
	return value, nil
}

func (value TargetKind) valid() bool {
	switch value {
	case TargetExtractionRun, TargetMention, TargetObservation, TargetIdentityDecision:
		return true
	default:
		return false
	}
}

func (value Outcome) valid() bool {
	return value == Admitted || value == Quarantined || value == Retired
}

func (value Authority) valid() bool {
	return value == AuthorityAutomatic || value == AuthorityReviewer || value == AuthorityPolicy
}

func digestDecision(value Decision) evidence.ContentDigest {
	encoder := canonicalhash.New(DecisionDigestVersion)
	encoder.Time(value.recordedAt)
	encoder.String(string(value.targetKind))
	encoder.String(value.targetID)
	encoder.String(string(value.outcome))
	encoder.String(string(value.authority))
	encoder.String(value.reasonCode)
	encoder.String(value.supersedesID)
	return evidence.ContentDigest(encoder.Sum())
}

// ID returns the stable decision identifier.
func (value Decision) ID() string { return value.id }

// TargetKind returns the category controlled by the decision.
func (value Decision) TargetKind() TargetKind { return value.targetKind }

// TargetID returns the opaque identifier of the controlled target.
func (value Decision) TargetID() string { return value.targetID }

// Outcome returns the admission state selected by the decision.
func (value Decision) Outcome() Outcome { return value.outcome }

// ReasonCode returns the exact non-secret decision reason code.
func (value Decision) ReasonCode() string { return value.reasonCode }

// Authority returns who or what made the decision.
func (value Decision) Authority() Authority { return value.authority }

// RecordedAt returns when Stacks recorded the decision.
func (value Decision) RecordedAt() time.Time { return value.recordedAt }

// SupersedesID returns the preceding decision replaced by this decision.
func (value Decision) SupersedesID() string { return value.supersedesID }

// Digest returns the canonical semantic admission decision digest.
func (value Decision) Digest() evidence.ContentDigest { return value.digest }

// DigestVersion returns the encoding version for Digest.
func (value Decision) DigestVersion() string { return DecisionDigestVersion }
