package temporal_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

func TestReconstructStateIncludesOnlyTheSelectedInstant(t *testing.T) {
	at := pointTime(2024, time.January, 15)
	selection := pointSelection(t, at)
	candidates := []temporal.StateCandidate{
		pointCandidate(t, "matching", "status", "active", pointInstant(t, at), pointRecordedAt(1), observation.StatusObserved, nil, observation.EvidenceSupporting),
		pointCandidate(t, "other", "status", "inactive", pointInstant(t, at.Add(time.Hour)), pointRecordedAt(2), observation.StatusObserved, nil, observation.EvidenceSupporting),
	}

	summary, err := temporal.ReconstructState(selection, temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("ReconstructState() error = %v", err)
	}
	if summary.Selection != selection {
		t.Fatalf("Selection = %#v, want %#v", summary.Selection, selection)
	}
	if len(summary.Facts) != 1 || len(summary.Unresolved) != 0 {
		t.Fatalf("summary = %#v, want one resolved fact", summary)
	}
	assertPointFact(t, summary.Facts[0], "active", []observation.ObservationID{"matching"})
}

func TestReconstructStateUsesHalfOpenIntervalEndpoints(t *testing.T) {
	start := pointTime(2024, time.January, 1)
	end := pointTime(2024, time.February, 1)
	extent := pointInterval(t, start, end)
	candidate := pointCandidate(t, "interval", "status", "active", extent, pointRecordedAt(1), observation.StatusObserved, nil, observation.EvidenceSupporting)

	atStart, err := temporal.ReconstructState(pointSelection(t, start), temporal.CurrentKnowledge(), []temporal.StateCandidate{candidate})
	if err != nil {
		t.Fatalf("ReconstructState(start) error = %v", err)
	}
	if len(atStart.Facts) != 1 {
		t.Fatalf("start Facts = %#v, want interval included at its start", atStart.Facts)
	}

	atEnd, err := temporal.ReconstructState(pointSelection(t, end), temporal.CurrentKnowledge(), []temporal.StateCandidate{candidate})
	if err != nil {
		t.Fatalf("ReconstructState(end) error = %v", err)
	}
	if len(atEnd.Facts) != 0 || len(atEnd.Unresolved) != 0 {
		t.Fatalf("end summary = %#v, want interval excluded at its end", atEnd)
	}
}

func TestReconstructStateHandlesOpenIntervals(t *testing.T) {
	start := pointTime(2024, time.January, 10)
	end := pointTime(2024, time.January, 20)
	since, err := observation.Since(start)
	if err != nil {
		t.Fatalf("observation.Since() error = %v", err)
	}
	until, err := observation.Until(end)
	if err != nil {
		t.Fatalf("observation.Until() error = %v", err)
	}
	candidates := []temporal.StateCandidate{
		pointCandidate(t, "since", "since-status", "active", since, pointRecordedAt(1), observation.StatusObserved, nil, observation.EvidenceSupporting),
		pointCandidate(t, "until", "until-status", "active", until, pointRecordedAt(2), observation.StatusObserved, nil, observation.EvidenceSupporting),
	}

	beforeStart, err := temporal.ReconstructState(pointSelection(t, start.Add(-time.Hour)), temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("ReconstructState(before start) error = %v", err)
	}
	if got := pointFactPredicates(beforeStart.Facts); !slices.Equal(got, []observation.Predicate{"until-status"}) {
		t.Fatalf("before-start predicates = %v, want only open-start interval", got)
	}

	atStart, err := temporal.ReconstructState(pointSelection(t, start), temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("ReconstructState(at start) error = %v", err)
	}
	if got := pointFactPredicates(atStart.Facts); !slices.Equal(got, []observation.Predicate{"since-status", "until-status"}) {
		t.Fatalf("at-start predicates = %v, want both open intervals", got)
	}

	atEnd, err := temporal.ReconstructState(pointSelection(t, end), temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("ReconstructState(at end) error = %v", err)
	}
	if got := pointFactPredicates(atEnd.Facts); !slices.Equal(got, []observation.Predicate{"since-status"}) {
		t.Fatalf("at-end predicates = %v, want only open-end interval", got)
	}
}

