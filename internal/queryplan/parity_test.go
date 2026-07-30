package queryplan

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
	"stacks/internal/query"
)

func TestPlannedAndDirectTemporalQueriesPreserveFourIntentParity(t *testing.T) {
	fixture := newPlannerParityFixture(t)
	direct := query.Service{Reader: fixture.reader, Limits: fixture.limits}

	for _, test := range []struct {
		name     string
		proposal string
		request  query.Request
	}{
		{name: "point", proposal: fixture.pointProposal, request: fixture.pointRequest},
		{name: "trend", proposal: fixture.trendProposal, request: fixture.trendRequest},
		{name: "trajectory", proposal: fixture.trajectoryProposal, request: fixture.trajectoryRequest},
		{name: "causal", proposal: fixture.causalProposal, request: fixture.causalRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			wantRequest, err := query.NormalizeRequest(test.request, fixture.limits)
			if err != nil {
				t.Fatal(err)
			}
			wantResult, err := direct.Query(t.Context(), wantRequest)
			if err != nil {
				t.Fatalf("direct Query() error = %v", err)
			}
			service := Service{
				Model: modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
					return syntheticModelResponse([]byte(test.proposal)), nil
				}),
				Executor:         plannerParityExecutor{service: direct},
				Limits:           fixture.limits,
				PlannerTimeout:   time.Second,
				MaxQuestionBytes: 1024,
			}
			execution, err := service.Ask(t.Context(), Input{
				Question:      "What changed in the synthetic atlas?",
				EntityIDs:     fixture.entityIDs,
				ReferenceTime: fixture.referenceTime,
			})
			if err != nil {
				t.Fatalf("Ask() error = %v", err)
			}
			if !reflect.DeepEqual(execution.Request, wantRequest) {
				t.Fatalf("planned request = %#v, want normalized direct request %#v", execution.Request, wantRequest)
			}
			if !reflect.DeepEqual(execution.Result, wantResult) {
				t.Fatalf("planned result = %#v, want direct result %#v", execution.Result, wantResult)
			}
			assertPlannerParityEvidence(t, execution.Result)
		})
	}
}

func TestPlannedAndDirectKnownAsOfExcludesLateRecordedEvidence(t *testing.T) {
	fixture := newPlannerParityFixture(t)
	direct := query.Service{Reader: fixture.reader, Limits: fixture.limits}
	wantRequest, err := query.NormalizeRequest(fixture.historicalPointRequest, fixture.limits)
	if err != nil {
		t.Fatal(err)
	}
	wantResult, err := direct.Query(t.Context(), wantRequest)
	if err != nil {
		t.Fatalf("direct Query() error = %v", err)
	}
	service := Service{
		Model: modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
			return syntheticModelResponse([]byte(fixture.historicalPointProposal)), nil
		}),
		Executor:         plannerParityExecutor{service: direct},
		Limits:           fixture.limits,
		PlannerTimeout:   time.Second,
		MaxQuestionBytes: 1024,
	}
	execution, err := service.Ask(t.Context(), Input{
		Question:      "What was true before the recorded cutoff?",
		EntityIDs:     fixture.entityIDs,
		ReferenceTime: fixture.referenceTime,
	})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if !reflect.DeepEqual(execution.Request, wantRequest) || !reflect.DeepEqual(execution.Result, wantResult) {
		t.Fatalf("planned execution = %#v, want direct request/result %#v / %#v", execution, wantRequest, wantResult)
	}
	point, ok := execution.Result.Payload.Point()
	if !ok || len(point.Facts) != 0 || len(point.Unresolved) != 0 {
		t.Fatalf("historical point = %#v, want late-recorded values excluded", point)
	}
}

func TestPlannedTrajectoryLimitFailsWithoutPartialExecution(t *testing.T) {
	fixture := newPlannerParityFixture(t)
	direct := query.Service{Reader: fixture.reader, Limits: fixture.limits}
	service := Service{
		Model: modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
			return syntheticModelResponse([]byte(fixture.trajectoryOverflowProposal)), nil
		}),
		Executor:         plannerParityExecutor{service: direct},
		Limits:           fixture.limits,
		PlannerTimeout:   time.Second,
		MaxQuestionBytes: 1024,
	}
	_, err := service.Ask(t.Context(), Input{Question: "Show every transition.", EntityIDs: fixture.entityIDs, ReferenceTime: fixture.referenceTime})
	if !errors.Is(err, query.ErrLimitExceeded) {
		t.Fatalf("Ask() error = %v, want query.ErrLimitExceeded", err)
	}
}

