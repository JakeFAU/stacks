// Package entity owns deterministic, reviewable identity resolution policy.
package entity

import (
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

// Kind identifies the category of a canonical entity. Additional kinds can be
// added without changing the durable entity contract.
type Kind string

const (
	// KindPerson identifies a person entity.
	KindPerson Kind = "person"
)

// AliasType identifies the normalization policy used for an accepted alias.
type AliasType string

const (
	// AliasTypeName identifies a human-readable name alias.
	AliasTypeName AliasType = "name"
	// AliasTypeEmail identifies an email address alias.
	AliasTypeEmail AliasType = "email"
)

// Alias is an accepted identifier associated with an entity snapshot.
type Alias struct {
	Type  AliasType
	Value string
}

// EntitySnapshot contains the resolution data available at one deterministic
// point in time. Aliases must contain accepted aliases only.
type EntitySnapshot struct {
	ID          string
	Kind        Kind
	DisplayName string
	RecordedAt  time.Time
	Aliases     []Alias
}

// Mention is the private source-grounded surface supplied to a resolver.
// It must be kept out of logs and telemetry by callers.
type Mention struct {
	Surface string
}

// Candidate is a reviewable possible identity. It never represents accepted
// graph truth until a review decision is persisted.
type Candidate struct {
	EntityID   string
	Confidence float64
	Reason     string
}

// Resolution records either an automatic accepted-identifier match or a
// ranked review queue. EntityID is set only for AutoResolved results.
type Resolution struct {
	EntityID     string
	AutoResolved bool
	Candidates   []Candidate
}

// NormalizeName canonicalizes Unicode representation, case, and whitespace
// for person-name comparison. It deliberately does not apply email rules.
func NormalizeName(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(norm.NFKC.String(value)), " "))
}

// NormalizeEmail canonicalizes Unicode representation, outer whitespace, and
// case for exact email comparison. It deliberately preserves internal bytes
// such as dots because email local parts are not person names.
func NormalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(norm.NFKC.String(value)))
}
