package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"

	"stacks/internal/ingest"
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

func TestCompleteObservationPreflightRejectsUUIDBoundFieldsBeforeDatabaseAccess(t *testing.T) {
	repository := &GraphRepository{}
	for _, testCase := range []struct {
		name   string
		mutate func(*testing.T, *observation.ObservationInput)
		origin func() []evidence.EvidenceID
		signal func() (*SignalInput, []SignalEvidenceInput)
	}{
		{name: "observation_id", mutate: func(_ *testing.T, input *observation.ObservationInput) { input.ID = "invalid-observation-id" }},
		{name: "run_id", mutate: func(_ *testing.T, input *observation.ObservationInput) { input.Derivation.RunID = "invalid-run-id" }},
		{name: "subject_entity", mutate: func(t *testing.T, input *observation.ObservationInput) {
			term, err := observation.NewEntityTerm("invalid-subject-entity", "")
			if err != nil {
				t.Fatalf("new subject entity term: %v", err)
			}
			input.Statement.Subject = term
		}},
		{name: "object_entity", mutate: func(t *testing.T, input *observation.ObservationInput) {
			term, err := observation.NewEntityTerm("invalid-object-entity", "")
			if err != nil {
				t.Fatalf("new object entity term: %v", err)
			}
			input.Statement.Object = term
		}},
		{name: "subject_grounding_mention", mutate: func(t *testing.T, input *observation.ObservationInput) {
			term, err := observation.NewEntityTerm(string(codecOrigin()[0]), "invalid-subject-grounding")
			if err != nil {
				t.Fatalf("new subject grounding term: %v", err)
			}
			input.Statement.Subject = term
		}},
		{name: "object_grounding_mention", mutate: func(t *testing.T, input *observation.ObservationInput) {
			term, err := observation.NewEntityTerm(string(codecOrigin()[0]), "invalid-object-grounding")
			if err != nil {
				t.Fatalf("new object grounding term: %v", err)
			}
			input.Statement.Object = term
		}},
		{name: "subject_mention", mutate: func(t *testing.T, input *observation.ObservationInput) {
			term, err := observation.NewMentionTerm("invalid-subject-mention")
			if err != nil {
				t.Fatalf("new subject mention term: %v", err)
			}
			input.Statement.Subject = term
		}},
		{name: "object_mention", mutate: func(t *testing.T, input *observation.ObservationInput) {
			term, err := observation.NewMentionTerm("invalid-object-mention")
			if err != nil {
				t.Fatalf("new object mention term: %v", err)
			}
			input.Statement.Object = term
		}},
		{name: "canonical_evidence", mutate: func(_ *testing.T, input *observation.ObservationInput) {
			input.Evidence = []observation.EvidenceLink{{EvidenceID: "invalid-canonical-evidence", Role: observation.EvidenceSupporting}}
		}},
		{name: "origin_evidence", origin: func() []evidence.EvidenceID { return []evidence.EvidenceID{"invalid-origin-evidence"} }},
		{name: "signal_evidence", signal: func() (*SignalInput, []SignalEvidenceInput) {
			signal := codecActiveSignal().Input
			return &signal, []SignalEvidenceInput{{EvidenceSpanID: "invalid-signal-evidence", Role: "supporting"}}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			value := codecActiveObservation(t, func(input *observation.ObservationInput) {
				if testCase.mutate != nil {
					testCase.mutate(t, input)
				}
			})
			origin := codecOrigin()
			if testCase.origin != nil {
				origin = testCase.origin()
			}
			var signal *SignalInput
			var signalEvidence []SignalEvidenceInput
			if testCase.signal != nil {
				signal, signalEvidence = testCase.signal()
			}
			err := completeWithoutDatabase(t, repository, value, origin, signal, signalEvidence)
			if !errors.Is(err, ErrObservationNotRepresentable) || strings.Contains(err.Error(), "invalid-") {
				t.Fatalf("UUID preflight error = %v", err)
			}
		})
	}
}

