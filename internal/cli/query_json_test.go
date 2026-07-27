package cli

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"

	"stacks/internal/query"
)

func TestQueryJSONUsesExactV1TrendWireShape(t *testing.T) {
	rendered, err := renderQueryJSON(populatedTrendResult(t, false))
	if err != nil {
		t.Fatalf("renderQueryJSON() error = %v", err)
	}
	if !bytes.HasSuffix(rendered, []byte("\n")) {
		t.Fatalf("renderQueryJSON() = %q, want trailing newline", rendered)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(rendered, &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	assertJSONKeys(t, envelope, "gaps", "intent", "request", "result", "schema_version")
	assertJSONLiteral(t, envelope["schema_version"], `"stacks.temporal-query.v1"`)
	assertJSONLiteral(t, envelope["intent"], `"trend-comparison"`)

	var request map[string]json.RawMessage
	mustUnmarshalJSON(t, envelope["request"], &request)
	assertJSONKeys(t, request, "entity_ids", "entity_match", "knowledge_scope", "limit", "predicates", "selections")
	assertJSONLiteral(t, request["entity_ids"], `["entity-a","entity-b"]`)
	assertJSONLiteral(t, request["entity_match"], `"all"`)
	assertJSONLiteral(t, request["predicates"], `["a.changed","b.removed","c.added","d.conflict","e.hypothesis"]`)
	assertJSONLiteral(t, request["limit"], `0`)
	assertJSONLiteral(t, request["knowledge_scope"], `{"kind":"as-of","at":"2026-04-30T23:02:03.987654Z"}`)
	assertJSONLiteral(t, request["selections"], `[{"kind":"window","label":"before","start":"2026-01-01T13:09:10.654321Z","end":"2026-02-01T13:09:10.654321Z"},{"kind":"window","label":"after","start":"2026-03-01T13:09:10.654321Z","end":"2026-04-01T13:09:10.654321Z"}]`)

	var result map[string]json.RawMessage
	mustUnmarshalJSON(t, envelope["result"], &result)
	assertJSONKeys(t, result, "trend")
	var trend map[string]json.RawMessage
	mustUnmarshalJSON(t, result["trend"], &trend)
	assertJSONKeys(t, trend, "after", "before", "changes", "unresolved_keys")

	var before map[string]json.RawMessage
	mustUnmarshalJSON(t, trend["before"], &before)
	assertJSONKeys(t, before, "facts", "selection", "unresolved")
	var facts []map[string]json.RawMessage
	mustUnmarshalJSON(t, before["facts"], &facts)
	if len(facts) != 2 {
		t.Fatalf("before facts length = %d, want 2", len(facts))
	}
	changedFact := facts[1]
	assertJSONKeys(t, changedFact, "contradicting_citations", "contributions", "key", "supporting_citations", "value")
	assertJSONLiteral(t, changedFact["key"], `{"subject":{"kind":"entity","entity_id":"entity-a"},"predicate":"a.changed"}`)
	assertJSONLiteral(t, changedFact["value"], `{"kind":"text","text":"remote"}`)

	var contributions []map[string]json.RawMessage
	mustUnmarshalJSON(t, changedFact["contributions"], &contributions)
	if len(contributions) != 2 {
		t.Fatalf("contributions length = %d, want 2", len(contributions))
	}
	assertJSONKeys(t, contributions[0], "derivation", "observation_id", "recorded_at", "status", "valid_time")
	assertJSONLiteral(t, contributions[0]["valid_time"], `{"kind":"unknown"}`)
	assertJSONLiteral(t, contributions[0]["recorded_at"], `"2026-06-01T09:06:07.123456Z"`)
	assertJSONKeys(t, contributions[1], "derivation", "object_grounding_mention_id", "observation_id", "recorded_at", "status", "subject_grounding_mention_id", "valid_time")
	assertJSONLiteral(t, contributions[1]["valid_time"], `{"kind":"instant","at":"2026-01-16T13:09:10.654321Z"}`)
	assertJSONLiteral(t, contributions[1]["derivation"], `{"method":"extract","version":"v2","run_id":"run-2","model":"synthetic-model","prompt_version":"prompt-v3"}`)

	var supporting []map[string]json.RawMessage
	mustUnmarshalJSON(t, changedFact["supporting_citations"], &supporting)
	if len(supporting) != 2 {
		t.Fatalf("supporting citations length = %d, want 2", len(supporting))
	}
	assertJSONKeys(t, supporting[0], "document_version_id", "end_offset", "evidence_id", "role", "section_id", "section_order", "section_path", "section_role", "section_title", "source_document_id", "start_offset")
	assertJSONLiteral(t, supporting[0]["section_path"], `[]`)
	if _, exists := supporting[0]["locator"]; exists {
		t.Fatal("optional locator is present when absent")
	}
	if _, exists := supporting[0]["text"]; exists {
		t.Fatal("optional text is present when absent")
	}
	assertJSONKeys(t, supporting[1], "document_version_id", "end_offset", "evidence_id", "locator", "role", "section_id", "section_order", "section_path", "section_role", "section_title", "source_document_id", "start_offset", "text")
	assertJSONLiteral(t, supporting[1]["text"], `"exact synthetic bytes"`)

	var contradicting []map[string]json.RawMessage
	mustUnmarshalJSON(t, changedFact["contradicting_citations"], &contradicting)
	if len(contradicting) != 1 {
		t.Fatalf("contradicting citations length = %d, want 1", len(contradicting))
	}
	assertJSONLiteral(t, contradicting[0]["role"], `"contradicting"`)

	var changes []map[string]json.RawMessage
	mustUnmarshalJSON(t, trend["changes"], &changes)
	if len(changes) != 3 {
		t.Fatalf("changes length = %d, want 3", len(changes))
	}
	assertJSONKeys(t, changes[0], "after", "key", "kind")
	assertJSONLiteral(t, changes[0]["kind"], `"added"`)
	assertJSONKeys(t, changes[1], "before", "key", "kind")
	assertJSONLiteral(t, changes[1]["kind"], `"removed"`)
	assertJSONKeys(t, changes[2], "after", "before", "key", "kind")
	assertJSONLiteral(t, changes[2]["kind"], `"changed"`)

	var unresolved []map[string]json.RawMessage
	mustUnmarshalJSON(t, before["unresolved"], &unresolved)
	if len(unresolved) != 1 {
		t.Fatalf("before unresolved length = %d, want 1", len(unresolved))
	}
	assertJSONKeys(t, unresolved[0], "candidates", "key", "reason")
	assertJSONLiteral(t, unresolved[0]["reason"], `"conflicting-values"`)

	assertJSONLiteral(t, trend["unresolved_keys"], `[{"subject":{"kind":"text","text":"hypothesis subject"},"predicate":"e.hypothesis"},{"subject":{"kind":"entity","entity_id":"entity-b"},"predicate":"d.conflict"}]`)
	assertJSONLiteral(t, envelope["gaps"], `[{"kind":"no-evidence"},{"kind":"valid-time-excluded","entity_id":"entity-b","predicate":"b.removed","selection_label":"after"}]`)
	if bytes.Contains(rendered, []byte(`null`)) {
		t.Fatalf("rendered JSON contains null: %s", rendered)
	}
}

func TestQueryJSONEncodesEveryClosedTermAndExtentShape(t *testing.T) {
	terms := []struct {
		name string
		term observation.Term
		want string
	}{
		{name: "absent", term: observation.AbsentTerm(), want: `{"kind":"absent"}`},
		{name: "text", term: mustQueryText(t, "exact bytes"), want: `{"kind":"text","text":"exact bytes"}`},
		{name: "mention", term: mustQueryMention(t, "mention-a"), want: `{"kind":"mention","mention_id":"mention-a"}`},
		{name: "entity", term: mustQueryEntity(t, "entity-a"), want: `{"kind":"entity","entity_id":"entity-a"}`},
	}
	for _, test := range terms {
		t.Run(test.name, func(t *testing.T) {
			dto, err := queryTermJSON(test.term)
			if err != nil {
				t.Fatalf("queryTermJSON() error = %v", err)
			}
			got, err := json.Marshal(dto)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("term JSON = %s, want %s", got, test.want)
			}
		})
	}

	instant := testInstant(2026, 1, 2)
	end := testInstant(2026, 1, 3)
	extents := []struct {
		name   string
		extent observation.TemporalExtent
		want   string
	}{
		{name: "unknown", extent: observation.UnknownTime(), want: `{"kind":"unknown"}`},
		{name: "instant", extent: mustQueryInstant(t, instant), want: `{"kind":"instant","at":"2026-01-02T13:09:10.654321Z"}`},
		{name: "interval start", extent: mustQuerySince(t, instant), want: `{"kind":"interval","start":"2026-01-02T13:09:10.654321Z"}`},
		{name: "interval end", extent: mustQueryUntil(t, end), want: `{"kind":"interval","end":"2026-01-03T13:09:10.654321Z"}`},
		{name: "interval bounded", extent: mustQueryDuring(t, instant, end), want: `{"kind":"interval","start":"2026-01-02T13:09:10.654321Z","end":"2026-01-03T13:09:10.654321Z"}`},
		{name: "window", extent: mustQueryWithin(t, instant, end), want: `{"kind":"window","start":"2026-01-02T13:09:10.654321Z","end":"2026-01-03T13:09:10.654321Z"}`},
	}
	for _, test := range extents {
		t.Run(test.name, func(t *testing.T) {
			dto, err := queryExtentToJSON(test.extent)
			if err != nil {
				t.Fatalf("queryExtentToJSON() error = %v", err)
			}
			got, err := json.Marshal(dto)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("extent JSON = %s, want %s", got, test.want)
			}
		})
	}
}

