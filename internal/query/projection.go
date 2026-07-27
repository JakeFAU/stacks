package query

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

type citationKey struct {
	evidenceID evidence.EvidenceID
	role       observation.EvidenceRole
}

type projectionIndex struct {
	contributions map[observation.ObservationID]Contribution
	citations     map[citationKey]Citation
}

func indexSnapshot(request Request, snapshot ReadSnapshot) ([]temporal.StateCandidate, projectionIndex, error) {
	if err := validateSnapshotAuthority(request, snapshot.Entities); err != nil {
		return nil, projectionIndex{}, err
	}

	index := projectionIndex{
		contributions: make(map[observation.ObservationID]Contribution),
		citations:     make(map[citationKey]Citation),
	}
	evidenceMetadata := make(map[evidence.EvidenceID]Citation)
	seenCandidates := make(map[observation.ObservationID]temporal.StateCandidate)
	candidates := make([]temporal.StateCandidate, 0, len(snapshot.Observations))
	for _, item := range snapshot.Observations {
		candidate, err := candidateFromReadObservation(item)
		if err != nil {
			return nil, projectionIndex{}, err
		}
		if err := indexObservationEvidence(item, &index, evidenceMetadata); err != nil {
			return nil, projectionIndex{}, err
		}

		observationID := item.Observation.ID()
		if prior, exists := seenCandidates[observationID]; exists {
			if !canonicalObservationsEqual(prior.Observation, candidate.Observation) {
				return nil, projectionIndex{}, projectionFailure("observation payload conflicts")
			}
			if !stateCandidatesEqual(prior, candidate) {
				return nil, projectionIndex{}, projectionFailure("observation state mapping conflicts")
			}
			continue
		}
		seenCandidates[observationID] = candidate
		index.contributions[observationID] = contributionFromReadObservation(item)
		candidates = append(candidates, candidate)
	}
	if err := validateCoverageReasons(snapshot.Coverage); err != nil {
		return nil, projectionIndex{}, err
	}
	return candidates, index, nil
}

func validateSnapshotAuthority(request Request, authorities []EntityAuthority) error {
	known := make(map[identity.EntityID]bool, len(authorities))
	requested := make(map[identity.EntityID]struct{}, len(request.EntityIDs))
	for _, entityID := range request.EntityIDs {
		requested[entityID] = struct{}{}
	}
	for _, authority := range authorities {
		if strings.TrimSpace(string(authority.EntityID)) == "" {
			return projectionFailure("entity authority is invalid")
		}
		if _, expected := requested[authority.EntityID]; !expected {
			return projectionFailure("entity authority is outside the selection")
		}
		if prior, exists := known[authority.EntityID]; exists && prior != authority.Known {
			return projectionFailure("entity authority conflicts")
		}
		if _, exists := known[authority.EntityID]; exists {
			return projectionFailure("entity authority is duplicated")
		}
		known[authority.EntityID] = authority.Known
	}
	for _, entityID := range request.EntityIDs {
		isKnown, exists := known[entityID]
		if !exists {
			return projectionFailure("entity authority is missing")
		}
		if !isKnown {
			return ErrEntityNotFound
		}
	}
	return nil
}

func candidateFromReadObservation(item ReadObservation) (temporal.StateCandidate, error) {
	statement := item.Observation.Statement()
	if !resolvedTermMatchesSource(item.Subject, statement.Subject, item.SubjectGroundingMentionID) ||
		!resolvedTermMatchesSource(item.Object, statement.Object, item.ObjectGroundingMentionID) {
		return temporal.StateCandidate{}, projectionFailure("resolved observation terms are invalid")
	}
	key, err := temporal.NewStateKey(item.Subject, statement.Predicate)
	if err != nil {
		return temporal.StateCandidate{}, projectionFailure("resolved observation state is invalid")
	}
	return temporal.StateCandidate{
		Key:                       key,
		Value:                     item.Object,
		Observation:               item.Observation,
		SubjectGroundingMentionID: item.SubjectGroundingMentionID,
		ObjectGroundingMentionID:  item.ObjectGroundingMentionID,
	}, nil
}

