package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/identity"
)

func TestIdentityRepositoryAcceptsOpaqueTextIDs(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	entity := canonicalEntity(t, "entity:person/external-17", "Synthetic Person")
	mention := canonicalMention(t, "mention:document/17#person-1", fixture.evidence[0], "Synthetic Person", "")
	proposal := canonicalProposal(t, "proposal:mention/17", mention.ID(), fixture.evidence...)
	candidate := canonicalCandidate(
		t,
		"candidate:proposal/17#rank-1",
		proposal.ID(),
		entity.ID(),
		1,
		"exact_name",
		identity.CandidateSource{Kind: "heuristic", Reference: "name:opaque/17"},
	)

	persistIdentityReview(t, fixture, entity, mention, proposal, candidate)

	gotEntity, err := fixture.database.LoadEntity(fixture.ctx, entity.ID())
	if err != nil {
		t.Fatalf("LoadEntity() error = %v", err)
	}
	if gotEntity.Entity.ID() != entity.ID() {
		t.Fatalf("loaded entity ID = %q, want %q", gotEntity.Entity.ID(), entity.ID())
	}
	gotProposal, err := fixture.database.LoadResolutionProposal(fixture.ctx, proposal.ID())
	if err != nil {
		t.Fatalf("LoadResolutionProposal() error = %v", err)
	}
	if gotProposal.Proposal.ID() != proposal.ID() ||
		len(gotProposal.Candidates) != 1 ||
		gotProposal.Candidates[0].ID() != candidate.ID() {
		t.Fatalf("loaded proposal = %#v, want opaque proposal and candidate IDs", gotProposal)
	}
	if !reflect.DeepEqual(gotProposal.Proposal.EvidenceIDs(), proposal.EvidenceIDs()) {
		t.Fatalf("proposal evidence IDs = %v, want ordered %v", gotProposal.Proposal.EvidenceIDs(), proposal.EvidenceIDs())
	}
}

func TestIdentityRetryIsIdempotentAndPayloadConflictFails(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	entity := canonicalEntity(t, "entity:opaque/retry", "Synthetic Person")
	mention := canonicalMention(t, "mention:opaque/retry", fixture.evidence[0], "Synthetic Person", "")
	proposal := canonicalProposal(t, "proposal:opaque/retry", mention.ID(), fixture.evidence...)
	candidate := canonicalCandidate(
		t,
		"candidate:opaque/retry",
		proposal.ID(),
		entity.ID(),
		1,
		"exact_name",
		identity.CandidateSource{Kind: "heuristic", Reference: "source:opaque/retry"},
	)

	var created []bool
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		for _, put := range []func() (bool, error){
			func() (bool, error) { return transaction.PutEntity(fixture.ctx, entity) },
			func() (bool, error) { return transaction.PutMention(fixture.ctx, mention) },
			func() (bool, error) { return transaction.PutResolutionProposal(fixture.ctx, proposal) },
			func() (bool, error) { return transaction.PutResolutionCandidate(fixture.ctx, candidate) },
			func() (bool, error) { return transaction.PutEntity(fixture.ctx, entity) },
			func() (bool, error) { return transaction.PutMention(fixture.ctx, mention) },
			func() (bool, error) { return transaction.PutResolutionProposal(fixture.ctx, proposal) },
			func() (bool, error) { return transaction.PutResolutionCandidate(fixture.ctx, candidate) },
		} {
			value, err := put()
			if err != nil {
				return err
			}
			created = append(created, value)
		}
		return nil
	}); err != nil {
		t.Fatalf("exact identity retry: %v", err)
	}
	if !reflect.DeepEqual(created, []bool{true, true, true, true, false, false, false, false}) {
		t.Fatalf("created sequence = %v, want first writes then read-only retries", created)
	}

	conflicting := canonicalEntity(t, entity.ID(), "Different Synthetic Person")
	err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		_, err := transaction.PutEntity(fixture.ctx, conflicting)
		return err
	})
	if !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("conflicting PutEntity() error = %v, want ErrConflict", err)
	}
}

