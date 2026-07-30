package queryplan

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
	"github.com/jackc/pgx/v5"
	"stacks/internal/query"
)

func TestPostgresPlannedAndDirectTemporalQueriesPreserveFourIntentParity(t *testing.T) {
	fixture := seedPlannerAtlasCorpus(t, t.Context(), newPlannerPostgresDatabase(t))
	executor := plannerPostgresExecutor{Database: fixture.database, Limits: fixture.limits}
	direct := query.Service{Reader: query.PostgresRepository{Database: fixture.database}, Limits: fixture.limits}

	for _, test := range []struct {
		name     string
		proposal string
		request  query.Request
	}{
		{name: "point", proposal: fixture.pointProposal, request: fixture.PointRequest},
		{name: "trend", proposal: fixture.trendProposal, request: fixture.TrendRequest},
		{name: "trajectory", proposal: fixture.trajectoryProposal, request: fixture.TrajectoryRequest},
		{name: "causal", proposal: fixture.causalProposal, request: fixture.CausalRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := query.NormalizeRequest(test.request, fixture.limits)
			if err != nil {
				t.Fatal(err)
			}
			directResult, err := direct.Query(t.Context(), request)
			if err != nil {
				t.Fatalf("direct Query() error = %v", err)
			}
			planned, err := Service{
				Model: modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
					return syntheticModelResponse([]byte(test.proposal)), nil
				}),
				Executor: executor, Limits: fixture.limits, PlannerTimeout: time.Second, MaxQuestionBytes: 1024,
			}.Ask(t.Context(), Input{Question: "What changed in the synthetic PostgreSQL atlas?", EntityIDs: fixture.EntityIDs, ReferenceTime: fixture.referenceTime})
			if err != nil {
				t.Fatalf("Ask() error = %v", err)
			}
			assertPlannedDirectParity(t, planned, directResult)
			assertPlannerPostgresEvidence(t, planned.Result)
		})
	}

	historicalRequest, err := query.NormalizeRequest(fixture.HistoricalPointRequest, fixture.limits)
	if err != nil {
		t.Fatal(err)
	}
	directHistorical, err := direct.Query(t.Context(), historicalRequest)
	if err != nil {
		t.Fatalf("direct historical Query() error = %v", err)
	}
	plannedHistorical, err := Service{
		Model: modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
			return syntheticModelResponse([]byte(fixture.historicalPointProposal)), nil
		}),
		Executor: executor, Limits: fixture.limits, PlannerTimeout: time.Second, MaxQuestionBytes: 1024,
	}.Ask(t.Context(), Input{Question: "What was true by the recorded cutoff?", EntityIDs: fixture.EntityIDs, ReferenceTime: fixture.referenceTime})
	if err != nil {
		t.Fatalf("historical Ask() error = %v", err)
	}
	assertPlannedDirectParity(t, plannedHistorical, directHistorical)
	historicalPoint, _ := plannedHistorical.Result.Payload.Point()
	if len(historicalPoint.Facts) != 0 || len(historicalPoint.Unresolved) != 0 {
		t.Fatalf("historical point = %#v, want late-recorded conflict excluded", historicalPoint)
	}

	authorityRequest, err := query.NormalizeRequest(fixture.AuthorityRequest, fixture.limits)
	if err != nil {
		t.Fatal(err)
	}
	authorityResult, err := direct.Query(t.Context(), authorityRequest)
	if err != nil {
		t.Fatalf("direct authority Query() error = %v", err)
	}
	if !containsPlannerGap(authorityResult.Gaps, query.GapAuthorityExcluded) {
		t.Fatalf("authority gaps = %#v, want authority exclusion", authorityResult.Gaps)
	}
}

func TestPostgresPlannerInvalidOutputDoesNotReadSnapshot(t *testing.T) {
	fixture := seedPlannerAtlasCorpus(t, t.Context(), newPlannerPostgresDatabase(t))
	counter := &plannerSnapshotCounter{database: fixture.database}
	executor := query.Service{Reader: query.PostgresRepository{Database: counter}, Limits: fixture.limits}
	for _, test := range []struct {
		name   string
		output string
		reads  int
		valid  bool
	}{
		{name: "malformed", output: `{`, reads: 0},
		{name: "cannot plan", output: cannotPlanProposal, reads: 0},
		{name: "valid", output: fixture.pointProposal, reads: 1, valid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			counter.reads = 0
			_, err := Service{
				Model: modelFunc(func(context.Context, ModelRequest) (ModelResponse, error) {
					return syntheticModelResponse([]byte(test.output)), nil
				}),
				Executor: plannerParityExecutor{service: executor}, Limits: fixture.limits, PlannerTimeout: time.Second, MaxQuestionBytes: 1024,
			}.Ask(t.Context(), Input{Question: "What was true?", EntityIDs: fixture.EntityIDs, ReferenceTime: fixture.referenceTime})
			if test.valid && err != nil {
				t.Fatalf("Ask() error = %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("Ask() error = nil")
			}
			if counter.reads != test.reads {
				t.Fatalf("snapshot reads = %d, want %d", counter.reads, test.reads)
			}
		})
	}
}

