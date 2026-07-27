package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
	"github.com/spf13/cobra"

	"stacks/internal/query"
)

// QueryOutput identifies the requested query rendering format.
type QueryOutput string

const (
	QueryOutputText QueryOutput = "text"
	QueryOutputJSON QueryOutput = "json"
)

// QueryInput is the fully parsed temporal query invocation.
type QueryInput struct {
	Request query.Request
	Output  QueryOutput
}

func parseTrendQuery(command *cobra.Command) (QueryInput, error) {
	common, err := parseQueryCommon(command, true)
	if err != nil {
		return QueryInput{}, err
	}
	before, err := parseQueryWindow(command, queryBeforeFlagName)
	if err != nil {
		return QueryInput{}, err
	}
	after, err := parseQueryWindow(command, queryAfterFlagName)
	if err != nil {
		return QueryInput{}, err
	}
	request := query.Request{
		Intent: temporal.IntentTrendComparison, EntityIDs: common.entityIDs,
		EntityMatch: common.match, Predicates: common.predicates,
		Selections:     []temporal.TemporalSelection{before, after},
		KnowledgeScope: common.knowledgeScope,
	}
	if err := validateParsedQuery(request, "trend"); err != nil {
		return QueryInput{}, err
	}
	return QueryInput{Request: request, Output: common.output}, nil
}

func parsePointQuery(command *cobra.Command) (QueryInput, error) {
	common, err := parseQueryCommon(command, true)
	if err != nil {
		return QueryInput{}, err
	}
	value, err := flagString(command, queryAtFlagName)
	if err != nil {
		return QueryInput{}, err
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return QueryInput{}, fmt.Errorf("query point instant must be RFC3339")
	}
	selection, err := temporal.At("point", at)
	if err != nil {
		return QueryInput{}, fmt.Errorf("query point instant is invalid: %w", err)
	}
	request := query.Request{
		Intent: temporal.IntentPointInTime, EntityIDs: common.entityIDs,
		EntityMatch: common.match, Predicates: common.predicates,
		Selections:     []temporal.TemporalSelection{selection},
		KnowledgeScope: common.knowledgeScope,
	}
	if err := validateParsedQuery(request, "point"); err != nil {
		return QueryInput{}, err
	}
	return QueryInput{Request: request, Output: common.output}, nil
}

func parseTrajectoryQuery(command *cobra.Command) (QueryInput, error) {
	return parseChronologyQuery(command, temporal.IntentTrajectory, false)
}

func parseCausalQuery(command *cobra.Command) (QueryInput, error) {
	return parseChronologyQuery(command, temporal.IntentCausalChain, true)
}

func parseChronologyQuery(
	command *cobra.Command,
	intent temporal.Intent,
	causal bool,
) (QueryInput, error) {
	common, err := parseQueryCommon(command, !causal)
	if err != nil {
		return QueryInput{}, err
	}
	selection, err := parseQueryWindow(command, queryBetweenFlagName)
	if err != nil {
		return QueryInput{}, err
	}
	value, err := flagString(command, queryLimitFlagName)
	if err != nil {
		return QueryInput{}, err
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return QueryInput{}, fmt.Errorf("query chronology limit must be a positive integer")
	}
	if causal {
		common.predicates = []observation.Predicate{query.CausalPredicate}
	}
	request := query.Request{
		Intent: intent, EntityIDs: common.entityIDs, EntityMatch: common.match,
		Predicates: common.predicates, Selections: []temporal.TemporalSelection{selection},
		KnowledgeScope: common.knowledgeScope, Limit: limit,
	}
	if err := validateParsedQuery(request, string(intent)); err != nil {
		return QueryInput{}, err
	}
	return QueryInput{Request: request, Output: common.output}, nil
}

type parsedQueryCommon struct {
	entityIDs      []identity.EntityID
	match          query.EntityMatch
	predicates     []observation.Predicate
	knowledgeScope temporal.KnowledgeScope
	output         QueryOutput
}

