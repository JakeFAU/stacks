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
			assertPlannerExpectedRequest(t, planned.Request, request)
			assertPlannedDirectParity(t, planned, directResult)
			assertPlannerPostgresSemantics(t, fixture, test.name, planned.Result)
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
	assertPlannerExpectedRequest(t, plannedHistorical.Request, historicalRequest)
	assertPlannerHistoricalSemantics(t, fixture, plannedHistorical.Result)
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
	ownerID                 identity.EntityID
	responsibilityPredicate observation.Predicate
	uncertaintyPredicate    observation.Predicate
	authorityPredicate      observation.Predicate
	gapPredicate            observation.Predicate
	windowStart             time.Time
	boundary                time.Time
	windowEnd               time.Time
	historicalCutoff        time.Time
	supportCitation         query.Citation
	counterCitation         query.Citation
	PointRequest            query.Request
	TrendRequest            query.Request
	TrajectoryRequest       query.Request
	CausalRequest           query.Request
	HistoricalPointRequest  query.Request
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
	gapMention, err := identity.NewMention(identity.MentionInput{
		ID: "mention:planner-postgres/unresolved", EvidenceID: support.ID(),
		DerivationRunID: "run:planner-postgres/identity", Surface: "Synthetic unresolved participant",
		NormalizedName: identity.NormalizeName("Synthetic unresolved participant"), Role: "participant",
		RecordedAt: recorded.Add(4 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	gapTerm, err := observation.NewMentionTerm(string(gapMention.ID()))
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
	gapPredicate := observation.Predicate("planner.postgres/unresolved-gap")
	observations := []observation.Observation{
		newObservation("initial", owner, responsibility, text("Alex"), during(start, end), recorded.Add(10*time.Minute), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting}),
		newObservation("transfer", owner, responsibility, text("Blair"), during(boundary, end), cutoff.Add(24*time.Hour), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting}),
		newObservation("conflict", owner, responsibility, text("Casey"), during(boundary, end), cutoff.Add(25*time.Hour), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting, observation.EvidenceContradicting}),
		newObservation("hypothesis", owner, uncertainty, text("uncertain"), observation.UnknownTime(), recorded.Add(11*time.Minute), observation.StatusHypothesized, []observation.EvidenceRole{observation.EvidenceSupporting}),
		newObservation("authority", owner, authorityPredicate, text("authority excluded currently"), during(start, end), recorded.Add(12*time.Minute), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting}),
		newObservation("unresolved-gap", owner, gapPredicate, gapTerm, observation.UnknownTime(), recorded.Add(13*time.Minute), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting}),
		newObservation("cause-one", owner, query.CausalPredicate, text("handoff"), at(start.Add(5*24*time.Hour)), recorded.Add(14*time.Minute), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting, observation.EvidenceContradicting}),
		newObservation("cause-two", text("handoff"), query.CausalPredicate, owner, at(start.Add(10*24*time.Hour)), recorded.Add(15*time.Minute), observation.StatusObserved, []observation.EvidenceRole{observation.EvidenceSupporting}),
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
	gapMentionAdmission, err := admission.NewDecision(admission.DecisionInput{ID: "admission:" + string(gapMention.ID()), TargetKind: admission.TargetMention, TargetID: string(gapMention.ID()), Outcome: admission.Admitted, ReasonCode: "synthetic_acceptance", Authority: admission.AuthorityReviewer, RecordedAt: recorded.Add(5 * time.Minute)})
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
		if _, err := transaction.PutMention(ctx, gapMention); err != nil {
			return err
		}
		if err := transaction.AppendAdmissionDecision(ctx, gapMentionAdmission); err != nil {
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
		database: database, limits: limits, EntityIDs: []identity.EntityID{ownerID}, ownerID: ownerID,
		responsibilityPredicate: responsibility, uncertaintyPredicate: uncertainty, authorityPredicate: authorityPredicate, gapPredicate: gapPredicate,
		windowStart: start, boundary: boundary, windowEnd: end, historicalCutoff: cutoff, referenceTime: end,
		supportCitation:         query.Citation{EvidenceID: support.ID(), Role: observation.EvidenceSupporting, SourceDocumentID: documentRef.Ref.SourceDocumentID, DocumentVersionID: documentRef.Ref.VersionID, SectionID: sectionSupport.ID(), SectionTitle: sectionSupport.Title(), SectionPath: sectionSupport.Path(), SectionOrder: sectionSupport.Order(), SectionRole: sectionSupport.Role(), StartOffset: support.StartOffset(), EndOffset: support.EndOffset(), Locator: support.Locator(), Text: support.Text()},
		counterCitation:         query.Citation{EvidenceID: counter.ID(), Role: observation.EvidenceContradicting, SourceDocumentID: documentRef.Ref.SourceDocumentID, DocumentVersionID: documentRef.Ref.VersionID, SectionID: sectionCounter.ID(), SectionTitle: sectionCounter.Title(), SectionPath: sectionCounter.Path(), SectionOrder: sectionCounter.Order(), SectionRole: sectionCounter.Role(), StartOffset: counter.StartOffset(), EndOffset: counter.EndOffset(), Locator: counter.Locator(), Text: counter.Text()},
		PointRequest:            query.Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{responsibility, authorityPredicate, gapPredicate}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge()},
		TrendRequest:            query.Request{Intent: temporal.IntentTrendComparison, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{responsibility}, Selections: []temporal.TemporalSelection{before, after}, KnowledgeScope: temporal.CurrentKnowledge()},
		TrajectoryRequest:       query.Request{Intent: temporal.IntentTrajectory, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{responsibility, uncertainty}, Selections: []temporal.TemporalSelection{between}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 20},
		CausalRequest:           query.Request{Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{query.CausalPredicate}, Selections: []temporal.TemporalSelection{between}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 20},
		HistoricalPointRequest:  query.Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{ownerID}, EntityMatch: query.EntityMatchAll, Predicates: []observation.Predicate{responsibility}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: mustPlannerKnownAsOf(t, cutoff)},
		pointProposal:           `{"status":"executable","reason":"none","intent":"point-in-time","entity_match":"all","predicates":["planner.postgres/responsibility","planner.postgres/authority","planner.postgres/unresolved-gap"],"selections":[{"kind":"point","label":"point","at":"2026-03-15T00:00:00Z","start":"","end":""}],"knowledge_scope":{"kind":"current","as_of":""},"chronology_limit":0}`,
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

