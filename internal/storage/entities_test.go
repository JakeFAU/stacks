package storage

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
)

func TestPutAliasRejectsMalformedEmailBeforePersistence(t *testing.T) {
	repository := &EntityRepository{}

	_, err := repository.PutAlias(context.Background(), AliasInput{
		EntityID:        "11111111-2222-3333-4444-555555555555",
		NormalizedValue: "riya@@synthetic.example",
		Type:            "email",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("PutAlias() error = %v, want malformed email rejection", err)
	}
}

func TestCreateMentionRejectsMalformedEmailBeforePersistence(t *testing.T) {
	repository := &EntityRepository{}

	_, err := repository.CreateMention(context.Background(), MentionInput{
		EvidenceSpanID: "11111111-2222-3333-4444-555555555555",
		Surface:        "Riya Chen",
		Email:          "riya@@synthetic.example",
		Role:           "speaker",
	})
	if err == nil || !strings.Contains(err.Error(), "email is invalid") {
		t.Fatalf("CreateMention() error = %v, want malformed email rejection", err)
	}
}

func TestDirectoryDecisionDigestPreservesLegacyDigestWhenNoDirectoryEvidence(t *testing.T) {
	digest := resolutionDecisionDigest(ResolutionDecisionInput{
		ProposalID: "11111111-2222-3333-4444-555555555555",
		Outcome:    ResolutionOutcomeAccepted,
		EntityID:   "66666666-7777-8888-9999-aaaaaaaaaaaa",
	}, "")

	const legacyDigest = "87b89f76caa59c004884826d40c650621f11c9d586c1fa32ba6339ad78e671cd"
	if got := hex.EncodeToString(digest[:]); got != legacyDigest {
		t.Fatalf("resolutionDecisionDigest() = %q, want byte-compatible legacy digest %q", got, legacyDigest)
	}
}

func TestMaskReviewEmailRetainsOnlyFirstLocalRuneAndDomain(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		email string
		want  string
	}{
		{name: "ordinary", email: "riya.chen@corp.example", want: "r***@corp.example"},
		{name: "single local rune", email: "r@corp.example", want: "r***@corp.example"},
		{name: "unicode local rune", email: "ø.person@corp.example", want: "ø***@corp.example"},
		{name: "empty local part", email: "@corp.example", want: ""},
		{name: "missing domain", email: "riya.chen@", want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := maskReviewEmail(testCase.email); got != testCase.want {
				t.Fatalf("maskReviewEmail(%q) = %q, want %q", testCase.email, got, testCase.want)
			}
		})
	}
}
