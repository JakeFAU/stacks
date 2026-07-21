package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReviewCommandAcceptRequiresExplicitProposalID(t *testing.T) {
	command := ReviewCommand{Service: &ReviewService{Store: &fakeReviewStore{}}}
	err := command.Run(context.Background(), []string{"accept"})
	if err == nil || !strings.Contains(err.Error(), "proposal ID") {
		t.Fatalf("Run() error = %v, want explicit proposal ID error", err)
	}
}

func TestReviewServiceCorrectDelegatesAppendOnlyReplacement(t *testing.T) {
	store := &fakeReviewStore{}
	service := ReviewService{Store: store}

	decision, err := service.Correct(context.Background(), "decision-1", "person-2")
	if err != nil {
		t.Fatalf("Correct() error = %v", err)
	}
	if decision.SupersedesID != "decision-1" {
		t.Fatalf("SupersedesID = %q, want %q", decision.SupersedesID, "decision-1")
	}
	if store.correctedID != "decision-1" || store.correctedEntityID != "person-2" {
		t.Fatalf("correct call = (%q, %q), want (decision-1, person-2)", store.correctedID, store.correctedEntityID)
	}
}

func TestReviewCommandWritesPrivateProposalContextOnlyToStdout(t *testing.T) {
	const privateContext = "Synthetic confidential transcript context"
	store := &fakeReviewStore{proposal: ReviewProposal{ID: "proposal-1", Context: privateContext}}
	var stdout strings.Builder
	command := ReviewCommand{Service: &ReviewService{Store: store}, Output: &stdout}

	if err := command.Run(context.Background(), []string{"show", "proposal-1"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stdout.String(), privateContext) {
		t.Fatalf("stdout = %q, want private review context", stdout.String())
	}
	if strings.Contains(store.logMessages.String(), privateContext) {
		t.Fatalf("logger received private context: %q", store.logMessages.String())
	}
}

func TestReviewServicePropagatesStaleEffectiveDecisionFailure(t *testing.T) {
	store := &fakeReviewStore{correctErr: errors.New("decision is not effective")}
	service := ReviewService{Store: store}

	_, err := service.Correct(context.Background(), "decision-1", "person-2")
	if err == nil || !strings.Contains(err.Error(), "not effective") {
		t.Fatalf("Correct() error = %v, want stale decision failure", err)
	}
}

type fakeReviewStore struct {
	proposal          ReviewProposal
	correctedID       string
	correctedEntityID string
	correctErr        error
	logMessages       strings.Builder
}

func (store *fakeReviewStore) ListReviewProposals(context.Context) ([]ReviewProposal, error) {
	return []ReviewProposal{store.proposal}, nil
}

func (store *fakeReviewStore) ShowReviewProposal(context.Context, string) (ReviewProposal, error) {
	return store.proposal, nil
}

func (store *fakeReviewStore) AcceptReviewProposal(context.Context, string, string) (ReviewDecision, error) {
	return ReviewDecision{}, nil
}

func (store *fakeReviewStore) RejectReviewProposal(context.Context, string) (ReviewDecision, error) {
	return ReviewDecision{}, nil
}

func (store *fakeReviewStore) CreateReviewPerson(context.Context, string, CreatePersonInput) (ReviewDecision, error) {
	return ReviewDecision{}, nil
}

func (store *fakeReviewStore) CorrectReviewDecision(_ context.Context, decisionID, entityID string) (ReviewDecision, error) {
	store.correctedID = decisionID
	store.correctedEntityID = entityID
	if store.correctErr != nil {
		return ReviewDecision{}, store.correctErr
	}
	return ReviewDecision{ID: "decision-2", SupersedesID: decisionID, EntityID: entityID}, nil
}
