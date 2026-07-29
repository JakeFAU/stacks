package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/identity"
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

func TestQueryJSONPreservesExactTrendAssociationsWithoutExtras(t *testing.T) {
	rendered, err := renderQueryJSON(populatedTrendResult(t, false))
	if err != nil {
		t.Fatalf("renderQueryJSON() error = %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(rendered))
	decoder.DisallowUnknownFields()
	var got queryJSONTestEnvelope
	if err := decoder.Decode(&got); err != nil {
		t.Fatalf("strict JSON decode error = %v", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("strict JSON trailing decode error = %v, want EOF", err)
	}

	removedCitation := queryJSONTestCitation{
		EvidenceID: "evidence-removed", Role: "supporting",
		SourceDocumentID: "document-evidence-removed", DocumentVersionID: "version-evidence-removed",
		SectionID: "section-evidence-removed", SectionTitle: "Synthetic section",
		SectionPath: []string{}, SectionOrder: 2, SectionRole: "body", StartOffset: 3, EndOffset: 11,
	}
	supportACitation := queryJSONTestCitation{
		EvidenceID: "evidence-support-a", Role: "supporting",
		SourceDocumentID: "document-evidence-support-a", DocumentVersionID: "version-evidence-support-a",
		SectionID: "section-evidence-support-a", SectionTitle: "Synthetic section",
		SectionPath: []string{}, SectionOrder: 2, SectionRole: "body", StartOffset: 3, EndOffset: 11,
	}
	supportBCitation := queryJSONTestCitation{
		EvidenceID: "evidence-support-b", Role: "supporting",
		SourceDocumentID: "document-evidence-support-b", DocumentVersionID: "version-evidence-support-b",
		SectionID: "section-evidence-support-b", SectionTitle: "Synthetic section",
		SectionPath: []string{"Parent", "Child"}, SectionOrder: 2, SectionRole: "body", StartOffset: 3, EndOffset: 11,
		Locator: testStringPointer("synthetic://document/evidence-support-b"),
		Text:    testStringPointer("exact synthetic bytes"),
	}
	counterACitation := queryJSONTestCitation{
		EvidenceID: "evidence-counter-a", Role: "contradicting",
		SourceDocumentID: "document-evidence-counter-a", DocumentVersionID: "version-evidence-counter-a",
		SectionID: "section-evidence-counter-a", SectionTitle: "Synthetic section",
		SectionPath: []string{"Parent", "Child"}, SectionOrder: 2, SectionRole: "body", StartOffset: 3, EndOffset: 11,
		Locator: testStringPointer("synthetic://document/evidence-counter-a"),
		Text:    testStringPointer("exact synthetic bytes"),
	}
	conflictACitation := queryJSONTestCitation{
		EvidenceID: "evidence-conflict-a", Role: "supporting",
		SourceDocumentID: "document-evidence-conflict-a", DocumentVersionID: "version-evidence-conflict-a",
		SectionID: "section-evidence-conflict-a", SectionTitle: "Synthetic section",
		SectionPath: []string{}, SectionOrder: 2, SectionRole: "body", StartOffset: 3, EndOffset: 11,
	}
	conflictBCitation := queryJSONTestCitation{
		EvidenceID: "evidence-conflict-b", Role: "supporting",
		SourceDocumentID: "document-evidence-conflict-b", DocumentVersionID: "version-evidence-conflict-b",
		SectionID: "section-evidence-conflict-b", SectionTitle: "Synthetic section",
		SectionPath: []string{}, SectionOrder: 2, SectionRole: "body", StartOffset: 3, EndOffset: 11,
	}
	addedCitation := queryJSONTestCitation{
		EvidenceID: "evidence-added", Role: "supporting",
		SourceDocumentID: "document-evidence-added", DocumentVersionID: "version-evidence-added",
		SectionID: "section-evidence-added", SectionTitle: "Synthetic section",
		SectionPath: []string{}, SectionOrder: 2, SectionRole: "body", StartOffset: 3, EndOffset: 11,
	}
	afterCitation := queryJSONTestCitation{
		EvidenceID: "evidence-after", Role: "supporting",
		SourceDocumentID: "document-evidence-after", DocumentVersionID: "version-evidence-after",
		SectionID: "section-evidence-after", SectionTitle: "Synthetic section",
		SectionPath: []string{"Parent", "Child"}, SectionOrder: 2, SectionRole: "body", StartOffset: 3, EndOffset: 11,
		Locator: testStringPointer("synthetic://document/evidence-after"),
		Text:    testStringPointer("exact synthetic bytes"),
	}
	hypothesisCitation := queryJSONTestCitation{
		EvidenceID: "evidence-hypothesis", Role: "contradicting",
		SourceDocumentID: "document-evidence-hypothesis", DocumentVersionID: "version-evidence-hypothesis",
		SectionID: "section-evidence-hypothesis", SectionTitle: "Synthetic section",
		SectionPath: []string{}, SectionOrder: 2, SectionRole: "body", StartOffset: 3, EndOffset: 11,
	}

	const recordedAt = "2026-06-01T09:06:07.123456Z"
	removedKey := queryJSONTestStateKey{
		Subject:   queryJSONTestTerm{Kind: "text", Text: testStringPointer("subject bytes")},
		Predicate: "b.removed",
	}
	changedKey := queryJSONTestStateKey{
		Subject:   queryJSONTestTerm{Kind: "entity", EntityID: testStringPointer("entity-a")},
		Predicate: "a.changed",
	}
	conflictKey := queryJSONTestStateKey{
		Subject:   queryJSONTestTerm{Kind: "entity", EntityID: testStringPointer("entity-b")},
		Predicate: "d.conflict",
	}
	addedKey := queryJSONTestStateKey{
		Subject:   queryJSONTestTerm{Kind: "absent"},
		Predicate: "c.added",
	}
	hypothesisKey := queryJSONTestStateKey{
		Subject:   queryJSONTestTerm{Kind: "text", Text: testStringPointer("hypothesis subject")},
		Predicate: "e.hypothesis",
	}

	removedFact := queryJSONTestFact{
		Key:   removedKey,
		Value: queryJSONTestTerm{Kind: "entity", EntityID: testStringPointer("entity-owner")},
		Contributions: []queryJSONTestContribution{{
			ObservationID: "observation-removed", Status: "hypothesized",
			ValidTime:  queryJSONTestExtent{Kind: "interval", Start: testStringPointer("2025-12-01T13:09:10.654321Z")},
			RecordedAt: recordedAt, Derivation: queryJSONTestDerivation{Method: "extract", Version: "v1"},
		}},
		SupportingCitations: []queryJSONTestCitation{removedCitation}, ContradictingCitations: []queryJSONTestCitation{},
	}
	changedBeforeFact := queryJSONTestFact{
		Key: changedKey, Value: queryJSONTestTerm{Kind: "text", Text: testStringPointer("remote")},
		Contributions: []queryJSONTestContribution{
			{
				ObservationID: "observation-changed-a", Status: "observed",
				ValidTime: queryJSONTestExtent{Kind: "unknown"}, RecordedAt: recordedAt,
				Derivation: queryJSONTestDerivation{Method: "manual", Version: "v1"},
			},
			{
				ObservationID: "observation-changed-b", Status: "inferred",
				ValidTime:  queryJSONTestExtent{Kind: "instant", At: testStringPointer("2026-01-16T13:09:10.654321Z")},
				RecordedAt: recordedAt,
				Derivation: queryJSONTestDerivation{
					Method: "extract", Version: "v2", RunID: testStringPointer("run-2"),
					Model: testStringPointer("synthetic-model"), PromptVersion: testStringPointer("prompt-v3"),
				},
				SubjectGroundingMentionID: testStringPointer("mention-subject"),
				ObjectGroundingMentionID:  testStringPointer("mention-object"),
			},
		},
		SupportingCitations:    []queryJSONTestCitation{supportACitation, supportBCitation},
		ContradictingCitations: []queryJSONTestCitation{counterACitation},
	}
	conflictAFact := queryJSONTestFact{
		Key: conflictKey, Value: queryJSONTestTerm{Kind: "text", Text: testStringPointer("alpha")},
		Contributions: []queryJSONTestContribution{{
			ObservationID: "observation-conflict-a", Status: "rejected",
			ValidTime: queryJSONTestExtent{
				Kind: "window", Start: testStringPointer("2026-01-10T13:09:10.654321Z"),
				End: testStringPointer("2026-01-20T13:09:10.654321Z"),
			},
			RecordedAt: recordedAt, Derivation: queryJSONTestDerivation{Method: "review", Version: "v2"},
		}},
		SupportingCitations: []queryJSONTestCitation{conflictACitation}, ContradictingCitations: []queryJSONTestCitation{},
	}
	conflictBFact := queryJSONTestFact{
		Key: conflictKey, Value: queryJSONTestTerm{Kind: "text", Text: testStringPointer("beta")},
		Contributions: []queryJSONTestContribution{{
			ObservationID: "observation-conflict-b", Status: "validated_empirically",
			ValidTime: queryJSONTestExtent{
				Kind: "interval", Start: testStringPointer("2026-01-11T13:09:10.654321Z"),
				End: testStringPointer("2026-01-21T13:09:10.654321Z"),
			},
			RecordedAt: recordedAt, Derivation: queryJSONTestDerivation{Method: "review", Version: "v2"},
		}},
		SupportingCitations: []queryJSONTestCitation{conflictBCitation}, ContradictingCitations: []queryJSONTestCitation{},
	}
	addedFact := queryJSONTestFact{
		Key: addedKey, Value: queryJSONTestTerm{Kind: "absent"},
		Contributions: []queryJSONTestContribution{{
			ObservationID: "observation-added", Status: "validated_structurally",
			ValidTime:  queryJSONTestExtent{Kind: "interval", End: testStringPointer("2026-04-01T13:09:10.654321Z")},
			RecordedAt: recordedAt, Derivation: queryJSONTestDerivation{Method: "rule", Version: "v5"},
		}},
		SupportingCitations: []queryJSONTestCitation{addedCitation}, ContradictingCitations: []queryJSONTestCitation{},
	}
	changedAfterFact := queryJSONTestFact{
		Key: changedKey, Value: queryJSONTestTerm{Kind: "text", Text: testStringPointer("office")},
		Contributions: []queryJSONTestContribution{{
			ObservationID: "observation-after", Status: "validated_empirically",
			ValidTime: queryJSONTestExtent{
				Kind: "interval", Start: testStringPointer("2026-03-01T13:09:10.654321Z"),
				End: testStringPointer("2026-04-01T13:09:10.654321Z"),
			},
			RecordedAt: recordedAt, Derivation: queryJSONTestDerivation{Method: "review", Version: "v4"},
		}},
		SupportingCitations: []queryJSONTestCitation{afterCitation}, ContradictingCitations: []queryJSONTestCitation{},
	}
	hypothesisFact := queryJSONTestFact{
		Key: hypothesisKey, Value: queryJSONTestTerm{Kind: "text", Text: testStringPointer("candidate")},
		Contributions: []queryJSONTestContribution{{
			ObservationID: "observation-hypothesis", Status: "hypothesized",
			ValidTime:  queryJSONTestExtent{Kind: "instant", At: testStringPointer("2026-03-12T13:09:10.654321Z")},
			RecordedAt: recordedAt, Derivation: queryJSONTestDerivation{Method: "extract", Version: "v7"},
		}},
		SupportingCitations: []queryJSONTestCitation{}, ContradictingCitations: []queryJSONTestCitation{hypothesisCitation},
	}

	beforeSelection := queryJSONTestSelection{
		Kind: "window", Label: "before",
		Start: testStringPointer("2026-01-01T13:09:10.654321Z"),
		End:   testStringPointer("2026-02-01T13:09:10.654321Z"),
	}
	afterSelection := queryJSONTestSelection{
		Kind: "window", Label: "after",
		Start: testStringPointer("2026-03-01T13:09:10.654321Z"),
		End:   testStringPointer("2026-04-01T13:09:10.654321Z"),
	}
	want := queryJSONTestEnvelope{
		SchemaVersion: "stacks.temporal-query.v1",
		Intent:        "trend-comparison",
		Request: queryJSONTestRequest{
			EntityIDs:   []string{"entity-a", "entity-b"},
			EntityMatch: "all",
			Predicates:  []string{"a.changed", "b.removed", "c.added", "d.conflict", "e.hypothesis"},
			Selections:  []queryJSONTestSelection{beforeSelection, afterSelection},
			KnowledgeScope: queryJSONTestKnowledge{
				Kind: "as-of", At: testStringPointer("2026-04-30T23:02:03.987654Z"),
			},
			Limit: 0,
		},
		Result: queryJSONTestResult{Trend: queryJSONTestTrend{
			Before: queryJSONTestWindow{
				Selection: beforeSelection,
				Facts:     []queryJSONTestFact{removedFact, changedBeforeFact},
				Unresolved: []queryJSONTestUnresolved{{
					Key: conflictKey, Reason: "conflicting-values",
					Candidates: []queryJSONTestFact{conflictAFact, conflictBFact},
				}},
			},
			After: queryJSONTestWindow{
				Selection: afterSelection,
				Facts:     []queryJSONTestFact{addedFact, changedAfterFact},
				Unresolved: []queryJSONTestUnresolved{{
					Key: hypothesisKey, Reason: "hypothesized",
					Candidates: []queryJSONTestFact{hypothesisFact},
				}},
			},
			Changes: []queryJSONTestChange{
				{Kind: "added", Key: addedKey, After: &addedFact},
				{Kind: "removed", Key: removedKey, Before: &removedFact},
				{Kind: "changed", Key: changedKey, Before: &changedBeforeFact, After: &changedAfterFact},
			},
			UnresolvedKeys: []queryJSONTestStateKey{hypothesisKey, conflictKey},
		}},
		Gaps: []queryJSONTestGap{
			{Kind: "no-evidence"},
			{
				Kind: "valid-time-excluded", EntityID: testStringPointer("entity-b"),
				Predicate: testStringPointer("b.removed"), SelectionLabel: testStringPointer("after"),
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("strict JSON associations mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestQueryCommandJSONPreservesGenericTemporalEvidenceParity(t *testing.T) {
	result := genericTemporalParityCLIResult(t)
	service := &recordingQueryService{result: result}
	var output bytes.Buffer
	err := (QueryCommand{Service: service, Output: &output}).Run(t.Context(), Invocation{
		Command: CommandQuery,
		Action:  ActionTrajectory,
		Query: &QueryInput{
			Request: requestFromResult(result),
			Output:  QueryOutputJSON,
		},
	})
	if err != nil {
		t.Fatalf("QueryCommand.Run() error = %v", err)
	}
	if service.calls != 1 || !reflect.DeepEqual(service.request, requestFromResult(result)) {
		t.Fatalf("service calls/request = %d/%#v, want one exact public request", service.calls, service.request)
	}

	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.DisallowUnknownFields()
	var got queryJSONTestEnvelope
	if err := decoder.Decode(&got); err != nil {
		t.Fatalf("strict v1 JSON decode error = %v", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("strict v1 JSON trailing decode error = %v, want EOF", err)
	}
	if got.SchemaVersion != temporalQuerySchemaVersion ||
		got.Intent != string(temporal.IntentTrajectory) ||
		!slices.Equal(got.Request.EntityIDs, []string{"entity:atlas-owner", "entity:atlas-reviewer"}) ||
		got.Request.EntityMatch != string(query.EntityMatchAny) ||
		got.Request.KnowledgeScope.Kind != "as-of" ||
		got.Request.KnowledgeScope.At == nil ||
		*got.Request.KnowledgeScope.At != "2034-01-10T12:00:00Z" {
		t.Fatalf("strict v1 envelope/request = %#v, want exact generic as-of request", got)
	}
	trajectory := got.Result.Trajectory
	if trajectory == nil || len(trajectory.Transitions) != 2 || len(trajectory.Unresolved) != 2 {
		t.Fatalf("trajectory shape = %#v, want exact chronology plus conflict and uncertainty", trajectory)
	}
	first := trajectory.Transitions[0]
	if first.Kind != "added" || first.After == nil ||
		len(first.After.Contributions) != 1 ||
		first.After.Contributions[0].ObservationID != "observation:generic/initial" ||
		first.After.Contributions[0].SubjectGroundingMentionID == nil ||
		*first.After.Contributions[0].SubjectGroundingMentionID != "mention:atlas-owner" ||
		first.After.Contributions[0].ValidTime.Kind != "interval" {
		t.Fatalf("first chronology item = %#v, want exact contribution and valid time", first)
	}
	if len(first.After.SupportingCitations) != 1 ||
		len(first.After.ContradictingCitations) != 1 ||
		first.After.SupportingCitations[0].Role != "supporting" ||
		first.After.ContradictingCitations[0].Role != "contradicting" ||
		first.After.SupportingCitations[0].StartOffset != 7 ||
		first.After.SupportingCitations[0].EndOffset != 26 ||
		first.After.SupportingCitations[0].Text == nil ||
		*first.After.SupportingCitations[0].Text != "generic exact span" {
		t.Fatalf("first chronology citations = %#v/%#v, want exact support and counterevidence", first.After.SupportingCitations, first.After.ContradictingCitations)
	}
	if trajectory.Unresolved[0].Reason != "conflicting-values" ||
		trajectory.Unresolved[1].Reason != "temporal-uncertainty" ||
		len(trajectory.Unresolved[1].Candidates) != 1 ||
		trajectory.Unresolved[1].Candidates[0].Contributions[0].ValidTime.Kind != "unknown" {
		t.Fatalf("trajectory unresolved = %#v, want conflict and unknown-time uncertainty", trajectory.Unresolved)
	}
	if !reflect.DeepEqual(got.Gaps, []queryJSONTestGap{
		{
			Kind:      "authority-excluded",
			EntityID:  testStringPointer("entity:atlas-owner"),
			Predicate: testStringPointer("atlas.responsibility/admission"),
		},
		{
			Kind:      "unresolved-mention",
			EntityID:  testStringPointer("entity:atlas-reviewer"),
			Predicate: testStringPointer("atlas.responsibility/gap"),
		},
	}) {
		t.Fatalf("strict v1 gaps = %#v, want exact authority and identity gaps", got.Gaps)
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

type queryJSONTestEnvelope struct {
	SchemaVersion string               `json:"schema_version"`
	Intent        string               `json:"intent"`
	Request       queryJSONTestRequest `json:"request"`
	Result        queryJSONTestResult  `json:"result"`
	Gaps          []queryJSONTestGap   `json:"gaps"`
}

type queryJSONTestRequest struct {
	EntityIDs      []string                 `json:"entity_ids"`
	EntityMatch    string                   `json:"entity_match"`
	Predicates     []string                 `json:"predicates"`
	Selections     []queryJSONTestSelection `json:"selections"`
	KnowledgeScope queryJSONTestKnowledge   `json:"knowledge_scope"`
	Limit          int                      `json:"limit"`
}

type queryJSONTestSelection struct {
	Kind  string  `json:"kind"`
	Label string  `json:"label"`
	At    *string `json:"at"`
	Start *string `json:"start"`
	End   *string `json:"end"`
}

type queryJSONTestKnowledge struct {
	Kind string  `json:"kind"`
	At   *string `json:"at"`
}

type queryJSONTestResult struct {
	Trend      queryJSONTestTrend       `json:"trend"`
	Trajectory *queryJSONTestTrajectory `json:"trajectory"`
}

type queryJSONTestTrend struct {
	Before         queryJSONTestWindow     `json:"before"`
	After          queryJSONTestWindow     `json:"after"`
	Changes        []queryJSONTestChange   `json:"changes"`
	UnresolvedKeys []queryJSONTestStateKey `json:"unresolved_keys"`
}

type queryJSONTestWindow struct {
	Selection  queryJSONTestSelection    `json:"selection"`
	Facts      []queryJSONTestFact       `json:"facts"`
	Unresolved []queryJSONTestUnresolved `json:"unresolved"`
}

type queryJSONTestStateKey struct {
	Subject   queryJSONTestTerm `json:"subject"`
	Predicate string            `json:"predicate"`
}

type queryJSONTestTerm struct {
	Kind      string  `json:"kind"`
	Text      *string `json:"text"`
	MentionID *string `json:"mention_id"`
	EntityID  *string `json:"entity_id"`
}

type queryJSONTestExtent struct {
	Kind  string  `json:"kind"`
	At    *string `json:"at"`
	Start *string `json:"start"`
	End   *string `json:"end"`
}

type queryJSONTestContribution struct {
	ObservationID             string                  `json:"observation_id"`
	Status                    string                  `json:"status"`
	ValidTime                 queryJSONTestExtent     `json:"valid_time"`
	RecordedAt                string                  `json:"recorded_at"`
	Derivation                queryJSONTestDerivation `json:"derivation"`
	SubjectGroundingMentionID *string                 `json:"subject_grounding_mention_id"`
	ObjectGroundingMentionID  *string                 `json:"object_grounding_mention_id"`
}

type queryJSONTestDerivation struct {
	Method        string  `json:"method"`
	Version       string  `json:"version"`
	RunID         *string `json:"run_id"`
	Model         *string `json:"model"`
	PromptVersion *string `json:"prompt_version"`
}

type queryJSONTestCitation struct {
	EvidenceID        string   `json:"evidence_id"`
	Role              string   `json:"role"`
	SourceDocumentID  string   `json:"source_document_id"`
	DocumentVersionID string   `json:"document_version_id"`
	SectionID         string   `json:"section_id"`
	SectionTitle      string   `json:"section_title"`
	SectionPath       []string `json:"section_path"`
	SectionOrder      int      `json:"section_order"`
	SectionRole       string   `json:"section_role"`
	StartOffset       int      `json:"start_offset"`
	EndOffset         int      `json:"end_offset"`
	Locator           *string  `json:"locator"`
	Text              *string  `json:"text"`
}

type queryJSONTestFact struct {
	Key                    queryJSONTestStateKey       `json:"key"`
	Value                  queryJSONTestTerm           `json:"value"`
	Contributions          []queryJSONTestContribution `json:"contributions"`
	SupportingCitations    []queryJSONTestCitation     `json:"supporting_citations"`
	ContradictingCitations []queryJSONTestCitation     `json:"contradicting_citations"`
}

type queryJSONTestUnresolved struct {
	Key        queryJSONTestStateKey `json:"key"`
	Reason     string                `json:"reason"`
	Candidates []queryJSONTestFact   `json:"candidates"`
}

type queryJSONTestChange struct {
	Kind   string                `json:"kind"`
	Key    queryJSONTestStateKey `json:"key"`
	Before *queryJSONTestFact    `json:"before"`
	After  *queryJSONTestFact    `json:"after"`
}

type queryJSONTestTrajectory struct {
	Selection   queryJSONTestSelection    `json:"selection"`
	Transitions []queryJSONTestTransition `json:"transitions"`
	Unresolved  []queryJSONTestUnresolved `json:"unresolved"`
}

type queryJSONTestTransition struct {
	Kind       string                    `json:"kind"`
	Key        queryJSONTestStateKey     `json:"key"`
	ValidTime  queryJSONTestExtent       `json:"valid_time"`
	Before     *queryJSONTestFact        `json:"before"`
	After      *queryJSONTestFact        `json:"after"`
	Unresolved []queryJSONTestUnresolved `json:"unresolved"`
}

type queryJSONTestGap struct {
	Kind           string  `json:"kind"`
	EntityID       *string `json:"entity_id"`
	Predicate      *string `json:"predicate"`
	SelectionLabel *string `json:"selection_label"`
}

func testStringPointer(value string) *string {
	return &value
}

func genericTemporalParityCLIResult(t *testing.T) query.Result {
	t.Helper()
	window := mustQueryWindow(
		t,
		"responsibility-window",
		time.Date(2034, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2034, time.February, 1, 0, 0, 0, 0, time.UTC),
	)
	asOf, err := temporal.KnownAsOf(time.Date(2034, time.January, 10, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	stateKey := mustQueryKey(t, mustQueryEntity(t, "entity:atlas-owner"), "atlas.responsibility/state")
	unknownKey := mustQueryKey(t, mustQueryEntity(t, "entity:atlas-owner"), "atlas.responsibility/unknown")
	support := query.Citation{
		EvidenceID: "evidence:generic/support", Role: observation.EvidenceSupporting,
		SourceDocumentID: "document:generic", DocumentVersionID: "version:generic",
		SectionID: "section:generic", SectionTitle: "Generic Atlas record",
		SectionPath: []string{"Generic Atlas"}, SectionOrder: 0, SectionRole: "synthetic-record",
		StartOffset: 7, EndOffset: 26, Locator: "synthetic://generic-atlas",
		Text: "generic exact span",
	}
	counter := support
	counter.EvidenceID = "evidence:generic/counter"
	counter.Role = observation.EvidenceContradicting
	initial := query.Fact{
		Key: stateKey, Value: mustQueryText(t, "queued"),
		Contributions: []query.Contribution{{
			ObservationID: "observation:generic/initial", Status: observation.StatusObserved,
			ValidTime: mustQueryDuring(
				t,
				time.Date(2034, time.January, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2034, time.January, 15, 0, 0, 0, 0, time.UTC),
			),
			RecordedAt:                time.Date(2034, time.January, 2, 9, 20, 0, 0, time.UTC),
			Derivation:                observation.Derivation{Method: "synthetic-review", Version: "generic-parity-v1"},
			SubjectGroundingMentionID: "mention:atlas-owner",
		}},
		SupportingCitations: []query.Citation{support}, ContradictingCitations: []query.Citation{counter},
	}
	transfer := query.Fact{
		Key: stateKey, Value: mustQueryText(t, "active"),
		Contributions: []query.Contribution{{
			ObservationID: "observation:generic/transfer", Status: observation.StatusObserved,
			ValidTime: mustQueryDuring(
				t,
				time.Date(2034, time.January, 15, 0, 0, 0, 0, time.UTC),
				time.Date(2034, time.February, 1, 0, 0, 0, 0, time.UTC),
			),
			RecordedAt:                time.Date(2034, time.January, 3, 9, 20, 0, 0, time.UTC),
			Derivation:                observation.Derivation{Method: "synthetic-review", Version: "generic-parity-v1"},
			SubjectGroundingMentionID: "mention:atlas-owner",
		}},
		SupportingCitations: []query.Citation{support}, ContradictingCitations: []query.Citation{},
	}
	conflict := transfer
	conflict.Value = mustQueryText(t, "paused")
	conflict.Contributions = []query.Contribution{{
		ObservationID: "observation:generic/conflict", Status: observation.StatusObserved,
		ValidTime: mustQueryDuring(
			t,
			time.Date(2034, time.January, 15, 0, 0, 0, 0, time.UTC),
			time.Date(2034, time.January, 20, 0, 0, 0, 0, time.UTC),
		),
		RecordedAt:                time.Date(2034, time.January, 3, 9, 21, 0, 0, time.UTC),
		Derivation:                observation.Derivation{Method: "synthetic-review", Version: "generic-parity-v1"},
		SubjectGroundingMentionID: "mention:atlas-owner",
	}}
	conflict.ContradictingCitations = []query.Citation{counter}
	unknown := query.Fact{
		Key: unknownKey, Value: mustQueryEntity(t, "entity:atlas-reviewer"),
		Contributions: []query.Contribution{{
			ObservationID: "observation:generic/unknown", Status: observation.StatusObserved,
			ValidTime:                 observation.UnknownTime(),
			RecordedAt:                time.Date(2034, time.January, 2, 9, 23, 0, 0, time.UTC),
			Derivation:                observation.Derivation{Method: "synthetic-review", Version: "generic-parity-v1"},
			SubjectGroundingMentionID: "mention:atlas-owner",
			ObjectGroundingMentionID:  "mention:atlas-reviewer",
		}},
		SupportingCitations: []query.Citation{support}, ContradictingCitations: []query.Citation{},
	}
	conflictItem := query.UnresolvedItem{
		Key: stateKey, Reason: temporal.UnresolvedConflict, Candidates: []query.Fact{transfer, conflict},
	}
	payload, err := query.NewTrajectoryPayload(query.TrajectoryResult{
		Selection: window,
		Transitions: []query.Transition{
			{
				Kind: temporal.ChangeAdded, Key: stateKey,
				ValidTime: mustQueryInstant(t, time.Date(2034, time.January, 1, 0, 0, 0, 0, time.UTC)),
				After:     queryFactPointer(initial), Unresolved: []query.UnresolvedItem{},
			},
			{
				Kind: temporal.ChangeRemoved, Key: stateKey,
				ValidTime: mustQueryInstant(t, time.Date(2034, time.January, 15, 0, 0, 0, 0, time.UTC)),
				Before:    queryFactPointer(initial), Unresolved: []query.UnresolvedItem{conflictItem},
			},
		},
		Unresolved: []query.UnresolvedItem{
			{
				Key: stateKey, Reason: temporal.UnresolvedConflict,
				Candidates: []query.Fact{initial, transfer, conflict},
			},
			{
				Key: unknownKey, Reason: temporal.UnresolvedTemporalUncertainty,
				Candidates: []query.Fact{unknown},
			},
		},
	})
	if err != nil {
		t.Fatalf("query.NewTrajectoryPayload() error = %v", err)
	}
	return query.Result{
		Intent: temporal.IntentTrajectory,
		EntityIDs: []identity.EntityID{
			"entity:atlas-owner",
			"entity:atlas-reviewer",
		},
		EntityMatch: query.EntityMatchAny,
		Predicates: []observation.Predicate{
			"atlas.responsibility/admission",
			"atlas.responsibility/gap",
			"atlas.responsibility/state",
			"atlas.responsibility/unknown",
		},
		Selections: []temporal.TemporalSelection{window}, KnowledgeScope: asOf,
		Limit: 32, Payload: payload,
		Gaps: []query.Gap{
			{
				Kind: query.GapAuthorityExcluded, EntityID: "entity:atlas-owner",
				Predicate: "atlas.responsibility/admission",
			},
			{
				Kind: query.GapUnresolvedMention, EntityID: "entity:atlas-reviewer",
				Predicate: "atlas.responsibility/gap",
			},
		},
	}
}
