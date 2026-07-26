package ingest

import (
	"testing"

	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
)

func TestCanonicalWriteSetDigestCoversResolvedPayload(t *testing.T) {
	base := validationCompletion(t)
	baseResolved, err := resolveCanonicalWriteSet(base)
	if err != nil {
		t.Fatal(err)
	}
	baseDigest := digestCanonicalWriteSet(base, baseResolved)

	decision, err := admission.NewDecision(admission.DecisionInput{
		ID: "admission-extra", TargetKind: admission.TargetExtractionRun,
		TargetID: "run-extra", Outcome: admission.Admitted,
		ReasonCode: "synthetic_policy", Authority: admission.AuthorityPolicy,
		RecordedAt: validationRecordedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := identity.NewResolutionProposal(identity.ResolutionProposalInput{
		ID: "proposal-extra", MentionID: "mention-extra", ReasonCode: "synthetic_proposal",
		EvidenceIDs: []evidence.EvidenceID{base.Evidence[0].Span.ID()},
		RecordedAt:  validationRecordedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	mutated := base
	mutated.Proposals = []identity.ResolutionProposal{proposal}
	mutated.AdmissionDecisions = []admission.Decision{decision}
	mutatedResolved, err := resolveCanonicalWriteSet(mutated)
	if err != nil {
		t.Fatal(err)
	}
	mutatedDigest := digestCanonicalWriteSet(mutated, mutatedResolved)

	if mutatedDigest == baseDigest {
		t.Fatal("canonical write-set digest ignored resolved proposal and admission payload")
	}
}