func TestNameOnlyCandidateNeverCreatesAutomaticAuthority(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	entity := canonicalEntity(t, "entity:name-only", "Synthetic Person")
	mention := canonicalMention(t, "mention:name-only", fixture.evidence[0], "Synthetic Person", "")
	proposal := canonicalProposal(t, "proposal:name-only", mention.ID(), fixture.evidence[0])
	candidate := canonicalCandidate(
		t,
		"candidate:name-only",
		proposal.ID(),
		entity.ID(),
		1,
		"exact_name",
		identity.CandidateSource{Kind: "heuristic", Reference: "name-only:opaque"},
	)
	persistIdentityReview(t, fixture, entity, mention, proposal, candidate)

	snapshots, err := fixture.database.EntitySnapshots(fixture.ctx)
	if err != nil {
		t.Fatalf("EntitySnapshots() error = %v", err)
	}
	resolution := (identity.Resolver{}).Resolve(identity.Mention{Name: mention.Surface()}, snapshots)
	if resolution.AutoResolved || resolution.EntityID != "" || len(resolution.Candidates) != 1 {
		t.Fatalf("name-only resolution = %#v, want one review candidate and no authority", resolution)
	}
	if _, err := fixture.database.EffectiveResolutionDecision(fixture.ctx, proposal.ID()); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("EffectiveResolutionDecision() error = %v, want ErrNotFound", err)
	}
	automatic := canonicalResolutionDecision(
		t,
		"decision:name-only/automatic",
		proposal.ID(),
		identity.DecisionAccepted,
		entity.ID(),
		identity.AuthorityAutomatic,
		"",
		identityRecordedAt.Add(4*time.Minute),
	)
	err = fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendResolutionDecision(fixture.ctx, automatic, nil)
	})
	if !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("automatic name-only decision error = %v, want ErrConflict", err)
	}
}

func TestUniqueExactWorkEmailCanCreateAutomaticAuthority(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	entity := canonicalEntity(t, "entity:email-authority", "Synthetic Person")
	firstMention := canonicalMention(t, "mention:email-authority/seed", fixture.evidence[0], "Synthetic Person", "")
	firstProposal := canonicalProposal(t, "proposal:email-authority/seed", firstMention.ID(), fixture.evidence[0])
	persistIdentityReview(t, fixture, entity, firstMention, firstProposal)
	seedDecision := canonicalResolutionDecision(
		t,
		"decision:email-authority/seed",
		firstProposal.ID(),
		identity.DecisionAccepted,
		entity.ID(),
		identity.AuthorityReviewer,
		"",
		identityRecordedAt.Add(4*time.Minute),
	)
	emailAlias := canonicalAliasAssertion(
		t,
		"alias:email-authority/seed",
		seedDecision.ID(),
		entity.ID(),
		identity.Alias{Type: identity.AliasTypeEmail, Value: "person@synthetic.example"},
		identityRecordedAt.Add(4*time.Minute),
	)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendResolutionDecision(fixture.ctx, seedDecision, []identity.AliasAssertion{emailAlias})
	}); err != nil {
		t.Fatalf("append accepted email authority: %v", err)
	}

	secondMention := canonicalMention(
		t,
		"mention:email-authority/automatic",
		fixture.evidence[1],
		"Unrelated Surface",
		" PERSON@Synthetic.Example ",
	)
	secondProposal := canonicalProposal(
		t,
		"proposal:email-authority/automatic",
		secondMention.ID(),
		fixture.evidence[1],
	)
	secondCandidate := canonicalCandidate(
		t,
		"candidate:email-authority/automatic",
		secondProposal.ID(),
		entity.ID(),
		1,
		"unique_exact_work_email",
		identity.CandidateSource{Kind: "directory", Reference: "profile:opaque/17"},
	)
	persistIdentityReview(t, fixture, entity, secondMention, secondProposal, secondCandidate)

	snapshots, err := fixture.database.EntitySnapshots(fixture.ctx)
	if err != nil {
		t.Fatalf("EntitySnapshots() error = %v", err)
	}
	resolution := (identity.Resolver{}).Resolve(
		identity.Mention{Email: "PERSON@synthetic.example"},
		snapshots,
	)
	if !resolution.AutoResolved || resolution.EntityID != string(entity.ID()) {
		t.Fatalf("exact accepted work-email resolution = %#v, want automatic entity %q", resolution, entity.ID())
	}
	automatic := canonicalResolutionDecision(
		t,
		"decision:email-authority/automatic",
		secondProposal.ID(),
		identity.DecisionAccepted,
		entity.ID(),
		identity.AuthorityAutomatic,
		"",
		identityRecordedAt.Add(5*time.Minute),
	)
	automaticAlias := canonicalAliasAssertion(
		t,
		"alias:email-authority/automatic",
		automatic.ID(),
		entity.ID(),
		identity.Alias{Type: identity.AliasTypeEmail, Value: "person@synthetic.example"},
		identityRecordedAt.Add(5*time.Minute),
	)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendResolutionDecision(
			fixture.ctx,
			automatic,
			[]identity.AliasAssertion{automaticAlias},
		)
	}); err != nil {
		t.Fatalf("append automatic exact-email authority: %v", err)
	}
	effective, err := fixture.database.EffectiveResolutionDecision(fixture.ctx, secondProposal.ID())
	if err != nil {
		t.Fatalf("EffectiveResolutionDecision() error = %v", err)
	}
	if effective.ID() != automatic.ID() || effective.Authority() != identity.AuthorityAutomatic {
		t.Fatalf("effective automatic decision = %#v, want %q", effective, automatic.ID())
	}
}

