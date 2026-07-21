package cli

import (
	"context"
	"strings"
	"testing"
)

func TestRunnerDefaultsToServe(t *testing.T) {
	called := false
	runner := Runner{Commands: map[string]Command{
		"serve": CommandFunc(func(context.Context, []string) error {
			called = true
			return nil
		}),
	}}

	if err := runner.Run(context.Background(), nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !called {
		t.Fatal("serve was not called")
	}
}

func TestRunnerRejectsUnknownCommand(t *testing.T) {
	runner := Runner{Commands: map[string]Command{}}

	err := runner.Run(context.Background(), []string{"unknown"})
	if err == nil {
		t.Fatal("Run() error = nil, want unknown command error")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("Run() error = %q, want unknown command error", err)
	}
}
