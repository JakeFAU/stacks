package query

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestPostgresRepositoryMapsNormalizedSelectionAndExactSnapshot(t *testing.T) {
	before := mustPostgresTestWindow(
		t,
		"before",
		time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),
	)
	after := mustPostgresTestWindow(
		t,
		"after",
		time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC),
	)
	cutoffInput := time.Date(
		2026,
		time.March,
		2,
		12,
		30,
		0,
		0,
		time.FixedZone("synthetic-zone", -5*60*60),
	)
	knowledgeScope, err := temporal.KnownAsOf(cutoffInput)
	if err != nil {
		t.Fatalf("temporal.KnownAsOf() error = %v", err)
	}
	selection := ReadSelection{
		EntityIDs: []identity.EntityID{
			"entity:synthetic/alpha",
			"entity:synthetic/beta",
		},
		EntityMatch: EntityMatchAny,
		Predicates: []observation.Predicate{
			"project.synthetic/owner",
			"project.synthetic/state",
		},
		Selections:     []temporal.TemporalSelection{before, after},
		KnowledgeScope: knowledgeScope,
	}
	record := postgresTestObservationRecord(
		t,
		"observation:synthetic/state",
		mustPostgresTestEntity(t, "entity:synthetic/alpha", ""),
		mustPostgresTestText(t, "active"),
		postgres.TemporalEvidenceRecord{
			EvidenceID:        "evidence:synthetic/support",
			Role:              observation.EvidenceSupporting,
			SourceDocumentID:  "source:synthetic/document",
			DocumentVersionID: "version:synthetic/document",
			SectionID:         "section:synthetic/body",
			SectionTitle:      "Synthetic body",
			SectionPath:       []string{"Synthetic", "Body"},
			SectionOrder:      1,
			SectionRole:       "body",
			StartOffset:       2,
			EndOffset:         12,
			Locator:           "synthetic://document/body",
			Text:              "active",
		},
	)
	database := &postgresSnapshotDatabase{
		snapshot: postgres.TemporalQuerySnapshot{
			Entities: []postgres.TemporalEntityRecord{
				{EntityID: "entity:synthetic/alpha", Known: true},
				{EntityID: "entity:synthetic/beta", Known: false},
			},
			Observations: []postgres.TemporalObservationRecord{record},
			Coverage: []postgres.TemporalCoverageRecord{
				{
					Reason:        postgres.TemporalCoverageUnresolvedMention,
					ObservationID: "observation:synthetic/unresolved",
				},
				{
					Reason:        postgres.TemporalCoverageAuthorityExcluded,
					ObservationID: "observation:synthetic/excluded",
				},
				{
					Reason:        postgres.TemporalCoverageEntityFiltered,
					EntityID:      "entity:synthetic/beta",
					ObservationID: "observation:synthetic/entity-filtered",
				},
				{
					Reason:        postgres.TemporalCoveragePredicateFiltered,
					Predicate:     "project.synthetic/other",
					ObservationID: "observation:synthetic/predicate-filtered",
				},
			},
		},
	}

	got, err := (PostgresRepository{Database: database}).Read(
		context.Background(),
		selection,
	)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	wantCutoff, _ := knowledgeScope.AsOf()
	wantAdapterSelection := postgres.TemporalQuerySelection{
		EntityIDs:     append([]identity.EntityID(nil), selection.EntityIDs...),
		EntityMatch:   postgres.TemporalEntityMatchAny,
		Predicates:    append([]observation.Predicate(nil), selection.Predicates...),
		Selections:    append([]temporal.TemporalSelection(nil), selection.Selections...),
		KnowledgeAsOf: &wantCutoff,
	}
	if database.calls != 1 {
		t.Fatalf("LoadTemporalQuerySnapshot() calls = %d, want 1", database.calls)
	}
	if !reflect.DeepEqual(database.selection, wantAdapterSelection) {
		t.Fatalf(
			"adapter selection = %#v, want %#v",
			database.selection,
			wantAdapterSelection,
		)
	}
	want := ReadSnapshot{
		Entities: []EntityAuthority{
			{EntityID: "entity:synthetic/alpha", Known: true},
			{EntityID: "entity:synthetic/beta", Known: false},
		},
		Observations: []ReadObservation{{
			Observation: record.Observation,
			Subject:     record.Subject,
			Object:      record.Object,
			Evidence: []Citation{{
				EvidenceID:        "evidence:synthetic/support",
				Role:              observation.EvidenceSupporting,
				SourceDocumentID:  "source:synthetic/document",
				DocumentVersionID: "version:synthetic/document",
				SectionID:         "section:synthetic/body",
				SectionTitle:      "Synthetic body",
				SectionPath:       []string{"Synthetic", "Body"},
				SectionOrder:      1,
				SectionRole:       "body",
				StartOffset:       2,
				EndOffset:         12,
				Locator:           "synthetic://document/body",
				Text:              "active",
			}},
		}},
		Coverage: []Coverage{
			{
				Reason:        CoverageUnresolvedMention,
				ObservationID: "observation:synthetic/unresolved",
			},
			{
				Reason:        CoverageAuthorityExcluded,
				ObservationID: "observation:synthetic/excluded",
			},
			{
				Reason:        CoverageEntityFiltered,
				EntityID:      "entity:synthetic/beta",
				ObservationID: "observation:synthetic/entity-filtered",
			},
			{
				Reason:        CoveragePredicateFiltered,
				Predicate:     "project.synthetic/other",
				ObservationID: "observation:synthetic/predicate-filtered",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Read() = %#v, want %#v", got, want)
	}

	selection.EntityIDs[0] = "entity:synthetic/mutated-input"
	selection.Predicates[0] = "project.synthetic/mutated-input"
	selection.Selections[0] = temporal.TemporalSelection{}
	database.snapshot.Entities[0].EntityID = "entity:synthetic/mutated-adapter"
	database.snapshot.Observations[0].Evidence[0].SectionPath[0] = "Mutated adapter"
	database.snapshot.Coverage[0].ObservationID = "observation:synthetic/mutated"
	if database.selection.EntityIDs[0] != "entity:synthetic/alpha" ||
		database.selection.Predicates[0] != "project.synthetic/owner" ||
		database.selection.Selections[0] != before ||
		database.selection.KnowledgeAsOf == nil ||
		!database.selection.KnowledgeAsOf.Equal(wantCutoff) {
		t.Fatalf("adapter selection aliases caller-owned memory: %#v", database.selection)
	}
	if got.Entities[0].EntityID != "entity:synthetic/alpha" ||
		got.Observations[0].Evidence[0].SectionPath[0] != "Synthetic" ||
		got.Coverage[0].ObservationID != "observation:synthetic/unresolved" {
		t.Fatalf("read snapshot aliases adapter-owned memory: %#v", got)
	}
}

func TestPostgresRepositoryPreservesCanonicalTermsGroundingAndEvidenceRoles(t *testing.T) {
	sourceSubject := mustPostgresTestMention(t, "mention:synthetic/subject")
	sourceObject := mustPostgresTestEntity(
		t,
		"entity:synthetic/object",
		"mention:synthetic/object",
	)
	resolvedSubject := mustPostgresTestEntity(
		t,
		"entity:synthetic/subject",
		"",
	)
	resolvedObject := mustPostgresTestEntity(
		t,
		"entity:synthetic/object",
		"",
	)
	supporting := postgresTestEvidence(
		"evidence:synthetic/supporting",
		observation.EvidenceSupporting,
	)
	contradicting := postgresTestEvidence(
		"evidence:synthetic/contradicting",
		observation.EvidenceContradicting,
	)
	record := postgresTestObservationRecord(
		t,
		"observation:synthetic/grounded",
		sourceSubject,
		sourceObject,
		contradicting,
		supporting,
	)
	record.Subject = resolvedSubject
	record.Object = resolvedObject
	record.SubjectGroundingMentionID = "mention:synthetic/subject"
	record.ObjectGroundingMentionID = "mention:synthetic/object"
	database := &postgresSnapshotDatabase{
		snapshot: postgres.TemporalQuerySnapshot{
			Entities: []postgres.TemporalEntityRecord{{
				EntityID: "entity:synthetic/subject",
				Known:    true,
			}},
			Observations: []postgres.TemporalObservationRecord{record},
		},
	}

	got, err := (PostgresRepository{Database: database}).Read(
		context.Background(),
		postgresTestReadSelection(t),
	)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(got.Observations) != 1 {
		t.Fatalf("observation count = %d, want 1", len(got.Observations))
	}
	item := got.Observations[0]
	if item.Observation.Statement().Subject != sourceSubject ||
		item.Observation.Statement().Object != sourceObject ||
		item.Subject != resolvedSubject ||
		item.Object != resolvedObject ||
		item.SubjectGroundingMentionID != "mention:synthetic/subject" ||
		item.ObjectGroundingMentionID != "mention:synthetic/object" {
		t.Fatalf("canonical/resolved term mapping = %#v", item)
	}
	gotRoles := make([]observation.EvidenceRole, len(item.Evidence))
	for index, citation := range item.Evidence {
		gotRoles[index] = citation.Role
	}
	slices.Sort(gotRoles)
	if !slices.Equal(
		gotRoles,
		[]observation.EvidenceRole{
			observation.EvidenceContradicting,
			observation.EvidenceSupporting,
		},
	) {
		t.Fatalf("evidence roles = %#v", gotRoles)
	}
}

func TestPostgresRepositoryRejectsMalformedAdapterRecords(t *testing.T) {
	const privateReason = postgres.TemporalCoverageReason(
		"private-malformed-adapter-reason",
	)
	database := &postgresSnapshotDatabase{
		snapshot: postgres.TemporalQuerySnapshot{
			Coverage: []postgres.TemporalCoverageRecord{{
				Reason:        privateReason,
				ObservationID: "private-observation-id",
			}},
		},
	}

	_, err := (PostgresRepository{Database: database}).Read(
		context.Background(),
		postgresTestReadSelection(t),
	)
	if err == nil {
		t.Fatal("Read() error = nil, want malformed adapter record rejection")
	}
	for _, privateValue := range []string{
		string(privateReason),
		"private-observation-id",
	} {
		if strings.Contains(err.Error(), privateValue) {
			t.Fatalf("Read() error leaked %q: %v", privateValue, err)
		}
	}
}

func TestPostgresRepositoryPreservesCancellation(t *testing.T) {
	const privateError = "private database URL and entity identifier"
	database := &postgresSnapshotDatabase{
		err: fmt.Errorf("%s: %w", privateError, context.Canceled),
	}

	_, err := (PostgresRepository{Database: database}).Read(
		context.Background(),
		postgresTestReadSelection(t),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() error = %v, want context.Canceled identity", err)
	}
	if strings.Contains(err.Error(), privateError) {
		t.Fatalf("Read() leaked adapter error detail: %v", err)
	}
}

func TestPostgresRepositoryContainsNoSQLOrDriverPolicy(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	for _, name := range []string{"postgres.go", "postgres_observability.go"} {
		contents, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), name))
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error = %v", name, err)
		}
		source := string(contents)
		for _, forbidden := range []string{
			"database/sql",
			"github.com/jackc/pgx",
			"BeginTx(",
			".Query(",
			".QueryRow(",
			".Exec(",
			"SELECT ",
			"INSERT ",
			"UPDATE ",
			"DELETE ",
			"stacks.interaction",
			"manager-confidence",
		} {
			if strings.Contains(source, forbidden) {
				t.Fatalf("%s contains forbidden SQL, driver, or manager policy %q", name, forbidden)
			}
		}
	}
}

