package temporal

import (
	"fmt"
	"time"

	"github.com/JakeFAU/stacks/core/observation"
)

// PointSummary is deterministic state reconstructed at one valid-time instant.
type PointSummary struct {
	Selection  TemporalSelection
	Facts      []Fact
	Unresolved []UnresolvedFact
}

// ReconstructState selects candidates valid at one exact instant and
// aggregates them without converting the point into an arbitrary window.
// Recorded-time scope remains an independent eligibility filter.
func ReconstructState(
	selection TemporalSelection,
	scope KnowledgeScope,
	candidates []StateCandidate,
) (PointSummary, error) {
	instant, ok := selection.Point()
	if !ok {
		return PointSummary{}, fmt.Errorf("state reconstruction requires a point selection")
	}
	if scope.kind != KnowledgeCurrent && scope.kind != KnowledgeAsOf {
		return PointSummary{}, fmt.Errorf("state reconstruction knowledge scope is invalid")
	}
	groups, err := aggregateCandidates(scope, candidates, func(extent observation.TemporalExtent) (bool, bool) {
		return pointValidTimeEligibility(instant, extent)
	})
	if err != nil {
		return PointSummary{}, err
	}
	material := summarizeGroups(groups)
	return PointSummary{
		Selection:  selection,
		Facts:      material.facts,
		Unresolved: material.unresolved,
	}, nil
}

func pointValidTimeEligibility(point time.Time, extent observation.TemporalExtent) (eligible, uncertain bool) {
	switch extent.Kind() {
	case observation.TemporalUnknown:
		return false, true
	case observation.TemporalInstant:
		instant, _ := extent.Instant()
		return instant.Equal(point), false
	case observation.TemporalInterval:
		return intervalContains(extent, point), false
	case observation.TemporalWindow:
		if !intervalContains(extent, point) {
			return false, false
		}
		return false, true
	default:
		return false, true
	}
}
