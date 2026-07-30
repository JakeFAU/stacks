package main

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/trace"

	"stacks/internal/query"
)

// temporalQueryExecutor opens the canonical query database only after the
// caller request has passed deterministic normalization.
type temporalQueryExecutor struct {
	Open        func(context.Context, string) (queryDatabase, error)
	DatabaseURL string
	Limits      query.Limits
	Tracer      trace.Tracer
}

func (executor temporalQueryExecutor) Query(
	ctx context.Context,
	request query.Request,
) (query.Result, error) {
	normalized, err := query.NormalizeRequest(request, executor.Limits)
	if err != nil {
		return query.Result{}, err
	}
	if executor.Open == nil {
		return query.Result{}, errors.New("query command dependencies are not configured")
	}
	database, err := executor.Open(ctx, executor.DatabaseURL)
	if err != nil {
		if cancellationErr := canonicalContextError(ctx, err); cancellationErr != nil {
			return query.Result{}, cancellationErr
		}
		return query.Result{}, errors.New("open query database failed")
	}
	defer database.Close()
	result, err := (query.Service{
		Reader: query.PostgresRepository{
			Database:         database,
			SnapshotObserver: query.PostgresSnapshotObserver{Tracer: executor.Tracer},
		},
		Limits: executor.Limits,
		Tracer: executor.Tracer,
	}).Query(ctx, normalized)
	if cancellationErr := canonicalContextError(ctx, err); cancellationErr != nil {
		return query.Result{}, cancellationErr
	}
	return result, err
}
