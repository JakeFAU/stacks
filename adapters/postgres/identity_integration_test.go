package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/jackc/pgx/v5"
)

var identityRecordedAt = time.Date(2026, time.July, 25, 18, 0, 0, 123456000, time.UTC)

type identityRepositoryFixture struct {
	documentRepositoryFixture
	evidence []evidence.EvidenceSpan
}

func newIdentityRepositoryFixture(t testing.TB) identityRepositoryFixture {
	t.Helper()
	documentFixture := newDocumentRepositoryFixture(t)
	document := canonicalDocument(t, "identity-source:opaque/1", "revision-a", documentRecordedAt)
	if _, err := documentFixture.database.PutDocumentVersion(documentFixture.ctx, document); err != nil {
		t.Fatalf("persist identity fixture document: %v", err)
	}
	spans := []evidence.EvidenceSpan{
		canonicalIdentityEvidence(t, document, "Alpha", 0, 5, identityRecordedAt),
		canonicalIdentityEvidence(t, document, "café", 6, 11, identityRecordedAt.Add(time.Microsecond)),
	}
	if err := documentFixture.database.InTransaction(
		documentFixture.ctx,
		func(transaction *postgres.Transaction) error {
			for _, span := range spans {
				created, err := transaction.PutEvidenceSpan(documentFixture.ctx, span)
				if err != nil {
					return err
				}
				if !created {
					return fmt.Errorf("identity evidence %q was not created", span.ID())
				}
			}
			return nil
		},
	); err != nil {
		t.Fatalf("persist identity fixture evidence: %v", err)
	}
	return identityRepositoryFixture{
		documentRepositoryFixture: documentFixture,
		evidence:                  spans,
	}
}

func canonicalIdentityEvidence(
	t testing.TB,
	document evidence.DocumentVersion,
	quote string,
	start int,
	end int,
	recordedAt time.Time,
) evidence.EvidenceSpan {
	t.Helper()
	span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
		Document:    document,
		SectionID:   "section-transcript",
		StartOffset: start,
		EndOffset:   end,
		Quote:       quote,
		RecordedAt:  recordedAt,
	})
	if err != nil {
		t.Fatalf("NewEvidenceSpan(%q) error = %v", quote, err)
	}
	return span
}

func canonicalEntity(t testing.TB, id identity.EntityID, displayName string) identity.Entity {
	t.Helper()
	entity, err := identity.NewEntity(identity.EntityInput{
		ID:          id,
		Kind:        identity.KindPerson,
		DisplayName: displayName,
		RecordedAt:  identityRecordedAt,
	})
	if err != nil {
		t.Fatalf("NewEntity(%q) error = %v", id, err)
	}
	return entity
}

func canonicalMention(
	t testing.TB,
	id identity.MentionID,
	span evidence.EvidenceSpan,
	name string,
	email string,
) identity.MentionRecord {
	t.Helper()
	input := identity.MentionInput{
		ID:              id,
		EvidenceID:      span.ID(),
		DerivationRunID: "run:opaque/identity",
		Surface:         name,
		NormalizedName:  identity.NormalizeName(name),
		Role:            "speaker",
		RecordedAt:      identityRecordedAt.Add(time.Minute),
	}
	if email != "" {
		input.ProposedEmail = email
		input.ProposedEmailEvidenceID = span.ID()
	}
	mention, err := identity.NewMention(input)
	if err != nil {
		t.Fatalf("NewMention(%q) error = %v", id, err)
	}
	return mention
}

func canonicalProposal(
	t testing.TB,
	id identity.ProposalID,
	mentionID identity.MentionID,
	spans ...evidence.EvidenceSpan,
) identity.ResolutionProposal {
	t.Helper()
	evidenceIDs := make([]evidence.EvidenceID, len(spans))
	for index, span := range spans {
		evidenceIDs[index] = span.ID()
	}
	proposal, err := identity.NewResolutionProposal(identity.ResolutionProposalInput{
		ID:          id,
		MentionID:   mentionID,
		ReasonCode:  "identity_review",
		EvidenceIDs: evidenceIDs,
		RecordedAt:  identityRecordedAt.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("NewResolutionProposal(%q) error = %v", id, err)
	}
	return proposal
}

func canonicalCandidate(
	t testing.TB,
	id identity.CandidateID,
	proposalID identity.ProposalID,
	entityID identity.EntityID,
	rank int,
	reasonCode string,
	source identity.CandidateSource,
) identity.ResolutionCandidate {
	t.Helper()
	candidate, err := identity.NewResolutionCandidate(identity.ResolutionCandidateInput{
		ID:         id,
		ProposalID: proposalID,
		EntityID:   entityID,
		Rank:       rank,
		Confidence: 0.75,
		ReasonCode: reasonCode,
		Source:     source,
		RecordedAt: identityRecordedAt.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("NewResolutionCandidate(%q) error = %v", id, err)
	}
	return candidate
}

func canonicalResolutionDecision(
	t testing.TB,
	id identity.DecisionID,
	proposalID identity.ProposalID,
	outcome identity.DecisionOutcome,
	entityID identity.EntityID,
	authority identity.DecisionAuthority,
	supersedesID identity.DecisionID,
	recordedAt time.Time,
) identity.ResolutionDecision {
	t.Helper()
	decision, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
		ID:           id,
		ProposalID:   proposalID,
		Outcome:      outcome,
		EntityID:     entityID,
		Authority:    authority,
		ReasonCode:   "identity_authority",
		RecordedAt:   recordedAt,
		SupersedesID: supersedesID,
	})
	if err != nil {
		t.Fatalf("NewResolutionDecision(%q) error = %v", id, err)
	}
	return decision
}

func canonicalAliasAssertion(
	t testing.TB,
	id identity.AliasAssertionID,
	decisionID identity.DecisionID,
	entityID identity.EntityID,
	alias identity.Alias,
	recordedAt time.Time,
) identity.AliasAssertion {
	t.Helper()
	assertion, err := identity.NewAliasAssertion(identity.AliasAssertionInput{
		ID:         id,
		DecisionID: decisionID,
		EntityID:   entityID,
		Alias:      alias,
		RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("NewAliasAssertion(%q) error = %v", id, err)
	}
	return assertion
}

func persistIdentityReview(
	t testing.TB,
	fixture identityRepositoryFixture,
	entity identity.Entity,
	mention identity.MentionRecord,
	proposal identity.ResolutionProposal,
	candidates ...identity.ResolutionCandidate,
) {
	t.Helper()
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		if _, err := transaction.PutEntity(fixture.ctx, entity); err != nil {
			return err
		}
		if _, err := transaction.PutMention(fixture.ctx, mention); err != nil {
			return err
		}
		if _, err := transaction.PutResolutionProposal(fixture.ctx, proposal); err != nil {
			return err
		}
		for _, candidate := range candidates {
			if _, err := transaction.PutResolutionCandidate(fixture.ctx, candidate); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("persist identity review fixture: %v", err)
	}
}

func identityRowXID(
	t testing.TB,
	ctx context.Context,
	connection *pgx.Conn,
	decisionID identity.DecisionID,
) string {
	t.Helper()
	var xid string
	if err := connection.QueryRow(
		ctx,
		`SELECT xmin::text
		 FROM stacks_core.resolution_decisions
		 WHERE id = $1`,
		decisionID,
	).Scan(&xid); err != nil {
		t.Fatalf("read resolution decision xmin: %v", err)
	}
	return xid
}
