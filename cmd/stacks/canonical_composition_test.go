package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/analysis"
	"stacks/internal/cli"
	"stacks/internal/config"
	"stacks/internal/directory"
	"stacks/internal/extract"
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
	"stacks/internal/source"
)

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
