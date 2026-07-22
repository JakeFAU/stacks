package app

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"testing"

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