func TestIdentityAutomaticEmailAuthorityRequiresSourceGroundedCanonicalAlias(t *testing.T) {
	tests := []struct {
		name                 string
		aliasValues          []string
		wrongCandidateEntity bool
	}{
		{name: "unrelated valid email", aliasValues: []string{"unrelated@synthetic.example"}},
		{name: "noncanonical alias", aliasValues: []string{" SOURCE.BOUND@Synthetic.Example "}},
		{name: "absent alias"},
		{
			name:        "multiple aliases",
			aliasValues: []string{"source.bound@synthetic.example", "other@synthetic.example"},
		},
		{
			name:                 "candidate names another entity",
			aliasValues:          []string{"source.bound@synthetic.example"},
			wrongCandidateEntity: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIdentityRepositoryFixture(t)
			decisionEntity := canonicalEntity(t, "entity:automatic-alias/decision", "Decision Entity")
			mention := canonicalMention(
				t,
				"mention:automatic-alias",
				fixture.evidence[0],
				"Synthetic Person",
				" Source.Bound@Synthetic.Example ",
			)
			proposal := canonicalProposal(
				t,
				"proposal:automatic-alias",
				mention.ID(),
				fixture.evidence[0],
			)
			candidateEntity := decisionEntity
			if test.wrongCandidateEntity {
				candidateEntity = canonicalEntity(
					t,
					"entity:automatic-alias/candidate",
					"Candidate Entity",
				)
			}
			persistIdentityReview(t, fixture, decisionEntity, mention, proposal)
			candidate := canonicalCandidate(
				t,
				"candidate:automatic-alias",
				proposal.ID(),
				candidateEntity.ID(),
				1,
				"unique_exact_work_email",
				identity.CandidateSource{Kind: "directory", Reference: "profile:opaque/automatic-alias"},
			)
			if err := fixture.database.InTransaction(
				fixture.ctx,
				func(transaction *postgres.Transaction) error {
					if candidateEntity.ID() != decisionEntity.ID() {
						if _, err := transaction.PutEntity(fixture.ctx, candidateEntity); err != nil {
							return err
						}
					}
					_, err := transaction.PutResolutionCandidate(fixture.ctx, candidate)
					return err
				},
			); err != nil {
				t.Fatalf("persist automatic candidate: %v", err)
			}
			decision := canonicalResolutionDecision(
				t,
				"decision:automatic-alias",
				proposal.ID(),
				identity.DecisionAccepted,
				decisionEntity.ID(),
				identity.AuthorityAutomatic,
				"",
				identityRecordedAt.Add(4*time.Minute),
			)
			aliases := make([]identity.AliasAssertion, len(test.aliasValues))
			for index, value := range test.aliasValues {
				aliases[index] = canonicalAliasAssertion(
					t,
					identity.AliasAssertionID(fmt.Sprintf("alias:automatic-alias/%d", index)),
					decision.ID(),
					decisionEntity.ID(),
					identity.Alias{Type: identity.AliasTypeEmail, Value: value},
					identityRecordedAt.Add(4*time.Minute),
				)
			}
			err := fixture.database.InTransaction(
				fixture.ctx,
				func(transaction *postgres.Transaction) error {
					return transaction.AppendResolutionDecision(fixture.ctx, decision, aliases)
				},
			)
			if !errors.Is(err, postgres.ErrConflict) {
				t.Fatalf("AppendResolutionDecision() error = %v, want ErrConflict", err)
			}
			var decisions, assertions int
			if err := fixture.admin.QueryRow(fixture.ctx, `
				SELECT
					(SELECT count(*) FROM stacks_core.resolution_decisions WHERE proposal_id = $1),
					(SELECT count(*) FROM stacks_core.entity_alias_assertions WHERE decision_id = $2)`,
				proposal.ID(),
				decision.ID(),
			).Scan(&decisions, &assertions); err != nil {
				t.Fatalf("count rejected automatic authority rows: %v", err)
			}
			if decisions != 0 || assertions != 0 {
				t.Fatalf("automatic decision/alias rows = %d/%d, want 0/0", decisions, assertions)
			}
		})
	}
}