func TestPostgresRepositorySnapshotSpanUsesOnlyBoundedAttributes(t *testing.T) {
	const privateError = "private entity predicate SQL citation URL and database URL"
	const privateCountBucket = postgres.TemporalSnapshotCountBucket(
		"private-count-bucket",
	)
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	observer := PostgresSnapshotObserver{Tracer: provider.Tracer("test")}
	database := &postgresSnapshotDatabase{
		observe: &postgres.TemporalSnapshotAttributes{
			HasKnowledgeCutoff:   true,
			EntityCountBucket:    privateCountBucket,
			PredicateCountBucket: postgres.TemporalSnapshotCountSixPlus,
			SelectionCountBucket: postgres.TemporalSnapshotCountOne,
		},
		err: errors.New(privateError),
	}

	_, err := (PostgresRepository{
		Database:         database,
		SnapshotObserver: observer,
	}).Read(context.Background(), postgresTestReadSelection(t))
	if err == nil {
		t.Fatal("Read() error = nil, want synthetic adapter failure")
	}
	if database.observer != observer {
		t.Fatal("repository did not pass its snapshot observer to the adapter")
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name != "stacks.postgres.temporal_snapshot" {
		t.Fatalf(
			"span name = %q, want stacks.postgres.temporal_snapshot",
			span.Name,
		)
	}
	if span.Status.Code != codes.Error {
		t.Fatalf("span status = %v, want Error", span.Status.Code)
	}
	wantAttributes := map[string]attribute.Value{
		"stacks.postgres.temporal_snapshot.has_knowledge_cutoff": attribute.BoolValue(true),
		"stacks.postgres.temporal_snapshot.entity_count_bucket":  attribute.StringValue("invalid"),
		"stacks.postgres.temporal_snapshot.predicate_count_bucket": attribute.StringValue(
			"6-plus",
		),
		"stacks.postgres.temporal_snapshot.selection_count_bucket": attribute.StringValue(
			"1",
		),
		"stacks.postgres.temporal_snapshot.outcome": attribute.StringValue("failed"),
	}
	if len(span.Attributes) != len(wantAttributes) {
		t.Fatalf("span attributes = %#v, want only %#v", span.Attributes, wantAttributes)
	}
	for _, got := range span.Attributes {
		want, exists := wantAttributes[string(got.Key)]
		if !exists || got.Value != want {
			t.Fatalf("unexpected span attribute %s=%v", got.Key, got.Value)
		}
	}
	serialized := fmt.Sprint(
		span.Attributes,
		span.Events,
		span.Status.Description,
	)
	for _, privateValue := range []string{privateError, string(privateCountBucket)} {
		if strings.Contains(serialized, privateValue) {
			t.Fatalf(
				"snapshot telemetry leaked private value %q: %s",
				privateValue,
				serialized,
			)
		}
	}
}

type postgresSnapshotDatabase struct {
	calls     int
	selection postgres.TemporalQuerySelection
	observer  postgres.TemporalSnapshotObserver
	snapshot  postgres.TemporalQuerySnapshot
	observe   *postgres.TemporalSnapshotAttributes
	err       error
}

func (database *postgresSnapshotDatabase) LoadTemporalQuerySnapshot(
	ctx context.Context,
	selection postgres.TemporalQuerySelection,
	observer postgres.TemporalSnapshotObserver,
) (postgres.TemporalQuerySnapshot, error) {
	database.calls++
	database.selection = selection
	database.observer = observer
	if database.observe == nil || observer == nil {
		return database.snapshot, database.err
	}
	_, finish := observer.StartTemporalSnapshot(ctx, *database.observe)
	finish(database.err)
	return database.snapshot, database.err
}

func postgresTestReadSelection(t *testing.T) ReadSelection {
	t.Helper()
	return ReadSelection{
		EntityIDs:   []identity.EntityID{"entity:synthetic/subject"},
		EntityMatch: EntityMatchAll,
		Predicates:  []observation.Predicate{"project.synthetic/relationship"},
		Selections: []temporal.TemporalSelection{mustPostgresTestWindow(
			t,
			"synthetic",
			time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),
		)},
		KnowledgeScope: temporal.CurrentKnowledge(),
	}
}

