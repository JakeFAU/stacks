package storage

import (
	"strings"
	"testing"
	"time"

	"stacks/internal/directory"
	"stacks/internal/entity"
)

func TestDirectoryProfileDigestIgnoresProviderOrder(t *testing.T) {
	recorded := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	first := entity.DirectoryProfile{
		Provider:    "google_people",
		SubjectID:   "synthetic-subject",
		Source:      entity.DirectorySourceDomainProfile,
		DisplayName: "Synthetic Person",
		Emails: []entity.DirectoryEmail{
			{Value: "synthetic.person@synthetic.example", Primary: true},
			{Value: "alternate@synthetic.example"},
		},
		ObservedAt: recorded,
	}
	reordered := first
	reordered.Emails = []entity.DirectoryEmail{first.Emails[1], first.Emails[0]}

	firstDigest, err := directoryProfileDigest(first)
	if err != nil {
		t.Fatalf("directoryProfileDigest(first) error = %v", err)
	}
	reorderedDigest, err := directoryProfileDigest(reordered)
	if err != nil {
		t.Fatalf("directoryProfileDigest(reordered) error = %v", err)
	}
	if firstDigest != reorderedDigest {
		t.Fatal("directory profile digest changed with provider email order")
	}
}

func TestDirectoryLookupDigestIncludesMentionQueryPolicyAndOutcome(t *testing.T) {
	recorded := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	profile := entity.DirectoryProfile{
		Provider:    "google_people",
		SubjectID:   "synthetic-subject",
		Source:      entity.DirectorySourceDomainProfile,
		DisplayName: "Synthetic Person",
		Emails: []entity.DirectoryEmail{{
			Value: "synthetic.person@synthetic.example", Primary: true,
		}},
		ObservedAt: recorded,
	}
	base := directory.PersistInput{
		Mention: directory.PendingMention{
			MentionID:  "11111111-2222-3333-4444-555555555555",
			ProposalID: "66666666-7777-8888-9999-aaaaaaaaaaaa",
		},
		Query: entity.DirectoryQuery{
			Kind:          entity.DirectoryQueryEmail,
			Email:         "synthetic.person@synthetic.example",
			EmailEvidence: entity.EmailEvidenceSourceBound,
		},
		Lookup: directory.LookupResult{
			Outcome:  entity.DirectoryMatched,
			Profiles: []entity.DirectoryProfile{profile},
		},
		Evaluation: entity.DirectoryEvaluation{
			Outcome:       entity.DirectoryMatched,
			CreatePerson:  true,
			AcceptedEmail: "synthetic.person@synthetic.example",
			Profile:       &profile,
		},
		AttemptCount: 1,
		RecordedAt:   recorded,
	}
	baseline, err := directoryLookupDigest(base, entity.DirectoryPolicyVersion)
	if err != nil {
		t.Fatalf("directoryLookupDigest(base) error = %v", err)
	}

	tests := []struct {
		name          string
		input         directory.PersistInput
		policyVersion string
	}{
		{
			name: "mention",
			input: func() directory.PersistInput {
				changed := base
				changed.Mention.MentionID = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
				return changed
			}(),
			policyVersion: entity.DirectoryPolicyVersion,
		},
		{
			name: "query",
			input: func() directory.PersistInput {
				changed := base
				changed.Query.Email = "other@synthetic.example"
				changedProfile := profile
				changedProfile.Emails = []entity.DirectoryEmail{{
					Value: "other@synthetic.example", Primary: true,
				}}
				changed.Lookup.Profiles = []entity.DirectoryProfile{changedProfile}
				changed.Evaluation.AcceptedEmail = "other@synthetic.example"
				changed.Evaluation.Profile = &changedProfile
				return changed
			}(),
			policyVersion: entity.DirectoryPolicyVersion,
		},
		{
			name:          "policy",
			input:         base,
			policyVersion: "directory-identity-v2",
		},
		{
			name: "outcome",
			input: func() directory.PersistInput {
				changed := base
				changed.Lookup.Outcome = entity.DirectoryNoMatch
				changed.Lookup.Profiles = nil
				changed.Evaluation = entity.DirectoryEvaluation{Outcome: entity.DirectoryNoMatch}
				return changed
			}(),
			policyVersion: entity.DirectoryPolicyVersion,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			digest, err := directoryLookupDigest(test.input, test.policyVersion)
			if err != nil {
				t.Fatalf("directoryLookupDigest() error = %v", err)
			}
			if digest == baseline {
				t.Fatalf("directory lookup digest did not include %s", test.name)
			}
		})
	}
}

func TestValidateDirectoryPersistInputRejectsPrivateOrUnboundedReason(t *testing.T) {
	const privateOutcome = "provider error for synthetic.person@synthetic.example"
	input := directory.PersistInput{
		Mention: directory.PendingMention{
			MentionID:  "11111111-2222-3333-4444-555555555555",
			ProposalID: "66666666-7777-8888-9999-aaaaaaaaaaaa",
		},
		Query: entity.DirectoryQuery{
			Kind:          entity.DirectoryQueryEmail,
			Email:         "synthetic.person@synthetic.example",
			EmailEvidence: entity.EmailEvidenceSourceBound,
		},
		Lookup:       directory.LookupResult{Outcome: entity.DirectoryOutcome(privateOutcome)},
		Evaluation:   entity.DirectoryEvaluation{Outcome: entity.DirectoryNoMatch},
		AttemptCount: 1,
		RecordedAt:   time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
	}

	err := validateDirectoryPersistInput(input)
	if err == nil {
		t.Fatal("validateDirectoryPersistInput() error = nil, want bounded-outcome rejection")
	}
	if strings.Contains(err.Error(), privateOutcome) ||
		strings.Contains(err.Error(), input.Query.Email) {
		t.Fatalf("validateDirectoryPersistInput() error disclosed private input: %v", err)
	}
}

func TestValidateDirectoryPersistInputRejectsMismatchedAutomaticEmail(t *testing.T) {
	profile := entity.DirectoryProfile{
		Provider:    "google_people",
		SubjectID:   "synthetic-subject",
		Source:      entity.DirectorySourceDomainProfile,
		DisplayName: "Synthetic Person",
		Emails: []entity.DirectoryEmail{{
			Value: "profile@synthetic.example", Primary: true,
		}},
	}
	input := directory.PersistInput{
		Mention: directory.PendingMention{
			MentionID:  "11111111-2222-3333-4444-555555555555",
			ProposalID: "66666666-7777-8888-9999-aaaaaaaaaaaa",
		},
		Query: entity.DirectoryQuery{
			Kind:          entity.DirectoryQueryEmail,
			Email:         "query@synthetic.example",
			EmailEvidence: entity.EmailEvidenceSourceBound,
		},
		Lookup: directory.LookupResult{
			Outcome:  entity.DirectoryMatched,
			Profiles: []entity.DirectoryProfile{profile},
		},
		Evaluation: entity.DirectoryEvaluation{
			Outcome:       entity.DirectoryMatched,
			CreatePerson:  true,
			AcceptedEmail: "query@synthetic.example",
			Profile:       &profile,
		},
		AttemptCount: 1,
		RecordedAt:   time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
	}

	if err := validateDirectoryPersistInput(input); err == nil {
		t.Fatal("validateDirectoryPersistInput() error = nil, want exact profile-email rejection")
	}
}
