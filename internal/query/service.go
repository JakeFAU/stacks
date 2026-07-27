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
// snapshot. D1 supports trend comparison; later intents extend this boundary.
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
	if normalized.Intent != temporal.IntentTrendComparison {
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
	candidates, index, err := indexTrendSnapshot(normalized, snapshot)
	if err != nil {
		return Result{}, err
	}

	beforeSummary, err := temporal.AggregateWindow(normalized.Selections[0], normalized.KnowledgeScope, candidates)
	if err != nil {
		return Result{}, boundedQueryError{operation: "aggregate before trend window", cause: err}
	}
	afterSummary, err := temporal.AggregateWindow(normalized.Selections[1], normalized.KnowledgeScope, candidates)
	if err != nil {
		return Result{}, boundedQueryError{operation: "aggregate after trend window", cause: err}
	}
	comparison, err := temporal.CompareWindowSummaries(beforeSummary, afterSummary)
	if err != nil {
		return Result{}, boundedQueryError{operation: "compare trend windows", cause: err}
	}
	trend, err := projectTrend(comparison, index)
	if err != nil {
		return Result{}, err
	}
	payload, err := NewTrendPayload(trend)
	if err != nil {
		return Result{}, boundedQueryError{operation: "construct trend result", cause: err}
	}
	gaps, err := projectTrendGaps(normalized, snapshot, candidates, beforeSummary, afterSummary)
	if err != nil {
		return Result{}, err
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
