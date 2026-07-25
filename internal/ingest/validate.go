package ingest

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/JakeFAU/stacks/core/observation"

	"stacks/internal/entity"
	"stacks/internal/extract"
)

// ErrPersistenceCollision reports that distinct model records would collapse
// onto one durable identity. It deliberately contains no model/source value.
var ErrPersistenceCollision = errors.New("extraction output has a duplicate durable identity")

// ErrPersistenceReference reports a missing model-local reference without
// including the untrusted identifier or private value.
var ErrPersistenceReference = errors.New("extraction output has an invalid durable reference")

// ValidateForPersistence rejects schema-valid output that would collide with
// PostgreSQL identities after model-local IDs are translated into durable
// evidence, mention/proposal, observation, or signal records.
func ValidateForPersistence(completion Completion) error {
	if !completion.DataMode.ValidForNewRun() {
		return ErrPersistenceReference
	}
	evidenceIdentities, err := validateEvidenceIdentities(completion.Evidence)
	if err != nil {
		return err
	}
	evidenceQuotes := make(map[string]string, len(completion.Evidence))
	for _, record := range completion.Evidence {
		evidenceQuotes[record.Key] = record.Span.Text()
	}
	mentionKeys, err := validateMentionIdentities(completion.Mentions, evidenceIdentities, evidenceQuotes)
	if err != nil {
		return err
	}
	observationIDs, err := validateObservationReferences(completion.Observations, evidenceIdentities, mentionKeys)
	if err != nil {
		return err
	}
	return validateSignalIdentities(completion.Signals, observationIDs, evidenceIdentities)
}

func validateEvidenceIdentities(records []EvidenceRecord) (map[string][sha256.Size]byte, error) {
	byKey := make(map[string][sha256.Size]byte, len(records))
	seenDurable := make(map[[sha256.Size]byte]string, len(records))
	for _, record := range records {
		if !canonicalLocalIdentifier(record.Key) {
			return nil, ErrPersistenceReference
		}
		identity := digestIdentity(struct {
			Provider       string
			DocumentID     string
			DocumentDigest string
			TabID          string
			StartOffset    int
			EndOffset      int
		}{
			Provider: record.Span.Provider(), DocumentID: record.Span.ProviderDocumentID(),
			DocumentDigest: record.Span.DocumentDigest().String(), TabID: record.Span.SectionID(),
			StartOffset: record.Span.StartOffset(), EndOffset: record.Span.EndOffset(),
		})
		if _, exists := byKey[record.Key]; exists {
			return nil, ErrPersistenceCollision
		}
		if priorKey, exists := seenDurable[identity]; exists && priorKey != record.Key {
			return nil, ErrPersistenceCollision
		}
		byKey[record.Key] = identity
		seenDurable[identity] = record.Key
	}
	return byKey, nil
}

func validateMentionIdentities(records []MentionRecord, evidence map[string][sha256.Size]byte, evidenceQuotes map[string]string) (map[string]struct{}, error) {
	seenKeys := make(map[string]struct{}, len(records))
	seenDurable := make(map[[sha256.Size]byte]struct{}, len(records))
	for _, record := range records {
		evidenceIdentity, exists := evidence[record.EvidenceKey]
		if !canonicalLocalIdentifier(record.Key) || !canonicalLocalIdentifier(record.EvidenceKey) || !exists {
			return nil, ErrPersistenceReference
		}
		if record.NormalizedName == "" {
			if record.ProposedEmail != "" || record.ProposedEmailEvidenceKey != "" || record.Resolution.AutoResolved || record.Resolution.EntityID != "" {
				return nil, ErrPersistenceReference
			}
		} else if record.NormalizedName != entity.NormalizeName(record.Surface) {
			return nil, ErrPersistenceReference
		}
		if record.ProposedEmail == "" && record.ProposedEmailEvidenceKey != "" {
			return nil, ErrPersistenceReference
		}
		if record.ProposedEmail != "" && (record.ProposedEmail != entity.NormalizeEmail(record.ProposedEmail) ||
			!entity.ValidEmail(record.ProposedEmail) || !canonicalLocalIdentifier(record.ProposedEmailEvidenceKey)) {
			return nil, ErrPersistenceReference
		}
		identityCitations := []extract.Citation{{ID: record.EvidenceKey, Quote: evidenceQuotes[record.EvidenceKey]}}
		identityCitationIDs := []string{record.EvidenceKey}
		if record.ProposedEmail != "" {
			if _, exists := evidence[record.ProposedEmailEvidenceKey]; !exists {
				return nil, ErrPersistenceReference
			}
			if record.ProposedEmailEvidenceKey != record.EvidenceKey {
				identityCitations = append(identityCitations, extract.Citation{
					ID: record.ProposedEmailEvidenceKey, Quote: evidenceQuotes[record.ProposedEmailEvidenceKey],
				})
				identityCitationIDs = append(identityCitationIDs, record.ProposedEmailEvidenceKey)
			}
		}
		groundedIdentity, err := extract.GroundPersonIdentity(extract.PersonMention{
			Surface: record.Surface, Email: record.ProposedEmail, CitationIDs: identityCitationIDs,
		}, identityCitations)
		if err != nil || (record.NormalizedName != "" &&
			(groundedIdentity.NameEvidenceCitationID != record.EvidenceKey || groundedIdentity.NormalizedName != record.NormalizedName ||
				groundedIdentity.EmailEvidenceCitationID != record.ProposedEmailEvidenceKey || groundedIdentity.ProposedEmail != record.ProposedEmail)) {
			return nil, ErrPersistenceReference
		}
		if _, exists := seenKeys[record.Key]; exists {
			return nil, ErrPersistenceCollision
		}
		seenKeys[record.Key] = struct{}{}
		identity := digestIdentity(struct {
			Evidence [sha256.Size]byte
			Surface  string
			Role     string
		}{Evidence: evidenceIdentity, Surface: record.Surface, Role: record.Role})
		if _, exists := seenDurable[identity]; exists {
			return nil, ErrPersistenceCollision
		}
		seenDurable[identity] = struct{}{}
	}
	return seenKeys, nil
}

