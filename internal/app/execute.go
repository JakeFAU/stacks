package app

import (
	"context"
	"io"

	"stacks/internal/cli"
	"stacks/internal/config"
)

// Runtime owns the server command's runtime behavior.
type Runtime interface {
	Serve(ctx context.Context, settings config.Settings) error
}

// RuntimeFunc adapts a function into a Runtime.
type RuntimeFunc func(ctx context.Context, settings config.Settings) error

// Serve starts the runtime.
func (fn RuntimeFunc) Serve(ctx context.Context, settings config.Settings) error {
	return fn(ctx, settings)
}

// Execute validates the selected command's configuration and dispatches it.
// stdout and stderr are accepted here so later CLI commands can write only to
// their explicit process boundary.
func Execute(
	ctx context.Context,
	args []string,
	settings config.Settings,
	runtime Runtime,
	stdout, stderr io.Writer,
) error {
	_ = stdout
	_ = stderr

	command := config.CommandServe
	if len(args) > 0 {
		command = config.Command(args[0])
	}
	if err := settings.PoC.Validate(command); err != nil {
		return err
	}

	runner := cli.Runner{Commands: map[string]cli.Command{
		string(config.CommandServe): cli.CommandFunc(func(ctx context.Context, _ []string) error {
			return runtime.Serve(ctx, settings)
		}),
	}}
	return runner.Run(ctx, args)
}
