package temporal

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
)

// Fact is an aggregated state value with role-separated provenance.
type Fact struct {
	Key                      string
	Value                    string
	ObservationIDs           []observation.ObservationID
	SupportingEvidenceIDs    []evidence.EvidenceID
	ContradictingEvidenceIDs []evidence.EvidenceID
	LegacyUncited            bool
}

// UnresolvedReason explains why aggregation did not promote a fact to state.
type UnresolvedReason string

const (
	UnresolvedConflict            UnresolvedReason = "conflicting-values"
	UnresolvedTransition          UnresolvedReason = "multiple-states-in-window"
	UnresolvedTemporalUncertainty UnresolvedReason = "temporal-uncertainty"
	UnresolvedHypothesis          UnresolvedReason = "hypothesized"
	UnresolvedCounterevidenceOnly UnresolvedReason = "counterevidence-only"
	UnresolvedLegacyUncited       UnresolvedReason = "legacy-uncited"
)

// UnresolvedFact preserves candidate values and provenance that aggregation
// could not safely collapse into one state value.
type UnresolvedFact struct {
	Key        string
	Reason     UnresolvedReason
	Candidates []Fact
}

// WindowSummary is a pre-narration aggregate for one resolved selection.
type WindowSummary struct {
	Selection  TemporalSelection
	Facts      []Fact
	Unresolved []UnresolvedFact
}

// StateCandidate is an entity-resolved projection of an observation. Key and
// Value are supplied by the retrieval boundary.
type StateCandidate struct {
	Key         string
	Value       string
	Observation observation.Observation
}

type candidateIdentity struct {
	key         string
	value       string
	observation observation.Observation
}

type factAccumulator struct {
	key           string
	value         string
	observations  map[observation.ObservationID]struct{}
	supporting    map[evidence.EvidenceID]struct{}
	contradicting map[evidence.EvidenceID]struct{}
	extents       []observation.TemporalExtent
	legacyUncited bool
}

type keyAccumulator struct {
	supported        map[string]*factAccumulator
	tentative        map[string]*factAccumulator
	counterOnly      map[string]*factAccumulator
	legacyUncited    map[string]*factAccumulator
	temporalValues   map[string]struct{}
	hypothesisValues map[string]struct{}
}

// AggregateWindow selects eligible observations and constructs deterministic
// state for one valid-time window. Confidence is never used as a truth
// threshold or conflict tie-breaker.
func AggregateWindow(
	selection TemporalSelection,
	scope KnowledgeScope,
	candidates []StateCandidate,
) (WindowSummary, error) {
	if selection.kind != SelectionWindow {
		return WindowSummary{}, fmt.Errorf("state aggregation requires a window selection")
	}
	if scope.kind != KnowledgeCurrent && scope.kind != KnowledgeAsOf {
		return WindowSummary{}, fmt.Errorf("state aggregation knowledge scope is invalid")
	}

	groups := make(map[string]*keyAccumulator)
	seenObservations := make(map[observation.ObservationID]candidateIdentity)
	for _, candidate := range candidates {
		key := strings.TrimSpace(candidate.Key)
		value := strings.TrimSpace(candidate.Value)
		if key == "" || value == "" {
			return WindowSummary{}, fmt.Errorf("state candidate key and value are required")
		}

		observationID := candidate.Observation.ID()
		if strings.TrimSpace(string(observationID)) == "" {
			return WindowSummary{}, fmt.Errorf("state candidate observation is invalid")
		}
		identity := candidateIdentity{key: key, value: value, observation: candidate.Observation}
		if prior, exists := seenObservations[observationID]; exists {
			if prior.key != identity.key || prior.value != identity.value {
				return WindowSummary{}, fmt.Errorf("observation %q maps to conflicting state candidates", observationID)
			}
			if !observationsEqual(prior.observation, identity.observation) {
				return WindowSummary{}, fmt.Errorf("observation %q maps to conflicting canonical payloads", observationID)
			}
			continue
		}
		seenObservations[observationID] = identity

		if !recordedTimeEligible(scope, candidate.Observation) {
			continue
		}
		eligible, temporallyUncertain := validTimeEligibility(selection, candidate.Observation.ValidTime())
		if !eligible && !temporallyUncertain {
			continue
		}
		if candidate.Observation.Status() == observation.StatusRejected {
			continue
		}

		group := groups[key]
		if group == nil {
			group = &keyAccumulator{
				supported:        make(map[string]*factAccumulator),
				tentative:        make(map[string]*factAccumulator),
				counterOnly:      make(map[string]*factAccumulator),
				legacyUncited:    make(map[string]*factAccumulator),
				temporalValues:   make(map[string]struct{}),
				hypothesisValues: make(map[string]struct{}),
			}
			groups[key] = group
		}

		hasSupporting := observationHasSupportingEvidence(candidate.Observation)
		switch {
		case candidate.Observation.LegacyUncited():
			mergeCandidate(group.legacyUncited, key, value, candidate.Observation)
		case !hasSupporting:
			mergeCandidate(group.counterOnly, key, value, candidate.Observation)
		case temporallyUncertain || candidate.Observation.Status() == observation.StatusHypothesized:
			mergeCandidate(group.tentative, key, value, candidate.Observation)
			if temporallyUncertain {
				group.temporalValues[value] = struct{}{}
			} else {
				group.hypothesisValues[value] = struct{}{}
			}
		default:
			mergeCandidate(group.supported, key, value, candidate.Observation)
		}
	}

	return summarizeGroups(selection, groups), nil
}

