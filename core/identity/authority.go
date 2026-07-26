package identity

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/internal/canonicalhash"
	"github.com/JakeFAU/stacks/core/internal/reasoncode"
	"github.com/JakeFAU/stacks/core/timepoint"
)

// ResolutionDecisionDigestVersion identifies the canonical resolution
// decision encoding.
const ResolutionDecisionDigestVersion = "stacks.identity-resolution-decision.v1.canonical"

// EntityInput contains the values required to construct a canonical entity.
type EntityInput struct {
	ID          EntityID
	Kind        Kind
	DisplayName string
	RecordedAt  time.Time
}

// Entity is an immutable canonical entity authority value.
type Entity struct {
	id          EntityID
	kind        Kind
	displayName string
	recordedAt  time.Time
}

// NewEntity validates and constructs a canonical entity.
func NewEntity(input EntityInput) (Entity, error) {
	id, err := requiredEntityID(input.ID)
	if err != nil {
		return Entity{}, err
	}
	if input.Kind != KindPerson {
		return Entity{}, fmt.Errorf("entity kind is invalid")
	}
	if strings.TrimSpace(input.DisplayName) == "" {
		return Entity{}, fmt.Errorf("entity display name is required")
	}
	if input.RecordedAt.IsZero() {
		return Entity{}, fmt.Errorf("entity recorded time is required")
	}
	return Entity{
		id:          id,
		kind:        input.Kind,
		displayName: input.DisplayName,
		recordedAt:  timepoint.Normalize(input.RecordedAt),
	}, nil
}

// ID returns the stable entity identifier.
func (value Entity) ID() EntityID { return value.id }

// Kind returns the entity category.
func (value Entity) Kind() Kind { return value.kind }

// DisplayName returns the human-readable entity name.
func (value Entity) DisplayName() string { return value.displayName }

// RecordedAt returns when Stacks first recorded the entity.
func (value Entity) RecordedAt() time.Time { return value.recordedAt }

// MentionInput contains source-grounded mention and derivation provenance.
type MentionInput struct {
	ID                      MentionID
	EvidenceID              evidence.EvidenceID
	DerivationRunID         string
	Surface                 string
	NormalizedName          string
	ProposedEmail           string
	ProposedEmailEvidenceID evidence.EvidenceID
	Role                    string
	RecordedAt              time.Time
}

// MentionRecord is an immutable source-grounded identity mention.
type MentionRecord struct {
	id                      MentionID
	evidenceID              evidence.EvidenceID
	derivationRunID         string
	surface                 string
	normalizedName          string
	proposedEmail           string
	proposedEmailEvidenceID evidence.EvidenceID
	role                    string
	recordedAt              time.Time
}

// NewMention validates and constructs an immutable mention.
func NewMention(input MentionInput) (MentionRecord, error) {
	id, err := requiredMentionID(input.ID)
	if err != nil {
		return MentionRecord{}, err
	}
	evidenceID, err := requiredEvidenceID(input.EvidenceID, "mention evidence ID")
	if err != nil {
		return MentionRecord{}, err
	}
	derivationRunID := strings.TrimSpace(input.DerivationRunID)
	if derivationRunID == "" {
		return MentionRecord{}, fmt.Errorf("mention derivation run ID is required")
	}
	if strings.TrimSpace(input.Surface) == "" {
		return MentionRecord{}, fmt.Errorf("mention surface is required")
	}
	if strings.TrimSpace(input.NormalizedName) == "" {
		return MentionRecord{}, fmt.Errorf("mention normalized name is required")
	}
	if strings.TrimSpace(input.Role) == "" {
		return MentionRecord{}, fmt.Errorf("mention role is required")
	}
	if input.RecordedAt.IsZero() {
		return MentionRecord{}, fmt.Errorf("mention recorded time is required")
	}

	proposedEmailEvidenceID := evidence.EvidenceID(strings.TrimSpace(string(input.ProposedEmailEvidenceID)))
	switch {
	case input.ProposedEmail == "" && proposedEmailEvidenceID != "":
		return MentionRecord{}, fmt.Errorf("mention proposed email evidence requires a proposed email")
	case input.ProposedEmail != "" && proposedEmailEvidenceID == "":
		return MentionRecord{}, fmt.Errorf("mention proposed email evidence is required")
	case input.ProposedEmail != "" && !ValidEmail(input.ProposedEmail):
		return MentionRecord{}, fmt.Errorf("mention proposed email is invalid")
	}

	return MentionRecord{
		id:                      id,
		evidenceID:              evidenceID,
		derivationRunID:         derivationRunID,
		surface:                 input.Surface,
		normalizedName:          input.NormalizedName,
		proposedEmail:           input.ProposedEmail,
		proposedEmailEvidenceID: proposedEmailEvidenceID,
		role:                    input.Role,
		recordedAt:              timepoint.Normalize(input.RecordedAt),
	}, nil
}

