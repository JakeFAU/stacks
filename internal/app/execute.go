package app

import (
	"context"
	"errors"
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

// Execute parses one invocation, loads and validates its selected settings
// target, and bootstraps only executable application commands.
func Execute(
	ctx context.Context,
	args []string,
	loader SettingsLoader,
	bootstrap Bootstrap,
	stdout, stderr io.Writer,
) error {
	runner := cli.Runner{
		Input: nil, Output: stdout, Error: stderr,
		Execute: func(ctx context.Context, invocation cli.Invocation) error {
			if loader == nil {
				return fmt.Errorf("settings loader is not configured")
			}
			settings, err := loader.Load(config.LoadOptions{ConfigFile: invocation.ConfigFile})
			if err != nil {
				return err
			}
			target, err := targetForInvocation(invocation)
			if err != nil {
				return err
			}
			if err := validateInvocation(settings, target); err != nil {
				return err
			}
			if invocation.Command == cli.CommandConfig {
				return cli.ConfigValidateCommand{Output: stdout}.Run(ctx, invocation)
			}
			if bootstrap == nil {
				return fmt.Errorf("runtime bootstrap is not configured")
			}
			dependencies, err := bootstrap.Start(ctx, settings)
			if err != nil {
				return err
			}
			if dependencies.Shutdown == nil {
				return fmt.Errorf("runtime shutdown is not configured")
			}
			runError := dispatch(ctx, invocation, settings, dependencies, stdout, stderr)
			shutdownError := shutdownExecution(ctx, dependencies.Shutdown)
			return errors.Join(runError, shutdownError)
		},
	}
	return runner.Run(ctx, args)
}

func dispatch(
	ctx context.Context,
	invocation cli.Invocation,
	settings config.Settings,
	dependencies ExecutionDependencies,
	stdout, stderr io.Writer,
) error {
	command := config.Command(invocation.Command)
	if command == config.CommandServe {
		if dependencies.Runtime == nil {
			return fmt.Errorf("%s runtime is not configured", command)
		}
		return dependencies.Runtime.Serve(ctx, settings)
	}
	if dependencies.CommandProvider == nil {
		return fmt.Errorf("%s command is not configured", command)
	}
	commands, err := dependencies.CommandProvider.Commands(ctx, settings, stdout, stderr)
	if err != nil {
		return err
	}
	selected, ok := commands[string(command)]
	if !ok || selected == nil {
		return fmt.Errorf("%s command is not configured", command)
	}
	return selected.Run(ctx, invocation)
}

func shutdownExecution(ctx context.Context, shutdown func(context.Context) error) error {
	shutdownContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		runtimeShutdownTimeout,
	)
	defer cancel()
	if err := shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shut down runtime: %w", err)
	}
	return nil
}
