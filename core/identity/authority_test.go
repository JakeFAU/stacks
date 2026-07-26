package identity_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
)

func TestIdentityReasonCodesEnforceUnicodeRuneBoundary(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 25, 12, 30, 0, 0, time.UTC)
	validReason := strings.Repeat("界", 128)
	tooLongReason := strings.Repeat("界", 129)

	tests := []struct {
		name      string
		construct func(string) error
	}{
		{name: "proposal", construct: func(reason string) error {
			_, err := identity.NewResolutionProposal(identity.ResolutionProposalInput{
				ID:          "proposal-1",
				MentionID:   "mention-1",
				ReasonCode:  reason,
				EvidenceIDs: []evidence.EvidenceID{"evidence-1"},
				RecordedAt:  recordedAt,
			})
			return err
		}},
		{name: "candidate", construct: func(reason string) error {
			_, err := identity.NewResolutionCandidate(identity.ResolutionCandidateInput{
				ID:         "candidate-1",
				ProposalID: "proposal-1",
				EntityID:   "entity-1",
				Rank:       1,
				Confidence: 0.5,
				ReasonCode: reason,
				Source:     identity.CandidateSource{Kind: "accepted_alias", Reference: "alias-1"},
				RecordedAt: recordedAt,
			})
			return err
		}},
		{name: "decision", construct: func(reason string) error {
			_, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
				ID:         "decision-1",
				ProposalID: "proposal-1",
				Outcome:    identity.DecisionRejected,
				Authority:  identity.AuthorityReviewer,
				ReasonCode: reason,
				RecordedAt: recordedAt,
			})
			return err
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name+"/128_runes", func(t *testing.T) {
			if err := testCase.construct(validReason); err != nil {
				t.Fatalf("constructor error = %v, want 128-rune reason accepted", err)
			}
		})
		t.Run(testCase.name+"/129_runes", func(t *testing.T) {
			if err := testCase.construct(tooLongReason); err == nil {
				t.Fatal("constructor error = nil, want 129-rune reason rejected")
			}
		})
	}
}

