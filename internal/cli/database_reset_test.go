package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

type fakeDatabaseResetter struct {
	confirmation string
	output       io.Writer
}

func (resetter *fakeDatabaseResetter) Reset(
	_ context.Context,
	confirmation string,
	output io.Writer,
) error {
	resetter.confirmation = confirmation
	resetter.output = output
	return nil
}

func TestDBResetRequiresAndPassesExactConfirmation(t *testing.T) {
	t.Parallel()

	resetter := &fakeDatabaseResetter{}
	var output strings.Builder
	command := DBResetCommand{Resetter: resetter, Output: &output}
	if err := command.Run(t.Context(), Invocation{Command: CommandDBReset}); err == nil {
		t.Fatal("DBResetCommand.Run() error = nil, want missing confirmation rejection")
	}
	if err := command.Run(
		t.Context(),
		Invocation{Command: CommandDBReset, Arguments: []string{"delete-local-stacks-postgres"}},
	); err != nil {
		t.Fatalf("DBResetCommand.Run() error = %v", err)
	}
	if resetter.confirmation != "delete-local-stacks-postgres" || resetter.output != &output {
		t.Fatalf("reset call = (%q, %T), want exact confirmation and output", resetter.confirmation, resetter.output)
	}
}

func TestDBResetPassesWrongAndExactTokensToTheResetter(t *testing.T) {
	resetter := &guardedDatabaseResetter{required: "delete-local-stacks-postgres"}
	command := DBResetCommand{Resetter: resetter}
	wrong := Invocation{Command: CommandDBReset, Arguments: []string{"wrong-token"}}
	if err := command.Run(t.Context(), wrong); err == nil {
		t.Fatal("Run() error = nil, want resetter token rejection")
	}
	if resetter.calls != 1 || resetter.last != "wrong-token" {
		t.Fatalf("wrong token resetter calls = %d/%q, want 1/wrong-token", resetter.calls, resetter.last)
	}
	exact := Invocation{Command: CommandDBReset, Arguments: []string{"delete-local-stacks-postgres"}}
	if err := command.Run(t.Context(), exact); err != nil {
		t.Fatalf("Run() exact token error = %v", err)
	}
	if resetter.calls != 2 || resetter.last != resetter.required {
		t.Fatalf("exact token resetter calls = %d/%q, want 2/%q", resetter.calls, resetter.last, resetter.required)
	}
}

type guardedDatabaseResetter struct {
	calls    int
	last     string
	required string
}

func (resetter *guardedDatabaseResetter) Reset(_ context.Context, confirmation string, _ io.Writer) error {
	resetter.calls++
	resetter.last = confirmation
	if confirmation != resetter.required {
		return fmt.Errorf("confirmation is invalid")
	}
	return nil
}
