package cli

import (
	"errors"
	"fmt"
	"strings"

	"stacks/internal/queryplan"
)

func renderQueryAskText(execution queryplan.Execution) ([]byte, error) {
	if err := validateQueryAskExecution(execution); err != nil {
		return nil, errors.New("query ask execution is invalid")
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "query ask schema: %s\n", execution.SchemaVersion)
	fmt.Fprintf(&rendered, "reference time: %s\n", queryTime(execution.ReferenceTime))
	fmt.Fprintf(&rendered, "planner: provider=%s model=%s prompt=%s schema=%s attempts=%d\n", execution.Planner.Provider, execution.Planner.ModelID, execution.Planner.PromptVersion, execution.Planner.SchemaName, execution.Planner.Attempts)
	fmt.Fprintf(&rendered, "planner usage: input_tokens=%d output_tokens=%d total_tokens=%d\n", execution.Planner.Usage.InputTokens, execution.Planner.Usage.OutputTokens, execution.Planner.Usage.TotalTokens)
	fmt.Fprintf(&rendered, "planner latency: wall_seconds=%.6f provider_seconds=%.6f\n", execution.Planner.WallLatency.Seconds(), execution.Planner.ProviderLatency.Seconds())
	rendered.WriteString("validated plan:\n")
	if err := renderQueryRequestText(&rendered, execution.Request); err != nil {
		return nil, errors.New("query ask request is invalid")
	}
	rendered.WriteString("deterministic result:\n")
	if err := renderQueryRequestText(&rendered, execution.Request); err != nil {
		return nil, errors.New("query ask request is invalid")
	}
	if err := renderQueryResultText(&rendered, execution.Result); err != nil {
		return nil, errors.New("query ask result is invalid")
	}
	return []byte(rendered.String()), nil
}
