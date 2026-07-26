package directory

import (
	"context"
	"fmt"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"

	"stacks/internal/entity"
)

type postgresDirectoryStore interface {
	LoadWork(
		context.Context,
		postgres.DirectoryWorkRequest,
	) (postgres.DirectoryWorkset, error)
	LoadIdentityState(context.Context) (postgres.DirectoryIdentityState, error)
	Persist(
		context.Context,
		postgres.DirectoryPersistInput,
	) (postgres.DirectoryPersistResult, error)
}

// PostgresRepository maps the consumer-owned optional directory contract onto
// adapter-neutral PostgreSQL values.
type PostgresRepository struct {
	store postgresDirectoryStore
}

// NewPostgresRepository constructs optional directory persistence over a
// caller-owned canonical database. Disabled composition does not call it.
func NewPostgresRepository(database *postgres.Database) *PostgresRepository {
	return &PostgresRepository{
		store: postgres.DirectoryStore{Database: database},
	}
}

// LoadWork implements Repository.
func (repository *PostgresRepository) LoadWork(
	ctx context.Context,
	derivationID string,
	now time.Time,
	freshness time.Duration,
	retryAfter time.Duration,
) (Workset, error) {
	if repository == nil || repository.store == nil {
		return Workset{}, fmt.Errorf("load directory work: PostgreSQL repository is not configured")
	}
	work, err := repository.store.LoadWork(ctx, postgres.DirectoryWorkRequest{
		DerivationID: derivationID,
		Now:          now,
		Freshness:    freshness,
		RetryAfter:   retryAfter,
	})
	if err != nil {
		return Workset{}, fmt.Errorf("load directory work: %w", err)
	}
	result := Workset{
		Mentions: make([]PendingMention, len(work.Mentions)),
		Reused:   work.Reused,
	}
	for index, mention := range work.Mentions {
		result.Mentions[index] = PendingMention{
			MentionID:      mention.MentionID,
			ProposalID:     mention.ProposalID,
			Surface:        mention.Surface,
			NormalizedName: mention.NormalizedName,
			ProposedEmail:  mention.ProposedEmail,
			NameQuote:      mention.NameQuote,
			EmailQuote:     mention.EmailQuote,
		}
	}
	return result, nil
}

// LoadIdentityState implements Repository.
func (repository *PostgresRepository) LoadIdentityState(
	ctx context.Context,
) (IdentityState, error) {
	if repository == nil || repository.store == nil {
		return IdentityState{}, fmt.Errorf("load directory identity state: PostgreSQL repository is not configured")
	}
	state, err := repository.store.LoadIdentityState(ctx)
	if err != nil {
		return IdentityState{}, fmt.Errorf("load directory identity state: %w", err)
	}
	result := IdentityState{
		Snapshots: state.Snapshots,
		Links:     make([]entity.DirectoryIdentityLink, len(state.Links)),
	}
	for index, link := range state.Links {
		result.Links[index] = entity.DirectoryIdentityLink{
			Provider:  link.Provider,
			SubjectID: link.SubjectID,
			EntityID:  link.EntityID,
		}
	}
	return result, nil
}

// Persist implements Repository.
func (repository *PostgresRepository) Persist(
	ctx context.Context,
	input PersistInput,
) (PersistResult, error) {
	if repository == nil || repository.store == nil {
		return PersistResult{}, fmt.Errorf("persist directory lookup: PostgreSQL repository is not configured")
	}
	persisted, err := repository.store.Persist(ctx, postgres.DirectoryPersistInput{
		Mention: postgres.DirectoryPendingMention{
			MentionID:      input.Mention.MentionID,
			ProposalID:     input.Mention.ProposalID,
			Surface:        input.Mention.Surface,
			NormalizedName: input.Mention.NormalizedName,
			ProposedEmail:  input.Mention.ProposedEmail,
			NameQuote:      input.Mention.NameQuote,
			EmailQuote:     input.Mention.EmailQuote,
		},
		Query: postgres.DirectoryQuery{
			Kind:          string(input.Query.Kind),
			Name:          input.Query.Name,
			Email:         input.Query.Email,
			EmailEvidence: string(input.Query.EmailEvidence),
		},
		Lookup: postgres.DirectoryLookupResult{
			Provider:   input.Lookup.Provider,
			Outcome:    string(input.Lookup.Outcome),
			Profiles:   postgresDirectoryProfiles(input.Lookup.Profiles),
			RetryAfter: input.Lookup.RetryAfter,
		},
		Evaluation:   postgresDirectoryEvaluation(input.Evaluation),
		AttemptCount: input.AttemptCount,
		RecordedAt:   input.RecordedAt,
		RetryAfter:   input.RetryAfter,
	})
	if err != nil {
		return PersistResult{}, fmt.Errorf("persist directory lookup: %w", err)
	}
	return PersistResult{
		AutoResolved: persisted.AutoResolved,
		EntityID:     persisted.EntityID,
	}, nil
}

func postgresDirectoryEvaluation(
	value entity.DirectoryEvaluation,
) postgres.DirectoryEvaluation {
	result := postgres.DirectoryEvaluation{
		Outcome:       string(value.Outcome),
		EntityID:      value.EntityID,
		CreatePerson:  value.CreatePerson,
		AcceptedEmail: value.AcceptedEmail,
		Candidates:    postgresDirectoryProfiles(value.Candidates),
	}
	if value.Profile != nil {
		profile := postgresDirectoryProfile(*value.Profile)
		result.Profile = &profile
	}
	return result
}

func postgresDirectoryProfiles(
	values []entity.DirectoryProfile,
) []postgres.DirectoryProfile {
	result := make([]postgres.DirectoryProfile, len(values))
	for index, value := range values {
		result[index] = postgresDirectoryProfile(value)
	}
	return result
}

func postgresDirectoryProfile(
	value entity.DirectoryProfile,
) postgres.DirectoryProfile {
	emails := make([]postgres.DirectoryEmail, len(value.Emails))
	for index, email := range value.Emails {
		emails[index] = postgres.DirectoryEmail{
			Value:   email.Value,
			Primary: email.Primary,
		}
	}
	return postgres.DirectoryProfile{
		Provider:    value.Provider,
		SubjectID:   value.SubjectID,
		Source:      string(value.Source),
		DisplayName: value.DisplayName,
		Emails:      emails,
		ObservedAt:  value.ObservedAt,
	}
}
