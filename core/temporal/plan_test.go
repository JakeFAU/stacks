package temporal_test

import (
	"slices"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/temporal"
)

func TestPlanMapsIntentToDeterministicOperations(t *testing.T) {
	start := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 3, 0)
	window, err := temporal.Between("Q1 2025", start, end)
	if err != nil {
		t.Fatalf("Between() error = %v", err)
	}
	tests := []struct {
		name       string
		intent     temporal.Intent
		selections []temporal.TemporalSelection
		want       []temporal.RetrievalOperation
	}{
		{
			name:       "trajectory",
			intent:     temporal.IntentTrajectory,
			selections: []temporal.TemporalSelection{window},
			want: []temporal.RetrievalOperation{
				temporal.OperationPartitionTimeline,
				temporal.OperationAggregateWindows,
				temporal.OperationDiffWindows,
				temporal.OperationOrderTransitions,
			},
		},
		{
			name:       "causal chain",
			intent:     temporal.IntentCausalChain,
			selections: []temporal.TemporalSelection{window},
			want:       []temporal.RetrievalOperation{temporal.OperationRetrieveCausalClaims, temporal.OperationOrderCausalClaims},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := temporal.NewPlan(temporal.PlanInput{
				Intent:         test.intent,
				EntityIDs:      []string{"entity-1"},
				Selections:     test.selections,
				KnowledgeScope: temporal.CurrentKnowledge(),
			})
			if err != nil {
				t.Fatalf("NewPlan() error = %v", err)
			}
			if !slices.Equal(plan.Operations(), test.want) {
				t.Errorf("Operations() = %v, want %v", plan.Operations(), test.want)
			}
		})
	}
}

func TestTrendComparisonRequiresTwoResolvedWindows(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	plan, err := temporal.NewPlan(temporal.PlanInput{
		Intent:         temporal.IntentTrendComparison,
		EntityIDs:      []string{" product-stacks "},
		Selections:     []temporal.TemporalSelection{windowA, windowB},
		KnowledgeScope: temporal.CurrentKnowledge(),
	})
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	if got := plan.EntityIDs()[0]; got != "product-stacks" {
		t.Errorf("EntityIDs()[0] = %q, want %q", got, "product-stacks")
	}
	wantOperations := []temporal.RetrievalOperation{temporal.OperationAggregateWindows, temporal.OperationDiffWindows}
	if !slices.Equal(plan.Operations(), wantOperations) {
		t.Errorf("Operations() = %v, want %v", plan.Operations(), wantOperations)
	}
}

func TestKnowledgeCutoffIsSeparateFromValidTime(t *testing.T) {
	validAt := time.Date(2024, time.June, 1, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.FixedZone("EST", -5*60*60))
	selection, err := temporal.At("June 2024", validAt)
	if err != nil {
		t.Fatalf("At() error = %v", err)
	}
	scope, err := temporal.KnownAsOf(cutoff)
	if err != nil {
		t.Fatalf("KnownAsOf() error = %v", err)
	}
	plan, err := temporal.NewPlan(temporal.PlanInput{
		Intent:         temporal.IntentPointInTime,
		EntityIDs:      []string{"relationship-1"},
		Selections:     []temporal.TemporalSelection{selection},
		KnowledgeScope: scope,
	})
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	asOf, ok := plan.KnowledgeScope().AsOf()
	if !ok || asOf != cutoff.UTC() {
		t.Errorf("KnowledgeScope().AsOf() = (%v, %v), want (%v, true)", asOf, ok, cutoff.UTC())
	}
	point, ok := plan.Selections()[0].Point()
	if !ok || point != validAt {
		t.Errorf("Selections()[0].Point() = (%v, %v), want (%v, true)", point, ok, validAt)
	}
}

func TestNewPlanRejectsIntentSelectionMismatch(t *testing.T) {
	windowA, _ := comparisonWindows(t)
	if _, err := temporal.NewPlan(temporal.PlanInput{
		Intent:         temporal.IntentPointInTime,
		EntityIDs:      []string{"entity-1"},
		Selections:     []temporal.TemporalSelection{windowA},
		KnowledgeScope: temporal.CurrentKnowledge(),
	}); err == nil {
		t.Fatal("NewPlan() error = nil, want selection mismatch error")
	}
}

func TestTrendComparisonRejectsReversedWindows(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	if _, err := temporal.NewPlan(temporal.PlanInput{
		Intent:         temporal.IntentTrendComparison,
		EntityIDs:      []string{"entity-1"},
		Selections:     []temporal.TemporalSelection{windowB, windowA},
		KnowledgeScope: temporal.CurrentKnowledge(),
	}); err == nil {
		t.Fatal("NewPlan() error = nil, want reversed windows error")
	}
}

func TestTemporalPlanDefensivelyCopiesInputsAndOutputs(t *testing.T) {
	windowA, windowB := comparisonWindows(t)
	entityIDs := []string{"entity-2", "entity-1"}
	selections := []temporal.TemporalSelection{windowA, windowB}
	plan, err := temporal.NewPlan(temporal.PlanInput{
		Intent:         temporal.IntentTrendComparison,
		EntityIDs:      entityIDs,
		Selections:     selections,
		KnowledgeScope: temporal.CurrentKnowledge(),
	})
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	entityIDs[0] = "changed-input"
	selections[0] = windowB
	gotIDs := plan.EntityIDs()
	gotSelections := plan.Selections()
	gotOperations := plan.Operations()
	gotIDs[0] = "changed-output"
	gotSelections[0] = windowB
	gotOperations[0] = temporal.OperationOrderTransitions
	if !slices.Equal(plan.EntityIDs(), []string{"entity-2", "entity-1"}) {
		t.Errorf("EntityIDs() = %v, want original ordered IDs", plan.EntityIDs())
	}
	if plan.Selections()[0].Label() != "Q1 2025" {
		t.Errorf("Selections()[0].Label() = %q, want Q1 2025", plan.Selections()[0].Label())
	}
	if !slices.Equal(plan.Operations(), []temporal.RetrievalOperation{temporal.OperationAggregateWindows, temporal.OperationDiffWindows}) {
		t.Errorf("Operations() = %v, want deterministic operations", plan.Operations())
	}
}

func comparisonWindows(t *testing.T) (temporal.TemporalSelection, temporal.TemporalSelection) {
	t.Helper()
	start := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	middle := start.AddDate(0, 3, 0)
	end := middle.AddDate(0, 3, 0)
	windowA, err := temporal.Between("Q1 2025", start, middle)
	if err != nil {
		t.Fatalf("Between() first window error = %v", err)
	}
	windowB, err := temporal.Between("Q2 2025", middle, end)
	if err != nil {
		t.Fatalf("Between() second window error = %v", err)
	}
	return windowA, windowB
}
