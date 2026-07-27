package query

import (
	"context"
	"errors"
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
)

func TestPostgresRepositoryPostgresCancellation(t *testing.T) {
	isolated := postgrestest.NewDatabase(t)
	manifest, err := coremigrations.Manifest()
	if err != nil {
		t.Fatalf("coremigrations.Manifest() error = %v", err)
	}
	applicationConfig, err := pgx.ParseConfig(isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("parse application database URL: %v", err)
	}
	if _, err := (migration.Migrator{
		DatabaseURL:     isolated.AdminURL(),
		ApplicationRole: applicationConfig.User,
		Manifests:       []migration.Manifest{manifest},
	}).Apply(context.Background()); err != nil {
		t.Fatalf("apply core migrations: %v", err)
	}
	database, err := postgres.Open(
		context.Background(),
		isolated.ApplicationURL(),
	)
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	t.Cleanup(database.Close)
	repository := PostgresRepository{Database: database}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = repository.Read(ctx, postgresTestReadSelection(t))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() error = %v, want context.Canceled", err)
	}
}

func TestPostgresRepositoryExecutesAllTemporalIntentsOverSyntheticAtlas(t *testing.T) {
	fixture := newAllIntentPostgresFixture(t)
	service := Service{
		Reader: PostgresRepository{Database: fixture.database},
		Limits: Limits{MaxEntities: 16, MaxPredicates: 32, MaxChronology: 1000},
	}
	queryPoint := func(at time.Time, scope temporal.KnowledgeScope) Result {
		t.Helper()
		selection, err := temporal.At("point", at)
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Query(t.Context(), Request{
			Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{fixture.ownerID},
			EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{fixture.responsibilityPredicate},
			Selections: []temporal.TemporalSelection{selection}, KnowledgeScope: scope,
		})
		if err != nil {
			t.Fatalf("point Query() error = %v", err)
		}
		return result
	}

	before := queryPoint(fixture.boundary.Add(-time.Second), temporal.CurrentKnowledge())
	beforePoint, ok := before.Payload.Point()
	if !ok || len(beforePoint.Facts) != 1 || len(beforePoint.Unresolved) != 0 {
		t.Fatalf("before point = %#v, want one resolved fact", beforePoint)
	}
	at := queryPoint(fixture.boundary, temporal.CurrentKnowledge())
	atPoint, ok := at.Payload.Point()
	if !ok || len(atPoint.Facts) != 0 || len(atPoint.Unresolved) != 1 {
		t.Fatalf("boundary point = %#v, want cited conflict", atPoint)
	}
	after := queryPoint(fixture.conflictEnd, temporal.CurrentKnowledge())
	afterPoint, ok := after.Payload.Point()
	if !ok || len(afterPoint.Facts) != 1 || len(afterPoint.Unresolved) != 0 {
		t.Fatalf("after point = %#v, want transferred responsibility", afterPoint)
	}

	historicalScope, err := temporal.KnownAsOf(fixture.historicalCutoff)
	if err != nil {
		t.Fatal(err)
	}
	historical := queryPoint(fixture.conflictEnd, historicalScope)
	historicalPoint, ok := historical.Payload.Point()
	if !ok || len(historicalPoint.Facts) != 0 {
		t.Fatalf("historical point = %#v, want later authority hidden", historicalPoint)
	}
	if !reflect.DeepEqual(after.Selections, historical.Selections) {
		t.Fatal("valid-time selections differ across recorded-time scope")
	}

	window, err := temporal.Between("between", fixture.windowStart, fixture.windowEnd)
	if err != nil {
		t.Fatal(err)
	}
	trajectory, err := service.Query(t.Context(), Request{
		Intent: temporal.IntentTrajectory, EntityIDs: []identity.EntityID{fixture.ownerID},
		EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{
			fixture.responsibilityPredicate, fixture.uncertaintyPredicate,
		},
		Selections:     []temporal.TemporalSelection{window},
		KnowledgeScope: temporal.CurrentKnowledge(), Limit: 10,
	})
	if err != nil {
		t.Fatalf("trajectory Query() error = %v", err)
	}
	trajectoryPayload, ok := trajectory.Payload.Trajectory()
	if !ok || len(trajectoryPayload.Transitions) < 2 || len(trajectoryPayload.Unresolved) < 2 {
		t.Fatalf("trajectory = %#v, want transfer, conflict, and uncertainty", trajectoryPayload)
	}

	causal, err := service.Query(t.Context(), Request{
		Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{fixture.ownerID},
		EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{CausalPredicate},
		Selections:     []temporal.TemporalSelection{window},
		KnowledgeScope: temporal.CurrentKnowledge(), Limit: 10,
	})
	if err != nil {
		t.Fatalf("causal Query() error = %v", err)
	}
	causalPayload, ok := causal.Payload.Causal()
	if !ok || len(causalPayload.Links) != 2 ||
		len(causalPayload.Links[0].ContradictingCitations)+len(causalPayload.Links[1].ContradictingCitations) != 1 {
		t.Fatalf("causal = %#v, want two cited links with counterevidence", causalPayload)
	}

	chronologyOnly, err := service.Query(t.Context(), Request{
		Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{fixture.chronologyOnlyID},
		EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{CausalPredicate},
		Selections:     []temporal.TemporalSelection{window},
		KnowledgeScope: temporal.CurrentKnowledge(), Limit: 10,
	})
	if err != nil {
		t.Fatalf("chronology-only causal Query() error = %v", err)
	}
	chronologyPayload, ok := chronologyOnly.Payload.Causal()
	if !ok || len(chronologyPayload.Links) != 0 ||
		len(chronologyOnly.Gaps) != 1 ||
		chronologyOnly.Gaps[0].Kind != GapNoCausalEvidence ||
		chronologyOnly.Gaps[0].Predicate != CausalPredicate ||
		chronologyOnly.Gaps[0].SelectionLabel != "between" {
		t.Fatalf("chronology-only causal result = %#v", chronologyOnly)
	}

	for _, test := range []struct {
		name    string
		request Request
	}{
		{
			name: "trajectory",
			request: Request{
				Intent: temporal.IntentTrajectory, EntityIDs: []identity.EntityID{fixture.ownerID},
				EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{fixture.responsibilityPredicate},
				Selections:     []temporal.TemporalSelection{window},
				KnowledgeScope: temporal.CurrentKnowledge(), Limit: 1,
			},
		},
		{
			name: "causal",
			request: Request{
				Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{fixture.ownerID},
				EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{CausalPredicate},
				Selections:     []temporal.TemporalSelection{window},
				KnowledgeScope: temporal.CurrentKnowledge(), Limit: 1,
			},
		},
	} {
		t.Run(test.name+" overflow", func(t *testing.T) {
			result, err := service.Query(t.Context(), test.request)
			if !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("Query() error = %v, want ErrLimitExceeded", err)
			}
			if !reflect.DeepEqual(result, Result{}) {
				t.Fatalf("overflow returned partial result: %#v", result)
			}
		})
	}
}

