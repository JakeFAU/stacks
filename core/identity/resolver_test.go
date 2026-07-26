package identity_test

import (
	"fmt"
	"testing"

	"github.com/JakeFAU/stacks/core/identity"
)

func BenchmarkResolverRanksLargeEntitySet(b *testing.B) {
	const snapshotCount = 1_000

	snapshots := make([]identity.EntitySnapshot, snapshotCount)
	for index := range snapshots {
		snapshots[index] = identity.EntitySnapshot{
			ID:          fmt.Sprintf("entity-%04d", index),
			Kind:        identity.KindPerson,
			DisplayName: fmt.Sprintf("Synthetic Person %04d", index),
			Aliases: []identity.Alias{{
				Type:  identity.AliasTypeName,
				Value: fmt.Sprintf("Test Person %04d", index),
			}},
		}
	}
	mention := identity.Mention{Name: "Unmatched Example"}
	resolver := identity.Resolver{}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		resolver.Resolve(mention, snapshots)
	}
}

func TestResolverAutoResolvesAcceptedExactEmail(t *testing.T) {
	resolution := identity.Resolver{}.Resolve(identity.Mention{Email: "riya.chen@synthetic.example"}, []identity.EntitySnapshot{{
		ID:   "person-1",
		Kind: identity.KindPerson,
		Aliases: []identity.Alias{{
			Type:  identity.AliasTypeEmail,
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
	resolution := identity.Resolver{}.Resolve(identity.Mention{Name: "  RIYA\u00a0CHEN  "}, []identity.EntitySnapshot{{
		ID:   "person-1",
		Kind: identity.KindPerson,
		Aliases: []identity.Alias{{
			Type:  identity.AliasTypeName,
			Value: "Riya Chen",
		}},
	}})

	if resolution.EntityID != "person-1" {
		t.Fatalf("EntityID = %q, want unique accepted alias entity", resolution.EntityID)
	}
}

func TestResolverLeavesDuplicateAcceptedAliasPending(t *testing.T) {
	resolution := identity.Resolver{}.Resolve(identity.Mention{Name: "Riya Chen"}, []identity.EntitySnapshot{
		{ID: "person-1", Kind: identity.KindPerson, Aliases: []identity.Alias{{Type: identity.AliasTypeName, Value: "Riya Chen"}}},
		{ID: "person-2", Kind: identity.KindPerson, Aliases: []identity.Alias{{Type: identity.AliasTypeName, Value: "Riya Chen"}}},
	})

	if resolution.EntityID != "" {
		t.Fatalf("EntityID = %q, want no accepted identity for ambiguous alias", resolution.EntityID)
	}
	if resolution.AutoResolved {
		t.Fatal("AutoResolved = true, want false")
	}
}

func TestResolverRanksGuessesByConfidenceThenEntityIDWithoutResolving(t *testing.T) {
	resolution := identity.Resolver{}.Resolve(identity.Mention{Name: "Riya Chen"}, []identity.EntitySnapshot{
		{ID: "person-b", Kind: identity.KindPerson, DisplayName: "Riya Chen"},
		{ID: "person-a", Kind: identity.KindPerson, DisplayName: "Riya Chen"},
		{ID: "person-c", Kind: identity.KindPerson, DisplayName: "Riya Chandra"},
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
	resolution := identity.Resolver{}.Resolve(identity.Mention{Name: "Unrelated Synthetic Person"}, []identity.EntitySnapshot{{
		ID:          "person-1",
		Kind:        identity.KindPerson,
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
	resolution := identity.Resolver{}.Resolve(identity.Mention{Email: "Riya Chen"}, []identity.EntitySnapshot{{
		ID:   "person-1",
		Kind: identity.KindPerson,
		Aliases: []identity.Alias{{
			Type:  identity.AliasTypeName,
			Value: "Riya Chen",
		}},
	}})

	if resolution.AutoResolved || resolution.EntityID != "" || len(resolution.Candidates) != 0 {
		t.Fatalf("resolution = %#v, want malformed email rejected without name comparison", resolution)
	}
}

func TestResolverComparesNameAndEmailOnlyToMatchingAliasTypes(t *testing.T) {
	resolution := identity.Resolver{}.Resolve(identity.Mention{
		Name:  "Riya Chen",
		Email: "different@synthetic.example",
	}, []identity.EntitySnapshot{
		{ID: "person-name", Kind: identity.KindPerson, Aliases: []identity.Alias{{Type: identity.AliasTypeName, Value: "Riya Chen"}}},
		{ID: "person-email", Kind: identity.KindPerson, Aliases: []identity.Alias{{Type: identity.AliasTypeEmail, Value: "riya.chen@synthetic.example"}}},
	})

	if !resolution.AutoResolved || resolution.EntityID != "person-name" {
		t.Fatalf("resolution = %#v, want exact typed name alias only", resolution)
	}
}
