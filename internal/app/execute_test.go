package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/temporal"

	"stacks/internal/cli"
	"stacks/internal/config"
)

func TestExecuteHelpAndSyntaxFailuresDoNotConstructCommands(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		isHelp bool
	}{
		{name: "root help", args: []string{"--help"}, isHelp: true},
		{name: "nested help", args: []string{"review", "create", "--help"}, isHelp: true},
		{name: "unknown command", args: []string{"unknown"}},
		{name: "invalid arity", args: []string{"entities", "show"}},
		{name: "unknown flag", args: []string{"review", "create", "proposal-1", "--unknown"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			providerCalls := 0
			provider := CommandProviderFunc(func(context.Context, config.Settings, io.Writer, io.Writer) (map[string]cli.Command, error) {
				providerCalls++
				return nil, errors.New("provider must not be constructed")
			})
			err := executeWithSettings(t.Context(), testCase.args, config.Settings{},
				RuntimeFunc(func(context.Context, config.Settings) error {
					return errors.New("serve must not run")
				}),
				provider, io.Discard, io.Discard)
			if testCase.isHelp {
				if err != nil {
					t.Fatalf("Execute() help error = %v", err)
				}
			} else if err == nil {
				t.Fatal("Execute() error = nil, want syntax failure")
			}
			if providerCalls != 0 {
				t.Fatalf("provider calls = %d, want 0", providerCalls)
			}
		})
	}
}

func TestExecuteServesWithoutApplicationSettings(t *testing.T) {
	called := false
	runtime := RuntimeFunc(func(context.Context, config.Settings) error {
		called = true
		return nil
	})

	err := executeWithSettings(context.Background(), nil, config.Settings{}, runtime, nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !called {
		t.Fatal("serve runtime was not called")
	}
}

func TestExecuteRoutesEntityAndReviewCommandsWithRemainingArguments(t *testing.T) {
	settings := config.Settings{Database: config.DatabaseSettings{URL: "postgres://synthetic"}}
	cases := []struct {
		name           string
		arguments      []string
		commandName    string
		wantInvocation cli.Invocation
	}{
		{name: "entities", arguments: []string{"entities", "show", "person-1"}, commandName: "entities", wantInvocation: cli.Invocation{Command: cli.CommandEntities, Action: cli.ActionShow, Arguments: []string{"person-1"}}},
		{name: "review", arguments: []string{"review", "accept", "proposal-1", "person-1"}, commandName: "review", wantInvocation: cli.Invocation{Command: cli.CommandReview, Action: cli.ActionAccept, Arguments: []string{"proposal-1", "person-1"}}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var gotInvocation cli.Invocation
			var stdoutCalled bool
			provider := CommandProviderFunc(func(context.Context, config.Settings, io.Writer, io.Writer) (map[string]cli.Command, error) {
				return map[string]cli.Command{testCase.commandName: cli.CommandFunc(func(_ context.Context, invocation cli.Invocation) error {
					gotInvocation = invocation
					stdoutCalled = true
					return nil
				})}, nil
			})

			err := executeWithSettings(context.Background(), testCase.arguments, settings,
				RuntimeFunc(func(context.Context, config.Settings) error { return fmt.Errorf("serve should not run") }),
				provider, io.Discard, io.Discard)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !stdoutCalled || !reflect.DeepEqual(gotInvocation, testCase.wantInvocation) {
				t.Fatalf("command call = (%t, %#v), want (true, %#v)", stdoutCalled, gotInvocation, testCase.wantInvocation)
			}
		})
	}
}

