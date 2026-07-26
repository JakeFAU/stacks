package cli

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stacks/internal/directory"
	"stacks/internal/entity"
)

func TestCanonicalReviewStoreRejectsTransitionsWithoutRepository(t *testing.T) {
	store := NewCanonicalReviewStore(nil)
	cases := []struct {
		name string
		call func() error
	}{
		{name: "accept", call: func() error {
			_, err := store.AcceptReviewProposal(context.Background(), "proposal-1", "person-1")
			return err
		}},
		{name: "reject", call: func() error {
			_, err := store.RejectReviewProposal(context.Background(), "proposal-1")
			return err
		}},
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

func TestReviewerEmailVerificationIsAdditive(t *testing.T) {
	malformed := directory.ReviewerVerification{
		Query: entity.DirectoryQuery{
			Kind:          entity.DirectoryQueryEmail,
			Email:         "reviewer@corp.example",
			EmailEvidence: entity.EmailEvidenceReviewerSupplied,
		},
	}
	verifier := &fakeReviewerEmailVerifier{verification: malformed}
	got, err := verifyReviewerEmail(
		context.Background(),
		verifier,
		"reviewer@corp.example",
	)
	if err != nil {
		t.Fatalf("verifyReviewerEmail() error = %v", err)
	}
	if got != nil || verifier.calls != 1 {
		t.Fatalf("malformed reviewer verification = %#v calls = %d, want omitted metadata", got, verifier.calls)
	}

	verification := directory.ReviewerVerification{
		Query: malformed.Query,
		Lookup: directory.LookupResult{
			Provider: "google_people",
			Outcome:  entity.DirectoryNoMatch,
		},
		Evaluation: entity.DirectoryEvaluation{
			Outcome: entity.DirectoryNoMatch,
		},
		AttemptCount: 1,
		RecordedAt:   time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC),
	}
	verifier.verification = verification
	got, err = verifyReviewerEmail(
		context.Background(),
		verifier,
		"reviewer@corp.example",
	)
	if err != nil || got == nil || !reflect.DeepEqual(*got, verification) {
		t.Fatalf("valid reviewer verification = %#v, %v; want complete additive metadata", got, err)
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
}

type fakeReviewerEmailVerifier struct {
	verification directory.ReviewerVerification
	err          error
	calls        int
}

func (verifier *fakeReviewerEmailVerifier) VerifyReviewerEmail(
	context.Context,
	string,
) (directory.ReviewerVerification, error) {
	verifier.calls++
	return verifier.verification, verifier.err
}
