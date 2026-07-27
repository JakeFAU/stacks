// Package cli owns Stacks' typed command-line transport boundary.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"stacks/internal/query"
)

const (
	rootCommandUse                = "stacks"
	configFlagName                = "config"
	queryEntityFlagName           = "entity"
	queryEntityMatchFlagName      = "entity-match"
	queryPredicateFlagName        = "predicate"
	queryBeforeFlagName           = "before"
	queryAfterFlagName            = "after"
	queryKnownAsOfFlagName        = "known-as-of"
	queryOutputFlagName           = "output"
	reviewCreateNameFlagName      = "name"
	reviewCreateEmailFlagName     = "email"
	acceptDirectoryEntityFlagName = "entity"
	invalidCommandSyntaxMessage   = "invalid command syntax"
)

// CommandName identifies a top-level Stacks application command.
type CommandName string

const (
	CommandServe     CommandName = "serve"
	CommandConfig    CommandName = "config"
	CommandAuth      CommandName = "auth"
	CommandDoctor    CommandName = "doctor"
	CommandSync      CommandName = "sync"
	CommandEntities  CommandName = "entities"
	CommandReview    CommandName = "review"
	CommandAnalyze   CommandName = "analyze"
	CommandQuery     CommandName = "query"
	CommandDBMigrate CommandName = "db-migrate"
	CommandDBStatus  CommandName = "db-status"
	CommandDBReset   CommandName = "db-reset"
)

// Action identifies a validated nested command action.
type Action string

const (
	ActionValidate            Action = "validate"
	ActionAuthGoogle          Action = "google"
	ActionAuthGoogleDirectory Action = "google-directory"
	ActionList                Action = "list"
	ActionShow                Action = "show"
	ActionAccept              Action = "accept"
	ActionAcceptDirectory     Action = "accept-directory"
	ActionReject              Action = "reject"
	ActionCreate              Action = "create"
	ActionCorrect             Action = "correct"
	ActionTrend               Action = "trend"
)

// Invocation is the validated, provider-neutral CLI input for one application command.
type Invocation struct {
	Command          CommandName
	Action           Action
	Arguments        []string
	ConfigFile       *string
	ConfigValidation *ConfigValidationInput
	CreatePerson     *CreatePersonInput
	AcceptDirectory  *AcceptDirectoryInput
	Query            *QueryInput
}

// Command executes a typed application invocation.
type Command interface {
	Run(context.Context, Invocation) error
}

// CommandFunc adapts a function into a Command.
type CommandFunc func(context.Context, Invocation) error

// Run executes fn.
func (fn CommandFunc) Run(ctx context.Context, invocation Invocation) error {
	return fn(ctx, invocation)
}

// Runner creates and executes a fresh command tree for one invocation.
type Runner struct {
	Execute CommandFunc
	Input   io.Reader
	Output  io.Writer
	Error   io.Writer
}

// Run parses args and invokes the selected typed command leaf.
func (r Runner) Run(ctx context.Context, args []string) error {
	handled := false
	root := r.newRootCommand(&handled)
	root.SetArgs(args)
	root.SetContext(ctx)
	root.SetIn(r.Input)
	root.SetOut(r.Output)
	root.SetErr(r.Error)
	err := root.ExecuteContext(ctx)
	if err != nil && !handled {
		return errors.New(invalidCommandSyntaxMessage)
	}
	return err
}

