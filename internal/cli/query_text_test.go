package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestQueryTextShowsCompleteTrendMaterialAndRoleSeparatedCitations(t *testing.T) {
	rendered, err := renderQueryText(populatedTrendResult(t, false))
	if err != nil {
		t.Fatalf("renderQueryText() error = %v", err)
	}
	text := string(rendered)
	for _, want := range []string{
		"intent: trend-comparison",
		"before window: [2026-01-01T13:09:10.654321Z, 2026-02-01T13:09:10.654321Z)",
		"after window: [2026-03-01T13:09:10.654321Z, 2026-04-01T13:09:10.654321Z)",
		"knowledge scope: as-of 2026-04-30T23:02:03.987654Z",
		"before facts:",
		"after facts:",
		"changes:",
		"unresolved:",
		"unresolved keys:",
		"gaps:",
		"contributions:",
		"supporting citations:",
		"contradicting citations:",
		"kind=changed",
		"kind=removed",
		"kind=added",
		"reason=conflicting-values",
		"reason=hypothesized",
		"kind=no-evidence",
		"kind=valid-time-excluded entity_id=entity-b predicate=b.removed selection_label=after",
		`text="exact synthetic bytes"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text output missing %q:\n%s", want, text)
		}
	}
}

func TestQueryRendererTextAndJSONExposeIdenticalStableIDsAndRoles(t *testing.T) {
	result := populatedTrendResult(t, false)
	textOutput, err := renderQueryText(result)
	if err != nil {
		t.Fatal(err)
	}
	jsonOutput, err := renderQueryJSON(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{
		"observation-changed-a",
		"observation-changed-b",
		"observation-after",
		"observation-added",
		"observation-removed",
		"observation-conflict-a",
		"observation-conflict-b",
		"observation-hypothesis",
		"evidence-support-a",
		"evidence-support-b",
		"evidence-counter-a",
		"evidence-after",
		"evidence-added",
		"evidence-removed",
		"evidence-conflict-a",
		"evidence-conflict-b",
		"evidence-hypothesis",
		"supporting",
		"contradicting",
		"a.changed",
		"b.removed",
		"c.added",
		"d.conflict",
		"e.hypothesis",
		"no-evidence",
		"valid-time-excluded",
	} {
		if !bytes.Contains(textOutput, []byte(token)) {
			t.Errorf("text output missing parity token %q", token)
		}
		if !bytes.Contains(jsonOutput, []byte(token)) {
			t.Errorf("JSON output missing parity token %q", token)
		}
	}
}

func TestQueryTextIsDeterministicAcrossReorderedCanonicalInputs(t *testing.T) {
	first, err := renderQueryText(populatedTrendResult(t, false))
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderQueryText(populatedTrendResult(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("reordered result bytes differ:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
