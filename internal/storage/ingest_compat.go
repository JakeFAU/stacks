package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"

	"stacks/internal/ingest"
	"stacks/internal/modelpolicy"
)

// ErrCanonicalIngestionRequiresPostgresRepository is the compile-only cut
// boundary for legacy runtime composition. Task 11 replaces that composition;
// canonical writes must never be translated back into the retired schema.
var ErrCanonicalIngestionRequiresPostgresRepository = errors.New(
	"canonical ingestion requires the canonical PostgreSQL repository",
)

type legacyObservationDraft struct {
	ID                observation.ObservationID
	SubjectEntityID   string
	ObjectEntityID    string
	SubjectMentionKey string
	ObjectMentionKey  string
	Predicate         observation.Predicate
	ValidTime         observation.TemporalExtent
	RecordedAt        time.Time
	EvidenceKeys      []string
	SourceConfidence  observation.Confidence
}

type legacySignalEvidenceRecord struct {
	EvidenceKey string
	Role        string
}

type legacySignalRecord struct {
	ID                string
	ObservationID     string
	Category          string
	Direction         string
	ExtractionModelID string
	PromptVersion     string
	Rationale         string
	Confidence        float64
	Evidence          []legacySignalEvidenceRecord
}

type legacyIngestionCompletion struct {
	VersionID    string
	DerivationID string
	LeaseOwner   string
	DataMode     modelpolicy.DataMode
	Evidence     []ingest.EvidenceRecord
	Mentions     []ingest.MentionRecord
	Observations []legacyObservationDraft
	Signals      []legacySignalRecord
}

type legacyVersionState struct {
	ID                 string
	DerivationID       string
	DerivationDigest   [sha256.Size]byte
	DocumentRecordedAt time.Time
	RecordedAt         time.Time
	LeaseOwner         string
	LeaseExpiresAt     time.Time
	Status             ingest.VersionStatus
	RetryCount         int
	FailureCode        ingest.FailureCode
}

func validateLegacyForPersistence(completion legacyIngestionCompletion) error {
	if !completion.DataMode.ValidForNewRun() {
		return ingest.ErrPersistenceReference
	}
	for _, record := range completion.Evidence {
		if !legacyLocalIdentifier(record.Key) {
			return ingest.ErrPersistenceReference
		}
	}
	for _, record := range completion.Mentions {
		if !legacyLocalIdentifier(record.Key) || !legacyLocalIdentifier(record.EvidenceKey) {
			return ingest.ErrPersistenceReference
		}
	}
	for _, record := range completion.Observations {
		if !legacyLocalIdentifier(string(record.ID)) {
			return ingest.ErrPersistenceReference
		}
	}
	for _, record := range completion.Signals {
		if !legacyLocalIdentifier(record.ID) || !legacyLocalIdentifier(record.ObservationID) {
			return ingest.ErrPersistenceReference
		}
	}
	return nil
}

func legacyLocalIdentifier(identifier string) bool {
	return identifier != "" && strings.TrimSpace(identifier) == identifier
}

func (*IngestionRepository) PrepareVersion(
	context.Context,
	evidence.DocumentVersion,
	ingest.SourceRevisionMetadata,
	ingest.DerivationIdentity,
	modelpolicy.DataMode,
	time.Duration,
) (ingest.VersionState, error) {
	return ingest.VersionState{}, ErrCanonicalIngestionRequiresPostgresRepository
}

func (*IngestionRepository) CompleteVersion(
	context.Context,
	ingest.Completion,
) error {
	return ErrCanonicalIngestionRequiresPostgresRepository
}

func (*IngestionRepository) RecordFailure(
	context.Context,
	ingest.Failure,
) error {
	return ErrCanonicalIngestionRequiresPostgresRepository
}

var _ ingest.Repository = (*IngestionRepository)(nil)
