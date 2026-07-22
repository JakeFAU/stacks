// Package cli routes Stacks subcommands without coupling command handlers to
// process-level concerns such as signals or exit status.
package cli

import (
	"context"
	"fmt"
)

const defaultCommand = "serve"

// Command handles a parsed command and its remaining arguments.
type Command interface {
	Run(ctx context.Context, args []string) error
}

// CommandFunc adapts a function into a Command.
type CommandFunc func(ctx context.Context, args []string) error

// Run executes fn.
func (fn CommandFunc) Run(ctx context.Context, args []string) error {
	return fn(ctx, args)
}

// Runner dispatches the first argument to a configured command.
type Runner struct {
	Commands map[string]Command
}

// Run executes serve when no command is supplied, otherwise it dispatches the
// requested command with the remaining arguments.
func (r Runner) Run(ctx context.Context, args []string) error {
	commandName := defaultCommand
	commandArgs := args
	if len(args) > 0 {
		commandName = args[0]
		commandArgs = args[1:]
	}

	command, ok := r.Commands[commandName]
	if !ok {
		return fmt.Errorf("unknown command %q", commandName)
	}
	if command == nil {
		return fmt.Errorf("command %q is not configured", commandName)
	}

	return command.Run(ctx, commandArgs)
}
