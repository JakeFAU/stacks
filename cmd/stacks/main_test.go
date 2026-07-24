package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/analysis"
	"stacks/internal/cli"
	"stacks/internal/config"
	"stacks/internal/doctor"
	"stacks/internal/extract"
	"stacks/internal/ingest"
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
	"stacks/internal/observability"
	"stacks/internal/source"
)

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
	settings := validCommandPoCSettings(config.CommandSync, modelpolicy.ProviderBedrock, modelpolicy.DataModeRestricted)
	calls := []string{}
	runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingDisabled)

	commands, err := pocCommandProviderWithRuntime(
		context.Background(), config.Settings{PoC: settings}, io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
	)
	if err != nil {
		t.Fatalf("pocCommandProviderWithRuntime() error = %v", err)
	}
	err = commands[string(config.CommandSync)].Run(context.Background(), nil)
	if !errors.Is(err, errStopAfterModelConstruction) {
		t.Fatalf("sync error = %v, want sentinel model-construction stop", err)
	}
	want := []string{"logging", "google", "postgres", "model", "close"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestRestrictedAnalyzeChecksDisclosureBeforeExternalConstruction(t *testing.T) {
	settings := validCommandPoCSettings(config.CommandAnalyze, modelpolicy.ProviderBedrock, modelpolicy.DataModeRestricted)
	calls := []string{}
	runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingDisabled)

	commands, err := pocCommandProviderWithRuntime(
		context.Background(), config.Settings{PoC: settings}, io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
	)
	if err != nil {
		t.Fatalf("pocCommandProviderWithRuntime() error = %v", err)
	}
	err = commands[string(config.CommandAnalyze)].Run(context.Background(), nil)
	if !errors.Is(err, errStopAfterModelConstruction) {
		t.Fatalf("analyze error = %v, want sentinel model-construction stop", err)
	}
	want := []string{"logging", "postgres", "model", "close"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestFailedRestrictedGateConstructsNoSourceStorageOrModel(t *testing.T) {
	for _, command := range []config.Command{config.CommandSync, config.CommandAnalyze} {
		t.Run(string(command), func(t *testing.T) {
			settings := validCommandPoCSettings(command, modelpolicy.ProviderBedrock, modelpolicy.DataModeRestricted)
			calls := []string{}
			runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingEnabled)
			commands, err := pocCommandProviderWithRuntime(
				context.Background(), config.Settings{PoC: settings}, io.Discard, io.Discard,
				tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
			)
			if err != nil {
				t.Fatalf("pocCommandProviderWithRuntime() error = %v", err)
			}
			err = commands[string(command)].Run(context.Background(), nil)
			if !errors.Is(err, doctor.ErrDisclosureNotConfirmed) {
				t.Fatalf("%s error = %v, want disclosure rejection", command, err)
			}
			if strings.Join(calls, ",") != "logging" {
				t.Fatalf("calls = %v, want only disclosure inspection", calls)
			}
		})
	}
}

func TestRestrictedDirectProvidersRejectBeforeExternalConstruction(t *testing.T) {
	for _, command := range []config.Command{config.CommandSync, config.CommandAnalyze} {
		for _, provider := range []modelpolicy.Provider{modelpolicy.ProviderOpenAI, modelpolicy.ProviderAnthropic} {
			t.Run(string(command)+"/"+string(provider), func(t *testing.T) {
				settings := validCommandPoCSettings(command, provider, modelpolicy.DataModeRestricted)
				calls := []string{}
				runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingDisabled)
				commands, err := pocCommandProviderWithRuntime(
					context.Background(), config.Settings{PoC: settings}, io.Discard, io.Discard,
					tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
				)
				if err != nil {
					t.Fatalf("pocCommandProviderWithRuntime() error = %v", err)
				}
				if err := commands[string(command)].Run(context.Background(), nil); err == nil {
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
	settings := validCommandPoCSettings(config.CommandSync, modelpolicy.ProviderOpenAI, modelpolicy.DataModePersonal)
	calls := []string{}
	runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingEnabled)
	commands, err := pocCommandProviderWithRuntime(
		context.Background(), config.Settings{PoC: settings}, io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
	)
	if err != nil {
		t.Fatalf("pocCommandProviderWithRuntime() error = %v", err)
	}
	err = commands[string(config.CommandSync)].Run(context.Background(), nil)
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
			settings := config.Settings{PoC: validCommandPoCSettings(config.CommandAuth, modelpolicy.ProviderBedrock, modelpolicy.DataModePersonal)}
			settings.PoC.Directory.OAuthClientFile = "/synthetic/directory-client.json"
			settings.PoC.Directory.OAuthTokenFile = "/synthetic/directory-token.json"
			calls := struct{ drive, directory int }{}
			runtime := pocCommandRuntime{
				newDriveAuthorizer: func(string, string, io.Writer) cli.GoogleAuthorizer {
					calls.drive++
					return recordingGoogleAuthorizer{}
				},
				newDirectoryAuthorizer: func(string, string, io.Writer) cli.GoogleAuthorizer {
					calls.directory++
					return recordingGoogleAuthorizer{}
				},
			}
			commands, err := pocCommandProviderWithRuntime(context.Background(), settings, io.Discard, io.Discard, tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime)
			if err != nil {
				t.Fatalf("pocCommandProviderWithRuntime() error = %v", err)
			}
			if err := commands[string(config.CommandAuth)].Run(context.Background(), []string{testCase.argument}); err != nil {
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
	runtime := pocCommandRuntime{
		newDriveAuthorizer:     func(string, string, io.Writer) cli.GoogleAuthorizer { calls++; return recordingGoogleAuthorizer{} },
		newDirectoryAuthorizer: func(string, string, io.Writer) cli.GoogleAuthorizer { calls++; return recordingGoogleAuthorizer{} },
	}
	commands, err := pocCommandProviderWithRuntime(context.Background(), config.Settings{}, io.Discard, io.Discard, tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime)
	if err != nil {
		t.Fatalf("pocCommandProviderWithRuntime() error = %v", err)
	}
	err = commands[string(config.CommandAuth)].Run(context.Background(), []string{"invalid"})
	if err == nil || err.Error() != "usage: stacks auth google | stacks auth google-directory" {
		t.Fatalf("auth error = %v, want exact usage error", err)
	}
	if calls != 0 {
		t.Fatalf("authorizer constructors = %d, want 0", calls)
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

	for _, command := range []config.Command{config.CommandSync, config.CommandAnalyze} {
		for _, testCase := range tests {
			t.Run(string(command)+"/"+testCase.name, func(t *testing.T) {
				settings := validCommandPoCSettings(command, testCase.provider, modelpolicy.DataModePersonal)
				testCase.configure(&settings.Model)
				calls := []string{}
				runtime := boundaryOrderRuntime(&calls, doctor.InvocationLoggingEnabled)
				commands, err := pocCommandProviderWithRuntime(
					context.Background(), config.Settings{PoC: settings}, io.Discard, io.Discard,
					tracenoop.NewTracerProvider().Tracer("synthetic"), nil, nil, runtime,
				)
				if err != nil {
					t.Fatalf("pocCommandProviderWithRuntime() error = %v", err)
				}

				err = commands[string(command)].Run(context.Background(), nil)
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

func (probe recordingDisclosureProbe) InvocationLogging(context.Context) (doctor.InvocationLoggingState, error) {
	*probe.calls = append(*probe.calls, "logging")
	return probe.state, nil
}

func boundaryOrderRuntime(calls *[]string, state doctor.InvocationLoggingState) pocCommandRuntime {
	return pocCommandRuntime{
		newDoctorProviderProbe: func(config.ModelSettings) (doctor.ModelProbe, doctor.DisclosureProbe, error) {
			return nil, recordingDisclosureProbe{calls: calls, state: state}, nil
		},
		newSource: func(context.Context, config.PoCSettings) (source.Source, error) {
			*calls = append(*calls, "google")
			return nil, nil
		},
		openIngestionRepository: func(context.Context, string) (ingest.Repository, func(), error) {
			*calls = append(*calls, "postgres")
			return nil, func() { *calls = append(*calls, "close") }, nil
		},
		openAnalysisRepository: func(context.Context, string) (analysis.Repository, func(), error) {
			*calls = append(*calls, "postgres")
			return nil, func() { *calls = append(*calls, "close") }, nil
		},
		newModel: func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (extract.Model, error) {
			*calls = append(*calls, "model")
			return nil, errStopAfterModelConstruction
		},
	}
}

func validCommandPoCSettings(command config.Command, provider modelpolicy.Provider, mode modelpolicy.DataMode) config.PoCSettings {
	settings := config.PoCSettings{
		DatabaseURL: "postgres://synthetic", GoogleFolderID: "synthetic-folder",
		GoogleOAuthClientFile: "/synthetic/client.json", GoogleOAuthTokenFile: "/synthetic/token.json",
		TranscriptTitles: []string{"Transcript"}, NotesTitles: []string{"Notes"},
		Model: config.ModelSettings{
			Provider: provider, DataMode: mode, ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1,
			AWSRegion: "us-east-1", OpenAIAPIKey: "synthetic-openai-key", AnthropicAPIKey: "synthetic-anthropic-key",
		},
		IngestionLeaseDuration: 5 * time.Minute, IngestionAttemptTimeout: 4 * time.Minute,
		ExtractionPromptVersion: extract.ExtractionPromptVersion, AnalysisPromptVersion: extract.AnalysisPromptVersion,
	}
	if command == config.CommandAnalyze {
		settings.EmployeeEntityID = "employee-id"
		settings.ManagerEntityID = "manager-id"
	}
	return settings
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

func TestPoCCommandProviderRegistersDoctorSyncAndAnalyzeWithoutConstructingLiveDependencies(t *testing.T) {
	recorder, err := observability.NewDecisionRecorder(noop.NewMeterProvider().Meter("synthetic"))
	if err != nil {
		t.Fatalf("create decision recorder: %v", err)
	}
	invocations, err := modeltelemetry.NewMetricsRecorder(noop.NewMeterProvider().Meter("synthetic"))
	if err != nil {
		t.Fatalf("create invocation recorder: %v", err)
	}
	commands, err := pocCommandProvider(
		context.Background(), config.Settings{}, io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), recorder, invocations,
	)
	if err != nil {
		t.Fatalf("pocCommandProvider() error = %v", err)
	}
	if commands[string(config.CommandSync)] == nil {
		t.Fatal("sync command is not registered")
	}
	if commands[string(config.CommandDoctor)] == nil {
		t.Fatal("doctor command is not registered")
	}
	if commands[string(config.CommandAnalyze)] == nil {
		t.Fatal("analyze command is not registered")
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
			settings := config.PoCSettings{Model: config.ModelSettings{Provider: test.provider, DataMode: test.dataMode, AWSRegion: test.region}}
			t.Run("sync", func(t *testing.T) {
				ingestion := newIngestionService(settings, nil, nil, nil, nil, nil, nil)
				if ingestion.Provider != test.provider || ingestion.DataMode != test.dataMode || ingestion.Region != test.region {
					t.Fatalf("ingestion invocation = %q/%q/%q, want %q/%q/%q", ingestion.Provider, ingestion.DataMode, ingestion.Region, test.provider, test.dataMode, test.region)
				}
			})
			t.Run("analyze", func(t *testing.T) {
				pairAnalysis := newAnalysisService(settings, nil, nil, nil, nil, nil)
				if pairAnalysis.Provider != test.provider || pairAnalysis.DataMode != test.dataMode || pairAnalysis.Region != test.region {
					t.Fatalf("analysis invocation = %q/%q/%q, want %q/%q/%q", pairAnalysis.Provider, pairAnalysis.DataMode, pairAnalysis.Region, test.provider, test.dataMode, test.region)
				}
			})
		})
	}
}
