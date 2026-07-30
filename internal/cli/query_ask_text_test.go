package cli

import (
	"strings"
	"testing"
)

func TestQueryAskTextPrefixesExistingDeterministicRendering(t *testing.T) {
	execution := validQueryAskExecution(t)
	got, err := renderQueryAskText(execution)
	if err != nil {
		t.Fatalf("renderQueryAskText() error = %v", err)
	}
	result, err := renderQueryText(execution.Result)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"query ask schema: query-ask-v1\n", "reference time: 2026-07-29T16:00:00Z\n",
		"planner: provider=openai model=synthetic-planner-model prompt=query-plan-v1 schema=temporal_query_plan_v1 attempts=1\n",
		"planner usage: input_tokens=120 output_tokens=40 total_tokens=160\n",
		"planner latency: wall_seconds=0.250000 provider_seconds=0.200000\n",
		"validated plan:\n", "deterministic result:\n", string(result),
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}
