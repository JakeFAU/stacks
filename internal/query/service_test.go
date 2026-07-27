package query

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTrendQueryNormalizesBeforeReaderAndExecutesOneRead(t *testing.T) {
	fixture := newTrendFixture(t)
	reader := &recordingTrendReader{snapshot: ReadSnapshot{
		Entities: []EntityAuthority{
			{EntityID: "entity-a", Known: true},
			{EntityID: "entity-b", Known: true},
		},
	}}
	request := fixture.request
	request.EntityIDs = []identity.EntityID{" entity-b ", "entity-a"}
	request.EntityMatch = ""
	request.Predicates = []observation.Predicate{" work.location ", "work.mode"}

	result, err := (Service{Reader: reader, Limits: validLimits()}).Query(context.Background(), request)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if reader.calls != 1 {
		t.Fatalf("Reader.Read() calls = %d, want 1", reader.calls)
	}
	wantSelection := ReadSelection{
		EntityIDs:      []identity.EntityID{"entity-a", "entity-b"},
		EntityMatch:    EntityMatchAll,
		Predicates:     []observation.Predicate{"work.location", "work.mode"},
		Selections:     append([]temporal.TemporalSelection{}, fixture.request.Selections...),
		KnowledgeScope: fixture.request.KnowledgeScope,
	}
	if !reflect.DeepEqual(reader.selection, wantSelection) {
		t.Fatalf("Reader.Read() selection = %#v, want %#v", reader.selection, wantSelection)
	}
	if !slices.Equal(result.EntityIDs, wantSelection.EntityIDs) ||
		!slices.Equal(result.Predicates, wantSelection.Predicates) ||
		result.EntityMatch != EntityMatchAll {
		t.Fatalf("Query() returned non-normalized request metadata: %#v", result)
	}
	if err := ValidateResult(result); err != nil {
		t.Fatalf("ValidateResult() error = %v", err)
	}
}

