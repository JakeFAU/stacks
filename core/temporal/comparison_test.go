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
			fact(t, "relationship", "collaborator", "observation-1", "evidence-1"),
			fact(t, "status", "prototype", "observation-2", "evidence-2"),
		},
	}
	after := temporal.WindowSummary{
		Selection: windowB,
		Facts: []temporal.Fact{
			fact(t, "relationship", "partner", "observation-3", "evidence-3"),
			fact(t, "priority", "high", "observation-4", "evidence-4"),
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
		gotKeys[index] = string(change.Key.Predicate)
	}
	if !slices.Equal(gotKinds, wantKinds) {
		t.Errorf("change kinds = %v, want %v", gotKinds, wantKinds)
	}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Errorf("change keys = %v, want %v", gotKeys, wantKeys)
	}
	if got := []string{string(comparison.BeforeFacts[0].Key.Predicate), string(comparison.BeforeFacts[1].Key.Predicate)}; !slices.Equal(got, []string{"relationship", "status"}) {
		t.Errorf("BeforeFacts keys = %v, want ordered summarized state", got)
	}
}

func TestCompareWindowSummariesDoesNotInventChangesForUnresolvedKeys(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	before := temporal.WindowSummary{
		Selection: windowA,
		Facts:     []temporal.Fact{fact(t, "status", "prototype", "observation-1", "evidence-1")},
		Unresolved: []temporal.UnresolvedFact{{
			Key:    stateKey(t, "relationship"),
			Reason: temporal.UnresolvedConflict,
			Candidates: []temporal.Fact{
				fact(t, "relationship", "colleague", "observation-3", "evidence-3"),
				fact(t, "relationship", "friend", "observation-5", "evidence-5"),
			},
		}},
	}
	after := temporal.WindowSummary{
		Selection: windowB,
		Facts:     []temporal.Fact{fact(t, "status", "released", "observation-2", "evidence-2")},
		Unresolved: []temporal.UnresolvedFact{{
			Key:        stateKey(t, "relationship"),
			Reason:     temporal.UnresolvedHypothesis,
			Candidates: []temporal.Fact{fact(t, "relationship", "partner", "observation-4", "evidence-4")},
		}},
	}
	comparison, err := temporal.CompareWindowSummaries(before, after)
	if err != nil {
		t.Fatalf("CompareWindowSummaries() error = %v", err)
	}
	if got := []string{string(comparison.UnresolvedKeys[0].Predicate)}; !slices.Equal(got, []string{"relationship"}) {
		t.Errorf("UnresolvedKeys = %v, want relationship", comparison.UnresolvedKeys)
	}
	if len(comparison.Changes) != 1 || comparison.Changes[0].Key.Predicate != "status" {
		t.Errorf("Changes = %v, want only resolved status change", comparison.Changes)
	}
}

