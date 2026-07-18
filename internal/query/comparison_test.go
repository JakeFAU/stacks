package query

import (
	"slices"
	"testing"

	"stacks/internal/knowledge"
)

func TestCompareWindowSummariesComputesOrderedSemanticChanges(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	before := WindowSummary{
		Selection: windowA,
		Facts: []Fact{
			fact("relationship", "collaborator", "observation-1", "evidence-1"),
			fact("status", "prototype", "observation-2", "evidence-2"),
		},
	}
	after := WindowSummary{
		Selection: windowB,
		Facts: []Fact{
			fact("relationship", "partner", "observation-3", "evidence-3"),
			fact("priority", "high", "observation-4", "evidence-4"),
		},
	}

	comparison, err := CompareWindowSummaries(before, after)
	if err != nil {
		t.Fatalf("CompareWindowSummaries() error = %v", err)
	}

	wantKinds := []ChangeKind{ChangeAdded, ChangeChanged, ChangeRemoved}
	wantKeys := []string{"priority", "relationship", "status"}
	gotKinds := make([]ChangeKind, len(comparison.Changes))
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
	before := WindowSummary{
		Selection: windowA,
		Facts:     []Fact{fact("status", "prototype", "observation-1", "evidence-1")},
		Unresolved: []UnresolvedFact{{
			Key:    "relationship",
			Reason: UnresolvedConflict,
			Candidates: []Fact{
				fact("relationship", "colleague", "observation-3", "evidence-3"),
				fact("relationship", "friend", "observation-5", "evidence-5"),
			},
		}},
	}
	after := WindowSummary{
		Selection: windowB,
		Facts:     []Fact{fact("status", "released", "observation-2", "evidence-2")},
		Unresolved: []UnresolvedFact{{
			Key:        "relationship",
			Reason:     UnresolvedHypothesis,
			Candidates: []Fact{fact("relationship", "partner", "observation-4", "evidence-4")},
		}},
	}

	comparison, err := CompareWindowSummaries(before, after)
	if err != nil {
		t.Fatalf("CompareWindowSummaries() error = %v", err)
	}

	if !slices.Equal(comparison.UnresolvedKeys, []string{"relationship"}) {
		t.Errorf("UnresolvedKeys = %v, want %v", comparison.UnresolvedKeys, []string{"relationship"})
	}
	if len(comparison.Changes) != 1 || comparison.Changes[0].Key != "status" {
		t.Errorf("Changes = %v, want only resolved status change", comparison.Changes)
	}
}

func TestCompareWindowSummariesRequiresProvenance(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	before := WindowSummary{
		Selection: windowA,
		Facts:     []Fact{{Key: "status", Value: "prototype"}},
	}
	after := WindowSummary{Selection: windowB}

	if _, err := CompareWindowSummaries(before, after); err == nil {
		t.Fatal("CompareWindowSummaries() error = nil, want missing provenance error")
	}
}

func TestCompareWindowSummariesRejectsReversedWindows(t *testing.T) {
	windowA, windowB := comparisonWindows(t)

	if _, err := CompareWindowSummaries(
		WindowSummary{Selection: windowB},
		WindowSummary{Selection: windowA},
	); err == nil {
		t.Fatal("CompareWindowSummaries() error = nil, want reversed windows error")
	}
}

func fact(key, value string, observationID knowledge.ObservationID, evidenceID knowledge.EvidenceID) Fact {
	return Fact{
		Key:            key,
		Value:          value,
		ObservationIDs: []knowledge.ObservationID{observationID},
		EvidenceIDs:    []knowledge.EvidenceID{evidenceID},
	}
}