type allIntentPostgresFixture struct {
	database                *postgres.Database
	ownerID                 identity.EntityID
	chronologyOnlyID        identity.EntityID
	responsibilityPredicate observation.Predicate
	uncertaintyPredicate    observation.Predicate
	windowStart             time.Time
	boundary                time.Time
	conflictEnd             time.Time
	windowEnd               time.Time
	historicalCutoff        time.Time
}

func newAllIntentPostgresFixture(t *testing.T) allIntentPostgresFixture {
	t.Helper()
	isolated := postgrestest.NewDatabase(t)
	manifest, err := coremigrations.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	applicationConfig, err := pgx.ParseConfig(isolated.ApplicationURL())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migration.Migrator{
		DatabaseURL: isolated.AdminURL(), ApplicationRole: applicationConfig.User,
		Manifests: []migration.Manifest{manifest},
	}).Apply(t.Context()); err != nil {
		t.Fatalf("apply core migrations: %v", err)
	}
	database, err := postgres.Open(t.Context(), isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	t.Cleanup(database.Close)

	fixture := allIntentPostgresFixture{
		database: database,
		ownerID:  "entity:project-atlas/owner", chronologyOnlyID: "entity:project-atlas/chronology-only",
		responsibilityPredicate: "project.atlas/responsibility",
		uncertaintyPredicate:    "project.atlas/uncertainty",
		windowStart:             time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		boundary:                time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC),
		conflictEnd:             time.Date(2026, time.January, 20, 0, 0, 0, 0, time.UTC),
		windowEnd:               time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),
		historicalCutoff:        time.Date(2026, time.January, 20, 12, 0, 0, 0, time.UTC),
	}
	fixture.seed(t)
	return fixture
}