func resolvedTermMatchesSource(resolved, source observation.Term, groundingMentionID string) bool {
	if !canonicalResolvedTerm(resolved) {
		return false
	}
	switch source.Kind() {
	case observation.TermAbsent, observation.TermText:
		return groundingMentionID == "" && temporal.CompareTerms(resolved, source) == 0
	case observation.TermEntity:
		if resolved.Kind() != observation.TermEntity {
			return false
		}
		sourceID, sourceGroundingMentionID, _ := source.Entity()
		resolvedID, _, _ := resolved.Entity()
		return sourceID == resolvedID && groundingMentionID == sourceGroundingMentionID
	case observation.TermMention:
		if resolved.Kind() != observation.TermEntity {
			return false
		}
		mentionID, _ := source.MentionID()
		return groundingMentionID == mentionID
	default:
		return false
	}
}

func canonicalResolvedTerm(term observation.Term) bool {
	switch term.Kind() {
	case observation.TermAbsent:
		return true
	case observation.TermText:
		value, _ := term.Text()
		return strings.TrimSpace(value) != ""
	case observation.TermEntity:
		entityID, groundingMentionID, _ := term.Entity()
		return strings.TrimSpace(entityID) != "" && groundingMentionID == ""
	default:
		return false
	}
}

func indexObservationEvidence(item ReadObservation, index *projectionIndex, metadata map[evidence.EvidenceID]Citation) error {
	links := make(map[citationKey]struct{}, len(item.Observation.EvidenceLinks()))
	for _, link := range item.Observation.EvidenceLinks() {
		links[citationKey{evidenceID: link.EvidenceID, role: link.Role}] = struct{}{}
	}
	itemCitations := make(map[citationKey]struct{}, len(item.Evidence))
	for _, citation := range item.Evidence {
		if err := validateCitation(citation); err != nil {
			return projectionFailure("citation metadata is invalid")
		}
		key := citationKey{evidenceID: citation.EvidenceID, role: citation.Role}
		if _, linked := links[key]; !linked {
			return projectionFailure("citation role does not match observation evidence")
		}
		if prior, exists := metadata[citation.EvidenceID]; exists && !citationMetadataEqual(prior, citation) {
			return projectionFailure("evidence metadata conflicts")
		}
		metadata[citation.EvidenceID] = cloneCitation(citation)
		if prior, exists := index.citations[key]; exists && !citationsEqual(prior, citation) {
			return projectionFailure("evidence metadata conflicts")
		}
		index.citations[key] = cloneCitation(citation)
		itemCitations[key] = struct{}{}
	}
	for key := range links {
		if _, exists := itemCitations[key]; !exists {
			return projectionFailure("observation evidence is missing")
		}
	}
	return nil
}

func citationMetadataEqual(left, right Citation) bool {
	left.Role, right.Role = "", ""
	return citationsEqual(left, right)
}

func citationsEqual(left, right Citation) bool {
	return left.EvidenceID == right.EvidenceID &&
		left.Role == right.Role &&
		left.SourceDocumentID == right.SourceDocumentID &&
		left.DocumentVersionID == right.DocumentVersionID &&
		left.SectionID == right.SectionID &&
		left.SectionTitle == right.SectionTitle &&
		slices.Equal(left.SectionPath, right.SectionPath) &&
		left.SectionOrder == right.SectionOrder &&
		left.SectionRole == right.SectionRole &&
		left.StartOffset == right.StartOffset &&
		left.EndOffset == right.EndOffset &&
		left.Locator == right.Locator &&
		left.Text == right.Text
}

func cloneCitation(value Citation) Citation {
	value.SectionPath = append([]string{}, value.SectionPath...)
	return value
}

func contributionFromReadObservation(item ReadObservation) Contribution {
	return Contribution{
		ObservationID:             item.Observation.ID(),
		Status:                    item.Observation.Status(),
		ValidTime:                 item.Observation.ValidTime(),
		RecordedAt:                item.Observation.RecordedAt(),
		Derivation:                item.Observation.Derivation(),
		SubjectGroundingMentionID: item.SubjectGroundingMentionID,
		ObjectGroundingMentionID:  item.ObjectGroundingMentionID,
	}
}

