package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"

	"stacks/internal/cli"
	"stacks/internal/config"
	"stacks/internal/directory"
	"stacks/internal/doctor"
	"stacks/internal/entity"
	"stacks/internal/extract"
	"stacks/internal/ingest"
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
	"stacks/internal/observability"
	"stacks/internal/source"
)

func TestExecutionDependenciesComposeRuntimeCommandsTelemetryAndShutdown(t *testing.T) {
	decisionRecorder, err := observability.NewDecisionRecorder(noop.NewMeterProvider().Meter("synthetic"))
	if err != nil {
		t.Fatalf("create decision recorder: %v", err)
	}
	tracerProvider := &recordingExecutionTracerProvider{
		TracerProvider: tracenoop.NewTracerProvider(),
	}
	meterProvider := &recordingExecutionMeterProvider{
		MeterProvider: noop.NewMeterProvider(),
	}
	runtime := &recordingExecutionObservability{
		logger:         zap.NewNop(),
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
		decisions:      decisionRecorder,
	}

	originalNewRuntime := newObservabilityRuntime
	originalCommandProvider := composeCommands
	t.Cleanup(func() {
		newObservabilityRuntime = originalNewRuntime
		composeCommands = originalCommandProvider
	})

	bootstrapSettings := config.Settings{
		LogLevel:  "error",
		Telemetry: config.TelemetrySettings{Enabled: false},
	}
	var runtimeSettings config.Settings
	newObservabilityRuntime = func(_ context.Context, settings config.Settings) (executionObservability, error) {
		runtimeSettings = settings
		return runtime, nil
	}

	var (
		commandProviderCalls int
		commandSettings      config.Settings
		commandTracer        trace.Tracer
		commandDecisions     *observability.DecisionRecorder
		commandInvocations   modeltelemetry.Recorder
	)
	composeCommands = func(
		_ context.Context,
		settings config.Settings,
		_, _ io.Writer,
		tracer trace.Tracer,
		decisions *observability.DecisionRecorder,
		invocations modeltelemetry.Recorder,
	) (map[string]cli.Command, error) {
		commandProviderCalls++
		commandSettings = settings
		commandTracer = tracer
		commandDecisions = decisions
		commandInvocations = invocations
		return map[string]cli.Command{}, nil
	}

	dependencies, err := newExecutionDependencies(t.Context(), bootstrapSettings)
	if err != nil {
		t.Fatalf("newExecutionDependencies() error = %v", err)
	}
	if dependencies.Runtime == nil {
		t.Fatal("runtime dependency = nil")
	}
	if dependencies.CommandProvider == nil {
		t.Fatal("command provider dependency = nil")
	}
	if dependencies.Shutdown == nil {
		t.Fatal("shutdown dependency = nil")
	}
	if !reflect.DeepEqual(runtimeSettings, bootstrapSettings) {
		t.Fatalf("observability settings = %#v, want %#v", runtimeSettings, bootstrapSettings)
	}
	if commandProviderCalls != 0 || runtime.decisionRecorderCalls != 0 ||
		len(tracerProvider.names) != 0 || len(meterProvider.names) != 0 {
		t.Fatalf(
			"eager command telemetry construction = provider:%d decisions:%d tracers:%v meters:%v",
			commandProviderCalls,
			runtime.decisionRecorderCalls,
			tracerProvider.names,
			meterProvider.names,
		)
	}

	validatedSettings := config.Settings{
		LogLevel: "warn",
		Database: config.DatabaseSettings{
			URL:             "postgres://synthetic-app",
			MigrationURL:    "postgres://synthetic-admin",
			Scopes:          []config.DatabaseScope{config.DatabaseScopeCore},
			ApplicationRole: "synthetic_role",
		},
	}
	if _, err := dependencies.CommandProvider.Commands(
		t.Context(),
		validatedSettings,
		io.Discard,
		io.Discard,
	); err != nil {
		t.Fatalf("Commands() error = %v", err)
	}
	if commandProviderCalls != 1 {
		t.Fatalf("command provider calls = %d, want 1", commandProviderCalls)
	}
	if !reflect.DeepEqual(commandSettings, validatedSettings) {
		t.Fatalf("command settings = %#v, want %#v", commandSettings, validatedSettings)
	}
	if runtime.decisionRecorderCalls != 1 || commandDecisions != decisionRecorder {
		t.Fatalf(
			"decision recorder calls/value = %d/%p, want 1/%p",
			runtime.decisionRecorderCalls,
			commandDecisions,
			decisionRecorder,
		)
	}
	if strings.Join(tracerProvider.names, ",") != "stacks" || commandTracer == nil {
		t.Fatalf("command tracer names/value = %v/%v, want stacks/non-nil", tracerProvider.names, commandTracer)
	}
	if strings.Join(meterProvider.names, ",") != "stacks" || commandInvocations == nil {
		t.Fatalf("command meter names/recorder = %v/%v, want stacks/non-nil", meterProvider.names, commandInvocations)
	}

	shutdownContext := t.Context()
	if err := dependencies.Shutdown(shutdownContext); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if runtime.shutdownCalls != 1 || runtime.shutdownContext != shutdownContext {
		t.Fatalf(
			"runtime shutdown calls/context = %d/%v, want 1/%v",
			runtime.shutdownCalls,
			runtime.shutdownContext,
			shutdownContext,
		)
	}
}

type recordingExecutionObservability struct {
	logger                *zap.Logger
	tracerProvider        trace.TracerProvider
	meterProvider         metric.MeterProvider
	decisions             *observability.DecisionRecorder
	decisionRecorderCalls int
	shutdownCalls         int
	shutdownContext       context.Context
}

func (runtime *recordingExecutionObservability) Logger() *zap.Logger {
	return runtime.logger
}

func (runtime *recordingExecutionObservability) TracerProvider() trace.TracerProvider {
	return runtime.tracerProvider
}

func (runtime *recordingExecutionObservability) MeterProvider() metric.MeterProvider {
	return runtime.meterProvider
}

func (runtime *recordingExecutionObservability) DecisionRecorder() (*observability.DecisionRecorder, error) {
	runtime.decisionRecorderCalls++
	return runtime.decisions, nil
}

func (runtime *recordingExecutionObservability) Shutdown(ctx context.Context) error {
	runtime.shutdownCalls++
	runtime.shutdownContext = ctx
	return nil
}

