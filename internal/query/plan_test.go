package query

import (
	"slices"
	"testing"
	"time"
)

func TestPlanMapsIntentToDeterministicOperations(t *testing.T) {
	start := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 3, 0)
	window, err := Between("Q1 2025", start, end)
	if err != nil {
		t.Fatalf("Between() error = %v", err)
	}

	tests := []struct {
		name       string
		intent     Intent
		selections []TemporalSelection
		want       []RetrievalOperation
	}{
		{
			name:       "trajectory",
			intent:     IntentTrajectory,
			selections: []TemporalSelection{window},
			want: []RetrievalOperation{
				OperationPartitionTimeline,
				OperationAggregateWindows,
				OperationDiffWindows,
				OperationOrderTransitions,
			},
		},
		{
			name:       "causal chain",
			intent:     IntentCausalChain,
			selections: []TemporalSelection{window},
			want:       []RetrievalOperation{OperationRetrieveCausalClaims, OperationOrderCausalClaims},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := NewPlan(PlanInput{
				Intent:         test.intent,
				EntityIDs:      []string{"entity-1"},
				Selections:     test.selections,
				KnowledgeScope: CurrentKnowledge(),
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

	plan, err := NewPlan(PlanInput{
		Intent:         IntentTrendComparison,
		EntityIDs:      []string{" product-stacks "},
		Selections:     []TemporalSelection{windowA, windowB},
		KnowledgeScope: CurrentKnowledge(),
	})
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}

	if got := plan.EntityIDs()[0]; got != "product-stacks" {
		t.Errorf("EntityIDs()[0] = %q, want %q", got, "product-stacks")
	}
	wantOperations := []RetrievalOperation{OperationAggregateWindows, OperationDiffWindows}
	if !slices.Equal(plan.Operations(), wantOperations) {
		t.Errorf("Operations() = %v, want %v", plan.Operations(), wantOperations)
	}
}

func TestKnowledgeCutoffIsSeparateFromValidTime(t *testing.T) {
	validAt := time.Date(2024, time.June, 1, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.FixedZone("EST", -5*60*60))
	selection, err := At("June 2024", validAt)
	if err != nil {
		t.Fatalf("At() error = %v", err)
	}
	scope, err := KnownAsOf(cutoff)
	if err != nil {
		t.Fatalf("KnownAsOf() error = %v", err)
	}

	plan, err := NewPlan(PlanInput{
		Intent:         IntentPointInTime,
		EntityIDs:      []string{"relationship-1"},
		Selections:     []TemporalSelection{selection},
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

	if _, err := NewPlan(PlanInput{
		Intent:         IntentPointInTime,
		EntityIDs:      []string{"entity-1"},
		Selections:     []TemporalSelection{windowA},
		KnowledgeScope: CurrentKnowledge(),
	}); err == nil {
		t.Fatal("NewPlan() error = nil, want selection mismatch error")
	}
}

func TestTrendComparisonRejectsReversedWindows(t *testing.T) {
	windowA, windowB := comparisonWindows(t)

	if _, err := NewPlan(PlanInput{
		Intent:         IntentTrendComparison,
		EntityIDs:      []string{"entity-1"},
		Selections:     []TemporalSelection{windowB, windowA},
		KnowledgeScope: CurrentKnowledge(),
	}); err == nil {
		t.Fatal("NewPlan() error = nil, want reversed windows error")
	}
}

func comparisonWindows(t *testing.T) (TemporalSelection, TemporalSelection) {
	t.Helper()
	start := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	middle := start.AddDate(0, 3, 0)
	end := middle.AddDate(0, 3, 0)
	windowA, err := Between("Q1 2025", start, middle)
	if err != nil {
		t.Fatalf("Between() first window error = %v", err)
	}
	windowB, err := Between("Q2 2025", middle, end)
	if err != nil {
		t.Fatalf("Between() second window error = %v", err)
	}
	return windowA, windowB
}
