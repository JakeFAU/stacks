package queryplan

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/temporal"
	"stacks/internal/query"
)

const executablePointProposal = `{
  "status": "executable",
  "reason": "none",
  "intent": "point-in-time",
  "entity_match": "all",
  "predicates": ["assigned_to"],
  "selections": [
    {"kind": "point", "label": "point", "at": "2026-06-01T12:00:00-04:00", "start": "", "end": ""}
  ],
  "knowledge_scope": {"kind": "current", "as_of": ""},
  "chronology_limit": 0
}`

const cannotPlanProposal = `{
  "status": "cannot-plan",
  "reason": "ambiguous-question",
  "intent": "",
  "entity_match": "",
  "predicates": [],
  "selections": [],
  "knowledge_scope": {"kind": "", "as_of": ""},
  "chronology_limit": 0
}`

func TestComposeRequestDecodesExecutablePoint(t *testing.T) {
	request, err := composeRequest([]byte(executablePointProposal), []identity.EntityID{"entity-atlas-001"}, plannerLimits())
	if err != nil {
		t.Fatal(err)
	}
	if request.Intent != temporal.IntentPointInTime || request.EntityMatch != query.EntityMatchAll || request.Limit != 0 {
		t.Fatalf("composeRequest() metadata = %#v", request)
	}
	if !equalEntityIDs(request.EntityIDs, []identity.EntityID{"entity-atlas-001"}) {
		t.Fatalf("composeRequest() entity IDs = %v", request.EntityIDs)
	}
	if len(request.Predicates) != 1 || request.Predicates[0] != "assigned_to" {
		t.Fatalf("composeRequest() predicates = %v", request.Predicates)
	}
	if len(request.Selections) != 1 || request.Selections[0].Kind() != temporal.SelectionPoint || request.Selections[0].Label() != "point" {
		t.Fatalf("composeRequest() selections = %#v", request.Selections)
	}
	point, ok := request.Selections[0].Point()
	if !ok || !point.Equal(time.Date(2026, time.June, 1, 16, 0, 0, 0, time.UTC)) || point.Location() != time.UTC {
		t.Fatalf("composeRequest() point = %s, %t", point, ok)
	}
	if request.KnowledgeScope.Kind() != temporal.KnowledgeCurrent {
		t.Fatalf("composeRequest() knowledge scope = %#v", request.KnowledgeScope)
	}
}

func TestComposeRequestRejectsInvalidWireShapes(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{"malformed json", `{`},
		{"second json value", executablePointProposal + ` {}`},
		{"unknown top level field", strings.Replace(executablePointProposal, `"chronology_limit": 0`, `"chronology_limit": 0, "entity_ids": ["private-invented-id"]`, 1)},
		{"unknown nested field", strings.Replace(executablePointProposal, `"start": "", "end": ""`, `"start": "", "end": "", "extra": "value"`, 1)},
		{"invalid enum", strings.Replace(executablePointProposal, `"status": "executable"`, `"status": "unknown"`, 1)},
		{"missing required field", strings.Replace(executablePointProposal, `  "reason": "none",`, ``, 1)},
		{"null required field", strings.Replace(executablePointProposal, `"entity_match": "all"`, `"entity_match": null`, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := composeRequest([]byte(test.output), []identity.EntityID{"entity-atlas-001"}, plannerLimits())
			assertInvalidProposal(t, err)
		})
	}
}

func TestCannotPlanReturnsApprovedReasons(t *testing.T) {
	for _, reason := range []CannotPlanReason{CannotPlanAmbiguous, CannotPlanUnsupported, CannotPlanInsufficientTemporalDetail} {
		t.Run(string(reason), func(t *testing.T) {
			output := strings.Replace(cannotPlanProposal, `"ambiguous-question"`, `"`+string(reason)+`"`, 1)
			_, err := composeRequest([]byte(output), []identity.EntityID{"entity-atlas-001"}, plannerLimits())
			var cannotPlan CannotPlanError
			if !errors.As(err, &cannotPlan) || cannotPlan.Reason != reason {
				t.Fatalf("composeRequest() error = %#v, want CannotPlanError{%q}", err, reason)
			}
		})
	}
}

