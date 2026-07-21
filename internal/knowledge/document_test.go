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
