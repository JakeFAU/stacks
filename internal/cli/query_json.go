package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"

	"stacks/internal/query"
)

const temporalQuerySchemaVersion = "stacks.temporal-query.v1"

type queryEnvelopeJSON struct {
	SchemaVersion string           `json:"schema_version"`
	Intent        string           `json:"intent"`
	Request       queryRequestJSON `json:"request"`
	Result        queryResultJSON  `json:"result"`
	Gaps          []queryGapJSON   `json:"gaps"`
}

type queryRequestJSON struct {
	EntityIDs      []string             `json:"entity_ids"`
	EntityMatch    string               `json:"entity_match"`
	Predicates     []string             `json:"predicates"`
	Selections     []querySelectionJSON `json:"selections"`
	KnowledgeScope queryKnowledgeJSON   `json:"knowledge_scope"`
	Limit          int                  `json:"limit"`
}

type querySelectionJSON struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	At    string `json:"at,omitempty"`
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

type queryKnowledgeJSON struct {
	Kind string `json:"kind"`
	At   string `json:"at,omitempty"`
}

// queryResultJSON is the only custom map-like wire behavior.
type queryResultJSON struct {
	Intent     temporal.Intent
	Point      *queryPointJSON
	Trend      *queryTrendJSON
	Trajectory *queryTrajectoryJSON
	Causal     *queryCausalJSON
}

func (value queryResultJSON) MarshalJSON() ([]byte, error) {
	switch value.Intent {
	case temporal.IntentPointInTime:
		if value.Point == nil || value.Trend != nil || value.Trajectory != nil || value.Causal != nil {
			return nil, fmt.Errorf("query JSON result union is invalid")
		}
		type result struct {
			Point *queryPointJSON `json:"point"`
		}
		return json.Marshal(result{Point: value.Point})
	case temporal.IntentTrendComparison:
		if value.Point != nil || value.Trend == nil || value.Trajectory != nil || value.Causal != nil {
			return nil, fmt.Errorf("query JSON result union is invalid")
		}
		type result struct {
			Trend *queryTrendJSON `json:"trend"`
		}
		return json.Marshal(result{Trend: value.Trend})
	case temporal.IntentTrajectory:
		if value.Point != nil || value.Trend != nil || value.Trajectory == nil || value.Causal != nil {
			return nil, fmt.Errorf("query JSON result union is invalid")
		}
		type result struct {
			Trajectory *queryTrajectoryJSON `json:"trajectory"`
		}
		return json.Marshal(result{Trajectory: value.Trajectory})
	case temporal.IntentCausalChain:
		if value.Point != nil || value.Trend != nil || value.Trajectory != nil || value.Causal == nil {
			return nil, fmt.Errorf("query JSON result union is invalid")
		}
		type result struct {
			Causal *queryCausalJSON `json:"causal"`
		}
		return json.Marshal(result{Causal: value.Causal})
	default:
		return nil, fmt.Errorf("query JSON result union is invalid")
	}
}

type queryPointJSON struct {
	Selection  querySelectionJSON    `json:"selection"`
	Facts      []queryFactJSON       `json:"facts"`
	Unresolved []queryUnresolvedJSON `json:"unresolved"`
}

type queryTrendJSON struct {
	Before         queryWindowJSON     `json:"before"`
	After          queryWindowJSON     `json:"after"`
	Changes        []queryChangeJSON   `json:"changes"`
	UnresolvedKeys []queryStateKeyJSON `json:"unresolved_keys"`
}

type queryWindowJSON struct {
	Selection  querySelectionJSON    `json:"selection"`
	Facts      []queryFactJSON       `json:"facts"`
	Unresolved []queryUnresolvedJSON `json:"unresolved"`
}

type queryStateKeyJSON struct {
	Subject   queryTermJSONDTO `json:"subject"`
	Predicate string           `json:"predicate"`
}

type queryTermJSONDTO struct {
	Kind      string `json:"kind"`
	Text      string `json:"text,omitempty"`
	MentionID string `json:"mention_id,omitempty"`
	EntityID  string `json:"entity_id,omitempty"`
}

