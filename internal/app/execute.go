package app

import (
	"context"
	"fmt"
	"io"

	"stacks/internal/cli"
	"stacks/internal/config"
)

// Runtime owns the server command's runtime behavior.
type Runtime interface {
	Serve(ctx context.Context, settings config.Settings) error
}

// CommandProvider supplies already-wired non-server commands. It keeps process
// dependencies such as PostgreSQL outside of the argument router.
type CommandProvider interface {
	Commands(ctx context.Context, settings config.Settings, stdout, stderr io.Writer) (map[string]cli.Command, error)
}

// CommandProviderFunc adapts a function into a CommandProvider.
type CommandProviderFunc func(ctx context.Context, settings config.Settings, stdout, stderr io.Writer) (map[string]cli.Command, error)

// Commands returns the command map constructed by fn.
func (fn CommandProviderFunc) Commands(ctx context.Context, settings config.Settings, stdout, stderr io.Writer) (map[string]cli.Command, error) {
	return fn(ctx, settings, stdout, stderr)
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
	commandProvider CommandProvider,
	stdout, stderr io.Writer,
) error {
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
	if command == config.CommandAuth || command == config.CommandDoctor || command == config.CommandSync || command == config.CommandEntities || command == config.CommandReview || command == config.CommandAnalyze {
		if commandProvider == nil {
			return fmt.Errorf("%s command is not configured", command)
		}
		commands, err := commandProvider.Commands(ctx, settings, stdout, stderr)
		if err != nil {
			return err
		}
		for name, registered := range commands {
			runner.Commands[name] = registered
		}
	}
	return runner.Run(ctx, args)
}
