package storage

import "testing"

func TestComputeObservationDigestCanonicalizesSemanticIdentity(t *testing.T) {
	base := ObservationInput{
		ID:              "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		SubjectEntityID: "11111111-2222-3333-4444-555555555555",
		ObjectEntityID:  "66666666-7777-8888-9999-aaaaaaaaaaaa",
		Predicate:       "interacted_with",
		Derivation:      "synthetic",
		EpistemicStatus: "inferred",
	}
	baseline, err := ComputeObservationDigest(base, []string{
		"99999999-aaaa-bbbb-cccc-dddddddddddd",
		"00000000-1111-2222-3333-444444444444",
	})
	if err != nil {
		t.Fatalf("compute baseline observation digest: %v", err)
	}

	equivalent := base
	equivalent.ID = "BBBBBBBB-CCCC-DDDD-EEEE-FFFFFFFFFFFF"
	equivalent.SubjectEntityID = "11111111-2222-3333-4444-555555555555"
	equivalent.ObjectEntityID = "66666666-7777-8888-9999-AAAAAAAAAAAA"
	got, err := ComputeObservationDigest(equivalent, []string{
		"99999999-AAAA-BBBB-CCCC-DDDDDDDDDDDD",
		"00000000-1111-2222-3333-444444444444",
		"99999999-aaaa-bbbb-cccc-dddddddddddd",
	})
	if err != nil {
		t.Fatalf("compute equivalent observation digest: %v", err)
	}
	if got != baseline {
		t.Fatal("equivalent observation retry produced a different semantic digest")
	}

	changed := base
	changed.Predicate = "different_interaction"
	changedDigest, err := ComputeObservationDigest(changed, []string{
		"00000000-1111-2222-3333-444444444444",
		"99999999-aaaa-bbbb-cccc-dddddddddddd",
	})
	if err != nil {
		t.Fatalf("compute changed observation digest: %v", err)
	}
	if changedDigest == baseline {
		t.Fatal("changed observation payload retained its semantic digest")
	}
}

func TestComputeSignalDigestCanonicalizesSemanticIdentity(t *testing.T) {
	base := SignalInput{
		ID:                "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		ObservationID:     "11111111-2222-3333-4444-555555555555",
		Category:          "delegation_autonomy",
		Direction:         "strengthening",
		ExtractionModelID: "synthetic-model",
		PromptVersion:     "synthetic-v1",
		Confidence:        0.8,
	}
	baseline, err := ComputeSignalDigest(base, []SignalEvidenceInput{
		{EvidenceSpanID: "99999999-aaaa-bbbb-cccc-dddddddddddd", Role: "supporting"},
		{EvidenceSpanID: "00000000-1111-2222-3333-444444444444", Role: "contradicting"},
	})
	if err != nil {
		t.Fatalf("compute baseline signal digest: %v", err)
	}

	equivalent := base
	equivalent.ID = "BBBBBBBB-CCCC-DDDD-EEEE-FFFFFFFFFFFF"
	equivalent.ObservationID = "11111111-2222-3333-4444-555555555555"
	got, err := ComputeSignalDigest(equivalent, []SignalEvidenceInput{
		{EvidenceSpanID: "00000000-1111-2222-3333-444444444444", Role: "contradicting"},
		{EvidenceSpanID: "99999999-AAAA-BBBB-CCCC-DDDDDDDDDDDD", Role: "supporting"},
		{EvidenceSpanID: "99999999-aaaa-bbbb-cccc-dddddddddddd", Role: "supporting"},
	})
	if err != nil {
		t.Fatalf("compute equivalent signal digest: %v", err)
	}
	if got != baseline {
		t.Fatal("equivalent signal retry produced a different semantic digest")
	}

	changed := base
	changed.Direction = "weakening"
	changedDigest, err := ComputeSignalDigest(changed, []SignalEvidenceInput{
		{EvidenceSpanID: "99999999-aaaa-bbbb-cccc-dddddddddddd", Role: "supporting"},
		{EvidenceSpanID: "00000000-1111-2222-3333-444444444444", Role: "contradicting"},
	})
	if err != nil {
		t.Fatalf("compute changed signal digest: %v", err)
	}
	if changedDigest == baseline {
		t.Fatal("changed signal payload retained its semantic digest")
	}
}
