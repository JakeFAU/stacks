package queryplan

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestPlannerContractConstants(t *testing.T) {
	if PromptVersion != "query-plan-v2" {
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
	if first.Version != "query-plan-v2" || first.SchemaName != "temporal_query_plan_v1" {
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
	if _, err := PromptContract("query-plan-v1"); err == nil {
		t.Fatal("PromptContract(query-plan-v1) error = nil")
	}
}

func TestPromptContractV2SeparatesCurrentFromRecordedTime(t *testing.T) {
	contract, err := PromptContract("query-plan-v2")
	if err != nil {
		t.Fatal(err)
	}
	const requiredInstruction = `For an executable request without an explicit recorded-time knowledge cutoff, emit knowledge_scope.kind as "current" and knowledge_scope.as_of as the empty string. Use "as-of" and a non-empty as_of only when the question explicitly asks what was known or recorded by a cutoff.`
	if contract.Version != "query-plan-v2" {
		t.Fatalf("contract version = %q", contract.Version)
	}
	if !strings.Contains(contract.SystemPrompt, requiredInstruction) {
		t.Fatal("query-plan-v2 does not define the current-versus-as-of sentinel rule")
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
