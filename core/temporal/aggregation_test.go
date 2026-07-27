package temporal_test

import (
	"slices"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

func TestAggregateWindowFiltersValidAndRecordedTime(t *testing.T) {
	selection := aggregationWindow(t)
	cutoff := time.Date(2025, time.April, 15, 0, 0, 0, 0, time.UTC)
	scope, err := temporal.KnownAsOf(cutoff)
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
	candidates := []temporal.StateCandidate{
		stateCandidate(t, "status", "active", "observation-1", supporting("evidence-1"), inside, cutoff.Add(-time.Hour), observation.StatusObserved),
		stateCandidate(t, "priority", "high", "observation-2", supporting("evidence-2"), inside, cutoff.Add(time.Hour), observation.StatusObserved),
		stateCandidate(t, "owner", "Jacob", "observation-3", supporting("evidence-3"), outside, cutoff.Add(-time.Hour), observation.StatusObserved),
	}
	summary, err := temporal.AggregateWindow(selection, scope, candidates)
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}
	if len(summary.Facts) != 1 || summary.Facts[0].Key.Predicate != "status" {
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
	first := stateCandidate(t, "status", "active", "observation-2", supporting("evidence-2"), validTime, recordedAt, observation.StatusObserved)
	second := stateCandidate(t, "status", "active", "observation-1", supporting("evidence-1"), validTime, recordedAt, observation.StatusInferred)
	summary, err := temporal.AggregateWindow(selection, temporal.CurrentKnowledge(), []temporal.StateCandidate{first, second, first})
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}
	if len(summary.Facts) != 1 {
		t.Fatalf("len(Facts) = %d, want 1", len(summary.Facts))
	}
	fact := summary.Facts[0]
	if !slices.Equal(fact.ObservationIDs, []observation.ObservationID{"observation-1", "observation-2"}) {
		t.Errorf("ObservationIDs = %v, want sorted unique IDs", fact.ObservationIDs)
	}
	if !slices.Equal(fact.SupportingEvidenceIDs, []evidence.EvidenceID{"evidence-1", "evidence-2"}) {
		t.Errorf("SupportingEvidenceIDs = %v, want sorted unique IDs", fact.SupportingEvidenceIDs)
	}
}

