package query

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"stacks/internal/knowledge"
)

// StateCandidate is an entity-resolved projection of an observation. Key and
// Value are supplied by the graph/retrieval boundary; aggregation does not
// perform entity resolution or reinterpret source text.
type StateCandidate struct {
	Key         string
	Value       string
	Observation knowledge.Observation
}

type candidateIdentity struct {
	key   string
	value string
}

type factAccumulator struct {
	key          string
	value        string
	observations map[knowledge.ObservationID]struct{}
	evidence     map[knowledge.EvidenceID]struct{}
	extents      []knowledge.TemporalExtent
}

type keyAccumulator struct {
	supported        map[string]*factAccumulator
	tentative        map[string]*factAccumulator
	temporalValues   map[string]struct{}
	hypothesisValues map[string]struct{}
}

// AggregateWindow selects eligible observations and constructs deterministic
// state for one valid-time window. Confidence is retained on observations but
// is never used as a truth threshold or conflict tie-breaker.
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
	seenObservations := make(map[knowledge.ObservationID]candidateIdentity)
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
		identity := candidateIdentity{key: key, value: value}
		if prior, exists := seenObservations[observationID]; exists {
			if prior != identity {
				return WindowSummary{}, fmt.Errorf("observation %q maps to conflicting state candidates", observationID)
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
		if candidate.Observation.Status() == knowledge.StatusRejected {
			continue
		}

		group := groups[key]
		if group == nil {
			group = &keyAccumulator{
				supported:        make(map[string]*factAccumulator),
				tentative:        make(map[string]*factAccumulator),
				temporalValues:   make(map[string]struct{}),
				hypothesisValues: make(map[string]struct{}),
			}
			groups[key] = group
		}

		tentative := temporallyUncertain || candidate.Observation.Status() == knowledge.StatusHypothesized
		target := group.supported
		if tentative {
			target = group.tentative
			if temporallyUncertain {
				group.temporalValues[value] = struct{}{}
			} else {
				group.hypothesisValues[value] = struct{}{}
			}
		}
		mergeCandidate(target, key, value, candidate.Observation)
	}

	return summarizeGroups(selection, groups), nil
}

func recordedTimeEligible(scope KnowledgeScope, observation knowledge.Observation) bool {
	cutoff, hasCutoff := scope.AsOf()
	return !hasCutoff || !observation.RecordedAt().After(cutoff)
}

func validTimeEligibility(selection TemporalSelection, extent knowledge.TemporalExtent) (eligible, uncertain bool) {
	windowStart, windowEnd, _ := selection.Window()
	switch extent.Kind() {
	case knowledge.TemporalUnknown:
		return false, true
	case knowledge.TemporalInstant:
		instant, _ := extent.Instant()
		return !instant.Before(windowStart) && instant.Before(windowEnd), false
	case knowledge.TemporalInterval:
		start, hasStart, end, hasEnd := extent.Bounds()
		startsBeforeWindowEnds := !hasStart || start.Before(windowEnd)
		endsAfterWindowStarts := !hasEnd || end.After(windowStart)
		return startsBeforeWindowEnds && endsAfterWindowStarts, false
	case knowledge.TemporalWindow:
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
	observation knowledge.Observation,
) {
	accumulator := target[value]
	if accumulator == nil {
		accumulator = &factAccumulator{
			key:          key,
			value:        value,
			observations: make(map[knowledge.ObservationID]struct{}),
			evidence:     make(map[knowledge.EvidenceID]struct{}),
		}
		target[value] = accumulator
	}
	accumulator.observations[observation.ID()] = struct{}{}
	accumulator.extents = append(accumulator.extents, observation.ValidTime())
	for _, evidenceID := range observation.EvidenceIDs() {
		accumulator.evidence[evidenceID] = struct{}{}
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

		if len(supportedValues) == 1 && noAlternativeValues(supportedValues[0], tentativeValues) {
			summary.Facts = append(summary.Facts, accumulatorFact(group.supported[supportedValues[0]]))
			continue
		}

		allCandidates := mergeAccumulators(group.supported, group.tentative)
		if len(allCandidates) == 0 {
			continue
		}
		reason := unresolvedReason(group, supportedValues, tentativeValues)
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

func unresolvedReason(group *keyAccumulator, supported, tentative []string) UnresolvedReason {
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
		switch {
		case supportedValuesOverlap(group.supported):
			return UnresolvedConflict
		default:
			return UnresolvedTransition
		}
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

func temporalExtentsOverlap(left, right knowledge.TemporalExtent) bool {
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

func intervalContains(extent knowledge.TemporalExtent, instant time.Time) bool {
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
				target = &factAccumulator{
					key:          source.key,
					value:        source.value,
					observations: make(map[knowledge.ObservationID]struct{}),
					evidence:     make(map[knowledge.EvidenceID]struct{}),
				}
				merged[value] = target
			}
			for observationID := range source.observations {
				target.observations[observationID] = struct{}{}
			}
			for evidenceID := range source.evidence {
				target.evidence[evidenceID] = struct{}{}
			}
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
	observationIDs := make([]knowledge.ObservationID, 0, len(accumulator.observations))
	for observationID := range accumulator.observations {
		observationIDs = append(observationIDs, observationID)
	}
	sort.Slice(observationIDs, func(left, right int) bool {
		return observationIDs[left] < observationIDs[right]
	})

	evidenceIDs := make([]knowledge.EvidenceID, 0, len(accumulator.evidence))
	for evidenceID := range accumulator.evidence {
		evidenceIDs = append(evidenceIDs, evidenceID)
	}
	sort.Slice(evidenceIDs, func(left, right int) bool {
		return evidenceIDs[left] < evidenceIDs[right]
	})

	return Fact{
		Key:            accumulator.key,
		Value:          accumulator.value,
		ObservationIDs: observationIDs,
		EvidenceIDs:    evidenceIDs,
	}
}
