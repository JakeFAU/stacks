package query

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
	"github.com/JakeFAU/stacks/core/timepoint"
)

// GapKind describes a successful-query evidence gap.
type GapKind string

const (
	GapNoEvidence        GapKind = "no-evidence"
	GapValidTimeExcluded GapKind = "valid-time-excluded"
	GapUnresolvedMention GapKind = "unresolved-mention"
	GapAuthorityExcluded GapKind = "authority-excluded"
	GapNoCausalEvidence  GapKind = "no-causal-evidence"
)

// Gap is bounded contextual information about absent qualifying material.
type Gap struct {
	Kind           GapKind
	EntityID       identity.EntityID
	Predicate      observation.Predicate
	SelectionLabel string
}

// Contribution preserves the observation-level provenance of one fact.
type Contribution struct {
	ObservationID             observation.ObservationID
	Status                    observation.EpistemicStatus
	ValidTime                 observation.TemporalExtent
	RecordedAt                time.Time
	Derivation                observation.Derivation
	SubjectGroundingMentionID string
	ObjectGroundingMentionID  string
}

// Citation is immutable exact evidence metadata supplied in the snapshot.
type Citation struct {
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

// Fact is one resolved state value with role-separated citations.
type Fact struct {
	Key                    temporal.StateKey
	Value                  observation.Term
	Contributions          []Contribution
	SupportingCitations    []Citation
	ContradictingCitations []Citation
}

// UnresolvedItem preserves competing cited candidates for a state key.
type UnresolvedItem struct {
	Key        temporal.StateKey
	Reason     temporal.UnresolvedReason
	Candidates []Fact
}

// WindowResult is the resolved and unresolved material for one window.
type WindowResult struct {
	Selection  temporal.TemporalSelection
	Facts      []Fact
	Unresolved []UnresolvedItem
}

// Change is a deterministic state difference between trend windows.
type Change struct {
	Kind   temporal.ChangeKind
	Key    temporal.StateKey
	Before *Fact
	After  *Fact
}

// Transition is one bounded trajectory state transition.
type Transition struct {
	Kind       temporal.ChangeKind
	Key        temporal.StateKey
	ValidTime  observation.TemporalExtent
	Before     *Fact
	After      *Fact
	Unresolved []UnresolvedItem
}

// CausalLink is one explicit causal observation with cited provenance.
type CausalLink struct {
	Cause                  observation.Term
	Effect                 observation.Term
	Contributions          []Contribution
	SupportingCitations    []Citation
	ContradictingCitations []Citation
}

// PointInTimeResult contains reconstructed state at one selected instant.
type PointInTimeResult struct {
	Selection  temporal.TemporalSelection
	Facts      []Fact
	Unresolved []UnresolvedItem
}

// TrendResult contains two window summaries and their deterministic diff.
type TrendResult struct {
	Before         WindowResult
	After          WindowResult
	Changes        []Change
	UnresolvedKeys []temporal.StateKey
}

// TrajectoryResult contains chronologically ordered state transitions.
type TrajectoryResult struct {
	Selection   temporal.TemporalSelection
	Transitions []Transition
}

// CausalChainResult contains chronologically ordered explicit causal links.
type CausalChainResult struct {
	Selection temporal.TemporalSelection
	Links     []CausalLink
}

// IntentPayload is a closed result union. Its fields stay private so callers
// cannot construct a mismatched tag/member pair through the public API.
type IntentPayload struct {
	intent     temporal.Intent
	point      *PointInTimeResult
	trend      *TrendResult
	trajectory *TrajectoryResult
	causal     *CausalChainResult
}

// Result is the renderer-neutral output of one temporal query.
type Result struct {
	Intent         temporal.Intent
	EntityIDs      []identity.EntityID
	EntityMatch    EntityMatch
	Predicates     []observation.Predicate
	Selections     []temporal.TemporalSelection
	KnowledgeScope temporal.KnowledgeScope
	Limit          int
	Payload        IntentPayload
	Gaps           []Gap
}

// NewPointPayload constructs a point-in-time payload with canonical ordering.
func NewPointPayload(value PointInTimeResult) (IntentPayload, error) {
	value = clonePointResult(value)
	if err := validatePointCitations(value); err != nil {
		return IntentPayload{}, err
	}
	if err := normalizePointResult(&value); err != nil {
		return IntentPayload{}, err
	}
	return IntentPayload{intent: temporal.IntentPointInTime, point: &value}, nil
}

// NewTrendPayload constructs a trend-comparison payload with canonical ordering.
func NewTrendPayload(value TrendResult) (IntentPayload, error) {
	value = cloneTrendResult(value)
	if err := validateTrendCitations(value); err != nil {
		return IntentPayload{}, err
	}
	if err := normalizeTrendResult(&value); err != nil {
		return IntentPayload{}, err
	}
	return IntentPayload{intent: temporal.IntentTrendComparison, trend: &value}, nil
}

// NewTrajectoryPayload constructs a trajectory payload with canonical ordering.
func NewTrajectoryPayload(value TrajectoryResult) (IntentPayload, error) {
	value = cloneTrajectoryResult(value)
	if err := validateTrajectoryCitations(value); err != nil {
		return IntentPayload{}, err
	}
	if err := normalizeTrajectoryResult(&value); err != nil {
		return IntentPayload{}, err
	}
	return IntentPayload{intent: temporal.IntentTrajectory, trajectory: &value}, nil
}

// NewCausalPayload constructs a causal-chain payload with canonical ordering.
func NewCausalPayload(value CausalChainResult) (IntentPayload, error) {
	value = cloneCausalResult(value)
	if err := validateCausalCitations(value); err != nil {
		return IntentPayload{}, err
	}
	if err := normalizeCausalResult(&value); err != nil {
		return IntentPayload{}, err
	}
	return IntentPayload{intent: temporal.IntentCausalChain, causal: &value}, nil
}

// Intent returns the union tag.
func (payload IntentPayload) Intent() temporal.Intent { return payload.intent }
func (payload IntentPayload) Point() (PointInTimeResult, bool) {
	if payload.point == nil {
		return PointInTimeResult{}, false
	}
	return clonePointResult(*payload.point), true
}
func (payload IntentPayload) Trend() (TrendResult, bool) {
	if payload.trend == nil {
		return TrendResult{}, false
	}
	return cloneTrendResult(*payload.trend), true
}
func (payload IntentPayload) Trajectory() (TrajectoryResult, bool) {
	if payload.trajectory == nil {
		return TrajectoryResult{}, false
	}
	return cloneTrajectoryResult(*payload.trajectory), true
}
func (payload IntentPayload) Causal() (CausalChainResult, bool) {
	if payload.causal == nil {
		return CausalChainResult{}, false
	}
	return cloneCausalResult(*payload.causal), true
}

// NormalizeResult validates and defensively copies one complete result.
func NormalizeResult(result Result) (Result, error) {
	limits := Limits{MaxEntities: len(result.EntityIDs), MaxPredicates: max(1, len(result.Predicates)), MaxChronology: max(1, result.Limit)}
	normalized, err := NormalizeRequest(Request{Intent: result.Intent, EntityIDs: result.EntityIDs, EntityMatch: result.EntityMatch, Predicates: result.Predicates, Selections: result.Selections, KnowledgeScope: result.KnowledgeScope, Limit: result.Limit}, limits)
	if err != nil {
		return Result{}, err
	}
	result.Intent, result.EntityIDs, result.EntityMatch, result.Predicates, result.Selections, result.KnowledgeScope, result.Limit = normalized.Intent, normalized.EntityIDs, normalized.EntityMatch, normalized.Predicates, normalized.Selections, normalized.KnowledgeScope, normalized.Limit
	payload, err := normalizePayload(result.Payload)
	if err != nil {
		return Result{}, err
	}
	if payload.intent != result.Intent {
		return Result{}, fmt.Errorf("result payload intent does not match result intent")
	}
	if selections, err := payloadSelections(payload); err != nil || !slices.Equal(result.Selections, selections) {
		return Result{}, fmt.Errorf("result selections do not match payload selections")
	}
	result.Payload = payload
	result.Gaps = append([]Gap{}, result.Gaps...)
	for index := range result.Gaps {
		if !validGapKind(result.Gaps[index].Kind) {
			return Result{}, fmt.Errorf("result gap kind is invalid")
		}
	}
	orderGaps(result.Gaps)
	return result, nil
}

// ValidateResult reports whether a result is complete, valid, and canonical.
func ValidateResult(result Result) error {
	normalized, err := NormalizeResult(result)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(result, normalized) {
		return fmt.Errorf("result collections must be non-nil and canonically ordered")
	}
	return nil
}

func normalizePayload(payload IntentPayload) (IntentPayload, error) {
	count := 0
	if payload.point != nil {
		count++
	}
	if payload.trend != nil {
		count++
	}
	if payload.trajectory != nil {
		count++
	}
	if payload.causal != nil {
		count++
	}
	if count != 1 {
		return IntentPayload{}, fmt.Errorf("result payload must contain exactly one intent shape")
	}
	switch payload.intent {
	case temporal.IntentPointInTime:
		if payload.point == nil {
			return IntentPayload{}, fmt.Errorf("result point payload is missing")
		}
		return NewPointPayload(*payload.point)
	case temporal.IntentTrendComparison:
		if payload.trend == nil {
			return IntentPayload{}, fmt.Errorf("result trend payload is missing")
		}
		return NewTrendPayload(*payload.trend)
	case temporal.IntentTrajectory:
		if payload.trajectory == nil {
			return IntentPayload{}, fmt.Errorf("result trajectory payload is missing")
		}
		return NewTrajectoryPayload(*payload.trajectory)
	case temporal.IntentCausalChain:
		if payload.causal == nil {
			return IntentPayload{}, fmt.Errorf("result causal payload is missing")
		}
		return NewCausalPayload(*payload.causal)
	default:
		return IntentPayload{}, fmt.Errorf("result payload intent is invalid")
	}
}

func normalizePointResult(value *PointInTimeResult) error {
	if value.Selection.Kind() != temporal.SelectionPoint {
		return fmt.Errorf("point payload requires a point selection")
	}
	value.Facts = normalizeFacts(value.Facts)
	value.Unresolved = normalizeUnresolved(value.Unresolved)
	return nil
}
func normalizeTrendResult(value *TrendResult) error {
	if value.Before.Selection.Kind() != temporal.SelectionWindow || value.After.Selection.Kind() != temporal.SelectionWindow {
		return fmt.Errorf("trend payload requires window selections")
	}
	value.Before = normalizeWindow(value.Before)
	value.After = normalizeWindow(value.After)
	changes, err := normalizeChanges(value.Changes)
	if err != nil {
		return err
	}
	value.Changes = changes
	value.UnresolvedKeys = append([]temporal.StateKey{}, value.UnresolvedKeys...)
	orderStateKeys(value.UnresolvedKeys)
	return nil
}
func normalizeTrajectoryResult(value *TrajectoryResult) error {
	if value.Selection.Kind() != temporal.SelectionWindow {
		return fmt.Errorf("trajectory payload requires a window selection")
	}
	value.Transitions = append([]Transition{}, value.Transitions...)
	for index := range value.Transitions {
		if err := normalizeTransition(&value.Transitions[index]); err != nil {
			return err
		}
	}
	orderTransitions(value.Transitions)
	return nil
}
func normalizeCausalResult(value *CausalChainResult) error {
	if value.Selection.Kind() != temporal.SelectionWindow {
		return fmt.Errorf("causal payload requires a window selection")
	}
	value.Links = append([]CausalLink{}, value.Links...)
	for index := range value.Links {
		value.Links[index] = normalizeCausalLink(value.Links[index])
	}
	orderCausalLinks(value.Links)
	return nil
}
func normalizeWindow(value WindowResult) WindowResult {
	value.Facts = normalizeFacts(value.Facts)
	value.Unresolved = normalizeUnresolved(value.Unresolved)
	return value
}
func normalizeFacts(values []Fact) []Fact {
	values = append([]Fact{}, values...)
	for index := range values {
		values[index] = normalizeFact(values[index])
	}
	orderFacts(values)
	return values
}
func normalizeUnresolved(values []UnresolvedItem) []UnresolvedItem {
	values = append([]UnresolvedItem{}, values...)
	for index := range values {
		values[index].Candidates = normalizeFacts(values[index].Candidates)
	}
	orderUnresolvedItems(values)
	return values
}
func normalizeChanges(values []Change) ([]Change, error) {
	values = append([]Change{}, values...)
	for index := range values {
		if err := validateChangeShape(values[index].Kind, values[index].Before, values[index].After); err != nil {
			return nil, err
		}
		if values[index].Before != nil {
			value := normalizeFact(*values[index].Before)
			values[index].Before = &value
		}
		if values[index].After != nil {
			value := normalizeFact(*values[index].After)
			values[index].After = &value
		}
	}
	orderChanges(values)
	return values, nil
}
func normalizeTransition(value *Transition) error {
	if err := validateChangeShape(value.Kind, value.Before, value.After); err != nil {
		return err
	}
	if value.Before != nil {
		fact := normalizeFact(*value.Before)
		value.Before = &fact
	}
	if value.After != nil {
		fact := normalizeFact(*value.After)
		value.After = &fact
	}
	value.Unresolved = normalizeUnresolved(value.Unresolved)
	return nil
}
func normalizeCausalLink(value CausalLink) CausalLink {
	value.Contributions = normalizeContributions(value.Contributions)
	value.SupportingCitations = normalizeCitations(value.SupportingCitations)
	value.ContradictingCitations = normalizeCitations(value.ContradictingCitations)
	return value
}
func normalizeFact(value Fact) Fact {
	value.Contributions = normalizeContributions(value.Contributions)
	value.SupportingCitations = normalizeCitations(value.SupportingCitations)
	value.ContradictingCitations = normalizeCitations(value.ContradictingCitations)
	return value
}
func normalizeContributions(values []Contribution) []Contribution {
	values = append([]Contribution{}, values...)
	for index := range values {
		values[index].RecordedAt = timepoint.Normalize(values[index].RecordedAt)
	}
	orderContributions(values)
	return values
}
func normalizeCitations(values []Citation) []Citation {
	values = append([]Citation{}, values...)
	for index := range values {
		values[index].SectionPath = append([]string{}, values[index].SectionPath...)
	}
	orderCitations(values)
	return values
}

func validGapKind(value GapKind) bool {
	switch value {
	case GapNoEvidence, GapValidTimeExcluded, GapUnresolvedMention, GapAuthorityExcluded, GapNoCausalEvidence:
		return true
	default:
		return false
	}
}
func validateChangeShape(kind temporal.ChangeKind, before, after *Fact) error {
	switch kind {
	case temporal.ChangeAdded:
		if before != nil || after == nil {
			return fmt.Errorf("added change requires only an after fact")
		}
	case temporal.ChangeRemoved:
		if before == nil || after != nil {
			return fmt.Errorf("removed change requires only a before fact")
		}
	case temporal.ChangeChanged:
		if before == nil || after == nil {
			return fmt.Errorf("changed change requires before and after facts")
		}
	default:
		return fmt.Errorf("change kind is invalid")
	}
	return nil
}

func payloadSelections(payload IntentPayload) ([]temporal.TemporalSelection, error) {
	switch payload.intent {
	case temporal.IntentPointInTime:
		if payload.point == nil {
			return nil, fmt.Errorf("point payload is missing")
		}
		return []temporal.TemporalSelection{payload.point.Selection}, nil
	case temporal.IntentTrendComparison:
		if payload.trend == nil {
			return nil, fmt.Errorf("trend payload is missing")
		}
		return []temporal.TemporalSelection{payload.trend.Before.Selection, payload.trend.After.Selection}, nil
	case temporal.IntentTrajectory:
		if payload.trajectory == nil {
			return nil, fmt.Errorf("trajectory payload is missing")
		}
		return []temporal.TemporalSelection{payload.trajectory.Selection}, nil
	case temporal.IntentCausalChain:
		if payload.causal == nil {
			return nil, fmt.Errorf("causal payload is missing")
		}
		return []temporal.TemporalSelection{payload.causal.Selection}, nil
	default:
		return nil, fmt.Errorf("payload intent is invalid")
	}
}

func clonePointResult(value PointInTimeResult) PointInTimeResult {
	value.Facts = cloneFacts(value.Facts)
	value.Unresolved = cloneUnresolved(value.Unresolved)
	return value
}
func cloneTrendResult(value TrendResult) TrendResult {
	value.Before = cloneWindow(value.Before)
	value.After = cloneWindow(value.After)
	value.Changes = cloneChanges(value.Changes)
	value.UnresolvedKeys = append([]temporal.StateKey{}, value.UnresolvedKeys...)
	return value
}
func cloneTrajectoryResult(value TrajectoryResult) TrajectoryResult {
	value.Transitions = append([]Transition{}, value.Transitions...)
	for index := range value.Transitions {
		value.Transitions[index].Before = cloneFactPointer(value.Transitions[index].Before)
		value.Transitions[index].After = cloneFactPointer(value.Transitions[index].After)
		value.Transitions[index].Unresolved = cloneUnresolved(value.Transitions[index].Unresolved)
	}
	return value
}
func cloneCausalResult(value CausalChainResult) CausalChainResult {
	value.Links = append([]CausalLink{}, value.Links...)
	for index := range value.Links {
		value.Links[index] = cloneCausalLink(value.Links[index])
	}
	return value
}
func cloneWindow(value WindowResult) WindowResult {
	value.Facts = cloneFacts(value.Facts)
	value.Unresolved = cloneUnresolved(value.Unresolved)
	return value
}
func cloneFacts(values []Fact) []Fact {
	result := append([]Fact{}, values...)
	for index := range result {
		result[index] = cloneFact(result[index])
	}
	return result
}
func cloneFact(value Fact) Fact {
	value.Contributions = append([]Contribution{}, value.Contributions...)
	value.SupportingCitations = cloneCitations(value.SupportingCitations)
	value.ContradictingCitations = cloneCitations(value.ContradictingCitations)
	return value
}
func cloneCitations(values []Citation) []Citation {
	result := append([]Citation{}, values...)
	for index := range result {
		result[index].SectionPath = append([]string{}, result[index].SectionPath...)
	}
	return result
}
func cloneUnresolved(values []UnresolvedItem) []UnresolvedItem {
	result := append([]UnresolvedItem{}, values...)
	for index := range result {
		result[index].Candidates = cloneFacts(result[index].Candidates)
	}
	return result
}
func cloneChanges(values []Change) []Change {
	result := append([]Change{}, values...)
	for index := range result {
		result[index].Before = cloneFactPointer(result[index].Before)
		result[index].After = cloneFactPointer(result[index].After)
	}
	return result
}
func cloneFactPointer(value *Fact) *Fact {
	if value == nil {
		return nil
	}
	result := cloneFact(*value)
	return &result
}
func cloneCausalLink(value CausalLink) CausalLink {
	value.Contributions = append([]Contribution{}, value.Contributions...)
	value.SupportingCitations = cloneCitations(value.SupportingCitations)
	value.ContradictingCitations = cloneCitations(value.ContradictingCitations)
	return value
}

func validateCitation(value Citation) error {
	if strings.TrimSpace(string(value.EvidenceID)) == "" || !validEvidenceRole(value.Role) || strings.TrimSpace(value.SourceDocumentID) == "" || strings.TrimSpace(value.DocumentVersionID) == "" || strings.TrimSpace(value.SectionID) == "" || strings.TrimSpace(value.SectionTitle) == "" || value.SectionOrder < 0 || strings.TrimSpace(value.SectionRole) == "" || value.StartOffset < 0 || value.EndOffset <= value.StartOffset {
		return fmt.Errorf("citation is invalid")
	}
	for _, title := range value.SectionPath {
		if strings.TrimSpace(title) == "" {
			return fmt.Errorf("citation section path is invalid")
		}
	}
	return nil
}
func validEvidenceRole(value observation.EvidenceRole) bool {
	return value == observation.EvidenceSupporting || value == observation.EvidenceContradicting
}

func validatePointCitations(value PointInTimeResult) error {
	return validateFactsCitations(value.Facts, value.Unresolved)
}

func validateTrendCitations(value TrendResult) error {
	if err := validateFactsCitations(value.Before.Facts, value.Before.Unresolved); err != nil {
		return err
	}
	if err := validateFactsCitations(value.After.Facts, value.After.Unresolved); err != nil {
		return err
	}
	for _, change := range value.Changes {
		if err := validateFactPointerCitations(change.Before); err != nil {
			return err
		}
		if err := validateFactPointerCitations(change.After); err != nil {
			return err
		}
	}
	return nil
}

func validateTrajectoryCitations(value TrajectoryResult) error {
	for _, transition := range value.Transitions {
		if err := validateFactPointerCitations(transition.Before); err != nil {
			return err
		}
		if err := validateFactPointerCitations(transition.After); err != nil {
			return err
		}
		if err := validateFactsCitations(nil, transition.Unresolved); err != nil {
			return err
		}
	}
	return nil
}

func validateCausalCitations(value CausalChainResult) error {
	for _, link := range value.Links {
		if err := validateRoleCitations(link.SupportingCitations, observation.EvidenceSupporting); err != nil {
			return err
		}
		if err := validateRoleCitations(link.ContradictingCitations, observation.EvidenceContradicting); err != nil {
			return err
		}
	}
	return nil
}

func validateRoleCitations(citations []Citation, role observation.EvidenceRole) error {
	for _, citation := range citations {
		if citation.Role != role {
			return fmt.Errorf("citation role does not match its result collection")
		}
		if err := validateCitation(citation); err != nil {
			return err
		}
	}
	return nil
}

func validateFactsCitations(facts []Fact, unresolved []UnresolvedItem) error {
	for _, fact := range facts {
		if err := validateFactCitations(fact); err != nil {
			return err
		}
	}
	for _, item := range unresolved {
		if err := validateFactsCitations(item.Candidates, nil); err != nil {
			return err
		}
	}
	return nil
}

func validateFactPointerCitations(value *Fact) error {
	if value == nil {
		return nil
	}
	return validateFactCitations(*value)
}

func validateFactCitations(value Fact) error {
	if err := validateRoleCitations(value.SupportingCitations, observation.EvidenceSupporting); err != nil {
		return err
	}
	return validateRoleCitations(value.ContradictingCitations, observation.EvidenceContradicting)
}
