package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/app"
	"stacks/internal/cli"
	"stacks/internal/config"
	"stacks/internal/directory"
	"stacks/internal/doctor"
	"stacks/internal/extract"
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
	"stacks/internal/query"
	"stacks/internal/queryplan"
	"stacks/internal/source"
)

func TestQueryTrendOpensOneCanonicalDatabaseAndClosesItOnce(t *testing.T) {
	database := &recordingQueryDatabase{
		snapshot: postgres.TemporalQuerySnapshot{
			Entities: []postgres.TemporalEntityRecord{
				{EntityID: "entity-a", Known: true},
			},
		},
	}
	var (
		opens       int
		databaseURL string
	)
	runtime := commandRuntime{
		openQueryDatabase: func(_ context.Context, gotDatabaseURL string) (queryDatabase, error) {
			opens++
			databaseURL = gotDatabaseURL
			return database, nil
		},
	}
	settings := validQueryCommandSettings()
	commands, err := commandProviderWithRuntime(
		context.Background(), settings, io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}

	err = commands[string(config.CommandQuery)].Run(
		context.Background(),
		validQueryTrendInvocation(t),
	)
	if err != nil {
		t.Fatalf("query trend error = %v", err)
	}
	if opens != 1 || database.loads != 1 || database.closes != 1 {
		t.Fatalf(
			"canonical query database calls = open:%d load:%d close:%d, want 1/1/1",
			opens,
			database.loads,
			database.closes,
		)
	}
	if databaseURL != settings.Database.URL {
		t.Fatalf("query database URL = %q, want configured application URL", databaseURL)
	}
}

func TestQueryDatabaseOpenErrorIsBoundedAndPreservesContextIdentity(t *testing.T) {
	const privateDatabaseURL = "postgres://private-user:private-password@private-host/private-database"
	tests := []struct {
		name string
		err  error
		want error
	}{
		{
			name: "provider detail",
			err:  errors.New(privateDatabaseURL),
		},
		{
			name: "cancellation",
			err:  fmt.Errorf("%s: %w", privateDatabaseURL, context.Canceled),
			want: context.Canceled,
		},
		{
			name: "deadline",
			err:  fmt.Errorf("%s: %w", privateDatabaseURL, context.DeadlineExceeded),
			want: context.DeadlineExceeded,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := commandRuntime{
				openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
					return nil, test.err
				},
			}
			commands, err := commandProviderWithRuntime(
				context.Background(),
				validQueryCommandSettings(),
				io.Discard,
				io.Discard,
				tracenoop.NewTracerProvider().Tracer("synthetic"),
				nil,
				nil,
				runtime,
			)
			if err != nil {
				t.Fatalf("commandProviderWithRuntime() error = %v", err)
			}

			err = commands[string(config.CommandQuery)].Run(
				context.Background(),
				validQueryTrendInvocation(t),
			)
			if err == nil {
				t.Fatal("query database-open error = nil")
			}
			if strings.Contains(err.Error(), privateDatabaseURL) {
				t.Fatalf("query database-open error exposed private connection detail: %v", err)
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("query database-open error = %v, want errors.Is(_, %v)", err, test.want)
			}
		})
	}
}

func TestQueryDatabaseOpenErrorPrefersCallerCancellationOverProviderDeadline(t *testing.T) {
	const privateProviderDetail = "private provider deadline detail"
	runtime := commandRuntime{
		openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
			return nil, fmt.Errorf("%s: %w", privateProviderDetail, context.DeadlineExceeded)
		},
	}
	commands, err := commandProviderWithRuntime(
		context.Background(),
		validQueryCommandSettings(),
		io.Discard,
		io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"),
		nil,
		nil,
		runtime,
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = commands[string(config.CommandQuery)].Run(ctx, validQueryTrendInvocation(t))
	if err != context.Canceled {
		t.Fatalf("query database-open error = %v, want canonical context.Canceled", err)
	}
	if strings.Contains(err.Error(), privateProviderDetail) {
		t.Fatalf("query database-open error exposed private provider detail: %v", err)
	}
}

func TestQueryTrendConstructsNoSourceDirectoryModelOrProvider(t *testing.T) {
	database := &recordingQueryDatabase{
		snapshot: postgres.TemporalQuerySnapshot{
			Entities: []postgres.TemporalEntityRecord{
				{EntityID: "entity-a", Known: true},
			},
		},
	}
	var forbiddenConstructions int
	runtime := commandRuntime{
		openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
			return database, nil
		},
		newSource: func(context.Context, config.ApplicationSettings) (source.Source, error) {
			forbiddenConstructions++
			return emptyRuntimeSource{}, nil
		},
		newDirectoryLookup: func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
			forbiddenConstructions++
			return &recordingRuntimeDirectoryLookup{}, nil
		},
		openCanonicalRepositories: func(context.Context, string, bool) (canonicalRepositories, error) {
			forbiddenConstructions++
			return canonicalRepositories{}, nil
		},
		newModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (extract.Model, error) {
			forbiddenConstructions++
			return noOpRuntimeModel{}, nil
		},
		newDoctorProviderProbe: func(config.ModelSettings) (doctor.ModelProbe, doctor.DisclosureProbe, error) {
			forbiddenConstructions++
			return nil, nil, nil
		},
	}
	commands, err := commandProviderWithRuntime(
		context.Background(), validQueryCommandSettings(), io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}

	if err := commands[string(config.CommandQuery)].Run(
		context.Background(),
		validQueryTrendInvocation(t),
	); err != nil {
		t.Fatalf("query trend error = %v", err)
	}
	if forbiddenConstructions != 0 {
		t.Fatalf("forbidden query constructions = %d, want 0", forbiddenConstructions)
	}
}

