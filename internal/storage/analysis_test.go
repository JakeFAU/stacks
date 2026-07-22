package storage

import (
	"crypto/sha256"
	"testing"

	analysisdomain "stacks/internal/analysis"
)

func TestComputeAnalysisDigestIncludesPairVersionsAndOrderedInputs(t *testing.T) {
	firstDigest := sha256.Sum256([]byte("first-input"))
	secondDigest := sha256.Sum256([]byte("second-input"))
	base := AnalysisInput{
		EmployeeEntityID:      "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ManagerEntityID:       "11111111-2222-3333-4444-555555555555",
		AnalysisPromptVersion: "analyze-v1",
		PolicyVersion:         "policy-v1",
		Inputs: []AnalysisInputReference{
			{Kind: AnalysisInputKindSignal, ID: "00000000-0000-0000-0000-000000000003", Digest: firstDigest[:]},
			{Kind: AnalysisInputKindSignal, ID: "00000000-0000-0000-0000-000000000004", Digest: secondDigest[:]},
		},
	}

	baseline, err := ComputeAnalysisDigest(base)
	if err != nil {
		t.Fatalf("compute baseline digest: %v", err)
	}

	tests := []struct {
		name  string
		input AnalysisInput
	}{
		{
			name: "employee changes",
			input: func() AnalysisInput {
				changed := base
				changed.EmployeeEntityID = "00000000-0000-0000-0000-000000000005"
				return changed
			}(),
		},
		{
			name: "prompt version changes",
			input: func() AnalysisInput {
				changed := base
				changed.AnalysisPromptVersion = "analyze-v2"
				return changed
			}(),
		},
		{
			name: "input order changes",
			input: func() AnalysisInput {
				changed := base
				changed.Inputs = []AnalysisInputReference{base.Inputs[1], base.Inputs[0]}
				return changed
			}(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ComputeAnalysisDigest(test.input)
			if err != nil {
				t.Fatalf("compute changed digest: %v", err)
			}
			if got == baseline {
				t.Fatal("changed analysis identity produced the baseline digest")
			}
		})
	}
}

func TestComputeAnalysisDigestCanonicalizesUUIDs(t *testing.T) {
	digest := sha256.Sum256([]byte("input"))
	canonical := AnalysisInput{
		EmployeeEntityID:      "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ManagerEntityID:       "11111111-2222-3333-4444-555555555555",
		AnalysisPromptVersion: "analyze-v1",
		PolicyVersion:         "policy-v1",
		Inputs: []AnalysisInputReference{{
			Kind:   AnalysisInputKindSignal,
			ID:     "99999999-aaaa-bbbb-cccc-dddddddddddd",
			Digest: digest[:],
		}},
	}
	variant := canonical
	variant.EmployeeEntityID = "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"
	variant.Inputs = append([]AnalysisInputReference(nil), canonical.Inputs...)
	variant.Inputs[0].ID = "99999999-AAAA-BBBB-CCCC-DDDDDDDDDDDD"

	canonicalDigest, err := ComputeAnalysisDigest(canonical)
	if err != nil {
		t.Fatalf("compute canonical digest: %v", err)
	}
	variantDigest, err := ComputeAnalysisDigest(variant)
	if err != nil {
		t.Fatalf("compute variant digest: %v", err)
	}
	if canonicalDigest != variantDigest {
		t.Fatal("equivalent UUID spellings produced distinct analysis digests")
	}
}

func TestComputeAnalysisDigestRejectsInvalidUUID(t *testing.T) {
	digest := sha256.Sum256([]byte("input"))
	_, err := ComputeAnalysisDigest(AnalysisInput{
		EmployeeEntityID:      "not-a-uuid",
		ManagerEntityID:       "11111111-2222-3333-4444-555555555555",
		AnalysisPromptVersion: "analyze-v1",
		PolicyVersion:         "policy-v1",
		Inputs: []AnalysisInputReference{{
			Kind:   AnalysisInputKindSignal,
			ID:     "99999999-aaaa-bbbb-cccc-dddddddddddd",
			Digest: digest[:],
		}},
	})
	if err == nil {
		t.Fatal("invalid employee UUID was accepted")
	}
}