type plannerAtlasFixture struct {
	database                *postgres.Database
	limits                  query.Limits
	EntityIDs               []identity.EntityID
	PointRequest            query.Request
	TrendRequest            query.Request
	TrajectoryRequest       query.Request
	CausalRequest           query.Request
	HistoricalPointRequest  query.Request
	AuthorityRequest        query.Request
	referenceTime           time.Time
	pointProposal           string
	trendProposal           string
	trajectoryProposal      string
	causalProposal          string
	historicalPointProposal string
}

type plannerPostgresExecutor struct {
	Database *postgres.Database
	Limits   query.Limits
}

func (executor plannerPostgresExecutor) Query(ctx context.Context, request query.Request) (query.Result, error) {
	return (query.Service{Reader: query.PostgresRepository{Database: executor.Database}, Limits: executor.Limits}).Query(ctx, request)
}

type plannerSnapshotCounter struct {
	database *postgres.Database
	reads    int
}

func (counter *plannerSnapshotCounter) LoadTemporalQuerySnapshot(ctx context.Context, selection postgres.TemporalQuerySelection, observer postgres.TemporalSnapshotObserver) (postgres.TemporalQuerySnapshot, error) {
	counter.reads++
	return counter.database.LoadTemporalQuerySnapshot(ctx, selection, observer)
}

func newPlannerPostgresDatabase(t *testing.T) *postgres.Database {
	t.Helper()
	isolation := postgrestest.NewDatabase(t)
	manifest, err := coremigrations.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	applicationConfig, err := pgx.ParseConfig(isolation.ApplicationURL())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migration.Migrator{DatabaseURL: isolation.AdminURL(), ApplicationRole: applicationConfig.User, Manifests: []migration.Manifest{manifest}}).Apply(t.Context()); err != nil {
		t.Fatalf("apply core migrations: %v", err)
	}
	database, err := postgres.Open(t.Context(), isolation.ApplicationURL())
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	t.Cleanup(database.Close)
	return database
}

