package entity

import "testing"

func TestNewDirectoryPolicyRejectsEmptyInvalidAndDuplicateDomains(t *testing.T) {
	for _, domains := range [][]string{
		nil,
		{""},
		{"@example.test"},
		{"example.test", "EXAMPLE.TEST"},
	} {
		if _, err := NewDirectoryPolicy(domains); err == nil {
			t.Fatalf("NewDirectoryPolicy(%q) error = nil", domains)
		}
	}
}

func TestSourceBoundMailboxRequiresOneExactNamedMailbox(t *testing.T) {
	if !SourceBoundMailbox("Riya Chen", "riya.chen@corp.example", "Riya Chen <riya.chen@corp.example>") {
		t.Fatal("exact named mailbox was not source-bound")
	}
	for _, quote := range []string{
		"Alex Reviewer and Riya Chen <riya.chen@corp.example>",
		"Alex Reviewer <riya.chen@corp.example>",
		"Riya Chen <other@corp.example>",
	} {
		if SourceBoundMailbox("Riya Chen", "riya.chen@corp.example", quote) {
			t.Fatalf("SourceBoundMailbox accepted %q", quote)
		}
	}
}

func TestDirectoryPolicyAutoCreatesForUniqueSourceBoundApprovedEmail(t *testing.T) {
	policy := directoryPolicy(t)
	evaluation := policy.Evaluate(DirectoryQuery{
		Kind:          DirectoryQueryEmail,
		Email:         "Riya.Chen@Corp.Example",
		EmailEvidence: EmailEvidenceSourceBound,
	}, []DirectoryProfile{directoryProfile("google", "100", DirectorySourceDomainProfile, "Riya Chen", "riya.chen@corp.example")}, nil, nil)

	if evaluation.Outcome != DirectoryMatched || !evaluation.CreatePerson || evaluation.EntityID != "" || evaluation.AcceptedEmail != "riya.chen@corp.example" {
		t.Fatalf("evaluation = %#v, want new person from the exact approved source-bound email", evaluation)
	}
	if evaluation.Profile == nil || evaluation.Profile.SubjectID != "100" {
		t.Fatalf("profile = %#v, want exact directory profile", evaluation.Profile)
	}
}

func TestDirectoryPolicyKeepsProfilesWithoutDirectoryBindingReviewOnly(t *testing.T) {
	policy := directoryPolicy(t)
	for _, test := range []struct {
		name     string
		provider string
		subject  string
	}{
		{name: "blank provider", provider: "", subject: "100"},
		{name: "whitespace provider", provider: " \t", subject: "100"},
		{name: "blank subject", provider: "google", subject: ""},
		{name: "whitespace subject", provider: "google", subject: " \t"},
	} {
		t.Run(test.name, func(t *testing.T) {
			evaluation := policy.Evaluate(DirectoryQuery{
				Kind:          DirectoryQueryEmail,
				Email:         "riya.chen@corp.example",
				EmailEvidence: EmailEvidenceSourceBound,
			}, []DirectoryProfile{directoryProfile(test.provider, test.subject, DirectorySourceDomainProfile, "Riya Chen", "riya.chen@corp.example")}, nil, nil)

			if evaluation.Outcome != DirectoryReview || evaluation.CreatePerson || evaluation.EntityID != "" || evaluation.AcceptedEmail != "" || evaluation.Profile != nil {
				t.Fatalf("evaluation = %#v, want unbound profile to remain review-only", evaluation)
			}
		})
	}
}

func TestDirectoryPolicyUsesExistingUniqueAcceptedEmailOwner(t *testing.T) {
	policy := directoryPolicy(t)
	evaluation := policy.Evaluate(DirectoryQuery{
		Kind:          DirectoryQueryEmail,
		Email:         "riya.chen@corp.example",
		EmailEvidence: EmailEvidenceReviewerSupplied,
	}, []DirectoryProfile{directoryProfile("google", "100", DirectorySourceDomainProfile, "Riya Chen", "riya.chen@corp.example")}, []EntitySnapshot{{
		ID:   "person-1",
		Kind: KindPerson,
		Aliases: []Alias{{
			Type:  AliasTypeEmail,
			Value: "Riya.Chen@Corp.Example",
		}},
	}}, nil)

	if evaluation.Outcome != DirectoryMatched || evaluation.CreatePerson || evaluation.EntityID != "person-1" || evaluation.AcceptedEmail != "riya.chen@corp.example" {
		t.Fatalf("evaluation = %#v, want existing accepted email owner", evaluation)
	}
}

