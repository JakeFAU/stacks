package query

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

func validatePointResult(value PointInTimeResult) error {
	return validateStateMaterial(value.Facts, value.Unresolved)
}

func validateTrendResult(value TrendResult) error {
	if err := validateStateMaterial(value.Before.Facts, value.Before.Unresolved); err != nil {
		return err
	}
	if err := validateStateMaterial(value.After.Facts, value.After.Unresolved); err != nil {
		return err
	}
	if err := validateChanges(value.Changes); err != nil {
		return err
	}
	return validateTrendRelationships(value)
}

func validateTrajectoryResult(value TrajectoryResult) error {
	if err := validateStateMaterial(nil, value.Unresolved); err != nil {
		return err
	}
	for _, transition := range value.Transitions {
		if err := validateTransition(transition); err != nil {
			return err
		}
	}
	return nil
}

func validateCausalResult(value CausalChainResult) error {
	for _, link := range value.Links {
		if err := validateCanonicalResultTerm("causal cause", link.Cause); err != nil {
			return err
		}
		if err := validateCanonicalResultTerm("causal effect", link.Effect); err != nil {
			return err
		}
		if err := validateResultContributions(link.Contributions); err != nil {
			return err
		}
		if err := validateResultCitationCollections(link.SupportingCitations, link.ContradictingCitations); err != nil {
			return err
		}
	}
	return nil
}

func validateStateMaterial(facts []Fact, unresolved []UnresolvedItem) error {
	resolvedKeys := make(map[temporal.StateKey]struct{}, len(facts))
	for _, fact := range facts {
		if err := validateResultFact(fact); err != nil {
			return err
		}
		if _, exists := resolvedKeys[fact.Key]; exists {
			return fmt.Errorf("result fact keys must be unique")
		}
		resolvedKeys[fact.Key] = struct{}{}
	}

	unresolvedKeys := make(map[temporal.StateKey]struct{}, len(unresolved))
	for _, item := range unresolved {
		if err := validateResultStateKey("unresolved key", item.Key); err != nil {
			return err
		}
		if !validUnresolvedReason(item.Reason) {
			return fmt.Errorf("result unresolved reason is invalid")
		}
		if len(item.Candidates) == 0 {
			return fmt.Errorf("result unresolved candidates are required")
		}
		if item.Reason == temporal.UnresolvedConflict && len(item.Candidates) < 2 {
			return fmt.Errorf("result unresolved conflict requires multiple candidates")
		}
		if _, exists := unresolvedKeys[item.Key]; exists {
			return fmt.Errorf("result unresolved keys must be unique")
		}
		if _, exists := resolvedKeys[item.Key]; exists {
			return fmt.Errorf("result key cannot be both resolved and unresolved")
		}
		unresolvedKeys[item.Key] = struct{}{}

		candidateValues := make([]observation.Term, 0, len(item.Candidates))
		for _, candidate := range item.Candidates {
			if err := validateResultFact(candidate); err != nil {
				return err
			}
			if temporal.CompareStateKeys(candidate.Key, item.Key) != 0 {
				return fmt.Errorf("result unresolved candidate key must match its parent")
			}
			if slices.ContainsFunc(candidateValues, func(value observation.Term) bool {
				return temporal.CompareTerms(value, candidate.Value) == 0
			}) {
				return fmt.Errorf("result unresolved candidate values must be unique")
			}
			candidateValues = append(candidateValues, candidate.Value)
		}
	}
	return nil
}

func validateResultFact(value Fact) error {
	if err := validateResultStateKey("fact key", value.Key); err != nil {
		return err
	}
	if err := validateCanonicalResultTerm("fact value", value.Value); err != nil {
		return err
	}
	if err := validateResultContributions(value.Contributions); err != nil {
		return err
	}
	return validateResultCitationCollections(value.SupportingCitations, value.ContradictingCitations)
}

func validateResultStateKey(name string, value temporal.StateKey) error {
	if _, err := temporal.NewStateKey(value.Subject, value.Predicate); err != nil {
		return fmt.Errorf("result %s is invalid: %w", name, err)
	}
	return nil
}

func validateCanonicalResultTerm(name string, value observation.Term) error {
	if _, err := temporal.NewStateKey(value, CausalPredicate); err != nil {
		return fmt.Errorf("result %s is invalid: %w", name, err)
	}
	return nil
}

