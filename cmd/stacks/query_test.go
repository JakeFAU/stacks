package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/temporal"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"go.opentelemetry.io/otel/trace"
	"stacks/internal/cli"
	"stacks/internal/config"
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
	"stacks/internal/query"
	"stacks/internal/queryplan"
)

func TestTemporalQueryExecutorNormalizesBeforeOpeningDatabase(t *testing.T) {
	var opens int
	executor := temporalQueryExecutor{
		Open: func(context.Context, string) (queryDatabase, error) {
			opens++
			return &recordingQueryDatabase{}, nil
		},
		DatabaseURL: "postgres://synthetic-query-app",
		Limits:      query.Limits{},
	}

	_, err := executor.Query(context.Background(), validQueryTrendInvocation(t).Query.Request)
	if err == nil {
		t.Fatal("Query() error = nil, want invalid limits")
	}
	if opens != 0 {
		t.Fatalf("database opens = %d, want 0", opens)
	}
}

func TestTemporalQueryExecutorReadsOneCanonicalSnapshotAndClosesDatabase(t *testing.T) {
	database := &recordingQueryDatabase{snapshot: postgres.TemporalQuerySnapshot{
		Entities: []postgres.TemporalEntityRecord{{EntityID: "entity-a", Known: true}},
	}}
	var opens int
	executor := temporalQueryExecutor{
		Open: func(_ context.Context, databaseURL string) (queryDatabase, error) {
			opens++
			if databaseURL != "postgres://synthetic-query-app" {
				t.Fatalf("database URL = %q, want configured URL", databaseURL)
			}
			return database, nil
		},
		DatabaseURL: "postgres://synthetic-query-app",
		Limits: query.Limits{
			MaxEntities: 16, MaxPredicates: 32, MaxChronology: 1000,
		},
		Tracer: tracenoop.NewTracerProvider().Tracer("synthetic"),
	}

	result, err := executor.Query(context.Background(), validQueryTrendInvocation(t).Query.Request)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if result.Intent != validQueryTrendInvocation(t).Query.Request.Intent {
		t.Fatalf("result intent = %q, want %q", result.Intent, validQueryTrendInvocation(t).Query.Request.Intent)
	}
	if opens != 1 || database.loads != 1 || database.closes != 1 {
		t.Fatalf("database calls = open:%d load:%d close:%d, want 1/1/1", opens, database.loads, database.closes)
	}
}

func TestTemporalQueryExecutorBoundsOpenErrorsAndPreservesCallerCancellation(t *testing.T) {
	const privateDatabaseURL = "postgres://private-user:private-password@private-host/private-database"
	executor := temporalQueryExecutor{
		Open: func(context.Context, string) (queryDatabase, error) {
			return nil, errors.New(privateDatabaseURL)
		},
		DatabaseURL: privateDatabaseURL,
		Limits:      query.Limits{MaxEntities: 16, MaxPredicates: 32, MaxChronology: 1000},
	}
	_, err := executor.Query(context.Background(), validQueryTrendInvocation(t).Query.Request)
	if err == nil || strings.Contains(err.Error(), privateDatabaseURL) {
		t.Fatalf("open error = %v, want bounded error without database URL", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	executor.Open = func(context.Context, string) (queryDatabase, error) {
		return nil, context.DeadlineExceeded
	}
	_, err = executor.Query(canceled, validQueryTrendInvocation(t).Query.Request)
	if err != context.Canceled {
		t.Fatalf("caller cancellation error = %v, want context.Canceled", err)
	}
}

func TestQueryAskConstructsPlannerAfterLocalValidationAndOpensDatabaseAfterPlan(t *testing.T) {
	database := &recordingQueryDatabase{snapshot: postgres.TemporalQuerySnapshot{
		Entities: []postgres.TemporalEntityRecord{{EntityID: "entity-a", Known: true}},
	}}
	var (
		plannerConstructions int
		modelCalls           int
		databaseOpens        int
	)
	runtime := commandRuntime{
		openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
			databaseOpens++
			return database, nil
		},
		newQueryPlannerModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (queryplan.Model, error) {
			plannerConstructions++
			return queryPlanModelFunc(func(context.Context, queryplan.ModelRequest) (queryplan.ModelResponse, error) {
				modelCalls++
				return executableTrendPlanResponse(t), nil
			}), nil
		},
	}
	var output strings.Builder
	commands, err := commandProviderWithRuntime(
		context.Background(), validQueryAskSettings(), &output, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
		strings.NewReader("What changed between the two windows?"),
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}
	if err := commands[string(config.CommandQuery)].Run(context.Background(), validQueryAskInvocation()); err != nil {
		t.Fatalf("query ask error = %v", err)
	}
	if plannerConstructions != 1 || modelCalls != 1 || databaseOpens != 1 || database.closes != 1 {
		t.Fatalf("ask lifecycle = planner:%d model:%d database-open:%d close:%d, want 1/1/1/1", plannerConstructions, modelCalls, databaseOpens, database.closes)
	}
	if output.Len() == 0 {
		t.Fatal("query ask output is empty")
	}
}

func TestQueryAskInvalidLocalInputConstructsNoPlannerOrDatabase(t *testing.T) {
	var plannerConstructions, databaseOpens int
	runtime := commandRuntime{
		openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
			databaseOpens++
			return &recordingQueryDatabase{}, nil
		},
		newQueryPlannerModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (queryplan.Model, error) {
			plannerConstructions++
			return nil, errors.New("unexpected provider construction")
		},
	}
	commands, err := commandProviderWithRuntime(
		context.Background(), validQueryAskSettings(), io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
		strings.NewReader("entity-a must not be disclosed"),
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}
	err = commands[string(config.CommandQuery)].Run(context.Background(), validQueryAskInvocation())
	if err == nil {
		t.Fatal("query ask error = nil, want local input rejection")
	}
	if plannerConstructions != 0 || databaseOpens != 0 {
		t.Fatalf("invalid input constructions = planner:%d database:%d, want 0/0", plannerConstructions, databaseOpens)
	}
}