type recordingExecutionTracerProvider struct {
	trace.TracerProvider
	names []string
}

func (provider *recordingExecutionTracerProvider) Tracer(
	name string,
	options ...trace.TracerOption,
) trace.Tracer {
	provider.names = append(provider.names, name)
	return provider.TracerProvider.Tracer(name, options...)
}

type recordingExecutionMeterProvider struct {
	metric.MeterProvider
	names []string
}

func (provider *recordingExecutionMeterProvider) Meter(
	name string,
	options ...metric.MeterOption,
) metric.Meter {
	provider.names = append(provider.names, name)
	return provider.MeterProvider.Meter(name, options...)
}

func TestAWSLoadOptionsUseDefaultCredentialChainWhenProfileIsAbsent(t *testing.T) {
	options := awsLoadOptions("", "us-east-1")
	loaded := awsconfig.LoadOptions{}
	for _, option := range options {
		if err := option(&loaded); err != nil {
			t.Fatalf("apply AWS load option: %v", err)
		}
	}
	if loaded.Region != "us-east-1" || loaded.SharedConfigProfile != "" {
		t.Fatalf("AWS load options = %#v, want region plus default credential chain", loaded)
	}
}

func TestRestrictedSyncChecksDisclosureBeforeExternalConstruction(t *testing.T) {
	settings := validCommandApplicationSettings(config.CommandSync, modelpolicy.ProviderBedrock, modelpolicy.DataModeRestricted)
	calls := []string{}
	runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingDisabled)

	commands, err := commandProviderWithRuntime(
		context.Background(), commandSettings(settings), io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}
	err = commands[string(config.CommandSync)].Run(context.Background(), cli.Invocation{Command: cli.CommandSync})
	if !errors.Is(err, errStopAfterModelConstruction) {
		t.Fatalf("sync error = %v, want sentinel model-construction stop", err)
	}
	want := []string{"logging", "google", "postgres", "model", "close"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestFailedRestrictedGateConstructsNoSourceStorageOrModel(t *testing.T) {
	for _, command := range []config.Command{config.CommandSync} {
		t.Run(string(command), func(t *testing.T) {
			settings := validCommandApplicationSettings(command, modelpolicy.ProviderBedrock, modelpolicy.DataModeRestricted)
			if command == config.CommandSync {
				enableRuntimeDirectory(&settings)
			}
			calls := []string{}
			runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingEnabled)
			runtime.newDirectoryLookup = func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
				calls = append(calls, "directory")
				return &recordingRuntimeDirectoryLookup{}, nil
			}
			commands, err := commandProviderWithRuntime(
				context.Background(), commandSettings(settings), io.Discard, io.Discard,
				tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
			)
			if err != nil {
				t.Fatalf("commandProviderWithRuntime() error = %v", err)
			}
			err = commands[string(command)].Run(context.Background(), cli.Invocation{Command: cli.CommandName(command)})
			if !errors.Is(err, doctor.ErrDisclosureNotConfirmed) {
				t.Fatalf("%s error = %v, want disclosure rejection", command, err)
			}
			if strings.Join(calls, ",") != "logging" {
				t.Fatalf("calls = %v, want only disclosure inspection", calls)
			}
		})
	}
}

func TestCanceledRestrictedSyncGateConstructsNoDriveDirectoryStorageOrModel(t *testing.T) {
	settings := validCommandApplicationSettings(config.CommandSync, modelpolicy.ProviderBedrock, modelpolicy.DataModeRestricted)
	enableRuntimeDirectory(&settings)
	calls := []string{}
	runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingDisabled)
	runtime.newDoctorProviderProbe = func(config.ModelSettings) (doctor.ModelProbe, doctor.DisclosureProbe, error) {
		return nil, cancelingDisclosureProbe{calls: &calls}, nil
	}
	runtime.newDirectoryLookup = func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
		calls = append(calls, "directory")
		return &recordingRuntimeDirectoryLookup{}, nil
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
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = commands[string(config.CommandSync)].Run(ctx, cli.Invocation{Command: cli.CommandSync})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sync error = %v, want context.Canceled", err)
	}
	if strings.Join(calls, ",") != "logging" {
		t.Fatalf("calls = %v, want only disclosure inspection", calls)
	}
}

func TestFailedRestrictedReviewGateConstructsNoDirectoryOrStorage(t *testing.T) {
	settings := validCommandApplicationSettings(config.CommandReview, modelpolicy.ProviderBedrock, modelpolicy.DataModeRestricted)
	enableRuntimeDirectory(&settings)
	calls := []string{}
	runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingEnabled)
	runtime.newDirectoryLookup = func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
		calls = append(calls, "directory")
		return &recordingRuntimeDirectoryLookup{}, nil
	}
	runtime.openCanonicalRepositories = func(context.Context, string, bool) (canonicalRepositories, error) {
		calls = append(calls, "postgres")
		return canonicalRepositories{}, nil
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

	err = commands[string(config.CommandReview)].Run(context.Background(), cli.Invocation{Command: cli.CommandReview, Action: cli.ActionList})

	if !errors.Is(err, doctor.ErrDisclosureNotConfirmed) {
		t.Fatalf("review error = %v, want disclosure rejection", err)
	}
	if strings.Join(calls, ",") != "logging" {
		t.Fatalf("calls = %v, want only disclosure inspection", calls)
	}
}

func TestRestrictedDirectProvidersRejectBeforeExternalConstruction(t *testing.T) {
	for _, command := range []config.Command{config.CommandSync} {
		for _, provider := range []modelpolicy.Provider{modelpolicy.ProviderOpenAI, modelpolicy.ProviderAnthropic} {
			t.Run(string(command)+"/"+string(provider), func(t *testing.T) {
				settings := validCommandApplicationSettings(command, provider, modelpolicy.DataModeRestricted)
				calls := []string{}
				runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingDisabled)
				commands, err := commandProviderWithRuntime(
					context.Background(), commandSettings(settings), io.Discard, io.Discard,
					tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
				)
				if err != nil {
					t.Fatalf("commandProviderWithRuntime() error = %v", err)
				}
				if err := commands[string(command)].Run(context.Background(), cli.Invocation{Command: cli.CommandName(command)}); err == nil {
					t.Fatalf("%s error = nil, want static policy rejection", command)
				}
				if len(calls) != 0 {
					t.Fatalf("calls = %v, want no external construction", calls)
				}
			})
		}
	}
}

