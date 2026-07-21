package app

import (
	"context"
	"io"
	"testing"

	"stacks/internal/config"
)

func TestExecuteServesWithoutPoCSettings(t *testing.T) {
	called := false
	runtime := RuntimeFunc(func(context.Context, config.Settings) error {
		called = true
		return nil
	})

	err := Execute(context.Background(), nil, config.Settings{}, runtime, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !called {
		t.Fatal("serve runtime was not called")
	}
}