func TestUniqueExactWorkEmailRejectsAmbiguousAutomaticAuthority(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	firstEntity := canonicalEntity(t, "entity:email-ambiguous/first", "First Synthetic Person")
	secondEntity := canonicalEntity(t, "entity:email-ambiguous/second", "Second Synthetic Person")
	mention := canonicalMention(
		t,
		"mention:email-ambiguous",
		fixture.evidence[0],
		"Synthetic Person",
		"",
	)
	proposal := canonicalProposal(
		t,
		"proposal:email-ambiguous",
		mention.ID(),
		fixture.evidence[0],
	)
	firstCandidate := canonicalCandidate(
		t,
		"candidate:email-ambiguous/first",
		proposal.ID(),
		firstEntity.ID(),
		1,
		"unique_exact_work_email",
		identity.CandidateSource{Kind: "directory", Reference: "profile:opaque/first"},
	)
	secondCandidate := canonicalCandidate(
		t,
		"candidate:email-ambiguous/second",
		proposal.ID(),
		secondEntity.ID(),
		2,
		"unique_exact_work_email",
		identity.CandidateSource{Kind: "directory", Reference: "profile:opaque/second"},
	)
	persistIdentityReview(t, fixture, firstEntity, mention, proposal, firstCandidate)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		if _, err := transaction.PutEntity(fixture.ctx, secondEntity); err != nil {
			return err
		}
		_, err := transaction.PutResolutionCandidate(fixture.ctx, secondCandidate)
		return err
	}); err != nil {
		t.Fatalf("persist ambiguous exact-email candidate: %v", err)
	}
	automatic := canonicalResolutionDecision(
		t,
		"decision:email-ambiguous/automatic",
		proposal.ID(),
		identity.DecisionAccepted,
		firstEntity.ID(),
		identity.AuthorityAutomatic,
		"",
		identityRecordedAt.Add(4*time.Minute),
	)
	err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendResolutionDecision(fixture.ctx, automatic, nil)
	})
	if !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("ambiguous automatic decision error = %v, want ErrConflict", err)
	}
}

func TestReviewerCorrectionAppendsAndSupersedes(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	firstEntity := canonicalEntity(t, "entity:correction/first", "First Synthetic Person")
	secondEntity := canonicalEntity(t, "entity:correction/second", "Second Synthetic Person")
	mention := canonicalMention(t, "mention:correction", fixture.evidence[0], "Synthetic Person", "")
	proposal := canonicalProposal(t, "proposal:correction", mention.ID(), fixture.evidence[0])
	persistIdentityReview(t, fixture, firstEntity, mention, proposal)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		_, err := transaction.PutEntity(fixture.ctx, secondEntity)
		return err
	}); err != nil {
		t.Fatalf("persist correction entity: %v", err)
	}
	initial := canonicalResolutionDecision(
		t, "decision:correction/initial", proposal.ID(), identity.DecisionAccepted,
		firstEntity.ID(), identity.AuthorityReviewer, "", identityRecordedAt.Add(4*time.Minute),
	)
	correction := canonicalResolutionDecision(
		t, "decision:correction/replacement", proposal.ID(), identity.DecisionAccepted,
		secondEntity.ID(), identity.AuthorityReviewer, initial.ID(), identityRecordedAt.Add(5*time.Minute),
	)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendResolutionDecision(fixture.ctx, initial, nil)
	}); err != nil {
		t.Fatalf("append initial reviewer decision: %v", err)
	}
	initialXID := identityRowXID(t, fixture.ctx, fixture.admin, initial.ID())
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendResolutionDecision(fixture.ctx, correction, nil)
	}); err != nil {
		t.Fatalf("append reviewer correction: %v", err)
	}
	if got := identityRowXID(t, fixture.ctx, fixture.admin, initial.ID()); got != initialXID {
		t.Fatalf("reviewer correction rewrote predecessor xmin from %s to %s", initialXID, got)
	}
	effective, err := fixture.database.EffectiveResolutionDecision(fixture.ctx, proposal.ID())
	if err != nil {
		t.Fatalf("EffectiveResolutionDecision() error = %v", err)
	}
	if effective.ID() != correction.ID() {
		t.Fatalf("effective decision ID = %q, want correction %q", effective.ID(), correction.ID())
	}
	loadedInitial, err := fixture.database.LoadResolutionDecision(fixture.ctx, initial.ID())
	if err != nil {
		t.Fatalf("LoadResolutionDecision(initial) error = %v", err)
	}
	if loadedInitial.Digest() != initial.Digest() {
		t.Fatal("loaded initial decision digest changed after correction")
	}
}

