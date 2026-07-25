package evidence_test

import (
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
)

func TestEvidencePreservesImmutableSourceIdentity(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
	digest := evidence.DigestContent([]byte("synthetic source content"))

	got, err := evidence.NewEvidence(evidence.EvidenceInput{
		ID:     evidence.EvidenceID("evidence-1"),
		Source: evidence.SourceReference{Provider: " drive ", DocumentID: " document-1 ", Version: " revision-3 ", Locator: " /Products/Stacks "},
		Digest: digest, RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	if got.ID() != "evidence-1" || got.Source().Provider != "drive" || got.Digest() != digest || got.RecordedAt() != recordedAt.UTC() {
		t.Fatalf("NewEvidence() = %#v, want normalized immutable evidence", got)
	}
}

func TestParseContentDigestRoundTrips(t *testing.T) {
	want := evidence.DigestContent([]byte("synthetic source content"))
	got, err := evidence.ParseContentDigest(want.String())
	if err != nil {
		t.Fatalf("ParseContentDigest() error = %v", err)
	}
	if got != want {
		t.Errorf("ParseContentDigest() = %q, want %q", got, want)
	}
}

func TestParseContentDigestRejectsInvalidValue(t *testing.T) {
	if _, err := evidence.ParseContentDigest("not-a-sha256-digest"); err == nil {
		t.Fatal("ParseContentDigest() error = nil, want invalid digest error")
	}
}

func TestNewEvidenceRejectsInvalidInput(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	digest := evidence.DigestContent([]byte("synthetic source content"))
	tests := []evidence.EvidenceInput{
		{Source: evidence.SourceReference{Provider: "drive", DocumentID: "document-1"}, Digest: digest, RecordedAt: recordedAt},
		{ID: "evidence-1", Source: evidence.SourceReference{DocumentID: "document-1"}, Digest: digest, RecordedAt: recordedAt},
		{ID: "evidence-1", Source: evidence.SourceReference{Provider: "drive"}, Digest: digest, RecordedAt: recordedAt},
		{ID: "evidence-1", Source: evidence.SourceReference{Provider: "drive", DocumentID: "document-1"}, RecordedAt: recordedAt},
		{ID: "evidence-1", Source: evidence.SourceReference{Provider: "drive", DocumentID: "document-1"}, Digest: digest},
	}
	for _, input := range tests {
		if _, err := evidence.NewEvidence(input); err == nil {
			t.Fatal("NewEvidence() error = nil, want validation error")
		}
	}
}