func TestCompleteObservationNeverRendersInvalidObservationIDBeforePreflight(t *testing.T) {
	const invalidObservationID = "private-invalid-observation-id"
	repository := &GraphRepository{}
	for _, testCase := range []struct {
		name   string
		value  func(*testing.T) observation.Observation
		signal func() (*SignalInput, []SignalEvidenceInput, string)
	}{
		{
			name: "legacy_unversioned",
			value: func(t *testing.T) observation.Observation {
				return codecObservationWith(t, func(input *observation.ObservationInput) {
					input.ID = invalidObservationID
					input.Derivation = observation.Derivation{Method: "codec_derivation", LegacyUnversioned: true}
				})
			},
		},
		{
			name: "missing_run_id",
			value: func(t *testing.T) observation.Observation {
				return codecActiveObservation(t, func(input *observation.ObservationInput) {
					input.ID = invalidObservationID
					input.Derivation.RunID = ""
				})
			},
		},
		{
			name: "unrepresentable_recorded_at",
			value: func(t *testing.T) observation.Observation {
				return codecActiveObservation(t, func(input *observation.ObservationInput) {
					input.ID = invalidObservationID
					input.RecordedAt = time.Date(2026, time.July, 25, 12, 0, 0, 1, time.UTC)
				})
			},
		},
		{
			name: "unrepresentable_valid_time",
			value: func(t *testing.T) observation.Observation {
				instant, err := observation.AtTime(time.Date(2026, time.July, 25, 12, 0, 0, 1, time.UTC))
				if err != nil {
					t.Fatalf("new unrepresentable instant: %v", err)
				}
				return codecActiveObservation(t, func(input *observation.ObservationInput) {
					input.ID = invalidObservationID
					input.ValidTime = instant
				})
			},
		},
		{
			name:  "invalid_signal_id",
			value: func(t *testing.T) observation.Observation { return codecActiveObservation(t, nil) },
			signal: func() (*SignalInput, []SignalEvidenceInput, string) {
				signal := codecActiveSignal().Input
				const invalidSignalID = "private-invalid-signal-id"
				signal.ID = invalidSignalID
				return &signal, codecActiveSignal().Evidence, invalidSignalID
			},
		},
		{
			name:  "invalid_signal_observation_id",
			value: func(t *testing.T) observation.Observation { return codecActiveObservation(t, nil) },
			signal: func() (*SignalInput, []SignalEvidenceInput, string) {
				signal := codecActiveSignal().Input
				const invalidSignalObservationID = "private-invalid-signal-observation-id"
				signal.ObservationID = invalidSignalObservationID
				return &signal, codecActiveSignal().Evidence, invalidSignalObservationID
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			value := testCase.value(t)
			var signal *SignalInput
			var signalEvidence []SignalEvidenceInput
			invalidValue := invalidObservationID
			if testCase.signal != nil {
				signal, signalEvidence, invalidValue = testCase.signal()
			}
			err := completeWithoutDatabase(t, repository, value, codecOrigin(), signal, signalEvidence)
			if !errors.Is(err, ErrObservationNotRepresentable) || strings.Contains(err.Error(), invalidValue) || strings.Contains(err.Error(), invalidObservationID) {
				t.Fatalf("invalid identifier boundary error = %v", err)
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

func TestStageCanonicalIngestionWritesRejectsDuplicateDigest(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 25, 12, 0, 0, 123456000, time.UTC)
	run := owningExtractionRun{
		ID:            "11111111-1111-1111-1111-111111111111",
		ModelID:       "synthetic-model",
		PromptVersion: "extract-v1",
		RecordedAt:    recordedAt,
	}
	predicate, err := observation.NewPredicate("interaction_signal")
	if err != nil {
		t.Fatalf("new ingestion predicate: %v", err)
	}
	confidence, err := observation.NewUnitIntervalConfidence(0.8)
	if err != nil {
		t.Fatalf("new ingestion confidence: %v", err)
	}
	newDraft := func(id observation.ObservationID) ingest.ObservationDraft {
		return ingest.ObservationDraft{
			ID: id, Predicate: predicate, ValidTime: observation.UnknownTime(),
			RecordedAt: recordedAt, EvidenceKeys: []string{"citation"},
			SourceConfidence: confidence,
		}
	}
	newSignal := func(id string, observationID observation.ObservationID) ingest.SignalRecord {
		return ingest.SignalRecord{
			ID: id, ObservationID: string(observationID),
			Category: "delegation_autonomy", Direction: "strengthening",
			ExtractionModelID: run.ModelID, PromptVersion: run.PromptVersion,
			Rationale: "Synthetic staged duplicate.", Confidence: 0.8,
			Evidence: []ingest.SignalEvidenceRecord{{EvidenceKey: "citation", Role: "supporting"}},
		}
	}
	firstID := observation.ObservationID("22222222-2222-2222-2222-222222222222")
	secondID := observation.ObservationID("33333333-3333-3333-3333-333333333333")
	_, err = stageCanonicalIngestionWrites(
		run,
		[]ingest.ObservationDraft{newDraft(firstID), newDraft(secondID)},
		[]ingest.SignalRecord{
			newSignal("44444444-4444-4444-4444-444444444444", firstID),
			newSignal("55555555-5555-5555-5555-555555555555", secondID),
		},
		map[string]string{"citation": "66666666-6666-6666-6666-666666666666"},
		nil,
	)
	if !errors.Is(err, ErrObservationConflict) {
		t.Fatalf("stageCanonicalIngestionWrites() error = %v, want duplicate durable digest conflict", err)
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