func TestConcurrentCorrectionsCannotBranch(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	entity := canonicalEntity(t, "entity:concurrent", "Synthetic Person")
	mention := canonicalMention(t, "mention:concurrent", fixture.evidence[0], "Synthetic Person", "")
	proposal := canonicalProposal(t, "proposal:concurrent", mention.ID(), fixture.evidence[0])
	persistIdentityReview(t, fixture, entity, mention, proposal)
	initial := canonicalResolutionDecision(
		t, "decision:concurrent/initial", proposal.ID(), identity.DecisionAccepted,
		entity.ID(), identity.AuthorityReviewer, "", identityRecordedAt.Add(4*time.Minute),
	)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendResolutionDecision(fixture.ctx, initial, nil)
	}); err != nil {
		t.Fatalf("append initial concurrent decision: %v", err)
	}

	corrections := []identity.ResolutionDecision{
		canonicalResolutionDecision(
			t, "decision:concurrent/a", proposal.ID(), identity.DecisionRejected, "",
			identity.AuthorityReviewer, initial.ID(), identityRecordedAt.Add(5*time.Minute),
		),
		canonicalResolutionDecision(
			t, "decision:concurrent/b", proposal.ID(), identity.DecisionRejected, "",
			identity.AuthorityReviewer, initial.ID(), identityRecordedAt.Add(6*time.Minute),
		),
	}
	start := make(chan struct{})
	results := make(chan error, len(corrections))
	var ready sync.WaitGroup
	ready.Add(len(corrections))
	for _, correction := range corrections {
		correction := correction
		go func() {
			ready.Done()
			<-start
			results <- fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
				return transaction.AppendResolutionDecision(fixture.ctx, correction, nil)
			})
		}()
	}
	ready.Wait()
	close(start)
	var succeeded, conflicted int
	for range corrections {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, postgres.ErrConflict):
			conflicted++
		default:
			t.Fatalf("concurrent correction error = %v, want nil or ErrConflict", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent correction outcomes success/conflict = %d/%d, want 1/1", succeeded, conflicted)
	}
}