type queryExtentJSON struct {
	Kind  string `json:"kind"`
	At    string `json:"at,omitempty"`
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

type queryContributionJSON struct {
	ObservationID             string              `json:"observation_id"`
	Status                    string              `json:"status"`
	ValidTime                 queryExtentJSON     `json:"valid_time"`
	RecordedAt                string              `json:"recorded_at"`
	Derivation                queryDerivationJSON `json:"derivation"`
	SubjectGroundingMentionID string              `json:"subject_grounding_mention_id,omitempty"`
	ObjectGroundingMentionID  string              `json:"object_grounding_mention_id,omitempty"`
}

type queryDerivationJSON struct {
	Method        string `json:"method"`
	Version       string `json:"version"`
	RunID         string `json:"run_id,omitempty"`
	Model         string `json:"model,omitempty"`
	PromptVersion string `json:"prompt_version,omitempty"`
}

type queryCitationJSON struct {
	EvidenceID        string   `json:"evidence_id"`
	Role              string   `json:"role"`
	SourceDocumentID  string   `json:"source_document_id"`
	DocumentVersionID string   `json:"document_version_id"`
	SectionID         string   `json:"section_id"`
	SectionTitle      string   `json:"section_title"`
	SectionPath       []string `json:"section_path"`
	SectionOrder      int      `json:"section_order"`
	SectionRole       string   `json:"section_role"`
	StartOffset       int      `json:"start_offset"`
	EndOffset         int      `json:"end_offset"`
	Locator           string   `json:"locator,omitempty"`
	Text              string   `json:"text,omitempty"`
}

type queryFactJSON struct {
	Key                    queryStateKeyJSON       `json:"key"`
	Value                  queryTermJSONDTO        `json:"value"`
	Contributions          []queryContributionJSON `json:"contributions"`
	SupportingCitations    []queryCitationJSON     `json:"supporting_citations"`
	ContradictingCitations []queryCitationJSON     `json:"contradicting_citations"`
}

type queryUnresolvedJSON struct {
	Key        queryStateKeyJSON `json:"key"`
	Reason     string            `json:"reason"`
	Candidates []queryFactJSON   `json:"candidates"`
}

type queryChangeJSON struct {
	Kind   string            `json:"kind"`
	Key    queryStateKeyJSON `json:"key"`
	Before *queryFactJSON    `json:"before,omitempty"`
	After  *queryFactJSON    `json:"after,omitempty"`
}

type queryTrajectoryJSON struct {
	Selection   querySelectionJSON    `json:"selection"`
	Transitions []queryTransitionJSON `json:"transitions"`
	Unresolved  []queryUnresolvedJSON `json:"unresolved"`
}

type queryTransitionJSON struct {
	Kind       string                `json:"kind"`
	Key        queryStateKeyJSON     `json:"key"`
	ValidTime  queryExtentJSON       `json:"valid_time"`
	Before     *queryFactJSON        `json:"before,omitempty"`
	After      *queryFactJSON        `json:"after,omitempty"`
	Unresolved []queryUnresolvedJSON `json:"unresolved"`
}

type queryCausalJSON struct {
	Selection querySelectionJSON    `json:"selection"`
	Links     []queryCausalLinkJSON `json:"links"`
}

type queryCausalLinkJSON struct {
	Cause                  queryTermJSONDTO        `json:"cause"`
	Effect                 queryTermJSONDTO        `json:"effect"`
	Contributions          []queryContributionJSON `json:"contributions"`
	SupportingCitations    []queryCitationJSON     `json:"supporting_citations"`
	ContradictingCitations []queryCitationJSON     `json:"contradicting_citations"`
}

type queryGapJSON struct {
	Kind           string `json:"kind"`
	EntityID       string `json:"entity_id,omitempty"`
	Predicate      string `json:"predicate,omitempty"`
	SelectionLabel string `json:"selection_label,omitempty"`
}

func renderQueryJSON(result query.Result) ([]byte, error) {
	if err := query.ValidateResult(result); err != nil {
		return nil, fmt.Errorf("render query JSON: invalid result: %w", err)
	}
	request, err := queryRequestToJSON(result)
	if err != nil {
		return nil, err
	}
	union := queryResultJSON{Intent: result.Intent}
	switch result.Intent {
	case temporal.IntentPointInTime:
		point, ok := result.Payload.Point()
		if !ok {
			return nil, fmt.Errorf("render query JSON: point result is required")
		}
		converted, err := queryPointToJSON(point)
		if err != nil {
			return nil, err
		}
		union.Point = &converted
	case temporal.IntentTrendComparison:
		trend, ok := result.Payload.Trend()
		if !ok {
			return nil, fmt.Errorf("render query JSON: trend result is required")
		}
		converted, err := queryTrendToJSON(trend)
		if err != nil {
			return nil, err
		}
		union.Trend = &converted
	case temporal.IntentTrajectory:
		trajectory, ok := result.Payload.Trajectory()
		if !ok {
			return nil, fmt.Errorf("render query JSON: trajectory result is required")
		}
		converted, err := queryTrajectoryToJSON(trajectory)
		if err != nil {
			return nil, err
		}
		union.Trajectory = &converted
	case temporal.IntentCausalChain:
		causal, ok := result.Payload.Causal()
		if !ok {
			return nil, fmt.Errorf("render query JSON: causal result is required")
		}
		converted, err := queryCausalToJSON(causal)
		if err != nil {
			return nil, err
		}
		union.Causal = &converted
	default:
		return nil, fmt.Errorf("render query JSON: result intent is invalid")
	}
	envelope := queryEnvelopeJSON{
		SchemaVersion: temporalQuerySchemaVersion,
		Intent:        string(result.Intent),
		Request:       request,
		Result:        union,
		Gaps:          queryGapsToJSON(result.Gaps),
	}
	rendered, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("render query JSON: %w", err)
	}
	return append(rendered, '\n'), nil
}

