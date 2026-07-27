package query

import (
	"context"
	"errors"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/observability"
)

const postgresTemporalSnapshotSpanName = "stacks.postgres.temporal_snapshot"

var errPostgresTemporalSnapshotTelemetry = errors.New(
	"PostgreSQL temporal snapshot failed",
)

// PostgresSnapshotObserver implements the adapter-owned snapshot observation
// boundary with root OpenTelemetry policy.
type PostgresSnapshotObserver struct {
	Tracer trace.Tracer
}

var _ postgres.TemporalSnapshotObserver = PostgresSnapshotObserver{}

// StartTemporalSnapshot starts one bounded storage span and returns its finish
// callback to the adapter that owns the transaction lifecycle.
func (observer PostgresSnapshotObserver) StartTemporalSnapshot(
	ctx context.Context,
	input postgres.TemporalSnapshotAttributes,
) (context.Context, func(error)) {
	tracer := observer.Tracer
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("stacks")
	}
	ctx, span := tracer.Start(
		ctx,
		postgresTemporalSnapshotSpanName,
		trace.WithAttributes(
			attribute.Bool(
				"stacks.postgres.temporal_snapshot.has_knowledge_cutoff",
				input.HasKnowledgeCutoff,
			),
			attribute.String(
				"stacks.postgres.temporal_snapshot.entity_count_bucket",
				postgresTemporalSnapshotCountBucket(input.EntityCountBucket),
			),
			attribute.String(
				"stacks.postgres.temporal_snapshot.predicate_count_bucket",
				postgresTemporalSnapshotCountBucket(
					input.PredicateCountBucket,
				),
			),
			attribute.String(
				"stacks.postgres.temporal_snapshot.selection_count_bucket",
				postgresTemporalSnapshotCountBucket(
					input.SelectionCountBucket,
				),
			),
		),
	)
	return ctx, func(err error) {
		span.SetAttributes(attribute.String(
			"stacks.postgres.temporal_snapshot.outcome",
			postgresTemporalSnapshotOutcome(err),
		))
		if err != nil {
			observability.FinishSpan(
				span,
				errPostgresTemporalSnapshotTelemetry,
			)
			return
		}
		observability.FinishSpan(span, nil)
	}
}

func postgresTemporalSnapshotCountBucket(
	value postgres.TemporalSnapshotCountBucket,
) string {
	switch value {
	case postgres.TemporalSnapshotCountZero,
		postgres.TemporalSnapshotCountOne,
		postgres.TemporalSnapshotCountTwoToFive,
		postgres.TemporalSnapshotCountSixPlus:
		return string(value)
	default:
		return "invalid"
	}
}

func postgresTemporalSnapshotOutcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline-exceeded"
	default:
		return "failed"
	}
}