func TestQueryValidationFailsBeforeDatabaseConstruction(t *testing.T) {
	settings := validQueryCommandSettings()
	settings.Query.MaxEntities = 0
	var opens int
	runtime := commandRuntime{
		openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
			opens++
			return &recordingQueryDatabase{}, nil
		},
	}
	commands, err := commandProviderWithRuntime(
		context.Background(), settings, io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}

	err = commands[string(config.CommandQuery)].Run(
		context.Background(),
		validQueryTrendInvocation(t),
	)
	if err == nil || !strings.Contains(err.Error(), config.QueryMaxEntitiesEnvironmentVariable) {
		t.Fatalf("query trend error = %v, want query limit validation", err)
	}
	if opens != 0 {
		t.Fatalf("query database opens before validation = %d, want 0", opens)
	}
}

func TestQueryRequestCardinalityFailsBeforeDatabaseConstruction(t *testing.T) {
	const (
		privateEntityA    = identity.EntityID("private-entity-a")
		privateEntityB    = identity.EntityID("private-entity-b")
		privatePredicateA = observation.Predicate("private.predicate.a")
		privatePredicateB = observation.Predicate("private.predicate.b")
	)
	tests := []struct {
		name       string
		configure  func(*config.Settings, *cli.Invocation)
		privateIDs []string
	}{
		{
			name: "entities exceed configured maximum",
			configure: func(settings *config.Settings, invocation *cli.Invocation) {
				settings.Query.MaxEntities = 1
				invocation.Query.Request.EntityIDs = []identity.EntityID{privateEntityA, privateEntityB}
			},
			privateIDs: []string{string(privateEntityA), string(privateEntityB)},
		},
		{
			name: "predicates exceed configured maximum",
			configure: func(settings *config.Settings, invocation *cli.Invocation) {
				settings.Query.MaxPredicates = 1
				invocation.Query.Request.Predicates = []observation.Predicate{privatePredicateA, privatePredicateB}
			},
			privateIDs: []string{string(privatePredicateA), string(privatePredicateB)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := validQueryCommandSettings()
			invocation := validQueryTrendInvocation(t)
			test.configure(&settings, &invocation)
			var opens int
			runtime := commandRuntime{
				openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
					opens++
					return &recordingQueryDatabase{}, nil
				},
			}
			commands, err := commandProviderWithRuntime(
				context.Background(), settings, io.Discard, io.Discard,
				tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
			)
			if err != nil {
				t.Fatalf("commandProviderWithRuntime() error = %v", err)
			}

			err = commands[string(config.CommandQuery)].Run(context.Background(), invocation)
			if err == nil {
				t.Fatal("query trend error = nil, want request cardinality error")
			}
			if opens != 0 {
				t.Fatalf("query database opens before request validation = %d, want 0", opens)
			}
			for _, privateID := range test.privateIDs {
				if strings.Contains(err.Error(), privateID) {
					t.Fatalf("query trend error %q contains private request value", err)
				}
			}
		})
	}
}

func TestQueryMalformedInvocationFailsBeforeDatabaseConstruction(t *testing.T) {
	tests := []struct {
		name       string
		invocation func(*testing.T) cli.Invocation
	}{
		{
			name: "missing query input",
			invocation: func(*testing.T) cli.Invocation {
				return cli.Invocation{Command: cli.CommandQuery, Action: cli.ActionTrend}
			},
		},
		{
			name: "unexpected positional argument",
			invocation: func(t *testing.T) cli.Invocation {
				invocation := validQueryTrendInvocation(t)
				invocation.Arguments = []string{"private-positional-value"}
				return invocation
			},
		},
		{
			name: "invalid output",
			invocation: func(t *testing.T) cli.Invocation {
				invocation := validQueryTrendInvocation(t)
				invocation.Query.Output = cli.QueryOutput("private-output-value")
				return invocation
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var opens int
			runtime := commandRuntime{
				openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
					opens++
					return &recordingQueryDatabase{}, nil
				},
			}
			commands, err := commandProviderWithRuntime(
				context.Background(), validQueryCommandSettings(), io.Discard, io.Discard,
				tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
			)
			if err != nil {
				t.Fatalf("commandProviderWithRuntime() error = %v", err)
			}

			err = commands[string(config.CommandQuery)].Run(context.Background(), test.invocation(t))
			if err == nil {
				t.Fatal("query trend error = nil, want invalid invocation error")
			}
			if opens != 0 {
				t.Fatalf("query database opens before invocation validation = %d, want 0", opens)
			}
			for _, privateValue := range []string{"private-positional-value", "private-output-value"} {
				if strings.Contains(err.Error(), privateValue) {
					t.Fatalf("query trend error %q contains private invocation value", err)
				}
			}
		})
	}
}

