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
	if result := compareTerms(left.Value, right.Value); result != 0 {
		return result
	}
	if result := compareContributions(left.Contributions, right.Contributions); result != 0 {
		return result
	}
	if result := compareCitations(left.SupportingCitations, right.SupportingCitations); result != 0 {
		return result
	}
	return compareCitations(left.ContradictingCitations, right.ContradictingCitations)
}
func orderFacts(values []Fact) {
	sort.Slice(values, func(left, right int) bool { return compareFacts(values[left], values[right]) < 0 })
}

func compareTerms(left, right observation.Term) int {
	if result := temporal.CompareTerms(left, right); result != 0 {
		return result
	}
	leftEntity, leftGrounding, leftOK := left.Entity()
	rightEntity, rightGrounding, rightOK := right.Entity()
	if leftOK != rightOK {
		if leftOK {
			return 1
		}
		return -1
	}
	if leftOK {
		if result := cmp.Compare(leftEntity, rightEntity); result != 0 {
			return result
		}
		return cmp.Compare(leftGrounding, rightGrounding)
	}
	return 0
}

func compareContributions(left, right []Contribution) int {
	for index := 0; index < min(len(left), len(right)); index++ {
		if result := compareContribution(left[index], right[index]); result != 0 {
			return result
		}
	}
	return cmp.Compare(len(left), len(right))
}
func compareContribution(left, right Contribution) int {
	if result := cmp.Compare(left.ObservationID, right.ObservationID); result != 0 {
		return result
	}
	if result := cmp.Compare(left.Status, right.Status); result != 0 {
		return result
	}
	if result := compareTemporalExtent(left.ValidTime, right.ValidTime); result != 0 {
		return result
	}
	if result := compareOptionalTime(left.RecordedAt, right.RecordedAt); result != 0 {
		return result
	}
	if result := compareDerivation(left.Derivation, right.Derivation); result != 0 {
		return result
	}
	if result := cmp.Compare(left.SubjectGroundingMentionID, right.SubjectGroundingMentionID); result != 0 {
		return result
	}
	return cmp.Compare(left.ObjectGroundingMentionID, right.ObjectGroundingMentionID)
}
func orderContributions(values []Contribution) {
	sort.Slice(values, func(left, right int) bool { return compareContribution(values[left], values[right]) < 0 })
}
func compareDerivation(left, right observation.Derivation) int {
	if result := cmp.Compare(left.Method, right.Method); result != 0 {
		return result
	}
	if result := cmp.Compare(left.Version, right.Version); result != 0 {
		return result
	}
	if result := cmp.Compare(left.RunID, right.RunID); result != 0 {
		return result
	}
	if result := cmp.Compare(left.Model, right.Model); result != 0 {
		return result
	}
	return cmp.Compare(left.PromptVersion, right.PromptVersion)
}
func compareTemporalExtent(left, right observation.TemporalExtent) int {
	if result := cmp.Compare(left.Kind(), right.Kind()); result != 0 {
		return result
	}
	if leftInstant, ok := left.Instant(); ok {
		rightInstant, _ := right.Instant()
		return compareOptionalTime(leftInstant, rightInstant)
	}
	leftStart, leftHasStart, leftEnd, leftHasEnd := left.Bounds()
	rightStart, rightHasStart, rightEnd, rightHasEnd := right.Bounds()
	if leftHasStart != rightHasStart {
		if leftHasStart {
			return -1
		}
		return 1
	}
	if leftHasStart {
		if result := compareOptionalTime(leftStart, rightStart); result != 0 {
			return result
		}
	}
	if leftHasEnd != rightHasEnd {
		if leftHasEnd {
			return -1
		}
		return 1
	}
	if leftHasEnd {
		return compareOptionalTime(leftEnd, rightEnd)
	}
	return 0
}

