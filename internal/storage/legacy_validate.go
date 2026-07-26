package storage

import (
	"crypto/sha256"
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/JakeFAU/stacks/core/observation"

	"stacks/internal/entity"
	"stacks/internal/extract"
	"stacks/internal/ingest"
)

func validateLegacyForPersistence(completion legacyIngestionCompletion) error {
	if !completion.DataMode.ValidForNewRun() {
		return ingest.ErrPersistenceReference
	}
	evidenceIdentities, err := validateLegacyEvidenceIdentities(completion.Evidence)
	if err != nil {
		return err
	}
	evidenceQuotes := make(map[string]string, len(completion.Evidence))
	for _, record := range completion.Evidence {
		evidenceQuotes[record.Key] = record.Span.Text()
	}
	mentionKeys, err := validateLegacyMentionIdentities(
		completion.Mentions,
		evidenceIdentities,
		evidenceQuotes,
	)
	if err != nil {
		return err
	}
	observationIDs, err := validateLegacyObservationReferences(
		completion.Observations,
		evidenceIdentities,
		mentionKeys,
	)
	if err != nil {
		return err
	}
	return validateLegacySignalIdentities(
		completion.Signals,
		observationIDs,
		evidenceIdentities,
	)
}

func validateLegacyEvidenceIdentities(
	records []ingest.EvidenceRecord,
) (map[string][sha256.Size]byte, error) {
	byKey := make(map[string][sha256.Size]byte, len(records))
	seenDurable := make(map[[sha256.Size]byte]string, len(records))
	for _, record := range records {
		if !legacyLocalIdentifier(record.Key) {
			return nil, ingest.ErrPersistenceReference
		}
		identity := sha256.Sum256([]byte(record.Span.ID()))
		if _, exists := byKey[record.Key]; exists {
			return nil, ingest.ErrPersistenceCollision
		}
		if priorKey, exists := seenDurable[identity]; exists && priorKey != record.Key {
			return nil, ingest.ErrPersistenceCollision
		}
		byKey[record.Key] = identity
		seenDurable[identity] = record.Key
	}
	return byKey, nil
}

func validateLegacyMentionIdentities(
	records []ingest.MentionRecord,
	evidenceIdentities map[string][sha256.Size]byte,
	evidenceQuotes map[string]string,
) (map[string]struct{}, error) {
	seenKeys := make(map[string]struct{}, len(records))
	seenDurable := make(map[[sha256.Size]byte]struct{}, len(records))
	for _, record := range records {
		evidenceIdentity, exists := evidenceIdentities[record.EvidenceKey]
		if !legacyLocalIdentifier(record.Key) ||
			!legacyLocalIdentifier(record.EvidenceKey) ||
			!exists {
			return nil, ingest.ErrPersistenceReference
		}
		if record.NormalizedName == "" {
			if record.ProposedEmail != "" ||
				record.ProposedEmailEvidenceKey != "" ||
				record.Resolution.AutoResolved ||
				record.Resolution.EntityID != "" {
				return nil, ingest.ErrPersistenceReference
			}
		} else if record.NormalizedName != entity.NormalizeName(record.Surface) {
			return nil, ingest.ErrPersistenceReference
		}
		if record.ProposedEmail == "" && record.ProposedEmailEvidenceKey != "" {
			return nil, ingest.ErrPersistenceReference
		}
		if record.ProposedEmail != "" &&
			(record.ProposedEmail != entity.NormalizeEmail(record.ProposedEmail) ||
				!entity.ValidEmail(record.ProposedEmail) ||
				!legacyLocalIdentifier(record.ProposedEmailEvidenceKey)) {
			return nil, ingest.ErrPersistenceReference
		}
		identityCitations := []extract.Citation{{
			ID: record.EvidenceKey, Quote: evidenceQuotes[record.EvidenceKey],
		}}
		identityCitationIDs := []string{record.EvidenceKey}
		if record.ProposedEmail != "" {
			if _, exists := evidenceIdentities[record.ProposedEmailEvidenceKey]; !exists {
				return nil, ingest.ErrPersistenceReference
			}
			if record.ProposedEmailEvidenceKey != record.EvidenceKey {
				identityCitations = append(identityCitations, extract.Citation{
					ID:    record.ProposedEmailEvidenceKey,
					Quote: evidenceQuotes[record.ProposedEmailEvidenceKey],
				})
				identityCitationIDs = append(
					identityCitationIDs,
					record.ProposedEmailEvidenceKey,
				)
			}
		}
		groundedIdentity, err := extract.GroundPersonIdentity(
			extract.PersonMention{
				Surface:     record.Surface,
				Email:       record.ProposedEmail,
				CitationIDs: identityCitationIDs,
			},
			identityCitations,
		)
		if err != nil ||
			(record.NormalizedName != "" &&
				(groundedIdentity.NameEvidenceCitationID != record.EvidenceKey ||
					groundedIdentity.NormalizedName != record.NormalizedName ||
					groundedIdentity.EmailEvidenceCitationID !=
						record.ProposedEmailEvidenceKey ||
					groundedIdentity.ProposedEmail != record.ProposedEmail)) {
			return nil, ingest.ErrPersistenceReference
		}
		if _, exists := seenKeys[record.Key]; exists {
			return nil, ingest.ErrPersistenceCollision
		}
		seenKeys[record.Key] = struct{}{}
		identity := legacyDigestIdentity(struct {
			Evidence [sha256.Size]byte
			Surface  string
			Role     string
		}{
			Evidence: evidenceIdentity,
			Surface:  record.Surface,
			Role:     record.Role,
		})
		if _, exists := seenDurable[identity]; exists {
			return nil, ingest.ErrPersistenceCollision
		}
		seenDurable[identity] = struct{}{}
	}
	return seenKeys, nil
}