func parseQueryCommon(command *cobra.Command, predicatesAllowed bool) (parsedQueryCommon, error) {
	entityValues, err := command.Flags().GetStringArray(queryEntityFlagName)
	if err != nil {
		return parsedQueryCommon{}, fmt.Errorf("read entity flags: %w", err)
	}
	entityIDs, err := parseUniqueEntityIDs(entityValues)
	if err != nil {
		return parsedQueryCommon{}, err
	}
	matchValue, err := flagString(command, queryEntityMatchFlagName)
	if err != nil {
		return parsedQueryCommon{}, err
	}
	match := query.EntityMatch(matchValue)
	if match != query.EntityMatchAll && match != query.EntityMatchAny {
		return parsedQueryCommon{}, fmt.Errorf("query entity match is invalid")
	}
	predicates := []observation.Predicate{}
	if predicatesAllowed {
		predicateValues, err := command.Flags().GetStringArray(queryPredicateFlagName)
		if err != nil {
			return parsedQueryCommon{}, fmt.Errorf("read predicate flags: %w", err)
		}
		predicates, err = parseUniquePredicates(predicateValues)
		if err != nil {
			return parsedQueryCommon{}, err
		}
	}
	knowledgeScope := temporal.CurrentKnowledge()
	if command.Flags().Changed(queryKnownAsOfFlagName) {
		value, err := flagString(command, queryKnownAsOfFlagName)
		if err != nil {
			return parsedQueryCommon{}, err
		}
		cutoff, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return parsedQueryCommon{}, fmt.Errorf("query knowledge cutoff must be RFC3339")
		}
		knowledgeScope, err = temporal.KnownAsOf(cutoff)
		if err != nil {
			return parsedQueryCommon{}, fmt.Errorf("query knowledge cutoff is invalid: %w", err)
		}
	}
	outputValue, err := flagString(command, queryOutputFlagName)
	if err != nil {
		return parsedQueryCommon{}, err
	}
	output := QueryOutput(outputValue)
	if output != QueryOutputText && output != QueryOutputJSON {
		return parsedQueryCommon{}, fmt.Errorf("query output is invalid")
	}
	return parsedQueryCommon{
		entityIDs: entityIDs, match: match, predicates: predicates,
		knowledgeScope: knowledgeScope, output: output,
	}, nil
}

func validateParsedQuery(request query.Request, action string) error {
	if _, err := temporal.NewPlan(temporal.PlanInput{
		Intent:         request.Intent,
		EntityIDs:      entityIDsAsStrings(request.EntityIDs),
		Selections:     request.Selections,
		KnowledgeScope: request.KnowledgeScope,
	}); err != nil {
		return fmt.Errorf("query %s is invalid: %w", action, err)
	}
	return nil
}

func parseUniqueEntityIDs(values []string) ([]identity.EntityID, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("query entity ID is required")
	}
	result := make([]identity.EntityID, len(values))
	seen := make(map[identity.EntityID]struct{}, len(values))
	for index, value := range values {
		entityID := identity.EntityID(strings.TrimSpace(value))
		if entityID == "" {
			return nil, fmt.Errorf("query entity ID is required")
		}
		if _, exists := seen[entityID]; exists {
			return nil, fmt.Errorf("query entity IDs must be unique")
		}
		seen[entityID] = struct{}{}
		result[index] = entityID
	}
	return result, nil
}

func parseUniquePredicates(values []string) ([]observation.Predicate, error) {
	result := make([]observation.Predicate, len(values))
	seen := make(map[observation.Predicate]struct{}, len(values))
	for index, value := range values {
		predicate, err := observation.NewPredicate(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("query predicate is invalid: %w", err)
		}
		if _, exists := seen[predicate]; exists {
			return nil, fmt.Errorf("query predicates must be unique")
		}
		seen[predicate] = struct{}{}
		result[index] = predicate
	}
	return result, nil
}

func parseQueryWindow(command *cobra.Command, flagName string) (temporal.TemporalSelection, error) {
	value, err := flagString(command, flagName)
	if err != nil {
		return temporal.TemporalSelection{}, err
	}
	if strings.Count(value, "/") != 1 {
		return temporal.TemporalSelection{}, fmt.Errorf("query %s window must contain exactly one slash", flagName)
	}
	bounds := strings.SplitN(value, "/", 2)
	start, err := time.Parse(time.RFC3339, bounds[0])
	if err != nil {
		return temporal.TemporalSelection{}, fmt.Errorf("query %s window start must be RFC3339", flagName)
	}
	end, err := time.Parse(time.RFC3339, bounds[1])
	if err != nil {
		return temporal.TemporalSelection{}, fmt.Errorf("query %s window end must be RFC3339", flagName)
	}
	selection, err := temporal.Between(flagName, start, end)
	if err != nil {
		return temporal.TemporalSelection{}, fmt.Errorf("query %s window is invalid: %w", flagName, err)
	}
	return selection, nil
}

func flagString(command *cobra.Command, name string) (string, error) {
	flag := command.Flags().Lookup(name)
	if flag == nil {
		return "", fmt.Errorf("query %s flag is not configured", name)
	}
	return flag.Value.String(), nil
}

func entityIDsAsStrings(values []identity.EntityID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}
