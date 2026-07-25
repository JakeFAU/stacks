// Package evidence defines provider-neutral immutable source evidence.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// EvidenceID is assigned by the ingestion boundary. Retries for the same
// logical evidence must use the same identifier.
type EvidenceID string

// SourceReference identifies the source document represented by evidence.
// Version and Locator may be empty when a provider does not supply them.
type SourceReference struct {
	Provider   string
	DocumentID string
	Version    string
	Locator    string
}

// ContentDigest identifies immutable source content by its SHA-256 digest.
type ContentDigest [sha256.Size]byte

// DigestContent computes the digest used to identify source content.
func DigestContent(content []byte) ContentDigest { return sha256.Sum256(content) }

// ParseContentDigest parses a hexadecimal SHA-256 digest.
func ParseContentDigest(value string) (ContentDigest, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return ContentDigest{}, fmt.Errorf("content digest must be a hexadecimal SHA-256 value")
	}
	var digest ContentDigest
	copy(digest[:], decoded)
	return digest, nil
}

// String returns the lowercase hexadecimal digest.
func (digest ContentDigest) String() string { return hex.EncodeToString(digest[:]) }

// EvidenceInput contains the values needed to construct immutable evidence.
type EvidenceInput struct {
	ID         EvidenceID
	Source     SourceReference
	Digest     ContentDigest
	RecordedAt time.Time
}

// Evidence identifies an immutable version of source content and when Stacks
// first recorded it.
type Evidence struct {
	id         EvidenceID
	source     SourceReference
	digest     ContentDigest
	recordedAt time.Time
}

// NewEvidence validates and constructs source evidence.
func NewEvidence(input EvidenceInput) (Evidence, error) {
	id := EvidenceID(strings.TrimSpace(string(input.ID)))
	if id == "" {
		return Evidence{}, fmt.Errorf("evidence ID is required")
	}
	if strings.TrimSpace(input.Source.Provider) == "" {
		return Evidence{}, fmt.Errorf("source provider is required")
	}
	if strings.TrimSpace(input.Source.DocumentID) == "" {
		return Evidence{}, fmt.Errorf("source document ID is required")
	}
	if input.RecordedAt.IsZero() {
		return Evidence{}, fmt.Errorf("evidence recorded time is required")
	}
	if input.Digest == (ContentDigest{}) {
		return Evidence{}, fmt.Errorf("evidence content digest is required")
	}
	source := input.Source
	source.Provider, source.DocumentID = strings.TrimSpace(source.Provider), strings.TrimSpace(source.DocumentID)
	source.Version, source.Locator = strings.TrimSpace(source.Version), strings.TrimSpace(source.Locator)
	return Evidence{id: id, source: source, digest: input.Digest, recordedAt: input.RecordedAt.UTC()}, nil
}

// ID returns the stable evidence identifier.
func (value Evidence) ID() EvidenceID { return value.id }

// Source returns the evidence source reference.
func (value Evidence) Source() SourceReference { return value.source }

// Digest returns the immutable content digest.
func (value Evidence) Digest() ContentDigest { return value.digest }

// RecordedAt returns when Stacks first recorded the evidence.
func (value Evidence) RecordedAt() time.Time { return value.recordedAt }