// ID returns the stable mention identifier.
func (value MentionRecord) ID() MentionID { return value.id }

// EvidenceID returns the source evidence grounding the mention.
func (value MentionRecord) EvidenceID() evidence.EvidenceID { return value.evidenceID }

// DerivationRunID returns the extraction run that proposed the mention.
func (value MentionRecord) DerivationRunID() string { return value.derivationRunID }

// Surface returns the exact source-grounded mention surface.
func (value MentionRecord) Surface() string { return value.surface }

// NormalizedName returns the name used for deterministic comparison.
func (value MentionRecord) NormalizedName() string { return value.normalizedName }

// ProposedEmail returns the untrusted, audit-only email proposal.
func (value MentionRecord) ProposedEmail() string { return value.proposedEmail }

// ProposedEmailEvidenceID returns the evidence grounding a proposed email.
func (value MentionRecord) ProposedEmailEvidenceID() evidence.EvidenceID {
	return value.proposedEmailEvidenceID
}

// Role returns the mention's source-grounded role.
func (value MentionRecord) Role() string { return value.role }

// RecordedAt returns when Stacks recorded the mention.
func (value MentionRecord) RecordedAt() time.Time { return value.recordedAt }

// ResolutionProposalInput contains the values needed to propose identity
// resolution.
type ResolutionProposalInput struct {
	ID          ProposalID
	MentionID   MentionID
	ReasonCode  string
	EvidenceIDs []evidence.EvidenceID
	RecordedAt  time.Time
}

// ResolutionProposal is an immutable request for an authority decision.
type ResolutionProposal struct {
	id          ProposalID
	mentionID   MentionID
	reasonCode  string
	evidenceIDs []evidence.EvidenceID
	recordedAt  time.Time
}

// NewResolutionProposal validates and constructs a resolution proposal.
func NewResolutionProposal(input ResolutionProposalInput) (ResolutionProposal, error) {
	id, err := requiredProposalID(input.ID)
	if err != nil {
		return ResolutionProposal{}, err
	}
	mentionID, err := requiredMentionID(input.MentionID)
	if err != nil {
		return ResolutionProposal{}, err
	}
	reasonCode, err := reasoncode.Validate(input.ReasonCode)
	if err != nil {
		return ResolutionProposal{}, fmt.Errorf("resolution proposal: %w", err)
	}
	if len(input.EvidenceIDs) == 0 {
		return ResolutionProposal{}, fmt.Errorf("resolution proposal evidence is required")
	}
	evidenceIDs := make([]evidence.EvidenceID, len(input.EvidenceIDs))
	seen := make(map[evidence.EvidenceID]struct{}, len(input.EvidenceIDs))
	for index, inputID := range input.EvidenceIDs {
		evidenceID, err := requiredEvidenceID(inputID, "resolution proposal evidence ID")
		if err != nil {
			return ResolutionProposal{}, err
		}
		if _, exists := seen[evidenceID]; exists {
			return ResolutionProposal{}, fmt.Errorf("resolution proposal evidence IDs must be unique")
		}
		seen[evidenceID] = struct{}{}
		evidenceIDs[index] = evidenceID
	}
	if input.RecordedAt.IsZero() {
		return ResolutionProposal{}, fmt.Errorf("resolution proposal recorded time is required")
	}
	return ResolutionProposal{
		id:          id,
		mentionID:   mentionID,
		reasonCode:  reasonCode,
		evidenceIDs: evidenceIDs,
		recordedAt:  timepoint.Normalize(input.RecordedAt),
	}, nil
}

// ID returns the stable proposal identifier.
func (value ResolutionProposal) ID() ProposalID { return value.id }

// MentionID returns the mention being resolved.
func (value ResolutionProposal) MentionID() MentionID { return value.mentionID }

// ReasonCode returns the exact non-secret proposal reason code.
func (value ResolutionProposal) ReasonCode() string { return value.reasonCode }

// EvidenceIDs returns a defensive copy of the proposal evidence identifiers.
func (value ResolutionProposal) EvidenceIDs() []evidence.EvidenceID {
	return append([]evidence.EvidenceID(nil), value.evidenceIDs...)
}

