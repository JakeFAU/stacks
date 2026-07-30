package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"stacks/internal/cli"
	"stacks/internal/config"
)

type recordingBootstrap struct {
	calls        *[]string
	dependencies ExecutionDependencies
	err          error
}

func (bootstrap recordingBootstrap) Start(context.Context, config.Settings) (ExecutionDependencies, error) {
	*bootstrap.calls = append(*bootstrap.calls, "bootstrap")
	return bootstrap.dependencies, bootstrap.err
}

func TestExecuteLoadsValidatesThenBootstrapsSelectedCommand(t *testing.T) {
	calls := []string{}
	settings := config.Settings{
		Database:    config.DatabaseSettings{URL: "postgres://synthetic"},
		Application: validSyncSettingsForExecute("extract-v2"),
	}
	loader := SettingsLoaderFunc(func(config.LoadOptions) (config.Settings, error) {
		calls = append(calls, "load")
		return settings, nil
	})
	dependencies := ExecutionDependencies{
		Runtime: RuntimeFunc(func(context.Context, config.Settings) error {
			return errors.New("serve must not run")
		}),
		CommandProvider: CommandProviderFunc(func(
			context.Context, config.Settings, io.Reader, io.Writer, io.Writer,
		) (map[string]cli.Command, error) {
			calls = append(calls, "commands")
			return map[string]cli.Command{"sync": cli.CommandFunc(func(context.Context, cli.Invocation) error {
				calls = append(calls, "run")
				return nil
			})}, nil
		}),
		Shutdown: func(context.Context) error {
			calls = append(calls, "shutdown")
			return nil
		},
	}
	bootstrap := recordingBootstrap{calls: &calls, dependencies: dependencies}

	if err := Execute(t.Context(), []string{"sync"}, loader, bootstrap, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := strings.Join(calls, ","), "load,bootstrap,commands,run,shutdown"; got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
}

func TestExecuteRejectsInvalidSettingsBeforeBootstrap(t *testing.T) {
	calls := []string{}
	loader := SettingsLoaderFunc(func(config.LoadOptions) (config.Settings, error) {
		calls = append(calls, "load")
		return config.Settings{}, nil
	})
	bootstrap := recordingBootstrap{calls: &calls}

	err := Execute(t.Context(), []string{"sync"}, loader, bootstrap, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil {
		t.Fatal("Execute() error = nil, want validation failure")
	}
	if got, want := strings.Join(calls, ","), "load"; got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
}

func TestExecuteSyntaxAndHelpDoNotLoadOrBootstrap(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantHelp bool
	}{
		{name: "root help", args: []string{"--help"}, wantHelp: true},
		{name: "nested help", args: []string{"review", "create", "--help"}, wantHelp: true},
		{name: "completion help", args: []string{"completion", "--help"}, wantHelp: true},
		{name: "unknown command", args: []string{"unknown"}},
		{name: "unknown flag", args: []string{"review", "create", "proposal-1", "--unknown"}},
		{name: "invalid arity", args: []string{"entities", "show"}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			loaderCalls := 0
			bootstrapCalls := []string{}
			loader := SettingsLoaderFunc(func(config.LoadOptions) (config.Settings, error) {
				loaderCalls++
				return config.Settings{}, errors.New("loader must not run")
			})

			err := Execute(t.Context(), testCase.args, loader, recordingBootstrap{calls: &bootstrapCalls}, strings.NewReader(""), io.Discard, io.Discard)
			if testCase.wantHelp {
				if err != nil {
					t.Fatalf("Execute() error = %v, want help success", err)
				}
			} else if err == nil {
				t.Fatal("Execute() error = nil, want syntax failure")
			}
			if loaderCalls != 0 || len(bootstrapCalls) != 0 {
				t.Fatalf("loader/bootstrap calls = %d/%d, want 0/0", loaderCalls, len(bootstrapCalls))
			}
		})
	}
}

