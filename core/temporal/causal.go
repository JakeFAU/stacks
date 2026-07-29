package temporal

import (
	"fmt"
	"sort"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
)

// CausalPredicate is the sole provider-neutral causal predicate in v1.
const CausalPredicate observation.Predicate = "stacks.causal.v1/causes"

// CausalLink is one exact explicit causal statement with role-separated
// provenance. Multiple observations of the same typed cause and effect merge
// without selecting by confidence.
type CausalLink struct {
	Cause                    observation.Term
	Effect                   observation.Term
	ObservationIDs           []observation.ObservationID
	SupportingEvidenceIDs    []evidence.EvidenceID
	ContradictingEvidenceIDs []evidence.EvidenceID
}

type causalLinkIdentity struct {
	cause  termIdentity
	effect termIdentity
}

type causalLinkAccumulator struct {
	cause         observation.Term
	effect        observation.Term
	observations  map[observation.ObservationID]observation.Observation
	supporting    map[evidence.EvidenceID]struct{}
	contradicting map[evidence.EvidenceID]struct{}
}

// BuildCausalChain returns chronologically ordered explicit causal links
// selected by valid time and recorded-time scope. Each returned edge must be
// present as an admitted exact-predicate observation; temporal adjacency and
// typed-term similarity never synthesize another edge.
func BuildCausalChain(
	selection TemporalSelection,
	scope KnowledgeScope,
	candidates []StateCandidate,
) ([]CausalLink, error) {
	if selection.kind != SelectionWindow {
		return nil, fmt.Errorf("causal chain requires a window selection")
	}
	if scope.kind != KnowledgeCurrent && scope.kind != KnowledgeAsOf {
		return nil, fmt.Errorf("causal chain knowledge scope is invalid")
	}

	groups := make(map[causalLinkIdentity]*causalLinkAccumulator)
	seenObservations := make(map[observation.ObservationID]candidateIdentity)
	for _, candidate := range candidates {
		if err := candidateMatchesObservation(candidate); err != nil {
			return nil, err
		}
		if candidate.Key.Predicate != CausalPredicate {
			continue
		}

		observationID := candidate.Observation.ID()
		identity := candidateIdentity{
			key:         identityForStateKey(candidate.Key),
			value:       identityForTerm(candidate.Value),
			observation: candidate.Observation,
		}
		if prior, exists := seenObservations[observationID]; exists {
			if prior.key != identity.key || prior.value != identity.value ||
				!observationsEqual(prior.observation, identity.observation) {
				return nil, fmt.Errorf("causal observation maps to conflicting canonical payloads")
			}
			continue
		}
		seenObservations[observationID] = identity

		if !recordedTimeEligible(scope, candidate.Observation) {
			continue
		}
		eligible, _ := validTimeEligibility(selection, candidate.Observation.ValidTime())
		if !eligible {
			continue
		}

		linkIdentity := causalLinkIdentity{
			cause:  identityForTerm(candidate.Key.Subject),
			effect: identityForTerm(candidate.Value),
		}
		group := groups[linkIdentity]
		if group == nil {
			group = &causalLinkAccumulator{
				cause:         candidate.Key.Subject,
				effect:        candidate.Value,
				observations:  make(map[observation.ObservationID]observation.Observation),
				supporting:    make(map[evidence.EvidenceID]struct{}),
				contradicting: make(map[evidence.EvidenceID]struct{}),
			}
			groups[linkIdentity] = group
		}
		group.observations[observationID] = candidate.Observation
		for _, link := range candidate.Observation.EvidenceLinks() {
			switch link.Role {
			case observation.EvidenceSupporting:
				group.supporting[link.EvidenceID] = struct{}{}
			case observation.EvidenceContradicting:
				group.contradicting[link.EvidenceID] = struct{}{}
			}
		}
	}

	values := make([]causalLinkAccumulator, 0, len(groups))
	for _, group := range groups {
		values = append(values, *group)
	}
	sort.Slice(values, func(left, right int) bool {
		return compareCausalAccumulators(values[left], values[right]) < 0
	})

	result := make([]CausalLink, len(values))
	for index, value := range values {
		result[index] = causalAccumulatorLink(value)
	}
	return result, nil
}

func causalAccumulatorLink(value causalLinkAccumulator) CausalLink {
	observationIDs := make([]observation.ObservationID, 0, len(value.observations))
	for observationID := range value.observations {
		observationIDs = append(observationIDs, observationID)
	}
	sort.Slice(observationIDs, func(left, right int) bool {
		return observationIDs[left] < observationIDs[right]
	})
	supporting := sortedEvidenceIDs(value.supporting)
	contradicting := sortedEvidenceIDs(value.contradicting)
	return CausalLink{
		Cause:                    value.cause,
		Effect:                   value.effect,
		ObservationIDs:           observationIDs,
		SupportingEvidenceIDs:    supporting,
		ContradictingEvidenceIDs: contradicting,
	}
}

func compareCausalAccumulators(left, right causalLinkAccumulator) int {
	leftValid, leftRecorded, leftID := causalAccumulatorChronology(left)
	rightValid, rightRecorded, rightID := causalAccumulatorChronology(right)
	if result := compareCausalTime(leftValid, rightValid); result != 0 {
		return result
	}
	if result := compareCausalTime(leftRecorded, rightRecorded); result != 0 {
		return result
	}
	if leftID < rightID {
		return -1
	}
	if leftID > rightID {
		return 1
	}
	if result := CompareTerms(left.cause, right.cause); result != 0 {
		return result
	}
	return CompareTerms(left.effect, right.effect)
}

func causalAccumulatorChronology(value causalLinkAccumulator) (time.Time, time.Time, observation.ObservationID) {
	var earliestValid time.Time
	var earliestRecorded time.Time
	var earliestID observation.ObservationID
	for observationID, valueObservation := range value.observations {
		valid := causalExtentBound(valueObservation.ValidTime())
		if earliestValid.IsZero() || (!valid.IsZero() && valid.Before(earliestValid)) {
			earliestValid = valid
		}
		recorded := valueObservation.RecordedAt()
		if earliestRecorded.IsZero() || recorded.Before(earliestRecorded) {
			earliestRecorded = recorded
		}
		if earliestID == "" || observationID < earliestID {
			earliestID = observationID
		}
	}
	return earliestValid, earliestRecorded, earliestID
}

func causalExtentBound(value observation.TemporalExtent) time.Time {
	if instant, ok := value.Instant(); ok {
		return instant
	}
	start, hasStart, end, hasEnd := value.Bounds()
	if hasStart {
		return start
	}
	if hasEnd {
		return end
	}
	return time.Time{}
}

func compareCausalTime(left, right time.Time) int {
	if left.IsZero() != right.IsZero() {
		if left.IsZero() {
			return 1
		}
		return -1
	}
	if left.Before(right) {
		return -1
	}
	if left.After(right) {
		return 1
	}
	return 0
}