func observationsEqual(left, right observation.Observation) bool {
	if left.ID() != right.ID() ||
		left.Statement() != right.Statement() ||
		!temporalExtentsEqual(left.ValidTime(), right.ValidTime()) ||
		!left.RecordedAt().Equal(right.RecordedAt()) ||
		!slices.Equal(left.EvidenceLinks(), right.EvidenceLinks()) ||
		left.Derivation() != right.Derivation() ||
		left.Status() != right.Status() ||
		left.LegacyUncited() != right.LegacyUncited() {
		return false
	}
	leftConfidence, leftHasConfidence := left.Confidence()
	rightConfidence, rightHasConfidence := right.Confidence()
	return leftHasConfidence == rightHasConfidence &&
		(!leftHasConfidence || leftConfidence == rightConfidence)
}

func temporalExtentsEqual(left, right observation.TemporalExtent) bool {
	if left.Kind() != right.Kind() {
		return false
	}
	if leftInstant, leftIsInstant := left.Instant(); leftIsInstant {
		rightInstant, rightIsInstant := right.Instant()
		return rightIsInstant && leftInstant.Equal(rightInstant)
	}
	leftStart, leftHasStart, leftEnd, leftHasEnd := left.Bounds()
	rightStart, rightHasStart, rightEnd, rightHasEnd := right.Bounds()
	return leftHasStart == rightHasStart &&
		leftHasEnd == rightHasEnd &&
		(!leftHasStart || leftStart.Equal(rightStart)) &&
		(!leftHasEnd || leftEnd.Equal(rightEnd))
}

func observationHasSupportingEvidence(value observation.Observation) bool {
	for _, link := range value.EvidenceLinks() {
		if link.Role == observation.EvidenceSupporting {
			return true
		}
	}
	return false
}

func recordedTimeEligible(scope KnowledgeScope, value observation.Observation) bool {
	cutoff, hasCutoff := scope.AsOf()
	return !hasCutoff || !value.RecordedAt().After(cutoff)
}

func validTimeEligibility(selection TemporalSelection, extent observation.TemporalExtent) (eligible, uncertain bool) {
	windowStart, windowEnd, _ := selection.Window()
	switch extent.Kind() {
	case observation.TemporalUnknown:
		return false, true
	case observation.TemporalInstant:
		instant, _ := extent.Instant()
		return !instant.Before(windowStart) && instant.Before(windowEnd), false
	case observation.TemporalInterval:
		start, hasStart, end, hasEnd := extent.Bounds()
		startsBeforeWindowEnds := !hasStart || start.Before(windowEnd)
		endsAfterWindowStarts := !hasEnd || end.After(windowStart)
		return startsBeforeWindowEnds && endsAfterWindowStarts, false
	case observation.TemporalWindow:
		start, _, end, _ := extent.Bounds()
		if !start.Before(windowEnd) || !end.After(windowStart) {
			return false, false
		}
		return false, true
	default:
		return false, true
	}
}