func assertPlannerExpectedRequest(t *testing.T, got, want query.Request) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("planned request = %#v, want normalized expected request %#v", got, want)
	}
}

func assertPlannerPostgresSemantics(t *testing.T, fixture plannerAtlasFixture, intent string, result query.Result) {
	t.Helper()
	if err := query.ValidateResult(result); err != nil {
		t.Fatal(err)
	}
	switch intent {
	case "point":
		point, ok := result.Payload.Point()
		if !ok || point.Selection != result.Selections[0] || len(point.Facts) != 0 {
			t.Fatalf("point payload = %#v, want the requested conflicting point", point)
		}
		initial := plannerPostgresFact(t, fixture, fixture.responsibilityPredicate, "Alex", "initial", mustPlannerDuring(t, fixture.windowStart, fixture.windowEnd), fixture.windowStart.Add(70*time.Minute), observation.StatusObserved, false)
		transfer := plannerPostgresFact(t, fixture, fixture.responsibilityPredicate, "Blair", "transfer", mustPlannerDuring(t, fixture.boundary, fixture.windowEnd), fixture.historicalCutoff.Add(24*time.Hour), observation.StatusObserved, false)
		conflict := plannerPostgresFact(t, fixture, fixture.responsibilityPredicate, "Casey", "conflict", mustPlannerDuring(t, fixture.boundary, fixture.windowEnd), fixture.historicalCutoff.Add(25*time.Hour), observation.StatusObserved, true)
		key := initial.Key
		wantUnresolved := []query.UnresolvedItem{{Key: key, Reason: temporal.UnresolvedConflict, Candidates: []query.Fact{initial, transfer, conflict}}}
		if !reflect.DeepEqual(point.Unresolved, wantUnresolved) {
			t.Fatalf("point unresolved = %#v, want exact cited candidates %#v", point.Unresolved, wantUnresolved)
		}
		wantGaps := []query.Gap{
			{Kind: query.GapAuthorityExcluded, EntityID: fixture.ownerID, Predicate: fixture.authorityPredicate},
			{Kind: query.GapUnresolvedMention, EntityID: fixture.ownerID, Predicate: fixture.gapPredicate},
		}
		if !reflect.DeepEqual(result.Gaps, wantGaps) {
			t.Fatalf("point gaps = %#v, want exact %#v", result.Gaps, wantGaps)
		}
	case "trend":
		trend, ok := result.Payload.Trend()
		if !ok || trend.Before.Selection != result.Selections[0] || trend.After.Selection != result.Selections[1] {
			t.Fatalf("trend payload = %#v, want both requested windows", trend)
		}
		initial := plannerPostgresFact(t, fixture, fixture.responsibilityPredicate, "Alex", "initial", mustPlannerDuring(t, fixture.windowStart, fixture.windowEnd), fixture.windowStart.Add(70*time.Minute), observation.StatusObserved, false)
		transfer := plannerPostgresFact(t, fixture, fixture.responsibilityPredicate, "Blair", "transfer", mustPlannerDuring(t, fixture.boundary, fixture.windowEnd), fixture.historicalCutoff.Add(24*time.Hour), observation.StatusObserved, false)
		conflict := plannerPostgresFact(t, fixture, fixture.responsibilityPredicate, "Casey", "conflict", mustPlannerDuring(t, fixture.boundary, fixture.windowEnd), fixture.historicalCutoff.Add(25*time.Hour), observation.StatusObserved, true)
		wantTrend := query.TrendResult{
			Before:  query.WindowResult{Selection: result.Selections[0], Facts: []query.Fact{initial}, Unresolved: []query.UnresolvedItem{}},
			After:   query.WindowResult{Selection: result.Selections[1], Facts: []query.Fact{}, Unresolved: []query.UnresolvedItem{{Key: initial.Key, Reason: temporal.UnresolvedConflict, Candidates: []query.Fact{initial, transfer, conflict}}}},
			Changes: []query.Change{}, UnresolvedKeys: []temporal.StateKey{initial.Key},
		}
		if !reflect.DeepEqual(trend, wantTrend) {
			t.Fatalf("trend = %#v, want exact cited trend %#v", trend, wantTrend)
		}
		wantGaps := []query.Gap{{Kind: query.GapValidTimeExcluded, EntityID: fixture.ownerID, Predicate: fixture.responsibilityPredicate, SelectionLabel: "before"}}
		if !reflect.DeepEqual(result.Gaps, wantGaps) {
			t.Fatalf("trend gaps = %#v, want exact %#v", result.Gaps, wantGaps)
		}
	case "trajectory":
		trajectory, ok := result.Payload.Trajectory()
		if !ok || trajectory.Selection != result.Selections[0] {
			t.Fatalf("trajectory payload = %#v, want requested window", trajectory)
		}
		assertPlannerTrajectorySemantics(t, fixture, trajectory)
	case "causal":
		causal, ok := result.Payload.Causal()
		if !ok || causal.Selection != result.Selections[0] {
			t.Fatalf("causal payload = %#v, want requested window", causal)
		}
		assertPlannerCausalSemantics(t, fixture, causal)
	default:
		t.Fatalf("unexpected intent case %q", intent)
	}
}

