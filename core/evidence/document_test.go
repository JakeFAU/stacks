package evidence_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
)

const stableDigestGolden = "c48323ec2669ee9dfea8d5c702ab31ffd6c68107d033df07c7f1c2861ea1943b"

func TestPublicAPIUsesProviderNeutralTimeAndSectionNames(t *testing.T) {
	documentType := reflect.TypeOf(evidence.DocumentVersion{})
	if _, exists := documentType.MethodByName("SourceTime"); !exists {
		t.Fatal("DocumentVersion.SourceTime is missing")
	}
	if _, exists := documentType.MethodByName("SourceMeetingTime"); exists {
		t.Fatal("DocumentVersion exposes meeting-specific SourceMeetingTime")
	}
	if _, exists := documentType.MethodByName("ProviderRevision"); exists {
		t.Fatal("DocumentVersion exposes source-revision compatibility metadata")
	}
	if _, exists := reflect.TypeOf(evidence.DocumentVersionInput{}).FieldByName("ProviderRevision"); exists {
		t.Fatal("DocumentVersionInput exposes source-revision compatibility metadata")
	}

	spanInputType := reflect.TypeOf(evidence.EvidenceSpanInput{})
	if _, exists := spanInputType.FieldByName("SectionID"); !exists {
		t.Fatal("EvidenceSpanInput.SectionID is missing")
	}
	if _, exists := spanInputType.FieldByName("TabID"); exists {
		t.Fatal("EvidenceSpanInput exposes provider-specific TabID")
	}
}

func TestSectionRejectsEmptyIDTitleRoleInvalidOrderAndInvalidUTF8(t *testing.T) {
	tests := []evidence.SectionInput{
		{Title: "Transcript", Role: "transcript", Text: "text"},
		{ID: "tab", Role: "transcript", Text: "text"},
		{ID: "tab", Title: "Transcript", Text: "text"},
		{ID: "tab", Title: "Transcript", Role: "transcript", Order: -1, Text: "text"},
		{ID: "tab", Title: "Transcript", Role: "transcript", Text: string([]byte{0xff})},
	}
	for _, input := range tests {
		if _, err := evidence.NewSection(input); err == nil {
			t.Fatal("NewSection() error = nil, want validation error")
		}
	}
}

func TestSectionDefensivelyCopiesPath(t *testing.T) {
	path := []string{" Transcript "}
	section, err := evidence.NewSection(evidence.SectionInput{ID: " tab ", Title: " Transcript ", ParentID: " parent ", Path: path, Role: " transcript ", Text: "text"})
	if err != nil {
		t.Fatal(err)
	}
	path[0] = "changed"
	got := section.Path()
	got[0] = "changed again"
	if section.ID() != "tab" || section.Title() != "Transcript" || section.ParentID() != "parent" || section.Role() != "transcript" || section.Path()[0] != "Transcript" {
		t.Fatalf("section was not copied and normalized: %#v", section)
	}
}

func TestDocumentVersionDefensivelyCopiesSections(t *testing.T) {
	sections := []evidence.Section{section(t, "t.transcript", "Transcript", "Alex: synthetic words")}
	version := document(t, sections)
	sections[0], _ = evidence.NewSection(evidence.SectionInput{ID: "changed", Title: "Changed", Role: "other", Text: "changed"})
	got := version.Sections()
	got[0], _ = evidence.NewSection(evidence.SectionInput{ID: "changed-again", Title: "Changed", Role: "other", Text: "changed"})
	if version.Sections()[0].ID() != "t.transcript" || version.Sections()[0].Text() != "Alex: synthetic words" {
		t.Fatal("document sections were mutable")
	}
}

func TestDocumentVersionDigestChangesWhenSectionOrderChanges(t *testing.T) {
	first := document(t, []evidence.Section{section(t, "t.transcript", "Transcript", "Alex: synthetic words"), section(t, "t.notes", "Meeting notes", "summary")})
	second := document(t, []evidence.Section{section(t, "t.notes", "Meeting notes", "summary"), section(t, "t.transcript", "Transcript", "Alex: synthetic words")})
	if first.Digest() == second.Digest() {
		t.Fatal("document digests are equal after section order changed")
	}
}

func TestDocumentVersionDigestChangesWhenSectionContentChanges(t *testing.T) {
	first := document(t, []evidence.Section{section(t, "t.transcript", "Transcript", "Alex: synthetic words")})
	second := document(t, []evidence.Section{section(t, "t.transcript", "Transcript", "Alex: revised words")})
	if first.Digest() == second.Digest() {
		t.Fatal("document digests are equal after section content changed")
	}
}