func TestPersonalSyncPerformsNoDisclosureInspection(t *testing.T) {
	settings := validCommandApplicationSettings(config.CommandSync, modelpolicy.ProviderOpenAI, modelpolicy.DataModePersonal)
	calls := []string{}
	runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingEnabled)
	commands, err := commandProviderWithRuntime(
		context.Background(), commandSettings(settings), io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
	)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}
	err = commands[string(config.CommandSync)].Run(context.Background(), cli.Invocation{Command: cli.CommandSync})
	if !errors.Is(err, errStopAfterModelConstruction) {
		t.Fatalf("sync error = %v, want sentinel model-construction stop", err)
	}
	want := []string{"google", "postgres", "model", "close"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want no disclosure inspection and then %v", calls, want)
	}
}

func TestAuthCommandConstructsOnlySelectedAuthorizer(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		argument      string
		wantDrive     int
		wantDirectory int
	}{
		{name: "Drive", argument: "google", wantDrive: 1},
		{name: "directory", argument: "google-directory", wantDirectory: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			settings := config.Settings{Application: validCommandApplicationSettings(config.CommandAuth, modelpolicy.ProviderBedrock, modelpolicy.DataModePersonal)}
			settings.Application.Directory.OAuthClientFile = "/synthetic/directory-client.json"
			settings.Application.Directory.OAuthTokenFile = "/synthetic/directory-token.json"
			calls := struct{ drive, directory int }{}
			runtime := commandRuntime{
				newDriveAuthorizer: func(string, string, io.Writer) cli.GoogleAuthorizer {
					calls.drive++
					return recordingGoogleAuthorizer{}
				},
				newDirectoryAuthorizer: func(string, string, io.Writer) cli.GoogleAuthorizer {
					calls.directory++
					return recordingGoogleAuthorizer{}
				},
			}
			commands, err := commandProviderWithRuntime(context.Background(), settings, io.Discard, io.Discard, tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime)
			if err != nil {
				t.Fatalf("commandProviderWithRuntime() error = %v", err)
			}
			if err := commands[string(config.CommandAuth)].Run(context.Background(), cli.Invocation{Command: cli.CommandAuth, Action: cli.Action(testCase.argument)}); err != nil {
				t.Fatalf("auth %s error = %v", testCase.argument, err)
			}
			if calls.drive != testCase.wantDrive || calls.directory != testCase.wantDirectory {
				t.Fatalf("authorizer construction = drive:%d directory:%d, want drive:%d directory:%d", calls.drive, calls.directory, testCase.wantDrive, testCase.wantDirectory)
			}
		})
	}
}

func TestAuthCommandRejectsInvalidTargetBeforeConstructingAuthorizers(t *testing.T) {
	calls := 0
	runtime := commandRuntime{
		newDriveAuthorizer:     func(string, string, io.Writer) cli.GoogleAuthorizer { calls++; return recordingGoogleAuthorizer{} },
		newDirectoryAuthorizer: func(string, string, io.Writer) cli.GoogleAuthorizer { calls++; return recordingGoogleAuthorizer{} },
	}
	commands, err := commandProviderWithRuntime(context.Background(), config.Settings{}, io.Discard, io.Discard, tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime)
	if err != nil {
		t.Fatalf("commandProviderWithRuntime() error = %v", err)
	}
	err = commands[string(config.CommandAuth)].Run(context.Background(), cli.Invocation{Command: cli.CommandAuth})
	if err == nil || !strings.Contains(err.Error(), "invocation is invalid") {
		t.Fatalf("auth error = %v, want invalid invocation error", err)
	}
	if calls != 0 {
		t.Fatalf("authorizer constructors = %d, want 0", calls)
	}
}

func TestDisabledSyncAndReviewDoNotConstructDirectoryBoundaries(t *testing.T) {
	settings := validCommandApplicationSettings(config.CommandSync, modelpolicy.ProviderOpenAI, modelpolicy.DataModePersonal)
	var directoryLookupCalls, directoryProbeCalls int
	var syncRepositoryCalls, reviewRepositoryCalls int
	var directoryRepositoryConstructions int
	var closes int
	runtime := commandRuntime{
		newDoctorDirectory: func(config.GoogleDirectorySettings) doctor.DirectoryProbe {
			directoryProbeCalls++
			return &readyDirectoryProbe{}
		},
		newDirectoryLookup: func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
			directoryLookupCalls++
			return &recordingRuntimeDirectoryLookup{}, nil
		},
		newSource: func(context.Context, config.ApplicationSettings) (source.Source, error) {
			return emptyRuntimeSource{}, nil
		},
		openCanonicalRepositories: func(_ context.Context, _ string, includeDirectory bool) (canonicalRepositories, error) {
			syncRepositoryCalls++
			if includeDirectory {
				directoryRepositoryConstructions++
				return canonicalRepositories{
					ingestion: noOpRuntimeIngestionRepository{},
					directory: noOpRuntimeDirectoryRepository{},
					close:     func() { closes++ },
				}, nil
			}
			return canonicalRepositories{
				ingestion: noOpRuntimeIngestionRepository{},
				close:     func() { closes++ },
			}, nil
		},
		newModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (extract.Model, error) {
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
		t.Fatalf("disabled sync error = %v", err)
	}
	if err := commands[string(config.CommandReview)].Run(context.Background(), cli.Invocation{Command: cli.CommandReview, Action: cli.ActionList}); err == nil {
		t.Fatal("review list error = nil, want nil repository sentinel")
	}

	if directoryLookupCalls != 0 || directoryProbeCalls != 0 {
		t.Fatalf("directory constructors = lookup:%d probe:%d, want 0/0", directoryLookupCalls, directoryProbeCalls)
	}
	if directoryRepositoryConstructions != 0 {
		t.Fatalf("directory repository constructions = %d, want 0", directoryRepositoryConstructions)
	}
	reviewRepositoryCalls = syncRepositoryCalls - 1
	if syncRepositoryCalls != 2 || reviewRepositoryCalls != 1 || closes != 2 {
		t.Fatalf("repositories/closes = total:%d review:%d closes:%d, want 2/1/2", syncRepositoryCalls, reviewRepositoryCalls, closes)
	}
}

