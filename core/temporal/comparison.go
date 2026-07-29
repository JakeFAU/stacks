package temporal

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
)

// ChangeKind identifies a semantic state difference between two windows.
type ChangeKind string

const (
	ChangeAdded   ChangeKind = "added"
	ChangeRemoved ChangeKind = "removed"
	ChangeChanged ChangeKind = "changed"
)

// Change is a deterministic semantic difference. Before is absent for added
// facts; After is absent for removed facts.
type Change struct {
	Kind   ChangeKind
	Key    StateKey
	Before *Fact
	After  *Fact
}

// Comparison is the dated, ordered structure supplied to narration.
type Comparison struct {
	Before           TemporalSelection
	After            TemporalSelection
	BeforeFacts      []Fact
	AfterFacts       []Fact
	Changes          []Change
	UnresolvedKeys   []StateKey
	BeforeUnresolved []UnresolvedFact
	AfterUnresolved  []UnresolvedFact
}

// CompareWindowSummaries computes semantic changes without model inference.
// Keys unresolved in either window are excluded from Changes.
func CompareWindowSummaries(before, after WindowSummary) (Comparison, error) {
	beforeFacts, beforeUnresolved, err := validateSummary("before", before)
	if err != nil {
		return Comparison{}, err
	}
	afterFacts, afterUnresolved, err := validateSummary("after", after)
	if err != nil {
		return Comparison{}, err
	}
	if !after.Selection.start.After(before.Selection.start) {
		return Comparison{}, fmt.Errorf("comparison windows must be ordered by start time")
	}

	keys := make(map[stateKeyIdentity]StateKey, len(beforeFacts)+len(afterFacts)+len(beforeUnresolved)+len(afterUnresolved))
	for identity, fact := range beforeFacts {
		keys[identity] = fact.Key
	}
	for identity, fact := range afterFacts {
		keys[identity] = fact.Key
	}
	for identity, item := range beforeUnresolved {
		keys[identity] = item.Key
	}
	for identity, item := range afterUnresolved {
		keys[identity] = item.Key
	}
	orderedKeys := make([]StateKey, 0, len(keys))
	for _, key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Slice(orderedKeys, func(left, right int) bool { return CompareStateKeys(orderedKeys[left], orderedKeys[right]) < 0 })

	comparison := Comparison{Before: before.Selection, After: after.Selection, BeforeFacts: orderedFacts(beforeFacts), AfterFacts: orderedFacts(afterFacts), BeforeUnresolved: orderedUnresolved(beforeUnresolved), AfterUnresolved: orderedUnresolved(afterUnresolved)}
	for _, key := range orderedKeys {
		identity := identityForStateKey(key)
		if _, unresolved := beforeUnresolved[identity]; unresolved {
			comparison.UnresolvedKeys = append(comparison.UnresolvedKeys, key)
			continue
		}
		if _, unresolved := afterUnresolved[identity]; unresolved {
			comparison.UnresolvedKeys = append(comparison.UnresolvedKeys, key)
			continue
		}
		beforeFact, inBefore := beforeFacts[identity]
		afterFact, inAfter := afterFacts[identity]
		switch {
		case !inBefore && inAfter:
			afterCopy := cloneFact(afterFact)
			comparison.Changes = append(comparison.Changes, Change{Kind: ChangeAdded, Key: key, After: &afterCopy})
		case inBefore && !inAfter:
			beforeCopy := cloneFact(beforeFact)
			comparison.Changes = append(comparison.Changes, Change{Kind: ChangeRemoved, Key: key, Before: &beforeCopy})
		case inBefore && inAfter && CompareTerms(beforeFact.Value, afterFact.Value) != 0:
			beforeCopy, afterCopy := cloneFact(beforeFact), cloneFact(afterFact)
			comparison.Changes = append(comparison.Changes, Change{Kind: ChangeChanged, Key: key, Before: &beforeCopy, After: &afterCopy})
		}
	}
	return comparison, nil
}

func validateSummary(name string, summary WindowSummary) (map[stateKeyIdentity]Fact, map[stateKeyIdentity]UnresolvedFact, error) {
	if summary.Selection.kind != SelectionWindow {
		return nil, nil, fmt.Errorf("%s summary requires a window selection", name)
	}
	facts := make(map[stateKeyIdentity]Fact, len(summary.Facts))
	for _, fact := range summary.Facts {
		normalized, err := validateFact(name, fact)
		if err != nil {
			return nil, nil, err
		}
		identity := identityForStateKey(normalized.Key)
		if _, exists := facts[identity]; exists {
			return nil, nil, fmt.Errorf("%s summary fact keys must be unique", name)
		}
		facts[identity] = normalized
	}

	unresolved := make(map[stateKeyIdentity]UnresolvedFact, len(summary.Unresolved))
	for _, item := range summary.Unresolved {
		if err := validateStateKey(item.Key); err != nil {
			return nil, nil, fmt.Errorf("%s summary unresolved key: %w", name, err)
		}
		if !item.Reason.valid() {
			return nil, nil, fmt.Errorf("%s summary unresolved reason is invalid", name)
		}
		if len(item.Candidates) == 0 {
			return nil, nil, fmt.Errorf("%s summary unresolved candidates are required", name)
		}
		if item.Reason == UnresolvedConflict && len(item.Candidates) < 2 {
			return nil, nil, fmt.Errorf("%s summary conflict requires at least two candidates", name)
		}
		identity := identityForStateKey(item.Key)
		if _, exists := unresolved[identity]; exists {
			return nil, nil, fmt.Errorf("%s summary unresolved keys must be unique", name)
		}
		if _, exists := facts[identity]; exists {
			return nil, nil, fmt.Errorf("%s summary key cannot be both resolved and unresolved", name)
		}
		candidates := make([]Fact, len(item.Candidates))
		seenValues := make(map[termIdentity]struct{}, len(item.Candidates))
		for index, candidate := range item.Candidates {
			if CompareStateKeys(candidate.Key, item.Key) != 0 {
				return nil, nil, fmt.Errorf("%s summary unresolved candidate key must match its parent", name)
			}
			normalized, err := validateFact(name, candidate)
			if err != nil {
				return nil, nil, err
			}
			valueIdentity := identityForTerm(normalized.Value)
			if _, exists := seenValues[valueIdentity]; exists {
				return nil, nil, fmt.Errorf("%s summary unresolved candidate values must be unique", name)
			}
			seenValues[valueIdentity] = struct{}{}
			candidates[index] = normalized
		}
		sort.Slice(candidates, func(left, right int) bool { return CompareTerms(candidates[left].Value, candidates[right].Value) < 0 })
		item.Candidates = candidates
		unresolved[identity] = item
	}
	return facts, unresolved, nil
}