func TestDirectoryPolicyKeepsCitationVerifiedEmailReviewOnly(t *testing.T) {
	policy := directoryPolicy(t)
	evaluation := policy.Evaluate(DirectoryQuery{
		Kind:          DirectoryQueryEmail,
		Name:          "Riya Chen",
		Email:         "riya.chen@corp.example",
		EmailEvidence: EmailEvidenceCitationVerified,
	}, []DirectoryProfile{directoryProfile("google", "100", DirectorySourceDomainProfile, "Riya Chen", "riya.chen@corp.example")}, nil, nil)

	if evaluation.Outcome != DirectoryReview || evaluation.CreatePerson || evaluation.EntityID != "" || evaluation.AcceptedEmail != "" {
		t.Fatalf("evaluation = %#v, want citation-verified email to remain review-only", evaluation)
	}
	if len(evaluation.Candidates) != 1 || evaluation.Candidates[0].SubjectID != "100" {
		t.Fatalf("candidates = %#v, want exact directory profile for review", evaluation.Candidates)
	}
}

func TestDirectoryPolicyKeepsNameOnlyProfilesReviewOnly(t *testing.T) {
	policy := directoryPolicy(t)
	evaluation := policy.Evaluate(DirectoryQuery{Kind: DirectoryQueryName, Name: "Riya Chen"}, []DirectoryProfile{
		directoryProfile("google", "100", DirectorySourceDomainProfile, "Riya Chen", "riya.chen@corp.example"),
	}, nil, nil)

	if evaluation.Outcome != DirectoryReview || evaluation.CreatePerson || evaluation.EntityID != "" || evaluation.AcceptedEmail != "" {
		t.Fatalf("evaluation = %#v, want name-only lookup to remain review-only", evaluation)
	}
}

func TestDirectoryPolicyRejectsExternalDomainAndSharedContactAuthority(t *testing.T) {
	policy := directoryPolicy(t)
	query := DirectoryQuery{Kind: DirectoryQueryEmail, Email: "riya.chen@corp.example", EmailEvidence: EmailEvidenceSourceBound}
	for _, profile := range []DirectoryProfile{
		directoryProfile("google", "external", DirectorySourceDomainProfile, "Riya Chen", "riya.chen@external.example"),
		directoryProfile("google", "contact", DirectorySourceDomainContact, "Riya Chen", "riya.chen@corp.example"),
	} {
		t.Run(profile.SubjectID, func(t *testing.T) {
			query.Email = profile.Emails[0].Value
			evaluation := policy.Evaluate(query, []DirectoryProfile{profile}, nil, nil)
			if evaluation.Outcome != DirectoryReview || evaluation.CreatePerson || evaluation.EntityID != "" || evaluation.AcceptedEmail != "" {
				t.Fatalf("evaluation = %#v, want non-authoritative profile to remain review-only", evaluation)
			}
		})
	}
}

func TestDirectoryPolicyKeepsDuplicateEmailMatchesAmbiguous(t *testing.T) {
	policy := directoryPolicy(t)
	evaluation := policy.Evaluate(DirectoryQuery{
		Kind:          DirectoryQueryEmail,
		Email:         "riya.chen@corp.example",
		EmailEvidence: EmailEvidenceSourceBound,
	}, []DirectoryProfile{
		directoryProfile("google", "200", DirectorySourceDomainProfile, "Riya Chen", "riya.chen@corp.example"),
		directoryProfile("google", "100", DirectorySourceDomainProfile, "Riya Chen", "riya.chen@corp.example"),
	}, nil, nil)

	if evaluation.Outcome != DirectoryAmbiguous || evaluation.CreatePerson || evaluation.EntityID != "" || evaluation.AcceptedEmail != "" {
		t.Fatalf("evaluation = %#v, want duplicate exact email profiles to remain ambiguous", evaluation)
	}
	if len(evaluation.Candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(evaluation.Candidates))
	}
}