func TestEnabledSyncConstructsOneDirectoryBoundaryAndOneSharedRepositoryOwner(t *testing.T) {
	settings := validCommandApplicationSettings(config.CommandSync, modelpolicy.ProviderOpenAI, modelpolicy.DataModePersonal)
	enableRuntimeDirectory(&settings)
	var calls []string
	lookup := &recordingRuntimeDirectoryLookup{}
	runtime := commandRuntime{
		newDirectoryLookup: func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
			calls = append(calls, "directory")
			return lookup, nil
		},
		newSource: func(context.Context, config.ApplicationSettings) (source.Source, error) {
			calls = append(calls, "drive")
			return emptyRuntimeSource{}, nil
		},
		openCanonicalRepositories: func(_ context.Context, _ string, includeDirectory bool) (canonicalRepositories, error) {
			if !includeDirectory {
				t.Fatal("enabled sync requested no directory repository")
			}
			calls = append(calls, "postgres")
			return canonicalRepositories{
				ingestion: noOpRuntimeIngestionRepository{},
				directory: noOpRuntimeDirectoryRepository{},
				close: func() {
					calls = append(calls, "close")
				},
			}, nil
		},
		newModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (extract.Model, error) {
			calls = append(calls, "model")
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
		t.Fatalf("enabled sync error = %v", err)
	}
	want := []string{"drive", "directory", "postgres", "model", "close"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if lookup.calls != 0 {
		t.Fatalf("directory searches = %d, want no bulk search for an empty workset", lookup.calls)
	}
}

func TestEnabledSyncContinuesWhenDirectoryConstructionIsUnavailable(t *testing.T) {
	settings := validCommandApplicationSettings(config.CommandSync, modelpolicy.ProviderOpenAI, modelpolicy.DataModePersonal)
	enableRuntimeDirectory(&settings)
	const privateDirectoryFailure = "private directory construction failure 7f31"
	var calls []string
	runtime := commandRuntime{
		newDirectoryLookup: func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
			calls = append(calls, "directory")
			return nil, errors.New(privateDirectoryFailure)
		},
		newSource: func(context.Context, config.ApplicationSettings) (source.Source, error) {
			calls = append(calls, "drive")
			return emptyRuntimeSource{}, nil
		},
		openCanonicalRepositories: func(_ context.Context, _ string, includeDirectory bool) (canonicalRepositories, error) {
			if !includeDirectory {
				t.Fatal("enabled sync requested no directory repository")
			}
			calls = append(calls, "postgres")
			return canonicalRepositories{
				ingestion: noOpRuntimeIngestionRepository{},
				directory: noOpRuntimeDirectoryRepository{},
				close: func() {
					calls = append(calls, "close")
				},
			}, nil
		},
		newModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (extract.Model, error) {
			calls = append(calls, "model")
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
		t.Fatalf("enabled sync error = %v, want unavailable directory to remain additive", err)
	}
	want := []string{"drive", "directory", "postgres", "model", "close"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want fail-soft composition %v", calls, want)
	}
}

func TestEnabledReviewUsageContinuesWhenDirectoryConstructionIsUnavailable(t *testing.T) {
	settings := validCommandApplicationSettings(config.CommandReview, modelpolicy.ProviderOpenAI, modelpolicy.DataModePersonal)
	enableRuntimeDirectory(&settings)
	const privateDirectoryFailure = "private directory construction failure 92bc"
	var calls []string
	runtime := commandRuntime{
		newDirectoryLookup: func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
			calls = append(calls, "directory")
			return nil, errors.New(privateDirectoryFailure)
		},
		openCanonicalRepositories: func(_ context.Context, _ string, includeDirectory bool) (canonicalRepositories, error) {
			if !includeDirectory {
				t.Fatal("enabled review requested no directory repository")
			}
			calls = append(calls, "postgres")
			return canonicalRepositories{
				directory: noOpRuntimeDirectoryRepository{},
				close: func() {
					calls = append(calls, "close")
				},
			}, nil
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

	err = commands[string(config.CommandReview)].Run(context.Background(), cli.Invocation{Command: cli.CommandReview})
	if err == nil || !strings.Contains(err.Error(), "invocation is invalid") {
		t.Fatalf("review error = %v, want invalid invocation result", err)
	}
	if strings.Contains(err.Error(), privateDirectoryFailure) {
		t.Fatalf("review error exposed directory construction detail: %v", err)
	}
	want := []string{"directory", "postgres", "close"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want fail-soft composition %v", calls, want)
	}
}

func TestDirectoryConstructionCancellationRemainsCanonical(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{name: "canceled", err: fmt.Errorf("directory wrapper: %w", context.Canceled)},
		{name: "deadline exceeded", err: fmt.Errorf("directory wrapper: %w", context.DeadlineExceeded)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			settings := validCommandApplicationSettings(config.CommandSync, modelpolicy.ProviderOpenAI, modelpolicy.DataModePersonal)
			enableRuntimeDirectory(&settings)
			var repositoryCalls, modelCalls int
			runtime := commandRuntime{
				newDirectoryLookup: func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
					return nil, testCase.err
				},
				newSource: func(context.Context, config.ApplicationSettings) (source.Source, error) {
					return emptyRuntimeSource{}, nil
				},
				openCanonicalRepositories: func(_ context.Context, _ string, includeDirectory bool) (canonicalRepositories, error) {
					if !includeDirectory {
						t.Fatal("enabled sync requested no directory repository")
					}
					repositoryCalls++
					return canonicalRepositories{
						ingestion: noOpRuntimeIngestionRepository{},
						directory: noOpRuntimeDirectoryRepository{},
					}, nil
				},
				newModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (extract.Model, error) {
					modelCalls++
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

			err = commands[string(config.CommandSync)].Run(context.Background(), cli.Invocation{Command: cli.CommandSync})
			if err != errors.Unwrap(testCase.err) {
				t.Fatalf("sync error = %v, want canonical %v", err, errors.Unwrap(testCase.err))
			}
			if repositoryCalls != 0 || modelCalls != 0 {
				t.Fatalf("post-cancellation constructions = repositories:%d model:%d, want 0/0", repositoryCalls, modelCalls)
			}
		})
	}
}

func TestReviewDirectoryConstructionCancellationRemainsCanonical(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{name: "canceled", err: fmt.Errorf("directory wrapper: %w", context.Canceled)},
		{name: "deadline exceeded", err: fmt.Errorf("directory wrapper: %w", context.DeadlineExceeded)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			settings := validCommandApplicationSettings(config.CommandReview, modelpolicy.ProviderOpenAI, modelpolicy.DataModePersonal)
			enableRuntimeDirectory(&settings)
			var repositoryCalls int
			runtime := commandRuntime{
				newDirectoryLookup: func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
					return nil, testCase.err
				},
				openCanonicalRepositories: func(context.Context, string, bool) (canonicalRepositories, error) {
					repositoryCalls++
					return canonicalRepositories{
						directory: noOpRuntimeDirectoryRepository{},
					}, nil
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

			err = commands[string(config.CommandReview)].Run(context.Background(), cli.Invocation{Command: cli.CommandReview})
			if err != errors.Unwrap(testCase.err) {
				t.Fatalf("review error = %v, want canonical %v", err, errors.Unwrap(testCase.err))
			}
			if repositoryCalls != 0 {
				t.Fatalf("post-cancellation repository constructions = %d, want 0", repositoryCalls)
			}
		})
	}
}

