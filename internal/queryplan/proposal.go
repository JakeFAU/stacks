package queryplan

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
	"stacks/internal/query"
)

// CannotPlanReason is the bounded reason a planner declined to propose a
// representable closed temporal request.
type CannotPlanReason string

const (
	CannotPlanAmbiguous                  CannotPlanReason = "ambiguous-question"
	CannotPlanUnsupported                CannotPlanReason = "unsupported-question"
	CannotPlanInsufficientTemporalDetail CannotPlanReason = "insufficient-temporal-detail"
)

// CannotPlanError reports the planner's approved non-executable outcome.
type CannotPlanError struct {
	Reason CannotPlanReason
}

func (err CannotPlanError) Error() string {
	switch err.Reason {
	case CannotPlanAmbiguous, CannotPlanUnsupported, CannotPlanInsufficientTemporalDetail:
		return "query planner cannot plan: " + string(err.Reason)
	default:
		return "query planner cannot plan"
	}
}

type proposalWire struct {
	Status          *string          `json:"status"`
	Reason          *string          `json:"reason"`
	Intent          *string          `json:"intent"`
	EntityMatch     *string          `json:"entity_match"`
	Predicates      *[]string        `json:"predicates"`
	Selections      *[]selectionWire `json:"selections"`
	KnowledgeScope  *knowledgeWire   `json:"knowledge_scope"`
	ChronologyLimit *int             `json:"chronology_limit"`
}

type selectionWire struct {
	Kind  *string `json:"kind"`
	Label *string `json:"label"`
	At    *string `json:"at"`
	Start *string `json:"start"`
	End   *string `json:"end"`
}

type knowledgeWire struct {
	Kind *string `json:"kind"`
	AsOf *string `json:"as_of"`
}

type proposal struct {
	Status          string
	Reason          string
	Intent          string
	EntityMatch     string
	Predicates      []string
	Selections      []selection
	KnowledgeScope  knowledge
	ChronologyLimit int
}

type selection struct {
	Kind  string
	Label string
	At    string
	Start string
	End   string
}

type knowledge struct {
	Kind string
	AsOf string
}

func composeRequest(output json.RawMessage, entityIDs []identity.EntityID, limits query.Limits) (query.Request, error) {
	value, err := decodeProposal(output)
	if err != nil {
		return query.Request{}, invalidProposalError()
	}
	if value.Status == "cannot-plan" {
		if !validCannotPlan(value) {
			return query.Request{}, invalidProposalError()
		}
		return query.Request{}, CannotPlanError{Reason: CannotPlanReason(value.Reason)}
	}
	if value.Status != "executable" || value.Reason != "none" {
		return query.Request{}, invalidProposalError()
	}
	request, err := executableRequest(value, entityIDs)
	if err != nil {
		return query.Request{}, invalidProposalError()
	}
	normalized, err := query.NormalizeRequest(request, limits)
	if err != nil {
		return query.Request{}, invalidProposalError()
	}
	return normalized, nil
}

