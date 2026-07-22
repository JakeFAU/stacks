package cli

import (
	"context"
	"errors"
	"reflect"
	"strconv"
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

func TestReviewCommandAppliesEveryReviewTransitionAndSurfacesCurrentStateFailures(t *testing.T) {
	store := newStatefulReviewStore()
	command := ReviewCommand{Service: &ReviewService{Store: store}}

	for _, args := range [][]string{
		{"accept", "proposal-1", "person-1"},
		{"reject", "proposal-2"},
		{"create", "proposal-3", "--name", "Synthetic Person", "--email", "person@example.test"},
		{"correct", "decision-1", "person-2"},
	} {
		if err := command.Run(context.Background(), args); err != nil {
			t.Fatalf("Run(%q) error = %v", args, err)
		}
	}
	if err := command.Run(context.Background(), []string{"accept", "proposal-1", "person-1"}); err == nil || !strings.Contains(err.Error(), "effective decision") {
		t.Fatalf("repeat accept error = %v, want effective-state error", err)
	}
	if err := command.Run(context.Background(), []string{"correct", "decision-1", "person-1"}); err == nil || !strings.Contains(err.Error(), "not effective") {
		t.Fatalf("stale correction error = %v, want stale-state error", err)
	}
	if got, want := store.actions, []string{
		"accept:proposal-1:person-1",
		"reject:proposal-2",
		"create:proposal-3:Synthetic Person:person@example.test",
		"correct:decision-1:person-2",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("actions = %#v, want %#v", got, want)
	}
}

type fakeReviewStore struct {
	proposal          ReviewProposal
	correctedID       string
	correctedEntityID string
	correctErr        error
	lastCall          string
	logMessages       strings.Builder
}

func (store *fakeReviewStore) ListReviewProposals(context.Context) ([]ReviewProposal, error) {
	return []ReviewProposal{store.proposal}, nil
}

func (store *fakeReviewStore) ShowReviewProposal(context.Context, string) (ReviewProposal, error) {
	return store.proposal, nil
}

func (store *fakeReviewStore) AcceptReviewProposal(_ context.Context, proposalID, entityID string) (ReviewDecision, error) {
	store.lastCall = "accept:" + proposalID + ":" + entityID
	return ReviewDecision{}, nil
}

type statefulReviewStore struct {
	effective map[string]string
	decisions map[string]string
	actions   []string
}

func newStatefulReviewStore() *statefulReviewStore {
	return &statefulReviewStore{effective: make(map[string]string), decisions: make(map[string]string)}
}

func (store *statefulReviewStore) ListReviewProposals(context.Context) ([]ReviewProposal, error) {
	return nil, nil
}
func (store *statefulReviewStore) ShowReviewProposal(context.Context, string) (ReviewProposal, error) {
	return ReviewProposal{}, nil
}
func (store *statefulReviewStore) AcceptReviewProposal(_ context.Context, proposalID, entityID string) (ReviewDecision, error) {
	if _, exists := store.effective[proposalID]; exists {
		return ReviewDecision{}, errors.New("proposal already has an effective decision")
	}
	store.actions = append(store.actions, "accept:"+proposalID+":"+entityID)
	return store.record(proposalID, entityID, "accepted", ""), nil
}
func (store *statefulReviewStore) RejectReviewProposal(_ context.Context, proposalID string) (ReviewDecision, error) {
	if _, exists := store.effective[proposalID]; exists {
		return ReviewDecision{}, errors.New("proposal already has an effective decision")
	}
	store.actions = append(store.actions, "reject:"+proposalID)
	return store.record(proposalID, "", "rejected", ""), nil
}
func (store *statefulReviewStore) CreateReviewPerson(_ context.Context, proposalID string, input CreatePersonInput) (ReviewDecision, error) {
	if _, exists := store.effective[proposalID]; exists {
		return ReviewDecision{}, errors.New("proposal already has an effective decision")
	}
	store.actions = append(store.actions, "create:"+proposalID+":"+input.Name+":"+input.Email)
	return store.record(proposalID, "created-person", "created", ""), nil
}
func (store *statefulReviewStore) CorrectReviewDecision(_ context.Context, decisionID, entityID string) (ReviewDecision, error) {
	proposalID, exists := store.decisions[decisionID]
	if !exists || store.effective[proposalID] != decisionID {
		return ReviewDecision{}, errors.New("decision is not effective")
	}
	store.actions = append(store.actions, "correct:"+decisionID+":"+entityID)
	return store.record(proposalID, entityID, "accepted", decisionID), nil
}
func (store *statefulReviewStore) record(proposalID, entityID, outcome, supersedesID string) ReviewDecision {
	decisionID := "decision-" + strconv.Itoa(len(store.decisions)+1)
	store.decisions[decisionID] = proposalID
	store.effective[proposalID] = decisionID
	return ReviewDecision{ID: decisionID, ProposalID: proposalID, EntityID: entityID, Outcome: outcome, SupersedesID: supersedesID}
}

func (store *fakeReviewStore) RejectReviewProposal(_ context.Context, proposalID string) (ReviewDecision, error) {
	store.lastCall = "reject:" + proposalID
	return ReviewDecision{}, nil
}

func (store *fakeReviewStore) CreateReviewPerson(_ context.Context, proposalID string, input CreatePersonInput) (ReviewDecision, error) {
	store.lastCall = "create:" + proposalID + ":" + input.Name + ":" + input.Email
	return ReviewDecision{}, nil
}

func (store *fakeReviewStore) CorrectReviewDecision(_ context.Context, decisionID, entityID string) (ReviewDecision, error) {
	store.lastCall = "correct:" + decisionID + ":" + entityID
	store.correctedID = decisionID
	store.correctedEntityID = entityID
	if store.correctErr != nil {
		return ReviewDecision{}, store.correctErr
	}
	return ReviewDecision{ID: "decision-2", SupersedesID: decisionID, EntityID: entityID}, nil
}
