package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
)

var canonicalDirectoryRecordedAt = time.Date(
	2026,
	time.July,
	26,
	14,
	0,
	0,
	123456000,
	time.UTC,
)

func TestCanonicalDirectoryRejectsRawNonCanonicalTimesBeforeSQL(t *testing.T) {
	base := canonicalDirectoryPersistInput()
	nonUTC := time.Date(
		2026,
		time.July,
		26,
		10,
		0,
		0,
		123456000,
		time.FixedZone("synthetic", -4*60*60),
	)
	subMicrosecond := canonicalDirectoryRecordedAt.Add(time.Nanosecond)

	tests := []struct {
		name   string
		mutate func(*postgres.DirectoryPersistInput)
	}{
		{
			name: "shared snapshot lookup and link recorded time",
			mutate: func(input *postgres.DirectoryPersistInput) {
				input.RecordedAt = nonUTC
			},
		},
		{
			name: "shared snapshot lookup and link sub-microsecond time",
			mutate: func(input *postgres.DirectoryPersistInput) {
				input.RecordedAt = subMicrosecond
			},
		},
		{
			name: "shared snapshot lookup and link monotonic time",
			mutate: func(input *postgres.DirectoryPersistInput) {
				input.RecordedAt = time.Now()
			},
		},
		{
			name: "retry time",
			mutate: func(input *postgres.DirectoryPersistInput) {
				value := subMicrosecond.Add(time.Minute)
				input.RetryAfter = &value
				input.Lookup.Outcome = postgres.DirectoryOutcomeUnavailable
				input.Evaluation = postgres.DirectoryEvaluation{}
				input.Lookup.Profiles = nil
			},
		},
		{
			name: "profile observation time",
			mutate: func(input *postgres.DirectoryPersistInput) {
				input.Lookup.Profiles[0].ObservedAt = nonUTC
				input.Evaluation.Profile = &input.Lookup.Profiles[0]
			},
		},
		{
			name: "profile monotonic observation time",
			mutate: func(input *postgres.DirectoryPersistInput) {
				input.Lookup.Profiles[0].ObservedAt = time.Now()
				input.Evaluation.Profile = &input.Lookup.Profiles[0]
			},
		},
		{
			name: "evaluation profile observation time",
			mutate: func(input *postgres.DirectoryPersistInput) {
				input.Evaluation.Profile = &postgres.DirectoryProfile{
					Provider: "google_people", SubjectID: "people/synthetic",
					Source:      postgres.DirectorySourceDomainProfile,
					DisplayName: "Synthetic Person",
					Emails: []postgres.DirectoryEmail{{
						Value: "synthetic.person@example.test", Primary: true,
					}},
					ObservedAt: subMicrosecond,
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			input.Lookup.Profiles = append(
				[]postgres.DirectoryProfile(nil),
				base.Lookup.Profiles...,
			)
			profile := input.Lookup.Profiles[0]
			input.Evaluation.Profile = &profile
			test.mutate(&input)

			_, err := (postgres.DirectoryStore{}).Persist(context.Background(), input)
			if err == nil || !strings.Contains(err.Error(), "canonical") {
				t.Fatalf("Persist() error = %v, want canonical-time rejection", err)
			}
			if strings.Contains(err.Error(), "not configured") {
				t.Fatalf("Persist() reached database validation before raw time rejection: %v", err)
			}
		})
	}
}

func TestCanonicalDirectoryRejectsInvalidLookupProviderBeforeSQL(t *testing.T) {
	for _, provider := range []string{" \t", strings.Repeat("p", 129)} {
		input := canonicalDirectoryPersistInput()
		input.Lookup.Provider = provider

		_, err := (postgres.DirectoryStore{}).Persist(context.Background(), input)
		if err == nil || !strings.Contains(err.Error(), "lookup provider") {
			t.Fatalf("Persist() error = %v, want bounded lookup-provider rejection", err)
		}
		if strings.Contains(err.Error(), "not configured") {
			t.Fatalf("Persist() reached database validation before provider rejection: %v", err)
		}
	}
}

func TestCanonicalDirectoryLoadWorkRejectsRawNonCanonicalNowBeforeSQL(t *testing.T) {
	nonUTC := time.Date(
		2026,
		time.July,
		26,
		10,
		0,
		0,
		123456000,
		time.FixedZone("synthetic", -4*60*60),
	)
	tests := []struct {
		name string
		now  time.Time
	}{
		{name: "non UTC", now: nonUTC},
		{name: "sub microsecond", now: canonicalDirectoryRecordedAt.Add(time.Nanosecond)},
		{name: "monotonic", now: time.Now()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (postgres.DirectoryStore{}).LoadWork(
				context.Background(),
				postgres.DirectoryWorkRequest{
					DerivationID: "run:synthetic",
					Now:          test.now,
					Freshness:    time.Hour,
					RetryAfter:   time.Minute,
				},
			)
			if err == nil || !strings.Contains(err.Error(), "canonical") {
				t.Fatalf("LoadWork() error = %v, want canonical-time rejection", err)
			}
			if strings.Contains(err.Error(), "not configured") {
				t.Fatalf("LoadWork() reached database validation before raw time rejection: %v", err)
			}
		})
	}
}

func TestCanonicalDirectoryCancellationPreservesErrorsIs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store := postgres.DirectoryStore{}
	if _, err := store.LoadWork(ctx, postgres.DirectoryWorkRequest{
		DerivationID: "run:synthetic",
		Now:          canonicalDirectoryRecordedAt,
		Freshness:    time.Hour,
		RetryAfter:   time.Minute,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadWork() error = %v, want context.Canceled", err)
	}
	if _, err := store.LoadIdentityState(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadIdentityState() error = %v, want context.Canceled", err)
	}
	if _, err := store.Persist(ctx, canonicalDirectoryPersistInput()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Persist() error = %v, want context.Canceled", err)
	}
}

func canonicalDirectoryPersistInput() postgres.DirectoryPersistInput {
	profile := postgres.DirectoryProfile{
		Provider:    "google_people",
		SubjectID:   "people/synthetic",
		Source:      postgres.DirectorySourceDomainProfile,
		DisplayName: "Synthetic Person",
		Emails: []postgres.DirectoryEmail{{
			Value: "synthetic.person@example.test", Primary: true,
		}},
		ObservedAt: canonicalDirectoryRecordedAt.Add(-time.Hour),
	}
	return postgres.DirectoryPersistInput{
		Mention: postgres.DirectoryPendingMention{
			MentionID:      "mention:synthetic",
			ProposalID:     "proposal:synthetic",
			Surface:        "Synthetic Person",
			NormalizedName: "synthetic person",
			ProposedEmail:  "synthetic.person@example.test",
			NameQuote:      "Synthetic Person",
			EmailQuote:     "Synthetic Person <synthetic.person@example.test>",
		},
		Query: postgres.DirectoryQuery{
			Kind:          postgres.DirectoryQueryEmail,
			Email:         "synthetic.person@example.test",
			EmailEvidence: postgres.DirectoryEmailEvidenceSourceBound,
		},
		Lookup: postgres.DirectoryLookupResult{
			Provider: "google_people",
			Outcome:  postgres.DirectoryOutcomeMatched,
			Profiles: []postgres.DirectoryProfile{profile},
		},
		Evaluation: postgres.DirectoryEvaluation{
			Outcome:       postgres.DirectoryOutcomeMatched,
			CreatePerson:  true,
			AcceptedEmail: "synthetic.person@example.test",
			Profile:       &profile,
		},
		AttemptCount: 1,
		RecordedAt:   canonicalDirectoryRecordedAt,
	}
}