func validUnresolvedReason(value temporal.UnresolvedReason) bool {
	switch value {
	case temporal.UnresolvedConflict,
		temporal.UnresolvedTransition,
		temporal.UnresolvedTemporalUncertainty,
		temporal.UnresolvedHypothesis,
		temporal.UnresolvedCounterevidenceOnly:
		return true
	default:
		return false
	}
}

func validateResultContributions(values []Contribution) error {
	if len(values) == 0 {
		return fmt.Errorf("result contribution provenance is required")
	}
	seen := make(map[observation.ObservationID]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(string(value.ObservationID)) == "" {
			return fmt.Errorf("result contribution observation ID is required")
		}
		if _, exists := seen[value.ObservationID]; exists {
			return fmt.Errorf("result contribution observation IDs must be unique")
		}
		seen[value.ObservationID] = struct{}{}
		if !validEpistemicStatus(value.Status) {
			return fmt.Errorf("result contribution epistemic status is invalid")
		}
		if err := validateResultTemporalExtent(value.ValidTime); err != nil {
			return err
		}
		if value.RecordedAt.IsZero() {
			return fmt.Errorf("result contribution recorded time is required")
		}
		if err := validateResultDerivation(value.Derivation); err != nil {
			return err
		}
		if optionalWhitespace(value.SubjectGroundingMentionID) ||
			optionalWhitespace(value.ObjectGroundingMentionID) {
			return fmt.Errorf("result contribution grounding mention is invalid")
		}
	}
	return nil
}

func validEpistemicStatus(value observation.EpistemicStatus) bool {
	switch value {
	case observation.StatusObserved,
		observation.StatusInferred,
		observation.StatusHypothesized,
		observation.StatusValidatedStructurally,
		observation.StatusValidatedEmpirically,
		observation.StatusRejected:
		return true
	default:
		return false
	}
}

func validateResultTemporalExtent(value observation.TemporalExtent) error {
	switch value.Kind() {
	case observation.TemporalUnknown:
		return nil
	case observation.TemporalInstant:
		instant, ok := value.Instant()
		if !ok || instant.IsZero() {
			return fmt.Errorf("result contribution valid instant is invalid")
		}
	case observation.TemporalInterval:
		start, hasStart, end, hasEnd := value.Bounds()
		if !hasStart && !hasEnd {
			return fmt.Errorf("result contribution valid interval requires a bound")
		}
		if hasStart && start.IsZero() || hasEnd && end.IsZero() {
			return fmt.Errorf("result contribution valid interval bound is invalid")
		}
		if hasStart && hasEnd && !end.After(start) {
			return fmt.Errorf("result contribution valid interval is invalid")
		}
	case observation.TemporalWindow:
		start, hasStart, end, hasEnd := value.Bounds()
		if !hasStart || !hasEnd || start.IsZero() || end.IsZero() || !end.After(start) {
			return fmt.Errorf("result contribution valid window is invalid")
		}
	default:
		return fmt.Errorf("result contribution valid time kind is invalid")
	}
	return nil
}

func validateResultDerivation(value observation.Derivation) error {
	if strings.TrimSpace(value.Method) == "" {
		return fmt.Errorf("result contribution derivation method is required")
	}
	if strings.TrimSpace(value.Version) == "" {
		return fmt.Errorf("result contribution derivation version is required")
	}
	if optionalWhitespace(value.RunID) ||
		optionalWhitespace(value.Model) ||
		optionalWhitespace(value.PromptVersion) {
		return fmt.Errorf("result contribution derivation provenance is invalid")
	}
	if (value.Model == "") != (value.PromptVersion == "") {
		return fmt.Errorf("result contribution model and prompt version must appear together")
	}
	return nil
}

func optionalWhitespace(value string) bool {
	return value != "" && strings.TrimSpace(value) == ""
}

func validateResultCitationCollections(supporting, contradicting []Citation) error {
	if len(supporting)+len(contradicting) == 0 {
		return fmt.Errorf("result citation provenance is required")
	}
	if err := validateUniqueResultCitations(supporting); err != nil {
		return err
	}
	return validateUniqueResultCitations(contradicting)
}

func validateUniqueResultCitations(values []Citation) error {
	seen := make(map[evidence.EvidenceID]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value.EvidenceID]; exists {
			return fmt.Errorf("result citation evidence IDs must be unique within a role")
		}
		seen[value.EvidenceID] = struct{}{}
	}
	return nil
}