func queryPointToJSON(value query.PointInTimeResult) (queryPointJSON, error) {
	selection, err := querySelectionToJSON(value.Selection)
	if err != nil {
		return queryPointJSON{}, err
	}
	facts, err := queryFactsToJSON(value.Facts)
	if err != nil {
		return queryPointJSON{}, err
	}
	unresolved, err := queryUnresolvedToJSON(value.Unresolved)
	if err != nil {
		return queryPointJSON{}, err
	}
	return queryPointJSON{Selection: selection, Facts: facts, Unresolved: unresolved}, nil
}

func queryRequestToJSON(result query.Result) (queryRequestJSON, error) {
	entityIDs := make([]string, len(result.EntityIDs))
	for index, entityID := range result.EntityIDs {
		entityIDs[index] = string(entityID)
	}
	predicates := make([]string, len(result.Predicates))
	for index, predicate := range result.Predicates {
		predicates[index] = string(predicate)
	}
	selections := make([]querySelectionJSON, len(result.Selections))
	for index, selection := range result.Selections {
		converted, err := querySelectionToJSON(selection)
		if err != nil {
			return queryRequestJSON{}, fmt.Errorf("render query JSON request: %w", err)
		}
		selections[index] = converted
	}
	knowledge, err := queryKnowledgeToJSON(result.KnowledgeScope)
	if err != nil {
		return queryRequestJSON{}, fmt.Errorf("render query JSON request: %w", err)
	}
	return queryRequestJSON{
		EntityIDs: entityIDs, EntityMatch: string(result.EntityMatch),
		Predicates: predicates, Selections: selections, KnowledgeScope: knowledge, Limit: result.Limit,
	}, nil
}

func querySelectionToJSON(value temporal.TemporalSelection) (querySelectionJSON, error) {
	switch value.Kind() {
	case temporal.SelectionPoint:
		at, ok := value.Point()
		if !ok {
			return querySelectionJSON{}, fmt.Errorf("selection point is invalid")
		}
		return querySelectionJSON{Kind: "point", Label: value.Label(), At: queryTime(at)}, nil
	case temporal.SelectionWindow:
		start, end, ok := value.Window()
		if !ok {
			return querySelectionJSON{}, fmt.Errorf("selection window is invalid")
		}
		return querySelectionJSON{Kind: "window", Label: value.Label(), Start: queryTime(start), End: queryTime(end)}, nil
	default:
		return querySelectionJSON{}, fmt.Errorf("selection kind is invalid")
	}
}