func (fixture allIntentPostgresFixture) seed(t *testing.T) {
	t.Helper()
	recordedBase := time.Date(2026, time.January, 2, 9, 0, 0, 0, time.UTC)
	supportText := "Project Atlas synthetic support records responsibility and causality."
	counterText := "Project Atlas synthetic counterevidence challenges the causal link."
	supportSection, err := evidence.NewSection(evidence.SectionInput{
		ID: "section:project-atlas/all-intents-support", Title: "Synthetic support",
		Path: []string{"Project Atlas"}, Order: 0, Role: "synthetic", Text: supportText,
	})
	if err != nil {
		t.Fatal(err)
	}
	counterSection, err := evidence.NewSection(evidence.SectionInput{
		ID: "section:project-atlas/all-intents-counter", Title: "Synthetic counterevidence",
		Path: []string{"Project Atlas"}, Order: 1, Role: "synthetic", Text: counterText,
	})
	if err != nil {
		t.Fatal(err)
	}
	document, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{
		Provider: "synthetic", ProviderDocumentID: "project-atlas-all-intents",
		Title:   "Synthetic Project Atlas all-intent fixture",
		Locator: "synthetic://project-atlas/all-intents", ProviderVersion: "v1",
		ModifiedAt: recordedBase, RecordedAt: recordedBase, SourceTime: &recordedBase,
		Sections: []evidence.Section{supportSection, counterSection},
	})
	if err != nil {
		t.Fatal(err)
	}
	documentRef, err := fixture.database.PutDocumentVersion(t.Context(), document)
	if err != nil {
		t.Fatal(err)
	}
	newSpan := func(section evidence.Section, text, quote string, recordedAt time.Time) evidence.EvidenceSpan {
		t.Helper()
		start := strings.Index(text, quote)
		if start < 0 {
			t.Fatalf("quote %q not found", quote)
		}
		span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
			Document: document, SectionID: section.ID(), StartOffset: start,
			EndOffset: start + len(quote), Quote: quote, RecordedAt: recordedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		return span
	}
	supportSpan := newSpan(supportSection, supportText, "synthetic support", recordedBase.Add(time.Minute))
	counterSpan := newSpan(counterSection, counterText, "synthetic counterevidence", recordedBase.Add(2*time.Minute))

	newEntity := func(id identity.EntityID, name string) identity.Entity {
		t.Helper()
		value, err := identity.NewEntity(identity.EntityInput{
			ID: id, Kind: identity.KindPerson, DisplayName: name, RecordedAt: recordedBase.Add(3 * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	owner := newEntity(fixture.ownerID, "Synthetic Atlas Owner")
	chronologyOnly := newEntity(fixture.chronologyOnlyID, "Synthetic Atlas Chronology")
	ownerTerm, err := observation.NewEntityTerm(string(fixture.ownerID), "")
	if err != nil {
		t.Fatal(err)
	}
	chronologyTerm, err := observation.NewEntityTerm(string(fixture.chronologyOnlyID), "")
	if err != nil {
		t.Fatal(err)
	}
	textTerm := func(value string) observation.Term {
		t.Helper()
		term, err := observation.NewTextTerm(value)
		if err != nil {
			t.Fatal(err)
		}
		return term
	}
	during := func(start, end time.Time) observation.TemporalExtent {
		t.Helper()
		value, err := observation.During(start, end)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	at := func(value time.Time) observation.TemporalExtent {
		t.Helper()
		result, err := observation.AtTime(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	newObservation := func(
		id string,
		subject observation.Term,
		predicate observation.Predicate,
		object observation.Term,
		validTime observation.TemporalExtent,
		recordedAt time.Time,
		counter bool,
		status observation.EpistemicStatus,
	) observation.Observation {
		t.Helper()
		links := []observation.EvidenceLink{{
			EvidenceID: supportSpan.ID(), Role: observation.EvidenceSupporting,
		}}
		if counter {
			links = append(links, observation.EvidenceLink{
				EvidenceID: counterSpan.ID(), Role: observation.EvidenceContradicting,
			})
		}
		value, err := observation.NewObservation(observation.ObservationInput{
			ID:        observation.ObservationID(id),
			Statement: observation.Statement{Subject: subject, Predicate: predicate, Object: object},
			ValidTime: validTime, RecordedAt: recordedAt, Evidence: links,
			Derivation: observation.Derivation{Method: "synthetic", Version: "all-intents-v1"},
			Status:     status,
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	lateRecorded := time.Date(2026, time.January, 30, 9, 0, 0, 0, time.UTC)
	observations := []observation.Observation{
		newObservation("observation:project-atlas/responsibility-initial", ownerTerm,
			fixture.responsibilityPredicate, textTerm("Alex"), during(fixture.windowStart, fixture.boundary),
			recordedBase.Add(10*time.Minute), false, observation.StatusObserved),
		newObservation("observation:project-atlas/responsibility-transfer", ownerTerm,
			fixture.responsibilityPredicate, textTerm("Blair"), during(fixture.boundary, fixture.windowEnd),
			lateRecorded, false, observation.StatusObserved),
		newObservation("observation:project-atlas/responsibility-conflict", ownerTerm,
			fixture.responsibilityPredicate, textTerm("Casey"), during(fixture.boundary, fixture.conflictEnd),
			lateRecorded.Add(time.Minute), true, observation.StatusObserved),
		newObservation("observation:project-atlas/uncertainty", ownerTerm,
			fixture.uncertaintyPredicate, textTerm("uncertain"), observation.UnknownTime(),
			recordedBase.Add(11*time.Minute), false, observation.StatusObserved),
		newObservation("observation:project-atlas/cause-one", ownerTerm,
			CausalPredicate, textTerm("handoff"), at(fixture.windowStart.Add(5*24*time.Hour)),
			recordedBase.Add(12*time.Minute), true, observation.StatusObserved),
		newObservation("observation:project-atlas/cause-two", textTerm("handoff"),
			CausalPredicate, ownerTerm, at(fixture.windowStart.Add(10*24*time.Hour)),
			recordedBase.Add(13*time.Minute), false, observation.StatusObserved),
		newObservation("observation:project-atlas/chronology-only", chronologyTerm,
			fixture.responsibilityPredicate, textTerm("dated change"), at(fixture.boundary),
			recordedBase.Add(14*time.Minute), false, observation.StatusObserved),
	}
	admissions := make([]admission.Decision, len(observations))
	for index, value := range observations {
		recordedAt := value.RecordedAt().Add(time.Minute)
		decision, err := admission.NewDecision(admission.DecisionInput{
			ID:         "admission:" + string(value.ID()),
			TargetKind: admission.TargetObservation, TargetID: string(value.ID()),
			Outcome: admission.Admitted, ReasonCode: "synthetic_acceptance",
			Authority: admission.AuthorityReviewer, RecordedAt: recordedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		admissions[index] = decision
	}

	if err := fixture.database.InTransaction(t.Context(), func(transaction *postgres.Transaction) error {
		if err := transaction.SetCurrentDocumentVersion(
			t.Context(), documentRef.Ref.SourceDocumentID, documentRef.Ref.VersionID,
		); err != nil {
			return err
		}
		for _, span := range []evidence.EvidenceSpan{supportSpan, counterSpan} {
			if _, err := transaction.PutEvidenceSpan(t.Context(), span); err != nil {
				return err
			}
		}
		for _, entity := range []identity.Entity{owner, chronologyOnly} {
			if _, err := transaction.PutEntity(t.Context(), entity); err != nil {
				return err
			}
		}
		for _, value := range observations {
			if _, err := transaction.PutObservation(t.Context(), value); err != nil {
				return err
			}
		}
		for _, decision := range admissions {
			if err := transaction.AppendAdmissionDecision(t.Context(), decision); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed synthetic all-intent PostgreSQL fixture: %v", err)
	}
}
