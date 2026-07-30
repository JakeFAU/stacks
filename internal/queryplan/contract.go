// Package queryplan defines the provider-neutral natural-language planning boundary.
package queryplan

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/JakeFAU/stacks/core/identity"
	"stacks/internal/modelpolicy"
	"stacks/internal/query"
)

const (
	// PromptVersion identifies the embedded planner instruction contract.
	PromptVersion = "query-plan-v1"
	// SchemaName identifies the structured model-output schema.
	SchemaName = "temporal_query_plan_v1"
	// OutputSchemaVersion identifies an executable planner output.
	OutputSchemaVersion = "query-ask-v1"
)

// Input is the private question and local query context submitted to a planner.
type Input struct {
	Question      string
	EntityIDs     []identity.EntityID
	ReferenceTime time.Time
}

// Usage records model token accounting.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}

func (usage Usage) valid() bool {
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.TotalTokens < 0 {
		return false
	}
	return usage.TotalTokens >= usage.InputTokens && usage.TotalTokens-usage.InputTokens >= usage.OutputTokens
}

// ModelRequest is the complete provider-neutral model-planning input.
type ModelRequest struct {
	PromptVersion string
	SystemPrompt  string
	Input         string
	SchemaName    string
	JSONSchema    []byte
}

// ModelResponse is one provider-neutral structured-planning response.
type ModelResponse struct {
	Output          json.RawMessage
	Provider        modelpolicy.Provider
	ModelID         string
	PromptVersion   string
	SchemaName      string
	Usage           Usage
	Attempts        int
	WallLatency     time.Duration
	ProviderLatency time.Duration
}

// Model proposes one structured temporal query plan.
type Model interface {
	Plan(context.Context, ModelRequest) (ModelResponse, error)
}

// Executor executes one normalized temporal query request.
type Executor interface {
	Query(context.Context, query.Request) (query.Result, error)
}

// PlannerMetadata records the provider and accounting provenance of a plan.
type PlannerMetadata struct {
	Provider        modelpolicy.Provider
	ModelID         string
	PromptVersion   string
	SchemaName      string
	Usage           Usage
	Attempts        int
	WallLatency     time.Duration
	ProviderLatency time.Duration
}

// Execution is one executable plan and its cited deterministic query result.
type Execution struct {
	SchemaVersion string
	ReferenceTime time.Time
	Request       query.Request
	Planner       PlannerMetadata
	Result        query.Result
}

// Contract is the immutable prompt and schema supplied to a planner model.
type Contract struct {
	Version      string
	SystemPrompt string
	SchemaName   string
	JSONSchema   []byte
}

// PromptContract returns the supported immutable planner contract.
func PromptContract(version string) (Contract, error) {
	if version != PromptVersion {
		return Contract{}, fmt.Errorf("query plan prompt version is unsupported")
	}
	return Contract{
		Version:      PromptVersion,
		SystemPrompt: queryPlanPrompt,
		SchemaName:   SchemaName,
		JSONSchema:   append([]byte(nil), queryPlanSchema...),
	}, nil
}
