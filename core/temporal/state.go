package temporal

import (
	"fmt"
	"strings"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
)

// StateKey identifies the subject and predicate of one canonical state.
type StateKey struct {
	Subject   observation.Term
	Predicate observation.Predicate
}

// StateCandidate is an entity-resolved projection of an observation.
// Grounding mentions retain the source mention used to resolve a canonical
// entity without becoming part of the canonical state identity.
type StateCandidate struct {
	Key                       StateKey
	Value                     observation.Term
	Observation               observation.Observation
	SubjectGroundingMentionID string
	ObjectGroundingMentionID  string
}

// Fact is an aggregated state value with role-separated provenance.
type Fact struct {
	Key                      StateKey
	Value                    observation.Term
	ObservationIDs           []observation.ObservationID
	SupportingEvidenceIDs    []evidence.EvidenceID
	ContradictingEvidenceIDs []evidence.EvidenceID
}

type termIdentity struct {
	kind    observation.TermKind
	payload string
}

type stateKeyIdentity struct {
	subject   termIdentity
	predicate observation.Predicate
}

// NewStateKey validates a canonical, ungrounded state key.
func NewStateKey(subject observation.Term, predicate observation.Predicate) (StateKey, error) {
	key := StateKey{Subject: subject, Predicate: predicate}
	if err := validateStateKey(key); err != nil {
		return StateKey{}, err
	}
	return key, nil
}

// CompareStateKeys provides a total deterministic ordering for state keys.
func CompareStateKeys(left, right StateKey) int {
	if result := CompareTerms(left.Subject, right.Subject); result != 0 {
		return result
	}
	return strings.Compare(string(left.Predicate), string(right.Predicate))
}

// CompareTerms provides a total deterministic ordering for closed term kinds.
// Entity identity deliberately excludes its optional source grounding mention.
func CompareTerms(left, right observation.Term) int {
	if left.Kind() != right.Kind() {
		if left.Kind() < right.Kind() {
			return -1
		}
		return 1
	}
	return strings.Compare(termPayload(left), termPayload(right))
}

func validateStateKey(key StateKey) error {
	if err := validateCanonicalTerm("state key subject", key.Subject); err != nil {
		return err
	}
	if _, err := observation.NewPredicate(string(key.Predicate)); err != nil {
		return fmt.Errorf("state key predicate: %w", err)
	}
	return nil
}

func validateCanonicalTerm(name string, term observation.Term) error {
	if err := validateTerm(name, term); err != nil {
		return err
	}
	if _, groundingMentionID, isEntity := term.Entity(); isEntity && groundingMentionID != "" {
		return fmt.Errorf("%s entity must not retain grounding mention", name)
	}
	return nil
}

func validateTerm(name string, term observation.Term) error {
	switch term.Kind() {
	case observation.TermAbsent:
		return nil
	case observation.TermText:
		value, _ := term.Text()
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s text is required", name)
		}
		return nil
	case observation.TermMention:
		mentionID, _ := term.MentionID()
		if strings.TrimSpace(mentionID) == "" {
			return fmt.Errorf("%s mention ID is required", name)
		}
		return nil
	case observation.TermEntity:
		entityID, _, _ := term.Entity()
		if strings.TrimSpace(entityID) == "" {
			return fmt.Errorf("%s entity ID is required", name)
		}
		return nil
	default:
		return fmt.Errorf("%s kind is invalid", name)
	}
}

func termPayload(term observation.Term) string {
	switch term.Kind() {
	case observation.TermText:
		value, _ := term.Text()
		return value
	case observation.TermMention:
		mentionID, _ := term.MentionID()
		return mentionID
	case observation.TermEntity:
		entityID, _, _ := term.Entity()
		return entityID
	default:
		return ""
	}
}

func identityForTerm(term observation.Term) termIdentity {
	return termIdentity{kind: term.Kind(), payload: termPayload(term)}
}

func identityForStateKey(key StateKey) stateKeyIdentity {
	return stateKeyIdentity{subject: identityForTerm(key.Subject), predicate: key.Predicate}
}

func candidateMatchesObservation(candidate StateCandidate) error {
	if err := validateStateKey(candidate.Key); err != nil {
		return err
	}
	if err := validateCanonicalTerm("state candidate value", candidate.Value); err != nil {
		return err
	}
	statement := candidate.Observation.Statement()
	if candidate.Key.Predicate != statement.Predicate {
		return fmt.Errorf("state candidate predicate does not match observation statement")
	}
	if !termMapsToState(candidate.Key.Subject, statement.Subject, candidate.SubjectGroundingMentionID) {
		return fmt.Errorf("state candidate subject does not match observation statement")
	}
	if !termMapsToState(candidate.Value, statement.Object, candidate.ObjectGroundingMentionID) {
		return fmt.Errorf("state candidate value does not match observation statement")
	}
	return nil
}

func termMapsToState(stateTerm, sourceTerm observation.Term, groundingMentionID string) bool {
	switch sourceTerm.Kind() {
	case observation.TermEntity:
		if stateTerm.Kind() != observation.TermEntity {
			return false
		}
		sourceEntityID, _, _ := sourceTerm.Entity()
		stateEntityID, _, _ := stateTerm.Entity()
		return stateEntityID == sourceEntityID
	case observation.TermMention:
		if stateTerm.Kind() == observation.TermMention {
			return CompareTerms(stateTerm, sourceTerm) == 0
		}
		if stateTerm.Kind() != observation.TermEntity {
			return false
		}
		sourceMentionID, _ := sourceTerm.MentionID()
		return groundingMentionID == sourceMentionID
	case observation.TermText, observation.TermAbsent:
		return CompareTerms(stateTerm, sourceTerm) == 0
	default:
		return false
	}
}
