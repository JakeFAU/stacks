package analysis

import (
	"fmt"
	"strings"

	"github.com/JakeFAU/stacks/core/observation"
)

const interactionObservationPredicateNamespace = "stacks.interaction.v1"

// InteractionObservationPredicate encodes the finite interaction vocabulary
// into an ordinary, versioned canonical predicate.
func InteractionObservationPredicate(category Category, direction Direction) (observation.Predicate, error) {
	if !validInteractionCategory(category) {
		return "", fmt.Errorf("interaction observation category is invalid")
	}
	if !validInteractionDirection(direction) {
		return "", fmt.Errorf("interaction observation direction is invalid")
	}
	return observation.NewPredicate(
		interactionObservationPredicateNamespace + "/" + string(category) + "/" + string(direction),
	)
}

// ParseInteractionObservationPredicate decodes only the exact current
// namespace and finite vocabulary.
func ParseInteractionObservationPredicate(predicate observation.Predicate) (Category, Direction, error) {
	parts := strings.Split(string(predicate), "/")
	if len(parts) != 3 || parts[0] != interactionObservationPredicateNamespace {
		return "", "", fmt.Errorf("interaction observation predicate namespace is invalid")
	}
	category, direction := Category(parts[1]), Direction(parts[2])
	if !validInteractionCategory(category) {
		return "", "", fmt.Errorf("interaction observation predicate category is invalid")
	}
	if !validInteractionDirection(direction) {
		return "", "", fmt.Errorf("interaction observation predicate direction is invalid")
	}
	return category, direction, nil
}

func validInteractionCategory(category Category) bool {
	switch category {
	case CategoryDelegationAutonomy,
		CategoryScrutinyCorrection,
		CategoryEndorsementTrust,
		CategorySupportAdvocacy,
		CategoryFutureResponsibility:
		return true
	default:
		return false
	}
}

func validInteractionDirection(direction Direction) bool {
	switch direction {
	case DirectionStrengthening, DirectionWeakening, DirectionMixed, DirectionUnclear:
		return true
	default:
		return false
	}
}