func assertPlannerHistoricalSemantics(t *testing.T, fixture plannerAtlasFixture, result query.Result) {
	t.Helper()
	if err := query.ValidateResult(result); err != nil {
		t.Fatal(err)
	}
	point, ok := result.Payload.Point()
	if !ok || point.Selection != result.Selections[0] || result.Selections[0].Label() != "point" {
		t.Fatalf("historical payload = %#v, want the caller valid-time point", point)
	}
	selectedAt, ok := point.Selection.Point()
	if !ok || !selectedAt.Equal(fixture.boundary) {
		t.Fatalf("historical valid-time selection = %#v, want point %s", point.Selection, fixture.boundary)
	}
	cutoff, ok := result.KnowledgeScope.AsOf()
	if !ok || !cutoff.Equal(fixture.historicalCutoff) {
		t.Fatalf("historical knowledge scope = %#v, want exact cutoff %s", result.KnowledgeScope, fixture.historicalCutoff)
	}
	wantInitial := plannerPostgresFact(t, fixture, fixture.responsibilityPredicate, "Alex", "initial", mustPlannerDuring(t, fixture.windowStart, fixture.windowEnd), fixture.windowStart.Add(70*time.Minute), observation.StatusObserved, false)
	if !reflect.DeepEqual(point.Facts, []query.Fact{wantInitial}) || len(point.Unresolved) != 0 {
		t.Fatalf("historical point = %#v, want only exact early recorded evidence", point)
	}
	if !reflect.DeepEqual(result.Gaps, []query.Gap{}) {
		t.Fatalf("historical gaps = %#v, want no excluded valid-time coverage", result.Gaps)
	}
}