func TestTrendQueryProjectsExactContributionsAndRoleSeparatedCitations(t *testing.T) {
	fixture := newTrendFixture(t)
	beforeSupporting := fixture.citation("evidence-before-support", observation.EvidenceSupporting)
	beforeContradicting := fixture.citation("evidence-before-counter", observation.EvidenceContradicting)
	afterSupporting := fixture.citation("evidence-after-support", observation.EvidenceSupporting)
	before := fixture.readObservation(
		"observation-before",
		fixture.entity("entity-a"),
		fixture.text("remote"),
		fixture.instant(2024, time.January, 15),
		observation.StatusObserved,
		nil,
		beforeSupporting,
		beforeContradicting,
	)
	after := fixture.readObservation(
		"observation-after",
		fixture.entity("entity-a"),
		fixture.text("office"),
		fixture.instant(2024, time.March, 15),
		observation.StatusValidatedEmpirically,
		nil,
		afterSupporting,
	)
	reader := &recordingTrendReader{snapshot: fixture.snapshot(before, after)}

	result, err := (Service{Reader: reader, Limits: validLimits()}).Query(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	trend, ok := result.Payload.Trend()
	if !ok {
		t.Fatal("Payload.Trend() = false")
	}
	if len(trend.Before.Facts) != 1 || len(trend.After.Facts) != 1 || len(trend.Changes) != 1 {
		t.Fatalf("trend sizes = before %d after %d changes %d, want 1/1/1", len(trend.Before.Facts), len(trend.After.Facts), len(trend.Changes))
	}
	beforeFact := trend.Before.Facts[0]
	if got := beforeFact.Contributions; len(got) != 1 ||
		got[0].ObservationID != "observation-before" ||
		got[0].Status != observation.StatusObserved ||
		got[0].ValidTime != before.Observation.ValidTime() ||
		!got[0].RecordedAt.Equal(before.Observation.RecordedAt()) ||
		got[0].Derivation != before.Observation.Derivation() {
		t.Fatalf("before contributions = %#v, want exact source observation contribution", got)
	}
	if !reflect.DeepEqual(beforeFact.SupportingCitations, []Citation{beforeSupporting}) {
		t.Fatalf("supporting citations = %#v, want %#v", beforeFact.SupportingCitations, []Citation{beforeSupporting})
	}
	if !reflect.DeepEqual(beforeFact.ContradictingCitations, []Citation{beforeContradicting}) {
		t.Fatalf("contradicting citations = %#v, want %#v", beforeFact.ContradictingCitations, []Citation{beforeContradicting})
	}
	change := trend.Changes[0]
	if change.Kind != temporal.ChangeChanged || change.Before == nil || change.After == nil {
		t.Fatalf("change = %#v, want changed with before and after facts", change)
	}
	if !reflect.DeepEqual(*change.Before, trend.Before.Facts[0]) || !reflect.DeepEqual(*change.After, trend.After.Facts[0]) {
		t.Fatalf("change facts do not preserve the cited window facts")
	}

	reader.snapshot.Observations[0].Evidence[0].SectionPath[0] = "mutated"
	reader.snapshot.Observations[0].Evidence[0].Text = "mutated"
	trendAgain, _ := result.Payload.Trend()
	if !reflect.DeepEqual(trend, trendAgain) || trendAgain.Before.Facts[0].SupportingCitations[0].SectionPath[0] == "mutated" {
		t.Fatal("result retained mutable snapshot citation storage")
	}
}

func TestPointQueryProjectsExactCitationsAndGaps(t *testing.T) {
	fixture := newTrendFixture(t)
	at := time.Date(2024, time.January, 15, 12, 0, 0, 0, time.UTC)
	selection, err := temporal.At("at", at)
	if err != nil {
		t.Fatalf("temporal.At() error = %v", err)
	}
	request := Request{
		Intent:         temporal.IntentPointInTime,
		EntityIDs:      []identity.EntityID{" entity-a "},
		EntityMatch:    EntityMatchAll,
		Predicates:     []observation.Predicate{" work.location "},
		Selections:     []temporal.TemporalSelection{selection},
		KnowledgeScope: temporal.CurrentKnowledge(),
	}
	supporting := fixture.citation("evidence-point-support", observation.EvidenceSupporting)
	contradicting := fixture.citation("evidence-point-counter", observation.EvidenceContradicting)
	active := fixture.readObservation(
		"observation-point",
		fixture.entity("entity-a"),
		fixture.text("remote"),
		fixture.interval(2024, time.January, 1, time.February, 1),
		observation.StatusObserved,
		nil,
		supporting,
		contradicting,
	)
	outside := fixture.readObservation(
		"observation-outside-point",
		fixture.entity("entity-a"),
		fixture.text("office"),
		fixture.instant(2024, time.March, 15),
		observation.StatusObserved,
		nil,
		fixture.citation("evidence-point-outside", observation.EvidenceSupporting),
	)
	snapshot := fixture.snapshot(outside, active)
	snapshot.Coverage = []Coverage{{
		Reason:        CoverageUnresolvedMention,
		EntityID:      "entity-a",
		Predicate:     "work.owner",
		ObservationID: "coverage-point-unresolved",
	}}
	reader := &recordingTrendReader{snapshot: snapshot}

	result, err := (Service{Reader: reader, Limits: validLimits()}).Query(context.Background(), request)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if reader.calls != 1 {
		t.Fatalf("Reader.Read() calls = %d, want 1", reader.calls)
	}
	if !reflect.DeepEqual(reader.selection, ReadSelection{
		EntityIDs:      []identity.EntityID{"entity-a"},
		EntityMatch:    EntityMatchAll,
		Predicates:     []observation.Predicate{"work.location"},
		Selections:     []temporal.TemporalSelection{selection},
		KnowledgeScope: temporal.CurrentKnowledge(),
	}) {
		t.Fatalf("Reader.Read() selection = %#v, want normalized point selection", reader.selection)
	}
	point, ok := result.Payload.Point()
	if !ok {
		t.Fatal("Payload.Point() = false")
	}
	if point.Selection != selection || len(point.Facts) != 1 || len(point.Unresolved) != 0 {
		t.Fatalf("point payload = %#v, want one resolved fact", point)
	}
	fact := point.Facts[0]
	if got := fact.Contributions; len(got) != 1 ||
		got[0].ObservationID != "observation-point" ||
		got[0].Status != observation.StatusObserved ||
		got[0].ValidTime != active.Observation.ValidTime() {
		t.Fatalf("point contributions = %#v, want exact source observation", got)
	}
	if !reflect.DeepEqual(fact.SupportingCitations, []Citation{supporting}) ||
		!reflect.DeepEqual(fact.ContradictingCitations, []Citation{contradicting}) {
		t.Fatalf("point citations = support %#v counter %#v", fact.SupportingCitations, fact.ContradictingCitations)
	}
	wantGaps := []Gap{
		{Kind: GapValidTimeExcluded, EntityID: "entity-a", Predicate: "work.location", SelectionLabel: "at"},
		{Kind: GapUnresolvedMention, EntityID: "entity-a", Predicate: "work.owner"},
	}
	orderGaps(wantGaps)
	if !reflect.DeepEqual(result.Gaps, wantGaps) {
		t.Fatalf("point gaps = %#v, want %#v", result.Gaps, wantGaps)
	}
	if err := ValidateResult(result); err != nil {
		t.Fatalf("ValidateResult() error = %v", err)
	}
}

func TestTrendQueryPreservesConflictHypothesisCounterevidenceAndTemporalUncertainty(t *testing.T) {
	fixture := newTrendFixture(t)
	observations := []ReadObservation{
		fixture.readObservation("conflict-a", fixture.entity("entity-a"), fixture.text("remote"), fixture.interval(2024, time.January, 1, time.February, 1), observation.StatusObserved, nil, fixture.citation("evidence-conflict-a", observation.EvidenceSupporting)),
		fixture.readObservation("conflict-b", fixture.entity("entity-a"), fixture.text("office"), fixture.interval(2024, time.January, 10, time.January, 25), observation.StatusObserved, nil, fixture.citation("evidence-conflict-b", observation.EvidenceSupporting)),
		fixture.readObservationWithPredicate("hypothesis", "work.mode", fixture.entity("entity-a"), fixture.text("hybrid"), fixture.instant(2024, time.January, 16), observation.StatusHypothesized, nil, fixture.citation("evidence-hypothesis", observation.EvidenceSupporting)),
		fixture.readObservationWithPredicate("counter", "work.schedule", fixture.entity("entity-a"), fixture.text("fixed"), fixture.instant(2024, time.January, 17), observation.StatusObserved, nil, fixture.citation("evidence-counter", observation.EvidenceContradicting)),
		fixture.readObservationWithPredicate("uncertain", "work.team", fixture.entity("entity-a"), fixture.text("platform"), observation.UnknownTime(), observation.StatusObserved, nil, fixture.citation("evidence-uncertain", observation.EvidenceSupporting)),
	}
	request := fixture.request
	request.Predicates = nil
	result, err := (Service{Reader: &recordingTrendReader{snapshot: fixture.snapshot(observations...)}, Limits: validLimits()}).Query(context.Background(), request)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	trend, _ := result.Payload.Trend()
	gotReasons := make(map[temporal.UnresolvedReason]UnresolvedItem)
	for _, item := range trend.Before.Unresolved {
		gotReasons[item.Reason] = item
	}
	for _, reason := range []temporal.UnresolvedReason{
		temporal.UnresolvedConflict,
		temporal.UnresolvedHypothesis,
		temporal.UnresolvedCounterevidenceOnly,
		temporal.UnresolvedTemporalUncertainty,
	} {
		item, ok := gotReasons[reason]
		if !ok {
			t.Fatalf("before unresolved reasons = %#v, missing %q", gotReasons, reason)
		}
		if len(item.Candidates) == 0 || len(item.Candidates[0].Contributions) == 0 {
			t.Fatalf("unresolved %q lost observation contributions", reason)
		}
	}
	counter := gotReasons[temporal.UnresolvedCounterevidenceOnly].Candidates[0]
	if len(counter.SupportingCitations) != 0 || len(counter.ContradictingCitations) != 1 {
		t.Fatalf("counterevidence citations = supporting %d contradicting %d, want 0/1", len(counter.SupportingCitations), len(counter.ContradictingCitations))
	}
	if len(trend.UnresolvedKeys) != 4 {
		t.Fatalf("unresolved keys = %d, want 4", len(trend.UnresolvedKeys))
	}
}

func TestTrendQueryCreatesNoEvidenceValidTimeUnresolvedMentionAndAuthorityGaps(t *testing.T) {
	fixture := newTrendFixture(t)
	request := fixture.request
	request.EntityIDs = []identity.EntityID{"entity-a", "entity-b"}
	request.EntityMatch = EntityMatchAny
	outside := fixture.readObservation(
		"outside-windows",
		fixture.entity("entity-a"),
		fixture.text("remote"),
		fixture.instant(2024, time.June, 1),
		observation.StatusObserved,
		nil,
		fixture.citation("evidence-outside", observation.EvidenceSupporting),
	)
	snapshot := fixture.snapshot(outside)
	snapshot.Entities = append(snapshot.Entities, EntityAuthority{EntityID: "entity-b", Known: true})
	snapshot.Coverage = []Coverage{
		{Reason: CoverageUnresolvedMention, EntityID: "entity-a", Predicate: "work.owner", ObservationID: "coverage-unresolved"},
		{Reason: CoverageAuthorityExcluded, EntityID: "entity-a", Predicate: "work.team", ObservationID: "coverage-authority"},
	}

	result, err := (Service{Reader: &recordingTrendReader{snapshot: snapshot}, Limits: validLimits()}).Query(context.Background(), request)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	want := []Gap{
		{Kind: GapNoEvidence, EntityID: "entity-b"},
		{Kind: GapValidTimeExcluded, EntityID: "entity-a", Predicate: "work.location", SelectionLabel: "after"},
		{Kind: GapValidTimeExcluded, EntityID: "entity-a", Predicate: "work.location", SelectionLabel: "before"},
		{Kind: GapUnresolvedMention, EntityID: "entity-a", Predicate: "work.owner"},
		{Kind: GapAuthorityExcluded, EntityID: "entity-a", Predicate: "work.team"},
	}
	orderGaps(want)
	if !reflect.DeepEqual(result.Gaps, want) {
		t.Fatalf("Query() gaps = %#v, want %#v", result.Gaps, want)
	}
}

func TestTrendQueryCreatesNoEvidenceGapForFilteredOnlyCoverage(t *testing.T) {
	tests := []struct {
		name     string
		coverage Coverage
	}{
		{
			name: "entity filtered",
			coverage: Coverage{
				Reason:        CoverageEntityFiltered,
				EntityID:      "entity-a",
				Predicate:     "work.location",
				ObservationID: "filtered-entity",
			},
		},
		{
			name: "predicate filtered",
			coverage: Coverage{
				Reason:        CoveragePredicateFiltered,
				EntityID:      "entity-a",
				Predicate:     "work.other",
				ObservationID: "filtered-predicate",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrendFixture(t)
			snapshot := fixture.snapshot()
			snapshot.Coverage = []Coverage{test.coverage}
			result, err := (Service{
				Reader: &recordingTrendReader{snapshot: snapshot},
				Limits: validLimits(),
			}).Query(context.Background(), fixture.request)
			if err != nil {
				t.Fatalf("Query() error = %v", err)
			}
			want := []Gap{{Kind: GapNoEvidence, EntityID: "entity-a"}}
			if !reflect.DeepEqual(result.Gaps, want) {
				t.Fatalf("Query() gaps = %#v, want %#v", result.Gaps, want)
			}
		})
	}
}

func TestTrendQueryCreatesNoEvidenceGapForRejectedOnlyObservations(t *testing.T) {
	tests := []struct {
		name      string
		validTime observation.TemporalExtent
	}{
		{name: "inside selection", validTime: newTrendFixture(t).instant(2024, time.January, 15)},
		{name: "outside selections", validTime: newTrendFixture(t).instant(2024, time.June, 1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrendFixture(t)
			rejected := fixture.readObservation(
				"rejected-only",
				fixture.entity("entity-a"),
				fixture.text("remote"),
				test.validTime,
				observation.StatusRejected,
				nil,
				fixture.citation("evidence-rejected", observation.EvidenceSupporting),
			)
			result, err := (Service{
				Reader: &recordingTrendReader{snapshot: fixture.snapshot(rejected)},
				Limits: validLimits(),
			}).Query(context.Background(), fixture.request)
			if err != nil {
				t.Fatalf("Query() error = %v", err)
			}
			want := []Gap{{Kind: GapNoEvidence, EntityID: "entity-a"}}
			if !reflect.DeepEqual(result.Gaps, want) {
				t.Fatalf("Query() gaps = %#v, want %#v", result.Gaps, want)
			}
			trend, ok := result.Payload.Trend()
			if !ok {
				t.Fatal("Trend() ok = false")
			}
			if len(trend.Before.Facts)+len(trend.Before.Unresolved)+
				len(trend.After.Facts)+len(trend.After.Unresolved) != 0 {
				t.Fatalf("rejected observation emitted trend material: %#v", trend)
			}
		})
	}
}

func TestTrendQueryReturnsUnknownEntityErrorWithoutEchoingID(t *testing.T) {
	fixture := newTrendFixture(t)
	const privateID = "entity-private-do-not-echo"
	request := fixture.request
	request.EntityIDs = []identity.EntityID{privateID}
	reader := &recordingTrendReader{snapshot: ReadSnapshot{
		Entities: []EntityAuthority{{EntityID: privateID, Known: false}},
	}}

	_, err := (Service{Reader: reader, Limits: validLimits()}).Query(context.Background(), request)
	if !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("Query() error = %v, want ErrEntityNotFound", err)
	}
	if strings.Contains(err.Error(), privateID) {
		t.Fatalf("Query() error leaked entity ID: %v", err)
	}
	if reader.calls != 1 {
		t.Fatalf("Reader.Read() calls = %d, want 1", reader.calls)
	}
}

