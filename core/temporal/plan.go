// Package temporal provides deterministic temporal planning, aggregation, and
// comparison over canonical observations.
package temporal

import (
	"fmt"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/timepoint"
)

// Intent identifies the temporal question the retrieval layer must execute.
type Intent string

const (
	IntentPointInTime     Intent = "point-in-time"
	IntentTrendComparison Intent = "trend-comparison"
	IntentTrajectory      Intent = "trajectory"
	IntentCausalChain     Intent = "causal-chain"
)

// SelectionKind distinguishes a state boundary from an aggregation window.
type SelectionKind uint8

const (
	SelectionPoint SelectionKind = iota + 1
	SelectionWindow
)

// TemporalSelection is a resolved valid-time selection. Windows are half-open.
type TemporalSelection struct {
	kind  SelectionKind
	label string
	start time.Time
	end   time.Time
}

// At selects state at a specific valid-time boundary.
func At(label string, instant time.Time) (TemporalSelection, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return TemporalSelection{}, fmt.Errorf("temporal selection label is required")
	}
	if instant.IsZero() {
		return TemporalSelection{}, fmt.Errorf("temporal selection instant is required")
	}
	return TemporalSelection{kind: SelectionPoint, label: label, start: timepoint.Normalize(instant)}, nil
}

// Between selects the half-open valid-time window [start, end).
func Between(label string, start, end time.Time) (TemporalSelection, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return TemporalSelection{}, fmt.Errorf("temporal selection label is required")
	}
	if start.IsZero() || end.IsZero() {
		return TemporalSelection{}, fmt.Errorf("temporal selection window bounds are required")
	}
	start, end = timepoint.Normalize(start), timepoint.Normalize(end)
	if !end.After(start) {
		return TemporalSelection{}, fmt.Errorf("temporal selection window end must be after start")
	}
	return TemporalSelection{
		kind:  SelectionWindow,
		label: label,
		start: start,
		end:   end,
	}, nil
}

// Kind returns whether the selection is a point or window.
func (selection TemporalSelection) Kind() SelectionKind {
	return selection.kind
}

// Label returns the planner-provided human-readable label.
func (selection TemporalSelection) Label() string {
	return selection.label
}

// Point returns the selected instant when Kind is SelectionPoint.
func (selection TemporalSelection) Point() (time.Time, bool) {
	return selection.start, selection.kind == SelectionPoint
}

// Window returns the selected bounds when Kind is SelectionWindow.
func (selection TemporalSelection) Window() (time.Time, time.Time, bool) {
	return selection.start, selection.end, selection.kind == SelectionWindow
}

// KnowledgeScopeKind distinguishes current knowledge from a historical
// recorded-time cutoff.
type KnowledgeScopeKind uint8

const (
	KnowledgeCurrent KnowledgeScopeKind = iota
	KnowledgeAsOf
)

// KnowledgeScope controls which observations are eligible by recorded time.
type KnowledgeScope struct {
	kind KnowledgeScopeKind
	asOf time.Time
}

// CurrentKnowledge uses all observations currently known.
func CurrentKnowledge() KnowledgeScope {
	return KnowledgeScope{kind: KnowledgeCurrent}
}

// KnownAsOf excludes observations recorded after the supplied cutoff.
func KnownAsOf(cutoff time.Time) (KnowledgeScope, error) {
	if cutoff.IsZero() {
		return KnowledgeScope{}, fmt.Errorf("knowledge cutoff is required")
	}
	return KnowledgeScope{kind: KnowledgeAsOf, asOf: timepoint.Normalize(cutoff)}, nil
}

// Kind returns the recorded-time scope kind.
func (scope KnowledgeScope) Kind() KnowledgeScopeKind {
	return scope.kind
}

// AsOf returns the recorded-time cutoff when Kind is KnowledgeAsOf.
func (scope KnowledgeScope) AsOf() (time.Time, bool) {
	return scope.asOf, scope.kind == KnowledgeAsOf
}

// RetrievalOperation is a deterministic operation executed before narration.
type RetrievalOperation string

const (
	OperationReconstructState     RetrievalOperation = "reconstruct-state"
	OperationAggregateWindows     RetrievalOperation = "aggregate-windows"
	OperationDiffWindows          RetrievalOperation = "diff-windows"
	OperationPartitionTimeline    RetrievalOperation = "partition-timeline"
	OperationOrderTransitions     RetrievalOperation = "order-transitions"
	OperationRetrieveCausalClaims RetrievalOperation = "retrieve-explicit-causal-claims"
	OperationOrderCausalClaims    RetrievalOperation = "order-causal-claims"
)

