package cli

import (
	"context"
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
	if err := command.Run(t.Context(), nil); err == nil {
		t.Fatal("DBResetCommand.Run() error = nil, want missing confirmation rejection")
	}
	if err := command.Run(
		t.Context(),
		[]string{"delete-local-stacks-postgres"},
	); err != nil {
		t.Fatalf("DBResetCommand.Run() error = %v", err)
	}
	if resetter.confirmation != "delete-local-stacks-postgres" || resetter.output != &output {
		t.Fatalf("reset call = (%q, %T), want exact confirmation and output", resetter.confirmation, resetter.output)
	}
}
