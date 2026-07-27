package temporal

import (
	"fmt"
	"sort"
	"time"

	"github.com/JakeFAU/stacks/core/observation"
)

// Transition is one deterministic change in resolved state at a canonical
// valid-time boundary. Unresolved carries material that prevented a state from
// being resolved on either side of that boundary.
type Transition struct {
	Kind       ChangeKind
	Key        StateKey
	ValidTime  observation.TemporalExtent
	Before     *Fact
	After      *Fact
	Unresolved []UnresolvedFact
}

// BuildTrajectory partitions one selected window only at its boundary and at
// eligible canonical observation boundaries, then diffs adjacent resolved
// state. It never chooses a value from unresolved material.
func BuildTrajectory(
	selection TemporalSelection,
	scope KnowledgeScope,
	candidates []StateCandidate,
) ([]Transition, error) {
	windowStart, windowEnd, ok := selection.Window()
	if !ok {
		return nil, fmt.Errorf("trajectory requires a window selection")
	}
	if scope.kind != KnowledgeCurrent && scope.kind != KnowledgeAsOf {
		return nil, fmt.Errorf("trajectory knowledge scope is invalid")
	}

	boundaries := map[time.Time]struct{}{
		windowStart: {},
		windowEnd:   {},
	}
	observations := make(map[observation.ObservationID]observation.Observation)
	for _, candidate := range candidates {
		if err := candidateMatchesObservation(candidate); err != nil {
			return nil, err
		}
		observationID := candidate.Observation.ID()
		if previous, exists := observations[observationID]; exists &&
			!observationsEqual(previous, candidate.Observation) {
			return nil, fmt.Errorf("observation maps to conflicting canonical payloads")
		}
		observations[observationID] = candidate.Observation
		if !recordedTimeEligible(scope, candidate.Observation) ||
			candidate.Observation.Status() == observation.StatusRejected {
			continue
		}
		addTrajectoryBoundaries(
			boundaries,
			windowStart,
			windowEnd,
			candidate.Observation.ValidTime(),
		)
	}

	orderedBoundaries := make([]time.Time, 0, len(boundaries))
	for boundary := range boundaries {
		orderedBoundaries = append(orderedBoundaries, boundary)
	}
	sort.Slice(orderedBoundaries, func(left, right int) bool {
		return orderedBoundaries[left].Before(orderedBoundaries[right])
	})

	groupsBeforeStart, err := aggregateCandidates(
		scope,
		candidates,
		func(extent observation.TemporalExtent) (bool, bool) {
			return validImmediatelyBefore(windowStart, extent)
		},
	)
	if err != nil {
		return nil, err
	}
	beforeStart := summarizeGroups(groupsBeforeStart)
	previous := WindowSummary{
		Selection:  selection,
		Facts:      beforeStart.facts,
		Unresolved: beforeStart.unresolved,
	}
	var transitions []Transition
	for index := 0; index+1 < len(orderedBoundaries); index++ {
		partition := TemporalSelection{
			kind:  SelectionWindow,
			label: selection.label,
			start: orderedBoundaries[index],
			end:   orderedBoundaries[index+1],
		}
		current, err := AggregateWindow(partition, scope, candidates)
		if err != nil {
			return nil, err
		}
		atBoundary, err := observation.AtTime(orderedBoundaries[index])
		if err != nil {
			return nil, fmt.Errorf("construct trajectory boundary: %w", err)
		}
		transitions = append(
			transitions,
			diffTrajectoryState(previous, current, atBoundary)...,
		)
		previous = current
	}

	orderCoreTransitions(transitions, observations)
	if transitions == nil {
		transitions = []Transition{}
	}
	return transitions, nil
}

func validImmediatelyBefore(
	boundary time.Time,
	extent observation.TemporalExtent,
) (eligible, uncertain bool) {
	switch extent.Kind() {
	case observation.TemporalUnknown:
		return false, true
	case observation.TemporalInstant:
		return false, false
	case observation.TemporalInterval, observation.TemporalWindow:
		start, hasStart, end, hasEnd := extent.Bounds()
		overlapsLeft := (!hasStart || start.Before(boundary)) &&
			(!hasEnd || !end.Before(boundary))
		if !overlapsLeft {
			return false, false
		}
		return extent.Kind() == observation.TemporalInterval,
			extent.Kind() == observation.TemporalWindow
	default:
		return false, true
	}
}

func addTrajectoryBoundaries(
	boundaries map[time.Time]struct{},
	windowStart, windowEnd time.Time,
	extent observation.TemporalExtent,
) {
	if instant, ok := extent.Instant(); ok {
		if !instant.Before(windowStart) && instant.Before(windowEnd) {
			boundaries[instant] = struct{}{}
		}
		return
	}
	start, hasStart, end, hasEnd := extent.Bounds()
	if hasStart && !start.Before(windowStart) && start.Before(windowEnd) {
		boundaries[start] = struct{}{}
	}
	if hasEnd && end.After(windowStart) && end.Before(windowEnd) {
		boundaries[end] = struct{}{}
	}
}