func canonicalObservationsEqual(left, right observation.Observation) bool {
	if left.ID() != right.ID() ||
		left.Statement() != right.Statement() ||
		left.ValidTime() != right.ValidTime() ||
		!left.RecordedAt().Equal(right.RecordedAt()) ||
		!slices.Equal(left.EvidenceLinks(), right.EvidenceLinks()) ||
		left.Derivation() != right.Derivation() ||
		left.Status() != right.Status() {
		return false
	}
	leftConfidence, leftHasConfidence := left.Confidence()
	rightConfidence, rightHasConfidence := right.Confidence()
	return leftHasConfidence == rightHasConfidence &&
		(!leftHasConfidence || leftConfidence.Value() == rightConfidence.Value() && leftConfidence.Scale() == rightConfidence.Scale())
}

func stateCandidatesEqual(left, right temporal.StateCandidate) bool {
	return temporal.CompareStateKeys(left.Key, right.Key) == 0 &&
		temporal.CompareTerms(left.Value, right.Value) == 0 &&
		left.SubjectGroundingMentionID == right.SubjectGroundingMentionID &&
		left.ObjectGroundingMentionID == right.ObjectGroundingMentionID
}

func projectTrend(comparison temporal.Comparison, index projectionIndex) (TrendResult, error) {
	before, err := projectWindow(comparison.Before, comparison.BeforeFacts, comparison.BeforeUnresolved, index)
	if err != nil {
		return TrendResult{}, err
	}
	after, err := projectWindow(comparison.After, comparison.AfterFacts, comparison.AfterUnresolved, index)
	if err != nil {
		return TrendResult{}, err
	}
	changes := make([]Change, len(comparison.Changes))
	for position, change := range comparison.Changes {
		beforeFact, err := projectFactPointer(change.Before, index)
		if err != nil {
			return TrendResult{}, err
		}
		afterFact, err := projectFactPointer(change.After, index)
		if err != nil {
			return TrendResult{}, err
		}
		changes[position] = Change{Kind: change.Kind, Key: change.Key, Before: beforeFact, After: afterFact}
	}
	return TrendResult{
		Before:         before,
		After:          after,
		Changes:        changes,
		UnresolvedKeys: append([]temporal.StateKey{}, comparison.UnresolvedKeys...),
	}, nil
}

func projectPoint(summary temporal.PointSummary, index projectionIndex) (PointInTimeResult, error) {
	projected, err := projectStateMaterial(summary.Facts, summary.Unresolved, index)
	if err != nil {
		return PointInTimeResult{}, err
	}
	return PointInTimeResult{
		Selection:  summary.Selection,
		Facts:      projected.facts,
		Unresolved: projected.unresolved,
	}, nil
}

func projectWindow(
	selection temporal.TemporalSelection,
	facts []temporal.Fact,
	unresolved []temporal.UnresolvedFact,
	index projectionIndex,
) (WindowResult, error) {
	projected, err := projectStateMaterial(facts, unresolved, index)
	if err != nil {
		return WindowResult{}, err
	}
	return WindowResult{
		Selection:  selection,
		Facts:      projected.facts,
		Unresolved: projected.unresolved,
	}, nil
}

type projectedStateMaterial struct {
	facts      []Fact
	unresolved []UnresolvedItem
}

func projectStateMaterial(
	facts []temporal.Fact,
	unresolved []temporal.UnresolvedFact,
	index projectionIndex,
) (projectedStateMaterial, error) {
	projectedFacts := make([]Fact, len(facts))
	for position, fact := range facts {
		projected, err := projectFact(fact, index)
		if err != nil {
			return projectedStateMaterial{}, err
		}
		projectedFacts[position] = projected
	}
	projectedUnresolved := make([]UnresolvedItem, len(unresolved))
	for position, item := range unresolved {
		candidates := make([]Fact, len(item.Candidates))
		for candidatePosition, candidate := range item.Candidates {
			projected, err := projectFact(candidate, index)
			if err != nil {
				return projectedStateMaterial{}, err
			}
			candidates[candidatePosition] = projected
		}
		projectedUnresolved[position] = UnresolvedItem{Key: item.Key, Reason: item.Reason, Candidates: candidates}
	}
	return projectedStateMaterial{facts: projectedFacts, unresolved: projectedUnresolved}, nil
}