func seedPlannerAtlasCorpus(t *testing.T, ctx context.Context, database *postgres.Database) plannerAtlasFixture {
	t.Helper()
	ownerID := identity.EntityID("entity:planner-postgres-atlas-owner")
	start := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	boundary := time.Date(2026, time.March, 15, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, time.March, 20, 0, 0, 0, 0, time.UTC)
	recorded := start.Add(time.Hour)
	sectionSupport, err := evidence.NewSection(evidence.SectionInput{ID: "section:planner-postgres/support", Title: "Synthetic support", Path: []string{"Synthetic Atlas"}, Order: 0, Role: "fixture", Text: "Synthetic support establishes the changing state and causal chain."})
	if err != nil {
		t.Fatal(err)
	}
	sectionCounter, err := evidence.NewSection(evidence.SectionInput{ID: "section:planner-postgres/counter", Title: "Synthetic counterevidence", Path: []string{"Synthetic Atlas"}, Order: 1, Role: "fixture", Text: "Synthetic counterevidence disputes one causal link and competing state."})
	if err != nil {
		t.Fatal(err)
	}
	document, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{Provider: "synthetic", ProviderDocumentID: "planner-postgres-atlas", Title: "Synthetic planner PostgreSQL atlas", Locator: "synthetic://planner-postgres-atlas", ProviderVersion: "v1", ModifiedAt: recorded, RecordedAt: recorded, Sections: []evidence.Section{sectionSupport, sectionCounter}})
	if err != nil {
		t.Fatal(err)
	}
	documentRef, err := database.PutDocumentVersion(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	newSpan := func(section evidence.Section, quote string, at time.Time) evidence.EvidenceSpan {
		t.Helper()
		startOffset := strings.Index(section.Text(), quote)
		if startOffset < 0 {
			t.Fatalf("quote %q absent", quote)
		}
		span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{Document: document, SectionID: section.ID(), StartOffset: startOffset, EndOffset: startOffset + len(quote), Quote: quote, RecordedAt: at})
		if err != nil {
			t.Fatal(err)
		}
		return span
	}
	support := newSpan(sectionSupport, "Synthetic support", recorded.Add(time.Minute))
	counter := newSpan(sectionCounter, "Synthetic counterevidence", recorded.Add(2*time.Minute))
	ownerEntity, err := identity.NewEntity(identity.EntityInput{ID: ownerID, Kind: identity.KindPerson, DisplayName: "Synthetic Planner Atlas Owner", RecordedAt: recorded.Add(3 * time.Minute)})
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
		extent, err := observation.AtTime(value)
		if err != nil {
			t.Fatal(err)
		}
		return extent
	}
	newObservation := func(id string, subject observation.Term, predicate observation.Predicate, object observation.Term, valid observation.TemporalExtent, recordedAt time.Time, status observation.EpistemicStatus, roles []observation.EvidenceRole) observation.Observation {
		links := make([]observation.EvidenceLink, len(roles))
		for index, role := range roles {
			evidenceID := support.ID()
			if role == observation.EvidenceContradicting {
				evidenceID = counter.ID()
			}
			links[index] = observation.EvidenceLink{EvidenceID: evidenceID, Role: role}
		}
		value, err := observation.NewObservation(observation.ObservationInput{ID: observation.ObservationID("observation:planner-postgres/" + id), Statement: observation.Statement{Subject: subject, Predicate: predicate, Object: object}, ValidTime: valid, RecordedAt: recordedAt, Evidence: links, Derivation: observation.Derivation{Method: "synthetic", Version: "planner-postgres-v1"}, Status: status})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	responsibility := observation.Predicate("planner.postgres/responsibility")
	uncertainty := observation.Predicate("planner.postgres/uncertainty")
	authorityPredicate := observation.Predicate("planner.postgres/authority")
	observations := []observation.Observation{
		newObservation("initial", owner, responsibility, text("Alex"), during(start, boundary), recorded.Add(10*time.Minute), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting}),
		newObservation("transfer", owner, responsibility, text("Blair"), during(boundary, end), cutoff.Add(24*time.Hour), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting}),
		newObservation("conflict", owner, responsibility, text("Casey"), during(boundary, end), cutoff.Add(25*time.Hour), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting, observation.EvidenceContradicting}),
		newObservation("hypothesis", owner, uncertainty, text("uncertain"), observation.UnknownTime(), recorded.Add(11*time.Minute), observation.StatusHypothesized, []observation.EvidenceRole{observation.EvidenceSupporting}),
		newObservation("authority", owner, authorityPredicate, text("authority excluded currently"), during(start, end), recorded.Add(12*time.Minute), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting}),
		newObservation("cause-one", owner, query.CausalPredicate, text("handoff"), at(start.Add(5*24*time.Hour)), recorded.Add(13*time.Minute), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting, observation.EvidenceContradicting}),
		newObservation("cause-two", text("handoff"), query.CausalPredicate, owner, at(start.Add(10*24*time.Hour)), recorded.Add(14*time.Minute), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting}),
	}
	admissions := make([]admission.Decision, len(observations))
	for index, value := range observations {
		decision, err := admission.NewDecision(admission.DecisionInput{ID: "admission:" + string(value.ID()), TargetKind: admission.TargetObservation, TargetID: string(value.ID()), Outcome: admission.Admitted, ReasonCode: "synthetic_acceptance", Authority: admission.AuthorityReviewer, RecordedAt: value.RecordedAt().Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		admissions[index] = decision
	}
	retired, err := admission.NewDecision(admission.DecisionInput{ID: "admission:planner-postgres-authority-retired", TargetKind: admission.TargetObservation, TargetID: string(observations[4].ID()), Outcome: admission.Retired, ReasonCode: "synthetic_successor", Authority: admission.AuthorityReviewer, RecordedAt: cutoff.Add(48 * time.Hour), SupersedesID: "admission:" + string(observations[4].ID())})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.InTransaction(ctx, func(transaction *postgres.Transaction) error {
		if err := transaction.SetCurrentDocumentVersion(ctx, documentRef.Ref.SourceDocumentID, documentRef.Ref.VersionID); err != nil {
			return err
		}
		for _, span := range []evidence.EvidenceSpan{support, counter} {
			if _, err := transaction.PutEvidenceSpan(ctx, span); err != nil {
				return err
			}
		}
		if _, err := transaction.PutEntity(ctx, ownerEntity); err != nil {
			return err
		}
		for _, value := range observations {
			if _, err := transaction.PutObservation(ctx, value); err != nil {
				return err
			}
		}
		for _, value := range admissions {
			if err := transaction.AppendAdmissionDecision(ctx, value); err != nil {
				return err
			}
		}
		return transaction.AppendAdmissionDecision(ctx, retired)
	}); err != nil {
		t.Fatalf("seed synthetic planner PostgreSQL corpus: %v", err)
	}
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
	limits := query.Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 20}
	return plannerAtlasFixture{
		database: database, limits: limits, EntityIDs: []identity.EntityID{ownerID}, referenceTime: end,
		PointRequest:            query.Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{responsibility}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge()},
		TrendRequest:            query.Request{Intent: temporal.IntentTrendComparison, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{responsibility}, Selections: []temporal.TemporalSelection{before, after}, KnowledgeScope: temporal.CurrentKnowledge()},
		TrajectoryRequest:       query.Request{Intent: temporal.IntentTrajectory, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{responsibility, uncertainty}, Selections: []temporal.TemporalSelection{between}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 20},
		CausalRequest:           query.Request{Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{query.CausalPredicate}, Selections: []temporal.TemporalSelection{between}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 20},
		HistoricalPointRequest:  query.Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{responsibility}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: mustPlannerKnownAsOf(t, cutoff)},
		AuthorityRequest:        query.Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{authorityPredicate}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge()},
		pointProposal:           `{"status":"executable","reason":"none","intent":"point-in-time","entity_match":"all","predicates":["planner.postgres/responsibility"],"selections":[{"kind":"point","label":"point","at":"2026-03-15T00:00:00Z","start":"","end":""}],"knowledge_scope":{"kind":"current","as_of":""},"chronology_limit":0}`,
		trendProposal:           `{"status":"executable","reason":"none","intent":"trend-comparison","entity_match":"all","predicates":["planner.postgres/responsibility"],"selections":[{"kind":"window","label":"before","at":"","start":"2026-03-01T00:00:00Z","end":"2026-03-15T00:00:00Z"},{"kind":"window","label":"after","at":"","start":"2026-03-15T00:00:00Z","end":"2026-04-01T00:00:00Z"}],"knowledge_scope":{"kind":"current","as_of":""},"chronology_limit":0}`,
		trajectoryProposal:      `{"status":"executable","reason":"none","intent":"trajectory","entity_match":"all","predicates":["planner.postgres/responsibility","planner.postgres/uncertainty"],"selections":[{"kind":"window","label":"between","at":"","start":"2026-03-01T00:00:00Z","end":"2026-04-01T00:00:00Z"}],"knowledge_scope":{"kind":"current","as_of":""},"chronology_limit":20}`,
		causalProposal:          `{"status":"executable","reason":"none","intent":"causal-chain","entity_match":"all","predicates":["stacks.causal.v1/causes"],"selections":[{"kind":"window","label":"between","at":"","start":"2026-03-01T00:00:00Z","end":"2026-04-01T00:00:00Z"}],"knowledge_scope":{"kind":"current","as_of":""},"chronology_limit":20}`,
		historicalPointProposal: `{"status":"executable","reason":"none","intent":"point-in-time","entity_match":"all","predicates":["planner.postgres/responsibility"],"selections":[{"kind":"point","label":"point","at":"2026-03-15T00:00:00Z","start":"","end":""}],"knowledge_scope":{"kind":"as-of","as_of":"2026-03-20T00:00:00Z"},"chronology_limit":0}`,
	}
}