func TestIdentityAuthorityRejectsMalformedLocalShape(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 25, 12, 30, 0, 0, time.UTC)
	entityInput := identity.EntityInput{ID: "entity-1", Kind: identity.KindPerson, DisplayName: "Synthetic Person", RecordedAt: recordedAt}
	for _, testCase := range []struct {
		name   string
		mutate func(*identity.EntityInput)
	}{
		{name: "blank entity ID", mutate: func(input *identity.EntityInput) { input.ID = " " }},
		{name: "unknown entity kind", mutate: func(input *identity.EntityInput) { input.Kind = "unknown" }},
		{name: "blank display name", mutate: func(input *identity.EntityInput) { input.DisplayName = " " }},
		{name: "missing recorded time", mutate: func(input *identity.EntityInput) { input.RecordedAt = time.Time{} }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := entityInput
			testCase.mutate(&input)
			if _, err := identity.NewEntity(input); err == nil {
				t.Fatal("NewEntity() error = nil, want malformed entity rejected")
			}
		})
	}

	mentionInput := identity.MentionInput{
		ID:              "mention-1",
		EvidenceID:      "evidence-1",
		DerivationRunID: "run-1",
		Surface:         "Synthetic Person",
		NormalizedName:  "synthetic person",
		Role:            "speaker",
		RecordedAt:      recordedAt,
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*identity.MentionInput)
	}{
		{name: "blank mention ID", mutate: func(input *identity.MentionInput) { input.ID = " " }},
		{name: "blank evidence ID", mutate: func(input *identity.MentionInput) { input.EvidenceID = " " }},
		{name: "blank derivation run ID", mutate: func(input *identity.MentionInput) { input.DerivationRunID = " " }},
		{name: "blank surface", mutate: func(input *identity.MentionInput) { input.Surface = " " }},
		{name: "blank normalized name", mutate: func(input *identity.MentionInput) { input.NormalizedName = " " }},
		{name: "blank role", mutate: func(input *identity.MentionInput) { input.Role = " " }},
		{name: "missing recorded time", mutate: func(input *identity.MentionInput) { input.RecordedAt = time.Time{} }},
		{name: "malformed proposed email", mutate: func(input *identity.MentionInput) {
			input.ProposedEmail = "not-an-email"
			input.ProposedEmailEvidenceID = "evidence-1"
		}},
		{name: "proposed email without evidence", mutate: func(input *identity.MentionInput) { input.ProposedEmail = "synthetic@example.test" }},
		{name: "proposed email evidence without email", mutate: func(input *identity.MentionInput) { input.ProposedEmailEvidenceID = "evidence-1" }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := mentionInput
			testCase.mutate(&input)
			if _, err := identity.NewMention(input); err == nil {
				t.Fatal("NewMention() error = nil, want malformed mention rejected")
			}
		})
	}

	proposalInput := identity.ResolutionProposalInput{
		ID:          "proposal-1",
		MentionID:   "mention-1",
		ReasonCode:  "identity_review",
		EvidenceIDs: []evidence.EvidenceID{"evidence-1"},
		RecordedAt:  recordedAt,
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*identity.ResolutionProposalInput)
	}{
		{name: "blank proposal ID", mutate: func(input *identity.ResolutionProposalInput) { input.ID = " " }},
		{name: "blank mention ID", mutate: func(input *identity.ResolutionProposalInput) { input.MentionID = " " }},
		{name: "blank reason code", mutate: func(input *identity.ResolutionProposalInput) { input.ReasonCode = " " }},
		{name: "missing evidence", mutate: func(input *identity.ResolutionProposalInput) { input.EvidenceIDs = nil }},
		{name: "blank evidence ID", mutate: func(input *identity.ResolutionProposalInput) { input.EvidenceIDs = []evidence.EvidenceID{" "} }},
		{name: "duplicate evidence ID", mutate: func(input *identity.ResolutionProposalInput) {
			input.EvidenceIDs = []evidence.EvidenceID{"evidence-1", "evidence-1"}
		}},
		{name: "missing recorded time", mutate: func(input *identity.ResolutionProposalInput) { input.RecordedAt = time.Time{} }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := proposalInput
			testCase.mutate(&input)
			if _, err := identity.NewResolutionProposal(input); err == nil {
				t.Fatal("NewResolutionProposal() error = nil, want malformed proposal rejected")
			}
		})
	}
}

