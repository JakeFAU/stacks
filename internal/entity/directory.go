package entity

import (
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"
)

// DirectoryPolicyVersion identifies the deterministic directory identity policy.
const DirectoryPolicyVersion = "directory-identity-v1"

type DirectoryQueryKind string
type EmailEvidence string
type DirectorySource string
type DirectoryOutcome string

const (
	DirectoryQueryEmail DirectoryQueryKind = "email"
	DirectoryQueryName  DirectoryQueryKind = "name"

	EmailEvidenceSourceBound      EmailEvidence = "source_bound"
	EmailEvidenceCitationVerified EmailEvidence = "citation_verified"
	EmailEvidenceReviewerSupplied EmailEvidence = "reviewer_supplied"
	EmailEvidenceNone             EmailEvidence = "none"

	DirectorySourceDomainProfile DirectorySource = "domain_profile"
	DirectorySourceDomainContact DirectorySource = "domain_contact"

	DirectoryMatched             DirectoryOutcome = "matched"
	DirectoryNoMatch             DirectoryOutcome = "no_match"
	DirectoryAmbiguous           DirectoryOutcome = "ambiguous"
	DirectoryReview              DirectoryOutcome = "review"
	DirectoryDisabled            DirectoryOutcome = "disabled"
	DirectoryNotConfigured       DirectoryOutcome = "not_configured"
	DirectoryUnauthorized        DirectoryOutcome = "unauthorized"
	DirectoryForbidden           DirectoryOutcome = "forbidden"
	DirectoryRateLimited         DirectoryOutcome = "rate_limited"
	DirectoryUnavailable         DirectoryOutcome = "unavailable"
	DirectoryInvalidResponse     DirectoryOutcome = "invalid_response"
	DirectoryResultLimitExceeded DirectoryOutcome = "result_limit_exceeded"
)

type DirectoryQuery struct {
	Kind          DirectoryQueryKind
	Name          string
	Email         string
	EmailEvidence EmailEvidence
}

type DirectoryEmail struct {
	Value   string
	Primary bool
}

type DirectoryProfile struct {
	Provider    string
	SubjectID   string
	Source      DirectorySource
	DisplayName string
	Emails      []DirectoryEmail
	ObservedAt  time.Time
}

type DirectoryIdentityLink struct {
	Provider  string
	SubjectID string
	EntityID  string
}

type DirectoryEvaluation struct {
	Outcome       DirectoryOutcome
	EntityID      string
	CreatePerson  bool
	AcceptedEmail string
	Profile       *DirectoryProfile
	Candidates    []DirectoryProfile
}

// DirectoryPolicy contains an approved-domain set that is only populated at
// construction time. Its members are intentionally not exported.
type DirectoryPolicy struct {
	approvedDomains map[string]struct{}
}

// NewDirectoryPolicy validates and records the configured approved work-email
// domains in normalized form.
func NewDirectoryPolicy(domains []string) (DirectoryPolicy, error) {
	if len(domains) == 0 {
		return DirectoryPolicy{}, fmt.Errorf("directory approved domains are required")
	}

	approvedDomains := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		normalized := strings.ToLower(strings.TrimSpace(domain))
		if !validDomain(normalized) {
			return DirectoryPolicy{}, fmt.Errorf("invalid directory approved domain %q", domain)
		}
		if _, exists := approvedDomains[normalized]; exists {
			return DirectoryPolicy{}, fmt.Errorf("duplicate directory approved domain %q", domain)
		}
		approvedDomains[normalized] = struct{}{}
	}

	return DirectoryPolicy{approvedDomains: approvedDomains}, nil
}

// SourceBoundMailbox reports whether quote is exactly one named mailbox whose
// normalized name and email match the supplied source fields.
func SourceBoundMailbox(surface, email, quote string) bool {
	parsed, err := mail.ParseAddress(strings.TrimSpace(quote))
	if err != nil {
		return false
	}
	return NormalizeName(parsed.Name) != "" &&
		NormalizeName(parsed.Name) == NormalizeName(surface) &&
		ValidEmail(email) &&
		NormalizeEmail(parsed.Address) == NormalizeEmail(email)
}

// LookupEligible reports whether a normalized query may cross the directory
// disclosure boundary under the configured work-domain policy.
func (policy DirectoryPolicy) LookupEligible(query DirectoryQuery) bool {
	switch query.Kind {
	case DirectoryQueryEmail:
		email := NormalizeEmail(query.Email)
		if NormalizeName(query.Name) != "" ||
			!ValidEmail(email) {
			return false
		}
		switch query.EmailEvidence {
		case EmailEvidenceSourceBound,
			EmailEvidenceCitationVerified,
			EmailEvidenceReviewerSupplied:
		default:
			return false
		}
		_, approved := policy.approvedDomains[emailDomain(email)]
		return approved
	case DirectoryQueryName:
		return NormalizeName(query.Name) != "" &&
			NormalizeEmail(query.Email) == "" &&
			query.EmailEvidence == EmailEvidenceNone
	default:
		return false
	}
}

// Evaluate applies the deterministic directory authority policy without
// mutating directory results, entity snapshots, or identity links.
func (policy DirectoryPolicy) Evaluate(query DirectoryQuery, profiles []DirectoryProfile, snapshots []EntitySnapshot, links []DirectoryIdentityLink) DirectoryEvaluation {
	sortedProfiles := sortedDirectoryProfiles(profiles)
	switch query.Kind {
	case DirectoryQueryEmail:
		return policy.evaluateEmail(query, sortedProfiles, snapshots, links)
	case DirectoryQueryName:
		if NormalizeName(query.Name) == "" || len(sortedProfiles) == 0 {
			return DirectoryEvaluation{Outcome: DirectoryNoMatch}
		}
		return DirectoryEvaluation{Outcome: DirectoryReview, Candidates: sortedProfiles}
	default:
		return DirectoryEvaluation{Outcome: DirectoryNoMatch}
	}
}

