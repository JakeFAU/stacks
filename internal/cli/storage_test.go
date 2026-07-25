package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stacks/internal/directory"
	"stacks/internal/entity"
	"stacks/internal/storage"
)

func TestStorageReviewStoreRejectsTransitionsWithoutRepository(t *testing.T) {
	store := NewStorageReviewStore(nil)
	cases := []struct {
		name string
		call func() error
	}{
		{name: "accept", call: func() error {
			_, err := store.AcceptReviewProposal(context.Background(), "proposal-1", "person-1")
			return err
		}},
		{name: "reject", call: func() error { _, err := store.RejectReviewProposal(context.Background(), "proposal-1"); return err }},
		{name: "correct", call: func() error {
			_, err := store.CorrectReviewDecision(context.Background(), "decision-1", "person-1")
			return err
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.call()
			if err == nil || !strings.Contains(err.Error(), "repository is not configured") {
				t.Fatalf("transition error = %v, want repository configuration error", err)
			}
		})
	}
}

func TestReviewProposalFromStoragePreservesOnlyBoundedDirectoryProjection(t *testing.T) {
	detail := storage.ResolutionProposalDetail{
		ID:      "proposal-directory",
		Context: "Synthetic cited context",
		Candidates: []storage.ResolutionCandidateDetail{{
			DirectoryProfileID: "profile-directory",
			DisplayName:        "Synthetic Directory Person",
			MaskedEmail:        "r***@corp.example",
			Source:             "domain_profile",
			Reason:             "directory name candidate requires review",
		}},
	}

	proposal := reviewProposalFromStorage(detail)
	if len(proposal.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(proposal.Candidates))
	}
	candidate := proposal.Candidates[0]
	if candidate.DirectoryProfileID != "profile-directory" ||
		candidate.DisplayName != "Synthetic Directory Person" ||
		candidate.MaskedEmail != "r***@corp.example" ||
		candidate.Source != "domain_profile" {
		t.Fatalf("directory candidate = %#v, want bounded projection", candidate)
	}
}

func TestReviewerEmailVerificationIsAdditive(t *testing.T) {
	verification := directory.ReviewerVerification{
		Query: entity.DirectoryQuery{
			Kind:          entity.DirectoryQueryEmail,
			Email:         "reviewer@corp.example",
			EmailEvidence: entity.EmailEvidenceReviewerSupplied,
		},
	}
	verifier := &fakeReviewerEmailVerifier{verification: verification}
	got, err := verifyReviewerEmail(
		context.Background(),
		verifier,
		"reviewer@corp.example",
	)
	if err != nil {
		t.Fatalf("verifyReviewerEmail() error = %v", err)
	}
	if got == nil || got.Query != verification.Query ||
		verifier.calls != 1 ||
		verifier.email != "reviewer@corp.example" {
		t.Fatalf("reviewer verification = %#v calls/email = %d/%q, want successful additive metadata", got, verifier.calls, verifier.email)
	}

	verifier.err = errors.New("synthetic private provider failure")
	got, err = verifyReviewerEmail(
		context.Background(),
		verifier,
		"reviewer@corp.example",
	)
	if err != nil || got != nil {
		t.Fatalf("provider failure result = %#v, %v; want ignored additive failure", got, err)
	}

	verifier.err = nil
	verifier.verification = directory.ReviewerVerification{
		Evaluation: entity.DirectoryEvaluation{Outcome: entity.DirectoryDisabled},
	}
	got, err = verifyReviewerEmail(
		context.Background(),
		verifier,
		"reviewer@corp.example",
	)
	if err != nil || got != nil {
		t.Fatalf("disabled verification result = %#v, %v; want explicit review decision preserved", got, err)
	}
}

type fakeReviewerEmailVerifier struct {
	verification directory.ReviewerVerification
	err          error
	calls        int
	email        string
}

func (verifier *fakeReviewerEmailVerifier) VerifyReviewerEmail(
	_ context.Context,
	email string,
) (directory.ReviewerVerification, error) {
	verifier.calls++
	verifier.email = email
	return verifier.verification, verifier.err
}