func queryKnowledgeToJSON(value temporal.KnowledgeScope) (queryKnowledgeJSON, error) {
	switch value.Kind() {
	case temporal.KnowledgeCurrent:
		return queryKnowledgeJSON{Kind: "current"}, nil
	case temporal.KnowledgeAsOf:
		at, ok := value.AsOf()
		if !ok {
			return queryKnowledgeJSON{}, fmt.Errorf("knowledge scope is invalid")
		}
		return queryKnowledgeJSON{Kind: "as-of", At: queryTime(at)}, nil
	default:
		return queryKnowledgeJSON{}, fmt.Errorf("knowledge scope kind is invalid")
	}
}

func queryTrendToJSON(value query.TrendResult) (queryTrendJSON, error) {
	before, err := queryWindowToJSON(value.Before)
	if err != nil {
		return queryTrendJSON{}, err
	}
	after, err := queryWindowToJSON(value.After)
	if err != nil {
		return queryTrendJSON{}, err
	}
	changes := make([]queryChangeJSON, len(value.Changes))
	for index, change := range value.Changes {
		converted, err := queryChangeToJSON(change)
		if err != nil {
			return queryTrendJSON{}, err
		}
		changes[index] = converted
	}
	unresolvedKeys := make([]queryStateKeyJSON, len(value.UnresolvedKeys))
	for index, key := range value.UnresolvedKeys {
		converted, err := queryStateKeyToJSON(key)
		if err != nil {
			return queryTrendJSON{}, err
		}
		unresolvedKeys[index] = converted
	}
	return queryTrendJSON{Before: before, After: after, Changes: changes, UnresolvedKeys: unresolvedKeys}, nil
}

func queryWindowToJSON(value query.WindowResult) (queryWindowJSON, error) {
	selection, err := querySelectionToJSON(value.Selection)
	if err != nil {
		return queryWindowJSON{}, err
	}
	facts, err := queryFactsToJSON(value.Facts)
	if err != nil {
		return queryWindowJSON{}, err
	}
	unresolved, err := queryUnresolvedToJSON(value.Unresolved)
	if err != nil {
		return queryWindowJSON{}, err
	}
	return queryWindowJSON{Selection: selection, Facts: facts, Unresolved: unresolved}, nil
}

func queryFactsToJSON(values []query.Fact) ([]queryFactJSON, error) {
	result := make([]queryFactJSON, len(values))
	for index, value := range values {
		converted, err := queryFactToJSON(value)
		if err != nil {
			return nil, err
		}
		result[index] = converted
	}
	return result, nil
}

func queryFactToJSON(value query.Fact) (queryFactJSON, error) {
	key, err := queryStateKeyToJSON(value.Key)
	if err != nil {
		return queryFactJSON{}, err
	}
	term, err := queryTermJSON(value.Value)
	if err != nil {
		return queryFactJSON{}, err
	}
	contributions := make([]queryContributionJSON, len(value.Contributions))
	for index, contribution := range value.Contributions {
		converted, err := queryContributionToJSON(contribution)
		if err != nil {
			return queryFactJSON{}, err
		}
		contributions[index] = converted
	}
	return queryFactJSON{
		Key: key, Value: term, Contributions: contributions,
		SupportingCitations:    queryCitationsToJSON(value.SupportingCitations),
		ContradictingCitations: queryCitationsToJSON(value.ContradictingCitations),
	}, nil
}

func queryStateKeyToJSON(value temporal.StateKey) (queryStateKeyJSON, error) {
	subject, err := queryTermJSON(value.Subject)
	if err != nil {
		return queryStateKeyJSON{}, err
	}
	return queryStateKeyJSON{Subject: subject, Predicate: string(value.Predicate)}, nil
}