func projectFactPointer(value *temporal.Fact, index projectionIndex) (*Fact, error) {
	if value == nil {
		return nil, nil
	}
	projected, err := projectFact(*value, index)
	if err != nil {
		return nil, err
	}
	return &projected, nil
}

func projectFact(value temporal.Fact, index projectionIndex) (Fact, error) {
	contributions := make([]Contribution, len(value.ObservationIDs))
	for position, observationID := range value.ObservationIDs {
		contribution, exists := index.contributions[observationID]
		if !exists {
			return Fact{}, projectionFailure("observation contribution is missing")
		}
		contributions[position] = contribution
	}
	supporting, err := projectCitations(value.SupportingEvidenceIDs, observation.EvidenceSupporting, index)
	if err != nil {
		return Fact{}, err
	}
	contradicting, err := projectCitations(value.ContradictingEvidenceIDs, observation.EvidenceContradicting, index)
	if err != nil {
		return Fact{}, err
	}
	return Fact{
		Key:                    value.Key,
		Value:                  value.Value,
		Contributions:          contributions,
		SupportingCitations:    supporting,
		ContradictingCitations: contradicting,
	}, nil
}

func projectCitations(ids []evidence.EvidenceID, role observation.EvidenceRole, index projectionIndex) ([]Citation, error) {
	citations := make([]Citation, len(ids))
	for position, evidenceID := range ids {
		citation, exists := index.citations[citationKey{evidenceID: evidenceID, role: role}]
		if !exists {
			return nil, projectionFailure("fact citation is missing")
		}
		citations[position] = cloneCitation(citation)
	}
	return citations, nil
}

func projectTrendGaps(
	request Request,
	snapshot ReadSnapshot,
	candidates []temporal.StateCandidate,
	before temporal.WindowSummary,
	after temporal.WindowSummary,
) ([]Gap, error) {
	return projectStateGaps(
		request,
		snapshot,
		candidates,
		[]stateGapMaterial{
			{selection: before.Selection, facts: before.Facts, unresolved: before.Unresolved},
			{selection: after.Selection, facts: after.Facts, unresolved: after.Unresolved},
		},
	)
}

func projectPointGaps(
	request Request,
	snapshot ReadSnapshot,
	candidates []temporal.StateCandidate,
	summary temporal.PointSummary,
) ([]Gap, error) {
	return projectStateGaps(
		request,
		snapshot,
		candidates,
		[]stateGapMaterial{{
			selection:  summary.Selection,
			facts:      summary.Facts,
			unresolved: summary.Unresolved,
		}},
	)
}

type stateGapMaterial struct {
	selection  temporal.TemporalSelection
	facts      []temporal.Fact
	unresolved []temporal.UnresolvedFact
}

func projectStateGaps(
	request Request,
	snapshot ReadSnapshot,
	candidates []temporal.StateCandidate,
	summaries []stateGapMaterial,
) ([]Gap, error) {
	if err := validateCoverageReasons(snapshot.Coverage); err != nil {
		return nil, err
	}
	gaps := make(map[Gap]struct{})
	hasMaterial := make(map[identity.EntityID]bool, len(request.EntityIDs))
	for _, coverage := range snapshot.Coverage {
		switch coverage.Reason {
		case CoverageUnresolvedMention:
			gaps[Gap{Kind: GapUnresolvedMention, EntityID: coverage.EntityID, Predicate: coverage.Predicate}] = struct{}{}
		case CoverageAuthorityExcluded:
			gaps[Gap{Kind: GapAuthorityExcluded, EntityID: coverage.EntityID, Predicate: coverage.Predicate}] = struct{}{}
		case CoverageEntityFiltered, CoveragePredicateFiltered:
		}
	}
	for _, summary := range summaries {
		addValidTimeGaps(gaps, request, candidates, summary.selection)
		addStateSummaryMaterial(hasMaterial, summary)
	}
	for gap := range gaps {
		if gap.EntityID != "" {
			hasMaterial[gap.EntityID] = true
		}
	}
	for _, entityID := range request.EntityIDs {
		if !hasMaterial[entityID] {
			gaps[Gap{Kind: GapNoEvidence, EntityID: entityID}] = struct{}{}
		}
	}

	result := make([]Gap, 0, len(gaps))
	for gap := range gaps {
		result = append(result, gap)
	}
	orderGaps(result)
	return result, nil
}