func TestExecuteRejectsRetiredAnalyzeBeforeLoadOrBootstrap(t *testing.T) {
	loaderCalls := 0
	bootstrapCalls := []string{}
	loader := SettingsLoaderFunc(func(config.LoadOptions) (config.Settings, error) {
		loaderCalls++
		return config.Settings{}, nil
	})

	err := Execute(
		t.Context(),
		[]string{"analyze"},
		loader,
		recordingBootstrap{calls: &bootstrapCalls},
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	)
	if err == nil {
		t.Fatal("Execute(analyze) error = nil, want retired command rejection")
	}
	if loaderCalls != 0 || len(bootstrapCalls) != 0 {
		t.Fatalf("loader/bootstrap calls = %d/%d, want 0/0", loaderCalls, len(bootstrapCalls))
	}
}

func TestExecutePassesSelectedConfigFileToLoader(t *testing.T) {
	var gotOptions config.LoadOptions
	loader := SettingsLoaderFunc(func(options config.LoadOptions) (config.Settings, error) {
		gotOptions = options
		return config.Settings{}, nil
	})
	bootstrap := BootstrapFunc(func(context.Context, config.Settings) (ExecutionDependencies, error) {
		return ExecutionDependencies{
			Runtime:  RuntimeFunc(func(context.Context, config.Settings) error { return nil }),
			Shutdown: func(context.Context) error { return nil },
		}, nil
	})

	if err := Execute(t.Context(), []string{"serve", "--config", "/synthetic/stacks.yaml"}, loader, bootstrap, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotOptions.ConfigFile == nil || *gotOptions.ConfigFile != "/synthetic/stacks.yaml" {
		t.Fatalf("LoadOptions.ConfigFile = %v, want selected path", gotOptions.ConfigFile)
	}
}

func TestExecuteLoaderFailureDoesNotBootstrap(t *testing.T) {
	loadError := errors.New("load sentinel")
	calls := []string{}
	loader := SettingsLoaderFunc(func(config.LoadOptions) (config.Settings, error) {
		calls = append(calls, "load")
		return config.Settings{}, loadError
	})

	err := Execute(t.Context(), []string{"serve"}, loader, recordingBootstrap{calls: &calls}, strings.NewReader(""), io.Discard, io.Discard)
	if !errors.Is(err, loadError) {
		t.Fatalf("Execute() error = %v, want loader sentinel", err)
	}
	if got, want := strings.Join(calls, ","), "load"; got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
}

func TestExecuteValidatesSelectedGoogleAuthBeforeBootstrap(t *testing.T) {
	tests := []struct {
		name        string
		action      string
		application config.ApplicationSettings
		wantError   string
	}{
		{
			name:   "drive",
			action: "google",
			application: config.ApplicationSettings{Directory: config.GoogleDirectorySettings{
				OAuthClientFile: "/synthetic/directory-client.json",
				OAuthTokenFile:  "/synthetic/directory-token.json",
			}},
			wantError: config.GoogleOAuthClientFileEnvironmentVariable,
		},
		{
			name:   "directory",
			action: "google-directory",
			application: config.ApplicationSettings{
				GoogleOAuthClientFile: "/synthetic/drive-client.json",
				GoogleOAuthTokenFile:  "/synthetic/drive-token.json",
			},
			wantError: config.GoogleDirectoryClientFileEnvironmentVariable,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			calls := []string{}
			loader := settingsLoader(config.Settings{Application: testCase.application})
			err := Execute(t.Context(), []string{"auth", testCase.action}, loader, recordingBootstrap{calls: &calls}, strings.NewReader(""), io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("Execute() error = %v, want selected auth target failure containing %s", err, testCase.wantError)
			}
			if len(calls) != 0 {
				t.Fatalf("bootstrap calls = %d, want 0", len(calls))
			}
		})
	}
}

