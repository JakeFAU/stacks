package knowledge

import (
	"testing"
	"time"

	"stacks/internal/source"
)

func TestDocumentVersionDigestChangesWhenTabOrderChanges(t *testing.T) {
	first, err := NewDocumentVersion(documentVersionInput([]source.Tab{
		sourceTab("t.transcript", "Transcript", "Alex: synthetic words"),
		sourceTab("t.notes", "Meeting notes", "summary"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewDocumentVersion(documentVersionInput([]source.Tab{
		sourceTab("t.notes", "Meeting notes", "summary"),
		sourceTab("t.transcript", "Transcript", "Alex: synthetic words"),
	}))
	if err != nil {
		t.Fatal(err)
	}

	if first.Digest() == second.Digest() {
		t.Fatal("document digests are equal after tab order changed")
	}
}

func TestDocumentVersionDigestChangesWhenTabContentChanges(t *testing.T) {
	first, err := NewDocumentVersion(documentVersionInput([]source.Tab{
		sourceTab("t.transcript", "Transcript", "Alex: synthetic words"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewDocumentVersion(documentVersionInput([]source.Tab{
		sourceTab("t.transcript", "Transcript", "Alex: revised words"),
	}))
	if err != nil {
		t.Fatal(err)
	}

	if first.Digest() == second.Digest() {
		t.Fatal("document digests are equal after tab content changed")
	}
}

func TestDocumentVersionCopiesMutableSourceTabs(t *testing.T) {
	tabs := []source.Tab{sourceTab("t.transcript", "Transcript", "Alex: synthetic words")}
	version, err := NewDocumentVersion(documentVersionInput(tabs))
	if err != nil {
		t.Fatal(err)
	}

	tabs[0].Text = "changed"
	tabs[0].Path[0] = "Changed"
	gotTabs := version.Tabs()
	gotTabs[0].Text = "changed again"
	gotTabs[0].Path[0] = "Changed again"

	if version.Tabs()[0].Text != "Alex: synthetic words" {
		t.Errorf("stored tab text = %q, want immutable source text", version.Tabs()[0].Text)
	}
	if version.Tabs()[0].Path[0] != "Transcript" {
		t.Errorf("stored tab path = %#v, want immutable source path", version.Tabs()[0].Path)
	}
}

func TestDocumentVersionRetainsImmutableSourceProvenance(t *testing.T) {
	modifiedAt := time.Date(2026, time.July, 20, 15, 30, 0, 0, time.UTC)
	meetingTime := time.Date(2026, time.July, 20, 9, 0, 0, 0, time.FixedZone("synthetic", -4*60*60))
	input := documentVersionInput([]source.Tab{sourceTab("t.transcript", "Transcript", "Alex: synthetic words")})
	input.Title = "Synthetic weekly meeting"
	input.Locator = "https://docs.example.invalid/document/doc-1"
	input.ProviderVersion = "drive-version-42"
	input.ProviderRevision = "docs-revision-7"
	input.ModifiedAt = modifiedAt
	input.SourceMeetingTime = &meetingTime

	version, err := NewDocumentVersion(input)
	if err != nil {
		t.Fatalf("NewDocumentVersion() error = %v", err)
	}
	meetingTime = meetingTime.Add(24 * time.Hour)

	if version.Title() != input.Title || version.Locator() != input.Locator ||
		version.ProviderVersion() != input.ProviderVersion || version.ProviderRevision() != input.ProviderRevision ||
		!version.ModifiedAt().Equal(modifiedAt) {
		t.Fatalf("source provenance was not retained: title=%q locator=%q version=%q revision=%q modified=%s",
			version.Title(), version.Locator(), version.ProviderVersion(), version.ProviderRevision(), version.ModifiedAt())
	}
	if got := version.SourceMeetingTime(); got == nil || !got.Equal(time.Date(2026, time.July, 20, 9, 0, 0, 0, time.FixedZone("synthetic", -4*60*60))) {
		t.Fatalf("SourceMeetingTime() = %v, want immutable source value", got)
	}
}

func TestDocumentVersionDigestIgnoresProviderRevisionChurn(t *testing.T) {
	firstInput := documentVersionInput([]source.Tab{sourceTab("t.transcript", "Transcript", "Alex: synthetic words")})
	firstInput.ProviderRevision = "revision-1"
	secondInput := firstInput
	secondInput.ProviderRevision = "revision-2"

	first, err := NewDocumentVersion(firstInput)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewDocumentVersion(secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest() != second.Digest() {
		t.Fatal("provider revision churn changed the immutable content identity")
	}
}

func TestDocumentVersionAllowsMissingOptionalProviderRevision(t *testing.T) {
	input := documentVersionInput([]source.Tab{sourceTab("t.transcript", "Transcript", "Alex: synthetic words")})
	input.ProviderRevision = ""

	version, err := NewDocumentVersion(input)
	if err != nil {
		t.Fatalf("NewDocumentVersion() error = %v, want view-only source without revision metadata", err)
	}
	if version.ProviderRevision() != "" {
		t.Fatalf("ProviderRevision() = %q, want missing optional provenance retained", version.ProviderRevision())
	}
}

func TestEvidenceSpanRejectsInvalidUTF8ByteOffsets(t *testing.T) {
	version, err := NewDocumentVersion(documentVersionInput([]source.Tab{
		sourceTab("t.transcript", "Transcript", "AéB"),
	}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewEvidenceSpan(EvidenceSpanInput{
		Document:    version,
		TabID:       "t.transcript",
		StartOffset: 2,
		EndOffset:   3,
		Quote:       "",
	})
	if err == nil {
		t.Fatal("NewEvidenceSpan() error = nil, want invalid UTF-8 offset error")
	}
}

func TestEvidenceSpanRejectsExactQuoteMismatch(t *testing.T) {
	version, err := NewDocumentVersion(documentVersionInput([]source.Tab{
		sourceTab("t.transcript", "Transcript", "Alex: synthetic words"),
	}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewEvidenceSpan(EvidenceSpanInput{
		Document:    version,
		TabID:       "t.transcript",
		StartOffset: 0,
		EndOffset:   4,
		Quote:       "Alec",
	})
	if err == nil {
		t.Fatal("NewEvidenceSpan() error = nil, want exact quote mismatch error")
	}
}

func TestEvidenceSpanRetainsProviderDocumentTabAndExactLocalText(t *testing.T) {
	version, err := NewDocumentVersion(documentVersionInput([]source.Tab{
		sourceTab("t.transcript", "Transcript", "Alex: synthetic words"),
	}))
	if err != nil {
		t.Fatal(err)
	}

	span, err := NewEvidenceSpan(EvidenceSpanInput{
		Document:    version,
		TabID:       "t.transcript",
		StartOffset: 6,
		EndOffset:   15,
		Quote:       "synthetic",
	})
	if err != nil {
		t.Fatal(err)
	}

	if span.ProviderDocumentID() != "doc-1" {
		t.Errorf("ProviderDocumentID() = %q, want %q", span.ProviderDocumentID(), "doc-1")
	}
	if span.TabID() != "t.transcript" {
		t.Errorf("TabID() = %q, want %q", span.TabID(), "t.transcript")
	}
	if span.Text() != "synthetic" {
		t.Errorf("Text() = %q, want exact local quote", span.Text())
	}
}

func documentVersionInput(tabs []source.Tab) DocumentVersionInput {
	return DocumentVersionInput{
		Provider:           "drive",
		ProviderDocumentID: "doc-1",
		Title:              "Synthetic meeting",
		Locator:            "https://docs.example.invalid/document/doc-1",
		ProviderVersion:    "drive-version-1",
		ProviderRevision:   "docs-revision-1",
		ModifiedAt:         time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC),
		RecordedAt:         time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
		Tabs:               tabs,
	}
}

func sourceTab(id, title, text string) source.Tab {
	return source.Tab{
		ID:    id,
		Title: title,
		Path:  []string{title},
		Role:  source.TabRoleTranscript,
		Text:  text,
	}
}
