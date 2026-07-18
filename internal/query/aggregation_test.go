package query

import (
	"slices"
	"testing"
	"time"

	"stacks/internal/knowledge"
)

func TestAggregateWindowFiltersValidAndRecordedTime(t *testing.T) {
	selection := aggregationWindow(t)
	cutoff := time.Date(2025, time.April, 15, 0, 0, 0, 0, time.UTC)
	scope, err := KnownAsOf(cutoff)
	if err != nil {
		t.Fatalf("KnownAsOf() error = %v", err)
	}

	inside := mustDuring(t,
		time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
	)
	outside := mustDuring(t,
		time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, time.June, 1, 0, 0, 0, 0, time.UTC),
	)
	candidates := []StateCandidate{
		stateCandidate(t, "status", "active", "observation-1", "evidence-1", inside, cutoff.Add(-time.Hour), knowledge.StatusObserved),
		stateCandidate(t, "priority", "high", "observation-2", "evidence-2", inside, cutoff.Add(time.Hour), knowledge.StatusObserved),
		stateCandidate(t, "owner", "Jacob", "observation-3", "evidence-3", outside, cutoff.Add(-time.Hour), knowledge.StatusObserved),
	}

	summary, err := AggregateWindow(selection, scope, candidates)
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}

	if len(summary.Facts) != 1 || summary.Facts[0].Key != "status" {
		t.Errorf("Facts = %v, want only eligible status fact", summary.Facts)
	}
}

func TestAggregateWindowMergesAgreementAndIsIdempotent(t *testing.T) {
	selection := aggregationWindow(t)
	validTime := mustDuring(t,
		time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
	)
	recordedAt := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	first := stateCandidate(t, "status", "active", "observation-2", "evidence-2", validTime, recordedAt, knowledge.StatusObserved)
	second := stateCandidate(t, "status", "active", "observation-1", "evidence-1", validTime, recordedAt, knowledge.StatusInferred)

	summary, err := AggregateWindow(selection, CurrentKnowledge(), []StateCandidate{first, second, first})
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}

	if len(summary.Facts) != 1 {
		t.Fatalf("len(Facts) = %d, want 1", len(summary.Facts))
	}
	fact := summary.Facts[0]
	if !slices.Equal(fact.ObservationIDs, []knowledge.ObservationID{"observation-1", "observation-2"}) {
		t.Errorf("ObservationIDs = %v, want sorted unique IDs", fact.ObservationIDs)
	}
	if !slices.Equal(fact.EvidenceIDs, []knowledge.EvidenceID{"evidence-1", "evidence-2"}) {
		t.Errorf("EvidenceIDs = %v, want sorted unique IDs", fact.EvidenceIDs)
	}
}

func TestAggregateWindowPreservesConflictingProvenance(t *testing.T) {
	selection := aggregationWindow(t)
	validTime := mustDuring(t,
		time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
	)
	recordedAt := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	candidates := []StateCandidate{
		stateCandidate(t, "status", "active", "observation-1", "evidence-1", validTime, recordedAt, knowledge.StatusObserved),
		stateCandidate(t, "status", "paused", "observation-2", "evidence-2", validTime, recordedAt, knowledge.StatusObserved),
	}

	summary, err := AggregateWindow(selection, CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}

	if len(summary.Facts) != 0 {
		t.Errorf("Facts = %v, want no falsely resolved state", summary.Facts)
	}
	if len(summary.Unresolved) != 1 {
		t.Fatalf("len(Unresolved) = %d, want 1", len(summary.Unresolved))
	}
	unresolved := summary.Unresolved[0]
	if unresolved.Reason != UnresolvedConflict {
		t.Errorf("Reason = %q, want %q", unresolved.Reason, UnresolvedConflict)
	}
	if got := []string{unresolved.Candidates[0].Value, unresolved.Candidates[1].Value}; !slices.Equal(got, []string{"active", "paused"}) {
		t.Errorf("candidate values = %v, want ordered conflicting values", got)
	}
}

func TestAggregateWindowDistinguishesTransitionFromConflict(t *testing.T) {
	selection := aggregationWindow(t)
	recordedAt := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	activeTime := mustDuring(t,
		time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.May, 1, 0, 0, 0, 0, time.UTC),
	)
	pausedTime := mustDuring(t,
		time.Date(2025, time.May, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
	)

	summary, err := AggregateWindow(selection, CurrentKnowledge(), []StateCandidate{
		stateCandidate(t, "status", "active", "observation-1", "evidence-1", activeTime, recordedAt, knowledge.StatusObserved),
		stateCandidate(t, "status", "paused", "observation-2", "evidence-2", pausedTime, recordedAt, knowledge.StatusObserved),
	})
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}

	if len(summary.Unresolved) != 1 || summary.Unresolved[0].Reason != UnresolvedTransition {
		t.Errorf("Unresolved = %v, want sequential state transition", summary.Unresolved)
	}
}