func TestExecuteOfflineGoogleAuthValidationDoesNotBootstrap(t *testing.T) {
	tests := []struct {
		name        string
		action      string
		application config.ApplicationSettings
		wantOutput  string
		wantError   string
	}{
		{
			name:   "drive",
			action: "google",
			application: config.ApplicationSettings{
				GoogleOAuthClientFile: "/synthetic/drive-client.json",
				GoogleOAuthTokenFile:  "/synthetic/drive-token.json",
			},
			wantOutput: "configuration valid for auth google\n",
		},
		{
			name:   "directory",
			action: "google-directory",
			application: config.ApplicationSettings{Directory: config.GoogleDirectorySettings{
				OAuthClientFile: "/synthetic/directory-client.json",
				OAuthTokenFile:  "/synthetic/directory-token.json",
			}},
			wantOutput: "configuration valid for auth google-directory\n",
		},
		{
			name:        "drive invalid",
			action:      "google",
			wantError:   config.GoogleOAuthClientFileEnvironmentVariable,
			wantOutput:  "",
			application: config.ApplicationSettings{},
		},
		{
			name:        "directory invalid",
			action:      "google-directory",
			wantError:   config.GoogleDirectoryClientFileEnvironmentVariable,
			wantOutput:  "",
			application: config.ApplicationSettings{},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			calls := []string{}
			var output bytes.Buffer
			err := Execute(
				t.Context(),
				[]string{"config", "validate", "auth", testCase.action},
				settingsLoader(config.Settings{Application: testCase.application}),
				recordingBootstrap{calls: &calls},
				strings.NewReader(""),
				&output,
				io.Discard,
			)
			if testCase.wantError == "" {
				if err != nil {
					t.Fatalf("Execute() error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("Execute() error = %v, want failure containing %s", err, testCase.wantError)
			}
			if output.String() != testCase.wantOutput {
				t.Fatalf("output = %q, want %q", output.String(), testCase.wantOutput)
			}
			if len(calls) != 0 {
				t.Fatalf("bootstrap calls = %d, want 0", len(calls))
			}
		})
	}
}

func TestExecuteOfflineTargetsUseSettingsValidation(t *testing.T) {
	targets := [][]string{
		{"serve"},
		{"doctor"},
		{"sync"},
		{"entities"},
		{"review"},
		{"db-migrate"},
		{"db-status"},
		{"db-reset"},
	}
	settings := config.Settings{Database: config.DatabaseSettings{
		Scopes: []config.DatabaseScope{"unsupported"},
	}}

	for _, target := range targets {
		t.Run(strings.Join(target, " "), func(t *testing.T) {
			var output bytes.Buffer
			calls := []string{}
			args := append([]string{"config", "validate"}, target...)
			err := Execute(t.Context(), args, settingsLoader(settings), recordingBootstrap{calls: &calls}, strings.NewReader(""), &output, io.Discard)
			if err == nil || !strings.Contains(err.Error(), config.DatabaseScopesEnvironmentVariable) {
				t.Fatalf("Execute() error = %v, want Settings.Validate failure", err)
			}
			if output.Len() != 0 {
				t.Fatalf("output = %q, want no effective values", output.String())
			}
			if len(calls) != 0 {
				t.Fatalf("bootstrap calls = %d, want 0", len(calls))
			}
		})
	}
}

func TestExecuteOfflineValidationDelegatesBoundedOutput(t *testing.T) {
	var output bytes.Buffer
	calls := []string{}
	err := Execute(
		t.Context(),
		[]string{"config", "validate", "serve"},
		settingsLoader(config.Settings{}),
		recordingBootstrap{calls: &calls},
		strings.NewReader(""),
		&output,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := output.String(), "configuration valid for serve\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if len(calls) != 0 {
		t.Fatalf("bootstrap calls = %d, want 0", len(calls))
	}
}

func TestExecuteOfflineQueryValidationDoesNotBootstrap(t *testing.T) {
	var output bytes.Buffer
	calls := []string{}
	settings := config.Settings{
		Database: config.DatabaseSettings{URL: "postgres://synthetic"},
		Query:    config.QuerySettings{MaxEntities: 16, MaxPredicates: 32, MaxChronology: 1000},
	}
	err := Execute(
		t.Context(),
		[]string{"config", "validate", "query"},
		settingsLoader(settings),
		recordingBootstrap{calls: &calls},
		strings.NewReader(""),
		&output,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.String() != "configuration valid for query\n" {
		t.Fatalf("output = %q, want exact query validation outcome", output.String())
	}
	if len(calls) != 0 {
		t.Fatalf("bootstrap calls = %d, want 0", len(calls))
	}
}

func TestExecuteConfigValidateQueryAskUsesAskSettingsTarget(t *testing.T) {
	calls := []string{}
	settings := config.Settings{
		Database: config.DatabaseSettings{URL: "postgres://synthetic"},
		Query:    config.QuerySettings{MaxEntities: 16, MaxPredicates: 32, MaxChronology: 1000},
		QueryPlanner: config.QueryPlannerSettings{
			Timeout: 0, MaxQuestionBytes: 1024,
		},
	}

	err := Execute(
		t.Context(),
		[]string{"config", "validate", "query", "ask"},
		settingsLoader(settings),
		recordingBootstrap{calls: &calls},
		strings.NewReader("synthetic question\n"),
		io.Discard,
		io.Discard,
	)
	if err == nil || !strings.Contains(err.Error(), config.QueryPlannerTimeoutEnvironmentVariable) {
		t.Fatalf("Execute() error = %v, want query ask planner validation failure", err)
	}
	if len(calls) != 0 {
		t.Fatalf("bootstrap calls = %d, want 0", len(calls))
	}
}

func TestExecuteInvalidQueryAskSettingsDoNotBootstrapOrConstructCommands(t *testing.T) {
	loaderCalls := 0
	bootstrapCalls := []string{}
	settings := config.Settings{
		Database: config.DatabaseSettings{URL: "postgres://synthetic"},
		Query:    config.QuerySettings{MaxEntities: 16, MaxPredicates: 32, MaxChronology: 1000},
		QueryPlanner: config.QueryPlannerSettings{
			Timeout: time.Minute, MaxQuestionBytes: 0,
		},
	}
	loader := SettingsLoaderFunc(func(config.LoadOptions) (config.Settings, error) {
		loaderCalls++
		return settings, nil
	})

	err := Execute(
		t.Context(),
		[]string{"query", "ask", "--entity", "entity-a", "--reference-time", "2026-07-30T00:00:00Z"},
		loader,
		recordingBootstrap{calls: &bootstrapCalls},
		strings.NewReader("synthetic question\n"),
		io.Discard,
		io.Discard,
	)
	if err == nil || !strings.Contains(err.Error(), config.QueryPlannerMaxQuestionBytesEnvironmentVariable) {
		t.Fatalf("Execute() error = %v, want query ask planner validation failure", err)
	}
	if loaderCalls != 1 || len(bootstrapCalls) != 0 {
		t.Fatalf("loader/bootstrap calls = %d/%d, want 1/0", loaderCalls, len(bootstrapCalls))
	}
}

func TestExecuteDoesNotReadInputForHelpSyntaxOrConfigValidation(t *testing.T) {
	validAskSettings := config.Settings{
		Database: config.DatabaseSettings{URL: "postgres://synthetic"},
		Query:    config.QuerySettings{MaxEntities: 16, MaxPredicates: 32, MaxChronology: 1000},
		QueryPlanner: config.QueryPlannerSettings{
			Timeout: time.Minute, MaxQuestionBytes: 1024,
		},
		Application: config.ApplicationSettings{Model: config.ModelSettings{
			DataMode: "personal", Provider: "bedrock", ModelID: "synthetic-model",
			MaxOutputTokens: 256, MaxAttempts: 1, AWSRegion: "us-east-1",
		}},
	}
	tests := []struct {
		name string
		args []string
	}{
		{name: "help", args: []string{"query", "ask", "--help"}},
		{name: "malformed ask", args: []string{"query", "ask", "question", "--entity", "entity-a", "--reference-time", "2026-07-30T00:00:00Z"}},
		{name: "config validation", args: []string{"config", "validate", "query", "ask"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			input := strings.NewReader("synthetic question\n")
			loaderCalls := 0
			bootstrapCalls := []string{}
			err := Execute(
				t.Context(),
				testCase.args,
				SettingsLoaderFunc(func(config.LoadOptions) (config.Settings, error) {
					loaderCalls++
					return validAskSettings, nil
				}),
				recordingBootstrap{calls: &bootstrapCalls},
				input,
				io.Discard,
				io.Discard,
			)
			if testCase.name == "malformed ask" {
				if err == nil {
					t.Fatal("Execute() error = nil, want syntax failure")
				}
			} else if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if input.Len() != len("synthetic question\n") {
				t.Fatalf("stdin length = %d, want input unread", input.Len())
			}
			if len(bootstrapCalls) != 0 {
				t.Fatalf("bootstrap calls = %d, want 0", len(bootstrapCalls))
			}
			wantLoaderCalls := 1
			if testCase.name != "config validation" {
				wantLoaderCalls = 0
			}
			if loaderCalls != wantLoaderCalls {
				t.Fatalf("loader calls = %d, want %d", loaderCalls, wantLoaderCalls)
			}
		})
	}
}

func TestExecuteInvalidQueryConfigurationDoesNotBootstrapOrWriteSuccess(t *testing.T) {
	var output bytes.Buffer
	calls := []string{}
	settings := config.Settings{
		Database: config.DatabaseSettings{URL: "postgres://synthetic"},
		Query:    config.QuerySettings{},
	}
	err := Execute(
		t.Context(),
		[]string{"config", "validate", "query"},
		settingsLoader(settings),
		recordingBootstrap{calls: &calls},
		strings.NewReader(""),
		&output,
		io.Discard,
	)
	if err == nil || !strings.Contains(err.Error(), config.QueryMaxEntitiesEnvironmentVariable) {
		t.Fatalf("Execute() error = %v, want invalid query limit rejection", err)
	}
	if output.Len() != 0 {
		t.Fatalf("output = %q, want no success output", output.String())
	}
	if len(calls) != 0 {
		t.Fatalf("bootstrap calls = %d, want 0", len(calls))
	}
}

func TestExecuteRejectsInvalidTrendSyntaxBeforeLoadOrBootstrap(t *testing.T) {
	loaderCalls := 0
	bootstrapCalls := []string{}
	loader := SettingsLoaderFunc(func(config.LoadOptions) (config.Settings, error) {
		loaderCalls++
		return config.Settings{}, errors.New("loader must not run")
	})
	err := Execute(
		t.Context(),
		[]string{
			"query", "trend",
			"--entity", "entity-a",
			"--before", "2025-01-01T00:00:00Z",
			"--after", "2025-03-01T00:00:00Z/2025-04-01T00:00:00Z",
		},
		loader,
		recordingBootstrap{calls: &bootstrapCalls},
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	)
	if err == nil {
		t.Fatal("Execute() error = nil, want syntax failure")
	}
	if loaderCalls != 0 || len(bootstrapCalls) != 0 {
		t.Fatalf("loader/bootstrap calls = %d/%d, want 0/0", loaderCalls, len(bootstrapCalls))
	}
}

func TestExecuteBootstrapFailurePreservesIdentity(t *testing.T) {
	bootstrapError := errors.New("bootstrap sentinel")
	calls := []string{}
	err := Execute(
		t.Context(),
		[]string{"serve"},
		settingsLoader(config.Settings{}),
		recordingBootstrap{calls: &calls, err: bootstrapError},
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	)
	if !errors.Is(err, bootstrapError) {
		t.Fatalf("Execute() error = %v, want bootstrap sentinel", err)
	}
}

func TestExecuteCommandFailureStillShutsDown(t *testing.T) {
	commandError := errors.New("command sentinel")
	shutdownCalls := 0
	bootstrap := BootstrapFunc(func(context.Context, config.Settings) (ExecutionDependencies, error) {
		return ExecutionDependencies{
			CommandProvider: CommandProviderFunc(func(
				context.Context, config.Settings, io.Reader, io.Writer, io.Writer,
			) (map[string]cli.Command, error) {
				return map[string]cli.Command{
					"sync": cli.CommandFunc(func(context.Context, cli.Invocation) error {
						return commandError
					}),
				}, nil
			}),
			Shutdown: func(context.Context) error {
				shutdownCalls++
				return nil
			},
		}, nil
	})

	settings := config.Settings{
		Database:    config.DatabaseSettings{URL: "postgres://synthetic"},
		Application: validSyncSettingsForExecute("extract-v2"),
	}
	err := Execute(t.Context(), []string{"sync"}, settingsLoader(settings), bootstrap, strings.NewReader(""), io.Discard, io.Discard)
	if !errors.Is(err, commandError) {
		t.Fatalf("Execute() error = %v, want command sentinel", err)
	}
	if shutdownCalls != 1 {
		t.Fatalf("shutdown calls = %d, want 1", shutdownCalls)
	}
}

func TestExecuteJoinsCommandAndShutdownErrors(t *testing.T) {
	commandError := errors.New("command sentinel")
	shutdownError := errors.New("shutdown sentinel")
	bootstrap := BootstrapFunc(func(context.Context, config.Settings) (ExecutionDependencies, error) {
		return ExecutionDependencies{
			CommandProvider: CommandProviderFunc(func(
				context.Context, config.Settings, io.Reader, io.Writer, io.Writer,
			) (map[string]cli.Command, error) {
				return map[string]cli.Command{
					"sync": cli.CommandFunc(func(context.Context, cli.Invocation) error {
						return commandError
					}),
				}, nil
			}),
			Shutdown: func(context.Context) error {
				return shutdownError
			},
		}, nil
	})

	settings := config.Settings{
		Database:    config.DatabaseSettings{URL: "postgres://synthetic"},
		Application: validSyncSettingsForExecute("extract-v2"),
	}
	err := Execute(t.Context(), []string{"sync"}, settingsLoader(settings), bootstrap, strings.NewReader(""), io.Discard, io.Discard)
	if !errors.Is(err, commandError) || !errors.Is(err, shutdownError) {
		t.Fatalf("Execute() error = %v, want command and shutdown sentinels", err)
	}
}

func TestShutdownExecutionAddsOperationContextWithoutChangingIdentity(t *testing.T) {
	shutdownError := errors.New("shutdown sentinel")

	err := shutdownExecution(t.Context(), func(context.Context) error {
		return shutdownError
	})

	if !errors.Is(err, shutdownError) {
		t.Fatalf("shutdownExecution() error = %v, want shutdown sentinel", err)
	}
	if !strings.Contains(err.Error(), "shut down runtime") {
		t.Fatalf("shutdownExecution() error = %v, want runtime shutdown context", err)
	}
}

func TestExecuteCanceledCommandUsesValuePreservingShutdownContext(t *testing.T) {
	type contextKey string
	const key contextKey = "shutdown-value"

	callerContext, cancel := context.WithCancel(context.WithValue(context.Background(), key, "preserved"))
	shutdownCalled := false
	bootstrap := BootstrapFunc(func(context.Context, config.Settings) (ExecutionDependencies, error) {
		return ExecutionDependencies{
			Runtime: RuntimeFunc(func(ctx context.Context, _ config.Settings) error {
				cancel()
				return ctx.Err()
			}),
			Shutdown: func(ctx context.Context) error {
				shutdownCalled = true
				if err := ctx.Err(); err != nil {
					return fmt.Errorf("shutdown context canceled: %w", err)
				}
				deadline, ok := ctx.Deadline()
				if !ok {
					return fmt.Errorf("shutdown context has no deadline")
				}
				remaining := time.Until(deadline)
				if remaining <= 0 || remaining > runtimeShutdownTimeout {
					return fmt.Errorf("shutdown deadline remaining = %s, want within %s", remaining, runtimeShutdownTimeout)
				}
				if got := ctx.Value(key); got != "preserved" {
					return fmt.Errorf("shutdown context value = %v, want preserved", got)
				}
				return nil
			},
		}, nil
	})

	err := Execute(callerContext, []string{"serve"}, settingsLoader(config.Settings{}), bootstrap, strings.NewReader(""), io.Discard, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
	if !shutdownCalled {
		t.Fatal("shutdown was not called")
	}
}

func TestExecuteRejectsMissingShutdownBeforeApplicationWork(t *testing.T) {
	validSync := config.Settings{
		Database:    config.DatabaseSettings{URL: "postgres://synthetic"},
		Application: validSyncSettingsForExecute("extract-v2"),
	}
	tests := []struct {
		name         string
		args         []string
		settings     config.Settings
		dependencies func(*int) ExecutionDependencies
	}{
		{
			name: "serve",
			args: []string{"serve"},
			dependencies: func(applicationCalls *int) ExecutionDependencies {
				return ExecutionDependencies{
					Runtime: RuntimeFunc(func(context.Context, config.Settings) error {
						(*applicationCalls)++
						return nil
					}),
				}
			},
		},
		{
			name:     "non-server command",
			args:     []string{"sync"},
			settings: validSync,
			dependencies: func(applicationCalls *int) ExecutionDependencies {
				return ExecutionDependencies{
					CommandProvider: CommandProviderFunc(func(
						context.Context, config.Settings, io.Reader, io.Writer, io.Writer,
					) (map[string]cli.Command, error) {
						(*applicationCalls)++
						return map[string]cli.Command{
							"sync": cli.CommandFunc(func(context.Context, cli.Invocation) error {
								(*applicationCalls)++
								return nil
							}),
						}, nil
					}),
				}
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			applicationCalls := 0
			err := Execute(
				t.Context(),
				testCase.args,
				settingsLoader(testCase.settings),
				fixedBootstrap(testCase.dependencies(&applicationCalls)),
				strings.NewReader(""),
				io.Discard,
				io.Discard,
			)
			if err == nil || !strings.Contains(err.Error(), "runtime shutdown is not configured") {
				t.Fatalf("Execute() error = %v, want missing shutdown failure", err)
			}
			if applicationCalls != 0 {
				t.Fatalf("application calls = %d, want 0", applicationCalls)
			}
		})
	}
}

func TestExecuteReportsMissingRequiredDependencies(t *testing.T) {
	validSync := config.Settings{
		Database:    config.DatabaseSettings{URL: "postgres://synthetic"},
		Application: validSyncSettingsForExecute("extract-v2"),
	}
	tests := []struct {
		name      string
		args      []string
		loader    SettingsLoader
		bootstrap Bootstrap
		want      string
	}{
		{
			name: "loader", args: []string{"serve"},
			want: "settings loader is not configured",
		},
		{
			name: "bootstrap", args: []string{"serve"}, loader: settingsLoader(config.Settings{}),
			want: "runtime bootstrap is not configured",
		},
		{
			name: "runtime", args: []string{"serve"}, loader: settingsLoader(config.Settings{}),
			bootstrap: fixedBootstrap(ExecutionDependencies{Shutdown: func(context.Context) error { return nil }}),
			want:      "serve runtime is not configured",
		},
		{
			name: "command provider", args: []string{"sync"}, loader: settingsLoader(validSync),
			bootstrap: fixedBootstrap(ExecutionDependencies{Shutdown: func(context.Context) error { return nil }}),
			want:      "sync command is not configured",
		},
		{
			name: "shutdown", args: []string{"serve"}, loader: settingsLoader(config.Settings{}),
			bootstrap: fixedBootstrap(ExecutionDependencies{
				Runtime: RuntimeFunc(func(context.Context, config.Settings) error { return nil }),
			}),
			want: "runtime shutdown is not configured",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := Execute(t.Context(), testCase.args, testCase.loader, testCase.bootstrap, strings.NewReader(""), io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Execute() error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func settingsLoader(settings config.Settings) SettingsLoader {
	return SettingsLoaderFunc(func(config.LoadOptions) (config.Settings, error) {
		return settings, nil
	})
}

func fixedBootstrap(dependencies ExecutionDependencies) Bootstrap {
	return BootstrapFunc(func(context.Context, config.Settings) (ExecutionDependencies, error) {
		return dependencies, nil
	})
}

func commandBootstrap(runtime Runtime, provider CommandProvider) Bootstrap {
	return fixedBootstrap(ExecutionDependencies{
		Runtime:         runtime,
		CommandProvider: provider,
		Shutdown:        func(context.Context) error { return nil },
	})
}