func mergeCandidate(
	target map[string]*factAccumulator,
	key string,
	value string,
	valueObservation observation.Observation,
) {
	accumulator := target[value]
	if accumulator == nil {
		accumulator = newFactAccumulator(key, value)
		target[value] = accumulator
	}
	accumulator.observations[valueObservation.ID()] = struct{}{}
	accumulator.extents = append(accumulator.extents, valueObservation.ValidTime())
	accumulator.legacyUncited = accumulator.legacyUncited || valueObservation.LegacyUncited()
	for _, link := range valueObservation.EvidenceLinks() {
		switch link.Role {
		case observation.EvidenceSupporting:
			accumulator.supporting[link.EvidenceID] = struct{}{}
		case observation.EvidenceContradicting:
			accumulator.contradicting[link.EvidenceID] = struct{}{}
		}
	}
}

func newFactAccumulator(key, value string) *factAccumulator {
	return &factAccumulator{
		key:           key,
		value:         value,
		observations:  make(map[observation.ObservationID]struct{}),
		supporting:    make(map[evidence.EvidenceID]struct{}),
		contradicting: make(map[evidence.EvidenceID]struct{}),
	}
}

func summarizeGroups(selection TemporalSelection, groups map[string]*keyAccumulator) WindowSummary {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	summary := WindowSummary{Selection: selection}
	for _, key := range keys {
		group := groups[key]
		supportedValues := sortedAccumulatorValues(group.supported)
		tentativeValues := sortedAccumulatorValues(group.tentative)
		counterOnlyValues := sortedAccumulatorValues(group.counterOnly)
		legacyUncitedValues := sortedAccumulatorValues(group.legacyUncited)

		if len(counterOnlyValues) == 0 &&
			len(legacyUncitedValues) == 0 &&
			len(supportedValues) == 1 &&
			noAlternativeValues(supportedValues[0], tentativeValues) {
			summary.Facts = append(summary.Facts, accumulatorFact(group.supported[supportedValues[0]]))
			continue
		}

		allCandidates := mergeAccumulators(group.supported, group.tentative, group.counterOnly, group.legacyUncited)
		if len(allCandidates) == 0 {
			continue
		}
		reason := unresolvedReason(group, supportedValues, tentativeValues, counterOnlyValues, legacyUncitedValues)
		summary.Unresolved = append(summary.Unresolved, UnresolvedFact{
			Key:        key,
			Reason:     reason,
			Candidates: orderedAccumulatorFacts(allCandidates),
		})
	}
	return summary
}

func noAlternativeValues(supported string, tentative []string) bool {
	for _, value := range tentative {
		if value != supported {
			return false
		}
	}
	return true
}

func unresolvedReason(
	group *keyAccumulator,
	supported []string,
	tentative []string,
	counterOnly []string,
	legacyUncited []string,
) UnresolvedReason {
	values := make(map[string]struct{}, len(supported)+len(tentative))
	for _, value := range supported {
		values[value] = struct{}{}
	}
	for _, value := range tentative {
		values[value] = struct{}{}
	}
	supportedSet := make(map[string]struct{}, len(supported))
	for _, value := range supported {
		supportedSet[value] = struct{}{}
	}
	if hasAlternative(group.temporalValues, supportedSet) {
		return UnresolvedTemporalUncertainty
	}
	if hasAlternative(group.hypothesisValues, supportedSet) {
		return UnresolvedHypothesis
	}
	if len(values) > 1 {
		if supportedValuesOverlap(group.supported) {
			return UnresolvedConflict
		}
		return UnresolvedTransition
	}
	if len(counterOnly) > 0 {
		return UnresolvedCounterevidenceOnly
	}
	if len(legacyUncited) > 0 {
		return UnresolvedLegacyUncited
	}
	return UnresolvedHypothesis
}