func decodeProposal(output json.RawMessage) (proposal, error) {
	if err := validateProposalJSON(output); err != nil {
		return proposal{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	var wire proposalWire
	if err := decoder.Decode(&wire); err != nil {
		return proposal{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return proposal{}, errors.New("multiple JSON values")
	}
	if wire.Status == nil || wire.Reason == nil || wire.Intent == nil || wire.EntityMatch == nil ||
		wire.Predicates == nil || wire.Selections == nil || wire.KnowledgeScope == nil || wire.ChronologyLimit == nil ||
		wire.KnowledgeScope.Kind == nil || wire.KnowledgeScope.AsOf == nil {
		return proposal{}, errors.New("proposal field is missing")
	}
	selections := make([]selection, len(*wire.Selections))
	for index, item := range *wire.Selections {
		if item.Kind == nil || item.Label == nil || item.At == nil || item.Start == nil || item.End == nil {
			return proposal{}, errors.New("selection field is missing")
		}
		selections[index] = selection{Kind: *item.Kind, Label: *item.Label, At: *item.At, Start: *item.Start, End: *item.End}
	}
	return proposal{
		Status:          *wire.Status,
		Reason:          *wire.Reason,
		Intent:          *wire.Intent,
		EntityMatch:     *wire.EntityMatch,
		Predicates:      append([]string(nil), (*wire.Predicates)...),
		Selections:      selections,
		KnowledgeScope:  knowledge{Kind: *wire.KnowledgeScope.Kind, AsOf: *wire.KnowledgeScope.AsOf},
		ChronologyLimit: *wire.ChronologyLimit,
	}, nil
}

type proposalJSONContext uint8

const (
	proposalJSONRoot proposalJSONContext = iota + 1
	proposalJSONSelection
	proposalJSONKnowledge
	proposalJSONSelections
	proposalJSONPredicates
	proposalJSONScalar
)

func validateProposalJSON(input []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	if err := consumeProposalJSONValue(decoder, proposalJSONRoot); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func consumeProposalJSONValue(decoder *json.Decoder, context proposalJSONContext) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		if context != proposalJSONRoot && context != proposalJSONSelection && context != proposalJSONKnowledge {
			return errors.New("proposal object is invalid")
		}
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is invalid")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate object key")
			}
			seen[key] = struct{}{}
			childContext, ok := proposalJSONFieldContext(context, key)
			if !ok {
				return errors.New("proposal object key is invalid")
			}
			if err := consumeProposalJSONValue(decoder, childContext); err != nil {
				return err
			}
		}
		return consumeDelimiter(decoder, '}')
	case '[':
		var childContext proposalJSONContext
		switch context {
		case proposalJSONSelections:
			childContext = proposalJSONSelection
		case proposalJSONPredicates:
			childContext = proposalJSONScalar
		default:
			return errors.New("proposal array is invalid")
		}
		for decoder.More() {
			if err := consumeProposalJSONValue(decoder, childContext); err != nil {
				return err
			}
		}
		return consumeDelimiter(decoder, ']')
	default:
		return errors.New("JSON delimiter is invalid")
	}
}

func proposalJSONFieldContext(context proposalJSONContext, key string) (proposalJSONContext, bool) {
	switch context {
	case proposalJSONRoot:
		switch key {
		case "status", "reason", "intent", "entity_match", "chronology_limit":
			return proposalJSONScalar, true
		case "predicates":
			return proposalJSONPredicates, true
		case "selections":
			return proposalJSONSelections, true
		case "knowledge_scope":
			return proposalJSONKnowledge, true
		}
	case proposalJSONSelection:
		switch key {
		case "kind", "label", "at", "start", "end":
			return proposalJSONScalar, true
		}
	case proposalJSONKnowledge:
		switch key {
		case "kind", "as_of":
			return proposalJSONScalar, true
		}
	}
	return 0, false
}

func consumeDelimiter(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != want {
		return errors.New("JSON delimiter is invalid")
	}
	return nil
}

func validCannotPlan(value proposal) bool {
	if !validCannotPlanReason(value.Reason) || value.Intent != "" || value.EntityMatch != "" ||
		len(value.Predicates) != 0 || len(value.Selections) != 0 || value.KnowledgeScope.Kind != "" ||
		value.KnowledgeScope.AsOf != "" || value.ChronologyLimit != 0 {
		return false
	}
	return true
}

func validCannotPlanReason(value string) bool {
	switch CannotPlanReason(value) {
	case CannotPlanAmbiguous, CannotPlanUnsupported, CannotPlanInsufficientTemporalDetail:
		return true
	default:
		return false
	}
}