func TestRestrictedSyncConstructsDirectoryOnlyAfterDisclosureGate(t *testing.T) {
	settings := validCommandApplicationSettings(config.CommandSync, modelpolicy.ProviderBedrock, modelpolicy.DataModeRestricted)
	enableRuntimeDirectory(&settings)
	calls := []string{}
	runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingDisabled)
	runtime.newDirectoryLookup = func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
		calls = append(calls, "directory")
		return nil, errors.New("synthetic bounded directory construction failure")
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
	if err := commands[string(config.CommandSync)].Run(context.Background(), cli.Invocation{Command: cli.CommandSync}); !errors.Is(err, errStopAfterModelConstruction) {
		t.Fatalf("sync error = %v, want sentinel model-construction stop after fail-soft directory construction", err)
	}
	want := []string{"logging", "google", "directory", "postgres", "model", "close"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want disclosure before Drive and directory construction %v", calls, want)
	}
}

func TestDoctorConstructsOptionalProbeOnlyWhenEnabledAndNeverConstructsRuntimeOrSearch(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			settings := validCommandApplicationSettings(config.CommandDoctor, modelpolicy.ProviderOpenAI, modelpolicy.DataModePersonal)
			if enabled {
				enableRuntimeDirectory(&settings)
			}
			var probeConstructions, modelRuntimeConstructions, lookupConstructions int
			probe := &readyDirectoryProbe{}
			runtime := commandRuntime{
				openDoctorDatabase: func(context.Context, string) (doctorDatabase, error) {
					return &readyDoctorDatabase{}, nil
				},
				newDoctorGoogle: func(config.ApplicationSettings) doctor.Google { return readyDoctorGoogle{} },
				newDoctorDirectory: func(config.GoogleDirectorySettings) doctor.DirectoryProbe {
					probeConstructions++
					return probe
				},
				newDoctorProviderProbe: func(config.ModelSettings) (doctor.ModelProbe, doctor.DisclosureProbe, error) {
					return readyDoctorModelProbe{}, nil, nil
				},
				newDirectoryLookup: func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error) {
					lookupConstructions++
					return &recordingRuntimeDirectoryLookup{}, nil
				},
				newModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (extract.Model, error) {
					modelRuntimeConstructions++
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

			if err := commands[string(config.CommandDoctor)].Run(context.Background(), cli.Invocation{Command: cli.CommandDoctor}); err != nil {
				t.Fatalf("doctor error = %v", err)
			}
			wantProbeConstructions := 0
			wantProbeChecks := 0
			if enabled {
				wantProbeConstructions = 1
				wantProbeChecks = 1
			}
			if probeConstructions != wantProbeConstructions ||
				probe.calls != wantProbeChecks ||
				modelRuntimeConstructions != 0 ||
				lookupConstructions != 0 {
				t.Fatalf(
					"doctor constructions/checks = probe:%d/%d model:%d lookup:%d, want %d/%d/0/0",
					probeConstructions,
					probe.calls,
					modelRuntimeConstructions,
					lookupConstructions,
					wantProbeConstructions,
					wantProbeChecks,
				)
			}
		})
	}
}