func TestPlannedCausalLimitFailsWithoutPartialExecution(t *testing.T) {
	fixture := newPlannerParityFixture(t)
	direct := query.Service{Reader: fixture.reader, Limits: fixture.limits}
	service := Service{
		Model: modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
			return syntheticModelResponse([]byte(fixture.causalOverflowProposal)), nil
		}),
		Executor:         plannerParityExecutor{service: direct},
		Limits:           fixture.limits,
		PlannerTimeout:   time.Second,
		MaxQuestionBytes: 1024,
	}
	_, err := service.Ask(t.Context(), Input{Question: "Show the causal chain.", EntityIDs: fixture.entityIDs, ReferenceTime: fixture.referenceTime})
	if !errors.Is(err, query.ErrLimitExceeded) {
		t.Fatalf("Ask() error = %v, want query.ErrLimitExceeded", err)
	}
}

type plannerParityExecutor struct{ service query.Service }

func (executor plannerParityExecutor) Query(ctx context.Context, request query.Request) (query.Result, error) {
	return executor.service.Query(ctx, request)
}

type plannerParityReader struct{ snapshot query.ReadSnapshot }

func (reader plannerParityReader) Read(_ context.Context, selection query.ReadSelection) (query.ReadSnapshot, error) {
	observations := make([]query.ReadObservation, 0, len(reader.snapshot.Observations))
	for _, item := range reader.snapshot.Observations {
		for _, predicate := range selection.Predicates {
			if item.Observation.Statement().Predicate == predicate {
				observations = append(observations, item)
				break
			}
		}
	}
	coverage := make([]query.Coverage, 0, len(reader.snapshot.Coverage))
	for _, item := range reader.snapshot.Coverage {
		for _, predicate := range selection.Predicates {
			if item.Predicate == predicate {
				coverage = append(coverage, item)
				break
			}
		}
	}
	result := reader.snapshot
	result.Observations = observations
	result.Coverage = coverage
	return result, nil
}

type plannerParityFixture struct {
	reader                     plannerParityReader
	limits                     query.Limits
	entityIDs                  []identity.EntityID
	referenceTime              time.Time
	pointRequest               query.Request
	trendRequest               query.Request
	trajectoryRequest          query.Request
	causalRequest              query.Request
	historicalPointRequest     query.Request
	pointProposal              string
	trendProposal              string
	trajectoryProposal         string
	causalProposal             string
	historicalPointProposal    string
	trajectoryOverflowProposal string
	causalOverflowProposal     string
}

