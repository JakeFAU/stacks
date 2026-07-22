// Package source defines provider-neutral document discovery and retrieval
// contracts. Provider implementations translate their SDK responses at this
// boundary so domain code does not depend on provider types.
package source

import (
	"context"
	"time"
)

// Source discovers documents in a collection and retrieves one document with
// its complete tab structure.
type Source interface {
	List(ctx context.Context, collectionID string) ([]Document, error)
	Get(ctx context.Context, documentID string) (Document, error)
}

// RepresentativeSource verifies a collection and finds at most one supported
// document within it. It exists for bounded read-only preflight checks that
// must not enumerate an entire provider collection.
type RepresentativeSource interface {
	CheckCollection(ctx context.Context, collectionID string) error
	GetRepresentative(ctx context.Context, collectionID string) (Document, bool, error)
	Get(ctx context.Context, documentID string) (Document, error)
}

// Document is a provider-neutral source document. Tabs are ordered in the
// provider's user-visible order and retain their hierarchy independently.
type Document struct {
	Provider   string
	ID         string
	Title      string
	Locator    string
	Version    string
	ModifiedAt time.Time
	Tabs       []Tab
}

// TabRole identifies the evidentiary role assigned from a configured tab
// title. Roles are deterministic and do not depend on position or model output.
type TabRole string

const (
	TabRoleOther       TabRole = "other"
	TabRoleTranscript  TabRole = "transcript"
	TabRoleGeminiNotes TabRole = "gemini-notes"
)

// Tab is one immutable-provider tab as returned during source retrieval.
// Path contains the user-visible title hierarchy from the root through this
// tab, and Order is its zero-based depth-first user-visible position.
type Tab struct {
	ID       string
	Title    string
	ParentID string
	Path     []string
	Order    int
	Role     TabRole
	Text     string
}
