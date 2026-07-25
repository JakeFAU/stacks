// Package directory defines provider-neutral directory lookup boundaries.
package directory

import (
	"context"
	"time"

	"stacks/internal/entity"
)

// LookupResult contains the bounded outcome of one directory lookup.
type LookupResult struct {
	Outcome    entity.DirectoryOutcome
	Profiles   []entity.DirectoryProfile
	RetryAfter time.Duration
}

// Lookup searches a directory without exposing provider-specific types.
type Lookup interface {
	Search(context.Context, entity.DirectoryQuery) (LookupResult, error)
}
