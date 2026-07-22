package entity

import "testing"

func TestResolverAutoResolvesAcceptedExactEmail(t *testing.T) {
	resolution := Resolver{}.Resolve(Mention{Email: "riya.chen@synthetic.example"}, []EntitySnapshot{{
		ID:   "person-1",
		Kind: KindPerson,
		Aliases: []Alias{{
			Type:  AliasTypeEmail,
			Value: "Riya.Chen@synthetic.example",
		}},
	}})

	if resolution.EntityID != "person-1" {
		t.Fatalf("EntityID = %q, want accepted email entity", resolution.EntityID)
	}
	if !resolution.AutoResolved {
		t.Fatal("AutoResolved = false, want true")
	}
}

func TestResolverAutoResolvesUniqueAcceptedNameAlias(t *testing.T) {
	resolution := Resolver{}.Resolve(Mention{Name: "  RIYA\u00a0CHEN  "}, []EntitySnapshot{{
		ID:   "person-1",
		Kind: KindPerson,
		Aliases: []Alias{{
			Type:  AliasTypeName,
			Value: "Riya Chen",
		}},
	}})

	if resolution.EntityID != "person-1" {
		t.Fatalf("EntityID = %q, want unique accepted alias entity", resolution.EntityID)
	}
}

func TestResolverLeavesDuplicateAcceptedAliasPending(t *testing.T) {
	resolution := Resolver{}.Resolve(Mention{Name: "Riya Chen"}, []EntitySnapshot{
		{ID: "person-1", Kind: KindPerson, Aliases: []Alias{{Type: AliasTypeName, Value: "Riya Chen"}}},
		{ID: "person-2", Kind: KindPerson, Aliases: []Alias{{Type: AliasTypeName, Value: "Riya Chen"}}},
	})

	if resolution.EntityID != "" {
		t.Fatalf("EntityID = %q, want no accepted identity for ambiguous alias", resolution.EntityID)
	}
	if resolution.AutoResolved {
		t.Fatal("AutoResolved = true, want false")
	}
}

func TestResolverRanksGuessesByConfidenceThenEntityIDWithoutResolving(t *testing.T) {
	resolution := Resolver{}.Resolve(Mention{Name: "Riya Chen"}, []EntitySnapshot{
		{ID: "person-b", Kind: KindPerson, DisplayName: "Riya Chen"},
		{ID: "person-a", Kind: KindPerson, DisplayName: "Riya Chen"},
		{ID: "person-c", Kind: KindPerson, DisplayName: "Riya Chandra"},
	})

	if resolution.EntityID != "" {
		t.Fatalf("EntityID = %q, want guesses to remain unresolved", resolution.EntityID)
	}
	if len(resolution.Candidates) != 3 {
		t.Fatalf("candidate count = %d, want 3", len(resolution.Candidates))
	}
	if resolution.Candidates[0].EntityID != "person-a" || resolution.Candidates[1].EntityID != "person-b" {
		t.Fatalf("equal-confidence candidates = %#v, want stable entity ID ordering", resolution.Candidates)
	}
}

func TestResolverReturnsNoCandidateForUnrelatedMention(t *testing.T) {
	resolution := Resolver{}.Resolve(Mention{Name: "Unrelated Synthetic Person"}, []EntitySnapshot{{
		ID:          "person-1",
		Kind:        KindPerson,
		DisplayName: "Riya Chen",
	}})

	if resolution.EntityID != "" {
		t.Fatalf("EntityID = %q, want unresolved", resolution.EntityID)
	}
	if len(resolution.Candidates) != 0 {
		t.Fatalf("candidate count = %d, want 0", len(resolution.Candidates))
	}
}

func TestResolverDoesNotTreatMalformedEmailAsName(t *testing.T) {
	resolution := Resolver{}.Resolve(Mention{Email: "Riya Chen"}, []EntitySnapshot{{
		ID:   "person-1",
		Kind: KindPerson,
		Aliases: []Alias{{
			Type:  AliasTypeName,
			Value: "Riya Chen",
		}},
	}})

	if resolution.AutoResolved || resolution.EntityID != "" || len(resolution.Candidates) != 0 {
		t.Fatalf("resolution = %#v, want malformed email rejected without name comparison", resolution)
	}
}

func TestResolverComparesNameAndEmailOnlyToMatchingAliasTypes(t *testing.T) {
	resolution := Resolver{}.Resolve(Mention{
		Name:  "Riya Chen",
		Email: "different@synthetic.example",
	}, []EntitySnapshot{
		{ID: "person-name", Kind: KindPerson, Aliases: []Alias{{Type: AliasTypeName, Value: "Riya Chen"}}},
		{ID: "person-email", Kind: KindPerson, Aliases: []Alias{{Type: AliasTypeEmail, Value: "riya.chen@synthetic.example"}}},
	})

	if !resolution.AutoResolved || resolution.EntityID != "person-name" {
		t.Fatalf("resolution = %#v, want exact typed name alias only", resolution)
	}
}