func TestQueryTrendRejectsD4IntentsBeforeDatabaseConstruction(t *testing.T) {
	const privateEntity = identity.EntityID("private-d4-intent-entity")
	point, err := temporal.At(
		"private-point-selection",
		time.Date(2024, time.January, 15, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("construct point query selection: %v", err)
	}
	window, err := temporal.Between(
		"private-window-selection",
		time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, time.February, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("construct window query selection: %v", err)
	}
	tests := []struct {
		name    string
		request query.Request
	}{
		{
			name: "point in time",
			request: query.Request{
				Intent:         temporal.IntentPointInTime,
				EntityIDs:      []identity.EntityID{privateEntity},
				EntityMatch:    query.EntityMatchAll,
				Selections:     []temporal.TemporalSelection{point},
				KnowledgeScope: temporal.CurrentKnowledge(),
			},
		},
		{
			name: "trajectory",
			request: query.Request{
				Intent:         temporal.IntentTrajectory,
				EntityIDs:      []identity.EntityID{privateEntity},
				EntityMatch:    query.EntityMatchAll,
				Selections:     []temporal.TemporalSelection{window},
				KnowledgeScope: temporal.CurrentKnowledge(),
				Limit:          10,
			},
		},
		{
			name: "causal chain",
			request: query.Request{
				Intent:         temporal.IntentCausalChain,
				EntityIDs:      []identity.EntityID{privateEntity},
				EntityMatch:    query.EntityMatchAll,
				Predicates:     []observation.Predicate{query.CausalPredicate},
				Selections:     []temporal.TemporalSelection{window},
				KnowledgeScope: temporal.CurrentKnowledge(),
				Limit:          10,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := validQueryCommandSettings()
			limits := query.Limits{
				MaxEntities:   settings.Query.MaxEntities,
				MaxPredicates: settings.Query.MaxPredicates,
				MaxChronology: settings.Query.MaxChronology,
			}
			if _, err := query.NormalizeRequest(test.request, limits); err != nil {
				t.Fatalf("D4 request fixture is not contract-valid: %v", err)
			}
			var opens int
			runtime := commandRuntime{
				openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
					opens++
					return &recordingQueryDatabase{}, nil
				},
			}
			commands, err := commandProviderWithRuntime(
				context.Background(), settings, io.Discard, io.Discard,
				tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
			)
			if err != nil {
				t.Fatalf("commandProviderWithRuntime() error = %v", err)
			}
			invocation := validQueryTrendInvocation(t)
			invocation.Query.Request = test.request

			err = commands[string(config.CommandQuery)].Run(context.Background(), invocation)
			if err == nil || !strings.Contains(err.Error(), "intent") {
				t.Fatalf("query trend error = %v, want bounded intent mismatch error", err)
			}
			if strings.Contains(err.Error(), string(privateEntity)) ||
				strings.Contains(err.Error(), "private-point-selection") ||
				strings.Contains(err.Error(), "private-window-selection") {
				t.Fatalf("query trend error %q contains private request material", err)
			}
			if opens != 0 {
				t.Fatalf("query database opens before intent validation = %d, want 0", opens)
			}
		})
	}
}

func TestQueryChronologyAndCausalValidationFailsBeforeDatabaseConstruction(t *testing.T) {
	window, err := temporal.Between(
		"between",
		time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, time.February, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		action     cli.Action
		intent     temporal.Intent
		predicates []observation.Predicate
		limit      int
	}{
		{name: "trajectory missing limit", action: cli.ActionTrajectory, intent: temporal.IntentTrajectory},
		{name: "trajectory negative limit", action: cli.ActionTrajectory, intent: temporal.IntentTrajectory, limit: -1},
		{name: "trajectory over maximum", action: cli.ActionTrajectory, intent: temporal.IntentTrajectory, limit: 1001},
		{
			name: "causal missing limit", action: cli.ActionCausal, intent: temporal.IntentCausalChain,
			predicates: []observation.Predicate{query.CausalPredicate},
		},
		{
			name: "causal negative limit", action: cli.ActionCausal, intent: temporal.IntentCausalChain,
			predicates: []observation.Predicate{query.CausalPredicate}, limit: -1,
		},
		{
			name: "causal over maximum", action: cli.ActionCausal, intent: temporal.IntentCausalChain,
			predicates: []observation.Predicate{query.CausalPredicate}, limit: 1001,
		},
		{
			name: "causal wrong predicate", action: cli.ActionCausal, intent: temporal.IntentCausalChain,
			predicates: []observation.Predicate{"private.causal-predicate"}, limit: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocation := cli.Invocation{
				Command: cli.CommandQuery, Action: test.action,
				Query: &cli.QueryInput{
					Request: query.Request{
						Intent: test.intent, EntityIDs: []identity.EntityID{"private-entity"},
						EntityMatch: query.EntityMatchAll, Predicates: test.predicates,
						Selections:     []temporal.TemporalSelection{window},
						KnowledgeScope: temporal.CurrentKnowledge(), Limit: test.limit,
					},
					Output: cli.QueryOutputText,
				},
			}
			var opens int
			runtime := commandRuntime{
				openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
					opens++
					return &recordingQueryDatabase{}, nil
				},
			}
			commands, err := commandProviderWithRuntime(
				context.Background(), validQueryCommandSettings(), io.Discard, io.Discard,
				tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
			)
			if err != nil {
				t.Fatal(err)
			}
			err = commands[string(config.CommandQuery)].Run(context.Background(), invocation)
			if err == nil {
				t.Fatal("query error = nil, want validation failure")
			}
			if opens != 0 {
				t.Fatalf("database opens = %d, want 0", opens)
			}
			for _, privateValue := range []string{"private-entity", "private.causal-predicate"} {
				if strings.Contains(err.Error(), privateValue) {
					t.Fatalf("error %q contains private request material", err)
				}
			}
		})
	}
}

func TestQueryTrendPreservesCancellationAndBoundedTelemetry(t *testing.T) {
	const (
		privateEntity    = identity.EntityID("private-entity-marker-d34")
		privatePredicate = observation.Predicate("private.predicate.marker-d34")
	)
	database := &recordingQueryDatabase{err: context.Canceled}
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	runtime := commandRuntime{
		openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
			return database, nil
		},
	}
	commands, err := commandProviderWithRuntime(
		context.Background(), validQueryCommandSettings(), io.Discard, io.Discard,
		tracerProvider.Tracer("query-composition-test"), nil, nil, runtime,
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}
	invocation := validQueryTrendInvocation(t)
	invocation.Query.Request.EntityIDs = []identity.EntityID{privateEntity}
	invocation.Query.Request.Predicates = []observation.Predicate{privatePredicate}
	ctx, cancel := context.WithCancel(context.WithValue(
		context.Background(),
		queryCompositionContextKey{},
		"preserved",
	))
	cancel()

	err = commands[string(config.CommandQuery)].Run(ctx, invocation)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("query trend error = %v, want context cancellation", err)
	}
	if !errors.Is(database.inputContext.Err(), context.Canceled) ||
		database.inputContext.Value(queryCompositionContextKey{}) != "preserved" {
		t.Fatal("query trend did not preserve caller cancellation and values to PostgreSQL")
	}
	if database.closes != 1 {
		t.Fatalf("query database closes = %d, want 1", database.closes)
	}
	spans := exporter.GetSpans()
	if len(spans) != 2 ||
		spans[0].Name != "stacks.postgres.temporal_snapshot" ||
		spans[1].Name != "stacks.query.temporal" {
		t.Fatalf("query spans = %#v, want bounded PostgreSQL and service spans", spans)
	}
	telemetry := fmt.Sprintf("%#v", spans)
	for _, privateMarker := range []string{string(privateEntity), string(privatePredicate)} {
		if strings.Contains(telemetry, privateMarker) {
			t.Fatalf("query telemetry contains private marker %q", privateMarker)
		}
	}
	for _, boundedMarker := range []string{"entity_count_bucket", "predicate_count_bucket", "canceled"} {
		if !strings.Contains(telemetry, boundedMarker) {
			t.Fatalf("query telemetry is missing bounded marker %q", boundedMarker)
		}
	}
}