func TestExecuteRoutesGoogleAuthWithRemainingArguments(t *testing.T) {
	settings := config.Settings{Application: config.ApplicationSettings{
		GoogleOAuthClientFile: "/synthetic/client.json",
		GoogleOAuthTokenFile:  "/synthetic/token.json",
	}}
	var gotInvocation cli.Invocation
	provider := CommandProviderFunc(func(context.Context, config.Settings, io.Writer, io.Writer) (map[string]cli.Command, error) {
		return map[string]cli.Command{"auth": cli.CommandFunc(func(_ context.Context, invocation cli.Invocation) error {
			gotInvocation = invocation
			return nil
		})}, nil
	})

	err := executeWithSettings(context.Background(), []string{"auth", "google"}, settings,
		RuntimeFunc(func(context.Context, config.Settings) error { return fmt.Errorf("serve should not run") }),
		provider, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotInvocation.Command != cli.CommandAuth || gotInvocation.Action != cli.ActionAuthGoogle || len(gotInvocation.Arguments) != 0 {
		t.Fatalf("auth command invocation = %#v, want typed Google auth", gotInvocation)
	}
}

func TestExecuteRoutesSyncThroughLazyCommandProvider(t *testing.T) {
	settings := config.Settings{Database: config.DatabaseSettings{URL: "postgres://synthetic"}, Application: config.ApplicationSettings{
		GoogleFolderID:        "synthetic-folder",
		GoogleOAuthClientFile: "/synthetic/client.json",
		GoogleOAuthTokenFile:  "/synthetic/token.json",
		TranscriptTitles:      []string{"Transcript"},
		NotesTitles:           []string{"Notes"},
		Model: config.ModelSettings{
			DataMode: "personal", Provider: "bedrock", ModelID: "synthetic-model",
			MaxOutputTokens: 256, MaxAttempts: 1, AWSProfile: "synthetic-profile", AWSRegion: "us-east-1",
		},
		IngestionLeaseDuration:  5 * time.Minute,
		IngestionAttemptTimeout: 4 * time.Minute,
		ExtractionPromptVersion: "extract-v2",
		ManagerConfidence:       config.ManagerConfidenceSettings{PromptVersion: "analyze-v1"},
	}}
	providerCalls := 0
	syncCalls := 0
	provider := CommandProviderFunc(func(context.Context, config.Settings, io.Writer, io.Writer) (map[string]cli.Command, error) {
		providerCalls++
		return map[string]cli.Command{"sync": cli.CommandFunc(func(_ context.Context, invocation cli.Invocation) error {
			syncCalls++
			if invocation.Command != cli.CommandSync || invocation.Action != "" || len(invocation.Arguments) != 0 {
				return fmt.Errorf("sync received unexpected invocation")
			}
			return nil
		})}, nil
	})

	err := executeWithSettings(context.Background(), []string{"sync"}, settings,
		RuntimeFunc(func(context.Context, config.Settings) error { return fmt.Errorf("serve should not run") }),
		provider, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if providerCalls != 1 || syncCalls != 1 {
		t.Fatalf("provider/sync calls = %d/%d, want 1/1", providerCalls, syncCalls)
	}
}

func TestExecuteRoutesDoctorThroughLazyCommandProvider(t *testing.T) {
	settings := config.Settings{Database: config.DatabaseSettings{URL: "postgres://synthetic"}, Application: config.ApplicationSettings{
		GoogleFolderID:        "synthetic-folder",
		GoogleOAuthClientFile: "/synthetic/client.json",
		GoogleOAuthTokenFile:  "/synthetic/token.json",
		TranscriptTitles:      []string{"Transcript"},
		NotesTitles:           []string{"Notes"},
		Model: config.ModelSettings{
			DataMode: "personal", Provider: "bedrock", ModelID: "synthetic-model",
			MaxOutputTokens: 256, MaxAttempts: 1, AWSRegion: "us-east-1",
		},
	}}
	providerCalls := 0
	doctorCalls := 0
	provider := CommandProviderFunc(func(context.Context, config.Settings, io.Writer, io.Writer) (map[string]cli.Command, error) {
		providerCalls++
		return map[string]cli.Command{"doctor": cli.CommandFunc(func(_ context.Context, invocation cli.Invocation) error {
			doctorCalls++
			if invocation.Command != cli.CommandDoctor || invocation.Action != "" || len(invocation.Arguments) != 0 {
				return fmt.Errorf("doctor received unexpected invocation")
			}
			return nil
		})}, nil
	})

	err := executeWithSettings(context.Background(), []string{"doctor"}, settings,
		RuntimeFunc(func(context.Context, config.Settings) error { return fmt.Errorf("serve should not run") }),
		provider, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if providerCalls != 1 || doctorCalls != 1 {
		t.Fatalf("provider/doctor calls = %d/%d, want 1/1", providerCalls, doctorCalls)
	}
}

func TestExecuteRoutesAnalyzeThroughLazyCommandProvider(t *testing.T) {
	settings := config.Settings{Database: config.DatabaseSettings{URL: "postgres://synthetic"}, Application: config.ApplicationSettings{
		Model: config.ModelSettings{
			DataMode: "personal", Provider: "bedrock", ModelID: "synthetic-model",
			MaxOutputTokens: 256, MaxAttempts: 1, AWSProfile: "synthetic-profile", AWSRegion: "us-east-1",
		},
		ExtractionPromptVersion: "extract-v2",
		ManagerConfidence: config.ManagerConfidenceSettings{
			PromptVersion: "analyze-v1", EmployeeEntityID: "employee-id", ManagerEntityID: "manager-id",
		},
	}}
	providerCalls := 0
	analyzeCalls := 0
	provider := CommandProviderFunc(func(context.Context, config.Settings, io.Writer, io.Writer) (map[string]cli.Command, error) {
		providerCalls++
		return map[string]cli.Command{"analyze": cli.CommandFunc(func(_ context.Context, invocation cli.Invocation) error {
			analyzeCalls++
			if invocation.Command != cli.CommandAnalyze || invocation.Action != "" || len(invocation.Arguments) != 0 {
				return fmt.Errorf("analyze received unexpected invocation")
			}
			return nil
		})}, nil
	})

	err := executeWithSettings(context.Background(), []string{"analyze"}, settings,
		RuntimeFunc(func(context.Context, config.Settings) error { return fmt.Errorf("serve should not run") }),
		provider, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if providerCalls != 1 || analyzeCalls != 1 {
		t.Fatalf("provider/analyze calls = %d/%d, want 1/1", providerCalls, analyzeCalls)
	}
}

func TestExecuteRoutesTypedTrendThroughLazyQueryCommand(t *testing.T) {
	settings := config.Settings{
		Database: config.DatabaseSettings{URL: "postgres://synthetic"},
		Query:    config.QuerySettings{MaxEntities: 16, MaxPredicates: 32, MaxChronology: 1000},
	}
	var got cli.Invocation
	providerCalls := 0
	provider := CommandProviderFunc(func(context.Context, config.Settings, io.Writer, io.Writer) (map[string]cli.Command, error) {
		providerCalls++
		return map[string]cli.Command{"query": cli.CommandFunc(func(_ context.Context, invocation cli.Invocation) error {
			got = invocation
			return nil
		})}, nil
	})

	err := executeWithSettings(t.Context(), []string{
		"query", "trend",
		"--entity", "entity-a",
		"--before", "2025-01-01T00:00:00Z/2025-02-01T00:00:00Z",
		"--after", "2025-03-01T00:00:00Z/2025-04-01T00:00:00Z",
	}, settings, RuntimeFunc(func(context.Context, config.Settings) error {
		return errors.New("serve must not run")
	}), provider, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if providerCalls != 1 || got.Command != cli.CommandQuery || got.Action != cli.ActionTrend ||
		got.Query == nil || got.Query.Request.Intent != temporal.IntentTrendComparison {
		t.Fatalf("provider calls/invocation = %d/%#v, want one typed trend dispatch", providerCalls, got)
	}
}

func TestExecuteRejectsSupersededPromptContractsBeforeConstructingBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		settings  config.ApplicationSettings
	}{
		{
			name: "sync legacy extraction", arguments: []string{"sync"},
			settings: validSyncSettingsForExecute("extract-v1", "analyze-v1"),
		},
		{
			name: "analyze legacy extraction", arguments: []string{"analyze"},
			settings: validAnalyzeSettingsForExecute("extract-v1", "analyze-v1"),
		},
		{
			name: "sync legacy analysis", arguments: []string{"sync"},
			settings: validSyncSettingsForExecute("extract-v2", "analyze-v0"),
		},
		{
			name: "analyze legacy analysis", arguments: []string{"analyze"},
			settings: validAnalyzeSettingsForExecute("extract-v2", "analyze-v0"),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			providerCalls := 0
			provider := CommandProviderFunc(func(context.Context, config.Settings, io.Writer, io.Writer) (map[string]cli.Command, error) {
				providerCalls++
				return nil, fmt.Errorf("provider must not be constructed")
			})

			err := executeWithSettings(context.Background(), testCase.arguments, config.Settings{
				Database:    config.DatabaseSettings{URL: "postgres://synthetic"},
				Application: testCase.settings,
			},
				RuntimeFunc(func(context.Context, config.Settings) error { return fmt.Errorf("serve should not run") }),
				provider, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "run stacks sync") {
				t.Fatalf("Execute() error = %v, want actionable prompt upgrade rejection", err)
			}
			if providerCalls != 0 {
				t.Fatalf("command provider calls = %d, want 0 before provider/database boundaries", providerCalls)
			}
		})
	}
}

func validSyncSettingsForExecute(extractionVersion, analysisVersion string) config.ApplicationSettings {
	return config.ApplicationSettings{
		GoogleFolderID:        "synthetic-folder",
		GoogleOAuthClientFile: "/synthetic/client.json", GoogleOAuthTokenFile: "/synthetic/token.json",
		TranscriptTitles: []string{"Transcript"}, NotesTitles: []string{"Notes"},
		Model: config.ModelSettings{
			DataMode: "personal", Provider: "bedrock", ModelID: "synthetic-model",
			MaxOutputTokens: 256, MaxAttempts: 1, AWSRegion: "us-east-1",
		},
		IngestionLeaseDuration: 5 * time.Minute, IngestionAttemptTimeout: 4 * time.Minute,
		ExtractionPromptVersion: extractionVersion,
		ManagerConfidence:       config.ManagerConfidenceSettings{PromptVersion: analysisVersion},
	}
}

func validAnalyzeSettingsForExecute(extractionVersion, analysisVersion string) config.ApplicationSettings {
	settings := validSyncSettingsForExecute(extractionVersion, analysisVersion)
	settings.ManagerConfidence.EmployeeEntityID = "employee-id"
	settings.ManagerConfidence.ManagerEntityID = "manager-id"
	return settings
}

func executeWithSettings(
	ctx context.Context,
	args []string,
	settings config.Settings,
	runtime Runtime,
	commandProvider CommandProvider,
	stdout, stderr io.Writer,
) error {
	return Execute(
		ctx,
		args,
		settingsLoader(settings),
		commandBootstrap(runtime, commandProvider),
		stdout,
		stderr,
	)
}