func addValidTimeGaps(
	gaps map[Gap]struct{},
	request Request,
	candidates []temporal.StateCandidate,
	selection temporal.TemporalSelection,
) {
	for _, candidate := range candidates {
		if candidate.Observation.Status() == observation.StatusRejected {
			continue
		}
		if !definitelyOutsideSelection(candidate.Observation.ValidTime(), selection) {
			continue
		}
		entityIDs := candidateEntityIDs(candidate)
		added := false
		for _, entityID := range entityIDs {
			if slices.Contains(request.EntityIDs, entityID) {
				gaps[Gap{Kind: GapValidTimeExcluded, EntityID: entityID, Predicate: candidate.Key.Predicate, SelectionLabel: selection.Label()}] = struct{}{}
				added = true
			}
		}
		if !added {
			gaps[Gap{Kind: GapValidTimeExcluded, Predicate: candidate.Key.Predicate, SelectionLabel: selection.Label()}] = struct{}{}
		}
	}
}

func addStateSummaryMaterial(hasMaterial map[identity.EntityID]bool, summary stateGapMaterial) {
	for _, fact := range summary.facts {
		addStateMaterialEntityIDs(hasMaterial, fact.Key, fact.Value)
	}
	for _, item := range summary.unresolved {
		for _, candidate := range item.Candidates {
			addStateMaterialEntityIDs(hasMaterial, candidate.Key, candidate.Value)
		}
	}
}

func addStateMaterialEntityIDs(
	hasMaterial map[identity.EntityID]bool,
	key temporal.StateKey,
	value observation.Term,
) {
	for _, entityID := range stateEntityIDs(key, value) {
		hasMaterial[entityID] = true
	}
}

func definitelyOutsideSelection(extent observation.TemporalExtent, selection temporal.TemporalSelection) bool {
	if point, ok := selection.Point(); ok {
		switch extent.Kind() {
		case observation.TemporalInstant:
			instant, _ := extent.Instant()
			return !instant.Equal(point)
		case observation.TemporalInterval, observation.TemporalWindow:
			return !pointInsideExtent(extent, point)
		default:
			return false
		}
	}
	windowStart, windowEnd, ok := selection.Window()
	if !ok {
		return false
	}
	switch extent.Kind() {
	case observation.TemporalInstant:
		instant, _ := extent.Instant()
		return instant.Before(windowStart) || !instant.Before(windowEnd)
	case observation.TemporalInterval, observation.TemporalWindow:
		start, hasStart, end, hasEnd := extent.Bounds()
		return hasStart && !start.Before(windowEnd) || hasEnd && !end.After(windowStart)
	default:
		return false
	}
}

func pointInsideExtent(extent observation.TemporalExtent, point time.Time) bool {
	start, hasStart, end, hasEnd := extent.Bounds()
	return (!hasStart || !point.Before(start)) && (!hasEnd || point.Before(end))
}

func candidateEntityIDs(candidate temporal.StateCandidate) []identity.EntityID {
	return stateEntityIDs(candidate.Key, candidate.Value)
}

func stateEntityIDs(key temporal.StateKey, value observation.Term) []identity.EntityID {
	result := make([]identity.EntityID, 0, 2)
	for _, term := range []observation.Term{key.Subject, value} {
		entityID, _, ok := term.Entity()
		if !ok {
			continue
		}
		value := identity.EntityID(entityID)
		if !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	orderEntityIDs(result)
	return result
}

func validateCoverageReasons(values []Coverage) error {
	for _, coverage := range values {
		switch coverage.Reason {
		case CoverageUnresolvedMention, CoverageAuthorityExcluded, CoverageEntityFiltered, CoveragePredicateFiltered:
		default:
			return projectionFailure("coverage reason is invalid")
		}
	}
	return nil
}

func projectionFailure(reason string) error {
	return errors.New("project temporal snapshot: " + reason)
}