func TestAggregateWindowPreservesTemporalUncertainty(t *testing.T) {
	selection := aggregationWindow(t)
	recordedAt := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	uncertainTime := knowledge.UnknownTime()

	summary, err := AggregateWindow(selection, CurrentKnowledge(), []StateCandidate{
		stateCandidate(t, "status", "active", "observation-1", "evidence-1", uncertainTime, recordedAt, knowledge.StatusObserved),
	})
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}

	if len(summary.Unresolved) != 1 || summary.Unresolved[0].Reason != UnresolvedTemporalUncertainty {
		t.Errorf("Unresolved = %v, want temporal uncertainty", summary.Unresolved)
	}
}

func TestAggregateWindowDoesNotLetMatchingHypothesisUndermineSupportedState(t *testing.T) {
	selection := aggregationWindow(t)
	validTime := mustDuring(t,
		time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
	)
	recordedAt := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	candidates := []StateCandidate{
		stateCandidate(t, "status", "active", "observation-1", "evidence-1", validTime, recordedAt, knowledge.StatusObserved),
		stateCandidate(t, "status", "active", "observation-2", "evidence-2", validTime, recordedAt, knowledge.StatusHypothesized),
	}

	summary, err := AggregateWindow(selection, CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}

	if len(summary.Facts) != 1 || summary.Facts[0].Value != "active" {
		t.Errorf("Facts = %v, want supported active state", summary.Facts)
	}
	if !slices.Equal(summary.Facts[0].ObservationIDs, []knowledge.ObservationID{"observation-1"}) {
		t.Errorf("ObservationIDs = %v, want only supporting observation", summary.Facts[0].ObservationIDs)
	}
}

func TestAggregateAndCompareReconstructsRelationshipChange(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	recordedAt := time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC)
	firstStart, firstEnd, _ := windowA.Window()
	secondStart, secondEnd, _ := windowB.Window()
	candidates := []StateCandidate{
		stateCandidate(
			t,
			"relationship:entity-1:entity-2",
			"collaborator",
			"observation-1",
			"evidence-1",
			mustDuring(t, firstStart, firstEnd),
			recordedAt,
			knowledge.StatusObserved,
		),
		stateCandidate(
			t,
			"relationship:entity-1:entity-2",
			"partner",
			"observation-2",
			"evidence-2",
			mustDuring(t, secondStart, secondEnd),
			recordedAt,
			knowledge.StatusObserved,
		),
	}

	before, err := AggregateWindow(windowA, CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("AggregateWindow() before error = %v", err)
	}
	after, err := AggregateWindow(windowB, CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("AggregateWindow() after error = %v", err)
	}
	comparison, err := CompareWindowSummaries(before, after)
	if err != nil {
		t.Fatalf("CompareWindowSummaries() error = %v", err)
	}

	if len(comparison.Changes) != 1 {
		t.Fatalf("len(Changes) = %d, want 1", len(comparison.Changes))
	}
	change := comparison.Changes[0]
	if change.Kind != ChangeChanged || change.Before.Value != "collaborator" || change.After.Value != "partner" {
		t.Errorf("Change = %+v, want collaborator to partner", change)
	}
}

func aggregationWindow(t *testing.T) TemporalSelection {
	t.Helper()
	selection, err := Between(
		"Q2 2025",
		time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("Between() error = %v", err)
	}
	return selection
}

func mustDuring(t *testing.T, start, end time.Time) knowledge.TemporalExtent {
	t.Helper()
	extent, err := knowledge.During(start, end)
	if err != nil {
		t.Fatalf("knowledge.During() error = %v", err)
	}
	return extent
}

func stateCandidate(
	t *testing.T,
	key string,
	value string,
	observationID knowledge.ObservationID,
	evidenceID knowledge.EvidenceID,
	validTime knowledge.TemporalExtent,
	recordedAt time.Time,
	status knowledge.EpistemicStatus,
) StateCandidate {
	t.Helper()
	observation, err := knowledge.NewObservation(knowledge.ObservationInput{
		ID: observationID,
		Statement: knowledge.Statement{
			Subject:   "entity-1",
			Predicate: key,
			Object:    value,
		},
		ValidTime:   validTime,
		RecordedAt:  recordedAt,
		EvidenceIDs: []knowledge.EvidenceID{evidenceID},
		Derivation: knowledge.Derivation{
			Method:  "synthetic-test",
			Version: "v1",
		},
		Status: status,
	})
	if err != nil {
		t.Fatalf("knowledge.NewObservation() error = %v", err)
	}
	return StateCandidate{Key: key, Value: value, Observation: observation}
}
