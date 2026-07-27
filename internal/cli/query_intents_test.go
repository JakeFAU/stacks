package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"

	"stacks/internal/query"
)

func TestRunnerParsesEveryTemporalQueryLeaf(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		action     Action
		intent     temporal.Intent
		limit      int
		predicates []observation.Predicate
		assert     func(*testing.T, query.Request)
	}{
		{
			name: "point",
			args: []string{
				"query", "point", "--entity", "entity-a",
				"--at", "2026-01-02T03:04:05-05:00",
				"--predicate", "project.atlas/owner", "--output", "json",
			},
			action: ActionPoint, intent: temporal.IntentPointInTime,
			predicates: []observation.Predicate{"project.atlas/owner"},
			assert: func(t *testing.T, request query.Request) {
				t.Helper()
				at, ok := request.Selections[0].Point()
				if !ok || request.Selections[0].Label() != "point" ||
					at.Format(time.RFC3339) != "2026-01-02T08:04:05Z" {
					t.Fatalf("point selection = %#v, want normalized point", request.Selections[0])
				}
			},
		},
		{
			name: "trajectory",
			args: []string{
				"query", "trajectory", "--entity", "entity-a",
				"--between", "2026-01-01T00:00:00Z/2026-02-01T00:00:00Z",
				"--limit", "7", "--predicate", "project.atlas/owner",
			},
			action: ActionTrajectory, intent: temporal.IntentTrajectory, limit: 7,
			predicates: []observation.Predicate{"project.atlas/owner"},
			assert: func(t *testing.T, request query.Request) {
				t.Helper()
				assertQueryWindow(t, request.Selections[0], "between",
					"2026-01-01T00:00:00Z", "2026-02-01T00:00:00Z")
			},
		},
		{
			name: "causal",
			args: []string{
				"query", "causal", "--entity", "entity-a",
				"--between", "2026-01-01T00:00:00Z/2026-02-01T00:00:00Z",
				"--limit", "9",
			},
			action: ActionCausal, intent: temporal.IntentCausalChain, limit: 9,
			predicates: []observation.Predicate{query.CausalPredicate},
			assert: func(t *testing.T, request query.Request) {
				t.Helper()
				assertQueryWindow(t, request.Selections[0], "between",
					"2026-01-01T00:00:00Z", "2026-02-01T00:00:00Z")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got Invocation
			err := (Runner{Execute: func(_ context.Context, invocation Invocation) error {
				got = invocation
				return nil
			}}).Run(t.Context(), test.args)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got.Command != CommandQuery || got.Action != test.action ||
				got.Query == nil || got.Query.Request.Intent != test.intent ||
				got.Query.Request.Limit != test.limit ||
				!reflect.DeepEqual(got.Query.Request.Predicates, test.predicates) {
				t.Fatalf("invocation = %#v, want %s/%s limit %d predicates %v",
					got, test.action, test.intent, test.limit, test.predicates)
			}
			if got.Query.Request.EntityMatch != query.EntityMatchAll ||
				got.Query.Request.KnowledgeScope.Kind() != temporal.KnowledgeCurrent {
				t.Fatalf("defaults = %#v, want all/current", got.Query.Request)
			}
			test.assert(t, got.Query.Request)
		})
	}
}