func (policy DirectoryPolicy) evaluateEmail(query DirectoryQuery, profiles []DirectoryProfile, snapshots []EntitySnapshot, links []DirectoryIdentityLink) DirectoryEvaluation {
	email := NormalizeEmail(query.Email)
	if !ValidEmail(email) {
		return DirectoryEvaluation{Outcome: DirectoryNoMatch}
	}

	exactProfiles := matchingDirectoryProfiles(email, profiles)
	if len(exactProfiles) == 0 {
		return DirectoryEvaluation{Outcome: DirectoryNoMatch}
	}
	if len(exactProfiles) > 1 {
		return DirectoryEvaluation{Outcome: DirectoryAmbiguous, Candidates: exactProfiles}
	}

	profile := exactProfiles[0]
	if !policy.canAutomaticallyAccept(query, email, profile) {
		return DirectoryEvaluation{Outcome: DirectoryReview, Candidates: exactProfiles}
	}

	owners := acceptedEmailOwners(email, snapshots)
	if len(owners) > 1 {
		return DirectoryEvaluation{Outcome: DirectoryReview, Candidates: exactProfiles}
	}
	if conflictingDirectoryLink(profile, owners, links) {
		return DirectoryEvaluation{Outcome: DirectoryReview, Candidates: exactProfiles}
	}

	evaluation := DirectoryEvaluation{
		Outcome:       DirectoryMatched,
		AcceptedEmail: email,
		Profile:       &profile,
	}
	if len(owners) == 0 {
		evaluation.CreatePerson = true
		return evaluation
	}
	evaluation.EntityID = owners[0]
	return evaluation
}

func (policy DirectoryPolicy) canAutomaticallyAccept(query DirectoryQuery, email string, profile DirectoryProfile) bool {
	if !hasDirectoryIdentityBinding(profile) {
		return false
	}
	if query.EmailEvidence != EmailEvidenceSourceBound && query.EmailEvidence != EmailEvidenceReviewerSupplied {
		return false
	}
	if _, approved := policy.approvedDomains[emailDomain(email)]; !approved {
		return false
	}
	return profile.Source == DirectorySourceDomainProfile
}

// hasDirectoryIdentityBinding verifies that an automatic decision can retain a
// provider-scoped directory identity. ObservedAt is intentionally not checked:
// source observation time may be unknown.
func hasDirectoryIdentityBinding(profile DirectoryProfile) bool {
	return strings.TrimSpace(profile.Provider) != "" && strings.TrimSpace(profile.SubjectID) != ""
}

func matchingDirectoryProfiles(email string, profiles []DirectoryProfile) []DirectoryProfile {
	matches := make([]DirectoryProfile, 0, len(profiles))
	for _, profile := range profiles {
		for _, profileEmail := range profile.Emails {
			if NormalizeEmail(profileEmail.Value) == email {
				matches = append(matches, profile)
				break
			}
		}
	}
	return matches
}

func acceptedEmailOwners(email string, snapshots []EntitySnapshot) []string {
	owners := make(map[string]struct{})
	for _, snapshot := range snapshots {
		if snapshot.Kind != KindPerson || snapshot.ID == "" {
			continue
		}
		for _, alias := range snapshot.Aliases {
			if alias.Type == AliasTypeEmail && NormalizeEmail(alias.Value) == email {
				owners[snapshot.ID] = struct{}{}
				break
			}
		}
	}

	identifiers := make([]string, 0, len(owners))
	for identifier := range owners {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	return identifiers
}

func conflictingDirectoryLink(profile DirectoryProfile, owners []string, links []DirectoryIdentityLink) bool {
	linkedEntities := make(map[string]struct{})
	for _, link := range links {
		if link.Provider == profile.Provider && link.SubjectID == profile.SubjectID && link.EntityID != "" {
			linkedEntities[link.EntityID] = struct{}{}
		}
	}
	if len(linkedEntities) == 0 {
		return false
	}
	if len(owners) == 0 {
		return true
	}
	_, ownedByEmail := linkedEntities[owners[0]]
	return len(linkedEntities) != 1 || !ownedByEmail
}

func sortedDirectoryProfiles(profiles []DirectoryProfile) []DirectoryProfile {
	sorted := append([]DirectoryProfile(nil), profiles...)
	sort.Slice(sorted, func(left, right int) bool {
		return directoryProfileSortKey(sorted[left]) < directoryProfileSortKey(sorted[right])
	})
	return sorted
}

func directoryProfileSortKey(profile DirectoryProfile) string {
	emails := make([]string, 0, len(profile.Emails))
	for _, email := range profile.Emails {
		primary := "0"
		if email.Primary {
			primary = "1"
		}
		emails = append(emails, NormalizeEmail(email.Value)+"\x00"+primary)
	}
	sort.Strings(emails)
	return strings.Join([]string{
		profile.Provider,
		profile.SubjectID,
		string(profile.Source),
		NormalizeName(profile.DisplayName),
		strings.Join(emails, "\x01"),
		profile.ObservedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
}

func emailDomain(email string) string {
	_, domain, found := strings.Cut(email, "@")
	if !found {
		return ""
	}
	return domain
}

func validDomain(domain string) bool {
	if len(domain) == 0 || len(domain) > 253 {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || !isASCIIAlphaNumeric(label[0]) || !isASCIIAlphaNumeric(label[len(label)-1]) {
			return false
		}
		for index := 0; index < len(label); index++ {
			if !isASCIIAlphaNumeric(label[index]) && label[index] != '-' {
				return false
			}
		}
	}
	return true
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}
