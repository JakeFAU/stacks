// Package observation defines immutable temporal propositions with provenance.
// Ordinary observations require evidence; explicit legacy compatibility
// observations may be uncited and remain unresolved during temporal
// aggregation.
package observation

import (
	"fmt"
	"strings"
)

// TermKind identifies the closed form carried by a Term.
type TermKind uint8

const (
	TermAbsent TermKind = iota
	TermText
	TermMention
	TermEntity
)

// Term is one side of a proposition. Its private fields prevent callers from
// constructing combinations that do not match the declared kind.
type Term struct {
	kind               TermKind
	text               string
	mentionID          string
	entityID           string
	groundingMentionID string
}

// AbsentTerm represents a deliberately absent term in a lossless legacy or
// unary observation.
func AbsentTerm() Term {
	return Term{kind: TermAbsent}
}

// NewTextTerm creates a term containing bounded source text.
func NewTextTerm(value string) (Term, error) {
	if strings.TrimSpace(value) == "" {
		return Term{}, fmt.Errorf("term text is required")
	}
	return Term{kind: TermText, text: value}, nil
}

// NewMentionTerm creates a term referring to an unresolved source mention.
func NewMentionTerm(mentionID string) (Term, error) {
	mentionID = strings.TrimSpace(mentionID)
	if mentionID == "" {
		return Term{}, fmt.Errorf("term mention ID is required")
	}
	return Term{kind: TermMention, mentionID: mentionID}, nil
}

// NewEntityTerm creates a term referring to a canonical entity. The optional
// grounding mention preserves which source mention was resolved to the entity.
func NewEntityTerm(entityID, groundingMentionID string) (Term, error) {
	entityID = strings.TrimSpace(entityID)
	if entityID == "" {
		return Term{}, fmt.Errorf("term entity ID is required")
	}
	return Term{
		kind:               TermEntity,
		entityID:           entityID,
		groundingMentionID: strings.TrimSpace(groundingMentionID),
	}, nil
}

// Kind returns the term's closed form.
func (term Term) Kind() TermKind {
	return term.kind
}

// Text returns the text value when Kind is TermText.
func (term Term) Text() (string, bool) {
	return term.text, term.kind == TermText
}

// MentionID returns the mention identifier when Kind is TermMention.
func (term Term) MentionID() (string, bool) {
	return term.mentionID, term.kind == TermMention
}

// Entity returns the canonical entity and optional grounding mention when Kind
// is TermEntity.
func (term Term) Entity() (entityID, groundingMentionID string, ok bool) {
	return term.entityID, term.groundingMentionID, term.kind == TermEntity
}