func validateObservationReferences(records []ObservationDraft, evidence map[string][sha256.Size]byte, mentions map[string]struct{}) (map[string]struct{}, error) {
	byID := make(map[string]struct{}, len(records))
	for _, record := range records {
		identifier := string(record.ID)
		if !canonicalLocalIdentifier(identifier) || record.RecordedAt.IsZero() || !validSourceConfidence(record.SourceConfidence) {
			return nil, ErrPersistenceReference
		}
		if _, exists := byID[identifier]; exists {
			return nil, ErrPersistenceCollision
		}
		for _, mentionKey := range []string{record.SubjectMentionKey, record.ObjectMentionKey} {
			if mentionKey == "" {
				continue
			}
			if _, exists := mentions[mentionKey]; !canonicalLocalIdentifier(mentionKey) || !exists {
				return nil, ErrPersistenceReference
			}
		}
		if _, err := canonicalEvidenceSet(record.EvidenceKeys, evidence); err != nil {
			return nil, err
		}
		byID[identifier] = struct{}{}
	}
	return byID, nil
}

func validateSignalIdentities(records []SignalRecord, observations map[string]struct{}, evidence map[string][sha256.Size]byte) error {
	seenIDs := make(map[string]struct{}, len(records))
	seenObservations := make(map[string]struct{}, len(records))
	for _, record := range records {
		_, exists := observations[record.ObservationID]
		if !canonicalLocalIdentifier(record.ID) || !canonicalLocalIdentifier(record.ObservationID) || !exists {
			return ErrPersistenceReference
		}
		if _, exists := seenIDs[record.ID]; exists {
			return ErrPersistenceCollision
		}
		if _, exists := seenObservations[record.ObservationID]; exists {
			return ErrPersistenceCollision
		}
		seenIDs[record.ID] = struct{}{}
		seenObservations[record.ObservationID] = struct{}{}
		if _, err := canonicalSignalEvidenceSet(record.Evidence, evidence); err != nil {
			return err
		}
	}
	return nil
}

func validSourceConfidence(value observation.Confidence) bool {
	score := value.Value()
	return value.Scale() == observation.ConfidenceUnitInterval &&
		!math.IsNaN(score) && !math.IsInf(score, 0) &&
		score >= 0 && score <= 1
}

type signalEvidenceIdentity struct {
	Evidence [sha256.Size]byte
	Role     string
}

func canonicalEvidenceSet(keys []string, evidence map[string][sha256.Size]byte) ([][sha256.Size]byte, error) {
	set := make(map[[sha256.Size]byte]struct{}, len(keys))
	for _, key := range keys {
		identity, exists := evidence[key]
		if !canonicalLocalIdentifier(key) || !exists {
			return nil, ErrPersistenceReference
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

func canonicalSignalEvidenceSet(records []SignalEvidenceRecord, evidence map[string][sha256.Size]byte) ([]signalEvidenceIdentity, error) {
	set := make(map[signalEvidenceIdentity]struct{}, len(records))
	for _, record := range records {
		identity, exists := evidence[record.EvidenceKey]
		if !canonicalLocalIdentifier(record.EvidenceKey) || !exists {
			return nil, ErrPersistenceReference
		}
		pair := signalEvidenceIdentity{Evidence: identity, Role: record.Role}
		if _, exists := set[pair]; exists {
			return nil, ErrPersistenceCollision
		}
		set[pair] = struct{}{}
	}
	canonical := make([]signalEvidenceIdentity, 0, len(set))
	for identity := range set {
		canonical = append(canonical, identity)
	}
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].Evidence == canonical[right].Evidence {
			return canonical[left].Role < canonical[right].Role
		}
		return string(canonical[left].Evidence[:]) < string(canonical[right].Evidence[:])
	})
	return canonical, nil
}

func canonicalLocalIdentifier(identifier string) bool {
	return identifier != "" && strings.TrimSpace(identifier) == identifier
}

func digestIdentity(value any) [sha256.Size]byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic("ingestion identity contains an unsupported value")
	}
	return sha256.Sum256(encoded)
}
