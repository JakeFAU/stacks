package evidence_test

import (
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
)

func TestSourceRevisionDigestSeparatesProviderRevisionFromContentVersion(t *testing.T) {
	document := document(t, []evidence.Section{section(t, "t.transcript", "Transcript", "Alex: synthetic words")})
	input := evidence.SourceRevisionObservationInput{
		Provider: "drive", ProviderDocumentID: "doc-1", DocumentDigestVersion: evidence.DocumentDigestVersion, DocumentDigest: document.Digest(),
		ProviderVersion: "drive-version-1", ProviderRevision: "revision-1", FirstRecordedAt: time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
	}
	first, err := evidence.NewSourceRevisionObservation(input)
	if err != nil {
		t.Fatal(err)
	}
	input.ProviderRevision = "revision-2"
	second, err := evidence.NewSourceRevisionObservation(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() == second.ID() || first.Digest() == second.Digest() {
		t.Fatal("provider revision did not create distinct source-revision provenance")
	}
	input.ProviderRevision = "revision-1"
	input.FirstRecordedAt = input.FirstRecordedAt.Add(time.Second)
	third, err := evidence.NewSourceRevisionObservation(input)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID() != first.ID() || third.Digest() == first.Digest() {
		t.Fatal("source-revision identity included recorded time or digest omitted it")
	}
}

func TestSourceRevisionExposesExactCanonicalPersistenceFields(t *testing.T) {
	documentDigest := evidence.DigestContent([]byte("synthetic immutable document"))
	recordedAt := time.Date(
		2026,
		time.July,
		21,
		8,
		30,
		0,
		123456789,
		time.FixedZone("synthetic", -4*60*60),
	)
	got, err := evidence.NewSourceRevisionObservation(evidence.SourceRevisionObservationInput{
		Provider:              " drive ",
		ProviderDocumentID:    " document-opaque ",
		DocumentDigestVersion: " document-digest-v1 ",
		DocumentDigest:        documentDigest,
		ProviderVersion:       " provider-version-opaque ",
		ProviderRevision:      "",
		FirstRecordedAt:       recordedAt,
	})
	if err != nil {
		t.Fatalf("NewSourceRevisionObservation() error = %v", err)
	}

	wantRecordedAt := time.Date(2026, time.July, 21, 12, 30, 0, 123456000, time.UTC)
	if got.Provider() != "drive" ||
		got.ProviderDocumentID() != "document-opaque" ||
		got.DocumentDigestVersion() != "document-digest-v1" ||
		got.DocumentDigest() != documentDigest ||
		got.ProviderVersion() != "provider-version-opaque" ||
		got.ProviderRevision() != "" ||
		got.FirstRecordedAt() != wantRecordedAt {
		t.Fatalf("source revision accessors did not preserve exact canonical fields")
	}
}