func queryTermJSON(value observation.Term) (queryTermJSONDTO, error) {
	switch value.Kind() {
	case observation.TermAbsent:
		return queryTermJSONDTO{Kind: "absent"}, nil
	case observation.TermText:
		text, ok := value.Text()
		if !ok {
			return queryTermJSONDTO{}, fmt.Errorf("text term is invalid")
		}
		return queryTermJSONDTO{Kind: "text", Text: text}, nil
	case observation.TermMention:
		mentionID, ok := value.MentionID()
		if !ok {
			return queryTermJSONDTO{}, fmt.Errorf("mention term is invalid")
		}
		return queryTermJSONDTO{Kind: "mention", MentionID: mentionID}, nil
	case observation.TermEntity:
		entityID, _, ok := value.Entity()
		if !ok {
			return queryTermJSONDTO{}, fmt.Errorf("entity term is invalid")
		}
		return queryTermJSONDTO{Kind: "entity", EntityID: entityID}, nil
	default:
		return queryTermJSONDTO{}, fmt.Errorf("term kind is invalid")
	}
}

func queryContributionToJSON(value query.Contribution) (queryContributionJSON, error) {
	validTime, err := queryExtentToJSON(value.ValidTime)
	if err != nil {
		return queryContributionJSON{}, err
	}
	return queryContributionJSON{
		ObservationID: string(value.ObservationID), Status: string(value.Status),
		ValidTime: validTime, RecordedAt: queryTime(value.RecordedAt),
		Derivation: queryDerivationJSON{
			Method: value.Derivation.Method, Version: value.Derivation.Version,
			RunID: value.Derivation.RunID, Model: value.Derivation.Model, PromptVersion: value.Derivation.PromptVersion,
		},
		SubjectGroundingMentionID: value.SubjectGroundingMentionID,
		ObjectGroundingMentionID:  value.ObjectGroundingMentionID,
	}, nil
}

func queryExtentToJSON(value observation.TemporalExtent) (queryExtentJSON, error) {
	switch value.Kind() {
	case observation.TemporalUnknown:
		return queryExtentJSON{Kind: "unknown"}, nil
	case observation.TemporalInstant:
		at, ok := value.Instant()
		if !ok {
			return queryExtentJSON{}, fmt.Errorf("valid-time instant is invalid")
		}
		return queryExtentJSON{Kind: "instant", At: queryTime(at)}, nil
	case observation.TemporalInterval:
		start, hasStart, end, hasEnd := value.Bounds()
		result := queryExtentJSON{Kind: "interval"}
		if hasStart {
			result.Start = queryTime(start)
		}
		if hasEnd {
			result.End = queryTime(end)
		}
		return result, nil
	case observation.TemporalWindow:
		start, hasStart, end, hasEnd := value.Bounds()
		if !hasStart || !hasEnd {
			return queryExtentJSON{}, fmt.Errorf("valid-time window is invalid")
		}
		return queryExtentJSON{Kind: "window", Start: queryTime(start), End: queryTime(end)}, nil
	default:
		return queryExtentJSON{}, fmt.Errorf("valid-time kind is invalid")
	}
}

func queryCitationsToJSON(values []query.Citation) []queryCitationJSON {
	result := make([]queryCitationJSON, len(values))
	for index, value := range values {
		result[index] = queryCitationJSON{
			EvidenceID: string(value.EvidenceID), Role: string(value.Role),
			SourceDocumentID: value.SourceDocumentID, DocumentVersionID: value.DocumentVersionID,
			SectionID: value.SectionID, SectionTitle: value.SectionTitle,
			SectionPath: append([]string{}, value.SectionPath...), SectionOrder: value.SectionOrder,
			SectionRole: value.SectionRole, StartOffset: value.StartOffset, EndOffset: value.EndOffset,
			Locator: value.Locator, Text: value.Text,
		}
	}
	return result
}

func queryUnresolvedToJSON(values []query.UnresolvedItem) ([]queryUnresolvedJSON, error) {
	result := make([]queryUnresolvedJSON, len(values))
	for index, value := range values {
		key, err := queryStateKeyToJSON(value.Key)
		if err != nil {
			return nil, err
		}
		candidates, err := queryFactsToJSON(value.Candidates)
		if err != nil {
			return nil, err
		}
		result[index] = queryUnresolvedJSON{Key: key, Reason: string(value.Reason), Candidates: candidates}
	}
	return result, nil
}