func diffTrajectoryState(
	before WindowSummary,
	after WindowSummary,
	validTime observation.TemporalExtent,
) []Transition {
	beforeFacts := factsByIdentity(before.Facts)
	afterFacts := factsByIdentity(after.Facts)
	beforeUnresolved := unresolvedByIdentity(before.Unresolved)
	afterUnresolved := unresolvedByIdentity(after.Unresolved)

	keys := make(map[stateKeyIdentity]StateKey, len(beforeFacts)+len(afterFacts))
	for identity, fact := range beforeFacts {
		keys[identity] = fact.Key
	}
	for identity, fact := range afterFacts {
		keys[identity] = fact.Key
	}
	orderedKeys := make([]StateKey, 0, len(keys))
	for _, key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Slice(orderedKeys, func(left, right int) bool {
		return CompareStateKeys(orderedKeys[left], orderedKeys[right]) < 0
	})

	transitions := make([]Transition, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		identity := identityForStateKey(key)
		beforeFact, inBefore := beforeFacts[identity]
		afterFact, inAfter := afterFacts[identity]
		unresolved := trajectoryUnresolved(
			beforeUnresolved[identity],
			afterUnresolved[identity],
		)
		switch {
		case !inBefore && inAfter:
			afterCopy := cloneFact(afterFact)
			transitions = append(transitions, Transition{
				Kind:       ChangeAdded,
				Key:        key,
				ValidTime:  validTime,
				After:      &afterCopy,
				Unresolved: unresolved,
			})
		case inBefore && !inAfter:
			beforeCopy := cloneFact(beforeFact)
			transitions = append(transitions, Transition{
				Kind:       ChangeRemoved,
				Key:        key,
				ValidTime:  validTime,
				Before:     &beforeCopy,
				Unresolved: unresolved,
			})
		case inBefore && inAfter &&
			CompareTerms(beforeFact.Value, afterFact.Value) != 0:
			beforeCopy, afterCopy := cloneFact(beforeFact), cloneFact(afterFact)
			transitions = append(transitions, Transition{
				Kind:       ChangeChanged,
				Key:        key,
				ValidTime:  validTime,
				Before:     &beforeCopy,
				After:      &afterCopy,
				Unresolved: unresolved,
			})
		}
	}
	return transitions
}

func factsByIdentity(values []Fact) map[stateKeyIdentity]Fact {
	result := make(map[stateKeyIdentity]Fact, len(values))
	for _, value := range values {
		result[identityForStateKey(value.Key)] = value
	}
	return result
}

func unresolvedByIdentity(values []UnresolvedFact) map[stateKeyIdentity]UnresolvedFact {
	result := make(map[stateKeyIdentity]UnresolvedFact, len(values))
	for _, value := range values {
		result[identityForStateKey(value.Key)] = value
	}
	return result
}

func trajectoryUnresolved(values ...UnresolvedFact) []UnresolvedFact {
	result := make([]UnresolvedFact, 0, len(values))
	for _, value := range values {
		if len(value.Candidates) == 0 {
			continue
		}
		value.Candidates = append([]Fact(nil), value.Candidates...)
		for index := range value.Candidates {
			value.Candidates[index] = cloneFact(value.Candidates[index])
		}
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		if compared := CompareStateKeys(result[left].Key, result[right].Key); compared != 0 {
			return compared < 0
		}
		return result[left].Reason < result[right].Reason
	})
	return result
}

func orderCoreTransitions(
	values []Transition,
	observations map[observation.ObservationID]observation.Observation,
) {
	sort.Slice(values, func(left, right int) bool {
		leftInstant, _ := values[left].ValidTime.Instant()
		rightInstant, _ := values[right].ValidTime.Instant()
		if !leftInstant.Equal(rightInstant) {
			return leftInstant.Before(rightInstant)
		}
		leftRecorded, leftID := coreTransitionTie(values[left], observations)
		rightRecorded, rightID := coreTransitionTie(values[right], observations)
		if !leftRecorded.Equal(rightRecorded) {
			return leftRecorded.Before(rightRecorded)
		}
		if leftID != rightID {
			return leftID < rightID
		}
		if compared := CompareStateKeys(values[left].Key, values[right].Key); compared != 0 {
			return compared < 0
		}
		return values[left].Kind < values[right].Kind
	})
}

func coreTransitionTie(
	value Transition,
	observations map[observation.ObservationID]observation.Observation,
) (time.Time, observation.ObservationID) {
	ids := transitionObservationIDs(value)
	var earliest time.Time
	for _, id := range ids {
		recordedAt := observations[id].RecordedAt()
		if earliest.IsZero() || recordedAt.Before(earliest) {
			earliest = recordedAt
		}
	}
	var first observation.ObservationID
	for _, id := range ids {
		if first == "" || id < first {
			first = id
		}
	}
	return earliest, first
}

func transitionObservationIDs(value Transition) []observation.ObservationID {
	var result []observation.ObservationID
	addFact := func(fact *Fact) {
		if fact != nil {
			result = append(result, fact.ObservationIDs...)
		}
	}
	addFact(value.Before)
	addFact(value.After)
	for _, item := range value.Unresolved {
		for index := range item.Candidates {
			addFact(&item.Candidates[index])
		}
	}
	return result
}