func TestAggregateWindowRejectsDivergentPayloadForSameObservationIDRegardlessOfOrder(t *testing.T) {
	validTime := mustDuring(t,
		time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
	)
	recordedAt := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	base := stateCandidate(
		t,
		"status",
		"active",
		"observation-1",
		supporting("evidence-1"),
		validTime,
		recordedAt,
		observation.StatusObserved,
	)
	confidence, err := observation.NewUnitIntervalConfidence(0.8)
	if err != nil {
		t.Fatalf("NewUnitIntervalConfidence() error = %v", err)
	}
	differentSubject, err := observation.NewTextTerm("entity-2")
	if err != nil {
		t.Fatalf("NewTextTerm() error = %v", err)
	}
	differentPredicate, err := observation.NewPredicate(" status ")
	if err != nil {
		t.Fatalf("NewPredicate() error = %v", err)
	}
	differentValidTime := mustDuring(t,
		time.Date(2025, time.April, 2, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
	)
	tests := []struct {
		name   string
		mutate func(*observation.ObservationInput)
	}{
		{
			name: "evidence",
			mutate: func(input *observation.ObservationInput) {
				input.Evidence = supporting("evidence-2")
			},
		},
		{
			name: "statement",
			mutate: func(input *observation.ObservationInput) {
				input.Statement.Subject = differentSubject
			},
		},
		{
			name: "predicate bytes",
			mutate: func(input *observation.ObservationInput) {
				input.Statement.Predicate = differentPredicate
			},
		},
		{
			name: "valid time",
			mutate: func(input *observation.ObservationInput) {
				input.ValidTime = differentValidTime
			},
		},
		{
			name: "recorded time",
			mutate: func(input *observation.ObservationInput) {
				input.RecordedAt = recordedAt.Add(time.Second)
			},
		},
		{
			name: "derivation",
			mutate: func(input *observation.ObservationInput) {
				input.Derivation.RunID = "different-run"
			},
		},
		{
			name: "derivation bytes",
			mutate: func(input *observation.ObservationInput) {
				input.Derivation.Method = " synthetic-test "
			},
		},
		{
			name: "status",
			mutate: func(input *observation.ObservationInput) {
				input.Status = observation.StatusInferred
			},
		},
		{
			name: "confidence",
			mutate: func(input *observation.ObservationInput) {
				input.Confidence = &confidence
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			different := replaceCandidateObservation(t, base, test.mutate)
			var messages []string
			for _, candidates := range [][]temporal.StateCandidate{
				{base, different},
				{different, base},
			} {
				_, err := temporal.AggregateWindow(aggregationWindow(t), temporal.CurrentKnowledge(), candidates)
				if err == nil {
					t.Fatal("AggregateWindow() error = nil, want divergent stable-ID payload error")
				}
				messages = append(messages, err.Error())
			}
			if messages[0] != messages[1] {
				t.Errorf("order-reversed errors = %q and %q, want deterministic error", messages[0], messages[1])
			}
		})
	}
}

func TestAggregateWindowPreservesConflictingProvenance(t *testing.T) {
	selection := aggregationWindow(t)
	validTime := mustDuring(t,
		time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
	)
	recordedAt := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	candidates := []temporal.StateCandidate{
		stateCandidate(t, "status", "active", "observation-1", supporting("evidence-1"), validTime, recordedAt, observation.StatusObserved),
		stateCandidate(t, "status", "paused", "observation-2", supporting("evidence-2"), validTime, recordedAt, observation.StatusObserved),
	}
	summary, err := temporal.AggregateWindow(selection, temporal.CurrentKnowledge(), candidates)
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
	if unresolved.Reason != temporal.UnresolvedConflict {
		t.Errorf("Reason = %q, want %q", unresolved.Reason, temporal.UnresolvedConflict)
	}
	if got := []string{termText(t, unresolved.Candidates[0].Value), termText(t, unresolved.Candidates[1].Value)}; !slices.Equal(got, []string{"active", "paused"}) {
		t.Errorf("candidate values = %v, want ordered conflicting values", got)
	}
}

func TestAggregateWindowKeepsSubstantiveConflictWhenCounterevidenceOnlyCandidateExists(t *testing.T) {
	selection := aggregationWindow(t)
	validTime := mustDuring(t,
		time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
	)
	recordedAt := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	summary, err := temporal.AggregateWindow(selection, temporal.CurrentKnowledge(), []temporal.StateCandidate{
		stateCandidate(t, "status", "active", "observation-active", supporting("evidence-active"), validTime, recordedAt, observation.StatusObserved),
		stateCandidate(t, "status", "paused", "observation-paused", supporting("evidence-paused"), validTime, recordedAt, observation.StatusObserved),
		stateCandidate(
			t,
			"status",
			"active",
			"observation-counter",
			[]observation.EvidenceLink{{EvidenceID: "evidence-counter", Role: observation.EvidenceContradicting}},
			validTime,
			recordedAt,
			observation.StatusObserved,
		),
	})
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}
	if len(summary.Facts) != 0 || len(summary.Unresolved) != 1 {
		t.Fatalf("summary = %+v, want one unresolved conflict", summary)
	}
	unresolved := summary.Unresolved[0]
	if unresolved.Reason != temporal.UnresolvedConflict {
		t.Fatalf("Reason = %q, want %q", unresolved.Reason, temporal.UnresolvedConflict)
	}
	if len(unresolved.Candidates) != 2 {
		t.Fatalf("Candidates = %+v, want active and paused", unresolved.Candidates)
	}
	active := unresolved.Candidates[0]
	if termText(t, active.Value) != "active" ||
		!slices.Equal(active.ObservationIDs, []observation.ObservationID{"observation-active", "observation-counter"}) ||
		!slices.Equal(active.SupportingEvidenceIDs, []evidence.EvidenceID{"evidence-active"}) ||
		!slices.Equal(active.ContradictingEvidenceIDs, []evidence.EvidenceID{"evidence-counter"}) {
		t.Errorf("active candidate = %+v, want merged support and counterevidence provenance", active)
	}

	afterSelection, err := temporal.Between(
		"Q3 2025",
		time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.October, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("Between(Q3) error = %v", err)
	}
	comparison, err := temporal.CompareWindowSummaries(
		summary,
		temporal.WindowSummary{Selection: afterSelection},
	)
	if err != nil {
		t.Fatalf("CompareWindowSummaries() error = %v", err)
	}
	comparedActive := comparison.BeforeUnresolved[0].Candidates[0]
	if !slices.Equal(comparedActive.SupportingEvidenceIDs, []evidence.EvidenceID{"evidence-active"}) ||
		!slices.Equal(comparedActive.ContradictingEvidenceIDs, []evidence.EvidenceID{"evidence-counter"}) {
		t.Errorf("compared active provenance = %+v, want both evidence roles", comparedActive)
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
	summary, err := temporal.AggregateWindow(selection, temporal.CurrentKnowledge(), []temporal.StateCandidate{
		stateCandidate(t, "status", "active", "observation-1", supporting("evidence-1"), activeTime, recordedAt, observation.StatusObserved),
		stateCandidate(t, "status", "paused", "observation-2", supporting("evidence-2"), pausedTime, recordedAt, observation.StatusObserved),
	})
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}
	if len(summary.Unresolved) != 1 || summary.Unresolved[0].Reason != temporal.UnresolvedTransition {
		t.Errorf("Unresolved = %v, want sequential state transition", summary.Unresolved)
	}
}