// PlanInput contains classified and resolved temporal query values.
type PlanInput struct {
	Intent         Intent
	EntityIDs      []string
	Selections     []TemporalSelection
	KnowledgeScope KnowledgeScope
}

// Plan is the validated contract consumed by temporal retrieval.
type Plan struct {
	intent         Intent
	entityIDs      []string
	selections     []TemporalSelection
	knowledgeScope KnowledgeScope
	operations     []RetrievalOperation
}

// NewPlan validates classified intent and resolved temporal selections.
func NewPlan(input PlanInput) (Plan, error) {
	if !input.Intent.valid() {
		return Plan{}, fmt.Errorf("query intent is invalid")
	}
	entityIDs, err := normalizeUniqueStrings("query entity ID", input.EntityIDs)
	if err != nil {
		return Plan{}, err
	}
	if err := validateSelections(input.Intent, input.Selections); err != nil {
		return Plan{}, err
	}
	if input.KnowledgeScope.kind != KnowledgeCurrent && input.KnowledgeScope.kind != KnowledgeAsOf {
		return Plan{}, fmt.Errorf("query knowledge scope is invalid")
	}
	return Plan{
		intent:         input.Intent,
		entityIDs:      entityIDs,
		selections:     append([]TemporalSelection(nil), input.Selections...),
		knowledgeScope: input.KnowledgeScope,
		operations:     operationsFor(input.Intent),
	}, nil
}

func (intent Intent) valid() bool {
	switch intent {
	case IntentPointInTime, IntentTrendComparison, IntentTrajectory, IntentCausalChain:
		return true
	default:
		return false
	}
}

func validateSelections(intent Intent, selections []TemporalSelection) error {
	wantCount := 1
	wantKind := SelectionWindow
	if intent == IntentPointInTime {
		wantKind = SelectionPoint
	}
	if intent == IntentTrendComparison {
		wantCount = 2
	}
	if len(selections) != wantCount {
		return fmt.Errorf("query intent %q requires %d temporal selections", intent, wantCount)
	}
	if intent == IntentTrendComparison && !selections[1].start.After(selections[0].start) {
		return fmt.Errorf("trend comparison windows must be ordered by start time")
	}
	seenLabels := make(map[string]struct{}, len(selections))
	for _, selection := range selections {
		if selection.kind != wantKind {
			return fmt.Errorf("query intent %q requires temporal selection kind %d", intent, wantKind)
		}
		if _, exists := seenLabels[selection.label]; exists {
			return fmt.Errorf("query temporal selection labels must be unique")
		}
		seenLabels[selection.label] = struct{}{}
	}
	return nil
}

func operationsFor(intent Intent) []RetrievalOperation {
	switch intent {
	case IntentPointInTime:
		return []RetrievalOperation{OperationReconstructState}
	case IntentTrendComparison:
		return []RetrievalOperation{OperationAggregateWindows, OperationDiffWindows}
	case IntentTrajectory:
		return []RetrievalOperation{
			OperationPartitionTimeline,
			OperationAggregateWindows,
			OperationDiffWindows,
			OperationOrderTransitions,
		}
	case IntentCausalChain:
		return []RetrievalOperation{OperationRetrieveCausalClaims, OperationOrderCausalClaims}
	default:
		return nil
	}
}

func normalizeUniqueStrings(name string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%s is required", name)
	}
	normalized := make([]string, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("%ss must be unique", name)
		}
		seen[value] = struct{}{}
		normalized[index] = value
	}
	return normalized, nil
}

// Intent returns the classified temporal intent.
func (plan Plan) Intent() Intent {
	return plan.intent
}

// EntityIDs returns a defensive copy of resolved entity identifiers.
func (plan Plan) EntityIDs() []string {
	return append([]string(nil), plan.entityIDs...)
}

// Selections returns a defensive copy of resolved valid-time selections.
func (plan Plan) Selections() []TemporalSelection {
	return append([]TemporalSelection(nil), plan.selections...)
}

// KnowledgeScope returns the recorded-time eligibility rule.
func (plan Plan) KnowledgeScope() KnowledgeScope {
	return plan.knowledgeScope
}

// Operations returns the ordered retrieval operations required before narration.
func (plan Plan) Operations() []RetrievalOperation {
	return append([]RetrievalOperation(nil), plan.operations...)
}