func TestReconstructStatePreservesUnknownAndWindowUncertainty(t *testing.T) {
	at := pointTime(2024, time.January, 15)
	window, err := observation.Within(at.Add(-time.Hour), at.Add(time.Hour))
	if err != nil {
		t.Fatalf("observation.Within() error = %v", err)
	}
	candidates := []temporal.StateCandidate{
		pointCandidate(t, "unknown", "risk", "watch", observation.UnknownTime(), pointRecordedAt(1), observation.StatusObserved, nil, observation.EvidenceSupporting),
		pointCandidate(t, "window", "risk", "elevated", window, pointRecordedAt(2), observation.StatusObserved, nil, observation.EvidenceSupporting),
	}

	summary, err := temporal.ReconstructState(pointSelection(t, at), temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("ReconstructState() error = %v", err)
	}
	if len(summary.Facts) != 0 || len(summary.Unresolved) != 1 {
		t.Fatalf("summary = %#v, want one unresolved key", summary)
	}
	unresolved := summary.Unresolved[0]
	if unresolved.Reason != temporal.UnresolvedTemporalUncertainty || len(unresolved.Candidates) != 2 {
		t.Fatalf("Unresolved = %#v, want both temporally uncertain candidates", unresolved)
	}
}

func TestReconstructStatePreservesConflictingValues(t *testing.T) {
	at := pointTime(2024, time.January, 15)
	candidates := []temporal.StateCandidate{
		pointCandidate(t, "remote", "location", "remote", pointInstant(t, at), pointRecordedAt(1), observation.StatusObserved, nil, observation.EvidenceSupporting),
		pointCandidate(t, "office", "location", "office", pointInstant(t, at), pointRecordedAt(2), observation.StatusObserved, nil, observation.EvidenceSupporting),
	}

	summary, err := temporal.ReconstructState(pointSelection(t, at), temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("ReconstructState() error = %v", err)
	}
	if len(summary.Facts) != 0 || len(summary.Unresolved) != 1 ||
		summary.Unresolved[0].Reason != temporal.UnresolvedConflict ||
		len(summary.Unresolved[0].Candidates) != 2 {
		t.Fatalf("summary = %#v, want both conflicting values", summary)
	}
}

func TestReconstructStateKeepsHypothesisUnresolved(t *testing.T) {
	at := pointTime(2024, time.January, 15)
	candidate := pointCandidate(t, "hypothesis", "scope", "expanded", pointInstant(t, at), pointRecordedAt(1), observation.StatusHypothesized, nil, observation.EvidenceSupporting)

	summary, err := temporal.ReconstructState(pointSelection(t, at), temporal.CurrentKnowledge(), []temporal.StateCandidate{candidate})
	if err != nil {
		t.Fatalf("ReconstructState() error = %v", err)
	}
	if len(summary.Facts) != 0 || len(summary.Unresolved) != 1 ||
		summary.Unresolved[0].Reason != temporal.UnresolvedHypothesis {
		t.Fatalf("summary = %#v, want cited hypothesis unresolved", summary)
	}
}

func TestReconstructStateKeepsCounterevidenceOnlyUnresolved(t *testing.T) {
	at := pointTime(2024, time.January, 15)
	candidate := pointCandidate(t, "counter", "status", "active", pointInstant(t, at), pointRecordedAt(1), observation.StatusObserved, nil, observation.EvidenceContradicting)

	summary, err := temporal.ReconstructState(pointSelection(t, at), temporal.CurrentKnowledge(), []temporal.StateCandidate{candidate})
	if err != nil {
		t.Fatalf("ReconstructState() error = %v", err)
	}
	if len(summary.Facts) != 0 || len(summary.Unresolved) != 1 ||
		summary.Unresolved[0].Reason != temporal.UnresolvedCounterevidenceOnly {
		t.Fatalf("summary = %#v, want counterevidence-only material unresolved", summary)
	}
	if len(summary.Unresolved[0].Candidates[0].SupportingEvidenceIDs) != 0 ||
		len(summary.Unresolved[0].Candidates[0].ContradictingEvidenceIDs) != 1 {
		t.Fatalf("candidate evidence = %#v, want role-separated counterevidence", summary.Unresolved[0].Candidates[0])
	}
}