func (r Runner) newRootCommand(handled *bool) *cobra.Command {
	selectedConfigFile := func(command *cobra.Command) (*string, error) {
		if !command.Flags().Changed(configFlagName) {
			return nil, nil
		}
		configFile, err := command.Flags().GetString(configFlagName)
		if err != nil {
			return nil, fmt.Errorf("read %s flag: %w", configFlagName, err)
		}
		if strings.TrimSpace(configFile) == "" {
			return nil, fmt.Errorf("--%s requires a configuration file", configFlagName)
		}
		copied := configFile
		return &copied, nil
	}
	execute := func(command *cobra.Command, invocation Invocation) error {
		configFile, err := selectedConfigFile(command)
		if err != nil {
			return err
		}
		*handled = true
		invocation.ConfigFile = configFile
		if r.Execute == nil {
			return fmt.Errorf("%s command is not configured", invocation.Command)
		}
		return r.Execute(command.Context(), invocation)
	}
	leaf := func(use string, topLevel CommandName, action Action, args cobra.PositionalArgs) *cobra.Command {
		return &cobra.Command{
			Use:  use,
			Args: args,
			RunE: func(command *cobra.Command, values []string) error {
				return execute(command, Invocation{Command: topLevel, Action: action, Arguments: values})
			},
		}
	}

	root := &cobra.Command{
		Use:           rootCommandUse,
		Short:         "Build provenance-backed temporal knowledge",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, _ []string) error {
			return execute(command, Invocation{Command: CommandServe})
		},
	}
	root.PersistentFlags().String(configFlagName, "", "configuration file")

	serve := leaf(string(CommandServe), CommandServe, "", cobra.NoArgs)
	root.AddCommand(serve)
	root.AddCommand(leaf(string(CommandDoctor), CommandDoctor, "", cobra.NoArgs))
	root.AddCommand(leaf(string(CommandSync), CommandSync, "", cobra.NoArgs))
	root.AddCommand(leaf(string(CommandAnalyze), CommandAnalyze, "", cobra.NoArgs))
	root.AddCommand(leaf(string(CommandDBMigrate), CommandDBMigrate, "", cobra.NoArgs))
	root.AddCommand(leaf(string(CommandDBStatus), CommandDBStatus, "", cobra.NoArgs))
	root.AddCommand(leaf(string(CommandDBReset)+" <confirmation>", CommandDBReset, "", cobra.ExactArgs(1)))

	invalidGroup := func(use string) *cobra.Command {
		return &cobra.Command{
			Use:  use,
			Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				return errors.New(invalidCommandSyntaxMessage)
			},
		}
	}
	configValidationLeaf := func(use string, target CommandName, targetAction Action) *cobra.Command {
		return &cobra.Command{
			Use:  use,
			Args: cobra.NoArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				return execute(command, Invocation{
					Command: CommandConfig,
					Action:  ActionValidate,
					ConfigValidation: &ConfigValidationInput{
						Command: target,
						Action:  targetAction,
					},
				})
			},
		}
	}
	config := invalidGroup(string(CommandConfig))
	validate := invalidGroup(string(ActionValidate))
	for _, target := range []CommandName{
		CommandServe,
		CommandDoctor,
		CommandSync,
		CommandEntities,
		CommandReview,
		CommandAnalyze,
		CommandQuery,
		CommandDBMigrate,
		CommandDBStatus,
		CommandDBReset,
	} {
		validate.AddCommand(configValidationLeaf(string(target), target, ""))
	}
	configAuth := invalidGroup(string(CommandAuth))
	configAuth.AddCommand(configValidationLeaf(string(ActionAuthGoogle), CommandAuth, ActionAuthGoogle))
	configAuth.AddCommand(configValidationLeaf(string(ActionAuthGoogleDirectory), CommandAuth, ActionAuthGoogleDirectory))
	validate.AddCommand(configAuth)
	config.AddCommand(validate)
	root.AddCommand(config)

	auth := &cobra.Command{Use: string(CommandAuth)}
	auth.AddCommand(leaf(string(ActionAuthGoogle), CommandAuth, ActionAuthGoogle, cobra.NoArgs))
	auth.AddCommand(leaf(string(ActionAuthGoogleDirectory), CommandAuth, ActionAuthGoogleDirectory, cobra.NoArgs))
	root.AddCommand(auth)

	queryCommand := invalidGroup(string(CommandQuery))
	trend := &cobra.Command{
		Use:  string(ActionTrend),
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			input, err := parseTrendQuery(command)
			if err != nil {
				return err
			}
			return execute(command, Invocation{
				Command: CommandQuery,
				Action:  ActionTrend,
				Query:   &input,
			})
		},
	}
	trend.Flags().StringArray(queryEntityFlagName, nil, "canonical entity ID")
	trend.Flags().Var(newSingleStringFlag(string(query.EntityMatchAll)), queryEntityMatchFlagName, "entity matching policy")
	trend.Flags().StringArray(queryPredicateFlagName, nil, "exact observation predicate")
	trend.Flags().Var(newSingleStringFlag(""), queryBeforeFlagName, "before half-open RFC3339 window")
	trend.Flags().Var(newSingleStringFlag(""), queryAfterFlagName, "after half-open RFC3339 window")
	trend.Flags().Var(newSingleStringFlag(""), queryKnownAsOfFlagName, "recorded-time RFC3339 cutoff")
	trend.Flags().Var(newSingleStringFlag(string(QueryOutputText)), queryOutputFlagName, "output format")
	_ = trend.MarkFlagRequired(queryEntityFlagName)
	_ = trend.MarkFlagRequired(queryBeforeFlagName)
	_ = trend.MarkFlagRequired(queryAfterFlagName)
	queryCommand.AddCommand(trend)
	root.AddCommand(queryCommand)

	entities := &cobra.Command{Use: string(CommandEntities)}
	entities.AddCommand(leaf(string(ActionList), CommandEntities, ActionList, cobra.NoArgs))
	entities.AddCommand(leaf(string(ActionShow)+" <entity-id>", CommandEntities, ActionShow, cobra.ExactArgs(1)))
	root.AddCommand(entities)

	review := &cobra.Command{Use: string(CommandReview)}
	review.AddCommand(leaf(string(ActionList), CommandReview, ActionList, cobra.NoArgs))
	review.AddCommand(leaf(string(ActionShow)+" <proposal-id>", CommandReview, ActionShow, cobra.ExactArgs(1)))
	review.AddCommand(leaf(string(ActionAccept)+" <proposal-id> <entity-id>", CommandReview, ActionAccept, cobra.ExactArgs(2)))
	review.AddCommand(leaf(string(ActionReject)+" <proposal-id>", CommandReview, ActionReject, cobra.ExactArgs(1)))
	review.AddCommand(leaf(string(ActionCorrect)+" <effective-decision-id> <entity-id>", CommandReview, ActionCorrect, cobra.ExactArgs(2)))

	acceptDirectory := leaf(string(ActionAcceptDirectory)+" <proposal-id> <directory-profile-id>", CommandReview, ActionAcceptDirectory, cobra.ExactArgs(2))
	acceptDirectory.Flags().String(acceptDirectoryEntityFlagName, "", "existing entity ID")
	acceptDirectory.RunE = func(command *cobra.Command, values []string) error {
		if _, err := selectedConfigFile(command); err != nil {
			return err
		}
		*handled = true
		entityID, err := command.Flags().GetString(acceptDirectoryEntityFlagName)
		if err != nil {
			return fmt.Errorf("read %s flag: %w", acceptDirectoryEntityFlagName, err)
		}
		if command.Flags().Changed(acceptDirectoryEntityFlagName) && strings.TrimSpace(entityID) == "" {
			return fmt.Errorf("review accept-directory: --entity requires an entity ID")
		}
		return execute(command, Invocation{
			Command: CommandReview, Action: ActionAcceptDirectory, Arguments: values,
			AcceptDirectory: &AcceptDirectoryInput{
				ProposalID: values[0], DirectoryProfileID: values[1], EntityID: entityID,
			},
		})
	}
	review.AddCommand(acceptDirectory)

	create := leaf(string(ActionCreate)+" <proposal-id>", CommandReview, ActionCreate, cobra.ExactArgs(1))
	create.Flags().String(reviewCreateNameFlagName, "", "new person name")
	create.Flags().String(reviewCreateEmailFlagName, "", "new person email")
	_ = create.MarkFlagRequired(reviewCreateNameFlagName)
	create.RunE = func(command *cobra.Command, values []string) error {
		if _, err := selectedConfigFile(command); err != nil {
			return err
		}
		*handled = true
		name, err := command.Flags().GetString(reviewCreateNameFlagName)
		if err != nil {
			return fmt.Errorf("read %s flag: %w", reviewCreateNameFlagName, err)
		}
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("review create: --name is required")
		}
		email, err := command.Flags().GetString(reviewCreateEmailFlagName)
		if err != nil {
			return fmt.Errorf("read %s flag: %w", reviewCreateEmailFlagName, err)
		}
		return execute(command, Invocation{
			Command: CommandReview, Action: ActionCreate, Arguments: values,
			CreatePerson: &CreatePersonInput{Name: name, Email: email},
		})
	}
	review.AddCommand(create)
	root.AddCommand(review)

	return root
}

type singleStringFlag struct {
	value string
	set   bool
}

func newSingleStringFlag(defaultValue string) *singleStringFlag {
	return &singleStringFlag{value: defaultValue}
}

func (flag *singleStringFlag) Set(value string) error {
	if flag.set {
		return fmt.Errorf("flag may be provided only once")
	}
	flag.value = value
	flag.set = true
	return nil
}

func (flag *singleStringFlag) String() string {
	return flag.value
}

func (*singleStringFlag) Type() string {
	return "string"
}