func TestDoctorOpensAndClosesOneCanonicalDatabaseOwner(t *testing.T) {
	settings := validCommandApplicationSettings(config.CommandDoctor, modelpolicy.ProviderOpenAI, modelpolicy.DataModePersonal)
	database := &readyDoctorDatabase{}
	var opens int
	runtime := commandRuntime{
		openDoctorDatabase: func(context.Context, string) (doctorDatabase, error) {
			opens++
			return database, nil
		},
		newDoctorGoogle: func(config.ApplicationSettings) doctor.Google { return readyDoctorGoogle{} },
		newDoctorProviderProbe: func(config.ModelSettings) (doctor.ModelProbe, doctor.DisclosureProbe, error) {
			return readyDoctorModelProbe{}, nil, nil
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

	if err := commands[string(config.CommandDoctor)].Run(context.Background(), cli.Invocation{Command: cli.CommandDoctor}); err != nil {
		t.Fatalf("doctor error = %v", err)
	}
	if opens != 1 || database.pings != 1 || database.statuses != 1 || database.closes != 1 {
		t.Fatalf(
			"canonical database calls = open:%d ping:%d status:%d close:%d, want 1/1/1/1",
			opens,
			database.pings,
			database.statuses,
			database.closes,
		)
	}
}

func TestComposedDirectoryServiceTelemetryContainsOnlyBoundedFields(t *testing.T) {
	const (
		sentinelName    = "Private Name Marker 9b41"
		sentinelEmail   = "private.marker@corp.example"
		sentinelSubject = "people/private-subject-9b41"
		sentinelToken   = "private-token-9b41"
		sentinelQuery   = "private query marker 9b41"
	)
	settings := config.GoogleDirectorySettings{
		Enabled:      true,
		EmailDomains: []string{"corp.example"},
		Freshness:    24 * time.Hour,
		RetryAfter:   time.Minute,
		MaxAttempts:  1,
	}
	repository := &runtimeDirectoryRepository{
		work: directory.Workset{Mentions: []directory.PendingMention{{
			MentionID:      "private-mention-9b41",
			ProposalID:     "private-proposal-9b41",
			Surface:        sentinelQuery,
			NormalizedName: sentinelName,
			ProposedEmail:  sentinelEmail,
			EmailQuote:     sentinelEmail,
		}}},
	}
	lookup := &recordingRuntimeDirectoryLookup{
		result: directory.LookupResult{Profiles: []entity.DirectoryProfile{{
			Provider:    "google_people",
			SubjectID:   sentinelSubject,
			Source:      entity.DirectorySourceDomainProfile,
			DisplayName: sentinelName,
			Emails:      []entity.DirectoryEmail{{Value: sentinelEmail, Primary: true}},
		}}},
		err: errors.New(sentinelToken),
	}
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	decisions := &runtimeDirectoryDecisions{}
	service, err := newDirectoryService(
		settings,
		lookup,
		repository,
		provider.Tracer("runtime-directory-test"),
		decisions,
		func() time.Time { return time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC) },
	)
	if err != nil {
		t.Fatalf("newDirectoryService() error = %v", err)
	}

	if _, err := service.Enrich(context.Background(), "private-derivation-9b41"); err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}
	if len(decisions.observations) != 1 {
		t.Fatalf("decision count = %d, want 1", len(decisions.observations))
	}
	observation := decisions.observations[0]
	if observation.Name != "directory_lookup" ||
		observation.Outcome != string(entity.DirectoryUnavailable) ||
		observation.InputSize != 1 ||
		observation.OutputSize != 0 {
		t.Fatalf("decision = %#v, want bounded directory outcome/counts", observation)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "stacks.directory.enrich" {
		t.Fatalf("spans = %#v, want one bounded directory span", spans)
	}
	telemetry := fmt.Sprintf("%#v %#v", decisions.observations, spans)
	for _, privateMarker := range []string{
		sentinelName,
		sentinelEmail,
		sentinelSubject,
		sentinelToken,
		sentinelQuery,
	} {
		if strings.Contains(telemetry, privateMarker) {
			t.Fatalf("telemetry contains private marker %q", privateMarker)
		}
	}
}

func TestComposedDirectoryServiceIsTheSharedEnrichmentAndReviewerBoundary(t *testing.T) {
	settings := config.GoogleDirectorySettings{
		Enabled:      true,
		EmailDomains: []string{"corp.example"},
		Freshness:    24 * time.Hour,
		RetryAfter:   time.Minute,
		MaxAttempts:  1,
	}
	directoryService, err := newDirectoryService(
		settings,
		&recordingRuntimeDirectoryLookup{},
		noOpRuntimeDirectoryRepository{},
		tracenoop.NewTracerProvider().Tracer("synthetic"),
		nil,
		time.Now,
	)
	if err != nil {
		t.Fatalf("newDirectoryService() error = %v", err)
	}
	ingestionService := newIngestionService(
		validCommandApplicationSettings(config.CommandSync, modelpolicy.ProviderOpenAI, modelpolicy.DataModePersonal),
		emptyRuntimeSource{},
		noOpRuntimeModel{},
		noOpRuntimeIngestionRepository{},
		directoryService,
		tracenoop.NewTracerProvider().Tracer("synthetic"),
		nil,
		time.Now,
	)
	var reviewerVerifier cli.ReviewerEmailVerifier = directoryService

	if ingestionService.IdentityEnricher != directoryService {
		t.Fatal("ingestion did not receive the composed directory service instance")
	}
	if reviewerVerifier != directoryService {
		t.Fatal("review did not receive the composed directory service instance")
	}
}

func TestPaddedDirectProviderSettingsRejectBeforeAnyBoundary(t *testing.T) {
	tests := []struct {
		name      string
		provider  modelpolicy.Provider
		configure func(*config.ModelSettings)
		wantName  string
		private   string
	}{
		{
			name: "OpenAI API key", provider: modelpolicy.ProviderOpenAI,
			configure: func(model *config.ModelSettings) { model.OpenAIAPIKey = " private-openai-key " },
			wantName:  config.OpenAIAPIKeyEnvironmentVariable, private: "private-openai-key",
		},
		{
			name: "Anthropic API key", provider: modelpolicy.ProviderAnthropic,
			configure: func(model *config.ModelSettings) { model.AnthropicAPIKey = " private-anthropic-key " },
			wantName:  config.AnthropicAPIKeyEnvironmentVariable, private: "private-anthropic-key",
		},
		{
			name: "OpenAI model ID", provider: modelpolicy.ProviderOpenAI,
			configure: func(model *config.ModelSettings) { model.ModelID = " padded-openai-model " },
			wantName:  config.ModelIDEnvironmentVariable, private: "padded-openai-model",
		},
		{
			name: "Anthropic model ID", provider: modelpolicy.ProviderAnthropic,
			configure: func(model *config.ModelSettings) { model.ModelID = " padded-anthropic-model " },
			wantName:  config.ModelIDEnvironmentVariable, private: "padded-anthropic-model",
		},
	}

	for _, command := range []config.Command{config.CommandSync} {
		for _, testCase := range tests {
			t.Run(string(command)+"/"+testCase.name, func(t *testing.T) {
				settings := validCommandApplicationSettings(command, testCase.provider, modelpolicy.DataModePersonal)
				testCase.configure(&settings.Model)
				calls := []string{}
				runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingEnabled)
				commands, err := commandProviderWithRuntime(
					context.Background(), commandSettings(settings), io.Discard, io.Discard,
					tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
				)
				if err != nil {
					t.Fatalf("commandProviderWithRuntime() error = %v", err)
				}

				err = commands[string(command)].Run(context.Background(), cli.Invocation{Command: cli.CommandName(command)})
				if err == nil || !strings.Contains(err.Error(), testCase.wantName) {
					t.Fatalf("%s error = %v, want bounded %s rejection", command, err, testCase.wantName)
				}
				if strings.Contains(err.Error(), testCase.private) {
					t.Fatalf("%s error exposed configured value: %v", command, err)
				}
				if len(calls) != 0 {
					t.Fatalf("calls = %v, want no disclosure, source, repository, or model construction", calls)
				}
			})
		}
	}
}

var errStopAfterModelConstruction = errors.New("stop after model construction")

type recordingGoogleAuthorizer struct{}

func (recordingGoogleAuthorizer) Authorize(context.Context) error { return nil }

type recordingDisclosureProbe struct {
	calls *[]string
	state doctor.InvocationLoggingState
}

