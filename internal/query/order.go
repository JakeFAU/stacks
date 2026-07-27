package query

import (
	"cmp"
	"sort"
	"time"

	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

func orderEntityIDs(values []identity.EntityID) {
	sort.Slice(values, func(left, right int) bool { return values[left] < values[right] })
}
func orderPredicates(values []observation.Predicate) {
	sort.Slice(values, func(left, right int) bool { return values[left] < values[right] })
}

func compareFacts(left, right Fact) int {
	if result := temporal.CompareStateKeys(left.Key, right.Key); result != 0 {
		return result
	}
	return temporal.CompareTerms(left.Value, right.Value)
}

func orderFacts(values []Fact) {
	sort.Slice(values, func(left, right int) bool { return compareFacts(values[left], values[right]) < 0 })
}

func orderContributions(values []Contribution) {
	sort.Slice(values, func(left, right int) bool {
		if result := cmp.Compare(values[left].ObservationID, values[right].ObservationID); result != 0 {
			return result < 0
		}
		return values[left].RecordedAt.Before(values[right].RecordedAt)
	})
}

func compareCitations(left, right Citation) int {
	if result := cmp.Compare(left.SourceDocumentID, right.SourceDocumentID); result != 0 {
		return result
	}
	if result := cmp.Compare(left.DocumentVersionID, right.DocumentVersionID); result != 0 {
		return result
	}
	if result := cmp.Compare(left.SectionOrder, right.SectionOrder); result != 0 {
		return result
	}
	if result := cmp.Compare(left.SectionID, right.SectionID); result != 0 {
		return result
	}
	if result := cmp.Compare(left.StartOffset, right.StartOffset); result != 0 {
		return result
	}
	if result := cmp.Compare(left.EndOffset, right.EndOffset); result != 0 {
		return result
	}
	return cmp.Compare(left.EvidenceID, right.EvidenceID)
}

func orderCitations(values []Citation) {
	sort.Slice(values, func(left, right int) bool { return compareCitations(values[left], values[right]) < 0 })
}

func orderChanges(values []Change) {
	sort.Slice(values, func(left, right int) bool { return compareChanges(values[left], values[right]) < 0 })
}
func compareChanges(left, right Change) int {
	if result := temporal.CompareStateKeys(left.Key, right.Key); result != 0 {
		return result
	}
	if result := cmp.Compare(left.Kind, right.Kind); result != 0 {
		return result
	}
	if result := cmp.Compare(factValue(left.Before), factValue(right.Before)); result != 0 {
		return result
	}
	return cmp.Compare(factValue(left.After), factValue(right.After))
}

func factValue(value *Fact) string {
	if value == nil {
		return ""
	}
	if text, ok := value.Value.Text(); ok {
		return text
	}
	if mention, ok := value.Value.MentionID(); ok {
		return mention
	}
	if entity, _, ok := value.Value.Entity(); ok {
		return entity
	}
	return ""
}

func orderUnresolvedItems(values []UnresolvedItem) {
	sort.Slice(values, func(left, right int) bool {
		if result := temporal.CompareStateKeys(values[left].Key, values[right].Key); result != 0 {
			return result < 0
		}
		return values[left].Reason < values[right].Reason
	})
}

func orderStateKeys(values []temporal.StateKey) {
	sort.Slice(values, func(left, right int) bool { return temporal.CompareStateKeys(values[left], values[right]) < 0 })
}

func orderTransitions(values []Transition) {
	sort.Slice(values, func(left, right int) bool {
		if result := compareChronology(transitionTime(values[left]), transitionRecordedAt(values[left]), transitionObservationID(values[left]), transitionTime(values[right]), transitionRecordedAt(values[right]), transitionObservationID(values[right])); result != 0 {
			return result < 0
		}
		return temporal.CompareStateKeys(values[left].Key, values[right].Key) < 0
	})
}

func orderCausalLinks(values []CausalLink) {
	sort.Slice(values, func(left, right int) bool {
		if result := compareChronology(contributionValidStart(values[left].Contributions), contributionRecordedAt(values[left].Contributions), contributionObservationID(values[left].Contributions), contributionValidStart(values[right].Contributions), contributionRecordedAt(values[right].Contributions), contributionObservationID(values[right].Contributions)); result != 0 {
			return result < 0
		}
		if result := temporal.CompareTerms(values[left].Cause, values[right].Cause); result != 0 {
			return result < 0
		}
		return temporal.CompareTerms(values[left].Effect, values[right].Effect) < 0
	})
}

func orderGaps(values []Gap) {
	sort.Slice(values, func(left, right int) bool {
		if result := cmp.Compare(values[left].EntityID, values[right].EntityID); result != 0 {
			return result < 0
		}
		if result := cmp.Compare(values[left].Kind, values[right].Kind); result != 0 {
			return result < 0
		}
		if result := cmp.Compare(values[left].SelectionLabel, values[right].SelectionLabel); result != 0 {
			return result < 0
		}
		return values[left].Predicate < values[right].Predicate
	})
}

func transitionTime(value Transition) time.Time {
	if instant, ok := value.ValidTime.Instant(); ok {
		return instant
	}
	start, hasStart, end, hasEnd := value.ValidTime.Bounds()
	if hasStart {
		return start
	}
	if hasEnd {
		return end
	}
	return time.Time{}
}
func transitionRecordedAt(value Transition) time.Time {
	return earliestFactRecordedAt(value.Before, value.After)
}
func transitionObservationID(value Transition) observation.ObservationID {
	return earliestFactObservationID(value.Before, value.After)
}
func earliestFactRecordedAt(values ...*Fact) time.Time {
	var all []Contribution
	for _, value := range values {
		if value != nil {
			all = append(all, value.Contributions...)
		}
	}
	return contributionRecordedAt(all)
}
func earliestFactObservationID(values ...*Fact) observation.ObservationID {
	var all []Contribution
	for _, value := range values {
		if value != nil {
			all = append(all, value.Contributions...)
		}
	}
	return contributionObservationID(all)
}
func contributionValidStart(values []Contribution) time.Time {
	var result time.Time
	for _, value := range values {
		current := contributionStart(value)
		if result.IsZero() || (!current.IsZero() && current.Before(result)) {
			result = current
		}
	}
	return result
}
func contributionStart(value Contribution) time.Time {
	if instant, ok := value.ValidTime.Instant(); ok {
		return instant
	}
	start, hasStart, end, hasEnd := value.ValidTime.Bounds()
	if hasStart {
		return start
	}
	if hasEnd {
		return end
	}
	return time.Time{}
}
func contributionRecordedAt(values []Contribution) time.Time {
	var result time.Time
	for _, value := range values {
		if result.IsZero() || value.RecordedAt.Before(result) {
			result = value.RecordedAt
		}
	}
	return result
}
func contributionObservationID(values []Contribution) observation.ObservationID {
	var result observation.ObservationID
	for _, value := range values {
		if result == "" || value.ObservationID < result {
			result = value.ObservationID
		}
	}
	return result
}
func compareChronology(leftTime, leftRecorded time.Time, leftID observation.ObservationID, rightTime, rightRecorded time.Time, rightID observation.ObservationID) int {
	if result := compareOptionalTime(leftTime, rightTime); result != 0 {
		return result
	}
	if result := compareOptionalTime(leftRecorded, rightRecorded); result != 0 {
		return result
	}
	return cmp.Compare(leftID, rightID)
}
func compareOptionalTime(left, right time.Time) int {
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
