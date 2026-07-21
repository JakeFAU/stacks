package storage

import (
	"crypto/sha256"
	"testing"
)

func TestComputeAnalysisDigestIncludesPairVersionsAndOrderedInputs(t *testing.T) {
	firstDigest := sha256.Sum256([]byte("first-input"))
	secondDigest := sha256.Sum256([]byte("second-input"))
	base := AnalysisInput{
		EmployeeEntityID:      "00000000-0000-0000-0000-000000000001",
		ManagerEntityID:       "00000000-0000-0000-0000-000000000002",
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