func TestDirectoryPolicyKeepsConflictingAliasOrProviderLinkReviewOnly(t *testing.T) {
	policy := directoryPolicy(t)
	query := DirectoryQuery{Kind: DirectoryQueryEmail, Email: "riya.chen@corp.example", EmailEvidence: EmailEvidenceSourceBound}
	profiles := []DirectoryProfile{directoryProfile("google", "100", DirectorySourceDomainProfile, "Riya Chen", "riya.chen@corp.example")}

	for _, test := range []struct {
		name      string
		snapshots []EntitySnapshot
		links     []DirectoryIdentityLink
	}{
		{
			name: "duplicate accepted email owners",
			snapshots: []EntitySnapshot{
				{ID: "person-1", Kind: KindPerson, Aliases: []Alias{{Type: AliasTypeEmail, Value: "riya.chen@corp.example"}}},
				{ID: "person-2", Kind: KindPerson, Aliases: []Alias{{Type: AliasTypeEmail, Value: "riya.chen@corp.example"}}},
			},
		},
		{
			name:      "provider link owned by another entity",
			snapshots: []EntitySnapshot{{ID: "person-1", Kind: KindPerson, Aliases: []Alias{{Type: AliasTypeEmail, Value: "riya.chen@corp.example"}}}},
			links:     []DirectoryIdentityLink{{Provider: "google", SubjectID: "100", EntityID: "person-2"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			evaluation := policy.Evaluate(query, profiles, test.snapshots, test.links)
			if evaluation.Outcome != DirectoryReview || evaluation.CreatePerson || evaluation.EntityID != "" || evaluation.AcceptedEmail != "" {
				t.Fatalf("evaluation = %#v, want conflict to remain review-only", evaluation)
			}
		})
	}
}

func TestDirectoryPolicySortsProfilesDeterministically(t *testing.T) {
	policy := directoryPolicy(t)
	evaluation := policy.Evaluate(DirectoryQuery{Kind: DirectoryQueryName, Name: "Riya Chen"}, []DirectoryProfile{
		directoryProfile("workspace-b", "200", DirectorySourceDomainProfile, "Riya Chen", "riya@corp.example"),
		directoryProfile("workspace-a", "300", DirectorySourceDomainProfile, "Riya Chen", "riya@corp.example"),
		directoryProfile("workspace-a", "100", DirectorySourceDomainProfile, "Riya Chen", "riya@corp.example"),
	}, nil, nil)

	if evaluation.Outcome != DirectoryReview {
		t.Fatalf("outcome = %q, want review", evaluation.Outcome)
	}
	got := []string{evaluation.Candidates[0].Provider + "/" + evaluation.Candidates[0].SubjectID, evaluation.Candidates[1].Provider + "/" + evaluation.Candidates[1].SubjectID, evaluation.Candidates[2].Provider + "/" + evaluation.Candidates[2].SubjectID}
	want := []string{"workspace-a/100", "workspace-a/300", "workspace-b/200"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("candidate order = %q, want %q", got, want)
		}
	}
}

func directoryPolicy(t *testing.T) DirectoryPolicy {
	t.Helper()
	policy, err := NewDirectoryPolicy([]string{"corp.example"})
	if err != nil {
		t.Fatalf("NewDirectoryPolicy() error = %v", err)
	}
	return policy
}

func directoryProfile(provider, subjectID string, source DirectorySource, displayName, email string) DirectoryProfile {
	return DirectoryProfile{
		Provider:    provider,
		SubjectID:   subjectID,
		Source:      source,
		DisplayName: displayName,
		Emails:      []DirectoryEmail{{Value: email, Primary: true}},
	}
}