func TestAggregateWindowTreatsInstantAtIntervalStartAsConflict(t *testing.T) {
	selection := aggregationWindow(t)
	intervalStart := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	interval := mustDuring(t, intervalStart, intervalStart.AddDate(0, 1, 0))
	instant, err := observation.AtTime(intervalStart)
	if err != nil {
		t.Fatalf("observation.AtTime() error = %v", err)
	}
	recordedAt := intervalStart.Add(time.Hour)

	summary, err := temporal.AggregateWindow(selection, temporal.CurrentKnowledge(), []temporal.StateCandidate{
		stateCandidate(t, "status", "active", "observation-interval", supporting("evidence-interval"), interval, recordedAt, observation.StatusObserved),
		stateCandidate(t, "status", "paused", "observation-instant", supporting("evidence-instant"), instant, recordedAt, observation.StatusObserved),
	})
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}
	if len(summary.Unresolved) != 1 || summary.Unresolved[0].Reason != temporal.UnresolvedConflict {
		t.Errorf("Unresolved = %v, want instant at inclusive interval start classified as conflict", summary.Unresolved)
	}
}

func TestAggregateWindowPreservesTemporalUncertainty(t *testing.T) {
	selection := aggregationWindow(t)
	recordedAt := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	summary, err := temporal.AggregateWindow(selection, temporal.CurrentKnowledge(), []temporal.StateCandidate{
		stateCandidate(t, "status", "active", "observation-1", supporting("evidence-1"), observation.UnknownTime(), recordedAt, observation.StatusObserved),
	})
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}
	if len(summary.Unresolved) != 1 || summary.Unresolved[0].Reason != temporal.UnresolvedTemporalUncertainty {
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
	candidates := []temporal.StateCandidate{
		stateCandidate(t, "status", "active", "observation-1", supporting("evidence-1"), validTime, recordedAt, observation.StatusObserved),
		stateCandidate(t, "status", "active", "observation-2", supporting("evidence-2"), validTime, recordedAt, observation.StatusHypothesized),
	}
	summary, err := temporal.AggregateWindow(selection, temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}
	if len(summary.Facts) != 1 || termText(t, summary.Facts[0].Value) != "active" {
		t.Errorf("Facts = %v, want supported active state", summary.Facts)
	}
	if !slices.Equal(summary.Facts[0].ObservationIDs, []observation.ObservationID{"observation-1"}) {
		t.Errorf("ObservationIDs = %v, want only supporting observation", summary.Facts[0].ObservationIDs)
	}
}

