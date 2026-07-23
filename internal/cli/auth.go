package cli

import (
	"context"
	"errors"
)

// GoogleAuthorizer performs the explicit mutating Google authorization flow.
type GoogleAuthorizer interface {
	Authorize(ctx context.Context) error
}

// AuthCommand routes only the explicit Google authorization subcommand.
type AuthCommand struct {
	Google GoogleAuthorizer
}

// Run executes `stacks auth google`.
func (command AuthCommand) Run(ctx context.Context, args []string) error {
	if len(args) != 1 || args[0] != "google" {
		return errors.New("usage: stacks auth google")
	}
	if command.Google == nil {
		return errors.New("google authorization is not configured")
	}
	return command.Google.Authorize(ctx)
}