func TestQueryJSONEncodesEveryEmptyCollectionAsArray(t *testing.T) {
	base := populatedTrendResult(t, false)
	payload, err := query.NewTrendPayload(query.TrendResult{
		Before:         query.WindowResult{Selection: base.Selections[0], Facts: []query.Fact{}, Unresolved: []query.UnresolvedItem{}},
		After:          query.WindowResult{Selection: base.Selections[1], Facts: []query.Fact{}, Unresolved: []query.UnresolvedItem{}},
		Changes:        []query.Change{},
		UnresolvedKeys: []temporal.StateKey{},
	})
	if err != nil {
		t.Fatal(err)
	}
	base.Predicates = []observation.Predicate{}
	base.Payload = payload
	base.Gaps = []query.Gap{}

	rendered, err := renderQueryJSON(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"predicates":[]`,
		`"facts":[]`,
		`"unresolved":[]`,
		`"changes":[]`,
		`"unresolved_keys":[]`,
		`"gaps":[]`,
	} {
		if !bytes.Contains(rendered, []byte(want)) {
			t.Fatalf("rendered JSON missing empty array %q: %s", want, rendered)
		}
	}
	if bytes.Contains(rendered, []byte(`null`)) {
		t.Fatalf("rendered JSON contains null: %s", rendered)
	}
}

func TestQueryJSONIsDeterministicAcrossReorderedCanonicalInputs(t *testing.T) {
	first, err := renderQueryJSON(populatedTrendResult(t, false))
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderQueryJSON(populatedTrendResult(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("reordered result bytes differ:\nfirst:  %s\nsecond: %s", first, second)
	}
}

func TestQueryJSONResultUnionRejectsMismatchedTagAndMember(t *testing.T) {
	trend := queryTrendJSON{}
	for _, value := range []queryResultJSON{
		{Intent: temporal.IntentTrendComparison},
		{Intent: temporal.IntentPointInTime, Trend: &trend},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("json.Marshal() error = nil, want invalid fixed union error")
		}
	}
}

func assertJSONKeys(t *testing.T, value map[string]json.RawMessage, want ...string) {
	t.Helper()
	got := make([]string, 0, len(value))
	for key := range value {
		got = append(got, key)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("JSON keys = %v, want %v", got, want)
	}
}

func assertJSONLiteral(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}

func mustUnmarshalJSON(t *testing.T, value json.RawMessage, target interface{}) {
	t.Helper()
	if err := json.Unmarshal(value, target); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", value, err)
	}
}
