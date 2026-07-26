package analysis

import (
	"testing"

	"github.com/JakeFAU/stacks/core/observation"
)

func TestInteractionObservationPredicateRoundTrips(t *testing.T) {
	tests := []struct {
		category  Category
		direction Direction
		want      observation.Predicate
	}{
		{CategoryDelegationAutonomy, DirectionStrengthening, "stacks.interaction.v1/delegation_autonomy/strengthening"},
		{CategoryDelegationAutonomy, DirectionWeakening, "stacks.interaction.v1/delegation_autonomy/weakening"},
		{CategoryDelegationAutonomy, DirectionMixed, "stacks.interaction.v1/delegation_autonomy/mixed"},
		{CategoryDelegationAutonomy, DirectionUnclear, "stacks.interaction.v1/delegation_autonomy/unclear"},
		{CategoryScrutinyCorrection, DirectionStrengthening, "stacks.interaction.v1/scrutiny_correction/strengthening"},
		{CategoryScrutinyCorrection, DirectionWeakening, "stacks.interaction.v1/scrutiny_correction/weakening"},
		{CategoryScrutinyCorrection, DirectionMixed, "stacks.interaction.v1/scrutiny_correction/mixed"},
		{CategoryScrutinyCorrection, DirectionUnclear, "stacks.interaction.v1/scrutiny_correction/unclear"},
		{CategoryEndorsementTrust, DirectionStrengthening, "stacks.interaction.v1/endorsement_trust/strengthening"},
		{CategoryEndorsementTrust, DirectionWeakening, "stacks.interaction.v1/endorsement_trust/weakening"},
		{CategoryEndorsementTrust, DirectionMixed, "stacks.interaction.v1/endorsement_trust/mixed"},
		{CategoryEndorsementTrust, DirectionUnclear, "stacks.interaction.v1/endorsement_trust/unclear"},
		{CategorySupportAdvocacy, DirectionStrengthening, "stacks.interaction.v1/support_advocacy/strengthening"},
		{CategorySupportAdvocacy, DirectionWeakening, "stacks.interaction.v1/support_advocacy/weakening"},
		{CategorySupportAdvocacy, DirectionMixed, "stacks.interaction.v1/support_advocacy/mixed"},
		{CategorySupportAdvocacy, DirectionUnclear, "stacks.interaction.v1/support_advocacy/unclear"},
		{CategoryFutureResponsibility, DirectionStrengthening, "stacks.interaction.v1/future_responsibility/strengthening"},
		{CategoryFutureResponsibility, DirectionWeakening, "stacks.interaction.v1/future_responsibility/weakening"},
		{CategoryFutureResponsibility, DirectionMixed, "stacks.interaction.v1/future_responsibility/mixed"},
		{CategoryFutureResponsibility, DirectionUnclear, "stacks.interaction.v1/future_responsibility/unclear"},
	}

	for _, testCase := range tests {
		t.Run(string(testCase.category)+"/"+string(testCase.direction), func(t *testing.T) {
			predicate, err := InteractionObservationPredicate(testCase.category, testCase.direction)
			if err != nil {
				t.Fatalf("InteractionObservationPredicate() error = %v", err)
			}
			if predicate != testCase.want {
				t.Fatalf("InteractionObservationPredicate() = %q, want %q", predicate, testCase.want)
			}
			category, direction, err := ParseInteractionObservationPredicate(predicate)
			if err != nil {
				t.Fatalf("ParseInteractionObservationPredicate() error = %v", err)
			}
			if category != testCase.category || direction != testCase.direction {
				t.Fatalf(
					"ParseInteractionObservationPredicate() = (%q, %q), want (%q, %q)",
					category,
					direction,
					testCase.category,
					testCase.direction,
				)
			}
		})
	}
}

func TestInteractionObservationPredicateRejectsUnknownVocabulary(t *testing.T) {
	for _, predicate := range []observation.Predicate{
		"interaction_signal",
		"other.interaction.v1/future_responsibility/strengthening",
		"stacks.interaction.v2/future_responsibility/strengthening",
		"stacks.interaction.v1/unknown/strengthening",
		"stacks.interaction.v1/future_responsibility/unknown",
		"stacks.interaction.v1/future_responsibility/strengthening/extra",
	} {
		t.Run(string(predicate), func(t *testing.T) {
			if _, _, err := ParseInteractionObservationPredicate(predicate); err == nil {
				t.Fatalf("ParseInteractionObservationPredicate(%q) error = nil", predicate)
			}
		})
	}

	if _, err := InteractionObservationPredicate(Category("unknown"), DirectionStrengthening); err == nil {
		t.Fatal("InteractionObservationPredicate() accepted unknown category")
	}
	if _, err := InteractionObservationPredicate(CategoryFutureResponsibility, Direction("unknown")); err == nil {
		t.Fatal("InteractionObservationPredicate() accepted unknown direction")
	}
}
