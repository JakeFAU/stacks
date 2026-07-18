package query

import (
	"fmt"
	"sort"
	"strings"

	"stacks/internal/knowledge"
)

// Fact is an aggregated state value with its supporting provenance.
type Fact struct {
	Key            string
	Value          string
	ObservationIDs []knowledge.ObservationID
	EvidenceIDs    []knowledge.EvidenceID
}

// WindowSummary is a pre-narration aggregate for one resolved window.
// UnresolvedKeys records conflicts or gaps that aggregation could not settle.
type WindowSummary struct {
	Selection      TemporalSelection
	Facts          []Fact
	UnresolvedKeys []string
}

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
	Before         TemporalSelection
	After          TemporalSelection
	BeforeFacts    []Fact
	AfterFacts     []Fact
	Changes        []Change
	UnresolvedKeys []string
}

// CompareWindowSummaries computes semantic changes without asking a model to
// infer them. Keys unresolved in either window are excluded from Changes.
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
		Before:      before.Selection,
		After:       after.Selection,
		BeforeFacts: orderedFacts(beforeFacts),
		AfterFacts:  orderedFacts(afterFacts),
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
			afterCopy := afterFact
			comparison.Changes = append(comparison.Changes, Change{
				Kind:  ChangeAdded,
				Key:   key,
				After: &afterCopy,
			})
		case inBefore && !inAfter:
			beforeCopy := beforeFact
			comparison.Changes = append(comparison.Changes, Change{
				Kind:   ChangeRemoved,
				Key:    key,
				Before: &beforeCopy,
			})
		case inBefore && inAfter && beforeFact.Value != afterFact.Value:
			beforeCopy := beforeFact
			afterCopy := afterFact
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

func validateSummary(name string, summary WindowSummary) (map[string]Fact, map[string]struct{}, error) {
	if summary.Selection.kind != SelectionWindow {
		return nil, nil, fmt.Errorf("%s summary requires a window selection", name)
	}

	facts := make(map[string]Fact, len(summary.Facts))
	for _, fact := range summary.Facts {
		fact.Key = strings.TrimSpace(fact.Key)
		fact.Value = strings.TrimSpace(fact.Value)
		if fact.Key == "" || fact.Value == "" {
			return nil, nil, fmt.Errorf("%s summary fact key and value are required", name)
		}
		if len(fact.ObservationIDs) == 0 || len(fact.EvidenceIDs) == 0 {
			return nil, nil, fmt.Errorf("%s summary fact provenance is required", name)
		}
		observationIDs, err := normalizeObservationIDs(name, fact.ObservationIDs)
		if err != nil {
			return nil, nil, err
		}
		evidenceIDs, err := normalizeEvidenceIDs(name, fact.EvidenceIDs)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := facts[fact.Key]; exists {
			return nil, nil, fmt.Errorf("%s summary fact keys must be unique", name)
		}
		fact.ObservationIDs = observationIDs
		fact.EvidenceIDs = evidenceIDs
		facts[fact.Key] = fact
	}

	unresolved := make(map[string]struct{}, len(summary.UnresolvedKeys))
	for _, key := range summary.UnresolvedKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, nil, fmt.Errorf("%s summary unresolved key is required", name)
		}
		if _, exists := unresolved[key]; exists {
			return nil, nil, fmt.Errorf("%s summary unresolved keys must be unique", name)
		}
		if _, exists := facts[key]; exists {
			return nil, nil, fmt.Errorf("%s summary key cannot be both resolved and unresolved", name)
		}
		unresolved[key] = struct{}{}
	}
	return facts, unresolved, nil
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

func normalizeObservationIDs(name string, values []knowledge.ObservationID) ([]knowledge.ObservationID, error) {
	normalized := make([]knowledge.ObservationID, len(values))
	seen := make(map[knowledge.ObservationID]struct{}, len(values))
	for index, value := range values {
		value = knowledge.ObservationID(strings.TrimSpace(string(value)))
		if value == "" {
			return nil, fmt.Errorf("%s summary observation ID is required", name)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("%s summary observation IDs must be unique", name)
		}
		seen[value] = struct{}{}
		normalized[index] = value
	}
	return normalized, nil
}

func normalizeEvidenceIDs(name string, values []knowledge.EvidenceID) ([]knowledge.EvidenceID, error) {
	normalized := make([]knowledge.EvidenceID, len(values))
	seen := make(map[knowledge.EvidenceID]struct{}, len(values))
	for index, value := range values {
		value = knowledge.EvidenceID(strings.TrimSpace(string(value)))
		if value == "" {
			return nil, fmt.Errorf("%s summary evidence ID is required", name)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("%s summary evidence IDs must be unique", name)
		}
		seen[value] = struct{}{}
		normalized[index] = value
	}
	return normalized, nil
}

func cloneFact(fact Fact) Fact {
	fact.ObservationIDs = append([]knowledge.ObservationID(nil), fact.ObservationIDs...)
	fact.EvidenceIDs = append([]knowledge.EvidenceID(nil), fact.EvidenceIDs...)
	return fact
}
