package extract

import (
	"testing"

	"github.com/JakeFAU/stacks/core/observation"
)

func TestInteractionObservationPredicateRoundTripsEveryExtractV2Signal(t *testing.T) {
	tests := []struct {
		category  string
		direction string
		want      observation.Predicate
	}{
		{SignalCategoryDelegationAutonomy, SignalDirectionStrengthening, "stacks.interaction.v1/delegation_autonomy/strengthening"},
		{SignalCategoryDelegationAutonomy, SignalDirectionWeakening, "stacks.interaction.v1/delegation_autonomy/weakening"},
		{SignalCategoryDelegationAutonomy, SignalDirectionMixed, "stacks.interaction.v1/delegation_autonomy/mixed"},
		{SignalCategoryDelegationAutonomy, SignalDirectionUnclear, "stacks.interaction.v1/delegation_autonomy/unclear"},
		{SignalCategoryScrutinyCorrection, SignalDirectionStrengthening, "stacks.interaction.v1/scrutiny_correction/strengthening"},
		{SignalCategoryScrutinyCorrection, SignalDirectionWeakening, "stacks.interaction.v1/scrutiny_correction/weakening"},
		{SignalCategoryScrutinyCorrection, SignalDirectionMixed, "stacks.interaction.v1/scrutiny_correction/mixed"},
		{SignalCategoryScrutinyCorrection, SignalDirectionUnclear, "stacks.interaction.v1/scrutiny_correction/unclear"},
		{SignalCategoryEndorsementTrust, SignalDirectionStrengthening, "stacks.interaction.v1/endorsement_trust/strengthening"},
		{SignalCategoryEndorsementTrust, SignalDirectionWeakening, "stacks.interaction.v1/endorsement_trust/weakening"},
		{SignalCategoryEndorsementTrust, SignalDirectionMixed, "stacks.interaction.v1/endorsement_trust/mixed"},
		{SignalCategoryEndorsementTrust, SignalDirectionUnclear, "stacks.interaction.v1/endorsement_trust/unclear"},
		{SignalCategorySupportAdvocacy, SignalDirectionStrengthening, "stacks.interaction.v1/support_advocacy/strengthening"},
		{SignalCategorySupportAdvocacy, SignalDirectionWeakening, "stacks.interaction.v1/support_advocacy/weakening"},
		{SignalCategorySupportAdvocacy, SignalDirectionMixed, "stacks.interaction.v1/support_advocacy/mixed"},
		{SignalCategorySupportAdvocacy, SignalDirectionUnclear, "stacks.interaction.v1/support_advocacy/unclear"},
		{SignalCategoryFutureResponsibility, SignalDirectionStrengthening, "stacks.interaction.v1/future_responsibility/strengthening"},
		{SignalCategoryFutureResponsibility, SignalDirectionWeakening, "stacks.interaction.v1/future_responsibility/weakening"},
		{SignalCategoryFutureResponsibility, SignalDirectionMixed, "stacks.interaction.v1/future_responsibility/mixed"},
		{SignalCategoryFutureResponsibility, SignalDirectionUnclear, "stacks.interaction.v1/future_responsibility/unclear"},
	}

	for _, testCase := range tests {
		t.Run(testCase.category+"/"+testCase.direction, func(t *testing.T) {
			predicate, err := InteractionObservationPredicate(testCase.category, testCase.direction)
			if err != nil {
				t.Fatalf("InteractionObservationPredicate() error = %v", err)
			}
			if predicate != testCase.want {
				t.Fatalf("InteractionObservationPredicate() = %q, want exact durable bytes %q", predicate, testCase.want)
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

func TestInteractionObservationPredicateRejectsUnknownCategoryAndDirection(t *testing.T) {
	t.Run("construction", func(t *testing.T) {
		tests := []struct {
			name      string
			category  string
			direction string
			wantError string
		}{
			{
				name: "category", category: "unknown", direction: SignalDirectionStrengthening,
				wantError: "interaction observation category is invalid",
			},
			{
				name: "direction", category: SignalCategoryFutureResponsibility, direction: "unknown",
				wantError: "interaction observation direction is invalid",
			},
		}

		for _, testCase := range tests {
			t.Run(testCase.name, func(t *testing.T) {
				_, err := InteractionObservationPredicate(testCase.category, testCase.direction)
				if err == nil || err.Error() != testCase.wantError {
					t.Fatalf("InteractionObservationPredicate() error = %v, want %q", err, testCase.wantError)
				}
			})
		}
	})

	t.Run("parsing", func(t *testing.T) {
		tests := []struct {
			predicate observation.Predicate
			wantError string
		}{
			{"interaction_signal", "interaction observation predicate namespace is invalid"},
			{"other.interaction.v1/future_responsibility/strengthening", "interaction observation predicate namespace is invalid"},
			{"stacks.interaction.v2/future_responsibility/strengthening", "interaction observation predicate namespace is invalid"},
			{"stacks.interaction.v1/unknown/strengthening", "interaction observation predicate category is invalid"},
			{"stacks.interaction.v1/future_responsibility/unknown", "interaction observation predicate direction is invalid"},
			{"stacks.interaction.v1/future_responsibility/strengthening/extra", "interaction observation predicate namespace is invalid"},
		}

		for _, testCase := range tests {
			t.Run(string(testCase.predicate), func(t *testing.T) {
				_, _, err := ParseInteractionObservationPredicate(testCase.predicate)
				if err == nil || err.Error() != testCase.wantError {
					t.Fatalf(
						"ParseInteractionObservationPredicate(%q) error = %v, want %q",
						testCase.predicate,
						err,
						testCase.wantError,
					)
				}
			})
		}
	})
}
