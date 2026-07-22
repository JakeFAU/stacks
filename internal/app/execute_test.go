package app

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	"stacks/internal/cli"
	"stacks/internal/config"
)

func TestExecuteServesWithoutPoCSettings(t *testing.T) {
	called := false
	runtime := RuntimeFunc(func(context.Context, config.Settings) error {
		called = true
		return nil
	})

	err := Execute(context.Background(), nil, config.Settings{}, runtime, nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !called {
		t.Fatal("serve runtime was not called")
	}
}

func TestExecuteRoutesEntityAndReviewCommandsWithRemainingArguments(t *testing.T) {
	settings := config.Settings{PoC: config.PoCSettings{DatabaseURL: "postgres://synthetic"}}
	cases := []struct {
		name        string
		arguments   []string
		commandName string
		wantArgs    []string
	}{
		{name: "entities", arguments: []string{"entities", "show", "person-1"}, commandName: "entities", wantArgs: []string{"show", "person-1"}},
		{name: "review", arguments: []string{"review", "accept", "proposal-1", "person-1"}, commandName: "review", wantArgs: []string{"accept", "proposal-1", "person-1"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var gotArgs []string
			var stdoutCalled bool
			provider := CommandProviderFunc(func(context.Context, config.Settings, io.Writer, io.Writer) (map[string]cli.Command, error) {
				return map[string]cli.Command{testCase.commandName: cli.CommandFunc(func(_ context.Context, args []string) error {
					gotArgs = append([]string(nil), args...)
					stdoutCalled = true
					return nil
				})}, nil
			})

			err := Execute(context.Background(), testCase.arguments, settings,
				RuntimeFunc(func(context.Context, config.Settings) error { return fmt.Errorf("serve should not run") }),
				provider, io.Discard, io.Discard)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !stdoutCalled || !reflect.DeepEqual(gotArgs, testCase.wantArgs) {
				t.Fatalf("command call = (%t, %#v), want (true, %#v)", stdoutCalled, gotArgs, testCase.wantArgs)
			}
		})
	}
}

func TestExecuteRoutesGoogleAuthWithRemainingArguments(t *testing.T) {
	settings := config.Settings{PoC: config.PoCSettings{
		GoogleOAuthClientFile: "/synthetic/client.json",
		GoogleOAuthTokenFile:  "/synthetic/token.json",
	}}
	var gotArgs []string
	provider := CommandProviderFunc(func(context.Context, config.Settings, io.Writer, io.Writer) (map[string]cli.Command, error) {
		return map[string]cli.Command{"auth": cli.CommandFunc(func(_ context.Context, args []string) error {
			gotArgs = append([]string(nil), args...)
			return nil
		})}, nil
	})

	err := Execute(context.Background(), []string{"auth", "google"}, settings,
		RuntimeFunc(func(context.Context, config.Settings) error { return fmt.Errorf("serve should not run") }),
		provider, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !reflect.DeepEqual(gotArgs, []string{"google"}) {
		t.Fatalf("auth command args = %#v, want %#v", gotArgs, []string{"google"})
	}
}

func TestExecuteRoutesSyncThroughLazyCommandProvider(t *testing.T) {
	settings := config.Settings{PoC: config.PoCSettings{
		DatabaseURL:             "postgres://synthetic",
		GoogleFolderID:          "synthetic-folder",
		GoogleOAuthClientFile:   "/synthetic/client.json",
		GoogleOAuthTokenFile:    "/synthetic/token.json",
		TranscriptTitles:        []string{"Transcript"},
		NotesTitles:             []string{"Notes"},
		AWSProfile:              "synthetic-profile",
		AWSRegion:               "us-east-1",
		BedrockModelID:          "synthetic-model",
		BedrockMaxTokens:        256,
		BedrockMaxAttempts:      1,
		IngestionLeaseDuration:  5 * time.Minute,
		IngestionAttemptTimeout: 4 * time.Minute,
		ExtractionPromptVersion: "extract-v1",
		AnalysisPromptVersion:   "analyze-v1",
	}}
	providerCalls := 0
	syncCalls := 0
	provider := CommandProviderFunc(func(context.Context, config.Settings, io.Writer, io.Writer) (map[string]cli.Command, error) {
		providerCalls++
		return map[string]cli.Command{"sync": cli.CommandFunc(func(_ context.Context, args []string) error {
			syncCalls++
			if len(args) != 0 {
				return fmt.Errorf("sync received unexpected arguments")
			}
			return nil
		})}, nil
	})

	err := Execute(context.Background(), []string{"sync"}, settings,
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
	settings := config.Settings{PoC: config.PoCSettings{
		DatabaseURL:           "postgres://synthetic",
		GoogleFolderID:        "synthetic-folder",
		GoogleOAuthClientFile: "/synthetic/client.json",
		GoogleOAuthTokenFile:  "/synthetic/token.json",
		TranscriptTitles:      []string{"Transcript"},
		NotesTitles:           []string{"Notes"},
		AWSRegion:             "us-east-1",
		BedrockModelID:        "synthetic-model",
	}}
	providerCalls := 0
	doctorCalls := 0
	provider := CommandProviderFunc(func(context.Context, config.Settings, io.Writer, io.Writer) (map[string]cli.Command, error) {
		providerCalls++
		return map[string]cli.Command{"doctor": cli.CommandFunc(func(_ context.Context, args []string) error {
			doctorCalls++
			if len(args) != 0 {
				return fmt.Errorf("doctor received unexpected arguments")
			}
			return nil
		})}, nil
	})

	err := Execute(context.Background(), []string{"doctor"}, settings,
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
	settings := config.Settings{PoC: config.PoCSettings{
		DatabaseURL: "postgres://synthetic", AWSProfile: "synthetic-profile", AWSRegion: "us-east-1",
		BedrockModelID: "synthetic-model", BedrockMaxTokens: 256, BedrockMaxAttempts: 1,
		ExtractionPromptVersion: "extract-v1", AnalysisPromptVersion: "analyze-v1",
		EmployeeEntityID: "employee-id", ManagerEntityID: "manager-id",
	}}
	providerCalls := 0
	analyzeCalls := 0
	provider := CommandProviderFunc(func(context.Context, config.Settings, io.Writer, io.Writer) (map[string]cli.Command, error) {
		providerCalls++
		return map[string]cli.Command{"analyze": cli.CommandFunc(func(_ context.Context, args []string) error {
			analyzeCalls++
			if len(args) != 0 {
				return fmt.Errorf("analyze received unexpected arguments")
			}
			return nil
		})}, nil
	})

	err := Execute(context.Background(), []string{"analyze"}, settings,
		RuntimeFunc(func(context.Context, config.Settings) error { return fmt.Errorf("serve should not run") }),
		provider, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if providerCalls != 1 || analyzeCalls != 1 {
		t.Fatalf("provider/analyze calls = %d/%d, want 1/1", providerCalls, analyzeCalls)
	}
}