type cancelingDisclosureProbe struct {
	calls *[]string
}

func (probe cancelingDisclosureProbe) InvocationLogging(context.Context) (doctor.InvocationLoggingState, error) {
	*probe.calls = append(*probe.calls, "logging")
	return doctor.InvocationLoggingUnknown, context.Canceled
}

func (probe recordingDisclosureProbe) InvocationLogging(context.Context) (doctor.InvocationLoggingState, error) {
	*probe.calls = append(*probe.calls, "logging")
	return probe.state, nil
}

type readyDirectoryProbe struct {
	calls int
}

func (probe *readyDirectoryProbe) CheckAuthorization(context.Context) error {
	probe.calls++
	return nil
}

type readyDoctorDatabase struct {
	pings    int
	statuses int
	closes   int
}

func (database *readyDoctorDatabase) Ping(context.Context) error {
	database.pings++
	return nil
}

func (database *readyDoctorDatabase) InspectMigrationStatus(
	context.Context,
	[]migration.Manifest,
	[]migration.Scope,
) ([]migration.ScopeStatus, error) {
	database.statuses++
	return []migration.ScopeStatus{
		{Scope: "core", State: migration.StateCurrent, Configured: true},
		{Scope: "directory", State: migration.StateAbsent, Configured: false},
	}, nil
}

func (database *readyDoctorDatabase) Close() { database.closes++ }

type readyDoctorGoogle struct{}

func (readyDoctorGoogle) CheckAuthorization(context.Context) error { return nil }

func (readyDoctorGoogle) CheckFolder(context.Context) error { return nil }

func (readyDoctorGoogle) GetRepresentative(context.Context) (source.Document, bool, error) {
	return source.Document{ID: "synthetic-document"}, true, nil
}

func (readyDoctorGoogle) GetDocument(context.Context, string) (source.Document, error) {
	return source.Document{Tabs: []source.Tab{{Role: source.TabRoleTranscript}}}, nil
}

type readyDoctorModelProbe struct{}

func (readyDoctorModelProbe) CheckCredentials(context.Context) error { return nil }

func (readyDoctorModelProbe) CheckModel(context.Context) error { return nil }

type emptyRuntimeSource struct{}

func (emptyRuntimeSource) List(context.Context, string) ([]source.Document, error) { return nil, nil }

func (emptyRuntimeSource) Get(context.Context, string) (source.Document, error) {
	return source.Document{}, errors.New("unexpected source get")
}

type noOpRuntimeModel struct{}

func (noOpRuntimeModel) Generate(context.Context, extract.Request) (extract.Response, error) {
	return extract.Response{}, nil
}

type noOpRuntimeIngestionRepository struct{}

func (noOpRuntimeIngestionRepository) PrepareVersion(
	context.Context,
	evidence.DocumentVersion,
	ingest.SourceRevisionMetadata,
	ingest.DerivationIdentity,
	modelpolicy.DataMode,
	time.Duration,
) (ingest.VersionState, error) {
	recordedAt := time.Date(2026, time.July, 25, 12, 0, 0, 123456000, time.UTC)
	return ingest.VersionState{DocumentRecordedAt: recordedAt}, nil
}

func (noOpRuntimeIngestionRepository) CompleteVersion(context.Context, ingest.Completion) error {
	return nil
}

func (noOpRuntimeIngestionRepository) RecordFailure(
	context.Context,
	ingest.Failure,
) error {
	return nil
}

func (noOpRuntimeIngestionRepository) EntitySnapshots(context.Context) ([]entity.EntitySnapshot, error) {
	return nil, nil
}

type noOpRuntimeDirectoryRepository struct{}

func (noOpRuntimeDirectoryRepository) LoadWork(
	context.Context,
	string,
	time.Time,
	time.Duration,
	time.Duration,
) (directory.Workset, error) {
	return directory.Workset{}, nil
}

func (noOpRuntimeDirectoryRepository) LoadIdentityState(context.Context) (directory.IdentityState, error) {
	return directory.IdentityState{}, nil
}

func (noOpRuntimeDirectoryRepository) Persist(
	context.Context,
	directory.PersistInput,
) (directory.PersistResult, error) {
	return directory.PersistResult{}, nil
}

type recordingRuntimeDirectoryLookup struct {
	result  directory.LookupResult
	err     error
	calls   int
	queries []entity.DirectoryQuery
}

func (lookup *recordingRuntimeDirectoryLookup) Search(
	_ context.Context,
	query entity.DirectoryQuery,
) (directory.LookupResult, error) {
	lookup.calls++
	lookup.queries = append(lookup.queries, query)
	return lookup.result, lookup.err
}

type runtimeDirectoryRepository struct {
	work      directory.Workset
	persisted []directory.PersistInput
}

func (repository *runtimeDirectoryRepository) LoadWork(
	context.Context,
	string,
	time.Time,
	time.Duration,
	time.Duration,
) (directory.Workset, error) {
	return repository.work, nil
}

func (repository *runtimeDirectoryRepository) LoadIdentityState(context.Context) (directory.IdentityState, error) {
	return directory.IdentityState{}, nil
}

func (repository *runtimeDirectoryRepository) Persist(
	_ context.Context,
	input directory.PersistInput,
) (directory.PersistResult, error) {
	repository.persisted = append(repository.persisted, input)
	return directory.PersistResult{}, nil
}

type runtimeDirectoryDecisions struct {
	observations []observability.DecisionObservation
}

func (decisions *runtimeDirectoryDecisions) Record(
	_ context.Context,
	observation observability.DecisionObservation,
) error {
	decisions.observations = append(decisions.observations, observation)
	return nil
}

func boundaryOrderRuntime(calls *[]string, state doctor.InvocationLoggingState) commandRuntime {
	return commandRuntime{
		newDoctorProviderProbe: func(config.ModelSettings) (doctor.ModelProbe, doctor.DisclosureProbe, error) {
			return nil, recordingDisclosureProbe{calls: calls, state: state}, nil
		},
		newSource: func(context.Context, config.ApplicationSettings) (source.Source, error) {
			*calls = append(*calls, "google")
			return nil, nil
		},
		openCanonicalRepositories: func(context.Context, string, bool) (canonicalRepositories, error) {
			*calls = append(*calls, "postgres")
			return canonicalRepositories{
				close: func() { *calls = append(*calls, "close") },
			}, nil
		},
		newModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (extract.Model, error) {
			*calls = append(*calls, "model")
			return nil, errStopAfterModelConstruction
		},
	}
}

