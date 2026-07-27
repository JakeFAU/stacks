package cli

import (
	"fmt"
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
	entityValues, err := command.Flags().GetStringArray(queryEntityFlagName)
	if err != nil {
		return QueryInput{}, fmt.Errorf("read entity flags: %w", err)
	}
	entityIDs, err := parseUniqueEntityIDs(entityValues)
	if err != nil {
		return QueryInput{}, err
	}
	matchValue, err := flagString(command, queryEntityMatchFlagName)
	if err != nil {
		return QueryInput{}, err
	}
	match := query.EntityMatch(matchValue)
	if match != query.EntityMatchAll && match != query.EntityMatchAny {
		return QueryInput{}, fmt.Errorf("query entity match is invalid")
	}
	predicateValues, err := command.Flags().GetStringArray(queryPredicateFlagName)
	if err != nil {
		return QueryInput{}, fmt.Errorf("read predicate flags: %w", err)
	}
	predicates, err := parseUniquePredicates(predicateValues)
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
	knowledgeScope := temporal.CurrentKnowledge()
	if command.Flags().Changed(queryKnownAsOfFlagName) {
		value, err := flagString(command, queryKnownAsOfFlagName)
		if err != nil {
			return QueryInput{}, err
		}
		cutoff, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return QueryInput{}, fmt.Errorf("query knowledge cutoff must be RFC3339")
		}
		knowledgeScope, err = temporal.KnownAsOf(cutoff)
		if err != nil {
			return QueryInput{}, fmt.Errorf("query knowledge cutoff is invalid: %w", err)
		}
	}
	outputValue, err := flagString(command, queryOutputFlagName)
	if err != nil {
		return QueryInput{}, err
	}
	output := QueryOutput(outputValue)
	if output != QueryOutputText && output != QueryOutputJSON {
		return QueryInput{}, fmt.Errorf("query output is invalid")
	}
	request := query.Request{
		Intent:         temporal.IntentTrendComparison,
		EntityIDs:      entityIDs,
		EntityMatch:    match,
		Predicates:     predicates,
		Selections:     []temporal.TemporalSelection{before, after},
		KnowledgeScope: knowledgeScope,
	}
	if _, err := temporal.NewPlan(temporal.PlanInput{
		Intent:         request.Intent,
		EntityIDs:      entityIDsAsStrings(request.EntityIDs),
		Selections:     request.Selections,
		KnowledgeScope: request.KnowledgeScope,
	}); err != nil {
		return QueryInput{}, fmt.Errorf("query trend is invalid: %w", err)
	}
	return QueryInput{Request: request, Output: output}, nil
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
