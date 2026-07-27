// Package query defines the provider-neutral cited temporal query boundary.
package query

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

// EntityMatch controls whether observations must relate to every requested
// entity or may relate to any requested entity.
type EntityMatch string

const (
	EntityMatchAll EntityMatch = "all"
	EntityMatchAny EntityMatch = "any"
)

// CausalPredicate is the sole provider-neutral causal predicate in v1.
const CausalPredicate observation.Predicate = temporal.CausalPredicate

const causalPredicate = CausalPredicate

// Limits bounds query request cardinality and chronology expansion.
type Limits struct {
	MaxEntities   int
	MaxPredicates int
	MaxChronology int
}

// Request is a caller-supplied temporal query before normalization.
type Request struct {
	Intent         temporal.Intent
	EntityIDs      []identity.EntityID
	EntityMatch    EntityMatch
	Predicates     []observation.Predicate
	Selections     []temporal.TemporalSelection
	KnowledgeScope temporal.KnowledgeScope
	Limit          int
}

// ReadSelection is the normalized read request consumed by a repository.
type ReadSelection struct {
	EntityIDs      []identity.EntityID
	EntityMatch    EntityMatch
	Predicates     []observation.Predicate
	Selections     []temporal.TemporalSelection
	KnowledgeScope temporal.KnowledgeScope
}

// Reader reads one coherent, provider-neutral temporal snapshot.
type Reader interface {
	Read(context.Context, ReadSelection) (ReadSnapshot, error)
}

// EntityAuthority states whether a requested canonical entity is known at the
// requested recorded-time scope.
type EntityAuthority struct {
	EntityID identity.EntityID
	Known    bool
}

// CoverageReason records why an otherwise relevant record was excluded.
type CoverageReason string

const (
	CoverageUnresolvedMention CoverageReason = "unresolved-mention"
	CoverageAuthorityExcluded CoverageReason = "authority-excluded"
	CoverageEntityFiltered    CoverageReason = "entity-filtered"
	CoveragePredicateFiltered CoverageReason = "predicate-filtered"
)

// Coverage is repository projection coverage that may become a result gap.
type Coverage struct {
	Reason        CoverageReason
	EntityID      identity.EntityID
	Predicate     observation.Predicate
	ObservationID observation.ObservationID
	ValidTime     observation.TemporalExtent
}

// ReadObservation is one qualified observation together with its resolved
// semantic terms, optional source grounding, and exact evidence records.
type ReadObservation struct {
	Observation               observation.Observation
	Subject                   observation.Term
	Object                    observation.Term
	SubjectGroundingMentionID string
	ObjectGroundingMentionID  string
	Evidence                  []Citation
}

// ReadSnapshot is one repository read projection.
type ReadSnapshot struct {
	Entities     []EntityAuthority
	Observations []ReadObservation
	Coverage     []Coverage
}

// ErrEntityNotFound reports an entity absent at the requested knowledge scope.
// It intentionally carries no supplied identifier.
var ErrEntityNotFound = errors.New("query entity not found")

// ErrLimitExceeded reports a bounded chronology that cannot be returned
// without truncation. It intentionally carries no supplied values.
var ErrLimitExceeded = errors.New("query limit exceeded")

// NormalizeRequest validates query-only constraints and produces a defensive,
// canonical request. Temporal intent, selections, and knowledge scope are
// validated by the core temporal planner before any repository read.
func NormalizeRequest(request Request, limits Limits) (Request, error) {
	if err := validateLimits(limits); err != nil {
		return Request{}, err
	}
	entityIDs, err := normalizeEntityIDs(request.EntityIDs, limits.MaxEntities)
	if err != nil {
		return Request{}, err
	}
	predicates, err := normalizePredicates(request.Predicates, limits.MaxPredicates)
	if err != nil {
		return Request{}, err
	}
	match := request.EntityMatch
	if match == "" {
		match = EntityMatchAll
	}
	if match != EntityMatchAll && match != EntityMatchAny {
		return Request{}, fmt.Errorf("query entity match is invalid")
	}
	if _, err := temporal.NewPlan(temporal.PlanInput{
		Intent: request.Intent, EntityIDs: entityIDsAsStrings(entityIDs), Selections: request.Selections, KnowledgeScope: request.KnowledgeScope,
	}); err != nil {
		return Request{}, fmt.Errorf("validate temporal query: %w", err)
	}
	if err := validateIntentLimit(request.Intent, request.Limit, limits.MaxChronology); err != nil {
		return Request{}, err
	}
	if request.Intent == temporal.IntentCausalChain && (len(predicates) != 1 || predicates[0] != CausalPredicate) {
		return Request{}, fmt.Errorf("causal query requires the exact causal predicate")
	}
	return Request{
		Intent: request.Intent, EntityIDs: entityIDs, EntityMatch: match, Predicates: predicates,
		Selections: append([]temporal.TemporalSelection{}, request.Selections...), KnowledgeScope: request.KnowledgeScope, Limit: request.Limit,
	}, nil
}

// ReadSelection returns the normalized repository request represented by a
// normalized Request. Callers should call NormalizeRequest first.
func (request Request) ReadSelection() ReadSelection {
	return ReadSelection{
		EntityIDs: append([]identity.EntityID{}, request.EntityIDs...), EntityMatch: request.EntityMatch,
		Predicates: append([]observation.Predicate{}, request.Predicates...), Selections: append([]temporal.TemporalSelection{}, request.Selections...), KnowledgeScope: request.KnowledgeScope,
	}
}

func validateLimits(limits Limits) error {
	if limits.MaxEntities <= 0 || limits.MaxPredicates <= 0 || limits.MaxChronology <= 0 {
		return fmt.Errorf("query limits must be positive")
	}
	return nil
}

func normalizeEntityIDs(values []identity.EntityID, maximum int) ([]identity.EntityID, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("query entity IDs are required")
	}
	if len(values) > maximum {
		return nil, fmt.Errorf("query entity IDs exceed the configured maximum")
	}
	result := make([]identity.EntityID, len(values))
	seen := make(map[identity.EntityID]struct{}, len(values))
	for index, value := range values {
		value = identity.EntityID(strings.TrimSpace(string(value)))
		if value == "" {
			return nil, fmt.Errorf("query entity ID is required")
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("query entity IDs must be unique")
		}
		seen[value] = struct{}{}
		result[index] = value
	}
	orderEntityIDs(result)
	return result, nil
}

func normalizePredicates(values []observation.Predicate, maximum int) ([]observation.Predicate, error) {
	if len(values) > maximum {
		return nil, fmt.Errorf("query predicates exceed the configured maximum")
	}
	result := make([]observation.Predicate, len(values))
	seen := make(map[observation.Predicate]struct{}, len(values))
	for index, value := range values {
		value = observation.Predicate(strings.TrimSpace(string(value)))
		if _, err := observation.NewPredicate(string(value)); err != nil {
			return nil, fmt.Errorf("query predicate: %w", err)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("query predicates must be unique")
		}
		seen[value] = struct{}{}
		result[index] = value
	}
	orderPredicates(result)
	return result, nil
}

func validateIntentLimit(intent temporal.Intent, limit, maximum int) error {
	switch intent {
	case temporal.IntentPointInTime, temporal.IntentTrendComparison:
		if limit != 0 {
			return fmt.Errorf("query intent %q does not accept a chronology limit", intent)
		}
	case temporal.IntentTrajectory, temporal.IntentCausalChain:
		if limit <= 0 || limit > maximum {
			return fmt.Errorf("query chronology limit is outside the configured maximum")
		}
	}
	return nil
}

func entityIDsAsStrings(values []identity.EntityID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}
