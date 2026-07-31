package queryplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/observability"
	"stacks/internal/query"
)

const (
	minimumPlannerTimeout = time.Second
	maximumPlannerTimeout = 5 * time.Minute
	plannerSpanName       = "stacks.query.plan"
)

var errQueryPlannerTelemetry = errors.New("temporal query planning failed")

// QuestionRecorder records only a private question's byte count.
type QuestionRecorder interface {
	RecordQuestionBytes(context.Context, int64)
}

// Service validates a private natural-language question, obtains one
// untrusted proposal, and executes only its normalized deterministic request.
type Service struct {
	Model            Model
	Executor         Executor
	Limits           query.Limits
	PlannerTimeout   time.Duration
	MaxQuestionBytes int
	QuestionRecorder QuestionRecorder
	Tracer           trace.Tracer
}

// Ask plans one private question and executes its exact normalized request.
func (service Service) Ask(ctx context.Context, input Input) (execution Execution, resultErr error) {
	if ctx == nil {
		return Execution{}, errors.New("query planner context is required")
	}
	if service.Model == nil {
		return Execution{}, errors.New("query planner model is required")
	}
	if service.Executor == nil {
		return Execution{}, errors.New("query planner executor is required")
	}
	if err := query.ValidateLimits(service.Limits); err != nil {
		return Execution{}, errors.New("query planner limits are invalid")
	}
	if service.PlannerTimeout < minimumPlannerTimeout || service.PlannerTimeout > maximumPlannerTimeout {
		return Execution{}, errors.New("query planner timeout is invalid")
	}
	if service.MaxQuestionBytes < minimumQuestionByteLimit || service.MaxQuestionBytes > maximumQuestionByteLimit {
		return Execution{}, errors.New("query planner question limit is invalid")
	}
	normalizedInput, err := NormalizeInput(input, service.Limits, service.MaxQuestionBytes)
	if err != nil {
		return Execution{}, err
	}

	tracer := service.Tracer
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("stacks")
	}
	spanContext, span := tracer.Start(ctx, plannerSpanName)
	defer func() {
		if resultErr != nil {
			observability.FinishSpan(span, errQueryPlannerTelemetry)
			return
		}
		observability.FinishSpan(span, nil)
	}()
	if service.QuestionRecorder != nil {
		service.QuestionRecorder.RecordQuestionBytes(spanContext, int64(len(normalizedInput.Question)))
	}
	modelRequest, err := modelRequestFor(normalizedInput, service.Limits)
	if err != nil {
		return Execution{}, errors.New("query planner request is invalid")
	}
	planningContext, cancel := context.WithTimeout(ctx, service.PlannerTimeout)
	response, err := service.Model.Plan(planningContext, modelRequest)
	cancel()
	if canonical := canonicalContextError(ctx, err); canonical != nil {
		return Execution{}, canonical
	}
	if err != nil {
		return Execution{}, fmt.Errorf("plan temporal query: %w", err)
	}
	if !validModelResponse(response) {
		return Execution{}, errors.New("query planner response is invalid")
	}
	authoritativeRequest, err := composeRequest(response.Output, normalizedInput.EntityIDs, service.Limits)
	if err != nil {
		return Execution{}, err
	}
	executorRequest, err := query.NormalizeRequest(authoritativeRequest, service.Limits)
	if err != nil {
		return Execution{}, errors.New("query planner request is invalid")
	}
	result, err := service.Executor.Query(ctx, executorRequest)
	if canonical := canonicalContextError(ctx, err); canonical != nil {
		return Execution{}, canonical
	}
	if err != nil {
		return Execution{}, fmt.Errorf("execute planned temporal query: %w", err)
	}
	normalizedResult, err := query.NormalizeResult(result)
	if err != nil {
		return Execution{}, errors.New("query planner result is invalid")
	}
	if !resultMatchesRequest(normalizedResult, authoritativeRequest) {
		return Execution{}, errors.New("query planner result does not match the normalized request")
	}

	return Execution{
		SchemaVersion: OutputSchemaVersion,
		ReferenceTime: normalizedInput.ReferenceTime,
		Request:       authoritativeRequest,
		Planner: PlannerMetadata{
			Provider: response.Provider, ModelID: response.ModelID,
			PromptVersion: response.PromptVersion, SchemaName: response.SchemaName,
			Usage: response.Usage, Attempts: response.Attempts,
			WallLatency: response.WallLatency, ProviderLatency: response.ProviderLatency,
		},
		Result: normalizedResult,
	}, nil
}

func validModelResponse(response ModelResponse) bool {
	return response.Provider.Valid() && strings.TrimSpace(response.ModelID) != "" &&
		response.PromptVersion == PromptVersion && response.SchemaName == SchemaName &&
		response.Attempts > 0 && response.Usage.valid() && response.WallLatency >= 0 &&
		response.ProviderLatency >= 0 && json.Valid(response.Output)
}

func resultMatchesRequest(result query.Result, request query.Request) bool {
	return result.Intent == request.Intent && reflect.DeepEqual(result.EntityIDs, request.EntityIDs) &&
		result.EntityMatch == request.EntityMatch && reflect.DeepEqual(result.Predicates, request.Predicates) &&
		reflect.DeepEqual(result.Selections, request.Selections) &&
		reflect.DeepEqual(result.KnowledgeScope, request.KnowledgeScope) && result.Limit == request.Limit
}

func canonicalContextError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}
