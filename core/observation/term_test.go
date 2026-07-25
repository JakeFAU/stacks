package observation_test

import (
	"testing"

	"github.com/JakeFAU/stacks/core/observation"
)

func TestEntityTermPreservesGroundingMention(t *testing.T) {
	term, err := observation.NewEntityTerm("entity-1", "mention-1")
	if err != nil {
		t.Fatalf("NewEntityTerm() error = %v", err)
	}

	entityID, groundingMentionID, ok := term.Entity()
	if !ok || entityID != "entity-1" || groundingMentionID != "mention-1" {
		t.Errorf("Entity() = (%q, %q, %v), want (%q, %q, true)", entityID, groundingMentionID, ok, "entity-1", "mention-1")
	}
}

func TestTermSupportsAllLegacyReferenceShapes(t *testing.T) {
	text, err := observation.NewTextTerm("text value")
	if err != nil {
		t.Fatalf("NewTextTerm() error = %v", err)
	}
	mention, err := observation.NewMentionTerm("mention-1")
	if err != nil {
		t.Fatalf("NewMentionTerm() error = %v", err)
	}
	entity, err := observation.NewEntityTerm("entity-1", "")
	if err != nil {
		t.Fatalf("NewEntityTerm() error = %v", err)
	}
	entityWithMention, err := observation.NewEntityTerm("entity-1", "mention-1")
	if err != nil {
		t.Fatalf("NewEntityTerm() error = %v", err)
	}

	tests := []struct {
		name string
		term observation.Term
		kind observation.TermKind
	}{
		{name: "absent", term: observation.AbsentTerm(), kind: observation.TermAbsent},
		{name: "text", term: text, kind: observation.TermText},
		{name: "mention", term: mention, kind: observation.TermMention},
		{name: "entity", term: entity, kind: observation.TermEntity},
		{name: "entity with grounding mention", term: entityWithMention, kind: observation.TermEntity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.term.Kind(); got != test.kind {
				t.Errorf("Kind() = %v, want %v", got, test.kind)
			}
		})
	}
}

func TestTermRejectsBlankTextMentionAndEntity(t *testing.T) {
	if _, err := observation.NewTextTerm(" \t"); err == nil {
		t.Fatal("NewTextTerm() error = nil, want blank text error")
	}
	if _, err := observation.NewMentionTerm("\n"); err == nil {
		t.Fatal("NewMentionTerm() error = nil, want blank mention error")
	}
	if _, err := observation.NewEntityTerm(" ", "mention-1"); err == nil {
		t.Fatal("NewEntityTerm() error = nil, want blank entity error")
	}
}

func TestTextTermPreservesNonblankCallerTextExactly(t *testing.T) {
	const input = " \t exact source text \n"
	term, err := observation.NewTextTerm(input)
	if err != nil {
		t.Fatalf("NewTextTerm() error = %v", err)
	}
	got, ok := term.Text()
	if !ok || got != input {
		t.Errorf("Text() = (%q, %v), want exact input (%q, true)", got, ok, input)
	}
}

func TestTermAccessorsDoNotConfuseKinds(t *testing.T) {
	text, err := observation.NewTextTerm("value")
	if err != nil {
		t.Fatalf("NewTextTerm() error = %v", err)
	}
	if _, ok := text.MentionID(); ok {
		t.Fatal("MentionID() ok = true for text term")
	}
	if _, _, ok := text.Entity(); ok {
		t.Fatal("Entity() ok = true for text term")
	}

	mention, err := observation.NewMentionTerm("mention-1")
	if err != nil {
		t.Fatalf("NewMentionTerm() error = %v", err)
	}
	if _, ok := mention.Text(); ok {
		t.Fatal("Text() ok = true for mention term")
	}

	entity, err := observation.NewEntityTerm("entity-1", "")
	if err != nil {
		t.Fatalf("NewEntityTerm() error = %v", err)
	}
	if _, ok := entity.Text(); ok {
		t.Fatal("Text() ok = true for entity term")
	}
	if _, ok := entity.MentionID(); ok {
		t.Fatal("MentionID() ok = true for entity term")
	}

	absent := observation.AbsentTerm()
	if _, ok := absent.Text(); ok {
		t.Fatal("Text() ok = true for absent term")
	}
	if _, ok := absent.MentionID(); ok {
		t.Fatal("MentionID() ok = true for absent term")
	}
	if _, _, ok := absent.Entity(); ok {
		t.Fatal("Entity() ok = true for absent term")
	}
}
