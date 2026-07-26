package timepoint_test

import (
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
	"github.com/JakeFAU/stacks/core/timepoint"
)

func TestNormalizeUsesUTCMicrosecondPrecision(t *testing.T) {
	input := time.Date(2026, time.July, 25, 10, 11, 12, 123456789, time.FixedZone("EDT", -4*60*60))
	want := time.Date(2026, time.July, 25, 14, 11, 12, 123456000, time.UTC)

	if got := timepoint.Normalize(input); got != want {
		t.Fatalf("Normalize() = %v, want %v", got, want)
	}
}

func TestNormalizeRemovesMonotonicState(t *testing.T) {
	input := time.Now()
	want := time.Unix(0, input.UnixNano()).UTC().Truncate(time.Microsecond)

	if got := timepoint.Normalize(input); got != want {
		t.Fatalf("Normalize() = %#v, want monotonic-free %#v", got, want)
	}
}

func TestIsCanonicalRejectsLocalAndSubMicrosecondValues(t *testing.T) {
	canonical := time.Date(2026, time.July, 25, 14, 11, 12, 123456000, time.UTC)
	local := canonical.In(time.FixedZone("EDT", -4*60*60))
	subMicrosecond := canonical.Add(789 * time.Nanosecond)

	if !timepoint.IsCanonical(canonical) {
		t.Fatal("IsCanonical() = false, want canonical UTC microsecond value accepted")
	}
	for _, value := range []time.Time{local, subMicrosecond} {
		if timepoint.IsCanonical(value) {
			t.Fatalf("IsCanonical(%v) = true, want false", value)
		}
	}
}

func TestEveryCoreConstructorNormalizesPersistedTimes(t *testing.T) {
	inputTime := time.Date(2026, time.July, 25, 10, 11, 12, 123456789, time.FixedZone("EDT", -4*60*60))
	want := time.Date(2026, time.July, 25, 14, 11, 12, 123456000, time.UTC)
	section, err := evidence.NewSection(evidence.SectionInput{ID: "section-1", Title: "Transcript", Role: "transcript", Text: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	document, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{
		Provider: "drive", ProviderDocumentID: "document-1", Title: "Synthetic", Locator: "https://example.invalid/document-1",
		ProviderVersion: "version-1", ModifiedAt: inputTime, SourceTime: &inputTime, RecordedAt: inputTime, Sections: []evidence.Section{section},
	})
	if err != nil {
		t.Fatal(err)
	}
	if document.ModifiedAt() != want || document.RecordedAt() != want || *document.SourceTime() != want {
		t.Fatalf("document times = (%v, %v, %v), want (%v, %v, %v)", document.ModifiedAt(), document.RecordedAt(), *document.SourceTime(), want, want, want)
	}

	revision, err := evidence.NewSourceRevisionObservation(evidence.SourceRevisionObservationInput{
		Provider: "drive", ProviderDocumentID: "document-1", DocumentDigestVersion: "stacks.document.v3.utc-microsecond", DocumentDigest: document.Digest(), ProviderVersion: "version-1", FirstRecordedAt: inputTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision.FirstRecordedAt() != want {
		t.Fatalf("source revision first recorded time = %v, want %v", revision.FirstRecordedAt(), want)
	}

	span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{Document: document, SectionID: "section-1", StartOffset: 0, EndOffset: len("Alice"), Quote: "Alice", RecordedAt: inputTime})
	if err != nil {
		t.Fatal(err)
	}
	if span.RecordedAt() != want {
		t.Fatalf("evidence recorded time = %v, want %v", span.RecordedAt(), want)
	}

	validTime, err := observation.AtTime(inputTime)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := observation.NewTextTerm("Alice")
	if err != nil {
		t.Fatal(err)
	}
	object, err := observation.NewTextTerm("Bob")
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := observation.NewPredicate("manages")
	if err != nil {
		t.Fatal(err)
	}
	value, err := observation.NewObservation(observation.ObservationInput{
		ID: "observation-retry-id", Statement: observation.Statement{Subject: subject, Predicate: predicate, Object: object}, ValidTime: validTime, RecordedAt: inputTime,
		Evidence: []observation.EvidenceLink{{EvidenceID: "evidence-1", Role: observation.EvidenceSupporting}}, Derivation: observation.Derivation{Method: "extractor", Version: "v1"}, Status: observation.StatusObserved,
	})
	if err != nil {
		t.Fatal(err)
	}
	instant, ok := value.ValidTime().Instant()
	if !ok || instant != want || value.RecordedAt() != want {
		t.Fatalf("observation times = (%v, %v, %v), want (%v, %v, true)", instant, value.RecordedAt(), ok, want, want)
	}

	selection, err := temporal.At("then", inputTime)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := temporal.KnownAsOf(inputTime)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := temporal.NewPlan(temporal.PlanInput{Intent: temporal.IntentPointInTime, EntityIDs: []string{"entity-1"}, Selections: []temporal.TemporalSelection{selection}, KnowledgeScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	point, _ := plan.Selections()[0].Point()
	cutoff, _ := plan.KnowledgeScope().AsOf()
	if point != want || cutoff != want {
		t.Fatalf("plan cutoffs = (%v, %v), want (%v, %v)", point, cutoff, want, want)
	}
}