func TestCannotPlanRejectsNonemptyRequestFieldsAndInvalidPairs(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{"executable non-none reason", strings.Replace(executablePointProposal, `"reason": "none"`, `"reason": "ambiguous-question"`, 1)},
		{"cannot plan none reason", strings.Replace(cannotPlanProposal, `"reason": "ambiguous-question"`, `"reason": "none"`, 1)},
		{"cannot plan executable intent", strings.Replace(cannotPlanProposal, `"intent": ""`, `"intent": "point-in-time"`, 1)},
		{"cannot plan entity match", strings.Replace(cannotPlanProposal, `"entity_match": ""`, `"entity_match": "all"`, 1)},
		{"cannot plan predicates", strings.Replace(cannotPlanProposal, `"predicates": []`, `"predicates": ["assigned_to"]`, 1)},
		{"cannot plan selection", strings.Replace(cannotPlanProposal, `"selections": []`, `"selections": [{"kind":"point","label":"point","at":"2026-06-01T12:00:00Z","start":"","end":""}]`, 1)},
		{"cannot plan knowledge", strings.Replace(cannotPlanProposal, `"kind": "", "as_of": ""`, `"kind": "current", "as_of": ""`, 1)},
		{"cannot plan chronology", strings.Replace(cannotPlanProposal, `"chronology_limit": 0`, `"chronology_limit": 1`, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := composeRequest([]byte(test.output), []identity.EntityID{"entity-atlas-001"}, plannerLimits())
			assertInvalidProposal(t, err)
		})
	}
}

func TestComposeRequestRejectsInvalidExecutableSemantics(t *testing.T) {
	trend := `{
  "status":"executable", "reason":"none", "intent":"trend-comparison", "entity_match":"any",
  "predicates":["assigned_to"],
  "selections":[
    {"kind":"window","label":"before","at":"","start":"2026-01-01T00:00:00Z","end":"2026-02-01T00:00:00Z"},
    {"kind":"window","label":"after","at":"","start":"2026-03-01T00:00:00-04:00","end":"2026-04-01T00:00:00-04:00"}
  ], "knowledge_scope":{"kind":"as-of","as_of":"2026-07-01T00:00:00-04:00"}, "chronology_limit":0
}`
	trajectory := `{
  "status":"executable", "reason":"none", "intent":"trajectory", "entity_match":"all",
  "predicates":[],
  "selections":[{"kind":"window","label":"between","at":"","start":"2026-01-01T00:00:00Z","end":"2026-02-01T00:00:00Z"}],
  "knowledge_scope":{"kind":"current","as_of":""}, "chronology_limit":2
}`

	tests := []struct {
		name   string
		output string
		limits query.Limits
	}{
		{"point wrong label", strings.Replace(executablePointProposal, `"label": "point"`, `"label": "between"`, 1), plannerLimits()},
		{"point wrong kind", strings.Replace(executablePointProposal, `"kind": "point"`, `"kind": "window"`, 1), plannerLimits()},
		{"point has window", strings.Replace(executablePointProposal, `"start": "", "end": ""`, `"start": "2026-01-01T00:00:00Z", "end": "2026-02-01T00:00:00Z"`, 1), plannerLimits()},
		{"point invalid timestamp", strings.Replace(executablePointProposal, `"2026-06-01T12:00:00-04:00"`, `"not-a-time"`, 1), plannerLimits()},
		{"trend wrong selection count", strings.Replace(trend, ",\n    {\"kind\":\"window\",\"label\":\"after\",\"at\":\"\",\"start\":\"2026-03-01T00:00:00-04:00\",\"end\":\"2026-04-01T00:00:00-04:00\"}", ``, 1), plannerLimits()},
		{"trend wrong label", strings.Replace(trend, `"label":"after"`, `"label":"between"`, 1), plannerLimits()},
		{"trend inverted windows", strings.Replace(trend, `"start":"2026-03-01T00:00:00-04:00"`, `"start":"2025-12-01T00:00:00-04:00"`, 1), plannerLimits()},
		{"invalid current knowledge", strings.Replace(executablePointProposal, `"kind": "current", "as_of": ""`, `"kind": "current", "as_of": "2026-01-01T00:00:00Z"`, 1), plannerLimits()},
		{"invalid as-of knowledge", strings.Replace(trend, `"as_of":"2026-07-01T00:00:00-04:00"`, `"as_of":"not-a-time"`, 1), plannerLimits()},
		{"duplicate predicates", strings.Replace(executablePointProposal, `["assigned_to"]`, `["assigned_to", "assigned_to"]`, 1), plannerLimits()},
		{"excessive predicates", strings.Replace(executablePointProposal, `["assigned_to"]`, `["first", "second"]`, 1), query.Limits{MaxEntities: 4, MaxPredicates: 1, MaxChronology: 20}},
		{"trajectory zero chronology", strings.Replace(trajectory, `"chronology_limit":2`, `"chronology_limit":0`, 1), plannerLimits()},
		{"trajectory excessive chronology", strings.Replace(trajectory, `"chronology_limit":2`, `"chronology_limit":3`, 1), query.Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := composeRequest([]byte(test.output), []identity.EntityID{"entity-atlas-001"}, test.limits)
			assertInvalidProposal(t, err)
		})
	}
}

