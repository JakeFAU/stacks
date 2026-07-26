package directory

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"

	"stacks/internal/entity"
)

func TestDirectoryPostgresMapsRepositoryContract(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 26, 15, 0, 0, 123456000, time.UTC)
	retryAfter := recordedAt.Add(time.Minute)
	rootProfile := entity.DirectoryProfile{
		Provider:    "google_people",
		SubjectID:   "people/synthetic",
		Source:      entity.DirectorySourceDomainProfile,
		DisplayName: "Synthetic Person",
		Emails: []entity.DirectoryEmail{{
			Value: "synthetic.person@example.test", Primary: true,
		}},
		ObservedAt: recordedAt.Add(-time.Hour),
	}
	fake := &recordingPostgresDirectoryStore{
		work: postgres.DirectoryWorkset{
			Mentions: []postgres.DirectoryPendingMention{{
				MentionID: "mention:synthetic", ProposalID: "proposal:synthetic",
				Surface: "Synthetic Person", NormalizedName: "synthetic person",
				ProposedEmail: "synthetic.person@example.test",
				NameQuote:     "Synthetic Person",
				EmailQuote:    "Synthetic Person <synthetic.person@example.test>",
			}},
			Reused: 2,
		},
		identity: postgres.DirectoryIdentityState{
			Snapshots: []entity.EntitySnapshot{{
				ID: "entity:synthetic", Kind: entity.KindPerson,
				DisplayName: "Synthetic Person",
				Aliases: []entity.Alias{{
					Type: entity.AliasTypeEmail, Value: "synthetic.person@example.test",
				}},
			}},
			Links: []postgres.DirectoryIdentityLink{{
				Provider: "google_people", SubjectID: "people/synthetic",
				EntityID: "entity:synthetic",
			}},
		},
		result: postgres.DirectoryPersistResult{
			AutoResolved: true,
			EntityID:     "entity:synthetic",
		},
	}
	repository := &PostgresRepository{store: fake}

	work, err := repository.LoadWork(
		context.Background(),
		"run:synthetic",
		recordedAt,
		time.Hour,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("LoadWork() error = %v", err)
	}
	if work.Reused != 2 ||
		len(work.Mentions) != 1 ||
		work.Mentions[0].ProposalID != "proposal:synthetic" {
		t.Fatalf("LoadWork() = %#v, want mapped adapter work", work)
	}
	if fake.workRequest != (postgres.DirectoryWorkRequest{
		DerivationID: "run:synthetic",
		Now:          recordedAt,
		Freshness:    time.Hour,
		RetryAfter:   time.Minute,
	}) {
		t.Fatalf("adapter work request = %#v, want exact root inputs", fake.workRequest)
	}

	state, err := repository.LoadIdentityState(context.Background())
	if err != nil {
		t.Fatalf("LoadIdentityState() error = %v", err)
	}
	if !reflect.DeepEqual(state.Snapshots, fake.identity.Snapshots) ||
		!reflect.DeepEqual(state.Links, []entity.DirectoryIdentityLink{{
			Provider: "google_people", SubjectID: "people/synthetic",
			EntityID: "entity:synthetic",
		}}) {
		t.Fatalf("LoadIdentityState() = %#v, want mapped canonical identity state", state)
	}

	input := PersistInput{
		Mention: PendingMention{
			MentionID: "mention:synthetic", ProposalID: "proposal:synthetic",
			Surface: "Synthetic Person", NormalizedName: "synthetic person",
			ProposedEmail: "synthetic.person@example.test",
			NameQuote:     "Synthetic Person",
			EmailQuote:    "Synthetic Person <synthetic.person@example.test>",
		},
		Query: entity.DirectoryQuery{
			Kind:          entity.DirectoryQueryEmail,
			Email:         "synthetic.person@example.test",
			EmailEvidence: entity.EmailEvidenceSourceBound,
		},
		Lookup: LookupResult{
			Provider: "google_people",
			Outcome:  entity.DirectoryMatched,
			Profiles: []entity.DirectoryProfile{rootProfile},
		},
		Evaluation: entity.DirectoryEvaluation{
			Outcome:       entity.DirectoryMatched,
			CreatePerson:  true,
			AcceptedEmail: "synthetic.person@example.test",
			Profile:       &rootProfile,
			Candidates:    []entity.DirectoryProfile{rootProfile},
		},
		AttemptCount: 1,
		RecordedAt:   recordedAt,
		RetryAfter:   &retryAfter,
	}
	result, err := repository.Persist(context.Background(), input)
	if err != nil {
		t.Fatalf("Persist() error = %v", err)
	}
	if result != (PersistResult{AutoResolved: true, EntityID: "entity:synthetic"}) {
		t.Fatalf("Persist() = %#v, want mapped adapter result", result)
	}
	if fake.persistInput.Mention.ProposalID != input.Mention.ProposalID ||
		fake.persistInput.Query.Kind != string(input.Query.Kind) ||
		fake.persistInput.Query.EmailEvidence != string(input.Query.EmailEvidence) ||
		fake.persistInput.Lookup.Provider != input.Lookup.Provider ||
		fake.persistInput.Lookup.Outcome != string(input.Lookup.Outcome) ||
		len(fake.persistInput.Lookup.Profiles) != 1 ||
		fake.persistInput.Lookup.Profiles[0].ObservedAt != rootProfile.ObservedAt ||
		fake.persistInput.Evaluation.Profile == nil ||
		fake.persistInput.Evaluation.Profile.SubjectID != rootProfile.SubjectID ||
		len(fake.persistInput.Evaluation.Candidates) != 1 ||
		fake.persistInput.RecordedAt != recordedAt ||
		fake.persistInput.RetryAfter == nil ||
		*fake.persistInput.RetryAfter != retryAfter {
		t.Fatalf("adapter persist input = %#v, want lossless root mapping", fake.persistInput)
	}
}

