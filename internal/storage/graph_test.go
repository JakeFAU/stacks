package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
)

func TestCompleteObservationPreflightRejectsBeforeDatabaseAccess(t *testing.T) {
	repository := &GraphRepository{}
	for _, testCase := range []struct {
		name   string
		invoke func(*testing.T) error
		want   error
	}{
		{
			name: "unrepresentable_recorded_at",
			invoke: func(t *testing.T) error {
				value := codecActiveObservation(t, func(input *observation.ObservationInput) {
					input.RecordedAt = time.Date(2026, time.July, 25, 12, 0, 0, 1, time.UTC)
				})
				return completeWithoutDatabase(t, repository, value, codecOrigin(), nil, nil)
			},
			want: ErrObservationNotRepresentable,
		},
		{
			name: "unsupported_text_term",
			invoke: func(t *testing.T) error {
				text, err := observation.NewTextTerm("synthetic unsupported text")
				if err != nil {
					t.Fatalf("new text term: %v", err)
				}
				value := codecActiveObservation(t, func(input *observation.ObservationInput) { input.Statement.Subject = text })
				return completeWithoutDatabase(t, repository, value, codecOrigin(), nil, nil)
			},
			want: ErrObservationNotRepresentable,
		},
		{
			name: "unsupported_until_time",
			invoke: func(t *testing.T) error {
				until, err := observation.Until(codecRecordedAt)
				if err != nil {
					t.Fatalf("new until time: %v", err)
				}
				value := codecActiveObservation(t, func(input *observation.ObservationInput) { input.ValidTime = until })
				return completeWithoutDatabase(t, repository, value, codecOrigin(), nil, nil)
			},
			want: ErrObservationNotRepresentable,
		},
		{
			name: "unsupported_confidence_scale",
			invoke: func(t *testing.T) error {
				confidence, err := observation.NewUnitIntervalConfidence(0.75)
				if err != nil {
					t.Fatalf("new unit confidence: %v", err)
				}
				value := codecActiveObservation(t, func(input *observation.ObservationInput) { input.Confidence = &confidence })
				return completeWithoutDatabase(t, repository, value, codecOrigin(), nil, nil)
			},
			want: ErrObservationNotRepresentable,
		},
		{
			name: "signal_observation_ownership",
			invoke: func(t *testing.T) error {
				value := codecActiveObservation(t, nil)
				signal := codecActiveSignal().Input
				signal.ObservationID = "cccccccc-dddd-eeee-ffff-000000000000"
				return completeWithoutDatabase(t, repository, value, codecOrigin(), &signal, codecActiveSignal().Evidence)
			},
			want: ErrObservationCompatibility,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.invoke(t)
			if !errors.Is(err, testCase.want) || strings.Contains(err.Error(), "private") {
				t.Fatalf("preflight error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func completeWithoutDatabase(
	t *testing.T,
	repository *GraphRepository,
	value observation.Observation,
	origin []evidence.EvidenceID,
	signal *SignalInput,
	signalEvidence []SignalEvidenceInput,
) (err error) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("complete observation attempted database access: %v", recovered)
		}
	}()
	_, _, err = repository.CompleteObservation(context.Background(), value, origin, signal, signalEvidence)
	return err
}

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
