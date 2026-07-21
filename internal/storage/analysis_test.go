package storage

import (
	"crypto/sha256"
	"testing"
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