func newPlannerParityFixture(t *testing.T) plannerParityFixture {
	t.Helper()
	ownerID := identity.EntityID("entity:planner-atlas-owner")
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	boundary := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, time.January, 20, 0, 0, 0, 0, time.UTC)
	point, err := temporal.At("point", boundary)
	if err != nil {
		t.Fatal(err)
	}
	before, err := temporal.Between("before", start, boundary)
	if err != nil {
		t.Fatal(err)
	}
	after, err := temporal.Between("after", boundary, end)
	if err != nil {
		t.Fatal(err)
	}
	between, err := temporal.Between("between", start, end)
	if err != nil {
		t.Fatal(err)
	}
	historical, err := temporal.KnownAsOf(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := observation.NewEntityTerm(string(ownerID), "")
	if err != nil {
		t.Fatal(err)
	}
	text := func(value string) observation.Term {
		term, err := observation.NewTextTerm(value)
		if err != nil {
			t.Fatal(err)
		}
		return term
	}
	during := func(from, until time.Time) observation.TemporalExtent {
		value, err := observation.During(from, until)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	at := func(value time.Time) observation.TemporalExtent {
		result, err := observation.AtTime(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	citation := func(id string, role observation.EvidenceRole) query.Citation {
		return query.Citation{EvidenceID: evidence.EvidenceID("evidence:" + id), Role: role, SourceDocumentID: "synthetic-document", DocumentVersionID: "synthetic-version", SectionID: "section:synthetic", SectionTitle: "Synthetic", SectionPath: []string{"Synthetic"}, SectionOrder: 1, SectionRole: "fixture", StartOffset: 0, EndOffset: 8, Locator: "synthetic://planner-parity", Text: "synthetic"}
	}
	newObservation := func(id string, subject observation.Term, predicate observation.Predicate, object observation.Term, valid observation.TemporalExtent, recorded time.Time, status observation.EpistemicStatus, roles []observation.EvidenceRole) query.ReadObservation {
		links := make([]observation.EvidenceLink, len(roles))
		citations := make([]query.Citation, len(roles))
		for index, role := range roles {
			evidenceID := evidence.EvidenceID("evidence:" + id + ":" + string(role))
			links[index] = observation.EvidenceLink{EvidenceID: evidenceID, Role: role}
			citations[index] = citation(id+":"+string(role), role)
			citations[index].EvidenceID = evidenceID
		}
		value, err := observation.NewObservation(observation.ObservationInput{ID: observation.ObservationID("observation:" + id), Statement: observation.Statement{Subject: subject, Predicate: predicate, Object: object}, ValidTime: valid, RecordedAt: recorded, Evidence: links, Derivation: observation.Derivation{Method: "synthetic", Version: "planner-parity-v1"}, Status: status})
		if err != nil {
			t.Fatal(err)
		}
		return query.ReadObservation{Observation: value, Subject: subject, Object: object, Evidence: citations}
	}
	responsibility := observation.Predicate("planner.atlas/responsibility")
	uncertainty := observation.Predicate("planner.atlas/uncertainty")
	initial := newObservation("initial", owner, responsibility, text("Alex"), during(start, boundary), start.Add(time.Hour), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting})
	transfer := newObservation("transfer", owner, responsibility, text("Blair"), during(boundary, end), cutoff.Add(24*time.Hour), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting})
	conflict := newObservation("conflict", owner, responsibility, text("Casey"), during(boundary, end), cutoff.Add(25*time.Hour), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting, observation.EvidenceContradicting})
	hypothesis := newObservation("hypothesis", owner, uncertainty, text("uncertain"), observation.UnknownTime(), start.Add(2*time.Hour), observation.StatusHypothesized, []observation.EvidenceRole{observation.EvidenceSupporting})
	causeOne := newObservation("cause-one", owner, query.CausalPredicate, text("handoff"), at(start.Add(5*24*time.Hour)), start.Add(3*time.Hour), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting, observation.EvidenceContradicting})
	causeTwo := newObservation("cause-two", text("handoff"), query.CausalPredicate, owner, at(start.Add(10*24*time.Hour)), start.Add(4*time.Hour), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting})
	limits := query.Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 20}
	fixture := plannerParityFixture{
		reader: plannerParityReader{snapshot: query.ReadSnapshot{
			Entities:     []query.EntityAuthority{{EntityID: ownerID, Known: true}},
			Observations: []query.ReadObservation{initial, transfer, conflict, hypothesis, causeOne, causeTwo},
			Coverage: []query.Coverage{
				{Reason: query.CoverageUnresolvedMention, EntityID: ownerID, Predicate: responsibility, ObservationID: "observation:unresolved-mention", ValidTime: observation.UnknownTime()},
				{Reason: query.CoverageAuthorityExcluded, EntityID: ownerID, Predicate: responsibility, ObservationID: "observation:authority-excluded", ValidTime: observation.UnknownTime()},
			},
		}},
		limits: limits, entityIDs: []identity.EntityID{ownerID}, referenceTime: end,
		pointRequest:           query.Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{responsibility}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge()},
		trendRequest:           query.Request{Intent: temporal.IntentTrendComparison, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{responsibility}, Selections: []temporal.TemporalSelection{before, after}, KnowledgeScope: temporal.CurrentKnowledge()},
		trajectoryRequest:      query.Request{Intent: temporal.IntentTrajectory, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{responsibility, uncertainty}, Selections: []temporal.TemporalSelection{between}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 20},
		causalRequest:          query.Request{Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{query.CausalPredicate}, Selections: []temporal.TemporalSelection{between}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 20},
		historicalPointRequest: query.Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{responsibility}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: historical},
	}
	fixture.pointProposal = `{"status":"executable","reason":"none","intent":"point-in-time","entity_match":"all","predicates":["planner.atlas/responsibility"],"selections":[{"kind":"point","label":"point","at":"2026-01-15T00:00:00Z","start":"","end":""}],"knowledge_scope":{"kind":"current","as_of":""},"chronology_limit":0}`
	fixture.trendProposal = `{"status":"executable","reason":"none","intent":"trend-comparison","entity_match":"all","predicates":["planner.atlas/responsibility"],"selections":[{"kind":"window","label":"before","at":"","start":"2026-01-01T00:00:00Z","end":"2026-01-15T00:00:00Z"},{"kind":"window","label":"after","at":"","start":"2026-01-15T00:00:00Z","end":"2026-02-01T00:00:00Z"}],"knowledge_scope":{"kind":"current","as_of":""},"chronology_limit":0}`
	fixture.trajectoryProposal = `{"status":"executable","reason":"none","intent":"trajectory","entity_match":"all","predicates":["planner.atlas/responsibility","planner.atlas/uncertainty"],"selections":[{"kind":"window","label":"between","at":"","start":"2026-01-01T00:00:00Z","end":"2026-02-01T00:00:00Z"}],"knowledge_scope":{"kind":"current","as_of":""},"chronology_limit":20}`
	fixture.causalProposal = `{"status":"executable","reason":"none","intent":"causal-chain","entity_match":"all","predicates":["stacks.causal.v1/causes"],"selections":[{"kind":"window","label":"between","at":"","start":"2026-01-01T00:00:00Z","end":"2026-02-01T00:00:00Z"}],"knowledge_scope":{"kind":"current","as_of":""},"chronology_limit":20}`
	fixture.historicalPointProposal = `{"status":"executable","reason":"none","intent":"point-in-time","entity_match":"all","predicates":["planner.atlas/responsibility"],"selections":[{"kind":"point","label":"point","at":"2026-01-15T00:00:00Z","start":"","end":""}],"knowledge_scope":{"kind":"as-of","as_of":"2026-01-20T00:00:00Z"},"chronology_limit":0}`
	fixture.trajectoryOverflowProposal = `{"status":"executable","reason":"none","intent":"trajectory","entity_match":"all","predicates":["planner.atlas/responsibility"],"selections":[{"kind":"window","label":"between","at":"","start":"2026-01-01T00:00:00Z","end":"2026-02-01T00:00:00Z"}],"knowledge_scope":{"kind":"current","as_of":""},"chronology_limit":1}`
	fixture.causalOverflowProposal = `{"status":"executable","reason":"none","intent":"causal-chain","entity_match":"all","predicates":["stacks.causal.v1/causes"],"selections":[{"kind":"window","label":"between","at":"","start":"2026-01-01T00:00:00Z","end":"2026-02-01T00:00:00Z"}],"knowledge_scope":{"kind":"current","as_of":""},"chronology_limit":1}`
	return fixture
}

func assertPlannerParityEvidence(t *testing.T, result query.Result) {
	t.Helper()
	if err := query.ValidateResult(result); err != nil {
		t.Fatal(err)
	}
	switch result.Intent {
	case temporal.IntentPointInTime:
		point, _ := result.Payload.Point()
		if len(point.Unresolved) == 0 || len(point.Unresolved[0].Candidates) != 2 {
			t.Fatalf("point conflicts = %#v, want competing cited values", point.Unresolved)
		}
		if len(result.Gaps) < 2 {
			t.Fatalf("point gaps = %#v, want unresolved mention and authority exclusion", result.Gaps)
		}
	case temporal.IntentTrajectory:
		trajectory, _ := result.Payload.Trajectory()
		if len(trajectory.Unresolved) == 0 {
			t.Fatalf("trajectory unresolved = %#v, want hypothesis or conflict", trajectory)
		}
	case temporal.IntentCausalChain:
		causal, _ := result.Payload.Causal()
		if len(causal.Links) != 2 || len(causal.Links[0].ContradictingCitations) == 0 {
			t.Fatalf("causal links = %#v, want cited counterevidence", causal)
		}
	}
}