func TestRunnerRejectsChronologyLimitAndCausalPredicateBeforeExecution(t *testing.T) {
	validTrajectory := []string{
		"query", "trajectory", "--entity", "entity-a",
		"--between", "2026-01-01T00:00:00Z/2026-02-01T00:00:00Z",
		"--limit", "5",
	}
	validCausal := []string{
		"query", "causal", "--entity", "entity-a",
		"--between", "2026-01-01T00:00:00Z/2026-02-01T00:00:00Z",
		"--limit", "5",
	}
	tests := []struct {
		name string
		args []string
	}{
		{name: "trajectory missing limit", args: withoutQueryFlag(validTrajectory, "--limit")},
		{name: "trajectory zero limit", args: replaceQueryFlag(validTrajectory, "--limit", "0")},
		{name: "trajectory negative limit", args: replaceQueryFlag(validTrajectory, "--limit", "-1")},
		{name: "causal missing limit", args: withoutQueryFlag(validCausal, "--limit")},
		{name: "causal zero limit", args: replaceQueryFlag(validCausal, "--limit", "0")},
		{name: "causal negative limit", args: replaceQueryFlag(validCausal, "--limit", "-1")},
		{name: "causal predicate", args: append(append([]string{}, validCausal...), "--predicate", "private.predicate")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			err := (Runner{Execute: func(context.Context, Invocation) error {
				calls++
				return nil
			}}).Run(t.Context(), test.args)
			if err == nil {
				t.Fatal("Run() error = nil, want syntax rejection")
			}
			if calls != 0 {
				t.Fatalf("Execute calls = %d, want 0", calls)
			}
			if strings.Contains(err.Error(), "private.predicate") {
				t.Fatalf("error exposed predicate: %q", err)
			}
		})
	}
}