type recordingQueryDatabase struct {
	snapshot     postgres.TemporalQuerySnapshot
	err          error
	loads        int
	closes       int
	inputContext context.Context
}

type queryCompositionContextKey struct{}

func (database *recordingQueryDatabase) LoadTemporalQuerySnapshot(
	ctx context.Context,
	selection postgres.TemporalQuerySelection,
	observer postgres.TemporalSnapshotObserver,
) (postgres.TemporalQuerySnapshot, error) {
	database.loads++
	database.inputContext = ctx
	if observer == nil {
		return postgres.TemporalQuerySnapshot{}, errors.New("query snapshot observer is required")
	}
	ctx, finish := observer.StartTemporalSnapshot(ctx, postgres.TemporalSnapshotAttributes{
		HasKnowledgeCutoff:   selection.KnowledgeAsOf != nil,
		EntityCountBucket:    temporalSnapshotCountBucket(len(selection.EntityIDs)),
		PredicateCountBucket: temporalSnapshotCountBucket(len(selection.Predicates)),
		SelectionCountBucket: temporalSnapshotCountBucket(len(selection.Selections)),
	})
	_ = ctx
	finish(database.err)
	if database.err != nil {
		return postgres.TemporalQuerySnapshot{}, database.err
	}
	return database.snapshot, nil
}

func (database *recordingQueryDatabase) Close() {
	database.closes++
}

func temporalSnapshotCountBucket(count int) postgres.TemporalSnapshotCountBucket {
	switch {
	case count == 0:
		return postgres.TemporalSnapshotCountZero
	case count == 1:
		return postgres.TemporalSnapshotCountOne
	case count <= 5:
		return postgres.TemporalSnapshotCountTwoToFive
	default:
		return postgres.TemporalSnapshotCountSixPlus
	}
}

func validQueryCommandSettings() config.Settings {
	return config.Settings{
		Database: config.DatabaseSettings{
			URL:    "postgres://synthetic-query-app",
			Scopes: []config.DatabaseScope{config.DatabaseScopeCore},
		},
		Query: config.QuerySettings{
			MaxEntities:   16,
			MaxPredicates: 32,
			MaxChronology: 1000,
		},
	}
}

func validQueryTrendInvocation(t *testing.T) cli.Invocation {
	t.Helper()
	before, err := temporal.Between(
		"before",
		time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, time.February, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("construct before query window: %v", err)
	}
	after, err := temporal.Between(
		"after",
		time.Date(2024, time.March, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, time.April, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("construct after query window: %v", err)
	}
	return cli.Invocation{
		Command: cli.CommandQuery,
		Action:  cli.ActionTrend,
		Query: &cli.QueryInput{
			Request: query.Request{
				Intent:         temporal.IntentTrendComparison,
				EntityIDs:      []identity.EntityID{"entity-a"},
				EntityMatch:    query.EntityMatchAll,
				Selections:     []temporal.TemporalSelection{before, after},
				KnowledgeScope: temporal.CurrentKnowledge(),
			},
			Output: cli.QueryOutputText,
		},
	}
}

func TestCoreCommandsOpenOnlyCanonicalPostgresRepositories(t *testing.T) {
	content, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read command composition: %v", err)
	}
	if strings.Contains(string(content), `"stacks/internal/storage"`) {
		t.Fatal("command composition still imports the retired legacy storage package")
	}

	settings := validCommandApplicationSettings(
		config.CommandSync,
		modelpolicy.ProviderOpenAI,
		modelpolicy.DataModePersonal,
	)
	var opened, closed int
	runtime := commandRuntime{
		newSource: func(context.Context, config.ApplicationSettings) (source.Source, error) {
			return emptyRuntimeSource{}, nil
		},
		openCanonicalRepositories: func(
			context.Context,
			string,
			bool,
		) (canonicalRepositories, error) {
			opened++
			return canonicalRepositories{
				ingestion: noOpRuntimeIngestionRepository{},
				close:     func() { closed++ },
			}, nil
		},
		newModel: func(
			context.Context,
			config.ModelSettings,
			modeltelemetry.Recorder,
			trace.Tracer,
		) (extract.Model, error) {
			return noOpRuntimeModel{}, nil
		},
	}
	commands, err := commandProviderWithRuntime(
		context.Background(),
		commandSettings(settings),
		io.Discard,
		io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"),
		nil,
		nil,
		runtime,
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}
	if err := commands[string(config.CommandSync)].Run(context.Background(), cli.Invocation{Command: cli.CommandSync}); err != nil {
		t.Fatalf("sync error = %v", err)
	}
	if opened != 1 || closed != 1 {
		t.Fatalf("canonical database owners opened/closed = %d/%d, want 1/1", opened, closed)
	}
}