func executableRequest(value proposal, entityIDs []identity.EntityID) (query.Request, error) {
	intent, err := parseIntent(value.Intent)
	if err != nil || (value.EntityMatch != string(query.EntityMatchAll) && value.EntityMatch != string(query.EntityMatchAny)) {
		return query.Request{}, errors.New("executable proposal is invalid")
	}
	selections, err := selectionsFor(intent, value.Selections)
	if err != nil {
		return query.Request{}, err
	}
	knowledgeScope, err := knowledgeScopeFor(value.KnowledgeScope)
	if err != nil {
		return query.Request{}, err
	}
	predicates := make([]observation.Predicate, len(value.Predicates))
	for index, predicateValue := range value.Predicates {
		predicate, err := observation.NewPredicate(predicateValue)
		if err != nil {
			return query.Request{}, err
		}
		predicates[index] = predicate
	}
	if (intent == temporal.IntentPointInTime || intent == temporal.IntentTrendComparison) && value.ChronologyLimit != 0 {
		return query.Request{}, errors.New("chronology limit is invalid")
	}
	if (intent == temporal.IntentTrajectory || intent == temporal.IntentCausalChain) && value.ChronologyLimit <= 0 {
		return query.Request{}, errors.New("chronology limit is invalid")
	}
	if intent == temporal.IntentCausalChain && (len(predicates) != 1 || predicates[0] != query.CausalPredicate) {
		return query.Request{}, errors.New("causal predicate is invalid")
	}
	return query.Request{
		Intent:         intent,
		EntityIDs:      append([]identity.EntityID(nil), entityIDs...),
		EntityMatch:    query.EntityMatch(value.EntityMatch),
		Predicates:     predicates,
		Selections:     selections,
		KnowledgeScope: knowledgeScope,
		Limit:          value.ChronologyLimit,
	}, nil
}

func parseIntent(value string) (temporal.Intent, error) {
	switch temporal.Intent(value) {
	case temporal.IntentPointInTime, temporal.IntentTrendComparison, temporal.IntentTrajectory, temporal.IntentCausalChain:
		return temporal.Intent(value), nil
	default:
		return "", errors.New("intent is invalid")
	}
}

func selectionsFor(intent temporal.Intent, values []selection) ([]temporal.TemporalSelection, error) {
	switch intent {
	case temporal.IntentPointInTime:
		if len(values) != 1 || values[0].Kind != "point" || values[0].Label != "point" || values[0].At == "" || values[0].Start != "" || values[0].End != "" {
			return nil, errors.New("point selection is invalid")
		}
		instant, err := parseRFC3339(values[0].At)
		if err != nil {
			return nil, err
		}
		selection, err := temporal.At(values[0].Label, instant)
		if err != nil {
			return nil, err
		}
		return []temporal.TemporalSelection{selection}, nil
	case temporal.IntentTrendComparison:
		if len(values) != 2 {
			return nil, errors.New("trend selections are invalid")
		}
		before, err := windowSelection(values[0], "before")
		if err != nil {
			return nil, err
		}
		after, err := windowSelection(values[1], "after")
		if err != nil {
			return nil, err
		}
		return []temporal.TemporalSelection{before, after}, nil
	case temporal.IntentTrajectory, temporal.IntentCausalChain:
		if len(values) != 1 {
			return nil, errors.New("window selection is invalid")
		}
		between, err := windowSelection(values[0], "between")
		if err != nil {
			return nil, err
		}
		return []temporal.TemporalSelection{between}, nil
	default:
		return nil, errors.New("intent is invalid")
	}
}

func windowSelection(value selection, label string) (temporal.TemporalSelection, error) {
	if value.Kind != "window" || value.Label != label || value.At != "" || value.Start == "" || value.End == "" {
		return temporal.TemporalSelection{}, errors.New("window selection is invalid")
	}
	start, err := parseRFC3339(value.Start)
	if err != nil {
		return temporal.TemporalSelection{}, err
	}
	end, err := parseRFC3339(value.End)
	if err != nil {
		return temporal.TemporalSelection{}, err
	}
	return temporal.Between(label, start, end)
}

func knowledgeScopeFor(value knowledge) (temporal.KnowledgeScope, error) {
	switch value.Kind {
	case "current":
		if value.AsOf != "" {
			return temporal.KnowledgeScope{}, errors.New("current knowledge scope is invalid")
		}
		return temporal.CurrentKnowledge(), nil
	case "as-of":
		if value.AsOf == "" {
			return temporal.KnowledgeScope{}, errors.New("as-of knowledge scope is invalid")
		}
		asOf, err := parseRFC3339(value.AsOf)
		if err != nil {
			return temporal.KnowledgeScope{}, err
		}
		return temporal.KnownAsOf(asOf)
	default:
		return temporal.KnowledgeScope{}, errors.New("knowledge scope is invalid")
	}
}

func parseRFC3339(value string) (time.Time, error) {
	return time.Parse(time.RFC3339, value)
}

func invalidProposalError() error {
	return errors.New("query planner proposal is invalid")
}
