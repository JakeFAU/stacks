package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/JakeFAU/stacks/core/temporal"

	"stacks/internal/query"
)

// QueryService executes one provider-neutral temporal query.
type QueryService interface {
	Query(context.Context, query.Request) (query.Result, error)
}

// QueryCommand executes and renders one typed temporal query invocation.
type QueryCommand struct {
	Service QueryService
	Output  io.Writer
}

// ValidateQueryInvocation validates the currently supported query transport
// shape without executing the query.
func ValidateQueryInvocation(invocation Invocation) error {
	if invocation.Command != CommandQuery || invocation.Action != ActionTrend ||
		invocation.Query == nil || len(invocation.Arguments) != 0 {
		return fmt.Errorf("query command: invocation is invalid")
	}
	if invocation.Query.Output != QueryOutputText && invocation.Query.Output != QueryOutputJSON {
		return fmt.Errorf("query command: output is invalid")
	}
	if invocation.Query.Request.Intent != temporal.IntentTrendComparison {
		return fmt.Errorf("query command: request intent is invalid")
	}
	return nil
}

// Run executes the currently supported trend query leaf.
func (command QueryCommand) Run(ctx context.Context, invocation Invocation) error {
	if err := ValidateQueryInvocation(invocation); err != nil {
		return err
	}
	if command.Service == nil {
		return fmt.Errorf("query command: service is not configured")
	}

	result, err := command.Service.Query(ctx, invocation.Query.Request)
	if err != nil {
		return err
	}

	var rendered []byte
	switch invocation.Query.Output {
	case QueryOutputText:
		rendered, err = renderQueryText(result)
	case QueryOutputJSON:
		rendered, err = renderQueryJSON(result)
	default:
		return fmt.Errorf("query command: output is invalid")
	}
	if err != nil {
		return err
	}

	output := command.Output
	if output == nil {
		output = io.Discard
	}
	written, err := output.Write(rendered)
	if err == nil && written != len(rendered) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("write query result: %w", err)
	}
	return nil
}
