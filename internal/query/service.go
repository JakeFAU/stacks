package query

import (
	"context"
	"errors"

	"github.com/JakeFAU/stacks/core/temporal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/observability"
)

const (
	querySpanName         = "stacks.query.temporal"
	smallCountBucketLimit = 5
)

var errQueryTelemetry = errors.New("temporal query failed")

// Service executes provider-neutral temporal queries over one coherent reader
// snapshot.
type Service struct {
	Reader Reader
	Limits Limits
	Tracer trace.Tracer
}

// Query validates and normalizes one request, reads exactly one snapshot, and
// executes deterministic temporal operations without model or provider calls.
func (service Service) Query(ctx context.Context, request Request) (result Result, resultErr error) {
	if ctx == nil {
		return Result{}, errors.New("query context is required")
	}
	if service.Reader == nil {
		return Result{}, errors.New("query reader is required")
	}
	normalized, err := NormalizeRequest(request, service.Limits)
	if err != nil {
		return Result{}, boundedQueryError{operation: "validate temporal query", cause: err}
	}
	if normalized.Intent != temporal.IntentPointInTime &&
		normalized.Intent != temporal.IntentTrendComparison &&
		normalized.Intent != temporal.IntentTrajectory &&
		normalized.Intent != temporal.IntentCausalChain {
		return Result{}, errors.New("execute temporal query: intent is not implemented")
	}

	tracer := service.Tracer
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("stacks")
	}
	ctx, span := tracer.Start(ctx, querySpanName, trace.WithAttributes(
		attribute.String("stacks.query.intent", string(normalized.Intent)),
		attribute.Bool("stacks.query.has_knowledge_cutoff", normalized.KnowledgeScope.Kind() == temporal.KnowledgeAsOf),
		attribute.String("stacks.query.entity_count_bucket", countBucket(len(normalized.EntityIDs))),
		attribute.String("stacks.query.predicate_count_bucket", countBucket(len(normalized.Predicates))),
	))
	defer func() {
		span.SetAttributes(attribute.String("stacks.query.outcome", queryOutcome(resultErr)))
		if resultErr != nil {
			observability.FinishSpan(span, errQueryTelemetry)
			return
		}
		observability.FinishSpan(span, nil)
	}()

	snapshot, err := service.Reader.Read(ctx, normalized.ReadSelection())
	if err != nil {
		return Result{}, boundedQueryError{operation: "read temporal snapshot", cause: err}
	}
	candidates, index, err := indexSnapshot(normalized, snapshot)
	if err != nil {
		return Result{}, err
	}

	var payload IntentPayload
	var gaps []Gap
	switch normalized.Intent {
	case temporal.IntentPointInTime:
		summary, reconstructErr := temporal.ReconstructState(
			normalized.Selections[0],
			normalized.KnowledgeScope,
			candidates,
		)
		if reconstructErr != nil {
			return Result{}, boundedQueryError{operation: "reconstruct temporal state", cause: reconstructErr}
		}
		point, projectErr := projectPoint(summary, index)
		if projectErr != nil {
			return Result{}, projectErr
		}
		payload, err = NewPointPayload(point)
		if err != nil {
			return Result{}, boundedQueryError{operation: "construct point result", cause: err}
		}
		gaps, err = projectPointGaps(normalized, snapshot, candidates, summary)
		if err != nil {
			return Result{}, err
		}
	case temporal.IntentTrendComparison:
		payload, gaps, err = executeTrend(normalized, snapshot, candidates, index)
		if err != nil {
			return Result{}, err
		}
	case temporal.IntentTrajectory:
		payload, gaps, err = executeTrajectory(normalized, snapshot, candidates, index)
		if err != nil {
			return Result{}, err
		}
	case temporal.IntentCausalChain:
		payload, gaps, err = executeCausal(normalized, candidates, index)
		if err != nil {
			return Result{}, err
		}
	}

	result, err = NormalizeResult(Result{
		Intent:         normalized.Intent,
		EntityIDs:      normalized.EntityIDs,
		EntityMatch:    normalized.EntityMatch,
		Predicates:     normalized.Predicates,
		Selections:     normalized.Selections,
		KnowledgeScope: normalized.KnowledgeScope,
		Limit:          normalized.Limit,
		Payload:        payload,
		Gaps:           gaps,
	})
	if err != nil {
		return Result{}, boundedQueryError{operation: "validate temporal query result", cause: err}
	}
	return result, nil
}

