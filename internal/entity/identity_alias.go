// Package entity provides a temporary compatibility bridge to core identity.
package entity

import coreidentity "github.com/JakeFAU/stacks/core/identity"

type Kind = coreidentity.Kind
type AliasType = coreidentity.AliasType
type Alias = coreidentity.Alias
type EntitySnapshot = coreidentity.EntitySnapshot
type Mention = coreidentity.Mention
type Candidate = coreidentity.Candidate
type Resolution = coreidentity.Resolution
type Resolver = coreidentity.Resolver

const (
	KindPerson     = coreidentity.KindPerson
	AliasTypeName  = coreidentity.AliasTypeName
	AliasTypeEmail = coreidentity.AliasTypeEmail
)

func NormalizeName(value string) string {
	return coreidentity.NormalizeName(value)
}

func NormalizeEmail(value string) string {
	return coreidentity.NormalizeEmail(value)
}

func ValidEmail(value string) bool {
	return coreidentity.ValidEmail(value)
}