func TestIdentityAuthorityDefensivelyCopiesProposalEvidence(t *testing.T) {
	evidenceIDs := []evidence.EvidenceID{"evidence-1", "evidence-2"}
	proposal, err := identity.NewResolutionProposal(identity.ResolutionProposalInput{
		ID:          "proposal-1",
		MentionID:   "mention-1",
		ReasonCode:  "  exact_reason_code  ",
		EvidenceIDs: evidenceIDs,
		RecordedAt:  time.Date(2026, time.July, 25, 12, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NewResolutionProposal() error = %v", err)
	}
	evidenceIDs[0] = "changed-input"
	got := proposal.EvidenceIDs()
	got[1] = "changed-output"
	if proposal.EvidenceIDs()[0] != "evidence-1" || proposal.EvidenceIDs()[1] != "evidence-2" {
		t.Fatalf("EvidenceIDs() = %#v, want immutable copy", proposal.EvidenceIDs())
	}
	if proposal.ReasonCode() != "  exact_reason_code  " {
		t.Fatalf("ReasonCode() = %q, want exact code preserved", proposal.ReasonCode())
	}
}

func TestResolutionCandidateRequiresPositiveRankAndFiniteUnitConfidence(t *testing.T) {
	valid := identity.ResolutionCandidateInput{
		ID:         "candidate-1",
		ProposalID: "proposal-1",
		EntityID:   "entity-1",
		Rank:       1,
		Confidence: 0.5,
		ReasonCode: "exact_name",
		Source:     identity.CandidateSource{Kind: "accepted_alias", Reference: "alias-1"},
		RecordedAt: time.Date(2026, time.July, 25, 12, 30, 0, 0, time.UTC),
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*identity.ResolutionCandidateInput)
	}{
		{name: "zero rank", mutate: func(input *identity.ResolutionCandidateInput) { input.Rank = 0 }},
		{name: "negative confidence", mutate: func(input *identity.ResolutionCandidateInput) { input.Confidence = -0.1 }},
		{name: "confidence above one", mutate: func(input *identity.ResolutionCandidateInput) { input.Confidence = 1.1 }},
		{name: "NaN confidence", mutate: func(input *identity.ResolutionCandidateInput) { input.Confidence = math.NaN() }},
		{name: "infinite confidence", mutate: func(input *identity.ResolutionCandidateInput) { input.Confidence = math.Inf(1) }},
		{name: "blank source kind", mutate: func(input *identity.ResolutionCandidateInput) { input.Source.Kind = " " }},
		{name: "blank source reference", mutate: func(input *identity.ResolutionCandidateInput) { input.Source.Reference = " " }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := valid
			testCase.mutate(&input)
			if _, err := identity.NewResolutionCandidate(input); err == nil {
				t.Fatal("NewResolutionCandidate() error = nil, want invalid candidate rejected")
			}
		})
	}
}

func TestIdentityAuthorityAcceptsOpaqueTrimmedIDs(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 25, 12, 30, 0, 0, time.UTC)
	entityValue, err := identity.NewEntity(identity.EntityInput{
		ID:          "entity:person/external-17",
		Kind:        identity.KindPerson,
		DisplayName: "Synthetic Person",
		RecordedAt:  recordedAt,
	})
	if err != nil {
		t.Fatalf("NewEntity() error = %v", err)
	}
	if entityValue.ID() != "entity:person/external-17" {
		t.Fatalf("entity ID = %q, want opaque ID preserved", entityValue.ID())
	}

	mention, err := identity.NewMention(identity.MentionInput{
		ID:              "mention:document/17#person-1",
		EvidenceID:      "evidence:document/17#span-1",
		DerivationRunID: "run:extract/17",
		Surface:         "Synthetic Person",
		NormalizedName:  "synthetic person",
		Role:            "speaker",
		RecordedAt:      recordedAt,
	})
	if err != nil {
		t.Fatalf("NewMention() error = %v", err)
	}
	if mention.ID() != "mention:document/17#person-1" {
		t.Fatalf("mention ID = %q, want opaque ID preserved", mention.ID())
	}

	proposal, err := identity.NewResolutionProposal(identity.ResolutionProposalInput{
		ID:          "proposal:mention/17",
		MentionID:   mention.ID(),
		ReasonCode:  "identity_review",
		EvidenceIDs: []evidence.EvidenceID{"evidence:document/17#span-1"},
		RecordedAt:  recordedAt,
	})
	if err != nil {
		t.Fatalf("NewResolutionProposal() error = %v", err)
	}
	if proposal.ID() != "proposal:mention/17" {
		t.Fatalf("proposal ID = %q, want opaque ID preserved", proposal.ID())
	}

	candidate, err := identity.NewResolutionCandidate(identity.ResolutionCandidateInput{
		ID:         "candidate:proposal/17#rank-1",
		ProposalID: proposal.ID(),
		EntityID:   entityValue.ID(),
		Rank:       1,
		Confidence: 0.75,
		ReasonCode: "exact_name",
		Source: identity.CandidateSource{
			Kind:      "accepted_alias",
			Reference: "alias:name/17",
		},
		RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("NewResolutionCandidate() error = %v", err)
	}
	if candidate.ID() != "candidate:proposal/17#rank-1" {
		t.Fatalf("candidate ID = %q, want opaque ID preserved", candidate.ID())
	}

	decision, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
		ID:         "decision:proposal/17#initial",
		ProposalID: proposal.ID(),
		Outcome:    identity.DecisionAccepted,
		EntityID:   entityValue.ID(),
		Authority:  identity.AuthorityReviewer,
		ReasonCode: "reviewer_confirmed",
		RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("NewResolutionDecision() error = %v", err)
	}
	if decision.ID() != "decision:proposal/17#initial" {
		t.Fatalf("decision ID = %q, want opaque ID preserved", decision.ID())
	}

	assertion, err := identity.NewAliasAssertion(identity.AliasAssertionInput{
		ID:         "alias-assertion:decision/17#name",
		DecisionID: decision.ID(),
		EntityID:   entityValue.ID(),
		Alias:      identity.Alias{Type: identity.AliasTypeName, Value: "Synthetic Person"},
		RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("NewAliasAssertion() error = %v", err)
	}
	if assertion.ID() != "alias-assertion:decision/17#name" {
		t.Fatalf("alias assertion ID = %q, want opaque ID preserved", assertion.ID())
	}
}