func TestCoreOnlyCompositionConstructsNoDirectoryBoundary(t *testing.T) {
	settings := validCommandApplicationSettings(
		config.CommandSync,
		modelpolicy.ProviderOpenAI,
		modelpolicy.DataModePersonal,
	)
	var directoryLookups, directoryRepositories int
	runtime := commandRuntime{
		newSource: func(context.Context, config.ApplicationSettings) (source.Source, error) {
			return emptyRuntimeSource{}, nil
		},
		newDirectoryLookup: func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
			directoryLookups++
			return &recordingRuntimeDirectoryLookup{}, nil
		},
		openCanonicalRepositories: func(
			_ context.Context,
			_ string,
			includeDirectory bool,
		) (canonicalRepositories, error) {
			if includeDirectory {
				directoryRepositories++
			}
			return canonicalRepositories{ingestion: noOpRuntimeIngestionRepository{}}, nil
		},
		newModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (extract.Model, error) {
			return noOpRuntimeModel{}, nil
		},
	}
	commands, err := commandProviderWithRuntime(
		context.Background(), commandSettings(settings), io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}
	if err := commands[string(config.CommandSync)].Run(context.Background(), cli.Invocation{Command: cli.CommandSync}); err != nil {
		t.Fatalf("core-only sync error = %v", err)
	}
	if directoryLookups != 0 || directoryRepositories != 0 {
		t.Fatalf("core-only directory lookup/repository calls = %d/%d, want 0/0", directoryLookups, directoryRepositories)
	}
}

func TestEnabledDirectoryUsesOptionalCanonicalScope(t *testing.T) {
	settings := validCommandApplicationSettings(
		config.CommandSync,
		modelpolicy.ProviderOpenAI,
		modelpolicy.DataModePersonal,
	)
	enableRuntimeDirectory(&settings)
	var includeDirectory bool
	runtime := commandRuntime{
		newSource: func(context.Context, config.ApplicationSettings) (source.Source, error) {
			return emptyRuntimeSource{}, nil
		},
		newDirectoryLookup: func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
			return &recordingRuntimeDirectoryLookup{}, nil
		},
		openCanonicalRepositories: func(
			_ context.Context,
			_ string,
			include bool,
		) (canonicalRepositories, error) {
			includeDirectory = include
			return canonicalRepositories{
				ingestion: noOpRuntimeIngestionRepository{},
				directory: noOpRuntimeDirectoryRepository{},
			}, nil
		},
		newModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (extract.Model, error) {
			return noOpRuntimeModel{}, nil
		},
	}
	commands, err := commandProviderWithRuntime(
		context.Background(), commandSettings(settings), io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}
	if err := commands[string(config.CommandSync)].Run(context.Background(), cli.Invocation{Command: cli.CommandSync}); err != nil {
		t.Fatalf("directory-enabled sync error = %v", err)
	}
	if !includeDirectory {
		t.Fatal("enabled directory did not request optional canonical scope")
	}
}

func TestEnabledDirectoryWithoutScopeFailsBeforeConstruction(t *testing.T) {
	settings := validCommandApplicationSettings(
		config.CommandSync,
		modelpolicy.ProviderOpenAI,
		modelpolicy.DataModePersonal,
	)
	enableRuntimeDirectory(&settings)
	full := commandSettings(settings)
	full.Database.Scopes = []config.DatabaseScope{config.DatabaseScopeCore}
	var constructions int
	runtime := commandRuntime{
		newSource: func(context.Context, config.ApplicationSettings) (source.Source, error) {
			constructions++
			return emptyRuntimeSource{}, nil
		},
		newDirectoryLookup: func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
			constructions++
			return &recordingRuntimeDirectoryLookup{}, nil
		},
		openCanonicalRepositories: func(context.Context, string, bool) (canonicalRepositories, error) {
			constructions++
			return canonicalRepositories{}, nil
		},
	}
	commands, err := commandProviderWithRuntime(
		context.Background(), full, io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}
	err = commands[string(config.CommandSync)].Run(context.Background(), cli.Invocation{Command: cli.CommandSync})
	if err == nil || !strings.Contains(err.Error(), config.DatabaseScopesEnvironmentVariable) {
		t.Fatalf("sync error = %v, want missing optional scope", err)
	}
	if constructions != 0 {
		t.Fatalf("constructions before scope failure = %d, want 0", constructions)
	}
}

