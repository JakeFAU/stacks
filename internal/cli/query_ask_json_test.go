package cli

import (
	"bytes"
	"testing"
)

func TestQueryAskJSONContainsAuditedPlanAndExactQueryEnvelope(t *testing.T) {
	execution := validQueryAskExecution(t)
	got, err := renderQueryAskJSON(execution)
	if err != nil {
		t.Fatalf("renderQueryAskJSON() error = %v", err)
	}
	query, err := renderQueryJSON(execution.Result)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`"schema_version":"query-ask-v1"`)) ||
		!bytes.Contains(got, []byte(`"reference_time":"2026-07-29T16:00:00Z"`)) ||
		!bytes.Contains(got, []byte(`"provider":"openai"`)) ||
		!bytes.Contains(got, query[:len(query)-1]) {
		t.Fatalf("rendered audit JSON omits required envelope: %s", got)
	}
	for _, forbidden := range []string{"What was assigned", "raw proposal", "question"} {
		if bytes.Contains(got, []byte(forbidden)) {
			t.Fatalf("rendered JSON exposed %q: %s", forbidden, got)
		}
	}
}
