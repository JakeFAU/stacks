package cli

import (
	"encoding/json"
	"errors"

	"stacks/internal/queryplan"
)

type queryAskEnvelopeJSON struct {
	SchemaVersion string              `json:"schema_version"`
	ReferenceTime string              `json:"reference_time"`
	Plan          queryAskPlanJSON    `json:"plan"`
	Planner       queryAskPlannerJSON `json:"planner"`
	Query         queryEnvelopeJSON   `json:"query"`
}

type queryAskPlanJSON struct {
	Intent  string           `json:"intent"`
	Request queryRequestJSON `json:"request"`
}

type queryAskUsageJSON struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type queryAskPlannerJSON struct {
	Provider               string            `json:"provider"`
	ModelID                string            `json:"model_id"`
	PromptVersion          string            `json:"prompt_version"`
	SchemaName             string            `json:"schema_name"`
	Attempts               int               `json:"attempts"`
	Usage                  queryAskUsageJSON `json:"usage"`
	WallLatencySeconds     float64           `json:"wall_latency_seconds"`
	ProviderLatencySeconds float64           `json:"provider_latency_seconds"`
}

func renderQueryAskJSON(execution queryplan.Execution) ([]byte, error) {
	if err := validateQueryAskExecution(execution); err != nil {
		return nil, errors.New("query ask execution is invalid")
	}
	request, err := queryRequestToJSON(execution.Request)
	if err != nil {
		return nil, errors.New("query ask request is invalid")
	}
	queryEnvelope, err := queryEnvelopeToJSON(execution.Result)
	if err != nil {
		return nil, errors.New("query ask result is invalid")
	}
	rendered, err := json.Marshal(queryAskEnvelopeJSON{
		SchemaVersion: execution.SchemaVersion,
		ReferenceTime: queryTime(execution.ReferenceTime),
		Plan:          queryAskPlanJSON{Intent: string(execution.Request.Intent), Request: request},
		Planner: queryAskPlannerJSON{
			Provider: string(execution.Planner.Provider), ModelID: execution.Planner.ModelID,
			PromptVersion: execution.Planner.PromptVersion, SchemaName: execution.Planner.SchemaName,
			Attempts: execution.Planner.Attempts,
			Usage: queryAskUsageJSON{
				InputTokens: execution.Planner.Usage.InputTokens, OutputTokens: execution.Planner.Usage.OutputTokens,
				TotalTokens: execution.Planner.Usage.TotalTokens,
			},
			WallLatencySeconds:     execution.Planner.WallLatency.Seconds(),
			ProviderLatencySeconds: execution.Planner.ProviderLatency.Seconds(),
		},
		Query: queryEnvelope,
	})
	if err != nil {
		return nil, errors.New("render query ask JSON failed")
	}
	return append(rendered, '\n'), nil
}