func TestQueryAskApplicationCompositionIsLazyAndPrivacyBounded(t *testing.T) {
	const (
		questionMarker = "private-question-marker"
		providerMarker = "private-provider-body-marker"
		databaseURL    = "postgres://private-user:private-password@private-host/private-database"
	)
	tests := []struct {
		name          string
		question      string
		modelResponse func(*testing.T) (queryplan.ModelResponse, error)
		open          func(context.Context, string) (queryDatabase, error)
		wantProvider  int
		wantDatabase  int
		wantError     bool
	}{
		{
			name:     "valid execution",
			question: questionMarker,
			modelResponse: func(t *testing.T) (queryplan.ModelResponse, error) {
				return executableTrendPlanResponse(t), nil
			},
			open: func(context.Context, string) (queryDatabase, error) {
				return &recordingQueryDatabase{snapshot: postgres.TemporalQuerySnapshot{Entities: []postgres.TemporalEntityRecord{{EntityID: "entity-a", Known: true}}}}, nil
			},
			wantProvider: 1, wantDatabase: 1,
		},
		{
			name:     "provider failure writes no output",
			question: questionMarker,
			modelResponse: func(*testing.T) (queryplan.ModelResponse, error) {
				return queryplan.ModelResponse{}, errors.New(providerMarker)
			},
			open: func(context.Context, string) (queryDatabase, error) {
				t.Fatal("database opened after provider failure")
				return nil, nil
			},
			wantProvider: 1, wantError: true,
		},
		{
			name:     "database failure writes no output",
			question: questionMarker,
			modelResponse: func(t *testing.T) (queryplan.ModelResponse, error) {
				return executableTrendPlanResponse(t), nil
			},
			open:         func(context.Context, string) (queryDatabase, error) { return nil, errors.New(databaseURL) },
			wantProvider: 1, wantDatabase: 1, wantError: true,
		},
		{
			name:     "invalid proposal opens no database",
			question: questionMarker,
			modelResponse: func(*testing.T) (queryplan.ModelResponse, error) {
				return queryplan.ModelResponse{Output: []byte(`{`), Provider: modelpolicy.ProviderOpenAI, ModelID: "synthetic-model", PromptVersion: queryplan.PromptVersion, SchemaName: queryplan.SchemaName, Attempts: 1}, nil
			},
			open: func(context.Context, string) (queryDatabase, error) {
				t.Fatal("database opened after invalid proposal")
				return nil, nil
			},
			wantProvider: 1, wantError: true,
		},
		{
			name:     "cannot plan opens no database",
			question: questionMarker,
			modelResponse: func(*testing.T) (queryplan.ModelResponse, error) {
				return queryplan.ModelResponse{Output: []byte(`{"status":"cannot-plan","reason":"ambiguous-question","intent":"","entity_match":"","predicates":[],"selections":[],"knowledge_scope":{"kind":"","as_of":""},"chronology_limit":0}`), Provider: modelpolicy.ProviderOpenAI, ModelID: "synthetic-model", PromptVersion: queryplan.PromptVersion, SchemaName: queryplan.SchemaName, Attempts: 1}, nil
			},
			open: func(context.Context, string) (queryDatabase, error) {
				t.Fatal("database opened after cannot-plan proposal")
				return nil, nil
			},
			wantProvider: 1, wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var providerCalls, databaseCalls, unrelatedCalls int
			var stdout, stderr strings.Builder
			runtime := commandRuntime{
				openQueryDatabase: func(ctx context.Context, url string) (queryDatabase, error) {
					databaseCalls++
					return test.open(ctx, url)
				},
				newQueryPlannerModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (queryplan.Model, error) {
					providerCalls++
					return queryPlanModelFunc(func(context.Context, queryplan.ModelRequest) (queryplan.ModelResponse, error) {
						return test.modelResponse(t)
					}), nil
				},
				newSource: func(context.Context, config.ApplicationSettings) (source.Source, error) {
					unrelatedCalls++
					return emptyRuntimeSource{}, nil
				},
				newDirectoryLookup: func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
					unrelatedCalls++
					return &recordingRuntimeDirectoryLookup{}, nil
				},
				openCanonicalRepositories: func(context.Context, string, bool) (canonicalRepositories, error) {
					unrelatedCalls++
					return canonicalRepositories{}, nil
				},
				newModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (extract.Model, error) {
					unrelatedCalls++
					return noOpRuntimeModel{}, nil
				},
			}
			err := executeRootQueryAsk(t, validQueryAskSettings(), runtime, strings.NewReader(test.question), &stdout, &stderr)
			if (err != nil) != test.wantError {
				t.Fatalf("app.Execute() error = %v, want error %t", err, test.wantError)
			}
			if providerCalls != test.wantProvider || databaseCalls != test.wantDatabase || unrelatedCalls != 0 {
				t.Fatalf("constructions = provider:%d database:%d unrelated:%d, want %d/%d/0", providerCalls, databaseCalls, unrelatedCalls, test.wantProvider, test.wantDatabase)
			}
			if test.wantError && stdout.Len() != 0 {
				t.Fatalf("failure stdout = %q, want empty", stdout.String())
			}
			for _, marker := range []string{questionMarker, providerMarker, databaseURL} {
				if strings.Contains(stderr.String(), marker) || (err != nil && strings.Contains(err.Error(), marker)) {
					t.Fatalf("private marker %q leaked through error or stderr", marker)
				}
			}
		})
	}
}

func TestQueryAskApplicationRejectsLocalDisclosureBeforeProviderConstruction(t *testing.T) {
	var providerCalls, databaseCalls int
	runtime := commandRuntime{
		openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
			databaseCalls++
			return &recordingQueryDatabase{}, nil
		},
		newQueryPlannerModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (queryplan.Model, error) {
			providerCalls++
			return nil, errors.New("unexpected")
		},
	}
	var stdout, stderr strings.Builder
	err := executeRootQueryAsk(t, validQueryAskSettings(), runtime, strings.NewReader("entity-a private disclosure"), &stdout, &stderr)
	if err == nil {
		t.Fatal("app.Execute() error = nil, want local disclosure rejection")
	}
	if providerCalls != 0 || databaseCalls != 0 || stdout.Len() != 0 {
		t.Fatalf("local disclosure handling = provider:%d database:%d stdout:%d, want 0/0/0", providerCalls, databaseCalls, stdout.Len())
	}
}