func TestResolutionDecisionRequiresBoundedAuthorityAndOutcome(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 25, 12, 30, 0, 0, time.UTC)
	valid := identity.ResolutionDecisionInput{
		ID:         "decision-1",
		ProposalID: "proposal-1",
		Outcome:    identity.DecisionAccepted,
		EntityID:   "entity-1",
		Authority:  identity.AuthorityReviewer,
		ReasonCode: "reviewer_confirmed",
		RecordedAt: recordedAt,
	}
	tests := []struct {
		name   string
		mutate func(*identity.ResolutionDecisionInput)
	}{
		{name: "unknown outcome", mutate: func(input *identity.ResolutionDecisionInput) {
			input.Outcome = "maybe"
		}},
		{name: "unknown authority", mutate: func(input *identity.ResolutionDecisionInput) {
			input.Authority = "model"
		}},
		{name: "accepted without entity", mutate: func(input *identity.ResolutionDecisionInput) {
			input.EntityID = ""
		}},
		{name: "rejected with entity", mutate: func(input *identity.ResolutionDecisionInput) {
			input.Outcome = identity.DecisionRejected
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			input := valid
			testCase.mutate(&input)
			if _, err := identity.NewResolutionDecision(input); err == nil {
				t.Fatal("NewResolutionDecision() error = nil, want invalid authority shape rejected")
			}
		})
	}

	rejected := valid
	rejected.Outcome = identity.DecisionRejected
	rejected.EntityID = ""
	if _, err := identity.NewResolutionDecision(rejected); err != nil {
		t.Fatalf("NewResolutionDecision(valid rejection) error = %v", err)
	}
}

func TestResolutionDecisionDigestIncludesSupersessionAndAuthority(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 25, 12, 30, 0, 0, time.UTC)
	base := identity.ResolutionDecisionInput{
		ID:           "decision-2",
		ProposalID:   "proposal-1",
		Outcome:      identity.DecisionAccepted,
		EntityID:     "entity-1",
		Authority:    identity.AuthorityReviewer,
		ReasonCode:   "reviewer_confirmed",
		RecordedAt:   recordedAt,
		SupersedesID: "decision-1",
	}
	first, err := identity.NewResolutionDecision(base)
	if err != nil {
		t.Fatalf("NewResolutionDecision() error = %v", err)
	}

	changedSupersession := base
	changedSupersession.ID = "decision-3"
	changedSupersession.SupersedesID = "decision-other"
	second, err := identity.NewResolutionDecision(changedSupersession)
	if err != nil {
		t.Fatalf("NewResolutionDecision(changed supersession) error = %v", err)
	}
	if first.Digest() == second.Digest() {
		t.Fatal("Digest() unchanged when superseded decision changed")
	}

	changedAuthority := base
	changedAuthority.ID = "decision-4"
	changedAuthority.Authority = identity.AuthorityAutomatic
	third, err := identity.NewResolutionDecision(changedAuthority)
	if err != nil {
		t.Fatalf("NewResolutionDecision(changed authority) error = %v", err)
	}
	if first.Digest() == third.Digest() {
		t.Fatal("Digest() unchanged when decision authority changed")
	}
	if first.DigestVersion() != identity.ResolutionDecisionDigestVersion {
		t.Fatalf("DigestVersion() = %q, want %q", first.DigestVersion(), identity.ResolutionDecisionDigestVersion)
	}
}