// RecordedAt returns when Stacks recorded the proposal.
func (value ResolutionProposal) RecordedAt() time.Time { return value.recordedAt }

// CandidateSource identifies provider-neutral provenance for a candidate.
type CandidateSource struct {
	Kind      string
	Reference string
}

// ResolutionCandidateInput contains one deterministic review candidate.
type ResolutionCandidateInput struct {
	ID         CandidateID
	ProposalID ProposalID
	EntityID   EntityID
	Rank       int
	Confidence float64
	ReasonCode string
	Source     CandidateSource
	RecordedAt time.Time
}

// ResolutionCandidate is an immutable ranked review candidate.
type ResolutionCandidate struct {
	id         CandidateID
	proposalID ProposalID
	entityID   EntityID
	rank       int
	confidence float64
	reasonCode string
	source     CandidateSource
	recordedAt time.Time
}

// NewResolutionCandidate validates and constructs a review candidate.
func NewResolutionCandidate(input ResolutionCandidateInput) (ResolutionCandidate, error) {
	id, err := requiredCandidateID(input.ID)
	if err != nil {
		return ResolutionCandidate{}, err
	}
	proposalID, err := requiredProposalID(input.ProposalID)
	if err != nil {
		return ResolutionCandidate{}, err
	}
	entityID, err := requiredEntityID(input.EntityID)
	if err != nil {
		return ResolutionCandidate{}, err
	}
	if input.Rank <= 0 {
		return ResolutionCandidate{}, fmt.Errorf("resolution candidate rank must be positive")
	}
	if math.IsNaN(input.Confidence) || math.IsInf(input.Confidence, 0) ||
		input.Confidence < 0 || input.Confidence > 1 {
		return ResolutionCandidate{}, fmt.Errorf("resolution candidate confidence must be finite and within the unit interval")
	}
	reasonCode, err := reasoncode.Validate(input.ReasonCode)
	if err != nil {
		return ResolutionCandidate{}, fmt.Errorf("resolution candidate: %w", err)
	}
	source := CandidateSource{
		Kind:      strings.TrimSpace(input.Source.Kind),
		Reference: strings.TrimSpace(input.Source.Reference),
	}
	if source.Kind == "" || source.Reference == "" {
		return ResolutionCandidate{}, fmt.Errorf("resolution candidate source is required")
	}
	if input.RecordedAt.IsZero() {
		return ResolutionCandidate{}, fmt.Errorf("resolution candidate recorded time is required")
	}
	return ResolutionCandidate{
		id:         id,
		proposalID: proposalID,
		entityID:   entityID,
		rank:       input.Rank,
		confidence: input.Confidence,
		reasonCode: reasonCode,
		source:     source,
		recordedAt: timepoint.Normalize(input.RecordedAt),
	}, nil
}

// ID returns the stable candidate identifier.
func (value ResolutionCandidate) ID() CandidateID { return value.id }

// ProposalID returns the proposal containing the candidate.
func (value ResolutionCandidate) ProposalID() ProposalID { return value.proposalID }

// EntityID returns the candidate entity.
func (value ResolutionCandidate) EntityID() EntityID { return value.entityID }

// Rank returns the deterministic positive candidate rank.
func (value ResolutionCandidate) Rank() int { return value.rank }

// Confidence returns the candidate's unit-interval confidence.
func (value ResolutionCandidate) Confidence() float64 { return value.confidence }

// ReasonCode returns the exact non-secret candidate reason code.
func (value ResolutionCandidate) ReasonCode() string { return value.reasonCode }

// Source returns provider-neutral candidate provenance.
func (value ResolutionCandidate) Source() CandidateSource { return value.source }

// RecordedAt returns when Stacks recorded the candidate.
func (value ResolutionCandidate) RecordedAt() time.Time { return value.recordedAt }

// DecisionOutcome is the bounded result of identity resolution.
type DecisionOutcome string

// DecisionAuthority identifies who or what made a resolution decision.
type DecisionAuthority string

const (
	// DecisionAccepted accepts one canonical entity for a proposal.
	DecisionAccepted DecisionOutcome = "accepted"
	// DecisionRejected rejects a proposal without naming an entity.
	DecisionRejected DecisionOutcome = "rejected"

	// AuthorityAutomatic identifies a deterministic automatic decision.
	AuthorityAutomatic DecisionAuthority = "automatic"
	// AuthorityReviewer identifies an explicit reviewer decision.
	AuthorityReviewer DecisionAuthority = "reviewer"
)