func TestQueryAskApplicationRestrictedDisclosureFailsClosedBeforePlannerConstruction(t *testing.T) {
	tests := []struct {
		name     string
		provider modelpolicy.Provider
		probe    doctor.DisclosureProbe
	}{
		{name: "openai", provider: modelpolicy.ProviderOpenAI},
		{name: "anthropic", provider: modelpolicy.ProviderAnthropic},
		{name: "bedrock logging enabled", provider: modelpolicy.ProviderBedrock, probe: rootDisclosureProbe{state: doctor.InvocationLoggingEnabled}},
		{name: "bedrock logging unknown", provider: modelpolicy.ProviderBedrock, probe: rootDisclosureProbe{state: doctor.InvocationLoggingUnknown}},
		{name: "bedrock access denied", provider: modelpolicy.ProviderBedrock, probe: rootDisclosureProbe{err: errors.New("private request ID marker")}},
		{name: "bedrock timeout", provider: modelpolicy.ProviderBedrock, probe: rootDisclosureProbe{err: context.DeadlineExceeded}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := validQueryAskSettings()
			settings.Application.Model.Provider = test.provider
			settings.Application.Model.DataMode = modelpolicy.DataModeRestricted
			settings.Application.Model.AWSRegion = "us-east-1"
			if test.provider == modelpolicy.ProviderAnthropic {
				settings.Application.Model.AnthropicAPIKey = "synthetic-anthropic-key"
			}
			var providerCalls, databaseCalls, probeCalls int
			runtime := commandRuntime{
				openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
					databaseCalls++
					return &recordingQueryDatabase{}, nil
				},
				newQueryPlannerModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (queryplan.Model, error) {
					providerCalls++
					return nil, errors.New("unexpected")
				},
				newDoctorProviderProbe: func(config.ModelSettings) (doctor.ModelProbe, doctor.DisclosureProbe, error) {
					probeCalls++
					return nil, test.probe, nil
				},
			}
			var stdout, stderr strings.Builder
			err := executeRootQueryAsk(t, settings, runtime, strings.NewReader("private question"), &stdout, &stderr)
			if err == nil {
				t.Fatal("app.Execute() error = nil, want restricted disclosure rejection")
			}
			if providerCalls != 0 || databaseCalls != 0 || stdout.Len() != 0 {
				t.Fatalf("restricted constructions = provider:%d database:%d stdout:%d, want 0/0/0", providerCalls, databaseCalls, stdout.Len())
			}
			if test.provider == modelpolicy.ProviderBedrock && probeCalls != 1 {
				t.Fatalf("Bedrock disclosure probes = %d, want 1", probeCalls)
			}
			if test.provider != modelpolicy.ProviderBedrock && probeCalls != 0 {
				t.Fatalf("direct-provider disclosure probes = %d, want 0", probeCalls)
			}
			if strings.Contains(stderr.String(), "private request ID marker") || strings.Contains(err.Error(), "private request ID marker") {
				t.Fatal("restricted disclosure error leaked private probe detail")
			}
		})
	}
}

func TestTypedQueryApplicationConstructsNoPlanner(t *testing.T) {
	database := &recordingQueryDatabase{snapshot: postgres.TemporalQuerySnapshot{Entities: []postgres.TemporalEntityRecord{{EntityID: "entity-a", Known: true}}}}
	var plannerCalls int
	runtime := commandRuntime{
		openQueryDatabase: func(context.Context, string) (queryDatabase, error) { return database, nil },
		newQueryPlannerModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (queryplan.Model, error) {
			plannerCalls++
			return nil, errors.New("typed query must not construct planner")
		},
	}
	settings := validQueryCommandSettings()
	var stdout, stderr strings.Builder
	err := app.Execute(
		context.Background(),
		[]string{"query", "trend", "--entity", "entity-a", "--before", "2024-01-01T00:00:00Z/2024-02-01T00:00:00Z", "--after", "2024-03-01T00:00:00Z/2024-04-01T00:00:00Z"},
		app.SettingsLoaderFunc(func(config.LoadOptions) (config.Settings, error) { return settings, nil }),
		app.BootstrapFunc(func(context.Context, config.Settings) (app.ExecutionDependencies, error) {
			return app.ExecutionDependencies{
				CommandProvider: app.CommandProviderFunc(func(ctx context.Context, settings config.Settings, stdin io.Reader, output, errors io.Writer) (map[string]cli.Command, error) {
					return commandProviderWithRuntime(ctx, settings, output, errors, tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime, stdin)
				}),
				Shutdown: func(context.Context) error { return nil },
			}, nil
		}),
		strings.NewReader("private typed question must remain unread"), &stdout, &stderr,
	)
	if err != nil {
		t.Fatalf("app.Execute() error = %v", err)
	}
	if plannerCalls != 0 || database.loads != 1 || database.closes != 1 {
		t.Fatalf("typed lifecycle = planner:%d load:%d close:%d, want 0/1/1", plannerCalls, database.loads, database.closes)
	}
}

func TestQueryAskApplicationRestrictedBedrockOrdersDisclosurePlannerAndDatabase(t *testing.T) {
	settings := validQueryAskSettings()
	settings.Application.Model.Provider = modelpolicy.ProviderBedrock
	settings.Application.Model.DataMode = modelpolicy.DataModeRestricted
	settings.Application.Model.AWSRegion = "us-east-1"
	calls := []string{}
	database := &orderedQueryDatabase{
		calls:    &calls,
		snapshot: postgres.TemporalQuerySnapshot{Entities: []postgres.TemporalEntityRecord{{EntityID: "entity-a", Known: true}}},
	}
	runtime := commandRuntime{
		newDoctorProviderProbe: func(config.ModelSettings) (doctor.ModelProbe, doctor.DisclosureProbe, error) {
			return nil, orderedDisclosureProbe{calls: &calls}, nil
		},
		newQueryPlannerModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (queryplan.Model, error) {
			calls = append(calls, "planner.factory")
			return queryPlanModelFunc(func(context.Context, queryplan.ModelRequest) (queryplan.ModelResponse, error) {
				calls = append(calls, "model.Plan")
				response := executableTrendPlanResponse(t)
				response.Provider = modelpolicy.ProviderBedrock
				return response, nil
			}), nil
		},
		openQueryDatabase: func(context.Context, string) (queryDatabase, error) {
			calls = append(calls, "database.Open")
			return database, nil
		},
	}
	var stdout, stderr strings.Builder
	if err := executeRootQueryAsk(t, settings, runtime, strings.NewReader("private question"), &stdout, &stderr); err != nil {
		t.Fatalf("app.Execute() error = %v", err)
	}
	want := []string{"disclosure.preflight", "planner.factory", "model.Plan", "database.Open", "database.Read", "database.Close"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("restricted Bedrock lifecycle = %v, want %v", calls, want)
	}
}