func TestReconstructStateKeepsRecordedCutoffIndependentFromValidPoint(t *testing.T) {
	at := pointTime(2024, time.January, 15)
	extent := pointInterval(t, at.Add(-time.Hour), at.Add(time.Hour))
	early := pointCandidate(t, "early", "status", "active", extent, pointRecordedAt(1), observation.StatusObserved, nil, observation.EvidenceSupporting)
	late := pointCandidate(t, "late", "status", "inactive", extent, pointRecordedAt(3), observation.StatusObserved, nil, observation.EvidenceSupporting)
	scope, err := temporal.KnownAsOf(pointRecordedAt(2))
	if err != nil {
		t.Fatalf("temporal.KnownAsOf() error = %v", err)
	}

	historical, err := temporal.ReconstructState(pointSelection(t, at), scope, []temporal.StateCandidate{late, early})
	if err != nil {
		t.Fatalf("ReconstructState(historical) error = %v", err)
	}
	if len(historical.Facts) != 1 || len(historical.Unresolved) != 0 {
		t.Fatalf("historical = %#v, want only cutoff-visible fact", historical)
	}
	assertPointFact(t, historical.Facts[0], "active", []observation.ObservationID{"early"})

	current, err := temporal.ReconstructState(pointSelection(t, at), temporal.CurrentKnowledge(), []temporal.StateCandidate{late, early})
	if err != nil {
		t.Fatalf("ReconstructState(current) error = %v", err)
	}
	if len(current.Facts) != 0 || len(current.Unresolved) != 1 ||
		current.Unresolved[0].Reason != temporal.UnresolvedConflict {
		t.Fatalf("current = %#v, want both valid-time candidates conflicting", current)
	}
}