func TestAggregateWindowTreatsValidatedStatusesAsSupported(t *testing.T) {
	validTime := mustDuring(t,
		time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
	)
	recordedAt := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	for _, status := range []observation.EpistemicStatus{
		observation.StatusValidatedStructurally,
		observation.StatusValidatedEmpirically,
	} {
		t.Run(string(status), func(t *testing.T) {
			summary, err := temporal.AggregateWindow(aggregationWindow(t), temporal.CurrentKnowledge(), []temporal.StateCandidate{
				stateCandidate(t, "status", "active", "observation-1", supporting("evidence-1"), validTime, recordedAt, status),
			})
			if err != nil {
				t.Fatalf("AggregateWindow() error = %v", err)
			}
			if len(summary.Facts) != 1 {
				t.Fatalf("Facts = %v, want validated observation promoted", summary.Facts)
			}
		})
	}
}

func TestAggregateWindowPreservesEvidenceRolesAndIgnoresInputOrder(t *testing.T) {
	validTime := mustDuring(t,
		time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
	)
	recordedAt := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	links := []observation.EvidenceLink{
		{EvidenceID: "evidence-shared", Role: observation.EvidenceContradicting},
		{EvidenceID: "evidence-z", Role: observation.EvidenceSupporting},
		{EvidenceID: "evidence-shared", Role: observation.EvidenceSupporting},
		{EvidenceID: "evidence-a", Role: observation.EvidenceContradicting},
	}
	first := stateCandidate(t, "status", "active", "observation-1", links, validTime, recordedAt, observation.StatusObserved)
	reversed := slices.Clone(links)
	slices.Reverse(reversed)
	second := stateCandidate(t, "status", "active", "observation-2", reversed, validTime, recordedAt, observation.StatusObserved)

	summaryA, err := temporal.AggregateWindow(aggregationWindow(t), temporal.CurrentKnowledge(), []temporal.StateCandidate{first, second})
	if err != nil {
		t.Fatalf("AggregateWindow(A) error = %v", err)
	}
	summaryB, err := temporal.AggregateWindow(aggregationWindow(t), temporal.CurrentKnowledge(), []temporal.StateCandidate{second, first})
	if err != nil {
		t.Fatalf("AggregateWindow(B) error = %v", err)
	}
	if len(summaryA.Facts) != 1 || len(summaryB.Facts) != 1 {
		t.Fatalf("Facts lengths = (%d, %d), want (1, 1)", len(summaryA.Facts), len(summaryB.Facts))
	}
	if !slices.Equal(summaryA.Facts[0].SupportingEvidenceIDs, []evidence.EvidenceID{"evidence-shared", "evidence-z"}) {
		t.Errorf("SupportingEvidenceIDs = %v", summaryA.Facts[0].SupportingEvidenceIDs)
	}
	if !slices.Equal(summaryA.Facts[0].ContradictingEvidenceIDs, []evidence.EvidenceID{"evidence-a", "evidence-shared"}) {
		t.Errorf("ContradictingEvidenceIDs = %v", summaryA.Facts[0].ContradictingEvidenceIDs)
	}
	if !slices.Equal(summaryA.Facts[0].SupportingEvidenceIDs, summaryB.Facts[0].SupportingEvidenceIDs) ||
		!slices.Equal(summaryA.Facts[0].ContradictingEvidenceIDs, summaryB.Facts[0].ContradictingEvidenceIDs) {
		t.Errorf("aggregation differs by input order: A=%+v B=%+v", summaryA.Facts[0], summaryB.Facts[0])
	}
}

