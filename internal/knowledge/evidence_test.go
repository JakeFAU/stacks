package knowledge

import (
	"testing"
	"time"
)

func TestEvidencePreservesImmutableSourceIdentity(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
	digest := DigestContent([]byte("synthetic source content"))

	evidence, err := NewEvidence(EvidenceInput{
		ID: EvidenceID("evidence-1"),
		Source: SourceReference{
			Provider:   " drive ",
			DocumentID: " document-1 ",
			Version:    " revision-3 ",
			Locator:    " /Products/Stacks ",
		},
		Digest:     digest,
		RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}

	if evidence.ID() != EvidenceID("evidence-1") {
		t.Errorf("ID() = %q, want %q", evidence.ID(), EvidenceID("evidence-1"))
	}
	if evidence.Source().Provider != "drive" {
		t.Errorf("Source().Provider = %q, want %q", evidence.Source().Provider, "drive")
	}
	if evidence.Digest() != digest {
		t.Errorf("Digest() = %q, want %q", evidence.Digest(), digest)
	}
	if evidence.RecordedAt() != recordedAt.UTC() {
		t.Errorf("RecordedAt() = %v, want %v", evidence.RecordedAt(), recordedAt.UTC())
	}
}

func TestParseContentDigestRoundTrips(t *testing.T) {
	want := DigestContent([]byte("synthetic source content"))

	got, err := ParseContentDigest(want.String())
	if err != nil {
		t.Fatalf("ParseContentDigest() error = %v", err)
	}
	if got != want {
		t.Errorf("ParseContentDigest() = %q, want %q", got, want)
	}
}

func TestParseContentDigestRejectsInvalidValue(t *testing.T) {
	if _, err := ParseContentDigest("not-a-sha256-digest"); err == nil {
		t.Fatal("ParseContentDigest() error = nil, want invalid digest error")
	}
}

func TestNewEvidenceRejectsInvalidInput(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	digest := DigestContent([]byte("synthetic source content"))

	tests := []struct {
		name  string
		input EvidenceInput
	}{
		{
			name: "missing ID",
			input: EvidenceInput{
				Source:     SourceReference{Provider: "drive", DocumentID: "document-1"},
				Digest:     digest,
				RecordedAt: recordedAt,
			},
		},
		{
			name: "missing provider",
			input: EvidenceInput{
				ID:         EvidenceID("evidence-1"),
				Source:     SourceReference{DocumentID: "document-1"},
				Digest:     digest,
				RecordedAt: recordedAt,
			},
		},
		{
			name: "missing document ID",
			input: EvidenceInput{
				ID:         EvidenceID("evidence-1"),
				Source:     SourceReference{Provider: "drive"},
				Digest:     digest,
				RecordedAt: recordedAt,
			},
		},
		{
			name: "missing digest",
			input: EvidenceInput{
				ID:         EvidenceID("evidence-1"),
				Source:     SourceReference{Provider: "drive", DocumentID: "document-1"},
				RecordedAt: recordedAt,
			},
		},
		{
			name: "missing recorded time",
			input: EvidenceInput{
				ID:     EvidenceID("evidence-1"),
				Source: SourceReference{Provider: "drive", DocumentID: "document-1"},
				Digest: digest,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewEvidence(test.input); err == nil {
				t.Fatal("NewEvidence() error = nil, want validation error")
			}
		})
	}
}