func TestEffectiveAliasesFollowOnlyCurrentAcceptedAuthority(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	firstEntity := canonicalEntity(t, "entity:aliases/first", "First Synthetic Person")
	secondEntity := canonicalEntity(t, "entity:aliases/second", "Second Synthetic Person")
	mention := canonicalMention(t, "mention:aliases", fixture.evidence[0], "Synthetic Person", "")
	proposal := canonicalProposal(t, "proposal:aliases", mention.ID(), fixture.evidence[0])
	persistIdentityReview(t, fixture, firstEntity, mention, proposal)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		_, err := transaction.PutEntity(fixture.ctx, secondEntity)
		return err
	}); err != nil {
		t.Fatalf("persist second alias entity: %v", err)
	}
	initial := canonicalResolutionDecision(
		t, "decision:aliases/initial", proposal.ID(), identity.DecisionAccepted,
		firstEntity.ID(), identity.AuthorityReviewer, "", identityRecordedAt.Add(4*time.Minute),
	)
	initialAlias := canonicalAliasAssertion(
		t, "alias:aliases/initial", initial.ID(), firstEntity.ID(),
		identity.Alias{Type: identity.AliasTypeName, Value: "Initial Alias"},
		identityRecordedAt.Add(4*time.Minute),
	)
	rejected := canonicalResolutionDecision(
		t, "decision:aliases/rejected", proposal.ID(), identity.DecisionRejected,
		"", identity.AuthorityReviewer, initial.ID(), identityRecordedAt.Add(5*time.Minute),
	)
	replacement := canonicalResolutionDecision(
		t, "decision:aliases/replacement", proposal.ID(), identity.DecisionAccepted,
		secondEntity.ID(), identity.AuthorityReviewer, rejected.ID(), identityRecordedAt.Add(6*time.Minute),
	)
	replacementAlias := canonicalAliasAssertion(
		t, "alias:aliases/replacement", replacement.ID(), secondEntity.ID(),
		identity.Alias{Type: identity.AliasTypeEmail, Value: "replacement@synthetic.example"},
		identityRecordedAt.Add(6*time.Minute),
	)
	for _, appendDecision := range []struct {
		decision identity.ResolutionDecision
		aliases  []identity.AliasAssertion
	}{
		{initial, []identity.AliasAssertion{initialAlias}},
		{rejected, nil},
		{replacement, []identity.AliasAssertion{replacementAlias}},
	} {
		if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
			return transaction.AppendResolutionDecision(fixture.ctx, appendDecision.decision, appendDecision.aliases)
		}); err != nil {
			t.Fatalf("AppendResolutionDecision(%q) error = %v", appendDecision.decision.ID(), err)
		}
	}

	snapshots, err := fixture.database.EntitySnapshots(fixture.ctx)
	if err != nil {
		t.Fatalf("EntitySnapshots() error = %v", err)
	}
	aliasesByEntity := make(map[string][]identity.Alias, len(snapshots))
	for _, snapshot := range snapshots {
		aliasesByEntity[snapshot.ID] = snapshot.Aliases
	}
	if len(aliasesByEntity[string(firstEntity.ID())]) != 0 {
		t.Fatalf("superseded first aliases = %v, want none", aliasesByEntity[string(firstEntity.ID())])
	}
	if got := aliasesByEntity[string(secondEntity.ID())]; !reflect.DeepEqual(
		got,
		[]identity.Alias{{Type: identity.AliasTypeEmail, Value: "replacement@synthetic.example"}},
	) {
		t.Fatalf("effective replacement aliases = %v, want only current accepted alias", got)
	}
}

func TestReviewReadModelsContainOnlyCanonicalIdentityState(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	entity := canonicalEntity(t, "entity:read-model", "Synthetic Person")
	mention := canonicalMention(t, "mention:read-model", fixture.evidence[0], "Synthetic Person", "")
	proposal := canonicalProposal(t, "proposal:read-model", mention.ID(), fixture.evidence...)
	candidate := canonicalCandidate(
		t,
		"candidate:read-model",
		proposal.ID(),
		entity.ID(),
		1,
		"exact_name",
		identity.CandidateSource{Kind: "directory", Reference: "directory-profile:opaque/17"},
	)
	persistIdentityReview(t, fixture, entity, mention, proposal, candidate)
	decision := canonicalResolutionDecision(
		t, "decision:read-model", proposal.ID(), identity.DecisionAccepted,
		entity.ID(), identity.AuthorityReviewer, "", identityRecordedAt.Add(4*time.Minute),
	)
	alias := canonicalAliasAssertion(
		t, "alias:read-model", decision.ID(), entity.ID(),
		identity.Alias{Type: identity.AliasTypeName, Value: "Synthetic Person"},
		identityRecordedAt.Add(4*time.Minute),
	)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendResolutionDecision(fixture.ctx, decision, []identity.AliasAssertion{alias})
	}); err != nil {
		t.Fatalf("append read-model decision: %v", err)
	}

	entities, err := fixture.database.ListEntities(fixture.ctx)
	if err != nil {
		t.Fatalf("ListEntities() error = %v", err)
	}
	if len(entities) != 1 ||
		len(entities[0].Aliases) != 1 ||
		!reflect.DeepEqual(entities[0].GroundingMentionIDs, []identity.MentionID{mention.ID()}) ||
		!reflect.DeepEqual(entities[0].EvidenceIDs, proposal.EvidenceIDs()) {
		t.Fatalf("canonical entity read model = %#v", entities)
	}
	loaded, err := fixture.database.LoadResolutionProposal(fixture.ctx, proposal.ID())
	if err != nil {
		t.Fatalf("LoadResolutionProposal() error = %v", err)
	}
	if loaded.EffectiveDecision == nil ||
		loaded.EffectiveDecision.ID() != decision.ID() ||
		len(loaded.Candidates) != 1 ||
		loaded.Candidates[0].Source().Reference != "directory-profile:opaque/17" {
		t.Fatalf("canonical proposal read model = %#v", loaded)
	}
	pending, err := fixture.database.ListPendingResolutionProposals(fixture.ctx)
	if err != nil {
		t.Fatalf("ListPendingResolutionProposals() error = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending proposals after effective decision = %d, want 0", len(pending))
	}
}

