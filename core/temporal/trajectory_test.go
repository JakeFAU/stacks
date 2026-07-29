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

func TestBuildTrajectoryPartitionsAtCanonicalBoundaries(t *testing.T) {
	start := trajectoryTime(1)
	firstBoundary := trajectoryTime(2)
	secondBoundary := trajectoryTime(3)
	end := trajectoryTime(5)
	selection := trajectorySelection(t, start, end)
	candidates := []temporal.StateCandidate{
		trajectoryCandidate(t, "active", "status", "active", trajectoryInterval(t, start, firstBoundary), trajectoryRecordedAt(1), nil),
		trajectoryCandidate(t, "paused", "status", "paused", trajectoryInterval(t, firstBoundary, secondBoundary), trajectoryRecordedAt(2), nil),
	}

	got, err := temporal.BuildTrajectory(selection, temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("BuildTrajectory() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("transitions = %#v, want add/change/remove at the three canonical boundaries", got)
	}
	assertTrajectoryTransition(t, got[0], temporal.ChangeAdded, start, "", "active")
	assertTrajectoryTransition(t, got[1], temporal.ChangeChanged, firstBoundary, "active", "paused")
	assertTrajectoryTransition(t, got[2], temporal.ChangeRemoved, secondBoundary, "paused", "")
}

func TestBuildTrajectoryDoesNotInventAnAdditionAtTheSelectedWindowStart(t *testing.T) {
	start := trajectoryTime(2)
	end := trajectoryTime(4)
	preexistingStart := trajectoryTime(1)
	candidate := trajectoryCandidate(
		t,
		"preexisting",
		"status",
		"active",
		trajectoryInterval(t, preexistingStart, end),
		trajectoryRecordedAt(1),
		nil,
	)

	got, err := temporal.BuildTrajectory(
		trajectorySelection(t, start, end),
		temporal.CurrentKnowledge(),
		[]temporal.StateCandidate{candidate},
	)
	if err != nil {
		t.Fatalf("BuildTrajectory() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("transitions = %#v, want no invented transition at the requested window boundary", got)
	}
}

func TestBuildTrajectoryPreservesOverlappingConflict(t *testing.T) {
	start := trajectoryTime(1)
	overlapStart := trajectoryTime(2)
	firstEnd := trajectoryTime(3)
	secondEnd := trajectoryTime(4)
	candidates := []temporal.StateCandidate{
		trajectoryCandidate(t, "active", "status", "active", trajectoryInterval(t, start, firstEnd), trajectoryRecordedAt(1), nil),
		trajectoryCandidate(t, "paused", "status", "paused", trajectoryInterval(t, overlapStart, secondEnd), trajectoryRecordedAt(2), nil),
	}

	got, err := temporal.BuildTrajectory(
		trajectorySelection(t, start, trajectoryTime(5)),
		temporal.CurrentKnowledge(),
		candidates,
	)
	if err != nil {
		t.Fatalf("BuildTrajectory() error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("transitions = %#v, want add/conflict-boundary/recovery/remove", got)
	}
	assertTrajectoryTransition(t, got[1], temporal.ChangeRemoved, overlapStart, "active", "")
	if len(got[1].Unresolved) != 1 ||
		got[1].Unresolved[0].Reason != temporal.UnresolvedConflict ||
		len(got[1].Unresolved[0].Candidates) != 2 {
		t.Fatalf("overlap transition unresolved = %#v, want both conflicting values", got[1].Unresolved)
	}
	assertTrajectoryTransition(t, got[2], temporal.ChangeAdded, firstEnd, "", "paused")
	if len(got[2].Unresolved) != 1 ||
		got[2].Unresolved[0].Reason != temporal.UnresolvedConflict {
		t.Fatalf("recovery transition unresolved = %#v, want prior conflict preserved", got[2].Unresolved)
	}
}

func TestBuildTrajectoryPreservesTemporalUncertaintyAtBoundaries(t *testing.T) {
	start := trajectoryTime(1)
	uncertainStart := trajectoryTime(2)
	uncertainEnd := trajectoryTime(3)
	end := trajectoryTime(4)
	uncertain, err := observation.Within(uncertainStart, uncertainEnd)
	if err != nil {
		t.Fatalf("observation.Within() error = %v", err)
	}
	candidates := []temporal.StateCandidate{
		trajectoryCandidate(t, "certain", "status", "active", trajectoryInterval(t, start, end), trajectoryRecordedAt(1), nil),
		trajectoryCandidate(t, "uncertain", "status", "paused", uncertain, trajectoryRecordedAt(2), nil),
	}

	got, err := temporal.BuildTrajectory(
		trajectorySelection(t, start, trajectoryTime(5)),
		temporal.CurrentKnowledge(),
		candidates,
	)
	if err != nil {
		t.Fatalf("BuildTrajectory() error = %v", err)
	}
	if len(got) < 3 {
		t.Fatalf("transitions = %#v, want uncertainty entry and recovery boundaries", got)
	}
	assertTrajectoryTransition(t, got[1], temporal.ChangeRemoved, uncertainStart, "active", "")
	if len(got[1].Unresolved) != 1 ||
		got[1].Unresolved[0].Reason != temporal.UnresolvedTemporalUncertainty {
		t.Fatalf("uncertainty transition = %#v, want temporal uncertainty", got[1])
	}
	assertTrajectoryTransition(t, got[2], temporal.ChangeAdded, uncertainEnd, "", "active")
	if len(got[2].Unresolved) != 1 ||
		got[2].Unresolved[0].Reason != temporal.UnresolvedTemporalUncertainty {
		t.Fatalf("uncertainty recovery = %#v, want prior uncertainty preserved", got[2])
	}
}

func TestBuildTrajectoryGroupsIdenticalInstantsDeterministically(t *testing.T) {
	start := trajectoryTime(1)
	at := trajectoryTime(2)
	end := trajectoryTime(4)
	candidates := []temporal.StateCandidate{
		trajectoryCandidate(t, "z", "status", "paused", trajectoryInstant(t, at), trajectoryRecordedAt(2), nil),
		trajectoryCandidate(t, "a", "status", "active", trajectoryInstant(t, at), trajectoryRecordedAt(1), nil),
		trajectoryCandidate(t, "prior", "status", "pending", trajectoryInterval(t, start, at), trajectoryRecordedAt(0), nil),
	}
	reversed := append([]temporal.StateCandidate{}, candidates...)
	slices.Reverse(reversed)

	first, err := temporal.BuildTrajectory(trajectorySelection(t, start, end), temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("BuildTrajectory(first) error = %v", err)
	}
	second, err := temporal.BuildTrajectory(trajectorySelection(t, start, end), temporal.CurrentKnowledge(), reversed)
	if err != nil {
		t.Fatalf("BuildTrajectory(second) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("reordered identical-instant results differ:\nfirst  %#v\nsecond %#v", first, second)
	}
	if len(first) != 2 {
		t.Fatalf("transitions = %#v, want one grouped transition at the identical instant", first)
	}
	assertTrajectoryTransition(t, first[1], temporal.ChangeRemoved, at, "pending", "")
	if len(first[1].Unresolved) != 1 ||
		first[1].Unresolved[0].Reason != temporal.UnresolvedConflict ||
		len(first[1].Unresolved[0].Candidates) != 2 {
		t.Fatalf("identical-instant unresolved = %#v, want one deterministic conflict", first[1].Unresolved)
	}
}

func TestBuildTrajectoryOrdersStableTiesByRecordedTimeObservationAndKey(t *testing.T) {
	start := trajectoryTime(1)
	end := trajectoryTime(3)
	at := trajectoryTime(2)
	candidates := []temporal.StateCandidate{
		trajectoryCandidate(t, "z-id", "z-key", "z-value", trajectoryInterval(t, at, end), trajectoryRecordedAt(2), nil),
		trajectoryCandidate(t, "b-id", "b-key", "b-value", trajectoryInterval(t, at, end), trajectoryRecordedAt(1), nil),
		trajectoryCandidate(t, "a-id", "a-key", "a-value", trajectoryInterval(t, at, end), trajectoryRecordedAt(1), nil),
	}
	reversed := append([]temporal.StateCandidate{}, candidates...)
	slices.Reverse(reversed)

	first, err := temporal.BuildTrajectory(trajectorySelection(t, start, end), temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("BuildTrajectory(first) error = %v", err)
	}
	second, err := temporal.BuildTrajectory(trajectorySelection(t, start, end), temporal.CurrentKnowledge(), reversed)
	if err != nil {
		t.Fatalf("BuildTrajectory(second) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("stable-tie results differ:\nfirst  %#v\nsecond %#v", first, second)
	}
	var atBoundary []temporal.Transition
	for _, transition := range first {
		instant, _ := transition.ValidTime.Instant()
		if instant.Equal(at.UTC().Truncate(time.Microsecond)) {
			atBoundary = append(atBoundary, transition)
		}
	}
	if len(atBoundary) != 3 {
		t.Fatalf("transitions at tie boundary = %#v, want 3", atBoundary)
	}
	gotIDs := []observation.ObservationID{
		atBoundary[0].After.ObservationIDs[0],
		atBoundary[1].After.ObservationIDs[0],
		atBoundary[2].After.ObservationIDs[0],
	}
	if !slices.Equal(gotIDs, []observation.ObservationID{"a-id", "b-id", "z-id"}) {
		t.Fatalf("tie observation order = %v, want recorded time then observation ID", gotIDs)
	}
}

func TestBuildTrajectoryNeverUsesConfidenceToSelectOrOrderState(t *testing.T) {
	start := trajectoryTime(1)
	at := trajectoryTime(2)
	end := trajectoryTime(4)
	low := trajectoryConfidence(t, 0.01)
	high := trajectoryConfidence(t, 0.99)
	candidates := []temporal.StateCandidate{
		trajectoryCandidate(t, "low", "status", "active", trajectoryInstant(t, at), trajectoryRecordedAt(1), &low),
		trajectoryCandidate(t, "high", "status", "paused", trajectoryInstant(t, at), trajectoryRecordedAt(2), &high),
		trajectoryCandidate(t, "prior", "status", "pending", trajectoryInterval(t, start, at), trajectoryRecordedAt(0), nil),
	}

	got, err := temporal.BuildTrajectory(
		trajectorySelection(t, start, end),
		temporal.CurrentKnowledge(),
		candidates,
	)
	if err != nil {
		t.Fatalf("BuildTrajectory() error = %v", err)
	}
	if len(got) != 2 || len(got[1].Unresolved) != 1 ||
		got[1].Unresolved[0].Reason != temporal.UnresolvedConflict ||
		len(got[1].Unresolved[0].Candidates) != 2 {
		t.Fatalf("transitions = %#v, want confidence-independent conflict", got)
	}
	gotValues := []string{
		trajectoryFactText(t, got[1].Unresolved[0].Candidates[0]),
		trajectoryFactText(t, got[1].Unresolved[0].Candidates[1]),
	}
	if !slices.Equal(gotValues, []string{"active", "paused"}) {
		t.Fatalf("candidate order = %v, want lexical typed values rather than confidence", gotValues)
	}
}

func TestBuildTrajectoryKeepsRecordedCutoffIndependentFromValidBoundaries(t *testing.T) {
	start := trajectoryTime(1)
	at := trajectoryTime(2)
	end := trajectoryTime(4)
	early := trajectoryCandidate(t, "early", "status", "active", trajectoryInterval(t, start, end), trajectoryRecordedAt(1), nil)
	late := trajectoryCandidate(t, "late", "status", "paused", trajectoryInterval(t, at, end), trajectoryRecordedAt(3), nil)
	scope, err := temporal.KnownAsOf(trajectoryRecordedAt(2))
	if err != nil {
		t.Fatalf("temporal.KnownAsOf() error = %v", err)
	}

	got, err := temporal.BuildTrajectory(trajectorySelection(t, start, end), scope, []temporal.StateCandidate{late, early})
	if err != nil {
		t.Fatalf("BuildTrajectory() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("transitions = %#v, want late-recorded boundary excluded without changing valid time", got)
	}
	assertTrajectoryTransition(t, got[0], temporal.ChangeAdded, start, "", "active")
}

func trajectorySelection(t *testing.T, start, end time.Time) temporal.TemporalSelection {
	t.Helper()
	selection, err := temporal.Between("trajectory", start, end)
	if err != nil {
		t.Fatalf("temporal.Between() error = %v", err)
	}
	return selection
}

func trajectoryCandidate(
	t *testing.T,
	id string,
	predicate observation.Predicate,
	value string,
	validTime observation.TemporalExtent,
	recordedAt time.Time,
	confidence *observation.Confidence,
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
	valueObservation, err := observation.NewObservation(observation.ObservationInput{
		ID:         observation.ObservationID(id),
		Statement:  observation.Statement{Subject: subject, Predicate: predicate, Object: object},
		ValidTime:  validTime,
		RecordedAt: recordedAt,
		Evidence: []observation.EvidenceLink{{
			EvidenceID: evidence.EvidenceID("evidence-" + id),
			Role:       observation.EvidenceSupporting,
		}},
		Derivation: observation.Derivation{Method: "synthetic", Version: "trajectory-v1"},
		Status:     observation.StatusObserved,
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

func trajectoryInterval(t *testing.T, start, end time.Time) observation.TemporalExtent {
	t.Helper()
	extent, err := observation.During(start, end)
	if err != nil {
		t.Fatalf("observation.During() error = %v", err)
	}
	return extent
}

func trajectoryInstant(t *testing.T, at time.Time) observation.TemporalExtent {
	t.Helper()
	extent, err := observation.AtTime(at)
	if err != nil {
		t.Fatalf("observation.AtTime() error = %v", err)
	}
	return extent
}

func trajectoryConfidence(t *testing.T, value float64) observation.Confidence {
	t.Helper()
	confidence, err := observation.NewUnitIntervalConfidence(value)
	if err != nil {
		t.Fatalf("observation.NewUnitIntervalConfidence() error = %v", err)
	}
	return confidence
}

func trajectoryTime(day int) time.Time {
	return time.Date(2024, time.January, day, 12, 0, 0, 987654321, time.FixedZone("synthetic", -5*60*60))
}

func trajectoryRecordedAt(day int) time.Time {
	return time.Date(2024, time.July, day, 12, 0, 0, 0, time.UTC)
}

func assertTrajectoryTransition(
	t *testing.T,
	got temporal.Transition,
	kind temporal.ChangeKind,
	at time.Time,
	beforeValue, afterValue string,
) {
	t.Helper()
	instant, ok := got.ValidTime.Instant()
	if !ok || !instant.Equal(at.UTC().Truncate(time.Microsecond)) {
		t.Fatalf("transition valid time = %#v, want instant %s", got.ValidTime, at)
	}
	if got.Kind != kind {
		t.Fatalf("transition kind = %q, want %q", got.Kind, kind)
	}
	if beforeValue == "" {
		if got.Before != nil {
			t.Fatalf("transition Before = %#v, want nil", got.Before)
		}
	} else if got.Before == nil || trajectoryFactText(t, *got.Before) != beforeValue {
		t.Fatalf("transition Before = %#v, want %q", got.Before, beforeValue)
	}
	if afterValue == "" {
		if got.After != nil {
			t.Fatalf("transition After = %#v, want nil", got.After)
		}
	} else if got.After == nil || trajectoryFactText(t, *got.After) != afterValue {
		t.Fatalf("transition After = %#v, want %q", got.After, afterValue)
	}
}

func trajectoryFactText(t *testing.T, fact temporal.Fact) string {
	t.Helper()
	value, ok := fact.Value.Text()
	if !ok {
		t.Fatalf("fact value = %#v, want text", fact.Value)
	}
	return value
}