func TestAggregateWindowKeepsCounterevidenceOnlyCandidateUnresolved(t *testing.T) {
	validTime := mustDuring(t,
		time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
	)
	candidate := stateCandidate(
		t,
		"status",
		"active",
		"observation-1",
		[]observation.EvidenceLink{{EvidenceID: "evidence-counter", Role: observation.EvidenceContradicting}},
		validTime,
		time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC),
		observation.StatusObserved,
	)
	summary, err := temporal.AggregateWindow(aggregationWindow(t), temporal.CurrentKnowledge(), []temporal.StateCandidate{candidate})
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}
	if len(summary.Facts) != 0 {
		t.Fatalf("Facts = %v, want no counterevidence-only promotion", summary.Facts)
	}
	if len(summary.Unresolved) != 1 || summary.Unresolved[0].Reason != temporal.UnresolvedCounterevidenceOnly {
		t.Fatalf("Unresolved = %v, want counterevidence-only candidate", summary.Unresolved)
	}
	got := summary.Unresolved[0].Candidates[0]
	if len(got.SupportingEvidenceIDs) != 0 ||
		!slices.Equal(got.ContradictingEvidenceIDs, []evidence.EvidenceID{"evidence-counter"}) {
		t.Errorf("counterevidence provenance = %+v", got)
	}
}

func TestAggregateWindowNeverUsesConfidenceToChooseTruth(t *testing.T) {
	validTime := mustDuring(t,
		time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
	)
	low := stateCandidate(t, "status", "active", "observation-low", supporting("evidence-low"), validTime, time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC), observation.StatusObserved)
	high := stateCandidate(t, "status", "paused", "observation-high", supporting("evidence-high"), validTime, time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC), observation.StatusObserved)
	low.Observation = withConfidence(t, low.Observation, 0.01)
	high.Observation = withConfidence(t, high.Observation, 0.99)

	summary, err := temporal.AggregateWindow(aggregationWindow(t), temporal.CurrentKnowledge(), []temporal.StateCandidate{low, high})
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}
	if len(summary.Facts) != 0 || len(summary.Unresolved) != 1 || summary.Unresolved[0].Reason != temporal.UnresolvedConflict {
		t.Errorf("summary = %+v, want unresolved conflict independent of confidence", summary)
	}
}

func TestAggregateAndCompareReconstructsRelationshipChange(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	recordedAt := time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC)
	firstStart, firstEnd, _ := windowA.Window()
	secondStart, secondEnd, _ := windowB.Window()
	candidates := []temporal.StateCandidate{
		stateCandidate(t, "relationship:entity-1:entity-2", "collaborator", "observation-1", supporting("evidence-1"), mustDuring(t, firstStart, firstEnd), recordedAt, observation.StatusObserved),
		stateCandidate(t, "relationship:entity-1:entity-2", "partner", "observation-2", supporting("evidence-2"), mustDuring(t, secondStart, secondEnd), recordedAt, observation.StatusObserved),
	}
	before, err := temporal.AggregateWindow(windowA, temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("AggregateWindow() before error = %v", err)
	}
	after, err := temporal.AggregateWindow(windowB, temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("AggregateWindow() after error = %v", err)
	}
	comparison, err := temporal.CompareWindowSummaries(before, after)
	if err != nil {
		t.Fatalf("CompareWindowSummaries() error = %v", err)
	}
	if len(comparison.Changes) != 1 {
		t.Fatalf("len(Changes) = %d, want 1", len(comparison.Changes))
	}
	change := comparison.Changes[0]
	if change.Kind != temporal.ChangeChanged || termText(t, change.Before.Value) != "collaborator" || termText(t, change.After.Value) != "partner" {
		t.Errorf("Change = %+v, want collaborator to partner", change)
	}
}