func TestTrendQueryPreservesReaderCancellationAndDoesNotRetry(t *testing.T) {
	fixture := newTrendFixture(t)
	const privateError = "private-reader-context"
	reader := &recordingTrendReader{err: fmt.Errorf("%s: %w", privateError, context.Canceled)}

	_, err := (Service{Reader: reader, Limits: validLimits()}).Query(context.Background(), fixture.request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Query() error = %v, want context cancellation identity", err)
	}
	if strings.Contains(err.Error(), privateError) {
		t.Fatalf("Query() error leaked reader context: %v", err)
	}
	if reader.calls != 1 {
		t.Fatalf("Reader.Read() calls = %d, want 1", reader.calls)
	}
}

func TestTrendQueryIsIdenticalAcrossReorderedSnapshotInput(t *testing.T) {
	fixture := newTrendFixture(t)
	before := fixture.readObservation("observation-before", fixture.entity("entity-a"), fixture.text("remote"), fixture.instant(2024, time.January, 15), observation.StatusObserved, nil,
		fixture.citation("evidence-z", observation.EvidenceSupporting),
		fixture.citation("evidence-a", observation.EvidenceContradicting),
	)
	after := fixture.readObservation("observation-after", fixture.entity("entity-a"), fixture.text("office"), fixture.instant(2024, time.March, 15), observation.StatusObserved, nil,
		fixture.citation("evidence-y", observation.EvidenceSupporting),
	)
	snapshotA := fixture.snapshot(before, after)
	snapshotA.Coverage = []Coverage{
		{Reason: CoverageAuthorityExcluded, EntityID: "entity-a", Predicate: "work.z"},
		{Reason: CoverageUnresolvedMention, EntityID: "entity-a", Predicate: "work.a"},
	}
	snapshotB := cloneReadSnapshot(snapshotA)
	slices.Reverse(snapshotB.Entities)
	slices.Reverse(snapshotB.Observations)
	slices.Reverse(snapshotB.Coverage)
	for index := range snapshotB.Observations {
		slices.Reverse(snapshotB.Observations[index].Evidence)
	}

	resultA, err := (Service{Reader: &recordingTrendReader{snapshot: snapshotA}, Limits: validLimits()}).Query(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("first Query() error = %v", err)
	}
	resultB, err := (Service{Reader: &recordingTrendReader{snapshot: snapshotB}, Limits: validLimits()}).Query(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("second Query() error = %v", err)
	}
	if !reflect.DeepEqual(resultA, resultB) {
		t.Fatalf("reordered snapshots differ:\nfirst  %#v\nsecond %#v", resultA, resultB)
	}
}