func TestDirectoryPostgresPreservesCancellation(t *testing.T) {
	for _, cancellation := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cancellation.Error(), func(t *testing.T) {
			fake := &recordingPostgresDirectoryStore{err: errors.Join(
				errors.New("synthetic adapter detail"),
				cancellation,
			)}
			repository := &PostgresRepository{store: fake}
			if _, err := repository.LoadWork(
				context.Background(),
				"run:synthetic",
				time.Date(2026, time.July, 26, 15, 0, 0, 0, time.UTC),
				time.Hour,
				time.Minute,
			); !errors.Is(err, cancellation) {
				t.Fatalf("LoadWork() error = %v, want %v", err, cancellation)
			}
			if _, err := repository.LoadIdentityState(context.Background()); !errors.Is(err, cancellation) {
				t.Fatalf("LoadIdentityState() error = %v, want %v", err, cancellation)
			}
			if _, err := repository.Persist(context.Background(), PersistInput{}); !errors.Is(err, cancellation) {
				t.Fatalf("Persist() error = %v, want %v", err, cancellation)
			}
		})
	}
}

type recordingPostgresDirectoryStore struct {
	workRequest  postgres.DirectoryWorkRequest
	persistInput postgres.DirectoryPersistInput
	work         postgres.DirectoryWorkset
	identity     postgres.DirectoryIdentityState
	result       postgres.DirectoryPersistResult
	err          error
}

func (store *recordingPostgresDirectoryStore) LoadWork(
	_ context.Context,
	request postgres.DirectoryWorkRequest,
) (postgres.DirectoryWorkset, error) {
	store.workRequest = request
	return store.work, store.err
}

func (store *recordingPostgresDirectoryStore) LoadIdentityState(
	context.Context,
) (postgres.DirectoryIdentityState, error) {
	return store.identity, store.err
}

func (store *recordingPostgresDirectoryStore) Persist(
	_ context.Context,
	input postgres.DirectoryPersistInput,
) (postgres.DirectoryPersistResult, error) {
	store.persistInput = input
	return store.result, store.err
}