func TestReconstructStateIsDeterministicAcrossInputOrder(t *testing.T) {
	at := pointTime(2024, time.January, 15)
	candidates := []temporal.StateCandidate{
		pointCandidate(t, "z", "status", "active", pointInstant(t, at), pointRecordedAt(2), observation.StatusObserved, nil, observation.EvidenceSupporting),
		pointCandidate(t, "a", "status", "active", pointInstant(t, at), pointRecordedAt(1), observation.StatusObserved, nil, observation.EvidenceSupporting),
	}
	reversed := append([]temporal.StateCandidate{}, candidates...)
	slices.Reverse(reversed)

	first, err := temporal.ReconstructState(pointSelection(t, at), temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("ReconstructState(first) error = %v", err)
	}
	second, err := temporal.ReconstructState(pointSelection(t, at), temporal.CurrentKnowledge(), reversed)
	if err != nil {
		t.Fatalf("ReconstructState(second) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("reordered results differ:\nfirst  %#v\nsecond %#v", first, second)
	}
	assertPointFact(t, first.Facts[0], "active", []observation.ObservationID{"a", "z"})
}

func TestReconstructStateNeverUsesConfidenceToSelectState(t *testing.T) {
	at := pointTime(2024, time.January, 15)
	low := pointConfidence(t, 0.01)
	high := pointConfidence(t, 0.99)
	candidates := []temporal.StateCandidate{
		pointCandidate(t, "low", "status", "active", pointInstant(t, at), pointRecordedAt(1), observation.StatusObserved, &low, observation.EvidenceSupporting),
		pointCandidate(t, "high", "status", "inactive", pointInstant(t, at), pointRecordedAt(2), observation.StatusObserved, &high, observation.EvidenceSupporting),
	}

	summary, err := temporal.ReconstructState(pointSelection(t, at), temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("ReconstructState() error = %v", err)
	}
	if len(summary.Facts) != 0 || len(summary.Unresolved) != 1 ||
		summary.Unresolved[0].Reason != temporal.UnresolvedConflict ||
		len(summary.Unresolved[0].Candidates) != 2 {
		t.Fatalf("summary = %#v, want confidence-independent conflict", summary)
	}
}

func pointSelection(t *testing.T, at time.Time) temporal.TemporalSelection {
	t.Helper()
	selection, err := temporal.At("selected-point", at)
	if err != nil {
		t.Fatalf("temporal.At() error = %v", err)
	}
	return selection
}

func pointCandidate(
	t *testing.T,
	id string,
	predicate observation.Predicate,
	value string,
	validTime observation.TemporalExtent,
	recordedAt time.Time,
	status observation.EpistemicStatus,
	confidence *observation.Confidence,
	role observation.EvidenceRole,
) temporal.StateCandidate {
	t.Helper()
	subject, err := observation.NewTextTerm("project-atlas")
	if err != nil {
		t.Fatalf("observation.NewTextTerm(subject) error = %v", err)
	}
	object, err := observation.NewTextTerm(value)
	if err != nil {
		t.Fatalf("observation.NewTextTerm(value) error = %v", err)
	}
	evidenceID := evidence.EvidenceID("evidence-" + id)
	valueObservation, err := observation.NewObservation(observation.ObservationInput{
		ID:         observation.ObservationID(id),
		Statement:  observation.Statement{Subject: subject, Predicate: predicate, Object: object},
		ValidTime:  validTime,
		RecordedAt: recordedAt,
		Evidence:   []observation.EvidenceLink{{EvidenceID: evidenceID, Role: role}},
		Derivation: observation.Derivation{Method: "synthetic", Version: "point-v1"},
		Status:     status,
		Confidence: confidence,
	})
	if err != nil {
		t.Fatalf("observation.NewObservation(%q) error = %v", id, err)
	}
	key, err := temporal.NewStateKey(subject, predicate)
	if err != nil {
		t.Fatalf("temporal.NewStateKey() error = %v", err)
	}
	return temporal.StateCandidate{Key: key, Value: object, Observation: valueObservation}
}

func pointInstant(t *testing.T, at time.Time) observation.TemporalExtent {
	t.Helper()
	extent, err := observation.AtTime(at)
	if err != nil {
		t.Fatalf("observation.AtTime() error = %v", err)
	}
	return extent
}

func pointInterval(t *testing.T, start, end time.Time) observation.TemporalExtent {
	t.Helper()
	extent, err := observation.During(start, end)
	if err != nil {
		t.Fatalf("observation.During() error = %v", err)
	}
	return extent
}

func pointConfidence(t *testing.T, value float64) observation.Confidence {
	t.Helper()
	confidence, err := observation.NewUnitIntervalConfidence(value)
	if err != nil {
		t.Fatalf("observation.NewUnitIntervalConfidence() error = %v", err)
	}
	return confidence
}

func pointTime(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 12, 0, 0, 123456789, time.FixedZone("synthetic", -5*60*60))
}

func pointRecordedAt(day int) time.Time {
	return time.Date(2024, time.July, day, 12, 0, 0, 0, time.UTC)
}

func pointFactPredicates(facts []temporal.Fact) []observation.Predicate {
	result := make([]observation.Predicate, len(facts))
	for index, fact := range facts {
		result[index] = fact.Key.Predicate
	}
	return result
}

func assertPointFact(t *testing.T, fact temporal.Fact, value string, observationIDs []observation.ObservationID) {
	t.Helper()
	gotValue, ok := fact.Value.Text()
	if !ok || gotValue != value || !slices.Equal(fact.ObservationIDs, observationIDs) {
		t.Fatalf("Fact = %#v, want value %q observations %v", fact, value, observationIDs)
	}
}
