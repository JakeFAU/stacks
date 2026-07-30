package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/timepoint"
	"github.com/spf13/cobra"

	"stacks/internal/query"
	"stacks/internal/queryplan"
)

// QueryAskInput is the public CLI context for a private natural-language question.
type QueryAskInput struct {
	EntityIDs     []identity.EntityID
	ReferenceTime time.Time
	Output        QueryOutput
}

// QueryAskService plans and executes one private question.
type QueryAskService interface {
	Ask(context.Context, queryplan.Input) (queryplan.Execution, error)
}

// QueryAskServiceFactory constructs the provider-owning service after local validation.
type QueryAskServiceFactory func(context.Context) (QueryAskService, error)

// QueryAskCommand owns private stdin handling and audited output rendering.
type QueryAskCommand struct {
	NewService       QueryAskServiceFactory
	Input            io.Reader
	Output           io.Writer
	Limits           query.Limits
	MaxQuestionBytes int
}

func parseQueryAsk(command *cobra.Command) (QueryAskInput, error) {
	values, err := command.Flags().GetStringArray(queryEntityFlagName)
	if err != nil {
		return QueryAskInput{}, errors.New("read query ask entity flags failed")
	}
	entityIDs, err := parseUniqueEntityIDs(values)
	if err != nil {
		return QueryAskInput{}, errors.New("query ask entity IDs are invalid")
	}
	referenceValue, err := flagString(command, queryReferenceTimeFlagName)
	if err != nil {
		return QueryAskInput{}, errors.New("query ask reference time is invalid")
	}
	referenceTime, err := time.Parse(time.RFC3339, referenceValue)
	if err != nil {
		return QueryAskInput{}, errors.New("query ask reference time must be RFC3339")
	}
	outputValue, err := flagString(command, queryOutputFlagName)
	if err != nil {
		return QueryAskInput{}, errors.New("query ask output is invalid")
	}
	output := QueryOutput(outputValue)
	if output != QueryOutputText && output != QueryOutputJSON {
		return QueryAskInput{}, errors.New("query ask output is invalid")
	}
	return QueryAskInput{EntityIDs: entityIDs, ReferenceTime: timepoint.Normalize(referenceTime), Output: output}, nil
}

// ValidateQueryAskInvocation validates the transport-owned query ask shape.
func ValidateQueryAskInvocation(invocation Invocation) error {
	if invocation.Command != CommandQuery || invocation.Action != ActionAsk || invocation.QueryAsk == nil ||
		invocation.Query != nil || len(invocation.Arguments) != 0 {
		return errors.New("query ask invocation is invalid")
	}
	if invocation.QueryAsk.Output != QueryOutputText && invocation.QueryAsk.Output != QueryOutputJSON ||
		invocation.QueryAsk.ReferenceTime.IsZero() {
		return errors.New("query ask invocation is invalid")
	}
	if _, err := parseUniqueEntityIDs(entityIDsAsStrings(invocation.QueryAsk.EntityIDs)); err != nil {
		return errors.New("query ask invocation is invalid")
	}
	return nil
}

// Run validates private input before provider construction, then writes one complete audit envelope.
func (command QueryAskCommand) Run(ctx context.Context, invocation Invocation) error {
	if err := ValidateQueryAskInvocation(invocation); err != nil {
		return err
	}
	question, err := readBoundedQuestion(command.Input, command.MaxQuestionBytes)
	if err != nil {
		return err
	}
	input, err := queryplan.NormalizeInput(queryplan.Input{
		Question: question, EntityIDs: invocation.QueryAsk.EntityIDs, ReferenceTime: invocation.QueryAsk.ReferenceTime,
	}, command.Limits, command.MaxQuestionBytes)
	if err != nil {
		return errors.New("query ask input is invalid")
	}
	if command.NewService == nil {
		return errors.New("query ask service is not configured")
	}
	service, err := command.NewService(ctx)
	if err != nil {
		return errors.New("query ask service construction failed")
	}
	if service == nil {
		return errors.New("query ask service is not configured")
	}
	execution, err := service.Ask(ctx, input)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return errors.New("query ask execution failed")
	}
	var rendered []byte
	switch invocation.QueryAsk.Output {
	case QueryOutputText:
		rendered, err = renderQueryAskText(execution)
	case QueryOutputJSON:
		rendered, err = renderQueryAskJSON(execution)
	default:
		return errors.New("query ask output is invalid")
	}
	if err != nil {
		return errors.New("render query ask output failed")
	}
	output := command.Output
	if output == nil {
		output = io.Discard
	}
	written, err := output.Write(rendered)
	if err == nil && written != len(rendered) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("write query ask result: %w", err)
	}
	return nil
}

func readBoundedQuestion(input io.Reader, maximum int) (string, error) {
	if input == nil || maximum <= 0 {
		return "", errors.New("query ask input is not configured")
	}
	payload, err := io.ReadAll(io.LimitReader(input, int64(maximum)+1))
	if err != nil {
		return "", errors.New("read query ask question failed")
	}
	if len(payload) > maximum {
		return "", errors.New("query ask question exceeds the configured maximum")
	}
	if !utf8.Valid(payload) || strings.TrimSpace(string(payload)) == "" {
		return "", errors.New("query ask question is invalid")
	}
	return string(payload), nil
}

func validateQueryAskExecution(execution queryplan.Execution) error {
	if execution.SchemaVersion != queryplan.OutputSchemaVersion || !canonicalQueryAskTime(execution.ReferenceTime) ||
		!queryAskPlannerValid(execution.Planner) || query.ValidateResult(execution.Result) != nil ||
		!reflect.DeepEqual(execution.Request, queryRequestFromResult(execution.Result)) {
		return errors.New("query ask execution is invalid")
	}
	return nil
}

func canonicalQueryAskTime(value time.Time) bool {
	if value.IsZero() || !value.Equal(timepoint.Normalize(value)) || value.Location() != time.UTC {
		return false
	}
	serialized := value.Format(time.RFC3339Nano)
	parsed, err := time.Parse(time.RFC3339Nano, serialized)
	return err == nil && parsed.Equal(value)
}

func queryAskPlannerValid(value queryplan.PlannerMetadata) bool {
	return value.Provider.Valid() && strings.TrimSpace(value.ModelID) != "" &&
		value.PromptVersion == queryplan.PromptVersion && value.SchemaName == queryplan.SchemaName &&
		value.Attempts > 0 && value.Usage.InputTokens >= 0 && value.Usage.OutputTokens >= 0 &&
		value.Usage.TotalTokens >= value.Usage.InputTokens &&
		value.Usage.TotalTokens-value.Usage.InputTokens >= value.Usage.OutputTokens &&
		value.WallLatency >= 0 && value.ProviderLatency >= 0
}