func executeCausal(
	request Request,
	candidates []temporal.StateCandidate,
	index projectionIndex,
) (IntentPayload, []Gap, error) {
	links, err := temporal.BuildCausalChain(
		request.Selections[0],
		request.KnowledgeScope,
		candidates,
	)
	if err != nil {
		return IntentPayload{}, nil, boundedQueryError{
			operation: "build temporal causal chain",
			cause:     err,
		}
	}
	if len(links) > request.Limit {
		return IntentPayload{}, nil, boundedQueryError{
			operation: "bound temporal causal chain",
			cause:     ErrLimitExceeded,
		}
	}
	causal, err := projectCausal(request.Selections[0], links, index)
	if err != nil {
		return IntentPayload{}, nil, err
	}
	payload, err := NewCausalPayload(causal)
	if err != nil {
		return IntentPayload{}, nil, boundedQueryError{
			operation: "construct causal result",
			cause:     err,
		}
	}
	gaps := []Gap{}
	if len(links) == 0 {
		gaps = append(gaps, Gap{
			Kind:           GapNoCausalEvidence,
			Predicate:      temporal.CausalPredicate,
			SelectionLabel: request.Selections[0].Label(),
		})
	}
	return payload, gaps, nil
}

func executeTrajectory(
	request Request,
	snapshot ReadSnapshot,
	candidates []temporal.StateCandidate,
	index projectionIndex,
) (IntentPayload, []Gap, error) {
	transitions, err := temporal.BuildTrajectory(
		request.Selections[0],
		request.KnowledgeScope,
		candidates,
	)
	if err != nil {
		return IntentPayload{}, nil, boundedQueryError{
			operation: "build temporal trajectory",
			cause:     err,
		}
	}
	if len(transitions) > request.Limit {
		return IntentPayload{}, nil, boundedQueryError{
			operation: "bound temporal trajectory",
			cause:     ErrLimitExceeded,
		}
	}
	summary, err := temporal.AggregateWindow(
		request.Selections[0],
		request.KnowledgeScope,
		candidates,
	)
	if err != nil {
		return IntentPayload{}, nil, boundedQueryError{
			operation: "aggregate trajectory window",
			cause:     err,
		}
	}
	trajectory, err := projectTrajectory(
		request.Selections[0],
		transitions,
		summary.Unresolved,
		index,
	)
	if err != nil {
		return IntentPayload{}, nil, err
	}
	payload, err := NewTrajectoryPayload(trajectory)
	if err != nil {
		return IntentPayload{}, nil, boundedQueryError{
			operation: "construct trajectory result",
			cause:     err,
		}
	}
	gaps, err := projectTrajectoryGaps(request, snapshot, candidates, summary)
	if err != nil {
		return IntentPayload{}, nil, err
	}
	return payload, gaps, nil
}

func executeTrend(
	request Request,
	snapshot ReadSnapshot,
	candidates []temporal.StateCandidate,
	index projectionIndex,
) (IntentPayload, []Gap, error) {
	beforeSummary, err := temporal.AggregateWindow(request.Selections[0], request.KnowledgeScope, candidates)
	if err != nil {
		return IntentPayload{}, nil, boundedQueryError{operation: "aggregate before trend window", cause: err}
	}
	afterSummary, err := temporal.AggregateWindow(request.Selections[1], request.KnowledgeScope, candidates)
	if err != nil {
		return IntentPayload{}, nil, boundedQueryError{operation: "aggregate after trend window", cause: err}
	}
	comparison, err := temporal.CompareWindowSummaries(beforeSummary, afterSummary)
	if err != nil {
		return IntentPayload{}, nil, boundedQueryError{operation: "compare trend windows", cause: err}
	}
	trend, err := projectTrend(comparison, index)
	if err != nil {
		return IntentPayload{}, nil, err
	}
	payload, err := NewTrendPayload(trend)
	if err != nil {
		return IntentPayload{}, nil, boundedQueryError{operation: "construct trend result", cause: err}
	}
	gaps, err := projectTrendGaps(request, snapshot, candidates, beforeSummary, afterSummary)
	if err != nil {
		return IntentPayload{}, nil, err
	}
	return payload, gaps, nil
}

type boundedQueryError struct {
	operation string
	cause     error
}

func (err boundedQueryError) Error() string { return err.operation + " failed" }
func (err boundedQueryError) Unwrap() error { return err.cause }

func queryOutcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline-exceeded"
	case errors.Is(err, ErrEntityNotFound):
		return "entity-not-found"
	case errors.Is(err, ErrLimitExceeded):
		return "limit-exceeded"
	default:
		return "failed"
	}
}

func countBucket(count int) string {
	switch {
	case count == 0:
		return "0"
	case count == 1:
		return "1"
	case count <= smallCountBucketLimit:
		return "2-5"
	default:
		return "6-plus"
	}
}