func enableRuntimeDirectory(settings *config.ApplicationSettings) {
	settings.Directory = config.GoogleDirectorySettings{
		Enabled:         true,
		OAuthClientFile: "/synthetic/directory-client.json",
		OAuthTokenFile:  "/synthetic/directory-token.json",
		EmailDomains:    []string{"corp.example"},
		Freshness:       24 * time.Hour,
		RetryAfter:      time.Minute,
		MaxAttempts:     1,
	}
}

func validCommandApplicationSettings(command config.Command, provider modelpolicy.Provider, mode modelpolicy.DataMode) config.ApplicationSettings {
	settings := config.ApplicationSettings{
		GoogleFolderID:        "synthetic-folder",
		GoogleOAuthClientFile: "/synthetic/client.json", GoogleOAuthTokenFile: "/synthetic/token.json",
		TranscriptTitles: []string{"Transcript"}, NotesTitles: []string{"Notes"},
		Model: config.ModelSettings{
			Provider: provider, DataMode: mode, ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1,
			AWSRegion: "us-east-1", OpenAIAPIKey: "synthetic-openai-key", AnthropicAPIKey: "synthetic-anthropic-key",
		},
		IngestionLeaseDuration: 5 * time.Minute, IngestionAttemptTimeout: 4 * time.Minute,
		ExtractionPromptVersion: extract.ExtractionPromptVersion,
	}
	return settings
}

func commandSettings(application config.ApplicationSettings) config.Settings {
	scopes := []config.DatabaseScope{config.DatabaseScopeCore}
	if application.Directory.Enabled {
		scopes = append(scopes, config.DatabaseScopeDirectory)
	}
	return config.Settings{
		Application: application,
		Database: config.DatabaseSettings{
			URL: "postgres://synthetic", Scopes: scopes,
		},
	}
}

func TestValidateAWSConfigurationCredentialsReturnsBoundedAuthenticationFailure(t *testing.T) {
	const privateProviderDetail = "synthetic private credential-provider detail"
	configuration := aws.Config{Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{}, errors.New(privateProviderDetail)
	})}

	err := validateAWSConfigurationCredentials(context.Background(), configuration)
	if !errors.Is(err, extract.ErrAuthentication) {
		t.Fatalf("validateAWSConfigurationCredentials() error = %v, want bounded authentication failure", err)
	}
	if strings.Contains(err.Error(), privateProviderDetail) {
		t.Fatalf("validateAWSConfigurationCredentials() exposed provider detail: %v", err)
	}
}

func TestValidateAWSConfigurationCredentialsReturnsBoundedAuthorizationFailure(t *testing.T) {
	configuration := aws.Config{Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{}, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "synthetic private authorization detail"}
	})}

	err := validateAWSConfigurationCredentials(context.Background(), configuration)
	if !errors.Is(err, extract.ErrAuthorization) {
		t.Fatalf("validateAWSConfigurationCredentials() error = %v, want bounded authorization failure", err)
	}
}

func TestValidateAWSConfigurationCredentialsPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	configuration := aws.Config{Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{}, errors.New("synthetic canceled credential retrieval")
	})}

	err := validateAWSConfigurationCredentials(ctx, configuration)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("validateAWSConfigurationCredentials() error = %v, want context cancellation", err)
	}
}

func TestValidateAWSConfigurationCredentialsAcceptsRetrievedSigningKeys(t *testing.T) {
	configuration := aws.Config{Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "synthetic-access-key", SecretAccessKey: "synthetic-secret-key"}, nil
	})}

	if err := validateAWSConfigurationCredentials(context.Background(), configuration); err != nil {
		t.Fatalf("validateAWSConfigurationCredentials() error = %v", err)
	}
}

func TestCommandProviderRegistersDoctorSyncAndQueryWithoutConstructingLiveDependencies(t *testing.T) {
	recorder, err := observability.NewDecisionRecorder(noop.NewMeterProvider().Meter("synthetic"))
	if err != nil {
		t.Fatalf("create decision recorder: %v", err)
	}
	invocations, err := modeltelemetry.NewMetricsRecorder(noop.NewMeterProvider().Meter("synthetic"))
	if err != nil {
		t.Fatalf("create invocation recorder: %v", err)
	}
	commands, err := commandProvider(
		context.Background(), config.Settings{}, io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), recorder, invocations,
	)
	if err != nil {
		t.Fatalf("commandProvider() error = %v", err)
	}
	if commands[string(config.CommandSync)] == nil {
		t.Fatal("sync command is not registered")
	}
	if commands[string(config.CommandDoctor)] == nil {
		t.Fatal("doctor command is not registered")
	}
	if commands[string(config.CommandQuery)] == nil {
		t.Fatal("query command is not registered")
	}
}

func TestCommandServicesReceiveSelectedInvocationPolicy(t *testing.T) {
	tests := []struct {
		name     string
		provider modelpolicy.Provider
		dataMode modelpolicy.DataMode
		region   string
	}{
		{name: "bedrock personal", provider: modelpolicy.ProviderBedrock, dataMode: modelpolicy.DataModePersonal, region: "us-east-1"},
		{name: "bedrock restricted", provider: modelpolicy.ProviderBedrock, dataMode: modelpolicy.DataModeRestricted, region: "us-east-1"},
		{name: "openai", provider: modelpolicy.ProviderOpenAI, dataMode: modelpolicy.DataModePersonal},
		{name: "anthropic", provider: modelpolicy.ProviderAnthropic, dataMode: modelpolicy.DataModePersonal},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := config.ApplicationSettings{Model: config.ModelSettings{Provider: test.provider, DataMode: test.dataMode, AWSRegion: test.region}}
			t.Run("sync", func(t *testing.T) {
				ingestion := newIngestionService(settings, nil, nil, nil, nil, nil, nil, nil)
				if ingestion.Provider != test.provider || ingestion.DataMode != test.dataMode || ingestion.Region != test.region {
					t.Fatalf("ingestion invocation = %q/%q/%q, want %q/%q/%q", ingestion.Provider, ingestion.DataMode, ingestion.Region, test.provider, test.dataMode, test.region)
				}
			})
		})
	}
}