func validateFact(name string, fact Fact) (Fact, error) {
	if err := validateStateKey(fact.Key); err != nil {
		return Fact{}, fmt.Errorf("%s summary fact key: %w", name, err)
	}
	if err := validateCanonicalTerm("summary fact value", fact.Value); err != nil {
		return Fact{}, fmt.Errorf("%s %w", name, err)
	}
	if len(fact.ObservationIDs) == 0 {
		return Fact{}, fmt.Errorf("%s summary fact observation provenance is required", name)
	}
	if len(fact.SupportingEvidenceIDs)+len(fact.ContradictingEvidenceIDs) == 0 {
		return Fact{}, fmt.Errorf("%s summary fact evidence is required", name)
	}
	observationIDs, err := normalizeObservationIDs(name, fact.ObservationIDs)
	if err != nil {
		return Fact{}, err
	}
	supportingEvidenceIDs, err := normalizeEvidenceIDs(name, "supporting", fact.SupportingEvidenceIDs)
	if err != nil {
		return Fact{}, err
	}
	contradictingEvidenceIDs, err := normalizeEvidenceIDs(name, "contradicting", fact.ContradictingEvidenceIDs)
	if err != nil {
		return Fact{}, err
	}
	fact.ObservationIDs, fact.SupportingEvidenceIDs, fact.ContradictingEvidenceIDs = observationIDs, supportingEvidenceIDs, contradictingEvidenceIDs
	return fact, nil
}

func (reason UnresolvedReason) valid() bool {
	switch reason {
	case UnresolvedConflict, UnresolvedTransition, UnresolvedTemporalUncertainty, UnresolvedHypothesis, UnresolvedCounterevidenceOnly:
		return true
	default:
		return false
	}
}
func orderedFacts(facts map[stateKeyIdentity]Fact) []Fact {
	ordered := make([]Fact, 0, len(facts))
	for _, fact := range facts {
		ordered = append(ordered, cloneFact(fact))
	}
	sort.Slice(ordered, func(left, right int) bool { return CompareStateKeys(ordered[left].Key, ordered[right].Key) < 0 })
	return ordered
}
func orderedUnresolved(items map[stateKeyIdentity]UnresolvedFact) []UnresolvedFact {
	ordered := make([]UnresolvedFact, 0, len(items))
	for _, item := range items {
		item.Candidates = append([]Fact(nil), item.Candidates...)
		for index := range item.Candidates {
			item.Candidates[index] = cloneFact(item.Candidates[index])
		}
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(left, right int) bool { return CompareStateKeys(ordered[left].Key, ordered[right].Key) < 0 })
	return ordered
}

func normalizeObservationIDs(name string, values []observation.ObservationID) ([]observation.ObservationID, error) {
	normalized := make([]observation.ObservationID, len(values))
	seen := make(map[observation.ObservationID]struct{}, len(values))
	for index, value := range values {
		value = observation.ObservationID(strings.TrimSpace(string(value)))
		if value == "" {
			return nil, fmt.Errorf("%s summary observation ID is required", name)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("%s summary observation IDs must be unique", name)
		}
		seen[value] = struct{}{}
		normalized[index] = value
	}
	sort.Slice(normalized, func(left, right int) bool { return normalized[left] < normalized[right] })
	return normalized, nil
}
func normalizeEvidenceIDs(name, role string, values []evidence.EvidenceID) ([]evidence.EvidenceID, error) {
	normalized := make([]evidence.EvidenceID, len(values))
	seen := make(map[evidence.EvidenceID]struct{}, len(values))
	for index, value := range values {
		value = evidence.EvidenceID(strings.TrimSpace(string(value)))
		if value == "" {
			return nil, fmt.Errorf("%s summary %s evidence ID is required", name, role)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("%s summary %s evidence IDs must be unique", name, role)
		}
		seen[value] = struct{}{}
		normalized[index] = value
	}
	sort.Slice(normalized, func(left, right int) bool { return normalized[left] < normalized[right] })
	return normalized, nil
}
func cloneFact(fact Fact) Fact {
	fact.ObservationIDs = append([]observation.ObservationID(nil), fact.ObservationIDs...)
	fact.SupportingEvidenceIDs = append([]evidence.EvidenceID(nil), fact.SupportingEvidenceIDs...)
	fact.ContradictingEvidenceIDs = append([]evidence.EvidenceID(nil), fact.ContradictingEvidenceIDs...)
	return fact
}
