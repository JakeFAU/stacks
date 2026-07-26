package ingest

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
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

// ValidateForPersistence rejects output whose local references cannot be
// resolved to one complete canonical write set.
func ValidateForPersistence(completion Completion) error {
	if !canonicalLocalIdentifier(completion.VersionID) ||
		!canonicalLocalIdentifier(completion.RunID) ||
		!canonicalLocalIdentifier(completion.AttemptID) ||
		!canonicalLocalIdentifier(completion.LeaseOwner) ||
		completion.CompletedAt.IsZero() {
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
	if err := validateCanonicalGraphIdentities(completion); err != nil {
		return err
	}
	return validateObservationReferences(completion.Observations, evidenceIdentities, mentionKeys)
}

func validateCanonicalGraphIdentities(completion Completion) error {
	if err := validateUniqueCanonicalIDs(
		len(completion.Proposals),
		func(index int) string {
			return string(completion.Proposals[index].ID())
		},
	); err != nil {
		return err
	}
	if err := validateUniqueCanonicalIDs(
		len(completion.Candidates),
		func(index int) string {
			return string(completion.Candidates[index].ID())
		},
	); err != nil {
		return err
	}
	decisionIDs := make(map[string]struct{}, len(completion.Decisions))
	for _, decision := range completion.Decisions {
		id := string(decision.ID())
		if !canonicalLocalIdentifier(id) {
			return ErrPersistenceReference
		}
		if _, exists := decisionIDs[id]; exists {
			return ErrPersistenceCollision
		}
		decisionIDs[id] = struct{}{}
	}
	aliasIDs := make(map[string]struct{}, len(completion.AliasAssertions))
	for _, assertion := range completion.AliasAssertions {
		id := string(assertion.ID())
		decisionID := string(assertion.DecisionID())
		if !canonicalLocalIdentifier(id) ||
			!canonicalLocalIdentifier(decisionID) {
			return ErrPersistenceReference
		}
		if _, exists := decisionIDs[decisionID]; !exists {
			return ErrPersistenceReference
		}
		if _, exists := aliasIDs[id]; exists {
			return ErrPersistenceCollision
		}
		aliasIDs[id] = struct{}{}
	}
	return validateUniqueCanonicalIDs(
		len(completion.AdmissionDecisions),
		func(index int) string {
			return completion.AdmissionDecisions[index].ID()
		},
	)
}

func validateUniqueCanonicalIDs(count int, identifier func(int) string) error {
	seen := make(map[string]struct{}, count)
	for index := range count {
		id := identifier(index)
		if !canonicalLocalIdentifier(id) {
			return ErrPersistenceReference
		}
		if _, exists := seen[id]; exists {
			return ErrPersistenceCollision
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validateEvidenceIdentities(records []EvidenceRecord) (map[string][sha256.Size]byte, error) {
	byKey := make(map[string][sha256.Size]byte, len(records))
	seenDurable := make(map[[sha256.Size]byte]string, len(records))
	for _, record := range records {
		if !canonicalLocalIdentifier(record.Key) {
			return nil, ErrPersistenceReference
		}
		identity := sha256.Sum256([]byte(record.Span.ID()))
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

func validateMentionIdentities(
	records []MentionRecord,
	evidence map[string][sha256.Size]byte,
	evidenceQuotes map[string]string,
) (map[string]struct{}, error) {
	seenKeys := make(map[string]struct{}, len(records))
	seenDurable := make(map[[sha256.Size]byte]struct{}, len(records))
	for _, record := range records {
		evidenceIdentity, exists := evidence[record.EvidenceKey]
		if !canonicalLocalIdentifier(record.Key) || !canonicalLocalIdentifier(record.EvidenceKey) || !exists {
			return nil, ErrPersistenceReference
		}
		if record.NormalizedName == "" {
			if record.ProposedEmail != "" || record.ProposedEmailEvidenceKey != "" ||
				record.Resolution.AutoResolved || record.Resolution.EntityID != "" {
				return nil, ErrPersistenceReference
			}
		} else if record.NormalizedName != entity.NormalizeName(record.Surface) {
			return nil, ErrPersistenceReference
		}
		if record.ProposedEmail == "" && record.ProposedEmailEvidenceKey != "" {
			return nil, ErrPersistenceReference
		}
		if record.ProposedEmail != "" &&
			(record.ProposedEmail != entity.NormalizeEmail(record.ProposedEmail) ||
				!entity.ValidEmail(record.ProposedEmail) ||
				!canonicalLocalIdentifier(record.ProposedEmailEvidenceKey)) {
			return nil, ErrPersistenceReference
		}
		identityCitations := []extract.Citation{{
			ID: record.EvidenceKey, Quote: evidenceQuotes[record.EvidenceKey],
		}}
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
			(groundedIdentity.NameEvidenceCitationID != record.EvidenceKey ||
				groundedIdentity.NormalizedName != record.NormalizedName ||
				groundedIdentity.EmailEvidenceCitationID != record.ProposedEmailEvidenceKey ||
				groundedIdentity.ProposedEmail != record.ProposedEmail)) {
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

func validateObservationReferences(
	records []CanonicalObservationDraft,
	evidence map[string][sha256.Size]byte,
	mentions map[string]struct{},
) error {
	seenIDs := make(map[observation.ObservationID]struct{}, len(records))
	for _, record := range records {
		if !canonicalLocalIdentifier(string(record.ID)) || record.RecordedAt.IsZero() ||
			record.Derivation.LegacyUnversioned {
			return ErrPersistenceReference
		}
		if _, exists := seenIDs[record.ID]; exists {
			return ErrPersistenceCollision
		}
		seenIDs[record.ID] = struct{}{}
		if err := validateDraftTerm(record.Subject, mentions); err != nil {
			return err
		}
		if err := validateDraftTerm(record.Object, mentions); err != nil {
			return err
		}
		if _, err := observation.NewPredicate(string(record.Predicate)); err != nil {
			return ErrPersistenceReference
		}
		if len(record.Evidence) == 0 {
			return ErrPersistenceReference
		}
		seenLinks := make(map[DraftEvidenceLink]struct{}, len(record.Evidence))
		for _, link := range record.Evidence {
			if !canonicalLocalIdentifier(link.EvidenceKey) {
				return ErrPersistenceReference
			}
			if _, exists := evidence[link.EvidenceKey]; !exists {
				return ErrPersistenceReference
			}
			if link.Role != observation.EvidenceSupporting &&
				link.Role != observation.EvidenceContradicting {
				return ErrPersistenceReference
			}
			if _, exists := seenLinks[link]; exists {
				return ErrPersistenceCollision
			}
			seenLinks[link] = struct{}{}
		}
		if record.Confidence != nil &&
			record.Confidence.Scale() != observation.ConfidenceUnitInterval {
			return ErrPersistenceReference
		}
	}
	return nil
}

func validateDraftTerm(term DraftTerm, mentions map[string]struct{}) error {
	switch term.Kind {
	case observation.TermAbsent:
		if term.Text != "" || term.MentionKey != "" || term.EntityID != "" || term.GroundingMentionKey != "" {
			return ErrPersistenceReference
		}
	case observation.TermText:
		if strings.TrimSpace(term.Text) == "" || term.MentionKey != "" ||
			term.EntityID != "" || term.GroundingMentionKey != "" {
			return ErrPersistenceReference
		}
	case observation.TermMention:
		if !canonicalLocalIdentifier(term.MentionKey) || term.Text != "" ||
			term.EntityID != "" || term.GroundingMentionKey != "" {
			return ErrPersistenceReference
		}
		if _, exists := mentions[term.MentionKey]; !exists {
			return ErrPersistenceReference
		}
	case observation.TermEntity:
		if !canonicalLocalIdentifier(term.EntityID) || term.Text != "" || term.MentionKey != "" {
			return ErrPersistenceReference
		}
		if term.GroundingMentionKey != "" {
			if !canonicalLocalIdentifier(term.GroundingMentionKey) {
				return ErrPersistenceReference
			}
			if _, exists := mentions[term.GroundingMentionKey]; !exists {
				return ErrPersistenceReference
			}
		}
	default:
		return ErrPersistenceReference
	}
	return nil
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