func postgresTestObservationRecord(
	t *testing.T,
	id observation.ObservationID,
	subject observation.Term,
	object observation.Term,
	evidenceRecords ...postgres.TemporalEvidenceRecord,
) postgres.TemporalObservationRecord {
	t.Helper()
	links := make([]observation.EvidenceLink, len(evidenceRecords))
	for index, record := range evidenceRecords {
		links[index] = observation.EvidenceLink{
			EvidenceID: record.EvidenceID,
			Role:       record.Role,
		}
	}
	value, err := observation.NewObservation(observation.ObservationInput{
		ID: id,
		Statement: observation.Statement{
			Subject:   subject,
			Predicate: "project.synthetic/relationship",
			Object:    object,
		},
		ValidTime:  observation.UnknownTime(),
		RecordedAt: time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC),
		Evidence:   links,
		Derivation: observation.Derivation{
			Method:  "synthetic-test",
			Version: "d2.2-v1",
		},
		Status: observation.StatusObserved,
	})
	if err != nil {
		t.Fatalf("observation.NewObservation() error = %v", err)
	}
	return postgres.TemporalObservationRecord{
		Observation: value,
		Subject:     subject,
		Object:      object,
		Evidence:    evidenceRecords,
	}
}

func postgresTestEvidence(
	id evidence.EvidenceID,
	role observation.EvidenceRole,
) postgres.TemporalEvidenceRecord {
	return postgres.TemporalEvidenceRecord{
		EvidenceID:        id,
		Role:              role,
		SourceDocumentID:  "source:synthetic/" + string(id),
		DocumentVersionID: "version:synthetic/" + string(id),
		SectionID:         "section:synthetic/" + string(id),
		SectionTitle:      "Synthetic evidence",
		SectionPath:       []string{"Synthetic", "Evidence"},
		SectionOrder:      1,
		SectionRole:       "body",
		StartOffset:       0,
		EndOffset:         9,
		Locator:           "synthetic://evidence",
		Text:              "synthetic",
	}
}

func mustPostgresTestWindow(
	t *testing.T,
	label string,
	start time.Time,
	end time.Time,
) temporal.TemporalSelection {
	t.Helper()
	value, err := temporal.Between(label, start, end)
	if err != nil {
		t.Fatalf("temporal.Between() error = %v", err)
	}
	return value
}

func mustPostgresTestText(t *testing.T, value string) observation.Term {
	t.Helper()
	term, err := observation.NewTextTerm(value)
	if err != nil {
		t.Fatalf("observation.NewTextTerm() error = %v", err)
	}
	return term
}

func mustPostgresTestMention(t *testing.T, value string) observation.Term {
	t.Helper()
	term, err := observation.NewMentionTerm(value)
	if err != nil {
		t.Fatalf("observation.NewMentionTerm() error = %v", err)
	}
	return term
}

func mustPostgresTestEntity(
	t *testing.T,
	entityID string,
	groundingMentionID string,
) observation.Term {
	t.Helper()
	term, err := observation.NewEntityTerm(entityID, groundingMentionID)
	if err != nil {
		t.Fatalf("observation.NewEntityTerm() error = %v", err)
	}
	return term
}