// ResolutionDecisionInput contains immutable identity authority.
type ResolutionDecisionInput struct {
	ID           DecisionID
	ProposalID   ProposalID
	Outcome      DecisionOutcome
	EntityID     EntityID
	Authority    DecisionAuthority
	ReasonCode   string
	RecordedAt   time.Time
	SupersedesID DecisionID
}

// ResolutionDecision is an immutable identity authority decision.
type ResolutionDecision struct {
	id           DecisionID
	proposalID   ProposalID
	outcome      DecisionOutcome
	entityID     EntityID
	authority    DecisionAuthority
	reasonCode   string
	recordedAt   time.Time
	supersedesID DecisionID
	digest       evidence.ContentDigest
}

// NewResolutionDecision validates and constructs an identity authority
// decision. Repository code owns predecessor and reviewer-authorization
// invariants.
func NewResolutionDecision(input ResolutionDecisionInput) (ResolutionDecision, error) {
	id, err := requiredDecisionID(input.ID)
	if err != nil {
		return ResolutionDecision{}, err
	}
	proposalID, err := requiredProposalID(input.ProposalID)
	if err != nil {
		return ResolutionDecision{}, err
	}
	if input.Outcome != DecisionAccepted && input.Outcome != DecisionRejected {
		return ResolutionDecision{}, fmt.Errorf("resolution decision outcome is invalid")
	}
	entityID := EntityID(strings.TrimSpace(string(input.EntityID)))
	switch {
	case input.Outcome == DecisionAccepted && entityID == "":
		return ResolutionDecision{}, fmt.Errorf("accepted resolution decision requires an entity")
	case input.Outcome == DecisionRejected && entityID != "":
		return ResolutionDecision{}, fmt.Errorf("rejected resolution decision cannot name an entity")
	}
	if input.Authority != AuthorityAutomatic && input.Authority != AuthorityReviewer {
		return ResolutionDecision{}, fmt.Errorf("resolution decision authority is invalid")
	}
	reasonCode, err := reasoncode.Validate(input.ReasonCode)
	if err != nil {
		return ResolutionDecision{}, fmt.Errorf("resolution decision: %w", err)
	}
	if input.RecordedAt.IsZero() {
		return ResolutionDecision{}, fmt.Errorf("resolution decision recorded time is required")
	}
	supersedesID := DecisionID(strings.TrimSpace(string(input.SupersedesID)))
	if supersedesID == id {
		return ResolutionDecision{}, fmt.Errorf("resolution decision cannot supersede itself")
	}
	value := ResolutionDecision{
		id:           id,
		proposalID:   proposalID,
		outcome:      input.Outcome,
		entityID:     entityID,
		authority:    input.Authority,
		reasonCode:   reasonCode,
		recordedAt:   timepoint.Normalize(input.RecordedAt),
		supersedesID: supersedesID,
	}
	value.digest = digestResolutionDecision(value)
	return value, nil
}

func digestResolutionDecision(value ResolutionDecision) evidence.ContentDigest {
	encoder := canonicalhash.New(ResolutionDecisionDigestVersion)
	encoder.Time(value.recordedAt)
	encoder.String(string(value.proposalID))
	encoder.String(string(value.outcome))
	encoder.String(string(value.authority))
	encoder.String(value.reasonCode)
	encoder.String(string(value.entityID))
	encoder.String(string(value.supersedesID))
	return evidence.ContentDigest(encoder.Sum())
}

// ID returns the stable decision identifier.
func (value ResolutionDecision) ID() DecisionID { return value.id }

// ProposalID returns the proposal decided.
func (value ResolutionDecision) ProposalID() ProposalID { return value.proposalID }

// Outcome returns whether the proposal was accepted or rejected.
func (value ResolutionDecision) Outcome() DecisionOutcome { return value.outcome }

// EntityID returns the accepted entity, or empty for a rejection.
func (value ResolutionDecision) EntityID() EntityID { return value.entityID }

// Authority returns who or what made the decision.
func (value ResolutionDecision) Authority() DecisionAuthority { return value.authority }

// ReasonCode returns the exact non-secret decision reason code.
func (value ResolutionDecision) ReasonCode() string { return value.reasonCode }

// RecordedAt returns when Stacks recorded the decision.
func (value ResolutionDecision) RecordedAt() time.Time { return value.recordedAt }