func plannerPostgresFact(t *testing.T, fixture plannerAtlasFixture, predicate observation.Predicate, value, observationSuffix string, validTime observation.TemporalExtent, recordedAt time.Time, status observation.EpistemicStatus, counter bool) query.Fact {
	t.Helper()
	subject, err := observation.NewEntityTerm(string(fixture.ownerID), "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := temporal.NewStateKey(subject, predicate)
	if err != nil {
		t.Fatal(err)
	}
	object, err := observation.NewTextTerm(value)
	if err != nil {
		t.Fatal(err)
	}
	contradicting := []query.Citation{}
	if counter {
		contradicting = []query.Citation{fixture.counterCitation}
	}
	return query.Fact{Key: key, Value: object, Contributions: []query.Contribution{{ObservationID: observation.ObservationID("observation:planner-postgres/" + observationSuffix), Status: status, ValidTime: validTime, RecordedAt: recordedAt, Derivation: observation.Derivation{Method: "synthetic", Version: "planner-postgres-v1"}}}, SupportingCitations: []query.Citation{fixture.supportCitation}, ContradictingCitations: contradicting}
}

func mustPlannerDuring(t *testing.T, start, end time.Time) observation.TemporalExtent {
	t.Helper()
	extent, err := observation.During(start, end)
	if err != nil {
		t.Fatal(err)
	}
	return extent
}

func assertPlannerTrajectorySemantics(t *testing.T, fixture plannerAtlasFixture, trajectory query.TrajectoryResult) {
	t.Helper()
	initial := plannerPostgresFact(t, fixture, fixture.responsibilityPredicate, "Alex", "initial", mustPlannerDuring(t, fixture.windowStart, fixture.windowEnd), fixture.windowStart.Add(70*time.Minute), observation.StatusObserved, false)
	transfer := plannerPostgresFact(t, fixture, fixture.responsibilityPredicate, "Blair", "transfer", mustPlannerDuring(t, fixture.boundary, fixture.windowEnd), fixture.historicalCutoff.Add(24*time.Hour), observation.StatusObserved, false)
	conflict := plannerPostgresFact(t, fixture, fixture.responsibilityPredicate, "Casey", "conflict", mustPlannerDuring(t, fixture.boundary, fixture.windowEnd), fixture.historicalCutoff.Add(25*time.Hour), observation.StatusObserved, true)
	hypothesis := plannerPostgresFact(t, fixture, fixture.uncertaintyPredicate, "uncertain", "hypothesis", observation.UnknownTime(), fixture.windowStart.Add(71*time.Minute), observation.StatusHypothesized, false)
	wantUnresolved := []query.UnresolvedItem{
		{Key: initial.Key, Reason: temporal.UnresolvedConflict, Candidates: []query.Fact{initial, transfer, conflict}},
		{Key: hypothesis.Key, Reason: temporal.UnresolvedTemporalUncertainty, Candidates: []query.Fact{hypothesis}},
	}
	if !reflect.DeepEqual(trajectory.Unresolved, wantUnresolved) {
		t.Fatalf("trajectory unresolved = %#v, want exact cited conflict and hypothesis %#v", trajectory.Unresolved, wantUnresolved)
	}
	start, err := observation.AtTime(fixture.windowStart)
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := observation.AtTime(fixture.boundary)
	if err != nil {
		t.Fatal(err)
	}
	wantTransitions := []query.Transition{
		{Kind: temporal.ChangeAdded, Key: initial.Key, ValidTime: start, After: plannerFactPointer(initial), Unresolved: []query.UnresolvedItem{}},
		{Kind: temporal.ChangeRemoved, Key: initial.Key, ValidTime: boundary, Before: plannerFactPointer(initial), Unresolved: []query.UnresolvedItem{{Key: initial.Key, Reason: temporal.UnresolvedConflict, Candidates: []query.Fact{initial, transfer, conflict}}}},
	}
	if !reflect.DeepEqual(trajectory.Transitions, wantTransitions) {
		t.Fatalf("trajectory transitions = %#v, want exact transitions %#v", trajectory.Transitions, wantTransitions)
	}
}

func plannerFactPointer(value query.Fact) *query.Fact { return &value }

func assertPlannerCausalSemantics(t *testing.T, fixture plannerAtlasFixture, causal query.CausalChainResult) {
	t.Helper()
	owner, err := observation.NewEntityTerm(string(fixture.ownerID), "")
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := observation.NewTextTerm("handoff")
	if err != nil {
		t.Fatal(err)
	}
	firstTime, err := observation.AtTime(fixture.windowStart.Add(5 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	secondTime, err := observation.AtTime(fixture.windowStart.Add(10 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wantLinks := []query.CausalLink{
		{Cause: owner, Effect: handoff, Contributions: []query.Contribution{{ObservationID: "observation:planner-postgres/cause-one", Status: observation.StatusObserved, ValidTime: firstTime, RecordedAt: fixture.windowStart.Add(74 * time.Minute), Derivation: observation.Derivation{Method: "synthetic", Version: "planner-postgres-v1"}}}, SupportingCitations: []query.Citation{fixture.supportCitation}, ContradictingCitations: []query.Citation{fixture.counterCitation}},
		{Cause: handoff, Effect: owner, Contributions: []query.Contribution{{ObservationID: "observation:planner-postgres/cause-two", Status: observation.StatusObserved, ValidTime: secondTime, RecordedAt: fixture.windowStart.Add(75 * time.Minute), Derivation: observation.Derivation{Method: "synthetic", Version: "planner-postgres-v1"}}}, SupportingCitations: []query.Citation{fixture.supportCitation}, ContradictingCitations: []query.Citation{}},
	}
	if !reflect.DeepEqual(causal.Links, wantLinks) {
		t.Fatalf("causal links = %#v, want exact cited causal links %#v", causal.Links, wantLinks)
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

var _ Executor = plannerPostgresExecutor{}