func TestAggregateWindowKeepsTypedEntityAndTextTermsDistinct(t *testing.T) {
	entity := entityTerm(t, "same:/雪", "")
	text := textTerm(t, "same:/雪")
	key := stateKeyFor(t, textTerm(t, " subject: /雪 "), "status: / ")
	validTime := mustDuring(t, time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC))
	recordedAt := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	summary, err := temporal.AggregateWindow(aggregationWindow(t), temporal.CurrentKnowledge(), []temporal.StateCandidate{
		typedCandidate(t, key, entity, key.Subject, key.Predicate, entity, "entity", validTime, recordedAt),
		typedCandidate(t, key, text, key.Subject, key.Predicate, text, "text", validTime, recordedAt),
	})
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}
	if len(summary.Facts) != 0 || len(summary.Unresolved) != 1 {
		t.Fatalf("summary = %+v, want unresolved typed conflict", summary)
	}
	if subject, ok := summary.Unresolved[0].Key.Subject.Text(); !ok || subject != " subject: /雪 " || summary.Unresolved[0].Key.Predicate != "status: / " {
		t.Errorf("state key = %+v, want exact whitespace-preserving text and predicate", summary.Unresolved[0].Key)
	}
	got := summary.Unresolved[0].Candidates
	if len(got) != 2 || got[0].Value.Kind() != observation.TermText || got[1].Value.Kind() != observation.TermEntity {
		t.Errorf("candidate kinds = %v, want text then entity with equal visible bytes", []observation.TermKind{got[0].Value.Kind(), got[1].Value.Kind()})
	}
}

func TestAggregateWindowIgnoresGroundingMentionForEntitySemanticIdentity(t *testing.T) {
	canonicalSubject := entityTerm(t, "entity:/雪", "")
	value := textTerm(t, "active")
	predicate, err := observation.NewPredicate("status")
	if err != nil {
		t.Fatalf("NewPredicate() error = %v", err)
	}
	key, err := temporal.NewStateKey(canonicalSubject, predicate)
	if err != nil {
		t.Fatalf("NewStateKey() error = %v", err)
	}
	firstMention := mentionTerm(t, "mention:1 /雪")
	secondMention := mentionTerm(t, "mention:2 /雪")
	validTime := mustDuring(t, time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC))
	recordedAt := time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC)
	summary, err := temporal.AggregateWindow(aggregationWindow(t), temporal.CurrentKnowledge(), []temporal.StateCandidate{
		typedCandidate(t, key, value, firstMention, predicate, value, "first", validTime, recordedAt, withSubjectGrounding("mention:1 /雪")),
		typedCandidate(t, key, value, secondMention, predicate, value, "second", validTime, recordedAt, withSubjectGrounding("mention:2 /雪")),
	})
	if err != nil {
		t.Fatalf("AggregateWindow() error = %v", err)
	}
	if len(summary.Facts) != 1 || temporal.CompareTerms(summary.Facts[0].Key.Subject, canonicalSubject) != 0 {
		t.Fatalf("Facts = %+v, want one entity-keyed fact", summary.Facts)
	}
	entityID, groundingMentionID, ok := summary.Facts[0].Key.Subject.Entity()
	if !ok || entityID != "entity:/雪" || groundingMentionID != "" {
		t.Errorf("fact subject = (%q, %q, %t), want ungrounded canonical entity", entityID, groundingMentionID, ok)
	}
	if !slices.Equal(summary.Facts[0].ObservationIDs, []observation.ObservationID{"first", "second"}) {
		t.Errorf("ObservationIDs = %v, want contributions from both grounded mentions", summary.Facts[0].ObservationIDs)
	}
	ungrounded := typedCandidate(t, key, value, firstMention, predicate, value, "ungrounded", validTime, recordedAt)
	if _, err := temporal.AggregateWindow(aggregationWindow(t), temporal.CurrentKnowledge(), []temporal.StateCandidate{ungrounded}); err == nil {
		t.Fatal("AggregateWindow() error = nil, want missing grounding mention rejection")
	}
}

