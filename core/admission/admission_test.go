package admission_test

import (
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/admission"
)

func TestAdmissionReasonCodeEnforcesUnicodeRuneBoundary(t *testing.T) {
	input := admission.DecisionInput{
		ID:         "admission-1",
		TargetKind: admission.TargetObservation,
		TargetID:   "observation-1",
		Outcome:    admission.Quarantined,
		Authority:  admission.AuthorityPolicy,
		RecordedAt: time.Date(2026, time.July, 25, 12, 30, 0, 0, time.UTC),
	}
	input.ReasonCode = strings.Repeat("界", 128)
	if _, err := admission.NewDecision(input); err != nil {
		t.Fatalf("NewDecision(128 runes) error = %v", err)
	}

	input.ReasonCode = strings.Repeat("界", 129)
	if _, err := admission.NewDecision(input); err == nil {
		t.Fatal("NewDecision(129 runes) error = nil, want bounded reason rejected")
	}
}

func TestAdmissionAuthorityRequiresBoundedLocalShape(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 25, 12, 30, 0, 0, time.UTC)
	valid := admission.DecisionInput{
		ID:         "admission-1",
		TargetKind: admission.TargetObservation,
		TargetID:   "observation-1",
		Outcome:    admission.Quarantined,
		ReasonCode: "conflicting_evidence",
		Authority:  admission.AuthorityPolicy,
		RecordedAt: recordedAt,
	}
	tests := []struct {
		name   string
		mutate func(*admission.DecisionInput)
	}{
		{name: "blank decision ID", mutate: func(input *admission.DecisionInput) { input.ID = " " }},
		{name: "unknown target kind", mutate: func(input *admission.DecisionInput) { input.TargetKind = "document" }},
		{name: "blank target ID", mutate: func(input *admission.DecisionInput) { input.TargetID = " " }},
		{name: "unknown outcome", mutate: func(input *admission.DecisionInput) { input.Outcome = "pending" }},
		{name: "blank reason code", mutate: func(input *admission.DecisionInput) { input.ReasonCode = " " }},
		{name: "unknown authority", mutate: func(input *admission.DecisionInput) { input.Authority = "model" }},
		{name: "missing recorded time", mutate: func(input *admission.DecisionInput) { input.RecordedAt = time.Time{} }},
		{name: "self supersession", mutate: func(input *admission.DecisionInput) { input.SupersedesID = input.ID }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			input := valid
			testCase.mutate(&input)
			if _, err := admission.NewDecision(input); err == nil {
				t.Fatal("NewDecision() error = nil, want malformed authority rejected")
			}
		})
	}
}

func TestAdmissionDecisionPreservesTargetOutcomeAuthorityAndSupersession(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 25, 12, 30, 0, 0, time.UTC)
	decision, err := admission.NewDecision(admission.DecisionInput{
		ID:           "admission:observation/17#review",
		TargetKind:   admission.TargetObservation,
		TargetID:     "observation:document/17#claim-2",
		Outcome:      admission.Admitted,
		ReasonCode:   "  reviewer_confirmed_source  ",
		Authority:    admission.AuthorityReviewer,
		RecordedAt:   recordedAt,
		SupersedesID: "admission:observation/17#automatic",
	})
	if err != nil {
		t.Fatalf("NewDecision() error = %v", err)
	}
	if decision.ID() != "admission:observation/17#review" ||
		decision.TargetKind() != admission.TargetObservation ||
		decision.TargetID() != "observation:document/17#claim-2" ||
		decision.Outcome() != admission.Admitted ||
		decision.ReasonCode() != "  reviewer_confirmed_source  " ||
		decision.Authority() != admission.AuthorityReviewer ||
		decision.SupersedesID() != "admission:observation/17#automatic" {
		t.Fatalf("decision = %#v, want exact authority fields preserved", decision)
	}
}

func TestAdmissionDecisionDigestChangesForEveryAuthorityField(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 25, 12, 30, 0, 0, time.UTC)
	base := admission.DecisionInput{
		ID:           "admission-2",
		TargetKind:   admission.TargetObservation,
		TargetID:     "observation-1",
		Outcome:      admission.Quarantined,
		ReasonCode:   "conflicting_evidence",
		Authority:    admission.AuthorityPolicy,
		RecordedAt:   recordedAt,
		SupersedesID: "admission-1",
	}
	first, err := admission.NewDecision(base)
	if err != nil {
		t.Fatalf("NewDecision() error = %v", err)
	}
	mutations := []struct {
		name   string
		mutate func(*admission.DecisionInput)
	}{
		{name: "target kind", mutate: func(input *admission.DecisionInput) { input.TargetKind = admission.TargetMention }},
		{name: "target ID", mutate: func(input *admission.DecisionInput) { input.TargetID = "observation-2" }},
		{name: "outcome", mutate: func(input *admission.DecisionInput) { input.Outcome = admission.Retired }},
		{name: "reason code", mutate: func(input *admission.DecisionInput) { input.ReasonCode = "policy_changed" }},
		{name: "authority", mutate: func(input *admission.DecisionInput) { input.Authority = admission.AuthorityReviewer }},
		{name: "recorded time", mutate: func(input *admission.DecisionInput) { input.RecordedAt = input.RecordedAt.Add(time.Microsecond) }},
		{name: "superseded decision", mutate: func(input *admission.DecisionInput) { input.SupersedesID = "admission-other" }},
	}
	for _, testCase := range mutations {
		t.Run(testCase.name, func(t *testing.T) {
			input := base
			input.ID = "admission-changed-" + testCase.name
			testCase.mutate(&input)
			changed, err := admission.NewDecision(input)
			if err != nil {
				t.Fatalf("NewDecision() error = %v", err)
			}
			if first.Digest() == changed.Digest() {
				t.Fatalf("Digest() unchanged when %s changed", testCase.name)
			}
		})
	}
	if first.DigestVersion() != admission.DecisionDigestVersion {
		t.Fatalf("DigestVersion() = %q, want %q", first.DigestVersion(), admission.DecisionDigestVersion)
	}
}