func TestValidateQueryInvocationAllowsOnlyMatchingActionAndIntent(t *testing.T) {
	point, err := temporal.At("point", time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	window := mustQueryWindow(t, "between", testInstant(2026, time.January, 1), testInstant(2026, time.February, 1))
	trend := populatedTrendResult(t, false)
	tests := []struct {
		action  Action
		request query.Request
	}{
		{ActionPoint, query.Request{
			Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{"entity-a"},
			EntityMatch: query.EntityMatchAll, Selections: []temporal.TemporalSelection{point},
			KnowledgeScope: temporal.CurrentKnowledge(),
		}},
		{ActionTrend, requestFromResult(trend)},
		{ActionTrajectory, query.Request{
			Intent: temporal.IntentTrajectory, EntityIDs: []identity.EntityID{"entity-a"},
			EntityMatch: query.EntityMatchAll, Selections: []temporal.TemporalSelection{window},
			KnowledgeScope: temporal.CurrentKnowledge(), Limit: 2,
		}},
		{ActionCausal, query.Request{
			Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{"entity-a"},
			EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{query.CausalPredicate},
			Selections:     []temporal.TemporalSelection{window},
			KnowledgeScope: temporal.CurrentKnowledge(), Limit: 2,
		}},
	}
	for _, test := range tests {
		invocation := Invocation{
			Command: CommandQuery, Action: test.action,
			Query: &QueryInput{Request: test.request, Output: QueryOutputText},
		}
		if err := ValidateQueryInvocation(invocation); err != nil {
			t.Fatalf("ValidateQueryInvocation(%s) error = %v", test.action, err)
		}
		for _, mismatch := range []Action{ActionPoint, ActionTrend, ActionTrajectory, ActionCausal} {
			if mismatch == test.action {
				continue
			}
			invocation.Action = mismatch
			if err := ValidateQueryInvocation(invocation); err == nil {
				t.Fatalf("ValidateQueryInvocation(%s action, %s intent) error = nil",
					mismatch, test.request.Intent)
			}
		}
	}
}

func TestQueryJSONEncodesExactRemainingIntentUnions(t *testing.T) {
	for name, result := range populatedRemainingIntentResults(t) {
		t.Run(name, func(t *testing.T) {
			rendered, err := renderQueryJSON(result)
			if err != nil {
				t.Fatalf("renderQueryJSON() error = %v", err)
			}
			if bytes.Contains(rendered, []byte("null")) {
				t.Fatalf("JSON contains null: %s", rendered)
			}
			var envelope map[string]json.RawMessage
			mustUnmarshalJSON(t, rendered, &envelope)
			var union map[string]json.RawMessage
			mustUnmarshalJSON(t, envelope["result"], &union)
			assertJSONKeys(t, union, name)
			if bytes.Contains(union[name], []byte(`"gaps"`)) {
				t.Fatalf("result payload contains nested gaps: %s", union[name])
			}
			if !bytes.Contains(envelope["gaps"], []byte(`[`)) {
				t.Fatalf("envelope gaps is not an array: %s", envelope["gaps"])
			}
		})
	}
}

func TestQueryJSONPreservesTransitionAndCausalAssociations(t *testing.T) {
	results := populatedRemainingIntentResults(t)
	trajectoryJSON, err := renderQueryJSON(results["trajectory"])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"valid_time":{"kind":"instant","at":"2026-01-16T13:09:10.654321Z"}`,
		`"kind":"added"`, `"after":`, `"kind":"removed"`, `"before":`,
		`"unresolved":[`, `"observation_id":"observation-unresolved"`,
	} {
		if !bytes.Contains(trajectoryJSON, []byte(want)) {
			t.Fatalf("trajectory JSON missing %s: %s", want, trajectoryJSON)
		}
	}
	var envelope map[string]json.RawMessage
	mustUnmarshalJSON(t, trajectoryJSON, &envelope)
	var union map[string]json.RawMessage
	mustUnmarshalJSON(t, envelope["result"], &union)
	var trajectory map[string]json.RawMessage
	mustUnmarshalJSON(t, union["trajectory"], &trajectory)
	var transitions []map[string]json.RawMessage
	mustUnmarshalJSON(t, trajectory["transitions"], &transitions)
	assertJSONKeys(t, transitions[0], "after", "key", "kind", "unresolved", "valid_time")
	assertJSONKeys(t, transitions[1], "before", "key", "kind", "unresolved", "valid_time")
	if _, exists := transitions[0]["before"]; exists {
		t.Fatalf("added transition contains before: %s", transitions[0]["before"])
	}
	if _, exists := transitions[1]["after"]; exists {
		t.Fatalf("removed transition contains after: %s", transitions[1]["after"])
	}

	causalJSON, err := renderQueryJSON(results["causal"])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"cause":{"kind":"text","text":"decision"}`,
		`"effect":{"kind":"text","text":"delivery"}`,
		`"observation_id":"observation-causal"`,
		`"supporting_citations":[`, `"contradicting_citations":[`,
		`"evidence_id":"evidence-causal-counter"`,
	} {
		if !bytes.Contains(causalJSON, []byte(want)) {
			t.Fatalf("causal JSON missing %s: %s", want, causalJSON)
		}
	}
}

func TestQueryTextAndJSONExposeSameRemainingIntentAssociations(t *testing.T) {
	for name, result := range populatedRemainingIntentResults(t) {
		t.Run(name, func(t *testing.T) {
			text, err := renderQueryText(result)
			if err != nil {
				t.Fatal(err)
			}
			jsonBytes, err := renderQueryJSON(result)
			if err != nil {
				t.Fatal(err)
			}
			for _, identifier := range remainingIntentIdentifiers(name) {
				if !bytes.Contains(text, []byte(identifier)) || !bytes.Contains(jsonBytes, []byte(identifier)) {
					t.Fatalf("%s association %q missing: text=%s JSON=%s", name, identifier, text, jsonBytes)
				}
			}
			assertRemainingIntentTextAssociations(t, name, string(text))
		})
	}
}

func TestQueryCommandUsesOneServiceAndOneWriteForEveryRemainingIntent(t *testing.T) {
	actions := map[string]Action{
		"point": ActionPoint, "trajectory": ActionTrajectory, "causal": ActionCausal,
	}
	for name, result := range populatedRemainingIntentResults(t) {
		t.Run(name, func(t *testing.T) {
			request := requestFromResult(result)
			service := &recordingQueryService{result: result}
			writer := &countingQueryWriter{}
			err := (QueryCommand{Service: service, Output: writer}).Run(t.Context(), Invocation{
				Command: CommandQuery, Action: actions[name],
				Query: &QueryInput{Request: request, Output: QueryOutputJSON},
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if service.calls != 1 || writer.calls != 1 {
				t.Fatalf("service/writer calls = %d/%d, want 1/1", service.calls, writer.calls)
			}
		})
	}
}

func populatedRemainingIntentResults(t *testing.T) map[string]query.Result {
	t.Helper()
	point := mustQueryPoint(t, "point", testInstant(2026, time.January, 16))
	window := mustQueryWindow(t, "between", testInstant(2026, time.January, 1), testInstant(2026, time.February, 1))
	key := mustQueryKey(t, mustQueryEntity(t, "entity-a"), "project.atlas/owner")
	unresolvedKey := mustQueryKey(t, mustQueryEntity(t, "entity-a"), "project.atlas/risk")
	before := testFact(t, key, mustQueryText(t, "alex"),
		testContribution(t, "observation-before", observation.StatusObserved,
			mustQuerySince(t, testInstant(2026, time.January, 1)),
			observation.Derivation{Method: "synthetic", Version: "v1"}, "", ""),
		testCitation("evidence-before", observation.EvidenceSupporting, false),
	)
	after := testFact(t, key, mustQueryText(t, "blair"),
		testContribution(t, "observation-after", observation.StatusObserved,
			mustQueryInstant(t, testInstant(2026, time.January, 16)),
			observation.Derivation{Method: "synthetic", Version: "v1"}, "", ""),
		testCitation("evidence-after", observation.EvidenceSupporting, false),
	)
	unresolvedFact := testFact(t, unresolvedKey, mustQueryText(t, "casey"),
		testContribution(t, "observation-unresolved", observation.StatusHypothesized,
			observation.UnknownTime(),
			observation.Derivation{Method: "synthetic", Version: "v1"}, "", ""),
		testCitation("evidence-unresolved", observation.EvidenceContradicting, false),
	)
	unresolved := query.UnresolvedItem{
		Key: unresolvedKey, Reason: temporal.UnresolvedHypothesis, Candidates: []query.Fact{unresolvedFact},
	}

	pointPayload, err := query.NewPointPayload(query.PointInTimeResult{
		Selection: point, Facts: []query.Fact{before}, Unresolved: []query.UnresolvedItem{unresolved},
	})
	if err != nil {
		t.Fatal(err)
	}
	pointResult := query.Result{
		Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{"entity-a"},
		EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{"project.atlas/owner"},
		Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge(),
		Payload: pointPayload, Gaps: []query.Gap{},
	}

	trajectoryPayload, err := query.NewTrajectoryPayload(query.TrajectoryResult{
		Selection: window,
		Transitions: []query.Transition{
			{
				Kind: temporal.ChangeAdded, Key: key,
				ValidTime: mustQueryInstant(t, testInstant(2026, time.January, 16)),
				After:     queryFactPointer(after), Unresolved: []query.UnresolvedItem{unresolved},
			},
			{
				Kind: temporal.ChangeRemoved, Key: key,
				ValidTime: mustQueryInstant(t, testInstant(2026, time.January, 20)),
				Before:    queryFactPointer(after), Unresolved: []query.UnresolvedItem{},
			},
		},
		Unresolved: []query.UnresolvedItem{unresolved},
	})
	if err != nil {
		t.Fatal(err)
	}
	trajectoryResult := query.Result{
		Intent: temporal.IntentTrajectory, EntityIDs: []identity.EntityID{"entity-a"},
		EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{"project.atlas/owner"},
		Selections: []temporal.TemporalSelection{window}, KnowledgeScope: temporal.CurrentKnowledge(),
		Limit: 4, Payload: trajectoryPayload, Gaps: []query.Gap{},
	}

	causalContribution := testContribution(t, "observation-causal", observation.StatusObserved,
		mustQueryInstant(t, testInstant(2026, time.January, 16)),
		observation.Derivation{Method: "synthetic", Version: "v1"}, "", "")
	causalPayload, err := query.NewCausalPayload(query.CausalChainResult{
		Selection: window,
		Links: []query.CausalLink{{
			Cause: mustQueryText(t, "decision"), Effect: mustQueryText(t, "delivery"),
			Contributions: []query.Contribution{causalContribution},
			SupportingCitations: []query.Citation{
				testCitation("evidence-causal-support", observation.EvidenceSupporting, false),
			},
			ContradictingCitations: []query.Citation{
				testCitation("evidence-causal-counter", observation.EvidenceContradicting, false),
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	causalResult := query.Result{
		Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{"entity-a"},
		EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{query.CausalPredicate},
		Selections: []temporal.TemporalSelection{window}, KnowledgeScope: temporal.CurrentKnowledge(),
		Limit: 4, Payload: causalPayload, Gaps: []query.Gap{{Kind: query.GapAuthorityExcluded}},
	}
	return map[string]query.Result{
		"point": pointResult, "trajectory": trajectoryResult, "causal": causalResult,
	}
}

func mustQueryPoint(t *testing.T, label string, at time.Time) temporal.TemporalSelection {
	t.Helper()
	value, err := temporal.At(label, at)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func remainingIntentIdentifiers(name string) []string {
	switch name {
	case "point":
		return []string{"observation-before", "evidence-before", "observation-unresolved", "evidence-unresolved"}
	case "trajectory":
		return []string{"observation-after", "evidence-after", "observation-unresolved", "evidence-unresolved"}
	case "causal":
		return []string{"observation-causal", "evidence-causal-support", "evidence-causal-counter"}
	default:
		return nil
	}
}

func assertRemainingIntentTextAssociations(t *testing.T, name, rendered string) {
	t.Helper()
	switch name {
	case "point":
		facts, unresolved, ok := strings.Cut(rendered, "unresolved:\n")
		if !ok ||
			!strings.Contains(facts, "observation_id=observation-before") ||
			!strings.Contains(facts, "evidence_id=evidence-before role=supporting") ||
			strings.Contains(facts, "observation-unresolved") ||
			!strings.Contains(unresolved, "observation_id=observation-unresolved") ||
			!strings.Contains(unresolved, "evidence_id=evidence-unresolved role=contradicting") {
			t.Fatalf("point text associations are incorrect:\n%s", rendered)
		}
	case "trajectory":
		addedStart := strings.Index(rendered, "  - kind=added ")
		removedStart := strings.Index(rendered, "  - kind=removed ")
		topUnresolved := strings.LastIndex(rendered, "\nunresolved:\n")
		if addedStart < 0 || removedStart <= addedStart || topUnresolved <= removedStart {
			t.Fatalf("trajectory sections are missing:\n%s", rendered)
		}
		added := rendered[addedStart:removedStart]
		removed := rendered[removedStart:topUnresolved]
		unresolved := rendered[topUnresolved:]
		if strings.Contains(added, "\n    before:\n") ||
			!strings.Contains(added, "\n    after:\n") ||
			!strings.Contains(added, "observation_id=observation-after") ||
			!strings.Contains(added, "evidence_id=evidence-after role=supporting") ||
			!strings.Contains(added, "observation_id=observation-unresolved") ||
			strings.Contains(removed, "\n    after:\n") ||
			!strings.Contains(removed, "\n    before:\n") ||
			!strings.Contains(unresolved, "observation_id=observation-unresolved") ||
			!strings.Contains(unresolved, "evidence_id=evidence-unresolved role=contradicting") {
			t.Fatalf("trajectory text associations are incorrect:\n%s", rendered)
		}
	case "causal":
		if strings.Count(rendered, "  - cause=") != 1 ||
			!strings.Contains(rendered, "cause=text:\"decision\" effect=text:\"delivery\"") ||
			!strings.Contains(rendered, "observation_id=observation-causal") ||
			!strings.Contains(rendered, "evidence_id=evidence-causal-support role=supporting") ||
			!strings.Contains(rendered, "evidence_id=evidence-causal-counter role=contradicting") {
			t.Fatalf("causal text associations are incorrect:\n%s", rendered)
		}
	default:
		t.Fatalf("unknown result name %q", name)
	}
}

type countingQueryWriter struct {
	calls int
}

func (writer *countingQueryWriter) Write(value []byte) (int, error) {
	writer.calls++
	return len(value), nil
}
