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
	Key    string
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
	UnresolvedKeys   []string
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

	keys := make(map[string]struct{}, len(beforeFacts)+len(afterFacts)+len(beforeUnresolved)+len(afterUnresolved))
	for key := range beforeFacts {
		keys[key] = struct{}{}
	}
	for key := range afterFacts {
		keys[key] = struct{}{}
	}
	for key := range beforeUnresolved {
		keys[key] = struct{}{}
	}
	for key := range afterUnresolved {
		keys[key] = struct{}{}
	}
	orderedKeys := make([]string, 0, len(keys))
	for key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)

	comparison := Comparison{
		Before:           before.Selection,
		After:            after.Selection,
		BeforeFacts:      orderedFacts(beforeFacts),
		AfterFacts:       orderedFacts(afterFacts),
		BeforeUnresolved: orderedUnresolved(beforeUnresolved),
		AfterUnresolved:  orderedUnresolved(afterUnresolved),
	}
	for _, key := range orderedKeys {
		if _, unresolved := beforeUnresolved[key]; unresolved {
			comparison.UnresolvedKeys = append(comparison.UnresolvedKeys, key)
			continue
		}
		if _, unresolved := afterUnresolved[key]; unresolved {
			comparison.UnresolvedKeys = append(comparison.UnresolvedKeys, key)
			continue
		}
		beforeFact, inBefore := beforeFacts[key]
		afterFact, inAfter := afterFacts[key]
		switch {
		case !inBefore && inAfter:
			afterCopy := cloneFact(afterFact)
			comparison.Changes = append(comparison.Changes, Change{Kind: ChangeAdded, Key: key, After: &afterCopy})
		case inBefore && !inAfter:
			beforeCopy := cloneFact(beforeFact)
			comparison.Changes = append(comparison.Changes, Change{Kind: ChangeRemoved, Key: key, Before: &beforeCopy})
		case inBefore && inAfter && beforeFact.Value != afterFact.Value:
			beforeCopy := cloneFact(beforeFact)
			afterCopy := cloneFact(afterFact)
			comparison.Changes = append(comparison.Changes, Change{
				Kind:   ChangeChanged,
				Key:    key,
				Before: &beforeCopy,
				After:  &afterCopy,
			})
		}
	}
	return comparison, nil
}

func validateSummary(name string, summary WindowSummary) (map[string]Fact, map[string]UnresolvedFact, error) {
	if summary.Selection.kind != SelectionWindow {
		return nil, nil, fmt.Errorf("%s summary requires a window selection", name)
	}

	facts := make(map[string]Fact, len(summary.Facts))
	for _, fact := range summary.Facts {
		normalized, err := validateFact(name, fact, false)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := facts[normalized.Key]; exists {
			return nil, nil, fmt.Errorf("%s summary fact keys must be unique", name)
		}
		facts[normalized.Key] = normalized
	}

	unresolved := make(map[string]UnresolvedFact, len(summary.Unresolved))
	for _, item := range summary.Unresolved {
		item.Key = strings.TrimSpace(item.Key)
		if item.Key == "" {
			return nil, nil, fmt.Errorf("%s summary unresolved key is required", name)
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
		if _, exists := unresolved[item.Key]; exists {
			return nil, nil, fmt.Errorf("%s summary unresolved keys must be unique", name)
		}
		if _, exists := facts[item.Key]; exists {
			return nil, nil, fmt.Errorf("%s summary key cannot be both resolved and unresolved", name)
		}

		candidates := make([]Fact, len(item.Candidates))
		seenValues := make(map[string]struct{}, len(item.Candidates))
		hasLegacyUncited := false
		for index, candidate := range item.Candidates {
			candidate.Key = strings.TrimSpace(candidate.Key)
			if candidate.Key != item.Key {
				return nil, nil, fmt.Errorf("%s summary unresolved candidate key must match its parent", name)
			}
			normalized, err := validateFact(name, candidate, true)
			if err != nil {
				return nil, nil, err
			}
			if _, exists := seenValues[normalized.Value]; exists {
				return nil, nil, fmt.Errorf("%s summary unresolved candidate values must be unique", name)
			}
			seenValues[normalized.Value] = struct{}{}
			candidates[index] = normalized
			hasLegacyUncited = hasLegacyUncited || normalized.LegacyUncited
		}
		if item.Reason == UnresolvedLegacyUncited && !hasLegacyUncited {
			return nil, nil, fmt.Errorf("%s summary legacy-uncited reason requires a legacy-uncited candidate", name)
		}
		sort.Slice(candidates, func(left, right int) bool {
			return candidates[left].Value < candidates[right].Value
		})
		item.Candidates = candidates
		unresolved[item.Key] = item
	}
	return facts, unresolved, nil
}

func validateFact(name string, fact Fact, allowLegacyUncited bool) (Fact, error) {
	fact.Key = strings.TrimSpace(fact.Key)
	fact.Value = strings.TrimSpace(fact.Value)
	if fact.Key == "" || fact.Value == "" {
		return Fact{}, fmt.Errorf("%s summary fact key and value are required", name)
	}
	if len(fact.ObservationIDs) == 0 {
		return Fact{}, fmt.Errorf("%s summary fact observation provenance is required", name)
	}
	if fact.LegacyUncited && !allowLegacyUncited {
		return Fact{}, fmt.Errorf("%s summary resolved fact cannot be legacy uncited", name)
	}
	if len(fact.SupportingEvidenceIDs)+len(fact.ContradictingEvidenceIDs) == 0 && !fact.LegacyUncited {
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
	fact.ObservationIDs = observationIDs
	fact.SupportingEvidenceIDs = supportingEvidenceIDs
	fact.ContradictingEvidenceIDs = contradictingEvidenceIDs
	return fact, nil
}

func (reason UnresolvedReason) valid() bool {
	switch reason {
	case UnresolvedConflict,
		UnresolvedTransition,
		UnresolvedTemporalUncertainty,
		UnresolvedHypothesis,
		UnresolvedCounterevidenceOnly,
		UnresolvedLegacyUncited:
		return true
	default:
		return false
	}
}

func orderedFacts(facts map[string]Fact) []Fact {
	keys := make([]string, 0, len(facts))
	for key := range facts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make([]Fact, 0, len(keys))
	for _, key := range keys {
		ordered = append(ordered, cloneFact(facts[key]))
	}
	return ordered
}

func orderedUnresolved(items map[string]UnresolvedFact) []UnresolvedFact {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make([]UnresolvedFact, 0, len(keys))
	for _, key := range keys {
		item := items[key]
		item.Candidates = append([]Fact(nil), item.Candidates...)
		for index := range item.Candidates {
			item.Candidates[index] = cloneFact(item.Candidates[index])
		}
		ordered = append(ordered, item)
	}
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
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left] < normalized[right]
	})
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
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left] < normalized[right]
	})
	return normalized, nil
}

func cloneFact(fact Fact) Fact {
	fact.ObservationIDs = append([]observation.ObservationID(nil), fact.ObservationIDs...)
	fact.SupportingEvidenceIDs = append([]evidence.EvidenceID(nil), fact.SupportingEvidenceIDs...)
	fact.ContradictingEvidenceIDs = append([]evidence.EvidenceID(nil), fact.ContradictingEvidenceIDs...)
	return fact
}
