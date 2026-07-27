package app

import (
	"context"
	"fmt"
	"time"

	"stacks/internal/cli"
	"stacks/internal/config"
)

const runtimeShutdownTimeout = 10 * time.Second

// SettingsLoader loads settings for one already-parsed invocation.
type SettingsLoader interface {
	Load(config.LoadOptions) (config.Settings, error)
}

// SettingsLoaderFunc adapts a function into a SettingsLoader.
type SettingsLoaderFunc func(config.LoadOptions) (config.Settings, error)

// Load loads settings using fn.
func (fn SettingsLoaderFunc) Load(options config.LoadOptions) (config.Settings, error) {
	return fn(options)
}

// ExecutionDependencies are the successfully bootstrapped runtime boundaries
// owned by one application invocation.
type ExecutionDependencies struct {
	Runtime         Runtime
	CommandProvider CommandProvider
	Shutdown        func(context.Context) error
}

// Bootstrap constructs runtime dependencies after selected-target validation.
type Bootstrap interface {
	Start(context.Context, config.Settings) (ExecutionDependencies, error)
}

// BootstrapFunc adapts a function into a Bootstrap.
type BootstrapFunc func(context.Context, config.Settings) (ExecutionDependencies, error)

// Start constructs execution dependencies using fn.
func (fn BootstrapFunc) Start(ctx context.Context, settings config.Settings) (ExecutionDependencies, error) {
	return fn(ctx, settings)
}

type validationTarget struct {
	Command config.Command
	Auth    config.GoogleAuthTarget
}

func targetForInvocation(invocation cli.Invocation) (validationTarget, error) {
	if invocation.Command == cli.CommandConfig {
		if invocation.Action != cli.ActionValidate || invocation.ConfigValidation == nil {
			return validationTarget{}, fmt.Errorf("configuration validation target is invalid")
		}
		return targetForConfigValidation(*invocation.ConfigValidation)
	}

	target := validationTarget{Command: config.Command(invocation.Command)}
	if invocation.Command == cli.CommandQuery {
		if invocation.Action != cli.ActionTrend || invocation.Query == nil || len(invocation.Arguments) != 0 {
			return validationTarget{}, fmt.Errorf("query invocation is invalid")
		}
		return target, nil
	}
	if invocation.Command != cli.CommandAuth {
		return target, nil
	}
	authTarget, err := googleAuthTarget(invocation.Action)
	if err != nil {
		return validationTarget{}, err
	}
	target.Auth = authTarget
	return target, nil
}

func targetForConfigValidation(input cli.ConfigValidationInput) (validationTarget, error) {
	if input.Command == cli.CommandAuth {
		authTarget, err := googleAuthTarget(input.Action)
		if err != nil {
			return validationTarget{}, fmt.Errorf("configuration validation target is invalid")
		}
		return validationTarget{Command: config.CommandAuth, Auth: authTarget}, nil
	}
	if input.Action != "" {
		return validationTarget{}, fmt.Errorf("configuration validation target is invalid")
	}
	switch input.Command {
	case cli.CommandServe, cli.CommandDoctor, cli.CommandSync, cli.CommandEntities,
		cli.CommandReview, cli.CommandAnalyze, cli.CommandQuery, cli.CommandDBMigrate,
		cli.CommandDBStatus, cli.CommandDBReset:
		return validationTarget{Command: config.Command(input.Command)}, nil
	default:
		return validationTarget{}, fmt.Errorf("configuration validation target is invalid")
	}
}

func googleAuthTarget(action cli.Action) (config.GoogleAuthTarget, error) {
	switch action {
	case cli.ActionAuthGoogle:
		return config.GoogleAuthDrive, nil
	case cli.ActionAuthGoogleDirectory:
		return config.GoogleAuthDirectory, nil
	default:
		return "", fmt.Errorf("unsupported Google authorization action %q", action)
	}
}

func validateInvocation(settings config.Settings, target validationTarget) error {
	if err := settings.Validate(target.Command); err != nil {
		return err
	}
	if target.Auth != "" {
		return settings.Application.ValidateGoogleAuth(target.Auth)
	}
	return nil
}
