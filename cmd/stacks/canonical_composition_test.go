package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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

	"stacks/internal/analysis"
	"stacks/internal/cli"
	"stacks/internal/config"
	"stacks/internal/directory"
	"stacks/internal/doctor"
	"stacks/internal/extract"
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
	"stacks/internal/query"
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

func TestAnalyzeUsesReadOnlyCanonicalQueryRepository(t *testing.T) {
	settings := validCommandApplicationSettings(
		config.CommandAnalyze,
		modelpolicy.ProviderOpenAI,
		modelpolicy.DataModePersonal,
	)
	repository := &recordingCanonicalAnalysisRepository{}
	var closes int
	runtime := commandRuntime{
		openCanonicalRepositories: func(context.Context, string, bool) (canonicalRepositories, error) {
			return canonicalRepositories{
				analysis: repository,
				close:    func() { closes++ },
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
	err = commands[string(config.CommandAnalyze)].Run(context.Background(), cli.Invocation{Command: cli.CommandAnalyze})
	if !errors.Is(err, analysis.ErrPairNotAccepted) {
		t.Fatalf("analyze error = %v, want canonical pair authority rejection", err)
	}
	if repository.calls != 1 || closes != 1 {
		t.Fatalf("canonical analysis calls/closes = %d/%d, want 1/1", repository.calls, closes)
	}
}

type recordingCanonicalAnalysisRepository struct {
	calls int
}

func (repository *recordingCanonicalAnalysisRepository) LoadPairInputs(
	context.Context,
	string,
	string,
) (analysis.PairSnapshot, error) {
	repository.calls++
	return analysis.PairSnapshot{Accepted: false}, nil
}