func TestDocumentVersionRetainsImmutableSourceProvenance(t *testing.T) {
	meeting := time.Date(2026, time.July, 20, 9, 0, 0, 0, time.FixedZone("synthetic", -4*60*60))
	input := documentInput([]evidence.Section{section(t, "t.transcript", "Transcript", "Alex: synthetic words")})
	input.Title, input.Locator, input.ProviderVersion = "Synthetic weekly meeting", "https://docs.example.invalid/document/doc-1", "drive-version-42"
	input.ModifiedAt, input.SourceTime = time.Date(2026, time.July, 20, 15, 30, 0, 0, time.UTC), &meeting
	version, err := evidence.NewDocumentVersion(input)
	if err != nil {
		t.Fatal(err)
	}
	meeting = meeting.Add(24 * time.Hour)
	got := version.SourceTime()
	if version.Title() != input.Title || version.Locator() != input.Locator || version.ProviderVersion() != input.ProviderVersion || got == nil || !got.Equal(time.Date(2026, time.July, 20, 9, 0, 0, 0, time.FixedZone("synthetic", -4*60*60))) {
		t.Fatal("source provenance was not retained")
	}
}

func TestDocumentVersionDigestV3Golden(t *testing.T) {
	version := document(t, []evidence.Section{section(t, "t.transcript", "Transcript", "Alex: synthetic words")})
	if version.DigestVersion() != evidence.DocumentDigestVersion {
		t.Fatalf("DigestVersion() = %q, want %q", version.DigestVersion(), evidence.DocumentDigestVersion)
	}
	if got := version.Digest().String(); got != stableDigestGolden {
		t.Fatalf("Digest() = %q, want %q", got, stableDigestGolden)
	}
}

func TestDocumentDigestIgnoresRecordedAt(t *testing.T) {
	input := documentInput([]evidence.Section{section(t, "t.transcript", "Transcript", "Alex: synthetic words")})
	first, err := evidence.NewDocumentVersion(input)
	if err != nil {
		t.Fatal(err)
	}
	input.RecordedAt = input.RecordedAt.Add(24 * time.Hour)
	second, err := evidence.NewDocumentVersion(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest() != second.Digest() {
		t.Fatal("content digest changed for recorded time")
	}
}

func TestEvidenceSpanDigestCoversExactSourceRangeAndRecordedTime(t *testing.T) {
	version := document(t, []evidence.Section{section(t, "t.transcript", "Transcript", "Alex synthetic")})
	recordedAt := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	first, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{Document: version, SectionID: "t.transcript", StartOffset: 0, EndOffset: 4, Quote: "Alex", RecordedAt: recordedAt})
	if err != nil {
		t.Fatal(err)
	}
	second, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{Document: version, SectionID: "t.transcript", StartOffset: 5, EndOffset: 14, Quote: "synthetic", RecordedAt: recordedAt})
	if err != nil {
		t.Fatal(err)
	}
	third, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{Document: version, SectionID: "t.transcript", StartOffset: 0, EndOffset: 4, Quote: "Alex", RecordedAt: recordedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() == second.ID() || first.Digest() == second.Digest() || first.ID() != third.ID() || first.Digest() == third.Digest() {
		t.Fatal("evidence digest or identity does not distinguish its required fields")
	}
}

func TestEvidenceSpanRejectsInvalidUTF8ByteOffsets(t *testing.T) {
	version := document(t, []evidence.Section{section(t, "t.transcript", "Transcript", "AéB")})
	if _, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{Document: version, SectionID: "t.transcript", StartOffset: 2, EndOffset: 3, RecordedAt: version.RecordedAt()}); err == nil {
		t.Fatal("NewEvidenceSpan() error = nil, want invalid UTF-8 offset error")
	}
}

func TestEvidenceSpanRejectsExactQuoteMismatch(t *testing.T) {
	version := document(t, []evidence.Section{section(t, "t.transcript", "Transcript", "Alex: synthetic words")})
	if _, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{Document: version, SectionID: "t.transcript", EndOffset: 4, Quote: "Alec", RecordedAt: version.RecordedAt()}); err == nil {
		t.Fatal("NewEvidenceSpan() error = nil, want exact quote mismatch error")
	}
}

func TestEvidenceSpanRetainsProviderDocumentSectionAndExactLocalText(t *testing.T) {
	version := document(t, []evidence.Section{section(t, "t.transcript", "Transcript", "Alex: synthetic words")})
	span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{Document: version, SectionID: "t.transcript", StartOffset: 6, EndOffset: 15, Quote: "synthetic", RecordedAt: version.RecordedAt()})
	if err != nil {
		t.Fatal(err)
	}
	if span.ProviderDocumentID() != "doc-1" || span.SectionID() != "t.transcript" || span.Text() != "synthetic" {
		t.Fatalf("span = %#v, want exact section-local evidence", span)
	}
}

func document(t *testing.T, sections []evidence.Section) evidence.DocumentVersion {
	t.Helper()
	got, err := evidence.NewDocumentVersion(documentInput(sections))
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func documentInput(sections []evidence.Section) evidence.DocumentVersionInput {
	return evidence.DocumentVersionInput{Provider: "drive", ProviderDocumentID: "doc-1", Title: "Synthetic meeting", Locator: "https://docs.example.invalid/document/doc-1", ProviderVersion: "drive-version-1", ModifiedAt: time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC), RecordedAt: time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC), Sections: sections}
}
func section(t *testing.T, id, title, text string) evidence.Section {
	t.Helper()
	got, err := evidence.NewSection(evidence.SectionInput{ID: id, Title: title, Path: []string{title}, Role: "transcript", Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return got
}
