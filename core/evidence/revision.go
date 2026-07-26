package evidence

import (
	"fmt"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/internal/canonicalhash"
	"github.com/JakeFAU/stacks/core/timepoint"
)

const (
	DocumentDigestVersion       = "stacks.document.v3.utc-microsecond"
	SourceRevisionDigestVersion = "stacks.source-revision.v1"
	EvidenceIDVersion           = "stacks.evidence-id.v1.source-span"
	EvidenceSpanDigestVersion   = "stacks.evidence-span.v1"
)

// SourceRevisionObservationInput identifies one append-only provider revision
// observed for an immutable document content version.
type SourceRevisionObservationInput struct {
	Provider              string
	ProviderDocumentID    string
	DocumentDigestVersion string
	DocumentDigest        ContentDigest
	ProviderVersion       string
	ProviderRevision      string
	FirstRecordedAt       time.Time
}

// SourceRevisionObservation preserves source-revision provenance separately
// from the stable document content identity.
type SourceRevisionObservation struct {
	id                    string
	digest                ContentDigest
	provider              string
	providerDocumentID    string
	documentDigestVersion string
	documentDigest        ContentDigest
	providerVersion       string
	providerRevision      string
	firstRecordedAt       time.Time
}

// NewSourceRevisionObservation validates and records one source revision.
func NewSourceRevisionObservation(input SourceRevisionObservationInput) (SourceRevisionObservation, error) {
	provider, providerDocumentID := strings.TrimSpace(input.Provider), strings.TrimSpace(input.ProviderDocumentID)
	if provider == "" || providerDocumentID == "" {
		return SourceRevisionObservation{}, fmt.Errorf("source revision document identity is required")
	}
	documentDigestVersion, providerVersion := strings.TrimSpace(input.DocumentDigestVersion), strings.TrimSpace(input.ProviderVersion)
	if input.DocumentDigest == (ContentDigest{}) || documentDigestVersion == "" || providerVersion == "" {
		return SourceRevisionObservation{}, fmt.Errorf("source revision document version is required")
	}
	if input.FirstRecordedAt.IsZero() {
		return SourceRevisionObservation{}, fmt.Errorf("source revision first recorded time is required")
	}
	firstRecordedAt := timepoint.Normalize(input.FirstRecordedAt)
	providerRevision := strings.TrimSpace(input.ProviderRevision)
	idEncoder := canonicalhash.New(SourceRevisionDigestVersion)
	idEncoder.String(provider)
	idEncoder.String(providerDocumentID)
	idEncoder.String(documentDigestVersion)
	idEncoder.Bytes(input.DocumentDigest[:])
	idEncoder.String(providerVersion)
	idEncoder.String(providerRevision)
	id := contentDigest(idEncoder).String()
	digestEncoder := canonicalhash.New(SourceRevisionDigestVersion)
	digestEncoder.String(provider)
	digestEncoder.String(providerDocumentID)
	digestEncoder.String(documentDigestVersion)
	digestEncoder.Bytes(input.DocumentDigest[:])
	digestEncoder.String(providerVersion)
	digestEncoder.String(providerRevision)
	digestEncoder.Time(firstRecordedAt)
	return SourceRevisionObservation{provider: provider, providerDocumentID: providerDocumentID, documentDigestVersion: documentDigestVersion, documentDigest: input.DocumentDigest, providerVersion: providerVersion, providerRevision: providerRevision, firstRecordedAt: firstRecordedAt, id: id, digest: contentDigest(digestEncoder)}, nil
}

// FirstRecordedAt returns when Stacks first durably recorded this content version.
func (value SourceRevisionObservation) FirstRecordedAt() time.Time { return value.firstRecordedAt }

// ID returns the source-revision identity without recorded-time provenance.
func (value SourceRevisionObservation) ID() string { return value.id }

// Digest returns the complete append-only source-revision provenance digest.
func (value SourceRevisionObservation) Digest() ContentDigest { return value.digest }

// DigestVersion returns the encoding version for Digest.
func (value SourceRevisionObservation) DigestVersion() string { return SourceRevisionDigestVersion }
