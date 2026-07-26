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

func TestReviewCommandAcceptDirectoryRequiresExactProposalAndSnapshotIDs(t *testing.T) {
	store := &fakeReviewStore{proposal: ReviewProposal{
		ID: "proposal-directory",
		Candidates: []ReviewCandidate{{
			DirectoryProfileID: "profile-directory",
		}},
	}}
	command := ReviewCommand{Service: &ReviewService{Store: store}}

	for _, args := range [][]string{
		{"accept-directory"},
		{"accept-directory", "proposal-directory"},
	} {
		err := command.Run(context.Background(), args)
		if err == nil ||
			!strings.Contains(err.Error(), "proposal ID") ||
			!strings.Contains(err.Error(), "directory profile ID") {
			t.Fatalf("Run(%q) error = %v, want exact proposal/profile ID error", args, err)
		}
	}
	if store.acceptedDirectory != (AcceptDirectoryInput{}) {
		t.Fatalf("implicit directory acceptance = %#v, want no transition", store.acceptedDirectory)
	}
}

func TestReviewCommandAcceptDirectoryParsesOptionalExistingEntity(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
		want AcceptDirectoryInput
	}{
		{
			name: "create person",
			args: []string{"accept-directory", "proposal-directory", "profile-directory"},
			want: AcceptDirectoryInput{
				ProposalID:         "proposal-directory",
				DirectoryProfileID: "profile-directory",
			},
		},
		{
			name: "existing person",
			args: []string{
				"accept-directory",
				"proposal-directory",
				"profile-directory",
				"--entity",
				"person-existing",
			},
			want: AcceptDirectoryInput{
				ProposalID:         "proposal-directory",
				DirectoryProfileID: "profile-directory",
				EntityID:           "person-existing",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := &fakeReviewStore{}
			command := ReviewCommand{Service: &ReviewService{Store: store}}
			if err := command.Run(context.Background(), testCase.args); err != nil {
				t.Fatalf("Run(%q) error = %v", testCase.args, err)
			}
			if store.acceptedDirectory != testCase.want {
				t.Fatalf("accept-directory input = %#v, want %#v", store.acceptedDirectory, testCase.want)
			}
		})
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
	store := &fakeReviewStore{proposal: ReviewProposal{
		ID:       "proposal-1",
		Evidence: []ReviewEvidence{{ID: "evidence-1", Quote: privateContext}},
	}}
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

func TestReviewCommandShowsOrderedProvenanceAndEffectiveAuthority(t *testing.T) {
	confidence := 0.75
	store := &fakeReviewStore{proposal: ReviewProposal{
		ID: "proposal-provenance",
		Evidence: []ReviewEvidence{
			{ID: "evidence-1", Quote: "First cited span"},
			{ID: "evidence-2", Quote: "Second cited span"},
		},
		Candidates: []ReviewCandidate{{
			EntityID:        "person-1",
			DisplayName:     "Synthetic Person",
			SourceKind:      "accepted_alias",
			SourceReference: "opaque-alias-1",
			Confidence:      &confidence,
			Reason:          "synthetic candidate",
		}},
		EffectiveDecision: &ReviewDecision{
			ID: "decision-1", ProposalID: "proposal-provenance",
			EntityID: "person-1", Outcome: "accepted", Authority: "reviewer",
		},
	}}
	var stdout strings.Builder
	command := ReviewCommand{Service: &ReviewService{Store: store}, Output: &stdout}

	if err := command.Run(context.Background(), []string{"show", "proposal-provenance"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"evidence evidence-1: First cited span",
		"evidence evidence-2: Second cited span",
		"effective decision: decision-1 outcome: accepted entity: person-1 authority: reviewer",
		"source-kind: accepted_alias source-ref: opaque-alias-1",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, want %q", output, want)
		}
	}
	if strings.Index(output, "evidence evidence-1") > strings.Index(output, "evidence evidence-2") {
		t.Fatalf("stdout evidence order = %q, want proposal order", output)
	}
}

func TestReviewCommandListRendersCompactHighestRankedGuessAndContext(t *testing.T) {
	confidence := 0.8125
	store := &fakeReviewStore{proposal: ReviewProposal{
		ID: "proposal-1",
		Evidence: []ReviewEvidence{{
			ID: "evidence-1", Quote: "Synthetic transcript context",
		}},
		Candidates: []ReviewCandidate{
			{EntityID: "person-primary", Confidence: &confidence, Reason: "exact accepted alias similarity"},
			{EntityID: "person-alternative", Reason: "weaker name similarity"},
		},
	}}
	var stdout strings.Builder
	command := ReviewCommand{Service: &ReviewService{Store: store}, Output: &stdout}

	if err := command.Run(context.Background(), []string{"list"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "proposal-1 | guess=person-primary | confidence=0.812 | alternatives=1 | reason=\"exact accepted alias similarity\" | context=\"Synthetic transcript context\"\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestReviewCommandListBoundsPrivateTextToOneLine(t *testing.T) {
	store := &fakeReviewStore{proposal: ReviewProposal{
		ID: "proposal-1",
		Evidence: []ReviewEvidence{{
			ID: "evidence-1",
			Quote: "first line\n" + strings.Repeat("private context ", 30) +
				"unbounded-context-suffix",
		}},
		Candidates: []ReviewCandidate{{
			EntityID: "person-primary",
			Reason:   strings.Repeat("bounded reason ", 30) + "unbounded-reason-suffix",
		}},
	}}
	var stdout strings.Builder
	command := ReviewCommand{Service: &ReviewService{Store: store}, Output: &stdout}

	if err := command.Run(context.Background(), []string{"list"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	line := stdout.String()
	if strings.Count(line, "\n") != 1 || strings.Contains(line, "unbounded-context-suffix") || strings.Contains(line, "unbounded-reason-suffix") {
		t.Fatalf("stdout = %q, want one bounded line without private suffixes", line)
	}
	if !strings.Contains(line, `confidence=unknown | alternatives=0`) || !strings.Contains(line, `..."`) {
		t.Fatalf("stdout = %q, want explicit unknown confidence, alternative count, and truncation marker", line)
	}
}

func TestReviewCommandDirectoryCandidateListIsMaskedAndShowHasRequestedDetail(t *testing.T) {
	const (
		providerSubject  = "people/private-subject"
		rawProviderError = "synthetic raw provider failure"
	)
	confidence := 0.75
	store := &fakeReviewStore{proposal: ReviewProposal{
		ID: "proposal-directory",
		Evidence: []ReviewEvidence{{
			ID: "evidence-directory", Quote: "Synthetic cited review context",
		}},
		Candidates: []ReviewCandidate{{
			DirectoryProfileID: "profile-directory",
			DisplayName:        "Synthetic Directory Person",
			MaskedEmail:        "r***@corp.example",
			DirectorySource:    "domain_profile",
			Confidence:         &confidence,
			Reason:             "directory name candidate requires review",
		}},
	}}

	var listOutput strings.Builder
	listCommand := ReviewCommand{
		Service: &ReviewService{Store: store},
		Output:  &listOutput,
	}
	if err := listCommand.Run(context.Background(), []string{"list"}); err != nil {
		t.Fatalf("list Run() error = %v", err)
	}
	listed := listOutput.String()
	for _, want := range []string{
		"guess=profile-directory",
		`display="Synthetic Directory Person"`,
		`email="r***@corp.example"`,
		"source=domain_profile",
	} {
		if !strings.Contains(listed, want) {
			t.Fatalf("list stdout = %q, want %q", listed, want)
		}
	}
	for _, private := range []string{"riya@corp.example", providerSubject, rawProviderError} {
		if strings.Contains(listed, private) {
			t.Fatalf("list stdout = %q, must not contain %q", listed, private)
		}
	}

	var showOutput strings.Builder
	showCommand := ReviewCommand{
		Service: &ReviewService{Store: store},
		Output:  &showOutput,
	}
	if err := showCommand.Run(context.Background(), []string{"show", "proposal-directory"}); err != nil {
		t.Fatalf("show Run() error = %v", err)
	}
	shown := showOutput.String()
	for _, want := range []string{
		"profile-directory",
		"Synthetic Directory Person",
		"r***@corp.example",
		"domain_profile",
		"Synthetic cited review context",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("show stdout = %q, want %q", shown, want)
		}
	}
	for _, private := range []string{providerSubject, rawProviderError} {
		if strings.Contains(shown, private) {
			t.Fatalf("show stdout = %q, must not contain %q", shown, private)
		}
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
	acceptedDirectory AcceptDirectoryInput
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
func (store *statefulReviewStore) AcceptDirectoryCandidate(_ context.Context, input AcceptDirectoryInput) (ReviewDecision, error) {
	if _, exists := store.effective[input.ProposalID]; exists {
		return ReviewDecision{}, errors.New("proposal already has an effective decision")
	}
	store.actions = append(store.actions, "accept-directory:"+input.ProposalID+":"+input.DirectoryProfileID+":"+input.EntityID)
	return store.record(input.ProposalID, input.EntityID, "accepted", ""), nil
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

func (store *fakeReviewStore) AcceptDirectoryCandidate(_ context.Context, input AcceptDirectoryInput) (ReviewDecision, error) {
	store.acceptedDirectory = input
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