func TestQueryAskApplicationRedactsDistinctPrivateMarkers(t *testing.T) {
	const (
		canonicalIDMarker = "entity-canonical-marker"
		predicateMarker   = "predicate-private-marker"
		timestampMarker   = "2026-07-29T16:00:00Z-private-marker"
		citationMarker    = "citation-private-marker"
		rawOutputMarker   = "raw-output-private-marker"
		providerBody      = "provider-body-private-marker"
		requestID         = "request-id-private-marker"
		databaseURL       = "postgres://private-user:private-password@private-host/private-database"
		credential        = "credential-private-marker"
		question          = "question-private-marker"
		prompt            = "prompt-private-marker"
	)
	markers := []string{canonicalIDMarker, predicateMarker, timestampMarker, citationMarker, rawOutputMarker, providerBody, requestID, databaseURL, credential, question, prompt}
	assertRedacted := func(t *testing.T, err error, stdout, stderr *strings.Builder) {
		t.Helper()
		if err == nil || stdout.Len() != 0 {
			t.Fatalf("failure result = error:%v stdout:%q, want bounded error and zero stdout", err, stdout.String())
		}
		for _, marker := range markers {
			if strings.Contains(err.Error(), marker) || strings.Contains(stderr.String(), marker) {
				t.Fatalf("private marker %q leaked through error or stderr", marker)
			}
		}
	}
	t.Run("canonical ID rejected locally", func(t *testing.T) {
		var stdout, stderr strings.Builder
		runtime := commandRuntime{}
		err := executeRootQueryAskWithEntity(t, validQueryAskSettings(), runtime, canonicalIDMarker, strings.NewReader(question+" "+canonicalIDMarker), &stdout, &stderr)
		assertRedacted(t, err, &stdout, &stderr)
	})
	t.Run("provider error and raw proposal are bounded", func(t *testing.T) {
		var stdout, stderr strings.Builder
		runtime := commandRuntime{
			newQueryPlannerModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (queryplan.Model, error) {
				return queryPlanModelFunc(func(context.Context, queryplan.ModelRequest) (queryplan.ModelResponse, error) {
					return queryplan.ModelResponse{}, errors.New(strings.Join([]string{providerBody, requestID, credential, prompt}, " "))
				}), nil
			},
		}
		err := executeRootQueryAsk(t, validQueryAskSettings(), runtime, strings.NewReader(question), &stdout, &stderr)
		assertRedacted(t, err, &stdout, &stderr)

		stdout.Reset()
		stderr.Reset()
		runtime.newQueryPlannerModel = func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (queryplan.Model, error) {
			return queryPlanModelFunc(func(context.Context, queryplan.ModelRequest) (queryplan.ModelResponse, error) {
				return queryplan.ModelResponse{Output: []byte(`{"raw":"` + predicateMarker + " " + timestampMarker + " " + citationMarker + " " + rawOutputMarker + `"}`), Provider: modelpolicy.ProviderOpenAI, ModelID: "synthetic-model", PromptVersion: queryplan.PromptVersion, SchemaName: queryplan.SchemaName, Attempts: 1}, nil
			}), nil
		}
		err = executeRootQueryAsk(t, validQueryAskSettings(), runtime, strings.NewReader(question), &stdout, &stderr)
		assertRedacted(t, err, &stdout, &stderr)
	})
	t.Run("database URL is bounded", func(t *testing.T) {
		var stdout, stderr strings.Builder
		runtime := commandRuntime{
			newQueryPlannerModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (queryplan.Model, error) {
				return queryPlanModelFunc(func(context.Context, queryplan.ModelRequest) (queryplan.ModelResponse, error) {
					return executableTrendPlanResponse(t), nil
				}), nil
			},
			openQueryDatabase: func(context.Context, string) (queryDatabase, error) { return nil, errors.New(databaseURL) },
		}
		err := executeRootQueryAsk(t, validQueryAskSettings(), runtime, strings.NewReader(question), &stdout, &stderr)
		assertRedacted(t, err, &stdout, &stderr)
	})
}

type orderedDisclosureProbe struct{ calls *[]string }

func (probe orderedDisclosureProbe) InvocationLogging(context.Context) (doctor.InvocationLoggingState, error) {
	*probe.calls = append(*probe.calls, "disclosure.preflight")
	return doctor.InvocationLoggingDisabled, nil
}

type orderedQueryDatabase struct {
	calls    *[]string
	snapshot postgres.TemporalQuerySnapshot
}

func (database *orderedQueryDatabase) LoadTemporalQuerySnapshot(context.Context, postgres.TemporalQuerySelection, postgres.TemporalSnapshotObserver) (postgres.TemporalQuerySnapshot, error) {
	*database.calls = append(*database.calls, "database.Read")
	return database.snapshot, nil
}

func (database *orderedQueryDatabase) Close() {
	*database.calls = append(*database.calls, "database.Close")
}

type rootDisclosureProbe struct {
	state doctor.InvocationLoggingState
	err   error
}

func (probe rootDisclosureProbe) InvocationLogging(context.Context) (doctor.InvocationLoggingState, error) {
	return probe.state, probe.err
}

func executeRootQueryAsk(t *testing.T, settings config.Settings, runtime commandRuntime, input io.Reader, stdout, stderr io.Writer) error {
	return executeRootQueryAskWithEntity(t, settings, runtime, "entity-a", input, stdout, stderr)
}

func executeRootQueryAskWithEntity(t *testing.T, settings config.Settings, runtime commandRuntime, entityID string, input io.Reader, stdout, stderr io.Writer) error {
	t.Helper()
	return app.Execute(
		context.Background(),
		[]string{"query", "ask", "--entity", entityID, "--reference-time", "2026-07-29T16:00:00Z", "--output", "json"},
		app.SettingsLoaderFunc(func(config.LoadOptions) (config.Settings, error) { return settings, nil }),
		app.BootstrapFunc(func(context.Context, config.Settings) (app.ExecutionDependencies, error) {
			return app.ExecutionDependencies{
				CommandProvider: app.CommandProviderFunc(func(ctx context.Context, settings config.Settings, stdin io.Reader, output, errors io.Writer) (map[string]cli.Command, error) {
					return commandProviderWithRuntime(ctx, settings, output, errors, tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime, stdin)
				}),
				Shutdown: func(context.Context) error { return nil },
			}, nil
		}),
		input, stdout, stderr,
	)
}
