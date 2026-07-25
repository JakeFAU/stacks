package identity

import (
	"sort"
	"strings"
)

// Resolver resolves person mentions only from accepted identifiers. Other
// similarity remains a ranked suggestion for explicit review.
type Resolver struct{}

// Resolve returns an automatic identity only when exactly one person has an
// accepted exact email or accepted exact name alias. Every other match is kept
// as a deterministic ranked candidate for review.
func (Resolver) Resolve(mention Mention, snapshots []EntitySnapshot) Resolution {
	normalizedName := NormalizeName(mention.Name)
	normalizedEmail := NormalizeEmail(mention.Email)
	if !ValidEmail(normalizedEmail) {
		normalizedEmail = ""
	}
	exactMatches := matchingAcceptedIdentifiers(normalizedName, normalizedEmail, snapshots)
	if len(exactMatches) == 1 {
		return Resolution{EntityID: exactMatches[0], AutoResolved: true}
	}

	return Resolution{Candidates: rankCandidates(normalizedName, snapshots)}
}

func matchingAcceptedIdentifiers(normalizedName, normalizedEmail string, snapshots []EntitySnapshot) []string {
	matches := make(map[string]struct{})
	for _, snapshot := range snapshots {
		if snapshot.Kind != KindPerson || snapshot.ID == "" {
			continue
		}
		for _, alias := range snapshot.Aliases {
			switch alias.Type {
			case AliasTypeEmail:
				if normalizedEmail != "" && normalizedEmail == NormalizeEmail(alias.Value) {
					matches[snapshot.ID] = struct{}{}
				}
			case AliasTypeName:
				if normalizedName != "" && normalizedName == NormalizeName(alias.Value) {
					matches[snapshot.ID] = struct{}{}
				}
			}
		}
	}

	identifiers := make([]string, 0, len(matches))
	for identifier := range matches {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	return identifiers
}

func rankCandidates(normalizedName string, snapshots []EntitySnapshot) []Candidate {
	if normalizedName == "" || strings.Contains(normalizedName, "@") {
		return nil
	}
	candidates := make(map[string]Candidate)
	for _, snapshot := range snapshots {
		if snapshot.Kind != KindPerson || snapshot.ID == "" {
			continue
		}
		confidence, reason := nameConfidence(normalizedName, NormalizeName(snapshot.DisplayName))
		for _, alias := range snapshot.Aliases {
			if alias.Type != AliasTypeName {
				continue
			}
			aliasConfidence, aliasReason := nameConfidence(normalizedName, NormalizeName(alias.Value))
			if aliasConfidence > confidence {
				confidence, reason = aliasConfidence, "accepted alias "+aliasReason
			}
		}
		if confidence == 0 {
			continue
		}
		candidates[snapshot.ID] = Candidate{EntityID: snapshot.ID, Confidence: confidence, Reason: reason}
	}

	ranked := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		ranked = append(ranked, candidate)
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].Confidence != ranked[right].Confidence {
			return ranked[left].Confidence > ranked[right].Confidence
		}
		return ranked[left].EntityID < ranked[right].EntityID
	})
	return ranked
}

func nameConfidence(mention, candidate string) (float64, string) {
	if candidate == "" {
		return 0, ""
	}
	if mention == candidate {
		return 1, "exact name match"
	}
	mentionTokens := strings.Fields(mention)
	candidateTokens := strings.Fields(candidate)
	if len(mentionTokens) == 0 || len(candidateTokens) == 0 {
		return 0, ""
	}
	shared := 0
	seen := make(map[string]struct{}, len(candidateTokens))
	for _, token := range candidateTokens {
		seen[token] = struct{}{}
	}
	for _, token := range mentionTokens {
		if _, exists := seen[token]; exists {
			shared++
		}
	}
	if shared == 0 {
		return 0, ""
	}
	denominator := len(mentionTokens)
	if len(candidateTokens) > denominator {
		denominator = len(candidateTokens)
	}
	return float64(shared) / float64(denominator), "name token overlap"
}