func TestAggregateWindowRejectsCandidateThatDoesNotMatchObservationStatement(t *testing.T) {
	key := stateKeyFor(t, textTerm(t, "entity-1"), "status")
	active := textTerm(t, "active")
	paused := textTerm(t, "paused")
	validTime := mustDuring(t, time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC))
	candidate := typedCandidate(t, key, paused, key.Subject, key.Predicate, active, "mismatch", validTime, time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC))
	if _, err := temporal.AggregateWindow(aggregationWindow(t), temporal.CurrentKnowledge(), []temporal.StateCandidate{candidate}); err == nil {
		t.Fatal("AggregateWindow() error = nil, want statement mapping error")
	}
}

func TestStateKeyNeverDependsOnStringDelimiterEncoding(t *testing.T) {
	first := stateKeyFor(t, textTerm(t, "subject:part /雪"), "predicate")
	second := stateKeyFor(t, textTerm(t, "subject"), "part /雪:predicate")
	if temporal.CompareStateKeys(first, second) == 0 {
		t.Fatal("CompareStateKeys() = 0, want distinct typed keys")
	}
}

func aggregationWindow(t *testing.T) temporal.TemporalSelection {
	t.Helper()
	selection, err := temporal.Between(
		"Q2 2025",
		time.Date(2025, time.April, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("Between() error = %v", err)
	}
	return selection
}

func mustDuring(t *testing.T, start, end time.Time) observation.TemporalExtent {
	t.Helper()
	extent, err := observation.During(start, end)
	if err != nil {
		t.Fatalf("observation.During() error = %v", err)
	}
	return extent
}

func supporting(id evidence.EvidenceID) []observation.EvidenceLink {
	return []observation.EvidenceLink{{EvidenceID: id, Role: observation.EvidenceSupporting}}
}

func stateCandidate(
	t *testing.T,
	key string,
	value string,
	observationID observation.ObservationID,
	evidenceLinks []observation.EvidenceLink,
	validTime observation.TemporalExtent,
	recordedAt time.Time,
	status observation.EpistemicStatus,
) temporal.StateCandidate {
	t.Helper()
	subject, err := observation.NewTextTerm("entity-1")
	if err != nil {
		t.Fatalf("NewTextTerm(subject) error = %v", err)
	}
	object, err := observation.NewTextTerm(value)
	if err != nil {
		t.Fatalf("NewTextTerm(object) error = %v", err)
	}
	predicate, err := observation.NewPredicate(key)
	if err != nil {
		t.Fatalf("NewPredicate() error = %v", err)
	}
	valueObservation, err := observation.NewObservation(observation.ObservationInput{
		ID: observationID,
		Statement: observation.Statement{
			Subject:   subject,
			Predicate: predicate,
			Object:    object,
		},
		ValidTime:  validTime,
		RecordedAt: recordedAt,
		Evidence:   evidenceLinks,
		Derivation: observation.Derivation{
			Method:  "synthetic-test",
			Version: "v1",
		},
		Status: status,
	})
	if err != nil {
		t.Fatalf("observation.NewObservation() error = %v", err)
	}
	return temporal.StateCandidate{
		Key:         temporal.StateKey{Subject: subject, Predicate: predicate},
		Value:       object,
		Observation: valueObservation,
	}
}

func textTerm(t *testing.T, value string) observation.Term {
	t.Helper()
	term, err := observation.NewTextTerm(value)
	if err != nil {
		t.Fatalf("NewTextTerm(%q) error = %v", value, err)
	}
	return term
}

func mentionTerm(t *testing.T, mentionID string) observation.Term {
	t.Helper()
	term, err := observation.NewMentionTerm(mentionID)
	if err != nil {
		t.Fatalf("NewMentionTerm(%q) error = %v", mentionID, err)
	}
	return term
}

func entityTerm(t *testing.T, entityID, groundingMentionID string) observation.Term {
	t.Helper()
	term, err := observation.NewEntityTerm(entityID, groundingMentionID)
	if err != nil {
		t.Fatalf("NewEntityTerm(%q, %q) error = %v", entityID, groundingMentionID, err)
	}
	return term
}

func stateKeyFor(t *testing.T, subject observation.Term, predicateValue string) temporal.StateKey {
	t.Helper()
	predicate, err := observation.NewPredicate(predicateValue)
	if err != nil {
		t.Fatalf("NewPredicate(%q) error = %v", predicateValue, err)
	}
	key, err := temporal.NewStateKey(subject, predicate)
	if err != nil {
		t.Fatalf("NewStateKey() error = %v", err)
	}
	return key
}

type candidateOption func(*temporal.StateCandidate)

func withSubjectGrounding(mentionID string) candidateOption {
	return func(candidate *temporal.StateCandidate) {
		candidate.SubjectGroundingMentionID = mentionID
	}
}

func typedCandidate(
	t *testing.T,
	key temporal.StateKey,
	value, subject observation.Term,
	predicate observation.Predicate,
	object observation.Term,
	id observation.ObservationID,
	validTime observation.TemporalExtent,
	recordedAt time.Time,
	options ...candidateOption,
) temporal.StateCandidate {
	t.Helper()
	valueObservation, err := observation.NewObservation(observation.ObservationInput{
		ID:         id,
		Statement:  observation.Statement{Subject: subject, Predicate: predicate, Object: object},
		ValidTime:  validTime,
		RecordedAt: recordedAt,
		Evidence:   supporting(evidence.EvidenceID("evidence-" + string(id))),
		Derivation: observation.Derivation{Method: "synthetic-test", Version: "v1"},
		Status:     observation.StatusObserved,
	})
	if err != nil {
		t.Fatalf("NewObservation() error = %v", err)
	}
	candidate := temporal.StateCandidate{Key: key, Value: value, Observation: valueObservation}
	for _, option := range options {
		option(&candidate)
	}
	return candidate
}

func termText(t *testing.T, term observation.Term) string {
	t.Helper()
	value, ok := term.Text()
	if !ok {
		t.Fatalf("term kind = %d, want text", term.Kind())
	}
	return value
}

func withConfidence(t *testing.T, source observation.Observation, value float64) observation.Observation {
	t.Helper()
	confidence, err := observation.NewUnitIntervalConfidence(value)
	if err != nil {
		t.Fatalf("NewUnitIntervalConfidence() error = %v", err)
	}
	return rebuildObservation(t, source, &confidence)
}

func rebuildObservation(t *testing.T, source observation.Observation, confidence *observation.Confidence) observation.Observation {
	t.Helper()
	result, err := observation.NewObservation(observation.ObservationInput{
		ID:         source.ID(),
		Statement:  source.Statement(),
		ValidTime:  source.ValidTime(),
		RecordedAt: source.RecordedAt(),
		Evidence:   source.EvidenceLinks(),
		Derivation: source.Derivation(),
		Status:     source.Status(),
		Confidence: confidence,
	})
	if err != nil {
		t.Fatalf("NewObservation(rebuild) error = %v", err)
	}
	return result
}

func replaceCandidateObservation(
	t *testing.T,
	source temporal.StateCandidate,
	mutate func(*observation.ObservationInput),
) temporal.StateCandidate {
	t.Helper()
	confidence, hasConfidence := source.Observation.Confidence()
	var confidencePointer *observation.Confidence
	if hasConfidence {
		confidencePointer = &confidence
	}
	input := observation.ObservationInput{
		ID:         source.Observation.ID(),
		Statement:  source.Observation.Statement(),
		ValidTime:  source.Observation.ValidTime(),
		RecordedAt: source.Observation.RecordedAt(),
		Evidence:   source.Observation.EvidenceLinks(),
		Derivation: source.Observation.Derivation(),
		Status:     source.Observation.Status(),
		Confidence: confidencePointer,
	}
	mutate(&input)
	replacement, err := observation.NewObservation(input)
	if err != nil {
		t.Fatalf("NewObservation(replacement) error = %v", err)
	}
	source.Observation = replacement
	return source
}