func compareCitations(left, right []Citation) int {
	for index := 0; index < min(len(left), len(right)); index++ {
		if result := compareCitation(left[index], right[index]); result != 0 {
			return result
		}
	}
	return cmp.Compare(len(left), len(right))
}
func compareCitation(left, right Citation) int {
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
	if result := cmp.Compare(left.EvidenceID, right.EvidenceID); result != 0 {
		return result
	}
	if result := cmp.Compare(left.Role, right.Role); result != 0 {
		return result
	}
	if result := cmp.Compare(left.SectionTitle, right.SectionTitle); result != 0 {
		return result
	}
	if result := compareStrings(left.SectionPath, right.SectionPath); result != 0 {
		return result
	}
	if result := cmp.Compare(left.SectionRole, right.SectionRole); result != 0 {
		return result
	}
	if result := cmp.Compare(left.Locator, right.Locator); result != 0 {
		return result
	}
	return cmp.Compare(left.Text, right.Text)
}
func orderCitations(values []Citation) {
	sort.Slice(values, func(left, right int) bool { return compareCitation(values[left], values[right]) < 0 })
}
func compareStrings(left, right []string) int {
	for index := 0; index < min(len(left), len(right)); index++ {
		if result := cmp.Compare(left[index], right[index]); result != 0 {
			return result
		}
	}
	return cmp.Compare(len(left), len(right))
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
	if result := compareFactPointers(left.Before, right.Before); result != 0 {
		return result
	}
	return compareFactPointers(left.After, right.After)
}
func compareFactPointers(left, right *Fact) int {
	if (left == nil) != (right == nil) {
		if left == nil {
			return -1
		}
		return 1
	}
	if left == nil {
		return 0
	}
	return compareFacts(*left, *right)
}

func orderUnresolvedItems(values []UnresolvedItem) {
	sort.Slice(values, func(left, right int) bool { return compareUnresolvedItems(values[left], values[right]) < 0 })
}
func compareUnresolvedItems(left, right UnresolvedItem) int {
	if result := temporal.CompareStateKeys(left.Key, right.Key); result != 0 {
		return result
	}
	if result := cmp.Compare(left.Reason, right.Reason); result != 0 {
		return result
	}
	for index := 0; index < min(len(left.Candidates), len(right.Candidates)); index++ {
		if result := compareFacts(left.Candidates[index], right.Candidates[index]); result != 0 {
			return result
		}
	}
	return cmp.Compare(len(left.Candidates), len(right.Candidates))
}
func orderStateKeys(values []temporal.StateKey) {
	sort.Slice(values, func(left, right int) bool { return temporal.CompareStateKeys(values[left], values[right]) < 0 })
}

func orderTransitions(values []Transition) {
	sort.Slice(values, func(left, right int) bool { return compareTransitions(values[left], values[right]) < 0 })
}
func compareTransitions(left, right Transition) int {
	if result := compareOptionalTime(transitionTime(left), transitionTime(right)); result != 0 {
		return result
	}
	if result := compareTemporalExtent(left.ValidTime, right.ValidTime); result != 0 {
		return result
	}
	if result := compareOptionalTime(transitionRecordedAt(left), transitionRecordedAt(right)); result != 0 {
		return result
	}
	if result := cmp.Compare(transitionObservationID(left), transitionObservationID(right)); result != 0 {
		return result
	}
	if result := temporal.CompareStateKeys(left.Key, right.Key); result != 0 {
		return result
	}
	if result := cmp.Compare(left.Kind, right.Kind); result != 0 {
		return result
	}
	if result := compareFactPointers(left.Before, right.Before); result != 0 {
		return result
	}
	if result := compareFactPointers(left.After, right.After); result != 0 {
		return result
	}
	return compareUnresolvedSlices(left.Unresolved, right.Unresolved)
}

func orderCausalLinks(values []CausalLink) {
	sort.Slice(values, func(left, right int) bool { return compareCausalLinks(values[left], values[right]) < 0 })
}
func compareCausalLinks(left, right CausalLink) int {
	if result := compareChronology(contributionValidStart(left.Contributions), contributionRecordedAt(left.Contributions), contributionObservationID(left.Contributions), contributionValidStart(right.Contributions), contributionRecordedAt(right.Contributions), contributionObservationID(right.Contributions)); result != 0 {
		return result
	}
	if result := compareTerms(left.Cause, right.Cause); result != 0 {
		return result
	}
	if result := compareTerms(left.Effect, right.Effect); result != 0 {
		return result
	}
	if result := compareContributions(left.Contributions, right.Contributions); result != 0 {
		return result
	}
	if result := compareCitations(left.SupportingCitations, right.SupportingCitations); result != 0 {
		return result
	}
	return compareCitations(left.ContradictingCitations, right.ContradictingCitations)
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
func compareUnresolvedSlices(left, right []UnresolvedItem) int {
	for index := 0; index < min(len(left), len(right)); index++ {
		if result := compareUnresolvedItems(left[index], right[index]); result != 0 {
			return result
		}
	}
	return cmp.Compare(len(left), len(right))
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