// SupersedesID returns the preceding decision replaced by this decision.
func (value ResolutionDecision) SupersedesID() DecisionID { return value.supersedesID }

// Digest returns the canonical semantic decision digest.
func (value ResolutionDecision) Digest() evidence.ContentDigest { return value.digest }

// DigestVersion returns the encoding version for Digest.
func (value ResolutionDecision) DigestVersion() string {
	return ResolutionDecisionDigestVersion
}

// AliasAssertionInput contains an accepted alias and its owning decision.
type AliasAssertionInput struct {
	ID         AliasAssertionID
	DecisionID DecisionID
	EntityID   EntityID
	Alias      Alias
	RecordedAt time.Time
}

// AliasAssertion is an immutable accepted alias assertion.
type AliasAssertion struct {
	id         AliasAssertionID
	decisionID DecisionID
	entityID   EntityID
	alias      Alias
	recordedAt time.Time
}

// NewAliasAssertion validates and constructs an accepted alias assertion.
// Repository code verifies that DecisionID names the accepted decision being
// appended and that EntityID matches that decision.
func NewAliasAssertion(input AliasAssertionInput) (AliasAssertion, error) {
	id := AliasAssertionID(strings.TrimSpace(string(input.ID)))
	if id == "" {
		return AliasAssertion{}, fmt.Errorf("alias assertion ID is required")
	}
	decisionID, err := requiredDecisionID(input.DecisionID)
	if err != nil {
		return AliasAssertion{}, err
	}
	entityID, err := requiredEntityID(input.EntityID)
	if err != nil {
		return AliasAssertion{}, err
	}
	if input.Alias.Type != AliasTypeName && input.Alias.Type != AliasTypeEmail {
		return AliasAssertion{}, fmt.Errorf("alias assertion type is invalid")
	}
	if strings.TrimSpace(input.Alias.Value) == "" {
		return AliasAssertion{}, fmt.Errorf("alias assertion value is required")
	}
	if input.Alias.Type == AliasTypeEmail && !ValidEmail(input.Alias.Value) {
		return AliasAssertion{}, fmt.Errorf("alias assertion email is invalid")
	}
	if input.RecordedAt.IsZero() {
		return AliasAssertion{}, fmt.Errorf("alias assertion recorded time is required")
	}
	return AliasAssertion{
		id:         id,
		decisionID: decisionID,
		entityID:   entityID,
		alias:      input.Alias,
		recordedAt: timepoint.Normalize(input.RecordedAt),
	}, nil
}

// ID returns the stable assertion identifier.
func (value AliasAssertion) ID() AliasAssertionID { return value.id }

// DecisionID returns the accepted decision owning the assertion.
func (value AliasAssertion) DecisionID() DecisionID { return value.decisionID }

// EntityID returns the entity named by the accepted decision.
func (value AliasAssertion) EntityID() EntityID { return value.entityID }

// Alias returns the accepted alias.
func (value AliasAssertion) Alias() Alias { return value.alias }

// RecordedAt returns when Stacks recorded the assertion.
func (value AliasAssertion) RecordedAt() time.Time { return value.recordedAt }

func requiredEntityID(value EntityID) (EntityID, error) {
	result := EntityID(strings.TrimSpace(string(value)))
	if result == "" {
		return "", fmt.Errorf("entity ID is required")
	}
	return result, nil
}

func requiredMentionID(value MentionID) (MentionID, error) {
	result := MentionID(strings.TrimSpace(string(value)))
	if result == "" {
		return "", fmt.Errorf("mention ID is required")
	}
	return result, nil
}

func requiredProposalID(value ProposalID) (ProposalID, error) {
	result := ProposalID(strings.TrimSpace(string(value)))
	if result == "" {
		return "", fmt.Errorf("resolution proposal ID is required")
	}
	return result, nil
}

func requiredCandidateID(value CandidateID) (CandidateID, error) {
	result := CandidateID(strings.TrimSpace(string(value)))
	if result == "" {
		return "", fmt.Errorf("resolution candidate ID is required")
	}
	return result, nil
}

func requiredDecisionID(value DecisionID) (DecisionID, error) {
	result := DecisionID(strings.TrimSpace(string(value)))
	if result == "" {
		return "", fmt.Errorf("resolution decision ID is required")
	}
	return result, nil
}

func requiredEvidenceID(value evidence.EvidenceID, field string) (evidence.EvidenceID, error) {
	result := evidence.EvidenceID(strings.TrimSpace(string(value)))
	if result == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	return result, nil
}