func TestAliasAssertionIsOwnedByAcceptedDecision(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 25, 12, 30, 0, 0, time.UTC)
	assertion, err := identity.NewAliasAssertion(identity.AliasAssertionInput{
		ID:         "alias-assertion-1",
		DecisionID: "accepted-decision-1",
		EntityID:   "entity-1",
		Alias:      identity.Alias{Type: identity.AliasTypeEmail, Value: "synthetic.person@example.test"},
		RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("NewAliasAssertion() error = %v", err)
	}
	if assertion.DecisionID() != "accepted-decision-1" ||
		assertion.EntityID() != "entity-1" ||
		assertion.Alias() != (identity.Alias{Type: identity.AliasTypeEmail, Value: "synthetic.person@example.test"}) {
		t.Fatalf("alias assertion = (%q, %q, %#v), want accepted-decision ownership preserved", assertion.DecisionID(), assertion.EntityID(), assertion.Alias())
	}

	for _, input := range []identity.AliasAssertionInput{
		{ID: "assertion-2", EntityID: "entity-1", Alias: identity.Alias{Type: identity.AliasTypeName, Value: "Synthetic Person"}, RecordedAt: recordedAt},
		{ID: "assertion-3", DecisionID: "decision-1", Alias: identity.Alias{Type: identity.AliasTypeName, Value: "Synthetic Person"}, RecordedAt: recordedAt},
		{ID: "assertion-4", DecisionID: "decision-1", EntityID: "entity-1", Alias: identity.Alias{Type: "unknown", Value: "Synthetic Person"}, RecordedAt: recordedAt},
	} {
		if _, err := identity.NewAliasAssertion(input); err == nil {
			t.Fatalf("NewAliasAssertion(%#v) error = nil, want invalid ownership shape rejected", input)
		}
	}
}

func TestIdentityAndAdmissionAuthorityNormalizeRecordedTime(t *testing.T) {
	location := time.FixedZone("synthetic-offset", -4*60*60)
	inputTime := time.Date(2026, time.July, 25, 8, 30, 0, 987654321, location)
	want := time.Date(2026, time.July, 25, 12, 30, 0, 987654000, time.UTC)

	identityDecision, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
		ID:         "decision-1",
		ProposalID: "proposal-1",
		Outcome:    identity.DecisionRejected,
		Authority:  identity.AuthorityReviewer,
		ReasonCode: "not_same_person",
		RecordedAt: inputTime,
	})
	if err != nil {
		t.Fatalf("NewResolutionDecision() error = %v", err)
	}
	admissionDecision, err := admission.NewDecision(admission.DecisionInput{
		ID:         "admission-1",
		TargetKind: admission.TargetIdentityDecision,
		TargetID:   "decision-1",
		Outcome:    admission.Quarantined,
		ReasonCode: "awaiting_review",
		Authority:  admission.AuthorityPolicy,
		RecordedAt: inputTime,
	})
	if err != nil {
		t.Fatalf("admission.NewDecision() error = %v", err)
	}

	if identityDecision.RecordedAt() != want {
		t.Fatalf("identity RecordedAt() = %s, want %s", identityDecision.RecordedAt(), want)
	}
	if admissionDecision.RecordedAt() != want {
		t.Fatalf("admission RecordedAt() = %s, want %s", admissionDecision.RecordedAt(), want)
	}
}