func TestCompareWindowSummariesPreservesCounterevidenceOnlyCandidate(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	candidate := temporal.Fact{
		Key:                      stateKey(t, "status"),
		Value:                    textTerm(t, "active"),
		ObservationIDs:           []observation.ObservationID{"observation-1"},
		ContradictingEvidenceIDs: []evidence.EvidenceID{"evidence-counter"},
	}
	before := temporal.WindowSummary{
		Selection: windowA,
		Unresolved: []temporal.UnresolvedFact{{
			Key:        stateKey(t, "status"),
			Reason:     temporal.UnresolvedCounterevidenceOnly,
			Candidates: []temporal.Fact{candidate},
		}},
	}
	comparison, err := temporal.CompareWindowSummaries(before, temporal.WindowSummary{Selection: windowB})
	if err != nil {
		t.Fatalf("CompareWindowSummaries() error = %v", err)
	}
	if got := []string{string(comparison.UnresolvedKeys[0].Predicate)}; !slices.Equal(got, []string{"status"}) {
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
		Key:                      stateKey(t, "status"),
		Value:                    textTerm(t, "active"),
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
		{Key: stateKey(t, "status"), Value: textTerm(t, "prototype")},
		{
			Key:                   stateKey(t, "status"),
			Value:                 textTerm(t, "prototype"),
			SupportingEvidenceIDs: []evidence.EvidenceID{"evidence-1"},
		},
		{
			Key:            stateKey(t, "status"),
			Value:          textTerm(t, "prototype"),
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

func TestCompareWindowSummariesRejectsReversedWindows(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	if _, err := temporal.CompareWindowSummaries(
		temporal.WindowSummary{Selection: windowB},
		temporal.WindowSummary{Selection: windowA},
	); err == nil {
		t.Fatal("CompareWindowSummaries() error = nil, want reversed windows error")
	}
}

func TestCompareWindowSummariesOrdersTypedKeysAndValuesDeterministically(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	unresolvedKey := stateKey(t, "status:/雪")
	textKey := stateKey(t, "priority:/雪")
	entitySubject, err := observation.NewEntityTerm("entity:/雪", "")
	if err != nil {
		t.Fatalf("NewEntityTerm() error = %v", err)
	}
	entityPredicate, err := observation.NewPredicate("status:/雪")
	if err != nil {
		t.Fatalf("NewPredicate() error = %v", err)
	}
	entityKey, err := temporal.NewStateKey(entitySubject, entityPredicate)
	if err != nil {
		t.Fatalf("NewStateKey() error = %v", err)
	}
	textValue := textTerm(t, "same: /雪")
	entityValue, err := observation.NewEntityTerm("same: /雪", "")
	if err != nil {
		t.Fatalf("NewEntityTerm(value) error = %v", err)
	}
	before := temporal.WindowSummary{
		Selection: windowA,
		Facts: []temporal.Fact{
			{Key: entityKey, Value: textTerm(t, "active"), ObservationIDs: []observation.ObservationID{"entity"}, SupportingEvidenceIDs: []evidence.EvidenceID{"e-entity"}},
			{Key: textKey, Value: textTerm(t, "active"), ObservationIDs: []observation.ObservationID{"text"}, SupportingEvidenceIDs: []evidence.EvidenceID{"e-text"}},
		},
		Unresolved: []temporal.UnresolvedFact{{
			Key: unresolvedKey, Reason: temporal.UnresolvedConflict,
			Candidates: []temporal.Fact{
				{Key: unresolvedKey, Value: entityValue, ObservationIDs: []observation.ObservationID{"entity-value"}, SupportingEvidenceIDs: []evidence.EvidenceID{"e-entity-value"}},
				{Key: unresolvedKey, Value: textValue, ObservationIDs: []observation.ObservationID{"text-value"}, SupportingEvidenceIDs: []evidence.EvidenceID{"e-text-value"}},
			},
		}},
	}
	comparison, err := temporal.CompareWindowSummaries(before, temporal.WindowSummary{Selection: windowB})
	if err != nil {
		t.Fatalf("CompareWindowSummaries() error = %v", err)
	}
	if len(comparison.BeforeFacts) != 2 || comparison.BeforeFacts[0].Key != textKey || comparison.BeforeFacts[1].Key != entityKey {
		t.Errorf("BeforeFacts = %+v, want text then entity key ordering", comparison.BeforeFacts)
	}
	if len(comparison.BeforeUnresolved) != 1 || len(comparison.BeforeUnresolved[0].Candidates) != 2 || comparison.BeforeUnresolved[0].Candidates[0].Value.Kind() != observation.TermText || comparison.BeforeUnresolved[0].Candidates[1].Value.Kind() != observation.TermEntity {
		t.Errorf("BeforeUnresolved = %+v, want text then entity candidate ordering", comparison.BeforeUnresolved)
	}
}

func fact(
	t *testing.T,
	key string,
	value string,
	observationID observation.ObservationID,
	evidenceID evidence.EvidenceID,
) temporal.Fact {
	t.Helper()
	return temporal.Fact{
		Key:                   stateKey(t, key),
		Value:                 textTerm(t, value),
		ObservationIDs:        []observation.ObservationID{observationID},
		SupportingEvidenceIDs: []evidence.EvidenceID{evidenceID},
	}
}

func stateKey(t *testing.T, predicateValue string) temporal.StateKey {
	t.Helper()
	subject := textTerm(t, "entity-1")
	predicate, err := observation.NewPredicate(predicateValue)
	if err != nil {
		t.Fatalf("NewPredicate() error = %v", err)
	}
	return temporal.StateKey{Subject: subject, Predicate: predicate}
}