func assertPlannedDirectParity(t *testing.T, planned Execution, direct query.Result) {
	t.Helper()
	if !reflect.DeepEqual(planned.Result, direct) {
		t.Fatalf("planned result = %#v, want direct result %#v", planned.Result, direct)
	}
	if err := query.ValidateResult(planned.Result); err != nil {
		t.Fatal(err)
	}
}

func assertPlannerPostgresEvidence(t *testing.T, result query.Result) {
	t.Helper()
	switch result.Intent {
	case temporal.IntentPointInTime:
		point, _ := result.Payload.Point()
		if len(point.Unresolved) != 1 || len(point.Unresolved[0].Candidates) != 2 {
			t.Fatalf("point conflicts = %#v, want competing alternatives", point)
		}
		if len(point.Unresolved[0].Candidates[0].ContradictingCitations) == 0 && len(point.Unresolved[0].Candidates[1].ContradictingCitations) == 0 {
			t.Fatalf("point conflict lost citation roles: %#v", point.Unresolved)
		}
		if len(result.Gaps) == 0 {
			t.Fatalf("point gaps = %#v, want valid-time coverage", result.Gaps)
		}
	case temporal.IntentTrajectory:
		trajectory, _ := result.Payload.Trajectory()
		if len(trajectory.Unresolved) == 0 {
			t.Fatalf("trajectory = %#v, want hypothesis/uncertainty", trajectory)
		}
	case temporal.IntentCausalChain:
		causal, _ := result.Payload.Causal()
		if len(causal.Links) != 2 || len(causal.Links[0].ContradictingCitations) == 0 {
			t.Fatalf("causal = %#v, want cited counterevidence", causal)
		}
	}
}

func mustPlannerKnownAsOf(t *testing.T, value time.Time) temporal.KnowledgeScope {
	t.Helper()
	scope, err := temporal.KnownAsOf(value)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func containsPlannerGap(gaps []query.Gap, kind query.GapKind) bool {
	for _, gap := range gaps {
		if gap.Kind == kind {
			return true
		}
	}
	return false
}

var _ Executor = plannerPostgresExecutor{}