func hasAlternative(tentative, supported map[string]struct{}) bool {
	if len(tentative) == 0 {
		return false
	}
	if len(supported) == 0 {
		return true
	}
	for value := range tentative {
		if _, exists := supported[value]; !exists {
			return true
		}
	}
	return false
}

func supportedValuesOverlap(values map[string]*factAccumulator) bool {
	orderedValues := sortedAccumulatorValues(values)
	for left := 0; left < len(orderedValues); left++ {
		for right := left + 1; right < len(orderedValues); right++ {
			for _, leftExtent := range values[orderedValues[left]].extents {
				for _, rightExtent := range values[orderedValues[right]].extents {
					if temporalExtentsOverlap(leftExtent, rightExtent) {
						return true
					}
				}
			}
		}
	}
	return false
}

func temporalExtentsOverlap(left, right observation.TemporalExtent) bool {
	leftInstant, leftIsInstant := left.Instant()
	rightInstant, rightIsInstant := right.Instant()
	switch {
	case leftIsInstant && rightIsInstant:
		return leftInstant.Equal(rightInstant)
	case leftIsInstant:
		return intervalContains(right, leftInstant)
	case rightIsInstant:
		return intervalContains(left, rightInstant)
	}

	leftStart, leftHasStart, leftEnd, leftHasEnd := left.Bounds()
	rightStart, rightHasStart, rightEnd, rightHasEnd := right.Bounds()
	leftStartsBeforeRightEnds := !leftHasStart || !rightHasEnd || leftStart.Before(rightEnd)
	rightStartsBeforeLeftEnds := !rightHasStart || !leftHasEnd || rightStart.Before(leftEnd)
	return leftStartsBeforeRightEnds && rightStartsBeforeLeftEnds
}

func intervalContains(extent observation.TemporalExtent, instant time.Time) bool {
	start, hasStart, end, hasEnd := extent.Bounds()
	return (!hasStart || !instant.Before(start)) && (!hasEnd || instant.Before(end))
}

func sortedAccumulatorValues(values map[string]*factAccumulator) []string {
	ordered := make([]string, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Strings(ordered)
	return ordered
}

func mergeAccumulators(groups ...map[string]*factAccumulator) map[string]*factAccumulator {
	merged := make(map[string]*factAccumulator)
	for _, group := range groups {
		for value, source := range group {
			target := merged[value]
			if target == nil {
				target = newFactAccumulator(source.key, source.value)
				merged[value] = target
			}
			for observationID := range source.observations {
				target.observations[observationID] = struct{}{}
			}
			for evidenceID := range source.supporting {
				target.supporting[evidenceID] = struct{}{}
			}
			for evidenceID := range source.contradicting {
				target.contradicting[evidenceID] = struct{}{}
			}
			target.legacyUncited = target.legacyUncited || source.legacyUncited
		}
	}
	return merged
}

func orderedAccumulatorFacts(values map[string]*factAccumulator) []Fact {
	orderedValues := sortedAccumulatorValues(values)
	facts := make([]Fact, 0, len(orderedValues))
	for _, value := range orderedValues {
		facts = append(facts, accumulatorFact(values[value]))
	}
	return facts
}

func accumulatorFact(accumulator *factAccumulator) Fact {
	observationIDs := make([]observation.ObservationID, 0, len(accumulator.observations))
	for observationID := range accumulator.observations {
		observationIDs = append(observationIDs, observationID)
	}
	sort.Slice(observationIDs, func(left, right int) bool {
		return observationIDs[left] < observationIDs[right]
	})
	return Fact{
		Key:                      accumulator.key,
		Value:                    accumulator.value,
		ObservationIDs:           observationIDs,
		SupportingEvidenceIDs:    sortedEvidenceIDs(accumulator.supporting),
		ContradictingEvidenceIDs: sortedEvidenceIDs(accumulator.contradicting),
		LegacyUncited:            accumulator.legacyUncited,
	}
}

func sortedEvidenceIDs(values map[evidence.EvidenceID]struct{}) []evidence.EvidenceID {
	ids := make([]evidence.EvidenceID, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool {
		return ids[left] < ids[right]
	})
	return ids
}