func TestLegacyAndTemporalAnalysisDigestsUseSeparateNamespaces(t *testing.T) {
	inputDigest := sha256.Sum256([]byte("shared-input"))
	legacy, err := ComputeAnalysisDigest(AnalysisInput{
		EmployeeEntityID:      "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ManagerEntityID:       "11111111-2222-3333-4444-555555555555",
		AnalysisPromptVersion: "analyze-v1",
		PolicyVersion:         "policy-v1",
		Inputs: []AnalysisInputReference{{
			Kind: AnalysisInputKindSignal, ID: "99999999-aaaa-bbbb-cccc-dddddddddddd", Digest: inputDigest[:],
		}},
	})
	if err != nil {
		t.Fatalf("ComputeAnalysisDigest() error = %v", err)
	}
	temporal, err := analysisdomain.ComputeInputDigest(analysisdomain.AnalysisIdentity{
		EmployeeEntityID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ManagerEntityID:  "11111111-2222-3333-4444-555555555555",
		PromptVersion:    "analyze-v1",
		PolicyVersion:    "policy-v1",
		Inputs: []analysisdomain.InputReference{{
			Kind: analysisdomain.InputSignal, ID: "99999999-aaaa-bbbb-cccc-dddddddddddd", Digest: inputDigest,
		}},
	})
	if err != nil {
		t.Fatalf("analysis.ComputeInputDigest() error = %v", err)
	}
	if legacy == temporal {
		t.Fatal("legacy completion can occupy the temporal report digest namespace")
	}
}

func TestMeetingInputReferenceUsesCanonicalSourceDocumentIdentity(t *testing.T) {
	canonical, err := meetingInputReference("99999999-aaaa-bbbb-cccc-dddddddddddd")
	if err != nil {
		t.Fatalf("meetingInputReference() error = %v", err)
	}
	variant, err := meetingInputReference("99999999-AAAA-BBBB-CCCC-DDDDDDDDDDDD")
	if err != nil {
		t.Fatalf("variant meetingInputReference() error = %v", err)
	}
	other, err := meetingInputReference("88888888-aaaa-bbbb-cccc-dddddddddddd")
	if err != nil {
		t.Fatalf("other meetingInputReference() error = %v", err)
	}
	if canonical.Kind != analysisdomain.InputSourceDocument || canonical.ID != "99999999-aaaa-bbbb-cccc-dddddddddddd" {
		t.Fatalf("meeting input = %#v, want canonical source-document reference", canonical)
	}
	if canonical != variant {
		t.Fatal("equivalent source-document UUID spellings produced different meeting inputs")
	}
	if canonical.Digest == other.Digest {
		t.Fatal("different source documents produced the same meeting digest")
	}
}

func TestPairIdentitySnapshotRequiresEffectiveDecisionForEachConfiguredEntity(t *testing.T) {
	employeeID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	managerID := "11111111-2222-3333-4444-555555555555"
	employeeDigest := sha256.Sum256([]byte("employee-decision"))
	managerDigest := sha256.Sum256([]byte("manager-decision"))
	employeeDecision := effectivePairDecision{
		EntityID: employeeID,
		Input: analysisdomain.InputReference{
			Kind: analysisdomain.InputResolutionDecision, ID: "00000000-0000-0000-0000-000000000001", Digest: employeeDigest,
		},
	}
	managerDecision := effectivePairDecision{
		EntityID: managerID,
		Input: analysisdomain.InputReference{
			Kind: analysisdomain.InputResolutionDecision, ID: "00000000-0000-0000-0000-000000000002", Digest: managerDigest,
		},
	}

	for _, test := range []struct {
		name      string
		decisions []effectivePairDecision
		accepted  bool
		inputs    int
	}{
		{name: "pending pair", decisions: nil, accepted: false},
		{name: "only employee accepted", decisions: []effectivePairDecision{employeeDecision}, accepted: false},
		{name: "both identities accepted", decisions: []effectivePairDecision{employeeDecision, managerDecision}, accepted: true, inputs: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := pairIdentitySnapshot(employeeID, managerID, test.decisions)
			if err != nil {
				t.Fatalf("pairIdentitySnapshot() error = %v", err)
			}
			if snapshot.Accepted != test.accepted || len(snapshot.Inputs) != test.inputs {
				t.Fatalf("snapshot = %#v, want accepted=%t inputs=%d", snapshot, test.accepted, test.inputs)
			}
		})
	}
}
