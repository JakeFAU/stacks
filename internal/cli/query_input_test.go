package cli

import (
	"testing"

	"github.com/JakeFAU/stacks/core/temporal"
	"stacks/internal/query"
)

func TestQueryInputDefinesExactSupportedOutputs(t *testing.T) {
	if QueryOutputText != QueryOutput("text") {
		t.Fatalf("QueryOutputText = %q, want text", QueryOutputText)
	}
	if QueryOutputJSON != QueryOutput("json") {
		t.Fatalf("QueryOutputJSON = %q, want json", QueryOutputJSON)
	}

	input := QueryInput{
		Request: query.Request{Intent: temporal.IntentTrendComparison},
		Output:  QueryOutputJSON,
	}
	if input.Request.Intent != temporal.IntentTrendComparison || input.Output != QueryOutputJSON {
		t.Fatalf("QueryInput = %#v, want typed trend JSON input", input)
	}
}
