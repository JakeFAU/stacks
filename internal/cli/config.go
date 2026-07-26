package cli

import (
	"context"
	"fmt"
	"io"
)

// ConfigValidationInput identifies an application command whose configuration is validated.
type ConfigValidationInput struct {
	Command CommandName
	Action  Action
}

// ConfigValidateCommand writes a bounded outcome after application validation succeeds.
type ConfigValidateCommand struct {
	Output io.Writer
}

// Run writes the typed configuration validation outcome without loading configuration.
func (command ConfigValidateCommand) Run(_ context.Context, invocation Invocation) error {
	if invocation.Command != CommandConfig || invocation.Action != ActionValidate ||
		invocation.ConfigValidation == nil || len(invocation.Arguments) != 0 {
		return fmt.Errorf("configuration validation invocation is invalid")
	}
	if command.Output == nil {
		return fmt.Errorf("configuration validation output is required")
	}
	target, err := configValidationTarget(*invocation.ConfigValidation)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(command.Output, "configuration valid for %s\n", target)
	return err
}

func configValidationTarget(input ConfigValidationInput) (string, error) {
	if input.Command == CommandAuth {
		switch input.Action {
		case ActionAuthGoogle, ActionAuthGoogleDirectory:
			return fmt.Sprintf("%s %s", input.Command, input.Action), nil
		default:
			return "", fmt.Errorf("configuration validation target is invalid")
		}
	}
	if input.Action != "" {
		return "", fmt.Errorf("configuration validation target is invalid")
	}
	switch input.Command {
	case CommandServe, CommandDoctor, CommandSync, CommandEntities, CommandReview,
		CommandAnalyze, CommandDBMigrate, CommandDBStatus, CommandDBReset:
		return string(input.Command), nil
	default:
		return "", fmt.Errorf("configuration validation target is invalid")
	}
}