func queryChangeToJSON(value query.Change) (queryChangeJSON, error) {
	key, err := queryStateKeyToJSON(value.Key)
	if err != nil {
		return queryChangeJSON{}, err
	}
	result := queryChangeJSON{Kind: string(value.Kind), Key: key}
	if value.Before != nil {
		before, err := queryFactToJSON(*value.Before)
		if err != nil {
			return queryChangeJSON{}, err
		}
		result.Before = &before
	}
	if value.After != nil {
		after, err := queryFactToJSON(*value.After)
		if err != nil {
			return queryChangeJSON{}, err
		}
		result.After = &after
	}
	return result, nil
}

func queryTrajectoryToJSON(value query.TrajectoryResult) (queryTrajectoryJSON, error) {
	selection, err := querySelectionToJSON(value.Selection)
	if err != nil {
		return queryTrajectoryJSON{}, err
	}
	transitions := make([]queryTransitionJSON, len(value.Transitions))
	for index, transition := range value.Transitions {
		converted, err := queryTransitionToJSON(transition)
		if err != nil {
			return queryTrajectoryJSON{}, err
		}
		transitions[index] = converted
	}
	unresolved, err := queryUnresolvedToJSON(value.Unresolved)
	if err != nil {
		return queryTrajectoryJSON{}, err
	}
	return queryTrajectoryJSON{
		Selection: selection, Transitions: transitions, Unresolved: unresolved,
	}, nil
}

func queryTransitionToJSON(value query.Transition) (queryTransitionJSON, error) {
	key, err := queryStateKeyToJSON(value.Key)
	if err != nil {
		return queryTransitionJSON{}, err
	}
	validTime, err := queryExtentToJSON(value.ValidTime)
	if err != nil {
		return queryTransitionJSON{}, err
	}
	unresolved, err := queryUnresolvedToJSON(value.Unresolved)
	if err != nil {
		return queryTransitionJSON{}, err
	}
	result := queryTransitionJSON{
		Kind: string(value.Kind), Key: key, ValidTime: validTime, Unresolved: unresolved,
	}
	if value.Before != nil {
		before, err := queryFactToJSON(*value.Before)
		if err != nil {
			return queryTransitionJSON{}, err
		}
		result.Before = &before
	}
	if value.After != nil {
		after, err := queryFactToJSON(*value.After)
		if err != nil {
			return queryTransitionJSON{}, err
		}
		result.After = &after
	}
	return result, nil
}

func queryCausalToJSON(value query.CausalChainResult) (queryCausalJSON, error) {
	selection, err := querySelectionToJSON(value.Selection)
	if err != nil {
		return queryCausalJSON{}, err
	}
	links := make([]queryCausalLinkJSON, len(value.Links))
	for index, link := range value.Links {
		cause, err := queryTermJSON(link.Cause)
		if err != nil {
			return queryCausalJSON{}, err
		}
		effect, err := queryTermJSON(link.Effect)
		if err != nil {
			return queryCausalJSON{}, err
		}
		contributions := make([]queryContributionJSON, len(link.Contributions))
		for contributionIndex, contribution := range link.Contributions {
			converted, err := queryContributionToJSON(contribution)
			if err != nil {
				return queryCausalJSON{}, err
			}
			contributions[contributionIndex] = converted
		}
		links[index] = queryCausalLinkJSON{
			Cause: cause, Effect: effect, Contributions: contributions,
			SupportingCitations:    queryCitationsToJSON(link.SupportingCitations),
			ContradictingCitations: queryCitationsToJSON(link.ContradictingCitations),
		}
	}
	return queryCausalJSON{Selection: selection, Links: links}, nil
}

func queryGapsToJSON(values []query.Gap) []queryGapJSON {
	result := make([]queryGapJSON, len(values))
	for index, value := range values {
		result[index] = queryGapJSON{
			Kind: string(value.Kind), EntityID: string(value.EntityID),
			Predicate: string(value.Predicate), SelectionLabel: value.SelectionLabel,
		}
	}
	return result
}

func queryTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
