package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func TestQueryJSONPreservesExactRemainingIntentAssociations(t *testing.T) {
	for name, result := range populatedRemainingIntentResults(t) {
		t.Run(name, func(t *testing.T) {
			rendered, err := renderQueryJSON(result)
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(bytes.NewReader(rendered))
			decoder.DisallowUnknownFields()
			var got remainingIntentJSONEnvelope
			if err := decoder.Decode(&got); err != nil {
				t.Fatalf("strict JSON decode error = %v", err)
			}
			var extra json.RawMessage
			if err := decoder.Decode(&extra); err != io.EOF {
				t.Fatalf("strict JSON trailing decode error = %v, want EOF", err)
			}
			want := expectedRemainingIntentJSON(name)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s JSON associations mismatch\n got: %#v\nwant: %#v", name, got, want)
			}
		})
	}
}

func TestQueryTextUsesExactRemainingIntentOracles(t *testing.T) {
	for name, result := range populatedRemainingIntentResults(t) {
		t.Run(name, func(t *testing.T) {
			text, err := renderQueryText(result)
			if err != nil {
				t.Fatal(err)
			}
			want := expectedRemainingIntentText(name)
			if string(text) != want {
				t.Fatalf("%s text mismatch\n--- got ---\n%s--- want ---\n%s", name, text, want)
			}
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

func TestQueryCommandRejectsUnauthorizedGapContextsWithoutWritingForEveryIntent(t *testing.T) {
	results := populatedRemainingIntentResults(t)
	results["trend"] = populatedTrendResult(t, false)
	actions := map[string]Action{
		"point": ActionPoint, "trend": ActionTrend, "trajectory": ActionTrajectory, "causal": ActionCausal,
	}
	for name, result := range results {
		t.Run(name, func(t *testing.T) {
			result.Gaps = []query.Gap{{Kind: query.GapNoEvidence, EntityID: "private-entity"}}
			service := &recordingQueryService{result: result}
			writer := &countingQueryWriter{}
			err := (QueryCommand{Service: service, Output: writer}).Run(t.Context(), Invocation{
				Command: CommandQuery, Action: actions[name],
				Query: &QueryInput{Request: requestFromResult(result), Output: QueryOutputJSON},
			})
			if err == nil {
				t.Fatal("Run() error = nil, want invalid result error")
			}
			if strings.Contains(err.Error(), "private-entity") {
				t.Fatalf("Run() error exposed private gap context: %q", err)
			}
			if service.calls != 1 || writer.calls != 0 {
				t.Fatalf("service/writer calls = %d/%d, want 1/0", service.calls, writer.calls)
			}
		})
	}
}

func TestQueryCommandRejectsChronologyResultsAboveLimitWithoutWriting(t *testing.T) {
	results := populatedRemainingIntentResults(t)
	trajectoryResult := results["trajectory"]
	trajectoryResult.Limit = 1
	causalResult := results["causal"]
	causal, ok := causalResult.Payload.Causal()
	if !ok {
		t.Fatal("Payload.Causal() = false")
	}
	causal.Links = append(causal.Links, query.CausalLink{
		Cause:  mustQueryText(t, "second-decision"),
		Effect: mustQueryText(t, "second-delivery"),
		Contributions: []query.Contribution{testContribution(
			t,
			"observation-causal-second",
			observation.StatusObserved,
			mustQueryInstant(t, testInstant(2026, time.January, 17)),
			observation.Derivation{Method: "synthetic", Version: "v1"},
			"",
			"",
		)},
		SupportingCitations: []query.Citation{
			testCitation("evidence-causal-second", observation.EvidenceSupporting, false),
		},
		ContradictingCitations: []query.Citation{},
	})
	payload, err := query.NewCausalPayload(causal)
	if err != nil {
		t.Fatalf("query.NewCausalPayload() error = %v", err)
	}
	causalResult.Payload = payload
	causalResult.Limit = 1
	tests := []struct {
		name   string
		action Action
		result query.Result
	}{
		{name: "trajectory", action: ActionTrajectory, result: trajectoryResult},
		{name: "causal", action: ActionCausal, result: causalResult},
	}
	for _, test := range tests {
		for _, output := range []QueryOutput{QueryOutputText, QueryOutputJSON} {
			t.Run(test.name+" "+string(output), func(t *testing.T) {
				result := test.result
				request := requestFromResult(result)
				service := &recordingQueryService{result: result}
				writer := &countingQueryWriter{}
				err := (QueryCommand{Service: service, Output: writer}).Run(t.Context(), Invocation{
					Command: CommandQuery, Action: test.action,
					Query: &QueryInput{Request: request, Output: output},
				})
				if err == nil {
					t.Fatal("Run() error = nil, want invalid chronology result error")
				}
				if service.calls != 1 || writer.calls != 0 {
					t.Fatalf("service/writer calls = %d/%d, want 1/0", service.calls, writer.calls)
				}
			})
		}
	}
}

func populatedRemainingIntentResults(t *testing.T) map[string]query.Result {
	t.Helper()
	point := mustQueryPoint(t, "point", testInstant(2026, time.January, 16))
	window := mustQueryWindow(t, "between", testInstant(2026, time.January, 1), testInstant(2026, time.February, 1))
	key := mustQueryKey(t, mustQueryEntity(t, "entity-a"), "project.atlas/owner")
	unresolvedKey := mustQueryKey(t, mustQueryEntity(t, "entity-a"), "project.atlas/risk")
	topOnlyKey := mustQueryKey(t, mustQueryEntity(t, "entity-a"), "project.atlas/scope")
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
	transitionUnresolvedFact := unresolvedFact
	transitionUnresolvedFact.Key = key
	transitionUnresolved := query.UnresolvedItem{
		Key: key, Reason: temporal.UnresolvedHypothesis, Candidates: []query.Fact{transitionUnresolvedFact},
	}
	topOnlyFact := testFact(t, topOnlyKey, mustQueryText(t, "expanded"),
		testContribution(t, "observation-top-only", observation.StatusHypothesized,
			mustQueryInstant(t, testInstant(2026, time.January, 18)),
			observation.Derivation{Method: "review", Version: "v2"}, "", ""),
		testCitation("evidence-top-only", observation.EvidenceSupporting, true),
	)
	topOnly := query.UnresolvedItem{
		Key: topOnlyKey, Reason: temporal.UnresolvedHypothesis, Candidates: []query.Fact{topOnlyFact},
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
				After:     queryFactPointer(after), Unresolved: []query.UnresolvedItem{transitionUnresolved},
			},
			{
				Kind: temporal.ChangeRemoved, Key: key,
				ValidTime: mustQueryInstant(t, testInstant(2026, time.January, 20)),
				Before:    queryFactPointer(after), Unresolved: []query.UnresolvedItem{},
			},
		},
		Unresolved: []query.UnresolvedItem{topOnly},
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

type remainingIntentJSONEnvelope struct {
	SchemaVersion string                   `json:"schema_version"`
	Intent        string                   `json:"intent"`
	Request       queryRequestJSON         `json:"request"`
	Result        remainingIntentJSONUnion `json:"result"`
	Gaps          []queryGapJSON           `json:"gaps"`
}

type remainingIntentJSONUnion struct {
	Point      *queryPointJSON      `json:"point,omitempty"`
	Trajectory *queryTrajectoryJSON `json:"trajectory,omitempty"`
	Causal     *queryCausalJSON     `json:"causal,omitempty"`
}

func expectedRemainingIntentJSON(name string) remainingIntentJSONEnvelope {
	pointSelection := querySelectionJSON{
		Kind: "point", Label: "point", At: "2026-01-16T13:09:10.654321Z",
	}
	windowSelection := querySelectionJSON{
		Kind: "window", Label: "between",
		Start: "2026-01-01T13:09:10.654321Z",
		End:   "2026-02-01T13:09:10.654321Z",
	}
	ownerKey := queryStateKeyJSON{
		Subject:   queryTermJSONDTO{Kind: "entity", EntityID: "entity-a"},
		Predicate: "project.atlas/owner",
	}
	riskKey := queryStateKeyJSON{
		Subject:   queryTermJSONDTO{Kind: "entity", EntityID: "entity-a"},
		Predicate: "project.atlas/risk",
	}
	scopeKey := queryStateKeyJSON{
		Subject:   queryTermJSONDTO{Kind: "entity", EntityID: "entity-a"},
		Predicate: "project.atlas/scope",
	}
	beforeFact := remainingIntentJSONFact(
		ownerKey,
		"alex",
		"observation-before",
		"observed",
		queryExtentJSON{Kind: "interval", Start: "2026-01-01T13:09:10.654321Z"},
		"synthetic",
		"v1",
		"evidence-before",
		"supporting",
		false,
	)
	afterFact := remainingIntentJSONFact(
		ownerKey,
		"blair",
		"observation-after",
		"observed",
		queryExtentJSON{Kind: "instant", At: "2026-01-16T13:09:10.654321Z"},
		"synthetic",
		"v1",
		"evidence-after",
		"supporting",
		false,
	)
	transitionUnresolvedFact := remainingIntentJSONFact(
		riskKey,
		"casey",
		"observation-unresolved",
		"hypothesized",
		queryExtentJSON{Kind: "unknown"},
		"synthetic",
		"v1",
		"evidence-unresolved",
		"contradicting",
		false,
	)
	topOnlyFact := remainingIntentJSONFact(
		scopeKey,
		"expanded",
		"observation-top-only",
		"hypothesized",
		queryExtentJSON{Kind: "instant", At: "2026-01-18T13:09:10.654321Z"},
		"review",
		"v2",
		"evidence-top-only",
		"supporting",
		true,
	)
	transitionUnresolved := queryUnresolvedJSON{
		Key: riskKey, Reason: "hypothesized",
		Candidates: []queryFactJSON{transitionUnresolvedFact},
	}
	ownerTransitionUnresolvedFact := transitionUnresolvedFact
	ownerTransitionUnresolvedFact.Key = ownerKey
	ownerTransitionUnresolved := queryUnresolvedJSON{
		Key: ownerKey, Reason: "hypothesized",
		Candidates: []queryFactJSON{ownerTransitionUnresolvedFact},
	}
	topOnlyUnresolved := queryUnresolvedJSON{
		Key: scopeKey, Reason: "hypothesized",
		Candidates: []queryFactJSON{topOnlyFact},
	}
	envelope := remainingIntentJSONEnvelope{
		SchemaVersion: temporalQuerySchemaVersion,
		Request: queryRequestJSON{
			EntityIDs: []string{"entity-a"}, EntityMatch: "all",
			KnowledgeScope: queryKnowledgeJSON{Kind: "current"},
		},
		Gaps: []queryGapJSON{},
	}
	switch name {
	case "point":
		envelope.Intent = "point-in-time"
		envelope.Request.Predicates = []string{"project.atlas/owner"}
		envelope.Request.Selections = []querySelectionJSON{pointSelection}
		envelope.Result.Point = &queryPointJSON{
			Selection:  pointSelection,
			Facts:      []queryFactJSON{beforeFact},
			Unresolved: []queryUnresolvedJSON{transitionUnresolved},
		}
	case "trajectory":
		envelope.Intent = "trajectory"
		envelope.Request.Predicates = []string{"project.atlas/owner"}
		envelope.Request.Selections = []querySelectionJSON{windowSelection}
		envelope.Request.Limit = 4
		envelope.Result.Trajectory = &queryTrajectoryJSON{
			Selection: windowSelection,
			Transitions: []queryTransitionJSON{
				{
					Kind: "added", Key: ownerKey,
					ValidTime:  queryExtentJSON{Kind: "instant", At: "2026-01-16T13:09:10.654321Z"},
					After:      &afterFact,
					Unresolved: []queryUnresolvedJSON{ownerTransitionUnresolved},
				},
				{
					Kind: "removed", Key: ownerKey,
					ValidTime:  queryExtentJSON{Kind: "instant", At: "2026-01-20T13:09:10.654321Z"},
					Before:     &afterFact,
					Unresolved: []queryUnresolvedJSON{},
				},
			},
			Unresolved: []queryUnresolvedJSON{topOnlyUnresolved},
		}
	case "causal":
		envelope.Intent = "causal-chain"
		envelope.Request.Predicates = []string{"stacks.causal.v1/causes"}
		envelope.Request.Selections = []querySelectionJSON{windowSelection}
		envelope.Request.Limit = 4
		envelope.Result.Causal = &queryCausalJSON{
			Selection: windowSelection,
			Links: []queryCausalLinkJSON{{
				Cause:  queryTermJSONDTO{Kind: "text", Text: "decision"},
				Effect: queryTermJSONDTO{Kind: "text", Text: "delivery"},
				Contributions: []queryContributionJSON{remainingIntentJSONContribution(
					"observation-causal",
					"observed",
					queryExtentJSON{Kind: "instant", At: "2026-01-16T13:09:10.654321Z"},
					"synthetic",
					"v1",
				)},
				SupportingCitations: []queryCitationJSON{
					remainingIntentJSONCitation("evidence-causal-support", "supporting", false),
				},
				ContradictingCitations: []queryCitationJSON{
					remainingIntentJSONCitation("evidence-causal-counter", "contradicting", false),
				},
			}},
		}
		envelope.Gaps = []queryGapJSON{{Kind: "authority-excluded"}}
	}
	return envelope
}

func remainingIntentJSONFact(
	key queryStateKeyJSON,
	value string,
	observationID string,
	status string,
	validTime queryExtentJSON,
	method string,
	version string,
	evidenceID string,
	role string,
	populatedOptional bool,
) queryFactJSON {
	fact := queryFactJSON{
		Key: key, Value: queryTermJSONDTO{Kind: "text", Text: value},
		Contributions: []queryContributionJSON{
			remainingIntentJSONContribution(
				observationID,
				status,
				validTime,
				method,
				version,
			),
		},
		SupportingCitations:    []queryCitationJSON{},
		ContradictingCitations: []queryCitationJSON{},
	}
	citation := remainingIntentJSONCitation(evidenceID, role, populatedOptional)
	if role == "supporting" {
		fact.SupportingCitations = []queryCitationJSON{citation}
	} else {
		fact.ContradictingCitations = []queryCitationJSON{citation}
	}
	return fact
}

func remainingIntentJSONContribution(
	observationID string,
	status string,
	validTime queryExtentJSON,
	method string,
	version string,
) queryContributionJSON {
	return queryContributionJSON{
		ObservationID: observationID,
		Status:        status,
		ValidTime:     validTime,
		RecordedAt:    "2026-06-01T09:06:07.123456Z",
		Derivation:    queryDerivationJSON{Method: method, Version: version},
	}
}

func remainingIntentJSONCitation(
	evidenceID string,
	role string,
	populatedOptional bool,
) queryCitationJSON {
	citation := queryCitationJSON{
		EvidenceID:        evidenceID,
		Role:              role,
		SourceDocumentID:  "document-" + evidenceID,
		DocumentVersionID: "version-" + evidenceID,
		SectionID:         "section-" + evidenceID,
		SectionTitle:      "Synthetic section",
		SectionPath:       []string{},
		SectionOrder:      2,
		SectionRole:       "body",
		StartOffset:       3,
		EndOffset:         11,
	}
	if populatedOptional {
		citation.SectionPath = []string{"Parent", "Child"}
		citation.Locator = "synthetic://document/" + evidenceID
		citation.Text = "exact synthetic bytes"
	}
	return citation
}

func expectedRemainingIntentText(name string) string {
	switch name {
	case "point":
		return `intent: point-in-time
entities: entity-a
entity match: all
predicates: project.atlas/owner
point point: 2026-01-16T13:09:10.654321Z
knowledge scope: current
limit: 0
facts:
  - key: subject=entity:entity-a predicate=project.atlas/owner value=text:"alex"
    contributions:
      - observation_id=observation-before status=observed valid_time=interval:[2026-01-01T13:09:10.654321Z,) recorded_at=2026-06-01T09:06:07.123456Z derivation_method=synthetic derivation_version=v1
    supporting citations:
      - evidence_id=evidence-before role=supporting source_document_id=document-evidence-before document_version_id=version-evidence-before section_id=section-evidence-before section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
    contradicting citations:
      (none)
unresolved:
  - key: subject=entity:entity-a predicate=project.atlas/risk reason=hypothesized
    candidates:
      - key: subject=entity:entity-a predicate=project.atlas/risk value=text:"casey"
        contributions:
          - observation_id=observation-unresolved status=hypothesized valid_time=unknown recorded_at=2026-06-01T09:06:07.123456Z derivation_method=synthetic derivation_version=v1
        supporting citations:
          (none)
        contradicting citations:
          - evidence_id=evidence-unresolved role=contradicting source_document_id=document-evidence-unresolved document_version_id=version-evidence-unresolved section_id=section-evidence-unresolved section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
gaps:
  (none)
`
	case "trajectory":
		return `intent: trajectory
entities: entity-a
entity match: all
predicates: project.atlas/owner
between window: [2026-01-01T13:09:10.654321Z, 2026-02-01T13:09:10.654321Z)
knowledge scope: current
limit: 4
transitions:
  - kind=added key: subject=entity:entity-a predicate=project.atlas/owner valid_time=instant:2026-01-16T13:09:10.654321Z
    after:
      - key: subject=entity:entity-a predicate=project.atlas/owner value=text:"blair"
        contributions:
          - observation_id=observation-after status=observed valid_time=instant:2026-01-16T13:09:10.654321Z recorded_at=2026-06-01T09:06:07.123456Z derivation_method=synthetic derivation_version=v1
        supporting citations:
          - evidence_id=evidence-after role=supporting source_document_id=document-evidence-after document_version_id=version-evidence-after section_id=section-evidence-after section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
        contradicting citations:
          (none)
    unresolved:
      - key: subject=entity:entity-a predicate=project.atlas/owner reason=hypothesized
        candidates:
          - key: subject=entity:entity-a predicate=project.atlas/owner value=text:"casey"
            contributions:
              - observation_id=observation-unresolved status=hypothesized valid_time=unknown recorded_at=2026-06-01T09:06:07.123456Z derivation_method=synthetic derivation_version=v1
            supporting citations:
              (none)
            contradicting citations:
              - evidence_id=evidence-unresolved role=contradicting source_document_id=document-evidence-unresolved document_version_id=version-evidence-unresolved section_id=section-evidence-unresolved section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
  - kind=removed key: subject=entity:entity-a predicate=project.atlas/owner valid_time=instant:2026-01-20T13:09:10.654321Z
    before:
      - key: subject=entity:entity-a predicate=project.atlas/owner value=text:"blair"
        contributions:
          - observation_id=observation-after status=observed valid_time=instant:2026-01-16T13:09:10.654321Z recorded_at=2026-06-01T09:06:07.123456Z derivation_method=synthetic derivation_version=v1
        supporting citations:
          - evidence_id=evidence-after role=supporting source_document_id=document-evidence-after document_version_id=version-evidence-after section_id=section-evidence-after section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
        contradicting citations:
          (none)
    unresolved:
      (none)
unresolved:
  - key: subject=entity:entity-a predicate=project.atlas/scope reason=hypothesized
    candidates:
      - key: subject=entity:entity-a predicate=project.atlas/scope value=text:"expanded"
        contributions:
          - observation_id=observation-top-only status=hypothesized valid_time=instant:2026-01-18T13:09:10.654321Z recorded_at=2026-06-01T09:06:07.123456Z derivation_method=review derivation_version=v2
        supporting citations:
          - evidence_id=evidence-top-only role=supporting source_document_id=document-evidence-top-only document_version_id=version-evidence-top-only section_id=section-evidence-top-only section_title="Synthetic section" section_path=["Parent" "Child"] section_order=2 section_role=body offsets=3:11 locator="synthetic://document/evidence-top-only" text="exact synthetic bytes"
        contradicting citations:
          (none)
gaps:
  (none)
`
	case "causal":
		return `intent: causal-chain
entities: entity-a
entity match: all
predicates: stacks.causal.v1/causes
between window: [2026-01-01T13:09:10.654321Z, 2026-02-01T13:09:10.654321Z)
knowledge scope: current
limit: 4
links:
  - cause=text:"decision" effect=text:"delivery"
    contributions:
      - observation_id=observation-causal status=observed valid_time=instant:2026-01-16T13:09:10.654321Z recorded_at=2026-06-01T09:06:07.123456Z derivation_method=synthetic derivation_version=v1
    supporting citations:
      - evidence_id=evidence-causal-support role=supporting source_document_id=document-evidence-causal-support document_version_id=version-evidence-causal-support section_id=section-evidence-causal-support section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
    contradicting citations:
      - evidence_id=evidence-causal-counter role=contradicting source_document_id=document-evidence-causal-counter document_version_id=version-evidence-causal-counter section_id=section-evidence-causal-counter section_title="Synthetic section" section_path=[] section_order=2 section_role=body offsets=3:11
gaps:
  - kind=authority-excluded
`
	default:
		return ""
	}
}

type countingQueryWriter struct {
	calls int
}

func (writer *countingQueryWriter) Write(value []byte) (int, error) {
	writer.calls++
	return len(value), nil
}
