package app

import (
	"context"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"

	"stacks/internal/cli"
	"stacks/internal/directory"
	"stacks/internal/entity"
)

func TestCanonicalProposalViewPreservesEvidenceAuthorityAndCandidateSource(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 26, 12, 34, 56, 123456000, time.UTC)
	proposal, err := identity.NewResolutionProposal(identity.ResolutionProposalInput{
		ID: "proposal-1", MentionID: "mention-1", ReasonCode: "review",
		EvidenceIDs: []evidence.EvidenceID{"evidence-1", "evidence-2"}, RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("construct proposal: %v", err)
	}
	person, err := identity.NewEntity(identity.EntityInput{
		ID: "person-1", Kind: identity.KindPerson, DisplayName: "Synthetic Person",
		RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("construct entity: %v", err)
	}
	candidate, err := identity.NewResolutionCandidate(identity.ResolutionCandidateInput{
		ID: "candidate-1", ProposalID: proposal.ID(), EntityID: person.ID(),
		Rank: 1, Confidence: 0.75, ReasonCode: "synthetic_candidate",
		Source: identity.CandidateSource{
			Kind: "synthetic_source", Reference: "opaque-reference-1",
		},
		RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("construct candidate: %v", err)
	}
	decision, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
		ID: "decision-1", ProposalID: proposal.ID(), Outcome: identity.DecisionAccepted,
		EntityID: person.ID(), Authority: identity.AuthorityReviewer,
		ReasonCode: "synthetic_decision", RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("construct decision: %v", err)
	}
	view := proposalView(postgres.ReviewerProposalRecord{
		Proposal: proposal,
		Evidence: []postgres.ReviewerEvidence{
			{ID: "evidence-1", Quote: "First citation"},
			{ID: "evidence-2", Quote: "Second citation"},
		},
		Candidates: []postgres.ReviewerCandidateRecord{{
			Candidate: candidate,
			Entity:    person,
		}},
		EffectiveDecision: &decision,
	})

	if len(view.Evidence) != 2 ||
		view.Evidence[0] != (cli.ReviewEvidence{ID: "evidence-1", Quote: "First citation"}) ||
		view.Evidence[1] != (cli.ReviewEvidence{ID: "evidence-2", Quote: "Second citation"}) ||
		view.Candidates[0].SourceKind != "synthetic_source" ||
		view.Candidates[0].SourceReference != "opaque-reference-1" ||
		view.EffectiveDecision == nil ||
		view.EffectiveDecision.Authority != string(identity.AuthorityReviewer) {
		t.Fatalf("proposal view = %#v, want complete ordered provenance", view)
	}
}

func TestCanonicalReviewCreationUsesInjectedIdentityAndClock(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 26, 12, 34, 56, 123456000, time.UTC)
	proposal, err := identity.NewResolutionProposal(identity.ResolutionProposalInput{
		ID: "proposal-1", MentionID: "mention-1", ReasonCode: "review",
		EvidenceIDs: []evidence.EvidenceID{"evidence-1"}, RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("construct proposal: %v", err)
	}
	mention, err := identity.NewMention(identity.MentionInput{
		ID: "mention-1", EvidenceID: "evidence-1", DerivationRunID: "run-1",
		Surface: "Source Person", NormalizedName: "source person",
		Role: "speaker", RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("construct mention: %v", err)
	}
	store := &fakeCanonicalReviewer{
		proposal: postgres.ReviewerProposalRecord{
			Proposal: proposal,
			Mention:  mention,
			Evidence: []postgres.ReviewerEvidence{{ID: "evidence-1", Quote: "Source Person"}},
		},
	}
	ids := &sequenceIDGenerator{values: []string{
		"entity-1", "decision-1", "alias-1", "alias-2",
	}}
	repository := NewReviewRepository(
		store,
		ids,
		ClockFunc(func() time.Time { return recordedAt }),
	)
	decision, err := repository.CreateReviewPerson(
		context.Background(),
		"proposal-1",
		cli.CreatePersonInput{Name: "Reviewer Person"},
		nil,
	)
	if err != nil {
		t.Fatalf("CreateReviewPerson() error = %v", err)
	}
	if decision.ID != "decision-1" || decision.EntityID != "entity-1" {
		t.Fatalf("decision = %#v, want injected IDs", decision)
	}
	if store.created.Entity.ID() != "entity-1" ||
		!store.created.Entity.RecordedAt().Equal(recordedAt) ||
		len(store.created.Aliases) != 2 {
		t.Fatalf("created command = %#v, want canonical entity and two aliases", store.created)
	}
}

func TestCanonicalReviewCreationUsesOneActionTimeAndPreservesProviderObservation(t *testing.T) {
	actionTime := time.Date(2026, time.July, 26, 15, 0, 0, 987654000, time.UTC)
	verificationTime := actionTime.Add(-time.Minute)
	observedAt := actionTime.Add(-24 * time.Hour)
	proposal, err := identity.NewResolutionProposal(identity.ResolutionProposalInput{
		ID: "proposal-time", MentionID: "mention-time", ReasonCode: "review",
		EvidenceIDs: []evidence.EvidenceID{"evidence-time"}, RecordedAt: verificationTime,
	})
	if err != nil {
		t.Fatalf("construct proposal: %v", err)
	}
	mention, err := identity.NewMention(identity.MentionInput{
		ID: "mention-time", EvidenceID: "evidence-time", DerivationRunID: "run-time",
		Surface: "Source Person", NormalizedName: "source person",
		Role: "speaker", RecordedAt: verificationTime,
	})
	if err != nil {
		t.Fatalf("construct mention: %v", err)
	}
	profile := entity.DirectoryProfile{
		Provider: "google_people", SubjectID: "people/synthetic-time",
		Source: entity.DirectorySourceDomainProfile, DisplayName: "Reviewer Person",
		Emails: []entity.DirectoryEmail{{
			Value: "reviewer@example.test", Primary: true,
		}},
		ObservedAt: observedAt,
	}
	verification := directory.ReviewerVerification{
		Query: entity.DirectoryQuery{
			Kind: entity.DirectoryQueryEmail, Email: "reviewer@example.test",
			EmailEvidence: entity.EmailEvidenceReviewerSupplied,
		},
		Lookup: directory.LookupResult{
			Provider: "google_people", Outcome: entity.DirectoryMatched,
			Profiles: []entity.DirectoryProfile{profile},
		},
		Evaluation: entity.DirectoryEvaluation{
			Outcome: entity.DirectoryMatched, CreatePerson: true,
			AcceptedEmail: "reviewer@example.test", Profile: &profile,
		},
		AttemptCount: 1,
		RecordedAt:   verificationTime,
	}
	store := &fakeCanonicalReviewer{
		proposal: postgres.ReviewerProposalRecord{
			Proposal: proposal, Mention: mention,
			Evidence: []postgres.ReviewerEvidence{{
				ID: "evidence-time", Quote: "Source Person",
			}},
		},
	}
	clock := &recordingClock{now: actionTime}
	repository := NewReviewRepository(
		store,
		&sequenceIDGenerator{values: []string{
			"entity-time", "decision-time", "alias-time-1",
			"alias-time-2", "alias-time-3",
		}},
		clock,
	)

	if _, err := repository.CreateReviewPerson(
		context.Background(),
		"proposal-time",
		cli.CreatePersonInput{
			Name: "Reviewer Person", Email: "reviewer@example.test",
		},
		&verification,
	); err != nil {
		t.Fatalf("CreateReviewPerson() error = %v", err)
	}
	command := store.created
	if clock.calls != 1 ||
		!command.Entity.RecordedAt().Equal(actionTime) ||
		!command.Decision.RecordedAt().Equal(actionTime) ||
		command.DirectoryEvidence == nil ||
		!command.DirectoryEvidence.RecordedAt.Equal(actionTime) {
		t.Fatalf(
			"action timestamps = clock:%d entity:%s decision:%s directory:%v, want one %s",
			clock.calls,
			command.Entity.RecordedAt(),
			command.Decision.RecordedAt(),
			command.DirectoryEvidence,
			actionTime,
		)
	}
	for _, alias := range command.Aliases {
		if !alias.RecordedAt().Equal(actionTime) {
			t.Fatalf("alias recorded_at = %s, want %s", alias.RecordedAt(), actionTime)
		}
	}
	if len(command.DirectoryEvidence.Lookup.Profiles) != 1 ||
		!command.DirectoryEvidence.Lookup.Profiles[0].ObservedAt.Equal(observedAt) ||
		command.DirectoryEvidence.Evaluation.Profile == nil ||
		!command.DirectoryEvidence.Evaluation.Profile.ObservedAt.Equal(observedAt) {
		t.Fatalf(
			"provider observations = %#v/%#v, want preserved %s",
			command.DirectoryEvidence.Lookup.Profiles,
			command.DirectoryEvidence.Evaluation.Profile,
			observedAt,
		)
	}
}

func TestCanonicalReviewCreationRebasesRetryWindowToActionTime(t *testing.T) {
	actionTime := time.Date(2026, time.July, 26, 18, 0, 0, 0, time.UTC)
	verificationTime := actionTime.Add(-2 * time.Hour)
	originalRetryAfter := verificationTime.Add(30 * time.Second)
	proposal, err := identity.NewResolutionProposal(identity.ResolutionProposalInput{
		ID: "proposal-retry", MentionID: "mention-retry", ReasonCode: "review",
		EvidenceIDs: []evidence.EvidenceID{"evidence-retry"}, RecordedAt: verificationTime,
	})
	if err != nil {
		t.Fatalf("construct proposal: %v", err)
	}
	mention, err := identity.NewMention(identity.MentionInput{
		ID: "mention-retry", EvidenceID: "evidence-retry", DerivationRunID: "run-retry",
		Surface: "Retry Person", NormalizedName: "retry person",
		Role: "speaker", RecordedAt: verificationTime,
	})
	if err != nil {
		t.Fatalf("construct mention: %v", err)
	}
	store := &fakeCanonicalReviewer{
		proposal: postgres.ReviewerProposalRecord{
			Proposal: proposal, Mention: mention,
			Evidence: []postgres.ReviewerEvidence{{
				ID: "evidence-retry", Quote: "Retry Person",
			}},
		},
	}
	verification := directory.ReviewerVerification{
		Query: entity.DirectoryQuery{
			Kind: entity.DirectoryQueryEmail, Email: "retry@example.test",
			EmailEvidence: entity.EmailEvidenceReviewerSupplied,
		},
		Lookup: directory.LookupResult{
			Provider: "google_people", Outcome: entity.DirectoryUnavailable,
		},
		Evaluation:   entity.DirectoryEvaluation{Outcome: entity.DirectoryUnavailable},
		AttemptCount: 1,
		RecordedAt:   verificationTime,
		RetryAfter:   &originalRetryAfter,
	}
	repository := NewReviewRepository(
		store,
		&sequenceIDGenerator{values: []string{
			"entity-retry", "decision-retry", "alias-retry-1",
			"alias-retry-2", "alias-retry-3",
		}},
		&recordingClock{now: actionTime},
	)

	if _, err := repository.CreateReviewPerson(
		context.Background(),
		"proposal-retry",
		cli.CreatePersonInput{Name: "Retry Person", Email: "retry@example.test"},
		&verification,
	); err != nil {
		t.Fatalf("CreateReviewPerson() error = %v", err)
	}
	if store.created.DirectoryEvidence == nil ||
		store.created.DirectoryEvidence.RetryAfter == nil {
		t.Fatalf("directory evidence = %#v, want additive retry metadata", store.created.DirectoryEvidence)
	}
	wantRetryAfter := actionTime.Add(30 * time.Second)
	if !store.created.DirectoryEvidence.RecordedAt.Equal(actionTime) ||
		!store.created.DirectoryEvidence.RetryAfter.Equal(wantRetryAfter) {
		t.Fatalf(
			"directory retry window = recorded:%s retry:%s, want %s/%s",
			store.created.DirectoryEvidence.RecordedAt,
			*store.created.DirectoryEvidence.RetryAfter,
			actionTime,
			wantRetryAfter,
		)
	}
}

func TestCanonicalReviewCreationOmitsMalformedOptionalDirectoryEvidence(t *testing.T) {
	actionTime := time.Date(2026, time.July, 26, 18, 0, 0, 0, time.UTC)
	proposal, err := identity.NewResolutionProposal(identity.ResolutionProposalInput{
		ID: "proposal-malformed", MentionID: "mention-malformed", ReasonCode: "review",
		EvidenceIDs: []evidence.EvidenceID{"evidence-malformed"}, RecordedAt: actionTime,
	})
	if err != nil {
		t.Fatalf("construct proposal: %v", err)
	}
	mention, err := identity.NewMention(identity.MentionInput{
		ID: "mention-malformed", EvidenceID: "evidence-malformed",
		DerivationRunID: "run-malformed", Surface: "Reviewer Person",
		NormalizedName: "reviewer person", ProposedEmail: "reviewer@example.test",
		ProposedEmailEvidenceID: "evidence-malformed", Role: "speaker",
		RecordedAt: actionTime,
	})
	if err != nil {
		t.Fatalf("construct mention: %v", err)
	}
	profile := entity.DirectoryProfile{
		Provider: "google_people", SubjectID: "people/malformed",
		Source: entity.DirectorySourceDomainProfile, DisplayName: "Reviewer Person",
		Emails: []entity.DirectoryEmail{{
			Value: "reviewer@example.test", Primary: true,
		}},
		ObservedAt: actionTime.Add(-time.Hour),
	}
	base := directory.ReviewerVerification{
		Query: entity.DirectoryQuery{
			Kind: entity.DirectoryQueryEmail, Email: "reviewer@example.test",
			EmailEvidence: entity.EmailEvidenceReviewerSupplied,
		},
		Lookup: directory.LookupResult{
			Provider: "google_people", Outcome: entity.DirectoryReview,
			Profiles: []entity.DirectoryProfile{profile},
		},
		Evaluation: entity.DirectoryEvaluation{
			Outcome:    entity.DirectoryReview,
			Candidates: []entity.DirectoryProfile{profile},
		},
		AttemptCount: 1,
		RecordedAt:   actionTime.Add(-time.Minute),
	}
	tests := []struct {
		name   string
		mutate func(*directory.ReviewerVerification)
	}{
		{
			name: "malformed evaluation candidate",
			mutate: func(value *directory.ReviewerVerification) {
				value.Evaluation.Candidates = append(
					[]entity.DirectoryProfile(nil),
					value.Evaluation.Candidates...,
				)
				value.Evaluation.Candidates[0].SubjectID = ""
			},
		},
		{
			name: "noncanonical lookup observation time",
			mutate: func(value *directory.ReviewerVerification) {
				value.Lookup.Profiles = append(
					[]entity.DirectoryProfile(nil),
					value.Lookup.Profiles...,
				)
				value.Lookup.Profiles[0].ObservedAt =
					value.Lookup.Profiles[0].ObservedAt.Add(time.Nanosecond)
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			verification := base
			testCase.mutate(&verification)
			store := &fakeCanonicalReviewer{
				proposal: postgres.ReviewerProposalRecord{
					Proposal: proposal, Mention: mention,
					Evidence: []postgres.ReviewerEvidence{{
						ID: "evidence-malformed", Quote: "Reviewer Person",
					}},
				},
			}
			repository := NewReviewRepository(
				store,
				&sequenceIDGenerator{values: []string{
					"entity-malformed", "decision-malformed",
					"alias-malformed-1", "alias-malformed-2",
				}},
				&recordingClock{now: actionTime},
			)
			result, err := repository.CreateReviewPerson(
				context.Background(),
				"proposal-malformed",
				cli.CreatePersonInput{
					Name: "Reviewer Person", Email: "reviewer@example.test",
				},
				&verification,
			)
			if err != nil {
				t.Fatalf("CreateReviewPerson() error = %v", err)
			}
			if result.ID == "" || store.created.DirectoryEvidence != nil {
				t.Fatalf(
					"review result/evidence = %#v/%#v, want committed decision without optional evidence",
					result,
					store.created.DirectoryEvidence,
				)
			}
		})
	}
}

type fakeCanonicalReviewer struct {
	proposal postgres.ReviewerProposalRecord
	created  postgres.ReviewerCreatePersonCommand
}

func (store *fakeCanonicalReviewer) ListEntities(context.Context) ([]postgres.ReviewerEntityRecord, error) {
	return nil, nil
}
func (store *fakeCanonicalReviewer) LoadEntity(context.Context, identity.EntityID) (postgres.ReviewerEntityRecord, error) {
	return postgres.ReviewerEntityRecord{}, nil
}
func (store *fakeCanonicalReviewer) ListProposals(context.Context) ([]postgres.ReviewerProposalRecord, error) {
	return nil, nil
}
func (store *fakeCanonicalReviewer) LoadProposal(context.Context, identity.ProposalID) (postgres.ReviewerProposalRecord, error) {
	return store.proposal, nil
}
func (store *fakeCanonicalReviewer) LoadDecision(context.Context, identity.DecisionID) (identity.ResolutionDecision, error) {
	return identity.ResolutionDecision{}, nil
}
func (store *fakeCanonicalReviewer) AppendDecision(context.Context, postgres.ReviewerDecisionCommand) (identity.ResolutionDecision, error) {
	return identity.ResolutionDecision{}, nil
}
func (store *fakeCanonicalReviewer) CreatePerson(_ context.Context, command postgres.ReviewerCreatePersonCommand) (identity.ResolutionDecision, error) {
	store.created = command
	return command.Decision, nil
}
func (store *fakeCanonicalReviewer) AcceptDirectoryCandidate(context.Context, postgres.ReviewerDirectoryDecisionCommand) (identity.ResolutionDecision, error) {
	return identity.ResolutionDecision{}, nil
}

type sequenceIDGenerator struct {
	values []string
	index  int
}

type recordingClock struct {
	now   time.Time
	calls int
}

func (clock *recordingClock) Now() time.Time {
	clock.calls++
	return clock.now
}

func (generator *sequenceIDGenerator) NewID() string {
	value := generator.values[generator.index]
	generator.index++
	return value
}