func TestIdentityRepositoryPreservesCancellation(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	canceled, cancel := context.WithCancel(fixture.ctx)
	cancel()
	if _, err := fixture.database.ListEntities(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListEntities() error = %v, want context.Canceled", err)
	}
}

func TestRejectedDecisionCreatesNoAliases(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	entity := canonicalEntity(t, "entity:rejected-alias", "Synthetic Person")
	mention := canonicalMention(t, "mention:rejected-alias", fixture.evidence[0], "Synthetic Person", "")
	proposal := canonicalProposal(t, "proposal:rejected-alias", mention.ID(), fixture.evidence[0])
	persistIdentityReview(t, fixture, entity, mention, proposal)
	rejected := canonicalResolutionDecision(
		t, "decision:rejected-alias", proposal.ID(), identity.DecisionRejected,
		"", identity.AuthorityReviewer, "", identityRecordedAt.Add(4*time.Minute),
	)
	alias := canonicalAliasAssertion(
		t, "alias:rejected-alias", rejected.ID(), entity.ID(),
		identity.Alias{Type: identity.AliasTypeName, Value: "Must Not Persist"},
		identityRecordedAt.Add(4*time.Minute),
	)
	err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendResolutionDecision(fixture.ctx, rejected, []identity.AliasAssertion{alias})
	})
	if !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("rejected decision with aliases error = %v, want ErrConflict", err)
	}
	var decisions, aliases int
	if err := fixture.admin.QueryRow(fixture.ctx, `
		SELECT
			(SELECT count(*) FROM stacks_core.resolution_decisions WHERE proposal_id = $1),
			(SELECT count(*) FROM stacks_core.entity_alias_assertions WHERE decision_id = $2)`,
		proposal.ID(),
		rejected.ID(),
	).Scan(&decisions, &aliases); err != nil {
		t.Fatalf("inspect rejected alias rollback: %v", err)
	}
	if decisions != 0 || aliases != 0 {
		t.Fatalf("rejected decision/alias rows = %d/%d, want 0/0", decisions, aliases)
	}
}

func TestAutomaticDecisionCannotSupersedeReviewerAuthority(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	entity := canonicalEntity(t, "entity:reviewer-authority", "Synthetic Person")
	mention := canonicalMention(t, "mention:reviewer-authority", fixture.evidence[0], "Synthetic Person", "")
	proposal := canonicalProposal(t, "proposal:reviewer-authority", mention.ID(), fixture.evidence[0])
	persistIdentityReview(t, fixture, entity, mention, proposal)
	reviewer := canonicalResolutionDecision(
		t, "decision:reviewer-authority", proposal.ID(), identity.DecisionAccepted,
		entity.ID(), identity.AuthorityReviewer, "", identityRecordedAt.Add(4*time.Minute),
	)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendResolutionDecision(fixture.ctx, reviewer, nil)
	}); err != nil {
		t.Fatalf("append reviewer authority: %v", err)
	}
	automatic := canonicalResolutionDecision(
		t, "decision:reviewer-authority/automatic", proposal.ID(), identity.DecisionAccepted,
		entity.ID(), identity.AuthorityAutomatic, reviewer.ID(), identityRecordedAt.Add(5*time.Minute),
	)
	err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendResolutionDecision(fixture.ctx, automatic, nil)
	})
	if !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("automatic reviewer supersession error = %v, want ErrConflict", err)
	}
}
