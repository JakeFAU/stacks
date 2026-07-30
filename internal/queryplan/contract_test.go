package queryplan

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
)

func TestPlannerContractConstants(t *testing.T) {
	if PromptVersion != "query-plan-v1" {
		t.Fatalf("PromptVersion = %q", PromptVersion)
	}
	if SchemaName != "temporal_query_plan_v1" {
		t.Fatalf("SchemaName = %q", SchemaName)
	}
	if OutputSchemaVersion != "query-ask-v1" {
		t.Fatalf("OutputSchemaVersion = %q", OutputSchemaVersion)
	}
}

func TestPromptContractIsExactAndIsolated(t *testing.T) {
	first, err := PromptContract(PromptVersion)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PromptContract(PromptVersion)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != "query-plan-v1" || first.SchemaName != "temporal_query_plan_v1" {
		t.Fatalf("contract identity = %#v", first)
	}
	if first.SystemPrompt == "" {
		t.Fatal("PromptContract() returned a blank prompt")
	}
	if !json.Valid(first.JSONSchema) {
		t.Fatalf("PromptContract() returned invalid JSON Schema: %q", first.JSONSchema)
	}
	first.JSONSchema[0] ^= 0xff
	if bytes.Equal(first.JSONSchema, second.JSONSchema) {
		t.Fatal("PromptContract() returned aliased schema bytes")
	}
	if _, err := PromptContract("query-plan-v2"); err == nil {
		t.Fatal("PromptContract(query-plan-v2) error = nil")
	}
}

func TestUsageValidRejectsNegativeAndIncompleteTotals(t *testing.T) {
	for _, usage := range []Usage{
		{InputTokens: -1, OutputTokens: 0, TotalTokens: 0},
		{InputTokens: 0, OutputTokens: -1, TotalTokens: 0},
		{InputTokens: 0, OutputTokens: 0, TotalTokens: -1},
		{InputTokens: 2, OutputTokens: 3, TotalTokens: 4},
	} {
		if usage.valid() {
			t.Fatalf("Usage%+v.valid() = true", usage)
		}
	}
	if !(Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}).valid() {
		t.Fatal("Usage.valid() = false for complete nonnegative usage")
	}
}

func TestUsageValidRejectsOverflowedTokenSum(t *testing.T) {
	usage := Usage{InputTokens: math.MaxInt64, OutputTokens: 1, TotalTokens: math.MaxInt64}
	if usage.valid() {
		t.Fatalf("Usage%+v.valid() = true", usage)
	}
}
