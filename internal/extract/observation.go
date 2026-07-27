package extract

import (
	"fmt"
	"strings"

	"github.com/JakeFAU/stacks/core/observation"
)

const interactionObservationPredicateNamespace = "stacks.interaction.v1"

// InteractionObservationPredicate encodes the finite extract-v2 interaction
// vocabulary into an ordinary, versioned canonical predicate.
func InteractionObservationPredicate(category, direction string) (observation.Predicate, error) {
	if !validSignalCategory(category) {
		return "", fmt.Errorf("interaction observation category is invalid")
	}
	if !validSignalDirection(direction) {
		return "", fmt.Errorf("interaction observation direction is invalid")
	}
	return observation.NewPredicate(
		interactionObservationPredicateNamespace + "/" + category + "/" + direction,
	)
}

// ParseInteractionObservationPredicate decodes only the exact current
// namespace and extract-v2 vocabulary.
func ParseInteractionObservationPredicate(predicate observation.Predicate) (string, string, error) {
	parts := strings.Split(string(predicate), "/")
	if len(parts) != 3 || parts[0] != interactionObservationPredicateNamespace {
		return "", "", fmt.Errorf("interaction observation predicate namespace is invalid")
	}
	category, direction := parts[1], parts[2]
	if !validSignalCategory(category) {
		return "", "", fmt.Errorf("interaction observation predicate category is invalid")
	}
	if !validSignalDirection(direction) {
		return "", "", fmt.Errorf("interaction observation predicate direction is invalid")
	}
	return category, direction, nil
}