func validateChanges(values []Change) error {
	seen := make(map[temporal.StateKey]struct{}, len(values))
	for _, value := range values {
		if err := validateResultStateKey("change key", value.Key); err != nil {
			return err
		}
		if _, exists := seen[value.Key]; exists {
			return fmt.Errorf("result change keys must be unique")
		}
		seen[value.Key] = struct{}{}
		if err := validateFactRelationship("change", value.Kind, value.Key, value.Before, value.After); err != nil {
			return err
		}
	}
	return nil
}

func validateTransition(value Transition) error {
	if err := validateResultStateKey("transition key", value.Key); err != nil {
		return err
	}
	if err := validateResultTemporalExtent(value.ValidTime); err != nil {
		return err
	}
	if err := validateFactRelationship("transition", value.Kind, value.Key, value.Before, value.After); err != nil {
		return err
	}
	return validateStateMaterial(nil, value.Unresolved)
}

func validateFactRelationship(
	name string,
	kind temporal.ChangeKind,
	key temporal.StateKey,
	before, after *Fact,
) error {
	if err := validateChangeShape(kind, before, after); err != nil {
		return err
	}
	if before != nil {
		if err := validateResultFact(*before); err != nil {
			return err
		}
		if temporal.CompareStateKeys(before.Key, key) != 0 {
			return fmt.Errorf("result %s before fact key must match its parent", name)
		}
	}
	if after != nil {
		if err := validateResultFact(*after); err != nil {
			return err
		}
		if temporal.CompareStateKeys(after.Key, key) != 0 {
			return fmt.Errorf("result %s after fact key must match its parent", name)
		}
	}
	if kind == temporal.ChangeChanged && temporal.CompareTerms(before.Value, after.Value) == 0 {
		return fmt.Errorf("result changed %s requires different values", name)
	}
	return nil
}

func validateTrendRelationships(value TrendResult) error {
	beforeFacts := factsByStateKey(value.Before.Facts)
	afterFacts := factsByStateKey(value.After.Facts)
	unresolved := make(map[temporal.StateKey]struct{}, len(value.Before.Unresolved)+len(value.After.Unresolved))
	for _, item := range value.Before.Unresolved {
		unresolved[item.Key] = struct{}{}
	}
	for _, item := range value.After.Unresolved {
		unresolved[item.Key] = struct{}{}
	}

	keys := make(map[temporal.StateKey]struct{}, len(beforeFacts)+len(afterFacts)+len(unresolved))
	for key := range beforeFacts {
		keys[key] = struct{}{}
	}
	for key := range afterFacts {
		keys[key] = struct{}{}
	}
	for key := range unresolved {
		keys[key] = struct{}{}
	}
	orderedKeys := make([]temporal.StateKey, 0, len(keys))
	for key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	orderStateKeys(orderedKeys)

	expectedChanges := make([]Change, 0, len(orderedKeys))
	expectedUnresolved := make([]temporal.StateKey, 0, len(unresolved))
	for _, key := range orderedKeys {
		if _, exists := unresolved[key]; exists {
			expectedUnresolved = append(expectedUnresolved, key)
			continue
		}
		before, inBefore := beforeFacts[key]
		after, inAfter := afterFacts[key]
		switch {
		case !inBefore && inAfter:
			afterCopy := cloneFact(after)
			expectedChanges = append(expectedChanges, Change{Kind: temporal.ChangeAdded, Key: key, After: &afterCopy})
		case inBefore && !inAfter:
			beforeCopy := cloneFact(before)
			expectedChanges = append(expectedChanges, Change{Kind: temporal.ChangeRemoved, Key: key, Before: &beforeCopy})
		case inBefore && inAfter && temporal.CompareTerms(before.Value, after.Value) != 0:
			beforeCopy, afterCopy := cloneFact(before), cloneFact(after)
			expectedChanges = append(expectedChanges, Change{Kind: temporal.ChangeChanged, Key: key, Before: &beforeCopy, After: &afterCopy})
		}
	}

	if !slices.Equal(value.UnresolvedKeys, expectedUnresolved) {
		return fmt.Errorf("result unresolved keys do not match window material")
	}
	if !reflect.DeepEqual(value.Changes, expectedChanges) {
		return fmt.Errorf("result changes do not match before and after window material")
	}
	return nil
}

func factsByStateKey(values []Fact) map[temporal.StateKey]Fact {
	result := make(map[temporal.StateKey]Fact, len(values))
	for _, value := range values {
		result[value.Key] = value
	}
	return result
}
