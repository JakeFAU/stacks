package temporal_test

import (
	"slices"
	"testing"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

func TestCompareWindowSummariesComputesOrderedSemanticChanges(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	before := temporal.WindowSummary{
		Selection: windowA,
		Facts: []temporal.Fact{
			fact("relationship", "collaborator", "observation-1", "evidence-1"),
			fact("status", "prototype", "observation-2", "evidence-2"),
		},
	}
	after := temporal.WindowSummary{
		Selection: windowB,
		Facts: []temporal.Fact{
			fact("relationship", "partner", "observation-3", "evidence-3"),
			fact("priority", "high", "observation-4", "evidence-4"),
		},
	}
	comparison, err := temporal.CompareWindowSummaries(before, after)
	if err != nil {
		t.Fatalf("CompareWindowSummaries() error = %v", err)
	}
	wantKinds := []temporal.ChangeKind{temporal.ChangeAdded, temporal.ChangeChanged, temporal.ChangeRemoved}
	wantKeys := []string{"priority", "relationship", "status"}
	gotKinds := make([]temporal.ChangeKind, len(comparison.Changes))
	gotKeys := make([]string, len(comparison.Changes))
	for index, change := range comparison.Changes {
		gotKinds[index] = change.Kind
		gotKeys[index] = change.Key
	}
	if !slices.Equal(gotKinds, wantKinds) {
		t.Errorf("change kinds = %v, want %v", gotKinds, wantKinds)
	}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Errorf("change keys = %v, want %v", gotKeys, wantKeys)
	}
	if got := []string{comparison.BeforeFacts[0].Key, comparison.BeforeFacts[1].Key}; !slices.Equal(got, []string{"relationship", "status"}) {
		t.Errorf("BeforeFacts keys = %v, want ordered summarized state", got)
	}
}

func TestCompareWindowSummariesDoesNotInventChangesForUnresolvedKeys(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	before := temporal.WindowSummary{
		Selection: windowA,
		Facts:     []temporal.Fact{fact("status", "prototype", "observation-1", "evidence-1")},
		Unresolved: []temporal.UnresolvedFact{{
			Key:    "relationship",
			Reason: temporal.UnresolvedConflict,
			Candidates: []temporal.Fact{
				fact("relationship", "colleague", "observation-3", "evidence-3"),
				fact("relationship", "friend", "observation-5", "evidence-5"),
			},
		}},
	}
	after := temporal.WindowSummary{
		Selection: windowB,
		Facts:     []temporal.Fact{fact("status", "released", "observation-2", "evidence-2")},
		Unresolved: []temporal.UnresolvedFact{{
			Key:        "relationship",
			Reason:     temporal.UnresolvedHypothesis,
			Candidates: []temporal.Fact{fact("relationship", "partner", "observation-4", "evidence-4")},
		}},
	}
	comparison, err := temporal.CompareWindowSummaries(before, after)
	if err != nil {
		t.Fatalf("CompareWindowSummaries() error = %v", err)
	}
	if !slices.Equal(comparison.UnresolvedKeys, []string{"relationship"}) {
		t.Errorf("UnresolvedKeys = %v, want relationship", comparison.UnresolvedKeys)
	}
	if len(comparison.Changes) != 1 || comparison.Changes[0].Key != "status" {
		t.Errorf("Changes = %v, want only resolved status change", comparison.Changes)
	}
}

func TestCompareWindowSummariesPreservesCounterevidenceOnlyCandidate(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	candidate := temporal.Fact{
		Key:                      "status",
		Value:                    "active",
		ObservationIDs:           []observation.ObservationID{"observation-1"},
		ContradictingEvidenceIDs: []evidence.EvidenceID{"evidence-counter"},
	}
	before := temporal.WindowSummary{
		Selection: windowA,
		Unresolved: []temporal.UnresolvedFact{{
			Key:        "status",
			Reason:     temporal.UnresolvedCounterevidenceOnly,
			Candidates: []temporal.Fact{candidate},
		}},
	}
	comparison, err := temporal.CompareWindowSummaries(before, temporal.WindowSummary{Selection: windowB})
	if err != nil {
		t.Fatalf("CompareWindowSummaries() error = %v", err)
	}
	if !slices.Equal(comparison.UnresolvedKeys, []string{"status"}) {
		t.Errorf("UnresolvedKeys = %v, want status", comparison.UnresolvedKeys)
	}
	got := comparison.BeforeUnresolved[0].Candidates[0]
	if len(got.SupportingEvidenceIDs) != 0 ||
		!slices.Equal(got.ContradictingEvidenceIDs, []evidence.EvidenceID{"evidence-counter"}) {
		t.Errorf("candidate provenance = %+v", got)
	}
}