func TestQueryAskInvalidProposalOpensNoDatabase(t *testing.T) {
	var plannerConstructions, databaseOpens int
	runtime := commandRuntime{
		openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
			databaseOpens++
			return &recordingQueryDatabase{}, nil
		},
		newQueryPlannerModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (queryplan.Model, error) {
			plannerConstructions++
			return queryPlanModelFunc(func(context.Context, queryplan.ModelRequest) (queryplan.ModelResponse, error) {
				return queryplan.ModelResponse{Output: json.RawMessage(`{`), Provider: modelpolicy.ProviderOpenAI, ModelID: "synthetic-model", PromptVersion: queryplan.PromptVersion, SchemaName: queryplan.SchemaName, Attempts: 1}, nil
			}), nil
		},
	}
	commands, err := commandProviderWithRuntime(
		context.Background(), validQueryAskSettings(), io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
		strings.NewReader("What changed between the two windows?"),
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}
	err = commands[string(config.CommandQuery)].Run(context.Background(), validQueryAskInvocation())
	if err == nil {
		t.Fatal("query ask error = nil, want invalid proposal rejection")
	}
	if plannerConstructions != 1 || databaseOpens != 0 {
		t.Fatalf("invalid proposal constructions = planner:%d database:%d, want 1/0", plannerConstructions, databaseOpens)
	}
}

type queryPlanModelFunc func(context.Context, queryplan.ModelRequest) (queryplan.ModelResponse, error)

func (function queryPlanModelFunc) Plan(ctx context.Context, request queryplan.ModelRequest) (queryplan.ModelResponse, error) {
	return function(ctx, request)
}

func validQueryAskSettings() config.Settings {
	settings := validQueryCommandSettings()
	settings.QueryPlanner = config.QueryPlannerSettings{Timeout: time.Minute, MaxQuestionBytes: 1024}
	settings.Application.Model = config.ModelSettings{
		Provider: modelpolicy.ProviderOpenAI, DataMode: modelpolicy.DataModePersonal,
		ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1, OpenAIAPIKey: "synthetic-api-key",
	}
	return settings
}

func validQueryAskInvocation() cli.Invocation {
	return cli.Invocation{
		Command: cli.CommandQuery,
		Action:  cli.ActionAsk,
		QueryAsk: &cli.QueryAskInput{
			EntityIDs:     []identity.EntityID{"entity-a"},
			ReferenceTime: time.Date(2026, time.July, 29, 16, 0, 0, 0, time.UTC),
			Output:        cli.QueryOutputJSON,
		},
	}
}

func executableTrendPlanResponse(t *testing.T) queryplan.ModelResponse {
	t.Helper()
	type selectionJSON struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
		At    string `json:"at"`
		Start string `json:"start"`
		End   string `json:"end"`
	}
	type knowledgeJSON struct {
		Kind string `json:"kind"`
		AsOf string `json:"as_of"`
	}
	payload, err := json.Marshal(struct {
		Status          string          `json:"status"`
		Reason          string          `json:"reason"`
		Intent          string          `json:"intent"`
		EntityMatch     string          `json:"entity_match"`
		Predicates      []string        `json:"predicates"`
		Selections      []selectionJSON `json:"selections"`
		KnowledgeScope  knowledgeJSON   `json:"knowledge_scope"`
		ChronologyLimit int             `json:"chronology_limit"`
	}{
		Status: "executable", Reason: "none", Intent: string(temporal.IntentTrendComparison), EntityMatch: "all", Predicates: []string{},
		Selections: []selectionJSON{
			{Kind: "window", Label: "before", Start: "2024-01-01T00:00:00Z", End: "2024-02-01T00:00:00Z"},
			{Kind: "window", Label: "after", Start: "2024-03-01T00:00:00Z", End: "2024-04-01T00:00:00Z"},
		},
		KnowledgeScope: knowledgeJSON{Kind: "current"},
	})
	if err != nil {
		t.Fatalf("marshal synthetic plan: %v", err)
	}
	return queryplan.ModelResponse{
		Output: payload, Provider: modelpolicy.ProviderOpenAI, ModelID: "synthetic-model",
		PromptVersion: queryplan.PromptVersion, SchemaName: queryplan.SchemaName,
		Usage: queryplan.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, Attempts: 1,
	}
}
