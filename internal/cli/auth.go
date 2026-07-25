package cli

import (
	"context"
	"errors"
)

// GoogleAuthorizer performs the explicit mutating Google authorization flow.
type GoogleAuthorizer interface {
	Authorize(ctx context.Context) error
}

// AuthCommand routes independently scoped Google authorization subcommands.
type AuthCommand struct {
	GoogleDrive     GoogleAuthorizer
	GoogleDirectory GoogleAuthorizer
}

// Run executes `stacks auth google` or `stacks auth google-directory`.
func (command AuthCommand) Run(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: stacks auth google | stacks auth google-directory")
	}
	switch args[0] {
	case "google":
		if command.GoogleDrive == nil {
			return errors.New("google authorization is not configured")
		}
		return command.GoogleDrive.Authorize(ctx)
	case "google-directory":
		if command.GoogleDirectory == nil {
			return errors.New("google directory authorization is not configured")
		}
		return command.GoogleDirectory.Authorize(ctx)
	default:
		return errors.New("usage: stacks auth google | stacks auth google-directory")
	}
}