func validateLegacyObservationReferences(
	records []legacyObservationDraft,
	evidenceIdentities map[string][sha256.Size]byte,
	mentions map[string]struct{},
) (map[string]struct{}, error) {
	byID := make(map[string]struct{}, len(records))
	for _, record := range records {
		identifier := string(record.ID)
		if !legacyLocalIdentifier(identifier) ||
			record.RecordedAt.IsZero() ||
			!validLegacySourceConfidence(record.SourceConfidence) {
			return nil, ingest.ErrPersistenceReference
		}
		if _, exists := byID[identifier]; exists {
			return nil, ingest.ErrPersistenceCollision
		}
		for _, mentionKey := range []string{
			record.SubjectMentionKey,
			record.ObjectMentionKey,
		} {
			if mentionKey == "" {
				continue
			}
			if _, exists := mentions[mentionKey]; !legacyLocalIdentifier(mentionKey) ||
				!exists {
				return nil, ingest.ErrPersistenceReference
			}
		}
		if _, err := canonicalLegacyEvidenceSet(
			record.EvidenceKeys,
			evidenceIdentities,
		); err != nil {
			return nil, err
		}
		byID[identifier] = struct{}{}
	}
	return byID, nil
}

func validateLegacySignalIdentities(
	records []legacySignalRecord,
	observations map[string]struct{},
	evidenceIdentities map[string][sha256.Size]byte,
) error {
	seenIDs := make(map[string]struct{}, len(records))
	seenObservations := make(map[string]struct{}, len(records))
	for _, record := range records {
		_, exists := observations[record.ObservationID]
		if !legacyLocalIdentifier(record.ID) ||
			!legacyLocalIdentifier(record.ObservationID) ||
			!exists {
			return ingest.ErrPersistenceReference
		}
		if _, exists := seenIDs[record.ID]; exists {
			return ingest.ErrPersistenceCollision
		}
		if _, exists := seenObservations[record.ObservationID]; exists {
			return ingest.ErrPersistenceCollision
		}
		seenIDs[record.ID] = struct{}{}
		seenObservations[record.ObservationID] = struct{}{}
		if _, err := canonicalLegacySignalEvidenceSet(
			record.Evidence,
			evidenceIdentities,
		); err != nil {
			return err
		}
	}
	return nil
}

func validLegacySourceConfidence(value observation.Confidence) bool {
	score := value.Value()
	return value.Scale() == observation.ConfidenceUnitInterval &&
		!math.IsNaN(score) &&
		!math.IsInf(score, 0) &&
		score >= 0 &&
		score <= 1
}

type legacySignalEvidenceIdentity struct {
	Evidence [sha256.Size]byte
	Role     string
}

func canonicalLegacyEvidenceSet(
	keys []string,
	evidenceIdentities map[string][sha256.Size]byte,
) ([][sha256.Size]byte, error) {
	set := make(map[[sha256.Size]byte]struct{}, len(keys))
	for _, key := range keys {
		identity, exists := evidenceIdentities[key]
		if !legacyLocalIdentifier(key) || !exists {
			return nil, ingest.ErrPersistenceReference
		}
		set[identity] = struct{}{}
	}
	canonical := make([][sha256.Size]byte, 0, len(set))
	for identity := range set {
		canonical = append(canonical, identity)
	}
	sort.Slice(canonical, func(left, right int) bool {
		return string(canonical[left][:]) < string(canonical[right][:])
	})
	return canonical, nil
}

func canonicalLegacySignalEvidenceSet(
	records []legacySignalEvidenceRecord,
	evidenceIdentities map[string][sha256.Size]byte,
) ([]legacySignalEvidenceIdentity, error) {
	set := make(map[legacySignalEvidenceIdentity]struct{}, len(records))
	for _, record := range records {
		identity, exists := evidenceIdentities[record.EvidenceKey]
		if !legacyLocalIdentifier(record.EvidenceKey) || !exists {
			return nil, ingest.ErrPersistenceReference
		}
		pair := legacySignalEvidenceIdentity{
			Evidence: identity,
			Role:     record.Role,
		}
		if _, exists := set[pair]; exists {
			return nil, ingest.ErrPersistenceCollision
		}
		set[pair] = struct{}{}
	}
	canonical := make([]legacySignalEvidenceIdentity, 0, len(set))
	for identity := range set {
		canonical = append(canonical, identity)
	}
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].Evidence == canonical[right].Evidence {
			return canonical[left].Role < canonical[right].Role
		}
		return string(canonical[left].Evidence[:]) <
			string(canonical[right].Evidence[:])
	})
	return canonical, nil
}

func legacyLocalIdentifier(identifier string) bool {
	return identifier != "" && strings.TrimSpace(identifier) == identifier
}

func legacyDigestIdentity(value any) [sha256.Size]byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic("legacy ingestion identity contains an unsupported value")
	}
	return sha256.Sum256(encoded)
}