func TestComposeRequestAttachesCanonicalIDsAndPreservesTemporalScopes(t *testing.T) {
	output := `{
  "status":"executable", "reason":"none", "intent":"trend-comparison", "entity_match":"any",
  "predicates":["assigned_to"],
  "selections":[
    {"kind":"window","label":"before","at":"","start":"2026-01-01T00:00:00-04:00","end":"2026-02-01T00:00:00-04:00"},
    {"kind":"window","label":"after","at":"","start":"2026-03-01T00:00:00-04:00","end":"2026-04-01T00:00:00-04:00"}
  ], "knowledge_scope":{"kind":"as-of","as_of":"2026-07-01T00:00:00-04:00"}, "chronology_limit":0
}`
	entityIDs := []identity.EntityID{"entity-atlas-002", "entity-atlas-001"}
	request, err := composeRequest([]byte(output), entityIDs, plannerLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !equalEntityIDs(request.EntityIDs, []identity.EntityID{"entity-atlas-001", "entity-atlas-002"}) {
		t.Fatalf("composeRequest() entity IDs = %v", request.EntityIDs)
	}
	entityIDs[0] = "mutated"
	if request.EntityIDs[0] != "entity-atlas-001" {
		t.Fatalf("composeRequest() retained caller slice: %v", request.EntityIDs)
	}
	if request.Intent != temporal.IntentTrendComparison || request.EntityMatch != query.EntityMatchAny || len(request.Predicates) != 1 || request.Predicates[0] != "assigned_to" {
		t.Fatalf("composeRequest() request = %#v", request)
	}
	if request.KnowledgeScope.Kind() != temporal.KnowledgeAsOf {
		t.Fatalf("composeRequest() knowledge scope = %#v", request.KnowledgeScope)
	}
	asOf, ok := request.KnowledgeScope.AsOf()
	wantAsOf := time.Date(2026, time.July, 1, 4, 0, 0, 0, time.UTC)
	if !ok || !asOf.Equal(wantAsOf) || asOf.Location() != time.UTC {
		t.Fatalf("composeRequest() as-of = %s, %t", asOf, ok)
	}
	for _, selection := range request.Selections {
		start, end, ok := selection.Window()
		if !ok || start.Location() != time.UTC || end.Location() != time.UTC {
			t.Fatalf("composeRequest() window = %s, %s, %t", start, end, ok)
		}
	}
}

func TestComposeRequestRestrictsCausalPredicate(t *testing.T) {
	output := `{
  "status":"executable", "reason":"none", "intent":"causal-chain", "entity_match":"all",
  "predicates":["stacks.causal.v1/causes"],
  "selections":[{"kind":"window","label":"between","at":"","start":"2026-01-01T00:00:00Z","end":"2026-02-01T00:00:00Z"}],
  "knowledge_scope":{"kind":"current","as_of":""}, "chronology_limit":2
}`
	request, err := composeRequest([]byte(output), []identity.EntityID{"entity-atlas-001"}, plannerLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Predicates) != 1 || request.Predicates[0] != query.CausalPredicate {
		t.Fatalf("composeRequest() predicates = %v", request.Predicates)
	}
	_, err = composeRequest([]byte(strings.Replace(output, `stacks.causal.v1/causes`, `assigned_to`, 1)), []identity.EntityID{"entity-atlas-001"}, plannerLimits())
	assertInvalidProposal(t, err)
}

func plannerLimits() query.Limits {
	return query.Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 20}
}

func assertInvalidProposal(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("composeRequest() error = nil")
	}
	if err.Error() != "query planner proposal is invalid" {
		t.Fatalf("composeRequest() error = %q, want bounded invalid proposal error", err)
	}
}