func TestTrendQueryNeverUsesConfidenceToSelectState(t *testing.T) {
	fixture := newTrendFixture(t)
	low := fixture.confidence(0.01)
	high := fixture.confidence(0.99)
	observations := []ReadObservation{
		fixture.readObservation("low-confidence", fixture.entity("entity-a"), fixture.text("remote"), fixture.instant(2024, time.January, 15), observation.StatusObserved, &low, fixture.citation("evidence-low", observation.EvidenceSupporting)),
		fixture.readObservation("high-confidence", fixture.entity("entity-a"), fixture.text("office"), fixture.instant(2024, time.January, 15), observation.StatusObserved, &high, fixture.citation("evidence-high", observation.EvidenceSupporting)),
	}

	result, err := (Service{Reader: &recordingTrendReader{snapshot: fixture.snapshot(observations...)}, Limits: validLimits()}).Query(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	trend, _ := result.Payload.Trend()
	if len(trend.Before.Facts) != 0 || len(trend.Before.Unresolved) != 1 ||
		trend.Before.Unresolved[0].Reason != temporal.UnresolvedConflict ||
		len(trend.Before.Unresolved[0].Candidates) != 2 {
		t.Fatalf("before result selected by confidence: %#v", trend.Before)
	}
}

func TestTrendQuerySpanContainsOnlyBoundedLowCardinalityAttributes(t *testing.T) {
	fixture := newTrendFixture(t)
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	reader := &recordingTrendReader{snapshot: fixture.snapshot(
		fixture.readObservation("private-observation", fixture.entity("entity-a"), fixture.text("private evidence text"), fixture.instant(2024, time.January, 15), observation.StatusObserved, nil, fixture.citation("private-evidence", observation.EvidenceSupporting)),
	)}

	_, err := (Service{Reader: reader, Limits: validLimits(), Tracer: provider.Tracer("test")}).Query(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name != "stacks.query.temporal" {
		t.Fatalf("span name = %q, want stacks.query.temporal", span.Name)
	}
	if span.Status.Code != codes.Ok {
		t.Fatalf("span status = %v, want OK", span.Status.Code)
	}
	wantAttributes := map[string]attribute.Value{
		"stacks.query.intent":                 attribute.StringValue(string(temporal.IntentTrendComparison)),
		"stacks.query.has_knowledge_cutoff":   attribute.BoolValue(false),
		"stacks.query.entity_count_bucket":    attribute.StringValue("1"),
		"stacks.query.predicate_count_bucket": attribute.StringValue("1"),
		"stacks.query.outcome":                attribute.StringValue("success"),
	}
	if len(span.Attributes) != len(wantAttributes) {
		t.Fatalf("span attributes = %#v, want only %#v", span.Attributes, wantAttributes)
	}
	for _, got := range span.Attributes {
		want, ok := wantAttributes[string(got.Key)]
		if !ok || got.Value != want {
			t.Fatalf("unexpected span attribute %s=%v", got.Key, got.Value)
		}
	}
	serialized := fmt.Sprint(span.Attributes, span.Events)
	for _, privateValue := range []string{"entity-a", "work.location", "private-observation", "private-evidence", "private evidence text"} {
		if strings.Contains(serialized, privateValue) {
			t.Fatalf("span telemetry leaked %q: %s", privateValue, serialized)
		}
	}
}

func TestTrendQueryRejectsInvalidInputsBeforeReaderAccess(t *testing.T) {
	fixture := newTrendFixture(t)
	reader := &recordingTrendReader{}
	tests := []struct {
		name    string
		ctx     context.Context
		service Service
		request Request
	}{
		{name: "nil context", ctx: nil, service: Service{Reader: reader, Limits: validLimits()}, request: fixture.request},
		{name: "missing reader", ctx: context.Background(), service: Service{Limits: validLimits()}, request: fixture.request},
		{name: "invalid limits", ctx: context.Background(), service: Service{Reader: reader}, request: fixture.request},
		{name: "invalid plan", ctx: context.Background(), service: Service{Reader: reader, Limits: validLimits()}, request: Request{Intent: temporal.IntentTrendComparison, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, KnowledgeScope: temporal.CurrentKnowledge()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := reader.calls
			if _, err := test.service.Query(test.ctx, test.request); err == nil {
				t.Fatal("Query() error = nil")
			}
			if reader.calls != before {
				t.Fatalf("Reader.Read() calls changed from %d to %d", before, reader.calls)
			}
		})
	}
}

type recordingTrendReader struct {
	snapshot  ReadSnapshot
	err       error
	calls     int
	selection ReadSelection
}

func (reader *recordingTrendReader) Read(_ context.Context, selection ReadSelection) (ReadSnapshot, error) {
	reader.calls++
	reader.selection = selection
	return cloneReadSnapshot(reader.snapshot), reader.err
}

type trendFixture struct {
	t       *testing.T
	request Request
}

func newTrendFixture(t *testing.T) trendFixture {
	t.Helper()
	before, err := temporal.Between("before", time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC), time.Date(2024, time.February, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("temporal.Between(before) error = %v", err)
	}
	after, err := temporal.Between("after", time.Date(2024, time.March, 1, 0, 0, 0, 0, time.UTC), time.Date(2024, time.April, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("temporal.Between(after) error = %v", err)
	}
	return trendFixture{
		t: t,
		request: Request{
			Intent:         temporal.IntentTrendComparison,
			EntityIDs:      []identity.EntityID{"entity-a"},
			EntityMatch:    EntityMatchAll,
			Predicates:     []observation.Predicate{"work.location"},
			Selections:     []temporal.TemporalSelection{before, after},
			KnowledgeScope: temporal.CurrentKnowledge(),
		},
	}
}

func (fixture trendFixture) snapshot(observations ...ReadObservation) ReadSnapshot {
	return ReadSnapshot{
		Entities:     []EntityAuthority{{EntityID: "entity-a", Known: true}},
		Observations: append([]ReadObservation{}, observations...),
	}
}

func (fixture trendFixture) readObservation(
	id string,
	subject observation.Term,
	object observation.Term,
	validTime observation.TemporalExtent,
	status observation.EpistemicStatus,
	confidence *observation.Confidence,
	citations ...Citation,
) ReadObservation {
	return fixture.readObservationWithPredicate(id, "work.location", subject, object, validTime, status, confidence, citations...)
}

func (fixture trendFixture) readObservationWithPredicate(
	id string,
	predicate observation.Predicate,
	subject observation.Term,
	object observation.Term,
	validTime observation.TemporalExtent,
	status observation.EpistemicStatus,
	confidence *observation.Confidence,
	citations ...Citation,
) ReadObservation {
	fixture.t.Helper()
	links := make([]observation.EvidenceLink, len(citations))
	for index, citation := range citations {
		links[index] = observation.EvidenceLink{EvidenceID: citation.EvidenceID, Role: citation.Role}
	}
	value, err := observation.NewObservation(observation.ObservationInput{
		ID:         observation.ObservationID(id),
		Statement:  observation.Statement{Subject: subject, Predicate: predicate, Object: object},
		ValidTime:  validTime,
		RecordedAt: time.Date(2024, time.July, 1, 12, 0, 0, 123456789, time.FixedZone("synthetic", 3600)),
		Evidence:   links,
		Derivation: observation.Derivation{Method: "synthetic", Version: "v1", RunID: "run-1"},
		Status:     status,
		Confidence: confidence,
	})
	if err != nil {
		fixture.t.Fatalf("observation.NewObservation(%q) error = %v", id, err)
	}
	return ReadObservation{
		Observation: value,
		Subject:     subject,
		Object:      object,
		Evidence:    cloneCitations(citations),
	}
}

func (fixture trendFixture) citation(id string, role observation.EvidenceRole) Citation {
	return Citation{
		EvidenceID:        evidence.EvidenceID(id),
		Role:              role,
		SourceDocumentID:  "document-" + id,
		DocumentVersionID: "version-" + id,
		SectionID:         "section-" + id,
		SectionTitle:      "Synthetic section",
		SectionPath:       []string{"Synthetic", "Section"},
		SectionOrder:      1,
		SectionRole:       "body",
		StartOffset:       2,
		EndOffset:         8,
		Locator:           "synthetic://document",
		Text:              "source",
	}
}

func (fixture trendFixture) entity(id string) observation.Term {
	fixture.t.Helper()
	value, err := observation.NewEntityTerm(id, "")
	if err != nil {
		fixture.t.Fatalf("observation.NewEntityTerm() error = %v", err)
	}
	return value
}

func (fixture trendFixture) text(value string) observation.Term {
	fixture.t.Helper()
	result, err := observation.NewTextTerm(value)
	if err != nil {
		fixture.t.Fatalf("observation.NewTextTerm() error = %v", err)
	}
	return result
}

func (fixture trendFixture) instant(year int, month time.Month, day int) observation.TemporalExtent {
	fixture.t.Helper()
	value, err := observation.AtTime(time.Date(year, month, day, 12, 0, 0, 0, time.UTC))
	if err != nil {
		fixture.t.Fatalf("observation.AtTime() error = %v", err)
	}
	return value
}

func (fixture trendFixture) interval(year int, startMonth time.Month, startDay int, endMonth time.Month, endDay int) observation.TemporalExtent {
	fixture.t.Helper()
	value, err := observation.During(
		time.Date(year, startMonth, startDay, 0, 0, 0, 0, time.UTC),
		time.Date(year, endMonth, endDay, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		fixture.t.Fatalf("observation.During() error = %v", err)
	}
	return value
}

func (fixture trendFixture) confidence(value float64) observation.Confidence {
	fixture.t.Helper()
	result, err := observation.NewUnitIntervalConfidence(value)
	if err != nil {
		fixture.t.Fatalf("observation.NewUnitIntervalConfidence() error = %v", err)
	}
	return result
}

func cloneReadSnapshot(value ReadSnapshot) ReadSnapshot {
	value.Entities = append([]EntityAuthority{}, value.Entities...)
	value.Observations = append([]ReadObservation{}, value.Observations...)
	for index := range value.Observations {
		value.Observations[index].Evidence = cloneCitations(value.Observations[index].Evidence)
	}
	value.Coverage = append([]Coverage{}, value.Coverage...)
	return value
}