func TestCompareWindowSummariesClonesBothEvidenceRoleSlices(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	source := temporal.Fact{
		Key:                      "status",
		Value:                    "active",
		ObservationIDs:           []observation.ObservationID{"observation-1"},
		SupportingEvidenceIDs:    []evidence.EvidenceID{"evidence-support"},
		ContradictingEvidenceIDs: []evidence.EvidenceID{"evidence-counter"},
	}
	before := temporal.WindowSummary{Selection: windowA, Facts: []temporal.Fact{source}}
	comparison, err := temporal.CompareWindowSummaries(before, temporal.WindowSummary{Selection: windowB})
	if err != nil {
		t.Fatalf("CompareWindowSummaries() error = %v", err)
	}
	before.Facts[0].ObservationIDs[0] = "changed-observation"
	before.Facts[0].SupportingEvidenceIDs[0] = "changed-support"
	before.Facts[0].ContradictingEvidenceIDs[0] = "changed-counter"
	if !slices.Equal(comparison.BeforeFacts[0].ObservationIDs, []observation.ObservationID{"observation-1"}) ||
		!slices.Equal(comparison.BeforeFacts[0].SupportingEvidenceIDs, []evidence.EvidenceID{"evidence-support"}) ||
		!slices.Equal(comparison.BeforeFacts[0].ContradictingEvidenceIDs, []evidence.EvidenceID{"evidence-counter"}) {
		t.Errorf("BeforeFacts[0] changed with input: %+v", comparison.BeforeFacts[0])
	}
	if comparison.Changes[0].Before == nil {
		t.Fatal("removed change Before = nil")
	}
	comparison.BeforeFacts[0].SupportingEvidenceIDs[0] = "changed-comparison"
	if !slices.Equal(comparison.Changes[0].Before.SupportingEvidenceIDs, []evidence.EvidenceID{"evidence-support"}) {
		t.Errorf("change provenance aliases BeforeFacts: %+v", comparison.Changes[0].Before)
	}
}

func TestCompareWindowSummariesRequiresObservationAndEvidenceProvenance(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	tests := []temporal.Fact{
		{Key: "status", Value: "prototype"},
		{
			Key:                   "status",
			Value:                 "prototype",
			SupportingEvidenceIDs: []evidence.EvidenceID{"evidence-1"},
		},
		{
			Key:            "status",
			Value:          "prototype",
			ObservationIDs: []observation.ObservationID{"observation-1"},
		},
	}
	for _, invalid := range tests {
		if _, err := temporal.CompareWindowSummaries(
			temporal.WindowSummary{Selection: windowA, Facts: []temporal.Fact{invalid}},
			temporal.WindowSummary{Selection: windowB},
		); err == nil {
			t.Fatalf("CompareWindowSummaries(%+v) error = nil, want missing provenance error", invalid)
		}
	}
}

func TestCompareWindowSummariesRejectsLegacyUncitedResolvedFact(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	legacyUncited := temporal.Fact{
		Key:            "status",
		Value:          "prototype",
		ObservationIDs: []observation.ObservationID{"observation-1"},
		LegacyUncited:  true,
	}
	if _, err := temporal.CompareWindowSummaries(
		temporal.WindowSummary{Selection: windowA, Facts: []temporal.Fact{legacyUncited}},
		temporal.WindowSummary{Selection: windowB},
	); err == nil {
		t.Fatal("CompareWindowSummaries() error = nil, want resolved legacy-uncited fact error")
	}
}

func TestCompareWindowSummariesRejectsReversedWindows(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	if _, err := temporal.CompareWindowSummaries(
		temporal.WindowSummary{Selection: windowB},
		temporal.WindowSummary{Selection: windowA},
	); err == nil {
		t.Fatal("CompareWindowSummaries() error = nil, want reversed windows error")
	}
}

func fact(
	key string,
	value string,
	observationID observation.ObservationID,
	evidenceID evidence.EvidenceID,
) temporal.Fact {
	return temporal.Fact{
		Key:                   key,
		Value:                 value,
		ObservationIDs:        []observation.ObservationID{observationID},
		SupportingEvidenceIDs: []evidence.EvidenceID{evidenceID},
	}
}
