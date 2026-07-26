// Package directory defines provider-neutral directory lookup boundaries.
package directory

import (
	"context"
	"time"

	"stacks/internal/entity"
)

// LookupResult contains the bounded outcome of one directory lookup.
type LookupResult struct {
	Provider   string
	Outcome    entity.DirectoryOutcome
	Profiles   []entity.DirectoryProfile
	RetryAfter time.Duration
}

// Lookup searches a directory without exposing provider-specific types.
type Lookup interface {
	Search(context.Context, entity.DirectoryQuery) (LookupResult, error)
}

// PendingMention is the private in-process identity evidence needed to plan a
// directory query. It must not be logged or returned in ordinary sync output.
type PendingMention struct {
	MentionID      string
	ProposalID     string
	Surface        string
	NormalizedName string
	ProposedEmail  string
	NameQuote      string
	EmailQuote     string
}

// Workset contains eligible unresolved mentions and the number of fresh
// conclusive lookup attempts reused without another provider request.
type Workset struct {
	Mentions []PendingMention
	Reused   int
}

// IdentityState contains only currently admissible identity authority.
type IdentityState struct {
	Snapshots []entity.EntitySnapshot
	Links     []entity.DirectoryIdentityLink
}

// PersistInput is the complete bounded result of one directory lookup and its
// deterministic policy evaluation.
type PersistInput struct {
	Mention      PendingMention
	Query        entity.DirectoryQuery
	Lookup       LookupResult
	Evaluation   entity.DirectoryEvaluation
	AttemptCount int
	RecordedAt   time.Time
	RetryAfter   *time.Time
}

// PersistResult reports whether storage atomically admitted identity
// authority. Review evidence is durable even when AutoResolved is false.
type PersistResult struct {
	AutoResolved bool
	EntityID     string
}

// Repository owns directory work loading and atomic persistence.
type Repository interface {
	LoadWork(context.Context, string, time.Time, time.Duration, time.Duration) (Workset, error)
	LoadIdentityState(context.Context) (IdentityState, error)
	Persist(context.Context, PersistInput) (PersistResult, error)
}
